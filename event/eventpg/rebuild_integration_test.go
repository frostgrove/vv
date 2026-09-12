//go:build integration

package eventpg

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"sync/atomic"
	"testing"

	"github.com/frostgrove/vv/event"
	"github.com/frostgrove/vv/event/projection"
)

// One aggregate of each covered family a pass, so a rebuild has something of
// both to disagree about and the deliberately wrong rebuild has a whole family
// to miss.
func writeRebuildBatch(t *testing.T, held *projectionCase, tag string, count int) {
	t.Helper()
	for index := range count {
		held.write(t, aStream(ordersFamily, fmt.Sprintf("%s-o-%d", tag, index)), ordersFamily+".placed", fmt.Sprintf("%s-placed-%d", tag, index))
		held.write(t, aStream(paymentsFamily, fmt.Sprintf("%s-p-%d", tag, index)), paymentsFamily+".authorised", fmt.Sprintf("%s-auth-%d", tag, index))
	}
}

func rebuildRouter(facts routed, into *destination, routesPayments bool) *projection.Router {
	router := projection.NewRouter(projection.SkipForeign)
	projection.On(router, facts.placed, into.applies)
	if routesPayments {
		projection.On(router, facts.authorised, into.applies)
	}
	return router
}

func sortedRows(t *testing.T, into *destination) []string {
	t.Helper()
	held := into.rows(t)
	slices.Sort(held)
	return held
}

// UC-120. A rebuild is two projections over one log with two destinations and
// nothing between them: the framework offers no API by which the new one could
// reset, pause or read the live one's checkpoint, and this case is what says so
// out of the database rather than out of the surface.
//
// The assertion the cutover is actually made on is that the two destinations
// hold the same rows. new.Highest >= old.Highest is asserted too and is a
// different claim — a statement about consumption and not about the rows — which
// is why the deliberately wrong rebuild below reaches the same Highest and holds
// different rows.
func TestARebuiltProjectionDrainsBesideTheLiveOneAndTheRowsAgree(t *testing.T) {
	facts := declareRouted(t)
	held := newProjectionCase(t, 12, 0)
	live := held.destination(t, "orders_read_model")
	rebuilt := held.destination(t, "orders_v2_read_model")
	wrong := held.destination(t, "orders_v3_read_model")

	writeRebuildBatch(t, held, "first", 3)
	running := held.run(t, held.spec(t, "orders", rebuildRouter(facts, live, true)))
	running.following(t, "the live projection drained what the log held")

	before, found := held.row(t, "orders")
	if !found {
		t.Fatal("the live projection saved no checkpoint, so there is nothing for a rebuild to run beside")
	}

	rebuilding := held.run(t, held.spec(t, "orders-v2", rebuildRouter(facts, rebuilt, true)))
	misrouting := held.run(t, held.spec(t, "orders-v3", rebuildRouter(facts, wrong, false)))
	writeRebuildBatch(t, held, "second", 3)

	last := storedPositions(t, held.schema)
	quiescent := last[len(last)-1]
	waitFor(t, "all three projections reached the log's highest position over a quiescent log", func() bool {
		for _, name := range []string{"orders", "orders-v2", "orders-v3"} {
			row, found := held.row(t, name)
			if !found || row.highest != quiescent {
				return false
			}
		}
		return true
	})
	running.following(t, "the live projection is still following")
	rebuilding.following(t, "the rebuilt projection reached PhaseFollowing")
	misrouting.following(t, "the deliberately wrong rebuild reached PhaseFollowing without halting")

	after, _ := held.row(t, "orders")
	newer, _ := held.row(t, "orders-v2")
	if after.applied <= before.applied {
		t.Fatalf("the live projection is at %d applied where it was at %d before the rebuild started, so it stopped while the rebuild ran", after.applied, before.applied)
	}
	if after.quarantined != 0 || newer.quarantined != 0 {
		t.Fatalf("the live projection records %d quarantined and the rebuilt one %d, and a destination with holes is not one a cutover compares", after.quarantined, newer.quarantined)
	}

	// The row comparison, which is the item. Sorted, because the live projection
	// applied the log in two drains and the rebuild in one, and the cutover is
	// about what the destinations hold rather than about the order they filled up.
	if got, want := sortedRows(t, rebuilt), sortedRows(t, live); !slices.Equal(got, want) {
		t.Fatalf("the rebuilt destination holds %v where the live one holds %v, and a cutover onto it would serve a different read model", got, want)
	}
	// The consumption comparison, which is not the item and is stated as its own
	// claim: it says the rebuild has read at least as far, and nothing about the
	// rows above.
	if newer.highest < after.highest {
		t.Fatalf("the rebuilt projection is at highest %d where the live one is at %d, so it has not consumed as far as what it would replace", newer.highest, after.highest)
	}

	// The control: the deliberately wrong rebuild leaves a whole family
	// unrouted OUTSIDE the ones it covers, so nothing halts, nothing is
	// quarantined, and it reaches the same Highest as the correct one — with a
	// different read model. A cutover decided on the number alone takes it.
	misrouted, _ := held.row(t, "orders-v3")
	if misrouted.highest != newer.highest {
		t.Fatalf("the wrong rebuild is at highest %d and the right one at %d, so this control is not comparable to the case above", misrouted.highest, newer.highest)
	}
	if misrouted.quarantined != 0 {
		t.Fatalf("the wrong rebuild quarantined %d envelopes where a family it routes nothing of is skipped and counted", misrouted.quarantined)
	}
	if got, want := sortedRows(t, wrong), sortedRows(t, live); slices.Equal(got, want) {
		t.Fatalf("the deliberately wrong rebuild holds the same rows as the live projection, so the row comparison above proves nothing")
	}
}

