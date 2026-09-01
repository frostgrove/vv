package jobs

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type workerBoundaryDriver struct {
	description BackendDescription
	claim       func(context.Context, ClaimRequest) (ClaimBatch, error)
	renew       func(context.Context, RenewRequest) (RenewResult, error)
	apply       func(context.Context, ApplyRequest) (ApplyResult, error)
	recover     func(context.Context, RecoverRequest) (RecoverResult, error)
}

func (driver *workerBoundaryDriver) Description() BackendDescription { return driver.description }
func (driver *workerBoundaryDriver) Claim(ctx context.Context, request ClaimRequest) (ClaimBatch, error) {
	return driver.claim(ctx, request)
}
func (driver *workerBoundaryDriver) Renew(ctx context.Context, request RenewRequest) (RenewResult, error) {
	return driver.renew(ctx, request)
}
func (driver *workerBoundaryDriver) Apply(ctx context.Context, request ApplyRequest) (ApplyResult, error) {
	return driver.apply(ctx, request)
}
func (driver *workerBoundaryDriver) Recover(ctx context.Context, request RecoverRequest) (RecoverResult, error) {
	return driver.recover(ctx, request)
}

type workerBoundaryClock struct {
	nowCalls   atomic.Int32
	timerCalls atomic.Int32
	now        func(int32) time.Time
	timer      func(int32, time.Time) Timer
}

func (clock *workerBoundaryClock) Now() time.Time {
	call := clock.nowCalls.Add(1)
	return clock.now(call)
}

func (clock *workerBoundaryClock) NewTimerAt(deadline time.Time) Timer {
	call := clock.timerCalls.Add(1)
	return clock.timer(call, deadline)
}

type workerBoundaryTimer struct {
	channel     chan time.Time
	stopCalls   atomic.Int32
	stopGoexit  bool
	stopPanic   bool
	stopEntered chan struct{}
	stopRelease <-chan struct{}
	stopSend    bool
	stopFalse   bool
}

type workerBoundaryBrokenContext struct {
	goexit bool
}

type workerBoundaryLateErrContext struct {
	calls   atomic.Int32
	panicAt int32
}

type workerBoundaryBrokenDeadlineContext struct {
	goexit bool
}

func (contextValue workerBoundaryBrokenContext) Deadline() (time.Time, bool) {
	return time.Time{}, false
}

func (contextValue workerBoundaryBrokenContext) Done() <-chan struct{} {
	if contextValue.goexit {
		runtime.Goexit()
	}
	panic("private context panic")
}

func (workerBoundaryBrokenContext) Err() error    { return nil }
func (workerBoundaryBrokenContext) Value(any) any { return nil }

func (contextValue *workerBoundaryLateErrContext) Deadline() (time.Time, bool) {
	return time.Time{}, false
}

func (*workerBoundaryLateErrContext) Done() <-chan struct{} { return nil }
func (contextValue *workerBoundaryLateErrContext) Err() error {
	if contextValue.calls.Add(1) == contextValue.panicAt {
		panic("private late context panic")
	}
	return nil
}
func (*workerBoundaryLateErrContext) Value(any) any { return nil }

func (contextValue workerBoundaryBrokenDeadlineContext) Deadline() (time.Time, bool) {
	if contextValue.goexit {
		runtime.Goexit()
	}
	panic("private deadline panic")
}

func (workerBoundaryBrokenDeadlineContext) Done() <-chan struct{} { return nil }
func (workerBoundaryBrokenDeadlineContext) Err() error            { return nil }
func (workerBoundaryBrokenDeadlineContext) Value(any) any         { return nil }

func (timer *workerBoundaryTimer) C() <-chan time.Time { return timer.channel }
func (timer *workerBoundaryTimer) Stop() bool {
	timer.stopCalls.Add(1)
	if timer.stopGoexit {
		runtime.Goexit()
	}
	if timer.stopPanic {
		panic("private timer stop panic")
	}
	if timer.stopEntered != nil {
		close(timer.stopEntered)
	}
	if timer.stopRelease != nil {
		<-timer.stopRelease
	}
	if timer.stopSend {
		timer.channel <- time.Time{}
	}
	if timer.stopFalse {
		return false
	}
	return true
}

type workerBoundaryFixtures struct {
	catalog        Catalog
	consumer       Consumer
	build          BuildID
	claimRequest   ClaimRequest
	claimResult    ClaimBatch
	renewRequest   RenewRequest
	renewResult    RenewResult
	applyRequest   ApplyRequest
	applyResult    ApplyResult
	recoverRequest RecoverRequest
	recoverResult  RecoverResult
}

type workerBoundaryContextState struct {
	err   error
	cause error
}

func TestWorkerDriverTypedCallsValidateEveryOperation(t *testing.T) {
	fixtures := newWorkerBoundaryFixtures(t)
	driver := &workerBoundaryDriver{
		description: queueTestBackendDescription(1),
		claim: func(context.Context, ClaimRequest) (ClaimBatch, error) {
			return fixtures.claimResult, nil
		},
		renew: func(context.Context, RenewRequest) (RenewResult, error) {
			return fixtures.renewResult, nil
		},
		apply: func(context.Context, ApplyRequest) (ApplyResult, error) {
			return fixtures.applyResult, nil
		},
		recover: func(context.Context, RecoverRequest) (RecoverResult, error) {
			return fixtures.recoverResult, nil
		},
	}
	workers, source, timers := newWorkerBoundaryWorkers(t, driver, fixtures)
	claim, claimCall := workers.callClaim(context.Background(), fixtures.claimRequest)
	renew, renewCall := workers.callRenew(context.Background(), fixtures.renewRequest)
	apply, applyCall := workers.callApply(context.Background(), fixtures.applyRequest)
	recovered, recoverCall := workers.callRecover(context.Background(), fixtures.recoverRequest)
	for name, call := range map[string]workerDriverCall{"claim": claimCall, "renew": renewCall, "apply": applyCall, "recover": recoverCall} {
		if call.outcome != WorkerOutcomeComplete || call.failure != WorkerFailureNone || call.err != nil || !call.started || call.uncertain || call.fatal() || call.elapsed <= 0 || call.elapsed >= MinimumOperationTimeout {
			t.Fatalf("%s call = %#v", name, call)
		}
	}
	if claim.Len() != 1 || renew.Len() != 1 || apply.Result().Mutation() != DeliveryMutationApplied || recovered.Released() != 1 || len(recovered.Items()) != 1 {
		t.Fatal("typed driver results were not retained")
	}
	if source.nowCalls.Load() != 16 || source.timerCalls.Load() != 4 || len(*timers) != 4 {
		t.Fatalf("clock calls = now %d timer %d, timers %d", source.nowCalls.Load(), source.timerCalls.Load(), len(*timers))
	}
	for _, timer := range *timers {
		if timer.stopCalls.Load() != 1 {
			t.Fatalf("timer stop calls = %d", timer.stopCalls.Load())
		}
	}
	if _, failed := workers.fatal.load(); failed {
		t.Fatal("successful calls latched a fatal failure")
	}
}

func TestWorkerDriverTypedCallsAcceptEmptyClaimAndRecover(t *testing.T) {
	fixtures := newWorkerBoundaryFixtures(t)
	emptyClaim, err := NewClaimBatch(fixtures.claimResult.ObservedAt(), nil)
	if err != nil {
		t.Fatal(err)
	}
	emptyRecover, err := NewRecoverResult(fixtures.recoverResult.ObservedAt(), nil, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	driver := &workerBoundaryDriver{
		description: queueTestBackendDescription(1),
		claim: func(context.Context, ClaimRequest) (ClaimBatch, error) {
			return emptyClaim, nil
		},
		renew: func(context.Context, RenewRequest) (RenewResult, error) { panic("unexpected renew") },
		apply: func(context.Context, ApplyRequest) (ApplyResult, error) { panic("unexpected apply") },
		recover: func(context.Context, RecoverRequest) (RecoverResult, error) {
			return emptyRecover, nil
		},
	}
	workers, _, _ := newWorkerBoundaryWorkers(t, driver, fixtures)
	claim, claimCall := workers.callClaim(context.Background(), fixtures.claimRequest)
	recovered, recoverCall := workers.callRecover(context.Background(), fixtures.recoverRequest)
	if claim.Len() != 0 || recovered.Released() != 0 || len(recovered.Items()) != 0 || claimCall.outcome != WorkerOutcomeComplete || recoverCall.outcome != WorkerOutcomeComplete {
		t.Fatalf("empty calls = claim %#v/%#v recover %#v/%#v", claim, claimCall, recovered, recoverCall)
	}
}

func TestWorkerDriverBoundaryDiscardsRawErrorsAndSpoofedTaxonomy(t *testing.T) {
	secret := errors.New("private driver secret")
	for _, returned := range []error{secret, ErrDriverContract, context.Canceled, context.DeadlineExceeded} {
		boundary, _, _ := newWorkerBoundary(t, nil)
		value, call := invokeWorkerDriver(boundary, context.Background(), func(context.Context) (int, error) {
			return 71, returned
		}, func(value int) (int, error) {
			panic("validator must not receive errored result")
		})
		if value != 0 || call.outcome != WorkerOutcomeFailed || call.failure != WorkerFailureDriver || call.err != ErrDriver || !call.started || !call.uncertain || call.fatal() {
			t.Fatalf("raw error %v = value %d call %#v", returned, value, call)
		}
		if _, failed := boundary.fatal.load(); failed {
			t.Fatal("ordinary driver error latched a fatal failure")
		}
	}
}

func TestWorkerDriverBoundaryContainsPanicAndContractFailures(t *testing.T) {
	tests := []struct {
		name        string
		invoke      func(context.Context) (int, error)
		validate    func(int) (int, error)
		wantFailure WorkerFailure
		wantErr     error
	}{
		{
			name: "driver panic",
			invoke: func(context.Context) (int, error) {
				panic("private driver panic")
			},
			validate:    func(value int) (int, error) { return value, nil },
			wantFailure: WorkerFailureDriverPanic,
			wantErr:     ErrDriver,
		},
		{
			name:        "invalid result",
			invoke:      func(context.Context) (int, error) { return 71, nil },
			validate:    func(int) (int, error) { return 19, errors.New("private validation detail") },
			wantFailure: WorkerFailureDriverContract,
			wantErr:     ErrDriverContract,
		},
		{
			name:        "validator panic",
			invoke:      func(context.Context) (int, error) { return 71, nil },
			validate:    func(int) (int, error) { panic("private validator panic") },
			wantFailure: WorkerFailureRuntime,
			wantErr:     ErrInvalid,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			boundary, _, _ := newWorkerBoundary(t, nil)
			value, call := invokeWorkerDriver(boundary, context.Background(), test.invoke, test.validate)
			if value != 0 || call.outcome != WorkerOutcomeFailed || call.failure != test.wantFailure || call.err != test.wantErr || !call.started || !call.uncertain || !call.fatal() {
				t.Fatalf("call = value %d %#v", value, call)
			}
			record, failed := boundary.fatal.load()
			if !failed || record.failure != test.wantFailure || record.err != test.wantErr {
				t.Fatalf("fatal record = (%#v, %t)", record, failed)
			}
		})
	}
}

