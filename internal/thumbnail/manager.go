package thumbnail

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/OpenListTeam/OpenList/v4/internal/conf"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/internal/op"
	"github.com/OpenListTeam/OpenList/v4/internal/setting"
	"github.com/OpenListTeam/OpenList/v4/pkg/utils"
	"github.com/OpenListTeam/tache"
)

var TaskManager *tache.Manager[*Task]
var createMu sync.Mutex

func Initialize() error {
	workers := setting.GetInt(conf.TaskThumbnailThreadsNum, conf.Conf.Tasks.Thumbnail.Workers)
	TaskManager = tache.NewManager[*Task](tache.WithWorks(max(0, workers)), tache.WithMaxRetry(0))
	FFmpegLimiter.SetLimit(setting.GetInt(conf.ThumbnailFFmpegThreadsNum, 2))
	op.RegisterSettingChangingCallback(func() {
		TaskManager.SetWorkersNumActive(int64(max(0, setting.GetInt(conf.TaskThumbnailThreadsNum, conf.Conf.Tasks.Thumbnail.Workers))))
		FFmpegLimiter.SetLimit(setting.GetInt(conf.ThumbnailFFmpegThreadsNum, 2))
	})
	return CleanTempRoot()
}

func Create(ctx context.Context, request CreateRequest, creator *model.User, forceScanOnly bool) (*Task, error) {
	options, err := ResolveOptions(request)
	if err != nil {
		return nil, &KindError{Kind: ErrorInvalid, Err: err}
	}
	if forceScanOnly {
		options.Mode = ModeScanOnly
		options.CleanupOrphan = false
	}
	target, err := ValidateTarget(ctx, options.RootPath)
	if err != nil {
		return nil, err
	}
	normalizeTargetOptions(&options, target, forceScanOnly)
	if options.Mode != ModeScanOnly {
		capabilities := GetCapabilities(ctx, true)
		if !capabilities.Ready {
			return nil, &KindError{Kind: ErrorCapability, Err: capabilities.ReadinessError()}
		}
	}
	createMu.Lock()
	defer createMu.Unlock()
	if overlappingTask(target.StorageID, options.RootPath, "") != nil {
		return nil, &KindError{Kind: ErrorConflict, Err: fmt.Errorf("an overlapping thumbnail task is already active")}
	}
	task := NewTask(options, target.StorageID, creator)
	TaskManager.Add(task)
	return task, nil
}

func normalizeTargetOptions(options *Options, target Target, forceScanOnly bool) {
	options.TargetType = target.Type
	if target.Type == TargetFile {
		options.Recursive = false
		options.CleanupOrphan = false
		options.IncludeImages = target.MediaType == "image"
		options.IncludeVideos = target.MediaType == "video"
		if !forceScanOnly {
			options.Mode = ModeForce
		}
	}
}

func (c Capabilities) ReadinessError() error {
	var reasons []string
	if !c.FFmpeg.Available {
		reasons = append(reasons, "ffmpeg: "+c.FFmpeg.Error)
	}
	if !c.WebPEncoder {
		reasons = append(reasons, "ffmpeg WebP encoder is unavailable")
	}
	if !c.TempWritable {
		reasons = append(reasons, "thumbnail temp directory is not writable")
	}
	if !c.TempSpace.Available && c.TempSpace.Error != "" {
		reasons = append(reasons, c.TempSpace.Error)
	}
	if !c.Loopback.Available {
		reasons = append(reasons, "loopback: "+c.Loopback.Error)
	}
	if len(reasons) == 0 {
		return fmt.Errorf("thumbnail capabilities are not ready")
	}
	return fmt.Errorf("thumbnail capabilities are not ready: %s", strings.Join(reasons, "; "))
}

func overlappingTask(storageID uint, rootPath, excludeID string) *Task {
	if TaskManager == nil {
		return nil
	}
	for _, task := range TaskManager.GetAll() {
		if task.GetID() == excludeID || task.StorageID != storageID {
			continue
		}
		switch task.GetState() {
		case tache.StatePending, tache.StateRunning, tache.StateCanceling,
			tache.StateErrored, tache.StateWaitingRetry, tache.StateBeforeRetry:
			if utils.IsSubPath(task.Options.RootPath, rootPath) || utils.IsSubPath(rootPath, task.Options.RootPath) {
				return task
			}
		}
	}
	return nil
}

func checkRuntimeOverlap(task *Task) error {
	createMu.Lock()
	defer createMu.Unlock()
	if other := overlappingTask(task.StorageID, task.Options.RootPath, task.GetID()); other != nil {
		return fmt.Errorf("thumbnail task overlaps active task %s", other.GetID())
	}
	return nil
}
