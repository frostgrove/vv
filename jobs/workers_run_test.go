package jobs

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type workersRunDriver struct {
	mu             sync.Mutex
	description    BackendDescription
	record         DeliveryRecord
	lease          LeaseRef
	invocation     Invocation
	observedAt     time.Time
	claimed        bool
	finished       chan struct{}
	finishOnce     sync.Once
	beginOnce      sync.Once
	kinds          []DeliveryCommandKind
	reasons        []Reason
	delays         []time.Duration
	deadlineDelays []time.Duration
	renewSizes     []int
	beginReached   chan struct{}
	beginRelease   chan struct{}
}

func (driver *workersRunDriver) Description() BackendDescription {
	return driver.description
}

func (driver *workersRunDriver) Claim(_ context.Context, request ClaimRequest) (ClaimBatch, error) {
	driver.mu.Lock()
	defer driver.mu.Unlock()
	if driver.claimed {
		return NewClaimBatch(driver.observedAt, nil)
	}
	driver.claimed = true
	delivery, err := NewClaimedDelivery(request.targets[0], driver.lease, driver.record)
	if err != nil {
		return ClaimBatch{}, err
	}
	return NewClaimBatch(driver.observedAt, []ClaimedDelivery{delivery})
}

func (driver *workersRunDriver) Renew(_ context.Context, request RenewRequest) (RenewResult, error) {
	driver.mu.Lock()
	defer driver.mu.Unlock()
	driver.renewSizes = append(driver.renewSizes, len(request.leases))
	items := make([]LeaseRenewal, len(request.leases))
	for index, lease := range request.leases {
		renewal, err := NewLeaseRenewal(lease, lease, DeliveryMutationApplied, DeliveryControlNone)
		if err != nil {
			return RenewResult{}, err
		}
		items[index] = renewal
	}
	return NewRenewResult(driver.observedAt, items)
}

func TestWorkerPoolBatchesEveryActiveLeaseIntoOneRenewCall(t *testing.T) {
	fixture := newWorkerDeliveryFixture(t, PlacementRegular)
	driver := &workersRunDriver{
		description: queueTestBackendDescription(1),
		observedAt:  fixture.invocation.EligibleAt(),
		finished:    make(chan struct{}),
	}
	consumer := On(fixture.definition, Handler[string](func(context.Context, string) error { return nil }), Binding("worker.primary"), Concurrency(2))
	workers, err := NewWorkers(WorkersSpec{
		Namespace: fixture.namespace,
		Catalog:   fixture.catalog,
		Driver:    driver,
		Build:     fixture.build,
		Identity:  workerDeliveryIdentityRestorer(t),
		Entropy:   bytes.NewReader(bytes.Repeat([]byte{1}, WorkerIncarnationBytes)),
	}, consumer)
	if err != nil {
		t.Fatal(err)
	}
	pool := newWorkerPool(workers, newWorkerRunSession(context.Background()))
	binding := pool.bindings[fixture.definition.Name()]
	secondLease, err := NewLeaseRef(fixture.lease.Backend(), queueTestInvocationID(t, 2), []byte("second-active-lease"))
	if err != nil {
		t.Fatal(err)
	}
	first := newActiveWorkerDelivery(pool, binding, fixture.lease, 1)
	second := newActiveWorkerDelivery(pool, binding, secondLease, 1)
	pool.active[first] = struct{}{}
	pool.active[second] = struct{}{}
	if !pool.renewActive(t.Context()) {
		t.Fatal("batched renew stopped the worker pool")
	}
	driver.mu.Lock()
	defer driver.mu.Unlock()
	if len(driver.renewSizes) != 1 || driver.renewSizes[0] != 2 {
		t.Fatalf("renew batches = %v", driver.renewSizes)
	}
}

