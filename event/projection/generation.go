package projection

import (
	"context"
	"errors"
	"fmt"

	"github.com/frostgrove/vv/event"
)

// The row a read path resolves its tables through, and the one write a cutover
// is. ONE ROW PER PROJECTION across all of its generations — it names the
// winner, so a row per generation could not — which is why this is keyed by a
// projection name where a Park is keyed by an Identity.
//
// THE PRECONDITION TRAVELS WITH THE PROMISE. The switch is atomic for every
// table at once FOR A READER THAT RESOLVES THIS ROW IN THE SAME SNAPSHOT AS THE
// TABLES IT THEN READS. A read path that caches the answer per process, per
// request or per connection is a legitimate choice with a window the length of
// its cache, and what it may not be called is atomic. A reader that resolved the
// row before the commit goes on reading the retiring generation afterwards, and
// that is correct until that generation is retired — which is why retirement is
// a second, later step an operator orders against its own readers.
//
// Active is the read path's own question: which generation's tables to read. A
// projection with no row answers Ungenerated and a NIL ERROR, because that is
// the state of every deployment that has never cut over, and an error there
// would stop a live projection over a row it never needed.
//
// IT IS ALSO THE EFFECT GATE'S READ, AND THERE IT CARRIES AN OBLIGATION THIS
// INTERFACE CANNOT ENFORCE: it must be a LOCKING read — `SELECT active … FOR
// SHARE`, `FOR KEY SHARE` — or run in a SERIALIZABLE unit. [[D-126]] leaves the
// isolation level to the caller, and at READ COMMITTED a plain read of this row
// and a concurrent Activate of it do not conflict: both commit, and a generation
// the row no longer names commits the effect it staged anyway. REPEATABLE READ
// does not close it either; a snapshot read of a row another transaction updated
// raises nothing. Under a locking read the cutover's UPDATE waits behind every
// unit that read the row, so the generation that staged is the generation that
// owned the row for the whole of its unit.
//
// The obligation is the implementation's, the way Park's ordering obligations
// are, and what measures it is a rollback rather than a self-report: the
// framework holds a method set and no resource, so it cannot ask this value what
// it locked any more than it can ask which pool it opened.
//
// Activate is fenced and is one statement. It moves the row from `from` to `to`
// and answers an error wrapping event.ErrConflict when the row does not hold
// `from`, so two operators cutting over at once leave one winner and one
// refusal. A read followed by a write is the one spelling it may not have: two
// operators would both read `from`, both find it, and both write.
//
// Both run inside the caller's unit, through the context they are given, and
// what that buys is exact: the arriving generation's own rows, the evidence read
// off them and this row move together or none of them does, so a generation that
// was at the barrier when it was measured is still there when the row moves.
// What it does not buy is a retiring generation that stands still — that one is
// a separate runner committing in its own transaction, nothing here claims or
// locks its rows, and no isolation level would close it. Cutover names that
// window and says how an operator avoids it.
type Generations interface {
	Active(ctx context.Context, projection string) (Generation, error)
	Activate(ctx context.Context, projection string, from, to Generation) error
}

// What a generation delivered, as one number over its whole partition set: every
// event at or below it was delivered by the projection as a whole, and nothing
// above it was.
//
// At is a Position and never a Cursor. This package compares it with >= against
// another generation's Progress.Highest and does nothing else with it — nothing
// resumes from it, stores it as a checkpoint or turns it into a cursor, because
// a position is an ordinal the log assigned and a cursor is bytes a store minted
// ([[D-129]]).
//
// An OPERATOR has one other use for it, and it is the only shipped source of the
// number: the mark observed from the generation now live is what the next
// generation's Spec.EffectsAfter is set to. That is a deployment-time act — a
// human reading a barrier and writing a constant — rather than a call this
// package makes, which is why it does not widen the sentence above.
//
// The distance between two positions is not a count of undelivered events
// either: a log burns a position for a rolled-back append and for an
// optimistic-concurrency loser, so Readiness.Behind is an upper bound and a hint.
type Barrier struct {
	Projection string
	Generation Generation
	At         event.Position
}

