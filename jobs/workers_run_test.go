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
	mu          sync.Mutex
	description BackendDescription
	record      DeliveryRecord
	lease       LeaseRef
	invocation  Invocation
	observedAt  time.Time
	claimed     bool
	finished    chan struct{}
	finishOnce  sync.Once
	kinds       []DeliveryCommandKind
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

func (driver *workersRunDriver) Apply(_ context.Context, request ApplyRequest) (ApplyResult, error) {
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
		Entropy:   bytes.NewReader(bytes.Repeat([]byte{1}, WorkerIncarnationBytes)),
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