func TestWorkerDriverBoundaryRejectsCancellationBeforeSideEffects(t *testing.T) {
	tests := []struct {
		name    string
		context func() context.Context
		outcome WorkerOutcome
		err     error
	}{
		{
			name: "cancelled",
			context: func() context.Context {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return ctx
			},
			outcome: WorkerOutcomeCancelled,
			err:     context.Canceled,
		},
		{
			name: "deadline",
			context: func() context.Context {
				ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
				cancel()
				return ctx
			},
			outcome: WorkerOutcomeTimedOut,
			err:     context.DeadlineExceeded,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var invoked atomic.Int32
			source := &workerBoundaryClock{
				now:   func(int32) time.Time { panic("clock must not run") },
				timer: func(int32, time.Time) Timer { panic("timer must not run") },
			}
			boundary, _, _ := newWorkerBoundary(t, source)
			value, call := invokeWorkerDriver(boundary, test.context(), func(context.Context) (int, error) {
				invoked.Add(1)
				return 1, nil
			}, func(value int) (int, error) { return value, nil })
			if value != 0 || call.outcome != test.outcome || call.err != test.err || call.started || call.uncertain || call.failure != WorkerFailureNone || invoked.Load() != 0 || source.nowCalls.Load() != 0 || source.timerCalls.Load() != 0 {
				t.Fatalf("pre-cancel call = value %d %#v invoked %d", value, call, invoked.Load())
			}
		})
	}
}

func TestWorkerDriverBoundaryDoesNotAcquireTimerForBrokenParentContext(t *testing.T) {
	boundary, source, timers := newWorkerBoundary(t, nil)
	var invoked atomic.Int32
	value, call := invokeWorkerDriver(boundary, workerBoundaryBrokenContext{}, func(context.Context) (int, error) {
		invoked.Add(1)
		return 71, nil
	}, func(value int) (int, error) { return value, nil })
	if value != 0 || call.outcome != WorkerOutcomeFailed || call.failure != WorkerFailureRuntime || call.err != ErrInvalid || call.started || call.uncertain || invoked.Load() != 0 || source.timerCalls.Load() != 1 || len(*timers) != 1 || (*timers)[0].stopCalls.Load() != 1 {
		t.Fatalf("broken parent = value %d %#v timer calls %d", value, call, source.timerCalls.Load())
	}

	goexitBoundary, goexitSource, goexitTimers := newWorkerBoundary(t, nil)
	returned := &atomic.Bool{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = invokeWorkerDriver(goexitBoundary, workerBoundaryBrokenContext{goexit: true}, func(context.Context) (int, error) {
			invoked.Add(1)
			return 71, nil
		}, func(value int) (int, error) { return value, nil })
		returned.Store(true)
	}()
	<-done
	if !returned.Load() || invoked.Load() != 0 || goexitSource.timerCalls.Load() != 1 || len(*goexitTimers) != 1 || (*goexitTimers)[0].stopCalls.Load() != 1 {
		t.Fatalf("Goexit parent = returned %t timer calls %d", returned.Load(), goexitSource.timerCalls.Load())
	}
	record, failed := goexitBoundary.fatal.load()
	if !failed || record.failure != WorkerFailureRuntime || record.err != ErrInvalid {
		t.Fatalf("Goexit parent fatal = (%#v, %t)", record, failed)
	}
}

func TestWorkerDriverBoundaryContainsInvalidFailureLatch(t *testing.T) {
	boundary := workerDriverBoundary{fatal: &workerFailureLatch{}}
	var invoked atomic.Int32
	value, call := invokeWorkerDriver(boundary, context.Background(), func(context.Context) (int, error) {
		invoked.Add(1)
		return 71, nil
	}, func(value int) (int, error) { return value, nil })
	if value != 0 || call.outcome != WorkerOutcomeFailed || call.failure != WorkerFailureRuntime || call.err != ErrInvalid || call.started || call.uncertain || invoked.Load() != 0 {
		t.Fatalf("invalid latch = value %d %#v driver %d", value, call, invoked.Load())
	}
}

func TestWorkerDriverBoundaryDoesNotAcquireTimerForBrokenParentDeadline(t *testing.T) {
	boundary, source, timers := newWorkerBoundary(t, nil)
	value, call := invokeWorkerDriver(boundary, workerBoundaryBrokenDeadlineContext{}, func(context.Context) (int, error) {
		panic("driver must not run")
	}, func(value int) (int, error) { return value, nil })
	if value != 0 || call.outcome != WorkerOutcomeFailed || call.failure != WorkerFailureRuntime || call.err != ErrInvalid || call.started || call.uncertain || source.nowCalls.Load() != 0 || source.timerCalls.Load() != 0 || len(*timers) != 0 {
		t.Fatalf("broken deadline = value %d %#v now %d timer %d", value, call, source.nowCalls.Load(), source.timerCalls.Load())
	}

	goexitBoundary, goexitSource, goexitTimers := newWorkerBoundary(t, nil)
	returned := &atomic.Bool{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = invokeWorkerDriver(goexitBoundary, workerBoundaryBrokenDeadlineContext{goexit: true}, func(context.Context) (int, error) {
			panic("driver must not run")
		}, func(value int) (int, error) { return value, nil })
		returned.Store(true)
	}()
	<-done
	if returned.Load() || goexitSource.nowCalls.Load() != 0 || goexitSource.timerCalls.Load() != 0 || len(*goexitTimers) != 0 {
		t.Fatalf("Goexit deadline = returned %t now %d timer %d", returned.Load(), goexitSource.nowCalls.Load(), goexitSource.timerCalls.Load())
	}
	record, failed := goexitBoundary.fatal.load()
	if !failed || record.failure != WorkerFailureRuntime || record.err != ErrInvalid {
		t.Fatalf("Goexit deadline fatal = (%#v, %t)", record, failed)
	}
}

func TestWorkerOperationContextPublishesErrAfterDone(t *testing.T) {
	base, cancel := context.WithCancelCause(context.Background())
	timedOut := &atomic.Bool{}
	timedOut.Store(true)
	ctx := &workerOperationContext{Context: base, deadline: time.Now().Add(time.Minute), timedOut: timedOut}
	if ctx.Err() != nil {
		t.Fatalf("Err before Done = %v", ctx.Err())
	}
	select {
	case <-ctx.Done():
		t.Fatal("Done closed before cancellation")
	default:
	}
	cancel(context.DeadlineExceeded)
	<-ctx.Done()
	if ctx.Err() != context.DeadlineExceeded || context.Cause(ctx) != context.DeadlineExceeded {
		t.Fatalf("timeout context = err %v cause %v", ctx.Err(), context.Cause(ctx))
	}
}

func TestWorkerDriverBoundaryCancelsWithInjectedDeadline(t *testing.T) {
	base := time.Date(2034, 5, 6, 7, 8, 9, 0, time.UTC)
	timeout := MinimumOperationTimeout
	timer := &workerBoundaryTimer{channel: make(chan time.Time, 1)}
	source := &workerBoundaryClock{
		now: func(call int32) time.Time {
			if call <= 3 {
				return base
			}
			return base.Add(timeout)
		},
		timer: func(_ int32, deadline time.Time) Timer {
			if deadline != base.Add(timeout) {
				panic("wrong deadline")
			}
			return timer
		},
	}
	boundary, _, _ := newWorkerBoundary(t, source)
	started := make(chan struct{})
	go func() {
		<-started
		timer.channel <- time.Time{}
	}()
	value, call := invokeWorkerDriver(boundary, context.Background(), func(ctx context.Context) (int, error) {
		deadline, ok := ctx.Deadline()
		if !ok || deadline != base.Add(timeout) {
			t.Fatalf("driver deadline = (%v, %t)", deadline, ok)
		}
		close(started)
		<-ctx.Done()
		if ctx.Err() != context.DeadlineExceeded || context.Cause(ctx) != context.DeadlineExceeded {
			t.Fatalf("driver context = err %v cause %v", ctx.Err(), context.Cause(ctx))
		}
		return 71, nil
	}, func(value int) (int, error) { return value, nil })
	if value != 0 || call.outcome != WorkerOutcomeTimedOut || call.failure != WorkerFailureNone || call.err != context.DeadlineExceeded || !call.started || !call.uncertain || call.elapsed != timeout || timer.stopCalls.Load() != 1 {
		t.Fatalf("timeout call = value %d %#v stops %d", value, call, timer.stopCalls.Load())
	}
	if _, failed := boundary.fatal.load(); failed {
		t.Fatal("ordinary timeout latched fatal failure")
	}
}

func TestWorkerDriverBoundaryDiscardsResultAfterParentCancellation(t *testing.T) {
	boundary, _, timers := newWorkerBoundary(t, nil)
	parent, cancel := context.WithCancelCause(context.Background())
	secret := errors.New("private cancellation cause")
	started := make(chan struct{})
	go func() {
		<-started
		cancel(secret)
	}()
	value, call := invokeWorkerDriver(boundary, parent, func(ctx context.Context) (int, error) {
		close(started)
		<-ctx.Done()
		if context.Cause(ctx) != secret {
			t.Fatalf("driver cause = %v", context.Cause(ctx))
		}
		return 71, nil
	}, func(value int) (int, error) { return value, nil })
	if value != 0 || call.outcome != WorkerOutcomeCancelled || call.failure != WorkerFailureNone || call.err != context.Canceled || !call.started || !call.uncertain || len(*timers) != 1 || (*timers)[0].stopCalls.Load() != 1 {
		t.Fatalf("parent cancellation = value %d %#v", value, call)
	}
	if _, failed := boundary.fatal.load(); failed {
		t.Fatal("parent cancellation latched fatal failure")
	}
}

