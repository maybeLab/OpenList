package _189pc

import (
	"testing"
	"time"
)

func TestRenameRespToFileFallbackUsesNewNameAndSourceFields(t *testing.T) {
	lastOp := Time(time.Date(2026, 6, 9, 10, 0, 0, 0, time.UTC))
	create := Time(time.Date(2026, 6, 8, 10, 0, 0, 0, time.UTC))
	src := &Cloud189File{
		ID:         String("file-id"),
		Name:       "old-name.jpg",
		Size:       123,
		Md5:        "old-md5",
		LastOpTime: lastOp,
		CreateDate: create,
	}
	src.Icon.SmallUrl = "https://example.com/thumb.jpg"

	got := (&RenameResp{}).toFile(src, "new-name.jpg")

	if got.GetName() != "new-name.jpg" {
		t.Fatalf("name = %q, want %q", got.GetName(), "new-name.jpg")
	}
	if got.ID != src.ID || got.Size != src.Size || got.Md5 != src.Md5 || got.LastOpTime != lastOp || got.CreateDate != create {
		t.Fatalf("fallback fields were not preserved: got %+v, src %+v", got, src)
	}
	if got.Icon.SmallUrl != src.Icon.SmallUrl {
		t.Fatalf("icon = %q, want %q", got.Icon.SmallUrl, src.Icon.SmallUrl)
	}
}

func TestRenameRespToFolderFallbackUsesNewNameAndSourceFields(t *testing.T) {
	lastOp := Time(time.Date(2026, 6, 9, 11, 0, 0, 0, time.UTC))
	create := Time(time.Date(2026, 6, 8, 11, 0, 0, 0, time.UTC))
	src := &Cloud189Folder{
		ID:         String("folder-id"),
		ParentID:   99,
		Name:       "old-folder",
		LastOpTime: lastOp,
		CreateDate: create,
	}

	got := (&RenameResp{}).toFolder(src, "new-folder")

	if got.GetName() != "new-folder" {
		t.Fatalf("name = %q, want %q", got.GetName(), "new-folder")
	}
	if got.ID != src.ID || got.ParentID != src.ParentID || got.LastOpTime != lastOp || got.CreateDate != create {
		t.Fatalf("fallback fields were not preserved: got %+v, src %+v", got, src)
	}
}

func TestRenameRespToFilePrefersResponseFields(t *testing.T) {
	src := &Cloud189File{
		ID:   String("old-id"),
		Name: "old-name.jpg",
		Size: 123,
		Md5:  "old-md5",
	}
	resp := &RenameResp{
		ID:   String("new-id"),
		Name: "response-name.jpg",
		Size: 456,
		MD5:  "response-md5",
	}

	got := resp.toFile(src, "fallback-name.jpg")

	if got.ID != resp.ID || got.GetName() != resp.Name || got.Size != resp.Size || got.Md5 != resp.MD5 {
		t.Fatalf("response fields were not preferred: got %+v, resp %+v", got, resp)
	}
}
