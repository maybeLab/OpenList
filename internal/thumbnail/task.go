package thumbnail

import (
	"context"
	"fmt"
	"os"
	stdpath "path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/conf"
	"github.com/OpenListTeam/OpenList/v4/internal/errs"
	"github.com/OpenListTeam/OpenList/v4/internal/fs"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/internal/stream"
	basetask "github.com/OpenListTeam/OpenList/v4/internal/task"
	"github.com/OpenListTeam/OpenList/v4/pkg/singleflight"
	"github.com/OpenListTeam/OpenList/v4/pkg/utils"
	"github.com/OpenListTeam/tache"
)

var generationGroup singleflight.Group[struct{}]
var signPattern = regexp.MustCompile(`(?i)(sign=)[^&\s\]]+`)

type Task struct {
	basetask.TaskExtension
	Options   Options `json:"options"`
	StorageID uint    `json:"storage_id"`

	mu                sync.RWMutex
	stats             Stats
	progress          float64
	activePaths       map[int]string
	failures          []Failure
	failuresTruncated bool
	workStarted       time.Time
}

func NewTask(options Options, storageID uint, creator *model.User) *Task {
	return &Task{
		TaskExtension: basetask.TaskExtension{Creator: creator},
		Options:       options,
		StorageID:     storageID,
		stats:         Stats{Phase: "pending", Result: ResultRunning},
		activePaths:   map[int]string{},
	}
}

func (t *Task) GetName() string {
	return fmt.Sprintf("thumbnail [%s]", t.Options.RootPath)
}

func (t *Task) GetStatus() string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.stats.Phase
}

func (t *Task) SetProgress(progress float64) {
	t.mu.Lock()
	t.progress = max(0, min(100, progress))
	t.mu.Unlock()
}

func (t *Task) GetProgress() float64 {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.progress
}

// OnBeforeRetry gives a manually retried task a fresh context. In particular,
// a canceled tache task retains its canceled context unless the task replaces
// it before the next run.
func (t *Task) OnBeforeRetry() {
	ctx, cancel := context.WithCancel(context.Background())
	t.SetCtx(ctx)
	t.SetCancelFunc(cancel)
}

func (t *Task) reset() {
	t.mu.Lock()
	t.stats = Stats{Phase: "scanning", Result: ResultRunning}
	t.progress = 0
	t.activePaths = map[int]string{}
	t.failures = nil
	t.failuresTruncated = false
	t.workStarted = time.Time{}
	t.mu.Unlock()
}

func (t *Task) Run() (runErr error) {
	if err := checkRuntimeOverlap(t); err != nil {
		return err
	}
	t.reset()
	t.ClearEndTime()
	t.SetStartTime(time.Now())
	defer func() {
		t.SetEndTime(time.Now())
		t.mu.Lock()
		switch {
		case t.Ctx().Err() != nil:
			t.stats.Result = ResultCanceled
		case runErr != nil:
			t.stats.Result = ResultFailed
		case t.stats.Failed > 0:
			t.stats.Result = ResultCompletedWithErrors
		default:
			t.stats.Result = ResultCompleted
		}
		t.stats.RunningWorkers = 0
		t.activePaths = map[int]string{}
		t.mu.Unlock()
	}()

	result, err := Scan(t.Ctx(), t.StorageID, t.Options, t.observeScan)
	if err != nil {
		return err
	}
	t.mu.Lock()
	t.stats.TotalCandidates = int64(len(result.Candidates))
	t.stats.Remaining = t.stats.TotalCandidates
	t.SetTotalBytes(t.stats.TotalCandidates)
	if t.Options.Mode == ModeScanOnly {
		t.stats.Phase = "completed"
		t.progress = 100
		t.mu.Unlock()
		return nil
	}
	t.stats.Phase = "generating"
	t.workStarted = time.Now()
	if len(result.Candidates) == 0 {
		t.progress = 100
	}
	t.mu.Unlock()

	taskDir := filepath.Join(tempRoot(), t.GetID())
	if err := os.MkdirAll(taskDir, 0o700); err != nil {
		return fmt.Errorf("create task temp directory: %w", err)
	}
	defer os.RemoveAll(taskDir)

	jobs := make(chan Candidate, perTaskWorkers*2)
	var workers sync.WaitGroup
	for workerID := 0; workerID < perTaskWorkers; workerID++ {
		workers.Add(1)
		go func(id int) {
			defer workers.Done()
			for candidate := range jobs {
				if t.Ctx().Err() != nil {
					return
				}
				t.processCandidate(id, candidate, taskDir)
			}
		}(workerID)
	}
	for _, candidate := range result.Candidates {
		select {
		case <-t.CtxDone():
			close(jobs)
			workers.Wait()
			return t.Ctx().Err()
		case jobs <- candidate:
		}
	}
	close(jobs)
	workers.Wait()
	if err := t.Ctx().Err(); err != nil {
		return err
	}

	if t.Options.CleanupOrphan {
		t.mu.Lock()
		t.stats.Phase = "cleaning"
		t.mu.Unlock()
		t.cleanupOrphans(result.Directories)
	}
	t.mu.Lock()
	t.stats.Phase = "completed"
	t.progress = 100
	t.mu.Unlock()
	return nil
}

