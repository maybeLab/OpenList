package crypt

import (
	"context"
	"fmt"
	stdpath "path"
	"path/filepath"
	"strings"

	"github.com/OpenListTeam/OpenList/v4/internal/errs"
	"github.com/OpenListTeam/OpenList/v4/internal/fs"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
)

// will give the best guessing based on the path
func guessPath(path string) (isFolder, secondTry bool) {
	if strings.HasSuffix(path, "/") {
		//confirmed a folder
		return true, false
	}
	lastSlash := strings.LastIndex(path, "/")
	if !strings.Contains(path[lastSlash:], ".") {
		//no dot, try folder then try file
		return true, true
	}
	return false, true
}

func (d *Crypt) encryptPath(path string, isFolder bool) string {
	if isFolder {
		return d.cipher.EncryptDirName(path)
	}
	dir, fileName := filepath.Split(path)
	return stdpath.Join(d.cipher.EncryptDirName(dir), d.cipher.EncryptFileName(fileName))
}

type thumbnailSyncAction string

const (
	thumbnailMove   thumbnailSyncAction = "move"
	thumbnailCopy   thumbnailSyncAction = "copy"
	thumbnailRename thumbnailSyncAction = "rename"
	thumbnailRemove thumbnailSyncAction = "remove"
)

type thumbnailSyncOps interface {
	Exists(ctx context.Context, path string) (bool, error)
	MakeDir(ctx context.Context, path string) error
	Move(ctx context.Context, srcPath, dstDirPath string) error
	Copy(ctx context.Context, srcPath, dstDirPath string) error
	Rename(ctx context.Context, srcPath, dstName string) error
	Remove(ctx context.Context, path string) error
}

type defaultThumbnailSyncOps struct{}

func (defaultThumbnailSyncOps) Exists(ctx context.Context, path string) (bool, error) {
	_, err := fs.Get(ctx, path, &fs.GetArgs{NoLog: true})
	if err == nil {
		return true, nil
	}
	if errs.IsObjectNotFound(err) {
		return false, nil
	}
	return false, err
}

func (defaultThumbnailSyncOps) MakeDir(ctx context.Context, path string) error {
	return fs.MakeDir(ctx, path)
}

func (defaultThumbnailSyncOps) Move(ctx context.Context, srcPath, dstDirPath string) error {
	_, err := fs.Move(ctx, srcPath, dstDirPath, true)
	return err
}

func (defaultThumbnailSyncOps) Copy(ctx context.Context, srcPath, dstDirPath string) error {
	_, err := fs.Copy(ctx, srcPath, dstDirPath, true)
	return err
}

func (defaultThumbnailSyncOps) Rename(ctx context.Context, srcPath, dstName string) error {
	return fs.Rename(ctx, srcPath, dstName, true)
}

func (defaultThumbnailSyncOps) Remove(ctx context.Context, path string) error {
	return fs.Remove(ctx, path)
}

func (d *Crypt) thumbnailDirPath(parentPath string) string {
	return stdpath.Join(parentPath, d.cipher.EncryptDirName(".thumbnails"))
}

func (d *Crypt) thumbnailPath(parentPath, objectName string) string {
	return stdpath.Join(d.thumbnailDirPath(parentPath), d.cipher.EncryptFileName(objectName+".webp"))
}

func (d *Crypt) isThumbnailObject(obj model.Obj) bool {
	objPath := stdpath.Clean(obj.GetPath())
	rootPath := stdpath.Clean(d.RemotePath)
	relPath := objPath
	if rootPath == "." {
		rootPath = "/"
	}
	if rootPath == "/" {
		relPath = strings.TrimPrefix(objPath, "/")
	} else if objPath == rootPath {
		relPath = ""
	} else if strings.HasPrefix(objPath, rootPath+"/") {
		relPath = strings.TrimPrefix(objPath, rootPath+"/")
	}

	thumbnailDirName := d.cipher.EncryptDirName(".thumbnails")
	for part := range strings.SplitSeq(relPath, "/") {
		if part == thumbnailDirName {
			return true
		}
	}
	return false
}

func (d *Crypt) syncThumbnail(ctx context.Context, action thumbnailSyncAction, srcObj, dstDir model.Obj, newName string, ops thumbnailSyncOps) error {
	if !d.Thumbnail || d.isThumbnailObject(srcObj) {
		return nil
	}

	srcParentPath := stdpath.Dir(srcObj.GetPath())
	srcThumbnailPath := d.thumbnailPath(srcParentPath, srcObj.GetName())
	if action == thumbnailRemove {
		if err := ops.Remove(ctx, srcThumbnailPath); err != nil {
			return fmt.Errorf("remove source thumbnail [%s]: %w", srcThumbnailPath, err)
		}
		return nil
	}

	dstParentPath := srcParentPath
	dstObjectName := srcObj.GetName()
	if action == thumbnailMove || action == thumbnailCopy {
		dstParentPath = dstDir.GetPath()
	} else if action == thumbnailRename {
		dstObjectName = newName
	}
	dstThumbnailDirPath := d.thumbnailDirPath(dstParentPath)
	dstThumbnailPath := d.thumbnailPath(dstParentPath, dstObjectName)
	if srcThumbnailPath == dstThumbnailPath {
		return nil
	}

	srcExists, err := ops.Exists(ctx, srcThumbnailPath)
	if err != nil {
		return fmt.Errorf("check source thumbnail [%s]: %w", srcThumbnailPath, err)
	}
	if err = ops.Remove(ctx, dstThumbnailPath); err != nil {
		return fmt.Errorf("remove destination thumbnail [%s]: %w", dstThumbnailPath, err)
	}
	if !srcExists {
		return nil
	}

	if action == thumbnailMove || action == thumbnailCopy {
		if err = ops.MakeDir(ctx, dstThumbnailDirPath); err != nil {
			return fmt.Errorf("make destination thumbnail directory [%s]: %w", dstThumbnailDirPath, err)
		}
	}

	switch action {
	case thumbnailMove:
		err = ops.Move(ctx, srcThumbnailPath, dstThumbnailDirPath)
	case thumbnailCopy:
		err = ops.Copy(ctx, srcThumbnailPath, dstThumbnailDirPath)
	case thumbnailRename:
		err = ops.Rename(ctx, srcThumbnailPath, stdpath.Base(dstThumbnailPath))
	default:
		return fmt.Errorf("unsupported thumbnail sync action %q", action)
	}
	if err != nil {
		return fmt.Errorf("%s thumbnail [%s] to [%s]: %w", action, srcThumbnailPath, dstThumbnailPath, err)
	}
	return nil
}
