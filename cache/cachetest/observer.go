package cachetest

import (
	"context"
	"sync"

	"github.com/frostgrove/vv/cache"
)

type Observer struct {
	mu     sync.Mutex
	events []cache.Event
	wake   chan struct{}
}

func NewObserver() *Observer {
	return &Observer{wake: make(chan struct{})}
}

func (observer *Observer) Observe(_ context.Context, event cache.Event) {
	if observer == nil {
		return
	}
	observer.mu.Lock()
	observer.events = append(observer.events, event)
	if observer.wake != nil {
		close(observer.wake)
	}
	observer.wake = make(chan struct{})
	observer.mu.Unlock()
}

func (observer *Observer) Events() []cache.Event {
	if observer == nil {
		return nil
	}
	observer.mu.Lock()
	defer observer.mu.Unlock()
	return append([]cache.Event(nil), observer.events...)
}

func (observer *Observer) Reset() {
	if observer == nil {
		return
	}
	observer.mu.Lock()
	observer.events = nil
	if observer.wake != nil {
		close(observer.wake)
	}
	observer.wake = make(chan struct{})
	observer.mu.Unlock()
}

func (observer *Observer) Wait(ctx context.Context, count int) ([]cache.Event, error) {
	if observer == nil || ctx == nil || count < 0 {
		return nil, cache.ErrInvalid
	}
	for {
		observer.mu.Lock()
		if len(observer.events) >= count {
			events := append([]cache.Event(nil), observer.events...)
			observer.mu.Unlock()
			return events, nil
		}
		if observer.wake == nil {
			observer.wake = make(chan struct{})
		}
		wake := observer.wake
		observer.mu.Unlock()
		select {
		case <-wake:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}
