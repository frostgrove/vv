package projection_test

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/frostgrove/vv/event"
	"github.com/frostgrove/vv/event/eventmemory"
	"github.com/frostgrove/vv/event/projection"
)

// Every call a split made of the checkpoint store, in the order it made them, so
// what reached the store is read as a sequence rather than counted: the tracker
// door loads before it saves and presents an advance the row answered for, and
// an implementation reaching past it would write rows nobody read.
type statements struct {
	event.Checkpoints

	mutex sync.Mutex
	made  []string
	saved []event.Checkpoint
}

func recordingStatements(held event.Checkpoints) *statements {
	return &statements{Checkpoints: held}
}

func (this *statements) Load(ctx context.Context, projection string) (event.Checkpoint, error) {
	this.record("load " + projection)
	return this.Checkpoints.Load(ctx, projection)
}

func (this *statements) Save(ctx context.Context, checkpoint event.Checkpoint) error {
	this.record("save " + checkpoint.Projection + "@" + strconv.FormatUint(checkpoint.Advance, 10))
	err := this.Checkpoints.Save(ctx, checkpoint)
	if err == nil {
		this.mutex.Lock()
		this.saved = append(this.saved, checkpoint)
		this.mutex.Unlock()
	}
	return err
}

func (this *statements) Forget(ctx context.Context, projection string) error {
	this.record("forget " + projection)
	return this.Checkpoints.Forget(ctx, projection)
}

func (this *statements) record(what string) {
	this.mutex.Lock()
	defer this.mutex.Unlock()
	this.made = append(this.made, what)
}

func (this *statements) calls() []string {
	this.mutex.Lock()
	defer this.mutex.Unlock()
	return append([]string(nil), this.made...)
}

func (this *statements) written() []event.Checkpoint {
	this.mutex.Lock()
	defer this.mutex.Unlock()
	return append([]event.Checkpoint(nil), this.saved...)
}

// A save and a forget are the writes; a load is not one, and the three a split
// makes are counted apart from the reads that decided them.
func (this *statements) writes() int {
	count := 0
	for _, made := range this.calls() {
		if strings.HasPrefix(made, "save ") || strings.HasPrefix(made, "forget ") {
			count++
		}
	}
	return count
}

func (this *statements) loaded(name string) bool {
	for _, made := range this.calls() {
		if made == "load "+name {
			return true
		}
	}
	return false
}

// A parent taken to the end of the log by a real runner, with one payload
// parked, so the row a split hands down carries a cursor a reader resumes from
// and two counters that are not both zero.
func drained(t *testing.T, held *stand, identity projection.Identity, poison string) event.Checkpoint {
	t.Helper()
	spec := held.spec(identity.Projection(), projection.HandlerFunc(func(_ context.Context, batch projection.Batch) error {
		for _, envelope := range batch.Envelopes {
			if string(envelope.Payload) == poison {
				return fmt.Errorf("%w: the fixture refuses the payload %q", event.ErrPayload, poison)
			}
		}
		held.model.write(payloadsOf(batch.Envelopes)...)
		return nil
	}))
	spec.Partition, spec.Generation = identity.Partition(), identity.Generation()
	cancel, returned := running(t, newProjection(t, held.parking(spec, newPark())))
	held.observer.await(t, "the parent "+identity.String()+" drained", func(state projection.State) bool {
		return state.Phase == projection.PhaseDegraded && state.Identity.Partition() == identity.Partition()
	})
	cancel()
	_ = returns(t, returned)

	row := held.row(t, identity.String())
	if row.Fresh() || row.Progress.Applied == 0 || row.Progress.Quarantined == 0 {
		t.Fatalf("the parent %q holds %+v, and what a split hands down is a cursor and two counters that are not both zero", identity, row)
	}
	return row
}

func splitOf(t *testing.T, held *stand, checkpoints event.Checkpoints, parent projection.Identity) (projection.Identity, projection.Identity, error) {
	t.Helper()
	return projection.Split(context.Background(), projection.SplitSpec{
		Checkpoints: checkpoints,
		Identity:    parent,
		Unit:        held.unit,
	})
}

func twelveStreams(t *testing.T, held *stand) {
	t.Helper()
	for index := range 12 {
		held.append(t, "k"+strconv.Itoa(index), "one", "two", "three")
	}
}

