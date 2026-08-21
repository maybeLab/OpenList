package crypt

import (
	"context"
	"errors"
	stdpath "path"
	"slices"
	"strings"
	"testing"

	"github.com/OpenListTeam/OpenList/v4/internal/model"
	logtest "github.com/sirupsen/logrus/hooks/test"
)

type fakeThumbnailSyncOps struct {
	sourceExists bool
	failCall     string
	calls        []string
}

func (f *fakeThumbnailSyncOps) record(call string) error {
	f.calls = append(f.calls, call)
	if call == f.failCall {
		return errors.New("injected thumbnail operation failure")
	}
	return nil
}

func (f *fakeThumbnailSyncOps) Exists(_ context.Context, path string) (bool, error) {
	if err := f.record("exists:" + path); err != nil {
		return false, err
	}
	return f.sourceExists, nil
}

func (f *fakeThumbnailSyncOps) MakeDir(_ context.Context, path string) error {
	return f.record("mkdir:" + path)
}

func (f *fakeThumbnailSyncOps) Move(_ context.Context, srcPath, dstDirPath string) error {
	return f.record("move:" + srcPath + "->" + dstDirPath)
}

func (f *fakeThumbnailSyncOps) Copy(_ context.Context, srcPath, dstDirPath string) error {
	return f.record("copy:" + srcPath + "->" + dstDirPath)
}

func (f *fakeThumbnailSyncOps) Rename(_ context.Context, srcPath, dstName string) error {
	return f.record("rename:" + srcPath + "->" + dstName)
}

func (f *fakeThumbnailSyncOps) Remove(_ context.Context, path string) error {
	return f.record("remove:" + path)
}

func newThumbnailTestCrypt(t *testing.T) *Crypt {
	t.Helper()
	d := &Crypt{Addition: Addition{
		FileNameEnc:     "standard",
		DirNameEnc:      "true",
		RemotePath:      "/underlying/crypt-root",
		Password:        "test-password",
		Salt:            "test-salt",
		EncryptedSuffix: ".bin",
		Thumbnail:       true,
	}}
	if err := d.Init(context.Background()); err != nil {
		t.Fatalf("initialize test Crypt: %v", err)
	}
	return d
}

func TestThumbnailPathEncryption(t *testing.T) {
	d := newThumbnailTestCrypt(t)
	parent := stdpath.Join(d.RemotePath, d.cipher.EncryptDirName("相册"))

	for _, name := range []string{"照片.final.JPG", "archive.tar.gz", ".hidden"} {
		t.Run(name, func(t *testing.T) {
			thumbnailPath := d.thumbnailPath(parent, name)
			decryptedDir, err := d.cipher.DecryptDirName(stdpath.Base(stdpath.Dir(thumbnailPath)))
			if err != nil {
				t.Fatalf("decrypt thumbnail directory: %v", err)
			}
			if decryptedDir != ".thumbnails" {
				t.Fatalf("thumbnail directory = %q, want .thumbnails", decryptedDir)
			}
			decryptedName, err := d.cipher.DecryptFileName(stdpath.Base(thumbnailPath))
			if err != nil {
				t.Fatalf("decrypt thumbnail name: %v", err)
			}
			if want := name + ".webp"; decryptedName != want {
				t.Fatalf("thumbnail name = %q, want %q", decryptedName, want)
			}
		})
	}
}

func TestSyncThumbnailActions(t *testing.T) {
	d := newThumbnailTestCrypt(t)
	srcParent := stdpath.Join(d.RemotePath, d.cipher.EncryptDirName("source"))
	dstParent := stdpath.Join(d.RemotePath, d.cipher.EncryptDirName("destination"))
	srcObj := &model.Object{
		Path: stdpath.Join(srcParent, d.cipher.EncryptFileName("photo.jpg")),
		Name: "photo.jpg",
	}
	dstDir := &model.Object{Path: dstParent, Name: "destination", IsFolder: true}
	srcThumbnail := d.thumbnailPath(srcParent, srcObj.GetName())
	dstThumbnailDir := d.thumbnailDirPath(dstParent)
	dstThumbnail := d.thumbnailPath(dstParent, srcObj.GetName())
	renameThumbnail := d.thumbnailPath(srcParent, "改名.final.jpg")

	tests := []struct {
		name    string
		action  thumbnailSyncAction
		dstDir  model.Obj
		newName string
		want    []string
	}{
		{
			name:   "move",
			action: thumbnailMove,
			dstDir: dstDir,
			want: []string{
				"exists:" + srcThumbnail,
				"remove:" + dstThumbnail,
				"mkdir:" + dstThumbnailDir,
				"move:" + srcThumbnail + "->" + dstThumbnailDir,
			},
		},
		{
			name:   "copy",
			action: thumbnailCopy,
			dstDir: dstDir,
			want: []string{
				"exists:" + srcThumbnail,
				"remove:" + dstThumbnail,
				"mkdir:" + dstThumbnailDir,
				"copy:" + srcThumbnail + "->" + dstThumbnailDir,
			},
		},
		{
			name:    "rename",
			action:  thumbnailRename,
			newName: "改名.final.jpg",
			want: []string{
				"exists:" + srcThumbnail,
				"remove:" + renameThumbnail,
				"rename:" + srcThumbnail + "->" + stdpath.Base(renameThumbnail),
			},
		},
		{
			name:   "remove",
			action: thumbnailRemove,
			want:   []string{"remove:" + srcThumbnail},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ops := &fakeThumbnailSyncOps{sourceExists: true}
			if err := d.syncThumbnail(context.Background(), tt.action, srcObj, tt.dstDir, tt.newName, ops); err != nil {
				t.Fatalf("sync thumbnail: %v", err)
			}
			if !slices.Equal(ops.calls, tt.want) {
				t.Fatalf("calls = %q, want %q", ops.calls, tt.want)
			}
		})
	}
}