func (t *Task) observeScan(event ScanEvent) {
	t.mu.Lock()
	t.stats.ScannedDirs += event.Dirs
	t.stats.ScannedFiles += event.Files
	t.stats.TotalMedia += event.Media
	t.stats.ExistingValid += event.ExistingValid
	t.stats.SkippedMounts += event.SkippedMounts
	t.stats.CurrentPath = event.CurrentPath
	t.mu.Unlock()
}

func (t *Task) processCandidate(workerID int, candidate Candidate, taskDir string) {
	t.setActive(workerID, candidate.SourcePath)
	defer t.clearActive(workerID)
	if err := ensureStorage(candidate.SourcePath, t.StorageID); err != nil {
		t.finishCandidate("failed", candidate, "storage", err)
		return
	}
	if t.Options.Mode == ModeMissing {
		thumb, err := fs.Get(t.Ctx(), candidate.ThumbnailPath, &fs.GetArgs{NoLog: true})
		if err == nil && thumb.GetSize() > 0 {
			t.finishCandidate("skipped", candidate, "", nil)
			return
		}
		if err != nil && !errs.IsNotFoundError(err) {
			t.finishCandidate("failed", candidate, "stat", err)
			return
		}
	}
	key := fmt.Sprintf("%d:%s:%d:%d", t.StorageID, candidate.SourcePath, candidate.ModTime.UnixNano(), candidate.Size)
	_, err, _ := generationGroup.Do(key, func() (struct{}, error) {
		output, err := os.CreateTemp(taskDir, "*.webp")
		if err != nil {
			return struct{}{}, fmt.Errorf("create output: %w", err)
		}
		outputPath := output.Name()
		_ = output.Close()
		_ = os.Remove(outputPath)
		defer os.Remove(outputPath)
		if err := Generate(t.Ctx(), candidate, t.Options, outputPath); err != nil {
			return struct{}{}, err
		}
		if err := t.upload(candidate, outputPath); err != nil {
			return struct{}{}, fmt.Errorf("upload: %w", err)
		}
		return struct{}{}, nil
	})
	if err != nil {
		stage := "ffmpeg"
		if strings.HasPrefix(err.Error(), "upload:") {
			stage = "upload"
		}
		t.finishCandidate("failed", candidate, stage, err)
		return
	}
	t.finishCandidate("generated", candidate, "", nil)
}

func (t *Task) upload(candidate Candidate, outputPath string) error {
	if err := ensureStorage(candidate.ThumbnailPath, t.StorageID); err != nil {
		return err
	}
	f, err := os.Open(outputPath)
	if err != nil {
		return err
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return err
	}
	file := &stream.FileStream{
		Ctx: t.Ctx(),
		Obj: &model.Object{
			Name:     stdpath.Base(candidate.ThumbnailPath),
			Size:     info.Size(),
			Modified: time.Now(),
		},
		Reader:   f,
		Mimetype: "image/webp",
		Closers:  utils.Closers{f},
	}
	return fs.PutDirectly(t.Ctx(), stdpath.Dir(candidate.ThumbnailPath), file, true)
}

func (t *Task) finishCandidate(result string, candidate Candidate, stage string, err error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.stats.Processed++
	switch result {
	case "generated":
		t.stats.Generated++
	case "skipped":
		t.stats.Skipped++
	case "failed":
		t.stats.Failed++
		t.addFailureLocked(candidate.SourcePath, candidate.MediaType, stage, err)
	}
	t.updateProgressLocked()
}

