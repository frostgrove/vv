package jobs

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"
)

type workerDriverCall struct {
	outcome   WorkerOutcome
	failure   WorkerFailure
	elapsed   time.Duration
	err       error
	started   bool
	uncertain bool
}

func (call workerDriverCall) fatal() bool {
	return call.failure == WorkerFailureDriverContract || call.failure == WorkerFailureDriverPanic || call.failure == WorkerFailureRuntime
}

type workerFailureRecord struct {
	failure WorkerFailure
	err     error
}

type workerFailureLatch struct {
	state atomic.Uint32
	ready chan struct{}
	value atomic.Pointer[workerFailureRecord]
}

const (
	workerFailureHealthy uint32 = iota
	workerFailurePublishing
	workerFailurePublished
)

type workerOperationGate struct {
	state atomic.Uint32
}

const (
	workerOperationPending uint32 = iota
	workerOperationStarted
	workerOperationSettled
)

func (gate *workerOperationGate) start() bool {
	return gate != nil && gate.state.CompareAndSwap(workerOperationPending, workerOperationStarted)
}

func (gate *workerOperationGate) settle() {
	if gate != nil {
		gate.state.CompareAndSwap(workerOperationPending, workerOperationSettled)
	}
}

func newWorkerFailureLatch() *workerFailureLatch {
	return &workerFailureLatch{ready: make(chan struct{})}
}

func (latch *workerFailureLatch) fail(failure WorkerFailure, err error) {
	if latch == nil || latch.ready == nil || failure != WorkerFailureDriverContract && failure != WorkerFailureDriverPanic && failure != WorkerFailureRuntime || err == nil {
		return
	}
	record := &workerFailureRecord{failure: failure, err: err}
	if latch.state.CompareAndSwap(workerFailureHealthy, workerFailurePublishing) {
		latch.value.Store(record)
		latch.state.Store(workerFailurePublished)
		close(latch.ready)
	}
}

func (latch *workerFailureLatch) load() (workerFailureRecord, bool) {
	if latch == nil || latch.ready == nil {
		return workerFailureRecord{}, false
	}
	if latch.state.Load() == workerFailureHealthy {
		return workerFailureRecord{}, false
	}
	<-latch.ready
	record := latch.value.Load()
	if record == nil {
		return workerFailureRecord{failure: WorkerFailureRuntime, err: ErrInvalid}, true
	}
	return *record, true
}

func (latch *workerFailureLatch) begin(operation *workerOperationGate, started *atomic.Bool) (workerFailureRecord, bool, bool) {
	if latch == nil || operation == nil || started == nil {
		return workerFailureRecord{failure: WorkerFailureRuntime, err: ErrInvalid}, true, false
	}
	if record, failed := latch.load(); failed {
		return record, true, false
	}
	if !operation.start() {
		return workerFailureRecord{}, false, false
	}
	if record, failed := latch.load(); failed {
		return record, true, false
	}
	started.Store(true)
	return workerFailureRecord{}, false, true
}

type workerDriverBoundary struct {
	namespace  Namespace
	driver     DeliveryDriver
	backend    BackendDescription
	clock      *workerClock
	timeout    time.Duration
	leaseTTL   time.Duration
	claimItems int
	claimBytes int
	fatal      *workerFailureLatch
}

func (boundary workerDriverBoundary) valid() bool {
	return boundary.namespace.valid() && !nilInterface(boundary.driver) && boundary.backend.valid() && boundary.clock != nil && boundary.timeout >= MinimumOperationTimeout && boundary.timeout <= MaximumOperationTimeout && validLeaseTTL(boundary.leaseTTL) && boundary.claimItems >= 1 && boundary.claimItems <= MaxClaimItems && boundary.claimBytes >= MaxDeliveryRecordBytes && boundary.claimBytes <= MaxClaimBytes && boundary.fatal != nil && boundary.fatal.ready != nil
}

func (workers *Workers) driverBoundary() workerDriverBoundary {
	if workers == nil {
		return workerDriverBoundary{}
	}
	return workerDriverBoundary{
		namespace:  workers.config.namespace,
		driver:     workers.config.driver,
		backend:    workers.config.backend,
		clock:      workers.config.clock,
		timeout:    workers.config.operationTimeout,
		leaseTTL:   workers.config.leaseTTL,
		claimItems: workers.config.claimItems,
		claimBytes: workers.config.claimBytes,
		fatal:      workers.fatal,
	}
}