func TestSyncThumbnailMissingSourceClearsDestination(t *testing.T) {
	d := newThumbnailTestCrypt(t)
	srcParent := stdpath.Join(d.RemotePath, d.cipher.EncryptDirName("source"))
	dstParent := stdpath.Join(d.RemotePath, d.cipher.EncryptDirName("destination"))
	srcObj := &model.Object{
		Path: stdpath.Join(srcParent, d.cipher.EncryptFileName("photo.jpg")),
		Name: "photo.jpg",
	}
	dstDir := &model.Object{Path: dstParent, IsFolder: true}
	srcThumbnail := d.thumbnailPath(srcParent, srcObj.GetName())

	tests := []struct {
		name    string
		action  thumbnailSyncAction
		dstDir  model.Obj
		newName string
		dstPath string
	}{
		{name: "move", action: thumbnailMove, dstDir: dstDir, dstPath: d.thumbnailPath(dstParent, srcObj.GetName())},
		{name: "copy", action: thumbnailCopy, dstDir: dstDir, dstPath: d.thumbnailPath(dstParent, srcObj.GetName())},
		{name: "rename", action: thumbnailRename, newName: "renamed.jpg", dstPath: d.thumbnailPath(srcParent, "renamed.jpg")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ops := &fakeThumbnailSyncOps{}
			if err := d.syncThumbnail(context.Background(), tt.action, srcObj, tt.dstDir, tt.newName, ops); err != nil {
				t.Fatalf("sync thumbnail: %v", err)
			}
			want := []string{"exists:" + srcThumbnail, "remove:" + tt.dstPath}
			if !slices.Equal(ops.calls, want) {
				t.Fatalf("calls = %q, want %q", ops.calls, want)
			}
		})
	}
}

func TestSyncThumbnailSkipsDisabledAndThumbnailObjects(t *testing.T) {
	ctx := context.Background()
	obj := &model.Object{Path: "/source/photo.jpg", Name: "photo.jpg"}
	disabledOps := &fakeThumbnailSyncOps{sourceExists: true}
	if err := (&Crypt{}).syncThumbnail(ctx, thumbnailRemove, obj, nil, "", disabledOps); err != nil {
		t.Fatalf("disabled thumbnail sync: %v", err)
	}
	if len(disabledOps.calls) != 0 {
		t.Fatalf("disabled thumbnail sync made calls: %q", disabledOps.calls)
	}

	d := newThumbnailTestCrypt(t)
	thumbnailDirName := d.cipher.EncryptDirName(".thumbnails")
	normalDirName := d.cipher.EncryptDirName("album")
	thumbnailObjName := d.cipher.EncryptFileName("photo.jpg.webp")
	objects := []*model.Object{
		{Path: stdpath.Join(d.RemotePath, thumbnailDirName), Name: ".thumbnails", IsFolder: true},
		{Path: stdpath.Join(d.RemotePath, normalDirName, thumbnailDirName, thumbnailObjName), Name: "photo.jpg.webp"},
		{Path: stdpath.Join(d.RemotePath, normalDirName, thumbnailDirName, normalDirName, thumbnailObjName), Name: "nested.webp"},
	}
	for _, thumbnailObj := range objects {
		ops := &fakeThumbnailSyncOps{sourceExists: true}
		if err := d.syncThumbnail(ctx, thumbnailRemove, thumbnailObj, nil, "", ops); err != nil {
			t.Fatalf("skip thumbnail object %q: %v", thumbnailObj.GetPath(), err)
		}
		if len(ops.calls) != 0 {
			t.Fatalf("thumbnail object %q made calls: %q", thumbnailObj.GetPath(), ops.calls)
		}
	}
}

