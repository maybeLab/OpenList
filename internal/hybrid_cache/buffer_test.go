package hybrid_cache_test

import (
	"errors"
	"io"
	"sync"
	"testing"

	"github.com/OpenListTeam/OpenList/v4/internal/hybrid_cache"
)

func TestBufferStore(t *testing.T) {
	type args struct {
		p   []byte
		off int64
	}
	bs := &hybrid_cache.BufferStore{}
	bs.Append([]byte("github.com"))
	bs.Append([]byte("/OpenList"))
	bs.Append([]byte("Team/?"))
	b := []byte("OpenList")
	off := bs.Size() - 1
	_ = bs.GrowTo(off + int64(len(b)))
	_, _ = bs.WriteAt(b, off)

	tests := []struct {
		name  string
		b     *hybrid_cache.BufferStore
		args  args
		check func(a args, n int, err error) error
	}{
		{
			name: "readAt len 10 offset 0",
			b:    bs,
			args: args{
				p:   make([]byte, 10),
				off: 0,
			},
			check: func(a args, n int, err error) error {
				if n != len(a.p) {
					return errors.New("read length not match")
				}
				if string(a.p) != "github.com" {
					return errors.New("read content not match")
				}
				if err != nil {
					return err
				}
				return nil
			},
		},
		{
			name: "readAt len 12 offset 11",
			b:    bs,
			args: args{
				p:   make([]byte, 12),
				off: 11,
			},
			check: func(a args, n int, err error) error {
				if n != len(a.p) {
					return errors.New("read length not match")
				}
				if string(a.p) != "OpenListTeam" {
					return errors.New("read content not match")
				}
				if err != nil {
					return err
				}
				return nil
			},
		},
		{
			name: "readAt len 50 offset 24",
			b:    bs,
			args: args{
				p:   make([]byte, 50),
				off: 24,
			},
			check: func(a args, n int, err error) error {
				if n != int(bs.Size()-a.off) {
					return errors.New("read length not match")
				}
				if string(a.p[:n]) != "OpenList" {
					return errors.New("read content not match")
				}
				if err != io.EOF {
					return errors.New("expect eof")
				}
				return nil
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.b.ReadAt(tt.args.p, tt.args.off)
			if err := tt.check(tt.args, got, err); err != nil {
				t.Errorf("BufferStore.ReadAt() error = %v", err)
			}
		})
	}
}

// Expanding the block table must not race with active download workers.
func TestBufferStoreConcurrentGrowth(t *testing.T) {
	bs := &hybrid_cache.BufferStore{}
	defer bs.Close()
	bs.Append(make([]byte, 4))
	start := make(chan struct{})
	var wg sync.WaitGroup
	for worker := range 4 {
		wg.Go(func() {
			<-start
			for range 200 {
				data := []byte{byte(worker)}
				if _, err := bs.WriteAt(data, int64(worker)); err != nil {
					t.Error(err)
					return
				}
				got := make([]byte, 1)
				if _, err := bs.ReadAt(got, int64(worker)); err != nil {
					t.Error(err)
					return
				}
				if got[0] != data[0] {
					t.Errorf("worker %d: got %v, want %v", worker, got, data)
					return
				}
			}
		})
	}
	wg.Go(func() {
		<-start
		for size := int64(5); size <= 204; size++ {
			if err := bs.GrowTo(size); err != nil {
				t.Error(err)
				return
			}
		}
	})
	close(start)
	wg.Wait()
	if got := bs.Size(); got != 204 {
		t.Fatalf("size = %d, want 204", got)
	}
}