func TestWorkerPoolShardsRenewalsAtTheDriverContractLimit(t *testing.T) {
	fixture := newWorkerDeliveryFixture(t, PlacementRegular)
	driver := &workersRunDriver{
		description: queueTestBackendDescription(1),
		observedAt:  fixture.invocation.EligibleAt(),
		finished:    make(chan struct{}),
	}
	consumer := On(fixture.definition, Handler[string](func(context.Context, string) error { return nil }), Binding("worker.primary"), Concurrency(1))
	workers, err := NewWorkers(WorkersSpec{
		Namespace: fixture.namespace,
		Catalog:   fixture.catalog,
		Driver:    driver,
		Build:     fixture.build,
		Identity:  workerDeliveryIdentityRestorer(t),
		Entropy:   bytes.NewReader(bytes.Repeat([]byte{1}, WorkerIncarnationBytes)),
	}, consumer)
	if err != nil {
		t.Fatal(err)
	}
	pool := newWorkerPool(workers, newWorkerRunSession(context.Background()))
	binding := pool.bindings[fixture.definition.Name()]
	for index := 0; index < MaxClaimItems+1; index++ {
		var raw [InvocationIDBytes]byte
		raw[0] = byte(index + 1)
		raw[1] = byte(index>>8) + 1
		id, idErr := InvocationIDFromBytes(raw)
		if idErr != nil {
			t.Fatal(idErr)
		}
		lease, leaseErr := NewLeaseRef(fixture.lease.Backend(), id, []byte{byte(index), byte(index >> 8), 1})
		if leaseErr != nil {
			t.Fatal(leaseErr)
		}
		pool.active[newActiveWorkerDelivery(pool, binding, lease, 1)] = struct{}{}
	}
	if !pool.renewActive(t.Context()) {
		t.Fatal("sharded renew stopped the worker pool")
	}
	driver.mu.Lock()
	defer driver.mu.Unlock()
	if len(driver.renewSizes) != 2 || driver.renewSizes[0] != MaxClaimItems || driver.renewSizes[1] != 1 {
		t.Fatalf("renew shards = %v", driver.renewSizes)
	}
}

func (driver *workersRunDriver) Apply(ctx context.Context, request ApplyRequest) (ApplyResult, error) {
	if request.command.kind == DeliveryCommandBeginAttempt && driver.beginReached != nil {
		driver.beginOnce.Do(func() { close(driver.beginReached) })
		select {
		case <-driver.beginRelease:
		case <-ctx.Done():
			return ApplyResult{}, ctx.Err()
		}
	}
	driver.mu.Lock()
	defer driver.mu.Unlock()
	application, err := ApplyDeliveryCommand(driver.invocation, request.command, driver.observedAt)
	if err != nil {
		return ApplyResult{}, err
	}
	result, err := NewDeliveryCommandResult(DeliveryMutationApplied, authoritativeApplicationControl(application))
	if err != nil {
		return ApplyResult{}, err
	}
	applied, err := NewApplyResult(driver.observedAt, result, application)
	if err != nil {
		return ApplyResult{}, err
	}
	if !application.invocation.IsZero() {
		driver.invocation = application.invocation
	}
	driver.kinds = append(driver.kinds, request.command.kind)
	driver.reasons = append(driver.reasons, request.command.reason)
	driver.delays = append(driver.delays, request.command.delay)
	driver.deadlineDelays = append(driver.deadlineDelays, request.command.deadlineDelay)
	driver.observedAt = driver.observedAt.Add(time.Millisecond)
	if request.command.kind == DeliveryCommandFinishAttempt {
		driver.finishOnce.Do(func() { close(driver.finished) })
	}
	return applied, nil
}

func (driver *workersRunDriver) Recover(context.Context, RecoverRequest) (RecoverResult, error) {
	driver.mu.Lock()
	defer driver.mu.Unlock()
	return NewRecoverResult(driver.observedAt, nil, 0, false)
}