// The handoff end to end over a drained partition: four writes inside one
// transaction of the caller's, both children at the parent's cursor byte for
// byte and at its watermark, the counters divided so their sum across the set is
// what it was, and every statement issued through the tracker door.
func TestASplitWritesTwoChildrenAtTheParentsCursorInOneTransaction(t *testing.T) {
	stand := newStand(t, eventmemory.Spec{})
	twelveStreams(t, stand)
	parent := identityOf(t, "orders", projection.Ungenerated, partitionOf(t, 1, 1))
	before := drained(t, stand, parent, "two")

	held := recordingStatements(stand.checkpoints)
	var staged []event.Checkpoint
	lower, higher, err := projection.Split(context.Background(), projection.SplitSpec{
		Checkpoints: held,
		Identity:    parent,
		Unit: func(ctx context.Context, work func(context.Context) error) error {
			tx, err := stand.checkpoints.Begin(ctx)
			if err != nil {
				return err
			}
			if err := work(eventmemory.WithTransaction(ctx, tx)); err != nil {
				_ = tx.Rollback(ctx)
				return err
			}
			staged = rowsOf(t, stand, parent.String(), "orders#1.3", "orders#3.3")
			return tx.Commit(ctx)
		},
	})
	if err != nil {
		t.Fatalf("splitting the drained parent %q answered %v", parent, err)
	}
	if lower.String() != "orders#1.3" || higher.String() != "orders#3.3" {
		t.Fatalf("splitting %q answered %q and %q, and a split divides the parent's own half of the space", parent, lower, higher)
	}

	if staged[0].Advance != before.Advance || !staged[1].Fresh() || !staged[2].Fresh() {
		t.Fatalf("read from outside the unit before it committed, the three rows were %+v — a split that is visible before its own transaction commits is four writes an operator can be interrupted between", staged)
	}
	if writes := held.writes(); writes != 4 {
		t.Fatalf("the split issued %d writes where a handoff is two child rows, the record of the retirement and the retirement: %v", writes, held.calls())
	}
	if !held.loaded(parent.String()) || !held.loaded(lower.String()) || !held.loaded(higher.String()) {
		t.Fatalf("the split issued %v, and it reads the parent AND both children before it writes either", held.calls())
	}
	for _, written := range held.written() {
		if written.Advance != 1 {
			t.Fatalf("a child row was presented at advance %d, and a split creates rows nothing else has: %v", written.Advance, held.calls())
		}
		if written.Cursor != before.Cursor {
			t.Fatalf("the child %q was written at the cursor %q where the parent stood at %q", written.Projection, written.Cursor, before.Cursor)
		}
	}
	if last := held.calls()[len(held.calls())-1]; last != "forget "+parent.String() {
		t.Fatalf("the last statement of the split was %q, and the parent is retired after its children are written: %v", last, held.calls())
	}

	if row := stand.row(t, parent.String()); !row.Fresh() {
		t.Fatalf("the parent %q still stands at advance %d beside its children, so one share of the log has two writers", parent, row.Advance)
	}
	for _, child := range []struct {
		identity    projection.Identity
		applied     uint64
		quarantined uint64
	}{
		{lower, before.Progress.Applied, before.Progress.Quarantined},
		{higher, 0, 0},
	} {
		row := stand.row(t, child.identity.String())
		switch {
		case row.Advance != 1:
			t.Fatalf("the child %q stands at advance %d, where a split creates its row", child.identity, row.Advance)
		case row.Cursor != before.Cursor:
			t.Fatalf("the child %q resumes from %q where the parent stood at %q, so it re-reads or skips the difference", child.identity, row.Cursor, before.Cursor)
		case row.Progress.Highest != before.Progress.Highest:
			t.Fatalf("the child %q reports the watermark %d where the parent reported %d — a barrier derived from the set is the lowest of them, and one at zero is reached by anything", child.identity, row.Progress.Highest, before.Progress.Highest)
		case row.Progress.Applied != child.applied || row.Progress.Quarantined != child.quarantined:
			t.Fatalf("the child %q reports %d applied and %d quarantined where it takes %d and %d, so the sum across the set is not the parent's", child.identity, row.Progress.Applied, row.Progress.Quarantined, child.applied, child.quarantined)
		}
	}
	sum := stand.row(t, lower.String()).Progress.Applied + stand.row(t, higher.String()).Progress.Applied
	if sum != before.Progress.Applied {
		t.Fatalf("the two children account for %d applied envelopes where the parent accounted for %d", sum, before.Progress.Applied)
	}

	t.Run("the control: the kernel's door admits the same rows at a watermark of zero", func(t *testing.T) {
		beside := newStand(t, eventmemory.Spec{})
		twelveStreams(t, beside)
		parent := identityOf(t, "orders", projection.Ungenerated, partitionOf(t, 1, 1))
		row := drained(t, beside, parent, "two")

		tracker, err := event.Track(beside.checkpoints, "orders#1.3")
		if err != nil {
			t.Fatalf("tracking the child answered %v", err)
		}
		if _, err := tracker.Load(context.Background()); err != nil {
			t.Fatalf("loading the child answered %v", err)
		}
		if _, err := tracker.Save(context.Background(), row.Cursor, event.Progress{Applied: row.Progress.Applied}); err != nil {
			t.Fatalf("writing a child row at a watermark of zero answered %v, and the door deliberately does not make Progress total", err)
		}
		if written := beside.row(t, "orders#1.3"); written.Progress.Highest != 0 {
			t.Fatalf("the row written at a watermark of zero holds %d, so the kernel filled it in and the assertion above is not this test's", written.Progress.Highest)
		}
	})
}

