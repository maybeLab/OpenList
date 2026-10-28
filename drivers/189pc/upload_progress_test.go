package _189pc

import (
	"errors"
	"strings"
	"testing"

	"github.com/OpenListTeam/OpenList/v4/drivers/base"
)

func TestUploadProgressRecoveryWithExtensionKeys(t *testing.T) {
	d := &Cloud189PC{}
	keys := uploadProgressKeys(t.Name(), "same-md5", "video.mp4")
	otherKeys := uploadProgressKeys(t.Name(), "same-md5", "video.mkv")
	if !base.SaveUploadProgress(d, uploadProgressWithID(""), keys...) {
		t.Fatal("could not cache invalid progress")
	}
	if _, ok := base.GetUploadProgress[*UploadProgress](d, otherKeys...); ok {
		t.Fatal("different extensions shared progress")
	}
	cached, ok := base.GetUploadProgress[*UploadProgress](d, keys...)
	if !ok {
		t.Fatal("progress missing under original extension key")
	}
	calls := 0
	got, err := getOrInitUploadProgress(cached, ok, func() (*UploadProgress, error) {
		calls++
		return uploadProgressWithID("reinitialized-id"), nil
	})
	if err != nil || calls != 1 || got.UploadInfo.Data.UploadFileID != "reinitialized-id" {
		t.Fatalf("recovery = %v, calls=%d, err=%v", got, calls, err)
	}
	base.SaveUploadProgress(d, got, keys...)
	cached, ok = base.GetUploadProgress[*UploadProgress](d, keys...)
	reused, err := getOrInitUploadProgress(cached, ok, func() (*UploadProgress, error) {
		t.Fatal("valid resumed progress must not be initialized again")
		return nil, nil
	})
	if err != nil || reused != got {
		t.Fatalf("resume = %v, err=%v", reused, err)
	}
}

func uploadProgressWithID(id string) *UploadProgress {
	progress := &UploadProgress{}
	progress.UploadInfo.Data.UploadFileID = id
	return progress
}

func TestGetOrInitUploadProgressUsesValidCache(t *testing.T) {
	cached := uploadProgressWithID("cached-id")
	initCalls := 0
	got, err := getOrInitUploadProgress(cached, true, func() (*UploadProgress, error) {
		initCalls++
		return uploadProgressWithID("new-id"), nil
	})
	if err != nil {
		t.Fatalf("getOrInitUploadProgress() error: %v", err)
	}
	if got != cached {
		t.Fatalf("getOrInitUploadProgress() returned %p, want cached %p", got, cached)
	}
	if initCalls != 0 {
		t.Fatalf("initializer calls = %d, want 0", initCalls)
	}
}

func TestGetOrInitUploadProgressReinitializesInvalidCache(t *testing.T) {
	for _, cached := range []*UploadProgress{nil, uploadProgressWithID(""), uploadProgressWithID("   ")} {
		initCalls := 0
		got, err := getOrInitUploadProgress(cached, true, func() (*UploadProgress, error) {
			initCalls++
			return uploadProgressWithID("new-id"), nil
		})
		if err != nil {
			t.Fatalf("getOrInitUploadProgress() error: %v", err)
		}
		if got.UploadInfo.Data.UploadFileID != "new-id" {
			t.Fatalf("uploadFileId = %q, want new-id", got.UploadInfo.Data.UploadFileID)
		}
		if initCalls != 1 {
			t.Fatalf("initializer calls = %d, want 1", initCalls)
		}
	}
}

func TestGetOrInitUploadProgressInitializesCacheMiss(t *testing.T) {
	initCalls := 0
	got, err := getOrInitUploadProgress(uploadProgressWithID("ignored-id"), false, func() (*UploadProgress, error) {
		initCalls++
		return uploadProgressWithID("new-id"), nil
	})
	if err != nil {
		t.Fatalf("getOrInitUploadProgress() error: %v", err)
	}
	if got.UploadInfo.Data.UploadFileID != "new-id" || initCalls != 1 {
		t.Fatalf("got id %q with %d initializer calls, want new-id with 1 call", got.UploadInfo.Data.UploadFileID, initCalls)
	}
}

func TestGetOrInitUploadProgressRejectsEmptyFreshID(t *testing.T) {
	got, err := getOrInitUploadProgress(nil, false, func() (*UploadProgress, error) {
		return uploadProgressWithID(""), nil
	})
	if got != nil {
		t.Fatalf("getOrInitUploadProgress() = %#v, want nil", got)
	}
	if err == nil || !strings.Contains(err.Error(), "empty uploadFileId") {
		t.Fatalf("getOrInitUploadProgress() error = %v, want empty uploadFileId", err)
	}
}

func TestGetOrInitUploadProgressReturnsInitError(t *testing.T) {
	wantErr := errors.New("init failed")
	got, err := getOrInitUploadProgress(nil, false, func() (*UploadProgress, error) {
		return nil, wantErr
	})
	if got != nil {
		t.Fatalf("getOrInitUploadProgress() = %#v, want nil", got)
	}
	if !errors.Is(err, wantErr) {
		t.Fatalf("getOrInitUploadProgress() error = %v, want %v", err, wantErr)
	}
}

func TestValidUploadProgressRejectsUnsavableState(t *testing.T) {
	for _, progress := range []*UploadProgress{nil, uploadProgressWithID(""), uploadProgressWithID(" \t")} {
		if validUploadProgress(progress) {
			t.Fatalf("validUploadProgress(%#v) = true, want false", progress)
		}
	}
	if !validUploadProgress(uploadProgressWithID("upload-id")) {
		t.Fatal("validUploadProgress(valid) = false, want true")
	}
}
