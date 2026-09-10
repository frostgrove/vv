package projection

import (
	"context"
	"fmt"
	"time"

	"github.com/frostgrove/vv/event"
)

// What a split is performed on, and inside. The framework opens no transaction
// of its own: Unit is the caller's — crud.InNewTx(ctx, source, work) is the
// one-line spelling — and it must open one on the resource the checkpoint store
// lives in, because the four writes below are one handoff or a topology nobody
// can read.
type SplitSpec struct {
	Checkpoints event.Checkpoints
	Identity    Identity
	Unit        func(ctx context.Context, work func(context.Context) error) error
}

// The one topology change there is: one partition becomes two, both starting at
// the parent's exact cursor, and the parent's row is retired. It answers the two
// children a Cover is then declared from, and there is no inverse — a merge
// would have to order two cursors, which is not a question a cursor answers.
//
// It is one way. The retirement is recorded durably, and a runner started at the
// retired share afterwards halts rather than resuming: redeploying the release
// that ran before the split would otherwise walk the whole log again into the
// read model the children are filling, with every row reporting a healthy
// watermark while it happened.
//
// Six steps in one transaction of the caller's, and the ones that look redundant
// are the point. Both children are read BEFORE either is written, because a
// child row beside a live parent is a finer topology already recording and
// writing over it hands one share of the log to two writers. And an absent
// parent is refused whatever the reason for the absence: a partition that never
// ran and one whose row was lost read alike from the rows, and the safe-looking
// guess starts two children at the origin against a live read model.
//
// Every decision is derived from rows read inside the run and nothing is carried
// between runs, so a Unit that runs its body twice performs one split or answers
// a refusal, and never two children at the origin.
func Split(ctx context.Context, spec SplitSpec) (Identity, Identity, error) {
	lower, higher, err := children(spec)
	if err != nil {
		return Identity{}, Identity{}, err
	}
	var refused error
	var ran bool
	answered := spec.Unit(ctx, func(inner context.Context) error {
		ran = true
		refused = handOver(inner, spec, lower, higher)
		return refused
	})
	switch {
	case refused != nil:
		return Identity{}, Identity{}, refused
	case answered != nil:
		return Identity{}, Identity{}, answered
	case !ran:
		return Identity{}, Identity{}, fmt.Errorf("%w: %q was not split: %w", ErrTopology, spec.Identity, errUnitRanNothing)
	}
	return lower, higher, nil
}

// What this call would create, decided before the unit opens: the arithmetic and
// the two names are facts about the spec, and a refusal of either is one the
// caller gets without a transaction being opened for it.
func children(spec SplitSpec) (Identity, Identity, error) {
	switch {
	case absent(spec.Checkpoints):
		return Identity{}, Identity{}, fmt.Errorf("%w: Checkpoints names no checkpoint store, and a split moves the rows of one", ErrSpec)
	case spec.Unit == nil:
		return Identity{}, Identity{}, fmt.Errorf("%w: Unit names no unit of work, and two child rows and one retirement issued outside one are a handoff a crash leaves half-made", ErrSpec)
	case spec.Identity.Projection() == "":
		return Identity{}, Identity{}, fmt.Errorf("%w: Identity is the zero value, which NewIdentity never answers — name the parent through NewIdentity", ErrSpec)
	}
	below, above, err := spec.Identity.Partition().Split()
	if err != nil {
		return Identity{}, Identity{}, err
	}
	lower, err := NewIdentity(spec.Identity.Projection(), spec.Identity.Generation(), below)
	if err != nil {
		return Identity{}, Identity{}, err
	}
	higher, err := NewIdentity(spec.Identity.Projection(), spec.Identity.Generation(), above)
	if err != nil {
		return Identity{}, Identity{}, err
	}
	return lower, higher, nil
}

