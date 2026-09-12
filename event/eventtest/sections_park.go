package eventtest

import (
	"context"
	"errors"
	"strconv"

	"github.com/frostgrove/vv/event"
	"github.com/frostgrove/vv/event/projection"
)

func parkInventory() []section[*park] {
	return []section[*park]{
		{name: "outside a unit", run: outsideAUnitSection},
		{name: "inside the unit", run: parkUnitSection},
		{name: "committed state", run: committedStateSection},
		{name: "counts", run: parkCountsSection},
		{name: "identity", run: parkIdentitySection},
		{name: "bounds", needs: needsADeclaredBound, run: parkBoundsSection},
	}
}

func needsADeclaredBound(this *park) string {
	sequences, letters := this.factory.Sequences, this.factory.Letters
	if sequences <= 0 && letters <= 0 {
		return "this factory declares neither of the two bounds this queue refuses at, and the numbers are the implementation's — ErrParkFull is what this package declares and a bound nobody stated is not one nobody has"
	}
	if sequences > widestBound || letters > widestBound {
		return "this factory declares a bound of " + strconv.Itoa(max(sequences, letters)) +
			" letters, and this harness will not write that many rows into somebody's queue to reach it: declare it over a park configured no wider than " + strconv.Itoa(widestBound)
	}
	return ""
}

// Sequences is asked before a pass opens a unit, once per resume and then once
// per pass while the count is non-zero. An implementation that required the
// ambient transaction here refuses every one of those, a pass reads the refusal
// as a postpone, and the projection retries for ever without advancing and
// without halting.
func outsideAUnitSection(this *park) {
	ctx := this.context()
	held := this.queue()
	of := this.of("out", projection.Ungenerated)

	if found := this.sequences(ctx, held, of); found != 0 {
		this.refuse("a projection nobody has parked into holds %d sequences", found)
	}
	this.parked(ctx, held, this.letter(of, "s-out", 1))
	if found := this.sequences(ctx, held, of); found != 1 {
		this.refuse("a projection holding one parked sequence counts %d of them outside a unit of work, and that count is what a resume reads before it opens one", found)
	}
}

// Holds and Park run inside the caller's unit, and that is what orders a
// redrive's eviction against the loop's own blocking test: they are rows in one
// transaction rather than two opinions about what is parked.
func parkUnitSection(this *park) {
	ctx := this.context()
	held := this.queue()
	of := this.of("in", projection.Ungenerated)

	inside, tx := this.begin(ctx, held)
	this.park(inside, held, this.letter(of, "s-in", 1))
	if !this.holds(inside, held, of, "s-in") {
		this.refuse("the blocking test issued inside the unit that had just parked the sequence answers that nothing is parked, so the pass that parks a letter goes on to hand the next envelope of that sequence to a handler over a read model that never received the one before it")
	}
	this.rollback(inside, tx)

	if this.holds(ctx, held, of, "s-in") {
		this.refuse("the unit that parked the letter rolled back and the sequence is still held, so the write was issued outside the caller's unit and the queue records a hole the read model does not have")
	}
	if found := this.sequences(ctx, held, of); found != 0 {
		this.refuse("the unit that parked the letter rolled back and the queue counts %d sequences", found)
	}
}

// HOLDS IS ALSO ASKED OUTSIDE A UNIT, BY A WAIT, on a request path's own
// goroutine and before every census. What it owes there is the committed state:
// an answer out of a snapshot older than the commit tells a caller that a
// sequence its change is stuck behind is not parked, and the wait then waits out
// its whole deadline for a mark that will never be reached.
func committedStateSection(this *park) {
	ctx := this.context()
	held := this.queue()
	of := this.of("wait", projection.Ungenerated)

	if this.holds(ctx, held, of, "s-wait") {
		this.refuse("a queue nobody has parked into holds the sequence %q", "s-wait")
	}
	this.parked(ctx, held, this.letter(of, "s-wait", 1))
	if !this.holds(ctx, held, of, "s-wait") {
		this.refuse("a sequence parked and committed is not held when the question is asked outside a unit of work, so a wait polls a queue it cannot see and answers a deadline for a change that is parked")
	}
}