// The lowest Highest across a cover, which is the only aggregate a set of
// checkpoint rows has: Highest is a completeness watermark for its own row and
// says nothing across rows, so a max, a sum or an average over a partition set
// is a number no partition ever reached.
//
// All members fresh and none of them retired answers the origin, and that is the
// true answer rather than a vacuous one — the generation delivered nothing, so
// nothing is owed. Some members fresh and some not is refused, naming one that
// holds no row: read as position zero, an absent row answers a barrier at the
// origin for a generation that is live on three partitions out of four, and
// every arriving generation clears it. A member with no row of its own beside a
// row recording that a split retired it is refused by the same rule — that
// retirement is the rows saying the share ran and handed its cursor down, so
// folding it in as the origin is the same lie with a second spelling.
//
// SILENCE IS STILL NOT A BARRIER ANYTHING MAY BE MOVED ON. The origin is the
// honest reading of no rows at all, and it is not evidence a read target may be
// switched on, which is why Cutover refuses it rather than this call widening
// what it reports.
//
// `of` names a projection and a generation and never a partition. The partition
// set is `over`, which is a value somebody checked, and an identity carrying one
// of its own would be a second and silent answer to the question the cover was
// asked.
func Observe(ctx context.Context, checkpoints event.Checkpoints, of Identity, over Cover) (Barrier, error) {
	if of.Projection() == "" {
		return Barrier{}, fmt.Errorf("%w: of is the zero Identity, which NewIdentity never answers — name the projection and its generation through NewIdentity", ErrSpec)
	}
	if over.Count() == 0 {
		return Barrier{}, fmt.Errorf("%w: over is the zero Cover, which NewCover never answers, and a fold over a set nobody checked takes its minimum across a hole", ErrTopology)
	}
	if !of.Partition().Whole() {
		return Barrier{}, fmt.Errorf("%w: of names the partition %s, and a barrier is observed over a whole generation — the partition set is the cover this call was given, and an identity carrying one of its own is a second answer to the same question",
			ErrTopology, of.Partition())
	}
	barrier, _, err := observed(ctx, checkpoints, of, over)
	return barrier, err
}

// The barrier and the rows it was folded from. The two callers need different
// halves of one walk, and walking twice would read the rows at two moments.
//
// The three doors above are Observe's own. A cutover's are cutting's, asked of
// the spec before the unit opens — the cover's size, and an identity built
// through NewIdentity at Whole() — so the refusal reaches the caller without a
// transaction being opened for it.
func observed(ctx context.Context, checkpoints event.Checkpoints, of Identity, over Cover) (Barrier, census, error) {
	held, err := surveyed(ctx, checkpoints, of, over)
	if err != nil {
		return Barrier{}, census{}, err
	}
	return Barrier{Projection: of.Projection(), Generation: of.Generation(), At: held.lowest}, held, nil
}

// What one generation's rows say about it, read once per member: the lowest
// watermark, the quarantined counts summed, and how many members hold a row at
// all.
//
// The third is the one the two callers read differently, so it travels rather
// than being decided here. To Observe, a generation with no rows anywhere is a
// generation at the origin. To Cutover it is nothing at all — nothing behind the
// read target on the arriving side, and no evidence to move on the retiring one.
type census struct {
	lowest      event.Position
	quarantined uint64
	recorded    int
}

func surveyed(ctx context.Context, checkpoints event.Checkpoints, of Identity, over Cover) (census, error) {
	var held census
	var absentRow Identity
	for _, part := range over.Partitions() {
		member, err := NewIdentity(of.Projection(), of.Generation(), part)
		if err != nil {
			return census{}, fmt.Errorf("the member %s of this cover names no identity: %w", part, err)
		}
		tracker, err := tracking(checkpoints, member)
		if err != nil {
			return census{}, err
		}
		found, err := tracker.Load(ctx)
		if err != nil {
			return census{}, fmt.Errorf("%w: the member %q could not be read: %w", ErrTopology, member, err)
		}
		if found.Fresh() {
			if refusal := neverHandedDown(ctx, checkpoints, member); refusal != nil {
				return census{}, refusal
			}
			if absentRow == (Identity{}) {
				absentRow = member
			}
			continue
		}
		if held.recorded == 0 || found.Progress.Highest < held.lowest {
			held.lowest = found.Progress.Highest
		}
		held.quarantined += found.Progress.Quarantined
		held.recorded++
	}
	if held.recorded > 0 && held.recorded < over.Count() {
		return census{}, fmt.Errorf("%w: %q holds a checkpoint row for %d of the %d members of its cover and none for %q, so what this set delivered is not a number its rows answer. A member with no row is not a member at position zero: the two are told apart here rather than folded together, because folding them answers the origin for a generation that is live everywhere else",
			ErrTopology, of, held.recorded, over.Count(), absentRow)
	}
	return held, nil
}

