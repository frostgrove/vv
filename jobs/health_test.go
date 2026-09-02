package jobs

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func newProbeWorkers(t *testing.T, observer WorkerObserver) *Workers {
	t.Helper()
	fixture := newWorkerDeliveryFixture(t, PlacementRegular)
	driver := &workersRunDriver{
		description: queueTestBackendDescription(1),
		observedAt:  fixture.invocation.EligibleAt(),
		finished:    make(chan struct{}),
	}
	return newObservedRuntimeWorkers(t, fixture, driver, observer, "worker.probe")
}

func TestWorkersAreNotReadyUntilTheyRunAndAreReadyWhileTheyDo(t *testing.T) {
	started := make(chan struct{})
	var startedOnce sync.Once
	workers := newProbeWorkers(t, WorkerObserverFunc(func(_ context.Context, event WorkerEvent) {
		if event.Operation() == WorkerOperationRun && event.Outcome() == WorkerOutcomeStarted {
			startedOnce.Do(func() { close(started) })
		}
	}))

	if err := workers.Check(context.Background()); !errors.Is(err, ErrNotActivated) {
		t.Fatalf("Check() error = %v, want a not-activated refusal before Run", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	runResult := make(chan error, 1)
	go func() { runResult <- workers.Run(ctx) }()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		cancel()
		t.Fatal("worker run did not start")
	}

	if err := workers.Check(context.Background()); err != nil {
		t.Fatalf("Check() error = %v, want a passing probe while running", err)
	}

	cancel()
	<-runResult
	if err := workers.Check(context.Background()); err == nil {
		t.Fatal("Check() passed after the workers stopped")
	}
}

func TestALatchedDriverFailureIsWhatTheProbeReports(t *testing.T) {
	workers := newProbeWorkers(t, nil)
	workers.fatal.fail(WorkerFailureDriverContract, ErrDriverContract)

	if err := workers.Check(context.Background()); !errors.Is(err, ErrDriverContract) {
		t.Fatalf("Check() error = %v, want the latched driver failure", err)
	}
}

func TestAProbeWithoutWorkersOrContextRefuses(t *testing.T) {
	var absent *Workers
	if err := absent.Check(context.Background()); !errors.Is(err, ErrInvalid) {
		t.Fatalf("Check() error = %v, want a refusal", err)
	}
	if err := newProbeWorkers(t, nil).Check(nil); !errors.Is(err, ErrInvalid) {
		t.Fatalf("Check(nil) error = %v, want a refusal", err)
	}
}