func (workers *Workers) callClaim(ctx context.Context, request ClaimRequest) (ClaimBatch, workerDriverCall) {
	boundary := workers.driverBoundary()
	if err := validateClaimRequest(request); err != nil || request.namespace != boundary.namespace || request.leaseTTL != boundary.leaseTTL || request.maxItems > boundary.claimItems || request.maxBytes > boundary.claimBytes || !workers.validClaimScope(request) {
		return ClaimBatch{}, boundary.runtimeFailure(false, 0)
	}
	return invokeWorkerDriver(boundary, ctx,
		func(callCtx context.Context) (ClaimBatch, error) {
			return boundary.driver.Claim(callCtx, request)
		},
		func(result ClaimBatch) (ClaimBatch, error) {
			return ValidateClaimBatch(boundary.backend, request, result)
		},
	)
}

func (workers *Workers) callRenew(ctx context.Context, request RenewRequest) (RenewResult, workerDriverCall) {
	boundary := workers.driverBoundary()
	if err := validateRenewRequest(request); err != nil || request.leases[0].backend != boundary.backend.ID() || request.leaseTTL != boundary.leaseTTL {
		return RenewResult{}, boundary.runtimeFailure(false, 0)
	}
	return invokeWorkerDriver(boundary, ctx,
		func(callCtx context.Context) (RenewResult, error) {
			return boundary.driver.Renew(callCtx, request)
		},
		func(result RenewResult) (RenewResult, error) {
			return ValidateRenewResult(boundary.backend, request, result)
		},
	)
}

func (workers *Workers) callApply(ctx context.Context, request ApplyRequest) (ApplyResult, workerDriverCall) {
	boundary := workers.driverBoundary()
	command, err := validateDeliveryCommand(request.command)
	if err != nil || command.lease.backend != boundary.backend.ID() || !workers.validApplyScope(command) {
		return ApplyResult{}, boundary.runtimeFailure(false, 0)
	}
	request.command = command
	return invokeWorkerDriver(boundary, ctx,
		func(callCtx context.Context) (ApplyResult, error) {
			return boundary.driver.Apply(callCtx, request)
		},
		func(result ApplyResult) (ApplyResult, error) {
			return ValidateApplyResult(boundary.backend, request, result)
		},
	)
}

func (workers *Workers) validClaimScope(request ClaimRequest) bool {
	if workers == nil || !workers.config.build.valid() || workers.config.catalog.Len() == 0 || len(workers.plan.bindings) == 0 {
		return false
	}
	for _, target := range request.targets {
		index, ok := workers.config.catalog.byName[target.definition]
		if !ok || index < 0 || index >= len(workers.config.catalog.descriptors) {
			return false
		}
		var matched *consumerBinding
		for bindingIndex := range workers.plan.bindings {
			binding := &workers.plan.bindings[bindingIndex]
			if binding.declaration.declarationName() == target.definition && binding.binding == target.binding {
				if matched != nil {
					return false
				}
				matched = binding
			}
		}
		if matched == nil || target.build != workers.config.build || target.available > matched.concurrency || !claimRevisionsMatch(workers.config.catalog.descriptors[index].Codec, target.revisions) {
			return false
		}
	}
	return true
}

func claimRevisionsMatch(codec CodecDescription, revisions []PayloadRevision) bool {
	if len(revisions) != len(codec.Upcasts)+1 {
		return false
	}
	for index, upcast := range codec.Upcasts {
		if revisions[index] != (PayloadRevision{codec: upcast.SourceCodec, version: upcast.From}) {
			return false
		}
	}
	return revisions[len(revisions)-1] == (PayloadRevision{codec: codec.ID, version: codec.CurrentVersion})
}

func (workers *Workers) validApplyScope(command DeliveryCommand) bool {
	if workers == nil {
		return false
	}
	bound := command.kind == DeliveryCommandBeginAttempt || command.kind == DeliveryCommandReleaseUnchanged && command.reason == ReasonCompatibility
	if !bound {
		return true
	}
	if command.build != workers.config.build {
		return false
	}
	for _, binding := range workers.plan.bindings {
		if binding.binding == command.binding {
			return true
		}
	}
	return false
}

