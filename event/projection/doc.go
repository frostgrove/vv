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