func TestWorkerDriverBoundaryPreservesParentDeadlineInsideDriver(t *testing.T) {
	boundary, _, _ := newWorkerBoundary(t, nil)
	boundary.timeout = 100 * time.Millisecond
	parent, cancel := context.WithDeadline(context.Background(), time.Now().Add(20*time.Millisecond))
	defer cancel()
	value, call := invokeWorkerDriver(boundary, parent, func(ctx context.Context) (int, error) {
		<-ctx.Done()
		if ctx.Err() != context.DeadlineExceeded || context.Cause(ctx) != context.DeadlineExceeded {
			t.Fatalf("driver parent deadline = err %v cause %v", ctx.Err(), context.Cause(ctx))
		}
		return 71, nil
	}, func(value int) (int, error) { return value, nil })
	if value != 0 || call.outcome != WorkerOutcomeTimedOut || call.err != context.DeadlineExceeded || !call.started || !call.uncertain {
		t.Fatalf("parent deadline = value %d %#v", value, call)
	}
}

func TestWorkerDriverBoundaryRejectsResultAtDeadlineWithoutTimerWake(t *testing.T) {
	base := time.Date(2034, 5, 6, 7, 8, 9, 0, time.UTC)
	timeout := MinimumOperationTimeout
	timer := &workerBoundaryTimer{channel: make(chan time.Time)}
	source := &workerBoundaryClock{
		now: func(call int32) time.Time {
			if call <= 3 {
				return base
			}
			return base.Add(timeout)
		},
		timer: func(int32, time.Time) Timer { return timer },
	}
	boundary, _, _ := newWorkerBoundary(t, source)
	value, call := invokeWorkerDriver(boundary, context.Background(), func(context.Context) (int, error) {
		return 71, nil
	}, func(value int) (int, error) { return value, nil })
	if value != 0 || call.outcome != WorkerOutcomeTimedOut || call.err != context.DeadlineExceeded || !call.started || !call.uncertain || call.elapsed != timeout || timer.stopCalls.Load() != 1 {
		t.Fatalf("deadline race = value %d %#v", value, call)
	}
}

func TestWorkerDriverBoundarySkipsDriverWhenTimerSetupConsumesDeadline(t *testing.T) {
	base := time.Date(2034, 5, 6, 7, 8, 9, 0, time.UTC)
	timeout := MinimumOperationTimeout
	timer := &workerBoundaryTimer{channel: make(chan time.Time)}
	advanced := &atomic.Bool{}
	source := &workerBoundaryClock{
		now: func(int32) time.Time {
			if advanced.Load() {
				return base.Add(timeout)
			}
			return base
		},
		timer: func(int32, time.Time) Timer {
			advanced.Store(true)
			return timer
		},
	}
	boundary, _, _ := newWorkerBoundary(t, source)
	var invoked atomic.Int32
	value, call := invokeWorkerDriver(boundary, context.Background(), func(context.Context) (int, error) {
		invoked.Add(1)
		return 71, nil
	}, func(value int) (int, error) { return value, nil })
	if value != 0 || call.outcome != WorkerOutcomeTimedOut || call.err != context.DeadlineExceeded || call.started || call.uncertain || invoked.Load() != 0 || source.nowCalls.Load() != 2 || source.timerCalls.Load() != 1 || timer.stopCalls.Load() != 1 {
		t.Fatalf("delayed setup = value %d %#v driver %d now %d timer %d stops %d", value, call, invoked.Load(), source.nowCalls.Load(), source.timerCalls.Load(), timer.stopCalls.Load())
	}
}

func TestWorkerDriverBoundaryKeepsAbsoluteDeadlineAfterPartialTimerSetup(t *testing.T) {
	base := time.Date(2034, 5, 6, 7, 8, 9, 0, time.UTC)
	timeout := MinimumOperationTimeout
	timer := &workerBoundaryTimer{channel: make(chan time.Time, 1)}
	phase := &atomic.Int32{}
	armedAt := make(chan time.Time, 1)
	source := &workerBoundaryClock{
		now: func(int32) time.Time {
			switch phase.Load() {
			case 1:
				return base.Add(timeout / 2)
			case 2:
				return base.Add(timeout)
			default:
				return base
			}
		},
		timer: func(_ int32, deadline time.Time) Timer {
			armedAt <- deadline
			phase.Store(1)
			return timer
		},
	}
	boundary, _, _ := newWorkerBoundary(t, source)
	started := make(chan struct{})
	go func() {
		<-started
		phase.Store(2)
		timer.channel <- time.Time{}
	}()
	value, call := invokeWorkerDriver(boundary, context.Background(), func(ctx context.Context) (int, error) {
		close(started)
		<-ctx.Done()
		return 71, nil
	}, func(value int) (int, error) { return value, nil })
	if deadline := <-armedAt; deadline != base.Add(timeout) {
		t.Fatalf("absolute timer deadline = %v", deadline)
	}
	if value != 0 || call.outcome != WorkerOutcomeTimedOut || call.err != context.DeadlineExceeded || !call.started || !call.uncertain || call.elapsed != timeout || timer.stopCalls.Load() != 1 {
		t.Fatalf("partial setup = value %d %#v stops %d", value, call, timer.stopCalls.Load())
	}
}

func TestWorkerDriverBoundaryRejectsDeadlineDuringFinalPreflight(t *testing.T) {
	base := time.Date(2034, 5, 6, 7, 8, 9, 0, time.UTC)
	timeout := MinimumOperationTimeout
	timer := &workerBoundaryTimer{channel: make(chan time.Time, 1)}
	preflight := make(chan struct{})
	continuePreflight := make(chan struct{})
	advanced := &atomic.Bool{}
	source := &workerBoundaryClock{
		now: func(call int32) time.Time {
			if call <= 2 {
				return base
			}
			close(preflight)
			<-continuePreflight
			if advanced.Load() {
				return base.Add(timeout)
			}
			return base
		},
		timer: func(int32, time.Time) Timer { return timer },
	}
	boundary, _, _ := newWorkerBoundary(t, source)
	var invoked atomic.Int32
	result := make(chan workerDriverCall, 1)
	go func() {
		_, call := invokeWorkerDriver(boundary, context.Background(), func(context.Context) (int, error) {
			invoked.Add(1)
			return 71, nil
		}, func(value int) (int, error) { return value, nil })
		result <- call
	}()
	<-preflight
	timer.channel <- time.Time{}
	advanced.Store(true)
	close(continuePreflight)
	call := <-result
	if call.outcome != WorkerOutcomeTimedOut || call.err != context.DeadlineExceeded || call.started || call.uncertain || invoked.Load() != 0 || timer.stopCalls.Load() != 1 {
		t.Fatalf("final preflight = %#v driver %d stops %d", call, invoked.Load(), timer.stopCalls.Load())
	}
}

func TestWorkerDriverBoundaryRejectsEarlyTimerRaisedByFinalClockRead(t *testing.T) {
	base := time.Date(2034, 5, 6, 7, 8, 9, 0, time.UTC)
	timer := &workerBoundaryTimer{channel: make(chan time.Time, 1)}
	source := &workerBoundaryClock{
		now: func(call int32) time.Time {
			if call == 3 {
				timer.channel <- time.Time{}
			}
			return base.Add(time.Duration(call - 1))
		},
		timer: func(int32, time.Time) Timer { return timer },
	}
	boundary, _, _ := newWorkerBoundary(t, source)
	var invoked atomic.Int32
	value, call := invokeWorkerDriver(boundary, context.Background(), func(context.Context) (int, error) {
		invoked.Add(1)
		return 71, nil
	}, func(value int) (int, error) { return value, nil })
	if value != 0 || call.outcome != WorkerOutcomeFailed || call.failure != WorkerFailureRuntime || call.err != ErrInvalid || call.started || call.uncertain || invoked.Load() != 0 || timer.stopCalls.Load() != 1 {
		t.Fatalf("final clock timer = value %d %#v driver %d stops %d", value, call, invoked.Load(), timer.stopCalls.Load())
	}
}

func TestWorkerDriverBoundaryRejectsParentCancellationRaisedByFinalClockRead(t *testing.T) {
	base := time.Date(2034, 5, 6, 7, 8, 9, 0, time.UTC)
	timer := &workerBoundaryTimer{channel: make(chan time.Time)}
	parent, cancel := context.WithCancel(context.Background())
	source := &workerBoundaryClock{
		now: func(call int32) time.Time {
			if call == 3 {
				cancel()
			}
			return base.Add(time.Duration(call - 1))
		},
		timer: func(int32, time.Time) Timer { return timer },
	}
	boundary, _, _ := newWorkerBoundary(t, source)
	var invoked atomic.Int32
	value, call := invokeWorkerDriver(boundary, parent, func(context.Context) (int, error) {
		invoked.Add(1)
		return 71, nil
	}, func(value int) (int, error) { return value, nil })
	if value != 0 || call.outcome != WorkerOutcomeCancelled || call.err != context.Canceled || call.started || call.uncertain || invoked.Load() != 0 || timer.stopCalls.Load() != 1 {
		t.Fatalf("final clock cancellation = value %d %#v driver %d stops %d", value, call, invoked.Load(), timer.stopCalls.Load())
	}
}

