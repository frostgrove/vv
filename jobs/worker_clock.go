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
	return clock.now()
}

func (clock *workerClock) now() (time.Time, error) {
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

func (clock *workerClock) startTimer(duration time.Duration) (time.Time, time.Time, *workerTimer, error) {
	if clock == nil || nilInterface(clock.source) || duration <= 0 {
		return time.Time{}, time.Time{}, nil, ErrInvalid
	}

	clock.mu.Lock()
	defer clock.mu.Unlock()

	startedAt, err := clock.now()
	if err != nil {
		return time.Time{}, time.Time{}, nil, err
	}
	deadline, err := requiredTime(startedAt.Add(duration), "worker operation deadline")
	if err != nil {
		return time.Time{}, time.Time{}, nil, ErrInvalid
	}
	inner, channel, err := callWorkerClockTimer(clock.source, deadline)
	if err != nil {
		stopWorkerTimer(inner)
		return time.Time{}, time.Time{}, nil, err
	}
	return startedAt, deadline, &workerTimer{clock: clock, inner: inner, channel: channel}, nil
}

func (clock *workerClock) NewTimer(duration time.Duration) (*workerTimer, error) {
	_, _, timer, err := clock.startTimer(duration)
	return timer, err
}

func (clock *workerClock) stopTimerChecked(timer Timer) (bool, bool) {
	if clock == nil {
		return false, false
	}
	clock.mu.Lock()
	defer clock.mu.Unlock()
	return stopWorkerTimerChecked(timer)
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

func callWorkerClockTimer(source Clock, deadline time.Time) (inner Timer, channel <-chan time.Time, err error) {
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
	inner = source.NewTimerAt(deadline)
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
	valid    bool
}

func (timer *workerTimer) C() <-chan time.Time {
	if timer == nil {
		return nil
	}
	return timer.channel
}

func (timer *workerTimer) Stop() bool {
	stopped, _ := timer.stop()
	return stopped
}

func (timer *workerTimer) stop() (bool, bool) {
	if timer == nil {
		return false, false
	}
	timer.stopOnce.Do(func() {
		timer.stopped, timer.valid = timer.clock.stopTimerChecked(timer.inner)
	})
	return timer.stopped, timer.valid
}

func stopWorkerTimer(timer Timer) (stopped bool) {
	stopped, _ = stopWorkerTimerChecked(timer)
	return stopped
}

func stopWorkerTimerChecked(timer Timer) (stopped bool, valid bool) {
	if nilInterface(timer) {
		return false, false
	}
	valid = true
	defer func() {
		if recover() != nil {
			stopped = false
			valid = false
		}
	}()
	stopped = timer.Stop()
	return stopped, valid
}
