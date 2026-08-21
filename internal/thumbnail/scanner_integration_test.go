package thumbnail

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	_ "github.com/OpenListTeam/OpenList/v4/drivers/crypt"
	_ "github.com/OpenListTeam/OpenList/v4/drivers/local"
	"github.com/OpenListTeam/OpenList/v4/internal/conf"
	"github.com/OpenListTeam/OpenList/v4/internal/errs"
	"github.com/OpenListTeam/OpenList/v4/internal/fs"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/internal/op"
	"github.com/OpenListTeam/OpenList/v4/internal/setting"
	"github.com/OpenListTeam/OpenList/v4/internal/stream"
	"github.com/OpenListTeam/OpenList/v4/pkg/utils"
	"github.com/OpenListTeam/tache"
)

var storageSequence atomic.Uint64

type cryptFixture struct {
	root      string
	rawRoot   string
	storageID uint
}

func jsonString(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func newCryptFixture(t *testing.T) cryptFixture {
	return newCryptFixtureWithThumbnail(t, true)
}

func newCryptFixtureWithThumbnail(t *testing.T, thumbnail bool) cryptFixture {
	t.Helper()
	sequence := storageSequence.Add(1)
	base := fmt.Sprintf("/thumbnail-test-%d", sequence)
	rawMount, cryptMount := base+"-raw", base+"-crypt"
	localRoot := t.TempDir()
	_, err := op.CreateStorage(context.Background(), model.Storage{
		MountPath: rawMount,
		Driver:    "Local",
		Addition: jsonString(t, map[string]any{
			"root_folder_path": localRoot,
			"show_hidden":      true,
			"mkdir_perm":       "700",
		}),
	})
	if err != nil {
		t.Fatalf("create Local storage: %v", err)
	}
	storageID, err := op.CreateStorage(context.Background(), model.Storage{
		MountPath: cryptMount,
		Driver:    "Crypt",
		Addition: jsonString(t, map[string]any{
			"filename_encryption":       "standard",
			"directory_name_encryption": "true",
			"remote_path":               rawMount,
			"password":                  "thumbnail-test-password",
			"salt":                      "thumbnail-test-salt",
			"encrypted_suffix":          ".bin",
			"filename_encoding":         "base64",
			"thumbnail":                 thumbnail,
			"show_hidden":               true,
		}),
	})
	if err != nil {
		t.Fatalf("create Crypt storage: %v", err)
	}
	return cryptFixture{root: cryptMount, rawRoot: rawMount, storageID: storageID}
}

func putFixtureFile(t *testing.T, fullPath string, content []byte) {
	t.Helper()
	file := &stream.FileStream{
		Ctx: context.Background(),
		Obj: &model.Object{
			Name:     path.Base(fullPath),
			Size:     int64(len(content)),
			Modified: time.Now(),
		},
		Reader: bytes.NewReader(content),
	}
	if err := fs.PutDirectly(context.Background(), path.Dir(fullPath), file, true); err != nil {
		t.Fatalf("put %s: %v", fullPath, err)
	}
}

func scanOptions(root, mode string) Options {
	return Options{
		RootPath:      root,
		TargetType:    TargetDirectory,
		Recursive:     true,
		IncludeImages: true,
		IncludeVideos: true,
		Mode:          mode,
		Width:         480,
		Quality:       75,
		VideoSecond:   3,
	}
}

func TestScanModesAndCryptValidation(t *testing.T) {
	fixture := newCryptFixture(t)
	putFixtureFile(t, path.Join(fixture.root, "photo.jpg"), []byte("encrypted source payload"))

	if got, err := ValidateTarget(context.Background(), fixture.root); err != nil || got.StorageID != fixture.storageID || got.Type != TargetDirectory {
		t.Fatalf("ValidateTarget() = %+v, %v", got, err)
	}

	missing, err := Scan(context.Background(), fixture.storageID, scanOptions(fixture.root, ModeMissing), func(ScanEvent) {})
	if err != nil {
		t.Fatal(err)
	}
	if len(missing.Candidates) != 1 || missing.Candidates[0].SourcePath != path.Join(fixture.root, "photo.jpg") {
		t.Fatalf("unexpected missing scan: %+v", missing)
	}

	putFixtureFile(t, path.Join(fixture.root, ".thumbnails", "photo.jpg.webp"), []byte("non-empty pre-generated thumbnail"))
	missing, err = Scan(context.Background(), fixture.storageID, scanOptions(fixture.root, ModeMissing), func(ScanEvent) {})
	if err != nil {
		t.Fatal(err)
	}
	if len(missing.Candidates) != 0 {
		t.Fatalf("missing scan returned existing thumbnail: %+v", missing.Candidates)
	}

	force, err := Scan(context.Background(), fixture.storageID, scanOptions(fixture.root, ModeForce), func(ScanEvent) {})
	if err != nil {
		t.Fatal(err)
	}
	if len(force.Candidates) != 1 {
		t.Fatalf("force candidates = %d, want 1", len(force.Candidates))
	}

	nonCryptRoot := fmt.Sprintf("/thumbnail-test-noncrypt-%d", storageSequence.Add(1))
	_, err = op.CreateStorage(context.Background(), model.Storage{
		MountPath: nonCryptRoot,
		Driver:    "Local",
		Addition:  jsonString(t, map[string]any{"root_folder_path": t.TempDir(), "show_hidden": true}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateTarget(context.Background(), nonCryptRoot); err == nil {
		t.Fatal("ValidateTarget() accepted a non-Crypt storage")
	}
}

func TestValidateTargetRejectsCryptWithThumbnailDisabled(t *testing.T) {
	fixture := newCryptFixtureWithThumbnail(t, false)

	_, err := ValidateTarget(context.Background(), fixture.root)
	var kindErr *KindError
	if !errors.As(err, &kindErr) || kindErr.Kind != ErrorCapability {
		t.Fatalf("ValidateTarget() error = %v, want capability error", err)
	}
	if !bytes.Contains([]byte(err.Error()), []byte("thumbnail support is disabled")) {
		t.Fatalf("ValidateTarget() error = %v, want disabled thumbnail explanation", err)
	}
	_, err = Create(context.Background(), CreateRequest{RootPath: fixture.root}, nil, true)
	if !errors.As(err, &kindErr) || kindErr.Kind != ErrorCapability {
		t.Fatalf("Create() error = %v, want capability error", err)
	}
}

func TestSingleFileTargetsAndScan(t *testing.T) {
	fixture := newCryptFixture(t)
	imagePath := path.Join(fixture.root, "space # percent %.jpg")
	videoPath := path.Join(fixture.root, "clip.mp4")
	textPath := path.Join(fixture.root, "notes.txt")
	putFixtureFile(t, imagePath, []byte("image"))
	putFixtureFile(t, videoPath, []byte("video"))
	putFixtureFile(t, textPath, []byte("text"))

	imageTarget, err := ValidateTarget(context.Background(), imagePath)
	if err != nil || imageTarget.Type != TargetFile || imageTarget.MediaType != "image" {
		t.Fatalf("image target = %+v, %v", imageTarget, err)
	}
	videoTarget, err := ValidateTarget(context.Background(), videoPath)
	if err != nil || videoTarget.Type != TargetFile || videoTarget.MediaType != "video" {
		t.Fatalf("video target = %+v, %v", videoTarget, err)
	}
	if _, err := ValidateTarget(context.Background(), textPath); err == nil {
		t.Fatal("non-media file was accepted")
	}

	options := scanOptions(imagePath, ModeForce)
	options.TargetType = TargetFile
	var observed ScanEvent
	result, err := Scan(context.Background(), fixture.storageID, options, func(event ScanEvent) {
		observed.Files += event.Files
		observed.Media += event.Media
	})
	if err != nil {
		t.Fatal(err)
	}
	if observed.Files != 1 || observed.Media != 1 || len(result.Candidates) != 1 {
		t.Fatalf("single-file scan event=%+v result=%+v", observed, result)
	}
	wantThumbnail := path.Join(fixture.root, ".thumbnails", "space # percent %.jpg.webp")
	if result.Candidates[0].SourcePath != imagePath || result.Candidates[0].ThumbnailPath != wantThumbnail {
		t.Fatalf("unexpected single-file candidate: %+v", result.Candidates[0])
	}

	putFixtureFile(t, wantThumbnail, []byte("existing"))
	options.Mode = ModeMissing
	result, err = Scan(context.Background(), fixture.storageID, options, func(ScanEvent) {})
	if err != nil || len(result.Candidates) != 0 {
		t.Fatalf("missing single-file scan = %+v, %v", result, err)
	}
}

func TestCreateNormalizesSingleFileOptionsAndOverlap(t *testing.T) {
	fixture := newCryptFixture(t)
	firstPath := path.Join(fixture.root, "first.jpg")
	secondPath := path.Join(fixture.root, "second.jpg")
	putFixtureFile(t, firstPath, []byte("first"))
	putFixtureFile(t, secondPath, []byte("second"))

	normalized := scanOptions(firstPath, ModeMissing)
	normalizeTargetOptions(&normalized, Target{StorageID: fixture.storageID, Type: TargetFile, MediaType: "image"}, false)
	if normalized.Mode != ModeForce || normalized.TargetType != TargetFile || normalized.Recursive || normalized.CleanupOrphan || !normalized.IncludeImages || normalized.IncludeVideos {
		t.Fatalf("create options were not normalized to forced image generation: %+v", normalized)
	}

	oldManager := TaskManager
	TaskManager = tache.NewManager[*Task](tache.WithWorks(0), tache.WithMaxRetry(0))
	t.Cleanup(func() {
		TaskManager.RemoveAll()
		TaskManager = oldManager
	})

	fileTask, err := Create(context.Background(), CreateRequest{
		RootPath:      firstPath,
		Recursive:     boolPtr(true),
		IncludeImages: boolPtr(false),
		IncludeVideos: boolPtr(true),
		CleanupOrphan: true,
	}, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if fileTask.Options.TargetType != TargetFile || fileTask.Options.Recursive || fileTask.Options.CleanupOrphan || !fileTask.Options.IncludeImages || fileTask.Options.IncludeVideos {
		t.Fatalf("file options were not normalized: %+v", fileTask.Options)
	}
	if _, err := Create(context.Background(), CreateRequest{RootPath: fixture.root}, nil, true); err == nil {
		t.Fatal("directory task did not conflict with contained file task")
	}
	if _, err := Create(context.Background(), CreateRequest{RootPath: secondPath}, nil, true); err != nil {
		t.Fatalf("different sibling file task conflicted: %v", err)
	}
}

func TestSingleFileThumbnailUploadUsesCryptPath(t *testing.T) {
	fixture := newCryptFixture(t)
	thumbnailPath := path.Join(fixture.root, ".thumbnails", "特殊 # photo.jpg.webp")
	outputPath := filepath.Join(t.TempDir(), "thumbnail.webp")
	payload := []byte("RIFF....WEBP encrypted thumbnail payload")
	if err := os.WriteFile(outputPath, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	task := NewTask(Options{RootPath: path.Join(fixture.root, "特殊 # photo.jpg"), TargetType: TargetFile}, fixture.storageID, nil)
	task.SetCtx(context.Background())
	if err := task.upload(Candidate{ThumbnailPath: thumbnailPath}, outputPath); err != nil {
		t.Fatalf("upload thumbnail: %v", err)
	}
	obj, err := fs.Get(context.Background(), thumbnailPath, &fs.GetArgs{NoLog: true})
	if err != nil || obj.GetSize() != int64(len(payload)) {
		t.Fatalf("Crypt-visible thumbnail = %+v, %v", obj, err)
	}
	rawObjects, err := fs.List(context.Background(), fixture.rawRoot, &fs.ListArgs{NoLog: true, Refresh: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, obj := range rawObjects {
		if obj.GetName() == ".thumbnails" {
			t.Fatal("thumbnail directory was written to the underlying storage without Crypt name encryption")
		}
	}
}

func TestScanSkipsCrossMountAndKeepsCandidates(t *testing.T) {
	fixture := newCryptFixture(t)
	rootImage := path.Join(fixture.root, "root.jpg")
	putFixtureFile(t, rootImage, []byte("root image"))
	crossMount := path.Join(fixture.root, "cross")
	_, err := op.CreateStorage(context.Background(), model.Storage{
		MountPath: crossMount,
		Driver:    "Local",
		Addition:  jsonString(t, map[string]any{"root_folder_path": t.TempDir(), "show_hidden": true}),
	})
	if err != nil {
		t.Fatal(err)
	}
	putFixtureFile(t, path.Join(crossMount, "mounted.jpg"), []byte("mounted image"))
	var skippedMounts int64
	var skippedPath string
	result, err := Scan(context.Background(), fixture.storageID, scanOptions(fixture.root, ModeMissing), func(event ScanEvent) {
		skippedMounts += event.SkippedMounts
		if event.SkippedMounts > 0 {
			skippedPath = event.CurrentPath
		}
	})
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	if skippedMounts != 1 || skippedPath != crossMount {
		t.Fatalf("cross-mount observation = %d at %q, want one skipped mount at %s", skippedMounts, skippedPath, crossMount)
	}
	if len(result.Candidates) != 1 || result.Candidates[0].SourcePath != rootImage {
		t.Fatalf("Scan() candidates = %+v, want only root Crypt candidate", result.Candidates)
	}
}

func TestCleanupOrphansKeepsExistingMediaThumbnail(t *testing.T) {
	fixture := newCryptFixture(t)
	putFixtureFile(t, path.Join(fixture.root, "keep.jpg"), []byte("source"))
	putFixtureFile(t, path.Join(fixture.root, ".thumbnails", "keep.jpg.webp"), []byte("keep"))
	putFixtureFile(t, path.Join(fixture.root, ".thumbnails", "gone.jpg.webp"), []byte("remove"))

	task := NewTask(scanOptions(fixture.root, ModeMissing), fixture.storageID, nil)
	task.SetCtx(context.Background())
	task.cleanupOrphans([]string{fixture.root})
	if _, err := fs.Get(context.Background(), path.Join(fixture.root, ".thumbnails", "keep.jpg.webp"), &fs.GetArgs{NoLog: true}); err != nil {
		t.Fatalf("existing thumbnail was removed: %v", err)
	}
	if _, err := fs.Get(context.Background(), path.Join(fixture.root, ".thumbnails", "gone.jpg.webp"), &fs.GetArgs{NoLog: true}); !errs.IsNotFoundError(err) {
		t.Fatalf("orphan still exists or returned wrong error: %v", err)
	}
	if detail := task.Detail(); detail.Stats.OrphansRemoved != 1 || detail.Stats.Failed != 0 {
		t.Fatalf("unexpected cleanup stats: %+v", detail.Stats)
	}
}

func TestTaskFailureHistoryIsBounded(t *testing.T) {
	task := NewTask(Options{RootPath: "/crypt"}, 1, nil)
	task.stats.TotalCandidates = failureLimit + 5
	task.workStarted = time.Now().Add(-time.Minute)
	for index := 0; index < failureLimit+5; index++ {
		candidate := Candidate{SourcePath: fmt.Sprintf("/crypt/%02d.mp4", index), MediaType: "video"}
		task.finishCandidate("failed", candidate, "ffmpeg", fmt.Errorf("failure %d", index))
	}
	detail := task.Detail()
	if len(detail.RecentFailures) != failureLimit || !detail.FailuresTruncated {
		t.Fatalf("failure history length=%d truncated=%v", len(detail.RecentFailures), detail.FailuresTruncated)
	}
	if !utils.PathEqual(detail.RecentFailures[0].Path, "/crypt/05.mp4") {
		t.Fatalf("old failures were not evicted: %+v", detail.RecentFailures[0])
	}
}

func TestScanTaskBypassesGenerationCapabilitiesAndRejectsOverlap(t *testing.T) {
	fixture := newCryptFixture(t)
	oldManager := TaskManager
	TaskManager = tache.NewManager[*Task](tache.WithWorks(0), tache.WithMaxRetry(0))
	t.Cleanup(func() {
		TaskManager.RemoveAll()
		TaskManager = oldManager
	})
	oldPort := conf.Conf.Scheme.HttpPort
	conf.Conf.Scheme.HttpPort = -1
	t.Cleanup(func() { conf.Conf.Scheme.HttpPort = oldPort })

	first, err := Create(context.Background(), CreateRequest{RootPath: fixture.root}, nil, true)
	if err != nil {
		t.Fatalf("scan-only Create() required generation capabilities: %v", err)
	}
	if first.Options.Mode != ModeScanOnly {
		t.Fatalf("mode = %q, want scan_only", first.Options.Mode)
	}
	_, err = Create(context.Background(), CreateRequest{RootPath: fixture.root}, nil, true)
	var kindErr *KindError
	if !errors.As(err, &kindErr) || kindErr.Kind != ErrorConflict {
		t.Fatalf("overlapping Create() error = %v, want conflict", err)
	}
}

func TestCanceledTaskReportsRemainingWork(t *testing.T) {
	fixture := newCryptFixture(t)
	putFixtureFile(t, path.Join(fixture.root, "video.mp4"), []byte("source"))
	task := NewTask(scanOptions(fixture.root, ModeMissing), fixture.storageID, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	task.SetCtx(ctx)
	if err := task.Run(); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run() error = %v, want context.Canceled", err)
	}
	if result := task.Detail().Stats.Result; result != ResultCanceled {
		t.Fatalf("result = %q, want canceled", result)
	}
}

func TestRetryReplacesCanceledContext(t *testing.T) {
	task := NewTask(Options{RootPath: "/crypt"}, 1, nil)
	ctx, cancel := context.WithCancel(context.Background())
	task.SetCtx(ctx)
	task.SetCancelFunc(cancel)
	cancel()

	task.OnBeforeRetry()
	if err := task.Ctx().Err(); err != nil {
		t.Fatalf("retry context remains canceled: %v", err)
	}
	task.Cancel()
	if !errors.Is(task.Ctx().Err(), context.Canceled) {
		t.Fatalf("replacement cancel function did not cancel retry context: %v", task.Ctx().Err())
	}
}

func TestRunFFmpegHonorsImageTimeout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test uses a shell script as a fake ffmpeg executable")
	}
	binDir := t.TempDir()
	fakeFFmpeg := filepath.Join(binDir, "ffmpeg")
	if err := os.WriteFile(fakeFFmpeg, []byte("#!/bin/sh\nwhile :; do :; done\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	oldTimeout := setting.GetStr(conf.ThumbnailImageTimeout)
	oldMinimum := setting.GetStr(conf.ThumbnailMinFreeSpace)
	op.Cache.SetSetting(conf.ThumbnailImageTimeout, &model.SettingItem{Key: conf.ThumbnailImageTimeout, Value: "1"})
	op.Cache.SetSetting(conf.ThumbnailMinFreeSpace, &model.SettingItem{Key: conf.ThumbnailMinFreeSpace, Value: "0"})
	t.Cleanup(func() {
		op.Cache.SetSetting(conf.ThumbnailImageTimeout, &model.SettingItem{Key: conf.ThumbnailImageTimeout, Value: oldTimeout})
		op.Cache.SetSetting(conf.ThumbnailMinFreeSpace, &model.SettingItem{Key: conf.ThumbnailMinFreeSpace, Value: oldMinimum})
	})
	oldLimiter := FFmpegLimiter
	FFmpegLimiter = NewDynamicLimiter(1)
	t.Cleanup(func() { FFmpegLimiter = oldLimiter })
	if err := os.MkdirAll(tempRoot(), 0o700); err != nil {
		t.Fatal(err)
	}

	started := time.Now()
	err := runFFmpeg(context.Background(), "/crypt/photo.jpg", Options{Width: 480, Quality: 75}, filepath.Join(t.TempDir(), "out.webp"), nil)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("runFFmpeg() error = %v, want deadline exceeded", err)
	}
	if elapsed := time.Since(started); elapsed > 3*time.Second {
		t.Fatalf("image timeout took too long: %v", elapsed)
	}
}