// UC-121. The rollback is that there is nothing to roll back: the live
// projection was never touched, so flipping reads back is an application's
// decision and not a repair.
func TestARollbackRestoresNothingBecauseNothingWasTouched(t *testing.T) {
	facts := declareRouted(t)
	held := newProjectionCase(t, 12, 0)
	live := held.destination(t, "orders_read_model")
	rebuilt := held.destination(t, "orders_v2_read_model")

	writeRebuildBatch(t, held, "first", 3)
	running := held.run(t, held.spec(t, "orders", rebuildRouter(facts, live, true)))
	running.following(t, "the live projection drained what the log held")

	rebuilding := held.run(t, held.spec(t, "orders-v2", rebuildRouter(facts, rebuilt, true)))
	rebuilding.following(t, "the rebuilt projection drained the same log")
	before, found := held.row(t, "orders")
	if !found {
		t.Fatal("the live projection saved no checkpoint, so the case below has nothing to compare")
	}

	// The cutover is rolled back: the rebuilt runner is stopped, its checkpoint
	// forgotten and its destination dropped. Nothing of the framework's is
	// repaired, because nothing of the framework's was moved.
	rebuilding.stop(t)
	if err := held.checkpoints(t).Forget(context.WithoutCancel(t.Context()), "orders-v2"); err != nil {
		t.Fatalf("forgetting the retired projection's checkpoint answered %v", err)
	}
	rebuilt.drop(t)

	if _, found := held.row(t, "orders-v2"); found {
		t.Fatal("the retired projection's checkpoint row is still there after Forget")
	}
	if after, found := held.row(t, "orders"); !found || !reflect.DeepEqual(after, before) {
		t.Fatalf("the live projection's checkpoint row is %+v (found %v) where it was %+v, and a rollback that repaired it would have had to move it first", after, found, before)
	}

	// And it never stopped: the log grows and the live projection applies it,
	// which is what proves the row above is where it always was rather than
	// where a stopped projection left it.
	writeRebuildBatch(t, held, "second", 2)
	waitFor(t, "the live projection applied what the log grew by after the rollback", func() bool {
		row, found := held.row(t, "orders")
		return found && row.applied > before.applied
	})
	running.following(t, "the live projection is still following after the rollback")

	// The framework's own contribution to deciding a cutover is Highest, the
	// phase and Quarantined — three numbers about consumption. Comparing two
	// cursors is not among them and is not expressible: a Cursor answers no
	// method at all, so nothing can be asked of it about order, and Checkpoint
	// answers exactly one, which is about absence.
	if got := reflect.TypeOf(event.Cursor("")).NumMethod(); got != 0 {
		t.Fatalf("event.Cursor answers %d methods where it answers none, and a cursor that could be compared is a resume point a caller computes", got)
	}
	if got := reflect.TypeOf(event.Checkpoint{}); got.NumMethod() != 1 || got.Method(0).Name != "Fresh" {
		t.Fatalf("event.Checkpoint answers %d methods where Fresh is the only one, and the others would be about a cursor nobody may order", got.NumMethod())
	}
}

