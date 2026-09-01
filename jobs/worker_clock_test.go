package jobs

import (
	"errors"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type workerClockDouble struct {
	nowCalls   atomic.Int32
	timerCalls atomic.Int32
	now        func() time.Time
	timer      func(time.Time) Timer
}

func (clock *workerClockDouble) Now() time.Time {
	clock.nowCalls.Add(1)
	return clock.now()
}

func (clock *workerClockDouble) NewTimerAt(deadline time.Time) Timer {
	clock.timerCalls.Add(1)
	return clock.timer(deadline)
}

type workerTimerDouble struct {
	channel   <-chan time.Time
	panicC    bool
	panicStop bool
	stopValue bool
	cCalls    atomic.Int32
	stopCalls atomic.Int32
}

func (timer *workerTimerDouble) C() <-chan time.Time {
	timer.cCalls.Add(1)
	if timer.panicC {
		panic("private timer channel panic")
	}
	return timer.channel
}

func (timer *workerTimerDouble) Stop() bool {
	timer.stopCalls.Add(1)
	if timer.panicStop {
		panic("private timer stop panic")
	}
	return timer.stopValue
}

func TestNewWorkerClockIsPureAndRejectsNilSources(t *testing.T) {
	base := time.Date(2032, 4, 5, 6, 7, 8, 9, time.UTC)
	source := &workerClockDouble{
		now:   func() time.Time { return base },
		timer: func(time.Time) Timer { panic("must not be called") },
	}
	clock, err := newWorkerClock(source)
	if err != nil || clock == nil {
		t.Fatalf("newWorkerClock() = (%v, %v)", clock, err)
	}
	if source.nowCalls.Load() != 0 || source.timerCalls.Load() != 0 {
		t.Fatalf("construction called the source: now=%d timer=%d", source.nowCalls.Load(), source.timerCalls.Load())
	}

	var typedNil *workerClockDouble
	for _, invalid := range []Clock{nil, typedNil} {
		clock, err := newWorkerClock(invalid)
		if clock != nil || err != ErrInvalid {
			t.Fatalf("newWorkerClock(%T) = (%v, %v), want nil and ErrInvalid", invalid, clock, err)
		}
	}
}

func TestWorkerClockNowCanonicalizesUTCAndStripsMonotonicTime(t *testing.T) {
	first := time.Now()
	values := []time.Time{
		first,
		first.Add(time.Second).In(time.FixedZone("source", 5*60*60)),
	}
	var index atomic.Int32
	source := &workerClockDouble{
		now:   func() time.Time { return values[int(index.Add(1))-1] },
		timer: func(time.Time) Timer { panic("must not be called") },
	}
	clock, err := newWorkerClock(source)
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range values {
		got, err := clock.Now()
		want := raw.Round(0).UTC()
		if err != nil || got != want || got.Location() != time.UTC {
			t.Fatalf("Now() = (%v, %v), want %v in UTC", got, err, want)
		}
	}
	if source.nowCalls.Load() != int32(len(values)) {
		t.Fatalf("source Now calls = %d, want %d", source.nowCalls.Load(), len(values))
	}
}

func TestWorkerClockNowNormalizesPanicsAndInvalidTimes(t *testing.T) {
	tests := []struct {
		name string
		now  func() time.Time
	}{
		{name: "panic", now: func() time.Time { panic("private clock panic") }},
		{name: "zero", now: func() time.Time { return time.Time{} }},
		{name: "year below range", now: func() time.Time { return time.Date(0, 1, 1, 0, 0, 0, 0, time.UTC) }},
		{name: "year above range", now: func() time.Time { return time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := &workerClockDouble{now: test.now, timer: func(time.Time) Timer { panic("must not be called") }}
			clock, err := newWorkerClock(source)
			if err != nil {
				t.Fatal(err)
			}
			now, err := clock.Now()
			if !now.IsZero() || err != ErrInvalid {
				t.Fatalf("Now() = (%v, %v), want zero and ErrInvalid", now, err)
			}
		})
	}

	var nilClock *workerClock
	if now, err := nilClock.Now(); !now.IsZero() || err != ErrInvalid {
		t.Fatalf("nil Now() = (%v, %v), want zero and ErrInvalid", now, err)
	}
}

func TestWorkerClockRejectsRegressionWithoutPoisoningItsLastTime(t *testing.T) {
	base := time.Date(2032, 4, 5, 6, 7, 8, 0, time.UTC)
	values := []time.Time{base, base.Add(-time.Second), base.Add(time.Second), base.Add(time.Second)}
	var index atomic.Int32
	source := &workerClockDouble{
		now: func() time.Time {
			return values[int(index.Add(1))-1]
		},
		timer: func(time.Time) Timer { panic("must not be called") },
	}
	clock, err := newWorkerClock(source)
	if err != nil {
		t.Fatal(err)
	}
	first, err := clock.Now()
	if err != nil || first != base {
		t.Fatalf("first Now() = (%v, %v)", first, err)
	}
	if regressed, err := clock.Now(); !regressed.IsZero() || err != ErrInvalid {
		t.Fatalf("regressing Now() = (%v, %v), want zero and ErrInvalid", regressed, err)
	}
	third, err := clock.Now()
	if err != nil || third != base.Add(time.Second) {
		t.Fatalf("Now() after regression = (%v, %v)", third, err)
	}
	equal, err := clock.Now()
	if err != nil || equal != third {
		t.Fatalf("equal Now() = (%v, %v), want %v", equal, err, third)
	}
}

type concurrentWorkerClock struct {
	base     time.Time
	active   atomic.Int32
	maximum  atomic.Int32
	sequence atomic.Int64
}

func (clock *concurrentWorkerClock) Now() time.Time {
	active := clock.active.Add(1)
	for maximum := clock.maximum.Load(); active > maximum && !clock.maximum.CompareAndSwap(maximum, active); maximum = clock.maximum.Load() {
	}
	runtime.Gosched()
	sequence := clock.sequence.Add(1)
	runtime.Gosched()
	clock.active.Add(-1)
	return clock.base.Add(time.Duration(sequence))
}

func (*concurrentWorkerClock) NewTimerAt(time.Time) Timer { panic("must not be called") }

func TestWorkerClockSerializesConcurrentNowCalls(t *testing.T) {
	source := &concurrentWorkerClock{base: time.Date(2032, 4, 5, 6, 7, 8, 0, time.UTC)}
	clock, err := newWorkerClock(source)
	if err != nil {
		t.Fatal(err)
	}
	const calls = 256
	start := make(chan struct{})
	errorsSeen := make(chan error, calls)
	var wait sync.WaitGroup
	wait.Add(calls)
	for range calls {
		go func() {
			defer wait.Done()
			<-start
			_, err := clock.Now()
			errorsSeen <- err
		}()
	}
	close(start)
	wait.Wait()
	close(errorsSeen)
	for err := range errorsSeen {
		if err != nil {
			t.Fatalf("concurrent Now() error = %v", err)
		}
	}
	if source.maximum.Load() != 1 || source.sequence.Load() != calls {
		t.Fatalf("source concurrency/calls = %d/%d, want 1/%d", source.maximum.Load(), source.sequence.Load(), calls)
	}
}

type workerClockCallbackProbe struct {
	active  atomic.Int32
	maximum atomic.Int32
}

func (probe *workerClockCallbackProbe) enter() {
	active := probe.active.Add(1)
	for maximum := probe.maximum.Load(); active > maximum && !probe.maximum.CompareAndSwap(maximum, active); maximum = probe.maximum.Load() {
	}
	runtime.Gosched()
}

func (probe *workerClockCallbackProbe) leave() {
	runtime.Gosched()
	probe.active.Add(-1)
}

type probedWorkerClock struct {
	probe    *workerClockCallbackProbe
	base     time.Time
	sequence atomic.Int64
}

func (clock *probedWorkerClock) Now() time.Time {
	clock.probe.enter()
	defer clock.probe.leave()
	return clock.base.Add(time.Duration(clock.sequence.Add(1)))
}

func (clock *probedWorkerClock) NewTimerAt(time.Time) Timer {
	clock.probe.enter()
	defer clock.probe.leave()
	return &probedWorkerTimer{probe: clock.probe, channel: make(chan time.Time)}
}

type probedWorkerTimer struct {
	probe   *workerClockCallbackProbe
	channel <-chan time.Time
}

func (timer *probedWorkerTimer) C() <-chan time.Time {
	timer.probe.enter()
	defer timer.probe.leave()
	return timer.channel
}

func (timer *probedWorkerTimer) Stop() bool {
	timer.probe.enter()
	defer timer.probe.leave()
	return true
}

func TestWorkerClockSerializesEverySourceCallback(t *testing.T) {
	probe := &workerClockCallbackProbe{}
	source := &probedWorkerClock{probe: probe, base: time.Date(2032, 4, 5, 6, 7, 8, 0, time.UTC)}
	clock, err := newWorkerClock(source)
	if err != nil {
		t.Fatal(err)
	}
	const calls = 128
	start := make(chan struct{})
	errorsSeen := make(chan error, calls)
	var wait sync.WaitGroup
	wait.Add(calls)
	for index := range calls {
		go func() {
			defer wait.Done()
			<-start
			if index%2 == 0 {
				_, err := clock.Now()
				errorsSeen <- err
				return
			}
			timer, err := clock.NewTimer(time.Second)
			if err == nil && !timer.Stop() {
				err = errors.New("timer did not stop")
			}
			errorsSeen <- err
		}()
	}
	close(start)
	wait.Wait()
	close(errorsSeen)
	for err := range errorsSeen {
		if err != nil {
			t.Fatalf("source callback error = %v", err)
		}
	}
	if probe.maximum.Load() != 1 || probe.active.Load() != 0 {
		t.Fatalf("source callback concurrency/active = %d/%d, want 1/0", probe.maximum.Load(), probe.active.Load())
	}
}

func TestWorkerClockNewTimerContainsEveryInvalidSourceResult(t *testing.T) {
	base := time.Date(2032, 4, 5, 6, 7, 8, 0, time.UTC)
	tests := []struct {
		name      string
		makeTimer func() Timer
		panicNew  bool
		wantStop  int32
	}{
		{name: "nil timer", makeTimer: func() Timer { return nil }},
		{name: "typed nil timer", makeTimer: func() Timer { var timer *workerTimerDouble; return timer }},
		{name: "nil channel", makeTimer: func() Timer { return &workerTimerDouble{stopValue: true} }, wantStop: 1},
		{name: "channel panic", makeTimer: func() Timer { return &workerTimerDouble{panicC: true, stopValue: true} }, wantStop: 1},
		{name: "channel and stop panic", makeTimer: func() Timer { return &workerTimerDouble{panicC: true, panicStop: true} }, wantStop: 1},
		{name: "new timer panic", panicNew: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var made *workerTimerDouble
			source := &workerClockDouble{
				now: func() time.Time { return base },
				timer: func(time.Time) Timer {
					if test.panicNew {
						panic("private new timer panic")
					}
					value := test.makeTimer()
					made, _ = value.(*workerTimerDouble)
					return value
				},
			}
			clock, err := newWorkerClock(source)
			if err != nil {
				t.Fatal(err)
			}
			timer, err := clock.NewTimer(time.Second)
			if timer != nil || err != ErrInvalid {
				t.Fatalf("NewTimer() = (%v, %v), want nil and ErrInvalid", timer, err)
			}
			if made != nil && made.stopCalls.Load() != test.wantStop {
				t.Fatalf("invalid timer stop calls = %d, want %d", made.stopCalls.Load(), test.wantStop)
			}
		})
	}
}

