# UC-038 — See the state a history held at a version

**Actor:** the application author answering a question about the past — a
support agent's, an auditor's, or an integration's
**Covered by:** [[FL-036]]

## Scenario

Somebody disputes a change. The order shows a total nobody recognises, and the
question is not *what is it now* but *what was it before that change landed*. The
author has the whole history — that is why they chose this shape — and no way to
ask it anything but *"give me the current state"*.

The workarounds are all wrong in the same direction. Reading the events and
adding up the interesting ones re-implements the fold in the report, and drifts
from it on the first change to the domain. Filtering events by a predicate
answers a question about *some* of the history, which is not a state. And reading
the head state for an integration that publishes one message per recorded fact
makes a replayed stream indistinguishable from N copies of the current state —
which is a correctness bug rather than a reporting inconvenience.

The author also does not want the thing that is one keystroke away from what they
asked for: a value that looks like a load token. Given one, the obvious next step
is to decide from the historical state and write the decision back — a decision
made against the history the read deliberately left out.

## What must hold

1. **The boundary is an exact version, and the result is the complete prefix up
   to it.** Not a filter, not a predicate, not a subset: every recorded fact from
   the first to the named version, folded in order by the same folds a full replay
   uses.
2. **The result yields nothing that can append.** There is no token, no
   provenance value and no second return of any kind. A historical read is a read,
   and what it refuses is the caller's own reasoning — a value that looked like a
   load token would invite deciding from a state that is missing everything above
   the boundary.
3. **A boundary the stream does not hold is a refusal, never a partial state.**
   Version zero, a version past the end of the stream, and a stream with no events
   at all are **one** refusal, in the class a caller can correct: version zero is
   the empty stream, and *"a stream that is empty"* and *"a stream that never
   existed"* are one thing in an append-only history. One honest answer is better
   than four the caller cannot tell apart.
4. **A fact the declaration cannot read is that refusal and the zero state**, on
   exactly the terms a full replay already gives: an unsupported revision, an
   unknown wire name, a refusing conversion or an oversized recorded payload each
   answer their own class, and never the part of the state that happened to fold
   first.
5. **No refusal names a version, a key, a position or an identity.** The refusal
   says the stream is shorter than the boundary this read was given, and names
   neither the boundary nor the head.
6. **The boundary is respected across page boundaries.** A prefix that ends in
   the middle of a page folds exactly up to its version and not one fact further,
   and a history answered out of order is a refusal rather than a state that is
   wrong at the right version with the right count.
7. **There is no timestamp boundary, and there is not going to be one by
   accident.** A recorded instant is a database clock, which is comparable across
   writers and is still not an ordering: two writers can share an instant, and a
   commit can land long after the instant its statement carried. If a time-shaped
   entry point is ever offered, it can only be a lookup that resolves to a
   **version**, and it needs its own contract.
8. **The read costs what it reads and no more.** The over-read is at most one
   page whatever the history's length, so a boundary at the fifth fact of a very
   long stream reads one page.
9. **This is a read over the existing history and adds nothing to the store's
   own contract.** A store certified before this existed is certified after it,
   unchanged: no method, no parameter, no ceiling and no filter is added to what a
   store must implement.

## Out of scope

- **A query language over history.** A version boundary is a prefix; a predicate
  is a query language, and that sentence is the whole of the line.
- **A list-and-filter endpoint over recorded facts**, or anything else that makes
  a fact history behave like a collection.
- **Appending from a historical state**, in any spelling. Correcting the past is a
  new domain command against the present.
- **A timestamp, a date range, or "as of" anything.**
- **A cache, a memo or a snapshot of a folded state.** This read folds from
  recorded facts, in order, every time, over its prefix.
- **Telling an empty history from one that never existed.**
- **A lower bound.** The prefix begins at the first recorded fact; resuming a
  fold from a base state belongs to a contract that does not exist yet.

## See also

[[UC-032]] is the contract this reads inside, and **none of its guarantees
change**: a load still rebuilds from the whole history every time, there is still
no cache, no snapshot and no partial rehydration, and the two reads it names are
still the two reads there are — this is the first of them with a ceiling, not a
third one. This use case exists because a bounded read is a new observable
behaviour and a new set of refusals, and widening a settled contract to absorb one
is how a contract stops meaning anything.
