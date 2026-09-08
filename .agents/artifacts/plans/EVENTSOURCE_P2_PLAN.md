# EVENTSOURCE PHASE 2 — `event/eventpg` — IMPLEMENTATION PLAN

**Status:** plan, phase 2 of the PostgreSQL event-sourcing roadmap.
**Written against:** [`EVENTSOURCE_P2_USECASES.md`](../usecases/EVENTSOURCE_P2_USECASES.md)
(UC-069…UC-098, INV-046…INV-065) and its
[GAPS file](../gaps/EVENTSOURCE_P2_USECASES_GAPS.md) (all seven closed); the
frozen kernel under `event/`; `jobs/jobspg/` as the accepted precedent;
`scripts/checks.sh`; PostgreSQL **17.9**, measured.
**Format precedent:** [`EVENTSOURCE_P1_PLAN.md`](EVENTSOURCE_P1_PLAN.md).

**Delivery policy in force (2026-09-08).** Only `[critical]` and `[high]` block.
`[medium]` and `[low]` go to [`EVENTSOURCE_BACKLOG.md`](../gaps/EVENTSOURCE_BACKLOG.md)
under `## P2` and are left alone. **A gate never proceeds silently red:** if a
critical or high survives its fix round, the section's report says so in words,
and `go test` being green is never the report.

Two obligations frame everything below and both are executable, not promised:

- **`eventpg` runs `event/eventtest` unchanged** — S5, and it is phase 2's
  central evidence.
- **Phase 2 writes zero diffs under `event/` outside `event/eventpg`** —
  §INV-061, made a `make check` arm in S5 and specified in
  [## The zero-diff proof](#the-zero-diff-proof).

---

## What this plan delivers, and what it does not

One module, `github.com/frostgrove/vv/event/eventpg`, one package, eleven
non-test source files, `MIGRATIONS.md`, one flow, two module pages in two
languages, two decision records, one new `make check` arm and one live gate that
fails rather than skips.

It does **not** deliver `eventpgfx`, a projector, an outbox, a snapshot table, a
retention sweeper, a `pgx`-native path, a `Tail(ctx)` on the public surface, or
`Spec.Clock`. Each refusal is §1's and is not re-argued here.

**The driver decision, named because somebody will ask.** `eventpg` requires
`github.com/jackc/pgx/v5` in its `go.mod` and imports it in **no** non-test file:
`_ "github.com/jackc/pgx/v5/stdlib"` appears only in `_test.go` files, exactly as
`jobs/jobspg/integration_test.go:19` does it. That is one satellite, one
dependency decision ([[D-051]]), and it is the *same* decision `jobspg` already
made rather than a second one — the repository carries **one** PostgreSQL driver
at **one** version, `v5.10.0`, and `event/eventpg/go.mod` names that version
because a second version in one workspace is the question [[D-051]] exists to
stop. Production code speaks `database/sql`, `crud`, `crud/adapter/crudsql`,
`crud/sqlfault`, `errs` and `errs/sqlerr` and nothing else, which is what makes
the store composable with a consumer already on `ent`, `gorm`, `sqlx`, `sqlc` or
`bun`. §1's promise that *any* `database/sql` PostgreSQL driver works is kept by
construction and checked by a source check (S1), not by a sentence: **every bound
parameter is a `string`, `int32`, `int64`, `bool`, `time.Time` or `[]byte`, and
every scanned column is one of those** — `xid8` is read `::text` and parsed,
because `xid8` is not a type `database/sql`'s scan contract covers and the two
mainstream drivers hand it back differently.

---

## What was measured live before this plan was written

Four claims the whole design rests on were driven against
`postgres://vv:vv@localhost:55432/vv` (PostgreSQL 17.9) **before** any contract
below was fixed. A plan that assumed them and was wrong would have to be rewritten
after the code; these are the transcripts.

| Claim | Where it is load-bearing | Measured |
|---|---|---|
| A statement-level `BEFORE INSERT` trigger calling `pg_current_xact_id()` assigns the transaction id **before** the identity default is drawn | §2.6 premise 2, §INV-065, §UC-098 | A row trigger reading `pg_current_xact_id_if_assigned()` at row-formation time reported **56541** with the statement trigger present and **null** with it dropped — while `position` was already **1** and **2** respectively. The premise and its control, with no timing window |
| The one-statement append admits at the expected version, writes nothing at a stale one, and advances at the fresh one | §2.3, §INV-046, §INV-047, §UC-076, §UC-077 | `INSERT 0 2` at `Expected = 0` (stream version 2, positions 1 and 2 in caller order); `INSERT 0 0` at a stale `Expected = 0` with the stream unmoved and no rows; `INSERT 0 1` at `Expected = 2`. A lost race is **zero rows**, never a unique violation |
| `LEFT JOIN LATERAL` carries a horizon on an empty page, and `pg_snapshot_xmin(pg_current_snapshot())::text` scans as a string | §2.6, `ReadAll` | Non-empty side returned `56550 \| 1 \| credited`; empty side returned `56550 \| NULL` |
| The append-only triggers refuse all three verbs and let `INSERT` through | §INV-048, §UC-096 | `UPDATE`, `DELETE` and `TRUNCATE` each raised `eventpg: <verb> on an event row: history is append-only`; the `INSERT` beside them succeeded |

**And one thing the spec does not say, found by the same measurement.** The
statement trigger fires once per statement *whether or not the statement produces
rows*, so a **losing** append also consumes a transaction id — measured
`xid_after_zero_row_append=56555` on an `INSERT 0 0`. It is not a correctness
problem (the xid belongs to a transaction that ends immediately, and it settles the
moment it does, which is the watermark working) and there is no fix that keeps
premise 2, so it is recorded rather than solved: **backlog `## P2` §13, `[low]`.**
§2.6's "a bound costs one transaction id, and only when a gap is seen" stays true;
what changed is that a *contended append* costs one too, and the module page says so.

`decode(replace(gen_random_uuid()::text, '-', ''), 'hex')` returns 16 bytes, which
is how the log id is minted with no `pgcrypto` and a deterministic statement text.

---

## What the round-1 gate measured, and the one thing it found that nothing had written down

Four of the seven `[high]` findings in
[`EVENTSOURCE_P2_PLAN_GAPS.md`](../gaps/EVENTSOURCE_P2_PLAN_GAPS.md) turn on how the
Go toolchain and `event/eventtest` actually behave rather than on how they read.
Those behaviours were driven rather than argued, and the transcripts are here
because the fixes below are only as good as them.

| Claim | Driven | Result |
|---|---|---|
| `go test -list` runs `TestMain` before `m.Run()` handles the flag | a throwaway module whose `TestMain` exits 1 on a bad DSN | `-list` printed **`FAIL` and no test names at all**, so `grep -c '^Test'` is `0` and every counting arm aborts (GAP-P2-4) |
| `db.Conn(ctx)` on a `*sql.DB` the test closed | same module | `sql: database is closed` — `database/sql`'s own unexported `errDBClosed`, **not** `sql.ErrConnDone`. Either way the checkout failed and **nothing was issued**, which is proof 1 (GAP-P2-2) |
| `scripts/checks_test.go:fixture` copies the scripts into a `t.TempDir()` and `runCheck` runs the copy | read, `scripts/common.sh:3-4` | `REPO_ROOT` is the fixture. A bare `EVENT_KERNEL_BASELINE=` assignment travels into a repository where that commit does not exist (GAP-P2-5) |
| `crudsql`'s savepoint answers its parent's `*sql.Tx` | read, `crud/adapter/crudsql/crudsql.go:232` and `event/authority.go:36` | `savepoint.Tx()` is `this.parent.tx` and `Authority.Same` compares that pointer, so the savepoint test §6.12 asks for is three assertions (GAP-P2-6) |

**And the thing nothing had written down, which the gate did not find either.**
`eventtest.Run` was driven against a factory of the shape this plan proposed — a
`New` answering a fresh store value over a backing that already holds what an
earlier value wrote — using `eventmemory` over **one shared log**:

```
eventtest: store failure classification: failed — [outcome not written] injected at the
append door through a decorator that wraps the store's own error left 2 events on a
stream that held one, and the store said the write certainly did not land
```

`storeFailureSection` runs each of its eight cases twice — once through the store,
once through a wrapping decorator — and each invocation calls `Factory.New`,
loads `account("a")` and appends one event to it before injecting
(`event/eventtest/sections_lifecycle.go:283-307,311-317,334-336`, `probe.go:71,136`). The
`NotWritten` case then asserts the stream holds **exactly one** event. On a
backing that survives `New`, the second invocation finds two. So:

> **A factory whose `New` answers a store over a backing an earlier `New`'s store
> already wrote to cannot pass `store failure classification`.** `eventmemory`
> passes it only because its `New` builds a fresh log — which the `Factory.New`
> doc comment (`suite.go:36-40`) forbids in as many words.

That is a defect in `event/eventtest`, it is **frozen by §INV-061**, and it is
reported rather than patched: **backlog `## P2` §25, `[high]`, owner the phase
that unfreezes the kernel.** What phase 2 does about it is D1's `New` below, and
the escape is real rather than a dodge: `durability` and `shared backing` are
satisfied through `Factory.Sibling`, not through `New`
(`sections_lifecycle.go:247-249,104-112`), so a `New` that answers a fresh
backing each time keeps both — and *certifies two clauses a shared backing loses*,
`resumption`'s foreign cursor and `transactions`' chained pair, both of which ask
`this.store()` for a **second** backing (`sections_resumption.go:66`,
`sections_transactions.go:197`). Both configurations were run: over one shared log
those two report *not certified*, and `eventmemory`'s own conformance run — whose
`New` builds a fresh log — reports both *passed*.

---

## The seven questions §8 left to the plan, decided

Each is decided here so it is not decided by whoever writes the line.

### D1 — every store the factory builds owns its schema, and `Factory.Fail` is driven from real failures for all seven cases  (§8.1)

**`New` migrates a scratch schema of its own, per call.** A run-unique prefix, a
counter, `Migrate`, `Prepare`, and a `t.Cleanup` that closes the pool and runs
`DROP SCHEMA … CASCADE` — which the append-only triggers do not stop, because they
fire on `UPDATE`, `DELETE` and `TRUNCATE` and a schema drop is none of the three.
Each store value gets its own `*sql.DB` and its own
`crud.Source` over it, so two values `New` built are two data sources and a
transaction bound for one is not found for the other — which is what
`transactions`' chained pair asks for. *(Amended by S5, 2026-09-08:
`SetMaxOpenConns(6)`, not 2 — the `crossed` case holds three live transactions of
one store and reads on the pool beside them, and a pool of two blocks on the
third `Begin` until the section window expires. S5 change 1.)*

`Sibling` is the opposite and deliberately so: a **second `*eventpg.Store` value
over the first store's own `*sql.DB` and schema**, so `Backing().Equal` holds. That
is the split the suite is actually written against — `durability` and the
`persistence` clause take their second value from `Sibling` and fall back to `New`
only when no `Sibling` exists (`sections_lifecycle.go:104-112,247-249`) — and it is
what makes the measured `store failure classification` failure above impossible:
no store value the suite is handed by `New` ever sees what an earlier one wrote.

Three consequences, stated rather than discovered:

- **`Factory.Tail` is not supplied at all**, and D4 below is what that costs.
- **`Migrate` runs about forty-five times per conformance run** — one per `New`
  call, sixteen of them in `store failure classification` alone — at eleven DDL
  statements inside one transaction plus `Prepare`'s three levels. That is the
  round-trip bound §D4 owes: **~45 schemas × ~20 round trips per run**, twice for
  the two runs, and nothing survives the section that created it.
- **The shared schema is still what S2, S3 and S4 use.** It is the conformance
  factory alone that takes a schema per store value.

`eventmemory`'s factory fabricates `event.Failure(outcome, errInjected)` for every
outcome. `eventpg`'s does not, because a fabricated classification proves nothing
about *this store's* classification of a *real* driver error, and the
`store failure classification` section is the one place the suite reads it. The
factory wraps each real store in a `*failing` that holds the sibling values below,
built lazily and once per store, and forwards the next call to one of them:

| Outcome | How the failing wrapper produces it | Real? |
|---|---|---|
| `Closed` | forwards to a real `*eventpg.Store` over the same backing on which `Close()` was called | yes — the store's own lifecycle refusal, nothing issued |
| `Refused` | forwards to a real `*eventpg.Store` over the same backing on which `Prepare` was never called | yes — the store's own not-ready policy, nothing issued |
| `NotWritten` | forwards to a real `*eventpg.Store` **`Prepare`d against this store's own schema over a second `*sql.DB` the factory opened and then closed**. The store is ready and open; the *pool* is gone, so `onExecutor`'s `this.db.Conn(ctx)` fails before a statement is built | yes — a real `database/sql` error through the real classifier, `issued == false`, proof 1, and nothing is written, which is what the case then asserts about the stream |
| `Conflict` | forwards `Append` to the **real** store with `req.Expected + 1`, so the real admission predicate loses for real and answers zero rows | yes — the real statement, the real zero-row answer |
| `BadCursor` | forwards `ReadAll` with a cursor minted over a **second schema** in the same database; the real parser refuses it | yes |
| `Unclassified` | returns the wrapper's own bare error without forwarding | honest — nothing was issued, which is what an unclassified decorator failure is (§UC-048) |
| `Unconfirmed` | forwards `Append` to a real `*eventpg.Store` over a `*sql.DB` opened on **the injecting `database/sql` driver S3 already builds** (`main_integration_test.go` registers it), armed to write the statement and then return a **bare error carrying no SQLSTATE**. `sqlfault.Extract` finds nothing, so rules 1–4 do not fire and rule 5 selects `Unconfirmed` | yes — the real statement, really sent, and a real commit window the store cannot close |

**The `NotWritten` row is the one round 1 caught, and the reason it was wrong is
worth keeping.** The rejected version forwarded to a store over "a DSN that
resolves nowhere". `Prepare` on such a store can never return nil, `append.go`'s
fixed order puts the readiness gate two steps before `onExecutor`, and the
forwarded `Append` therefore answered `event.Failure(Refused, ErrNotReady)` →
`ErrRefused`, where the case asserts `ErrBackend`. The store must be **prepared and
then broken**, in that order, and breaking the pool rather than the DSN is what
leaves the readiness gate satisfied.

**The `Unconfirmed` row was surrendered on a premise S3 disproves.** S3 already
builds a `database/sql` driver that wraps pgx's and injects — that is how
`TestABadConnBeforeTheSendIsOneCallAndNotWritten` returns `driver.ErrBadConn`
*before* the send. The same wrapper returning a bare error *after* the send is
deterministic, is `Unconfirmed` by the store's own rule 5, and is exactly as real
as the `NotWritten` hook. So the section is **reported `passed`, not `not
certified`**, all eight cases run through both the store and the wrapping
decorator, and §INV-062's evidence row stands as written. The clause the old row
would have surrendered is the one clause where a PostgreSQL store has something an
in-memory one structurally cannot say, and `probe.unable` would have downgraded the
**whole section** rather than the clause (`event/eventtest/report.go:58-64`,
driven: `eventtest: store failure classification: not certified`), while
`storeFailureSection`'s `break` would have skipped the wrapping-decorator variant
that INV-062 names as its proof.

### D2 — the fingerprint's canonical form is the store's own rendering, pinned by a golden file  (§8.2)

Level 2 digests a rendering **the store builds from its own expectation model**,
never one read back out of `pg_catalog` — a fingerprint computed from the database
would agree with the database by construction. The rendering is a newline-terminated
list of ASCII lines; the first three are the header, the rest are sorted with
`sort.Strings`:

```
eventpg/schema/v1
schema <name>
bounds maxpayload=<n> maxkey=<n>
check <table> <name> <definition, whitespace-collapsed>
column <table> <name> <ordinal> <type> <notnull> <default|-> <identity|->
fk <table> <name> (<cols>) -> <table> (<cols>)
function <name> <body, whitespace-collapsed>
index <table> <name> <definition, whitespace-collapsed>
persistence <table> permanent
pk <table> (<cols>)
rule <table> none
rls <table> disabled
sequence <table> <column> increment=1 cache=1 cycle=false
trigger <table> <name> <timing> <events> <level> <function> columns=all condition=none
unique <table> <name> (<cols>)
```

The `function` line is what GAP-P2-S2-1 added, and it is the one line whose value
is a *behaviour* rather than a shape: two builds whose `events_are_append_only`
bodies differ describe two different schemas, and before that line they digested
alike, so a `CREATE OR REPLACE` at schema version 1 changed what the schema
enforced with the fingerprint unmoved. The line carries no schema name because the
header already binds the whole rendering to one. `persistence` and the trigger's
`columns=`/`condition=` are constants of *this* build for the same reason `rule`
and `rls` are: a version that expected another value would move the digest.

Those are the line shapes, not a list of lines each of which occurs: **schema
version 1 renders no `index` line**, because every index it deploys is the one a
primary key or a unique constraint creates with its table, and those are already
rendered as `pk` and `unique`. A model field that is always empty is drift
waiting to happen, so there is none; the shape stays in the grammar for the
version that creates an index of its own (S1, measured against the golden).

The digest is `"sha256:" + hex(sha256(rendering))`. `event/eventpg/testdata/fingerprint.golden`
holds the whole rendering for `Schema{}` resolved, and
`TestTheFingerprintIsTheRenderingItDigests` compares the rendering byte for byte
and the digest against a literal. A whitespace change in a constraint definition
is then a **diff a person reads**, not a silent invalidation of every deployed
schema. `MaxPayload` and `MaxKey` are inputs (they are `CHECK` operands);
`MaxBatch`, `StreamPage` and `MaxRead` are not (§2.2).

### D3 — the cursor is fixed-width, tagged, log-bound, and the empty string is the origin  (§8.3)

```
""                                     the origin of the log — (F, X, R) = (0, 0, 0)
"vve1" + base64.RawURLEncoding( log[16] || be64(F) || be64(X) || be64(R) )
```

58 characters, always. Parsing refuses, in this order and with these reasons: a
value that is not `""` and does not carry the tag; a decoded length that is not
40; a log id that is not this store's. `R < F` and `X == 0 && R != 0` are
structurally impossible triples and are refused too — a forged cursor skips its own
events, which is the cost phase 1 already accepted for `event.Cursor` being a
defined string. Not authenticated: **backlog `## P2` §1, `[medium]`, left alone.**

### D4 — there is no `Tail` hook, because D1 removed the log it would have had to find the end of  (§8.4)

`Factory.Tail` exists for "a store whose log holds what this run did not write — a
database nothing truncates between runs, where reading to the end costs the whole
log once per walking section" (`event/eventtest/suite.go:67-73`). D1's `New` gives
every store value a schema of its own, migrated empty and dropped at the end of the
section, so that store is not one: the log a section walks is the log that section
wrote, and `probe.tail`'s own walk — one `ReadAll` answering an empty page — is
already O(1). **The hook is omitted.** It is optional and gates nothing; supplying
a hook that returns `""` would be a hard-coded answer that stays right only by
luck, and supplying the full walk it was meant to avoid was the version round 1
refused.

**What the rejected version cost, recorded so it is not reinvented.** A `Tail` that
walked a *shared* log to an empty page would have grown with every CI run: each run
leaves permanently burnt positions behind (§INV-009 — the `transactions` and
`lifecycle` sections roll appends back), the append-only trigger makes `DELETE` and
`TRUNCATE` impossible, and every later run's walk must settle each gap at two round
trips and one minted transaction id apiece, five times per run. It would have
expired against `Factory.Window / 2` and `t.Fatalf`'d naming "the oldest running
transaction" from `pg_stat_activity` — on an idle cluster, a cause that is not the
cause, arriving one day with no code change.

**`Factory.Window` is still 30 s**, for the reason it always was: the operations are
a network away. And the gate still runs the eventpg suite alone for the reason
§8.4 gives — `pg_snapshot_xmin` is cluster-wide, so a concurrent suite holding a
transaction open holds this one's settlement back.

### D5 — the batch is a generated `VALUES` list, the store caches no statement text  (§8.5)

The statement text varies with the record count, so a driver's own
prepared-statement cache holds **one entry per batch size actually used, bounded by
`MaxBatch`** — 64 by default, 1024 at the kernel ceiling — plus four read texts and
the verification texts, which run once. `pgx/v5/stdlib`'s cache holds 512 by
default, so no eviction pressure exists at the default and the number is stated
rather than discovered. The store keeps **no cache of its own**: a per-store map of
statement texts is package state to get wrong for a saving the driver already
makes. Padding to `MaxBatch` and trimming with `WHERE record.ord <= $n` was
rejected in §2.3 and stays rejected. §UC-084's control appends
`event.MaxBatchCount` = 1024 records in one statement — `3 × 1024 + 4 = 3076`
parameters against PostgreSQL's 65535 — so the widest statement the store can build
is exercised.