func TestWorkerDriverBoundaryDoesNotDetachContextIgnoringDriver(t *testing.T) {
	base := time.Date(2034, 5, 6, 7, 8, 9, 0, time.UTC)
	timeout := MinimumOperationTimeout
	timer := &workerBoundaryTimer{channel: make(chan time.Time, 1)}
	source := &workerBoundaryClock{
		now: func(call int32) time.Time {
			if call <= 3 {
				return base
			}
			return base.Add(timeout)
		},
		timer: func(int32, time.Time) Timer { return timer },
	}
	boundary, _, _ := newWorkerBoundary(t, source)
	started := make(chan struct{})
	cancelled := make(chan struct{})
	release := make(chan struct{})
	result := make(chan workerDriverCall, 1)
	go func() {
		_, call := invokeWorkerDriver(boundary, context.Background(), func(ctx context.Context) (int, error) {
			close(started)
			<-ctx.Done()
			close(cancelled)
			<-release
			return 71, nil
		}, func(value int) (int, error) { return value, nil })
		result <- call
	}()
	<-started
	timer.channel <- time.Time{}
	<-cancelled
	select {
	case call := <-result:
		t.Fatalf("boundary returned before driver: %#v", call)
	default:
	}
	close(release)
	call := <-result
	if call.outcome != WorkerOutcomeTimedOut || !call.uncertain || timer.stopCalls.Load() != 1 {
		t.Fatalf("late driver call = %#v stops %d", call, timer.stopCalls.Load())
	}
}

func TestWorkerDriverBoundaryKeepsSettledTimeoutAfterLateParentCancel(t *testing.T) {
	base := time.Date(2034, 5, 6, 7, 8, 9, 0, time.UTC)
	timeout := MinimumOperationTimeout
	timer := &workerBoundaryTimer{channel: make(chan time.Time, 1)}
	source := &workerBoundaryClock{
		now: func(call int32) time.Time {
			if call <= 3 {
				return base
			}
			return base.Add(timeout)
		},
		timer: func(int32, time.Time) Timer { return timer },
	}
	boundary, _, _ := newWorkerBoundary(t, source)
	parent, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	timedOut := make(chan struct{})
	driverErr := make(chan error, 1)
	release := make(chan struct{})
	result := make(chan workerDriverCall, 1)
	go func() {
		_, call := invokeWorkerDriver(boundary, parent, func(ctx context.Context) (int, error) {
			close(started)
			<-ctx.Done()
			driverErr <- ctx.Err()
			close(timedOut)
			<-release
			return 71, nil
		}, func(value int) (int, error) { return value, nil })
		result <- call
	}()
	<-started
	timer.channel <- time.Time{}
	<-timedOut
	if err := <-driverErr; err != context.DeadlineExceeded {
		t.Fatalf("settled driver error = %v", err)
	}
	cancel()
	close(release)
	call := <-result
	if call.outcome != WorkerOutcomeTimedOut || call.err != context.DeadlineExceeded {
		t.Fatalf("late parent cancellation changed timeout: %#v", call)
	}
}

func TestWorkerDriverBoundaryPublishesTimeoutBeforeBlockingCleanup(t *testing.T) {
	base := time.Date(2034, 5, 6, 7, 8, 9, 0, time.UTC)
	timeout := MinimumOperationTimeout
	stopEntered := make(chan struct{})
	stopRelease := make(chan struct{})
	timer := &workerBoundaryTimer{channel: make(chan time.Time, 1), stopEntered: stopEntered, stopRelease: stopRelease}
	source := &workerBoundaryClock{
		now: func(call int32) time.Time {
			if call <= 3 {
				return base
			}
			return base.Add(timeout)
		},
		timer: func(int32, time.Time) Timer { return timer },
	}
	boundary, _, _ := newWorkerBoundary(t, source)
	parent, cancel := context.WithCancelCause(context.Background())
	started := make(chan struct{})
	driverObserved := make(chan workerBoundaryContextState, 1)
	driverRelease := make(chan struct{})
	result := make(chan workerDriverCall, 1)
	go func() {
		_, call := invokeWorkerDriver(boundary, parent, func(ctx context.Context) (int, error) {
			close(started)
			<-ctx.Done()
			driverObserved <- workerBoundaryContextState{err: ctx.Err(), cause: context.Cause(ctx)}
			<-driverRelease
			return 71, nil
		}, func(value int) (int, error) { return value, nil })
		result <- call
	}()
	<-started
	timer.channel <- time.Time{}
	observed := <-driverObserved
	if observed.err != context.DeadlineExceeded || observed.cause != context.DeadlineExceeded {
		t.Fatalf("driver cleanup race = err %v cause %v", observed.err, observed.cause)
	}
	<-stopEntered
	cancel(errors.New("late private cancellation"))
	close(driverRelease)
	close(stopRelease)
	call := <-result
	if call.outcome != WorkerOutcomeTimedOut || call.err != context.DeadlineExceeded || timer.stopCalls.Load() != 1 {
		t.Fatalf("blocking cleanup changed timeout: %#v", call)
	}
}

func TestWorkerDriverSupervisorRendezvousPrecedesTerminalObservation(t *testing.T) {
	base := time.Date(2034, 5, 6, 7, 8, 9, 0, time.UTC)
	timeout := MinimumOperationTimeout
	inner := &workerBoundaryTimer{channel: make(chan time.Time, 1)}
	source := &workerBoundaryClock{
		now: func(call int32) time.Time {
			if call <= 2 {
				return base
			}
			return base.Add(timeout)
		},
		timer: func(int32, time.Time) Timer { panic("timer is already wrapped") },
	}
	clock, err := newWorkerClock(source)
	if err != nil {
		t.Fatal(err)
	}
	timer := &workerTimer{clock: clock, inner: inner, channel: inner.channel}
	baseContext, cancel := context.WithCancelCause(context.Background())
	defer cancel(context.Canceled)
	timedOut := &atomic.Bool{}
	fatal := newWorkerFailureLatch()
	operation := &workerOperationGate{}
	started := &atomic.Bool{}
	completed := make(chan struct{})
	startRequest := make(chan chan struct{})
	observed := make(chan workerOperationObservation, 1)
	go superviseWorkerOperation(baseContext, cancel, timedOut, clock, base.Add(timeout), timer, fatal, operation, started, completed, startRequest, observed)
	grant := make(chan struct{})
	startRequest <- grant
	for !started.Load() {
		runtime.Gosched()
	}
	inner.channel <- time.Time{}
	select {
	case observation := <-observed:
		t.Fatalf("observation preceded grant: %#v", observation)
	default:
	}
	<-grant
	close(completed)
	observation := <-observed
	if observation.signal != workerOperationTimedOut || observation.cleanupFailed || !timedOut.Load() || inner.stopCalls.Load() != 1 {
		t.Fatalf("rendezvous observation = %#v timeout %t stops %d", observation, timedOut.Load(), inner.stopCalls.Load())
	}
}

func TestWorkerDriverBoundaryKeepsPrestartSettlementAfterLateFatal(t *testing.T) {
	base := time.Date(2034, 5, 6, 7, 8, 9, 0, time.UTC)
	tests := []struct {
		name    string
		prepare func(context.CancelFunc, *workerBoundaryTimer)
		now     func(int32) time.Time
		outcome WorkerOutcome
		err     error
	}{
		{
			name: "parent",
			prepare: func(cancel context.CancelFunc, _ *workerBoundaryTimer) {
				cancel()
			},
			now:     func(call int32) time.Time { return base.Add(time.Duration(call - 1)) },
			outcome: WorkerOutcomeCancelled,
			err:     context.Canceled,
		},
		{
			name: "timeout",
			prepare: func(_ context.CancelFunc, timer *workerBoundaryTimer) {
				timer.channel <- time.Time{}
			},
			now: func(call int32) time.Time {
				if call == 1 {
					return base
				}
				return base.Add(MinimumOperationTimeout)
			},
			outcome: WorkerOutcomeTimedOut,
			err:     context.DeadlineExceeded,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stopEntered := make(chan struct{})
			stopRelease := make(chan struct{})
			timer := &workerBoundaryTimer{channel: make(chan time.Time, 1), stopEntered: stopEntered, stopRelease: stopRelease}
			parent, cancel := context.WithCancel(context.Background())
			defer cancel()
			source := &workerBoundaryClock{
				now: test.now,
				timer: func(int32, time.Time) Timer {
					test.prepare(cancel, timer)
					return timer
				},
			}
			boundary, _, _ := newWorkerBoundary(t, source)
			var invoked atomic.Int32
			result := make(chan workerDriverCall, 1)
			go func() {
				_, call := invokeWorkerDriver(boundary, parent, func(context.Context) (int, error) {
					invoked.Add(1)
					return 71, nil
				}, func(value int) (int, error) { return value, nil })
				result <- call
			}()
			<-stopEntered
			boundary.fatal.fail(WorkerFailureDriverPanic, ErrDriver)
			close(stopRelease)
			call := <-result
			if call.outcome != test.outcome || call.failure != WorkerFailureNone || call.err != test.err || call.started || call.uncertain || invoked.Load() != 0 || timer.stopCalls.Load() != 1 {
				t.Fatalf("settled call = %#v driver %d stops %d", call, invoked.Load(), timer.stopCalls.Load())
			}
		})
	}
}

func TestWorkerDriverBoundaryTreatsClosedAndEarlyTimersAsRuntimeFailure(t *testing.T) {
	base := time.Date(2034, 5, 6, 7, 8, 9, 0, time.UTC)
	timeout := MinimumOperationTimeout
	tests := []struct {
		name  string
		setup func(*workerBoundaryTimer)
		now   func(int32) time.Time
	}{
		{
			name: "closed",
			setup: func(timer *workerBoundaryTimer) {
				close(timer.channel)
			},
			now: func(call int32) time.Time { return base.Add(time.Duration(call - 1)) },
		},
		{
			name: "early",
			setup: func(timer *workerBoundaryTimer) {
				timer.channel <- time.Time{}
			},
			now: func(call int32) time.Time {
				if call == 1 {
					return base
				}
				return base.Add(timeout - 1)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			timer := &workerBoundaryTimer{channel: make(chan time.Time, 1)}
			test.setup(timer)
			source := &workerBoundaryClock{now: test.now, timer: func(int32, time.Time) Timer { return timer }}
			boundary, _, _ := newWorkerBoundary(t, source)
			var invoked atomic.Int32
			value, call := invokeWorkerDriver(boundary, context.Background(), func(ctx context.Context) (int, error) {
				invoked.Add(1)
				return 71, nil
			}, func(value int) (int, error) { return value, nil })
			if value != 0 || call.outcome != WorkerOutcomeFailed || call.failure != WorkerFailureRuntime || call.err != ErrInvalid || call.started || call.uncertain || !call.fatal() || timer.stopCalls.Load() != 1 || invoked.Load() != 0 {
				t.Fatalf("timer failure = value %d %#v stops %d", value, call, timer.stopCalls.Load())
			}
			record, failed := boundary.fatal.load()
			if !failed || record.failure != WorkerFailureRuntime || record.err != ErrInvalid {
				t.Fatalf("fatal record = (%#v, %t)", record, failed)
			}
		})
	}
}

