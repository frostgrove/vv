package i18n

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"
)

func controllerSnapshot(t *testing.T, revision, text string) *Snapshot {
	t.Helper()
	spec := testCatalog(simpleMessage("notice", text))
	spec.Revision = revision
	return mustSnapshot(t, spec)
}

func TestControllerActivationRollbackAndOpaqueHeadPreventABA(t *testing.T) {
	a := controllerSnapshot(t, "catalog-a", "A")
	b := controllerSnapshot(t, "catalog-b", "B")
	c, err := NewController(ControllerSpec{Initial: a, MaxRetained: 3})
	if err != nil {
		t.Fatal(err)
	}
	original := c.Current()
	bHead, err := c.Activate(original, b)
	if err != nil {
		t.Fatal(err)
	}
	if bHead.Reference() != b.Reference() || original.Snapshot() != a {
		t.Fatalf("activation lost snapshot identity: current=%+v original=%p", bHead.Reference(), original.Snapshot())
	}
	aHead, err := c.Rollback(bHead, a.Reference())
	if err != nil {
		t.Fatal(err)
	}
	if aHead.Reference() != a.Reference() || aHead.state == original.state {
		t.Fatal("rollback did not create a fresh opaque activation identity")
	}
	if _, err := c.Activate(original, b); !errors.Is(err, ErrConflict) {
		t.Fatalf("old A head after A-B-A = %v, want conflict", err)
	}
	if c.Current().Reference() != a.Reference() {
		t.Fatal("stale activation changed the current snapshot")
	}
}

func TestControllerRejectsInvalidCandidatesAndRevisionAmbiguityWithoutMutation(t *testing.T) {
	a := controllerSnapshot(t, "catalog-a", "A")
	c, err := NewController(ControllerSpec{Initial: a})
	if err != nil {
		t.Fatal(err)
	}
	head := c.Current()
	if _, err := c.Activate(head, &Snapshot{}); !errors.Is(err, ErrInvalidCatalog) {
		t.Fatalf("invalid candidate = %v", err)
	}
	conflicting := controllerSnapshot(t, "catalog-a", "different")
	if _, err := c.Activate(head, conflicting); !errors.Is(err, ErrConflict) {
		t.Fatalf("same revision with different digest = %v", err)
	}
	if c.Current().state != head.state || len(c.Retained()) != 0 {
		t.Fatal("a refused activation mutated controller state")
	}
}

func TestControllerRetentionEvictsOldestUnpinnedDeterministically(t *testing.T) {
	snapshots := make([]*Snapshot, 5)
	for index := range snapshots {
		snapshots[index] = controllerSnapshot(t, fmt.Sprintf("catalog-%d", index), fmt.Sprintf("message-%d", index))
	}
	c, err := NewController(ControllerSpec{Initial: snapshots[0], MaxRetained: 2})
	if err != nil {
		t.Fatal(err)
	}
	head := c.Current()
	for _, candidate := range snapshots[1:] {
		head, err = c.Activate(head, candidate)
		if err != nil {
			t.Fatal(err)
		}
	}
	want := []SnapshotRef{snapshots[2].Reference(), snapshots[3].Reference()}
	if got := c.Retained(); !slices.Equal(got, want) {
		t.Fatalf("retained = %+v, want %+v", got, want)
	}
	if _, err := c.Rollback(head, snapshots[1].Reference()); !errors.Is(err, ErrSnapshotNotFound) {
		t.Fatalf("rollback to evicted snapshot = %v", err)
	}
}

