package jobs

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
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
	startMu        sync.RWMutex
	draining       bool
	drain          chan struct{}
	force          chan struct{}
	drainOnce      sync.Once
	forceOnce      sync.Once
	incarnation    WorkerIncarnation
	active         atomic.Int64
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
		session.startMu.Lock()
		session.draining = true
		session.cancelPoll(ErrCancelled)
		close(session.drain)
		session.startMu.Unlock()
	})
}

func (session *workerRunSession) drainingRequested() bool {
	if session == nil {
		return true
	}
	session.startMu.RLock()
	defer session.startMu.RUnlock()
	return session.draining
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
	startedAt := workers.observeWorkerStart(WorkerOperationRun, 0)
	session.incarnation, err = newWorkerIncarnation(workers.config.entropy)
	if err != nil {
		workers.runtime.finish(err)
		workers.observeWorkerFinish(WorkerOperationRun, WorkerOutcomeFailed, WorkerFailureRuntime, 0, startedAt)
		return err
	}
	err, parentCancelled := workers.run(session)
	workers.runtime.finish(err)
	outcome := WorkerOutcomeComplete
	failure := WorkerFailureNone
	if parentCancelled {
		outcome = WorkerOutcomeCancelled
	} else if err != nil {
		outcome = WorkerOutcomeFailed
		failure = WorkerFailureRuntime
	}
	workers.observeWorkerFinish(WorkerOperationRun, outcome, failure, 0, startedAt)
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
	active := 0
	if session != nil {
		active = int(session.active.Load())
	}
	startedAt := workers.observeWorkerStart(WorkerOperationDrain, active)
	finish := func() error {
		result := workers.runtime.result()
		outcome := WorkerOutcomeComplete
		failure := WorkerFailureNone
		if result != nil {
			outcome = WorkerOutcomeFailed
			failure = WorkerFailureRuntime
		}
		workers.observeWorkerFinish(WorkerOperationDrain, outcome, failure, 0, startedAt)
		return result
	}
	select {
	case <-done:
		return finish()
	default:
	}
	select {
	case <-done:
		return finish()
	case <-ctx.Done():
		select {
		case <-done:
			return finish()
		default:
		}
		if session != nil {
			active = int(session.active.Load())
			session.requestForce()
		}
		workers.observeWorkerFinish(WorkerOperationDrain, WorkerOutcomeForced, WorkerFailureNone, active, startedAt)
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
	workers         *Workers
	session         *workerRunSession
	bindings        map[Name]*workerRuntimeBinding
	admissionGroups []*workerAdmissionGroup
	mu              sync.Mutex
	bytes           int
	active          map[*activeWorkerDelivery]struct{}
	wg              sync.WaitGroup
	failure         chan error
	failOnce        sync.Once
}

type workerRuntimeBinding struct {
	binding        consumerBinding
	admissionGroup *workerAdmissionGroup
	active         int
}

type workerAdmissionGroup struct {
	identity    WorkerAdmissionGroup
	admission   AdmissionReader
	members     []*workerRuntimeBinding
	concurrency int
	cursor      int
	observation workerAdmissionObservation
}

type workerAdmissionObservation struct {
	signal      AdmissionSignal
	outcome     WorkerOutcome
	active      int
	limit       int
	initialized bool
}

func newWorkerPool(workers *Workers, session *workerRunSession) *workerPool {
	bindings := make(map[Name]*workerRuntimeBinding, workers.plan.Len())
	groupsByAdmission := make(map[*admissionCell]*workerAdmissionGroup)
	groups := make([]*workerAdmissionGroup, 0)
	for _, binding := range workers.plan.workerBindings() {
		runtimeBinding := &workerRuntimeBinding{binding: binding}
		if binding.admission.initialized {
			group := groupsByAdmission[binding.admission.cell]
			if group == nil {
				group = &workerAdmissionGroup{identity: binding.admissionGroup, admission: binding.admission}
				groupsByAdmission[binding.admission.cell] = group
				groups = append(groups, group)
			}
			runtimeBinding.admissionGroup = group
			group.members = append(group.members, runtimeBinding)
			group.concurrency += binding.concurrency
		}
		bindings[binding.declaration.declarationName()] = runtimeBinding
	}
	return &workerPool{
		workers:         workers,
		session:         session,
		bindings:        bindings,
		admissionGroups: groups,
		active:          make(map[*activeWorkerDelivery]struct{}),
		failure:         make(chan error, 1),
	}
}

func (workers *Workers) run(session *workerRunSession) (error, bool) {
	pool := newWorkerPool(workers, session)
	heartbeatContext, stopHeartbeat := context.WithCancel(session.handlerContext)
	heartbeatDone := make(chan struct{})
	go func() {
		defer close(heartbeatDone)
		pool.heartbeat(heartbeatContext)
	}()
	defer func() {
		stopHeartbeat()
		<-heartbeatDone
	}()
	dispatchDone := make(chan struct{})
	go func() {
		defer close(dispatchDone)
		pool.dispatch()
	}()
	var result error
	parentCancelled := false
	select {
	case <-session.parent.Done():
		result = context.Cause(session.parent)
		parentCancelled = true
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
		return result, parentCancelled
	}
	if session.parent.Err() == nil {
		select {
		case <-activeDone:
			return result, parentCancelled
		case <-session.force:
			<-activeDone
			return result, parentCancelled
		}
	}
	timer := time.NewTimer(workers.config.shutdownGrace)
	defer timer.Stop()
	select {
	case <-activeDone:
		return result, parentCancelled
	case <-timer.C:
		session.requestForce()
		<-activeDone
		return result, parentCancelled
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
		active, limit, saturated := pool.observationCapacity()
		if saturated {
			pool.workers.observeSaturation(WorkerOperationClaim, active, limit)
		}
		return true
	}
	batch, call := pool.workers.callClaim(pool.session.pollContext, request)
	active, _, _ := pool.observationCapacity()
	pool.workers.observeClaim(batch, call, active)
	if call.err != nil {
		if call.fatal() {
			pool.fail(call.err)
			return false
		}
		return pool.session.pollContext.Err() == nil
	}
	return pool.dispatchClaimed(batch.items)
}

func (pool *workerPool) observationCapacity() (int, int, bool) {
	pool.mu.Lock()
	active := len(pool.active)
	byteSaturated := pool.workers.config.inFlightBytes-pool.bytes < MaxDeliveryRecordBytes
	pool.mu.Unlock()
	limit := pool.workers.plan.TotalConcurrency()
	if active >= limit {
		return active, limit, true
	}
	if byteSaturated && active > 0 {
		return active, active, true
	}
	return active, limit, false
}

func (pool *workerPool) claimRequest() (ClaimRequest, bool, error) {
	now, err := pool.workers.config.clock.Now()
	if err != nil {
		return ClaimRequest{}, false, err
	}
	pool.mu.Lock()
	var observations []workerEventSpec
	defer func() {
		pool.mu.Unlock()
		pool.observeAdmission(observations)
	}()
	remainingBytes := pool.workers.config.inFlightBytes - pool.bytes
	if remainingBytes < MaxDeliveryRecordBytes {
		return ClaimRequest{}, false, nil
	}
	targets := make([]ClaimTarget, 0, len(pool.bindings))
	total := 0
	groupAvailable := make(map[*workerAdmissionGroup]int, len(pool.admissionGroups))
	for _, group := range pool.admissionGroups {
		decision := group.admission.Evaluate(group.concurrency, now)
		active := 0
		for _, member := range group.members {
			active += member.active
		}
		if observation, changed := group.nextObservation(decision, active); changed && pool.workers.config.observer != nil {
			observations = append(observations, observation)
		}
		if decision.Err() != nil {
			continue
		}
		groupAvailable[group] = max(decision.Limit()-active, 0)
	}
	for _, planned := range pool.workers.plan.bindings {
		current := pool.bindings[planned.declaration.declarationName()]
		available := planned.concurrency - current.active
		if current.admissionGroup != nil {
			available = min(available, groupAvailable[current.admissionGroup])
		}
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
	pool.orderClaimTargets(targets)
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

func (pool *workerPool) orderClaimTargets(targets []ClaimTarget) {
	positions := make(map[*workerAdmissionGroup][]int, len(pool.admissionGroups))
	available := make(map[*workerAdmissionGroup]map[*workerRuntimeBinding]ClaimTarget, len(pool.admissionGroups))
	for index, target := range targets {
		binding := pool.bindings[target.definition]
		if binding == nil || binding.admissionGroup == nil {
			continue
		}
		group := binding.admissionGroup
		positions[group] = append(positions[group], index)
		if available[group] == nil {
			available[group] = make(map[*workerRuntimeBinding]ClaimTarget)
		}
		available[group][binding] = target
	}
	for _, group := range pool.admissionGroups {
		groupPositions := positions[group]
		if len(groupPositions) < 2 {
			continue
		}
		ordered := make([]ClaimTarget, 0, len(groupPositions))
		for scanned := 0; scanned < len(group.members); scanned++ {
			member := group.members[(group.cursor+scanned)%len(group.members)]
			if target, ok := available[group][member]; ok {
				ordered = append(ordered, target)
			}
		}
		for index, position := range groupPositions {
			targets[position] = ordered[index]
		}
	}
}

type workerAdmissionDemand struct {
	group  *workerAdmissionGroup
	counts map[*workerRuntimeBinding]int
	total  int
}

type workerAdmissionDecision struct {
	demand  workerAdmissionDemand
	allowed int
}

func (pool *workerPool) dispatchClaimed(items []ClaimedDelivery) bool {
	allowances, err := pool.admitClaimed(items)
	if err != nil {
		shutdown := pool.session.drainingRequested() || errors.Is(err, ErrCancelled) || errors.Is(err, context.Canceled)
		for _, delivery := range items {
			if shutdown {
				pool.releaseClaimed(delivery)
			} else {
				pool.releaseAdmission(delivery)
			}
		}
		if shutdown {
			return false
		}
		pool.fail(err)
		return false
	}
	stopping := false
	for _, delivery := range items {
		if pool.session.drainingRequested() {
			stopping = true
			pool.releaseClaimed(delivery)
			continue
		}
		binding := pool.bindings[delivery.target.definition]
		if binding == nil || allowances[binding] == 0 {
			pool.releaseAdmission(delivery)
			continue
		}
		allowances[binding]--
		if !pool.acceptClaimed(delivery) {
			pool.releaseClaimed(delivery)
		}
	}
	return !stopping && pool.session.pollContext.Err() == nil
}

func (pool *workerPool) admitClaimed(items []ClaimedDelivery) (map[*workerRuntimeBinding]int, error) {
	now, err := pool.workers.config.clock.Now()
	if err != nil {
		return nil, err
	}
	allowances := make(map[*workerRuntimeBinding]int, len(pool.bindings))
	groupDemands := make(map[*workerAdmissionGroup]*workerAdmissionDemand, len(pool.admissionGroups))
	pool.mu.Lock()
	var observations []workerEventSpec
	defer func() {
		pool.mu.Unlock()
		pool.observeAdmission(observations)
	}()
	for _, delivery := range items {
		binding := pool.bindings[delivery.target.definition]
		if binding == nil || binding.binding.binding != delivery.target.binding {
			return nil, invalid("claimed worker binding")
		}
		free := binding.binding.concurrency - binding.active
		if free <= allowances[binding] {
			continue
		}
		if binding.admissionGroup != nil {
			demand := groupDemands[binding.admissionGroup]
			if demand == nil {
				demand = &workerAdmissionDemand{group: binding.admissionGroup, counts: make(map[*workerRuntimeBinding]int)}
				groupDemands[binding.admissionGroup] = demand
			}
			if demand.counts[binding] < free {
				demand.counts[binding]++
				demand.total++
			}
			continue
		}
		allowances[binding]++
	}
	decisions := make([]workerAdmissionDecision, 0, len(groupDemands))
	for _, group := range pool.admissionGroups {
		demand := groupDemands[group]
		if demand == nil || demand.total == 0 {
			continue
		}
		decision := group.admission.Evaluate(group.concurrency, now)
		active := 0
		for _, member := range group.members {
			active += member.active
		}
		if observation, changed := group.nextObservation(decision, active); changed && pool.workers.config.observer != nil {
			observations = append(observations, observation)
		}
		if decision.Err() != nil {
			continue
		}
		allowed := min(max(decision.Limit()-active, 0), demand.total)
		decisions = append(decisions, workerAdmissionDecision{demand: *demand, allowed: allowed})
	}
	for _, decision := range decisions {
		group := decision.demand.group
		remaining := decision.allowed
		for remaining > 0 {
			selected := -1
			for scanned := 0; scanned < len(group.members); scanned++ {
				index := (group.cursor + scanned) % len(group.members)
				member := group.members[index]
				if decision.demand.counts[member] > 0 {
					selected = index
					break
				}
			}
			if selected < 0 {
				return nil, invalid("worker admission demand")
			}
			member := group.members[selected]
			decision.demand.counts[member]--
			allowances[member]++
			remaining--
			group.cursor = (selected + 1) % len(group.members)
		}
	}
	return allowances, nil
}

func (group *workerAdmissionGroup) nextObservation(decision AdmissionDecision, active int) (workerEventSpec, bool) {
	signal := decision.Signal()
	outcome := WorkerOutcomeInvalid
	limit := 0
	switch signal {
	case AdmissionReady, AdmissionUnrestricted:
		limit = decision.Limit()
		if limit > 0 && active >= limit {
			outcome = WorkerOutcomeSaturated
		} else if limit > 0 {
			outcome = WorkerOutcomeReady
		} else {
			signal = AdmissionInvalid
		}
	case AdmissionHeld:
		outcome = WorkerOutcomeHeld
	case AdmissionStale:
		outcome = WorkerOutcomeStale
	case AdmissionUninitialized, AdmissionInvalid:
		outcome = WorkerOutcomeInvalid
	default:
		signal = AdmissionInvalid
	}
	current := workerAdmissionObservation{signal: signal, outcome: outcome, active: active, limit: limit, initialized: true}
	if group.observation == current {
		return workerEventSpec{}, false
	}
	group.observation = current
	return workerEventSpec{
		Operation:       WorkerOperationAdmission,
		Outcome:         outcome,
		AdmissionGroup:  group.identity,
		AdmissionSignal: signal,
		Active:          active,
		Limit:           limit,
	}, true
}

func (pool *workerPool) observeAdmission(observations []workerEventSpec) {
	for _, observation := range observations {
		event, err := newWorkerEvent(pool.workers.plan, observation)
		if err == nil {
			safeObserve(pool.workers.config.observer, context.Background(), event)
		}
	}
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
	pool.session.startMu.RLock()
	if pool.session.draining {
		pool.session.startMu.RUnlock()
		return false
	}
	pool.mu.Lock()
	binding := pool.bindings[delivery.target.definition]
	if binding == nil || binding.binding.binding != delivery.target.binding || binding.active >= binding.binding.concurrency || size > pool.workers.config.inFlightBytes-pool.bytes {
		pool.mu.Unlock()
		pool.session.startMu.RUnlock()
		return false
	}
	active := newActiveWorkerDelivery(pool, binding, delivery.lease, size)
	binding.active++
	pool.bytes += size
	pool.active[active] = struct{}{}
	pool.session.active.Add(1)
	pool.wg.Add(1)
	pool.mu.Unlock()
	go active.runClaimed(delivery)
	pool.session.startMu.RUnlock()
	return true
}

func (pool *workerPool) releaseClaimed(delivery ClaimedDelivery) {
	pool.releaseClaimedWith(delivery, DefaultRetryDelay, ReleaseForShutdownCommand)
}

func (pool *workerPool) releaseAdmission(delivery ClaimedDelivery) {
	pool.releaseClaimedWith(delivery, MinRetryDelay, ReleaseForAdmissionCommand)
}

func (pool *workerPool) releaseClaimedWith(delivery ClaimedDelivery, delay time.Duration, build func(LeaseRef, time.Duration) (DeliveryCommand, error)) {
	command, err := build(delivery.lease, delay)
	if err != nil {
		pool.fail(err)
		return
	}
	request, err := NewApplyRequest(command)
	if err != nil {
		pool.fail(err)
		return
	}
	result, call := pool.workers.callApply(pool.session.controlContext, request)
	pool.workers.observeApply(delivery.target.definition, delivery.target.binding, request, result, call)
	if call.fatal() {
		pool.fail(call.err)
	}
}

func (pool *workerPool) recover() bool {
	for {
		request, ok, err := pool.recoverRequest()
		if err != nil {
			pool.fail(err)
			return false
		}
		if !ok {
			active, limit, saturated := pool.observationCapacity()
			if saturated {
				pool.workers.observeSaturation(WorkerOperationRecover, active, limit)
			}
			return true
		}
		result, call := pool.workers.callRecover(pool.session.pollContext, request)
		active, _, _ := pool.observationCapacity()
		pool.workers.observeRecover(result, call, active)
		if call.err != nil {
			if call.fatal() {
				pool.fail(call.err)
				return false
			}
			return pool.session.pollContext.Err() == nil
		}
		queued := make([]ClaimedDelivery, 0, len(result.items))
		for _, delivery := range result.items {
			claimed, ok, keepRunning := pool.prepareRecovered(delivery)
			if !keepRunning {
				return false
			}
			if ok {
				queued = append(queued, claimed)
			}
		}
		if len(queued) > 0 && !pool.dispatchClaimed(queued) {
			return false
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

func (pool *workerPool) recoverRequest() (RecoverRequest, bool, error) {
	pool.mu.Lock()
	remainingBytes := pool.workers.config.inFlightBytes - pool.bytes
	pool.mu.Unlock()
	if remainingBytes < MaxDeliveryRecordBytes {
		return RecoverRequest{}, false, nil
	}
	request, err := NewRecoverRequest(RecoverRequestSpec{
		Namespace:   pool.workers.config.namespace,
		Incarnation: pool.session.incarnation,
		MaxItems:    min(pool.workers.config.claimItems, MaxReclaimBatch),
		MaxBytes:    min(pool.workers.config.claimBytes, remainingBytes),
		LeaseTTL:    pool.workers.config.leaseTTL,
	})
	return request, err == nil, err
}

func (pool *workerPool) prepareRecovered(delivery RecoveredDelivery) (ClaimedDelivery, bool, bool) {
	restored, err := RestoreDeliveryRecord(pool.workers.config.catalog, delivery.Record())
	if err != nil {
		if errors.Is(err, ErrCorrupt) {
			return ClaimedDelivery{}, false, pool.applyRecovered(delivery.lease, func(lease LeaseRef) (DeliveryCommand, error) { return RejectCorruptCommand(lease) })
		}
		pool.fail(err)
		return ClaimedDelivery{}, false, false
	}
	invocation := restored.Invocation()
	switch invocation.State() {
	case InvocationRunning, InvocationCancelRequested:
		delay := retryBackoffCap(invocation.Policy().Backoff(), invocation.RetrySpent().Value())
		ok := pool.applyRecovered(delivery.lease, func(lease LeaseRef) (DeliveryCommand, error) {
			return RevokeAttemptCommand(lease, ReasonLeaseLost, delay)
		})
		return ClaimedDelivery{}, false, ok
	case InvocationQueued:
		binding := pool.bindings[invocation.Definition()]
		if binding == nil {
			ok := pool.applyRecovered(delivery.lease, func(lease LeaseRef) (DeliveryCommand, error) {
				return ReleaseForShutdownCommand(lease, DefaultRetryDelay)
			})
			return ClaimedDelivery{}, false, ok
		}
		target, targetErr := pool.claimTarget(binding.binding, 1)
		if targetErr != nil {
			pool.fail(targetErr)
			return ClaimedDelivery{}, false, false
		}
		return ClaimedDelivery{target: target, lease: delivery.lease, record: delivery.record}, true, true
	default:
		pool.fail(driverContractError("recover", invalid("recovered invocation state")))
		return ClaimedDelivery{}, false, false
	}
}

func (pool *workerPool) applyRecovered(lease LeaseRef, build func(LeaseRef) (DeliveryCommand, error)) bool {
	command, err := build(lease)
	if err != nil {
		pool.fail(err)
		return false
	}
	request, err := NewApplyRequest(command)
	if err != nil {
		pool.fail(err)
		return false
	}
	result, call := pool.workers.callApply(pool.session.controlContext, request)
	pool.workers.observeApply(Name{}, BindingName{}, request, result, call)
	if call.fatal() {
		pool.fail(call.err)
		return false
	}
	return pool.session.pollContext.Err() == nil
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
	pool           *workerPool
	binding        *workerRuntimeBinding
	size           int
	mu             sync.Mutex
	lease          LeaseRef
	invocation     Invocation
	closed         bool
	lost           chan struct{}
	lostOnce       sync.Once
	handlerContext context.Context
	cancel         context.CancelCauseFunc
}

func newActiveWorkerDelivery(pool *workerPool, binding *workerRuntimeBinding, lease LeaseRef, size int) *activeWorkerDelivery {
	handlerContext, cancel := context.WithCancelCause(pool.session.handlerContext)
	return &activeWorkerDelivery{
		pool:           pool,
		binding:        binding,
		size:           size,
		lease:          cloneLeaseRef(lease),
		lost:           make(chan struct{}),
		handlerContext: handlerContext,
		cancel:         cancel,
	}
}

func (delivery *activeWorkerDelivery) runClaimed(claimed ClaimedDelivery) {
	ctx := delivery.handlerContext
	defer func() {
		delivery.cancel(context.Canceled)
		delivery.pool.mu.Lock()
		delivery.binding.active--
		delivery.pool.bytes -= delivery.size
		delete(delivery.pool.active, delivery)
		delivery.pool.mu.Unlock()
		delivery.pool.session.active.Add(-1)
		delivery.pool.wg.Done()
	}()
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
		RetrySpent:       application.invocation.RetrySpent(),
		RetryLimit:       application.invocation.Policy().RetryLimit(),
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
	handled := make(chan error, 1)
	session := delivery.pool.session
	session.startMu.RLock()
	if session.draining {
		session.startMu.RUnlock()
		delivery.revoke(session.controlContext, ReasonShutdown)
		return
	}
	go func() {
		if delivery.binding.binding.mode == consumerHandlerAdapter {
			handled <- delivery.binding.binding.handleAdapter(preparation.context, preparation.decoded, meta, workerAttemptController{delivery: delivery})
			return
		}
		handled <- delivery.binding.binding.handle(preparation.context, preparation.decoded)
	}()
	session.startMu.RUnlock()
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
	if delivery.closed {
		delivery.mu.Unlock()
		return ApplyResult{}, workerDriverCall{outcome: WorkerOutcomeCancelled, err: ErrLeaseLost}
	}
	command, err := build(delivery.lease)
	if err != nil {
		delivery.closeLost(err)
		delivery.mu.Unlock()
		return ApplyResult{}, workerDriverCall{outcome: WorkerOutcomeFailed, failure: WorkerFailureRuntime, err: err}
	}
	request, err := NewApplyRequest(command)
	if err != nil {
		delivery.closeLost(err)
		delivery.mu.Unlock()
		return ApplyResult{}, workerDriverCall{outcome: WorkerOutcomeFailed, failure: WorkerFailureRuntime, err: err}
	}
	result, call := delivery.pool.workers.callApply(ctx, request)
	definition := delivery.binding.binding.declaration.declarationName()
	binding := delivery.binding.binding.binding
	if call.err != nil || result.result.mutation != DeliveryMutationApplied {
		delivery.closeLost(call.err)
		delivery.mu.Unlock()
		delivery.pool.workers.observeApply(definition, binding, request, result, call)
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
	delivery.mu.Unlock()
	delivery.pool.workers.observeApply(definition, binding, request, result, call)
	return result, call
}

func (pool *workerPool) heartbeat(ctx context.Context) {
	ticker := time.NewTicker(pool.workers.config.heartbeat)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if !pool.renewActive(ctx) {
				return
			}
		}
	}
}

func (pool *workerPool) renewActive(ctx context.Context) bool {
	pool.mu.Lock()
	active := make([]*activeWorkerDelivery, 0, len(pool.active))
	for delivery := range pool.active {
		active = append(active, delivery)
	}
	pool.mu.Unlock()
	for offset := 0; offset < len(active); offset += MaxClaimItems {
		end := min(offset+MaxClaimItems, len(active))
		if !pool.renewActiveBatch(ctx, active[offset:end]) {
			return false
		}
	}
	return true
}

func (pool *workerPool) renewActiveBatch(ctx context.Context, batch []*activeWorkerDelivery) bool {
	locked := make([]*activeWorkerDelivery, 0, len(batch))
	leases := make([]LeaseRef, 0, len(batch))
	for _, delivery := range batch {
		delivery.mu.Lock()
		if delivery.closed {
			delivery.mu.Unlock()
			continue
		}
		locked = append(locked, delivery)
		leases = append(leases, delivery.lease)
	}
	if len(leases) == 0 {
		return true
	}
	request, err := NewRenewRequest(leases, pool.workers.config.leaseTTL)
	if err != nil {
		for _, delivery := range locked {
			delivery.closeLost(err)
			delivery.mu.Unlock()
		}
		pool.fail(err)
		return false
	}
	result, call := pool.workers.callRenew(ctx, request)
	if call.err != nil || result.Len() != len(locked) {
		for _, delivery := range locked {
			delivery.closeLost(call.err)
			delivery.mu.Unlock()
		}
		pool.workers.observeRenew(request, result, call)
		if call.fatal() {
			pool.fail(call.err)
			return false
		}
		return true
	}
	for index, delivery := range locked {
		renewal := result.items[index]
		if renewal.mutation != DeliveryMutationApplied {
			delivery.closeLost(nil)
		} else {
			delivery.lease = renewal.current
			if renewal.control == DeliveryControlCancelRequested && delivery.cancel != nil {
				delivery.cancel(ErrCancelled)
			}
			if renewal.control == DeliveryControlTerminated {
				delivery.closeLost(ErrTerminated)
			}
		}
		delivery.mu.Unlock()
	}
	pool.workers.observeRenew(request, result, call)
	return true
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
	if err := ctx.Err(); err != nil {
		return err
	}
	controller.delivery.mu.Lock()
	closed := controller.delivery.closed
	progressTimeout := controller.delivery.invocation.Policy().ProgressTimeout()
	controller.delivery.mu.Unlock()
	if closed {
		return ErrLeaseLost
	}
	if progressTimeout == 0 {
		return ErrUnsupported
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
