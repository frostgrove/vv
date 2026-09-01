package jobs

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

type workersRuntimeState uint8

const (
	workersRuntimeFresh workersRuntimeState = iota
	workersRuntimeRunning
	workersRuntimeDraining
	workersRuntimeStopped
)

type workersRuntime struct {
	mu      sync.Mutex
	state   workersRuntimeState
	done    chan struct{}
	session *workerRunSession
	err     error
}

func newWorkersRuntime() *workersRuntime {
	return &workersRuntime{state: workersRuntimeFresh, done: make(chan struct{})}
}

type workerRunSession struct {
	parent         context.Context
	controlContext context.Context
	pollContext    context.Context
	handlerContext context.Context
	cancelPoll     context.CancelCauseFunc
	cancelHandlers context.CancelCauseFunc
	drain          chan struct{}
	force          chan struct{}
	drainOnce      sync.Once
	forceOnce      sync.Once
	incarnation    WorkerIncarnation
}

func newWorkerRunSession(parent context.Context) *workerRunSession {
	base := context.WithoutCancel(parent)
	pollContext, cancelPoll := context.WithCancelCause(base)
	handlerContext, cancelHandlers := context.WithCancelCause(base)
	return &workerRunSession{
		parent:         parent,
		controlContext: base,
		pollContext:    pollContext,
		handlerContext: handlerContext,
		cancelPoll:     cancelPoll,
		cancelHandlers: cancelHandlers,
		drain:          make(chan struct{}),
		force:          make(chan struct{}),
	}
}

func (session *workerRunSession) requestDrain() {
	session.drainOnce.Do(func() {
		session.cancelPoll(ErrCancelled)
		close(session.drain)
	})
}

func (session *workerRunSession) requestForce() {
	session.forceOnce.Do(func() {
		session.requestDrain()
		session.cancelHandlers(ErrTerminated)
		close(session.force)
	})
}

func (workers *Workers) Run(ctx context.Context) error {
	if workers == nil || workers.runtime == nil || nilInterface(ctx) {
		return ErrInvalid
	}
	session, err := workers.runtime.begin(ctx)
	if err != nil {
		return err
	}
	session.incarnation, err = newWorkerIncarnation(workers.config.entropy)
	if err != nil {
		workers.runtime.finish(err)
		return err
	}
	err = workers.run(session)
	workers.runtime.finish(err)
	return err
}

func (workers *Workers) Drain(ctx context.Context) error {
	if workers == nil || workers.runtime == nil || nilInterface(ctx) {
		return ErrInvalid
	}
	done, session, err := workers.runtime.drain()
	if err != nil {
		return err
	}
	if session != nil {
		session.requestDrain()
	}
	select {
	case <-done:
		return workers.runtime.result()
	case <-ctx.Done():
		if session != nil {
			session.requestForce()
		}
		return ctx.Err()
	}
}

func (runtime *workersRuntime) begin(parent context.Context) (*workerRunSession, error) {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if runtime.state != workersRuntimeFresh {
		return nil, ErrConflict
	}
	session := newWorkerRunSession(parent)
	runtime.state = workersRuntimeRunning
	runtime.session = session
	return session, nil
}

func (runtime *workersRuntime) drain() (<-chan struct{}, *workerRunSession, error) {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	switch runtime.state {
	case workersRuntimeFresh:
		runtime.state = workersRuntimeStopped
		close(runtime.done)
		return runtime.done, nil, nil
	case workersRuntimeRunning:
		runtime.state = workersRuntimeDraining
		return runtime.done, runtime.session, nil
	case workersRuntimeDraining:
		return runtime.done, runtime.session, nil
	case workersRuntimeStopped:
		return runtime.done, nil, nil
	default:
		return nil, nil, ErrInvalid
	}
}

func (runtime *workersRuntime) finish(err error) {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if runtime.state == workersRuntimeStopped {
		return
	}
	runtime.state = workersRuntimeStopped
	runtime.session = nil
	runtime.err = err
	close(runtime.done)
}

func (runtime *workersRuntime) result() error {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	return runtime.err
}

type workerPool struct {
	workers  *Workers
	session  *workerRunSession
	bindings map[Name]*workerRuntimeBinding
	mu       sync.Mutex
	bytes    int
	active   map[*activeWorkerDelivery]struct{}
	wg       sync.WaitGroup
	failure  chan error
	failOnce sync.Once
}