// The second reading of an absent row, and the rows are what tell it apart: a
// member with no row of its own beside a row recording that a split retired it
// did not fail to run — it ran, and its cursor is its children's now. Read as the
// origin it answers a barrier every generation clears, which is the whole of
// UC-180 asked one level down, so the walk asks this of an absent member and of
// no other.
//
// It costs one Load per absent member and nothing at all for a cover whose rows
// are all there, which is every cover a cutover is meant to be given.
func neverHandedDown(ctx context.Context, checkpoints event.Checkpoints, member Identity) error {
	tracker, gone, err := retired(ctx, checkpoints, member)
	if err != nil {
		return fmt.Errorf("%w: the retirement of %q could not be read, and an absent row is read as the origin or as a handoff on the strength of that one answer: %w", ErrTopology, member, err)
	}
	if tracker == nil || gone.Fresh() {
		return nil
	}
	return fmt.Errorf("%w: %q holds no checkpoint row of its own and %q records that a split retired it at the cursor it was handed down from, so this share ran and what it delivered is its children's watermark rather than the origin. Observe and cut over across the cover those children record at — folding a retired member in as position zero answers a barrier every arriving generation clears",
		ErrTopology, member, tracker.Projection())
}

// What one generation still owes a barrier.
//
// Quarantined is the durable cumulative count summed across the generation's
// partitions, and it never falls. Holes is what the queue answers now — the
// letters it holds plus the letters an operator evicted without applying — and
// it is the number a cutover reads: a generation that parked a sequence and then
// redrove it completely has a read model with no holes and a Quarantined that
// stands for ever.
type Readiness struct {
	Reached     bool
	Behind      event.Position
	Quarantined uint64
	Holes       uint64
}

// Whether the arriving generation has delivered everything the barrier accounts
// for. The comparison is >= against the lowest Highest of its own cover and
// nothing else.
//
// Holes is asked once, of the whole generation, because a park is keyed by an
// identity with the partition dropped. A nil park answers zero, which is what a
// projection with no queue has.
func Reached(ctx context.Context, checkpoints event.Checkpoints, park Park, barrier Barrier, arriving Identity, over Cover) (Readiness, error) {
	if arriving.Projection() == "" {
		return Readiness{}, fmt.Errorf("%w: arriving is the zero Identity, which NewIdentity never answers — name the projection and its generation through NewIdentity", ErrSpec)
	}
	if over.Count() == 0 {
		return Readiness{}, fmt.Errorf("%w: over is the zero Cover, which NewCover never answers, and a generation measured across a hole is one that reached the barrier by arithmetic", ErrTopology)
	}
	if !arriving.Partition().Whole() {
		return Readiness{}, fmt.Errorf("%w: arriving names the partition %s, and readiness is asked of a whole generation — the partition set is the cover this call was given",
			ErrTopology, arriving.Partition())
	}
	held, _, err := reaching(ctx, checkpoints, park, barrier, arriving, over)
	return held, err
}