// Both absences, and the framework cannot tell them apart from the rows: a
// partition that never ran and one whose row was lost read alike, and the
// safe-looking guess is two children at the origin against a live read model.
func TestASplitOfAParentWithNoRowIsRefusedForBothAbsences(t *testing.T) {
	for _, absence := range []struct {
		what string
		ran  bool
	}{
		{"a partition that never ran", false},
		{"a partition whose row was forgotten under it", true},
	} {
		t.Run(absence.what, func(t *testing.T) {
			stand := newStand(t, eventmemory.Spec{})
			twelveStreams(t, stand)
			parent := identityOf(t, "orders", projection.Ungenerated, partitionOf(t, 1, 1))
			if absence.ran {
				drained(t, stand, parent, "two")
				forget(t, stand, parent.String())
			}

			held := recordingStatements(stand.checkpoints)
			lower, higher, err := splitOf(t, stand, held, parent)
			if err == nil {
				t.Fatalf("splitting %q with no row answered %q and %q, and neither has a cursor to start from", parent, lower, higher)
			}
			if !errors.Is(err, projection.ErrTopology) {
				t.Fatalf("splitting a parent with no row was refused as %v, which is not the class a topology change carries", err)
			}
			if lower != (projection.Identity{}) || higher != (projection.Identity{}) {
				t.Fatalf("the refused split answered %q and %q, and a caller that took them would declare a Cover over rows nobody wrote", lower, higher)
			}
			for _, names := range []string{parent.String(), "never ran", "Cover", "restore"} {
				if !strings.Contains(err.Error(), names) {
					t.Fatalf("the refusal reads %q and does not name %q, where both readings and both remedies are what an operator has to choose between", err, names)
				}
			}
			if writes := held.writes(); writes != 0 {
				t.Fatalf("the refused split issued %d writes: %v", writes, held.calls())
			}
			for _, name := range []string{parent.String(), "orders#1.3", "orders#3.3"} {
				if row := stand.row(t, name); !row.Fresh() {
					t.Fatalf("the refused split left %q at advance %d", name, row.Advance)
				}
			}
		})
	}

	t.Run("the refusal names a child row it found, which is the attempt that committed after all", func(t *testing.T) {
		stand := newStand(t, eventmemory.Spec{})
		twelveStreams(t, stand)
		parent := identityOf(t, "orders", projection.Ungenerated, partitionOf(t, 1, 1))
		row := drained(t, stand, parent, "two")
		handedDown(t, stand, "orders#1.3", row)
		forget(t, stand, parent.String())

		_, _, err := splitOf(t, stand, stand.checkpoints, parent)
		if !errors.Is(err, projection.ErrTopology) {
			t.Fatalf("splitting a retired parent beside a live child was answered %v", err)
		}
		if !strings.Contains(err.Error(), "orders#1.3") {
			t.Fatalf("the refusal reads %q and never names the child row it found, which is the reading that says the first attempt committed", err)
		}
	})

	t.Run("the control: a split of a partition that has run writes exactly four statements", func(t *testing.T) {
		stand := newStand(t, eventmemory.Spec{})
		twelveStreams(t, stand)
		parent := identityOf(t, "orders", projection.Ungenerated, partitionOf(t, 1, 1))
		drained(t, stand, parent, "two")

		held := recordingStatements(stand.checkpoints)
		lower, higher, err := splitOf(t, stand, held, parent)
		if err != nil {
			t.Fatalf("splitting a parent that has run answered %v, so the refusals above refuse every split", err)
		}
		if lower.String() != "orders#1.3" || higher.String() != "orders#3.3" {
			t.Fatalf("the split answered %q and %q", lower, higher)
		}
		if writes := held.writes(); writes != 4 {
			t.Fatalf("the split issued %d writes: %v", writes, held.calls())
		}
	})
}