func TestWorkersRunProcessesClaimedDelivery(t *testing.T) {
	fixture := newWorkerDeliveryFixture(t, PlacementRegular)
	driver := &workersRunDriver{
		description: queueTestBackendDescription(1),
		record:      fixture.record,
		lease:       fixture.lease,
		invocation:  fixture.invocation,
		observedAt:  fixture.invocation.EligibleAt(),
		finished:    make(chan struct{}),
	}
	handled := make(chan string, 1)
	consumer := On(fixture.definition, Handler[string](func(_ context.Context, value string) error {
		handled <- value
		return nil
	}), Binding("worker.primary"), Concurrency(1))
	workers, err := NewWorkers(WorkersSpec{
		Namespace: fixture.namespace,
		Catalog:   fixture.catalog,
		Driver:    driver,
		Build:     fixture.build,
		Identity:  workerDeliveryIdentityRestorer(t),
		Entropy:   bytes.NewReader(bytes.Repeat([]byte{1}, WorkerIncarnationBytes+16)),
	}, consumer)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	runResult := make(chan error, 1)
	go func() { runResult <- workers.Run(ctx) }()
	select {
	case <-driver.finished:
		cancel()
	case <-time.After(3 * time.Second):
		t.Fatal("worker did not finish delivery")
	}
	select {
	case err = <-runResult:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("run = %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("worker did not stop")
	}
	select {
	case value := <-handled:
		if value != "secret payload" {
			t.Fatalf("payload = %q", value)
		}
	default:
		t.Fatal("handler was not called")
	}
	driver.mu.Lock()
	defer driver.mu.Unlock()
	if driver.invocation.State() != InvocationSucceeded || len(driver.kinds) != 2 || driver.kinds[0] != DeliveryCommandBeginAttempt || driver.kinds[1] != DeliveryCommandFinishAttempt {
		t.Fatalf("delivery state=%s commands=%v", driver.invocation.State(), driver.kinds)
	}
}

func TestWorkersRunSamplesFullJitterBeforeRetrySettlement(t *testing.T) {
	fixture := newWorkerDeliveryFixture(t, PlacementRegular)
	driver := &workersRunDriver{
		description: queueTestBackendDescription(1),
		record:      fixture.record,
		lease:       fixture.lease,
		invocation:  fixture.invocation,
		observedAt:  fixture.invocation.EligibleAt(),
		finished:    make(chan struct{}),
	}
	consumer := On(fixture.definition, Handler[string](func(context.Context, string) error {
		return errors.New("retry")
	}), Binding("worker.jitter"), Concurrency(1))
	entropy := append(bytes.Repeat([]byte{1}, WorkerIncarnationBytes), make([]byte, 16)...)
	workers, err := NewWorkers(WorkersSpec{
		Namespace: fixture.namespace,
		Catalog:   fixture.catalog,
		Driver:    driver,
		Build:     fixture.build,
		Identity:  workerDeliveryIdentityRestorer(t),
		Entropy:   bytes.NewReader(entropy),
	}, consumer)
	if err != nil {
		t.Fatal(err)
	}
	runResult := make(chan error, 1)
	go func() { runResult <- workers.Run(context.Background()) }()
	select {
	case <-driver.finished:
	case <-time.After(3 * time.Second):
		t.Fatal("worker did not settle retry")
	}
	drainContext, cancelDrain := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancelDrain()
	if err = workers.Drain(drainContext); err != nil {
		t.Fatal(err)
	}
	if err = <-runResult; err != nil {
		t.Fatal(err)
	}
	driver.mu.Lock()
	defer driver.mu.Unlock()
	if driver.invocation.State() != InvocationQueued || len(driver.kinds) != 2 || driver.kinds[1] != DeliveryCommandFinishAttempt || driver.delays[1] != MinRetryDelay || driver.deadlineDelays[1] != MinRetryDelay {
		t.Fatalf("retry state=%s commands=%v delays=%v deadline-delays=%v", driver.invocation.State(), driver.kinds, driver.delays, driver.deadlineDelays)
	}
}

func TestWorkersDrainWhileBeginIsBlockedDoesNotStartHandler(t *testing.T) {
	fixture := newWorkerDeliveryFixture(t, PlacementRegular)
	driver := &workersRunDriver{
		description:  queueTestBackendDescription(1),
		record:       fixture.record,
		lease:        fixture.lease,
		invocation:   fixture.invocation,
		observedAt:   fixture.invocation.EligibleAt(),
		finished:     make(chan struct{}),
		beginReached: make(chan struct{}),
		beginRelease: make(chan struct{}),
	}
	handled := make(chan struct{}, 1)
	consumer := On(fixture.definition, Handler[string](func(context.Context, string) error {
		handled <- struct{}{}
		return nil
	}), Binding("worker.primary"), Concurrency(1))
	workers, err := NewWorkers(WorkersSpec{
		Namespace: fixture.namespace,
		Catalog:   fixture.catalog,
		Driver:    driver,
		Build:     fixture.build,
		Identity:  workerDeliveryIdentityRestorer(t),
		Entropy:   bytes.NewReader(bytes.Repeat([]byte{1}, WorkerIncarnationBytes+16)),
	}, consumer)
	if err != nil {
		t.Fatal(err)
	}
	runResult := make(chan error, 1)
	go func() { runResult <- workers.Run(context.Background()) }()
	select {
	case <-driver.beginReached:
	case <-time.After(3 * time.Second):
		t.Fatal("worker did not reach begin attempt")
	}
	workers.runtime.mu.Lock()
	session := workers.runtime.session
	workers.runtime.mu.Unlock()
	drainContext, cancelDrain := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancelDrain()
	drainResult := make(chan error, 1)
	go func() { drainResult <- workers.Drain(drainContext) }()
	deadline := time.Now().Add(time.Second)
	for !session.drainingRequested() {
		if time.Now().After(deadline) {
			t.Fatal("worker did not enter drain")
		}
		time.Sleep(time.Millisecond)
	}
	close(driver.beginRelease)
	if err = <-drainResult; err != nil {
		t.Fatal(err)
	}
	if err = <-runResult; err != nil {
		t.Fatal(err)
	}
	select {
	case <-handled:
		t.Fatal("handler started after drain")
	default:
	}
	driver.mu.Lock()
	defer driver.mu.Unlock()
	if len(driver.kinds) != 2 || driver.kinds[0] != DeliveryCommandBeginAttempt || driver.kinds[1] != DeliveryCommandRevokeAttempt || driver.reasons[1] != ReasonShutdown {
		t.Fatalf("drain commands=%v reasons=%v", driver.kinds, driver.reasons)
	}
}

func TestWorkerAttemptControllerDoesNotLoseLeaseWhenProgressIsDisabled(t *testing.T) {
	fixture := newWorkerDeliveryFixture(t, PlacementRegular)
	delivery := &activeWorkerDelivery{invocation: fixture.invocation}
	err := (workerAttemptController{delivery: delivery}).Pulse(t.Context())
	if !errors.Is(err, ErrUnsupported) || delivery.closed {
		t.Fatalf("pulse = %v, closed = %t", err, delivery.closed)
	}
}

func TestWorkerAttemptControllerRejectsClosedDeliveryBeforeProgressCapability(t *testing.T) {
	fixture := newWorkerDeliveryFixture(t, PlacementRegular)
	delivery := &activeWorkerDelivery{invocation: fixture.invocation, closed: true}
	err := (workerAttemptController{delivery: delivery}).Pulse(t.Context())
	if !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("pulse = %v", err)
	}
}

func TestWorkersDrainBeforeRunSealsLifecycle(t *testing.T) {
	spec, consumer, _, _ := workersConfigFixture(t, "workers.run.sealed")
	workers, err := NewWorkers(spec, consumer)
	if err != nil {
		t.Fatal(err)
	}
	if err = workers.Drain(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err = workers.Drain(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err = workers.Run(context.Background()); !errors.Is(err, ErrConflict) {
		t.Fatalf("run after drain = %v", err)
	}
}
