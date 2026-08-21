package thumbnail

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/conf"
	"github.com/OpenListTeam/OpenList/v4/internal/setting"
	"github.com/shirou/gopsutil/v4/disk"
	"golang.org/x/image/webp"
)

var FFmpegLimiter = NewDynamicLimiter(2)

type limitedBuffer struct {
	mu    sync.Mutex
	limit int
	buf   bytes.Buffer
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := len(p)
	remaining := b.limit - b.buf.Len()
	if remaining > 0 {
		_, _ = b.buf.Write(p[:min(len(p), remaining)])
	}
	return n, nil
}

func (b *limitedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return strings.TrimSpace(b.buf.String())
}

func commandVersion(ctx context.Context, name string) CapabilityItem {
	path, err := exec.LookPath(name)
	if err != nil {
		return CapabilityItem{Error: err.Error()}
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, "-version")
	out, err := cmd.Output()
	if err != nil {
		return CapabilityItem{Error: err.Error()}
	}
	line, _, _ := strings.Cut(string(out), "\n")
	return CapabilityItem{Available: true, Version: strings.TrimSpace(line)}
}

func GetCapabilities(ctx context.Context, checkListener bool) Capabilities {
	ffmpeg := commandVersion(ctx, "ffmpeg")
	ffprobe := commandVersion(ctx, "ffprobe")
	webpEncoder := false
	if ffmpeg.Available {
		encoderCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		cmd := exec.CommandContext(encoderCtx, "ffmpeg", "-hide_banner", "-encoders")
		out, err := cmd.CombinedOutput()
		if err == nil {
			webpEncoder = strings.Contains(string(out), "libwebp")
		}
	}
	tempWritable, free, tempErr := tempCapability()
	tempSpace := CapabilityItem{}
	minimumMiB := max(0, setting.GetInt(conf.ThumbnailMinFreeSpace, 512))
	minimum := uint64(minimumMiB) * 1024 * 1024
	switch {
	case tempErr != nil:
		tempSpace.Error = tempErr.Error()
	case free < minimum:
		tempSpace.Error = fmt.Sprintf("temporary free space is below %d MiB", minimumMiB)
	default:
		tempSpace.Available = true
		tempSpace.Version = fmt.Sprintf("minimum %d MiB", minimumMiB)
	}
	loopback := CapabilityItem{Available: true}
	if checkListener {
		loopback = checkLoopback(ctx)
	} else if base, err := loopbackBaseURL(); err != nil {
		loopback = CapabilityItem{Error: err.Error()}
	} else {
		loopback.Version = base
	}
	return Capabilities{
		Ready:        ffmpeg.Available && webpEncoder && tempWritable && tempSpace.Available && loopback.Available,
		FFmpeg:       ffmpeg,
		FFprobe:      ffprobe,
		WebPEncoder:  webpEncoder,
		TempDir:      tempRoot(),
		TempWritable: tempWritable,
		TempFree:     free,
		TempSpace:    tempSpace,
		Loopback:     loopback,
	}
}

func tempRoot() string {
	return filepath.Join(conf.Conf.TempDir, "thumbnail")
}

func tempCapability() (bool, uint64, error) {
	root := tempRoot()
	if err := os.MkdirAll(root, 0o700); err != nil {
		return false, 0, fmt.Errorf("create thumbnail temp directory: %w", err)
	}
	f, err := os.CreateTemp(root, ".write-test-")
	if err != nil {
		return false, 0, fmt.Errorf("thumbnail temp directory is not writable: %w", err)
	}
	name := f.Name()
	_ = f.Close()
	_ = os.Remove(name)
	usage, err := disk.Usage(root)
	if err != nil {
		return true, 0, fmt.Errorf("check thumbnail temp free space: %w", err)
	}
	return true, usage.Free, nil
}

func CleanTempRoot() error {
	root := tempRoot()
	if filepath.Base(root) != "thumbnail" {
		return fmt.Errorf("refusing to clean unexpected thumbnail temp path %q", root)
	}
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if err := os.RemoveAll(filepath.Join(root, entry.Name())); err != nil {
			return err
		}
	}
	return nil
}