func TestWorkerClockStartTimerReleasesEveryInvalidSourceTimer(t *testing.T) {
	base := time.Date(2032, 4, 5, 6, 7, 8, 0, time.UTC)
	tests := []struct {
		name      string
		makeTimer func() Timer
		wantStop  int32
	}{
		{name: "nil timer", makeTimer: func() Timer { return nil }},
		{name: "typed nil timer", makeTimer: func() Timer { var timer *workerTimerDouble; return timer }},
		{name: "nil channel", makeTimer: func() Timer { return &workerTimerDouble{stopValue: true} }, wantStop: 1},
		{name: "channel panic", makeTimer: func() Timer { return &workerTimerDouble{panicC: true, stopValue: true} }, wantStop: 1},
		{name: "channel and stop panic", makeTimer: func() Timer { return &workerTimerDouble{panicC: true, panicStop: true} }, wantStop: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var made *workerTimerDouble
			source := &workerClockDouble{
				now: func() time.Time { return base },
				timer: func(time.Time) Timer {
					value := test.makeTimer()
					made, _ = value.(*workerTimerDouble)
					return value
				},
			}
			clock, err := newWorkerClock(source)
			if err != nil {
				t.Fatal(err)
			}
			startedAt, deadline, timer, err := clock.startTimer(time.Second)
			if !startedAt.IsZero() || !deadline.IsZero() || timer != nil || err != ErrInvalid {
				t.Fatalf("startTimer() = (%v, %v, %v, %v)", startedAt, deadline, timer, err)
			}
			if made != nil && made.stopCalls.Load() != test.wantStop {
				t.Fatalf("invalid timer stop calls = %d, want %d", made.stopCalls.Load(), test.wantStop)
			}
		})
	}
}

