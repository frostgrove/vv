# D-142 — An operation receipt is the application's table, written beside the append in one transaction

**Status:** accepted
**Invariant:** A receipt is a durable row in the **application's** own table, in
the event store's own database, written through the caller's own transaction and
behind an interface this framework declares and does not implement. The order is
**claim, decide, append, complete** and the claim is first. The fingerprint
covers the composed stream and the encoded records and **not** the version the
token was loaded at. `Store.Append` stays non-deduplicating: no store gains a
table, a column, a migration or a schema version, and a deployment that wants no
receipts deploys nothing.

## The decision

### It is a durable write inside the caller's transaction, which is what [[D-118]] demands before one

[[D-118]] is the rule this lands under, not an exception to it: this framework's
outbox is a durable write inside the caller's bound transaction, and *"'atomic'
across two handles is a sentence with no meaning."* A receipt is a second durable
row in that same unit, so D-118's file asks for a decision before one is written,
and this is it.

The atomicity is **checked** rather than documented. `receipt.Claim` asks the
ledger for its transaction and the store for its own and compares them with
`event.Authority.Same`; unless the store states `Transactions`, unless one of its
transactions is bound to the context, and unless the ledger resolves that same
context to the same transaction, the claim is `ErrSpec` before any statement.
That is `event/projection/pass.go`'s `checkUnit` put to a second purpose. A
receipt that is not atomic with its append is a row that outlives a rollback and
reports an operation that never happened.

### The claim comes first, and the two-retries-at-two-versions case is why

The tempting order is append-then-record: it is one fewer statement on the happy
path and it looks equivalent, because the store already refuses two writers at
one expected version.

It is not equivalent, and the case it loses is the only case the mechanism
exists for. A caller that does not know the outcome of its first attempt reloads
and retries, so its second attempt carries a **different** expected version.
Both are admitted by the store — neither is a concurrency conflict — and the
collision is discovered at the receipt with both sets of events already in the
log. Recording first makes the second attempt read a row before it decides
anything.

Nothing in `event/receipt` can see an append that already happened on a context,
so the order cannot be enforced by inspection. **`receipt.Once` is the spelling
that makes it unwritable in any other order** — claim, run the work only on
`Recorded`, complete in the same transaction — and the open-coded `Claim` /
`Append` / `Held.Complete` form is documented as the shape for a caller that
must answer a repeat differently, not as the shape a reader copies.

### The serialisation point is the primary-key index **plus the level's conflict behaviour**

An earlier draft of this contract said the claim *"holds at every isolation
level"* and published a single-statement CTE to do it. Both halves were executed
against PostgreSQL **17.9** rather than reasoned about, and both were wrong. The
measurement, two `psql` sessions with the winner holding its transaction open
3 s so the overlap is certain, 2026-09-12:

| The loser's side | READ COMMITTED | REPEATABLE READ / SERIALIZABLE | a third caller of the same key, while a repeat holds its unit open 4 s |
|---|---|---|---|
| the CTE — `DO NOTHING` feeding a `UNION ALL` over the same table | **`(0 rows)`** | `40001`, `ROLLBACK` | — |
| `DO UPDATE SET key = receipts.key RETURNING …, (xmax = 0) AS won` | the held row, `won = f` | `40001`, `ROLLBACK` | **blocked 3035 ms** |
| **`DO NOTHING;` then `SELECT`** — what this decision adopts | `INSERT 0 0`, then the held row | `40001`, `ROLLBACK` | **34 ms** |

The loser blocked 2037–2040 ms on every arm; one row survived for the key on
every arm and it was always the winner's; the retry after a `40001` answered the
held row at both stricter levels.

**So the claim is two statements and their order is the mechanism.** `INSERT …
ON CONFLICT (key) DO NOTHING`, **then** a `SELECT` of the same key, in that
order, in the caller's one transaction. The insert is what blocks on the index —
PostgreSQL's speculative insertion waits on a conflicting uncommitted tuple and
then inserts or does not on that transaction's outcome, which is why a claim
never has to answer the `Unresolved` a resolve does. The read has to be a
**statement of its own** because at READ COMMITTED a second statement is a second
snapshot: the CTE shares one snapshot taken before the winner committed, so its
`UNION ALL` branch sees nothing and the ledger answers `won == false` beside no
receipt at all. That is refused by this framework's own door with `ErrLedger`,
which is worth one sentence and not a conformance decorator.