// The ambiguous commit: a child row beside a live parent is a finer topology
// already recording, and writing over it is one share of the log with two
// writers, two fences and two watermarks.
func TestASplitOverAnExistingChildRowIsRefused(t *testing.T) {
	for _, standing := range []string{"orders#1.3", "orders#3.3"} {
		t.Run("a row at "+standing, func(t *testing.T) {
			stand := newStand(t, eventmemory.Spec{})
			twelveStreams(t, stand)
			parent := identityOf(t, "orders", projection.Ungenerated, partitionOf(t, 1, 1))
			row := drained(t, stand, parent, "two")
			handedDown(t, stand, standing, row)

			held := recordingStatements(stand.checkpoints)
			lower, higher, err := splitOf(t, stand, held, parent)
			if err == nil {
				t.Fatalf("splitting %q over a live %q answered %q and %q", parent, standing, lower, higher)
			}
			if !errors.Is(err, projection.ErrTopology) {
				t.Fatalf("a split over a live child row was refused as %v", err)
			}
			for _, names := range []string{parent.String(), standing} {
				if !strings.Contains(err.Error(), names) {
					t.Fatalf("the refusal reads %q and does not name %q", err, names)
				}
			}
			if writes := held.writes(); writes != 0 {
				t.Fatalf("the refused split issued %d writes: %v", writes, held.calls())
			}
			if after := stand.row(t, parent.String()); after.Advance != row.Advance || after.Cursor != row.Cursor {
				t.Fatalf("the refused split left the parent at %+v where it stood at %+v", after, row)
			}
			if beside := stand.row(t, sibling(standing)); !beside.Fresh() {
				t.Fatalf("the refused split wrote the other child at advance %d", beside.Advance)
			}
		})
	}

	t.Run("the control: the same split with neither child row is admitted", func(t *testing.T) {
		stand := newStand(t, eventmemory.Spec{})
		twelveStreams(t, stand)
		parent := identityOf(t, "orders", projection.Ungenerated, partitionOf(t, 1, 1))
		drained(t, stand, parent, "two")

		lower, higher, err := splitOf(t, stand, stand.checkpoints, parent)
		if err != nil {
			t.Fatalf("splitting a parent whose children hold no rows answered %v, so the refusals above refuse every split", err)
		}
		for _, child := range []projection.Identity{lower, higher} {
			if row := stand.row(t, child.String()); row.Advance != 1 {
				t.Fatalf("the child %q stands at advance %d", child, row.Advance)
			}
		}
	})
}

func sibling(child string) string {
	if child == "orders#1.3" {
		return "orders#3.3"
	}
	return "orders#1.3"
}

