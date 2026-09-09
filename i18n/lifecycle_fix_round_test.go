package i18n

import (
	"bytes"
	"context"
	"errors"
	"math"
	"testing"
	"time"
)

type reentrantErrContext struct {
	context.Context
	controller *Controller
	done       <-chan struct{}
	calls      int
}

func (ctx *reentrantErrContext) Done() <-chan struct{} {
	return ctx.done
}

func (ctx *reentrantErrContext) Err() error {
	ctx.calls++
	if ctx.calls == 3 {
		_ = ctx.controller.Current()
	}
	return nil
}

func TestControllerNeverCallsContextMethodsWhileLocked(t *testing.T) {
	for _, operation := range []string{"activate", "rollback"} {
		t.Run(operation, func(t *testing.T) {
			a := controllerSnapshot(t, "catalog-a", "A")
			b := controllerSnapshot(t, "catalog-b", "B")
			controller, err := NewController(ControllerSpec{Initial: a})
			if err != nil {
				t.Fatal(err)
			}
			head := controller.Current()
			if operation == "rollback" {
				head, err = controller.Activate(head, b)
				if err != nil {
					t.Fatal(err)
				}
			}
			closed := make(chan struct{})
			close(closed)
			ctx := &reentrantErrContext{Context: context.Background(), controller: controller, done: closed}
			result := make(chan error, 1)
			go func() {
				if operation == "activate" {
					_, callErr := controller.ActivateContext(ctx, head, b)
					result <- callErr
					return
				}
				_, callErr := controller.RollbackContext(ctx, head, a.Reference())
				result <- callErr
			}()
			select {
			case err := <-result:
				if !errors.Is(err, context.Canceled) || ctx.calls != 3 {
					t.Fatalf("operation = %v, calls=%d", err, ctx.calls)
				}
			case <-time.After(time.Second):
				t.Fatal("context method reentered a locked controller")
			}
		})
	}
}

func TestControllerBoundsActiveAndRetainedSnapshotBytesTransactionally(t *testing.T) {
	a := controllerSnapshot(t, "catalog-a", "A")
	b := controllerSnapshot(t, "catalog-b", "B")
	candidate := controllerSnapshot(t, "catalog-c", "C")
	pairBytes := int64(snapshotCatalogBytes(a) + snapshotCatalogBytes(b))
	limited, err := NewController(ControllerSpec{Initial: a, MaxRetained: 2, MaxSnapshotBytes: pairBytes - 1})
	if err != nil {
		t.Fatal(err)
	}
	original := limited.Current()
	if _, err := limited.Activate(original, b); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("over-budget activation = %v", err)
	}
	if limited.Current().state != original.state || len(limited.Retained()) != 0 {
		t.Fatal("over-budget activation mutated controller state")
	}

	controller, err := NewController(ControllerSpec{Initial: a, MaxRetained: 2, MaxSnapshotBytes: pairBytes})
	if err != nil {
		t.Fatal(err)
	}
	bHead, err := controller.Activate(controller.Current(), b)
	if err != nil {
		t.Fatal(err)
	}
	cHead, err := controller.Activate(bHead, candidate)
	if err != nil {
		t.Fatal(err)
	}
	retained := controller.Retained()
	if cHead.Reference() != candidate.Reference() || len(retained) != 1 || retained[0] != b.Reference() {
		t.Fatalf("byte-budget eviction = current %+v retained %+v", cHead.Reference(), retained)
	}

	pinned, err := NewController(ControllerSpec{Initial: a, MaxRetained: 2, MaxSnapshotBytes: pairBytes, MaxPinLifetime: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	lease, err := pinned.PinCurrent(time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	bHead, err = pinned.Activate(pinned.Current(), b)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pinned.Activate(bHead, candidate); !errors.Is(err, ErrPinUnavailable) {
		t.Fatalf("pinned byte-budget activation = %v", err)
	}
	if pinned.Current().Reference() != b.Reference() {
		t.Fatal("pinned byte-budget refusal changed current")
	}
	lease.Release()
}

func TestControllerRejectsPartiallySaturatedPinExpiry(t *testing.T) {
	now := time.Unix(math.MaxInt64-62135596800-5, 0).UTC()
	controller, err := NewController(ControllerSpec{
		Initial: controllerSnapshot(t, "catalog-a", "A"), MaxPinLifetime: time.Minute,
		Clock: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := controller.PinCurrent(10 * time.Second); !errors.Is(err, ErrPinUnavailable) {
		t.Fatalf("partially saturated expiry = %v", err)
	}
}

func TestSomeMapsEveryTypedNilToExplicitNull(t *testing.T) {
	var pointer *int
	if value := Some(pointer); !value.Present() || !value.IsNull() {
		t.Fatalf("typed nil pointer = present %v null %v", value.Present(), value.IsNull())
	}
	var slice []string
	if value := Some(slice); !value.Present() || !value.IsNull() {
		t.Fatalf("typed nil slice = present %v null %v", value.Present(), value.IsNull())
	}
	if value := Some[any](nil); !value.Present() || !value.IsNull() {
		t.Fatalf("nil interface = present %v null %v", value.Present(), value.IsNull())
	}
}

func TestDeadlineHasDistinctOutcomeAcrossPublicOperations(t *testing.T) {
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	observations := make([]Observation, 0, 4)
	observer := func(_ context.Context, observation Observation) {
		observations = append(observations, observation)
	}
	resolver, err := NewResolver(LocalePolicy{Supported: []string{"en"}, Default: "en", Observer: observer})
	if err != nil {
		t.Fatal(err)
	}
	if result := resolver.ResolveContext(ctx, Exact(SourceExplicit, "en")); result.Outcome != OutcomeTimedOut || result.Reason != ReasonContextDeadline {
		t.Fatalf("deadline resolution = %+v", result)
	}
	if _, err := (Loader{Observer: observer}).Load(ctx, bytes.NewReader(nil)); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("deadline load = %v", err)
	}
	a := controllerSnapshot(t, "catalog-a", "A")
	b := controllerSnapshot(t, "catalog-b", "B")
	controller, err := NewController(ControllerSpec{Initial: a, Observer: observer})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := controller.ActivateContext(ctx, controller.Current(), b); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("deadline activation = %v", err)
	}
	if len(observations) != 3 {
		t.Fatalf("terminal observations = %+v", observations)
	}
	for index, observation := range observations {
		if observation.Outcome != OutcomeTimedOut || observation.Reason != ReasonContextDeadline {
			t.Fatalf("observation %d = %+v", index, observation)
		}
	}
	renderObservations := make([]Observation, 0, 1)
	spec := testCatalog(simpleMessage("deadline", "Deadline"))
	spec.Observer = func(_ context.Context, observation Observation) {
		renderObservations = append(renderObservations, observation)
	}
	snapshot := mustSnapshot(t, spec)
	view, err := snapshot.For("en")
	if err != nil {
		t.Fatal(err)
	}
	renderObservations = nil
	message, err := snapshot.Bind("app.deadline")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := view.Render(ctx, message); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("deadline render = %v", err)
	}
	if len(renderObservations) != 1 || renderObservations[0].Outcome != OutcomeTimedOut || renderObservations[0].Reason != ReasonContextDeadline {
		t.Fatalf("render observations = %+v", renderObservations)
	}
}
