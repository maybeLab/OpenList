package smb

import (
	"bytes"
	"context"
	"errors"
	"io"
	"sync/atomic"
	"testing"
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/internal/stream"
	"github.com/OpenListTeam/OpenList/v4/pkg/http_range"
	"github.com/OpenListTeam/OpenList/v4/pkg/utils"
	"golang.org/x/time/rate"
)

func readSMBRange(t *testing.T, rr *model.FileRangeReader, ctx context.Context, r http_range.Range) io.ReadCloser {
	t.Helper()
	reader, err := rr.RangeRead(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reader.Close() })
	return reader
}

func TestSMBRangeCancellationIsolation(t *testing.T) {
	const content = "0123456789abcdefghijklmnopqrstuvwxyz"
	rr := newSMBRangeReader(int64(len(content)), bytes.NewReader([]byte(content)))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	first := readSMBRange(t, rr, ctx, http_range.Range{Start: 0, Length: 8})
	second := readSMBRange(t, rr, context.Background(), http_range.Range{Start: 4, Length: 12})
	buf := make([]byte, 2)
	if _, err := io.ReadFull(first, buf); err != nil {
		t.Fatal(err)
	}
	cancel()
	if n, err := first.Read(buf); n != 0 || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled read = %d, %v", n, err)
	}
	if n, err := first.(model.File).ReadAt(buf, 0); n != 0 || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled ReadAt = %d, %v", n, err)
	}
	third := readSMBRange(t, rr, context.Background(), http_range.Range{Start: 8, Length: -1})
	// Read overlapping ranges concurrently after canceling the first request.
	type result struct {
		got  []byte
		err  error
		want string
	}
	results := make(chan result, 2)
	for _, tc := range []struct {
		r    io.Reader
		want string
	}{{second, content[4:16]}, {third, content[8:]}} {
		go func() {
			got, err := io.ReadAll(tc.r)
			results <- result{got, err, tc.want}
		}()
	}
	for range 2 {
		res := <-results
		if res.err != nil || string(res.got) != res.want {
			t.Errorf("read = %q, %v; want %q", res.got, res.err, res.want)
		}
	}
	alreadyCanceled := readSMBRange(t, rr, ctx, http_range.Range{Length: 4})
	if n, err := alreadyCanceled.Read(buf); n != 0 || !errors.Is(err, context.Canceled) {
		t.Fatalf("already canceled read = %d, %v", n, err)
	}
}

func TestSMBRangeFileOperations(t *testing.T) {
	const content = "0123456789abcdef"
	rr := newSMBRangeReader(int64(len(content)), bytes.NewReader([]byte(content)))
	for _, tc := range []struct {
		name string
		r    http_range.Range
		want string
	}{
		{"bounded", http_range.Range{Start: 3, Length: 5}, content[3:8]},
		{"to end", http_range.Range{Start: 10, Length: -1}, content[10:]},
		{"clamped", http_range.Range{Start: 10, Length: 100}, content[10:]},
		{"at end", http_range.Range{Start: int64(len(content)), Length: -1}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := readSMBRange(t, rr, context.Background(), tc.r)
			file, ok := r.(model.File)
			if !ok {
				t.Fatal("range reader must support Read, ReadAt and Seek")
			}
			got, err := io.ReadAll(r)
			if err != nil || string(got) != tc.want {
				t.Fatalf("read = %q, %v; want %q", got, err, tc.want)
			}
			if _, err := file.Seek(0, io.SeekStart); err != nil {
				t.Fatal(err)
			}
			if len(tc.want) > 0 {
				got = make([]byte, len(tc.want))
				if n, err := file.ReadAt(got, 0); err != nil || n != len(got) || string(got) != tc.want {
					t.Fatalf("ReadAt = %q (%d), %v", got, n, err)
				}
			}
			if len(tc.want) > 1 {
				if _, err := file.Seek(1, io.SeekStart); err != nil {
					t.Fatal(err)
				}
				got, err = io.ReadAll(r)
				if err != nil || string(got) != tc.want[1:] {
					t.Fatalf("read after Seek = %q, %v", got, err)
				}
			}
		})
	}
}

type smbBlockingLimiter struct {
	*rate.Limiter
	entered chan int
}

func (l *smbBlockingLimiter) WaitN(ctx context.Context, n int) error {
	l.entered <- n
	<-ctx.Done()
	return ctx.Err()
}

func TestSMBRangeCancelDuringRateLimit(t *testing.T) {
	limiter := &smbBlockingLimiter{Limiter: rate.NewLimiter(1, 16), entered: make(chan int, 1)}
	previous := stream.ServerDownloadLimit
	stream.ServerDownloadLimit = limiter
	t.Cleanup(func() { stream.ServerDownloadLimit = previous })
	rr := newSMBRangeReader(16, bytes.NewReader([]byte("0123456789abcdef")))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := readSMBRange(t, rr, ctx, http_range.Range{Length: 8})
	done := make(chan error, 1)
	go func() {
		_, err := r.Read(make([]byte, 4))
		done <- err
	}()
	select {
	case n := <-limiter.entered:
		if n != 4 {
			t.Errorf("limiter charged %d bytes, want 4", n)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("read did not reach limiter")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("read error = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancel did not unblock limiter")
	}
}

type smbTrackedFile struct {
	*bytes.Reader
	closes atomic.Int32
}

func (f *smbTrackedFile) Close() error {
	f.closes.Add(1)
	return nil
}

func TestSMBRangeLinkLifetime(t *testing.T) {
	file := &smbTrackedFile{Reader: bytes.NewReader([]byte("0123456789"))}
	d := &SMB{activeOps: 1}
	var releases atomic.Int32
	link := &model.Link{
		RangeReader: newSMBRangeReader(10, file),
		SyncClosers: utils.NewSyncClosers(file, utils.CloseFunc(func() error {
			releases.Add(1)
			d.releaseConn()
			return nil
		})),
		RequireReference: true,
	}
	t.Cleanup(func() { _ = link.Close() })
	if !link.AcquireReference() || !link.AcquireReference() {
		t.Fatal("could not acquire link references")
	}
	r, err := link.RangeReader.RangeRead(context.Background(), http_range.Range{Length: 4})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if file.closes.Load() != 0 || releases.Load() != 0 {
		t.Fatal("closing a range released shared resources")
	}
	if err := link.Close(); err != nil {
		t.Fatal(err)
	}
	if file.closes.Load() != 0 || d.activeOps != 1 {
		t.Fatal("closing one link reference released shared resources")
	}
	if err := link.Close(); err != nil {
		t.Fatal(err)
	}
	if err := link.Close(); err != nil {
		t.Fatal(err)
	}
	if file.closes.Load() != 1 || releases.Load() != 1 || d.activeOps != 0 || link.AcquireReference() {
		t.Fatalf("final cleanup: closes=%d, releases=%d, activeOps=%d", file.closes.Load(), releases.Load(), d.activeOps)
	}
}