type workerGoexitTimer struct{ stopped atomic.Int32 }

func (*workerGoexitTimer) C() <-chan time.Time { runtime.Goexit(); return nil }
func (timer *workerGoexitTimer) Stop() bool {
	timer.stopped.Add(1)
	return true
}

func TestWorkerClockNewTimerGoexitReleasesSerializationAndTimer(t *testing.T) {
	inner := &workerGoexitTimer{}
	base := time.Date(2032, 4, 5, 6, 7, 8, 0, time.UTC)
	source := &workerClockDouble{
		now:   func() time.Time { return base },
		timer: func(time.Time) Timer { return inner },
	}
	clock, err := newWorkerClock(source)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = clock.NewTimer(time.Second)
	}()
	<-done
	if inner.stopped.Load() != 1 {
		t.Fatalf("Goexit timer stop calls = %d, want 1", inner.stopped.Load())
	}
	if now, err := clock.Now(); err != nil || now != base {
		t.Fatalf("Now() after Goexit = (%v, %v)", now, err)
	}
}

func TestWorkerClockNewTimerRejectsNonPositiveDurationsBeforeCallingSource(t *testing.T) {
	source := &workerClockDouble{
		now:   func() time.Time { panic("must not be called") },
		timer: func(time.Time) Timer { panic("must not be called") },
	}
	clock, err := newWorkerClock(source)
	if err != nil {
		t.Fatal(err)
	}
	for _, duration := range []time.Duration{0, -1} {
		if timer, err := clock.NewTimer(duration); timer != nil || err != ErrInvalid {
			t.Fatalf("NewTimer(%s) = (%v, %v), want nil and ErrInvalid", duration, timer, err)
		}
	}
	if source.timerCalls.Load() != 0 {
		t.Fatalf("source NewTimer calls = %d, want 0", source.timerCalls.Load())
	}

	var nilClock *workerClock
	if timer, err := nilClock.NewTimer(time.Second); timer != nil || err != ErrInvalid {
		t.Fatalf("nil NewTimer() = (%v, %v), want nil and ErrInvalid", timer, err)
	}
}