// A caller's unit may run the work more than once, and a split carries no
// decision between the runs: each reads the rows as they stand and does what
// they say. So a discarded run and a committed one are one split, and a run
// after a committed one finds the parent gone and is refused rather than
// writing two children at the origin.
func TestASplitUnderATwiceRunUnitIsOneSplitOrARefusal(t *testing.T) {
	stand := newStand(t, eventmemory.Spec{})
	twelveStreams(t, stand)
	parent := identityOf(t, "orders", projection.Ungenerated, partitionOf(t, 1, 1))
	before := drained(t, stand, parent, "two")

	held := recordingStatements(stand.checkpoints)
	lower, higher, err := projection.Split(context.Background(), projection.SplitSpec{
		Checkpoints: held,
		Identity:    parent,
		Unit: func(ctx context.Context, work func(context.Context) error) error {
			discarded, err := stand.checkpoints.Begin(ctx)
			if err != nil {
				return err
			}
			if err := work(eventmemory.WithTransaction(ctx, discarded)); err != nil {
				_ = discarded.Rollback(ctx)
				return err
			}
			if err := discarded.Rollback(ctx); err != nil {
				return err
			}
			kept, err := stand.checkpoints.Begin(ctx)
			if err != nil {
				return err
			}
			if err := work(eventmemory.WithTransaction(ctx, kept)); err != nil {
				_ = kept.Rollback(ctx)
				return err
			}
			return kept.Commit(ctx)
		},
	})
	if err != nil {
		t.Fatalf("a split under a unit that ran its body twice answered %v", err)
	}
	if writes := held.writes(); writes != 8 {
		t.Fatalf("the two runs issued %d writes, and a run that carried a decision from the first would have issued fewer: %v", writes, held.calls())
	}
	if row := stand.row(t, parent.String()); !row.Fresh() {
		t.Fatalf("the parent %q stands at advance %d after a split that committed", parent, row.Advance)
	}
	for _, child := range []projection.Identity{lower, higher} {
		row := stand.row(t, child.String())
		if row.Advance != 1 || row.Cursor != before.Cursor {
			t.Fatalf("the child %q stands at advance %d and the cursor %q, where one split leaves one row at the parent's cursor %q", child, row.Advance, row.Cursor, before.Cursor)
		}
	}

	t.Run("a run after a committed one is refused and names the children it found", func(t *testing.T) {
		again := recordingStatements(stand.checkpoints)
		_, _, err := splitOf(t, stand, again, parent)
		if !errors.Is(err, projection.ErrTopology) {
			t.Fatalf("a second split of %q was answered %v, where the parent it reads is gone", parent, err)
		}
		for _, names := range []string{lower.String(), higher.String()} {
			if !strings.Contains(err.Error(), names) {
				t.Fatalf("the refusal reads %q and does not name %q, which is what says the first attempt committed after all", err, names)
			}
		}
		if writes := again.writes(); writes != 0 {
			t.Fatalf("the refused second split issued %d writes: %v", writes, again.calls())
		}
	})

	t.Run("a unit that never runs the work is not a split that happened", func(t *testing.T) {
		beside := newStand(t, eventmemory.Spec{})
		twelveStreams(t, beside)
		parent := identityOf(t, "orders", projection.Ungenerated, partitionOf(t, 1, 1))
		drained(t, beside, parent, "two")

		lower, higher, err := projection.Split(context.Background(), projection.SplitSpec{
			Checkpoints: beside.checkpoints,
			Identity:    parent,
			Unit:        func(context.Context, func(context.Context) error) error { return nil },
		})
		if err == nil {
			t.Fatalf("a unit that ran nothing answered the children %q and %q, and a caller that took them would start two runners over rows nobody wrote", lower, higher)
		}
		if !errors.Is(err, projection.ErrTopology) || !strings.Contains(err.Error(), "answered without running the work") {
			t.Fatalf("a unit that ran nothing was answered %v", err)
		}
		if row := beside.row(t, parent.String()); row.Fresh() {
			t.Fatalf("the parent %q was retired by a unit that ran nothing", parent)
		}
	})

	t.Run("a unit that answers nil for a body that refused does not turn the refusal into a split", func(t *testing.T) {
		beside := newStand(t, eventmemory.Spec{})
		twelveStreams(t, beside)
		parent := identityOf(t, "orders", projection.Ungenerated, partitionOf(t, 1, 1))

		lower, higher, err := projection.Split(context.Background(), projection.SplitSpec{
			Checkpoints: beside.checkpoints,
			Identity:    parent,
			Unit: func(ctx context.Context, work func(context.Context) error) error {
				tx, opened := beside.checkpoints.Begin(ctx)
				if opened != nil {
					return opened
				}
				_ = work(eventmemory.WithTransaction(ctx, tx))
				return tx.Rollback(ctx)
			},
		})
		if err == nil {
			t.Fatalf("a unit that swallowed the refusal answered %q and %q, and what the caller's unit returns is not what the body reached", lower, higher)
		}
		if !errors.Is(err, projection.ErrTopology) {
			t.Fatalf("the refusal a unit swallowed came back as %v", err)
		}
	})

	t.Run("a unit that binds no transaction is refused before anything is read", func(t *testing.T) {
		beside := newStand(t, eventmemory.Spec{})
		twelveStreams(t, beside)
		parent := identityOf(t, "orders", projection.Ungenerated, partitionOf(t, 1, 1))
		before := drained(t, beside, parent, "two")

		held := recordingStatements(beside.checkpoints)
		_, _, err := projection.Split(context.Background(), projection.SplitSpec{
			Checkpoints: held,
			Identity:    parent,
			Unit:        func(ctx context.Context, work func(context.Context) error) error { return work(ctx) },
		})
		if !errors.Is(err, projection.ErrSpec) {
			t.Fatalf("a split under a unit that opened nothing was answered %v, where four writes outside one transaction are a handoff a crash leaves half made", err)
		}
		if writes := held.writes(); writes != 0 {
			t.Fatalf("the refused split issued %d writes: %v", writes, held.calls())
		}
		if row := beside.row(t, parent.String()); row.Advance != before.Advance {
			t.Fatalf("the parent %q stands at advance %d where it stood at %d", parent, row.Advance, before.Advance)
		}
	})

	t.Run("the doors Split refuses before it opens anything", func(t *testing.T) {
		beside := newStand(t, eventmemory.Spec{})
		parent := identityOf(t, "orders", projection.Ungenerated, partitionOf(t, 1, 1))
		opened := 0
		unit := func(ctx context.Context, work func(context.Context) error) error {
			opened++
			return beside.unit(ctx, work)
		}
		for _, refused := range []struct {
			what string
			spec projection.SplitSpec
		}{
			{"a spec naming no checkpoint store", projection.SplitSpec{Identity: parent, Unit: unit}},
			{"a spec naming no unit of work", projection.SplitSpec{Checkpoints: beside.checkpoints, Identity: parent}},
			{"the zero Identity", projection.SplitSpec{Checkpoints: beside.checkpoints, Unit: unit}},
		} {
			lower, higher, err := projection.Split(context.Background(), refused.spec)
			if err == nil {
				t.Fatalf("%s answered %q and %q", refused.what, lower, higher)
			}
			if !errors.Is(err, projection.ErrSpec) {
				t.Fatalf("%s was refused as %v, and a spec assembled wrong is ErrSpec", refused.what, err)
			}
		}
		if opened != 0 {
			t.Fatalf("a spec Split refuses opened %d units of work, and none of these three refusals needs a transaction to be reached", opened)
		}

		if _, _, err := projection.Split(context.Background(), projection.SplitSpec{
			Checkpoints: beside.checkpoints,
			Identity:    identityOf(t, "orders", projection.Ungenerated, partitionOf(t, 0, projection.MaxPartitions-1)),
			Unit:        unit,
		}); !errors.Is(err, projection.ErrTopology) || opened != 0 {
			t.Fatalf("a split at the ceiling was answered %v after opening %d units of work", err, opened)
		}
	})

	t.Run("the control: one run alone writes exactly four statements", func(t *testing.T) {
		beside := newStand(t, eventmemory.Spec{})
		twelveStreams(t, beside)
		parent := identityOf(t, "orders", projection.Ungenerated, partitionOf(t, 1, 1))
		drained(t, beside, parent, "two")

		once := recordingStatements(beside.checkpoints)
		if _, _, err := splitOf(t, beside, once, parent); err != nil {
			t.Fatalf("splitting under a unit that runs the work once answered %v", err)
		}
		if writes := once.writes(); writes != 4 {
			t.Fatalf("one run issued %d writes where a handoff is four, so the eight above are not the second run's: %v", writes, once.calls())
		}
	})
}

