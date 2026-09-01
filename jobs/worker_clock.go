package jobs

import (
	"sync"
	"time"
)

type workerClock struct {
	source Clock

	mu      sync.Mutex
	last    time.Time
	started bool
}

func newWorkerClock(source Clock) (*workerClock, error) {
	if nilInterface(source) {
		return nil, ErrInvalid
	}
	return &workerClock{source: source}, nil
}

func (clock *workerClock) Now() (time.Time, error) {
	if clock == nil || nilInterface(clock.source) {
		return time.Time{}, ErrInvalid
	}

	clock.mu.Lock()
	defer clock.mu.Unlock()

	now, err := callWorkerClockNow(clock.source)
	if err != nil {
		return time.Time{}, err
	}
	if clock.started && now.Before(clock.last) {
		return time.Time{}, ErrInvalid
	}
	clock.last = now
	clock.started = true
	return now, nil
}

func (clock *workerClock) NewTimer(duration time.Duration) (*workerTimer, error) {
	if clock == nil || nilInterface(clock.source) || duration <= 0 {
		return nil, ErrInvalid
	}

	inner, channel, err := clock.newTimer(duration)
	if err != nil {
		clock.stopTimer(inner)
		return nil, err
	}
	return &workerTimer{clock: clock, inner: inner, channel: channel}, nil
}

func (clock *workerClock) newTimer(duration time.Duration) (Timer, <-chan time.Time, error) {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	return callWorkerClockTimer(clock.source, duration)
}

func (clock *workerClock) stopTimer(timer Timer) bool {
	if clock == nil {
		return false
	}
	clock.mu.Lock()
	defer clock.mu.Unlock()
	return stopWorkerTimer(timer)
}

func callWorkerClockNow(source Clock) (now time.Time, err error) {
	defer func() {
		if recover() != nil {
			now = time.Time{}
			err = ErrInvalid
		}
	}()
	now, err = requiredTime(source.Now(), "worker clock time")
	if err != nil {
		return time.Time{}, ErrInvalid
	}
	return now, nil
}

func callWorkerClockTimer(source Clock, duration time.Duration) (inner Timer, channel <-chan time.Time, err error) {
	completed := false
	defer func() {
		if recovered := recover(); recovered != nil {
			channel = nil
			err = ErrInvalid
			return
		}
		if !completed {
			stopWorkerTimer(inner)
		}
	}()
	inner = source.NewTimer(duration)
	if nilInterface(inner) {
		completed = true
		return nil, nil, ErrInvalid
	}
	channel = inner.C()
	if channel == nil {
		completed = true
		return inner, nil, ErrInvalid
	}
	completed = true
	return inner, channel, nil
}

type workerTimer struct {
	clock   *workerClock
	inner   Timer
	channel <-chan time.Time

	stopOnce sync.Once
	stopped  bool
}

func (timer *workerTimer) C() <-chan time.Time {
	if timer == nil {
		return nil
	}
	return timer.channel
}

func (timer *workerTimer) Stop() bool {
	if timer == nil {
		return false
	}
	timer.stopOnce.Do(func() {
		timer.stopped = timer.clock.stopTimer(timer.inner)
	})
	return timer.stopped
}

func stopWorkerTimer(timer Timer) (stopped bool) {
	if nilInterface(timer) {
		return false
	}
	defer func() {
		if recover() != nil {
			stopped = false
		}
	}()
	return timer.Stop()
}
