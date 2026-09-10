package eventtest

import (
	"context"

	"github.com/frostgrove/vv/event"
)

// The two obligations a split of a partitioned projection rests on, and nothing
// else in this suite asks either of them. A split hands a parent's cursor to its
// children by writing the same bytes under another name, so those bytes have to
// survive a name that did not mint them; and it writes each child at advance 1,
// so the store's own fence is the whole of what stands between a handoff and a
// partition silently adopted from whatever was already recording.
//
// Both are mandatory rather than gated. A split has no other spelling — there is
// no second way to give two rows one starting point — and a capability flag for
// them would turn a conformance failure into something a deployment discovers at
// run time.
func topologySection(this *checkpoints) {
	ctx := this.context()
	held := this.store()
	parent, lower, higher := this.named("a"), this.named("b"), this.named("c")

	minted := event.Checkpoint{Projection: parent, Cursor: this.cursor(), Advance: 1, Progress: this.progress(97, 97, 3)}
	this.save(ctx, held, minted)
	found := this.load(ctx, held, parent)
	if found.Cursor != minted.Cursor {
		this.refuse("a cursor saved under %q was answered as %q, so there is nothing here for a split to hand down", parent, found.Cursor)
	}

	standing := map[string]event.Checkpoint{}
	for _, child := range []string{lower, higher} {
		handed := event.Checkpoint{Projection: child, Cursor: found.Cursor, Advance: 1, Progress: this.progress(97, 0, 0)}
		this.save(ctx, held, handed)
		standing[child] = handed
		if read := this.load(ctx, held, child); read.Cursor != minted.Cursor {
			this.refuse("the cursor %q was minted under %q, written under %q and answered there as %q — a split gives two children one starting point by writing one cursor under two names, and a store that binds a cursor to the name that saved it resumes both of them somewhere else in the log",
				minted.Cursor, parent, child, read.Cursor)
		}
	}

	adopted := event.Checkpoint{Projection: higher, Cursor: this.cursor(), Advance: 1, Progress: this.progress(101, 101, 0)}
	this.classified(held.Save(ctx, adopted), event.Conflict, "a save at advance 1 against a name whose row already stands at advance 1")
	this.unchangedRow(ctx, held, higher, standing[higher], "a save at advance 1 over a live row")
}

// The three writes of a handoff, in a transaction the caller opened: a store
// that leaves any one of them outside it commits half a topology change with no
// error on any path — a parent retired beside children that were never written,
// or children beside a parent that goes on recording the same keys.
//
// It is gated on the transactions claim rather than mandatory, because a store
// that correctly claims none cannot be asked to commit three statements at once
// and reporting it failed for that is the vacuity this suite holds itself to.
func topologyHandoffSection(this *checkpoints) {
	ctx := this.context()
	held := this.store()
	parent, lower, higher := this.named("a"), this.named("b"), this.named("c")

	minted := event.Checkpoint{Projection: parent, Cursor: this.cursor(), Advance: 1, Progress: this.progress(103, 103, 1)}
	this.save(ctx, held, minted)

	discarded, first := this.begin(ctx, held)
	this.handOver(discarded, held, minted, lower, higher)
	this.unchangedRow(ctx, held, parent, minted, "a handoff staged inside a unit of work nobody has committed")
	this.absentOutside(ctx, held, "a handoff staged inside a unit of work nobody has committed", lower, higher)
	this.rollback(ctx, first)
	this.unchangedRow(ctx, held, parent, minted, "a handoff inside a unit of work that rolled back")
	this.absentOutside(ctx, held, "a handoff inside a unit of work that rolled back", lower, higher)

	inside, second := this.begin(ctx, held)
	written := this.handOver(inside, held, minted, lower, higher)
	this.commit(ctx, second)
	if after := this.load(ctx, held, parent); !after.Fresh() {
		this.refuse("the parent %q stands at advance %d after a handoff that committed, so its children and it are recording one share of the log", parent, after.Advance)
	}
	for _, child := range written {
		found := this.load(ctx, held, child.Projection)
		if !sameCheckpoint(found, child) {
			this.refuse("the child %q answered %+v after a handoff that committed, where it was written %+v", child.Projection, found, child)
		}
		if found.Cursor != minted.Cursor {
			this.refuse("the child %q resumes from %q where the parent it was handed stood at %q", child.Projection, found.Cursor, minted.Cursor)
		}
	}
}

// The five statements of a handoff, in the order a split makes them: the parent
// is read, both children are written at advance 1 carrying its cursor, and the
// parent is retired. What the unit reads back is its own work, because a store
// that does not read its own staged rows would answer a split the parent it has
// already removed.
func (this *checkpoints) handOver(ctx context.Context, held event.Checkpoints, parent event.Checkpoint, lower, higher string) []event.Checkpoint {
	found := this.load(ctx, held, parent.Projection)
	if found.Cursor != parent.Cursor {
		this.refuse("the parent %q answered the cursor %q inside a unit of work where its row holds %q", parent.Projection, found.Cursor, parent.Cursor)
	}
	written := []event.Checkpoint{
		{Projection: lower, Cursor: found.Cursor, Advance: 1, Progress: this.progress(found.Progress.Highest, found.Progress.Applied, found.Progress.Quarantined)},
		{Projection: higher, Cursor: found.Cursor, Advance: 1, Progress: this.progress(found.Progress.Highest, 0, 0)},
	}
	for _, child := range written {
		this.save(ctx, held, child)
	}
	this.forget(ctx, held, parent.Projection)
	if staged := this.load(ctx, held, parent.Projection); !staged.Fresh() {
		this.refuse("the parent %q answered advance %d inside the unit that retired it, so the unit does not read its own work and a split run twice under one caller would answer a parent that is already gone", parent.Projection, staged.Advance)
	}
	return written
}

func (this *checkpoints) absentOutside(ctx context.Context, held event.Checkpoints, doing string, names ...string) {
	for _, name := range names {
		if found := this.load(ctx, held, name); !found.Fresh() {
			this.refuse("%s left the child %q at advance %d outside it, so a rollback leaves a row nobody accounted for and the partition it names is recorded twice", doing, name, found.Advance)
		}
	}
}
