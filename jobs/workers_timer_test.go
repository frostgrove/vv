package jobs

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestWaitWorkerIntervalUsesInjectedClockForFire(t *testing.T) {
	base := time.Date(2032, 4, 5, 6, 7, 8, 0, time.UTC)
	interval := 3 * time.Second
	fired := make(chan time.Time, 1)
	fired <- base.Add(interval)
	var nowCalls atomic.Int32
	var deadline time.Time
	timer := &workerTimerDouble{channel: fired}
	source := &workerClockDouble{
		now: func() time.Time {
			if nowCalls.Add(1) == 1 {
				return base
			}
			return base.Add(interval)
		},
		timer: func(value time.Time) Timer {
			deadline = value
			return timer
		},
	}
	clock, err := newWorkerClock(source)
	if err != nil {
		t.Fatal(err)
	}
	ready, err := waitWorkerInterval(context.Background(), clock, interval)
	if err != nil || !ready {
		t.Fatalf("wait interval = (%t, %v)", ready, err)
	}
	if deadline != base.Add(interval) || source.nowCalls.Load() != 2 || source.timerCalls.Load() != 1 || timer.stopCalls.Load() != 1 {
		t.Fatalf("clock calls/deadline = now %d timer %d stop %d deadline %v", source.nowCalls.Load(), source.timerCalls.Load(), timer.stopCalls.Load(), deadline)
	}
}

func TestWaitWorkerIntervalSkipsTimerForCancelledContext(t *testing.T) {
	source := &workerClockDouble{
		now:   func() time.Time { panic("cancelled wait must not read time") },
		timer: func(time.Time) Timer { panic("cancelled wait must not create timer") },
	}
	clock, err := newWorkerClock(source)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	ready, err := waitWorkerInterval(ctx, clock, time.Second)
	if err != nil || ready || source.nowCalls.Load() != 0 || source.timerCalls.Load() != 0 {
		t.Fatalf("cancelled wait = (%t, %v), calls=(%d,%d)", ready, err, source.nowCalls.Load(), source.timerCalls.Load())
	}
}

func TestWaitWorkerIntervalRejectsClosedAndEarlyTimerWake(t *testing.T) {
	base := time.Date(2032, 4, 5, 6, 7, 8, 0, time.UTC)
	closed := make(chan time.Time)
	close(closed)
	early := make(chan time.Time, 1)
	early <- base
	for name, channel := range map[string]<-chan time.Time{"closed": closed, "early": early} {
		t.Run(name, func(t *testing.T) {
			timer := &workerTimerDouble{channel: channel}
			source := &workerClockDouble{
				now:   func() time.Time { return base },
				timer: func(time.Time) Timer { return timer },
			}
			clock, err := newWorkerClock(source)
			if err != nil {
				t.Fatal(err)
			}
			ready, err := waitWorkerInterval(context.Background(), clock, time.Second)
			if ready || !errors.Is(err, ErrInvalid) || timer.stopCalls.Load() != 1 {
				t.Fatalf("invalid wake = (%t, %v), stops=%d", ready, err, timer.stopCalls.Load())
			}
		})
	}
}

func TestWorkerClockStartDeadlineKeepsAbsoluteDeadline(t *testing.T) {
	base := time.Date(2032, 4, 5, 6, 7, 8, 0, time.UTC)
	deadline := base.Add(5 * time.Second)
	var scheduled time.Time
	timer := &workerTimerDouble{channel: make(chan time.Time)}
	source := &workerClockDouble{
		now: func() time.Time { return base.Add(2 * time.Second) },
		timer: func(value time.Time) Timer {
			scheduled = value
			return timer
		},
	}
	clock, err := newWorkerClock(source)
	if err != nil {
		t.Fatal(err)
	}
	now, got, pending, err := clock.startDeadline(deadline)
	if err != nil || !pending || got == nil || now != base.Add(2*time.Second) || scheduled != deadline {
		t.Fatalf("absolute timer = now %v timer %v pending %t err %v scheduled %v", now, got, pending, err, scheduled)
	}
	if _, valid := got.stop(); !valid {
		t.Fatal("absolute timer cleanup failed")
	}
}

