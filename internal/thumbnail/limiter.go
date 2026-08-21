package thumbnail

import (
	"context"
	"sync"
)

type DynamicLimiter struct {
	mu     sync.Mutex
	limit  int
	active int
	notify chan struct{}
}

func NewDynamicLimiter(limit int) *DynamicLimiter {
	return &DynamicLimiter{limit: max(0, limit), notify: make(chan struct{})}
}

func (l *DynamicLimiter) signalLocked() {
	close(l.notify)
	l.notify = make(chan struct{})
}

func (l *DynamicLimiter) SetLimit(limit int) {
	l.mu.Lock()
	l.limit = max(0, limit)
	l.signalLocked()
	l.mu.Unlock()
}

func (l *DynamicLimiter) Acquire(ctx context.Context) error {
	for {
		l.mu.Lock()
		if l.limit > 0 && l.active < l.limit {
			l.active++
			l.mu.Unlock()
			return nil
		}
		notify := l.notify
		l.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-notify:
		}
	}
}

func (l *DynamicLimiter) Release() {
	l.mu.Lock()
	if l.active > 0 {
		l.active--
	}
	l.signalLocked()
	l.mu.Unlock()
}

func (l *DynamicLimiter) Active() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.active
}
