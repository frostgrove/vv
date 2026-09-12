# event/eventpg — the event store on PostgreSQL

```go
import "github.com/frostgrove/vv/event/eventpg"
```

```bash
go get github.com/frostgrove/vv/event/eventpg
```

**Module:** its own — its live fixtures need a driver, and one satellite is one
dependency decision ([[D-033]], [[D-051]]) · **Depends on:** [event](event.md),
[crud](crud.md), [crudsql](crudsql.md), [errs](errs.md) · **Depended on by:**
nothing

`eventpg` is a complete `event.Store` over PostgreSQL 14+. Expected-version
admission is a conditional row update PostgreSQL evaluates, not a version read
into Go; one append is **one statement**, so the version advance and the event
rows commit or roll back together on the pool exactly as they do inside the
caller's transaction; history is append-only in the database rather than by this
package containing no `UPDATE`; and the log walk hands over a **settled**
watermark, never the newest position.

It runs [`eventtest`](eventtest.md) unchanged, twice — at the defaults and at
narrow limits — and nineteen of its twenty sections report *passed*. The
twentieth is `monotone visibility`, which this store does not claim. That census
is asserted section by section rather than read off the log, because a section
`eventtest` could not certify is reported without failing the run.

---

## What you get

| | |
|---|---|
| `New(Spec)` | a `*Store`. Performs no I/O, starts nothing, reads no environment |
| `Store.Prepare(ctx)` | migrate (only under `ManageSchema`), then verify. Until it returns nil every operation refuses |
| `Store.Migrate(ctx)` · `Store.Verify(ctx)` | the deployment's two halves, separately runnable. `Migrate` refuses under `VerifySchema` |
| `Store.Check(ctx)` | a readiness answer — `health.Probe` structurally, with no import of `health` and no importance of its own ([[D-091]]) |
| `Store.Schema()` · `Store.SchemaManagement()` | what this value was built with |
| `MigrationStatements(Schema)` | the operator-managed path: thirteen statements, in order, all transactional DDL. One list builds schema version 2 and migrates a deployed version 1 at these bounds into it |
| `Schema.Resolved()` · `Schema.Fingerprint()` | the numbers a deployment is about to write, and the digest an incident compares — both without constructing a store |
| `NewCheckpoints(CheckpointSpec)` | a `*Checkpoints` over the same schema: the `event.Checkpoints` contract on the fourth table |
| `Checkpoints.Prepare(ctx)` · `Checkpoints.Check(ctx)` | the same two halves, for a resource of its own |

```go
store, err := eventpg.New(eventpg.Spec{
	DB:               pool,
	Source:           crudsql.Postgres(pool),
	Schema:           eventpg.Schema{Name: "frostgrove_events"},
	SchemaManagement: eventpg.ManageSchema, // development; production says nothing
})
if err := store.Prepare(ctx); err != nil {
	return err
}

repo, err := event.Bind(event.Open(store), Account)
```

`Spec.Source` is **required**. A store that cannot see the caller's transaction
writes beside the caller's work instead of inside it, which is a trap rather than
a degraded configuration, and `New` refuses a `Source` that is not the same data
source as `DB`.

Every number is optional and every number is checked. `Schema.MaxPayload` and
`Schema.MaxKey` are `CHECK` operands and fingerprint inputs, so they live on the
schema; `Spec.MaxBatch`, `Spec.StreamPage` and `Spec.MaxRead` touch no row and
live on the spec. A zero takes the default, a negative is refused, and so is
anything above the framework's ceiling.

## Any `database/sql` driver for PostgreSQL

The store binds only `string`, `int32`, `int64`, `bool`, `time.Time` and
`[]byte`, and scans only those — never a Go slice as a PostgreSQL array, which is
a driver extension `database/sql`'s own converter refuses. `xid8` is read
`::text` and parsed, because it is not a type the scan contract covers and the
two mainstream drivers hand it back differently. So `pgx/v5/stdlib`, `lib/pq` and
anything else that speaks `database/sql` all compose, and a consumer already on
`ent`, `gorm`, `sqlx`, `sqlc` or `bun` hands over the pool it already has. The
module requires `pgx/v5` for its own live fixtures and imports it in no non-test
file — the same one version `jobspg` already carries.

## Schema management is a deployment-profile choice

Saying nothing is `VerifySchema`: the process verifies and creates nothing.
`ManageSchema` migrates, under a session advisory lock held on **one pinned
connection** so two replicas starting together take one lock on one backend
rather than racing into a `23505` on `pg_type_typname_nsp_index`.

