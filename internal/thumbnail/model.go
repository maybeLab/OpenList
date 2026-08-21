package thumbnail

import (
	"fmt"
	"strings"
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/conf"
	"github.com/OpenListTeam/OpenList/v4/internal/setting"
	"github.com/OpenListTeam/OpenList/v4/pkg/utils"
)

const (
	ModeMissing  = "missing"
	ModeForce    = "force"
	ModeScanOnly = "scan_only"

	TargetDirectory = "directory"
	TargetFile      = "file"

	ResultRunning             = "running"
	ResultCompleted           = "completed"
	ResultCompletedWithErrors = "completed_with_errors"
	ResultCanceled            = "canceled"
	ResultFailed              = "failed"

	perTaskWorkers = 2
	maxOutputSize  = int64(20 * utils.MB)
	failureLimit   = 30
)

type Options struct {
	RootPath      string  `json:"root_path"`
	TargetType    string  `json:"target_type"`
	Recursive     bool    `json:"recursive"`
	IncludeImages bool    `json:"include_images"`
	IncludeVideos bool    `json:"include_videos"`
	Mode          string  `json:"mode"`
	CleanupOrphan bool    `json:"cleanup_orphan"`
	Width         int     `json:"width"`
	Quality       int     `json:"quality"`
	VideoSecond   float64 `json:"video_second"`
}

type CreateRequest struct {
	RootPath      string   `json:"root_path" binding:"required"`
	Recursive     *bool    `json:"recursive"`
	IncludeImages *bool    `json:"include_images"`
	IncludeVideos *bool    `json:"include_videos"`
	Mode          string   `json:"mode"`
	CleanupOrphan bool     `json:"cleanup_orphan"`
	Width         int      `json:"width"`
	Quality       int      `json:"quality"`
	VideoSecond   *float64 `json:"video_second"`
}

func ResolveOptions(req CreateRequest) (Options, error) {
	recursive, images, videos := true, true, true
	if req.Recursive != nil {
		recursive = *req.Recursive
	}
	if req.IncludeImages != nil {
		images = *req.IncludeImages
	}
	if req.IncludeVideos != nil {
		videos = *req.IncludeVideos
	}
	mode := req.Mode
	if mode == "" {
		mode = ModeMissing
	}
	width := req.Width
	if width == 0 {
		width = setting.GetInt(conf.ThumbnailDefaultWidth, 480)
	}
	quality := req.Quality
	if quality == 0 {
		quality = setting.GetInt(conf.ThumbnailDefaultQuality, 75)
	}
	second := setting.GetFloat(conf.ThumbnailVideoSecond, 3)
	if req.VideoSecond != nil {
		second = *req.VideoSecond
	}
	if !strings.HasPrefix(req.RootPath, "/") || hasParentSegment(req.RootPath) {
		return Options{}, fmt.Errorf("root_path must be an absolute path without parent traversal")
	}
	if mode != ModeMissing && mode != ModeForce && mode != ModeScanOnly {
		return Options{}, fmt.Errorf("invalid mode %q", mode)
	}
	if !images && !videos {
		return Options{}, fmt.Errorf("at least one media type must be selected")
	}
	if width < 64 || width > 1920 {
		return Options{}, fmt.Errorf("width must be between 64 and 1920")
	}
	if quality < 1 || quality > 100 {
		return Options{}, fmt.Errorf("quality must be between 1 and 100")
	}
	if second < 0 {
		return Options{}, fmt.Errorf("video_second cannot be negative")
	}
	return Options{
		RootPath:      utils.FixAndCleanPath(req.RootPath),
		TargetType:    TargetDirectory,
		Recursive:     recursive,
		IncludeImages: images,
		IncludeVideos: videos,
		Mode:          mode,
		CleanupOrphan: req.CleanupOrphan && mode != ModeScanOnly,
		Width:         width,
		Quality:       quality,
		VideoSecond:   second,
	}, nil
}

func hasParentSegment(p string) bool {
	for _, part := range strings.Split(strings.ReplaceAll(p, "\\", "/"), "/") {
		if part == ".." {
			return true
		}
	}
	return false
}

type Stats struct {
	Phase            string  `json:"phase"`
	Result           string  `json:"result"`
	ScannedDirs      int64   `json:"scanned_dirs"`
	ScannedFiles     int64   `json:"scanned_files"`
	TotalMedia       int64   `json:"total_media"`
	ExistingValid    int64   `json:"existing_valid"`
	SkippedMounts    int64   `json:"skipped_mounts"`
	TotalCandidates  int64   `json:"total_candidates"`
	Remaining        int64   `json:"remaining"`
	Processed        int64   `json:"processed"`
	Generated        int64   `json:"generated"`
	Skipped          int64   `json:"skipped"`
	Failed           int64   `json:"failed"`
	OrphansRemoved   int64   `json:"orphans_removed"`
	RunningWorkers   int     `json:"running_workers"`
	CurrentPath      string  `json:"current_path"`
	ItemsPerMinute   float64 `json:"items_per_minute"`
	EstimatedSeconds int64   `json:"estimated_seconds"`
}

type Failure struct {
	Path      string    `json:"path"`
	MediaType string    `json:"media_type"`
	Stage     string    `json:"stage"`
	Error     string    `json:"error"`
	Time      time.Time `json:"time"`
}

type Detail struct {
	ID                string    `json:"id"`
	Options           Options   `json:"options"`
	Progress          float64   `json:"progress"`
	Stats             Stats     `json:"stats"`
	ActivePaths       []string  `json:"active_paths"`
	RecentFailures    []Failure `json:"recent_failures"`
	FailuresTruncated bool      `json:"failures_truncated"`
}

type Candidate struct {
	SourcePath    string
	ThumbnailPath string
	MediaType     string
	Size          int64
	ModTime       time.Time
}

type ScanResult struct {
	Candidates  []Candidate
	Directories []string
}

type CapabilityItem struct {
	Available bool   `json:"available"`
	Version   string `json:"version,omitempty"`
	Error     string `json:"error,omitempty"`
}

type Capabilities struct {
	Ready        bool           `json:"ready"`
	FFmpeg       CapabilityItem `json:"ffmpeg"`
	FFprobe      CapabilityItem `json:"ffprobe"`
	WebPEncoder  bool           `json:"webp_encoder"`
	TempDir      string         `json:"temp_dir"`
	TempWritable bool           `json:"temp_writable"`
	TempFree     uint64         `json:"temp_free_bytes"`
	TempSpace    CapabilityItem `json:"temp_space"`
	Loopback     CapabilityItem `json:"loopback"`
}

type ErrorKind int

const (
	ErrorInvalid ErrorKind = iota + 1
	ErrorConflict
	ErrorCapability
)

type KindError struct {
	Kind ErrorKind
	Err  error
}

func (e *KindError) Error() string { return e.Err.Error() }
func (e *KindError) Unwrap() error { return e.Err }