func (workers *Workers) callRecover(ctx context.Context, request RecoverRequest) (RecoverResult, workerDriverCall) {
	boundary := workers.driverBoundary()
	if err := validateRecoverRequest(request); err != nil || request.namespace != boundary.namespace || request.leaseTTL != boundary.leaseTTL || request.maxItems > boundary.claimItems || request.maxBytes > boundary.claimBytes {
		return RecoverResult{}, boundary.runtimeFailure(false, 0)
	}
	return invokeWorkerDriver(boundary, ctx,
		func(callCtx context.Context) (RecoverResult, error) {
			return boundary.driver.Recover(callCtx, request)
		},
		func(result RecoverResult) (RecoverResult, error) {
			return ValidateRecoverResult(boundary.backend, request, result)
		},
	)
}

func invokeWorkerDriver[T any](boundary workerDriverBoundary, parent context.Context, invoke func(context.Context) (T, error), validate func(T) (T, error)) (value T, resultCall workerDriverCall) {
	returned := false
	started := &atomic.Bool{}
	defer func() {
		if recover() != nil {
			var zero T
			value = zero
			resultCall = boundary.runtimeFailure(started.Load(), 0)
			returned = true
			return
		}
		if !returned {
			boundary.fatal.fail(WorkerFailureRuntime, ErrInvalid)
		}
	}()
	value, resultCall = invokeWorkerDriverCall(boundary, parent, invoke, validate, started)
	returned = true
	return value, resultCall
}

func invokeWorkerDriverCall[T any](boundary workerDriverBoundary, parent context.Context, invoke func(context.Context) (T, error), validate func(T) (T, error), started *atomic.Bool) (zero T, resultCall workerDriverCall) {
	if !boundary.valid() || nilInterface(parent) || invoke == nil || validate == nil {
		return zero, boundary.runtimeFailure(false, 0)
	}
	if record, failed := boundary.fatal.load(); failed {
		return zero, failedWorkerDriverCall(record.failure, record.err, false, 0)
	}
	if err := parent.Err(); err != nil {
		return zero, cancelledWorkerDriverCall(err, false, 0)
	}
	parentDeadline, parentHasDeadline := parent.Deadline()
	if parentHasDeadline {
		if _, err := requiredTime(parentDeadline, "worker parent deadline"); err != nil {
			return zero, boundary.runtimeFailure(false, 0)
		}
	}
	startedAt, deadline, timer, err := boundary.clock.startTimer(boundary.timeout)
	if err != nil {
		return zero, boundary.runtimeFailure(false, 0)
	}
	contextDeadline := deadline
	if parentHasDeadline && parentDeadline.Before(contextDeadline) {
		contextDeadline = parentDeadline
	}
	base, cancel := context.WithCancelCause(context.WithoutCancel(parent))
	defer cancel(context.Canceled)
	timedOut := &atomic.Bool{}
	callCtx := &workerOperationContext{Context: base, deadline: contextDeadline, timedOut: timedOut}
	completed := make(chan struct{})
	startRequest := make(chan chan struct{})
	watched := make(chan workerOperationObservation, 1)
	operation := &workerOperationGate{}
	go superviseWorkerOperation(parent, cancel, timedOut, boundary.clock, deadline, timer, boundary.fatal, operation, started, completed, startRequest, watched)
	var finishOnce sync.Once
	finish := func() {
		finishOnce.Do(func() {
			close(completed)
		})
	}
	joined := false
	defer func() {
		finish()
		if !joined {
			<-watched
		}
	}()
	grant := make(chan struct{})
	select {
	case startRequest <- grant:
	case observation := <-watched:
		finish()
		joined = true
		return zero, prestartWorkerDriverCall(boundary, parent, callCtx, observation)
	}
	select {
	case <-grant:
	case observation := <-watched:
		finish()
		joined = true
		return zero, prestartWorkerDriverCall(boundary, parent, callCtx, observation)
	}
	value, driverErr, driverFailure := callWorkerDriver(boundary.fatal, callCtx, invoke)
	finish()
	observation := <-watched
	joined = true
	finishedAt, clockErr := boundary.clock.Now()
	if clockErr != nil {
		return zero, boundary.runtimeFailure(true, 0)
	}
	elapsed := finishedAt.Sub(startedAt)
	if elapsed < 0 {
		return zero, boundary.runtimeFailure(true, 0)
	}
	if observation.cleanupFailed || observation.signal == workerOperationRuntimeFailed {
		return zero, boundary.runtimeFailure(true, elapsed)
	}
	if driverFailure == WorkerFailureDriverPanic {
		return zero, boundary.fatalFailure(driverFailure, ErrDriver, true, elapsed)
	}
	if observation.signal == workerOperationParentDone {
		return zero, cancelledWorkerDriverCall(parent.Err(), true, elapsed)
	}
	if observation.signal == workerOperationTimedOut || !finishedAt.Before(deadline) {
		return zero, cancelledWorkerDriverCall(context.DeadlineExceeded, true, elapsed)
	}
	if driverErr != nil {
		return zero, failedWorkerDriverCall(WorkerFailureDriver, ErrDriver, true, elapsed)
	}
	validated, validationFailure := validateWorkerDriverResult(boundary.fatal, value, validate)
	if validationFailure != WorkerFailureNone {
		return zero, boundary.fatalFailure(validationFailure, failureError(validationFailure), true, elapsed)
	}
	return validated, workerDriverCall{outcome: WorkerOutcomeComplete, elapsed: elapsed, started: true}
}

