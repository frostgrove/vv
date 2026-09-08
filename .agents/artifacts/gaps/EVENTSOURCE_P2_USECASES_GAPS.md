# EVENTSOURCE — phase 2 (`event/eventpg`) usecases — GAPS

## Round 1 — spec auditor (coverage / invariants / DX, PostgreSQL reality) — 2026-09-08

Audited: [`EVENTSOURCE_P2_USECASES.md`](../usecases/EVENTSOURCE_P2_USECASES.md) against
[`EVENTSOURCE_P1_USECASES.md`](../usecases/EVENTSOURCE_P1_USECASES.md) (§D.14, §D.15), the frozen
code under `event/` (`event/store.go`, `event/reader.go`, `event/outcome.go`, `event/bounds.go`,
`event/eventtest/*`, `event/eventmemory/cursor.go`), `jobs/jobspg/`, [[D-118]], [[D-101]],
`crud/executor.go`, `crud/adapter/crudsql/crudsql.go`, `errs/sqlerr/`, and
`docs/roadmaps/2026-09-01-postgres-event-sourcing-roadmap.md`.

**Zero-diff obligation: clean.** Every value the proposed §5 surface must construct or return is
already constructible from outside `event`: `event.Failure` over the seven-value `Outcome` enum
(`Refused` included — §INV-062's "seven" matches `event/outcome.go`), `event.NewBacking` over a
comparable non-nil identity (a `struct{db *sql.DB; schema string}` is comparable, and
`Backing.Equal` is `crud.SameDataSource`, which is `type-equal && ==`, so two store values over one
db+schema compare `Equal` and two schemas do not), `event.NewAuthority(backing, tx)` over the
`*sql.Tx` `crudsql.Transaction` yields, plain `Envelope`/`Record`/`AppendRequest`/`Limits`/
`Capabilities` structs, the defined string/integer types, and the exported ceilings
(`event.MaxPayloadBytes`, `MaxKeyBytes`, `MaxBatchCount`, `MaxPageCount`, `ResidentPage`). The
factory hooks §UC-097 names — `New`, `Begin`, `Sibling`, `Fail`, `Tail`, `Unparsable`, `Window` —
all exist on `eventtest.Factory` today. **No type was found that eventpg cannot construct.**
`crudsql`'s savepoint really does answer its parent's `*sql.Tx` (`crudsql.go:232`), so §2.4's
savepoint claim and §6.12 are true rather than hoped.

**Global-position honesty: a strength, not a gap.** `MonotoneVisibility: Unsupported` plus a settled
watermark is the only honest answer a sequence can give, `Reader.checkPage` (`event/reader.go:74`)
forbids the descending-position alternative, and the `pg_snapshot_xmin = pg_snapshot_xmax` rule the
spec rejects really is false for the reason it measured. The soundness argument is sound — given one
premise it does not state, which is GAP-1.

**[[D-118]] conformance: one answer, not two.** Same-datasource refusal in `New`, no quiet fallback,
never autocommit under a bound non-transaction. The one divergence from `jobspg` — a nil `Source` is
refused rather than degraded — is argued in §2.4 and is *stricter*, not a second answer; it is
consistent with D-118's "do not describe the transactional path as available when the driver has no
`Source`". Not a finding.

Seven blocking findings follow. Medium and low go to
[`EVENTSOURCE_BACKLOG.md`](EVENTSOURCE_BACKLOG.md) `## P2` under the 2026-09-08 policy.

> **Fix round — 2026-09-08.** All seven are **closed** by edits to the spec; none was rejected. One
> close criterion, GAP-3's "§UC-083(b) asserts one outcome for both spellings", was answered
> differently and the argument is in GAP-3's status and in §UC-083 itself. Nothing under `event/`
> outside `event/eventpg` was touched — no code was written in this round at all.

---

### GAP-1 [high][immediate] The watermark's soundness rests on a premise the spec does not state and nothing verifies

- **Where:** §2.6, the block quote "Why the bound is minted rather than read out of the snapshot";
  §2.1's `CACHE 1 NO CYCLE` row; §INV-054; §2.2 level 3.
- **What:** The spec says the argument "rests on one schema property — the position sequence hands
  values out in increasing order over time". That is half of it. The full argument needs **two**
  independent premises:
  1. positions are handed out in increasing time order — `CACHE 1`, no `CYCLE`, which the spec
     states and level 3 verifies; and
  2. **every writer's transaction id is assigned before that writer draws a position.** Without (2)
     a writer can draw position `p`, have `X` minted by a reader a moment later, and only then be
     assigned an xid `> X`. `pg_snapshot_xmin > X` is then true while that writer is still running,
     the reader declares the gap at `p` burnt, delivers `p+1`, and `p` is **never delivered** after
     it commits. That is silent event loss for every consumer, and no conformance section, no
     `checkPage` and no audit query can see it.

  (2) happens to be true for the statement §2.3 specifies — the `admitted` CTE writes `streams`
  first, so the xid is assigned before the outer `INSERT` evaluates the identity default — but that
  is an accident of the statement's shape, not a stated invariant. It is falsified by any of: an
  implementer reordering the statement or splitting the fresh-stream case out of the CTE; a future
  schema version that draws the position anywhere before the first heap write; and **any other
  writer to the `events` table**, which §UC-093 explicitly admits exists ("another writer shares the
  table").
- **Why this severity:** Wrong behaviour with no error and no test: a consumer skips a committed
  event and its projection is permanently short one fact. It is not `[critical]` only because the
  algorithm as specified is sound today; the defect is that its soundness is unstated, unverified
  and one refactor away from being false.
- **Why this timing:** It is an invariant of the write statement that constrains how the write
  statement may ever be written. Discovering it after the statement and its tests are in means
  re-deriving the argument from the failure rather than from the document.
- **Close criteria:**
  - [ ] §2.6 states both premises, and names which one is a schema property and which one is a
        property of the append statement.
  - [ ] A new invariant states premise (2) over the append statement — "no position is drawn before
        this transaction has an xid" — with a falsification that is a test, not prose.
  - [ ] §2.6 states what the watermark guarantees when a writer that is **not** `eventpg` inserts
        into `events`, and either bounds the claim to "no other writer" or adds the check that makes
        it hold (e.g. the position column drawn only after a write to `streams`).
  - [x] The live gate carries a case that drives premise (2) directly: a session that draws a
        position before acquiring an xid (a hand-written `INSERT` into `events` with the identity
        default evaluated first) must not be skipped by a walk.
- **Status:** closed 2026-09-08 — accepted, and closed by making premise (2) a schema property
  rather than a statement property. §2.1 gains
  `CREATE TRIGGER events_position_needs_xid BEFORE INSERT ON events FOR EACH STATEMENT`, whose
  function is `PERFORM pg_current_xact_id()`. A statement-level `BEFORE INSERT` trigger fires
  before the statement produces its first row and therefore before the identity column's `nextval`
  is evaluated, so **every** writer — `eventpg`'s CTE, a psql `INSERT`, `COPY`, another service —
  has an xid before it draws a position. Cost is one plpgsql call per append statement and zero
  extra xids, because the writer was going to acquire one anyway. §2.6 now states both premises,
  names both as schema properties, and says which one §2.1 had to add and why the CTE's accidental
  ordering was not enough. §2.2 level 3 compares the trigger; §UC-072 and §6.7 carry the
  dropped-trigger mutation. New §INV-065; §INV-054 restated over the two premises. New §UC-098 with
  two controls: (a) a test-only `BEFORE INSERT ... FOR EACH ROW` trigger reads
  `pg_current_xact_id_if_assigned()` for a foreign insert — not null with the store's trigger, null
  with it dropped, which measures the premise directly with no timing window; (b) the one writer
  shape still outside the guarantee (`nextval` in one statement, `INSERT ... OVERRIDING SYSTEM
  VALUE` in a later one) is asserted to **be** skipped, so the bound on the claim is a test result.
  §2.6's "what a consumer may assume" list and the module docs carry that bound.

### GAP-2 [high][immediate] The prohibition on re-executing an append names no mechanism, and the obvious `database/sql` call violates it

- **Where:** §2.5, "What the framework must not do" — "it must not rely on `database/sql`'s
  bad-connection retry to make an append idempotent, and therefore issues the append through a path
  that never re-executes a statement whose fate is unknown"; §INV-053.
- **What:** No such path is named, and the default one does exactly what is forbidden.
  `(*sql.DB).ExecContext` retries a `driver.ErrBadConn` on up to two cached connections and then
  once more on a freshly opened one — three executions of one append. The only calls that do not
  retry are `(*sql.Conn).ExecContext` on a connection the store checked out itself and
  `(*sql.Tx).ExecContext`. So the autocommit path — the only path where §2.5 says uncertainty
  exists at all — is precisely the one that silently re-executes unless the spec forbids `*sql.DB`
  by name. The observable failure is the classification the whole of §2.5 is built to protect: the
  first attempt lands, the retry is refused at the same `Expected`, and the caller is told
  `ErrConflict` for a write of its own that succeeded, or `ErrBackend` where the honest answer was
  `ErrUncertain`.
- **Why this severity:** A stated invariant (§INV-053, "never retries a statement whose fate is
  unknown") is unimplementable by the naive reading and no reader can tell. It also makes §UC-081's
  "asserting exactly one statement was issued (counted at the driver)" a test that fails for a
  reason nobody will be able to name from the document.
- **Why this timing:** It decides how every statement in the package is issued — one helper, written
  once, at the bottom of the store. Retrofitting it means touching all four doors and re-running the
  whole live gate.
- **Close criteria:**
  - [ ] §2.5 names the mechanism: the autocommit path executes on a connection the store checked out
        (`(*sql.DB).Conn`) or otherwise on a path with no `ErrBadConn` retry, and says why.
  - [ ] §INV-053's falsification counts statements at the driver for a killed connection **and** for
        a `driver.ErrBadConn` returned before the statement is sent, so the two are told apart.
  - [x] The surface in §5 or §2.5 says who closes that connection and what happens when checkout
        itself fails (see GAP-3).
- **Status:** closed 2026-09-08 — accepted. §2.5 gains "The path that does not re-execute, named
  rather than implied": every statement on the autocommit path — the append and both reads — is
  issued on a `*sql.Conn` the store checks out with `(*sql.DB).Conn(ctx)`, because `(*sql.Conn)` and
  `(*sql.Tx)` are the only two `database/sql` calls that execute a statement exactly once. The
  store `defer`s `conn.Close()`, which returns the connection to the pool and never closes the
  `*sql.DB` the composition root owns; a checkout failure is `NotWritten` by GAP-3's proof 1;
  `Conn`'s own retry of *acquiring* a connection is not re-execution and is said so. The reads take
  the same path for a stated reason that is not symmetry — the store then contains no call to a
  statement method on `*sql.DB` at all, which is a source check rather than a discipline, and one
  `ReadAll` is one checkout instead of three. §INV-053 carries the mechanism and a two-case
  falsification: a counting `database/sql` driver wrapping the real one asserts one `ExecContext`
  for a killed connection answering `ErrUncertain`, and one `ExecContext` for a
  `driver.ErrBadConn` returned before the send answering `NotWritten` — so the two are told apart
  rather than both passing on one number. §UC-081 and §6.5 carry it live.

### GAP-3 [high][immediate] There is no rule for choosing between `NotWritten` and `Unconfirmed`, and the unstated default is the dangerous one

- **Where:** §2.5's table; §INV-052; §UC-081, §UC-082, §UC-083.
- **What:** The table gives four situations by name — "connection lost, reset, or the statement
  cancelled while in flight", "a clean SQL error (`23514`, `23505`, `22001`, `42P01`)",
  "`40001`/`40P01`/`55P03`", "inside a bound transaction" — and no **rule**. Everything not in those
  lists is unassigned, and the unassigned set is large and ordinary:
  - `57014 query_canceled` from a server-side `statement_timeout` or `pg_cancel_backend`: the server
    reported, so the statement certainly rolled back — `NotWritten`. The spec's row "the statement
    cancelled while in flight → `Unconfirmed`" says the opposite, and §UC-083(b) asserts
    `ErrUncertain` for a context cancellation that pgx may surface either as `context.DeadlineExceeded`
    or as `57014`. The case is therefore not deterministic as written.
  - **connection checkout failure** (pool exhausted, `connect: connection refused`, a DSN that
    resolves nowhere): nothing was issued, so this is `NotWritten` — but by the "connection lost"
    row it reads as `Unconfirmed`, and every start-up blip becomes an uncertainty that sends callers
    into §UC-034's recovery procedure for writes that never left the process.
  - a driver error carrying **no SQLSTATE at all**: unassigned.

  The direction of the missing default is what makes this blocking. Phase 1's own sketch (§D.15)
  ends `default: return event.Failure(event.NotWritten, …)` — an implementer copying the accepted
  shape declares *certainly not written* about every error it does not recognise, including the one
  class where that is a lie. `ErrBackend` states an obligation (`NotWritten` is the only outcome the
  conformance suite checks the stream against — `sections_lifecycle.go:334`), and a store that says
  it about a write that landed is the one failure §2.5 exists to prevent.
- **Why this severity:** It is the single most consequential classification in the store, it is
  stated as a list of examples rather than as a rule, and the list's own "cancelled while in flight"
  row is wrong for the commonest real cancellation (`57014`).
- **Why this timing:** `classify` is one function every door calls; the rule it encodes has to exist
  before it is written, and §6.5's and §6.10's live cases are written against it.
- **Close criteria:**
  - [x] §2.5 states a **rule**, not a list: e.g. `NotWritten` is selected only when the server
        reported a SQLSTATE (proof it processed and rejected the statement) or when the statement
        provably never left the process; every other autocommit failure is `Unconfirmed`.
  - [x] `57014`, `statement_timeout`, connection-checkout failure and "a driver error with no
        SQLSTATE" each appear in the table with an outcome and a one-line reason.
  - [~] §UC-083(b) says which of the two spellings PostgreSQL may answer with and asserts one
        outcome for both, so the case cannot pass for one reason and fail for the other.
        **Answered differently — see the argument below.**
  - [x] §INV-052 or a sibling states the fail-safe direction in one sentence: an autocommit failure
        the store cannot pin down is `Unconfirmed`, never `NotWritten`.
- **Status:** closed 2026-09-08 — accepted, with one close criterion answered differently and the
  difference argued in the spec. §2.5 now opens with the rule and demotes the table to its worked
  examples: **`NotWritten` is selected only on proof, and there are exactly two proofs** — nothing
  was issued (context done on entry, connection checkout failed, `driver.ErrBadConn`, which the
  `database/sql` driver contract permits only when the driver is certain the server never received
  the query), or the backend answered with a SQLSTATE **and survived to say so**. The rule needed
  that second clause rather than the criterion's bare "the server reported a SQLSTATE": `57P01`
  from `pg_terminate_backend` is a SQLSTATE that arrives *with* the session dying and is not proof
  of rollback, so the exception list is class `08` and `57P01`/`57P02`/`57P03`, named in the rule.
  Everything else is `Unconfirmed`, and the spec says in bold that there is no default branch
  answering `NotWritten` — the direction phase 1's §D.15 sketch fell in. The table gains rows for
  checkout failure (`NotWritten`), `57014` from `statement_timeout` or `pg_cancel_backend`
  (`NotWritten` — the finding's own reading, and the old "cancelled in flight → `Unconfirmed`" row
  is split rather than kept), a client-side cancel with no SQLSTATE (`Unconfirmed`),
  `driver.ErrBadConn` (`NotWritten`), and a driver error with no SQLSTATE at all (`Unconfirmed`).
  The SQLSTATE is read with `sqlfault.Extract`, which needs no driver import, and selection is
  driven by the SQLSTATE rather than by whether `sqlerr.Classify` recognises it — `57014` is not in
  that table and is still a server answer. New §INV-064 states the fail-safe direction and is
  falsified from real driver errors, including the no-SQLSTATE case that a `NotWritten` default
  fails. §6.5 runs the whole table live.

  **Where this answers the criterion differently, and why.** §UC-083(b) is *not* made to assert one
  outcome for both spellings. The two spellings have different truths: a server `57014` proves the
  statement rolled back, a client-side abandon proves nothing. Asserting one outcome for both would
  require the store to be wrong in one of the two directions, and it would make §UC-083(b) the one
  case in the document that contradicts the rule the rest of §2.5 now states. The underlying
  concern — a case that passes for one reason and fails for the other — is answered more strictly
  instead: §UC-083 gains a third leg (c), a deterministic server-side cancellation
  (`statement_timeout` / `pg_cancel_backend`) asserting `NotWritten`, and (b) asserts the **rule**
  by reading the cause through `event.CauseOf` — `ErrUncertain` when no SQLSTATE came back,
  `ErrBackend` when `57014` did, and a **failure** if the cause is neither. (b) and (c) are each
  other's controls: a store that answers `Unconfirmed` for every cancellation fails (c), one that
  answers `NotWritten` for every cancellation fails (b). Neither can pass by accepting both answers.

### GAP-4 [high][immediate] A session-level advisory lock over a `*sql.DB` does not serialise anything

- **Where:** §2.2, "One migration at a time"; §UC-073; §INV-051 ("`Migrate` … holds an advisory
  lock, which is not transaction control").
- **What:** `pg_advisory_lock` is held by a **session**, and a `*sql.DB` is a pool: the lock is
  taken on whichever connection the pool hands out, the `CREATE TABLE`s run on whichever connection
  the pool hands out next, and the unlock runs on a third — where it returns false and logs
  "you don't own a lock of type ExclusiveLock". Two replicas therefore race `CREATE TABLE` exactly
  as if no lock existed, and PostgreSQL's answer to that race is not always clean: concurrent
  `CREATE TABLE IF NOT EXISTS` / `CREATE TYPE` on one name raises
  `duplicate key value violates unique constraint "pg_type_typname_nsp_index"` (23505) in one of the
  two sessions. §UC-073's "no statement errored" is then intermittently false, which is the worst
  shape of red: a gate that passes most runs.
- **Why this severity:** The mechanism the spec names does not produce the property §UC-073 asserts.
  It is a secondary scenario (two replicas starting together) broken by construction, and the test
  written for it will be flaky rather than red.
- **Why this timing:** It changes `Migrate`'s connection handling and therefore what `Prepare` owns;
  it is a five-line decision now and a rewrite of the migration path plus its live case later.
- **Close criteria:**
  - [x] §2.2 states that the advisory lock and every statement it guards run on **one pinned
        connection** (`(*sql.DB).Conn`), or that the lock is `pg_advisory_xact_lock` taken inside the
        migration's own transaction — and says which, with the reason the other was rejected.
  - [x] §INV-051's "not transaction control" clause is reconciled with whichever is chosen (a
        transaction-scoped lock means `Migrate` opens a transaction, which is outside the eight
        methods but is still a claim the invariant makes).
  - [x] §UC-073 asserts the lock is held for the whole migration and released afterwards, through an
        observation of `pg_locks` rather than through the absence of an error.
- **Status:** closed 2026-09-08 — accepted. §2.2's "One migration at a time" becomes "One migration
  at a time, **on one connection**": `Migrate` checks a connection out with `(*sql.DB).Conn(ctx)`
  and does the lock, `BEGIN`, the statements, `COMMIT`, the unlock and the `Close` on that one
  `*sql.Conn` — `jobspg`'s `withMigrationLock` (`jobs/jobspg/retention_migration.go:115`), copied
  rather than reinvented. The failure the finding names is written out: three pooled connections,
  an unlock that returns false, and the `23505` on `pg_type_typname_nsp_index` that a real
  `CREATE TABLE` race raises. `pg_advisory_xact_lock` is named as the rejected alternative with its
  reason — it is pinned by construction but works only while every migration fits in one
  transaction, so the first version needing `CREATE INDEX CONCURRENTLY` would give the store two
  locking mechanisms for one question. §INV-051 is rewritten: the eight methods still open nothing,
  and `Migrate` is the one exception, stated rather than smoothed over, because the alternative is
  §UC-069's forbidden half-created schema. §INV-049's source check is narrowed to the files the
  eight methods reach, excludes the migration file by name and by reason, and refuses if that file
  does not exist — so a rename cannot turn the exclusion into a blanket exemption. §UC-073 now
  observes `pg_locks` (one granted `advisory` row, one backend pid for lock, DDL and unlock) and
  gains a control in which a second attempt blocks against a lock the test holds, so a run where
  nobody waited is not read as a pass. §6 gains item 15 for it.

### GAP-5 [high][immediate] The append statement's array parameters make the store driver-specific, which contradicts §1's dependency claim

- **Where:** §1 ("its production code takes no driver import at all: it speaks `database/sql` … so
  the module's only third-party requirement is the `pgx/v5` its `_test.go` files register");
  non-goal 4 ("One production dependency shape — `database/sql` — so a consumer already on `ent`,
  `gorm`, `sqlx`, `sqlc` or `bun` composes without a second pool"); §2.3's statement; §8.5.
- **What:** `unnest($5::text[], $6::int[], $7::bytea[])` requires the store to bind a Go slice as a
  PostgreSQL array. `database/sql`'s default converter accepts only `driver.Value` — a slice of
  string, of int32 or of `[]byte` is rejected with "unsupported type" — unless the **driver**
  implements `driver.NamedValueChecker`. `pgx/v5/stdlib` does; `lib/pq` does not, and requires an
  explicit `pq.Array()` wrapper, which is a driver import. So the append path works on exactly the
  driver the test fixtures happen to register and fails at runtime on the other mainstream
  `database/sql` PostgreSQL driver — while §1 sells the opposite, and the live gate (pgx) can never
  see it.
  §8.5's justification does not hold either: the batch is bounded by `event.MaxBatchCount` = 1024, so
  a row-per-record `VALUES` form costs `3 × 1024 + 4 = 3076` parameters against PostgreSQL's 65535.
  The parameter limit is not what forces `unnest`; nothing in the document is.
- **Why this severity:** A leaky abstraction stated as its opposite. Either §1's composition claim is
  false, or the module has an undeclared driver requirement in production code — and both are
  contracts other sections and the module's docs are about to be written against.
- **Why this timing:** It is the shape of the one statement everything in §2.3, §2.5, §INV-046,
  §INV-047 and half the live gate is written about. Choosing it after the tests exist means
  rewriting the statement and re-proving concurrency.
- **Close criteria:**
  - [x] §1 and non-goal 4 either drop the "any `database/sql` consumer composes" claim and state the
        driver requirement (with the list of drivers that satisfy it), **or** §2.3's statement is
        respelled in a form every `database/sql` driver binds — a row-per-record `VALUES` list, or a
        single-parameter encoding the statement decodes.
  - [x] §8.5 replaces the parameter-count argument with the real one, whichever it is, and pins the
        widest batch (`event.MaxBatchCount`) against the chosen form.
  - [x] `docs/modules/{en,ru}/eventpg.md`'s driver row says exactly what a consumer's driver must
        support.
- **Status:** closed 2026-09-08 — accepted, and closed by taking the second branch: the statement is
  respelled, the claim stands. §2.3's batch travels as a generated row-per-record `VALUES` list of
  scalars — `$5::text, $6::int, $7::bytea, 1::bigint`, then three parameters per further record —
  so the store binds only what `driver.DefaultParameterConverter` already accepts and never a Go
  slice as a PostgreSQL array. §1 gains a paragraph stating what "speaks `database/sql`" is required
  to mean, naming `driver.NamedValueChecker`, `pgx/v5/stdlib` and `lib/pq`, so the claim is checkable
  instead of implicit; non-goal 4 needed no change because it is now true. §2.3 records the `unnest`
  rejection where a reviewer will look for it, and states the cost the `VALUES` form carries: the
  statement text varies with the record count, so the prepared-statement cache holds one entry per
  batch size used, bounded by `MaxBatch` — and padding to `MaxBatch` was rejected for putting bytes
  on the wire nobody wrote. `ORDER BY record.ord` is added, because §UC-084 requires positions to
  ascend in the caller's order and neither form promises it without one. §8.5 is rewritten to say
  the parameter-count argument was wrong (`3 × 1024 + 4 = 3076` against 65535, so the limit never
  bit) and to hand the plan the real question. §UC-084 gains a control at `event.MaxBatchCount`
  records, and §7.2 names the driver row and the foreign-writer row as contracts of the module docs.

### GAP-6 [high][immediate] The empty cursor — how every walk starts — is unspecified, and §UC-091 reads as a refusal of it

- **Where:** §2.6 ("A cursor is an opaque string carrying a format tag, the 16-byte `log` … and
  three unsigned numbers"); §UC-091; §UC-088; §INV-056.
- **What:** Nothing says what `ReadAll(ctx, "")` does. Every first-time consumer passes it:
  `event.Read(log, "")` is the documented way to start a walk with no persisted checkpoint,
  `eventmemory` maps `cursor == ""` to position 0 (`event/eventmemory/cursor.go:27`), and the
  conformance suite itself issues it — `probe.tail` when no `Tail` hook is supplied
  (`eventtest/probe.go:228`), `injectedAtRead` in the store-failure section
  (`sections_lifecycle.go:348`), and `event.Read(store, "")` in the binding section. §UC-091 says a
  cursor "that is not this format" answers `ErrCursor` and that the walk "does **not** restart from
  the beginning"; an implementer applying that sentence to `""` ships a store no consumer can start,
  and the failure surfaces as `ErrCursor` at the first read of a fresh deployment.
- **Why this severity:** The primary entry point of the whole bounded-read actor (A4) is undefined,
  and the one rule the document does give points the wrong way. It is not `[critical]` only because
  the wrong choice errors loudly rather than losing data.
- **Why this timing:** It is a public contract clause the cursor parser and §6's walking cases are
  written against, and §UC-091's wording has to change either way.
- **Close criteria:**
  - [x] §2.6 states that the empty cursor is the origin of the log and is not a format error, and
        that it is the only cursor value not carrying a log id.
  - [x] §UC-091 excludes `""` from "unparsable" explicitly, and a case asserts a walk from `""`
        returns the log from its first settled position.
  - [x] §UC-088 or §6.13 asserts that a walk from `""` and a walk from a `Tail` cursor tile the same
        log, so "start from nothing" is exercised, not assumed.
- **Status:** closed 2026-09-08 — accepted. §2.6's cursor definition now ends "or it is the empty
  string, which is the origin of the log", followed by a paragraph saying it is the one cursor value
  carrying no log id, that it reads as `(F, X, R) = (0, 0, 0)`, and naming the three `eventtest`
  call sites and `eventmemory`'s mapping (`event/eventmemory/cursor.go:27`) that make it the entry
  point. §UC-091 excludes `""` by name in **Given**, in **Must not** and in its **Control**, which is
  now the pair "a cursor from this log resumes, and `""` resumes from the origin" — so the refusal
  has to discriminate rather than be universal. §UC-088 gains the first-time consumer, the tiling
  assertion against a `Tail` cursor, and a **Must not** against the other wrong answer ("start from
  now", which would silently skip a fresh consumer's backlog). §INV-056 carries the clause and
  §6.13 runs both live.

### GAP-7 [high][immediate] Schema verification enumerates what must be present and never asks what else is there

- **Where:** §2.2, verification level 3; §UC-072; §INV-057; §6.7.
- **What:** All three levels compare the deployed schema against an expectation of **required**
  objects: tables, columns, keys, the identity sequence's parameters, the unique constraint, the
  foreign key, the check constraints, "both triggers by name, table, timing and event set". Every
  mutation §UC-072 and §6.7 name is a *removal* or an *alteration*. Nothing asks whether anything
  **extra** exists, and the extras that change behaviour are the ones that fail silently:
  - `ALTER TABLE events ENABLE ROW LEVEL SECURITY` plus a policy — every `ReadStream` and `ReadAll`
    silently returns a filtered subset. A short page is "the end of the stream" (§UC-087), so an
    aggregate folds from a truncated history and no error is raised anywhere;
  - a third `BEFORE INSERT` trigger or a `RULE` on `events` that rewrites or suppresses a row — a
    fact is recorded that is not the fact that was decided, and §INV-048's "append-only is a property
    of the database" is true while the payload is not;
  - a `BEFORE INSERT` trigger on `streams`, which moves admission out from under §INV-046.

  §2.2 argues level 3's whole value on exactly this class ("a dropped unique constraint is a store
  that admits two events at one version … and neither of the other two levels can see it"). The
  symmetric case — an object nobody expected — is not covered by any of the three, and the
  fingerprint cannot see it because nobody rewrote it.
- **Why this severity:** The stated purpose of level 3 is "a hand-edited database", and a hand-edited
  database is at least as likely to have gained an object as lost one. The failure mode is silent
  history truncation, which is the worst outcome the document names anywhere.
- **Why this timing:** It changes what the verifier reads out of `pg_catalog` and what the
  fingerprint canonicalises, and §6.7's per-mutation scratch-schema cases are the harness that would
  prove it.
- **Close criteria:**
  - [x] §2.2 level 3 states set **equality**, not presence, for the classes where an extra object
        changes behaviour: triggers, rules, row-level-security enablement and policies, and column
        defaults/generated-ness on the three tables. It says explicitly which classes are exempt
        (an additional index is harmless) and why.
  - [x] §UC-072's mutation list gains the added-object direction — RLS enabled with a policy, a
        third trigger on `events`, a rule — and each refuses at `Prepare` naming the object.
  - [x] §6.7 runs those mutations in a scratch schema alongside the removals, with the intact schema
        as the control.
- **Status:** closed 2026-09-08 — accepted. §2.2 gains "Level 3 asks what else is there, not only
  what is missing", with a table of the classes compared as **set equality** over the three tables
  and what an extra member of each does if nobody looks: triggers (name, timing, event set, level,
  function — three on `events`, none on `streams` or `schema_meta`); rules; row-level security
  (`relrowsecurity`, `relforcerowsecurity`, `pg_policy`); column defaults and generated-ness; and
  relation kind plus inheritance (`relkind = 'r'`, `relhassubclass` false), which the finding did
  not name but which is the same hole — a child table joins every read of `events` carrying none of
  its constraints. The exemptions are stated with the reason that makes each safe rather than left
  implicit: an extra index or constraint fails **loudly** at write time (`23505` → `NotWritten` →
  `ErrBackend`), and comments, ownership, privileges, storage parameters and unrelated tables are
  never read. §UC-072 is restructured into removed-or-altered and added directions with six added
  mutations; §INV-057 carries set equality and the both-directions falsification; §6.7 runs both
  lists per scratch schema and adds a second control that must **pass** — an extra non-unique index
  — because a verifier that refuses everything proves nothing about the ones that matter.

---

**Not found, and checked for:** a value the proposed surface cannot construct (§D.15 walk redone,
none); a second answer to [[D-118]]'s ambient-transaction question; a monotone-visibility promise the
store cannot keep; a conformance section `eventtest` would fail for a reason the spec has not
already owned (`concurrency`, `transactions.contended`, `resumption.lateWriter` and
`lifecycle.stage` were each walked against the watermark and the one-statement append); a
spec-level hardcode — no use case, invariant or DX line encodes a particular aggregate, a fixed
family, a sample payload or a known input shape. The narrow-limits second run of §6.1 is the one
place a specific number does work it cannot do, and that is a `[medium]` in the backlog rather than
a universality finding.
