package projection_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/event/eventmemory"
	"github.com/frostgrove/vv/event/projection"
)

type ordering struct {
	mutex sync.Mutex
	steps []string
}

func (this *ordering) record(step string) {
	this.mutex.Lock()
	defer this.mutex.Unlock()
	this.steps = append(this.steps, step)
}

func (this *ordering) taken() []string {
	this.mutex.Lock()
	defer this.mutex.Unlock()
	return append([]string(nil), this.steps...)
}

func opening(spec projection.Spec, taken *ordering) projection.Spec {
	held := spec.Unit
	spec.Unit = func(ctx context.Context, work func(context.Context) error) error {
		taken.record("unit opens")
		err := held(ctx, work)
		taken.record("unit closes")
		return err
	}
	return spec
}

// The comparison InUnit's promise rests on: the authority the checkpoint store
// answers inside the unit and the authority built from the destination's bound
// executor are one value. Where it happens is the whole of what this measures —
// inside the unit, before the handler, and again on the next pass rather than
// once for the life of the projection.
func TestTheAlignmentIsComparedInsideTheUnitBeforeTheHandler(t *testing.T) {
	stand := newStand(t, eventmemory.Spec{MaxRead: 1})
	stand.append(t, "a", "one", "two")

	taken := &ordering{}
	held := &transaction{named: "the one both resources are reached through"}
	spec := stand.spec("orders", projection.HandlerFunc(func(_ context.Context, batch projection.Batch) error {
		taken.record("applied")
		stand.model.write(payloadsOf(batch.Envelopes)...)
		return nil
	}))
	spec = opening(stand.inUnit(spec, held, held, func() { taken.record("compared") }), taken)
	running(t, newProjection(t, spec))

	stand.observer.await(t, "the whole log was applied", func(state projection.State) bool {
		return state.Progress.Applied == 2
	})
	if rows := stand.model.rows(); !same(rows, []string{"one", "two"}) {
		t.Fatalf("an aligned projection applied %v, so the comparison refuses the wiring it is supposed to admit", rows)
	}
	if steps := taken.taken(); !same(steps, []string{
		"unit opens", "compared", "applied", "unit closes",
		"unit opens", "compared", "applied", "unit closes",
	}) {
		t.Fatalf("the two passes ran %v, where each compares inside its own unit, before its own handler call, and once for every page it advances over", steps)
	}
	if row := stand.row(t, "orders"); row.Advance != 2 {
		t.Fatalf("the checkpoint is at advance %d after two pages", row.Advance)
	}

	t.Run("the control: the same two resources at AfterApply are not compared at all", func(t *testing.T) {
		stand := newStand(t, eventmemory.Spec{MaxRead: 1})
		stand.append(t, "a", "one", "two")

		spec := stand.spec("orders", applies(stand.model))
		spec.Checkpoints = alignedCheckpoints{Checkpoints: stand.points, identity: &transaction{named: "the checkpoint store"}}
		spec.Destination = readModel{pool: stand}
		running(t, newProjection(t, spec))

		stand.observer.await(t, "the whole log was applied", func(state projection.State) bool {
			return state.Progress.Applied == 2
		})
		if count := stand.observer.counted(projection.PhaseHalted); count != 0 {
			t.Fatal("a projection at AfterApply halted over an alignment it never promised, and the wiring below it is the one InUnit refuses — so the comparison is a new universal requirement rather than InUnit's")
		}
		if rows := stand.model.rows(); !same(rows, []string{"one", "two"}) {
			t.Fatalf("the AfterApply control applied %v", rows)
		}
	})
}

