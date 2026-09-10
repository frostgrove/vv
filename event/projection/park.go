package projection

import (
	"context"

	"github.com/frostgrove/vv/event"
)

// A parked envelope, and the unit a redrive works in. Cause is the failure of
// the letter that could not be applied and is nil for every letter parked behind
// it: those never reached a handler, which is the whole of what the queue is
// for.
//
// Sequencer is the name of the function that answered Sequence. A redrive whose
// spec names another one is refused, because the ordering a letter's place in
// its sequence encodes is not the ordering another key would give it.
//
// Attempt is the attempt the pass was on when the letter was written, so a
// requeue can raise it and an implementation can decide when to stop trying.
type Letter struct {
	Identity  Identity
	Sequencer string
	Sequence  string
	Envelope  event.Envelope
	Cause     error
	Attempt   int
}

// The queue a permanent failure parks a whole sequence in, and it is the
// application's own table. The framework writes to it and counts it; it removes
// nothing, and only a redrive does.
//
// THE IDENTITY IS ALWAYS Identity.Whole() — the projection and its generation,
// with the partition dropped. The framework is the only producer of the value
// handed here, so an implementation that keys its table on what it is given is
// correct by construction. Keyed by the partition instead, a split would orphan
// every letter it holds: the children each read zero parked sequences, never ask
// Holds, and hand the next event of a parked sequence straight to a handler over
// a read model that never received the one before it.
//
// WHERE EACH METHOD RUNS IS PART OF THIS CONTRACT, AND THE FOUR DIFFER. Holds
// and Park run INSIDE the caller's unit of work, through the context they are
// given, and OnPermanentFailure: ParkSequence constructs at no other tier than
// the one that opens it. That is what orders a redrive's eviction against the
// loop's own blocking test: they are rows in one transaction rather than two
// opinions about what is parked. Sequences runs OUTSIDE it, before the pass
// opens one, because it is asked once per resume and then once per pass while
// the count is non-zero — a healthy projection would otherwise open a
// transaction per pass to be told the queue is still empty. Holes is a
// cutover's question and no pass asks it.
//
// So an implementation must not require the ambient transaction in Sequences.
// A refusal there is read as a postpone, and a projection whose count can never
// be read retries for ever without advancing and without halting.
//
// Two bounds, and the check is per sequence rather than per queue: a new
// sequence is refused when the queue is at its own limit, and an existing one
// when it holds its own limit of letters. The numbers are the
// implementation's — Axon's are 1024 and 1024 — and what this package declares
// is ErrParkFull and what a pass does about it.
//
// Sequences is read once per resume and again at the start of every pass while
// the count this loop believes is non-zero, so a healthy projection pays one
// round trip per resume and Holds is never called at all. The count is exact
// rather than cached, which is what makes that safe: only the projection parks
// and only a redrive removes, so a letter inserted by hand leaves the count low
// until the next pass that reads it.
//
// Holes is the second question, and the one a cutover asks: the letters queued
// now plus the letters an operator evicted without applying. Progress.Quarantined
// cannot answer it — it is cumulative and never falls, so a generation that
// parked one sequence and then redrove it completely carries a non-zero count
// for ever while its destination has no holes at all.
type Park interface {
	Sequences(ctx context.Context, of Identity) (uint64, error)
	Holds(ctx context.Context, of Identity, sequence string) (bool, error)
	Park(ctx context.Context, letter Letter) error
	Holes(ctx context.Context, of Identity) (uint64, error)
}