func callWorkerDriver[T any](fatal *workerFailureLatch, ctx context.Context, invoke func(context.Context) (T, error)) (value T, err error, failure WorkerFailure) {
	returned := false
	defer func() {
		if recovered := recover(); recovered != nil {
			var zero T
			value = zero
			err = ErrDriver
			failure = WorkerFailureDriverPanic
			fatal.fail(failure, err)
			return
		}
		if !returned {
			fatal.fail(WorkerFailureDriverContract, ErrDriverContract)
		}
	}()
	value, err = invoke(ctx)
	returned = true
	if err != nil {
		var zero T
		value = zero
		err = ErrDriver
		failure = WorkerFailureDriver
	}
	return value, err, failure
}

func validateWorkerDriverResult[T any](fatal *workerFailureLatch, value T, validate func(T) (T, error)) (validated T, failure WorkerFailure) {
	returned := false
	defer func() {
		if recover() != nil {
			var zero T
			validated = zero
			failure = WorkerFailureRuntime
			fatal.fail(failure, ErrInvalid)
			return
		}
		if !returned {
			fatal.fail(WorkerFailureRuntime, ErrInvalid)
		}
	}()
	var err error
	validated, err = validate(value)
	returned = true
	if err != nil {
		var zero T
		validated = zero
		failure = WorkerFailureDriverContract
		fatal.fail(failure, ErrDriverContract)
	}
	return validated, failure
}

type workerOperationContext struct {
	context.Context
	deadline time.Time
	timedOut *atomic.Bool
}

func (ctx *workerOperationContext) Deadline() (time.Time, bool) {
	if parent, ok := ctx.Context.Deadline(); ok && parent.Before(ctx.deadline) {
		return parent, true
	}
	return ctx.deadline, true
}

func (ctx *workerOperationContext) Err() error {
	err := ctx.Context.Err()
	if err == nil {
		return nil
	}
	if ctx.timedOut.Load() {
		return context.DeadlineExceeded
	}
	return err
}

type workerOperationSignal uint8

const (
	workerOperationCompleted workerOperationSignal = iota + 1
	workerOperationParentDone
	workerOperationTimedOut
	workerOperationRuntimeFailed
)

type workerOperationObservation struct {
	signal        workerOperationSignal
	cleanupFailed bool
}