// §6.9, §UC-158. ES-04's headline outcome, and the one nothing else in this
// phase runs: `orders` and `orders@2` over one log into two destinations, both
// draining, the rows compared at the end and each generation's checkpoint row
// read separately. It is UC-120's shipped assertion under the generation
// naming — a generation is a number in the recorded name and nothing else, so
// the naming change must not alter the result.
func TestAGenerationDrainsBesideTheLiveOneAndTheRowsAgree(t *testing.T) {
	held := newProjectionCase(t, 16, 0)
	live := held.destination(t, "generation_live")
	arriving := held.destination(t, "generation_arriving")
	stopped := held.destination(t, "generation_stopped")

	const keys = 12
	writePartitioned(t, held, "generation", keys)

	// The payload alone, because what the comparison is about is the read model's
	// contents: a handler that recorded which generation wrote a row would make
	// the two disagree by construction.
	generational := func(generation projection.Generation, into *destination) projection.Spec {
		spec := inUnit(held.spec(t, "generation", into.inserts(&deliveries{}, nil)), held.source, into.source)
		spec.Generation = generation
		return spec
	}

	running := held.run(t, generational(projection.Ungenerated, live))
	running.following(t, "the live generation drained the log")
	before, found := held.row(t, "generation")
	if !found {
		t.Fatal("the live generation saved no checkpoint, so there is nothing for a generation to run beside")
	}

	rebuilding := held.run(t, generational(2, arriving))
	writePartitioned(t, held, "generation-more", keys)
	rebuilding.following(t, "the arriving generation replayed the whole log from the origin")
	waitFor(t, "both generations reached the log's end", func() bool {
		return live.count(t) == 4*keys && arriving.count(t) == 4*keys
	})
	running.following(t, "the live generation is still following")

	after, _ := held.row(t, "generation")
	newer, foundNewer := held.row(t, "generation@2")
	if !foundNewer {
		t.Fatal("the arriving generation holds no row of its own, so the two are not recording through two names")
	}
	if after.applied <= before.applied {
		t.Fatalf("the live generation is at %d applied where it was at %d, so it stopped while the generation beside it ran", after.applied, before.applied)
	}
	if newer.highest < after.highest {
		t.Fatalf("the arriving generation is at highest %d where the live one is at %d", newer.highest, after.highest)
	}

	// Each generation's row, read separately and in psql: two names, two rows,
	// and nothing either could have done to the other's.
	for _, name := range []string{"generation", "generation@2"} {
		printed, asked := psqlAnswers(t, "SELECT projection FROM "+quoteIdentifier(held.schema.Name)+".checkpoints WHERE projection = '"+name+"'")
		if asked && !slices.Equal(printed, []string{name}) {
			t.Fatalf("psql prints %v for the checkpoint row of %q", printed, name)
		}
	}

	// The row comparison, which is the item.
	if got, want := sortedRows(t, arriving), sortedRows(t, live); !slices.Equal(got, want) {
		t.Fatalf("the arriving generation holds %d rows where the live one holds %d, and a cutover onto it would serve a different read model", len(got), len(want))
	}

	// The control: a generation stopped mid-drain leaves the rows DISAGREEING, so
	// the agreement above is the drain's and not the naming's. The stop is driven
	// rather than raced — a page size of two and a kill after the third page — so
	// this control cannot pass by draining first.
	t.Run("the control: a generation stopped mid-drain leaves the rows disagreeing", func(t *testing.T) {
		slow := held.restarted(t, 2)
		into := stopped.through(slow)
		kill := newKillPoint(t)
		pages := &atomic.Int64{}
		spec := inUnit(slow.spec(t, "generation", into.inserts(&deliveries{}, func(ctx context.Context) {
			if pages.Add(1) >= 3 {
				kill.fire(ctx)
			}
		})), slow.source, into.source)
		spec.Generation = 3
		partial := slow.runOn(t, kill.ctx, kill.cancel, spec)
		waitFor(t, "the third generation was stopped after three pages", kill.struck)
		_ = partial.stopped(t)

		got := into.count(t)
		if got == 0 || got >= 4*keys {
			t.Fatalf("the stopped generation holds %d rows over a log of %d, so it is not stopped MID-drain and this control compares nothing", got, 4*keys)
		}
		if got, want := sortedRows(t, into), sortedRows(t, live); slices.Equal(got, want) {
			t.Fatal("a generation stopped mid-drain holds the same rows as the live one, so the comparison above proves nothing")
		}
	})
}