type workerRuntimeBinding struct {
	binding consumerBinding
	active  int
}

func newWorkerPool(workers *Workers, session *workerRunSession) *workerPool {
	bindings := make(map[Name]*workerRuntimeBinding, workers.plan.Len())
	for _, binding := range workers.plan.workerBindings() {
		bindings[binding.declaration.declarationName()] = &workerRuntimeBinding{binding: binding}
	}
	return &workerPool{
		workers:  workers,
		session:  session,
		bindings: bindings,
		active:   make(map[*activeWorkerDelivery]struct{}),
		failure:  make(chan error, 1),
	}
}

func (workers *Workers) run(session *workerRunSession) error {
	pool := newWorkerPool(workers, session)
	dispatchDone := make(chan struct{})
	go func() {
		defer close(dispatchDone)
		pool.dispatch()
	}()
	var result error
	select {
	case <-session.parent.Done():
		result = context.Cause(session.parent)
		session.requestDrain()
	case <-session.drain:
		select {
		case result = <-pool.failure:
		default:
		}
	case result = <-pool.failure:
		session.requestForce()
	}
	<-dispatchDone
	activeDone := make(chan struct{})
	go func() {
		pool.wg.Wait()
		close(activeDone)
	}()
	if result != nil && !errors.Is(result, context.Canceled) && !errors.Is(result, context.DeadlineExceeded) {
		session.requestForce()
		<-activeDone
		return result
	}
	if session.parent.Err() == nil {
		select {
		case <-activeDone:
			return result
		case <-session.force:
			<-activeDone
			return result
		}
	}
	timer := time.NewTimer(workers.config.shutdownGrace)
	defer timer.Stop()
	select {
	case <-activeDone:
		return result
	case <-timer.C:
		session.requestForce()
		<-activeDone
		return result
	}
}

func (pool *workerPool) dispatch() {
	nextRecover := time.Time{}
	for {
		select {
		case <-pool.session.pollContext.Done():
			return
		default:
		}
		now, err := pool.workers.config.clock.Now()
		if err != nil {
			pool.fail(err)
			return
		}
		if nextRecover.IsZero() || !now.Before(nextRecover) {
			if !pool.recover() {
				return
			}
			nextRecover = now.Add(pool.workers.config.reclaimInterval)
		}
		if !pool.claim() {
			return
		}
		if !waitWorkerPoll(pool.session.pollContext, pool.workers.config.pollInterval) {
			return
		}
	}
}