// The two things a barrier itself can be wrong about, and both are exact. A
// barrier of another projection is evidence about another log — and the zero
// Barrier is one of those, because it names no projection at all, which is what
// closes the door a caller could otherwise hand a barrier of zero through. A
// barrier observed from the generation it is then asked about is reached by
// arithmetic rather than by draining.
func reaching(ctx context.Context, checkpoints event.Checkpoints, park Park, barrier Barrier, arriving Identity, over Cover) (Readiness, census, error) {
	if barrier.Projection != arriving.Projection() {
		return Readiness{}, census{}, fmt.Errorf("%w: this barrier was observed from %q and it is being asked about %q, which is another projection over another set of rows — the zero Barrier names no projection at all, and that is why one cannot be handed in",
			ErrTopology, barrier.Projection, arriving.Projection())
	}
	if barrier.Generation == arriving.Generation() {
		return Readiness{}, census{}, fmt.Errorf("%w: this barrier was observed from generation %d of %q and is being asked about that same generation, which clears its own watermark whatever it has delivered",
			ErrTopology, barrier.Generation, barrier.Projection)
	}
	held, err := surveyed(ctx, checkpoints, arriving, over)
	if err != nil {
		return Readiness{}, census{}, err
	}
	answered := Readiness{Reached: held.lowest >= barrier.At, Quarantined: held.quarantined}
	if !answered.Reached {
		answered.Behind = barrier.At - held.lowest
	}
	if absent(park) {
		return answered, held, nil
	}
	holes, err := park.Holes(ctx, arriving.Whole())
	if err != nil {
		return Readiness{}, census{}, fmt.Errorf("%w: the park of %q could not be counted, and a cutover admitted without that number is one made over a read model nobody asked about: %w",
			ErrTopology, arriving, err)
	}
	answered.Holes = holes
	return answered, held, nil
}

// What a cutover is performed on, and inside.
//
// THERE IS NO BARRIER FIELD. A barrier a caller can invent is not evidence, and
// the zero value of one would admit a generation that has delivered nothing —
// so Cutover observes its own from the retiring generation's rows, inside the
// unit, in the same breath as the write.
//
// Unit is the caller's — crud.InNewTx(ctx, source, work) is the one-line
// spelling — and it opens one transaction on the resource the checkpoint rows
// and the ownership row both live in. Park is optional: a nil one answers no
// holes, which is what a projection with no queue has.
//
// AcceptQuarantined admits a generation whose read model has holes — letters
// still parked, or letters an operator evicted without applying. It is an
// operator's deliberate act and is reachable by no default: the ordinary
// recovery path is park, fix, redrive, cut over, which leaves no holes and needs
// no override, and a check that is always overridden has stopped being a check.
type CutoverSpec struct {
	Checkpoints event.Checkpoints
	Generations Generations
	Park        Park
	Projection  string
	From, To    Generation
	Retiring    Cover
	Arriving    Cover
	Unit        func(ctx context.Context, work func(context.Context) error) error

	AcceptQuarantined bool
}

// The switch of the read target, and the whole of it is one fenced write. A
// rollback is this call with From and To exchanged and the two covers with them,
// which is not elegance: it is what makes the rollback path exercised by the
// tests the forward one is.
//
// Six steps inside one transaction of the caller's, and the order is the
// argument. The barrier is observed from the retiring generation's own rows
// rather than taken from the caller; a retiring generation with no rows at all is
// ErrRetired, because the barrier it would answer is the origin and every
// generation clears one; the arriving generation is measured against it over its
// own cover; a generation with no rows at all is ErrRetired there too, because a
// dropped generation and one that never ran read alike from the rows and neither
// has anything behind it; holes are refused unless the operator said otherwise;
// and then the row moves, once, fenced.
//
// THE COVERS ARE THE ONES THOSE GENERATIONS RECORD AT, and this call cannot check
// that for you — it reads the rows the cover names and no others. A live
// generation running at four partitions declared here as Whole() answers silence,
// and silence is refused rather than folded into a barrier at the origin. So is a
// cover whose members a split retired.
//
// THE OVERLAP WINDOW IS NAMED AND NOT CLOSED. The barrier is the retiring
// generation's watermark as this call read it. That generation is a separate
// runner committing in its own transaction: if it is still advancing it goes past
// the barrier while this unit is open, and the read target then moves to a
// generation standing where the retiring one stood a moment ago. Reads move
// BACKWARDS, by that generation's advance over the life of this transaction, and
// they stay there until the arriving generation catches up. Drain or stop the
// retiring generation before, or as, the switch commits and there is no window —
// Observe it twice and see whether the barrier moved, which is the whole of the
// check. Spec.Pace on the arriving generation lengthens the recovery, so drop it
// before cutting over.
//
// The one mechanism either reference has for this is Axon's resetTokens, which
// requires the processor to be shut down and then claims every token inside the
// transaction. Neither half is taken here. This call cannot stop a runner in
// another process, and claiming a live generation's checkpoint rows means writing
// them, which takes that runner's fence away — an operator's write in the advance
// path of a runner nobody here supervises, to buy a window an operator closes by
// draining.
//
// It never refuses on Progress.Quarantined. That count is cumulative and never
// falls, so a generation that parked a sequence and redrove it completely would
// be refused by it for ever — the ordinary recovery path closed by the check
// meant to guard it.
func Cutover(ctx context.Context, spec CutoverSpec) error {
	retiring, arriving, err := cutting(spec)
	if err != nil {
		return err
	}
	var refused error
	var ran bool
	answered := spec.Unit(ctx, func(inner context.Context) error {
		ran = true
		refused = switching(inner, spec, retiring, arriving)
		return refused
	})
	switch {
	case refused != nil:
		return refused
	case answered != nil:
		return answered
	case !ran:
		return fmt.Errorf("%w: the read target of %q was not moved from generation %d to %d: %w", ErrTopology, spec.Projection, spec.From, spec.To, errUnitRanNothing)
	}
	return nil
}