**"Holds at every isolation level" is false, and the true sentence is better.**
At REPEATABLE READ and SERIALIZABLE the loser does not block-and-repeat: it
raises SQLSTATE **`40001`**, which rolls the caller's **whole** unit back — the
claim, the append and the completion together, never split — and the **retry** is
what answers `Repeated`. Nothing is half-written and nothing is appended twice.
`40001` is already `errs.Retryable()` in this tree (`event/eventpg/classify.go`),
so the retry has a home, and **the retry is the caller's**, through
`crud.InNewTx`. This framework still sets no isolation level anywhere, which is
the whole of what [[D-126]] asks: what the level decides is *how a loser fails*,
not whether two callers may append.

**The one-statement form that does work is refused anyway, and that was measured
too.** `DO UPDATE … (xmax = 0) AS won` answers the loser correctly at READ
COMMITTED — and takes a row lock on the winner's receipt, which the repeat then
holds for the whole of its own unit of work, because a claim runs inside the
caller's transaction. A third presentation of the same key waits **3035 ms** on a
caller that is doing nothing with the row; under the two-statement form it waits
**34 ms**. A retry storm is exactly when a key is presented repeatedly, so that
is the load shape the mechanism exists for and the one the lock convoys. Two
smaller reasons: `DO UPDATE` writes a dead tuple and fires row triggers on every
repeat, and its discriminator is a system column where `DO NOTHING`'s is the
affected-row count every driver already has.

**What the two-statement form gives up, stated rather than discovered later:** a
window between the two statements in which a retention sweep could delete the row
the `SELECT` is about to read, which surfaces as `won == false` beside no receipt
and is refused with `ErrLedger`. A horizon shorter than the age of a row written
milliseconds ago is not a horizon, so the window is closed by retention rather
than by the statement.

### The fingerprint covers the append, and deliberately not the version

`Repo.Digest` is SHA-256 over a length-prefixed encoding of the composed stream
and each record's type, revision and payload, in order. Length-prefixed because
`"a"+"bc"` and `"ab"+"c"` are two different batches and one preimage otherwise.

**The version the token was loaded at is not in it.** The worked retry is why: a
retry that arrives in a new process holding an operation key loads what the store
*now* holds — which is the version the first attempt moved the stream to — so a
digest over the version cannot be reproduced by the one caller the mechanism
exists for. Every repeat would answer `Collided` for an operation nobody
performed, and the answer would be unanswerable rather than merely wrong.
`Append`'s own concurrency check is untouched and is where the anchor belongs.
What the digest answers is whether two attempts are the **same operation**, and
an operation is its stream and its records.

The anchor is still available to an operator, and it is **recorded rather than
compared**: for a range that exists, `Receipt.First - 1` is the version the
operation was decided at.

### The preimage is a frozen format

The fingerprint is read back by a **later build than the one that wrote it** — a
receipt written by one deployment is compared against a digest recomputed by the
next, and inside a retention window a deployment is routine. So reordering the
fields, narrowing a length, flipping the byte order or adding a separation tag
answers **every key still in the window** as a collision on an operation nobody
performed: a tidy inside the retention window turns every in-flight key into a
false `Collided`.

The layout is therefore frozen and is held as golden vectors rather than as a
description: the composed stream opens the preimage, each record follows in
order, and **every length and every revision is eight big-endian bytes**.
`TestTheDigestPreimageIsFrozen` is the six vectors; the rest of the digest's
tests are relative and a whole change of layout would preserve their injectivity.

### Byte-reproducibility is an obligation on the application, and it fails closed

Two attempts are the same operation only if they **encode the same bytes**. A
codec that records a clock, a fresh id or a map in iteration order encodes
differently every time, and under one operation key that is a refusal on every
retry rather than a duplicate append — `Collided`, loud, and never wrong. Nothing
in this framework can check it, which is why it is written on `Repo.Digest`, on
both module pages beside `Sequencer`'s three obligations, and here. The recipe is
the same one a `Sequencer` gets: take such a value from the command, the state or
the operation key, and never from the process.

### One key covers one append, to one stream

`Held.Complete` refuses a commit whose `Stream()` is not the claim's. A command
that appends to two aggregates is two operations and needs **two keys**, and the
recipe is a key per append — derive the second from the first by a suffix the
caller owns. A single key over two appends records one range and reports a
success for the half that may not have happened.

### `Store.Append` stays non-deduplicating

The appendix asks for *«отдельную постоянную квитанцию операции, а не изменение
обычного append»* and this decision is that sentence. An idempotent `Append`
would put the key in the store contract, which means a column, a migration, a
schema version and a conformance section for every store — including the ones
this repository has never seen — to serve a capability a deployment may not want.
`event/receipt` is a package of the root module whose closure is `event`, `crud`,
`errs`, `utils` and the standard library; `eventpg` stays at `SchemaVersion = 2`.