func waitWorkerPoll(ctx context.Context, interval time.Duration) bool {
	timer := time.NewTimer(interval)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func (pool *workerPool) claim() bool {
	request, ok, err := pool.claimRequest()
	if err != nil {
		pool.fail(err)
		return false
	}
	if !ok {
		return true
	}
	batch, call := pool.workers.callClaim(pool.session.pollContext, request)
	if call.err != nil {
		if call.fatal() {
			pool.fail(call.err)
			return false
		}
		return pool.session.pollContext.Err() == nil
	}
	for _, delivery := range batch.items {
		if !pool.acceptClaimed(delivery) {
			pool.releaseClaimed(delivery)
		}
	}
	return true
}

func (pool *workerPool) claimRequest() (ClaimRequest, bool, error) {
	now, err := pool.workers.config.clock.Now()
	if err != nil {
		return ClaimRequest{}, false, err
	}
	pool.mu.Lock()
	defer pool.mu.Unlock()
	remainingBytes := pool.workers.config.inFlightBytes - pool.bytes
	if remainingBytes < MaxDeliveryRecordBytes {
		return ClaimRequest{}, false, nil
	}
	targets := make([]ClaimTarget, 0, len(pool.bindings))
	total := 0
	for _, planned := range pool.workers.plan.bindings {
		current := pool.bindings[planned.declaration.declarationName()]
		limit := planned.concurrency
		if planned.admission.initialized {
			decision := planned.admission.Evaluate(planned.concurrency, now)
			if decision.Err() != nil {
				continue
			}
			limit = decision.Limit()
		}
		available := limit - current.active
		if available <= 0 {
			continue
		}
		target, targetErr := pool.claimTarget(planned, available)
		if targetErr != nil {
			return ClaimRequest{}, false, targetErr
		}
		targets = append(targets, target)
		total += available
	}
	if total == 0 {
		return ClaimRequest{}, false, nil
	}
	maxItems := min(pool.workers.config.claimItems, total)
	maxBytes := min(pool.workers.config.claimBytes, remainingBytes)
	request, err := NewClaimRequest(ClaimRequestSpec{
		Namespace:   pool.workers.config.namespace,
		Incarnation: pool.session.incarnation,
		Targets:     targets,
		MaxItems:    maxItems,
		MaxBytes:    maxBytes,
		LeaseTTL:    pool.workers.config.leaseTTL,
	})
	return request, err == nil, err
}

func (pool *workerPool) claimTarget(binding consumerBinding, available int) (ClaimTarget, error) {
	index, ok := pool.workers.config.catalog.byName[binding.declaration.declarationName()]
	if !ok {
		return ClaimTarget{}, ErrInvalid
	}
	codec := pool.workers.config.catalog.descriptors[index].Codec
	revisions := make([]PayloadRevision, 0, len(codec.Upcasts)+1)
	for _, upcast := range codec.Upcasts {
		revision, err := NewPayloadRevision(upcast.SourceCodec, upcast.From)
		if err != nil {
			return ClaimTarget{}, err
		}
		revisions = append(revisions, revision)
	}
	current, err := NewPayloadRevision(codec.ID, codec.CurrentVersion)
	if err != nil {
		return ClaimTarget{}, err
	}
	revisions = append(revisions, current)
	return NewClaimTarget(ClaimTargetSpec{
		Definition:         binding.declaration.declarationName(),
		Binding:            binding.binding,
		Build:              pool.workers.config.build,
		SupportedRevisions: revisions,
		Available:          available,
	})
}

func (pool *workerPool) acceptClaimed(delivery ClaimedDelivery) bool {
	size, err := delivery.recordSize()
	if err != nil {
		pool.fail(err)
		return false
	}
	pool.mu.Lock()
	binding := pool.bindings[delivery.target.definition]
	if binding == nil || binding.binding.binding != delivery.target.binding || binding.active >= binding.binding.concurrency || size > pool.workers.config.inFlightBytes-pool.bytes {
		pool.mu.Unlock()
		return false
	}
	active := newActiveWorkerDelivery(pool, binding, delivery.lease, size)
	binding.active++
	pool.bytes += size
	pool.active[active] = struct{}{}
	pool.wg.Add(1)
	pool.mu.Unlock()
	go active.runClaimed(delivery)
	return true
}

func (pool *workerPool) releaseClaimed(delivery ClaimedDelivery) {
	command, err := ReleaseForShutdownCommand(delivery.lease, DefaultRetryDelay)
	if err != nil {
		pool.fail(err)
		return
	}
	request, err := NewApplyRequest(command)
	if err != nil {
		pool.fail(err)
		return
	}
	_, call := pool.workers.callApply(pool.session.controlContext, request)
	if call.fatal() {
		pool.fail(call.err)
	}
}

func (pool *workerPool) recover() bool {
	request, err := NewRecoverRequest(RecoverRequestSpec{
		Namespace:   pool.workers.config.namespace,
		Incarnation: pool.session.incarnation,
		MaxItems:    min(pool.workers.config.claimItems, MaxReclaimBatch),
		MaxBytes:    pool.workers.config.claimBytes,
		LeaseTTL:    pool.workers.config.leaseTTL,
	})
	if err != nil {
		pool.fail(err)
		return false
	}
	for {
		result, call := pool.workers.callRecover(pool.session.pollContext, request)
		if call.err != nil {
			if call.fatal() {
				pool.fail(call.err)
				return false
			}
			return pool.session.pollContext.Err() == nil
		}
		for _, delivery := range result.items {
			pool.acceptRecovered(delivery)
		}
		if !result.more {
			return true
		}
		select {
		case <-pool.session.pollContext.Done():
			return false
		default:
		}
	}
}

func (pool *workerPool) acceptRecovered(delivery RecoveredDelivery) {
	restored, err := RestoreDeliveryRecord(pool.workers.config.catalog, delivery.Record())
	if err != nil {
		if errors.Is(err, ErrCorrupt) {
			pool.applyRecovered(delivery.lease, func(lease LeaseRef) (DeliveryCommand, error) { return RejectCorruptCommand(lease) })
			return
		}
		pool.fail(err)
		return
	}
	invocation := restored.Invocation()
	switch invocation.State() {
	case InvocationRunning, InvocationCancelRequested:
		delay := retryBackoffCap(invocation.Policy().Backoff(), invocation.RetrySpent().Value())
		pool.applyRecovered(delivery.lease, func(lease LeaseRef) (DeliveryCommand, error) {
			return RevokeAttemptCommand(lease, ReasonLeaseLost, delay)
		})
	case InvocationQueued:
		binding := pool.bindings[invocation.Definition()]
		if binding == nil {
			pool.applyRecovered(delivery.lease, func(lease LeaseRef) (DeliveryCommand, error) {
				return ReleaseForShutdownCommand(lease, DefaultRetryDelay)
			})
			return
		}
		target, targetErr := pool.claimTarget(binding.binding, 1)
		if targetErr != nil {
			pool.fail(targetErr)
			return
		}
		claimed := ClaimedDelivery{target: target, lease: delivery.lease, record: delivery.record}
		if !pool.acceptClaimed(claimed) {
			pool.releaseClaimed(claimed)
		}
	default:
		pool.fail(driverContractError("recover", invalid("recovered invocation state")))
	}
}

func (pool *workerPool) applyRecovered(lease LeaseRef, build func(LeaseRef) (DeliveryCommand, error)) {
	command, err := build(lease)
	if err != nil {
		pool.fail(err)
		return
	}
	request, err := NewApplyRequest(command)
	if err != nil {
		pool.fail(err)
		return
	}
	_, call := pool.workers.callApply(pool.session.controlContext, request)
	if call.fatal() {
		pool.fail(call.err)
	}
}

func (pool *workerPool) fail(err error) {
	if err == nil {
		return
	}
	pool.failOnce.Do(func() {
		pool.failure <- err
		pool.session.requestForce()
	})
}

type activeWorkerDelivery struct {
	pool       *workerPool
	binding    *workerRuntimeBinding
	size       int
	mu         sync.Mutex
	lease      LeaseRef
	invocation Invocation
	closed     bool
	done       chan struct{}
	lost       chan struct{}
	lostOnce   sync.Once
	cancel     context.CancelCauseFunc
}

func newActiveWorkerDelivery(pool *workerPool, binding *workerRuntimeBinding, lease LeaseRef, size int) *activeWorkerDelivery {
	return &activeWorkerDelivery{
		pool:    pool,
		binding: binding,
		size:    size,
		lease:   cloneLeaseRef(lease),
		done:    make(chan struct{}),
		lost:    make(chan struct{}),
	}
}

func (delivery *activeWorkerDelivery) runClaimed(claimed ClaimedDelivery) {
	ctx, cancel := context.WithCancelCause(delivery.pool.session.handlerContext)
	delivery.cancel = cancel
	defer func() {
		cancel(context.Canceled)
		close(delivery.done)
		delivery.pool.mu.Lock()
		delivery.binding.active--
		delivery.pool.bytes -= delivery.size
		delete(delivery.pool.active, delivery)
		delivery.pool.mu.Unlock()
		delivery.pool.wg.Done()
	}()
	go delivery.renew(ctx)
	select {
	case <-delivery.pool.session.drain:
		delivery.apply(ctx, func(lease LeaseRef) (DeliveryCommand, error) {
			return ReleaseForShutdownCommand(lease, DefaultRetryDelay)
		})
		return
	default:
	}
	preparation, err := prepareClaimedDelivery(ctx, delivery.pool.workers.config.namespace, delivery.pool.workers.config.catalog, delivery.binding.binding, delivery.pool.workers.config.build, delivery.pool.workers.config.identity, claimed)
	if err != nil {
		if ctx.Err() == nil {
			delivery.pool.fail(err)
		} else {
			delivery.apply(delivery.pool.session.controlContext, func(lease LeaseRef) (DeliveryCommand, error) {
				return ReleaseForShutdownCommand(lease, DefaultRetryDelay)
			})
		}
		return
	}
	if preparation.commanded() {
		delivery.apply(ctx, func(LeaseRef) (DeliveryCommand, error) { return preparation.command, nil })
		return
	}
	select {
	case <-delivery.pool.session.drain:
		delivery.apply(ctx, func(lease LeaseRef) (DeliveryCommand, error) {
			return ReleaseForShutdownCommand(lease, DefaultRetryDelay)
		})
		return
	default:
	}
	begin, call := delivery.apply(ctx, func(lease LeaseRef) (DeliveryCommand, error) {
		return BeginAttemptCommand(lease, delivery.binding.binding.binding, delivery.pool.workers.config.build)
	})
	if call.err != nil || !begin.HandlerReady() {
		return
	}
	delivery.handle(ctx, preparation, begin.application)
}

func (delivery *activeWorkerDelivery) handle(ctx context.Context, preparation claimedDeliveryPreparation, application DeliveryApplication) {
	attempt, ok := application.Attempt()
	if !ok {
		delivery.pool.fail(ErrInvalid)
		return
	}
	meta, err := NewDeliveryMeta(DeliveryMetaSpec{
		Invocation:       application.invocation.ID(),
		Definition:       application.invocation.Definition(),
		Binding:          attempt.Binding(),
		Build:            attempt.Build(),
		Attempt:          attempt.Ordinal(),
		CreatedAt:        application.invocation.CreatedAt(),
		EligibleAt:       application.invocation.EligibleAt(),
		StartedAt:        attempt.StartedAt(),
		AttemptDeadline:  attempt.Deadline(),
		MaxElapsedAt:     application.invocation.MaxElapsedAt(),
		ProgressDeadline: attempt.ProgressDeadline(),
	})
	if err != nil {
		delivery.pool.fail(err)
		return
	}
	handled := make(chan error, 1)
	go func() {
		if delivery.binding.binding.mode == consumerHandlerAdapter {
			handled <- delivery.binding.binding.handleAdapter(preparation.context, preparation.decoded, meta, workerAttemptController{delivery: delivery})
			return
		}
		handled <- delivery.binding.binding.handle(preparation.context, preparation.decoded)
	}()
	now, err := delivery.pool.workers.config.clock.Now()
	if err != nil {
		delivery.pool.fail(err)
		return
	}
	duration := attempt.Deadline().Sub(now)
	if duration <= 0 {
		duration = time.Nanosecond
	}
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case result := <-handled:
		select {
		case <-delivery.pool.session.force:
			delivery.cancel(ErrTerminated)
			delivery.revoke(delivery.pool.session.controlContext, ReasonShutdown)
		default:
			disposition := classifyHandlerResult(delivery.binding.binding.classifier, result)
			delivery.finish(ctx, disposition)
		}
	case <-timer.C:
		delivery.cancel(context.DeadlineExceeded)
		invocation := delivery.snapshotInvocation()
		delay := retryBackoffCap(invocation.Policy().Backoff(), invocation.RetrySpent().Value())
		delivery.apply(ctx, func(lease LeaseRef) (DeliveryCommand, error) {
			return ArbitrateAttemptDeadlineCommand(lease, delay)
		})
	case <-delivery.pool.session.force:
		delivery.cancel(ErrTerminated)
		delivery.revoke(delivery.pool.session.controlContext, ReasonShutdown)
	case <-delivery.lost:
		delivery.cancel(ErrLeaseLost)
	}
}

