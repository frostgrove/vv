package jobs

import (
	"sync"
	"time"
)

type workerClock struct {
	source Clock

	mu          sync.Mutex
	last        time.Time
	started     bool
	regressions uint64
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

// A wall clock goes backwards for ordinary reasons: an NTP step, a VM resumed
// from a snapshot, an operator correcting a drifting host. Treating that as
// ErrInvalid made it a fatal, unrestartable runtime failure — the pool stopped
// and stayed stopped over a time correction, which is a far worse outcome than
// the skew that caused it.
//
// The clock is instead held non-decreasing: a regression answers the last time
// observed, so nothing computed from it moves backwards, and the fact is reported
// through Regressions() rather than by killing the process. Stamps stay wall-clock
// because they are compared across processes and against the database.
func (clock *workerClock) now() (time.Time, error) {
	now, err := callWorkerClockNow(clock.source)
	if err != nil {
		return time.Time{}, err
	}
	if clock.started && now.Before(clock.last) {
		clock.regressions++
		return clock.last, nil
	}
	clock.last = now
	clock.started = true
	return now, nil
}

// How many times this clock has been read going backwards. A non-zero count is a
// host whose time is being corrected under a running worker, which is worth an
// operator's attention and is not worth stopping for.
func (clock *workerClock) Regressions() uint64 {
	if clock == nil {
		return 0
	}
	clock.mu.Lock()
	defer clock.mu.Unlock()
	return clock.regressions
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
	timer, err := clock.newTimerAt(deadline)
	if err != nil {
		return time.Time{}, time.Time{}, nil, err
	}
	return startedAt, deadline, timer, nil
}

func (clock *workerClock) NewTimer(duration time.Duration) (*workerTimer, error) {
	_, _, timer, err := clock.startTimer(duration)
	return timer, err
}

func (clock *workerClock) startDeadline(deadline time.Time) (time.Time, *workerTimer, bool, error) {
	if clock == nil || nilInterface(clock.source) {
		return time.Time{}, nil, false, ErrInvalid
	}
	deadline, err := requiredTime(deadline, "worker deadline")
	if err != nil {
		return time.Time{}, nil, false, ErrInvalid
	}

	clock.mu.Lock()
	defer clock.mu.Unlock()

	now, err := clock.now()
	if err != nil {
		return time.Time{}, nil, false, err
	}
	if !now.Before(deadline) {
		return now, nil, false, nil
	}
	timer, err := clock.newTimerAt(deadline)
	if err != nil {
		return time.Time{}, nil, false, err
	}
	return now, timer, true, nil
}

func (clock *workerClock) newTimerAt(deadline time.Time) (*workerTimer, error) {
	inner, channel, err := callWorkerClockTimer(clock.source, deadline)
	if err != nil {
		stopWorkerTimer(inner)
		return nil, err
	}
	return &workerTimer{clock: clock, inner: inner, channel: channel}, nil
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
	completed := false
	defer func() {
		_ = recover()
		if !completed {
			now = time.Time{}
			err = ErrInvalid
		}
	}()
	now, err = requiredTime(source.Now(), "worker clock time")
	completed = true
	if err != nil {
		return time.Time{}, ErrInvalid
	}
	return now, nil
}

func callWorkerClockTimer(source Clock, deadline time.Time) (inner Timer, channel <-chan time.Time, err error) {
	completed := false
	defer func() {
		_ = recover()
		if !completed {
			stopWorkerTimer(inner)
			inner = nil
			channel = nil
			err = ErrInvalid
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
	completed := false
	defer func() {
		_ = recover()
		if !completed {
			stopped = false
			valid = false
		}
	}()
	stopped = timer.Stop()
	valid = true
	completed = true
	return stopped, valid
}