## What `jobs` already does, and why a receipt is not one

The repository already ships a key → three-way-verdict mechanism with a live
PostgreSQL driver, and a decision that did not adjudicate it by symbol would be
leaving the repository with two answers to *"the same key was presented twice"*
and no document saying which.

The symbols, read at HEAD:

- `jobs.EnqueueOnce` / `jobs.EnqueueOnceIn` (`jobs/queue.go`), driven by a
  `ProducerIntent`, placing under `jobs.PlacementOnce` (`jobs/placement.go`) and
  answering **`jobs.EnqueueOnceOutcome`** — `EnqueueCreated` /
  `EnqueueExistingSamePayload` / `EnqueueConflict`, a rename of
  `jobs.PlacementOutcome`. *That* is the triple.
- `jobspg.Driver.placeExisting` (`jobs/jobspg/driver.go`), case
  `jobs.PlacementOnce`: `samePayloadDigest` decides
  `jobs.PlacementExistingSamePayload` against `jobs.PlacementConflict` — same key
  and same content is a repeat, different content is a conflict.
- reached through `findIntent` (`jobs/jobspg/repo_ops.go`, a `SELECT … FOR
  UPDATE`) and then `insertIntent` (`INSERT … ON CONFLICT … DO NOTHING`, with
  `rows != 1` becoming `errIntentConflict`).

**Two corrections to that comparison, because a decision that records a false
one is worse than one that records none.** First, `jobs.Unique` is **not** this
mechanism: it sets `jobs.PlacementUnique`, whose existing-key answer is
`jobs.PlacementExisting` with **no payload comparison at all** — "one live job
per key", not the triple. This decision names `EnqueueOnce` and not `Unique`.
Second, the losing path does not hand a caller `errIntentConflict`: both
`jobspg.Driver.Place` and `jobspg.TxStager.Stage` retry it up to three times, and
the retry's `findIntent` sees the winner's committed row and answers
`EnqueueExistingSamePayload`. Only three genuine conflicts in a row become
`jobs.RejectPlacement(jobs.ErrConflict)`.

**So the argument that a job cannot express a receipt is not the atomicity one.**
`jobspg.Driver.Place` **does** enlist in the caller's ambient transaction when one
is bound — `crud.ExecutorFor` → `crudsql.Transaction` → `TxStager.Stage` →
`placeInTx` — so a placement *can* be one commit with an append. The four reasons
that hold are these, and each is against the real API:

1. **A job is a row that will run.** `EnqueueOnce` creates a delivery a worker
   leases, retries and dead-letters; a receipt is a row nobody executes.
   Expressing one as the other needs a no-op handler and makes the invocation's
   lease, attempt and DLQ lifecycle the receipt's.
2. **A receipt must be readable later, by key, from another process — and `jobs`
   cannot be asked.** The intent key is `scope/revision/purpose/digest`, a
   **hash**; the preimage is never stored and `jobs` publishes no "what happened
   to intent K" read. `Resolve`'s `Unresolved` and `Expired` are unanswerable,
   which is the whole of ES-07's «зачем».
3. **A receipt records what the append produced.** `Held.Complete` writes `First`
   and `Last` onto the claimed row *after* the append, in the same transaction; a
   placement's payload digest is fixed at enqueue and there is no second write to
   the placed row.
4. **There is no horizon.** A swept `jobs` invocation is indistinguishable from
   one that never existed — exactly what `receipt.Ledger.Horizon` exists to
   prevent — and `jobs` publishes nothing to compare an `Issued` against.

Plus the boundary reason: `event/receipt`'s closure is `event`, `crud`, `errs`,
`utils` and the standard library, and importing `jobs` would be a new subsystem
edge from the event extension into another subsystem.

**The three-way verdict is convergent, not borrowed.** Both arrive at
created / existing-same-content / conflict because that is what a
content-addressed idempotency key can answer. The one place they differ is
deliberate: `receipt.Collided` travels as an **error** (`ErrCollision`) while
`EnqueueConflict` is a value, because `EnqueueOnce` *performs* the placement and
leaves nothing to ignore, whereas a claim's placement is the caller's next
statement — and a verdict is a value it is legal to discard.