func (delivery *activeWorkerDelivery) finish(ctx context.Context, disposition Disposition) {
	invocation := delivery.snapshotInvocation()
	delay := time.Duration(0)
	if disposition.Kind() == DispositionDeferred {
		delay = disposition.RetryAfter()
	}
	if disposition.Kind() == DispositionRetry {
		delay = retryBackoffCap(invocation.Policy().Backoff(), invocation.RetrySpent().Value())
		if disposition.RetryAfter() > delay {
			delay = disposition.RetryAfter()
		}
	}
	deadlineDelay := retryBackoffCap(invocation.Policy().Backoff(), invocation.RetrySpent().Value())
	delivery.apply(ctx, func(lease LeaseRef) (DeliveryCommand, error) {
		return FinishAttemptCommand(lease, disposition, delay, deadlineDelay)
	})
}

func (delivery *activeWorkerDelivery) revoke(ctx context.Context, reason Reason) {
	invocation := delivery.snapshotInvocation()
	delivery.apply(ctx, func(lease LeaseRef) (DeliveryCommand, error) {
		delay := retryBackoffCap(invocation.Policy().Backoff(), invocation.RetrySpent().Value())
		return RevokeAttemptCommand(lease, reason, delay)
	})
}

func (delivery *activeWorkerDelivery) snapshotInvocation() Invocation {
	delivery.mu.Lock()
	defer delivery.mu.Unlock()
	return delivery.invocation
}