// The rollback of a bad deploy: release n+1 splits, release n is put back. The
// spec that comes back is the one that was in production a release ago, and
// nothing about it is hand-declared — so the refusal cannot rest on the operator
// having done something documented against. Without the record a split leaves,
// that release finds no row of its own and none for any ancestor, resumes from
// the origin, and walks the whole log into the read model the live children are
// filling, with every row reporting a healthy watermark while it happens.
func TestTheReleaseThatRanBeforeASplitIsRefusedWhenItIsRedeployed(t *testing.T) {
	for _, level := range []struct {
		what   string
		parent projection.Identity
	}{
		{"the unpartitioned release", identityOfWhole(t)},
		{"a partition that was split again", identityOf(t, "orders", projection.Ungenerated, partitionOf(t, 1, 1))},
	} {
		t.Run(level.what, func(t *testing.T) {
			stand := newStand(t, eventmemory.Spec{})
			twelveStreams(t, stand)
			seen := newApplications()
			drainedInto(t, stand, level.parent, seen)
			applied, _, untouched := seen.counted(36)

			lower, higher, err := splitOf(t, stand, stand.checkpoints, level.parent)
			if err != nil {
				t.Fatalf("splitting the drained %q answered %v", level.parent, err)
			}
			for _, child := range []projection.Identity{lower, higher} {
				running(t, newProjection(t, partitioned(stand, child, seen)))
				stand.observer.await(t, "the child "+child.String()+" followed", func(state projection.State) bool {
					return state.Phase == projection.PhaseFollowing && state.Identity == child
				})
			}

			watching := newObserved()
			back := partitioned(stand, level.parent, seen)
			back.Observer = watching
			running(t, newProjection(t, back))
			halted := watching.await(t, "the release that ran before the split settled", func(state projection.State) bool {
				return state.Phase == projection.PhaseFollowing || state.Phase == projection.PhaseHalted
			})
			if halted.Phase != projection.PhaseHalted {
				once, twice, _ := seen.counted(36)
				t.Fatalf("redeploying %q was admitted beside its live children and reached %v: %d envelopes are held once and %d more than once", level.parent, halted.Phase, once, twice)
			}
			if !errors.Is(halted.Err, projection.ErrHalted) || !errors.Is(halted.Err, projection.ErrTopology) {
				t.Fatalf("redeploying %q was refused as %v, where a topology started over a live one is a halt over a topology refusal", level.parent, halted.Err)
			}
			for _, names := range []string{level.parent.String(), level.parent.String() + "#split", "new generation", "forgetting"} {
				if !strings.Contains(halted.Err.Error(), names) {
					t.Fatalf("the refusal reads %q and does not name %q, which is the record it found and the two ways out of it", halted.Err, names)
				}
			}
			if once, twice, never := seen.counted(36); once != applied || twice != 0 || never != untouched {
				t.Fatalf("%d envelopes are held once, %d more than once and %d never, where %d and %d were the two before the redeploy and a refused runner reaches no handler", once, twice, never, applied, untouched)
			}
			if row := stand.row(t, level.parent.String()); !row.Fresh() {
				t.Fatalf("the refused release left %q at advance %d, and a runner refused at its resume records nothing", level.parent, row.Advance)
			}
		})
	}

	t.Run("the control: the same release on a projection nothing ever split", func(t *testing.T) {
		beside := newStand(t, eventmemory.Spec{})
		twelveStreams(t, beside)
		seen := newApplications()
		drainedInto(t, beside, identityOfWhole(t), seen)
		if once, _, never := seen.counted(36); once != 36 || never != 0 {
			t.Fatalf("%d of 36 envelopes were applied and %d never, so the refusal above refuses every release", once, never)
		}
	})
}