func TestWorkerDriverBoundaryDoesNotLoseInvalidTimerToConcurrentCompletion(t *testing.T) {
	base := time.Date(2034, 5, 6, 7, 8, 9, 0, time.UTC)
	for iteration := 0; iteration < 100; iteration++ {
		timer := &workerBoundaryTimer{channel: make(chan time.Time, 1)}
		source := &workerBoundaryClock{
			now:   func(call int32) time.Time { return base.Add(time.Duration(call - 1)) },
			timer: func(int32, time.Time) Timer { return timer },
		}
		boundary, _, _ := newWorkerBoundary(t, source)
		started := make(chan struct{})
		release := make(chan struct{})
		result := make(chan workerDriverCall, 1)
		go func() {
			_, call := invokeWorkerDriver(boundary, context.Background(), func(context.Context) (int, error) {
				close(started)
				<-release
				return 71, nil
			}, func(value int) (int, error) { return value, nil })
			result <- call
		}()
		<-started
		close(timer.channel)
		close(release)
		call := <-result
		if call.outcome != WorkerOutcomeFailed || call.failure != WorkerFailureRuntime || call.err != ErrInvalid || !call.started || !call.uncertain {
			t.Fatalf("iteration %d lost invalid timer: %#v", iteration, call)
		}
	}
}

func TestWorkerDriverBoundaryRejectsEarlyTimerReportedByStop(t *testing.T) {
	base := time.Date(2034, 5, 6, 7, 8, 9, 0, time.UTC)
	timer := &workerBoundaryTimer{channel: make(chan time.Time, 1), stopSend: true, stopFalse: true}
	source := &workerBoundaryClock{
		now:   func(call int32) time.Time { return base.Add(time.Duration(call - 1)) },
		timer: func(int32, time.Time) Timer { return timer },
	}
	boundary, _, _ := newWorkerBoundary(t, source)
	value, call := invokeWorkerDriver(boundary, context.Background(), func(context.Context) (int, error) {
		return 71, nil
	}, func(value int) (int, error) { return value, nil })
	if value != 0 || call.outcome != WorkerOutcomeFailed || call.failure != WorkerFailureRuntime || call.err != ErrInvalid || !call.started || !call.uncertain || timer.stopCalls.Load() != 1 {
		t.Fatalf("early Stop timer = value %d %#v stops %d", value, call, timer.stopCalls.Load())
	}
}

func TestWorkerDriverBoundaryClassifiesExpiredTimerReportedByStop(t *testing.T) {
	base := time.Date(2034, 5, 6, 7, 8, 9, 0, time.UTC)
	timeout := MinimumOperationTimeout
	timer := &workerBoundaryTimer{channel: make(chan time.Time, 1), stopSend: true, stopFalse: true}
	source := &workerBoundaryClock{
		now: func(call int32) time.Time {
			if call <= 3 {
				return base
			}
			return base.Add(timeout)
		},
		timer: func(int32, time.Time) Timer { return timer },
	}
	boundary, _, _ := newWorkerBoundary(t, source)
	value, call := invokeWorkerDriver(boundary, context.Background(), func(context.Context) (int, error) {
		return 71, nil
	}, func(value int) (int, error) { return value, nil })
	if value != 0 || call.outcome != WorkerOutcomeTimedOut || call.failure != WorkerFailureNone || call.err != context.DeadlineExceeded || !call.started || !call.uncertain || timer.stopCalls.Load() != 1 {
		t.Fatalf("expired Stop timer = value %d %#v stops %d", value, call, timer.stopCalls.Load())
	}
	if _, failed := boundary.fatal.load(); failed {
		t.Fatal("expired operation timer latched fatal failure")
	}
}

func TestWorkerDriverBoundaryContainsClockFailures(t *testing.T) {
	base := time.Date(2034, 5, 6, 7, 8, 9, 0, time.UTC)
	tests := []struct {
		name      string
		now       func(int32) time.Time
		wantCalls int32
		started   bool
	}{
		{name: "start panic", now: func(int32) time.Time { panic("private clock panic") }},
		{name: "finish regression", now: func(call int32) time.Time {
			if call <= 3 {
				return base
			}
			return base.Add(-time.Nanosecond)
		}, wantCalls: 1, started: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			timer := &workerBoundaryTimer{channel: make(chan time.Time)}
			source := &workerBoundaryClock{now: test.now, timer: func(int32, time.Time) Timer { return timer }}
			boundary, _, _ := newWorkerBoundary(t, source)
			var calls atomic.Int32
			value, call := invokeWorkerDriver(boundary, context.Background(), func(context.Context) (int, error) {
				calls.Add(1)
				return 71, nil
			}, func(value int) (int, error) { return value, nil })
			if value != 0 || call.outcome != WorkerOutcomeFailed || call.failure != WorkerFailureRuntime || call.err != ErrInvalid || call.started != test.started || calls.Load() != test.wantCalls {
				t.Fatalf("clock failure = value %d %#v calls %d", value, call, calls.Load())
			}
		})
	}
}

func TestWorkerDriverBoundarySkipsDriverWhenParentCancelsDuringTimerSetup(t *testing.T) {
	base := time.Date(2034, 5, 6, 7, 8, 9, 0, time.UTC)
	parent, cancel := context.WithCancel(context.Background())
	timer := &workerBoundaryTimer{channel: make(chan time.Time)}
	source := &workerBoundaryClock{
		now: func(call int32) time.Time { return base.Add(time.Duration(call - 1)) },
		timer: func(int32, time.Time) Timer {
			cancel()
			return timer
		},
	}
	boundary, _, _ := newWorkerBoundary(t, source)
	var invoked atomic.Int32
	value, call := invokeWorkerDriver(boundary, parent, func(context.Context) (int, error) {
		invoked.Add(1)
		return 71, nil
	}, func(value int) (int, error) { return value, nil })
	if value != 0 || call.outcome != WorkerOutcomeCancelled || call.err != context.Canceled || call.started || call.uncertain || invoked.Load() != 0 || timer.stopCalls.Load() != 1 {
		t.Fatalf("setup cancellation = value %d %#v calls %d stops %d", value, call, invoked.Load(), timer.stopCalls.Load())
	}

	parent, cancel = context.WithCancel(context.Background())
	panicTimer := &workerBoundaryTimer{channel: make(chan time.Time), stopPanic: true}
	panicSource := &workerBoundaryClock{
		now: func(call int32) time.Time { return base.Add(time.Duration(call - 1)) },
		timer: func(int32, time.Time) Timer {
			cancel()
			return panicTimer
		},
	}
	panicBoundary, _, _ := newWorkerBoundary(t, panicSource)
	value, call = invokeWorkerDriver(panicBoundary, parent, func(context.Context) (int, error) {
		invoked.Add(1)
		return 71, nil
	}, func(value int) (int, error) { return value, nil })
	if value != 0 || call.outcome != WorkerOutcomeFailed || call.failure != WorkerFailureRuntime || call.err != ErrInvalid || call.started || call.uncertain || invoked.Load() != 0 || panicTimer.stopCalls.Load() != 1 {
		t.Fatalf("setup cancellation cleanup failure = value %d %#v calls %d stops %d", value, call, invoked.Load(), panicTimer.stopCalls.Load())
	}
}

func TestWorkerDriverBoundaryPreservesStartedOnLateContextPanic(t *testing.T) {
	boundary, _, timers := newWorkerBoundary(t, nil)
	ctx := &workerBoundaryLateErrContext{panicAt: 6}
	value, call := invokeWorkerDriver(boundary, ctx, func(context.Context) (int, error) {
		return 71, nil
	}, func(value int) (int, error) { return value, nil })
	if value != 0 || call.outcome != WorkerOutcomeFailed || call.failure != WorkerFailureRuntime || call.err != ErrInvalid || !call.started || !call.uncertain || len(*timers) != 1 || (*timers)[0].stopCalls.Load() != 1 {
		t.Fatalf("late context panic = value %d %#v", value, call)
	}
}

func TestWorkerDriverBoundaryCleansUpGoexit(t *testing.T) {
	boundary, _, timers := newWorkerBoundary(t, nil)
	returned := &atomic.Bool{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = invokeWorkerDriver(boundary, context.Background(), func(context.Context) (int, error) {
			runtime.Goexit()
			return 0, nil
		}, func(value int) (int, error) { return value, nil })
		returned.Store(true)
	}()
	<-done
	if returned.Load() {
		t.Fatal("Goexit driver returned")
	}
	record, failed := boundary.fatal.load()
	if !failed || record.failure != WorkerFailureDriverContract || record.err != ErrDriverContract {
		t.Fatalf("Goexit fatal record = (%#v, %t)", record, failed)
	}
	if len(*timers) != 1 || (*timers)[0].stopCalls.Load() != 1 {
		t.Fatalf("Goexit timers = %d", len(*timers))
	}
}

func TestWorkerDriverBoundaryDetectsTimerStopGoexit(t *testing.T) {
	base := time.Date(2034, 5, 6, 7, 8, 9, 0, time.UTC)
	timer := &workerBoundaryTimer{channel: make(chan time.Time), stopGoexit: true}
	source := &workerBoundaryClock{
		now:   func(call int32) time.Time { return base.Add(time.Duration(call - 1)) },
		timer: func(int32, time.Time) Timer { return timer },
	}
	boundary, _, _ := newWorkerBoundary(t, source)
	value, call := invokeWorkerDriver(boundary, context.Background(), func(context.Context) (int, error) {
		return 71, nil
	}, func(value int) (int, error) { return value, nil })
	if value != 0 || call.outcome != WorkerOutcomeFailed || call.failure != WorkerFailureRuntime || call.err != ErrInvalid || timer.stopCalls.Load() != 1 {
		t.Fatalf("Stop Goexit = value %d %#v stops %d", value, call, timer.stopCalls.Load())
	}
}

