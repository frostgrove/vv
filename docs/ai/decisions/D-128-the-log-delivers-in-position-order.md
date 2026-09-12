# D-128 — The log delivers in position order, and that is a kernel law

**Status:** accepted
**Invariant:** `Log.ReadAll` answers envelopes at strictly ascending positions,
and one stream's events appear in the log in the order that stream holds them.
Both hold for every store, on every page, with no capability to deny either.
`Reader.checkPage` refuses a page that breaks the first; the conformance suite
certifies both, ungated. `Progress.Highest` is therefore a completeness
watermark: once a checkpoint carrying `Highest = P` is saved, every event the log
will ever hold at a position at or below `P` has already been delivered, and a
read resumed from that checkpoint's cursor answers only positions above `P`.

## The decision

The alternative was real and it had a working implementation behind it. The
PostgreSQL event-sourcing reference — `github.com/eugene-khyst/postgresql-event-sourcing`
@ `90faafb` — stamps the writing transaction's `xid8` on every event row and
delivers `ORDER BY TRANSACTION_ID ASC, ID ASC`
(`postgresql-event-sourcing-core/…/repository/EventRepository.java:87`), with the
ceiling `TRANSACTION_ID < pg_snapshot_xmin(pg_current_snapshot())`. That read is
gap-free **by construction**: one row comparison, one index, nothing to settle.
`event/eventpg/read.go` reconstructs the same guarantee with a watermark walk —
a reach, a bound, a floor and a settling rule — resting on five facts that
compose, and it has already produced two serious defects under review. On the
evidence available from the write path alone, the reference's read is better than
vv's, and adopting it would mean adopting its order, because **the order is the
mechanism**: `ID` is vv's `position`, and under `(TRANSACTION_ID, ID)` positions
go backwards inside one page.

It is refused, and not because vv's page is simpler.

**The reference's order carries a precondition the reference never states, and
vv is forbidden to satisfy it.** The precondition is that a writing transaction
takes its transaction id *at the append and nowhere earlier*. The reference
satisfies it structurally and by accident: `@Transactional` opens the
transaction, `Propagation.MANDATORY` on every repository forbids any half of the
work escaping it, and the aggregate version CAS is the transaction's first write.
So for one aggregate the id order, the lock order and the version order are the
same order.

vv's store joins a transaction it did not open. [[D-118]] is why — a durable write
made while the caller's transaction is bound to the store's own source is written
inside it — and [[D-126]] forbids the store opening one of its own. By the time
`Append` runs, the caller's transaction may have been stamped by an unrelated
write minutes earlier. Then the transaction id order and the stream's version
order come apart, and the tuple read reorders one stream against itself.

Measured on PostgreSQL 17.9, over the shape `event/eventpg` writes — a `streams`
CAS row, an `events` identity `position`, `pg_current_xact_id()` on the row:

```
tx B: BEGIN; INSERT INTO app_side …          -- xid 242922, an unrelated write
tx A: BEGIN; append ledger/acct version 1    -- xid 242923, its first write
tx A: COMMIT
tx B: append ledger/acct version 2           -- reads A's committed version 1
tx B: COMMIT
```

| delivery | position | writer_xid | version |
|---|---|---|---|
| position order | 1 | 242923 | 1 |
| position order | 2 | 242922 | 2 |
| **tuple order** | **2** | **242922** | **2** |
| **tuple order** | **1** | **242923** | **1** |

The tuple read hands a projector version 2 of one stream before version 1. The
control — the same interleaving with no earlier write in B, which is the
reference's own architecture — delivers 242925/version 1 then 242926/version 2,
in order. So the reference is right about its own system and the technique does
not transfer.

**Positions going backwards was never the objection.** A projector that consumes
a page and checkpoints does not care that within one page position 6 precedes 5,
as long as the cursor never advances past an undelivered event — the tuple read
gives that, and more strongly than the ascending check does. What it buys is that
no committed event is skipped; delivery stays at least once (§INV-072) under
either order, and no ordering of the log makes it fewer. What it does not give
is `probe.subsequence`
(`event/eventtest/sections_read.go:47-59`): *one stream's order is a subsequence
of the log's*. That is the guarantee a projector folding per stream is built on,
it is what makes a read model and a `Repo.Load` agree, and there is no ordering
available over `(writer_xid, position)` that restores it once the caller's
transaction owns the id.