// A split leaves a record and a second split reads it, so a caller's unit that
// ran the body once and committed cannot be told from one that ran it twice by
// anything but the rows — and the second run is refused rather than writing two
// children over a topology that is already recording.
func TestASplitOfAnAlreadyRetiredParentIsRefused(t *testing.T) {
	stand := newStand(t, eventmemory.Spec{})
	twelveStreams(t, stand)
	parent := identityOf(t, "orders", projection.Ungenerated, partitionOf(t, 1, 1))
	drained(t, stand, parent, "two")
	lower, higher, err := splitOf(t, stand, stand.checkpoints, parent)
	if err != nil {
		t.Fatalf("splitting the drained %q answered %v", parent, err)
	}

	held := recordingStatements(stand.checkpoints)
	_, _, again := splitOf(t, stand, held, parent)
	if !errors.Is(again, projection.ErrTopology) {
		t.Fatalf("splitting %q a second time was answered %v", parent, again)
	}
	for _, names := range []string{"already retired", lower.String(), higher.String(), "new generation"} {
		if !strings.Contains(again.Error(), names) {
			t.Fatalf("the refusal reads %q and does not name %q", again, names)
		}
	}
	if writes := held.writes(); writes != 0 {
		t.Fatalf("the refused split issued %d writes: %v", writes, held.calls())
	}

	t.Run("a split over a child that was itself split away is refused too", func(t *testing.T) {
		if _, _, err := splitOf(t, stand, stand.checkpoints, lower); err != nil {
			t.Fatalf("splitting the child %q answered %v", lower, err)
		}
		// The parent's row is restored by hand, which is the one way back to a
		// state where a second split of it would be admitted by the rows alone.
		handedDown(t, stand, parent.String(), stand.row(t, higher.String()))
		forget(t, stand, parent.String()+"#split")

		beside := recordingStatements(stand.checkpoints)
		_, _, refusal := splitOf(t, stand, beside, parent)
		if !errors.Is(refusal, projection.ErrTopology) {
			t.Fatalf("splitting %q over a child that was itself split was answered %v", parent, refusal)
		}
		if !strings.Contains(refusal.Error(), lower.String()) || !strings.Contains(refusal.Error(), "finer still") {
			t.Fatalf("the refusal reads %q and does not name the child that was split away", refusal)
		}
		if writes := beside.writes(); writes != 0 {
			t.Fatalf("the refused split issued %d writes: %v", writes, beside.calls())
		}
	})
}