func (delivery *activeWorkerDelivery) apply(ctx context.Context, build func(LeaseRef) (DeliveryCommand, error)) (ApplyResult, workerDriverCall) {
	delivery.mu.Lock()
	defer delivery.mu.Unlock()
	if delivery.closed {
		return ApplyResult{}, workerDriverCall{outcome: WorkerOutcomeCancelled, err: ErrLeaseLost}
	}
	command, err := build(delivery.lease)
	if err != nil {
		delivery.closeLost(err)
		return ApplyResult{}, workerDriverCall{outcome: WorkerOutcomeFailed, failure: WorkerFailureRuntime, err: err}
	}
	request, err := NewApplyRequest(command)
	if err != nil {
		delivery.closeLost(err)
		return ApplyResult{}, workerDriverCall{outcome: WorkerOutcomeFailed, failure: WorkerFailureRuntime, err: err}
	}
	result, call := delivery.pool.workers.callApply(ctx, request)
	if call.err != nil || result.result.mutation != DeliveryMutationApplied {
		delivery.closeLost(call.err)
		if call.fatal() {
			delivery.pool.fail(call.err)
		}
		return result, call
	}
	if !result.application.invocation.IsZero() {
		delivery.invocation = result.application.invocation
	}
	if result.result.control == DeliveryControlCancelRequested && delivery.cancel != nil {
		delivery.cancel(ErrCancelled)
	}
	if command.kind != DeliveryCommandBeginAttempt && command.kind != DeliveryCommandProgress || result.result.control == DeliveryControlTerminated {
		delivery.closed = true
	}
	return result, call
}