func TestWorkerDriverBoundaryDetectsTimerStopPanic(t *testing.T) {
	base := time.Date(2034, 5, 6, 7, 8, 9, 0, time.UTC)
	timer := &workerBoundaryTimer{channel: make(chan time.Time), stopPanic: true}
	source := &workerBoundaryClock{
		now:   func(call int32) time.Time { return base.Add(time.Duration(call - 1)) },
		timer: func(int32, time.Time) Timer { return timer },
	}
	boundary, _, _ := newWorkerBoundary(t, source)
	value, call := invokeWorkerDriver(boundary, context.Background(), func(context.Context) (int, error) {
		return 71, nil
	}, func(value int) (int, error) { return value, nil })
	if value != 0 || call.outcome != WorkerOutcomeFailed || call.failure != WorkerFailureRuntime || call.err != ErrInvalid || timer.stopCalls.Load() != 1 {
		t.Fatalf("Stop panic = value %d %#v stops %d", value, call, timer.stopCalls.Load())
	}
}

func TestWorkerDriverBoundaryArbitratesConcurrentFatalFailures(t *testing.T) {
	base := time.Date(2034, 5, 6, 7, 8, 9, 0, time.UTC)
	timer := &workerBoundaryTimer{channel: make(chan time.Time), stopPanic: true}
	source := &workerBoundaryClock{
		now:   func(call int32) time.Time { return base.Add(time.Duration(call - 1)) },
		timer: func(int32, time.Time) Timer { return timer },
	}
	boundary, _, _ := newWorkerBoundary(t, source)
	value, call := invokeWorkerDriver(boundary, context.Background(), func(context.Context) (int, error) {
		panic("private driver panic")
	}, func(value int) (int, error) { return value, nil })
	record, failed := boundary.fatal.load()
	if value != 0 || !failed || call.failure != record.failure || call.err != record.err || call.failure != WorkerFailureDriverPanic || call.err != ErrDriver {
		t.Fatalf("fatal arbitration = value %d call %#v record %#v loaded %t", value, call, record, failed)
	}
}

func TestWorkerDriverBoundaryNeverStartsAfterFatalLatch(t *testing.T) {
	boundary, source, timers := newWorkerBoundary(t, nil)
	boundary.fatal.fail(WorkerFailureDriverContract, ErrDriverContract)
	var invoked atomic.Int32
	value, call := invokeWorkerDriver(boundary, context.Background(), func(context.Context) (int, error) {
		invoked.Add(1)
		return 71, nil
	}, func(value int) (int, error) { return value, nil })
	if value != 0 || call.failure != WorkerFailureDriverContract || call.err != ErrDriverContract || call.started || call.uncertain || invoked.Load() != 0 || source.nowCalls.Load() != 0 || source.timerCalls.Load() != 0 || len(*timers) != 0 {
		t.Fatalf("pre-latched call = value %d %#v driver %d now %d timer %d", value, call, invoked.Load(), source.nowCalls.Load(), source.timerCalls.Load())
	}

	latch := newWorkerFailureLatch()
	timer := &workerBoundaryTimer{channel: make(chan time.Time)}
	base := time.Date(2034, 5, 6, 7, 8, 9, 0, time.UTC)
	duringSource := &workerBoundaryClock{
		now: func(call int32) time.Time { return base.Add(time.Duration(call - 1)) },
		timer: func(int32, time.Time) Timer {
			latch.fail(WorkerFailureDriverPanic, ErrDriver)
			return timer
		},
	}
	during, _, _ := newWorkerBoundary(t, duringSource)
	during.fatal = latch
	value, call = invokeWorkerDriver(during, context.Background(), func(context.Context) (int, error) {
		invoked.Add(1)
		return 71, nil
	}, func(value int) (int, error) { return value, nil })
	if value != 0 || call.failure != WorkerFailureDriverPanic || call.err != ErrDriver || call.started || call.uncertain || invoked.Load() != 0 || timer.stopCalls.Load() != 1 {
		t.Fatalf("setup-latched call = value %d %#v driver %d stops %d", value, call, invoked.Load(), timer.stopCalls.Load())
	}
}

func TestWorkerDriverTypedRequestFailureIsFatalBeforeDriver(t *testing.T) {
	var calls atomic.Int32
	driver := &workerBoundaryDriver{
		description: queueTestBackendDescription(1),
		claim: func(context.Context, ClaimRequest) (ClaimBatch, error) {
			calls.Add(1)
			return ClaimBatch{}, nil
		},
		renew:   func(context.Context, RenewRequest) (RenewResult, error) { panic("unexpected") },
		apply:   func(context.Context, ApplyRequest) (ApplyResult, error) { panic("unexpected") },
		recover: func(context.Context, RecoverRequest) (RecoverResult, error) { panic("unexpected") },
	}
	fixtures := newWorkerBoundaryFixtures(t)
	workers, source, _ := newWorkerBoundaryWorkers(t, driver, fixtures)
	result, call := workers.callClaim(context.Background(), ClaimRequest{})
	if result.Len() != 0 || call.outcome != WorkerOutcomeFailed || call.failure != WorkerFailureRuntime || call.err != ErrInvalid || call.started || call.uncertain || calls.Load() != 0 || source.nowCalls.Load() != 0 || source.timerCalls.Load() != 0 {
		t.Fatalf("invalid request = %#v calls %d", call, calls.Load())
	}
}

func TestWorkerDriverTypedCallsRejectForeignScopeAndBudgetsBeforeDriver(t *testing.T) {
	fixtures := newWorkerBoundaryFixtures(t)
	foreignNamespace := queueTestNamespace(t, "foreign-worker-app")
	foreignClaim := fixtures.claimRequest
	foreignClaim.namespace = foreignNamespace
	foreignRecover := fixtures.recoverRequest
	foreignRecover.namespace = foreignNamespace
	foreignLease, err := NewLeaseRef(queueTestBackendID(2), fixtures.renewRequest.Leases()[0].InvocationID(), []byte("foreign-boundary"))
	if err != nil {
		t.Fatal(err)
	}
	foreignRenew, err := NewRenewRequest([]LeaseRef{foreignLease}, DefaultLeaseTTL)
	if err != nil {
		t.Fatal(err)
	}
	foreignCommand, err := RejectCorruptCommand(foreignLease)
	if err != nil {
		t.Fatal(err)
	}
	foreignApply, err := NewApplyRequest(foreignCommand)
	if err != nil {
		t.Fatal(err)
	}
	wrongClaimTTL := fixtures.claimRequest
	wrongClaimTTL.leaseTTL = MinimumLeaseTTL
	wrongRenewTTL := fixtures.renewRequest
	wrongRenewTTL.leaseTTL = MinimumLeaseTTL
	wrongRecoverTTL := fixtures.recoverRequest
	wrongRecoverTTL.leaseTTL = MinimumLeaseTTL
	claimWith := func(change func(*ClaimTarget)) ClaimRequest {
		request := fixtures.claimRequest
		request.targets = cloneClaimTargets(request.targets)
		change(&request.targets[0])
		return request
	}
	foreignDefinitionClaim := claimWith(func(target *ClaimTarget) { target.definition = testJobName(t, "worker.foreign") })
	foreignBindingClaim := claimWith(func(target *ClaimTarget) { target.binding = mustDriverBinding(t, "worker.foreign") })
	foreignBuild, err := ParseBuildID("deploy:foreign")
	if err != nil {
		t.Fatal(err)
	}
	foreignBuildClaim := claimWith(func(target *ClaimTarget) { target.build = foreignBuild })
	foreignRevisionClaim := claimWith(func(target *ClaimTarget) { target.revisions[0].version++ })
	foreignAvailabilityClaim := claimWith(func(target *ClaimTarget) { target.available++ })
	claimItemBudget := claimWith(func(target *ClaimTarget) { target.available++ })
	claimItemBudget.maxItems++
	claimByteBudget := fixtures.claimRequest
	claimByteBudget.maxBytes++
	recoverByteBudget := fixtures.recoverRequest
	recoverByteBudget.maxBytes++
	foreignBegin, err := BeginAttemptCommand(fixtures.renewRequest.Leases()[0], mustDriverBinding(t, "worker.foreign"), fixtures.build)
	if err != nil {
		t.Fatal(err)
	}
	foreignBuildBegin, err := BeginAttemptCommand(fixtures.renewRequest.Leases()[0], fixtures.claimRequest.targets[0].binding, foreignBuild)
	if err != nil {
		t.Fatal(err)
	}
	foreignCompatibility, err := ReleaseUnchangedCommand(fixtures.renewRequest.Leases()[0], mustDriverBinding(t, "worker.foreign"), fixtures.build, MinRetryDelay)
	if err != nil {
		t.Fatal(err)
	}
	foreignBuildCompatibility, err := ReleaseUnchangedCommand(fixtures.renewRequest.Leases()[0], fixtures.claimRequest.targets[0].binding, foreignBuild, MinRetryDelay)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name      string
		configure func(*Workers)
		call      func(*Workers) workerDriverCall
	}{
		{name: "claim namespace", call: func(workers *Workers) workerDriverCall {
			_, call := workers.callClaim(context.Background(), foreignClaim)
			return call
		}},
		{name: "claim definition", call: func(workers *Workers) workerDriverCall {
			_, call := workers.callClaim(context.Background(), foreignDefinitionClaim)
			return call
		}},
		{name: "claim binding", call: func(workers *Workers) workerDriverCall {
			_, call := workers.callClaim(context.Background(), foreignBindingClaim)
			return call
		}},
		{name: "claim build", call: func(workers *Workers) workerDriverCall {
			_, call := workers.callClaim(context.Background(), foreignBuildClaim)
			return call
		}},
		{name: "claim revisions", call: func(workers *Workers) workerDriverCall {
			_, call := workers.callClaim(context.Background(), foreignRevisionClaim)
			return call
		}},
		{name: "claim availability", call: func(workers *Workers) workerDriverCall {
			_, call := workers.callClaim(context.Background(), foreignAvailabilityClaim)
			return call
		}},
		{name: "recover namespace", call: func(workers *Workers) workerDriverCall {
			_, call := workers.callRecover(context.Background(), foreignRecover)
			return call
		}},
		{name: "renew backend", call: func(workers *Workers) workerDriverCall {
			_, call := workers.callRenew(context.Background(), foreignRenew)
			return call
		}},
		{name: "apply backend", call: func(workers *Workers) workerDriverCall {
			_, call := workers.callApply(context.Background(), foreignApply)
			return call
		}},
		{name: "apply binding", call: func(workers *Workers) workerDriverCall {
			_, call := workers.callApply(context.Background(), ApplyRequest{command: foreignBegin})
			return call
		}},
		{name: "apply build", call: func(workers *Workers) workerDriverCall {
			_, call := workers.callApply(context.Background(), ApplyRequest{command: foreignBuildBegin})
			return call
		}},
		{name: "compatibility binding", call: func(workers *Workers) workerDriverCall {
			_, call := workers.callApply(context.Background(), ApplyRequest{command: foreignCompatibility})
			return call
		}},
		{name: "compatibility build", call: func(workers *Workers) workerDriverCall {
			_, call := workers.callApply(context.Background(), ApplyRequest{command: foreignBuildCompatibility})
			return call
		}},
		{name: "claim ttl", call: func(workers *Workers) workerDriverCall {
			_, call := workers.callClaim(context.Background(), wrongClaimTTL)
			return call
		}},
		{name: "renew ttl", call: func(workers *Workers) workerDriverCall {
			_, call := workers.callRenew(context.Background(), wrongRenewTTL)
			return call
		}},
		{name: "recover ttl", call: func(workers *Workers) workerDriverCall {
			_, call := workers.callRecover(context.Background(), wrongRecoverTTL)
			return call
		}},
		{name: "claim item budget", configure: func(workers *Workers) { workers.plan.bindings[0].concurrency = 2 }, call: func(workers *Workers) workerDriverCall {
			_, call := workers.callClaim(context.Background(), claimItemBudget)
			return call
		}},
		{name: "claim byte budget", call: func(workers *Workers) workerDriverCall {
			_, call := workers.callClaim(context.Background(), claimByteBudget)
			return call
		}},
		{name: "recover item budget", call: func(workers *Workers) workerDriverCall {
			_, call := workers.callRecover(context.Background(), fixtures.recoverRequest)
			return call
		}},
		{name: "recover byte budget", call: func(workers *Workers) workerDriverCall {
			_, call := workers.callRecover(context.Background(), recoverByteBudget)
			return call
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var calls atomic.Int32
			driver := &workerBoundaryDriver{
				description: queueTestBackendDescription(1),
				claim: func(context.Context, ClaimRequest) (ClaimBatch, error) {
					calls.Add(1)
					return fixtures.claimResult, nil
				},
				renew: func(context.Context, RenewRequest) (RenewResult, error) {
					calls.Add(1)
					return fixtures.renewResult, nil
				},
				apply: func(context.Context, ApplyRequest) (ApplyResult, error) {
					calls.Add(1)
					return fixtures.applyResult, nil
				},
				recover: func(context.Context, RecoverRequest) (RecoverResult, error) {
					calls.Add(1)
					return fixtures.recoverResult, nil
				},
			}
			workers, source, timers := newWorkerBoundaryWorkers(t, driver, fixtures)
			workers.config.claimItems = 1
			workers.config.claimBytes = MaxDeliveryRecordBytes
			if test.configure != nil {
				test.configure(workers)
			}
			call := test.call(workers)
			if call.outcome != WorkerOutcomeFailed || call.failure != WorkerFailureRuntime || call.err != ErrInvalid || call.started || call.uncertain {
				t.Fatalf("preflight call = %#v", call)
			}
			if calls.Load() != 0 || source.nowCalls.Load() != 0 || source.timerCalls.Load() != 0 || len(*timers) != 0 {
				t.Fatalf("preflight effects = driver %d now %d timer %d", calls.Load(), source.nowCalls.Load(), source.timerCalls.Load())
			}
		})
	}
}

