package jobs

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type workerRuntimeObserver struct {
	mu     sync.Mutex
	events []WorkerEvent
}

type blockingWorkerClaimDriver struct {
	*workersRunDriver
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

type observedWorkerClaimDriver struct {
	*workersRunDriver
	claim func(context.Context, ClaimRequest) (ClaimBatch, error)
}

func (driver *observedWorkerClaimDriver) Claim(ctx context.Context, request ClaimRequest) (ClaimBatch, error) {
	return driver.claim(ctx, request)
}

func (driver *blockingWorkerClaimDriver) Claim(context.Context, ClaimRequest) (ClaimBatch, error) {
	driver.once.Do(func() { close(driver.entered) })
	<-driver.release
	return NewClaimBatch(driver.observedAt, nil)
}

func (observer *workerRuntimeObserver) Observe(_ context.Context, event WorkerEvent) {
	observer.mu.Lock()
	observer.events = append(observer.events, event)
	observer.mu.Unlock()
}

func (observer *workerRuntimeObserver) snapshot() []WorkerEvent {
	observer.mu.Lock()
	defer observer.mu.Unlock()
	return append([]WorkerEvent(nil), observer.events...)
}

func TestWorkersRuntimeEmitsLifecycleAndDriverEvents(t *testing.T) {
	fixture := newWorkerDeliveryFixture(t, PlacementRegular)
	driver := &workersRunDriver{
		description: queueTestBackendDescription(1),
		record:      fixture.record,
		lease:       fixture.lease,
		invocation:  fixture.invocation,
		observedAt:  fixture.invocation.EligibleAt(),
		finished:    make(chan struct{}),
	}
	observer := &workerRuntimeObserver{}
	consumer := On(fixture.definition, Handler[string](func(context.Context, string) error { return nil }), Binding("worker.observer"), Concurrency(1))
	workers, err := NewWorkers(WorkersSpec{
		Namespace: fixture.namespace,
		Catalog:   fixture.catalog,
		Driver:    driver,
		Build:     fixture.build,
		Identity:  workerDeliveryIdentityRestorer(t),
		Observer:  observer,
		Entropy:   bytes.NewReader(bytes.Repeat([]byte{1}, WorkerIncarnationBytes)),
	}, consumer)
	if err != nil {
		t.Fatal(err)
	}
	runResult := make(chan error, 1)
	go func() { runResult <- workers.Run(context.Background()) }()
	select {
	case <-driver.finished:
	case <-time.After(3 * time.Second):
		t.Fatal("worker did not finish delivery")
	}
	drainContext, cancelDrain := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancelDrain()
	if err := workers.Drain(drainContext); err != nil {
		t.Fatal(err)
	}
	if err := <-runResult; err != nil {
		t.Fatal(err)
	}
	events := observer.snapshot()
	assertWorkerRuntimeEvent(t, events, WorkerOperationRun, WorkerOutcomeStarted)
	assertWorkerRuntimeEvent(t, events, WorkerOperationRun, WorkerOutcomeComplete)
	assertWorkerRuntimeEvent(t, events, WorkerOperationDrain, WorkerOutcomeStarted)
	assertWorkerRuntimeEvent(t, events, WorkerOperationDrain, WorkerOutcomeComplete)
	assertWorkerRuntimeEvent(t, events, WorkerOperationRecover, WorkerOutcomeEmpty)
	claim := assertWorkerRuntimeEvent(t, events, WorkerOperationClaim, WorkerOutcomeComplete)
	if claim.Items() != 1 || claim.Bytes() == 0 {
		t.Fatalf("claim event metrics = items %d bytes %d", claim.Items(), claim.Bytes())
	}
	begin := assertWorkerRuntimeApplyEvent(t, events, WorkerOperationApply, DeliveryCommandBeginAttempt)
	finish := assertWorkerRuntimeApplyEvent(t, events, WorkerOperationApply, DeliveryCommandFinishAttempt)
	for _, event := range []WorkerEvent{begin, finish} {
		if event.Outcome() != WorkerOutcomeComplete || event.Definition() != fixture.definition.Name() || event.Binding().Value() != "worker.observer" || len(event.Results()) != 1 {
			t.Fatalf("apply event = %#v", event)
		}
	}
}

func TestWorkerPoolEmitsAggregatedRenewResult(t *testing.T) {
	fixture := newWorkerDeliveryFixture(t, PlacementRegular)
	driver := &workersRunDriver{
		description: queueTestBackendDescription(1),
		observedAt:  fixture.invocation.EligibleAt(),
		finished:    make(chan struct{}),
	}
	observer := &workerRuntimeObserver{}
	consumer := On(fixture.definition, Handler[string](func(context.Context, string) error { return nil }), Binding("worker.observer"), Concurrency(2))
	workers, err := NewWorkers(WorkersSpec{
		Namespace: fixture.namespace,
		Catalog:   fixture.catalog,
		Driver:    driver,
		Build:     fixture.build,
		Identity:  workerDeliveryIdentityRestorer(t),
		Observer:  observer,
		Entropy:   bytes.NewReader(bytes.Repeat([]byte{1}, WorkerIncarnationBytes)),
	}, consumer)
	if err != nil {
		t.Fatal(err)
	}
	pool := newWorkerPool(workers, newWorkerRunSession(context.Background()))
	binding := pool.bindings[fixture.definition.Name()]
	secondLease, err := NewLeaseRef(fixture.lease.Backend(), queueTestInvocationID(t, 2), []byte("observer-second-lease"))
	if err != nil {
		t.Fatal(err)
	}
	pool.active[newActiveWorkerDelivery(pool, binding, fixture.lease, 1)] = struct{}{}
	pool.active[newActiveWorkerDelivery(pool, binding, secondLease, 1)] = struct{}{}
	if !pool.renewActive(t.Context()) {
		t.Fatal("renew stopped the worker pool")
	}
	event := assertWorkerRuntimeEvent(t, observer.snapshot(), WorkerOperationRenew, WorkerOutcomeComplete)
	results := event.Results()
	if event.Items() != 2 || len(results) != 1 || results[0].Mutation() != DeliveryMutationApplied || results[0].Control() != DeliveryControlNone || results[0].Items() != 2 {
		t.Fatalf("renew event = items %d results %#v", event.Items(), results)
	}
}

func TestWorkersRuntimeClassifiesCustomParentCauseAsCancellation(t *testing.T) {
	fixture := newWorkerDeliveryFixture(t, PlacementRegular)
	driver := &workersRunDriver{
		description: queueTestBackendDescription(1),
		observedAt:  fixture.invocation.EligibleAt(),
		claimed:     true,
		finished:    make(chan struct{}),
	}
	started := make(chan struct{})
	var startedOnce sync.Once
	observer := &workerRuntimeObserver{}
	combined := WorkerObserverFunc(func(ctx context.Context, event WorkerEvent) {
		observer.Observe(ctx, event)
		if event.Operation() == WorkerOperationRun && event.Outcome() == WorkerOutcomeStarted {
			startedOnce.Do(func() { close(started) })
		}
	})
	workers := newObservedRuntimeWorkers(t, fixture, driver, combined, "worker.cancel")
	ctx, cancel := context.WithCancelCause(context.Background())
	runResult := make(chan error, 1)
	go func() { runResult <- workers.Run(ctx) }()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("worker run did not start")
	}
	cause := errors.New("deployment stopped")
	cancel(cause)
	select {
	case err := <-runResult:
		if !errors.Is(err, cause) {
			t.Fatalf("run cause = %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("worker run did not stop")
	}
	assertWorkerRuntimeEvent(t, observer.snapshot(), WorkerOperationRun, WorkerOutcomeCancelled)
	for _, event := range observer.snapshot() {
		if event.Operation() == WorkerOperationRun && event.Outcome() == WorkerOutcomeFailed {
			t.Fatal("custom parent cancellation was reported as runtime failure")
		}
	}
}

func TestWorkerPoolEmitsCapacitySaturationWithoutDriverCalls(t *testing.T) {
	fixture := newWorkerDeliveryFixture(t, PlacementRegular)
	driver := &workersRunDriver{
		description: queueTestBackendDescription(1),
		observedAt:  fixture.invocation.EligibleAt(),
		claimed:     true,
		finished:    make(chan struct{}),
	}
	observer := &workerRuntimeObserver{}
	workers := newObservedRuntimeWorkers(t, fixture, driver, observer, "worker.saturated")
	pool := newWorkerPool(workers, newWorkerRunSession(context.Background()))
	binding := pool.bindings[fixture.definition.Name()]
	binding.active = 1
	pool.active[newActiveWorkerDelivery(pool, binding, fixture.lease, 1)] = struct{}{}
	if !pool.claim() {
		t.Fatal("capacity-only claim stopped the pool")
	}
	pool.bytes = workers.config.inFlightBytes
	if !pool.recover() {
		t.Fatal("capacity-only recover stopped the pool")
	}
	claim := assertWorkerRuntimeEvent(t, observer.snapshot(), WorkerOperationClaim, WorkerOutcomeSaturated)
	recover := assertWorkerRuntimeEvent(t, observer.snapshot(), WorkerOperationRecover, WorkerOutcomeSaturated)
	for _, event := range []WorkerEvent{claim, recover} {
		if event.Active() != 1 || event.Limit() != 1 {
			t.Fatalf("saturation metrics = active %d limit %d", event.Active(), event.Limit())
		}
	}
}

func TestWorkerPoolEmitsAggregateApplyForRecoveredCleanup(t *testing.T) {
	fixture := newWorkerDeliveryFixture(t, PlacementRegular)
	driver := &workersRunDriver{
		description: queueTestBackendDescription(1),
		invocation:  fixture.invocation,
		observedAt:  fixture.invocation.EligibleAt(),
		finished:    make(chan struct{}),
	}
	observer := &workerRuntimeObserver{}
	workers := newObservedRuntimeWorkers(t, fixture, driver, observer, "worker.recovered")
	pool := newWorkerPool(workers, newWorkerRunSession(context.Background()))
	if !pool.applyRecovered(fixture.lease, func(lease LeaseRef) (DeliveryCommand, error) {
		return ReleaseForShutdownCommand(lease, DefaultRetryDelay)
	}) {
		t.Fatal("recovered cleanup stopped the pool")
	}
	event := assertWorkerRuntimeApplyEvent(t, observer.snapshot(), WorkerOperationApply, DeliveryCommandReleaseUnchanged)
	if event.Outcome() != WorkerOutcomeComplete || !event.Definition().IsZero() || !event.Binding().IsZero() || len(event.Results()) != 1 {
		t.Fatalf("recovered apply event = %#v", event)
	}
}

func TestWorkersForcedDrainReportsActualZeroActive(t *testing.T) {
	fixture := newWorkerDeliveryFixture(t, PlacementRegular)
	base := &workersRunDriver{
		description: queueTestBackendDescription(1),
		observedAt:  fixture.invocation.EligibleAt(),
		finished:    make(chan struct{}),
	}
	driver := &blockingWorkerClaimDriver{workersRunDriver: base, entered: make(chan struct{}), release: make(chan struct{})}
	observer := &workerRuntimeObserver{}
	workers := newObservedRuntimeWorkers(t, fixture, driver, observer, "worker.forced")
	runResult := make(chan error, 1)
	go func() { runResult <- workers.Run(context.Background()) }()
	select {
	case <-driver.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("worker did not enter claim")
	}
	drainContext, cancelDrain := context.WithCancel(context.Background())
	cancelDrain()
	if err := workers.Drain(drainContext); !errors.Is(err, context.Canceled) {
		t.Fatalf("forced drain = %v", err)
	}
	event := assertWorkerRuntimeEvent(t, observer.snapshot(), WorkerOperationDrain, WorkerOutcomeForced)
	if event.Active() != 0 {
		t.Fatalf("forced drain active = %d", event.Active())
	}
	close(driver.release)
	select {
	case err := <-runResult:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("forced worker did not stop")
	}
	assertWorkerRuntimeEvent(t, observer.snapshot(), WorkerOperationClaim, WorkerOutcomeCancelled)
}

func TestWorkerPoolEmitsDriverFailureAndTimeout(t *testing.T) {
	tests := []struct {
		name    string
		outcome WorkerOutcome
		failure WorkerFailure
		claim   func(context.Context, ClaimRequest) (ClaimBatch, error)
	}{
		{
			name:    "failure",
			outcome: WorkerOutcomeFailed,
			failure: WorkerFailureDriver,
			claim: func(context.Context, ClaimRequest) (ClaimBatch, error) {
				return ClaimBatch{}, errors.New("driver unavailable")
			},
		},
		{
			name:    "timeout",
			outcome: WorkerOutcomeTimedOut,
			claim: func(ctx context.Context, _ ClaimRequest) (ClaimBatch, error) {
				<-ctx.Done()
				return ClaimBatch{}, ctx.Err()
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newWorkerDeliveryFixture(t, PlacementRegular)
			base := &workersRunDriver{
				description: queueTestBackendDescription(1),
				observedAt:  fixture.invocation.EligibleAt(),
				finished:    make(chan struct{}),
			}
			driver := &observedWorkerClaimDriver{workersRunDriver: base, claim: test.claim}
			observer := &workerRuntimeObserver{}
			workers := newObservedRuntimeWorkers(t, fixture, driver, observer, "worker.driver-event")
			workers.config.operationTimeout = MinimumOperationTimeout
			session := newWorkerRunSession(context.Background())
			session.incarnation = driverTestWorkerIncarnation(t)
			pool := newWorkerPool(workers, session)
			if !pool.claim() {
				t.Fatal("recoverable driver event stopped the pool")
			}
			event := assertWorkerRuntimeEvent(t, observer.snapshot(), WorkerOperationClaim, test.outcome)
			if event.Failure() != test.failure {
				t.Fatalf("driver event failure = %s", event.Failure())
			}
		})
	}
}

func newObservedRuntimeWorkers(t *testing.T, fixture workerDeliveryFixture, driver DeliveryDriver, observer WorkerObserver, binding string) *Workers {
	t.Helper()
	consumer := On(fixture.definition, Handler[string](func(context.Context, string) error { return nil }), Binding(binding), Concurrency(1))
	workers, err := NewWorkers(WorkersSpec{
		Namespace: fixture.namespace,
		Catalog:   fixture.catalog,
		Driver:    driver,
		Build:     fixture.build,
		Identity:  workerDeliveryIdentityRestorer(t),
		Observer:  observer,
		Entropy:   bytes.NewReader(bytes.Repeat([]byte{1}, WorkerIncarnationBytes)),
	}, consumer)
	if err != nil {
		t.Fatal(err)
	}
	return workers
}

func assertWorkerRuntimeEvent(t *testing.T, events []WorkerEvent, operation WorkerOperation, outcome WorkerOutcome) WorkerEvent {
	t.Helper()
	for _, event := range events {
		if event.Operation() == operation && event.Outcome() == outcome {
			return event
		}
	}
	t.Fatalf("missing worker event %s/%s in %#v", operation, outcome, events)
	return WorkerEvent{}
}

func assertWorkerRuntimeApplyEvent(t *testing.T, events []WorkerEvent, operation WorkerOperation, kind DeliveryCommandKind) WorkerEvent {
	t.Helper()
	for _, event := range events {
		if event.Operation() == operation && event.CommandKind() == kind {
			return event
		}
	}
	t.Fatalf("missing worker apply event %s in %#v", kind, events)
	return WorkerEvent{}
}