Verification is three levels and fails closed at the first: the version integer,
then the fingerprint byte for byte, then the catalog itself — relation kind and
storage, columns and their defaults and identity, primary keys, unique
constraints and foreign keys compared *structurally*, check constraints compared
token by token, the identity sequence's `INCREMENT`/`CACHE`/`CYCLE`, the exact
set of triggers **and what their functions do**, and the absence of rules and
policies. Extra indexes and extra constraints are exempt, because each fails
loudly at write time. `Check` runs levels 1 and 2 plus the log identity, which is
the one drift a fingerprint cannot see: a schema dropped and migrated again under
a running store fingerprints identically and every cursor that store minted is
foreign.

[`MIGRATIONS.md`](https://github.com/frostgrove/vv/blob/main/event/eventpg/MIGRATIONS.md)
carries the operator's own path, the thirteen statements and what each is for, and
the remedy for a row the read refuses.

## The checkpoints table, and schema version 2

A projection's checkpoint row is the fourth table of the same schema, and
`Checkpoints` is a **resource of its own**: a `Store` and a `Checkpoints` over
one schema are two resources at one schema version, so a deployment migrates once
and each verifies at its own `Prepare`.

```go
checkpoints, err := eventpg.NewCheckpoints(eventpg.CheckpointSpec{
	DB:               pool,
	Source:           crudsql.Postgres(pool),
	Schema:           eventpg.Schema{Name: "frostgrove_events"},
	SchemaManagement: eventpg.ManageSchema,
})
if err := checkpoints.Prepare(ctx); err != nil {
	return err
}
```

| Column | Type |
|---|---|
| `projection` | `text NOT NULL PRIMARY KEY`, bounded by an `octet_length` check at the kernel's identifier bound — 128 bytes, the same one a family and a wire type name are held to |
| `cursor` | `bytea NOT NULL`, bounded **below as well as above**: `1 … 4096` |
| `advance` | `bigint NOT NULL CHECK (advance > 0)` — the fence |
| `highest` · `applied` · `quarantined` | `bigint NOT NULL`, each non-negative |
| `updated_at` | `timestamptz NOT NULL`, **bound** rather than taken from `statement_timestamp()` |

`cursor` is `bytea` for the reason `payload` is: it holds bytes the kernel does
not constrain, and a cursor carrying a NUL or an invalid UTF-8 byte is two server
errors in a `text` column — reproduced on 17.9. It is bounded below because the
empty cursor is the origin of a log, so a row carrying one at a live advance is a
readable checkpoint that restarts a consumer at the beginning. And `updated_at`
is bound because `Progress.At` is the *consumer's* observation and must
round-trip, where the append's instant is a property of the write and belongs to
the database.

**The save is two statements and exactly one is issued.** Above advance 1 it is
`UPDATE … WHERE projection = $1 AND advance = $3 - 1`, which moves a row and
matches nothing where the row has gone; at advance 1 it is
`INSERT … ON CONFLICT (projection) DO NOTHING`, which creates one and never
overwrites one. A single `INSERT … ON CONFLICT` would ask whether the row is
there against its own snapshot and which row it collides with against the live
index, so a `DELETE` committing between those two moments would bring a retired
row back at an advance no first save ever created. Both halves keep every
property the append has: one statement, admission a predicate PostgreSQL
evaluates against a row it has locked, and a lost race as a row count of zero
rather than an error.

**Schema version 2 is what adds the table**, and one migration list does both
jobs: it builds a fresh version-2 schema and transforms a deployed version 1 into
it. The stamping `UPDATE` is guarded on the version **and** on the version-1
fingerprint at these bounds, so a schema another expectation built is refused
rather than restamped, and a final statement asserts what the two wrote and
raises SQLSTATE `EVPG1` on a mismatch. Every deployment migrates, whether or not
it ever runs a projection — the table is part of the schema this build expects,
and verification fails closed against a schema that lacks it.

## The append is one statement

```
WITH admitted AS (INSERT INTO streams … ON CONFLICT DO UPDATE … WHERE s.version = $expected RETURNING version)
INSERT INTO events … SELECT … FROM admitted, (VALUES …) AS record(…) ORDER BY record.ord
```

A lost race is a **row count of zero**, never a unique violation, so a conflict
never depends on catching an error. A fresh stream takes the speculative
insertion, so two writers at version 0 do not race into a violation either: the
second blocks, takes the `DO UPDATE` path, fails the predicate and writes
nothing. `recorded_at` is `statement_timestamp()` — one instant for the whole
batch, from the database rather than from a process, which is why there is no
`Spec.Clock`.

The store **selects no isolation level and opens, commits and rolls back
nothing**. It is correct at `READ COMMITTED`, `REPEATABLE READ` and
`SERIALIZABLE`; what differs is what the loser of a race is told. At
`READ COMMITTED` a lost race is `ErrConflict`; at the two higher levels
PostgreSQL raises `40001` first, which this store classifies as a backend failure
with a **retryable** cause and never as a conflict ([[D-126]]).

## Uncertainty is never resolved by guessing

Every statement is issued on a `*sql.Tx` the caller bound or on a `*sql.Conn`
this call checked out — never on the `*sql.DB`, whose `ExecContext` retries a
`driver.ErrBadConn` on up to three connections and would execute one append three
times on exactly the path where the outcome is uncertain.

`NotWritten` is answered only on proof: the operation was inside the caller's
transaction, or nothing was issued at all, or the driver reported
`driver.ErrBadConn`, or a SQLSTATE outside class `08` and the three
session-termination codes came back — which means a backend processed the
statement, aborted it and survived to say so. **Everything else is
`Unconfirmed`**, which the kernel renders as `ErrUncertain`. There is no default
branch that guesses `NotWritten`.

## The log walk hands over a settled watermark

`position` comes from an identity column, so a value is drawn when a row is
inserted and not when it commits: positions have gaps a rollback burnt, and a
writer holding a low one can still be running while a higher one has committed. A
store answering `position > cursor` would deliver the higher one and never the
lower, and nothing downstream could see that it had.

So a cursor is three numbers — how far the walk has got, a transaction id minted
after every position up to a reach had been drawn, and that reach. A gap is
passed only once the cluster's floor has moved past that id, which means every
transaction that could have drawn a position below it has finished. A burnt gap
is passed inside one `ReadAll`; a live one stops the walk until the writer
finishes.

**Foreign writers are covered, and one shape is not.** The walk covers every
writer that lets the identity column draw the position — psql, a migration
script, a different service — because the `events_position_needs_xid` statement
trigger gives the writing transaction an id *before* the identity default is
drawn. It does **not** cover a writer that draws a position with `nextval` in one
transaction, lets that transaction end, and inserts it later with
`OVERRIDING SYSTEM VALUE`; that row is skipped. Drawn and inserted inside one
transaction it is covered.

**Draining the log belongs outside the write.** A walk on a context carrying a
transaction of this backing stops at the first gap it meets and does not pass it,
however many times it is called: it never mints the bound a gap is settled
against, because on the caller's own transaction such a bound settles nothing —
the floor a snapshot of that transaction reports never passes an id the
transaction itself holds — and minting it on a second connection while the caller
holds one is how a pool at its limit deadlocks. At `REPEATABLE READ` the
snapshot is frozen and the walk could not settle even in principle. A cursor
persisted from such a walk is also past events the transaction may still roll
back.

**The floor this walk waits on is the whole database's, not this schema's.**
`pg_snapshot_xmin(pg_current_snapshot())` is the oldest running transaction id in
the **cluster**, so any session anywhere that has written something and then gone
idle in transaction — an unrelated slow report, a leaked connection, a paused
debugger — holds that floor down and the walk cannot pass a burnt gap until that
session ends. Nothing is lost: every event is still there and every one of them
is delivered once the floor moves. **Delivery freezes, and every projection over
this schema freezes with it**, because they all wait on the same number.

It is the price of a gap-free read that blocks no writer, and it has three
mitigations, none of which is this library's to apply:

- **`idle_in_transaction_session_timeout`** on the application role, so a session
  that opened a transaction and stopped is terminated rather than left holding
  the floor. This is the one that matters; without it a single leaked connection
  is an unbounded stall.
- **`statement_timeout`** on the same role, which bounds the other shape — a
  transaction that is not idle but is running one very long statement.
- **A monitored lag metric.** `Progress.At` and `Progress.Highest` on the
  checkpoint row are the two numbers to alert on: a `Highest` that stops moving
  while the log grows is this stall and is indistinguishable from a halted
  projection at a glance, which is why the alert is on the pair rather than on
  either.

**This one stall is two symptoms on the request path, and they are both the same
number.** A `projection.Wait` over this schema polls a checkpoint row whose
`Highest` has stopped moving, so it burns its whole deadline and answers
`ErrNotVisible` — the *slow* answer, not the *stopped* one, with `Moved` false
across every poll. And a `receipt.Resolve` for an operation whose writing
transaction is the very session holding the floor down answers `Unresolved`, for
exactly as long, for exactly the same reason: `idle_in_transaction_session_timeout`
is the lever under both, and it is the deployment's rather than this library's
([[D-143]]). An operator seeing waits time out and receipts stay unresolved at the
same moment is looking at one idle session, not at two subsystems.

**N partitions x M generations multiply both the read traffic and this
exposure.** Each runner is a separate walk of the whole log with a settlement
bound of its own, so two generations at four partitions each is eight walks and
eight times a single projection's reads — measured, at exactly 8.0x
(`TestEightWalksCostEightTimesOneProjectionsReads`). The multiplication that
matters more is this section's: every one of those walks waits on the **same**
cluster-wide floor, so one session idle in transaction anywhere stalls all eight
at once, and a rebuild is precisely when a deployment has the most of them
running. Size the pool for the runners you start, and set
`idle_in_transaction_session_timeout` before you start a rebuild rather than
after.

The reference implementation this store was adjudicated against names the same
drawback and offers no mitigation for it. There is one documented alternative —
reading up to `pg_sequence_last_value` behind
`LOCK … IN SHARE ROW EXCLUSIVE MODE` taken in its own transaction — which bounds
delivery by the **slowest writer** rather than by the oldest transaction anywhere,
at the cost of blocking every write to the events table once per poll. It is
recorded rather than implemented.

**A new projection reads the whole log.** Adding one to a live deployment replays
every event ever written through its handler: `Checkpoint.Fresh()` is what says
the row is absent, and absence is the origin. It is paged by `MaxRead` and
resumable, so it is bounded work rather than one enormous transaction — but it is
the log's whole length of it, and it competes with live traffic for the same
pool.

## What a replay costs, measured

Measured on PostgreSQL 17.9 at the deployed defaults (`StreamPage` 256, `MaxRead`
256), 120-byte payloads, five passes after a warm-up, `-race` off, one
connection, ~0.1 ms round trip:

| Aggregate | Full replay, no-op codec | Full replay, `event.JSON` | Raw one-statement scan |
|---|---|---|---|
| 10 000 events (40 pages) | 10.19 – 10.93 ms | 16.25 – 17.72 ms | 5.67 – 7.05 ms |
| 100 000 events (391 pages) | 104.4 – 105.7 ms | 163.4 – 170.6 ms | 86.5 – 87.9 ms |

**Paging is a sixth of the replay, not its bulk.** The single-statement scan is
83 % of the paged replay at 100 000 events — 87.3 ms against 104.9 ms — so the
391 pages cost about **17.6 ms**, roughly 45 µs a page. That is the number a
deployment multiplies: on a 1 ms link the same term is about 390 ms. The raw scan
builds no envelopes and runs no fold, which accounts for part of the gap.

`BenchmarkStreamReplay` is the instrument, and it takes the stream size as a
parameter so a deployment measures its own environment rather than reading this
table. **There is no snapshot**, and the recorded trigger for building one is a
measured p99 aggregate replay above ~50 ms in a deployment's own environment
([[D-132]]).

## What it costs in transaction ids

The xid-first trigger fires once per statement whether or not the statement
produces rows, so a **contended** append — one that lost and wrote nothing —
consumes a transaction id too. It is not a correctness problem: the id belongs to
a transaction that ends immediately and settles the moment it does, which is the
watermark working. Beside it, a walk that meets a gap it has no useful
outstanding bound for mints one id, and only then.

## What it says about itself

| Capability | Answer |
|---|---|
| `Transactions` | `Supported` — `Spec.Source` is required, so this is unconditional |
| `Persistence` | `Supported` |
| `MonotoneVisibility` | `Unsupported` — a walk may stop short of an append that has already returned, which is the settled watermark working |
| `SharedBacking` | `Supported` — the backing is the pool **and** the schema together, so two schemas of one database are two backings and a cursor does not cross between them |

`Close` is idempotent, returns nil every time, closes no `*sql.DB` and resolves
no transaction: the pool was opened by the composition root and is shared with
every other store, repository and subsystem over it.

Nothing here starts a goroutine, registers a `runtime.Runner`, reads an
environment variable or writes to a logger ([[D-092]]).

## What is deliberately absent

| Not exported | Why |
|---|---|
| `Open(ctx, db, …)` | an event store's schema is history; `New` + `Prepare` is the only spelling, so nobody gets a migrating store by reaching for the short one |
| `Tail(ctx)` | a cursor at the end of the log is sound only if every position below it is settled, and no O(1) reading proves that |
| `Spec.Clock` | `recorded_at` is `statement_timestamp()`; a clock the store then ignored would be a lie |
| `Spec.TxOptions` | none of the eight methods begins a transaction |
| a retry, backoff or circuit breaker | [[D-040]] |
| a cursor, position or outcome type of its own | the kernel owns all three |

## See also

- [event](event.md) — the vocabulary, the seam and the refusals
- [eventmemory](eventmemory.md) — the same contract with no database
- [projection](projection.md) — the consumer that records through the fourth
  table
- [eventtest](eventtest.md) — the suite this store is certified by
- [`MIGRATIONS.md`](https://github.com/frostgrove/vv/blob/main/event/eventpg/MIGRATIONS.md)
  — the operator's path, the thirteen statements and the v1→v2 row
- [[D-091]] · [[D-101]] · [[D-118]] · [[D-121]] · [[D-126]] · [[D-127]] ·
  [[D-128]] · [[D-132]] · [[D-133]] · [[FL-036]] · [[FL-037]] · [[FL-038]] ·
  [[UC-032]]