func TestWorkerTimerCachesItsChannelAndAcceptsAClosedChannel(t *testing.T) {
	base := time.Date(2032, 4, 5, 6, 7, 8, 0, time.UTC)
	channel := make(chan time.Time)
	close(channel)
	inner := &workerTimerDouble{channel: channel, stopValue: true}
	source := &workerClockDouble{
		now:   func() time.Time { return base },
		timer: func(time.Time) Timer { return inner },
	}
	clock, err := newWorkerClock(source)
	if err != nil {
		t.Fatal(err)
	}
	timer, err := clock.NewTimer(time.Second)
	if err != nil || timer == nil {
		t.Fatalf("NewTimer() = (%v, %v)", timer, err)
	}
	if timer.C() != channel || timer.C() != channel || inner.cCalls.Load() != 1 {
		t.Fatalf("cached channel/source calls = %p/%d, want %p/1", timer.C(), inner.cCalls.Load(), channel)
	}
	if _, open := <-timer.C(); open {
		t.Fatal("closed timer channel was changed")
	}
	if !timer.Stop() || !timer.Stop() || inner.stopCalls.Load() != 1 {
		t.Fatalf("Stop results/calls = true expectation/%d", inner.stopCalls.Load())
	}

	var nilTimer *workerTimer
	if nilTimer.C() != nil || nilTimer.Stop() {
		t.Fatal("nil worker timer is not inert")
	}
}