func TestSyncThumbnailDoesNotTreatRemoteRootAsThumbnailTree(t *testing.T) {
	d := newThumbnailTestCrypt(t)
	thumbnailDirName := d.cipher.EncryptDirName(".thumbnails")
	d.RemotePath = stdpath.Join("/underlying", thumbnailDirName, "crypt-root")
	srcObj := &model.Object{
		Path: stdpath.Join(d.RemotePath, d.cipher.EncryptFileName("photo.jpg")),
		Name: "photo.jpg",
	}
	ops := &fakeThumbnailSyncOps{}
	if err := d.syncThumbnail(context.Background(), thumbnailRemove, srcObj, nil, "", ops); err != nil {
		t.Fatalf("sync thumbnail: %v", err)
	}
	if len(ops.calls) != 1 || !strings.HasPrefix(ops.calls[0], "remove:") {
		t.Fatalf("calls = %q, want one remove call", ops.calls)
	}
}

func TestSyncThumbnailHandlesDirectoriesAndSameNameRename(t *testing.T) {
	d := newThumbnailTestCrypt(t)
	parent := stdpath.Join(d.RemotePath, d.cipher.EncryptDirName("parent"))
	dstParent := stdpath.Join(d.RemotePath, d.cipher.EncryptDirName("destination"))
	dirObj := &model.Object{
		Path:     stdpath.Join(parent, d.cipher.EncryptDirName("album")),
		Name:     "album",
		IsFolder: true,
	}
	dstDir := &model.Object{Path: dstParent, IsFolder: true}
	ops := &fakeThumbnailSyncOps{sourceExists: true}
	if err := d.syncThumbnail(context.Background(), thumbnailCopy, dirObj, dstDir, "", ops); err != nil {
		t.Fatalf("copy directory thumbnail: %v", err)
	}
	if len(ops.calls) != 4 || !strings.HasPrefix(ops.calls[3], "copy:") {
		t.Fatalf("directory thumbnail calls = %q", ops.calls)
	}

	ops = &fakeThumbnailSyncOps{sourceExists: true}
	if err := d.syncThumbnail(context.Background(), thumbnailRename, dirObj, nil, dirObj.GetName(), ops); err != nil {
		t.Fatalf("same-name rename: %v", err)
	}
	if len(ops.calls) != 0 {
		t.Fatalf("same-name rename made calls: %q", ops.calls)
	}
}

func TestSyncThumbnailBestEffortLogsWarning(t *testing.T) {
	d := newThumbnailTestCrypt(t)
	srcObj := &model.Object{
		Path: stdpath.Join(d.RemotePath, d.cipher.EncryptFileName("photo.jpg")),
		Name: "photo.jpg",
	}
	srcThumbnail := d.thumbnailPath(stdpath.Dir(srcObj.GetPath()), srcObj.GetName())
	ops := &fakeThumbnailSyncOps{sourceExists: true, failCall: "remove:" + srcThumbnail}
	hook := logtest.NewGlobal()
	defer hook.Reset()

	d.syncThumbnailBestEffortWithOps(context.Background(), thumbnailRemove, srcObj, nil, "", ops)
	entry := hook.LastEntry()
	if entry == nil {
		t.Fatal("expected warning log entry")
	}
	if entry.Level.String() != "warning" || entry.Message != "failed to sync crypt thumbnail" {
		t.Fatalf("unexpected log entry: level=%s message=%q", entry.Level, entry.Message)
	}
	if entry.Data["operation"] != thumbnailRemove || entry.Data["source"] != srcObj.GetPath() {
		t.Fatalf("unexpected warning fields: %#v", entry.Data)
	}
}

func TestRunWithThumbnailSyncHonorsMainOperationResult(t *testing.T) {
	d := newThumbnailTestCrypt(t)
	srcObj := &model.Object{
		Path: stdpath.Join(d.RemotePath, d.cipher.EncryptFileName("photo.jpg")),
		Name: "photo.jpg",
	}
	mainErr := errors.New("main operation failed")
	ops := &fakeThumbnailSyncOps{sourceExists: true}
	err := d.runWithThumbnailSyncOps(context.Background(), thumbnailRemove, srcObj, nil, "", func() error {
		return mainErr
	}, ops)
	if !errors.Is(err, mainErr) {
		t.Fatalf("main operation error = %v, want %v", err, mainErr)
	}
	if len(ops.calls) != 0 {
		t.Fatalf("thumbnail operations ran after main failure: %q", ops.calls)
	}

	srcThumbnail := d.thumbnailPath(stdpath.Dir(srcObj.GetPath()), srcObj.GetName())
	ops = &fakeThumbnailSyncOps{failCall: "remove:" + srcThumbnail}
	err = d.runWithThumbnailSyncOps(context.Background(), thumbnailRemove, srcObj, nil, "", func() error {
		return nil
	}, ops)
	if err != nil {
		t.Fatalf("thumbnail failure changed main result: %v", err)
	}
}