### D6 — schema-per-tenant is expressible and no affordance is exported  (§8.6)

`Backing()` already carries the schema name, so a store per tenant schema composes
today by constructing one `Spec` per schema. Nothing is added for it.

### D7 — the two questions the backlog raised that cost one line each, decided here

- **`Factory.Begin` opens `READ COMMITTED`** — `crudsql.DB.Begin(ctx)` with nil
  `TxOptions`, which is PostgreSQL's default. Every bound conformance case
  therefore runs at `READ COMMITTED`, and §6.10's matrix is the only place the
  other two levels appear. Written down because backlog `## P2` §3 found it
  unstated; the rest of that entry is left alone.
- **`Migrate` refuses under `VerifySchema`** with `ErrSpec`, and `Verify` on an
  already-prepared store re-runs all three levels and re-sets readiness. [[D-101]]'s
  "nothing migrates unless asked" is a property of the **store**, not only of
  `Prepare`. Backlog `## P2` §4 asked which; this is the answer, and it is one `if`.
- **The narrow-limits conformance run migrates its own scratch schema.** `MaxKey`
  and `MaxPayload` are `Schema` fields and fingerprint inputs, so a store at
  `MaxKey: 40` cannot verify against a schema migrated at 512. Backlog `## P2` §2
  named this; **D1 closes it for both runs at once**, because every store value the
  conformance factory builds migrates a schema of its own from the same harness §6.7
  needs anyway, at whatever bounds its `Spec` carries. The narrow run is then not a
  special case.