// Tier B: both resolve, both are transactions, and they are two. Every check that
// shipped before this one passes, and the advance would quietly be a second write
// beside the handler's. The control is the identical composition with one
// transaction for both, so the refusal is discriminating rather than universal.
func TestTwoTransactionsUnderOneUnitAreRefusedAndOneIsAccepted(t *testing.T) {
	t.Run("two transactions under one unit", func(t *testing.T) {
		stand := newStand(t, eventmemory.Spec{})
		stand.append(t, "a", "one")

		applied := &atomic.Int64{}
		spec := stand.spec("orders", projection.HandlerFunc(func(context.Context, projection.Batch) error {
			applied.Add(1)
			return nil
		}))
		spec = stand.inUnit(spec, &transaction{named: "the checkpoint store"}, &transaction{named: "the read model"}, nil)
		running(t, newProjection(t, spec))

		halted := stand.observer.await(t, "the projection halted", func(state projection.State) bool {
			return state.Phase == projection.PhaseHalted
		})
		if !errors.Is(halted.Err, projection.ErrHalted) || !errors.Is(halted.Err, projection.ErrSpec) {
			t.Fatalf("a unit that opened two transactions was refused as %v, where a wiring InUnit cannot honour is a halt over a spec refusal", halted.Err)
		}
		if reported := halted.Err.Error(); !strings.Contains(reported, "Destination") || !strings.Contains(reported, "checkpoint store") {
			t.Fatalf("the refusal reads %q, which does not name the two resources the caller has to bring together", reported)
		}
		if count := applied.Load(); count != 0 {
			t.Fatalf("the handler was called %d times, and the comparison runs before it", count)
		}
		if row := stand.row(t, "orders"); !row.Fresh() {
			t.Fatalf("the checkpoint is at advance %d after a pass that was refused", row.Advance)
		}
		if saves := stand.points.saves.Load(); saves != 0 {
			t.Fatalf("%d saves were issued for a pass whose two transactions were refused", saves)
		}
	})

	t.Run("the control: one transaction for both drains the log", func(t *testing.T) {
		stand := newStand(t, eventmemory.Spec{})
		stand.append(t, "a", "one", "two", "three")

		held := &transaction{named: "the one both resources are reached through"}
		spec := stand.inUnit(stand.spec("orders", applies(stand.model)), held, held, nil)
		running(t, newProjection(t, spec))

		stand.observer.await(t, "the whole log was applied", func(state projection.State) bool {
			return state.Progress.Applied == 3
		})
		if rows := stand.model.rows(); !same(rows, []string{"one", "two", "three"}) {
			t.Fatalf("the aligned composition applied %v, so the refusal above refuses every wiring", rows)
		}
		if count := stand.observer.counted(projection.PhaseHalted); count != 0 {
			t.Fatal("the aligned composition halted")
		}
	})

	t.Run("a destination whose transaction carries no comparable identity is refused the same way", func(t *testing.T) {
		stand := newStand(t, eventmemory.Spec{})
		stand.append(t, "a", "one")

		pool := readModel{pool: stand}
		spec := stand.spec("orders", applies(stand.model))
		spec.Advance = projection.InUnit
		spec.Checkpoints = alignedCheckpoints{Checkpoints: stand.points, identity: &transaction{named: "the checkpoint store"}}
		spec.Destination = pool
		spec.Unit = func(ctx context.Context, work func(context.Context) error) error {
			tx, err := stand.checkpoints.Begin(ctx)
			if err != nil {
				return err
			}
			defer func() { _ = tx.Rollback(ctx) }()
			inner := crud.BindExecutor(eventmemory.WithTransaction(ctx, tx), pool, readModelTx{identity: []string{"a slice is not comparable"}})
			return work(inner)
		}
		running(t, newProjection(t, spec))

		halted := stand.observer.await(t, "the projection halted", func(state projection.State) bool {
			return state.Phase == projection.PhaseHalted
		})
		if !errors.Is(halted.Err, projection.ErrSpec) {
			t.Fatalf("a destination naming no comparable transaction was refused as %v, and an alignment that cannot be shown is not one this framework asserts", halted.Err)
		}
		if rows := stand.model.rows(); len(rows) != 0 {
			t.Fatalf("the handler applied %v before the comparison refused the pass", rows)
		}
	})
}

// Tier C: a destination crud cannot resolve, said out loud. No comparison is
// attempted at all — not even the resolution — and the arm that proves it is the
// same divergent wiring tier B refuses, which drains here.
func TestUncheckedMakesNoComparisonAtAll(t *testing.T) {
	stand := newStand(t, eventmemory.Spec{})
	stand.append(t, "a", "one", "two")

	asked := &atomic.Int64{}
	spec := stand.inUnit(
		stand.spec("orders", applies(stand.model)),
		&transaction{named: "the checkpoint store"},
		&transaction{named: "the read model in another database"},
		func() { asked.Add(1) },
	)
	spec.Destination = projection.Unchecked
	running(t, newProjection(t, spec))

	stand.observer.await(t, "the whole log was applied", func(state projection.State) bool {
		return state.Progress.Applied == 2
	})
	if rows := stand.model.rows(); !same(rows, []string{"one", "two"}) {
		t.Fatalf("an Unchecked projection applied %v, where the wiring underneath it is the one tier B refuses", rows)
	}
	if count := asked.Load(); count != 0 {
		t.Fatalf("the destination was resolved %d times under Unchecked, and Unchecked is the answer that no comparison can be made", count)
	}
	if count := stand.observer.counted(projection.PhaseHalted); count != 0 {
		t.Fatal("an Unchecked projection halted over an alignment nobody claimed")
	}

	t.Run("Unchecked is not reachable by leaving Destination zero", func(t *testing.T) {
		stand := newStand(t, eventmemory.Spec{})
		spec := stand.spec("orders", applies(stand.model))
		spec.Advance = projection.InUnit
		spec.Unit = runsTheWork
		built, err := projection.New(spec)
		if built != nil || !errors.Is(err, projection.ErrSpec) {
			t.Fatalf("a spec that left Destination unstated answered %v, and the answer 'I cannot check this' is written rather than defaulted into", err)
		}
		if !strings.Contains(err.Error(), "Destination") {
			t.Fatalf("an unstated Destination was refused with %q, which does not name the field", err)
		}
	})
}