// The six steps, every one of them through the tracker door and none of them
// against the raw store: the door owns the fence, so a child is written at
// advance 1 because its own Load answered no row, and never because this call
// computed the number.
//
// Progress.Highest is enumerated rather than left to travel with the cursor,
// because the door does not enumerate it: a row at Highest zero is legal and
// nothing refuses it, and a set whose lowest Highest is zero makes every barrier
// derived from it trivially reached. The lower child takes the parent's counts
// and the higher starts at zero, so the sum across the set is what it was.
//
// The retirement is recorded before the row is removed, and that record is the
// step this handoff is not a handoff without. Forget deletes the only evidence
// that the retired share was ever recorded, and the resume that refuses a second
// writer reads rows: without the record, the release that ran before the split
// is admitted at its next deploy, resumes from the origin, and walks the whole
// log into the read model the children are filling.
func handOver(ctx context.Context, spec SplitSpec, lower, higher Identity) error {
	parent, err := tracking(spec.Checkpoints, spec.Identity)
	if err != nil {
		return err
	}
	if err := inACallersTransaction(ctx, parent, spec.Identity); err != nil {
		return err
	}
	if !retirable(spec.Identity) {
		return unrecordable(spec.Identity)
	}
	held, err := parent.Load(ctx)
	if err != nil {
		return fmt.Errorf("%w: the parent %q could not be read: %w", ErrTopology, spec.Identity, err)
	}
	retiring, gone, err := retired(ctx, spec.Checkpoints, spec.Identity)
	if err != nil {
		return fmt.Errorf("%w: the retirement of %q could not be read: %w", ErrTopology, spec.Identity, err)
	}
	first, below, err := loaded(ctx, spec.Checkpoints, lower)
	if err != nil {
		return err
	}
	second, above, err := loaded(ctx, spec.Checkpoints, higher)
	if err != nil {
		return err
	}
	if !gone.Fresh() {
		return alreadySplit(spec.Identity, lower, below, higher, above)
	}
	if held.Fresh() {
		return noParent(spec.Identity, lower, below, higher, above)
	}
	if refusal := recordingFiner(ctx, spec, held, lower, below); refusal != nil {
		return refusal
	}
	if refusal := recordingFiner(ctx, spec, held, higher, above); refusal != nil {
		return refusal
	}

	at := time.Now()
	if _, err := first.Save(ctx, held.Cursor, event.Progress{
		Highest:     held.Progress.Highest,
		Applied:     held.Progress.Applied,
		Quarantined: held.Progress.Quarantined,
		At:          at,
	}); err != nil {
		return fmt.Errorf("%w: the child %q could not be written at the parent's cursor: %w", ErrTopology, lower, err)
	}
	if _, err := second.Save(ctx, held.Cursor, event.Progress{Highest: held.Progress.Highest, At: at}); err != nil {
		return fmt.Errorf("%w: the child %q could not be written at the parent's cursor: %w", ErrTopology, higher, err)
	}
	if _, err := retiring.Save(ctx, held.Cursor, event.Progress{Highest: held.Progress.Highest, At: at}); err != nil {
		return fmt.Errorf("%w: the retirement of %q could not be recorded beside its children: %w", ErrTopology, spec.Identity, err)
	}
	if err := parent.Forget(ctx); err != nil {
		return fmt.Errorf("%w: the parent %q could not be retired beside its children: %w", ErrTopology, spec.Identity, err)
	}
	return nil
}

// Whether the share this child would be written over is already recorded by a
// topology finer than the two rows this call would create — a live child row, or
// a child that was itself split away and whose own children are recording now.
func recordingFiner(ctx context.Context, spec SplitSpec, held event.Checkpoint, child Identity, found event.Checkpoint) error {
	if !found.Fresh() {
		return alreadyFiner(spec.Identity, held, child, found)
	}
	_, gone, err := retired(ctx, spec.Checkpoints, child)
	if err != nil {
		return fmt.Errorf("%w: the retirement of the child %q could not be read: %w", ErrTopology, child, err)
	}
	if gone.Fresh() {
		return nil
	}
	return fmt.Errorf("%w: %q stands at advance %d and its child %q was itself retired by a split, so this share of the key space is recorded by a topology finer still than the two rows this call would write",
		ErrTopology, spec.Identity, held.Advance, child)
}

func tracking(checkpoints event.Checkpoints, identity Identity) (*event.Tracker, error) {
	tracker, err := event.Track(checkpoints, identity.String())
	if err != nil {
		return nil, fmt.Errorf("%w: %q is not a name a checkpoint row can be keyed by, or Checkpoints names no store: %w", ErrSpec, identity, err)
	}
	return tracker, nil
}

// The row that records a split having retired an identity, keyed by a name no
// identity renders. It is an ordinary checkpoint row carrying the cursor the
// parent stood at, so an operator reads where the share was handed down and a
// third-party store needs nothing it does not already do.
//
// The tracker is nil for a name with no room for the mark. Nothing writes one
// for such a name either, so an absent row is conclusive at both doors.
func retired(ctx context.Context, checkpoints event.Checkpoints, identity Identity) (*event.Tracker, event.Checkpoint, error) {
	if !retirable(identity) {
		return nil, event.Checkpoint{}, nil
	}
	tracker, err := event.Track(checkpoints, identity.String()+retiredMark)
	if err != nil {
		return nil, event.Checkpoint{}, err
	}
	held, err := tracker.Load(ctx)
	if err != nil {
		return nil, event.Checkpoint{}, err
	}
	return tracker, held, nil
}