func TestPinsBlockEvictionUntilReleaseOrExpiry(t *testing.T) {
	now := time.Date(2026, time.September, 9, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	a := controllerSnapshot(t, "catalog-a", "A")
	b := controllerSnapshot(t, "catalog-b", "B")
	candidate := controllerSnapshot(t, "catalog-c", "C")
	d := controllerSnapshot(t, "catalog-d", "D")
	c, err := NewController(ControllerSpec{Initial: a, MaxRetained: 1, MaxPins: 2, MaxPinLifetime: time.Hour, Clock: clock})
	if err != nil {
		t.Fatal(err)
	}
	lease, err := c.PinCurrent(30 * time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	bHead, err := c.Activate(c.Current(), b)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Activate(bHead, candidate); !errors.Is(err, ErrPinUnavailable) {
		t.Fatalf("activation past pinned retention = %v", err)
	}
	if c.Current().Reference() != b.Reference() {
		t.Fatal("capacity refusal changed the active snapshot")
	}
	if pinned, err := lease.Snapshot(); err != nil || pinned != a {
		t.Fatalf("lease snapshot = %p, %v", pinned, err)
	}
	lease.Release()
	cHead, err := c.Activate(bHead, candidate)
	if err != nil {
		t.Fatal(err)
	}
	expiring, err := c.PinCurrent(10 * time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	dHead, err := c.Activate(cHead, d)
	if err != nil {
		t.Fatal(err)
	}
	if dHead.Reference() != d.Reference() {
		t.Fatal("D was not activated")
	}
	now = now.Add(10 * time.Minute)
	if _, err := expiring.Snapshot(); !errors.Is(err, ErrPinUnavailable) {
		t.Fatalf("expired lease = %v", err)
	}
	if err := c.Prune(candidate.Reference()); err != nil {
		t.Fatal(err)
	}
	if len(c.Retained()) != 0 {
		t.Fatal("explicit prune retained an inactive snapshot")
	}
}

func TestControllerPinValidationCapacityAndPruneSafety(t *testing.T) {
	now := time.Unix(100, 0)
	a := controllerSnapshot(t, "catalog-a", "A")
	b := controllerSnapshot(t, "catalog-b", "B")
	c, err := NewController(ControllerSpec{Initial: a, MaxPins: 1, MaxPinLifetime: time.Minute, Clock: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.PinCurrent(0); !errors.Is(err, ErrPinUnavailable) {
		t.Fatalf("zero lifetime = %v", err)
	}
	if _, err := c.PinCurrent(time.Minute + 1); !errors.Is(err, ErrPinUnavailable) {
		t.Fatalf("excess lifetime = %v", err)
	}
	lease, err := c.PinCurrent(time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.PinCurrent(time.Second); !errors.Is(err, ErrPinUnavailable) {
		t.Fatalf("pin capacity = %v", err)
	}
	bHead, err := c.Activate(c.Current(), b)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Prune(a.Reference()); !errors.Is(err, ErrPinUnavailable) {
		t.Fatalf("prune pinned = %v", err)
	}
	if err := c.Prune(bHead.Reference()); !errors.Is(err, ErrConflict) {
		t.Fatalf("prune active = %v", err)
	}
	lease.Release()
	lease.Release()
	if _, err := lease.Snapshot(); !errors.Is(err, ErrPinUnavailable) {
		t.Fatalf("released lease = %v", err)
	}
	if err := c.Prune(a.Reference()); err != nil {
		t.Fatal(err)
	}
	if err := c.Prune(a.Reference()); !errors.Is(err, ErrSnapshotNotFound) {
		t.Fatalf("second prune = %v", err)
	}
}

func TestControllerPinsAnExplicitRetainedRevisionWithAnObservableDeadline(t *testing.T) {
	now := time.Date(2026, time.September, 9, 14, 0, 0, 0, time.UTC)
	a := controllerSnapshot(t, "catalog-a", "A")
	b := controllerSnapshot(t, "catalog-b", "B")
	controller, err := NewController(ControllerSpec{
		Initial: a, MaxRetained: 2, MaxPins: 2, MaxPinLifetime: time.Hour,
		Clock: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := controller.Activate(controller.Current(), b); err != nil {
		t.Fatal(err)
	}
	lease, err := controller.Pin(a.Reference(), 30*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if lease.Reference() != a.Reference() || !lease.ExpiresAt().Equal(now.Add(30*time.Minute)) {
		t.Fatalf("lease = %+v until %s", lease.Reference(), lease.ExpiresAt())
	}
	if snapshot, err := lease.Snapshot(); err != nil || snapshot != a {
		t.Fatalf("pinned snapshot = %p, %v", snapshot, err)
	}
	missing := SnapshotRef{Revision: "missing", Digest: fmt.Sprintf("%064x", 1)}
	if _, err := controller.Pin(missing, time.Minute); !errors.Is(err, ErrSnapshotNotFound) {
		t.Fatalf("missing explicit pin = %v", err)
	}
	lease.Release()
	var empty *Lease
	if empty.Reference().Valid() || !empty.ExpiresAt().IsZero() {
		t.Fatal("nil lease exposed an identity or deadline")
	}
}

func TestControllerRejectsForeignAndStaleHeads(t *testing.T) {
	a := controllerSnapshot(t, "catalog-a", "A")
	b := controllerSnapshot(t, "catalog-b", "B")
	first, err := NewController(ControllerSpec{Initial: a})
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewController(ControllerSpec{Initial: a})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := first.Activate(second.Current(), b); !errors.Is(err, ErrConflict) {
		t.Fatalf("foreign head = %v", err)
	}
	old := first.Current()
	current, err := first.Activate(old, b)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := first.Rollback(old, a.Reference()); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale rollback = %v", err)
	}
	if first.Current().state != current.state {
		t.Fatal("stale rollback changed the head")
	}
}

func TestControllerConcurrentCapturesPinsAndPublicationsRemainWhole(t *testing.T) {
	a := controllerSnapshot(t, "catalog-a", "A")
	b := controllerSnapshot(t, "catalog-b", "B")
	c, err := NewController(ControllerSpec{Initial: a, MaxRetained: 2, MaxPins: 4096, MaxPinLifetime: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for worker := 0; worker < 16; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for iteration := 0; iteration < 100; iteration++ {
				head := c.Current()
				if !head.Valid() || head.Snapshot().Reference() != head.Reference() {
					t.Errorf("torn head: %+v", head.Reference())
					return
				}
				lease, pinErr := c.PinCurrent(time.Second)
				if pinErr == nil {
					if snapshot, leaseErr := lease.Snapshot(); leaseErr != nil || snapshot.Reference() != lease.Reference() {
						t.Errorf("torn lease: snapshot=%v err=%v", snapshot, leaseErr)
						lease.Release()
						return
					}
					lease.Release()
				}
			}
		}()
	}
	for iteration := 0; iteration < 200; iteration++ {
		for {
			head := c.Current()
			candidate := a
			if head.Reference() == a.Reference() {
				candidate = b
			}
			if _, err := c.Activate(head, candidate); err == nil {
				break
			} else if !errors.Is(err, ErrConflict) {
				t.Fatal(err)
			}
		}
	}
	wg.Wait()
}

func TestControllerNeverInvokesTheApplicationClockWhileHoldingItsLock(t *testing.T) {
	now := time.Unix(100, 0)
	var controller *Controller
	clock := func() time.Time {
		if controller != nil {
			_ = controller.Current()
		}
		return now
	}
	var err error
	controller, err = NewController(ControllerSpec{Initial: controllerSnapshot(t, "catalog-a", "A"), Clock: clock})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		lease, pinErr := controller.PinCurrent(time.Second)
		if lease != nil {
			lease.Release()
		}
		done <- pinErr
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("controller called its clock while holding the controller lock")
	}
}

func TestControllerConstructorsAndNilValuesFailClosed(t *testing.T) {
	if _, err := NewController(ControllerSpec{}); !errors.Is(err, ErrInvalidCatalog) {
		t.Fatalf("nil initial = %v", err)
	}
	a := controllerSnapshot(t, "catalog-a", "A")
	for _, spec := range []ControllerSpec{
		{Initial: a, MaxRetained: -1},
		{Initial: a, MaxPins: -1},
		{Initial: a, MaxPinLifetime: -1},
		{Initial: a, MaxSnapshotBytes: -1},
	} {
		if _, err := NewController(spec); !errors.Is(err, ErrLimitExceeded) {
			t.Fatalf("bad limits %+v = %v", spec, err)
		}
	}
	var controller *Controller
	if controller.Current().Valid() || controller.Retained() != nil {
		t.Fatal("nil controller exposed state")
	}
	if _, err := controller.PinCurrent(time.Second); !errors.Is(err, ErrPinUnavailable) {
		t.Fatalf("nil PinCurrent = %v", err)
	}
	if err := controller.Prune(SnapshotRef{}); !errors.Is(err, ErrSnapshotNotFound) {
		t.Fatalf("nil Prune = %v", err)
	}
	var lease *Lease
	if _, err := lease.Snapshot(); !errors.Is(err, ErrPinUnavailable) {
		t.Fatalf("nil lease = %v", err)
	}
	lease.Release()
}

func TestControllerLifecycleObservationsAreTerminalContextualAndUnlocked(t *testing.T) {
	a := controllerSnapshot(t, "catalog-a", "A")
	b := controllerSnapshot(t, "catalog-b", "B")
	observations := make([]Observation, 0, 4)
	var controller *Controller
	observer := func(ctx context.Context, observation Observation) {
		if ctx == nil {
			t.Error("observer received a nil context")
		}
		observations = append(observations, observation)
		_ = controller.Current()
	}
	var err error
	controller, err = NewController(ControllerSpec{Initial: a, Observer: observer})
	if err != nil {
		t.Fatal(err)
	}
	original := controller.Current()
	current, err := controller.ActivateContext(context.Background(), original, b)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := controller.ActivateContext(context.Background(), original, a); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale activation = %v", err)
	}
	missing := SnapshotRef{Revision: "missing", Digest: fmt.Sprintf("%064x", 1)}
	if _, err := controller.RollbackContext(context.Background(), current, missing); !errors.Is(err, ErrSnapshotNotFound) {
		t.Fatalf("missing rollback = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := controller.RollbackContext(ctx, current, a.Reference()); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled rollback = %v", err)
	}
	want := []struct {
		operation Operation
		outcome   Outcome
		reason    Reason
	}{
		{OperationActivate, OutcomeSuccess, ReasonNone},
		{OperationActivate, OutcomeInvalid, ReasonConflict},
		{OperationRollback, OutcomeMissing, ReasonSnapshotMissing},
		{OperationRollback, OutcomeCanceled, ReasonContextCanceled},
	}
	if len(observations) != len(want) {
		t.Fatalf("observations = %+v", observations)
	}
	for index, expected := range want {
		got := observations[index]
		if got.Operation != expected.operation || got.Outcome != expected.outcome || got.Reason != expected.reason || got.Count != 1 || got.Duration < 0 {
			t.Fatalf("observation %d = %+v, want %+v", index, got, expected)
		}
	}
}

func TestControllerUsesSnapshotObserverAndIsolatesObserverPanics(t *testing.T) {
	a := controllerSnapshot(t, "catalog-a", "A")
	b := controllerSnapshot(t, "catalog-b", "B")
	called := 0
	a.observer = func(context.Context, Observation) {
		called++
		panic("observer")
	}
	controller, err := NewController(ControllerSpec{Initial: a})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := controller.Activate(controller.Current(), b); err != nil {
		t.Fatalf("observer panic changed activation: %v", err)
	}
	if called != 1 {
		t.Fatalf("observer calls = %d", called)
	}
}