// The two counts are different questions and a queue that answers one of them
// twice is telling a cutover that a destination with letters in it has no holes.
func parkCountsSection(this *park) {
	ctx := this.context()
	held := this.queue()
	of := this.of("count", projection.Ungenerated)

	this.parked(ctx, held, this.letter(of, "s-one", 1))
	this.parked(ctx, held, this.letter(of, "s-one", 2))
	this.parked(ctx, held, this.letter(of, "s-two", 3))

	if found := this.sequences(ctx, held, of); found != 2 {
		this.refuse("a queue holding three letters across two sequences counts %d sequences, and Sequences is the number of sequences rather than the number of letters", found)
	}
	if found := this.holes(ctx, held, of); found != 3 {
		this.refuse("a queue holding three letters across two sequences answers %d holes, and a hole is a letter the read model never received rather than a sequence it is stuck behind", found)
	}
}

// The identity a park is keyed by is the projection AND its generation. Keyed by
// the name alone, a rebuild reads the retiring generation's queue as its own: it
// finds sequences it never parked, blocks changes that reached its own read
// model, and a cutover reads holes that belong to the generation being retired.
func parkIdentitySection(this *park) {
	ctx := this.context()
	held := this.queue()
	first, second := this.of("id", 1), this.of("id", 2)

	this.parked(ctx, held, this.letter(first, "s-id", 1))
	if this.holds(ctx, held, second, "s-id") {
		this.refuse("a letter parked by %s is held by %s as well, so this queue keys its rows on the projection name and drops the generation", first, second)
	}
	if found := this.sequences(ctx, held, second); found != 0 {
		this.refuse("%s counts %d parked sequences where everything parked was parked by %s", second, found, first)
	}
	if found := this.sequences(ctx, held, first); found != 1 {
		this.refuse("%s counts %d parked sequences where it parked one, so this queue keys its rows on something neither generation is", first, found)
	}
}

// The bound is per sequence and never per queue: a new sequence is refused when
// the queue is at its own limit, and an existing one when it holds its own limit
// of letters. What this package declares is ErrParkFull and what a pass does
// about it — the pass ends, the unit rolls back, nothing is parked and no
// envelope is skipped, and a refusal it cannot recognise is read as a permanent
// failure instead, which parks the sequence the queue had no room for.
func parkBoundsSection(this *park) {
	ctx := this.context()
	held := this.queue()

	if letters := this.factory.Letters; letters > 0 {
		of := this.of("letters", projection.Ungenerated)
		for position := 1; position <= letters; position++ {
			this.parked(ctx, held, this.letter(of, "s-letters", event.Position(position)))
		}
		this.full(ctx, held, this.letter(of, "s-letters", event.Position(letters+1)),
			"the sequence it declares a bound of "+strconv.Itoa(letters)+" letters for")
	}

	if sequences := this.factory.Sequences; sequences > 0 {
		of := this.of("queue", projection.Ungenerated)
		for number := 1; number <= sequences; number++ {
			this.parked(ctx, held, this.letter(of, "s-"+strconv.Itoa(number), event.Position(number)))
		}
		this.full(ctx, held, this.letter(of, "s-over", event.Position(sequences+1)),
			"the queue it declares a bound of "+strconv.Itoa(sequences)+" sequences for")
	}
}

func (this *park) full(ctx context.Context, held projection.Park, letter projection.Letter, what string) {
	inside, tx := this.begin(ctx, held)
	err := held.Park(inside, letter)
	this.rollback(inside, tx)
	if err == nil {
		this.refuse("a letter was admitted into %s, so the bound this factory declares is not one this queue refuses at and a pass has nothing to tell an operator to clear", what)
	}
	if !errors.Is(err, projection.ErrParkFull) {
		this.refuse("a letter over the bound of %s answered %v, which is not an error wrapping projection.ErrParkFull: a pass reads that as a permanent failure and parks the sequence the queue had no room for", what, err)
	}
}
