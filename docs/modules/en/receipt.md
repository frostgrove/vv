# event/receipt — what happened to an operation nobody confirmed

```go
import "github.com/frostgrove/vv/event/receipt"
```

**Module:** in the root module. Its whole closure is `event`, `crud`, `errs`,
`utils` and the standard library, so it costs no dependency ([[D-033]]) ·
**Depends on:** [event](event.md) for the transaction authority and the stream ·
**Depended on by:** nothing

The command was issued, the connection died, and the caller is holding nothing
but the request identity it minted before it started. `receipt` is the durable
row that answers *did that operation happen, and what did it write* — written
beside the append, in the caller's own transaction, in the application's own
table.

It is not a store capability. No store gains a table, a column, a migration or a
schema version: `eventpg` stays at `SchemaVersion = 2`, a deployment that wants
no receipts deploys nothing, and every store — including one this repository
never saw — gets receipts the day its application writes a `Ledger`. [[D-142]] is
the whole argument.

**The word is not `event.Commit`'s.** A commit is the token an append hands back
inside the process that made it and it does not survive that process; a receipt
is a row in a table, under a key the caller chose, and it is the only thing a
second process can find.

---

## What you get

| | |
|---|---|
| `NewKey(raw)` | the caller's own identity for one operation. Renders `"[operation key]"` and never its value |
| `Key.Value()` | the one door the value comes back out of, and it is the `Ledger`'s statement's ([[D-142]]) |
| `NewFingerprint(digest)` · `ParseFingerprint(text)` | the two directions of `"sha256:"+hex`, so the rendering is frozen in the package that owns it |
| `Receipt` | the durable row: `Key`, `Fingerprint`, `Stream`, `First`, `Last`, `Complete`, `RecordedAt` |
| `Ledger` | the application's own table: `Transaction`, `Claim`, `Complete`, `Find`, `Horizon` |
| `Once(ctx, spec, work)` | claim, decide, append, complete — with the order owned by the call |
| `Claim(ctx, spec)` | the open-coded door, for a caller that must answer a repeat differently |
| `Held` | `Verdict()` · `Receipt()` · `Complete(ctx, commit)` |
| `Verdict` | `Recorded` · `Repeated` · `Collided` |
| `Resolve(ctx, spec)` | what happened, from another process |
| `Standing` | `Found` · `Incomplete` · `Unresolved` · `Expired` |
| `ErrSpec` · `ErrCollision` · `ErrIncomplete` · `ErrLedger` | the four sentinels |

### The shape a command takes

```go
key, err := receipt.NewKey(request.Header.Get("Idempotency-Key"))
if err != nil {
	return err
}

err = crud.InNewTx(ctx, source, func(ctx context.Context) error {
	state, at, err := repo.Load(ctx, id)
	if err != nil {
		return err
	}
	changes := decide(state, command)

	print, err := repo.Digest(at, changes...)     // the bytes this append would write
	if err != nil {
		return err
	}
	fingerprint, err := receipt.NewFingerprint(print)
	if err != nil {
		return err
	}

	held, err := receipt.Once(ctx, receipt.ClaimSpec{
		Ledger:      ledger,
		Store:       store,
		Key:         key,
		Fingerprint: fingerprint,
		Stream:      at.Stream(),
	}, func(ctx context.Context) (event.Commit, error) {
		_, commit, err := repo.Append(ctx, at, changes...)
		return commit, err
	})
	if err != nil {
		return err                                // ErrCollision and ErrIncomplete come back here
	}
	answer = held.Receipt()                       // Recorded: the range this attempt wrote
	return nil                                    // Repeated: the range the first attempt wrote
})
```

**Reach for `Once` first.** It runs the work only on `Recorded`, runs it once,
runs it inside the caller's transaction, and completes the claim in that same
transaction. A repeated key never reaches the work at all, so a retry costs one
statement and no append. `Claim` / `Append` / `Held.Complete` open-coded is for
the caller that must answer a repeat *differently* from a first write — every
refusal `Claim` has is reachable through `Once` and
`TestEveryRefusalClaimHasIsReachableThroughOnce` is what keeps that true.

---

## The order is claim, decide, append, complete

The claim is **first**, and the tempting order is the one that loses the only
case this exists for.

