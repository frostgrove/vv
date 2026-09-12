// Package projection turns a log into a read model: one checkpointed consumer,
// supervised by the host rather than by itself.
//
// Delivery is at least once, in both advance modes, and the framework
// deduplicates nothing. What it hands a handler instead is the identity that
// makes idempotency cheap and that it was already carrying: (Stream, Version) is
// unique and stable for every event ever written, so an upsert keyed on it is
// the whole of what most handlers need. Under InUnit the handler's writes and
// the checkpoint advance ride in one transaction — as far as the handler writes
// through the context the unit gave it, which is an obligation this package
// checks as much of as it can see and states where it cannot.
//
// One projection name is one writer, and the checkpoint's fence is what holds
// when a deployment runs two — a rolling restart does, on purpose, for a few
// seconds. A save the fence refuses does not stop the loser: it takes the row
// the winner left, resumes from that row's own cursor and backs off, and says so
// through ErrOvertaken on State.Err and through Ready once the losing streak
// outlasts Tolerate. Under InUnit the advance is claimed before the handler
// runs, so against a checkpoint store that evaluates its fenced save against a
// tuple it is holding the loser is refused before it applies anything; under
// AfterApply there is nothing to hold across a handler call and both instances
// apply the overlapping page, which is what at least once means here.
//
// A pass presents one advance. Spec.Unit runs the work it is given exactly once,
// and a unit that retries its transaction answers the failure instead: the page
// is re-delivered rather than saved over twice.
//
// A permanent failure under OnPermanentFailure: ParkSequence parks a SEQUENCE
// and not an event. The failing envelope goes to the caller's own queue with its
// cause, every later envelope of the same sequence goes with it without reaching
// the handler at all, and every other sequence in the page carries on — so
// nothing is applied over a read model that never received what came before it.
// It constructs only beside InUnit and a resolvable Destination, because the
// order it promises is a property of the queue and the read model committing
// together. While the queue holds nothing it costs one round trip per resume and
// nothing per pass. A redrive is the operator's, on the operator's goroutine,
// and it touches no checkpoint.
//
// The live effects are not the projection. A Handler is handed a Batch and holds
// no route to an Effects; a rebuild is a spec with that field nil, so there is
// nothing to withhold from it because there is nothing to hold. Stage is called
// inside the transaction that commits the advance and is contracted to make a
// durable write and nothing a rollback cannot take back — the dial-out belongs
// to whatever drains the stage. Three things suppress it, checked cheapest
// first: a nil Effects, which costs nothing; EffectsAfter, per envelope, the
// envelope's side of it durable and the barrier's side a constant the
// deployment holds, so an interrupted warm-up resumes suppressed and a restart
// under a lower barrier does not; and the ownership row, read through the
// ambient transaction and — this half is the implementation's, because no
// isolation level is chosen here — under a lock the cutover waits on, so a
// retiring generation stops staging in the same breath as it commits its own
// advance. An effect
// follows its envelope rather than its page: a parked envelope is not staged and
// is not lost, because its letter carries it and the redrive that applies it is
// what stages it, while an eviction stages nothing at all.
//
// A wait is the caller's question over the rows the loop already writes, and it
// is not part of the loop: Wait polls on the caller's own goroutine, is bounded
// by the caller's context and costs nothing when nobody is waiting. What it waits
// for is a Mark — a position a store produced, minted from the caller's own
// commit or from a barrier Observe folded — because a target a caller can invent
// is not evidence. Each poll asks the park before the census and asks it every
// poll: a parked sequence is the one case where the watermark is past the mark
// and the read model never received the event, so a wait that compared only the
// watermark would answer reached for it. Reached means DELIVERED to the
// generation and the cover the wait was given, and APPLIED only where a Park and
// a Sequence were supplied; it says nothing about a second projection, a second
// database, a read replica or anything above the mark.
//
// There is no head. A store that will not promise monotone visibility has no
// number that is the end of the log, so being caught up is a statement about the
// last read and never about the log: PhaseFollowing means the last read
// delivered nothing, and the next one may deliver events at positions a writer
// was still holding. Draining and following are one loop with one branch.
//
// Run blocks on the caller's own goroutine and this package starts none. New
// performs no I/O, reads no environment and mutates nothing outside the value it
// returns, and nothing here writes a log line: what an operator needs travels
// through State, the Observer and Ready.
package projection