**So the ascending check is a proxy — and the law it proxies for is stronger, not
weaker.** `Reader.checkPage`'s stated purpose is that a consumer never
checkpoints past an event it never saw. The real invariant is that **the cursor
tiles the log in position order**: the page's positions ascend, the cursor is the
last of them, and the resumed read continues above it. Ascending positions is the
half of that a single page can be checked against, which is why the check is
written that way; the other half is the store's, is not checkable from one page,
and is certified live by the resumption section walking across an in-flight
writer. The check stays, and it stays a refusal rather than a capability probe.

**A capability was the shape available and it is the wrong shape.**
`Capabilities` already carries `MonotoneVisibility`, and adding `OrderedDelivery`
beside it would cost no new mechanism (`event/store.go:32-37`,
`event/eventtest/inventory.go:44`). `MonotoneVisibility` is a fair capability
because a consumer that does not need it is still correct without it. Delivery
order is not that kind of question: a projector folding per stream against a
store that denies the capability is **silently wrong**, with no error on any
path, and it is wrong in the read model rather than in the log. A capability that
half the stores deny makes the kernel's contract the weaker one for every
portable consumer, and portability across stores is the whole of what `eventtest`
certifies. Ordering is a law.

## What `Progress.Highest` means, and why it survives

It stays the completeness watermark, because position order stays a law and only
position order makes the sentence true. One testable sentence:

> Once a checkpoint carrying `Highest = P` has been saved for a projection, a
> read resumed from that checkpoint's cursor answers only positions above `P`,
> and no event at a position at or below `P` is ever delivered to that projection
> for the first time — every one of them was already handed to the handler.

Two clauses are load-bearing and neither is decoration. *Delivered*, not
*applied*: `Quarantined` is non-zero exactly when the destination has holes, and
`event/checkpoint.go:22-24` already says that is what keeps `Highest` from
reading as a completeness claim about the read model. And *for the first time*:
delivery is at least once (§INV-072), so an event at or below `P` may certainly
be delivered again after a crash.

The alternative — `Highest` becomes "the highest position on the page", safe
under either order — was rejected on the same ground the capability was. It is
strictly weaker, nothing in the type or the name marks the weakening, and every
consumer reading it as lag keeps reading it as lag while it silently stops being
lag. Adding a second field to carry the watermark beside it was rejected as
paying a permanent contract cost for an order this decision does not adopt.

`event/projection/pass.go:220` fills it as `this.page[len(this.page)-1].Position`
— the last envelope of the page, which is the highest one **because** of this
law. That line is correct under D-128 and is one of the sites the tuple read
would have made wrong without changing.

## What would change the answer

Say so out loud rather than reopening this quietly. Two things would, and one
would not.

- **`event` gains an at-version load and an integration-event sender**, and
  per-stream subsequence turns out to be recoverable another way — for instance
  a delivery order over `(writer_xid, position)` plus a per-stream reorder buffer
  the consumer owns. That is a different contract, not a relaxation of this one,
  and it is a new decision.
- **[[D-118]] is superseded** and the event store opens its own transaction for
  the append. Then the precondition above holds by construction, exactly as it
  does in the reference, and the tuple read becomes available. This is the real
  hinge: D-128 is downstream of D-118, and anyone reopening D-118 must read this
  file.
- **The watermark walk produces a third defect.** That is an argument for
  reworking `event/eventpg/read.go`, and it is not an argument for the tuple
  order, because the tuple order is not the only gap-free read. README §4-7-2 of
  the reference records an alternative it documents and does not implement —
  `pg_sequence_last_value` plus `LOCK … IN SHARE ROW EXCLUSIVE MODE` in its own
  transaction — which is gap-free **and** in position order, at the cost of
  blocking all writes once per poll. It is in the backlog for that reason.

## What it forbids

- Do not add `Capabilities.OrderedDelivery`, `Capabilities.PositionOrder`, or any
  other way for a store to deny either half of the invariant.
