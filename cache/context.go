package cache

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

type timedContext struct {
	parent   context.Context
	deadline time.Time
	done     chan struct{}
	exited   chan struct{}
	once     sync.Once
	mu       sync.Mutex
	err      error
}

type valueBlindContext struct {
	context.Context
}

func (valueBlindContext) Value(any) any { return nil }

func newTimedContext(parent context.Context, clock Clock, duration time.Duration, watchers *atomic.Int64) (context.Context, func(), error) {
	now, err := runtimeNow(clock)
	if err != nil {
		return nil, nil, err
	}
	deadline, ok := addTime(now, duration)
	if !ok {
		return nil, nil, fmt.Errorf("%w: context deadline overflows", ErrInvalid)
	}
	if parentDeadline, ok := parent.Deadline(); ok && parentDeadline.Before(deadline) {
		deadline = parentDeadline
	}
	ctx := &timedContext{parent: parent, deadline: deadline, done: make(chan struct{}), exited: make(chan struct{})}
	timer, err := runtimeTimer(clock, duration)
	if err != nil {
		return nil, nil, err
	}
	if parentErr := parent.Err(); parentErr != nil {
		ctx.finish(parentErr)
		timer.Stop()
		close(ctx.exited)
		return ctx, func() {
			ctx.finish(context.Canceled)
			<-ctx.exited
		}, nil
	}
	if watchers != nil {
		watchers.Add(1)
	}
	go func() {
		select {
		case <-parent.Done():
			ctx.finish(parent.Err())
		case <-timer.C():
			ctx.finish(context.DeadlineExceeded)
		case <-ctx.done:
		}
		timer.Stop()
		if watchers != nil {
			watchers.Add(-1)
		}
		close(ctx.exited)
	}()
	return ctx, func() {
		ctx.finish(context.Canceled)
		<-ctx.exited
	}, nil
}

func (this *timedContext) Deadline() (time.Time, bool) { return this.deadline, true }
func (this *timedContext) Done() <-chan struct{}       { return this.done }
func (this *timedContext) Value(key any) any           { return this.parent.Value(key) }

func (this *timedContext) Err() error {
	this.mu.Lock()
	defer this.mu.Unlock()
	return this.err
}

func (this *timedContext) finish(err error) {
	this.once.Do(func() {
		this.mu.Lock()
		this.err = err
		close(this.done)
		this.mu.Unlock()
	})
}

func (this *cacheCore[K, V]) backendContext(parent context.Context) (context.Context, func(), error) {
	return newTimedContext(parent, this.runtime.Clock, this.runtime.BackendTimeout, &this.timedWatchers)
}

func (this *cacheCore[K, V]) cleanupContext() (context.Context, func(), error) {
	return newTimedContext(context.Background(), this.runtime.Clock, this.runtime.CleanupTimeout, &this.timedWatchers)
}

func (this *cacheCore[K, V]) loaderContext(parent context.Context) (context.Context, func(), error) {
	return newTimedContext(parent, this.runtime.Clock, this.runtime.LoaderTimeout, &this.timedWatchers)
}