func TestWorkerDriverClaimRequiresExactUpcastRevisionVector(t *testing.T) {
	v1 := String(1)
	v2 := Bytes(2)
	v3 := String(3)
	definition := MustDefine(DefinitionSpec[string]{
		Name:  testJobName(t, "worker.revisioned"),
		Codec: v3,
		Upcasters: []Upcaster{
			Upcast(v2, v3, func(value []byte) (string, error) { return string(value), nil }),
			Upcast(v1, v2, func(value string) ([]byte, error) { return []byte(value), nil }),
		},
		Policy: testPolicy(t),
	})
	catalog := MustCatalog(definition)
	binding := mustDriverBinding(t, "worker.revisioned")
	consumer := On(definition, Handler[string](func(context.Context, string) error { return nil }), Binding(binding.String()), Concurrency(1))
	build := testBuildID(t)
	description := definition.Describe().Codec
	revisions := make([]PayloadRevision, 0, len(description.Upcasts)+1)
	for _, upcast := range description.Upcasts {
		revisions = append(revisions, PayloadRevision{codec: upcast.SourceCodec, version: upcast.From})
	}
	revisions = append(revisions, PayloadRevision{codec: description.ID, version: description.CurrentVersion})
	target, err := NewClaimTarget(ClaimTargetSpec{
		Definition:         definition.Name(),
		Binding:            binding,
		Build:              build,
		SupportedRevisions: revisions,
		Available:          1,
	})
	if err != nil {
		t.Fatal(err)
	}
	namespace := queueTestNamespace(t, "worker-revisioned")
	request, err := NewClaimRequest(ClaimRequestSpec{
		Namespace:   namespace,
		Incarnation: driverTestWorkerIncarnation(t),
		Targets:     []ClaimTarget{target},
		MaxItems:    1,
		MaxBytes:    MaxDeliveryRecordBytes,
		LeaseTTL:    DefaultLeaseTTL,
	})
	if err != nil {
		t.Fatal(err)
	}
	batch, err := NewClaimBatch(time.Date(2034, 5, 6, 7, 8, 9, 0, time.UTC), nil)
	if err != nil {
		t.Fatal(err)
	}
	fixtures := workerBoundaryFixtures{catalog: catalog, consumer: consumer, build: build, claimRequest: request, claimResult: batch}
	newDriver := func(calls *atomic.Int32) *workerBoundaryDriver {
		return &workerBoundaryDriver{
			description: queueTestBackendDescription(1),
			claim: func(context.Context, ClaimRequest) (ClaimBatch, error) {
				calls.Add(1)
				return batch, nil
			},
			renew:   func(context.Context, RenewRequest) (RenewResult, error) { panic("unexpected renew") },
			apply:   func(context.Context, ApplyRequest) (ApplyResult, error) { panic("unexpected apply") },
			recover: func(context.Context, RecoverRequest) (RecoverResult, error) { panic("unexpected recover") },
		}
	}
	var calls atomic.Int32
	workers, _, _ := newWorkerBoundaryWorkers(t, newDriver(&calls), fixtures)
	claimed, call := workers.callClaim(context.Background(), request)
	if claimed.Len() != 0 || call.outcome != WorkerOutcomeComplete || !call.started || call.uncertain || calls.Load() != 1 {
		t.Fatalf("exact revisions = result %d call %#v driver %d", claimed.Len(), call, calls.Load())
	}

	tests := []struct {
		name   string
		change func(*ClaimTarget)
	}{
		{name: "subset", change: func(target *ClaimTarget) { target.revisions = append([]PayloadRevision(nil), target.revisions[1:]...) }},
		{name: "superset", change: func(target *ClaimTarget) {
			target.revisions = append(target.revisions, PayloadRevision{codec: description.ID, version: description.CurrentVersion + 1})
		}},
		{name: "wrong source codec", change: func(target *ClaimTarget) { target.revisions[0].codec = v2.ID() }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			invalid := request
			invalid.targets = cloneClaimTargets(request.targets)
			test.change(&invalid.targets[0])
			var invalidCalls atomic.Int32
			workers, source, timers := newWorkerBoundaryWorkers(t, newDriver(&invalidCalls), fixtures)
			_, call := workers.callClaim(context.Background(), invalid)
			if call.outcome != WorkerOutcomeFailed || call.failure != WorkerFailureRuntime || call.err != ErrInvalid || call.started || call.uncertain {
				t.Fatalf("invalid revisions = %#v", call)
			}
			if invalidCalls.Load() != 0 || source.nowCalls.Load() != 0 || source.timerCalls.Load() != 0 || len(*timers) != 0 {
				t.Fatalf("invalid effects = driver %d now %d timer %d", invalidCalls.Load(), source.nowCalls.Load(), source.timerCalls.Load())
			}
		})
	}
}

func TestWorkerDriverTypedCallsPreserveCertainLeaseLossAndAmbiguity(t *testing.T) {
	fixtures := newWorkerBoundaryFixtures(t)
	previous := fixtures.renewRequest.Leases()[0]
	lost, err := NewLeaseRenewal(previous, LeaseRef{}, DeliveryMutationLeaseLost, DeliveryControlNone)
	if err != nil {
		t.Fatal(err)
	}
	renewResult, err := NewRenewResult(fixtures.renewResult.ObservedAt(), []LeaseRenewal{lost})
	if err != nil {
		t.Fatal(err)
	}
	ambiguousMutation, err := NewDeliveryCommandResult(DeliveryMutationAmbiguous, DeliveryControlNone)
	if err != nil {
		t.Fatal(err)
	}
	applyResult, err := NewApplyResult(time.Time{}, ambiguousMutation, DeliveryApplication{})
	if err != nil {
		t.Fatal(err)
	}
	driver := &workerBoundaryDriver{
		description: queueTestBackendDescription(1),
		claim:       func(context.Context, ClaimRequest) (ClaimBatch, error) { panic("unexpected claim") },
		renew:       func(context.Context, RenewRequest) (RenewResult, error) { return renewResult, nil },
		apply:       func(context.Context, ApplyRequest) (ApplyResult, error) { return applyResult, nil },
		recover:     func(context.Context, RecoverRequest) (RecoverResult, error) { panic("unexpected recover") },
	}
	workers, _, _ := newWorkerBoundaryWorkers(t, driver, fixtures)
	renewed, renewCall := workers.callRenew(context.Background(), fixtures.renewRequest)
	applied, applyCall := workers.callApply(context.Background(), fixtures.applyRequest)
	if renewCall.outcome != WorkerOutcomeComplete || applyCall.outcome != WorkerOutcomeComplete || renewed.Items()[0].Mutation() != DeliveryMutationLeaseLost || applied.Result().Mutation() != DeliveryMutationAmbiguous {
		t.Fatalf("certain non-applied results = renew %#v/%#v apply %#v/%#v", renewed, renewCall, applied, applyCall)
	}
}