// The record is a name with a mark on it and the kernel's ceiling covers the
// whole row key, so a name within six bytes of that ceiling has no room for one.
// A split of such a name is refused rather than performed unrecorded, which is
// what lets the resume above conclude that an absent record means no split.
func TestASplitOfANameWithNoRoomForItsRetirementIsRefused(t *testing.T) {
	for _, sized := range []struct {
		what   string
		bytes  int
		splits bool
	}{
		{"a name with no room for the mark", event.MaxNameBytes - len("#split") + 1, false},
		{"the control: one byte fewer, which has exactly enough", event.MaxNameBytes - len("#split"), true},
	} {
		t.Run(sized.what, func(t *testing.T) {
			stand := newStand(t, eventmemory.Spec{})
			twelveStreams(t, stand)
			name := strings.Repeat("o", sized.bytes)
			parent := identityOf(t, name, projection.Ungenerated, projection.Whole())

			spec := stand.spec(name, applies(stand.model))
			cancel, returned := running(t, newProjection(t, spec))
			stand.observer.await(t, "the long-named release drained", func(state projection.State) bool {
				return state.Phase == projection.PhaseFollowing && state.Progress.Applied == 36
			})
			cancel()
			_ = returns(t, returned)

			held := recordingStatements(stand.checkpoints)
			lower, higher, err := splitOf(t, stand, held, parent)
			if sized.splits {
				if err != nil {
					t.Fatalf("splitting a name with exactly enough room answered %v", err)
				}
				if row := stand.row(t, name+"#split"); row.Fresh() {
					t.Fatal("the split left no record of the retirement under a name that has room for one")
				}
				return
			}
			if !errors.Is(err, projection.ErrTopology) {
				t.Fatalf("splitting a name with no room for its retirement answered %v", err)
			}
			for _, names := range []string{"rename", "rebuild", strconv.Itoa(event.MaxNameBytes)} {
				if !strings.Contains(err.Error(), names) {
					t.Fatalf("the refusal reads %q and does not name %q", err, names)
				}
			}
			if lower != (projection.Identity{}) || higher != (projection.Identity{}) {
				t.Fatalf("the refused split answered %q and %q", lower, higher)
			}
			if writes := held.writes(); writes != 0 {
				t.Fatalf("the refused split issued %d writes: %v", writes, held.calls())
			}
			if row := stand.row(t, name); row.Fresh() {
				t.Fatalf("the refused split retired %q, whose retirement it could not record", name)
			}
		})
	}
}

func identityOfWhole(t *testing.T) projection.Identity {
	t.Helper()
	return identityOf(t, "orders", projection.Ungenerated, projection.Whole())
}

// A runner at this identity taken to the end of the log, recording where every
// envelope landed so a redeploy's re-application is counted rather than argued.
func drainedInto(t *testing.T, held *stand, identity projection.Identity, seen *applications) {
	t.Helper()
	cancel, returned := running(t, newProjection(t, partitioned(held, identity, seen)))
	held.observer.await(t, "the release at "+identity.String()+" drained", func(state projection.State) bool {
		return state.Phase == projection.PhaseFollowing && state.Identity == identity
	})
	cancel()
	_ = returns(t, returned)
	if row := held.row(t, identity.String()); row.Fresh() {
		t.Fatalf("the release at %q holds no row, and what a split hands down is the cursor it stood at", identity)
	}
}

func partitioned(held *stand, identity projection.Identity, seen *applications) projection.Spec {
	spec := held.spec(identity.Projection(), seen.records(identity.Partition()))
	spec.Partition, spec.Generation = identity.Partition(), identity.Generation()
	return spec
}

func rowsOf(t *testing.T, held *stand, names ...string) []event.Checkpoint {
	t.Helper()
	rows := make([]event.Checkpoint, 0, len(names))
	for _, name := range names {
		rows = append(rows, held.row(t, name))
	}
	return rows
}