func (t *Task) addFailureLocked(path, mediaType, stage string, err error) {
	limit := failureLimit
	message := "unknown error"
	if err != nil {
		message = signPattern.ReplaceAllString(err.Error(), "${1}<redacted>")
		if len(message) > 1024 {
			message = message[:1024]
		}
	}
	failure := Failure{Path: path, MediaType: mediaType, Stage: stage, Error: message, Time: time.Now()}
	if len(t.failures) >= limit {
		copy(t.failures, t.failures[1:])
		t.failures[len(t.failures)-1] = failure
		t.failuresTruncated = true
		return
	}
	t.failures = append(t.failures, failure)
}

func (t *Task) updateProgressLocked() {
	t.stats.Remaining = max(0, t.stats.TotalCandidates-t.stats.Processed)
	if t.stats.TotalCandidates > 0 {
		t.progress = float64(t.stats.Processed) / float64(t.stats.TotalCandidates) * 100
	}
	elapsed := time.Since(t.workStarted)
	if t.stats.Processed >= 5 || elapsed >= 10*time.Second {
		minutes := elapsed.Minutes()
		if minutes > 0 {
			t.stats.ItemsPerMinute = float64(t.stats.Processed) / minutes
			if t.stats.ItemsPerMinute > 0 {
				t.stats.EstimatedSeconds = int64(float64(t.stats.Remaining) / t.stats.ItemsPerMinute * 60)
			}
		}
	}
}

func (t *Task) setActive(workerID int, path string) {
	t.mu.Lock()
	t.activePaths[workerID] = path
	t.stats.RunningWorkers = len(t.activePaths)
	t.stats.CurrentPath = path
	t.mu.Unlock()
}

func (t *Task) clearActive(workerID int) {
	t.mu.Lock()
	delete(t.activePaths, workerID)
	t.stats.RunningWorkers = len(t.activePaths)
	t.mu.Unlock()
}

func (t *Task) cleanupOrphans(directories []string) {
	for _, dir := range directories {
		if t.Ctx().Err() != nil {
			return
		}
		if err := ensureStorage(dir, t.StorageID); err != nil {
			t.addCleanupFailure(dir, err)
			continue
		}
		sources, err := fs.List(t.Ctx(), dir, &fs.ListArgs{NoLog: true, Refresh: true})
		if err != nil {
			t.addCleanupFailure(dir, err)
			continue
		}
		media := make(map[string]struct{})
		for _, obj := range sources {
			if obj.IsDir() {
				continue
			}
			typeID := utils.GetObjType(obj.GetName(), false)
			if typeID == conf.IMAGE || typeID == conf.VIDEO {
				media[obj.GetName()] = struct{}{}
			}
		}
		thumbDir := stdpath.Join(dir, ".thumbnails")
		thumbs, err := fs.List(t.Ctx(), thumbDir, &fs.ListArgs{NoLog: true, Refresh: true})
		if err != nil {
			if !errs.IsNotFoundError(err) {
				t.addCleanupFailure(thumbDir, err)
			}
			continue
		}
		for _, thumb := range thumbs {
			name := thumb.GetName()
			if thumb.IsDir() || strings.HasPrefix(name, "._") || !strings.HasSuffix(strings.ToLower(name), ".webp") {
				continue
			}
			sourceName := name[:len(name)-len(".webp")]
			if _, ok := media[sourceName]; ok {
				continue
			}
			thumbPath := stdpath.Join(thumbDir, name)
			if !utils.IsSubPath(t.Options.RootPath, thumbPath) {
				t.addCleanupFailure(thumbPath, fmt.Errorf("path escaped task root"))
				continue
			}
			ctx := context.WithValue(t.Ctx(), conf.SkipHookKey, struct{}{})
			if err := fs.Remove(ctx, thumbPath); err != nil {
				t.addCleanupFailure(thumbPath, err)
				continue
			}
			t.mu.Lock()
			t.stats.OrphansRemoved++
			t.mu.Unlock()
		}
	}
}

func (t *Task) addCleanupFailure(path string, err error) {
	t.mu.Lock()
	t.stats.Failed++
	t.addFailureLocked(path, "", "cleanup", err)
	t.mu.Unlock()
}

func (t *Task) Detail() Detail {
	t.mu.RLock()
	defer t.mu.RUnlock()
	active := make([]string, 0, len(t.activePaths))
	for _, path := range t.activePaths {
		active = append(active, path)
	}
	sort.Strings(active)
	failures := append([]Failure(nil), t.failures...)
	return Detail{
		ID:                t.GetID(),
		Options:           t.Options,
		Progress:          t.progress,
		Stats:             t.stats,
		ActivePaths:       active,
		RecentFailures:    failures,
		FailuresTruncated: t.failuresTruncated,
	}
}

var _ tache.TaskWithInfo = (*Task)(nil)