func TestWorkerDriverTypedCallsRejectEveryUnvalidatedResult(t *testing.T) {
	fixtures := newWorkerBoundaryFixtures(t)
	tests := []struct {
		name string
		call func(*Workers) workerDriverCall
	}{
		{name: "claim", call: func(workers *Workers) workerDriverCall {
			_, call := workers.callClaim(context.Background(), fixtures.claimRequest)
			return call
		}},
		{name: "renew", call: func(workers *Workers) workerDriverCall {
			_, call := workers.callRenew(context.Background(), fixtures.renewRequest)
			return call
		}},
		{name: "apply", call: func(workers *Workers) workerDriverCall {
			_, call := workers.callApply(context.Background(), fixtures.applyRequest)
			return call
		}},
		{name: "recover", call: func(workers *Workers) workerDriverCall {
			_, call := workers.callRecover(context.Background(), fixtures.recoverRequest)
			return call
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			driver := &workerBoundaryDriver{
				description: queueTestBackendDescription(1),
				claim:       func(context.Context, ClaimRequest) (ClaimBatch, error) { return ClaimBatch{}, nil },
				renew:       func(context.Context, RenewRequest) (RenewResult, error) { return RenewResult{}, nil },
				apply:       func(context.Context, ApplyRequest) (ApplyResult, error) { return ApplyResult{}, nil },
				recover:     func(context.Context, RecoverRequest) (RecoverResult, error) { return RecoverResult{}, nil },
			}
			workers, _, _ := newWorkerBoundaryWorkers(t, driver, fixtures)
			call := test.call(workers)
			if call.outcome != WorkerOutcomeFailed || call.failure != WorkerFailureDriverContract || call.err != ErrDriverContract || !call.fatal() || !call.started || !call.uncertain {
				t.Fatalf("invalid typed result = %#v", call)
			}
			record, failed := workers.fatal.load()
			if !failed || record.failure != WorkerFailureDriverContract || record.err != ErrDriverContract {
				t.Fatalf("fatal record = (%#v, %t)", record, failed)
			}
		})
	}
}

func TestWorkerDriverTypedCallUsesPublicWorkersConstruction(t *testing.T) {
	fixtures := newWorkerBoundaryFixtures(t)
	driver := &workerBoundaryDriver{
		description: queueTestBackendDescription(1),
		claim:       func(context.Context, ClaimRequest) (ClaimBatch, error) { return fixtures.claimResult, nil },
		renew:       func(context.Context, RenewRequest) (RenewResult, error) { panic("unexpected renew") },
		apply:       func(context.Context, ApplyRequest) (ApplyResult, error) { panic("unexpected apply") },
		recover:     func(context.Context, RecoverRequest) (RecoverResult, error) { panic("unexpected recover") },
	}
	_, source, timers := newWorkerBoundary(t, nil)
	workers, err := NewWorkers(WorkersSpec{
		Namespace: fixtures.claimRequest.Namespace(),
		Catalog:   fixtures.catalog,
		Driver:    driver,
		Build:     fixtures.build,
		Identity:  workerDeliveryIdentityRestorer(t),
		Clock:     source,
	}, fixtures.consumer)
	if err != nil {
		t.Fatal(err)
	}
	result, call := workers.callClaim(context.Background(), fixtures.claimRequest)
	if result.Len() != 1 || call.outcome != WorkerOutcomeComplete || !call.started || call.uncertain || call.err != nil || workers.fatal == nil || len(*timers) != 1 || (*timers)[0].stopCalls.Load() != 1 {
		t.Fatalf("constructed call = result %d %#v timers %d", result.Len(), call, len(*timers))
	}
}

func newWorkerBoundaryFixtures(t *testing.T) workerBoundaryFixtures {
	t.Helper()
	catalog, definition, invocation, _, record := deliveryRecordFixture(t, PlacementRegular)
	target := driverClaimTarget(t, definition.Name(), record.Payload.Codec, record.Payload.Version, 1, "worker.boundary")
	consumer := On(definition, Handler[string](func(context.Context, string) error { return nil }), Binding(target.Binding().String()), Concurrency(1))
	claimRequest := mustClaimRequest(t, record.Genesis.Namespace, []ClaimTarget{target}, 1, MaxDeliveryRecordBytes)
	lease := deliveryTestLease(t, invocation.ID(), []byte("boundary-lease"))
	claimed, err := NewClaimedDelivery(target, lease, record)
	if err != nil {
		t.Fatal(err)
	}
	claimResult, err := NewClaimBatch(record.Genesis.EligibleAt, []ClaimedDelivery{claimed})
	if err != nil {
		t.Fatal(err)
	}
	renewRequest, err := NewRenewRequest([]LeaseRef{lease}, DefaultLeaseTTL)
	if err != nil {
		t.Fatal(err)
	}
	current, err := NewLeaseRef(lease.Backend(), invocation.ID(), []byte("boundary-rotated"))
	if err != nil {
		t.Fatal(err)
	}
	renewal, err := NewLeaseRenewal(lease, current, DeliveryMutationApplied, DeliveryControlNone)
	if err != nil {
		t.Fatal(err)
	}
	renewResult, err := NewRenewResult(record.Genesis.EligibleAt, []LeaseRenewal{renewal})
	if err != nil {
		t.Fatal(err)
	}
	command, err := RejectCorruptCommand(lease)
	if err != nil {
		t.Fatal(err)
	}
	applyRequest, err := NewApplyRequest(command)
	if err != nil {
		t.Fatal(err)
	}
	application, err := ApplyDeliveryCommand(Invocation{}, command, record.Genesis.EligibleAt)
	if err != nil {
		t.Fatal(err)
	}
	commandResult, err := NewDeliveryCommandResult(DeliveryMutationApplied, DeliveryControlNone)
	if err != nil {
		t.Fatal(err)
	}
	applyResult, err := NewApplyResult(record.Genesis.EligibleAt, commandResult, application)
	if err != nil {
		t.Fatal(err)
	}
	recoverRequest, err := NewRecoverRequest(RecoverRequestSpec{Namespace: record.Genesis.Namespace, Incarnation: driverTestWorkerIncarnation(t), MaxItems: 2, MaxBytes: MaxDeliveryRecordBytes, LeaseTTL: DefaultLeaseTTL})
	if err != nil {
		t.Fatal(err)
	}
	recovered, err := NewRecoveredDelivery(lease, record)
	if err != nil {
		t.Fatal(err)
	}
	recoverResult, err := NewRecoverResult(record.Genesis.EligibleAt, []RecoveredDelivery{recovered}, 1, false)
	if err != nil {
		t.Fatal(err)
	}
	return workerBoundaryFixtures{
		catalog:        catalog,
		consumer:       consumer,
		build:          target.Build(),
		claimRequest:   claimRequest,
		claimResult:    claimResult,
		renewRequest:   renewRequest,
		renewResult:    renewResult,
		applyRequest:   applyRequest,
		applyResult:    applyResult,
		recoverRequest: recoverRequest,
		recoverResult:  recoverResult,
	}
}

func newWorkerBoundaryWorkers(t *testing.T, driver DeliveryDriver, fixtures workerBoundaryFixtures) (*Workers, *workerBoundaryClock, *[]*workerBoundaryTimer) {
	t.Helper()
	boundary, source, timers := newWorkerBoundary(t, nil)
	boundary.driver = driver
	plan, err := NewWorkerPlan(fixtures.catalog, fixtures.consumer)
	if err != nil {
		t.Fatal(err)
	}
	workers := &Workers{
		config: resolvedWorkersConfig{
			namespace:        fixtures.claimRequest.Namespace(),
			catalog:          fixtures.catalog,
			driver:           driver,
			backend:          boundary.backend,
			build:            fixtures.build,
			clock:            boundary.clock,
			operationTimeout: boundary.timeout,
			leaseTTL:         boundary.leaseTTL,
			claimItems:       boundary.claimItems,
			claimBytes:       boundary.claimBytes,
		},
		plan:  plan,
		fatal: boundary.fatal,
	}
	return workers, source, timers
}

func newWorkerBoundary(t *testing.T, source *workerBoundaryClock) (workerDriverBoundary, *workerBoundaryClock, *[]*workerBoundaryTimer) {
	t.Helper()
	base := time.Date(2034, 5, 6, 7, 8, 9, 0, time.UTC)
	timers := &[]*workerBoundaryTimer{}
	var timersMu sync.Mutex
	if source == nil {
		source = &workerBoundaryClock{
			now: func(call int32) time.Time {
				return base.Add(time.Duration(call-1) * time.Microsecond)
			},
			timer: func(_ int32, _ time.Time) Timer {
				timer := &workerBoundaryTimer{channel: make(chan time.Time)}
				timersMu.Lock()
				*timers = append(*timers, timer)
				timersMu.Unlock()
				return timer
			},
		}
	}
	clock, err := newWorkerClock(source)
	if err != nil {
		t.Fatal(err)
	}
	driver := &workerBoundaryDriver{
		description: queueTestBackendDescription(1),
		claim:       func(context.Context, ClaimRequest) (ClaimBatch, error) { panic("unexpected claim") },
		renew:       func(context.Context, RenewRequest) (RenewResult, error) { panic("unexpected renew") },
		apply:       func(context.Context, ApplyRequest) (ApplyResult, error) { panic("unexpected apply") },
		recover:     func(context.Context, RecoverRequest) (RecoverResult, error) { panic("unexpected recover") },
	}
	return workerDriverBoundary{
		namespace:  queueTestNamespace(t, "worker-boundary"),
		driver:     driver,
		backend:    driver.description,
		clock:      clock,
		timeout:    MinimumOperationTimeout,
		leaseTTL:   DefaultLeaseTTL,
		claimItems: MaxClaimItems,
		claimBytes: MaxClaimBytes,
		fatal:      newWorkerFailureLatch(),
	}, source, timers
}