func (delivery *activeWorkerDelivery) renew(ctx context.Context) {
	ticker := time.NewTicker(delivery.pool.workers.config.heartbeat)
	defer ticker.Stop()
	for {
		select {
		case <-delivery.done:
			return
		case <-delivery.lost:
			return
		case <-ticker.C:
			delivery.renewOnce(ctx)
		}
	}
}

func (delivery *activeWorkerDelivery) renewOnce(ctx context.Context) {
	delivery.mu.Lock()
	defer delivery.mu.Unlock()
	if delivery.closed {
		return
	}
	request, err := NewRenewRequest([]LeaseRef{delivery.lease}, delivery.pool.workers.config.leaseTTL)
	if err != nil {
		delivery.closeLost(err)
		return
	}
	result, call := delivery.pool.workers.callRenew(ctx, request)
	if call.err != nil || result.Len() != 1 || result.items[0].mutation != DeliveryMutationApplied {
		delivery.closeLost(call.err)
		if call.fatal() {
			delivery.pool.fail(call.err)
		}
		return
	}
	renewal := result.items[0]
	delivery.lease = renewal.current
	if renewal.control == DeliveryControlCancelRequested && delivery.cancel != nil {
		delivery.cancel(ErrCancelled)
	}
	if renewal.control == DeliveryControlTerminated {
		delivery.closeLost(ErrTerminated)
	}
}

func (delivery *activeWorkerDelivery) closeLost(error) {
	delivery.closed = true
	delivery.lostOnce.Do(func() { close(delivery.lost) })
}

type workerAttemptController struct {
	delivery *activeWorkerDelivery
}

func (controller workerAttemptController) Pulse(ctx context.Context) error {
	if controller.delivery == nil || nilInterface(ctx) {
		return ErrInvalid
	}
	_, call := controller.delivery.apply(ctx, func(lease LeaseRef) (DeliveryCommand, error) {
		return ProgressCommand(lease)
	})
	return call.err
}

func (controller workerAttemptController) Guard(ctx context.Context, fence LeaseFence) error {
	if controller.delivery == nil || nilInterface(ctx) || nilInterface(fence) {
		return ErrInvalid
	}
	return controller.delivery.guard(ctx, fence)
}

func (controller workerAttemptController) String() string {
	return fmt.Sprintf("[job attempt controller active=%t]", controller.delivery != nil)
}

func (delivery *activeWorkerDelivery) guard(ctx context.Context, fence LeaseFence) (err error) {
	delivery.mu.Lock()
	defer delivery.mu.Unlock()
	if delivery.closed {
		return ErrLeaseLost
	}
	defer func() {
		if recover() != nil {
			err = ErrDriver
		}
	}()
	if err := ctx.Err(); err != nil {
		return err
	}
	return fence.Fence(ctx, delivery.lease)
}