func TestWaitWorkerIntervalRejectsInvalidTimerCleanup(t *testing.T) {
	base := time.Date(2032, 4, 5, 6, 7, 8, 0, time.UTC)
	timer := &workerTimerDouble{channel: make(chan time.Time), panicStop: true}
	created := make(chan struct{})
	source := &workerClockDouble{
		now: func() time.Time { return base },
		timer: func(time.Time) Timer {
			close(created)
			return timer
		},
	}
	clock, err := newWorkerClock(source)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		ready, waitErr := waitWorkerInterval(ctx, clock, time.Second)
		if ready {
			result <- errors.New("interval unexpectedly became ready")
			return
		}
		result <- waitErr
	}()
	<-created
	cancel()
	if err = <-result; err != ErrInvalid || timer.stopCalls.Load() != 1 {
		t.Fatalf("invalid timer cleanup = (%v, %d)", err, timer.stopCalls.Load())
	}
}

type timerRunDriver struct {
	description BackendDescription
	observedAt  time.Time
}

func (driver *timerRunDriver) Description() BackendDescription { return driver.description }
func (driver *timerRunDriver) Claim(context.Context, ClaimRequest) (ClaimBatch, error) {
	return NewClaimBatch(driver.observedAt, nil)
}
func (driver *timerRunDriver) Renew(context.Context, RenewRequest) (RenewResult, error) {
	return NewRenewResult(driver.observedAt, nil)
}
func (driver *timerRunDriver) Apply(context.Context, ApplyRequest) (ApplyResult, error) {
	return ApplyResult{}, errors.New("apply must not run")
}
func (driver *timerRunDriver) Recover(context.Context, RecoverRequest) (RecoverResult, error) {
	return NewRecoverResult(driver.observedAt, nil, 0, false)
}

type failingStopClock struct {
	now     time.Time
	created chan struct{}
	once    sync.Once
}

func (clock *failingStopClock) Now() time.Time { return clock.now }
func (clock *failingStopClock) NewTimerAt(time.Time) Timer {
	clock.once.Do(func() { close(clock.created) })
	return &workerTimerDouble{channel: make(chan time.Time), panicStop: true}
}

func TestWorkersRunPreservesLateTimerFailureDuringCancellation(t *testing.T) {
	definition := testQueueDefinition(t, "workers.timer-failure", String(1))
	clock := &failingStopClock{
		now:     time.Date(2032, 4, 5, 6, 7, 8, 0, time.UTC),
		created: make(chan struct{}),
	}
	driver := &timerRunDriver{description: queueTestBackendDescription(33), observedAt: clock.now}
	build, err := ParseBuildID("deploy:timer-failure")
	if err != nil {
		t.Fatal(err)
	}
	workers, err := NewWorkers(WorkersSpec{
		Namespace: queueTestNamespace(t, "workers-timer-failure"),
		Catalog:   MustCatalog(definition),
		Driver:    driver,
		Build:     build,
		Identity:  &workersConfigIdentity{},
		Clock:     clock,
		Entropy:   bytes.NewReader(bytes.Repeat([]byte{1}, WorkerIncarnationBytes)),
	}, On(definition, Handler[string](func(context.Context, string) error { return nil }), Concurrency(1)))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() { result <- workers.Run(ctx) }()
	select {
	case <-clock.created:
	case <-time.After(3 * time.Second):
		t.Fatal("worker did not create timer")
	}
	cancel()
	select {
	case err = <-result:
		if !errors.Is(err, ErrInvalid) {
			t.Fatalf("Run() = %v, want ErrInvalid", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("worker did not stop")
	}
}