**Which a consumer reaches for.** Reach for `receipt` when the question is *what
happened to this operation* and the answer must be readable later, by key, from
another process. Reach for `jobs.EnqueueOnce` when the question is *has this work
already been scheduled* and the answer is only needed at the moment of
scheduling. That sentence is on `docs/modules/{en,ru}/receipt.md` and on the
`jobs` page, so the repository does not hold two answers and no document saying
which.

**And `jobspg`'s losing path is a defect of `jobs` worth recording, though not
the one it first looks like.** The retry makes the READ COMMITTED case correct;
above it, `TxStager.Stage` retries on a transaction it does not own and that the
first attempt's `40001` has already aborted, so all three attempts fail and the
caller gets `jobs.ErrConflict` for a plain repeat. That is
`EVENTSOURCE_BACKLOG.md` `## P5` item 23, `[medium]`, measured — and it is named
here so the comparison this decision draws is against `jobs` as it actually is.

## What it forbids

- Do not make `Store.Append` deduplicate, and do not put an operation key,
  column, table or schema version into the store contract.
- Do not record the receipt after the append, and do not publish an open-coded
  order as the shape a reader copies while `Once` exists.
- Do not put the expected version into the fingerprint's preimage, and do not
  reorder, narrow, re-endian or tag that preimage: every key still inside a
  retention window reads as a collision if you do.
- Do not spell the claim as one statement, and do not swap the two statements.
  `SELECT`-first is a measured defect and `DO UPDATE` is a measured convoy.
- Do not choose an isolation level anywhere in this package ([[D-126]]), and do
  not retry a `40001` inside it — the unit is the caller's.
- Do not cover two appends with one operation key.
- Do not implement `Ledger` in this framework, and do not prune from it.

## Where it lives

- `event/receipt/claim.go` — `Claim`, `Once`, `Held.Complete`, `sameUnit`,
  `claimAnswered`, and the three verdicts.
- `event/receipt/receipt.go` — `Receipt` and `Ledger`, whose doc comment carries
  the two statements and their order.
- `event/receipt/key.go` · `event/receipt/fingerprint.go` — `Key`, `Key.Value`,
  `Fingerprint`, `ParseFingerprint`.
- `event/repo.go` — `Repo.Digest`, the first four of `Append`'s six steps and no
  store call.
- `_examples/event-receipts/main.go` — the reference `Ledger`, whose two claim
  statements `TestTheExampleLedgerIsTheOneTheLiveSuiteProved` compares with the
  live suite's byte for byte and in order.

## Proven by

- `TestADigestIsTheBytesThisAppendWouldWrite` and
  `TestTwoAttemptsAtDifferentVersionsDigestEqual` — the fingerprint covers the
  append and not the version, with a recording store asserting zero store calls.
- `TestTheDigestPreimageIsFrozen` — the six golden vectors that make the preimage
  a value rather than a description.
- `TestTwoCallersRaceOneKey` — three isolation levels, each with the answer that
  level actually gives: `Repeated` at READ COMMITTED, and a `40001` whose retry
  repeats at the other two, with `psql` row counts on every arm.
- `TestFourLedgerDefectsEachBreakTheCaseThatNamesThem` — the `select-first` claim
  breaks `TestTwoCallersRaceOneKey`, and three more defects break the cases that
  name them.
- `TestTheReferenceLedgerIsCertified` and
  `TestTheLedgerHarnessStillDetectsEveryDefectItWasBuiltToDetect`
  (`event/eventtest`) — the two statements and their order as a section a
  consumer can run over its own `Ledger`, and the eleven defects the seven
  sections are falsified against.
- `TestTheLiveLedgerSatisfiesTheContract` and
  `TestTheApplicationHarnessesCatchADefectiveImplementation` (`event/eventpg`,
  live) — the same seven sections over PostgreSQL, and `select-before-insert`,
  `echoes-the-fingerprint-it-was-handed`, `horizon-from-the-newest-row` and
  `find-on-the-claiming-connection` each reported by the section that names it.
- `TestEveryRefusalClaimHasIsReachableThroughOnce` — every refusal `Claim` has is
  reachable through `Once` with the same sentinel.
- `TestOneKeyTwoStreamsAndTheKeyPerAppendControl` — one key covers one append,
  with the key-per-append control.
- `TestACodecThatDoesNotEncodeTheSameBytesTwice` — the obligation fails closed.
- `TestTheExampleLedgerIsTheOneTheLiveSuiteProved` — the published reference is
  the one the live suite ran, both statements and their order.

## See also

[[D-118]] [[D-122]] [[D-125]] [[D-126]] [[D-143]] [[FL-043]] [[UC-037]]
[[UC-032]]