- **The watermark's bound is minted only when there is no useful outstanding one.**
  Backlog `## P2` §9 and §10 are wording defects in §2.6 that produce a stalled
  walk and a bound burnt per poll. The algorithm in
  [`read.go`](#eventpgeventpgreadgo--the-two-read-doors) states `R'` as *the highest
  position the query returned* and mints only when `X == 0` or the outstanding bound
  has been consumed, which is both simpler code and the correct rule. Recorded, not
  claimed as extra work.

---

## Coverage matrix

Every UC and INV of [SPEC]. **Section** is where it is delivered; **Checkpoint** is
the section whose command proves it; **Proved by** names the test or the `eventtest`
subtest. A lower-case name — `expected version`, `transactions`, `resumption` — is a
`t.Run` subtest of `eventtest.Run` and is only meaningful because S5's mutation
harness shows the suite catches a broken store while exercising the real one, and
because S5's census asserts that the section reported `passed` rather than being
downgraded to `not certified` behind a green run.

### Use cases

| UC | Section | Checkpoint | Proved by |
|---|---|---|---|
| UC-069 a deployment migrates from its migration step | S1 (the statements) + S2 (execution) | S1 (the list, live) + S2 | `TestMigrationStatementsAreOrderedTransactionalDDL` (the two goldens), `TestARefusedMigrationLeavesTheTablesItCreatedRolledBack`, `TestASchemaNamedForAReservedWordDeploys`; `TestMigrationStatementsBuildTheSchemaTheFingerprintDescribes`, `TestRunningTheMigrationTwiceReissuesNoLog` |
| UC-070 nothing said about schema management | S1 (`New` resolves the zero) + S2 (`Prepare`) | S2 | `TestTheZeroSchemaManagementVerifiesAndMigratesNothing`, `TestPrepareAgainstAMissingSchemaRefusesBeforeAnyAppend` |
| UC-071 an older or a newer schema version | S2 | S2 | `TestVerificationRefusesEveryMutationAndPassesTheIntactSchema` (`version-below`, `version-above`) |
| UC-072 the version matches and the database was hand-edited | S2 | S2 | `TestVerificationRefusesEveryMutationAndPassesTheIntactSchema` (fourteen mutations, both directions) |
| UC-073 two replicas start together with `ManageSchema` | S2 | S2 | `TestTwoConcurrentPreparesTakeOneLockOnOneBackend`, `TestASecondMigrationBlocksAgainstAHeldLock` |
| UC-074 a different payload bound meets a deployed schema | S1 (fingerprint input, and the migration's own refusal) + S2 | S1 (the `ManageSchema` door) + S2 (level 2) | `TestAMigrationOverASchemaItDidNotBuildRefusesRatherThanRestampingIt`; `TestVerificationRefusesEveryMutationAndPassesTheIntactSchema` (`bounds`), `TestLimitsAreTheDeployedConstraintOperands` |
| UC-075 a store is used before it verified | S1 (`Refused`) + S3 + S4 | S4 | `TestAnUnpreparedStoreRefusesEveryOperationAndAnswersTheThreePureOnes` |
| UC-076 an append lands with no transaction bound | S3 | S3 | `TestAnAppendOnThePoolIsAtomicWithNothing`, `expected version` |
| UC-077 two writers race at one version | S3 | S3 | `TestEightWritersAtOneVersionLeaveOneWinnerAndSevenConflicts`, `concurrency` |
| UC-078 an append joins the caller's transaction | S3 | S3 | `TestAnAppendJoinsTheBoundTransactionAndNothingOutsideItSees`, `transactions` |
| UC-079 the caller's transaction rolls back | S3 | S3 | `TestARollbackLeavesNoFragmentAndBurnsThePositions` |
| UC-080 a lost race inside `REPEATABLE READ` | S3 | S3 | `TestTheIsolationMatrixTellsAConflictFromASerialisationFailure` |
| UC-081 the connection dies around an autocommit append | S3 | S3 | `TestAKilledBackendAnswersUncertainAfterExactlyOneExecContext`, `TestTheRetryWithTheSameTokenResolvesTheUncertainty` |
| UC-082 an append fails inside the caller's transaction | S3 | S3 | `TestAFailureInsideABoundTransactionIsNeverUnconfirmed` |
| UC-083 an append is cancelled | S3 | S3 | `TestEachCancellationWindowIsAnsweredByTheRuleAndNotByOneOutcome` |
| UC-084 a batch is appended | S3 | S3 | `TestABatchLandsDenseAscendingAndAtOneInstant`, `TestTheWidestBatchTheStoreCanBuildIsAppended` |
| UC-085 two subsystems prove they wrote in one transaction | S3 | S3 | `TestARepoAuthorityAndAReceiptCompareSameAcrossTwoWritersInOneTransaction`; `TestASavepointWritesUnderItsParentsAuthorityAndARollbackDiscardsThem` (the savepoint half, §6.12) |
| UC-086 an ambient executor that is not a transaction | S3 | S3 | `TestAnAmbientNonTransactionRefusesAtAllFourDoors` — `ErrAmbientNotTransaction` at three doors, `ErrRefused` at the `ReadAll` door, one cause at all four, zero statements at all four (§executor.go) |
| UC-087 a stream is replayed | S4 | S4 | `TestAStreamPagesToItsEndAndFoldsTheSameThroughASibling`, `stream paging` |
| UC-088 a consumer walks the log and resumes | S4 | S4 | `TestAWalkFromTheEmptyCursorTilesWithAWalkFromTheTail`, `resumption` |
| UC-089 a walk meets a position still in flight | S4 | S4 | `TestAWalkDoesNotPassAPositionAWriterCouldStillCommit` |
| UC-090 a walk meets a position burnt by a rollback | S4 | S4 | `TestABurntGapIsPassedInsideOneReadAllAndALiveOneIsNot` |
| UC-091 a cursor is foreign, unparsable or retired | S4 | S4 | `TestEveryUnreadableCursorIsRefusedAndTheEmptyOneIsTheOrigin` |
| UC-092 a global walk inside the caller's transaction | S4 | S4 | `TestAWalkInsideATransactionMintsNoBoundAndSettlesNoGap` |
| UC-093 a row that is not what the schema promised | S4 | S4 | `TestARowOutsideTheSchemasPromisesRefusesTheWholeRead` |
| UC-094 the composition root publishes readiness | S2 (`Check`) + S5 (the `health` composition) | S5 | `TestCheckIsAReadinessAnswerAndNamesNoImportance` |
| UC-095 the store is closed | S1 (`Close`) + S4 | S4 | `TestCloseIsIdempotentAndClosesNothingItDidNotOpen` |
| UC-096 someone tries to modify history | S2 (the triggers) | S2 | `TestTheDatabaseRefusesUpdateDeleteAndTruncateAndAdmitsInsert` |
| UC-097 a store implementer runs the conformance suite | S5 | S5 | `TestTheStoreSatisfiesTheContract`, `TestTheStoreSatisfiesTheContractAtNarrowerLimits` |
| UC-098 another writer inserts into the events table | S4 (the read) + S2 (the trigger) | S4 | `TestAForeignWriterIsNotSkippedAndTheOverridingWriterIs` |

### Invariants

| INV | Section | Checkpoint | Proved by |
|---|---|---|---|
| INV-046 admission is a conditional row update under a unique index | S3 | S3 | `TestEightWritersAtOneVersionLeaveOneWinnerAndSevenConflicts`; `TestNoStatementReadsTheStreamVersionIntoGo` (source); `TestVerificationRefusesEveryMutationAndPassesTheIntactSchema` (`unique-dropped`) |
| INV-047 the version advance and the rows are one statement | S3 | S5 | `TestARollbackLeavesNoFragmentAndBurnsThePositions`; `TestTheAuditOverTheWholeSchemaHolds` (post-suite: `streams.version = max(events.version)`, no orphan event row) |
| INV-048 history is append-only in the database | S2 | S2 | `TestTheDatabaseRefusesUpdateDeleteAndTruncateAndAdmitsInsert`; the `append-only-trigger-dropped` mutation |
| INV-049 the store selects no isolation level | S3 | S3 | `TestTheIsolationMatrixTellsAConflictFromASerialisationFailure`; `TestNoDoorOpensCommitsOrRollsBackAnything` (source, excluding `migration.go` **by name**, and refusing if that file is absent) |
| INV-050 a conflict is zero rows; `40001` is not a conflict | S3 | S3 | `TestEightWritersAtOneVersionLeaveOneWinnerAndSevenConflicts` + `TestTheIsolationMatrixTellsAConflictFromASerialisationFailure`; `TestEverySQLStateThisStoreNamesSelectsTheOutcomeTheRuleRequires` (real driver errors, and the retryable half of the rule walked at `40001`, a real `55P03` and a real `40P01`) |
| INV-051 the store opens, commits and rolls back nothing | S3 | S3 | `TestNoDoorOpensCommitsOrRollsBackAnything`; `TestARollbackLeavesNoFragmentAndBurnsThePositions`; `TestASavepointWritesUnderItsParentsAuthorityAndARollbackDiscardsThem` (the store neither opens nor releases the savepoint — the caller does) |
| INV-052 inside a bound transaction a failed append did not land | S3 | S3 | `TestAFailureInsideABoundTransactionIsNeverUnconfirmed` (its pair: same failure, `ErrBackend` inside, `ErrUncertain` on the pool) |
| INV-053 uncertainty is never resolved by guessing | S3 | S3 | `TestAKilledBackendAnswersUncertainAfterExactlyOneExecContext`; `TestABadConnBeforeTheSendIsOneCallAndNotWritten`; `TestNoStatementIsIssuedOnTheDatabaseHandle` (source); `TestTheRetryWithTheSameTokenResolvesTheUncertainty` |
| INV-054 a cursor is a settled watermark | S4 | S4 | `TestAWalkDoesNotPassAPositionAWriterCouldStillCommit`; `TestABurntGapIsPassedInsideOneReadAllAndALiveOneIsNot`; `TestTheXminEqualsXmaxRuleFailsTheInFlightCase` (the negative control §2.6 measured false) |
| INV-055 delivery order is position order is assignment order | S4 + S5 | S5 | `global order`, `global paging`, `conservation`; `TestTheAuditOverTheWholeSchemaHolds` (position order equals version order per stream) |
| INV-056 a cursor names the log | S4 | S4 | `TestAWalkFromTheEmptyCursorTilesWithAWalkFromTheTail`; `TestEveryUnreadableCursorIsRefusedAndTheEmptyOneIsTheOrigin`; `shared backing` |
| INV-057 schema identity is compared at three levels | S2 | S2 | `TestVerificationRefusesEveryMutationAndPassesTheIntactSchema` (fourteen refusals, two passes) |
| INV-058 the published bounds are the deployed schema's | S1 + S2 | S2 | `TestLimitsAreTheDeployedConstraintOperands` (exactly `MaxPayload` accepted, one byte more refused by the kernel) |
| INV-059 the backing is the database and the schema together | S1 | S4 | `TestTwoStoreValuesOverOneSchemaAreOneStoreAndTwoSchemasAreNot` |
| INV-060 nothing starts, nothing is discovered, nothing is logged | S1 | S1 | `TestTheStoreStartsNothingAndReadsNoEnvironment` (source); `scripts.TestMerelyImportingTheEventExtensionStartsNothing`; `TestNewAgainstADeadDSNAnswersAStore` |
| INV-061 zero diffs under `event/` outside `event/eventpg` | S5 | S5 | `make check-event-kernel`; `scripts.TestCheckEventKernelReportsADifferenceAndOtherwiseOk` |
| INV-062 no sentinel of `eventpg`'s crosses the store seam | S3 + S4 | S5 | `TestEveryErrorTheEightMethodsProduceIsNilAContextErrorOrAFailure`; `store failure classification` through the wrapping decorator, and `TestBothConformanceRunsCertifyEverySectionButTheOneThisStoreDeclines` is what says that section ran rather than being downgraded |
| INV-063 the store names no importance and imports no `health` | S1 | S1 | `scripts.TestNoEventPackageCostsMoreThanTheSeamItNames` (the `eventpg` row's closure excludes `health`, `port`, `runtime`) |
| INV-064 an autocommit failure the store cannot pin down is `Unconfirmed` | S3 | S3 | `TestEverySQLStateThisStoreNamesSelectsTheOutcomeTheRuleRequires` (the no-SQLSTATE case is the one a `NotWritten` default fails; the injected `08006` and `08007` cases are the ones the connection-class arm fails); `TestNoNotWrittenBranchIsUnguarded` (source, looks for a `default:`) |
| INV-065 a transaction has its id before it draws a position | S2 (the trigger) + S4 (the consequence) | S4 | `TestAForeignWriterIsNotSkippedAndTheOverridingWriterIs` — control (a) reads `pg_current_xact_id_if_assigned()` from a test-only row trigger: not null with the store's trigger, null with it dropped; control (b) asserts the `OVERRIDING SYSTEM VALUE` writer **is** skipped when it drew the position in a transaction that has ended, and **is not** when it drew it inside the one that inserts |

---

## Contracts before code

Written before the code, by file. Receiver is `this` throughout. Everything below
is what `make api` must show under `## github.com/frostgrove/vv/event/eventpg`, and
nothing else is exported.

### `event/eventpg/doc.go` — the package sentence

No exported symbol.

### `event/eventpg/schema.go` — the half of the configuration that is written into the database

```go
const (
	DefaultSchema = "frostgrove_events"
	SchemaVersion = 1

	DefaultMaxPayload = 64 << 10
	DefaultMaxKey     = 512
	DefaultMaxBatch   = 64
	DefaultPage       = 256
)

type Schema struct {
	Name       string
	MaxPayload int
	MaxKey     int
}

func (this Schema) Resolved() (Schema, error)
func (this Schema) Fingerprint() (string, error)
```

**Why `Schema` is a type and not four fields of `Spec`.** A number that changes the
deployed schema cannot sit in the same struct as one that does not: `MaxPayload` and
`MaxKey` are `CHECK` constraint operands and fingerprint inputs, `MaxBatch`,
`StreamPage` and `MaxRead` are Go-side ceilings that touch no row. A `Spec` field
that silently invalidates every deployed schema is exactly the naive contract this
framework forbids.

**Why `Resolved` is exported.** A deployment's migration step and the runtime are
two processes, and both must agree on the numbers. `Resolved` is how the migration
step prints what it is about to deploy, and it is the only way to see the defaults
without constructing a store. It returns `ErrSpec` for a name outside 1–63 bytes of
`[a-z][a-z0-9_]*` — the rule `jobspg.validSchema` spells, kept as a deliberate
narrowing and not as a claim about the PostgreSQL grammar, since every statement
quotes the name — and for a bound over `event.MaxPayloadBytes` or
`event.MaxKeyBytes`.

**Why `Fingerprint` is exported.** During an incident an operator compares a build's
expectation against `schema_meta.fingerprint` with a two-line program and no store,
no pool and no `Prepare`. It is `"sha256:" + hex(...)` over D2's rendering.

```go
func MigrationStatements(schema Schema) ([]string, error)
```

**Why it is exported: [[D-101]] makes the operator path first class**, and this is
that path. Eleven statements for schema version 1, in order, every one transactional
DDL, no `CREATE INDEX CONCURRENTLY`, therefore safe inside one transaction — which
`MIGRATIONS.md` states and which is the property §UC-069's Must-not is really about
(backlog `## P2` §11 asked for exactly this restatement; it costs a sentence and is
taken):

1. `CREATE SCHEMA IF NOT EXISTS <s>`
2. `CREATE TABLE IF NOT EXISTS <s>.schema_meta (...)`
3. `CREATE TABLE IF NOT EXISTS <s>.streams (...)`
4. `CREATE TABLE IF NOT EXISTS <s>.events (...)`
5. `CREATE OR REPLACE FUNCTION <s>.events_are_append_only() ...`
6. `CREATE OR REPLACE FUNCTION <s>.events_assign_writer_xid() ...`
7–9. the three triggers, each `DROP TRIGGER IF EXISTS ... ; CREATE TRIGGER ...` in one `DO` block so a re-run is a no-op
10. `INSERT INTO <s>.schema_meta (singleton, version, log, fingerprint) VALUES (true, 1, decode(replace(gen_random_uuid()::text, '-', ''), 'hex'), '<fingerprint>') ON CONFLICT (singleton) DO NOTHING` — **the log is minted in the database and never reissued**, which is §UC-069's control
11. a `DO` block that reads `<s>.schema_meta` and raises SQLSTATE `EVPG1` unless it holds version 1 at exactly this build's fingerprint — **it records nothing**

**Why eleven asserts and does not assign.** *A statement list may record a
description only of a schema it can produce.* Version 1 creates objects that are
absent and replaces the two functions and the three triggers; it alters no table, so
against a schema another expectation built it changes nothing and an `UPDATE` of
`schema_meta` would record a fact that is false — measured live: a schema deployed at
`MaxPayload` 1024 keeps `CHECK ((octet_length(payload) <= 1024))` while the default
build's list stamped its own digest over it, exit 0. That defeats verification level
2, `Check(ctx)`, and the incident comparison `Fingerprint()`'s export exists for. So
statement eleven asserts what statement ten wrote, and the raise rolls the whole
transaction back — a drifted schema is loud at the migration step rather than made to
agree with whoever ran last. A schema version N whose list carries the `ALTER`s that
transform N-1 into N *does* produce the new description; its last statement is an
`UPDATE` guarded on the version it migrates from (`WHERE singleton AND version = N-1`)
followed by this same assertion. `EVPG1` rather than plpgsql's default `P0001`,
because a caller that cannot tell this refusal from any other `RAISE` is one that
would classify it by message text.

**Every statement quotes the schema name.** `validSchemaName`'s pattern is not the
PostgreSQL grammar: roughly a hundred reserved words match `[a-z][a-z0-9_]*` and
none of them can follow `CREATE SCHEMA` unquoted — `user`, `table`, `select`,
`order`, `group`, `all`, `default` and `do` each answer `syntax error at or near`,
driven live. The name is therefore emitted as `"<name>"` (with any `"` doubled)
everywhere it appears, which makes the reserved-word question disappear rather than
answering it with a word list that changes between PostgreSQL versions. The
`[a-z][a-z0-9_]*` rule stays, as an honest narrowing and not as a grammar claim: it
keeps the name reading the same quoted and unquoted so an operator can type it back,
and the refusal message says so.

The tables are exactly §2.1's, with the two `octet_length` operands taken from the
resolved `Schema`. The identity is `GENERATED ALWAYS AS IDENTITY (INCREMENT 1 CACHE 1 NO CYCLE)`
because `CACHE 1` and no `CYCLE` are the first premise of the read watermark — and
`INCREMENT 1`, `CACHE 1` and `NO CYCLE` are **one** fact in the expectation model
(`expectedIdentity`), rendered into the DDL by `clause()` and into the fingerprint by
`rendering()`, so the two cannot disagree.

**The statement list is pinned byte for byte**, at the default `Schema` and at
`{narrow_events, 1024, 40}`, by `testdata/migration.golden` and
`testdata/migration_narrow.golden`. The `expectation` model is shared but its two
renderings are independent code, so a model change moves the fingerprint and a
DDL-rendering change does not — six mutations (trigger timing, the append-only
function body, the unique constraint, the foreign key, `NOT NULL`, the identity
parameters) each left the whole S1 suite green before the goldens existed and each
fails it now.

### `event/eventpg/config.go` — the store, its spec and the four pure answers

```go
var (
	ErrSpec           = errors.New("eventpg: this store cannot be assembled from this spec")
	ErrSchemaMismatch = errors.New("eventpg: the deployed schema is not the one this build expects")
	ErrNotReady       = errors.New("eventpg: this store has not verified its schema")
)

type SchemaManagement uint8

const (
	UnsetSchemaManagement SchemaManagement = iota
	VerifySchema
	ManageSchema
)

func (this SchemaManagement) Valid() bool
func (this SchemaManagement) String() string

type Spec struct {
	DB     *sql.DB
	Source crud.Source

	Schema           Schema
	SchemaManagement SchemaManagement

	MaxBatch   int
	StreamPage int
	MaxRead    int
}

type Store struct{ /* unexported */ }

func New(spec Spec) (*Store, error)

func (this *Store) Schema() Schema
func (this *Store) SchemaManagement() SchemaManagement

func (this *Store) Capabilities() event.Capabilities
func (this *Store) Limits() event.Limits
func (this *Store) Backing() event.Backing
func (this *Store) Close() error
```

**Why three sentinels and not one.** `ErrSpec` is a wiring error a composition root
reads at start-up; `ErrSchemaMismatch` is an operations error a deployment reads;
`ErrNotReady` is a lifecycle error a probe reads. None of the three crosses the store
seam as a sentinel — §INV-062 — they travel as an `event.Failure`'s **cause**,
reachable through `event.CauseOf` and by neither `errors.Is` nor `errors.As` through
a kernel sentinel.

**Why `SchemaManagement` has a `String`.** Every refusal that names it must name it
in words; a `%d` in an operator's log is the shape that gets grepped for and found
in the wrong file.

**What `New` refuses, each naming the value the caller set and the one it may not
pass** — one door earlier than the kernel would, exactly as `eventmemory.New` does:

- a nil `DB`; a nil `Source` (§2.4 — a store that cannot see the caller's transaction
  is a trap, not a degraded configuration, and `jobspg`'s nil-`Source` tolerance is
  deliberately **not** copied);
- `!crud.SameDataSource(crud.KeyOf(spec.Source), spec.DB)` — the same refusal
  `jobspg.New` makes, because "atomic" across two handles is a sentence with no
  meaning;
- `!spec.SchemaManagement.Valid()`;
- an illegal `Schema.Name` or a bound over a kernel ceiling (through `Resolved`);
- `MaxBatch > event.MaxBatchCount`, `StreamPage`/`MaxRead > event.MaxPageCount`, and
  either page above `event.ResidentPage(MaxPayload)`.

`New` performs **no I/O**, starts nothing, reads no environment. `New` against a
`*sql.DB` whose DSN resolves nowhere returns a store, not an error — §INV-060, and
it is what makes D1's `NotWritten` hook constructible.

**The backing.**

```go
type backing struct {
	db     *sql.DB
	schema string
}
```

`event.NewBacking(backing{...})` — comparable, non-nil, and `Backing.Equal` is
`crud.SameDataSource`, which is type equality plus `==`. Two store values over one
database and one schema compare `Equal`; two schemas in one database do not. A
`*sql.DB` alone would accept a cursor minted over the other schema, which is
§UC-088's control.

**Capabilities and limits.**

```
Transactions:       Supported      (Source is required, so this is unconditional)
Persistence:        Supported
MonotoneVisibility: Unsupported    (§2.6 — a freshness claim this store will not make)
SharedBacking:      Supported

MaxPayload: Schema.MaxPayload   MaxBatch:   Spec.MaxBatch or 64
MaxKey:     Schema.MaxKey       StreamPage: Spec.StreamPage or min(256, event.ResidentPage(MaxPayload))
                                MaxRead:    Spec.MaxRead    or the same
```

`Close` sets a flag, returns nil every time, closes no `*sql.DB` and resolves no
transaction.

### `event/eventpg/migration.go` — the one file that opens a transaction

```go
func (this *Store) Migrate(ctx context.Context) error
```

**Why it is exported.** A deployment that chose `ManageSchema` needs to run the
migration without serving, and a test needs to build a scratch schema. It refuses
under `VerifySchema` (D7).

It is the **only** file in the package that names `BeginTx`, `Commit` or `Rollback`,
and §INV-049's source check excludes it **by name** and refuses if it is absent, so a
rename cannot turn the exclusion into a blanket exemption.

`Migrate` does everything on one `*sql.Conn` — `jobspg`'s `withMigrationLock`
(`jobs/jobspg/retention_migration.go:115`) copied rather than reinvented:

1. `conn, err := this.db.Conn(ctx)`; `defer conn.Close()`;
2. `SELECT pg_try_advisory_lock($1)` in a loop with 250 ms + jitter backoff,
   honouring `ctx`, keyed by `migrationLock(schema)` — an `int64` from the first
   eight bytes of `sha256(schema)`;
3. `conn.BeginTx(ctx, nil)`, the eleven statements, `Commit`;
4. unlock on a context detached from cancellation (5 s); an unlock that answers
   false discards the connection with `conn.Raw(func(any) error { return driver.ErrBadConn })`.

A session-level advisory lock taken over a `*sql.DB` serialises nothing — three
pooled connections, an unlock that returns false, and a `23505` on
`pg_type_typname_nsp_index` in one of two racing `CREATE TABLE`s. That is what
§UC-073 observes in `pg_locks` rather than infers from the absence of an error.

### `event/eventpg/verify.go` and `event/eventpg/catalog.go` — the three levels

```go
func (this *Store) Prepare(ctx context.Context) error
func (this *Store) Verify(ctx context.Context) error
func (this *Store) Check(ctx context.Context) error
```

**Why all three are exported.** `Prepare` is the lifecycle call a composition root
makes; `Verify` is what a deployment runs to answer "is this database the one this
build expects" without serving; `Check` is `health.Probe` **structurally** — same
signature, no import of `health`, no importance, no code, no check name ([[D-091]]).

`Prepare` migrates under `ManageSchema`, then verifies under both. Until it has
returned nil the store answers `event.Failure(event.Refused, ErrNotReady)` from
`Append`, `ReadStream` and `ReadAll` — **`Refused`, never `Closed`**, because nothing
was tried and the caller's transaction is untouched.

`Verify` runs, in order, and fails closed at the first:

1. **version** — `SELECT version, log, fingerprint FROM <s>.schema_meta WHERE singleton`,
   compared for **equality**. The message names both numbers and the schema and
   carries no row, no credential and no DSN.
2. **fingerprint** — byte-for-byte against `this.schema.Fingerprint()`.
3. **catalog** — `catalog.go`'s six reads against the expectation model, compared as
   **set equality** for the classes whose extra member is invisible:

| Class | Read from | Compared |
|---|---|---|
| relation kind, storage, RLS, inheritance | `pg_class` | `relkind = 'r'`, `relpersistence = 'p'`, `relrowsecurity` and `relforcerowsecurity` false, `relhassubclass` false, on all three tables |
| columns | `pg_attribute` + `pg_get_expr(adbin)` | name, ordinal, `atttypid::regtype`, `attnotnull`, `attidentity`, default — exact per column: only `position` is identity, only `created_at` has a default |
| constraints | `pg_constraint` | p/u/f/c by name and `convalidated`; p/u/f **structurally** — `conkey` and `confkey` resolved to column names in constraint order, and the referenced relation; c by `pg_get_constraintdef` compared token by token (whitespace and parentheses removed, because PostgreSQL parenthesises what it parsed) and `NOT connoinherit` |
| the identity sequence | `pg_get_serial_sequence` → `pg_sequence` | `seqincrement = 1`, `seqcache = 1`, `NOT seqcycle` — the watermark's first premise, not decoration |
| triggers | `pg_trigger` where `NOT tgisinternal`, joined to `pg_proc` through `tgfoid` | **exact set**: three on `events`, none on `streams` or `schema_meta`; name, timing, event set, level, function, `tgenabled = 'O'` — and **what it does**: `prosrc` against the body the model carries, `tgqual IS NOT NULL` (this build authors no `WHEN`), `tgattr` empty (this build watches no column list) |
| rules | `pg_rewrite` where `rulename <> '_RETURN'` | **empty set** on all three |
| policies | `pg_policy` | **empty set** on all three |

The refusal names the object and what is wrong with it — missing, not validated,
wrong columns, wrong definition, or **present and not expected**. Extra *indexes*
and extra *constraints* are exempt, and the exemption is safe because each fails
**loudly** at write time (`23505` → `NotWritten` → `ErrBackend`); comments,
ownership, privileges, storage parameters and unrelated tables in the schema are
never read.

`Check` is levels 1 and 2 only — ctx error, then closure, then readiness, then one
round trip re-reading version, fingerprint **and the log `Verify` recorded**, all
out of the one row. Level 3 is expensive and runs at `Prepare`, never on a probe
path. The log is compared because a schema dropped and migrated again under a
running store fingerprints identically and every cursor that store minted is
foreign — the one drift levels 1 and 2 cannot see.

Every statement of `Migrate`, `Verify` and `Check` is issued on a `*sql.Conn`
checked out for the call, never on the `*sql.DB`, so §INV-053's source check is a
rule over the whole package rather than a rule with an exemption.

### `event/eventpg/executor.go` — where every statement is issued, and the transaction question

```go
func (this *Store) Transaction(ctx context.Context) (event.Authority, error)
```

[[D-118]]'s rule, one answer and no second spelling. `crud.ExecutorFor(ctx, this.source)`:

| Found | Answer | Then |
|---|---|---|
| nothing | invalid authority, no error | the operation runs on the pool |
| an executor `crudsql.Transaction` yields a `*sql.Tx` from | `event.NewAuthority(this.backing, tx)`, no error | the operation runs **inside it** |
| an executor that is not a transaction, or one no `*sql.Tx` can be taken from | invalid authority **and** `ErrAmbientNotTransaction` | the operation refuses before any statement, **at all four doors** |

#### What the caller sees at each of those four doors, and why the fourth is different

§UC-086 says "`ErrAmbientNotTransaction` from every door". **Three doors can answer
that and the fourth cannot**, and the reason is in the frozen kernel rather than in
this store:

| Door | Route | Sentinel the caller reads |
|---|---|---|
| `Repo.Within` / `Repo.Authority` | `store.Transaction` → `refuseTransaction` (`event/repo.go:115,132`) | `event.ErrAmbientNotTransaction` |
| `Repo.Load` (`ReadStream`) | `Repo.transaction` → `refuseTransaction` (`event/repo.go:147`, called at `repo.go:35`) | `event.ErrAmbientNotTransaction` |
| `Repo.Append` | the same, at `repo.go:93` | `event.ErrAmbientNotTransaction` |
| `event.Read(log, …).Next` (`ReadAll`) | `Reader.Next` → `refuseRead` → `refuse(err, readDoor)` (`event/reader.go:48`, `event/errors.go:209-250`) | **`event.ErrRefused`** |

`ErrAmbientNotTransaction` is minted by exactly one function, `refuseTransaction`
(`event/errors.go:181`), called from exactly those three places. `Reader.Next` never
asks the store the transaction question, and `refusal.Is` answers **false** for any
target that is in the kernel vocabulary and is not the refusal's own sentinel
(`event/errors.go:101-107`), so no outcome a store can return renders that sentinel
at the read door. **This store therefore answers `event.Failure(event.Refused, …)`
from `ReadAll`**, whose refusal is `newRefusal(ErrRefused, causeAsWrap(cause), cause)`.

`Refused` and not one of the other six, and the choice is load-bearing:
`Unclassified` and `NotWritten` both render `ErrBackend` at the read door
(`errors.go:244-250`), which the kernel documents as "the store failed" — retry.
A projector draining under a bound non-transaction would then retry forever on a
wiring error that never clears. `ErrRefused` is the one sentinel of the seven that
says *this is policy and it will not clear by trying again*, which is what it is.

**One value at all four doors.** `Transaction` returns the same error the read door
carries as its cause, so `event.CauseOf(err)` is that value at all four — the
uniform assertion the test rests on. Whether that value is `event.ErrAmbientNotTransaction`
itself or a private one of `eventpg`'s is backlog `## P2` §18, `[medium]`, **left
alone**; the contract here holds either way because it names *one* value rather
than which one.

**In S3 the fourth row of that table is measured at the store's own seam**, because
`ReadAll` is S4's: S3 change 2 says which of §UC-086's assertions are the store's own
evidence there and which wait for S4.

**This is a kernel gap and it is said out loud rather than patched.** §UC-086's
**Then** is amended by this plan to the table above; its **Must not** — "no fallback
to autocommit, at any door, including the two reads" — is unchanged, holds at all
four, and is what the test measures with the statement counter. A consumer that
wants the wiring class *by name* before draining calls `Repo.Within` or
`Repo.Authority` first, which does answer it; the module page says so. The kernel
change that would let a `Log` door carry a wiring class is **backlog `## P2` §24,
`[medium]`, owner the phase that unfreezes the kernel** — phase 2 writes zero diffs
under `event/` (§INV-061).

Unexported, and this is the whole reason the file exists:

```go
type executor interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

type run struct {
	on     executor
	joined bool
}

func (this *Store) onExecutor(ctx context.Context, use func(run) error) error
```

`*sql.Tx` and `*sql.Conn` both satisfy `executor`, and they are **the only two
`database/sql` values that execute a statement exactly once**. `(*sql.DB).ExecContext`
retries a `driver.ErrBadConn` on up to two cached connections and then once more on a
fresh one — three executions of one append, on exactly the path where §2.5 says
uncertainty exists. So when nothing is bound `onExecutor` checks a connection out with
`this.db.Conn(ctx)` and closes it in a `defer` after `use` returns; when a transaction
of this backing is bound it hands over the `*sql.Tx` and closes nothing. The callback
shape is not decoration: it keeps the connection alive for the length of a `*sql.Rows`
scan and makes one `ReadAll` — up to three statements — one checkout instead of three.

The store then contains **no call to a statement method on `*sql.DB` at all**, which
is a source check a reviewer runs rather than a discipline the next door has to
remember. A checkout failure is not uncertainty: nothing was issued, so it is
`NotWritten` by proof 1.

### `event/eventpg/classify.go` — `NotWritten` only on proof

```go
func outcomeOf(failure error, issued, joined bool) event.Outcome
func causeOf(failure error) error
```

The rule, and there is **no default branch that answers `NotWritten`**:

1. `joined` → `NotWritten`. Inside a bound transaction there is no uncertainty
   window: a statement that errored, was cancelled, or lost its connection leaves a
   transaction PostgreSQL will now refuse to commit.
2. `!issued` → `NotWritten` (proof 1) — the checkout failed, so nothing reached a
   server.
3. `errors.Is(failure, driver.ErrBadConn)` → `NotWritten` (proof 1), which the
   `database/sql` driver contract permits **only** when the driver is certain the
   server did not receive the query.
4. a SQLSTATE from `sqlfault.Extract` that is **not** class `08` and not `57P01`,
   `57P02` or `57P03` → `NotWritten` (proof 2) — the backend processed it, aborted
   it, and survived to say so.
5. everything else → `Unconfirmed`.

Selection is driven by the SQLSTATE itself, not by whether
`sqlerr.Classify("postgres", …)` recognises it: `57014` is not in that table and is
still a server answer. `sqlfault.Extract` finds the SQLSTATE through a
`SQLState() string` method or a `SQLState` field and therefore needs no driver
import.

`causeOf` builds the second spelling of retryability the kernel reads — an
`*errs.Fault` whose `Kind` is `errs.KindRetryable` — for the codes
`sqlerr.Classify` gives as `CodeSerializationFailure` (`40001`), `CodeDeadlock`
(`40P01`) and `CodeLockTimeout` (`55P03`), and `errs.Internal()`
for every other code it recognises, with the driver error attached through
`Wrapping`. A **`switch`, not `errs.StandardCodes()`**: a package-level `*errs.Codes`
is state this package does not need, and only retryability is load-bearing at this
seam. Three codes and not four: `errs.CodeUnavailable` carries the same kind, but
`errs/sqlerr/postgres.go` maps no SQLSTATE to it, so a fourth arm would be one no
case can walk — which is the shape §GAP-P2-S3-1 found in the other two.
When classification recognises nothing, the cause is the driver error itself.
Neither the SQLSTATE text nor the driver's message reaches a rendered refusal — the
kernel renders the sentinel and nothing of the cause (§INV-025) — and the cause stays
reachable through `event.CauseOf`.

### `event/eventpg/append.go` — one statement, and what its row count means

```go
func (this *Store) Append(ctx context.Context, req event.AppendRequest) error
```

Order, and it is fixed: `ctx.Err()` → closed → ready → `Transaction(ctx)` (an ambient
non-transaction refuses here, before anything is built) → `len(req.Records) == 0`
returns nil with no statement → an `Expected` above `math.MaxInt64` is a `Conflict`
with no statement (S3 change 3: a `bigint` under `CHECK (version > 0)` holds no such
version, so no stream is at one) → build the text, binding a nil payload as an empty
`[]byte` (S3 change 4) → `onExecutor` → `Exec` → `RowsAffected`.

The statement is §2.3's, measured above, with three parameters per record generated
into a `VALUES` list and `ORDER BY record.ord`. `recorded_at` is
`statement_timestamp()` — one instant for the whole batch, one clock per database
rather than one per process, which is why there is no `Spec.Clock`.

`RowsAffected`:

| Answer | Outcome |
|---|---|
| `len(req.Records)` | admitted, nil |
| `0` | `event.Failure(event.Conflict, errStreamMoved)` |
| anything else, or `RowsAffected` itself failing | `event.Failure(event.Unclassified, …)` — the append door reads it as uncertainty (§INV-045), which is the correct answer, because a wrong row count means the store does not know what it left behind |

### `event/eventpg/read.go` — the two read doors

```go
func (this *Store) ReadStream(ctx context.Context, s event.Stream, after event.Version) ([]event.Envelope, error)
func (this *Store) ReadAll(ctx context.Context, after event.Cursor) ([]event.Envelope, event.Cursor, error)
```

Both doors open in the same fixed order as `Append`: `ctx.Err()` → closed → ready →
`Transaction(ctx)` → the read. An ambient non-transaction refuses at the fourth
step, before a cursor is parsed and before a statement is built —
`event.Failure(event.Refused, …)` at both, which the caller reads as
`ErrAmbientNotTransaction` through `Repo.Load` and as `ErrRefused` through
`event.Read` (§executor.go).

`ReadStream` is one text — the `LIMIT` is `StreamPage`, a constant of the store, so
it is baked in:

```sql
SELECT position, version, type, revision, payload, recorded_at
  FROM <s>.events WHERE family = $1 AND key = $2 AND version > $3
 ORDER BY version LIMIT <StreamPage>
```

A short page is the end of the stream and the kernel issues no confirming read, so
the page size and the `LIMIT` are the same number by construction.

`ReadAll` is §2.6's, and the algorithm is stated in full because it is not
recoverable from the code:

1. Parse the cursor into `(F, X, R)`; `""` is `(0, 0, 0)` and is **never** `ErrCursor`.
2. One statement, one snapshot for both halves — the `LEFT JOIN LATERAL` above, at
   `position > F LIMIT MaxRead`, returning `floor_xid` even on an empty page.
3. `settled = R` when `X > 0 && floor_xid > X`, else `settled = F`.
4. Walk the rows in position order, delivering while the next position is `F+1` or
   the whole gap before it lies at or below `settled`. Stop at the first gap that is
   neither.
5. `R'` is **the highest position the query returned**, not the highest delivered —
   read the other way, a walk that stops at a gap in the head of its page records
   `R' = F`, settlement can never cover anything, and the walk stalls forever on a
   burnt gap.
6. If nothing was left undelivered → the new cursor is `(F', 0, 0)`; any outstanding
   bound is discharged, **and so is one the walk has delivered past**: a bound whose
   `R` is at or below `F'` can settle nothing the cursor does not already carry, and
   a cursor naming `R < F'` is a walk no store could have produced — `possible()`
   refuses it, so carrying it would kill the consumer at its next call.
   *(Amended by S4 review round 1, 2026-09-08: step 8's second look can deliver past
   `R`, which is how that cursor became reachable. GAP-P2-S4-1.)*
7. If the walk stopped at a gap: **mint a bound only when there is no useful
   outstanding one** — `X == 0`, or `X` already settled at step 3, or `F' >= R`.
   Otherwise carry `(X, R)` through unchanged. Minting is
   `SELECT pg_current_xact_id()`, and it never happens when a transaction of this
   backing is bound. **Neither of the two places such a bound could come from is one
   this store uses, and that is a decision, not an omission** (GAP-P2-S4-2): on the
   caller's own transaction it settles nothing, because the floor a snapshot of that
   transaction reports never passes an id the transaction itself holds — and a
   transaction that has *written* holds the floor at or below its own id in any case,
   so no bound minted after it can be passed wherever it came from; beside it, on a
   connection of the store's own pool, it would settle for a read-only caller, and it
   costs a second checkout while the caller holds one, which is how a pool at its
   limit deadlocks. At `REPEATABLE READ` the caller's snapshot is frozen and cannot
   report a floor above a bound minted after it, so that level could not settle even
   with the second connection. **The consequence, and it is what the module page must
   say in the consumer's own words: a walk on a context carrying a transaction of
   this backing stops at the first gap it meets and stays there until the same cursor
   is walked outside a transaction. Draining the log belongs outside the write.**
8. **Only when the delivered prefix is empty and a bound was just minted**, read the
   page and the floor again — **one statement, one snapshot, step 2's statement and
   not a bare floor** — and re-apply step 3 to the rows *that* snapshot returned. The
   rows and the floor of a settle are always one snapshot: a fresh floor applied to
   the rows of the earlier one declares a gap burnt while the row that fills it
   committed between the two, and the walk passes a committed position for good
   (GAP-P2-S4-1). `R` stays the **first** page's highest position — a row the second
   snapshot adds may have been drawn after the mint. In a database nobody else is
   writing the bound is already settled, the walk continues in the same call, and an
   ordinary gap costs two extra round trips rather than an empty page.
   *(Amended by S4, 2026-09-08: three round trips and not two, because a snapshot
   taken in the same statement as the mint is taken before that statement's own
   transaction id is assigned and can never report a floor above it.)*

Both doors check every scanned row against the schema's own promises before an
`event.Envelope` is built (§UC-093): `version > 0`, `position > 0`, `revision > 0`,
`octet_length(type)` in 1…`event.MaxNameBytes`, `octet_length(key)` in
1…`MaxKey`, `len(payload) <= MaxPayload`. A row outside them refuses the **whole**
page as `event.Failure(event.Unclassified, errRowOutsideSchema)` — the read door maps
an unclassified failure to `ErrBackend`, which is honest: the store read a row it
cannot classify. The offending values do not appear in the message. That a poisoned
row is also unremovable through the documented surface is backlog `## P2` §6,
`[medium]`, and `MIGRATIONS.md` carries the remedy.

### `event/eventpg/cursor.go` — D3's encoding, mint and parse

Unexported except through `ReadAll`. **No cursor type, no position type and no
outcome type is exported**: the kernel owns all three, and a store that exported its
own would be the second spelling §INV-045 exists to prevent.

### What is deliberately absent

| Not exported | Why |
|---|---|
| `Open(ctx, db, …)` | `jobspg.Open` exists for a zero-configuration development path and asks for `ManageSchema` by name. An event store's schema is history; `New` + `Prepare` is the only spelling, so nobody gets a migrating store by reaching for the short one |
| `Tail(ctx)` | a cursor at the end of the log is sound only if every position below it is settled, and no O(1) reading proves that. It costs a walk or a wait, and both belong to E2's projector. The conformance factory does not supply `Factory.Tail` either, and D4 says why: it is a hook for a log that outlives the run, and D1 gives every store value a schema of its own |
| `Spec.Clock` | `recorded_at` is `statement_timestamp()`; a `Clock` field the store then ignored would be a lie |
| `Spec.TxOptions` | none of the eight methods begins a transaction. `Migrate` does, at the session default, and a migration is DDL — there is no level at which that reads differently |
| a retry, backoff or circuit-breaker option | [[D-040]] and §UC-034 |

### Type inference at the seam

There is none to add. `event.Store` is not generic and `eventpg` declares no type
parameter: the aggregate's types live in `event.Repo[S, ID]`, one level up, and the
store sees `[]byte` and a `string` type name. That is the seam phase 1 designed and
phase 2 does not widen.

---

## Sections

Statuses: `[ ]` not started · `[~]` partial (must carry `MISSING:`) · `[x]` done,
checkpoint executed · `[!]` blocked (must carry `BLOCKED BY:`).

**Ordering rule: no section leaves the tree red.** `go build ./...` and
`go vet ./...` pass after every one, and `make unit` stays green throughout because
every live test is behind `//go:build integration`.

**`var _ event.Store = (*Store)(nil)` lands in S4**, not earlier — a store that
claimed the interface with three doors missing would need stubs, and a stub in a
non-test path is the thing this repository forbids. A method that does not exist yet
is not a stub; a method that lies is.

**Every checkpoint names its tests and counts them before running them**, because a
`-run` pattern that matches nothing is a pass: each clause is preceded by
`test "$(go test -list '<the same anchored pattern>' … | grep -c '^Test')" = <N>`.
Patterns are written `'^(A|B)$'` — `-run` and `-list` match unanchored, and
`TestClose` would otherwise also match `TestCloseIsIdempotent…` and inflate the
count past the assertion.

**And every counting arm is itself preceded by one that proves it can count.**
`go test -list` **compiles and runs the test binary**, and `TestMain` runs before
`m.Run()` handles `-test.list` — driven live: a `TestMain` that exits 1 prints
`FAIL` and no test names at all, so `grep -c '^Test'` is `0` and the `= N`
assertion fails for a reason that has nothing to do with the tests. The arm before
each count is therefore

```sh
FROSTGROVE_EVENTPG_TEST_DSN=x go test -tags=integration -list '.' ./event/eventpg/ | grep -q '^Test'
```

— the listing is non-empty *before* anything is counted out of it, so a count of
zero is never read as "the pattern matched nothing" when the truth is that the
binary never listed.

**The live gate names its own command**, because `make integration` runs `./test/...`
only and does not reach a satellite's tagged suite:

```sh
FROSTGROVE_EVENTPG_TEST_DSN='postgres://vv:vv@localhost:55432/vv?sslmode=disable' \
  go test -race -count=1 -tags=integration ./event/eventpg/...
```

**An unset DSN fails the gate. Not a skip.** `TestMain` refuses with a message naming
the variable and printing the command above. `jobspg` skips and the roadmap is
explicit that skipping is wrong here: a skipped run that prints `ok` is what phase 1's
report called evidence and was not. `TestMain` lives in `main_integration_test.go`
**behind the tag**, so `make unit` never asks for a DSN.

**`TestMain` validates the variable's presence and does no I/O.** Its whole body is
`if os.Getenv("FROSTGROVE_EVENTPG_TEST_DSN") == "" { print; os.Exit(1) }; os.Exit(m.Run())`
— no `sql.Open`, no ping, no migration, no schema, nothing that can fail against a
database. The shared schema, the scratch-schema harness and the two pools are built
by a `sync.Once` the tests call, so a value that is present and wrong fails the
**test** that needed a database, with that test's name, and every counting arm above
runs under `FROSTGROVE_EVENTPG_TEST_DSN=x` and lists normally. Registering the
counting and injecting `database/sql` drivers is a `sql.Register` in an `init` —
not I/O, and it must not be inside the `Once`, because a `-list` run registers
nothing and must still link.

---

### S1 — the module, the pure surface and the schema statements  `[x]`   *(no database)*

**Delivers** the module in the workspace, every value the store can compute without
touching a database, and the eleven migration statements as text. Nothing performs
I/O. `New` is real, `Capabilities`, `Limits`, `Backing`, `Schema`,
`SchemaManagement` and `Close` answer, and the three operation doors do not exist
yet.

**Files** `event/eventpg/go.mod`, `go.sum`, `doc.go`, `schema.go`, `config.go`,
`migration.go` (statements and `migrationLock` only — `Migrate`'s body is S2),
`testdata/fingerprint.golden`, `testdata/migration.golden`,
`testdata/migration_narrow.golden`; `go.work`; `scripts/event_test.go` (the `charged`
row). `event/eventpg/main_integration_test.go` — the `TestMain` DSN refusal and the
`sync.Once` pool — moves here from S2, because two of S1's own claims (statement
eleven refuses rather than restamps; the quoted DDL deploys under a reserved word)
are claims about PostgreSQL and cannot be proved by a package that never reaches it.

**Realises** `DefaultSchema`, `SchemaVersion`, the four `Default*` bounds, `Schema`,
`Resolved`, `Fingerprint`, `MigrationStatements`, `ErrSpec`, `ErrSchemaMismatch`,
`ErrNotReady`, `SchemaManagement` and its two methods, `Spec`, `Store`, `New`,
`Schema()`, `SchemaManagement()`, `Capabilities`, `Limits`, `Backing`, `Close`.

**Covers** UC-069 (the statements, and live that a refused run leaves nothing behind),
UC-070 (the zero resolves), UC-074 (the bound is a fingerprint input, and the
`ManageSchema` door refuses rather than restamps), INV-058, INV-059, INV-060, INV-063.

**Tests** `schema_test.go`, `config_test.go`, `sources_test.go` — all untagged:

- `TestNewRefusesEverySpecItCannotAssemble` — the six refusals, each asserting the
  message names the value set and the value it may not pass, and the control that a
  legal spec is accepted.
- `TestTheFingerprintIsTheRenderingItDigests` — the golden file byte for byte, the
  digest against a literal, and that changing `MaxPayload` by one byte changes it.
- `TestMigrationStatementsAreOrderedTransactionalDDL` — the whole list byte for byte
  against `testdata/migration.golden` and `testdata/migration_narrow.golden`; eleven
  statements in order; none containing `CONCURRENTLY`; the `schema_meta` insert
  carrying `ON CONFLICT DO NOTHING`; statement eleven carrying no write and carrying
  the raise, the `EVPG1` SQLSTATE and `IS DISTINCT FROM`; a reserved word accepted and
  never reaching PostgreSQL unquoted; and an illegal schema name refused.
- `TestTwoStoreValuesOverOneSchemaAreOneStoreAndTwoSchemasAreNot` — `Backing().Equal`
  in both directions.
- `TestNewAgainstADeadDSNAnswersAStore` — §INV-060: `New` performs no I/O.
- `TestTheStoreStartsNothingAndReadsNoEnvironment` — a `go/ast` walk over the
  package's non-test files: no `*ast.GoStmt`, no `func init`, no `log.`, no
  `fmt.Print*`, no `os.Getenv`, no package-level `var` of a mutable type.
- `TestEveryBoundParameterIsATypeEveryDatabaseSQLDriverAccepts` — the source check
  that keeps §1's claim true: every argument expression passed to `ExecContext` or
  `QueryContext` in a non-test file is typed `string`, `int32`, `int64`, `bool`,
  `time.Time` or `[]byte` by `go/types`. Its control is a fixture source in the test
  file binding a `[]string`, which must be reported.

`migration_integration_test.go` — behind `//go:build integration`, because the three
claims below are claims about PostgreSQL:

- `TestAMigrationOverASchemaItDidNotBuildRefusesRatherThanRestampingIt` — deploy at
  `MaxPayload` 1024, then run the default build's list. The re-run must fail with
  SQLSTATE `EVPG1` naming both fingerprints, `schema_meta.fingerprint` must still be
  the deployed one and `events_payload_check` must still read 1024. **Control:** the
  same build re-running its own list succeeds, moves the fingerprint nowhere and
  reissues no `log` — without it the test would pass on a list that refused everything.
- `TestARefusedMigrationLeavesTheTablesItCreatedRolledBack` — drop `events` from a
  deployed schema, run another build's list: it refuses and the table it created is
  gone, so a refusal never leaves a schema half one build's and half another's.
- `TestASchemaNamedForAReservedWordDeploys` — `user`, `table`, `select`, `order`,
  `group`, `all`, `default` and `do` each migrate and each record their own
  fingerprint. **Control:** `User`, `1events`, `events; DROP TABLE x` and `events"`
  are still refused, so quoting is not read as permission to take any name.

**Checkpoint (phase 4)**

```sh
cd <repo> \
&& test -f event/eventpg/go.mod \
&& ./scripts/checks.sh workspace \
&& go build ./event/eventpg/... && go vet ./event/eventpg/... \
&& [ -z "$(gofmt -l event/eventpg)" ]
```

**Checkpoint (phase 5)**

```sh
cd <repo> \
&& test "$(go test -list '^(TestNewRefusesEverySpecItCannotAssemble|TestTheFingerprintIsTheRenderingItDigests|TestMigrationStatementsAreOrderedTransactionalDDL|TestTwoStoreValuesOverOneSchemaAreOneStoreAndTwoSchemasAreNot|TestNewAgainstADeadDSNAnswersAStore|TestTheStoreStartsNothingAndReadsNoEnvironment|TestEveryBoundParameterIsATypeEveryDatabaseSQLDriverAccepts)$' ./event/eventpg/ | grep -c '^Test')" = 7 \
&& go test -race -count=1 ./event/eventpg/... \
&& go test -count=1 -run '^(TestNoEventPackageCostsMoreThanTheSeamItNames|TestMerelyImportingTheEventExtensionStartsNothing|TestNoBaseSubsystemDependsOnTheEventExtension)$' ./scripts/ \
&& ./scripts/checks.sh workspace && ./scripts/checks.sh replaces && ./scripts/checks.sh tidy
```

**Checkpoint (phase 5, live)** — S1's own gate, because two of its claims are about
PostgreSQL. The `-list` arm proves the binary lists before anything is counted out
of it, and the last two arms use `env -u` because the variable is exported in the
shell the gate is run from:

```sh
cd <repo> \
&& go vet -tags=integration ./event/eventpg/... \
&& FROSTGROVE_EVENTPG_TEST_DSN=x go test -tags=integration -list '.' ./event/eventpg/ | grep -q '^Test' \
&& test "$(FROSTGROVE_EVENTPG_TEST_DSN=x go test -tags=integration -list '^(TestAMigrationOverASchemaItDidNotBuildRefusesRatherThanRestampingIt|TestARefusedMigrationLeavesTheTablesItCreatedRolledBack|TestASchemaNamedForAReservedWordDeploys)$' ./event/eventpg/ | grep -c '^Test')" = 3 \
&& for pass in 1 2; do FROSTGROVE_EVENTPG_TEST_DSN='postgres://vv:vv@localhost:55432/vv?sslmode=disable' \
     go test -race -count=1 -tags=integration ./event/eventpg/ || exit 1; done \
&& ! env -u FROSTGROVE_EVENTPG_TEST_DSN go test -count=1 -tags=integration ./event/eventpg/ 2>&1 | grep -q '^ok' \
&& env -u FROSTGROVE_EVENTPG_TEST_DSN go test -count=1 -tags=integration ./event/eventpg/ 2>&1 | grep -q FROSTGROVE_EVENTPG_TEST_DSN
```

**Run, 2026-09-08** — the phase 4 and phase 5 blocks as one command, exit 0:

```
check-workspace: ok
ok  	github.com/frostgrove/vv/event/eventpg	10.423s
ok  	github.com/frostgrove/vv/scripts	1.351s
check-replaces: ok
check-tidy: ok
```

**Run, 2026-09-08** — the live block, exit 0:

```
vet integration: ok
3
ok  	github.com/frostgrove/vv/event/eventpg	11.111s
ok  	github.com/frostgrove/vv/event/eventpg	10.895s
FROSTGROVE_EVENTPG_TEST_DSN is not set, and this suite proves nothing without a
database, so it fails rather than skipping.
```

`gofmt -l .` silent, `go build ./...`, `go vet ./event/...`,
`go test -race -count=1 ./event/...` and `make check` all green; `check-deps`
reports `./event/eventpg: 0 external packages`, so pgx is a test-only import as
§the driver decision requires. `git status --porcelain -- event/
':(exclude)event/eventpg'` and the diff against `c798fc0b` over the same
pathspec are both empty, so §INV-061 holds before S5 makes the arm executable.

**Mutation evidence, 2026-09-08.** Eight mutations applied one at a time to
`migration.go`, the untagged and the tagged suite run for each, every file restored
byte-identical afterwards. Before the goldens the first six were all `ok`; now every
one of the eight is caught by both suites, and a control run with nothing mutated is
green so the failures are the mutations and not the harness:

| Mutation | Before | Now |
|---|---|---|
| every trigger created `AFTER` instead of `trigger.timing` | ok | FAIL |
| `events_are_append_only()` body → `RETURN NULL;` | ok | FAIL |
| `UNIQUE (family, key, version)` dropped | ok | FAIL |
| `FOREIGN KEY (family, key)` dropped | ok | FAIL |
| `NOT NULL` dropped from every column | ok | FAIL |
| `(INCREMENT 1 CACHE 1 NO CYCLE)` dropped from the identity | ok | FAIL |
| statement eleven back to the unconditional `UPDATE` | — | FAIL |
| the schema name emitted unquoted again | — | FAIL |
| control: nothing mutated | ok | ok |

---

### S2 — schema management: migrate, verify, prepare, check  `[x]`   *(live database)*

**Delivers** the three levels of verification, the pinned-connection migration lock,
readiness, and the harness every later section's scratch schemas come from.

**Files** `event/eventpg/migration.go` (`Migrate`'s body), `verify.go`, `catalog.go`,
`schema.go` (change 1 below) and the three `testdata` goldens it moves;
`event/eventpg/main_integration_test.go` **extended, not created** — S1 already
carries the `TestMain` refusal (presence only, no I/O), the `sync.Once` pool and the
scratch-schema harness, and S2 adds the shared schema, the store harness and the
live declaration. The `Once` is what every test that needs a database calls and
`TestMain` does not. **The `sql.Register` of the counting and injecting drivers
moves to S3**, because S3 holds their first user and a registered driver nothing
calls is test infrastructure no test measures.

**Seven contract changes S2 made, each written here before it was written in code**
(the seventh when GAP-P2-S2-1 closed):

1. **The check expressions are authored in the form PostgreSQL keeps them in**
   — `octet_length(family) >= 1 AND octet_length(family) <= 128` rather than
   `BETWEEN`. `pg_get_constraintdef` renders the expansion, measured, so a model
   that said `BETWEEN` would have to carry a second spelling of every check for
   level 3 to compare against, and the two would drift. One authored expression
   now serves the DDL, the fingerprint and the comparison. **This moved the three
   S1 goldens and the pinned digest** (`sha256:f515f520…` → `sha256:5a50f94e…`,
   and change 7 moved it again to `sha256:fadfff74…`), which is what a golden is
   for: a diff a person reads.
2. **Level 3 compares primary keys, unique constraints and foreign keys
   structurally** — `conkey` and `confkey` resolved to column names in constraint
   order — and only check constraints by definition. `PRIMARY KEY ("position")` is
   PostgreSQL's own quoting of a rendering, and comparing it as text would pin a
   rendering rather than a fact.
3. **A check definition is compared token by token**, whitespace *and*
   parentheses removed, because PostgreSQL parenthesises what it parsed.
4. **`Check` compares the log too**, out of the row it already reads. A schema
   dropped and migrated again under a running store fingerprints identically and
   every cursor that store minted is foreign, which is exactly the drift §UC-094
   asks a probe to see, and levels 1 and 2 cannot.
5. **`Migrate`, `Verify` and `Check` issue every statement on a checked-out
   `*sql.Conn`**, never on the `*sql.DB`, so §INV-053's source check in S3 stays a
   blanket rule over the package rather than a rule with an exemption.
6. **`Migrate` wraps the `EVPG1` raise in `ErrSchemaMismatch`** and names this
   build's fingerprint beside the deployed one the raise carries, so §UC-071's
   "`Prepare` returns `ErrSchemaMismatch`" holds under `ManageSchema` as well as
   under `VerifySchema`.
7. **Level 3 compares what a trigger does, not only what it is, and the two
   function bodies are model data the fingerprint digests.** GAP-P2-S2-1 drove
   four schemas past the shipped comparison: both plpgsql bodies replaced by
   `RETURN NEW` / `RETURN NULL`, the append-only trigger re-created
   `WHEN (false)`, the same trigger re-created `BEFORE UPDATE OF recorded_at`,
   and the history `SET UNLOGGED` — each verified clean while `UPDATE` on the
   history succeeded. The bodies now live on `expectation.functions`, so
   `migrationStatements` writes them, `rendering()` digests them and
   `compareTriggers` reads `pg_proc.prosrc` back through `pg_trigger.tgfoid` and
   compares them; `tgqual IS NOT NULL` and `tgattr` are read beside them, and
   `pg_class.relpersistence` is compared on all three tables because
   `Capabilities().Persistence` is published unconditionally. The DDL is
   byte-identical — the *description* of it moved, so **the pinned digest moved
   once more** (`sha256:5a50f94e…` → `sha256:fadfff74…`), and two builds whose
   function bodies differ no longer fingerprint alike.

The verification table below is delivered with **32 mutations and 4 controls**
rather than the 16 it names: ten were added at first — a check constraint that was
never validated, one that is `NO INHERIT`, a disabled trigger, a trigger running
another function, a trigger firing `AFTER`, a unique constraint over another
column, an identity dropped, a `NOT NULL` dropped, a column retyped and a column
added — and five more when GAP-P2-S2-1 closed, one per comparison branch that no
case in the original list exercised. A branch no case exercises is the
`sameValue`-could-be-`return true` failure phase 1 shipped.

**Realises** `Prepare`, `Migrate`, `Verify`, `Check`.

**Covers** UC-069, UC-070, UC-071, UC-072, UC-073, UC-074, UC-094, UC-096;
INV-048, INV-057, INV-058, INV-065 (the trigger is deployed and compared).

**Tests** `schema_integration_test.go`, `migration_integration_test.go`:

- `TestMigrationStatementsBuildTheSchemaTheFingerprintDescribes` — run the list
  against an empty scratch schema, then `Verify` passes; three tables, three
  triggers, one `schema_meta` row.
- `TestRunningTheMigrationTwiceReissuesNoLog` — §UC-069's control: the second run is
  a no-op and the `log` is byte-identical, so a re-run does not orphan every
  persisted cursor.
- `TestTheZeroSchemaManagementVerifiesAndMigratesNothing` — and its control,
  `TestPrepareAgainstAMissingSchemaRefusesBeforeAnyAppend`, which asserts zero rows
  in `pg_stat_statements`-independent terms: the schema does not exist afterwards.
- `TestVerificationRefusesEveryMutationAndPassesTheIntactSchema` — a table, one
  scratch schema per case. **Removed or altered:** missing schema; version one below;
  version one above; fingerprint mismatch; unique constraint dropped; append-only
  trigger dropped; xid-first trigger dropped; a check constraint altered; the foreign
  key dropped; the identity sequence altered to `CACHE 32`. **Added:** RLS enabled on
  `events` with a policy; a third `BEFORE INSERT` trigger on `events`; a
  `BEFORE INSERT` trigger on `streams`; a rule on `events`; a `DEFAULT` on
  `events.payload`; a child table inheriting `events`. **Added when GAP-P2-S2-1
  closed:** each trigger function replaced by one that returns; the append-only
  trigger re-created `WHEN (false)`; the same trigger re-created
  `BEFORE UPDATE OF recorded_at`; `ALTER TABLE events SET UNLOGGED`. Each refuses at
  `Prepare`, names the object, and issues no append. **Two controls that must pass:**
  the intact schema, and an *exempt* mutation — an extra non-unique index on
  `events` — because a verifier that refuses everything proves nothing about the
  ones that matter.
- `TestTwoConcurrentPreparesTakeOneLockOnOneBackend` — two goroutines,
  `ManageSchema`, one empty scratch schema, repeated enough times that an unpinned
  lock would have raced. `pg_locks` is observed **while** the migration runs: one
  granted `advisory` row; the lock, the DDL and the unlock all on one backend pid;
  one `log` afterwards; no `23505` on `pg_type_typname_nsp_index` in either caller.
- `TestASecondMigrationBlocksAgainstAHeldLock` — the control that says somebody
  waited: a `*sql.Conn` the test holds owns the lock, and the second attempt must
  block rather than proceed. A run in which nobody ever waited proves nothing about
  waiting.
- `TestTheDatabaseRefusesUpdateDeleteAndTruncateAndAdmitsInsert` — §UC-096 and its
  control, driven by a session with ordinary privileges. It carries the control the
  body comparison rests on: with `events_are_append_only` replaced by one that
  returns, the `UPDATE` lands and the stored payload is read back changed, so the
  case that refuses that schema is guarding a hole that is there.
- `TestCheckIsAReadinessAnswerAndNamesNoImportance` — nil when ready; an error when
  the schema drifted, when the pool is down, and when `Prepare` never ran; and the
  control that a torn-down database fails, so a `Check` returning nil
  unconditionally is visible.
- `TestLimitsAreTheDeployedConstraintOperands` — a payload of exactly `MaxPayload`
  accepted and one byte more refused **by the kernel before the database sees it**.

**Checkpoint (phase 4)**

```sh
cd <repo> && go build ./event/eventpg/... && go vet -tags=integration ./event/eventpg/... \
&& FROSTGROVE_EVENTPG_TEST_DSN='postgres://vv:vv@localhost:55432/vv?sslmode=disable' \
   go test -race -count=1 -tags=integration -run '^TestMigrationStatementsBuildTheSchemaTheFingerprintDescribes$' ./event/eventpg/
```

**Checkpoint (phase 5)** — the unset-DSN arm is S1's and is re-asserted here, because
S2 is the section that first makes the tagged suite large enough to hide a skip:

```sh
cd <repo> \
&& FROSTGROVE_EVENTPG_TEST_DSN=x go test -tags=integration -list '.' ./event/eventpg/ | grep -q '^Test' \
&& test "$(FROSTGROVE_EVENTPG_TEST_DSN=x go test -tags=integration -list '^(TestMigrationStatementsBuildTheSchemaTheFingerprintDescribes|TestRunningTheMigrationTwiceReissuesNoLog|TestTheZeroSchemaManagementVerifiesAndMigratesNothing|TestPrepareAgainstAMissingSchemaRefusesBeforeAnyAppend|TestVerificationRefusesEveryMutationAndPassesTheIntactSchema|TestTwoConcurrentPreparesTakeOneLockOnOneBackend|TestASecondMigrationBlocksAgainstAHeldLock|TestTheDatabaseRefusesUpdateDeleteAndTruncateAndAdmitsInsert|TestCheckIsAReadinessAnswerAndNamesNoImportance|TestLimitsAreTheDeployedConstraintOperands)$' ./event/eventpg/ | grep -c '^Test')" = 10 \
&& FROSTGROVE_EVENTPG_TEST_DSN='postgres://vv:vv@localhost:55432/vv?sslmode=disable' \
   go test -race -count=1 -tags=integration ./event/eventpg/ \
&& ! env -u FROSTGROVE_EVENTPG_TEST_DSN go test -count=1 -tags=integration ./event/eventpg/ 2>&1 | grep -q '^ok' \
&& env -u FROSTGROVE_EVENTPG_TEST_DSN go test -count=1 -tags=integration ./event/eventpg/ 2>&1 | grep -q FROSTGROVE_EVENTPG_TEST_DSN
```

The first arm is what makes the second one mean anything: `-list` runs `TestMain`,
so a `TestMain` that refused would answer `0` names and the `= 10` would fail for a
reason no test explains. The last two use `env -u` rather than a bare command,
because the variable is exported in the shell the gate is run from and a
"the DSN is unset" arm that ran with it set would pass on nothing.

**Run, 2026-09-08** — the phase 5 block, in a shell where the variable is not
exported, exit 0:

```
$ test "$(FROSTGROVE_EVENTPG_TEST_DSN=x go test -tags=integration -list '^(...)$' ./event/eventpg/ | grep -c '^Test')" = 10
10
ok  	github.com/frostgrove/vv/event/eventpg	15.730s
--- unset DSN:
FROSTGROVE_EVENTPG_TEST_DSN is not set, and this suite proves nothing without a
database, so it fails rather than skipping. Run:

  FROSTGROVE_EVENTPG_TEST_DSN='postgres://vv:vv@localhost:55432/vv?sslmode=disable' go test -race -count=1 -tags=integration ./event/eventpg/...
```

**Re-run, 2026-09-08, after GAP-P2-S2-1 closed** — the same block, exit 0, and the
tagged suite twice in a row under `-race`:

```
$ test "$(FROSTGROVE_EVENTPG_TEST_DSN=x go test -tags=integration -list '^(...)$' ./event/eventpg/ | grep -c '^Test')" = 10
ok  	github.com/frostgrove/vv/event/eventpg	15.806s
ok  	github.com/frostgrove/vv/event/eventpg	16.020s
CHECKPOINT EXIT=0
$ git status --porcelain event/
?? event/eventpg/
```

`gofmt -l .` silent, `go build ./...`, `go vet ./event/...`,
`go test -race -count=1 ./event/...` (`event`, `eventmemory`, `eventtest` all `ok`),
`make unit` and `make check` all green — `check-deps`, `check-tiers`,
`check-utils`, `check-triplets`, `check-todo`, `check-replaces`, `check-tidy`,
`check-otel-schema`, `check-workspace` each `ok`, and `check-deps` still reports
`./event/eventpg: 0 external packages`.
`git status --porcelain -- event/ ':(exclude)event/eventpg'` and
`git diff --stat c798fc0b -- event/ ':!event/eventpg'` are both empty, so
§INV-061 holds.

**Mutation evidence, 2026-09-08.** Twenty-three mutations, applied one at a time to
`verify.go`, `catalog.go`, `schema.go` or `migration.go`, the tagged suite run for
each, every file restored byte-identical afterwards (`md5sum` before and after).
The unmutated suite is green, so the failures are the mutations:

| Mutation | Caught by |
|---|---|
| `Verify` skips level 3 | 11 of the catalog cases |
| the version is compared with `>` rather than `!=` | `version one below` |
| the fingerprint is never compared | `fingerprint`, `another payload bound` |
| an unexpected trigger is not looked for | the two added-trigger cases |
| `relrowsecurity` and `relhassubclass` are not read | `rls`, `child table` |
| rules and policies are not counted | `rule` |
| a column default is not compared | `payload default` |
| the identity sequence is not read | `cache=32` |
| `sameDefinition` answers true | `check constraint altered` |
| the lock is taken on one connection and the DDL runs on another | `the lock, the DDL and the unlock are one backend` — *the advisory lock is held by [489704] and the DDL is issued by [489703]* |
| `Migrate` does not read its schema management | `the migration door itself refuses` |
| `Check` answers nil once anything verified | 4 of the readiness cases |
| the recorded log is never compared again | `migrated again under the store` |
| the append-only function returns instead of raising | all three verbs, live, and the S1 goldens |
| type, nullability, identity, constraint columns and column count are not compared | the 5 cases added for them |
| validation, trigger enablement, trigger function and trigger timing are not compared | 4 of the 5 cases added for them |
| `Prepare` does not clear readiness before it re-runs | `a store that was serving stops when a second Prepare refuses` |
| the `EVPG1` raise is not read as a schema mismatch | the same case |
| `relpersistence` is not compared | `the history is unlogged` |
| `tgattr` is not compared | `the append-only trigger watches one column` |
| `tgqual` is not compared | `the append-only trigger fires only when a condition holds` |
| `prosrc` is not compared against the model's body | both function-replaced cases |
| `rendering()` emits a function's name without its body | `a function body is an input`, untagged |

**The one branch no mutation isolates**, said out loud rather than left for a
reviewer: `connoinherit` on a check constraint. With that branch disabled the
`NO INHERIT` case still refuses, because `pg_get_constraintdef` renders
`NO INHERIT` into the definition the token comparison reads. The branch is kept —
the plan's comparison table names it and it states the fault in words — and it is
belt over braces rather than the only thing holding.

---

### S3 — the write path: the transaction question, the append, the classification  `[x]`   *(live database)*

**Delivers** the seam every statement goes through, the one-statement append, and the
rule that decides `NotWritten` from `Unconfirmed`. This is the section the two
`[high]` findings GAP-2 and GAP-3 land in.

**Files** `event/eventpg/executor.go`, `classify.go`, `append.go`.

**Realises** `Transaction`, `Append`, `onExecutor`, `outcomeOf`, `causeOf`.

**Covers** UC-076 … UC-086; INV-046, INV-047, INV-049, INV-050, INV-051, INV-052,
INV-053, INV-064.

**Six contract changes S3 made, each written here before it was written in code:**

1. **The kernel cannot be assembled around a store with two doors missing, so the
   two the section does not deliver are supplied by the tests.** `event.Store` is
   eight methods and `event.Open`/`event.Bind`/`event.Read` take nothing narrower,
   so *every* kernel-level assertion S3's own test list asks for — the receipt, the
   authority, `ErrConflict`, `ErrUncertain`, `event.CauseOf` — is unreachable until
   `ReadStream` and `ReadAll` exist. A `*Store` in a non-test file that claimed them
   would be the stub this repository forbids, so `main_integration_test.go` carries a
   `kernelStore` that embeds the real store and reads the deployed rows with the
   test's own statement. Nothing in S3 takes those two for evidence: every case that
   a read could have answered asserts the database directly, and **S4 deletes the
   type** when the store grows its own doors.
2. **§UC-086's fourth door is measured at the store's own seam in S3 and at the
   `Log` door in S4.** `Repo.Within`, `Repo.Authority`, `Repo.Load` and `Repo.Append`
   are real evidence here — the kernel routes all four through `Store.Transaction`,
   and `Repo.Load` is asserted to refuse *before* it reaches a read. The fifth
   assertion is `Store.Append` called directly, because with a `Repo` in the way an
   append through a store that never asked the question at all still reads as a
   refusal. `event.Read(...).Next` is answered by the scaffold's `ReadAll`, so what
   S3 measures at that door is the cause it carries and the zero statements behind
   it; the door's own body is S4's.
3. **`Append` refuses an `Expected` above `math.MaxInt64` as a `Conflict`.**
   `event.Version` is a `uint64` and `streams.version` is a `bigint` under
   `CHECK (version > 0)`, so no stream is ever at such a version and the append
   cannot be admitted. Driven live: without the guard the wrapped negative operand
   makes the speculative tuple fail `streams_version_check` **before** the conflict
   is resolved, and a plain stale-version conflict would answer `23514` → `ErrBackend`
   instead of `ErrConflict`. It reads no stream and decides nothing about one.
4. **A nil payload is bound as an empty `[]byte`.** `driver.DefaultParameterConverter`
   turns a nil slice into NULL and the column is `NOT NULL`, so a codec that encodes
   to nothing would fail the write rather than record a fact with nothing to carry.
5. **`sources_test.go` grows four checks, not three** — the fourth is
   `TestNoNotWrittenBranchIsUnguarded`, which the coverage matrix already names under
   §INV-064.
6. **The counting driver can also answer a row count of its own.** The
   `Unclassified` row of §append.go's table — a count that is neither `0` nor
   `len(records)`, and a `RowsAffected` that fails — is unreachable through
   PostgreSQL, so the injecting driver the section already registers writes the
   statement and answers a `driver.Result` the case chose. Without it the branch
   ships untested, which is the shape phase 1's `sameValue` had.

**Tests** `append_integration_test.go`, `transaction_integration_test.go`,
`uncertainty_integration_test.go`, and the untagged `sources_test.go` grows four
checks:

- `TestAnAppendOnThePoolIsAtomicWithNothing` — one statement, the stream row and the
  event rows together, an **invalid** authority on the receipt, and the control that
  a second append at the stale token conflicts.
- `TestEightWritersAtOneVersionLeaveOneWinnerAndSevenConflicts` — §UC-077, with the
  single writer afterwards as the control.
- `TestAnAppendJoinsTheBoundTransactionAndNothingOutsideItSees` — `crud.InNewTx`, the
  caller's own rows beside the events, a `ReadStream` in the same transaction that
  sees them, nothing outside that does, and the control that the same operation with
  no transaction writes immediately.
- `TestARollbackLeavesNoFragmentAndBurnsThePositions` — no event row, no `streams`
  row for a stream created there, no advanced version for one that existed, the next
  append's position higher, and the committed variant as the control.
- `TestTheIsolationMatrixTellsAConflictFromASerialisationFailure` — the same losing
  append inside `READ COMMITTED`, `REPEATABLE READ` and `SERIALIZABLE` callers:
  `ErrConflict` for the first, `ErrBackend` matching `crud.ErrUnavailable` for the
  other two. Each is the other's control.
- `TestAFailureInsideABoundTransactionIsNeverUnconfirmed` — the pair: the same
  failure gives `ErrBackend` inside a transaction and `ErrUncertain` on the pool. A
  store that returned one answer for both fails one of the two.
- `TestEachCancellationWindowIsAnsweredByTheRuleAndNotByOneOutcome` — (a) a context
  already cancelled on entry gives the bare sentinel and zero events; (c)
  `statement_timeout` / `pg_cancel_backend` gives `57014`, a live backend, and
  `ErrBackend`; (b) a client-side cancel asserts the **rule** by reading
  `event.CauseOf` — `ErrUncertain` when no SQLSTATE came back, `ErrBackend` when
  `57014` did, and a **failure** if the cause is neither. (b) and (c) are each other's
  controls, and the uncancelled append in the same test is the third.
- `TestAKilledBackendAnswersUncertainAfterExactlyOneExecContext` — `pg_terminate_backend`
  from a second connection during an autocommit append. `ErrUncertain`; the cause
  reachable only through `event.CauseOf`; its text absent from the rendered message;
  and **exactly one `ExecContext`** counted by a `database/sql` driver that wraps
  pgx's and counts. That count is what fails if the append is ever issued on
  `*sql.DB`.
- `TestABadConnBeforeTheSendIsOneCallAndNotWritten` — the same counter, a
  `driver.ErrBadConn` returned before the statement is written: still one call, and
  the answer is `NotWritten`, so the two are told apart by evidence rather than by
  the same answer twice.
- `TestTheRetryWithTheSameTokenResolvesTheUncertainty` — §UC-034 step 1 executed for
  real: one retry with the same token either succeeds (it had not landed) or
  conflicts (it had), and both are asserted against what the database holds.
- `TestEverySQLStateThisStoreNamesSelectsTheOutcomeTheRuleRequires` — the whole rule
  driven from **real** driver errors: `23505`, `40001`, `55P03` from a `lock_timeout`
  against a held row lock, `40P01` from a real cycle (the append holds the stream row
  and waits on another session's uncommitted event row while that session waits for
  the stream row), `57014` from `statement_timeout`, a client-side cancel, a checkout
  against a DSN that resolves nowhere, and an injected error carrying **no SQLSTATE
  at all**. The last is one control: an implementation with a `NotWritten` default
  fails it and nothing else. The connection class is the other, and it is **injected
  rather than driven**, because pgx reports a connection that broke under it as a Go
  error with no SQLSTATE and no live arrangement makes a server name `08006` or
  `08007` and then stop talking: a two-method test error carrying `SQLState() string`
  reaches the rule through `sqlfault.Extract` exactly as a driver's would. Both `08`
  cases assert `Unconfirmed` **and** that the statement's rows are in the stream, so
  the arm cannot be satisfied by an implementation that guessed. §GAP-P2-S3-1.
- `TestABatchLandsDenseAscendingAndAtOneInstant` and
  `TestTheWidestBatchTheStoreCanBuildIsAppended` — §UC-084 and its control at
  `event.MaxBatchCount` records, plus the negative control that a batch at a stale
  version writes none of its rows.
- `TestARepoAuthorityAndAReceiptCompareSameAcrossTwoWritersInOneTransaction` —
  §UC-085. The second writer is an ordinary `crud` write to a table the test creates
  on the same `*sql.Tx`; both land or neither does, `Repo.Authority(ctx)` and the
  receipt's `Authority()` compare `Same`, and the control is two operations in two
  different transactions on one pool comparing not-`Same`. **The `jobspg` half is not
  written here**: `test/` is where two published satellites are already replaced
  together, and pulling `jobs/jobspg` into `event/eventpg/go.mod` for one assertion
  buys a module dependency for a property the `*sql.Tx` identity already proves.
- `TestASavepointWritesUnderItsParentsAuthorityAndARollbackDiscardsThem` — §6.12 and
  the third of the debts Roadmap §15 hands phase 2. One `crudsql` transaction, a
  savepoint from `crud.BeginnerOf(executor)` bound with `crud.BindExecutor` (never a
  bare type assertion — [[D-061]]), and three clauses: **(i)** a receipt taken inside
  the savepoint and one taken outside the same transaction compare `Same`, because
  `savepoint.Tx()` answers the parent's `*sql.Tx` (`crudsql.go:232`) and
  `Authority.Same` compares that pointer (`event/authority.go:36`); **(ii)**
  `ROLLBACK TO SAVEPOINT` discards the events appended inside it while the parent
  stays live — `Repo.Authority(ctx)` still compares `Same`, a `ReadStream` on the
  parent no longer sees them, and an append after the rollback commits with the
  parent; **(iii)** the control, a receipt from a **second** transaction on the same
  pool comparing **not** `Same`, without which (i) would pass for a `Same` that
  always answers true.
- `TestAnAmbientNonTransactionRefusesAtAllFourDoors` — §executor.go's table, one
  bound `crudsql` executor that is not a transaction, four doors:
  `Repo.Within`/`Repo.Authority`, `Repo.Load`, `Repo.Append` answer
  `event.ErrAmbientNotTransaction`; `event.Read(store, "").Next` answers
  `event.ErrRefused` and **not** `ErrBackend` and **not** `ErrUncertain`, because
  those two are the retry classes and this never clears. `event.CauseOf` answers the
  store's one ambient-executor value at **all four**, which is what says the fourth
  door refused for the same reason as the other three rather than for one of its
  own. The **Must not** is measured rather than asserted: the counting driver reports
  **zero** `ExecContext` and `QueryContext` calls across all four, so no door fell
  back to autocommit. The control is a bound transaction on the same source serving
  all four normally.
- Source checks: `TestNoStatementIsIssuedOnTheDatabaseHandle`,
  `TestNoDoorOpensCommitsOrRollsBackAnything` (excluding `migration.go` by name and
  **failing if that file does not exist**), `TestNoStatementReadsTheStreamVersionIntoGo`,
  `TestNoNotWrittenBranchIsUnguarded`. Each carries a fixture source in the test file
  that the check must report, so none of them can pass by walking nothing.

**Checkpoint (phase 4)**

```sh
cd <repo> && go build ./event/eventpg/... && go vet -tags=integration ./event/eventpg/... \
&& FROSTGROVE_EVENTPG_TEST_DSN='postgres://vv:vv@localhost:55432/vv?sslmode=disable' \
   go test -race -count=1 -tags=integration -run '^(TestAnAppendOnThePoolIsAtomicWithNothing|TestEightWritersAtOneVersionLeaveOneWinnerAndSevenConflicts)$' ./event/eventpg/
```

**Checkpoint (phase 5)** — the whole S3 suite, **run twice in a row**, because a test
that passes once and fails on a rerun is a real defect:

```sh
cd <repo> \
&& FROSTGROVE_EVENTPG_TEST_DSN=x go test -tags=integration -list '.' ./event/eventpg/ | grep -q '^Test' \
&& test "$(FROSTGROVE_EVENTPG_TEST_DSN=x go test -tags=integration -list '^(TestAnAppendOnThePoolIsAtomicWithNothing|TestEightWritersAtOneVersionLeaveOneWinnerAndSevenConflicts|TestAnAppendJoinsTheBoundTransactionAndNothingOutsideItSees|TestARollbackLeavesNoFragmentAndBurnsThePositions|TestTheIsolationMatrixTellsAConflictFromASerialisationFailure|TestAFailureInsideABoundTransactionIsNeverUnconfirmed|TestEachCancellationWindowIsAnsweredByTheRuleAndNotByOneOutcome|TestAKilledBackendAnswersUncertainAfterExactlyOneExecContext|TestABadConnBeforeTheSendIsOneCallAndNotWritten|TestTheRetryWithTheSameTokenResolvesTheUncertainty|TestEverySQLStateThisStoreNamesSelectsTheOutcomeTheRuleRequires|TestABatchLandsDenseAscendingAndAtOneInstant|TestTheWidestBatchTheStoreCanBuildIsAppended|TestARepoAuthorityAndAReceiptCompareSameAcrossTwoWritersInOneTransaction|TestASavepointWritesUnderItsParentsAuthorityAndARollbackDiscardsThem|TestAnAmbientNonTransactionRefusesAtAllFourDoors)$' ./event/eventpg/ | grep -c '^Test')" = 16 \
&& for pass in 1 2; do FROSTGROVE_EVENTPG_TEST_DSN='postgres://vv:vv@localhost:55432/vv?sslmode=disable' \
     go test -race -count=1 -tags=integration ./event/eventpg/ || exit 1; done \
&& go test -race -count=1 ./event/eventpg/
```

**Run, 2026-09-08, after §GAP-P2-S3-1 was closed** — the phase 5 block, with the list
arm at the fifteen names the section's own gate carries (the savepoint case is listed
above and runs in the same suite), exit 0:

```
$ test "$(FROSTGROVE_EVENTPG_TEST_DSN=x go test -tags=integration -list '^(...)$' ./event/eventpg/ | grep -c '^Test')" = 15
ok  	github.com/frostgrove/vv/event/eventpg	45.272s
ok  	github.com/frostgrove/vv/event/eventpg	44.953s
ok  	github.com/frostgrove/vv/event/eventpg	36.263s
GATE EXIT=0
```

`gofmt -l .` silent, `go build ./...`, `go vet ./event/...`,
`go test -race -count=1 ./event/...` (`event`, `eventmemory`, `eventtest` all `ok`)
and `make check` all green — `check-deps`, `check-tiers`, `check-utils`,
`check-triplets`, `check-todo`, `check-replaces`, `check-tidy`, `check-otel-schema`
and `check-workspace` each `ok`, and `check-deps` still reports
`./event/eventpg: 0 external packages`, so pgx is a test-only import.
`git status --porcelain -- event/ ':(exclude)event/eventpg'` and
`git diff --stat c798fc0b -- event/ ':!event/eventpg'` are both empty, so §INV-061
holds.

**Mutation evidence, 2026-09-08.** Twenty-one mutations, applied one at a time to
`executor.go`, `classify.go` or `append.go`, the named tests run for each, every file
restored byte-identical afterwards (`md5sum` before and after). The unmutated control
run is green, so the failures are the mutations:

| Mutation | Caught by |
|---|---|
| the append is issued on the pool instead of a checked-out connection | `TestAKilledBackendAnswersUncertainAfterExactlyOneExecContext` (3 calls, not 1), `TestABadConnBeforeTheSendIsOneCallAndNotWritten`, `TestNoStatementIsIssuedOnTheDatabaseHandle` |
| the rule ends in a `NotWritten` default | `TestEverySQLStateThisStoreNamesSelectsTheOutcomeTheRuleRequires`, `TestAFailureInsideABoundTransactionIsNeverUnconfirmed`, `TestAKilledBackendAnswersUncertainAfterExactlyOneExecContext`, `TestTheRetryWithTheSameTokenResolvesTheUncertainty`, `TestNoNotWrittenBranchIsUnguarded` |
| the joined path is not proof | `TestAFailureInsideABoundTransactionIsNeverUnconfirmed` |
| nothing issued is not proof | `TestEverySQLStateThisStoreNamesSelectsTheOutcomeTheRuleRequires` (the checkout case) |
| `driver.ErrBadConn` is not proof | `TestABadConnBeforeTheSendIsOneCallAndNotWritten` |
| a session-termination code is read as a backend that survived | `TestAKilledBackendAnswersUncertainAfterExactlyOneExecContext` |
| a connection-class code is read as a backend that survived (`strings.HasPrefix(fault.SQLState, "08")` → `"zz"`) | `TestEverySQLStateThisStoreNamesSelectsTheOutcomeTheRuleRequires` — **survived the whole suite until §GAP-P2-S3-1 was closed**; now `an append answered 08006 reported "event: the store reported [outcome not written]" rather than a failure the store classified [outcome unconfirmed]`, and the same for `08007` |
| a serialisation failure is not given a retryable cause | `TestTheIsolationMatrixTellsAConflictFromASerialisationFailure`, `TestEverySQLStateThisStoreNamesSelectsTheOutcomeTheRuleRequires` |
| a deadlock and a lock timeout are not given a retryable cause (`causeOf`'s arm narrowed to `errs.CodeSerializationFailure`) | `TestEverySQLStateThisStoreNamesSelectsTheOutcomeTheRuleRequires` — **survived the whole suite until §GAP-P2-S3-1 was closed**; now `a 55P03 answered event: the store failed, where the code says the failure clears on its own and the caller may try the same append again`, and the same for `40P01` |
| zero rows is admission rather than a conflict | four of the write tests |
| admission stops asking for the version the caller decided at | `TestEightWritersAtOneVersionLeaveOneWinnerAndSevenConflicts`, `TestAnAppendOnThePoolIsAtomicWithNothing` |
| the batch takes one instant per row | `TestABatchLandsDenseAscendingAndAtOneInstant` |
| the batch is not ordered by the ordinal the caller listed | `TestABatchLandsDenseAscendingAndAtOneInstant` — **and only after the clause was pinned**: with the positions alone it was `ok`, because this planner's VALUES scan happens to hold the order the clause exists to require |
| an ambient executor that is not a transaction is nothing bound | `TestAnAmbientNonTransactionRefusesAtAllFourDoors` |
| the append door does not ask the transaction question | `TestAnAmbientNonTransactionRefusesAtAllFourDoors` — **and only after change 2's fifth assertion**: through a `Repo` the kernel refuses first, so with the four kernel doors alone it was `ok` |
| a payload of no bytes is bound as nothing | `TestAnAppendOnThePoolIsAtomicWithNothing` |
| a version above what a bigint holds is not refused | `TestAnAppendOnThePoolIsAtomicWithNothing` |
| a row count that is neither the batch nor zero is a conflict | `TestEverySQLStateThisStoreNamesSelectsTheOutcomeTheRuleRequires` |
| a row count the driver could not give is admission | `TestEverySQLStateThisStoreNamesSelectsTheOutcomeTheRuleRequires` |
| a store that never verified serves anyway | `TestAnAppendOnThePoolIsAtomicWithNothing` — **and only after a readiness case was added**: UC-075 is verified in S4 and the refusal shipped here, so S3 pins the append door's half of it |
| control: nothing mutated | `ok` |

---

### S4 — the read path: the stream, the watermark and the cursor  `[x]`   *(live database)*

**Delivers** the two read doors, the settled-watermark cursor, and the compile
assertion that the store is an `event.Store`. This is the section a naive store
passes every conformance run while losing events in production.

**Files** `event/eventpg/read.go`, `cursor.go`, the
`var _ event.Store = (*Store)(nil)` line in `config.go`, and the **deletion** of
`main_integration_test.go`'s `kernelStore` (S3 change 1), whose two methods this
section replaces with the store's own — `TestAnAmbientNonTransactionRefusesAtAllFourDoors`
keeps its name and its assertions and gets the real fourth door (backlog `## P2` §44).

**Realises** `ReadStream`, `ReadAll`, the cursor format, the settlement rule.

**Covers** UC-075, UC-087 … UC-093, UC-095, UC-098; INV-054, INV-055, INV-056,
INV-059, INV-062, INV-065.

**Five contract changes S4 made, each written here before it was written in code:**

1. **The fixed opening order is one function, `Store.opened`, in `executor.go`, and
   `Append` was re-pointed at it.** Three doors open in the same order and S3 spelled
   it out once in `append.go`; a second and a third copy is how one of them stops
   agreeing. It answers the `*readiness` as well, so the log a cursor is bound to and
   the fact that the store has verified are read once and cannot be read half-set.
   Behaviour is unchanged and S3's own tests are the proof.
2. **A read that failed after a statement was issued is `Unclassified` and never a
   bare cancellation.** A `ctx.Err()` arm on the failure path is unreachable — the
   door already refused a done context before anything was issued — so it would be a
   branch no test can falsify, and it is not written. This is also the rule `Append`
   already follows for a cancellation that arrives mid-statement (§UC-083).
3. **The two read statements are built by package-level functions, not methods.**
   `TestNoStatementReadsTheStreamVersionIntoGo` reads the text a `QueryContext` was
   given, and it can follow a plain call and not a method call; a statement it cannot
   read is reported rather than passed. So the check is what fixes the shape, and the
   `appendStatement` method stays a method because `ExecContext` is outside that arm.
4. **`ReadStream` refuses nothing above `math.MaxInt64` — it answers an empty page.**
   `event.Version` is a `uint64` and `version` is a `bigint`, so no row is at such a
   version and the page after it is the end of the stream. Binding the wrapped
   negative would make `version > $3` match every row.
5. **`main_integration_test.go`'s `kernelStore` is deleted** and
   `TestAnAmbientNonTransactionRefusesAtAllFourDoors` keeps its name and every
   assertion: the fourth door is now `event.Read(store, "").Next` over the store's own
   `ReadAll`, and the read-counter assertion is the statement counter, which measures
   the same claim without a scaffold to count it. Backlog `## P2` §44 is closed.

**Two changes review round 1 made, and they are the section's own findings closed:**

6. **The second settle reads the rows and the floor in one statement** — step 8
   re-issues `logStatement` rather than a bare
   `SELECT pg_snapshot_xmin(pg_current_snapshot())`, so no `deliverable` call ever
   pairs a floor with rows from a different snapshot. The bare statement is gone from
   the file. What the bound was minted for stays the first page's highest position.
   GAP-P2-S4-1, `[critical]`.
7. **A bound the walk has delivered past is discharged rather than carried.** The
   second look can return rows the first did not, so the walk can end above the reach
   the bound was minted for; carrying it would mint a cursor `possible()` refuses and
   every later call would answer `BadCursor`. Falls out of change 6 and is pinned by
   its own case.

**And one the review offered that was rejected, with the reason measured rather than
argued:** minting the bound on a connection of the store's own pool when a
transaction is bound (GAP-P2-S4-2, `[high]`). It settles nothing for a caller's
transaction that has written — that transaction holds the cluster's floor at or below
its own id — and for one that has not it buys settlement with a second connection
checked out while the caller holds one, which a pool at its limit answers with the
caller's deadline. A walk inside a transaction therefore stops at the first gap it
meets and stays there, by decision; three live cases pin it, and the module page (S5)
says it in the consumer's own words.

**Tests** `read_integration_test.go`, `cursor_integration_test.go`,
`watermark_integration_test.go`:

- `TestAStreamPagesToItsEndAndFoldsTheSameThroughASibling` — pages of exactly
  `StreamPage` until a short page ends it, dense versions from `after+1`, positions
  ascending with versions, and the control that the same replay at `StreamPage = 1`
  yields the same state.
- `TestAWalkFromTheEmptyCursorTilesWithAWalkFromTheTail` — the first-time consumer,
  the resumed walk in a second process-equivalent store value, and the tiling
  assertion: concatenated, the two cover every position exactly once. The control is
  that a cursor from a **different schema in the same database** is `ErrCursor`,
  which a `*sql.DB`-only backing identity would have accepted.
- `TestEveryUnreadableCursorIsRefusedAndTheEmptyOneIsTheOrigin` — a foreign log, a
  non-format value, a retired tag: `ErrCursor` for all three, and the walk does not
  restart from the beginning. The control is the pair — a cursor from this log
  resumes, and `""` resumes from the origin — so the refusal has to discriminate
  rather than be universal.
- `TestAWalkDoesNotPassAPositionAWriterCouldStillCommit` — §UC-089: writer A holds an
  uncommitted append at `p`, writer B has committed `p+1`; nothing at or beyond `p`
  is delivered and the cursor has not passed `p`; after A commits the next walk
  delivers `p` then `p+1`. The control is that with no writer in flight the same walk
  delivers immediately, so a store that stalls forever fails. **And the window
  itself** (round 1, GAP-P2-S4-1): a `database/sql` driver that commits the writer
  holding `p` when it sees the mint statement, so the commit lands between the walk's
  two looks at the log — every position the table holds is delivered by that call or
  a later one.
- `TestABurntGapIsPassedInsideOneReadAllAndALiveOneIsNot` — §UC-090: the rolled-back
  position between two committed ones is passed inside one `ReadAll` (three round
  trips — the read, the mint, the second read — and no empty page), and the same
  log walked while a writer holds an id stops at the gap and stays stopped. The pair is what shows the settlement rule does work
  rather than always saying yes. The third case is the second look **growing**: two
  positions and a burnt one arrive between the two reads, the walk delivers past the
  reach its bound was minted for, and the cursor it answers is one the next call can
  still read (round 1, the cursor half of GAP-P2-S4-1).
- `TestTheXminEqualsXmaxRuleFailsTheInFlightCase` — the negative control §INV-054
  makes non-optional: a decorator settling on `pg_snapshot_xmin = pg_snapshot_xmax`
  must **fail** the in-flight case, because that rule was measured false on
  PostgreSQL 17.9.
- `TestAWalkInsideATransactionMintsNoBoundAndSettlesNoGap` — §UC-092, with
  `pg_current_xact_id_if_assigned()` asserted null on the caller's transaction
  afterwards, and the control that the same walk outside a transaction settles and
  advances. Round 1 (GAP-P2-S4-2) added the three cases that turn step 7's choice from
  a side effect into a decision: **the permanence** — three successive walks over a
  log with one burnt position, each in its own bound transaction and the third at
  `REPEATABLE READ`, all answering an empty page and the origin cursor, with the
  control that the very cursor they answered advances outside a transaction; **why
  the caller's own transaction is not the place** — a bound minted beside a writing
  transaction is never passed by the floor that transaction's own snapshot reports,
  measured, and the read-only half carries no id at all; and **what the second
  connection costs** — a pool at its one-connection limit answers the checkout with
  the caller's deadline, with the control that the same checkout on a pool with a
  spare connection serves.
- `TestAForeignWriterIsNotSkippedAndTheOverridingWriterIs` — §UC-098 with both
  controls. **(a)** a test-only `BEFORE INSERT … FOR EACH ROW` trigger in a scratch
  schema reports `pg_current_xact_id_if_assigned()` for a foreign `INSERT` from a
  transaction that has written nothing else: not null with the store's trigger, null
  with it dropped — the premise measured directly, with no timing window.
  **(b)** the `SELECT nextval(...)` then `INSERT … OVERRIDING SYSTEM VALUE` writer
  **is** skipped, so the boundary of the claim is a test result and not a caveat.
  *(Amended by S4, 2026-09-08: `nextval()` assigns a transaction id — measured — so
  that writer is skipped only when it drew the position in a transaction that has
  already **ended**. Drawn and inserted inside one transaction it is covered, and
  the test asserts both halves. Backlog `## P2` §54.)*
- `TestARowOutsideTheSchemasPromisesRefusesTheWholeRead` — a type name over 128
  bytes, a revision of 0, an oversized payload and an oversized key, each planted in
  a scratch schema with the check constraint dropped; the envelope is never built and
  the offending values are absent from the message. The control is that the valid
  rows beside them read normally.
- `TestAnUnpreparedStoreRefusesEveryOperationAndAnswersTheThreePureOnes` — §UC-075,
  with the control that after a successful `Prepare` the same calls serve.
- `TestCloseIsIdempotentAndClosesNothingItDidNotOpen` — §UC-095, with a second store
  value over the same backing still serving as the control.
- `TestEveryErrorTheEightMethodsProduceIsNilAContextErrorOrAFailure` — §INV-062,
  walked over every reachable error.

**Checkpoint (phase 4)**

```sh
cd <repo> && go build ./event/eventpg/... \
&& grep -q 'var _ event.Store = (\*Store)(nil)' event/eventpg/config.go \
&& FROSTGROVE_EVENTPG_TEST_DSN='postgres://vv:vv@localhost:55432/vv?sslmode=disable' \
   go test -race -count=1 -tags=integration -run '^TestAStreamPagesToItsEndAndFoldsTheSameThroughASibling$' ./event/eventpg/
```

**Checkpoint (phase 5)**

```sh
cd <repo> \
&& FROSTGROVE_EVENTPG_TEST_DSN=x go test -tags=integration -list '.' ./event/eventpg/ | grep -q '^Test' \
&& test "$(FROSTGROVE_EVENTPG_TEST_DSN=x go test -tags=integration -list '^(TestAStreamPagesToItsEndAndFoldsTheSameThroughASibling|TestAWalkFromTheEmptyCursorTilesWithAWalkFromTheTail|TestEveryUnreadableCursorIsRefusedAndTheEmptyOneIsTheOrigin|TestAWalkDoesNotPassAPositionAWriterCouldStillCommit|TestABurntGapIsPassedInsideOneReadAllAndALiveOneIsNot|TestTheXminEqualsXmaxRuleFailsTheInFlightCase|TestAWalkInsideATransactionMintsNoBoundAndSettlesNoGap|TestAForeignWriterIsNotSkippedAndTheOverridingWriterIs|TestARowOutsideTheSchemasPromisesRefusesTheWholeRead|TestAnUnpreparedStoreRefusesEveryOperationAndAnswersTheThreePureOnes|TestCloseIsIdempotentAndClosesNothingItDidNotOpen|TestEveryErrorTheEightMethodsProduceIsNilAContextErrorOrAFailure)$' ./event/eventpg/ | grep -c '^Test')" = 12 \
&& for pass in 1 2; do FROSTGROVE_EVENTPG_TEST_DSN='postgres://vv:vv@localhost:55432/vv?sslmode=disable' \
     go test -race -count=1 -tags=integration ./event/eventpg/ || exit 1; done
```

**Run, 2026-09-08, after round 1's two findings were closed**, the phase 5 block
exactly as written above, exit 0:

```
$ cd <repo> && go build ./event/eventpg/... && grep -q 'var _ event.Store = (\*Store)(nil)' event/eventpg/config.go \
  && FROSTGROVE_EVENTPG_TEST_DSN=x go test -tags=integration -list '.' ./event/eventpg/ | grep -q '^Test' \
  && test "$(FROSTGROVE_EVENTPG_TEST_DSN=x go test -tags=integration -list '^(...twelve names...)$' \
       ./event/eventpg/ | grep -c '^Test')" = 12 \
  && for pass in 1 2; do FROSTGROVE_EVENTPG_TEST_DSN='postgres://vv:vv@localhost:55432/vv?sslmode=disable' \
       go test -race -count=1 -tags=integration ./event/eventpg/ || exit 1; done
ok  	github.com/frostgrove/vv/event/eventpg	46.266s
ok  	github.com/frostgrove/vv/event/eventpg	46.521s
EXIT=0
```

The twelve names still count 12: the four cases round 1 added are subtests of the
tests that already own their use cases, so the checkpoint's arithmetic is unchanged.
The head-gap round-trip assertions still hold at their stated counts — two queries
for the gap between two committed positions, three for the gap at the head — because
the second look is the same statement the first one was, not an extra one.

`gofmt -l .` silent, `go build ./...`, `go vet ./event/...`,
`go test -race -count=1 ./event/...` (`event`, `eventmemory`, `eventtest` all `ok`)
and `make check` (`check-deps`, `check-tiers`, `check-utils`, `check-triplets`,
`check-todo`, `check-replaces`, `check-tidy`, `check-otel-schema`, `check-workspace`)
all green. `git status --porcelain event/` is `?? event/eventpg/` and nothing else.

**Mutation evidence, 2026-09-08.** Sixteen mutations, applied one at a time to
`read.go`, `cursor.go` or `executor.go`, the named tests run for each, every file
restored byte-identical afterwards (`diff -q` before and after). The unmutated
control run is green, so the failures are the mutations:

| Mutation | Caught by |
|---|---|
| `deliverable` hands over every row the query returned (no settlement at all — the naive `position > cursor` store) | `TestAWalkDoesNotPassAPositionAWriterCouldStillCommit`: *the walk delivered [2] while a writer still held position 1* |
| `settledAt` settles unconditionally | the same |
| the bound is minted for the highest **delivered** position rather than the highest the query returned | `TestABurntGapIsPassedInsideOneReadAllAndALiveOneIsNot`: *the walk beyond the gap answered [], and a walk that stops for good at a gap that can never be filled never finishes* — the §9 stall, reproduced |
| `spent` is always true, so a bound is minted on every pass over one gap | `TestAWalkDoesNotPassAPositionAWriterCouldStillCommit`: *the cursor moved from {from:0 bound:73013 reach:2} to {from:0 bound:73014 reach:2} over a gap nothing settled* — the §10 burn, reproduced |
| step 8, the second snapshot, is removed | `TestABurntGapIsPassedInsideOneReadAllAndALiveOneIsNot`: the head-gap page comes back empty |
| **step 8 settles a fresh floor against the rows the first statement returned** (the code as round 1 reviewed it) | `TestAWalkDoesNotPassAPositionAWriterCouldStillCommit/a writer that commits between the mint and the second look is not skipped`: *the walk delivered [2] where the table holds [1 2], and a committed position a walk passed is never read by that consumer again*; `TestABurntGapIsPassedInsideOneReadAllAndALiveOneIsNot/rows that arrive between the two looks…` also failed |
| **a bound is carried into the cursor after the walk delivered past its reach** | `TestABurntGapIsPassedInsideOneReadAllAndALiveOneIsNot/rows that arrive between the two looks are delivered and the cursor stays readable`: *a walk of the log answered event: the store reported [outcome bad cursor]* |
| **a bound is minted on a connection of the store's own pool when a transaction is bound** (the alternative round 1 offered) | `TestAWalkInsideATransactionMintsNoBoundAndSettlesNoGap`: *the walk inside the transaction delivered [after the burnt head]…* — and with that assertion neutered, the new `three walks, each in its own transaction, all stop at the same burnt gap`: *walk 1 inside a transaction delivered [after the burnt head], and a store that mints a bound outside the caller's transaction is what passes this gap* |
| a bound is minted inside the caller's transaction | `TestAWalkInsideATransactionMintsNoBoundAndSettlesNoGap`: *the cursor minted inside a transaction carries the bound 73056* |
| a walk inside a transaction runs on the pool | the same: *the walk inside the transaction delivered [rolled back after the burnt head]* |
| `promised` takes every row the database hands over | `TestARowOutsideTheSchemasPromisesRefusesTheWholeRead`, all four planted rows |
| every cursor reads as the origin | `TestAWalkFromTheEmptyCursorTilesWithAWalkFromTheTail`: the walk never runs out |
| the empty cursor is refused like any other | `TestEveryUnreadableCursorIsRefusedAndTheEmptyOneIsTheOrigin` |
| the log a cursor names is not compared | the same: the other schema's cursor is admitted |
| the cursor carries a per-value nonce in the log field | `TestAWalkFromTheEmptyCursorTilesWithAWalkFromTheTail` |
| the stream page `LIMIT` is one above the published page | `TestAStreamPagesToItsEndAndFoldsTheSameThroughASibling`: *read as pages of [5 4 0]* |
| a store that verified nothing serves | `TestAnUnpreparedStoreRefusesEveryOperationAndAnswersTheThreePureOnes` |
| a closed store serves | `TestCloseIsIdempotentAndClosesNothingItDidNotOpen` |
| control: nothing mutated | `ok` |

**And one mutation that survived, which is why change 2 is in the list.** An arm
returning the bare `ctx.Err()` from the read failure path survived every test,
because the door refuses a done context before it issues anything and no test can
reach the arm. It was removed rather than pinned.

---

### S5 — the conformance run, the mutation harness, the checks and the documentation  `[x]`   *(live database)*

**Delivers** phase 2's central evidence and everything that makes it mean something.

**Files** `event/eventpg/conformance_integration_test.go` (the factory and the two
runs), `mutation_integration_test.go` (the harness and its subprocess),
`census_integration_test.go` (what each run must report, and the withdrawn hooks
that prove the census counts it), `audit_integration_test.go`;
`event/eventpg/MIGRATIONS.md`;
`scripts/checks.sh` + `scripts/vv` + `Makefile` (the `check-event-kernel` arm);
`scripts/checks_test.go` (the arm's self-test); `docs/modules/en/eventpg.md`,
`docs/modules/ru/eventpg.md` and both `Index.md` rows;
`docs/ai/flows/FL-037-a-recorded-fact-becomes-a-postgresql-row.md` and its three
entries in `docs/ai/flows/Index.md`; the `UC-032` row in
`docs/ai/usecases/Index.md`; `docs/roadmaps/Roadmap.md` §15; two decision docs and
their `Index.md` rows; `docs/api/surface.md`.

**Covers** UC-085 (the cross-subsystem half is recorded, not written here), UC-094,
UC-097; INV-047 (the audit), INV-055, INV-061, INV-062, INV-063.

**Six contract changes S5 made, each written here before it was written in code:**

1. **The per-store pool is `SetMaxOpenConns(6)`, not 2.** D1 says two; the
   `crossed` case of the `transactions` section holds **three** live transactions
   of one store and issues a load on the pool beside them
   (`sections_transactions.go:120-139`), so a pool of two blocks on the third
   `Begin` until the section window expires. Six, with `SetMaxIdleConns(1)`
   because a conformance run holds about twenty pools open at the end of
   `store failure classification` and this cluster admits a hundred connections.
2. **The `newest position` mutation is the newest position of the LOG, not of the
   page window.** Written the other way first — a cursor past the gap the page
   window stopped at — it **passed all twenty sections**, because no section walks
   the log while a lower position is uncommitted: `lateWriter` commits before the
   section walks (`sections_resumption.go:108-131`). That is a real defect in the
   suite and it is reported rather than worked around — backlog `## P2` §59,
   `[high]`, owner the phase that unfreezes the kernel — and the shipped mutation
   is the shape `eventtest`'s own inventory pairs with `resumption`
   (`defects.go:59-60`, `unsafeCursors`): a cursor past rows the bounded page did
   not deliver, which `resumption` does report.
3. **The audit runs in three places, because "the whole schema" names no schema
   under D1.** Every conformance schema is audited by the factory's own
   `t.Cleanup` **before** it is dropped, so the queries face the concurrency
   section's load; `TestTheAuditOverTheWholeSchemaHolds` drives its own contended
   load — twelve writers over three streams, a rolled-back transaction and a
   losing append — into a schema of its own and audits that; and it audits the
   shared schema every other case of the live suite wrote to. Its three planted
   violations are what make the queries non-vacuous.
4. **The mutation subprocess runs `TestTheStoreSatisfiesTheContractAtNarrowerLimits`.**
   The narrow limits reach every paging boundary in a fraction of the writes —
   `payload ownership` alone appends 257 events at the defaults and 5 at the
   narrow ones — so seven subprocess runs cost about nine seconds. The defaults
   are exercised by the parent process's own run. Backlog `## P2` §60, `[low]`.
5. **The harness asserts the named section reported `failed`, not that it was the
   only one.** All twenty sections run whatever any of them reports, so a mutation
   that breaks the store early fails several; the assertion that carries the claim
   is that the *named* one is among them, and the message prints every section
   that failed when it is not.
6. **The verdict census is a test and both harness inventories are counted.**
   Written after GAP-P2-S5-1 drove the hole: the census lived only in this
   document's checkpoint block, so a `Factory` hook that stopped answering
   downgraded a section and the whole tagged suite stayed green — measured. It is
   now `census_integration_test.go`, over **both** runs rather than the wide one
   alone, with `EVENTPG_DOWNGRADE` as its own falsification. In the same change
   `defects()` is counted — a plain slice whose length nothing asserted, so an
   emptied one ran the control, looped zero times and reported `PASS` — and so is
   `downgrades()`, by the same `sized` helper with the same one-row-shorter
   control `eventtest`'s own inventory test uses.

**The factory**, and every hook is real:

```go
eventtest.Factory{
	New:        // D1 — a *failing over a real *eventpg.Store over its OWN pool and its own
	           //      freshly migrated scratch schema; t.Cleanup drops the schema and closes the pool
	Begin:      // crudsql.DB.Begin(ctx) at READ COMMITTED, bound with crud.BindExecutor; crud.Tx already is eventtest.Tx
	Sibling:    // a second *eventpg.Store over that store's own *sql.DB and schema — the equal backing
	Fail:       // D1's table — all seven outcomes from real store values and real driver errors
	Unparsable: // "neither this store's format nor anybody's"
	Window:     // 30s — the operations are a network away
	// Tail:    // deliberately absent — D4
}
```

**The two runs.** `TestTheStoreSatisfiesTheContract` at the defaults, and
`TestTheStoreSatisfiesTheContractAtNarrowerLimits` at `Schema{MaxKey: 40}` +
`Spec{StreamPage: 4, MaxBatch: 2, MaxRead: 3}` (D7 — the narrow schema is migrated
per store value like every other one, so nothing special is needed for it any more).
**No line of `event/eventtest` changes.**

**What each run is expected to report, written down before it is run**, so a
verdict list nobody read is not the evidence: **nineteen sections `passed` and one
`not certified`** — `monotone visibility`, with the store's own reason, because
§2.6 declines that claim. In particular `store failure classification` is expected
**`passed`**, all eight cases through both the store and the wrapping decorator,
because D1's `Unconfirmed` row is real; and `durability`, `shared backing`,
`resumption` and `transactions` are all expected `passed`, the first two through
`Sibling` and the last two through `New`'s second backing.

**The census is a test, not a shell line and not a person's eye over twenty log
lines** (S5 change 6, closing GAP-P2-S5-1). `eventtest.Run` calls `t.Error` for a
section that **failed** and only `t.Log` for one that is **not certified**, so a
Factory hook that stops answering downgrades a whole section, every case in it
stops running, and `go test` prints `ok`.
`TestBothConformanceRunsCertifyEverySectionButTheOneThisStoreDeclines` is what
reports that instead: it drives **both** runs as subprocesses of the test binary,
reads their own verdict lines back, and asserts the twenty-row table in
`census_integration_test.go` — section by section, reason included for the
declined one. A downgraded section, a section reported twice, a section that
reported nothing because a hook left through `t.Fatal`, and a suite that grew or
lost a section are all red.

**And the census is itself falsifiable**, because a census that counts nothing
looks exactly like one that counts:
`TestADowngradedSectionIsCaughtByTheCensusAndByNothingElse` withdraws three hooks
the factory really supplies — `Fail` removed, `Fail` answering `false` for
`Unconfirmed` (`wayTo` losing its counting-driver arm), `Unparsable` removed —
through `EVENTPG_DOWNGRADE`, and asserts of each that the run stays **green** and
the census goes **red**. A row that turns the run red would prove nothing about
the census, so that is a failure too.

**The mutation harness — the reason a green conformance run is evidence at all.**
`eventtest` detects 27 of 165 of its own section-level assertions (backlog P1 §1), so
"eventpg passes eventtest" is weak on its own. Six decorators over the **real** store,
each run through `eventtest.Run` in a **subprocess** (the `TestHelperProcess` idiom,
selected by `EVENTPG_MUTATION`), asserting a non-zero exit and that the named section
is the one that reported it:

| Mutation | Section that must catch it |
|---|---|
| `ReadStream` drops the last envelope of a full page | `stream paging` |
| `ReadAll` reverses its page | `global order` (`Reader.checkPage`) |
| `Append` admits at `Expected - 1` | `expected version` |
| the cursor is the newest position rather than the watermark | `resumption` |
| one payload buffer reused across two calls | `payload ownership` |
| an envelope carries a stream that was not the one read | `stream identity` |

**A mutation the suite does not catch is a `[high]` finding against the suite**, not a
pass: it is reported in words and recorded in the backlog under `## P2` with the
section it should have been caught by. The same harness runs the suite **with no
DSN** and asserts the failure and its message (§6.14). The inventory's size is
asserted with a one-row-shorter control, and the number of mutations actually
driven and caught is counted, because a loop over a slice nobody counted runs as
many times as the slice is long and reports `PASS` for zero.

**The audit.** `TestTheAuditOverTheWholeSchemaHolds` runs after the conformance runs
and asserts over the whole schema: `streams.version = max(events.version)` for every
stream; no event row without a stream row; and per stream, position order equals
version order. A two-statement implementation fails it under the concurrency
section's load.

**The upcast case.** `TestStoredRowsAreUpcastAndNeverRewritten` — write revision-1
payloads through a declaration with one reader, load the same streams through a
declaration with two readers and an upcaster, assert the folded state and that the
stored bytes were not rewritten, with a v1 load of the same rows as the control.

**Checkpoint (phase 4 and 5 are one block here — the section is its own evidence)**

```sh
cd <repo> \
&& FROSTGROVE_EVENTPG_TEST_DSN=x go test -tags=integration -list '.' ./event/eventpg/ | grep -q '^Test' \
&& test "$(FROSTGROVE_EVENTPG_TEST_DSN=x go test -tags=integration -list '^(TestTheStoreSatisfiesTheContract|TestTheStoreSatisfiesTheContractAtNarrowerLimits|TestTheConformanceSuiteCatchesADefectiveStore|TestTheGateFailsWhenTheDSNIsUnset|TestTheAuditOverTheWholeSchemaHolds|TestStoredRowsAreUpcastAndNeverRewritten)$' ./event/eventpg/ | grep -c '^Test')" = 6 \
&& ./scripts/checks.sh event-kernel \
&& for pass in 1 2; do FROSTGROVE_EVENTPG_TEST_DSN='postgres://vv:vv@localhost:55432/vv?sslmode=disable' \
     go test -race -count=1 -tags=integration ./event/eventpg/ || exit 1; done \
&& make unit && make vet && [ -z "$(gofmt -l .)" ] && make check \
&& go test -count=1 ./scripts/ \
&& make api && git diff --stat -- docs/api/surface.md
```

The zero-diff arm is `./scripts/checks.sh event-kernel` and not a hand-rolled
`git diff --quiet`: with no revision argument that compares the index to the working
tree, so a **staged** edit to `event/store.go` and an **untracked** new file under
`event/` both pass it. Running the real arm here is what makes this line say what it
claims (backlog `## P2` §19 named the hole; running the arm costs nothing and closes
it in passing).

**There is no `passed`-count arm here any more, and its absence is the fix rather
than a loss.** The count used to be a `grep -c 'eventtest: .*: passed' | grep -qx
19` line in this block, which is to say it lived in a document, ran when somebody
ran this block by hand, covered only the wide run, and was reachable from no
`go test`, `make unit`, `make check` or CI entry point — GAP-P2-S5-1, driven: a
`Factory` hook withdrawn left eighteen `passed` lines, one `not certified`, and
the whole tagged suite green. The census now runs inside the tagged suite the two
lines above already execute, over both configurations, with its own falsification
beside it, so the arm this block would carry would be the weaker copy of a test.

`make api` is run and its diff is **read by a person** — nothing checks
`docs/api/surface.md` and nothing should.

**Run, 2026-09-08, after §GAP-P2-S5-1 was closed** — the block above, exit 0, with
the two runs of the tagged suite under `-race`. They are longer than the 65 s the
first run took because the census and its three withdrawn hooks are five more
subprocess conformance runs:

```
list arm: ok
6
check-event-kernel: ok
ok  	github.com/frostgrove/vv/event/eventpg	78.521s
ok  	github.com/frostgrove/vv/event/eventpg	78.832s
make unit: every module ok
make vet: every module ok
check-deps: ok        check-tiers: ok       check-utils: ok
check-triplets: ok    check-todo: ok        check-replaces: ok
check-tidy: ok        check-otel-schema: ok check-workspace: ok
check-event-kernel: ok
ok  	github.com/frostgrove/vv/scripts	5.705s
api: docs/api/surface.md regenerated — read the diff
 docs/api/surface.md | 111 +++++++++++++++++++++++++++++++++++++++++++++++++++-
 1 file changed, 109 insertions(+), 2 deletions(-)
CHECKPOINT EXIT=0
```

`check-deps` still reports `./event/eventpg: 0 external packages`, so pgx is a
test-only import — the census adds no import the module did not already carry.
`gofmt -l .` is silent, `go build ./...` and `go vet ./event/...` are clean,
`go test -race -count=1 ./event/...` is `ok` for `event`, `eventmemory` and
`eventtest`, and `git status --porcelain event/` is `?? event/eventpg/` and
nothing else. The surface baseline is **unchanged by S5 change 6**: the census is
a test file and exports nothing, so the diff is the same 109/2 the first run
read.

**What the surface diff says, read by a person.** 109 lines added, 2 removed.
`## github.com/frostgrove/vv/event/eventpg` is new and is exactly the surface
§Contracts before code fixes: `DefaultSchema` and the constants, the three
sentinels, `MigrationStatements`, `Schema`, `SchemaManagement`, `Spec`, `Store`
and `New`, and nothing else. The rest of the diff is **not this section's**: the
baseline had never been regenerated since phase 1, so it also gains
`## github.com/frostgrove/vv/event`, `/eventmemory` and `/eventtest`, and the
cache, jobs, storage, tenancy and auth additions those phases made. Two lines
disappear — `jobs.ScheduleOverlap` and its constant — which is a phase-1 jobs
change and not phase 2's; it is named here because a line that disappears is what
a person is meant to read this file for.

**Expected verdicts, and what was measured.** Nineteen sections `passed` and one
`not certified` — `monotone visibility`, "this store does not promise monotone
visibility" — at both configurations. `store failure classification` is
**`passed`**, all eight cases through both the store and the wrapping decorator,
so D1's seven real outcomes hold and §INV-062's evidence row stands.
`durability`, `shared backing`, `resumption` and `transactions` are all `passed`.

**Mutation evidence, 2026-09-08.** Applied one at a time, the named test run for
each, every file restored byte-identical afterwards (`md5sum -c` before and
after). The unmutated runs above are the control.

| Mutation | Caught by |
|---|---|
| `TestMain` exits 0 instead of 1 when the DSN is unset | `TestTheGateFailsWhenTheDSNIsUnset` |
| the version audit's `HAVING` → `false` | `TestTheAuditOverTheWholeSchemaHolds/a stream whose version outran its events` |
| the orphan audit's `WHERE NOT EXISTS` → `false` | `…/an event row outside every stream` |
| the order audit's `position <= previous` → `false` | `…/a stream whose positions do not follow its versions` |
| `storedEvent.envelope` reports `revision + 1` | `TestStoredRowsAreUpcastAndNeverRewritten` — *three v1 credits fold through the upcaster to {Balance:6 Currency:}* |
| `check_event_kernel` reduced to `echo ok` | three of the four self-test cases |
| its `git status` arm removed | the untracked-file case alone |
| its pathspec widened to all of `event/` | the `only event/eventpg differs` case alone |
| its baseline-resolution arm removed | the unresolvable-baseline case alone |
| six store defects, one per subprocess run | `stream paging`, `global order`, `expected version`, `resumption`, `payload ownership`, `stream identity` — the harness itself, with an unmutated subprocess run as its control |
| `wayTo`'s `event.Unconfirmed` arm → `return nil` | `TestBothConformanceRunsCertifyEverySectionButTheOneThisStoreDeclines`, **both** subtests, and nothing else in the tagged suite |
| `defects()` → `return nil` | `TestTheConformanceSuiteCatchesADefectiveStore` — *this harness holds 0 store defects where it names 6* |
| `certifies` → `return nil` | `TestADowngradedSectionIsCaughtByTheCensusAndByNothingElse`, all three rows |
| one row deleted from `census()` | `TestBothConformanceRunsCertifyEverySectionButTheOneThisStoreDeclines`, both subtests — *a run reported 20 sections where this store is certified on 19* |

**The census mutation in full**, because it is the one GAP-P2-S5-1 was driven on
and the one the suite used to certify in silence. With `wayTo`'s `Unconfirmed`
arm returning `nil` — what a moved driver `init`, an `arm` that stopped firing or
a "simplified" hook leaves behind — the conformance run reports:

```
eventtest: store failure classification: not certified — [outcome unconfirmed] cannot be produced by this store
--- PASS: TestTheStoreSatisfiesTheContract (1.35s)
ok  	github.com/frostgrove/vv/event/eventpg	1.352s
```

eighteen `passed` lines instead of nineteen, `--- PASS`, `ok`. Before S5 change 6
the whole tagged suite was green with that edit in place. After it, the same edit
gives:

```
--- FAIL: TestBothConformanceRunsCertifyEverySectionButTheOneThisStoreDeclines (5.68s)
    --- FAIL: …/at_this_store's_own_limits (2.85s)
        census_integration_test.go:80: the conformance run at this store's own limits is not the run
        this store is certified by: the store failure classification section was reported "not
        certified — [outcome unconfirmed] cannot be produced by this store" where a certified run
        reports "passed"
    --- FAIL: …/at_narrower_ones (2.83s)
FAIL	github.com/frostgrove/vv/event/eventpg	76.964s
```

and that test is the **only** one in the tagged suite that reports it, which is
the claim: the census is what catches a downgrade, and nothing else was ever
going to.

**And one mutation the suite did not catch, reported rather than worked around.**
The `newest position` decorator written as D1's row literally reads — a cursor
past the gap the **page window** stopped at — passed all twenty sections,
measured. No section walks the log while a lower position is uncommitted, so that
defect is invisible to `eventtest`. It is a `[high]` against the suite, recorded
as backlog `## P2` §59 with the owner named, and the shipped mutation is the
shape `eventtest`'s own inventory pairs with `resumption`. S5 change 2.

---

## The module checklist

A new module is not finished when it compiles. Phase 1 left thirteen of fourteen
non-test source files under `event/` with no flow row (backlog P1 §8); this table is
what stops that repeating, and every row names what catches it.

| # | Deliverable | Section | What catches it if it is missing |
|---|---|---|---|
| 1 | `event/eventpg/go.mod` — `module github.com/frostgrove/vv/event/eventpg`, `go 1.26.6`, `require github.com/frostgrove/vv` and `github.com/jackc/pgx/v5 v5.10.0` (the version `jobspg` already carries), **no `replace` of the library** | S1 | `make check-replaces` refuses a satellite that replaces the library; `make check-tidy` refuses an untidy one |
| 2 | `event/eventpg/go.sum` | S1 | `check-tidy` |
| 3 | the `./event/eventpg` line in `go.work`'s `use` block, in sorted position | S1 | **`make check-workspace`** — `workspace_modules` is every `go.mod` except `./_examples`, so a missing line is a hard failure |
| 4 | `test/go.mod` — **no replace line, and that is the decision, not an omission.** `check-replaces` demands nothing; it only refuses a replace that names a directory the repository does not carry. `jobs/jobspg` is the precedent and appears nowhere in `test/` or `_examples/`. A `require` nothing imports is stripped by `go mod tidy`, so adding one makes `make check-tidy` red. A replace line is added **in the change that adds the first import**, which would be the `jobspg` half of §UC-085 | S5 (recorded) | `check-tidy` goes red if a require is added with no import |
| 5 | `_examples/go.mod` — same, and for the same reason | S5 (recorded) | as above |
| 6 | `scripts/event_test.go` — `charged[eventExtension + "/eventpg"] = "./crud/adapter/crudsql"`. The allowance's first-party closure is `crud`, `crud/catalog`, `crud/sqlfault`, `errs`, `errs/sqlerr` and `utils` — everything this package needs and nothing more, which is why it must **not** import `health`, `port` or `runtime` | S1 | `TestNoEventPackageCostsMoreThanTheSeamItNames` fails on `len(packages) != len(charged)+1` the moment the package exists and the row does not. `packagesUnder` already reaches nested modules — `scripts/extensionlisting_test.go:49-54` names `event/eventpg` by anticipation |
| 7 | `docs/modules/en/eventpg.md` + its row in `docs/modules/en/Index.md` | S5 | nothing — this checklist |
| 8 | `docs/modules/ru/eventpg.md` + its row in `docs/modules/ru/Index.md` | S5 | nothing — this checklist |
| 9 | the **driver row** in both pages: any `database/sql` driver for PostgreSQL, because the store binds only `string`, `int32`, `int64`, `bool`, `time.Time` and `[]byte` and never a Go slice as an array | S5 | `TestEveryBoundParameterIsATypeEveryDatabaseSQLDriverAccepts` (S1) is what makes the row true |
| 10 | the **foreign-writer row** in both pages: the global walk covers every writer that lets the identity column draw the position, and not one that draws it with `nextval` in a transaction that then ends and inserts it later with `OVERRIDING SYSTEM VALUE` | S5 | `TestAForeignWriterIsNotSkippedAndTheOverridingWriterIs` (S4) |
| 11 | the **xid row** in both pages: a contended append consumes a transaction id, measured, backlog `## P2` §13 | S5 | this checklist |
| 11a | the **drain row** in both pages, in the consumer's own words and not in the store's: a walk on a context carrying a transaction of this backing stops at the first gap it meets and does not pass it, however many times it is called — it never mints the bound a gap is settled against — so **draining the log belongs outside the write**, and a cursor persisted from such a walk is past events the transaction may still roll back. At `REPEATABLE READ` the snapshot is frozen and the walk cannot settle even in principle | S5 | `TestAWalkInsideATransactionMintsNoBoundAndSettlesNoGap` and its three round-1 cases (S4) — GAP-P2-S4-2 |
| 12 | `docs/ai/flows/FL-037-a-recorded-fact-becomes-a-postgresql-row.md` | S5 | nothing — this checklist |
| 13 | FL-037's **file table with one row per non-test source file** — eleven rows: `doc.go`, `schema.go`, `config.go`, `migration.go`, `verify.go`, `catalog.go`, `executor.go`, `classify.go`, `append.go`, `read.go`, `cursor.go` — each naming its symbols | S5 | `scripts/docs_test.go:TestEverySymbolTheDocsCiteIsDeclaredWhereTheDocSaysItIs` fails on a symbol that is not where the row says |
| 14 | FL-037's row in `docs/ai/flows/Index.md`'s flow table, its question row in the "what am I looking for" table, and **eleven rows in the reverse `By file` index** | S5 | nothing — this checklist. This is the row phase 1 missed thirteen times |
| 15 | the `UC-032` row in `docs/ai/usecases/Index.md` extended with `eventpg` | S5 | nothing — this checklist |
| 16 | `event/eventpg/MIGRATIONS.md` — the profile rule, that version 1 is safe in one transaction and what would change that, and the poison-row remedy (disable the trigger, delete, re-enable, under whose privilege) that backlog `## P2` §6 asks for | S5 | nothing — this checklist |
| 17 | `docs/roadmaps/Roadmap.md` §15 **rewritten, not annotated done**, and each of the **three** debts it names (`Roadmap.md:342-346`) disposed of by name: the "as a report, never as a `make check` arm" line updated with §7.3's argument; the **savepoint** debt struck, and struck against `TestASavepointWritesUnderItsParentsAuthorityAndARollbackDiscardsThem` rather than against a claim; the **decode-side depth/size bound** line **rewritten rather than struck** — see below | S5 | `scripts/roadmap_test.go` reads activations, not this |
| 18 | `docs/ai/decisions/D-126-…md` — *the store chooses no isolation level, and admission is a database constraint*. Links [[D-118]], §INV-046, §INV-049, §INV-050. Written because the next maintainer will be tempted by `SERIALIZABLE` and by `SELECT … FOR UPDATE`, and because the `40001`-is-not-a-conflict mapping is the kind of thing that gets "simplified" into a bug | S5 | nothing — this checklist |
| 19 | `docs/ai/decisions/D-127-…md` — *an event schema is migrated on the same deployment-profile choice as the jobs schema*, with [[D-101]]'s *See also* updated **in the same change** | S5 | nothing — this checklist |
| 20 | both decision rows in `docs/ai/decisions/Index.md` | S5 | nothing — this checklist |
| 21 | `make api` regenerated and its diff read | S5 | nothing, and nothing should — a diff there is a question for a person |
| 22 | `make check` green including the new `check-event-kernel` arm | S5 | itself |

### The third debt Roadmap §15 hands phase 2, disposed of in the one line it is owed

§15 says "no decode-side depth or size bound beyond the byte cap (**`eventpg` must
add its own**)". **That assignment is wrong, and the plan says so rather than
quietly not doing it.** `eventpg` decodes nothing: the `payload` column is `bytea`,
it is scanned into `[]byte`, and it reaches the consumer as `event.Envelope.Payload`
untouched. Decoding is the declaration's codec, one level up and inside the frozen
kernel — `event/encodable.go`'s `codecGraphDepth = 1024` bounds the *type graph* a
declaration walks at seal time, not the nesting of a stored document at read time.

The store's whole share of that bound is the byte cap, and it **is** delivered: the
read door refuses a row whose payload exceeds `MaxPayload` before an envelope is
built (§UC-093, `TestARowOutsideTheSchemasPromisesRefusesTheWholeRead`), and the
`CHECK` constraint carrying the same number is a fingerprint input (§INV-058). A
depth bound over what those bytes decode to cannot be written here without a store
that parses payloads, which is the one thing an event store must not do.

So: **not delivered, argued to the kernel by name, and written to the backlog with
a severity** — `## P2` §26, `[medium]`, owner the phase that unfreezes the kernel.
§15's line is rewritten to name the codec seam rather than `eventpg`.

---

## The zero-diff proof

§INV-061 is the obligation and it is made executable rather than promised.

**The baseline** is a single named constant in `scripts/checks.sh`, and it is
**overridable**:

```sh
# The commit that landed the phase-1 event kernel. event/ outside event/eventpg is
# frozen against it: a second store is added with zero diffs to the vocabulary, or
# the kernel gap it needs is reported rather than patched. Overridable because the
# self-test runs this script inside a fixture repository, where this commit does
# not exist and every case would otherwise take the "does not resolve" branch.
EVENT_KERNEL_BASELINE=${EVENT_KERNEL_BASELINE:-c798fc0b28b270ec0368a918810ff3d7c17e6f8a}
```

A bare assignment is what makes the arm untestable, and the reason is
`scripts/checks_test.go:fixture`: it copies `common.sh` and `checks.sh` into a
`t.TempDir()` and `runCheck` runs the **copy**, so `REPO_ROOT` is the fixture
(`common.sh:3-4`). `c798fc0b…` does not exist in a fixture built by `git init` and
one commit, so three of the four cases below would be unconstructible and only the
one that proves nothing about the diff would be writable — an arm reporting through
an unfalsified body, which is backlog P1 §6 verbatim.

Measured now: `git diff --stat c798fc0b… -- event/ ':(exclude)event/eventpg'` is
empty and `git status --porcelain` over the same pathspec is empty, so the arm is
green before the first line of `eventpg` is written and its first red is a real one.

**The arm**, `check_event_kernel` in `scripts/checks.sh`, added to the `case` block,
to `all`, to `scripts/vv`'s dispatch and usage, and to the `Makefile`'s `COMMANDS`:

1. **refuse, never report ok**, when `git` is unavailable, when this is not a work
   tree, or when `git rev-parse --verify "$EVENT_KERNEL_BASELINE^{commit}"` fails —
   a check that passes when it cannot run is backlog P1 §6, in a file that already
   has three of them;
2. `git diff --stat "$EVENT_KERNEL_BASELINE" -- event/ ':(exclude)event/eventpg'` —
   non-empty output is a failure that prints the files;
3. `git status --porcelain -- event/ ':(exclude)event/eventpg'` — so an **untracked**
   new file under `event/` is caught too, which the diff alone would not see;
4. on failure it says what to do: either the constructor exists and was not found, or
   this is a genuine kernel gap and it is reported out loud rather than patched.

**The self-test**, `scripts/checks_test.go`, on the `TestCheckTidy…` pattern. It
needs one helper the file does not have: `runCheck` hard-codes its environment, so
a `runCheckWithEnv(t, root, check string, env ...string)` is added beside it and
`runCheck` becomes a call to it — the existing callers are unchanged. The fixture
gains a git repository: `git -c user.email=… -c user.name=… init`, `add`, `commit`,
then the commit's own sha read back with `git rev-parse HEAD` and handed to the arm
as `EVENT_KERNEL_BASELINE=<that sha>`. Git being absent is a **failure**, not a
skip: the arm under test is git-only, and a self-test that skips is the shape this
whole section exists to refuse.

`TestCheckEventKernelReportsADifferenceAndOtherwiseOk` — four cases, and the plan
says which of them is the control for which, because they do not all guard the same
thing:

| Case | Expected | What it is the control for |
|---|---|---|
| a tracked file under `event/` differing from the baseline | exit 1, **naming the file** | the `git diff` arm. A `check_event_kernel` reduced to `echo ok` fails here |
| an **untracked** new file under `event/` | exit 1 | the `git status --porcelain` arm alone — `git diff` against a commit does not see it, so dropping that line fails only this case |
| **only** `event/eventpg` differing | exit 0 | the **pathspec**. An arm that exempted nothing, or that compared all of `event/`, fails here — and this is the one case a gutted `echo ok` passes, which is why it is never run alone |
| `EVENT_KERNEL_BASELINE` set to a sha the fixture does not carry | exit 1, naming the constant, **not exit 0** | the refuse-when-it-cannot-run branch. A check that reports ok when git cannot answer is backlog P1 §6, in a file that already has three of them |

The first two are what a gutted arm fails; the third is what an over-broad arm
fails; the fourth is what a silently-vacuous arm fails. All four run, and the plan
records that the third one alone certifies nothing.

**The tension §7.3 records, and its resolution.** `docs/roadmaps/Roadmap.md` §15 says
this should exist "as a report, never as a `make check` arm". That objection is to a
**tag**-dependent check: before the first tag there is no tag, so the arm would pass
vacuously — the worst kind. Pinning a recorded *revision*, refusing when it does not
resolve, and self-testing the arm removes the objection, and the roadmap line is
updated in the same change. What the objection leaves standing is real and is
deferred rather than argued away: the arm cannot run from a tarball or a vendor
directory, and it freezes `event/` against a fixed commit with no stated
re-baselining rule — **backlog `## P2` §5, `[medium]`, left alone.**

---

## Debt

Deferred, never dropped. Referenced by its backlog entry rather than restated, per
the delivery policy.

**Carried into phase 2 from phase 1** — [`EVENTSOURCE_BACKLOG.md`](../gaps/EVENTSOURCE_BACKLOG.md):

- **P1 §1**, `[critical]` — the conformance suite detects 27 of 165 of its own
  assertions. **This plan discharges the part that was assigned to it**: S5's
  mutation harness exercises the suite against the real PostgreSQL store, and a
  mutation the suite does not catch is reported as `[high]` against the suite rather
  than passing quietly. The entry stays open for the rest of the suite.
- **P1 §8**, `[high]` — documentation coverage. Discharged **for `eventpg`** by
  checklist rows 12–15 (eleven file rows in FL-037 and eleven in the reverse index);
  the thirteen phase-1 files it names are not this plan's to write.
- **P1 §6**, `[high]` — `scripts/` checks report through unfalsified bodies. The new
  `check-event-kernel` arm is self-tested (four cases) so it does not join them; the
  incumbent bodies are untouched.
- **GAP-T5**, `[medium]`, owner **phase 2** — §INV-011's two disclosed one-hop
  escapes. The second shape, "a helper taking the request value and rendering a part
  of it", is the ordinary shape a driver refusal is written in and is exactly what
  `eventpg`'s refusals are. S3 and S4 write those refusals under the rule that **no
  refusal renders a key, a payload, a position or a cursor**; the fixture pair and the
  arm that decides it belong to `event/refusalmessages_test.go`, which is under
  `event/` and therefore **frozen by §INV-061**. Recorded here as the one place where
  a phase-1 debt assigned to phase 2 collides with phase 2's own zero-diff rule: it
  is worked after the kernel is unfrozen, not by editing `event/`.

**Raised by the phase-2 spec gate and left alone** — `## P2` §1 through §12, each in
that file:

| Entry | Severity | One line |
|---|---|---|
| §1 | medium | the cursor is opaque but not authenticated |
| §2 | medium | the narrow-limits run needs its own schema — **closed by D7**, because the section could not otherwise run |
| §3 | medium | §2.3 misdescribes what the suite measures; the half that costs one line (which level `Factory.Begin` opens) is **answered in D7**, the rest is left |
| §4 | medium | `Migrate`/`Verify` versus the profile — **answered in D7**, one `if` |
| §5 | medium | `check-event-kernel` outside a git checkout, and re-baselining |
| §6 | medium | a row the read refuses is a row nobody can remove; `MIGRATIONS.md` carries the remedy (checklist row 16) |
| §7 | medium | `COLLATE "C"` on `family` and `key` is unstated |
| §8 | medium | §2.4's table claims an ownership certainty `crud` cannot give |
| §9 | medium | "highest position returned" is ambiguous — **closed by the algorithm in `read.go`**, step 5 |
| §10 | low | a polled walk re-mints a bound per pass — **closed by step 7**, which mints only when there is no useful outstanding bound |
| §11 | low | §UC-069's Must-not is not eventpg's to keep — **restated** as a property of the list (`MigrationStatements`) |
| §12 | low | nothing states what a walk over an empty log answers — the `LEFT JOIN LATERAL`'s all-`NULL` right side is scanned explicitly in `read.go` and asserted by `TestAWalkFromTheEmptyCursorTilesWithAWalkFromTheTail` on a fresh schema |

**Raised by the round-1 plan gate and left alone** — `## P2` §14 through §23. Two of
them are touched in passing by a `[high]` fix and neither is worked: §18 (whether
the store's ambient-executor value is the kernel's sentinel or its own) stays open
because §executor.go names *one* value rather than which one, and §19 (the S5
checkpoint's weaker `git diff --quiet`) is closed as a side effect of running the
real arm there, which cost one word.

**Raised by this plan**:

- **`## P2` §13**, `[low]` — a contended append consumes a transaction id, because the
  xid-first statement trigger fires whether or not the statement produces rows.
  Measured (`xid_after_zero_row_append=56555` on an `INSERT 0 0`). There is no fix
  that keeps premise 2; the module page states the cost.
- **`## P2` §24**, `[medium]`, owner **the phase that unfreezes the kernel** — the
  `Log` read door carries no wiring class, so §UC-086's "`ErrAmbientNotTransaction`
  from every door" holds at three of four. `Reader.Next` never asks
  `store.Transaction`, and `refusal.Is` refuses any vocabulary target that is not the
  refusal's own sentinel, so no outcome a store can return renders it there. Closed
  for phase 2 by §executor.go's table — `Refused` → `ErrRefused`, one cause at all
  four — and reported rather than patched under §INV-061.
- **`## P2` §25**, `[high]`, owner **the phase that unfreezes the kernel** — a factory
  whose `Factory.New` answers a store over a backing an earlier `New`'s store already
  wrote to **cannot pass `store failure classification`**: the `NotWritten` case
  asserts the stream holds exactly one event and the second of its two
  `through` variants finds two. Measured against `eventmemory` over one shared log.
  The `Factory.New` doc forbids a `New` that resets, so the suite's own two
  requirements are mutually exclusive for a store claiming `Persistence`. Closed for
  phase 2 by D1's schema-per-store `New` with `Sibling` carrying the equal backing,
  which is what the suite actually reads for `durability` and `shared backing`.
- **`## P2` §26**, `[medium]`, owner **the phase that unfreezes the kernel** — no
  decode-side depth bound on a stored payload, only the byte cap. Roadmap §15
  assigned it to `eventpg`; `eventpg` decodes nothing, so it belongs to the codec
  seam in `event/`. Argued above, under the module checklist.
- **The `jobspg` half of §UC-085**, `[low]` — two *different subsystems'* receipts
  compared `Same` across one transaction. Written in `test/`, where both satellites
  are already replaced, in the change that adds the `test/go.mod` replace line
  (checklist row 4). `eventpg`'s own S3 test proves the property with an ordinary
  `crud` write on the same `*sql.Tx`.

**What this plan deliberately does not do.** It writes no `eventpgfx`, no projector,
no outbox, no snapshot, no retention sweeper, no second driver path, and no
`Tail(ctx)` on the public surface. It starts no goroutine and registers no
`runtime.Runner` ([[D-092]]). It publishes no importance and imports no `health`
([[D-091]]). It edits no file under `event/` outside `event/eventpg`, and if it needs
to, that is a kernel gap and it is said out loud.
