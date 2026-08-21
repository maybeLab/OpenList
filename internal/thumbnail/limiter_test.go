package thumbnail

import (
	"context"
	"testing"
	"time"
)

func TestDynamicLimiterZeroOnlyBlocksNewWork(t *testing.T) {
	limiter := NewDynamicLimiter(1)
	if err := limiter.Acquire(context.Background()); err != nil {
		t.Fatalf("first Acquire() error = %v", err)
	}
	limiter.SetLimit(0)
	if limiter.Active() != 1 {
		t.Fatalf("active = %d, want existing worker to continue", limiter.Active())
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if err := limiter.Acquire(ctx); err == nil {
		t.Fatal("Acquire() unexpectedly succeeded with a zero limit")
	}
	limiter.Release()
	if limiter.Active() != 0 {
		t.Fatalf("active = %d after release, want 0", limiter.Active())
	}
}

func TestDynamicLimiterIncreaseWakesWaiter(t *testing.T) {
	limiter := NewDynamicLimiter(0)
	done := make(chan error, 1)
	go func() { done <- limiter.Acquire(context.Background()) }()

	limiter.SetLimit(1)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Acquire() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("limit increase did not wake waiter")
	}
	limiter.Release()
}