func checkFreeSpace() error {
	usage, err := disk.Usage(tempRoot())
	if err != nil {
		return fmt.Errorf("check temporary free space: %w", err)
	}
	minimumMiB := max(0, setting.GetInt(conf.ThumbnailMinFreeSpace, 512))
	minimum := uint64(minimumMiB) * 1024 * 1024
	if usage.Free < minimum {
		return fmt.Errorf("temporary free space is below %d MiB", minimumMiB)
	}
	return nil
}

func Generate(ctx context.Context, candidate Candidate, options Options, outputPath string) error {
	if candidate.MediaType == "video" {
		seconds := []float64{options.VideoSecond, 10, 0, 30}
		seen := map[float64]bool{}
		var errs []string
		for _, second := range seconds {
			if seen[second] {
				continue
			}
			seen[second] = true
			if err := runFFmpeg(ctx, candidate.SourcePath, options, outputPath, &second); err == nil {
				return nil
			} else {
				errs = append(errs, fmt.Sprintf("%.3gs: %v", second, err))
			}
		}
		return fmt.Errorf("all video seek attempts failed: %s", strings.Join(errs, "; "))
	}
	return runFFmpeg(ctx, candidate.SourcePath, options, outputPath, nil)
}

func runFFmpeg(parent context.Context, sourcePath string, options Options, outputPath string, seek *float64) error {
	if err := checkFreeSpace(); err != nil {
		return err
	}
	timeoutSeconds := setting.GetInt(conf.ThumbnailImageTimeout, 60)
	if seek != nil {
		timeoutSeconds = setting.GetInt(conf.ThumbnailVideoTimeout, 120)
	}
	ctx, cancel := context.WithTimeout(parent, time.Duration(max(1, timeoutSeconds))*time.Second)
	defer cancel()
	if err := FFmpegLimiter.Acquire(ctx); err != nil {
		return err
	}
	defer FFmpegLimiter.Release()

	sourceURL, err := SourceURL(sourcePath)
	if err != nil {
		return err
	}
	_ = os.Remove(outputPath)
	args := []string{"-hide_banner", "-loglevel", "error", "-y"}
	if seek != nil {
		args = append(args, "-ss", strconv.FormatFloat(*seek, 'f', -1, 64))
	}
	args = append(args,
		"-i", sourceURL,
		"-vf", fmt.Sprintf("scale=%d:-2:force_original_aspect_ratio=decrease", options.Width),
		"-frames:v", "1",
		"-c:v", "libwebp",
		"-quality", strconv.Itoa(options.Quality),
		"-f", "webp",
		outputPath,
	)
	stderr := &limitedBuffer{limit: 8 * 1024}
	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	cmd.Stderr = stderr
	cmd.Env = commandEnvironment()
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if stderr.String() != "" {
			return fmt.Errorf("ffmpeg: %s", stderr.String())
		}
		return fmt.Errorf("ffmpeg: %w", err)
	}
	return ValidateWebP(outputPath)
}

func commandEnvironment() []string {
	env := os.Environ()
	filtered := env[:0]
	for _, entry := range env {
		key, _, _ := strings.Cut(entry, "=")
		if key == "NO_PROXY" || key == "no_proxy" {
			continue
		}
		filtered = append(filtered, entry)
	}
	direct := "127.0.0.1,localhost,::1,openlist-thumbnail-dev"
	return append(filtered, "NO_PROXY="+direct, "no_proxy="+direct)
}

func ValidateWebP(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.Size() == 0 {
		return fmt.Errorf("generated WebP is empty")
	}
	if info.Size() > maxOutputSize {
		return fmt.Errorf("generated WebP exceeds 20 MiB")
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	magic := make([]byte, 12)
	if _, err := f.Read(magic); err != nil {
		return fmt.Errorf("read WebP header: %w", err)
	}
	if string(magic[:4]) != "RIFF" || string(magic[8:12]) != "WEBP" {
		return fmt.Errorf("invalid WebP magic")
	}
	if _, err := f.Seek(0, 0); err != nil {
		return err
	}
	config, err := webp.DecodeConfig(f)
	if err != nil {
		return fmt.Errorf("decode WebP: %w", err)
	}
	if config.Width <= 0 || config.Height <= 0 {
		return fmt.Errorf("invalid WebP dimensions")
	}
	return nil
}