func loaded(ctx context.Context, checkpoints event.Checkpoints, identity Identity) (*event.Tracker, event.Checkpoint, error) {
	tracker, err := tracking(checkpoints, identity)
	if err != nil {
		return nil, event.Checkpoint{}, err
	}
	held, err := tracker.Load(ctx)
	if err != nil {
		return nil, event.Checkpoint{}, fmt.Errorf("%w: the child %q could not be read: %w", ErrTopology, identity, err)
	}
	return tracker, held, nil
}

// The one thing a caller's unit of work can be wrong about that costs the
// handoff its meaning, asked where the pass asks it: inside, before anything is
// read. A Unit that opened nothing leaves four writes the store commits one at
// a time, and a crash between the first child and the retirement is a parent and
// a child recording the same keys with two fences and two watermarks.
func inACallersTransaction(ctx context.Context, parent *event.Tracker, identity Identity) error {
	authority, err := parent.Transaction(ctx)
	if err != nil {
		return fmt.Errorf("%w: %q asked for its handoff inside the caller's unit and what that unit bound for the checkpoint store is not a transaction: %w", ErrSpec, identity, err)
	}
	if !authority.Valid() {
		return fmt.Errorf("%w: %q asked for its handoff inside the caller's unit and that unit bound no transaction of the checkpoint store's backing, so the two child rows, the record of the retirement and the retirement itself would be four commits", ErrSpec, identity)
	}
	return nil
}

func noParent(parent, lower Identity, below event.Checkpoint, higher Identity, above event.Checkpoint) error {
	return fmt.Errorf("%w: %q holds no checkpoint row, and a split hands down a cursor that row is the only record of. Both readings are refused, because the rows tell them apart from nothing: a partition that never ran needs no split — declare the Cover and start its members, and each begins where it is declared — and a partition whose row was forgotten, dropped or restored in part is a row to restore, because a child written at the origin walks the whole log again into a live read model%s",
		ErrTopology, parent, besideAnAbsentParent(lower, below, higher, above))
}

func besideAnAbsentParent(lower Identity, below event.Checkpoint, higher Identity, above event.Checkpoint) string {
	found := ""
	for _, child := range []struct {
		identity Identity
		held     event.Checkpoint
	}{{lower, below}, {higher, above}} {
		if child.held.Fresh() {
			continue
		}
		found += fmt.Sprintf(", %q at advance %d", child.identity, child.held.Advance)
	}
	if found == "" {
		return ""
	}
	return ". This parent's children already hold rows" + found + ", which reads as an earlier attempt that committed after all"
}

// The parent's row is gone and the record of its retirement is there, which is
// the one absence the rows tell apart: this split already happened. The children
// are named from what was read rather than from what would be written, so a
// caller's unit that ran the body twice reads the second refusal as the first
// run's commit.
func alreadySplit(parent, lower Identity, below event.Checkpoint, higher Identity, above event.Checkpoint) error {
	return fmt.Errorf("%w: %q was already retired by a split and holds no row of its own%s. A split hands down a cursor the parent's row is the only record of, and there is nothing left to hand down: the route to a coarser topology is a new generation, not a second split",
		ErrTopology, parent, besideAnAbsentParent(lower, below, higher, above))
}

func unrecordable(parent Identity) error {
	return fmt.Errorf("%w: %q renders %d bytes and recording its retirement needs %d more inside the kernel's ceiling of %d, so this split could not leave the record that keeps the retired share from being started again beside its children — the migration for a name this long is a rename, which is a rebuild",
		ErrTopology, parent, len(parent.String()), len(retiredMark), event.MaxNameBytes)
}

func alreadyFiner(parent Identity, held event.Checkpoint, child Identity, found event.Checkpoint) error {
	return fmt.Errorf("%w: %q stands at advance %d and its child %q already holds a row at advance %d, so this share of the key space is being recorded by a finer topology than this call would create — a split writes a child row nothing else has, and writing over one that is already recording gives one share of the log two writers, each with its own fence and its own watermark",
		ErrTopology, parent, held.Advance, child, found.Advance)
}