func TestWorkerTimerContainsStopPanicAndStopsOnlyOnceConcurrently(t *testing.T) {
	base := time.Date(2032, 4, 5, 6, 7, 8, 0, time.UTC)
	inner := &workerTimerDouble{channel: make(chan time.Time), panicStop: true}
	source := &workerClockDouble{
		now:   func() time.Time { return base },
		timer: func(time.Time) Timer { return inner },
	}
	clock, err := newWorkerClock(source)
	if err != nil {
		t.Fatal(err)
	}
	timer, err := clock.NewTimer(time.Second)
	if err != nil {
		t.Fatal(err)
	}
	const calls = 64
	results := make(chan bool, calls)
	var wait sync.WaitGroup
	wait.Add(calls)
	for range calls {
		go func() {
			defer wait.Done()
			results <- timer.Stop()
		}()
	}
	wait.Wait()
	close(results)
	for stopped := range results {
		if stopped {
			t.Fatal("panicking Stop reported success")
		}
	}
	if inner.stopCalls.Load() != 1 {
		t.Fatalf("source Stop calls = %d, want 1", inner.stopCalls.Load())
	}
}

func TestWorkerClockWrapsSystemClockAndTimer(t *testing.T) {
	clock, err := newWorkerClock(systemClock{})
	if err != nil {
		t.Fatal(err)
	}
	now, err := clock.Now()
	if err != nil || now.IsZero() || now != now.Round(0).UTC() || now.Location() != time.UTC {
		t.Fatalf("system Now() = (%v, %v)", now, err)
	}
	timer, err := clock.NewTimer(time.Hour)
	if err != nil || timer == nil || timer.C() == nil {
		t.Fatalf("system NewTimer() = (%v, %v)", timer, err)
	}
	first := timer.Stop()
	second := timer.Stop()
	if !first || second != first {
		t.Fatalf("system timer Stop() = %t then %t", first, second)
	}
}

func TestWorkerClockErrorsRemainTheExactSentinel(t *testing.T) {
	clockSource := &workerClockDouble{
		now:   func() time.Time { panic(errors.New("private clock failure")) },
		timer: func(time.Time) Timer { panic("must not be called") },
	}
	clock, err := newWorkerClock(clockSource)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := clock.Now(); err != ErrInvalid {
		t.Fatalf("Now() error = %#v, want exact ErrInvalid", err)
	}
	timerSource := &workerClockDouble{
		now:   func() time.Time { return time.Date(2032, 4, 5, 6, 7, 8, 0, time.UTC) },
		timer: func(time.Time) Timer { panic(errors.New("private timer failure")) },
	}
	timerClock, err := newWorkerClock(timerSource)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := timerClock.NewTimer(time.Second); err != ErrInvalid {
		t.Fatalf("NewTimer() error = %#v, want exact ErrInvalid", err)
	}
}
