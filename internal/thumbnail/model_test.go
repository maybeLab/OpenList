package thumbnail

import (
	"testing"
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/conf"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/internal/op"
)

func init() {
	settings := map[string]string{
		conf.ThumbnailDefaultWidth:   "480",
		conf.ThumbnailDefaultQuality: "75",
		conf.ThumbnailVideoSecond:    "3",
		conf.LinkExpiration:          "0",
		conf.Token:                   "thumbnail-test-token",
	}
	for key, value := range settings {
		op.Cache.SetSetting(key, &model.SettingItem{Key: key, Value: value})
	}
}

func boolPtr(value bool) *bool { return &value }

func floatPtr(value float64) *float64 { return &value }

func TestResolveOptionsDefaults(t *testing.T) {
	options, err := ResolveOptions(CreateRequest{RootPath: "/crypt/photos"})
	if err != nil {
		t.Fatalf("ResolveOptions() error = %v", err)
	}
	if options.RootPath != "/crypt/photos" || !options.Recursive || !options.IncludeImages || !options.IncludeVideos {
		t.Fatalf("unexpected defaults: %+v", options)
	}
	if options.Mode != ModeMissing || options.Width != 480 || options.Quality != 75 || options.VideoSecond != 3 {
		t.Fatalf("unexpected generation defaults: %+v", options)
	}
}

func TestResolveOptionsScanOnlyDisablesCleanup(t *testing.T) {
	options, err := ResolveOptions(CreateRequest{
		RootPath:      "/crypt",
		Recursive:     boolPtr(false),
		IncludeImages: boolPtr(true),
		IncludeVideos: boolPtr(false),
		Mode:          ModeScanOnly,
		CleanupOrphan: true,
		Width:         640,
		Quality:       80,
		VideoSecond:   floatPtr(0),
	})
	if err != nil {
		t.Fatalf("ResolveOptions() error = %v", err)
	}
	if options.Recursive || options.IncludeVideos || options.CleanupOrphan {
		t.Fatalf("scan-only options were not resolved correctly: %+v", options)
	}
}

func TestResolveOptionsRejectsUnsafeOrInvalidInput(t *testing.T) {
	tests := []CreateRequest{
		{RootPath: "relative"},
		{RootPath: "/crypt/../other"},
		{RootPath: `/crypt\..\other`},
		{RootPath: "/crypt", Mode: "stale"},
		{RootPath: "/crypt", IncludeImages: boolPtr(false), IncludeVideos: boolPtr(false)},
		{RootPath: "/crypt", Width: 63},
		{RootPath: "/crypt", Quality: 101},
		{RootPath: "/crypt", VideoSecond: floatPtr(-1)},
	}
	for _, request := range tests {
		if _, err := ResolveOptions(request); err == nil {
			t.Errorf("ResolveOptions(%+v) unexpectedly succeeded", request)
		}
	}
}

func TestFailureRingAndProgress(t *testing.T) {
	task := NewTask(Options{RootPath: "/crypt"}, 1, nil)
	task.stats.TotalCandidates = 2
	task.workStarted = time.Now().Add(-time.Minute)
	task.finishCandidate("failed", Candidate{SourcePath: "/crypt/a.mp4", MediaType: "video"}, "ffmpeg", assertError("sign=secret&x=1"))
	task.finishCandidate("skipped", Candidate{SourcePath: "/crypt/b.jpg", MediaType: "image"}, "", nil)
	detail := task.Detail()
	if detail.Stats.Processed != 2 || detail.Stats.Remaining != 0 || detail.Stats.Failed != 1 || detail.Stats.Skipped != 1 {
		t.Fatalf("unexpected stats: %+v", detail.Stats)
	}
	if detail.Progress != 100 {
		t.Fatalf("progress = %v, want 100", detail.Progress)
	}
	if got := detail.RecentFailures[0].Error; got != "sign=<redacted>&x=1" {
		t.Fatalf("failure was not redacted: %q", got)
	}
}

type assertError string

func (e assertError) Error() string { return string(e) }