Recording after the append is safe against two writers at one expected version —
the store admits exactly one of those anyway. It is **not** safe against **two
retries at two different expected versions**, which is precisely what a caller
does when it does not know the outcome. The second attempt reloads, decides at
the new version, and is admitted because it is not a concurrency conflict. Both
append. The collision is then discovered with both sets of events already in the
log.

Nothing in this package can see an append that already happened on a context, so
the order is not enforced by inspection. `Once` is the spelling that makes it
unwritable in any other order, and it carries the obligation.

## The claim is two statements, and their order is the mechanism

```sql
INSERT INTO receipts (key, fingerprint, family, stream_key, recorded_at)
VALUES ($1, $2, $3, $4, statement_timestamp())
ON CONFLICT (key) DO NOTHING;

SELECT key, fingerprint, family, stream_key, first_version, last_version, complete, recorded_at
  FROM receipts WHERE key = $1;
```

In that order, in the caller's one transaction. `won` is the insert's
affected-row count and nothing else.

**The insert is what blocks.** PostgreSQL's speculative insertion makes `DO
NOTHING` wait on a conflicting uncommitted tuple and then insert or not on that
transaction's outcome — which is why a claim never has to answer "unresolved" and
a `Resolve` does.

**The read has to be a statement of its own**, because at READ COMMITTED a second
statement is a second snapshot: that is what lets a loser read the row the winner
committed while it was blocked. A single statement that tries to do both shares
one snapshot, taken before the winner committed, and answers the loser zero rows
— neither a win nor a repeat, and refused here with `ErrLedger`. A `SELECT`
placed **first** is worse: it sees nothing, its caller decides it is first and
appends, and the insert's zero arrives after the decision.

**Nothing here chooses an isolation level** ([[D-126]]). What the level decides is
*how a loser fails*, not whether it may append:

| Level | The loser | Who repeats |
|---|---|---|
| READ COMMITTED | blocks on the index, then reads the winner's row | this call, answering `Repeated` |
| REPEATABLE READ · SERIALIZABLE | blocks, then SQLSTATE `40001` — the caller's **whole** unit rolls back, claim and append and completion together | the caller's own retry, through `crud.InNewTx` |

`40001` is already retryable in this tree, so the retry has a home, and the retry
is the **caller's**: this package opens no transaction and retries nothing.

**What the two-statement form gives up, stated rather than discovered later:** a
window between the two statements in which a retention sweep could delete the row
the `SELECT` is about to read, which surfaces as `won == false` beside no receipt
and is refused with `ErrLedger`. A horizon shorter than the age of a row written
milliseconds ago is not a horizon, so that window is closed by retention rather
than by the statement.

`_examples/event-receipts` is the **reference implementation** of a `Ledger` —
these two statements, in this order, plus the completing `UPDATE`, the SQL-side
horizon and a sweep — and it is a reference because the live suite ran it and
`TestTheExampleLedgerIsTheOneTheLiveSuiteProved` compares both statements, in
order, byte for byte.

## The fingerprint covers the append, and deliberately not the version

`Repo.Digest` is SHA-256 over a length-prefixed encoding of the composed stream
and each record's type, revision and payload, in order. It issues no store call
and writes nothing, and it answers the same refusals `Append` would — so a caller
that digests first learns a malformed append before it claims anything.

**The version the token was loaded at is not in it.** A retry that arrives in a
new process loads what the store *now* holds, which is the version the first
attempt moved the stream to; a digest over the version could not be reproduced by
the one caller the mechanism exists for, so every repeat would answer `Collided`
for an operation nobody performed. `Append`'s own concurrency check is untouched
and is where the anchor belongs. The anchor is still readable: for a range that
exists, `First - 1` is the version the operation was decided at — **recorded, and
compared by nobody**.

**The preimage is a frozen format.** A fingerprint is read back by a later build
than the one that wrote it, and inside a retention window a deployment is
routine. Reordering the fields, narrowing a length, flipping the byte order or
adding a separation tag answers **every key still in the window** as a collision
on an operation nobody performed. `TestTheDigestPreimageIsFrozen` holds the
layout as six golden vectors rather than as a description.

### Three obligations this framework cannot check

They are stated here for the reason `projection.Sequencer`'s three are stated on
its own page: nothing at any door can see them, and each is silent when it is
broken.

1. **A decision must encode to the same bytes under one key.** A codec that
   records a clock, a fresh identifier or a map in iteration order encodes
   differently every time, and under one operation key that is a refusal on
   **every** retry rather than a duplicate append — `Collided`, loud, and never
   wrong. Take such a value from the command, the state or the operation key, the
   way a `Sequencer` takes none of them from a clock.
2. **One key covers one append, to one stream.** `Held.Complete` refuses another
   aggregate's commit, which closes the half it can see; what it cannot see is a
   caller that issues two appends to the *same* stream under one key. **The
   recipe is a key per append:** derive the second from the first with a suffix
   the caller owns — `NewKey(base + ":shipment")` — so each append has its own
   row and its own range.
3. **The `Ledger` is the application's and its four obligations are its own.**
   The claim is the two statements above in that order; the horizon is monotone
   and comes from the database's clock; `Find` sees committed rows only; and
   `Claim` and `Complete` run inside the caller's transaction while `Find` and
   `Horizon` run outside one. **Run `eventtest.RunLedger` against yours** — it is
   the published conformance harness for a `Ledger`, it reports seven sections in
   the same three words the store suite uses, and its `claim order` section is
   the only thing that will tell you the two statements are in the right order
   before a retry tells you in production. See [eventtest](eventtest.md).

## Three answers at the write door, and two of them are refusals

| `Verdict` | When | What the caller does |
|---|---|---|
| `Recorded` | no row existed and this claim took the key | run the work, append, complete in the same transaction |
| `Repeated` | a **complete** row exists and its fingerprint compares equal | answer the client from `Held.Receipt()`'s range; append nothing |
| `Collided` | a complete row exists and its fingerprint differs | **refuse** — the key was spent on another operation |

`Collided` travels as `ErrCollision` **and** as a verdict, and the error is the
part that matters: a verdict is a value it is legal to discard, and the worst
code that compiles — `held, err := Claim(...)`, `if err != nil { return err }`,
then `Append` — must refuse rather than spend a stranger's key. `Repeated` stays
ignorable, and is caught one call later: `Complete` refuses a verdict that is not
`Recorded`, and the caller's own transaction rolls the second copy of the events
back. **The transaction is the enforcement**, which is the only enforcement a
package that opens no transaction can honestly have.

**`ErrIncomplete` is not a fourth verdict.** A row whose claim committed without
its completion carries no verdict at all, because a state nothing may be
concluded from is a refusal and not an answer. A retry told *"already done"* out
of a row whose range is `0..0` has reported a success that may never have
happened — and `0..0` is also the honest range of a decision that yielded no
changes, which is why the discriminator is `Complete` and never the range.

## Four answers at the read door, from another process

```go
resolution, err := receipt.Resolve(ctx, receipt.ResolveSpec{
	Ledger: ledger,
	Store:  store,
	Key:    key,
	Issued: whenTheClientMintedIt,   // optional
})
```

| `Standing` | What it means | What a caller does |
|---|---|---|
| `Found` | present and complete | answer from `Resolution.Receipt`'s range |
| `Incomplete` | present with no range — a claim that committed without its append | a **defect report**: this is not a state to retry through |
| `Unresolved` | **absent, and nothing may be concluded** | report the operation as in flight and ask again later |
| `Expired` | absent, and older than this ledger answers for | the row may have existed and been swept |

**An absent receipt is not a rollback** ([[D-143]]). A resolver on a second
connection sees no row for two different reasons — the writing transaction rolled
back, or it is **still open** — and PostgreSQL gives it nothing to tell them
apart: there is no row to lock, so a share lock blocks on nothing. `Unresolved`
is that state named rather than guessed at.

What bounds it is `idle_in_transaction_session_timeout` on the application role,
which is the **deployment's** lever. This package adds no second timeout of its
own.

**What a caller does with `Unresolved`: it does not re-issue the command.** A
fresh load and a fresh decision at the new version is a *different* operation
under this key, and nothing would catch the second write. It reports the
operation as in flight and resolves again once that timeout has elapsed.

`Resolve` refuses inside the writing transaction, of either handle: a resolve on
the claiming transaction's own context reads its own uncommitted row and answers
`Found` for an operation that can still roll back — and *"resolve first, then
decide, in one unit"* is an ordinary retry shape somebody writes.

## Retention, and the number to start from

`Ledger.Horizon` is **an instant at or before the oldest row this ledger still
answers for**, on the database's own clock: `MIN(recorded_at)`, or
`now() - retention` computed in SQL for a table that may be empty. Never a clock
read in the ledger's process.

The asymmetry is deliberate. Answering an instant **older** than the truth is
conformant and costs a caller one more `Unresolved`; answering a **newer** one is
not, because it reads a row that was swept as one that never existed. A ledger
that answers the zero instant claims to hold everything for ever, which is what a
table with no sweep is.

**How long to keep rows: retention must exceed the longest window in which a
client may present the same key again.** For an HTTP idempotency key that window
is conventionally **24 hours**, which is where a deployment with browser clients
starts. A deployment whose retries are a workflow's rather than a browser's sets
it to that workflow's own timeout instead — the number is a property of the
caller, not of this package. **The framework prunes nothing**; the sweep is the
application's, and `_examples/event-receipts` runs one as a `jobs` periodic.

## Which clock each instant comes from

| Instant | Clock |
|---|---|
| `Receipt.RecordedAt` | the ledger's database — `statement_timestamp()`, never a bound parameter |
| `Ledger.Horizon` | the ledger's database, in SQL |
| `ResolveSpec.Issued` | the **caller's** own, and it cannot be anything else |

So exactly one cross-clock comparison exists — `Issued` against `Horizon` — and
its error term is the skew between the resolving host and the database. It
decides only between two **non**-conclusions: a caller clock behind the
database's answers `Expired` for an operation in flight, and one ahead answers
`Unresolved` for a row that was swept. Neither is a false `Found` and neither is
a false *"it did not happen"*.

`Issued` is **optional**, and the zero instant gets `Unresolved` rather than a
refusal: an HTTP handler holding a retried idempotency key has the key and not
the instant, and a refusal there sends it to `time.Now()`, which is after every
horizon and disables `Expired` for ever with no error anywhere. Absent,
`Resolution.Horizon` carries the ledger's instant so the caller can make the
comparison with a date it does have.

## `receipt` or `jobs.EnqueueOnce`?

The repository holds two mechanisms that answer *"the same key was presented
twice"* with created / existing-same-content / conflict, and they converged on
that triple rather than one borrowing it from the other — it is what a
content-addressed idempotency key can answer.

**Reach for `receipt`** when the question is *what happened to this operation*
and the answer must be readable later, **by key, from another process**.
**Reach for `jobs.EnqueueOnce`** ([[FL-035]]) when the question is *has this work
already been scheduled* and the answer is only needed at the moment of
scheduling.

Three differences decide it: a job is a row that **will run**, with a lease, an
attempt count and a dead-letter lifecycle, where a receipt is a row nobody
executes; a job's intent key is a **hash** whose preimage is never stored and
which nothing can be asked about afterwards; and a receipt records what the
append **produced**, written onto the row after the append in the same
transaction. [[D-142]] adjudicates the comparison by symbol.

## What this is not

- **Not a deduplicating `Append`.** `Store.Append` is unchanged and unchanging:
  putting the key into the store contract would cost every store a column, a
  migration, a schema version and a conformance section for a capability a
  deployment may not want.
- **Not a replay of the domain decision.** On `Repeated` the caller answers from
  the recorded range; nothing here re-proposes the same facts at whatever version
  the store now holds.
- **Not a proof of rollback.** There is no fourth standing that says the
  operation definitely did not happen; the `pg_current_xact_id` /
  `pg_snapshot_xmin` version of one was designed and refused, with its cost, in
  [[D-143]].
- **Not a second durable-intent table.** A receipt is a durable write inside the
  caller's own transaction, which is this framework's only outbox shape
  ([[D-118]]).
- **Not a sweep.** Nothing here prunes.
- **Nothing continuous.** This package opens no transaction, starts no goroutine,
  reads no clock and writes no log line, span or metric.

## See also

- [event](event.md) — `Repo.Digest`, the transaction authority and the stream
- [eventpg](eventpg.md) — the store a receipt usually sits beside, and the stall
  that keeps one unresolved
- [projection](projection.md) — `Wait`, the other half of the same request path
- [`_examples/event-receipts`](../../../_examples/event-receipts/) — the
  reference `Ledger`, the sweep, and both refusals branched on
- [[D-033]] · [[D-118]] · [[D-122]] · [[D-125]] · [[D-126]] · [[D-142]] ·
  [[D-143]] · [[FL-043]] · [[UC-037]]