func TestSyncThumbnailStopsAfterFailure(t *testing.T) {
	d := newThumbnailTestCrypt(t)
	srcParent := stdpath.Join(d.RemotePath, d.cipher.EncryptDirName("source"))
	dstParent := stdpath.Join(d.RemotePath, d.cipher.EncryptDirName("destination"))
	srcObj := &model.Object{
		Path: stdpath.Join(srcParent, d.cipher.EncryptFileName("photo.jpg")),
		Name: "photo.jpg",
	}
	dstDir := &model.Object{Path: dstParent, IsFolder: true}
	srcThumbnail := d.thumbnailPath(srcParent, srcObj.GetName())
	dstThumbnail := d.thumbnailPath(dstParent, srcObj.GetName())
	dstThumbnailDir := d.thumbnailDirPath(dstParent)

	tests := []struct {
		name     string
		failCall string
		want     []string
	}{
		{
			name:     "source stat",
			failCall: "exists:" + srcThumbnail,
			want:     []string{"exists:" + srcThumbnail},
		},
		{
			name:     "destination cleanup",
			failCall: "remove:" + dstThumbnail,
			want:     []string{"exists:" + srcThumbnail, "remove:" + dstThumbnail},
		},
		{
			name:     "destination directory",
			failCall: "mkdir:" + dstThumbnailDir,
			want:     []string{"exists:" + srcThumbnail, "remove:" + dstThumbnail, "mkdir:" + dstThumbnailDir},
		},
		{
			name:     "thumbnail move",
			failCall: "move:" + srcThumbnail + "->" + dstThumbnailDir,
			want: []string{
				"exists:" + srcThumbnail,
				"remove:" + dstThumbnail,
				"mkdir:" + dstThumbnailDir,
				"move:" + srcThumbnail + "->" + dstThumbnailDir,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ops := &fakeThumbnailSyncOps{sourceExists: true, failCall: tt.failCall}
			err := d.syncThumbnail(context.Background(), thumbnailMove, srcObj, dstDir, "", ops)
			if err == nil {
				t.Fatal("expected thumbnail sync error")
			}
			if !slices.Equal(ops.calls, tt.want) {
				t.Fatalf("calls = %q, want %q", ops.calls, tt.want)
			}
		})
	}
}

func TestSyncThumbnailTransferFailures(t *testing.T) {
	d := newThumbnailTestCrypt(t)
	srcParent := stdpath.Join(d.RemotePath, d.cipher.EncryptDirName("source"))
	dstParent := stdpath.Join(d.RemotePath, d.cipher.EncryptDirName("destination"))
	srcObj := &model.Object{
		Path: stdpath.Join(srcParent, d.cipher.EncryptFileName("photo.jpg")),
		Name: "photo.jpg",
	}
	dstDir := &model.Object{Path: dstParent, IsFolder: true}
	srcThumbnail := d.thumbnailPath(srcParent, srcObj.GetName())
	dstThumbnailDir := d.thumbnailDirPath(dstParent)
	renameThumbnail := d.thumbnailPath(srcParent, "renamed.jpg")

	tests := []struct {
		name     string
		action   thumbnailSyncAction
		dstDir   model.Obj
		newName  string
		failCall string
	}{
		{
			name:     "copy",
			action:   thumbnailCopy,
			dstDir:   dstDir,
			failCall: "copy:" + srcThumbnail + "->" + dstThumbnailDir,
		},
		{
			name:     "rename",
			action:   thumbnailRename,
			newName:  "renamed.jpg",
			failCall: "rename:" + srcThumbnail + "->" + stdpath.Base(renameThumbnail),
		},
		{
			name:     "remove",
			action:   thumbnailRemove,
			failCall: "remove:" + srcThumbnail,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ops := &fakeThumbnailSyncOps{sourceExists: true, failCall: tt.failCall}
			err := d.syncThumbnail(context.Background(), tt.action, srcObj, tt.dstDir, tt.newName, ops)
			if err == nil {
				t.Fatal("expected thumbnail sync error")
			}
			if got := ops.calls[len(ops.calls)-1]; got != tt.failCall {
				t.Fatalf("last call = %q, want %q", got, tt.failCall)
			}
		})
	}
}
