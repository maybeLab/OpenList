package _189_tv

import (
	"testing"
	"time"
)

func TestNormalizeRenameObjFileFallback(t *testing.T) {
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

	got, ok := normalizeRenameObj(src, &Cloud189File{}, "new-name.jpg").(*Cloud189File)
	if !ok {
		t.Fatalf("got %T, want *Cloud189File", got)
	}
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

func TestNormalizeRenameObjFolderFallback(t *testing.T) {
	lastOp := Time(time.Date(2026, 6, 9, 11, 0, 0, 0, time.UTC))
	create := Time(time.Date(2026, 6, 8, 11, 0, 0, 0, time.UTC))
	src := &Cloud189Folder{
		ID:         String("folder-id"),
		ParentID:   99,
		Name:       "old-folder",
		LastOpTime: lastOp,
		CreateDate: create,
	}

	got, ok := normalizeRenameObj(src, &Cloud189Folder{}, "new-folder").(*Cloud189Folder)
	if !ok {
		t.Fatalf("got %T, want *Cloud189Folder", got)
	}
	if got.GetName() != "new-folder" {
		t.Fatalf("name = %q, want %q", got.GetName(), "new-folder")
	}
	if got.ID != src.ID || got.ParentID != src.ParentID || got.LastOpTime != lastOp || got.CreateDate != create {
		t.Fatalf("fallback fields were not preserved: got %+v, src %+v", got, src)
	}
}