- Do not relax `Reader.checkPage`'s ascending arm, and do not gate it on a
  capability. A page that goes backwards is `ErrBackend` for every store.
- Do not gate `probe.ascending` or `probe.subsequence` in
  `event/eventtest/sections_read.go` behind a `needs` function. Both sections are
  ungated on purpose; a `needs` there is this decision being reversed by one
  line.
- Do not restate `Progress.Highest` as "the highest position of the page". It is
  the same number and a different promise.
- Do not implement the `(writer_xid, position)` tuple read in `event/eventpg`.
  The column, the index and the `vve2` cursor are `eventpg`'s own business; the
  order they exist to serve is not, and without the order they buy nothing.
- Do not order a global read by `recorded_at`. It is a database clock
  (`append.go:123`) rather than an application one, which makes it comparable
  across writers and still not an order: two writers can share an instant, and a
  commit can land long after the statement timestamp it carries.

## Where it lives

- `event/store.go` — `Log.ReadAll`'s contract text, which is the sentence a store
  author implements against, and `Capabilities`, which deliberately has no
  member for this.
- `event/reader.go` — `Reader.checkPage`, the one arm of the law a single page
  can be checked against, and the comment that says which half it is.
- `event/eventtest/sections_read.go` — `probe.ascending` and `probe.subsequence`,
  the two halves certified for every store, plus `globalOrderSection`'s re-read
  arm, which is what says a position is never reassigned.
- `event/eventtest/sections_resumption.go` — `acrossTheFlight`, the half no page
  check can see: a cursor that advanced past a position that had not settled.
- `event/checkpoint.go` — `Progress.Highest`, whose meaning is this law read one
  level out.
- `event/eventpg/read.go` — the watermark walk, which is what a store pays to
  keep the law over an identity column that hands out positions before commit.

## Proven by

- `TestAConsumerReadsThroughPagesTheStorePublished` (`event/reader_test.go:69-97`)
  — its `a page whose positions do not ascend` case, which swaps the first and
  last envelopes of a real page and requires `ErrBackend`.
- `probe.ascending`, run twice per store by `globalOrderSection` and
  `globalPagingSection`, and `probe.subsequence`, run by `globalOrderSection` —
  the conformance halves, ungated for every store. Each of the two is named by a
  defect of its own in the inventory, so neither can be deleted quietly:
  `reorderedStreams` answers one stream's two events in the log in the order
  opposite to its own with the positions still ascending, which only
  `probe.subsequence` sees, and `movingPositions` answers one event at one
  position on one walk and at another on the next, which only the re-read arm
  does.
- `TestVersionsAreDenseAndPositionsAscendWhateverTheClockSays` and
  `TestManyWritersLeaveOneDenseHistoryAndAMonotoneLog` — the write-side half the
  read-side law rests on.
- The live measurement above, on PostgreSQL 17.9, with its control: the same
  interleaving without an earlier write in the caller's transaction puts the two
  versions back in order, so the difference is the caller's transaction and not
  the schedule.
- `TestAProjectionDoesNotPassAPositionAWriterCouldStillCommit`
  (`event/eventpg/projection_integration_test.go`, live) — the law observed
  through a consumer rather than through a page: with a writer holding `p` and
  `p+1` committed, the projection applies nothing and saves nothing, and after
  the commit its read model holds `p` and then `p+1` in that order. **Control:**
  the same projection over a quiescent log delivers at once, so a projection
  that stalled for good would pass the first half.

**Owed, and named here so a later phase does not have to re-derive it:** a
conformance arm that fails a store whose global read reorders one stream when the
appends were made from two callers' transactions stamped in the opposite order.
`probe.subsequence` proves the property today over appends the suite makes
itself, and the suite never stamps a caller's transaction before appending, so it
would not catch the tuple read. It belongs in `sections_read.go`, needs
`Transactions`, and is P4.

## See also

[[D-118]] [[D-121]] [[D-122]] [[D-126]] [[FL-036]] [[UC-032]] — and
`.agents/artifacts/gaps/EVENTSOURCE_REFERENCE.md`, the adjudication of the
reference this decision answers, technique R1/R2 and §Blocking.