func superviseWorkerOperation(parent context.Context, cancel context.CancelCauseFunc, timedOut *atomic.Bool, clock *workerClock, deadline time.Time, timer *workerTimer, fatal *workerFailureLatch, operation *workerOperationGate, started *atomic.Bool, completed <-chan struct{}, startRequest <-chan chan struct{}, result chan<- workerOperationObservation) {
	observation := workerOperationObservation{signal: workerOperationRuntimeFailed}
	returned := false
	stopReturned := false
	stopValid := false
	published := false
	var parentCause error
	parentDeadline := false
	defer func() {
		operation.settle()
		if recover() != nil || !returned || !stopReturned || !stopValid {
			observation.cleanupFailed = true
		}
		if observation.cleanupFailed {
			fatal.fail(WorkerFailureRuntime, ErrInvalid)
		}
		result <- observation
	}()
	defer func() {
		if !published {
			fatal.fail(WorkerFailureRuntime, ErrInvalid)
			publishWorkerOperation(cancel, timedOut, workerOperationRuntimeFailed, nil, false)
			published = true
		}
		_, stopValid = timer.stop()
		stopReturned = true
	}()
	parentDone := parent.Done()
	observation.signal = preflightWorkerOperation(parent, clock, deadline, timer, operation)
	if observation.signal == workerOperationCompleted {
		var grant chan struct{}
		grant, observation.signal = waitWorkerOperationStart(parent, parentDone, clock, deadline, timer, operation, completed, startRequest)
		if observation.signal == workerOperationCompleted {
			_, _, allowed := fatal.begin(operation, started)
			if allowed {
				grant <- struct{}{}
				observation.signal = waitWorkerOperation(parent, parentDone, clock, deadline, timer, operation, completed)
			} else {
				observation.signal = workerOperationRuntimeFailed
			}
		}
	}
	if observation.signal == workerOperationParentDone {
		var valid bool
		parentCause, parentDeadline, valid = safeWorkerParentCancellation(parent)
		if !valid {
			observation.signal = workerOperationRuntimeFailed
		}
	}
	if observation.signal == workerOperationCompleted {
		stopped, valid := timer.stop()
		stopReturned = true
		stopValid = valid
		if valid && !stopped {
			now, err := clock.Now()
			if err != nil || now.Before(deadline) {
				observation.signal = workerOperationRuntimeFailed
			} else {
				observation.signal = workerOperationTimedOut
			}
		}
	}
	if observation.signal == workerOperationRuntimeFailed {
		fatal.fail(WorkerFailureRuntime, ErrInvalid)
	}
	publishWorkerOperation(cancel, timedOut, observation.signal, parentCause, parentDeadline)
	published = true
	returned = true
}

func publishWorkerOperation(cancel context.CancelCauseFunc, timedOut *atomic.Bool, signal workerOperationSignal, parentCause error, parentDeadline bool) {
	switch signal {
	case workerOperationTimedOut:
		timedOut.Store(true)
		cancel(context.DeadlineExceeded)
	case workerOperationParentDone:
		if parentDeadline {
			timedOut.Store(true)
		}
		cancel(parentCause)
	case workerOperationRuntimeFailed:
		cancel(ErrInvalid)
	default:
		cancel(context.Canceled)
	}
}

func preflightWorkerOperation(parent context.Context, clock *workerClock, deadline time.Time, timer *workerTimer, operation *workerOperationGate) workerOperationSignal {
	if parent.Err() != nil {
		operation.settle()
		return workerOperationParentDone
	}
	if signal, settled := pollWorkerTimer(parent, clock, deadline, timer, operation); settled {
		return signal
	}
	now, err := clock.Now()
	if err != nil {
		operation.settle()
		return workerOperationRuntimeFailed
	}
	if !now.Before(deadline) {
		operation.settle()
		return workerOperationTimedOut
	}
	if parent.Err() != nil {
		operation.settle()
		return workerOperationParentDone
	}
	if signal, settled := pollWorkerTimer(parent, clock, deadline, timer, operation); settled {
		return signal
	}
	return workerOperationCompleted
}

func waitWorkerOperationStart(parent context.Context, parentDone <-chan struct{}, clock *workerClock, deadline time.Time, timer *workerTimer, operation *workerOperationGate, completed <-chan struct{}, startRequest <-chan chan struct{}) (chan struct{}, workerOperationSignal) {
	select {
	case grant := <-startRequest:
		if grant == nil {
			operation.settle()
			return nil, workerOperationRuntimeFailed
		}
		return grant, preflightWorkerOperation(parent, clock, deadline, timer, operation)
	case <-completed:
		operation.settle()
		return nil, workerOperationCompleted
	case <-parentDone:
		operation.settle()
		return nil, workerOperationParentDone
	case _, open := <-timer.C():
		operation.settle()
		return nil, resolveWorkerTimerWake(parent, clock, deadline, open)
	}
}

