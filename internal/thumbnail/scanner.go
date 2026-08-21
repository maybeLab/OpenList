package thumbnail

import (
	"context"
	"encoding/json"
	"fmt"
	stdpath "path"
	"strings"

	"github.com/OpenListTeam/OpenList/v4/internal/conf"
	"github.com/OpenListTeam/OpenList/v4/internal/errs"
	"github.com/OpenListTeam/OpenList/v4/internal/fs"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/internal/op"
	"github.com/OpenListTeam/OpenList/v4/pkg/utils"
)

type ScanEvent struct {
	CurrentPath   string
	Dirs          int64
	Files         int64
	Media         int64
	ExistingValid int64
	SkippedMounts int64
}

type Target struct {
	StorageID uint
	Type      string
	MediaType string
}

func ValidateTarget(ctx context.Context, rootPath string) (Target, error) {
	if containsThumbnailDirectory(rootPath) {
		return Target{}, &KindError{Kind: ErrorInvalid, Err: fmt.Errorf("root_path cannot be inside a .thumbnails directory")}
	}
	storage, err := fs.GetStorage(rootPath, &fs.GetStoragesArgs{})
	if err != nil {
		return Target{}, &KindError{Kind: ErrorInvalid, Err: fmt.Errorf("resolve root storage: %w", err)}
	}
	if storage.Config().Name != "Crypt" || storage.GetStorage().Driver != "Crypt" {
		return Target{}, &KindError{Kind: ErrorCapability, Err: fmt.Errorf("root_path must belong to a Crypt storage")}
	}
	var addition struct {
		Thumbnail bool `json:"thumbnail"`
	}
	if err := json.Unmarshal([]byte(storage.GetStorage().Addition), &addition); err != nil {
		return Target{}, &KindError{Kind: ErrorCapability, Err: fmt.Errorf("read Crypt thumbnail configuration: %w", err)}
	}
	if !addition.Thumbnail {
		return Target{}, &KindError{Kind: ErrorCapability, Err: fmt.Errorf("Crypt storage thumbnail support is disabled")}
	}
	if storage.Config().NoUpload {
		return Target{}, &KindError{Kind: ErrorCapability, Err: fmt.Errorf("Crypt storage is read-only")}
	}
	if storage.Config().CheckStatus && storage.GetStorage().Status != op.WORK {
		return Target{}, &KindError{Kind: ErrorCapability, Err: fmt.Errorf("Crypt storage is not initialized: %s", storage.GetStorage().Status)}
	}
	obj, err := fs.Get(ctx, rootPath, &fs.GetArgs{NoLog: true})
	if err != nil {
		return Target{}, &KindError{Kind: ErrorInvalid, Err: fmt.Errorf("get root_path: %w", err)}
	}
	target := Target{StorageID: storage.GetStorage().ID, Type: TargetDirectory}
	writeTarget := obj
	if !obj.IsDir() {
		switch utils.GetObjType(obj.GetName(), false) {
		case conf.IMAGE:
			target.Type, target.MediaType = TargetFile, "image"
		case conf.VIDEO:
			target.Type, target.MediaType = TargetFile, "video"
		default:
			return Target{}, &KindError{Kind: ErrorInvalid, Err: fmt.Errorf("root_path file must be an image or video")}
		}
		writeTarget, err = fs.Get(ctx, stdpath.Dir(rootPath), &fs.GetArgs{NoLog: true})
		if err != nil {
			return Target{}, &KindError{Kind: ErrorCapability, Err: fmt.Errorf("get target parent directory: %w", err)}
		}
	}
	if model.ObjHasMask(writeTarget, model.NoWrite) {
		return Target{}, &KindError{Kind: ErrorCapability, Err: fmt.Errorf("root_path is not writable")}
	}
	return target, nil
}

func containsThumbnailDirectory(p string) bool {
	for _, segment := range strings.Split(utils.FixAndCleanPath(p), "/") {
		if segment == ".thumbnails" {
			return true
		}
	}
	return false
}