// What this call would move, decided before the unit opens: two names and one
// arithmetic fact about the two generations are facts about the spec, and a
// refusal of any of them is one the caller gets without a transaction being
// opened for it. They are collected, because a cutover assembled wrong is
// usually assembled wrong in more than one field.
func cutting(spec CutoverSpec) (Identity, Identity, error) {
	var problems []error
	if absent(spec.Checkpoints) {
		problems = append(problems, fmt.Errorf("%w: Checkpoints names no checkpoint store, and a cutover derives its own evidence from the rows of one", ErrSpec))
	}
	if absent(spec.Generations) {
		problems = append(problems, fmt.Errorf("%w: Generations names no ownership row, and the switch of the read target is the one write a cutover is", ErrSpec))
	}
	if spec.Unit == nil {
		problems = append(problems, fmt.Errorf("%w: Unit names no unit of work, and an observation and a switch issued outside one point the read target at a generation that was ready a moment ago", ErrSpec))
	}
	if spec.From == spec.To {
		problems = append(problems, fmt.Errorf("%w: From and To both name generation %d, and a cutover moves the read target from one generation to another", ErrTopology, spec.From))
	}
	if spec.Retiring.Count() == 0 {
		problems = append(problems, fmt.Errorf("%w: Retiring is the zero Cover, which NewCover never answers, and the barrier is the lowest watermark across the retiring generation's own partition set", ErrTopology))
	}
	if spec.Arriving.Count() == 0 {
		problems = append(problems, fmt.Errorf("%w: Arriving is the zero Cover, which NewCover never answers, and what the arriving generation delivered is the lowest watermark across its own partition set", ErrTopology))
	}
	retiring, named := NewIdentity(spec.Projection, spec.From, Whole())
	if named != nil {
		problems = append(problems, fmt.Errorf("Projection and From name no identity: %w", named))
	}
	arriving, taken := NewIdentity(spec.Projection, spec.To, Whole())
	if taken != nil {
		problems = append(problems, fmt.Errorf("Projection and To name no identity: %w", taken))
	}
	if len(problems) > 0 {
		return Identity{}, Identity{}, errors.Join(problems...)
	}
	return retiring, arriving, nil
}

func switching(ctx context.Context, spec CutoverSpec, retiring, arriving Identity) error {
	tracker, err := tracking(spec.Checkpoints, retiring)
	if err != nil {
		return err
	}
	if err := inTheCallersUnit(ctx, tracker, spec); err != nil {
		return err
	}
	barrier, stood, err := observed(ctx, spec.Checkpoints, retiring, spec.Retiring)
	if err != nil {
		return err
	}
	if stood.recorded == 0 {
		return unobservable(retiring)
	}
	ready, held, err := reaching(ctx, spec.Checkpoints, spec.Park, barrier, arriving, spec.Arriving)
	switch {
	case err != nil:
		return err
	case held.recorded == 0:
		return unrecorded(arriving)
	case !ready.Reached:
		return behindTheBarrier(arriving, barrier)
	case ready.Holes > 0 && !spec.AcceptQuarantined:
		return holedRead(arriving, ready)
	}
	if err := spec.Generations.Activate(ctx, spec.Projection, spec.From, spec.To); err != nil {
		return fmt.Errorf("the read target of %q could not be moved from generation %d to %d: %w", spec.Projection, spec.From, spec.To, err)
	}
	return nil
}