func pollWorkerTimer(parent context.Context, clock *workerClock, deadline time.Time, timer *workerTimer, operation *workerOperationGate) (workerOperationSignal, bool) {
	select {
	case _, open := <-timer.C():
		operation.settle()
		return resolveWorkerTimerWake(parent, clock, deadline, open), true
	default:
		return 0, false
	}
}

func resolveWorkerTimerWake(parent context.Context, clock *workerClock, deadline time.Time, open bool) workerOperationSignal {
	if !open {
		return workerOperationRuntimeFailed
	}
	if parent.Err() != nil {
		return workerOperationParentDone
	}
	now, err := clock.Now()
	if err != nil || now.Before(deadline) {
		return workerOperationRuntimeFailed
	}
	return workerOperationTimedOut
}

func safeWorkerParentCancellation(parent context.Context) (cause error, deadline bool, valid bool) {
	defer func() {
		if recover() != nil {
			cause = nil
			deadline = false
			valid = false
		}
	}()
	parentErr := parent.Err()
	if parentErr == nil {
		return nil, false, false
	}
	cause = context.Cause(parent)
	if cause == nil {
		return nil, false, false
	}
	return cause, errors.Is(parentErr, context.DeadlineExceeded), true
}

func waitWorkerOperation(parent context.Context, parentDone <-chan struct{}, clock *workerClock, deadline time.Time, timer *workerTimer, operation *workerOperationGate, completed <-chan struct{}) workerOperationSignal {
	select {
	case <-completed:
		operation.settle()
		if parent.Err() != nil {
			return workerOperationParentDone
		}
		if signal, settled := pollWorkerTimer(parent, clock, deadline, timer, operation); settled {
			return signal
		}
		return workerOperationCompleted
	case <-parentDone:
		operation.settle()
		return workerOperationParentDone
	case _, open := <-timer.C():
		operation.settle()
		return resolveWorkerTimerWake(parent, clock, deadline, open)
	}
}

func (boundary workerDriverBoundary) runtimeFailure(started bool, elapsed time.Duration) workerDriverCall {
	return boundary.fatalFailure(WorkerFailureRuntime, ErrInvalid, started, elapsed)
}

func (boundary workerDriverBoundary) fatalFailure(failure WorkerFailure, err error, started bool, elapsed time.Duration) workerDriverCall {
	boundary.fatal.fail(failure, err)
	if record, loaded := boundary.fatal.load(); loaded {
		return failedWorkerDriverCall(record.failure, record.err, started, elapsed)
	}
	return failedWorkerDriverCall(failure, err, started, elapsed)
}

func failedWorkerDriverCall(failure WorkerFailure, err error, started bool, elapsed time.Duration) workerDriverCall {
	return workerDriverCall{outcome: WorkerOutcomeFailed, failure: failure, elapsed: elapsed, err: err, started: started, uncertain: started}
}

func cancelledWorkerDriverCall(err error, started bool, elapsed time.Duration) workerDriverCall {
	if errors.Is(err, context.DeadlineExceeded) {
		return workerDriverCall{outcome: WorkerOutcomeTimedOut, elapsed: elapsed, err: context.DeadlineExceeded, started: started, uncertain: started}
	}
	return workerDriverCall{outcome: WorkerOutcomeCancelled, elapsed: elapsed, err: context.Canceled, started: started, uncertain: started}
}

func failureError(failure WorkerFailure) error {
	if failure == WorkerFailureDriverContract {
		return ErrDriverContract
	}
	return ErrInvalid
}

func prestartWorkerDriverCall(boundary workerDriverBoundary, parent context.Context, operation *workerOperationContext, observation workerOperationObservation) workerDriverCall {
	if observation.cleanupFailed {
		return boundary.runtimeFailure(false, 0)
	}
	switch observation.signal {
	case workerOperationRuntimeFailed:
		return boundary.runtimeFailure(false, 0)
	case workerOperationTimedOut:
		return cancelledWorkerDriverCall(context.DeadlineExceeded, false, 0)
	case workerOperationParentDone:
		return cancelledWorkerDriverCall(parent.Err(), false, 0)
	case workerOperationCompleted:
		return cancelledWorkerDriverCall(operation.Err(), false, 0)
	default:
		return boundary.runtimeFailure(false, 0)
	}
}