func Scan(ctx context.Context, storageID uint, options Options, observe func(ScanEvent)) (ScanResult, error) {
	if options.TargetType == TargetFile {
		return scanFile(ctx, storageID, options, observe)
	}
	if err := ensureStorage(options.RootPath, storageID); err != nil {
		return ScanResult{}, err
	}
	queue := []string{options.RootPath}
	result := ScanResult{}
	for len(queue) > 0 {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		dir := queue[0]
		queue = queue[1:]
		sameStorage, err := belongsToStorage(dir, storageID)
		if err != nil {
			return result, err
		}
		if !sameStorage {
			observe(ScanEvent{CurrentPath: dir, SkippedMounts: 1})
			continue
		}
		observe(ScanEvent{CurrentPath: dir, Dirs: 1})
		result.Directories = append(result.Directories, dir)
		objs, err := fs.List(ctx, dir, &fs.ListArgs{NoLog: true})
		if err != nil {
			return result, fmt.Errorf("list %s: %w", dir, err)
		}
		for _, obj := range objs {
			if err := ctx.Err(); err != nil {
				return result, err
			}
			name := obj.GetName()
			child := stdpath.Join(dir, name)
			if obj.IsDir() {
				if name != ".thumbnails" && options.Recursive {
					queue = append(queue, child)
				}
				continue
			}
			observe(ScanEvent{CurrentPath: child, Files: 1})
			objType := utils.GetObjType(name, false)
			mediaType := ""
			switch objType {
			case conf.IMAGE:
				if options.IncludeImages {
					mediaType = "image"
				}
			case conf.VIDEO:
				if options.IncludeVideos {
					mediaType = "video"
				}
			}
			if mediaType == "" {
				continue
			}
			observe(ScanEvent{CurrentPath: child, Media: 1})
			thumbnailPath := stdpath.Join(dir, ".thumbnails", name+".webp")
			candidate := Candidate{
				SourcePath:    child,
				ThumbnailPath: thumbnailPath,
				MediaType:     mediaType,
				Size:          obj.GetSize(),
				ModTime:       obj.ModTime(),
			}
			if options.Mode == ModeForce {
				result.Candidates = append(result.Candidates, candidate)
				continue
			}
			thumb, err := fs.Get(ctx, thumbnailPath, &fs.GetArgs{NoLog: true})
			if err == nil && thumb.GetSize() > 0 {
				observe(ScanEvent{CurrentPath: child, ExistingValid: 1})
				continue
			}
			if err != nil && !errs.IsNotFoundError(err) {
				// Treat transient metadata failures as candidates and let the worker's
				// second check produce a file-level failure instead of aborting a scan.
			}
			result.Candidates = append(result.Candidates, candidate)
		}
	}
	return result, nil
}

func scanFile(ctx context.Context, storageID uint, options Options, observe func(ScanEvent)) (ScanResult, error) {
	if err := ensureStorage(options.RootPath, storageID); err != nil {
		return ScanResult{}, err
	}
	obj, err := fs.Get(ctx, options.RootPath, &fs.GetArgs{NoLog: true})
	if err != nil {
		return ScanResult{}, fmt.Errorf("get %s: %w", options.RootPath, err)
	}
	if obj.IsDir() {
		return ScanResult{}, fmt.Errorf("file target became a directory: %s", options.RootPath)
	}
	mediaType := ""
	switch utils.GetObjType(obj.GetName(), false) {
	case conf.IMAGE:
		mediaType = "image"
	case conf.VIDEO:
		mediaType = "video"
	default:
		return ScanResult{}, fmt.Errorf("file target is no longer an image or video: %s", options.RootPath)
	}
	observe(ScanEvent{CurrentPath: options.RootPath, Files: 1, Media: 1})
	candidate := Candidate{
		SourcePath:    options.RootPath,
		ThumbnailPath: stdpath.Join(stdpath.Dir(options.RootPath), ".thumbnails", stdpath.Base(options.RootPath)+".webp"),
		MediaType:     mediaType,
		Size:          obj.GetSize(),
		ModTime:       obj.ModTime(),
	}
	if options.Mode != ModeForce {
		thumb, statErr := fs.Get(ctx, candidate.ThumbnailPath, &fs.GetArgs{NoLog: true})
		if statErr == nil && thumb.GetSize() > 0 {
			observe(ScanEvent{CurrentPath: options.RootPath, ExistingValid: 1})
			return ScanResult{}, nil
		}
	}
	return ScanResult{Candidates: []Candidate{candidate}}, nil
}

func ensureStorage(path string, storageID uint) error {
	sameStorage, err := belongsToStorage(path, storageID)
	if err != nil {
		return err
	}
	if !sameStorage {
		return fmt.Errorf("cross-mount scan is not supported: %s", path)
	}
	return nil
}

func belongsToStorage(path string, storageID uint) (bool, error) {
	storage, err := fs.GetStorage(path, &fs.GetStoragesArgs{})
	if err != nil {
		return false, fmt.Errorf("resolve storage for %s: %w", path, err)
	}
	return storage.GetStorage().ID == storageID, nil
}