// The one thing a caller's unit of work can be wrong about that costs a cutover
// its meaning, asked where the pass and the handoff ask it: inside, before
// anything is read. A unit that opened nothing leaves the arriving generation's
// rows and the row this call writes in two snapshots, so a generation that was
// at the barrier when it was measured need not be when the read target moves.
//
// What the unit does not buy is the retiring generation standing still. That one
// commits in its own transaction and nothing here holds it — Cutover's own
// comment names that window, and a unit is not what closes it.
func inTheCallersUnit(ctx context.Context, tracker *event.Tracker, spec CutoverSpec) error {
	authority, err := tracker.Transaction(ctx)
	if err != nil {
		return fmt.Errorf("%w: the cutover of %q asked for its evidence and its switch inside the caller's unit and what that unit bound for the checkpoint store is not a transaction: %w", ErrSpec, spec.Projection, err)
	}
	if !authority.Valid() {
		return fmt.Errorf("%w: the cutover of %q asked for its evidence and its switch inside the caller's unit and Unit bound no transaction of the checkpoint store's backing, so the arriving generation's rows and the row this call writes are two snapshots and that generation can fall behind between them", ErrSpec, spec.Projection)
	}
	return nil
}

// The retiring half of the same absence, and it is refused for a different
// reason than the arriving half is. There the generation has nothing behind it;
// here it has nothing to say, and the barrier folded from its silence is the
// origin, which every arriving generation clears. Both readings of the silence
// are named, because the rows do not tell them apart and the operator can.
func unobservable(retiring Identity) error {
	return fmt.Errorf("%w: %q holds no checkpoint row for any member of the cover this cutover names as its retiring set, so the barrier derived from it is the origin and the read target would move on no evidence at all. Two readings, and the rows tell neither apart: the cover is not the one this generation records at — a generation running at four partitions is not observed through Whole() — or nothing ever recorded here, and standing a read target up where nothing preceded it is a row the application's own Generations writes rather than a switch this call derives",
		ErrRetired, retiring)
}

// Neither of these two names a number the log assigned: a barrier and the
// distance to it are positions, and a refusal names the rule that was broken and
// not the data. What an operator reads the numbers off is Reached, which answers
// them as values.
func unrecorded(arriving Identity) error {
	return fmt.Errorf("%w: %q holds no checkpoint row for any member of its cover, so it has recorded nothing and there is nothing behind the read target this call would point at. A generation whose rows were dropped and one that never ran read alike from the rows, and both are refused for the same reason: what a read served from it would answer is an empty read model",
		ErrRetired, arriving)
}

func behindTheBarrier(arriving Identity, barrier Barrier) error {
	return fmt.Errorf("%w: %q has not delivered everything the barrier observed from generation %d of %q accounts for, and the read target is not pointed at a generation that has delivered less than the one it replaces. Let it drain and cut over again — nothing here is spent, and a rollback is refused by the same arithmetic when the generation it would point back at fell behind. Reached answers how far it still is",
		ErrTopology, arriving, barrier.Generation, barrier.Projection)
}

func holedRead(arriving Identity, ready Readiness) error {
	return fmt.Errorf("%w: %q reached the barrier and its queue and its evictions account for %d envelopes its read model never received, so pointing reads at it publishes those holes. Drain the queue with a redrive and cut over with no override, which is the ordinary recovery, or set AcceptQuarantined to point reads at a generation known to be missing them. This refusal never reads Progress.Quarantined, which is cumulative and would refuse a generation that recovered completely for ever",
		ErrTopology, arriving, ready.Holes)
}
