package jobs

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"
)

func TestSharedAdmissionObservationIsAggregateStableAndTracksRecovery(t *testing.T) {
	base := time.Now().UTC().Add(-time.Minute)
	snapshot, err := NewAdmissionSnapshot(time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err = snapshot.Publisher().Update(0, admissionTestReason(t, "dependency.down"), base); err != nil {
		t.Fatal(err)
	}
	pool, bindings, deliveries := newSnapshotAdmissionPool(t, snapshot.Reader(), snapshot.Reader())
	description := pool.workers.Describe().Plan.Bindings
	if len(description) != 2 || description[0].AdmissionGroup.IsZero() || description[0].AdmissionGroup != description[1].AdmissionGroup || pool.admissionGroups[0].identity != description[0].AdmissionGroup {
		t.Fatalf("shared admission description = %#v", description)
	}
	separate := newReadyWorkerAdmissionSnapshot(t, 1)
	other, _, _ := newSnapshotAdmissionPool(t, separate.Reader(), separate.Reader())
	if other.admissionGroups[0].identity != pool.admissionGroups[0].identity {
		t.Fatalf("admission group identity changed: %q != %q", other.admissionGroups[0].identity, pool.admissionGroups[0].identity)
	}

	type contextKey struct{}
	pool.session.controlContext = context.WithValue(context.Background(), contextKey{}, "private")
	var mu sync.Mutex
	events := make([]WorkerEvent, 0, 6)
	unlocked := true
	valueBlind := true
	pool.workers.config.observer = WorkerObserverFunc(func(ctx context.Context, event WorkerEvent) {
		if pool.mu.TryLock() {
			pool.mu.Unlock()
		} else {
			unlocked = false
		}
		if ctx.Value(contextKey{}) != nil {
			valueBlind = false
		}
		mu.Lock()
		events = append(events, event)
		mu.Unlock()
		panic("private observer failure")
	})

	if request, ok, requestErr := pool.claimRequest(); requestErr != nil || ok || len(request.Targets()) != 0 {
		t.Fatalf("held claim request = (%v, %v, %#v)", ok, requestErr, request.Targets())
	}
	if allowances, admitErr := pool.admitClaimed(deliveries); admitErr != nil || len(allowances) != 0 {
		t.Fatalf("held recovered admission = (%v, %v)", allowances, admitErr)
	}
	if err = snapshot.Publisher().Update(2, HeldReason{}, base.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if allowances, admitErr := pool.admitClaimed(deliveries); admitErr != nil || allowances[bindings[0]]+allowances[bindings[1]] != 2 {
		t.Fatalf("recovered admission = (%v, %v)", allowances, admitErr)
	}
	pool.mu.Lock()
	bindings[0].active = 1
	pool.mu.Unlock()
	if _, _, requestErr := pool.claimRequest(); requestErr != nil {
		t.Fatal(requestErr)
	}
	pool.mu.Lock()
	bindings[0].active = 2
	pool.mu.Unlock()
	if _, _, requestErr := pool.claimRequest(); requestErr != nil {
		t.Fatal(requestErr)
	}
	if err = snapshot.Publisher().Update(0, admissionTestReason(t, "dependency.down"), base.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, _, requestErr := pool.claimRequest(); requestErr != nil {
		t.Fatal(requestErr)
	}

	mu.Lock()
	observed := append([]WorkerEvent(nil), events...)
	mu.Unlock()
	if !unlocked || !valueBlind {
		t.Fatalf("admission observer boundary = unlocked %t value-blind %t", unlocked, valueBlind)
	}
	if len(observed) != 5 {
		t.Fatalf("admission observations = %#v", observed)
	}
	wantOutcomes := []WorkerOutcome{WorkerOutcomeHeld, WorkerOutcomeReady, WorkerOutcomeReady, WorkerOutcomeSaturated, WorkerOutcomeHeld}
	wantActive := []int{0, 0, 1, 2, 2}
	for index, event := range observed {
		if event.Operation() != WorkerOperationAdmission || event.Outcome() != wantOutcomes[index] || event.Active() != wantActive[index] || event.AdmissionGroup() != description[0].AdmissionGroup || !event.Definition().IsZero() || !event.Binding().IsZero() || event.Failure() != WorkerFailureNone {
			t.Fatalf("admission observation %d = %#v", index, event)
		}
	}
}

func TestSharedAdmissionSnapshotUsesActualDemandAndPrioritizesNextBinding(t *testing.T) {
	snapshot := newReadyWorkerAdmissionSnapshot(t, 1)
	pool, bindings, deliveries := newSnapshotAdmissionPool(t, snapshot.Reader(), snapshot.Reader())
	first, ok, err := pool.claimRequest()
	if err != nil || !ok {
		t.Fatalf("first claim request = (%v, %v)", ok, err)
	}
	firstTargets := first.Targets()
	if len(firstTargets) != 2 || firstTargets[0].Definition() != bindings[0].binding.declaration.declarationName() || firstTargets[0].Available() != 1 || firstTargets[1].Available() != 1 {
		t.Fatalf("first claim targets = %#v", firstTargets)
	}
	allowances, err := pool.admitClaimed(deliveries[:1])
	if err != nil || allowances[bindings[0]] != 1 || allowances[bindings[1]] != 0 {
		t.Fatalf("first admission = (%v, %v)", allowances, err)
	}
	for index := 0; index < 2; index++ {
		request, requestOK, requestErr := pool.claimRequest()
		if requestErr != nil || !requestOK {
			t.Fatalf("claim request %d = (%v, %v)", index+2, requestOK, requestErr)
		}
		targets := request.Targets()
		if len(targets) != 2 || targets[0].Definition() != bindings[1].binding.declaration.declarationName() {
			t.Fatalf("claim targets %d = %#v", index+2, targets)
		}
	}
}

func TestUnrestrictedAdmissionUsesWorkerCapacity(t *testing.T) {
	snapshot, err := NewAdmissionSnapshot(time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err = snapshot.Publisher().Unrestricted(time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	pool, bindings, deliveries := newSnapshotAdmissionPool(t, snapshot.Reader())
	request, ok, err := pool.claimRequest()
	if err != nil || !ok {
		t.Fatalf("claim request = (%v, %v)", ok, err)
	}
	targets := request.Targets()
	if len(targets) != 1 || targets[0].Available() != bindings[0].binding.concurrency {
		t.Fatalf("claim targets = %#v", targets)
	}
	allowances, err := pool.admitClaimed(deliveries)
	if err != nil || allowances[bindings[0]] != 1 {
		t.Fatalf("admission = (%v, %v)", allowances, err)
	}
}

func TestSharedUnrestrictedAdmissionUsesAggregateWorkerCapacity(t *testing.T) {
	snapshot, err := NewAdmissionSnapshot(time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err = snapshot.Publisher().Unrestricted(time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	pool, _, _ := newSnapshotAdmissionPoolWithConcurrency(t, MaxBindingConcurrency, snapshot.Reader(), snapshot.Reader())
	var observed WorkerEvent
	pool.workers.config.observer = WorkerObserverFunc(func(_ context.Context, event WorkerEvent) {
		observed = event
	})
	request, ok, err := pool.claimRequest()
	if err != nil || !ok {
		t.Fatalf("claim request = (%v, %v)", ok, err)
	}
	total := 0
	for _, target := range request.Targets() {
		total += target.Available()
	}
	if len(request.Targets()) != 2 || total != 2*MaxBindingConcurrency {
		t.Fatalf("aggregate unrestricted targets = %#v", request.Targets())
	}
	if observed.Operation() != WorkerOperationAdmission || observed.AdmissionSignal() != AdmissionUnrestricted || observed.Limit() != 2*MaxBindingConcurrency {
		t.Fatalf("aggregate unrestricted observation = %#v", observed)
	}
}

func TestSharedAdmissionSnapshotDoesNotLoseAnEmptyFirstBinding(t *testing.T) {
	snapshot := newReadyWorkerAdmissionSnapshot(t, 1)
	pool, bindings, deliveries := newSnapshotAdmissionPool(t, snapshot.Reader(), snapshot.Reader())
	allowances, err := pool.admitClaimed(deliveries[1:])
	if err != nil || allowances[bindings[0]] != 0 || allowances[bindings[1]] != 1 {
		t.Fatalf("admission after empty first binding = (%v, %v)", allowances, err)
	}
	request, ok, err := pool.claimRequest()
	if err != nil || !ok || request.Targets()[0].Definition() != bindings[0].binding.declaration.declarationName() {
		t.Fatalf("next claim request = (%v, %v, %#v)", ok, err, request.Targets())
	}
}

func TestSharedAdmissionSnapshotGrantsConstantDemandFairly(t *testing.T) {
	snapshot := newReadyWorkerAdmissionSnapshot(t, 1)
	pool, bindings, deliveries := newSnapshotAdmissionPool(t, snapshot.Reader(), snapshot.Reader())
	for index := 0; index < 6; index++ {
		allowances, err := pool.admitClaimed(deliveries)
		if err != nil {
			t.Fatal(err)
		}
		selected := index % len(bindings)
		if allowances[bindings[selected]] != 1 || allowances[bindings[(selected+1)%len(bindings)]] != 0 {
			t.Fatalf("admission %d = %v", index, allowances)
		}
	}
}

func TestAdmissionSnapshotGroupsByFrameworkOwnedIdentity(t *testing.T) {
	shared := newReadyWorkerAdmissionSnapshot(t, 1)
	separate := newReadyWorkerAdmissionSnapshot(t, 1)
	pool, bindings, _ := newSnapshotAdmissionPool(t, shared.Reader(), shared.Reader(), separate.Reader())
	if len(pool.admissionGroups) != 2 || bindings[0].admissionGroup != bindings[1].admissionGroup || bindings[0].admissionGroup == bindings[2].admissionGroup {
		t.Fatalf("admission groups = %#v", pool.admissionGroups)
	}
}

func TestAdmissionSnapshotFailsClosedForUnavailableState(t *testing.T) {
	snapshot, err := NewAdmissionSnapshot(time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	pool, _, deliveries := newSnapshotAdmissionPool(t, snapshot.Reader())
	assertWorkerAdmissionClosed(t, pool, deliveries)
	now := time.Now().UTC()
	if err = snapshot.Publisher().Update(0, admissionTestReason(t, "dependency.down"), now); err != nil {
		t.Fatal(err)
	}
	assertWorkerAdmissionClosed(t, pool, deliveries)
	if err = snapshot.Publisher().Update(-1, HeldReason{}, now.Add(time.Nanosecond)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid admission update = %v", err)
	}
	assertWorkerAdmissionClosed(t, pool, deliveries)
}

func TestRecoveredQueuedDeliveryUsesAdmissionSnapshot(t *testing.T) {
	fixture := newWorkerDeliveryFixture(t, PlacementRegular)
	snapshot, err := NewAdmissionSnapshot(time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err = snapshot.Publisher().Update(0, admissionTestReason(t, "dependency.down"), time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	handled := false
	consumer := On(fixture.definition, Handler[string](func(context.Context, string) error {
		handled = true
		return nil
	}), Binding("worker.primary"), Concurrency(1), WithAdmission(snapshot.Reader()))
	driver := &workersRunDriver{
		description: queueTestBackendDescription(1),
		record:      fixture.record,
		lease:       fixture.lease,
		invocation:  fixture.invocation,
		observedAt:  fixture.invocation.EligibleAt(),
		finished:    make(chan struct{}),
	}
	workers := newAdmissionWorkers(t, fixture, driver, consumer)
	session := newWorkerRunSession(context.Background())
	pool := newWorkerPool(workers, session)
	recovered, err := NewRecoveredDelivery(fixture.lease, fixture.record)
	if err != nil {
		t.Fatal(err)
	}
	claimed, ok, keepRunning := pool.prepareRecovered(recovered)
	if !ok || !keepRunning {
		t.Fatalf("prepared recovered delivery = (%v, %v)", ok, keepRunning)
	}
	if !pool.dispatchClaimed([]ClaimedDelivery{claimed}) {
		t.Fatal("admission release stopped the worker")
	}
	if handled {
		t.Fatal("held recovered delivery started its handler")
	}
	driver.mu.Lock()
	defer driver.mu.Unlock()
	if len(driver.reasons) != 1 || driver.reasons[0] != ReasonAdmission || driver.invocation.State() != InvocationQueued || driver.invocation.DeliveryDeferrals().Value() != 0 || driver.invocation.AttemptOrdinal().Value() != 0 {
		t.Fatalf("recovered admission release = reasons %v invocation %v", driver.reasons, driver.invocation)
	}
}

func TestDrainBeforeAdmissionDispatchCannotStartHandler(t *testing.T) {
	fixture := newWorkerDeliveryFixture(t, PlacementRegular)
	snapshot := newReadyWorkerAdmissionSnapshot(t, 1)
	handled := false
	consumer := On(fixture.definition, Handler[string](func(context.Context, string) error {
		handled = true
		return nil
	}), Binding("worker.primary"), Concurrency(1), WithAdmission(snapshot.Reader()))
	driver := &workersRunDriver{
		description: queueTestBackendDescription(1),
		record:      fixture.record,
		lease:       fixture.lease,
		invocation:  fixture.invocation,
		observedAt:  fixture.invocation.EligibleAt(),
		finished:    make(chan struct{}),
	}
	workers := newAdmissionWorkers(t, fixture, driver, consumer)
	session := newWorkerRunSession(context.Background())
	pool := newWorkerPool(workers, session)
	session.requestDrain()
	if pool.dispatchClaimed([]ClaimedDelivery{fixture.delivery}) {
		t.Fatal("dispatch continued after drain")
	}
	if handled {
		t.Fatal("handler started after drain")
	}
	driver.mu.Lock()
	defer driver.mu.Unlock()
	if len(driver.reasons) != 1 || driver.reasons[0] != ReasonShutdown || driver.invocation.State() != InvocationQueued || driver.invocation.AttemptOrdinal().Value() != 0 {
		t.Fatalf("shutdown release = reasons %v invocation %v", driver.reasons, driver.invocation)
	}
}

func TestAdmissionReleaseTerminalOutcomeRoundTripsWithoutDeferral(t *testing.T) {
	fixture := newWorkerDeliveryFixture(t, PlacementRegular)
	observedAt := fixture.invocation.MaxElapsedAt().Add(-MinRetryDelay / 2)
	command, err := ReleaseForAdmissionCommand(fixture.lease, MinRetryDelay)
	if err != nil {
		t.Fatal(err)
	}
	application, err := ApplyDeliveryCommand(fixture.invocation, command, observedAt)
	if err != nil {
		t.Fatal(err)
	}
	invocation := application.Invocation()
	if invocation.State() != InvocationDead || invocation.Outcome().Reason() != ReasonAdmission || invocation.Outcome().TerminalReason() != ReasonMaxElapsed || invocation.DeliveryDeferrals().Value() != 0 || invocation.HandlerDeferrals().Value() != 0 || invocation.AttemptOrdinal().Value() != 0 {
		t.Fatalf("terminal admission release = %v", invocation)
	}
	record, err := NewDeliveryRecord(invocation, fixture.payload, fixture.record.WireDigest, fixture.record.PayloadDigest)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := RestoreDeliveryRecord(fixture.catalog, record)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(restored.Invocation().History(), invocation.History()) || !reflect.DeepEqual(restored.Invocation().AttemptRecords(), invocation.AttemptRecords()) {
		t.Fatalf("restored invocation differs: %v", restored.Invocation())
	}
}

func assertWorkerAdmissionClosed(t *testing.T, pool *workerPool, deliveries []ClaimedDelivery) {
	t.Helper()
	request, ok, err := pool.claimRequest()
	if err != nil || ok || len(request.Targets()) != 0 {
		t.Fatalf("closed claim request = (%v, %v, %#v)", ok, err, request.Targets())
	}
	allowances, err := pool.admitClaimed(deliveries)
	if err != nil || len(allowances) != 0 {
		t.Fatalf("closed admission = (%v, %v)", allowances, err)
	}
}

func newReadyWorkerAdmissionSnapshot(t *testing.T, limit int) *AdmissionSnapshot {
	t.Helper()
	snapshot, err := NewAdmissionSnapshot(time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err = snapshot.Publisher().Update(limit, HeldReason{}, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func newSnapshotAdmissionPool(t *testing.T, readers ...AdmissionReader) (*workerPool, []*workerRuntimeBinding, []ClaimedDelivery) {
	return newSnapshotAdmissionPoolWithConcurrency(t, 2, readers...)
}

func newSnapshotAdmissionPoolWithConcurrency(t *testing.T, concurrency int, readers ...AdmissionReader) (*workerPool, []*workerRuntimeBinding, []ClaimedDelivery) {
	t.Helper()
	names := []string{"workers.snapshot-runtime-a", "workers.snapshot-runtime-b", "workers.snapshot-runtime-c", "workers.snapshot-runtime-d"}
	bindings := []string{"workers.snapshot-a", "workers.snapshot-b", "workers.snapshot-c", "workers.snapshot-d"}
	if len(readers) == 0 || len(readers) > len(names) {
		t.Fatal("invalid admission reader fixture")
	}
	definitions := make([]*Definition[string], len(readers))
	declarations := make([]Declaration, len(readers))
	consumers := make([]Consumer, len(readers))
	for index, reader := range readers {
		definition := testQueueDefinition(t, names[index], String(1))
		definitions[index] = definition
		declarations[index] = definition
		consumers[index] = On(definition, Handler[string](func(context.Context, string) error { return nil }), Binding(bindings[index]), Concurrency(concurrency), WithAdmission(reader))
	}
	driver := &workersConfigDriver{description: queueTestBackendDescription(1)}
	workers, err := NewWorkers(WorkersSpec{
		Namespace: queueTestNamespace(t, "snapshot-workers"),
		Catalog:   MustCatalog(declarations...),
		Driver:    driver,
		Build:     testBuildID(t),
		Identity:  workerDeliveryIdentityRestorer(t),
		Entropy:   bytes.NewReader(bytes.Repeat([]byte{1}, WorkerIncarnationBytes)),
	}, consumers...)
	if err != nil {
		t.Fatal(err)
	}
	session := newWorkerRunSession(context.Background())
	session.incarnation, err = newWorkerIncarnation(workers.config.entropy)
	if err != nil {
		t.Fatal(err)
	}
	pool := newWorkerPool(workers, session)
	runtimeBindings := make([]*workerRuntimeBinding, len(definitions))
	deliveries := make([]ClaimedDelivery, len(definitions))
	for index, definition := range definitions {
		runtimeBinding := pool.bindings[definition.Name()]
		target, targetErr := pool.claimTarget(runtimeBinding.binding, 1)
		if targetErr != nil {
			t.Fatal(targetErr)
		}
		runtimeBindings[index] = runtimeBinding
		deliveries[index] = ClaimedDelivery{target: target}
	}
	return pool, runtimeBindings, deliveries
}

func newAdmissionWorkers(t *testing.T, fixture workerDeliveryFixture, driver DeliveryDriver, consumer Consumer) *Workers {
	t.Helper()
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
	return workers
}
