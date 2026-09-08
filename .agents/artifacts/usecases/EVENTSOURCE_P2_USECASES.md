# EVENTSOURCE PHASE 2 — `event/eventpg` USECASES & INVARIANTS

**Status:** specification, phase 2 of the PostgreSQL event-sourcing roadmap.
**Written against:** `event`, `event/eventmemory` and `event/eventtest` as they
stand, `jobs/jobspg` as the accepted precedent, PostgreSQL 17.9.
**Numbering:** continues phase 1. Use cases start at UC-069, invariants at
INV-046, so a bare `UC-0nn` or `INV-0nn` is unambiguous across both documents.

## 0. What this document is, and what it does not repeat

Phase 1's contract is frozen and is the input to this one:
[`EVENTSOURCE_P1_USECASES.md`](EVENTSOURCE_P1_USECASES.md) for the semantics,
[`EVENTSOURCE_P1_PLAN.md`](../plans/EVENTSOURCE_P1_PLAN.md) for the contracts as
built, and the code under `event/` for what they actually are. **Nothing in
phase 1 is restated here.** Where a rule already exists it is referenced by its
number and the reference is the whole of what this document says about it.

What is written out below is only what a PostgreSQL store adds beyond satisfying
that contract: the schema, the schema profile, admission under concurrent
writers, the caller's transaction, commit uncertainty, ordered reads, health, and
the evidence. Depth is spent where PostgreSQL genuinely differs — isolation,
concurrency, crash, schema — and nowhere else.

Two obligations from phase 1 are the frame this is written inside, and both are
executable rather than promised:

- **`eventpg` runs `event/eventtest` unchanged.** That is phase 2's central
  evidence, and §6 says what else has to be true for it to mean anything.
- **Phase 2 writes zero diffs under `event/` outside `event/eventpg`**
  (§INV-019, §INV-061). If an implementer wants to edit a file under `event/`,
  that is either a constructor they have not found or a genuine kernel gap, and
  the second one is reported out loud rather than patched.

**Delivery policy in force (set 2026-09-08).** Only `[critical]` and `[high]`
findings block. `[medium]` and `[low]` are appended to
[`EVENTSOURCE_BACKLOG.md`](../gaps/EVENTSOURCE_BACKLOG.md) under `## P2` and left
alone. A gate never proceeds silently red.

---

## 1. Scope

`event/eventpg` is **one module**, `github.com/frostgrove/vv/event/eventpg`,
holding **one package**, `eventpg`, that implements the eight methods of
`event.Store` over PostgreSQL. It is a module because its live fixtures import a
driver — one `go.mod`, one dependency decision ([[D-051]], [[D-116]], [[D-033]]).
Its production code takes no driver import at all: it speaks `database/sql`,
`crud`, `crud/adapter/crudsql`, `crud/sqlfault` and `errs/sqlerr`, all of which
`crudsql` already reaches, so the module's only third-party requirement is the
`pgx/v5` its `_test.go` files register as `database/sql`'s driver.

**What "speaks `database/sql`" is required to mean, stated so it can be
checked.** Every statement this store issues binds only values
`driver.DefaultParameterConverter` already accepts — `string`, `int32`, `int64`,
`bool`, `time.Time` and `[]byte` — and never a Go slice bound as a PostgreSQL
array. Array binding is not part of `database/sql`: it works only on a driver
that implements `driver.NamedValueChecker` (`pgx/v5/stdlib` does; `lib/pq` does
not, and wants a `pq.Array` wrapper, which is a driver import). A store whose
write path binds an array composes with the driver its own fixtures happen to
register and fails at run time on the other one, while this paragraph claims the
opposite. §2.3's append statement is spelled to keep the claim true, and §8.5
says what that costs.

> The 2026-09-01 roadmap writes the module path as
> `github.com/frostgrove/vv/eventpg`. That line predates the tree convention it
> would break: the top-level axis is the subsystem and the store lives beneath it,
> exactly as `jobs/jobspg` does ([[D-058]]). Phase 2 uses `event/eventpg`, which
> is also the path phase 1's §INV-019 and its `git diff` pathspec already name.

### In scope

1. The schema: three tables, the keys and indexes that carry the guarantees, and
   an append-only enforcement that is PostgreSQL's rather than Go's.
2. The schema profile: verify by default, migrate only when asked, fail closed
   before any append ([[D-101]]).
3. Expected-version append in one statement, and what two concurrent writers see.
4. Joining the caller's transaction, and what the commit receipt proves ([[D-118]]).
5. Commit uncertainty: which windows exist, which are impossible, and what the
   caller does.
6. Ordered reads: one stream in order, and a bounded global walk whose cursor is
   a settled watermark.
7. A readiness answer that names no importance of its own ([[D-091]]).
8. The live suite, its command, and the mutation harness that stops a green run
   from being evidence of nothing.

### Non-goals — named, not specified

Phase 1's non-goal list (§1) carries over whole. These are the phase-2-specific
refusals, each argued where it arises below:

1. **No projector, publisher, outbox, retention sweeper or archiver.** Nothing in
   `eventpg` runs; nothing starts a goroutine; there is no `runtime.Runner` here
   ([[D-092]]). E2 is where a projection arrives, with its own checkpoint story.
2. **No snapshot table and no `as of version N` read.** Full replay is still the
   only authority (§INV-008).
3. **No `eventpgfx`, no `eventpgotel`, no `eventpgtenancy`, no `eventpgaudit`.**
   A container binding is a later, separate module on the `jobspgfx` pattern; the
   intersections are application composition, never packages ([[D-116]]).
4. **No `pgx`-native path.** One production dependency shape — `database/sql` —
   so a consumer already on `ent`, `gorm`, `sqlx` or `bun` composes without a
   second pool. A `crudpgx` variant is additive and has no consumer yet.
5. **No exported "start from now" cursor.** A cursor at the end of the log is
   only sound if every position below it is settled, and there is no O(1) reading
   that proves that (§2.6): it costs a walk to the end or a wait. The conformance
   factory's `Tail` hook is implemented in eventpg's **test** package, where both
   are allowed (§2.6, §UC-088).
6. **No second durable-intent table.** [[D-118]] already decided the outbox.
7. **No multi-stream atomic append and no idempotency key.** Unchanged from
   phase 1 non-goals 11 and 14.
8. **No table partitioning, no schema-per-tenant, no logical replication slot.**
   Tenancy composes above this store by choosing a `crud.Source`; a partitioned
   variant is a schema version, not a phase-2 obligation.

---

## 2. The seven things PostgreSQL adds

### 2.1 The schema

Three tables in one schema, whose name is configuration and defaults to
`frostgrove_events`.

```
<schema>.schema_meta   singleton boolean PRIMARY KEY CHECK (singleton)
                       version     integer NOT NULL CHECK (version > 0)
                       log         bytea   NOT NULL CHECK (octet_length(log) = 16)
                       fingerprint text    NOT NULL
                       created_at  timestamptz NOT NULL DEFAULT clock_timestamp()

<schema>.streams       family  text   NOT NULL CHECK (octet_length(family) BETWEEN 1 AND 128)
                       key     text   NOT NULL CHECK (octet_length(key) BETWEEN 1 AND <MaxKey>)
                       version bigint NOT NULL CHECK (version > 0)
                       PRIMARY KEY (family, key)

<schema>.events        position    bigint GENERATED ALWAYS AS IDENTITY
                                          (INCREMENT 1 CACHE 1 NO CYCLE) PRIMARY KEY
                       family      text   NOT NULL CHECK (octet_length(family) BETWEEN 1 AND 128)
                       key         text   NOT NULL CHECK (octet_length(key) BETWEEN 1 AND <MaxKey>)
                       version     bigint NOT NULL CHECK (version > 0)
                       type        text   NOT NULL CHECK (octet_length(type) BETWEEN 1 AND 128)
                       revision    integer NOT NULL CHECK (revision > 0)
                       payload     bytea  NOT NULL CHECK (octet_length(payload) <= <MaxPayload>)
                       recorded_at timestamptz NOT NULL
                       UNIQUE (family, key, version)
                       FOREIGN KEY (family, key) REFERENCES <schema>.streams (family, key)
```

What each thing is for, and why it is in the database rather than in Go:

| Thing | What it guarantees | What it replaces |
|---|---|---|
| `UNIQUE (family, key, version)` | Two events can never occupy one version of one stream, whatever any writer believes | A Go comparison, which holds only for writers that ran the comparison |
| `streams` PK `(family, key)` | One row per stream, and therefore one row to lock | Nothing — it is what makes a lost race a clean answer rather than an aborted statement (§2.3) |
| `FOREIGN KEY (family, key)` | An event row with no stream row is unrepresentable | A Go promise that the append writes both |
| `position` identity, `PRIMARY KEY` | A total assignment order over rows, drawn at insert | An application-maintained counter |
| `CACHE 1 NO CYCLE` on that identity's sequence | Values are handed out in increasing order over time, which is the first of the read watermark's two premises (§2.6) | A default that silently widens gaps and, with `CYCLE`, breaks the argument outright |
| Xid-first trigger (below) | Every transaction that inserts into `events` has a transaction id **before** the first position of that statement is drawn — the watermark's second premise (§2.6) | An accident of the shape of one statement, which the next writer and the next refactor do not inherit |
| Append-only trigger (below) | `UPDATE`, `DELETE` and `TRUNCATE` on `events` are refused by PostgreSQL | The absence of an update statement in this package, which says nothing about the next package |
| `octet_length` checks | The bounds the store publishes in `Limits()` are the bounds the deployed schema enforces | A store that publishes 64 KiB and a schema that accepts 4 MiB, discovered when a reader refuses a row |
| `schema_meta.version` / `.fingerprint` / `.log` | Which schema this is, whether it is exactly the one this build expects, and which log a cursor was minted over | An assumption (§2.2) |

**Append-only is enforced by a trigger, not by the absence of a statement.**

```sql
CREATE FUNCTION <schema>.events_are_append_only() RETURNS trigger
  LANGUAGE plpgsql AS $$ BEGIN
    RAISE EXCEPTION 'eventpg: % on an event row: history is append-only', TG_OP; END $$;
CREATE TRIGGER events_append_only_row BEFORE UPDATE OR DELETE ON <schema>.events
  FOR EACH ROW EXECUTE FUNCTION <schema>.events_are_append_only();
CREATE TRIGGER events_append_only_truncate BEFORE TRUNCATE ON <schema>.events
  FOR EACH STATEMENT EXECUTE FUNCTION <schema>.events_are_append_only();
```

It fires on nothing an append does, so it costs the write path nothing, and it
makes §INV-001 a property of the database that a migration, a maintenance script
or the next package cannot quietly break. It is also the thing a later retention
or archival feature has to change **on purpose**, with its own decision, rather
than discovering that `DELETE` worked.

**A transaction has an xid before it draws a position, and that is a schema
object too.**

```sql
CREATE FUNCTION <schema>.events_assign_writer_xid() RETURNS trigger
  LANGUAGE plpgsql AS $$ BEGIN
    PERFORM pg_current_xact_id(); RETURN NULL; END $$;
CREATE TRIGGER events_position_needs_xid BEFORE INSERT ON <schema>.events
  FOR EACH STATEMENT EXECUTE FUNCTION <schema>.events_assign_writer_xid();
```

A `BEFORE INSERT ... FOR EACH STATEMENT` trigger fires once, before the
statement produces its first row and therefore before the identity column's
`nextval` is evaluated. `pg_current_xact_id()` assigns a transaction id if this
transaction has none and returns the existing one otherwise. The writer was
going to acquire an id anyway — it is inserting — so nothing is consumed that
was not already spent, and the cost is one plpgsql call per append statement,
not per row.

Why it is in the database rather than in the statement: without it, the premise
that the whole global-walk watermark rests on (§2.6, premise 2) holds only
because §2.3's CTE writes `streams` before the outer `INSERT` evaluates the
identity default. That is an accident of one statement's shape. It is not
inherited by a reordering of that statement, by a future schema version, or by
any other writer to `events` — and §UC-093 already admits another writer exists.
As a trigger it is a property of the table, it holds for every writer, and it is
one of the objects level 3 compares (§2.2).

**No index is created that no statement uses.** `ReadStream` rides the unique
index on `(family, key, version)`; the global walk rides the `position` primary
key; admission rides the `streams` primary key. Three access paths, three
indexes, and the deployment carries no fourth.

### 2.2 The schema profile, and what the comparison compares

The rule is [[D-101]]'s and it is not restated, only applied: the zero value is
`UnsetSchemaManagement`, which resolves to `VerifySchema`; only an explicit
`ManageSchema` creates or migrates anything; the operator path is
`MigrationStatements`. `New` performs no I/O. `Prepare(ctx)` migrates under
`ManageSchema`, then verifies under both, and until it has returned nil the store
refuses every operation as a matter of its own policy — `Refused`, never
`Closed`, because nothing was tried and the caller's transaction is untouched.

**What verification compares, in three levels, each catching a class the others
cannot:**

1. **The version integer** in `schema_meta`, compared for **equality**, not
   `>=`. An older schema and a newer one both fail closed, and the message names
   both numbers. `>=` is how a new binary writes rows an old replica cannot read,
   and it fails at the first read rather than at start-up.
2. **The fingerprint**: a SHA-256 over the canonical rendering of everything this
   build expects — every table, column, type, nullability, key, index, constraint
   definition and the two configured bounds — written into `schema_meta` at
   migration time and compared byte for byte at verification. This is the level
   that catches *a schema of the right version migrated by a different build*:
   somebody changed a check constraint or a bound and did not touch the version
   number.
3. **The catalog itself**, read from `pg_catalog` and compared to the same
   expectation: the tables and their columns; the `streams` primary key; the
   `events` primary key, its identity column, and that column's sequence with
   `CACHE 1`, `INCREMENT 1` and no `CYCLE` — which is not decoration, it is the
   first premise of the read watermark (§2.6); the unique constraint
   on `(family, key, version)` — present, unique, valid, not partial, exactly
   those three columns in that order, ascending, btree; the foreign key; every
   check constraint by normalised definition, `convalidated` and not
   `connoinherit`; and the classes below, compared as **sets**. This is the level
   that catches *a hand-edited database*, where the fingerprint still matches
   because nobody rewrote it.

**Level 3 asks what else is there, not only what is missing.** A hand-edited
database is at least as likely to have gained an object as to have lost one, and
the gained ones are the ones that fail silently: `ALTER TABLE events ENABLE ROW
LEVEL SECURITY` with a policy makes every read return a filtered subset, and a
short page is the end of a stream (§UC-087), so an aggregate folds from a
truncated history and nothing anywhere raises. So for the classes where an extra
object changes what a read returns or what a write records, the comparison is
**set equality** over the three tables, not presence:

| Class | Compared as | What an extra one does if nobody looks |
|---|---|---|
| Triggers (name, timing, event set, level, function) | exact set — three on `events`, none on `streams` or `schema_meta` | A third `BEFORE INSERT` trigger rewrites or suppresses a row: the fact recorded is not the fact decided, and §INV-048 is true while the payload is not. A trigger on `streams` moves admission out from under §INV-046 |
| Rules (`pg_rewrite`, other than a view's own `_RETURN`) | none on any of the three | A rule on `events` silently redirects or suppresses an insert |
| Row-level security | `relrowsecurity` and `relforcerowsecurity` false, and no `pg_policy` row, on all three | Silent history truncation on every read — the worst outcome this document names |
| Column defaults and generated-ness | exact per column: only `position` is identity, only `created_at` has a default | A default on `payload` or a generated `recorded_at` rewrites what was appended |
| Relation kind and inheritance | ordinary tables (`relkind = 'r'`), not views, not partitioned, not foreign, and `relhassubclass` false | A child table joins every read of `events` with none of its constraints; a view or a partitioned parent changes what the identity column even is |

**What is deliberately exempt, and why.** An extra *index* is not compared: a
non-unique one changes plans and not results, and a unique one refuses an append
the store should have admitted — which is a `23505`, classified `NotWritten`,
surfaced as `ErrBackend` at the first write. Extra *constraints* are exempt for
the same reason: they fail loudly at write time rather than quietly at read time.
So are comments, ownership, privileges, storage parameters and extra tables in
the schema, none of which this store reads. The line is not "everything" — it is
**every class whose extra member is invisible**, and every exemption above names
the loud failure that makes it safe.

Level 3 is the expensive one and it is the one that matters: a dropped unique
constraint is a store that admits two events at one version and reports success
to both writers, and an added policy is a store that silently forgets half its
history — and neither of the other two levels can see either. It runs once, at
`Prepare`, and never on the request path.

**The configured bounds are part of the schema.** `MaxPayload` and `MaxKey` are
`CHECK` constraint operands and therefore fingerprint inputs; `MaxBatch`,
`StreamPage` and `MaxRead` are Go-side ceilings that touch no row and are not.
That split is why `Schema` and `Spec` are two types (§5): a number that changes
the deployed schema cannot be set from the same struct field as one that does not.

**One migration at a time, on one connection.** `Migrate` checks a connection out
of the pool with `(*sql.DB).Conn(ctx)` and does everything on that one
`*sql.Conn`: `pg_advisory_lock` keyed by a digest of the schema name, then
`BEGIN`, then the statements, then `COMMIT`, then `pg_advisory_unlock`, then
`Close`, which returns the connection to the pool and closes no `*sql.DB`. That
is `jobspg`'s `withMigrationLock` and it is copied rather than reinvented.

The pinning is the whole point and it is not a detail. **A session-level advisory
lock taken over a `*sql.DB` serialises nothing**: `pg_advisory_lock` belongs to a
session, a `*sql.DB` is a pool, so the lock is taken on whichever connection the
pool hands out, the `CREATE TABLE`s run on the next one, and the unlock runs on a
third — where it returns false and the server logs that nobody owns that lock.
Two replicas would then race `CREATE TABLE` exactly as if no lock existed, and
PostgreSQL's answer to that race is not clean: concurrent `CREATE TABLE IF NOT
EXISTS` or `CREATE FUNCTION` on one name raises `23505` on
`pg_type_typname_nsp_index` in one of the two sessions. §UC-073's "no statement
errored" would be intermittently false, which is the worst shape of red — a gate
that passes most runs.

`pg_advisory_xact_lock` inside the migration's own transaction was the other
candidate and is rejected, not forgotten: it is pinned by construction and needs
no unlock branch, but it works only while every migration fits in one
transaction. The first schema version that needs a `CREATE INDEX CONCURRENTLY`
cannot be in one, and the store would then have two locking mechanisms for one
question. One mechanism that covers every version beats a simpler one that covers
version 1.

`MigrationStatements` is the operator's list and, for schema version 1, is safe
to run inside one transaction — there is no `CREATE INDEX CONCURRENTLY` in it,
because every index is created with its table. A later version that needs one
splits the list and says so in `MIGRATIONS.md`.

**A list records only a schema it can produce.** Version 1's statements create
what is absent and replace the two functions and the three triggers; they alter
no table. Against a schema some other expectation built they therefore change
nothing, and writing this build's fingerprint into `schema_meta` anyway would
record a fact that is false — and would erase the one piece of evidence level 2
reads. The list's last statement asserts instead: it raises unless `schema_meta`
holds this version at this fingerprint, which rolls the whole migration back and
leaves the drift where an operator can see it. A version N that carries the
`ALTER`s transforming N-1 into N *does* produce the new description, and its last
statement is an `UPDATE` guarded on the version it migrates from followed by the
same assertion.

**The schema name is quoted in every statement.** The name rule — 1 to 63 bytes
of `[a-z][a-z0-9_]*` — is a narrowing this store chooses, not the PostgreSQL
grammar: about a hundred reserved words match it and none of them may follow
`CREATE SCHEMA` unquoted. A validator that reports success for an input it cannot
serve is the naive contract this document refuses, so the identifier is emitted
quoted everywhere rather than checked against a word list that changes between
PostgreSQL versions. The rule stays because a name that reads the same quoted and
unquoted is one an operator can type back, and the refusal says that is the
reason.

**`Migrate` therefore opens a transaction, and that is said out loud rather than
smoothed over** (§INV-051). It is not one of the eight `event.Store` methods, it
runs before the store serves anything, and the transaction is its own — never a
caller's. The alternative, running eight DDL statements on autocommit, is
§UC-069's forbidden half-created schema.

### 2.3 Admission, isolation, and the two shapes of a lost race

**The whole append is one statement.** Not one transaction — one *statement*:

```sql
WITH admitted AS (
  INSERT INTO <schema>.streams AS s (family, key, version)
  SELECT $1, $2, $3::bigint + $4::bigint
   WHERE $3::bigint = 0
      OR EXISTS (SELECT 1 FROM <schema>.streams WHERE family = $1 AND key = $2)
  ON CONFLICT (family, key) DO UPDATE SET version = s.version + $4::bigint
   WHERE s.version = $3::bigint
  RETURNING version
)
INSERT INTO <schema>.events (family, key, version, type, revision, payload, recorded_at)
SELECT $1, $2, $3::bigint + record.ord, record.type, record.revision, record.payload,
       statement_timestamp()
  FROM admitted,
       (VALUES ($5::text, $6::int, $7::bytea, 1::bigint),
               ($8,       $9,      $10,       2)
               -- one row per record, generated: $(3n+2), $(3n+3), $(3n+4), n+1
       ) AS record(type, revision, payload, ord)
 ORDER BY record.ord
```

*(Shape, not final text. The exact statement is the plan's, and it is a large raw
SQL statement, which is one of the four things the house style says may carry a
comment.)*

**Why a `VALUES` list and not `unnest($5::text[], $6::int[], $7::bytea[])`.** The
`unnest` form is shorter and it is the one a reviewer will propose, so the
rejection is recorded. Binding a Go slice as a PostgreSQL array is not something
`database/sql` can do: `driver.DefaultParameterConverter` refuses `[]string` and
`[]int32` outright, and the binding works only because a driver implements
`driver.NamedValueChecker` and takes the slice itself. `pgx/v5/stdlib` does;
`lib/pq` does not, and asks for `pq.Array`, which is a driver import in
production code. An `unnest` append therefore works on exactly the driver this
module's fixtures register and fails at run time on the other mainstream
PostgreSQL driver, while §1 and non-goal 4 promise the opposite — and the live
gate, which runs on `pgx`, could never see it. The `VALUES` form binds `string`,
`int32` and `[]byte`, which every `database/sql` driver binds, so the claim in §1
stays true.

What it costs, stated rather than discovered: **the statement text depends on the
record count**, so a prepared-statement cache holds one entry per batch size
actually used, bounded by `MaxBatch` (default 64, kernel ceiling
`event.MaxBatchCount` = 1024). Padding every append to `MaxBatch` rows and
trimming with a `WHERE record.ord <= $n` would give one text and was rejected: it
puts bytes on the wire that no caller wrote, and it makes a one-event append pay
for the largest one anybody configured.

`ORDER BY record.ord` is not decoration either. §UC-084 requires that the
positions of one batch ascend in the order the caller listed its changes, and
neither a `VALUES` scan nor an `unnest` promises the order rows reach the
`INSERT` without it.

Five properties follow from it and each is load-bearing:

- **Atomicity is not borrowed from a transaction.** A single statement is atomic
  in PostgreSQL whether or not it runs inside an explicit transaction, so the
  version advance and the event rows commit or roll back together on the
  autocommit path exactly as they do inside the caller's. `eventpg` therefore
  opens, commits and rolls back nothing, and the `event.Store` contract's "none
  of the eight opens, commits or rolls back anything" is met literally rather
  than by an exception for the unbound case.
- **Admission is a conditional row update, never a read-then-write.** There is no
  `SELECT version` followed by a decision in Go. The predicate
  `s.version = $expected` is evaluated by PostgreSQL against the row it has
  locked, which is what makes it correct under concurrency; a Go comparison is
  correct only against a snapshot nobody else can move.
- **A lost race is zero rows.** The outer `INSERT ... SELECT FROM admitted`
  produces `len(records)` rows or none. `RowsAffected() == len(records)` is
  admission; `0` is `event.Failure(event.Conflict, …)`; anything else is a
  defect this store cannot classify and it returns an **unclassified** failure,
  which the append door reads as uncertainty (§INV-045) — the correct answer,
  because a wrong row count means the store does not know what it left behind.
- **The fresh-stream race is the same answer.** Two writers at `Expected = 0`:
  one inserts the `streams` row, the second blocks on the speculative insertion,
  then takes the `DO UPDATE` path, finds `s.version = 1`, fails the `WHERE`, and
  gets zero rows. No unique violation is raised on the `streams` key, ever.
- **The unique index is the floor under all of it.** If the `streams` row and the
  event rows ever disagree — a defect here, a second writer that bypassed this
  package, a restored dump — `UNIQUE (family, key, version)` is what stops two
  events at one version from both committing. It is unreachable through this
  store, so the live suite reaches it deliberately (§6.9) rather than trusting
  that it is there.

**Which isolation level does this store use? None of its own, and that is the
design rather than an omission.** On the autocommit path the statement runs at
the session default. Inside the caller's transaction the level is the caller's,
and the store could not change it if it wanted to — `SET TRANSACTION ISOLATION
LEVEL` must be the first statement of a transaction the store did not begin. So
the store's correctness **must not depend on an isolation level**, which is
exactly why admission is a conditional update plus a unique constraint and not a
read followed by a write. It is correct at all three levels; what differs is what
the loser is told.

| Where the losing append ran | What PostgreSQL does | What the store returns | What the caller does |
|---|---|---|---|
| Autocommit, or a `READ COMMITTED` caller transaction | The `DO UPDATE` predicate re-evaluates against the winner's committed row and fails | `Conflict` → `ErrConflict` | Load again, decide again (§UC-022) |
| A `REPEATABLE READ` or `SERIALIZABLE` caller transaction | `40001` — the update would have to see a row written after the transaction's snapshot | `NotWritten` + a retryable cause → `ErrBackend` wrapping `crud.ErrUnavailable` | Roll back and retry **the whole transaction** |
| Any level, lock waits exhausted | `40P01` deadlock, `55P03` lock timeout | `NotWritten` + retryable | Roll back and retry |

`40001` is deliberately **not** reported as a conflict. Under `SERIALIZABLE` it
can be raised by a dangerous dependency involving tables this store never
touched, so it is not evidence that the stream moved; and its obligation is
different — `ErrConflict` says *reload the aggregate*, while a serialisation
failure says *the transaction you are in is dead*. `errs/sqlerr` already maps
`40001` to `errs.CodeSerializationFailure` and `40P01` to `errs.CodeDeadlock`,
both `errs.KindRetryable`, and the kernel's `backendRefusal` already turns a
retryable cause into `ErrBackend` wrapping `crud.ErrUnavailable`. The store
selects the outcome; nothing new is invented.

**How the cause is made retryable, so the plan does not guess.** The kernel reads
retryability two ways — `errors.Is(cause, crud.ErrUnavailable)`, or an
`*errs.Fault` in the chain whose `Kind` is `errs.KindRetryable`. `eventpg`
classifies the driver error the way the rest of the framework does,
`sqlfault.Extract` then `sqlerr.Classify("postgres", …)`, and builds the second
spelling: an `errs` fault carrying the code that classification produced. Neither
the SQLSTATE text nor the driver's message travels — the kernel renders the
sentinel and nothing of the cause (§INV-025), and the cause stays reachable
through `event.CauseOf`.

The consequence is documented rather than hidden: **inside a `REPEATABLE READ` or
`SERIALIZABLE` caller transaction, a losing concurrent append is a retryable
backend failure and not `ErrConflict`.** That is not a contract violation —
`event.Store.Append` promises a conflict is reported from `Append` and never from
the caller's commit, and it is — and it is not what the conformance suite
measures, because the suite's `concurrency` section races on the unbound path.
§6.10 pins it live so nobody discovers it in production.

**Waiting is allowed and this store waits.** `Append` blocks on a competing
writer's row lock rather than refusing at once. The contract leaves that to the
store; a `NOWAIT` variant would turn every lock wait into a conflict the caller
must re-decide, which is worse for the common case and better for none.

### 2.4 Joining the caller's transaction, and what a receipt proves

[[D-118]]'s rule, applied without a second spelling. `Transaction(ctx)` asks
`crud.ExecutorFor(ctx, this.source)` and answers the three answers and no fourth:

| What is found for this store's source | Answer | Then |
|---|---|---|
| nothing | invalid authority, no error | the operation runs on the pool, in autocommit |
| a transaction `crudsql.Transaction` yields a `*sql.Tx` from | `event.NewAuthority(backing, tx)`, no error | the operation runs **inside it** |
| an executor that is not a transaction, or one no `*sql.Tx` can be taken from | invalid authority **and** an error | the operation refuses before any statement |

Three refusals sit around it, and all three are in `New` or in the method, never
in a comment:

- **One database.** `New` refuses a `Spec` whose `Source` is not the same data
  source as its `DB` — `crud.SameDataSource(crud.KeyOf(spec.Source), spec.DB)`,
  the same refusal `jobspg` makes. "Atomic" across two handles is a sentence with
  no meaning.
- **The `Source` is required.** `jobspg` allows a driver with no source, because
  a queue that always opens its own transaction is a legitimate configuration.
  An event store's whole point is that a decision and its facts commit together,
  so a store that cannot see the caller's transaction is not a degraded
  configuration, it is a trap: it would have to answer
  `Capabilities().Transactions = Unsupported`, and a deployment that assembled
  one by leaving a field nil would get silent autocommit writes. `New` refuses a
  nil `Source`.
- **No quiet fallback**, for reads as well as writes. An ambient executor that is
  not a transaction refuses at all four doors, not only at `Append`. A store that
  read through an unknown executor and wrote through the pool is the shape that
  produces "the read saw it and the write did not".

**Every statement goes to the same place.** When a transaction of this store's
backing is bound, `ReadStream`, `ReadAll` and `Append` all execute on that
`*sql.Tx`; when nothing is bound, all three execute on the `*sql.DB`. One rule,
so `ReadStream`'s contractual obligation to return the caller's own staged
appends is a consequence rather than a special case.

**What the receipt proves.** `Commit.Authority()` carries
`event.NewAuthority(backing, tx)` where `tx` is the live `*sql.Tx` the statement
ran on. `Authority.Same` is pointer identity over that value plus backing
equality, so two subsystems comparing receipts are comparing the *transaction
object*, not a description of one. A datasource pointer would be copyable and
would compare equal for two different transactions on one pool; a `*sql.Tx`
cannot be forged into equality without being the same transaction.

Two consequences are stated rather than left to be discovered:

- **A savepoint answers its parent's authority.** `crudsql`'s savepoint executor
  answers its own `Tx()` with its **parent's** `*sql.Tx`, so a receipt taken inside
  a savepoint and one taken outside it compare `Same`, and a savepoint rollback
  discards events whose receipt still names a live authority. This is the
  phase-1 debt the memory store had no vocabulary for; it is pinned live in §6.12.
- **A receipt is not a commit.** `event.Commit` is a receipt for an *append*. On
  the joined path durability is decided later, by whoever owns the transaction
  the authority names. Nothing in `eventpg` learns the outcome of that commit and
  nothing in `eventpg` reports it.

**The backing is the database and the schema together.** `Backing()` is minted
over a comparable unexported value holding the `*sql.DB` and the schema name, not
over the `*sql.DB` alone. Two schemas in one database are two logs; a token or a
cursor from one must not be accepted by the other, and `*sql.DB` alone would
accept both. Two store values over one database and one schema — a sibling, a
restart, a second process — compare `Equal`, which is what §UC-054 and §UC-053
require.

### 2.5 Commit uncertainty, and the window where it cannot happen

Phase 1 fixes the vocabulary (§UC-034, §UC-057, §INV-045) and the caller's
bounded recovery procedure. Phase 2 owes only the mapping from PostgreSQL
situations onto those windows, and one fact that inverts the usual expectation.

**The rule comes first, and the table is its worked examples.** A list of
situations is not a classification: everything not on the list falls to a default
branch, and phase 1's own sketch (§D.15) ends `default: return
event.Failure(event.NotWritten, …)` — which declares *certainly did not land*
about every error nobody thought of. `NotWritten` is the strongest claim this
store makes; `ErrBackend` is the only outcome the conformance suite checks the
stream against; and a store that says it about a write that landed is the single
failure this whole section exists to prevent. So:

> **On the autocommit path, `NotWritten` is selected only on proof, and there are
> exactly two proofs.**
>
> 1. **Nothing was issued.** The failure happened before the statement reached
>    the server: the context was already done on entry, the connection checkout
>    failed, or the driver answered `driver.ErrBadConn` — which
>    `database/sql`'s driver contract permits **only** when the driver is certain
>    the server did not receive the query, which is precisely this proof.
> 2. **The backend rejected it and survived to say so.** PostgreSQL answered with
>    a SQLSTATE, and that SQLSTATE is not one of the codes that arrive *with* the
>    session ending — class `08` (connection exception) and `57P01` / `57P02` /
>    `57P03` (admin shutdown, crash shutdown, cannot connect now). A code the
>    backend sent while staying alive is proof it processed the statement, aborted
>    it, and rolled it back; a code that accompanies the connection dying is not,
>    because the statement may already have committed when the FATAL was raised.
>
> **Everything else is `Unconfirmed`. There is no default branch that answers
> `NotWritten`.**

The SQLSTATE is read with `sqlfault.Extract`, which finds it through a
`SQLState() string` method or a `SQLState` field and therefore needs no driver
import (§1). Outcome selection is driven by the SQLSTATE itself, not by whether
`sqlerr.Classify("postgres", …)` recognises it: `57014` is not in that table and
is still a server answer. Classification supplies the *cause*'s code where it
has one, and nothing more.

| Situation | Outcome the store selects | Why |
|---|---|---|
| Context already cancelled before the statement is issued | bare `ctx.Err()` | Nothing was issued (§UC-057 window one) |
| Autocommit; connection checkout fails — pool exhausted, dial refused, a DSN that resolves nowhere | `NotWritten` | Proof 1: no statement reached a server. Every start-up blip would otherwise send callers into §UC-034's recovery for a write that never left the process |
| Autocommit; a clean SQL error (`23514`, `23505`, `22001`, `42P01`) | `NotWritten` | Proof 2: PostgreSQL rolled the statement back and stayed to say so |
| Autocommit; `40001` / `40P01` / `55P03` | `NotWritten`, cause retryable | Proof 2, and the caller may retry |
| Autocommit; `57014 query_canceled` from `statement_timeout` or `pg_cancel_backend` | `NotWritten` | Proof 2. A cancel is refused inside the commit-critical section, so a `57014` answer means the statement did not commit |
| Autocommit; the context is cancelled and the driver answers `context.Canceled` / `context.DeadlineExceeded` with no SQLSTATE | `Unconfirmed` | Neither proof. The client stopped waiting; the server may have committed |
| Autocommit; `driver.ErrBadConn` | `NotWritten` | Proof 1, by the driver contract: a driver may return it only when it is certain the server did not receive the query |
| Autocommit; connection lost or reset — `08006`, `57P01` from `pg_terminate_backend`, a socket read or write error, an unexpected EOF | `Unconfirmed` | Neither proof. The statement is its own transaction and its commit is exactly what was not acknowledged |
| Autocommit; a driver error carrying no SQLSTATE at all | `Unconfirmed` | Neither proof — and this is the branch phase 1's sketch answered `NotWritten` |
| **Inside a bound transaction; any failure at all** | **`NotWritten`** | See below |
| Row count neither `0` nor `len(records)` | unclassified → `ErrUncertain` | The store does not know what it left behind, and says so by not classifying |

**Inside a bound transaction there is no uncertainty window.** A statement that
errors, is cancelled, or dies with its connection leaves the caller's transaction
aborted or gone; nothing it wrote can become durable, because durability needs a
`COMMIT` that PostgreSQL will now refuse. So the store answers `NotWritten` and
means it. Uncertainty exists on the autocommit path and only there — which is the
opposite of the intuition that "inside a transaction is where things get
unclear", and it is worth stating because a store that reported `Unconfirmed`
from the joined path would send every caller down §UC-034's recovery procedure
for a write that certainly did not land.

**What the framework must not do**, restating only the PostgreSQL-specific
temptations: it must not re-read the stream after a failed append to work out
whether it landed — the answer is not stable, because an in-flight autocommit
statement can commit after the check; it must not retry the statement, at any
level, for any driver error; and it must not convert `Unconfirmed` into
`ErrConflict` because a later read happened to show the events present.

**The path that does not re-execute, named rather than implied.** "Do not retry"
is not a discipline here, it is a choice of API, because the obvious call
violates it. `(*sql.DB).ExecContext` retries a `driver.ErrBadConn` on up to two
cached connections and then once more on a fresh one — **three executions of one
append**, on exactly the path where §2.5 says uncertainty exists at all. The
observable failure is the one this section is built to prevent: the first attempt
lands, the retry is refused at the same `Expected`, and the caller is told
`ErrConflict` for a write of its own that succeeded.

So on the autocommit path every statement — the append and both reads — is issued
on a connection the store checked out itself:

- `conn, err := this.db.Conn(ctx)`, then `conn.ExecContext` / `conn.QueryContext`.
  `(*sql.Conn)` is pinned to one `driver.Conn` and has no `ErrBadConn` retry loop;
  `(*sql.Tx)` on the bound path has none either. Those are the only two
  `database/sql` calls that execute a statement exactly once.
- The store closes the connection in a `defer` on the same call. `Conn.Close`
  returns it to the pool and never closes the `*sql.DB`, which belongs to the
  composition root (§UC-095). A connection the pool finds broken is discarded by
  the pool, not by this store.
- **Checkout failure is not uncertainty.** `Conn` may itself fail — pool
  exhausted, dial refused, context done. Nothing was issued, so the answer is
  `NotWritten` by proof 1 of the rule above, and the cause is classified the way
  every other driver error is.
- `Conn` retries *acquiring* a connection, which is not re-execution: no
  statement of this store's has been sent when that loop runs.

A retried *read* is harmless — it is idempotent, and a re-execution just answers
from a newer snapshot. The reads take the same path anyway, for two reasons that
are not symmetry. The store then contains **no call to a statement method on
`*sql.DB` at all**, which is a source check a reviewer can run rather than a
discipline somebody has to remember at the next door added. And §2.6's walk
issues up to three statements in one `ReadAll`, which is one checkout instead of
three.

**What the caller does** is §UC-034's three-step procedure and nothing else, and
PostgreSQL makes step 1 exact: one retry with the same token. It carries the same
`Expected`, so it is admitted **if and only if** the first attempt did not land.
Success proves the first attempt was lost; `ErrConflict` proves it landed, or that
somebody else wrote, and either way the caller reloads. That is the whole
resolution, and it exists because admission is expected-version rather than blind.

### 2.6 Ordered reads: the stream, the log, and the watermark cursor

**One stream in order** is the easy half. `ReadStream(ctx, s, after)` is
`WHERE family = $1 AND key = $2 AND version > $3 ORDER BY version LIMIT
StreamPage`, riding the unique index. A short page is the end of the stream and
the kernel issues no confirming read, so the page size and the `LIMIT` are the
same number by construction. Inside a bound transaction it sees that
transaction's own staged appends, because it runs on that transaction.

**The global walk is the hard half, and the honest answer is that positions are
not gap-free and not commit-ordered.** `position` comes from an identity column,
so a value is drawn when a row is inserted, not when it commits. Two things
follow, both normal and neither hideable:

- **Gaps.** A rolled-back append burns its positions for good (§INV-009 already
  says so). Nothing renumbers.
- **Out-of-order commits.** A writer holding position 5 can still be uncommitted
  when the writer holding 7 commits. A reader that returned 7 and checkpointed
  there would never see 5.

The kernel forbids the easy way out: `Reader.checkPage` refuses a page whose
positions do not ascend, so a store cannot deliver 5 after 7 and call it caught
up. Phase 1 already decided the answer in §UC-037 — **the cursor is not the
store's newest position; it is a point below which no writer can still commit**,
and `MonotoneVisibility` is a *freshness* claim, not a safety one. So:

**`eventpg` publishes `MonotoneVisibility: Unsupported` and its cursor is a
settled watermark.** A consumer sees every event, once per pass, in position
order, slightly later than a store that could promise freshness. Nothing is
skipped. The `monotone visibility` conformance section is reported *not
certified* for this store, which is the honest verdict and not a failure.

**The cursor and the rule.** A cursor is an opaque string carrying a format tag,
the 16-byte `log` from `schema_meta`, and three unsigned numbers `(F, X, R)` —
or it is the empty string, which is the origin of the log:

- every event at position `≤ F` has already been returned by this walk;
- if `X > 0`, then `X` is a transaction id **minted after** every position `≤ R`
  had been drawn, so any position in `(F, R]` that is still invisible once a
  later snapshot reports `pg_snapshot_xmin > X` is burnt and will never appear.

**The empty cursor is the origin, and it is not a format error.** It is the one
cursor value that carries no log id, and it is how every walk starts:
`event.Read(log, "")` is the documented way to begin with no persisted
checkpoint, `eventmemory` maps it to position 0
(`event/eventmemory/cursor.go:27`), and the conformance suite itself issues it in
three places — `probe.tail` when no `Tail` hook is supplied, `injectedAtRead` in
the store-failure section, and `event.Read(store, "")` in the binding section. It
reads as `(F, X, R) = (0, 0, 0)`: nothing delivered, no bound outstanding. A
store that answered `ErrCursor` for `""` could not be started by any consumer,
and the failure would arrive at the first read of a fresh deployment. §UC-091's
"a cursor that is not this format answers `ErrCursor`" is the sentence that
produces exactly that store, so §UC-091 excludes `""` by name.

The read is one statement, one snapshot for both halves:

```sql
SELECT h.floor_xid, e.position, e.family, e.key, e.version,
       e.type, e.revision, e.payload, e.recorded_at
  FROM (SELECT pg_snapshot_xmin(s) AS floor_xid FROM pg_current_snapshot() AS s) h
  LEFT JOIN LATERAL (SELECT * FROM <schema>.events
                      WHERE position > $1 ORDER BY position LIMIT $2) e ON true
```

The `LEFT JOIN LATERAL` is why an empty page still carries a horizon. Then:

1. `settled = R` when `X > 0 and floor_xid > X`, else `settled = F`.
2. Walk the returned rows in position order, delivering while the next position
   is `F+1` or the whole gap before it lies at or below `settled`. Stop at the
   first gap that is neither.
3. If the walk stopped at a gap, mint a bound: `SELECT pg_current_xact_id()`.
   The new cursor is `(F', bound, highest position returned)`. Otherwise it is
   `(F', 0, 0)`.
4. **Only when the delivered prefix is empty and a bound was just minted**, take
   one more snapshot and re-apply step 1. In a database nobody else is writing,
   the bound is already settled by then and the walk continues in the same call,
   so an ordinary gap costs one extra round trip and never an empty page.

> **Why the bound is minted rather than read out of the snapshot, and how that
> was decided.** The obvious rule — "if `pg_snapshot_xmin = pg_snapshot_xmax`
> nothing is in flight" — is **false**, and it was falsified against PostgreSQL
> 17.9 while this document was being written rather than after the code was:
> with a writer holding an uncommitted append, an observing session reported
> `xmin = xmax = 56528` and an empty `xip`, because the writer's own xid *was*
> 56528. `xmax` is `latestCompletedXid + 1`, not the first unassigned id, so a
> running transaction can sit at or above it and appear in neither `xip` nor
> below `xmin`. `pg_current_xact_id()` is the one reading that is a real bound:
> it assigns an id, so every id assigned before it is strictly smaller, and
> `pg_snapshot_xmin > X` therefore proves every one of them has finished.
> Measured on the same pair of sessions: bound `X = 56532` minted while the
> writer held `56531`; `xmin = 56531 ≤ X` while it ran, `xmin = 56533 > X` after
> it committed.

**The two premises the settlement rule rests on.** "`pg_snapshot_xmin > X` proves
every position in the gap is burnt" is not one claim, it is two, and only one of
them is about the sequence. Both are written here because the argument is not
recoverable from the code and a refactor that keeps the code correct can still
break it.

1. **Positions are handed out in increasing order over time.** A *schema*
   property: the identity sequence has `INCREMENT 1`, `CACHE 1` and no `CYCLE`,
   so a value drawn later is larger, and level 3 of verification compares exactly
   those three (§2.2). Without it a position below the gap can be drawn after the
   bound and the whole ordering argument is gone.
2. **A transaction has its id before it draws a position.** Also a *schema*
   property, and only because §2.1 makes it one: the
   `events_position_needs_xid` statement trigger assigns the xid before the
   statement produces its first row. Without premise 2 a writer can draw position
   `p`, have `X` minted by a reader a moment later, and only then be assigned an
   xid `> X`. `pg_snapshot_xmin > X` is then true *while that writer is still
   running*, the reader declares the gap at `p` burnt, delivers `p+1`, and `p` is
   **never delivered** after it commits. That is silent event loss, and neither
   `checkPage`, nor any conformance section, nor an audit query can see it.

Premise 2 is worth one paragraph on how nearly it went unstated. It happens to
hold for §2.3's statement without the trigger, because the `admitted` CTE writes
`streams` first and the xid is therefore assigned before the outer `INSERT`
evaluates the identity default. That is an accident of one statement's shape. It
is falsified by reordering that statement, by splitting the fresh-stream case out
of the CTE, by a future schema version that draws the position before the first
heap write, and by **any other writer to `events`** — which §UC-093 explicitly
admits exists. A trigger holds for all four; a comment above the statement holds
for none.

**What the watermark guarantees when the writer is not `eventpg`.** With the
trigger in place, every writer that lets the identity column draw the position —
`INSERT`, `INSERT ... SELECT`, `COPY`, from any client in any language — is
covered, because the trigger fires before the statement's first row exists. One
writer shape is **not** covered and the limit is stated rather than hidden: a
writer that draws a position in one statement (`SELECT nextval(...)`) and inserts
it in a later one, which `GENERATED ALWAYS AS IDENTITY` forces it to spell
`OVERRIDING SYSTEM VALUE`. That writer can hold an unassigned transaction across
the bound and be skipped. It is a deliberate act, it is named in
`docs/modules/*/eventpg.md`, and the live gate drives it (§6.8) so the boundary
is measured rather than assumed.

Four consequences, said plainly:

- **A bound costs one transaction id, and only when a gap is seen.** A gap comes
  from a rollback, which already burned one. It is not spent per read.
- **A walk inside a caller's transaction never mints a bound and never settles a
  gap.** Minting one would assign an id to *the caller's* transaction, turning a
  read into a writer that holds back vacuum; and at `REPEATABLE READ` the
  snapshot is frozen anyway. The kernel already tells consumers to drain outside
  the write (`Reader.Cursor`); this is the PostgreSQL reason.
- **A walk can return an empty page while events exist**, because a lower
  position is still in flight. That is the watermark working. A consumer polls
  again; `Reader.Next` returning false means "nothing settled yet", not "the end".
- **The lag is bounded by the oldest open write transaction**, not by a timer.
  An application that holds a writing transaction open for an hour delays every
  consumer for an hour, and no amount of store-side cleverness can fix that
  without skipping the events it is waiting for.

**Three alternatives were considered and rejected**, recorded so they are not
rediscovered:

- *Draw positions from a counter row inside the append statement.* Gives
  commit-ordered, gap-free positions — and serialises every append in the store
  on one row for the whole of the caller's transaction, so one slow caller stalls
  every writer. Rejected for a framework that does not know its callers.
- *Deliver ordered by transaction id with a `xid8` column.* Sound, and it
  produces pages whose `position` values descend, which the kernel refuses.
- *Settle a gap from `pg_snapshot_xmax` observed at the sighting.* Cheaper —
  and unsound, for the reason measured above. It is named here because it is the
  rule a reviewer will propose.

**What a consumer may assume**, and it is the whole list: a walk starts from the
empty cursor and the empty cursor is the origin of the log; events arrive in
ascending `position`; none is skipped; none is repeated within one pass; the
cursor is opaque, is safe to persist and resume in another process, and is
refused by a store over a different log; positions have gaps; `RecordedAt` is not
an ordering key; delivery over repeated passes is at least once, because a
consumer that crashes after acting and before persisting its cursor acts again;
and "none is skipped" holds for every writer that lets the identity column draw
the position, which is every writer that does not spell `OVERRIDING SYSTEM
VALUE`.

### 2.7 Health

`Store.Check(ctx) error` is the readiness answer: the context's own error, then
closure, then "has `Prepare` returned nil", then one round trip that both proves
the pool answers and re-reads `schema_meta`'s version and fingerprint. It names
**no importance, no name and no code** — those belong to the composition root,
which builds the `health.Contribution` around it ([[D-091]]).

`eventpg` does not import `health`. The method matches `health.Probe`
structurally, exactly as `eventmemory.Store.Check` does, and the extension-cost
check in `scripts/event_test.go` allows this package one allowance
(`./crud/adapter/crudsql`) whose closure does not contain `health`. The same
reasoning excludes `port`: nothing here logs, so `port.Logger` is not reached and
`log.Printf` is a defect ([[D-062]]).

---

## 3. Use cases

Format is compressed against phase 1's: **Given** the state and the trigger,
**Then** what is observable, **Must not** the failure this case exists to
prevent, **Control** the case that fails if the positive one has stopped proving
anything. Everything phase 1 already specifies is referenced, not restated.

### Group P — Schema and profile

#### UC-069 A deployment migrates the schema from its migration step  [happy]
- **Given** An operator runs `eventpg.MigrationStatements(schema)` from the
  deployment's migration step against an empty database.
- **Then** The schema, three tables, their keys, indexes, constraints and all
  three triggers exist; `schema_meta` holds one row with the current version, a fresh
  random 16-byte `log`, and the fingerprint of exactly this build's expectation.
  A process started afterwards with nothing said about schema management verifies
  and serves.
- **Must not** The list must not require a privilege the application role does not
  have at run time, and must not leave a partially created schema behind when one
  statement fails — version 1 runs inside one transaction.
- **Control** The same list run twice is a no-op the second time and does not
  reissue a new `log`, so a re-run does not orphan every persisted cursor.

#### UC-070 A process starts with nothing said about schema management  [happy]
- **Given** `Spec{DB, Source}` with `SchemaManagement` left zero, against a
  schema at the expected version.
- **Then** `New` resolves the zero to `VerifySchema` and performs no I/O;
  `Prepare` creates nothing, migrates nothing, binds nothing, verifies all three
  levels, and the store serves.
- **Must not** No zero value and no absence of configuration may migrate
  ([[D-101]]). `SchemaManagement()` on a nil store must not claim it would.
- **Control** The same process against a *missing* schema refuses at `Prepare`
  and never at the first append.

#### UC-071 A process meets an older or a newer schema version  [edge]
- **Given** `schema_meta.version` is one below, or one above, this build's.
- **Then** `Prepare` returns `ErrSchemaMismatch` naming both numbers and the
  schema, before any append; the store stays not-ready and every operation
  answers `Refused` → `ErrRefused`.
- **Must not** A version comparison must not be `>=` in either direction, and the
  message must not contain a credential, a DSN or a row.
- **Control** The matching version passes with the same code path.

#### UC-072 The version matches and the database was hand-edited  [edge]
- **Given** The right version and fingerprint, and someone has changed the
  schema in one of two directions. **Removed or altered:**
  `events_family_key_version_key` dropped, the append-only trigger dropped, the
  xid-first trigger dropped, the foreign key dropped, a check constraint's
  definition altered. **Added:** row-level security enabled on `events` with a
  policy; a third `BEFORE INSERT` trigger on `events`; a `BEFORE INSERT` trigger
  on `streams`; a rule on `events`; a `DEFAULT` added to `events.payload`; a
  child table inheriting `events`.
- **Then** Level 3 refuses in both directions, naming the object that is wrong
  and what is wrong with it — missing, not validated, wrong columns, wrong
  definition, or **present and not expected**.
- **Must not** The store must not start on the fingerprint alone, and must not
  check only for absence. A dropped unique constraint is a store that admits two
  events at one version and tells both writers they won; an added policy is a
  store that silently returns a truncated history, which no error anywhere
  reports.
- **Control** The intact schema passes, and each mutation is a separate live case
  so a verifier that checks nothing fails all of them (§6.7).

#### UC-073 Two replicas start together with `ManageSchema`  [edge]
- **Given** Two processes calling `Prepare` at the same moment against an empty
  schema.
- **Then** One holds the advisory lock and migrates; the other waits, then
  verifies. Both end ready, one `log` exists, and no statement errored. The lock
  is observed **in `pg_locks`** — one `advisory` row, `granted`, on the migrating
  backend, for the whole of the migration and gone afterwards — rather than
  inferred from the absence of an error.
- **Must not** Two `log` values, a duplicate-object error surfaced to either
  caller (`23505` on `pg_type_typname_nsp_index` is what an unpinned lock
  produces, intermittently), or a lock held past the migration. The lock, the DDL
  and the unlock must be observed on **one** backend pid; three pids is the
  pooled-lock defect and it is what this case exists to catch.
- **Control** A single process takes the same path and the lock is released; and
  a second migration attempt while the lock is held by a `*sql.Conn` the test
  holds open blocks rather than proceeding, so a run in which nothing ever waited
  is not read as a pass.

#### UC-074 A build with a different payload bound meets a deployed schema  [edge]
- **Given** A schema migrated with `MaxPayload` 64 KiB and a process configured
  for 1 MiB.
- **Then** `Prepare` refuses before any append, and which door refuses depends on
  the profile, so both are named rather than left to be inferred. Under
  `VerifySchema` it is **level 2**: the fingerprints differ and the refusal names
  both digests and the bound this build is configured for. Under `ManageSchema`
  the migration runs first and refuses **before** level 2 is reached: its last
  statement finds a `schema_meta` this list cannot have written and raises,
  naming both digests, which rolls the migration back. Level 2 then never sees a
  fingerprint that was made to agree.
- **Must not** The store must not publish a `Limits()` the deployed schema will
  not accept. A payload the store admits and the database rejects is a `23514`
  at the worst moment. **And migrating must not rewrite `schema_meta` to agree
  with the running build**: a version whose statements alter no table cannot turn
  a deployed schema into the one it describes, so recording that description
  would record a fact that is false — and it would defeat level 2, `Check`, and
  the incident comparison `Fingerprint()` exists for, all at once.
- **Control** The matching bound passes through the same code path, `Limits().MaxPayload`
  equals the constraint's operand, and a re-run of the *same* build's list is a
  no-op that moves the fingerprint nowhere — without that control the refusal
  above is satisfied by a migration step that refuses everything.

#### UC-075 A store is used before it verified  [edge]
- **Given** `New` returned a store and `Prepare` was never called, or returned an
  error.
- **Then** `Capabilities()`, `Limits()` and `Backing()` answer, because they are
  pure; `Append`, `ReadStream` and `ReadAll` answer `Refused` → `ErrRefused`;
  `Transaction` answers what the context carries and reports no readiness.
- **Must not** No statement may be issued. The caller's ambient transaction must
  be usable afterwards, which is what `Refused` and not `NotWritten` says.
- **Control** After a successful `Prepare` the same calls serve.

### Group Q — Append

#### UC-076 An append lands with no transaction bound  [happy]
- **Given** A repository over a ready store, no ambient executor, one change at
  the loaded version.
- **Then** One statement; the stream row advances and the event row appears
  together; the receipt names versions `f..l`, count, and an **invalid**
  authority — the append was atomic with nothing.
- **Must not** The store must not begin, commit or roll back a transaction to
  achieve this.
- **Control** A second append at the stale token conflicts.

#### UC-077 Two writers race at one version  [edge]
- **Given** Eight goroutines that all loaded version 0 and append together, no
  transactions.
- **Then** Exactly one succeeds; the other seven answer `ErrConflict`; the stream
  holds exactly one event; a subsequent single append at the fresh version wins.
- **Must not** No writer may be told it won twice, and no writer may get a
  backend failure for a plain lost race at `READ COMMITTED`.
- **Control** The single writer at the end is the control; if the seven refusals
  were the store failing rather than conflicting, it fails too.

#### UC-078 An append joins the caller's transaction  [happy]
- **Given** `crud.InNewTx` over the store's own source; the caller writes its own
  rows and appends.
- **Then** `Transaction` answers a valid authority; the events are written inside
  that `*sql.Tx`; a `ReadStream` in the same transaction sees them; nothing
  outside it does until the caller commits.
- **Must not** No second connection, no second transaction, no autocommit write
  while a transaction of this source is bound ([[D-118]]).
- **Control** The same operation with no transaction writes immediately, so a
  store that always opened its own would pass neither.

#### UC-079 The caller's transaction rolls back  [edge]
- **Given** The append of §UC-078 followed by a rollback.
- **Then** No event row, no `streams` row for a stream created there, and no
  advanced version for one that existed. The positions the append drew are burnt;
  the next append's position is higher.
- **Must not** No fragment of any kind — an advanced `streams.version` with no
  events, or events with no stream row — may survive.
- **Control** The committed variant of the same operation leaves both halves.

#### UC-080 An append loses a race inside a `REPEATABLE READ` transaction  [edge]
- **Given** Two callers, each inside its own `REPEATABLE READ` transaction, both
  appending at the same version.
- **Then** One commits; the other's append answers `ErrBackend` matching
  `crud.ErrUnavailable`, and its transaction must be rolled back and retried
  whole.
- **Must not** `40001` must not be reported as `ErrConflict`, and the caller must
  not be told to reload the aggregate when what is dead is its transaction.
- **Control** The identical race at `READ COMMITTED` answers `ErrConflict`, which
  is what makes the distinction visible rather than asserted.

#### UC-081 The connection dies around an autocommit append  [edge]
- **Given** An append on the pool whose backend is terminated while the statement
  is in flight.
- **Then** `ErrUncertain`. The store classifies `Unconfirmed`; the kernel wraps
  nothing retryable around it; the cause is reachable only through
  `event.CauseOf` and its text is not in the rendered message.
- **Must not** No retry, no re-read to "check", no cancellation sentinel, no
  conflict. **Exactly one statement reaches the driver**, counted by a
  `database/sql` driver that wraps the real one and counts `ExecContext` — which
  is what fails if the append is ever issued on `*sql.DB` instead of a
  `*sql.Conn` the store checked out (§2.5).
- **Control** §UC-034's step 1 executed for real: one retry with the same token
  either succeeds (it had not landed) or conflicts (it had), and both are
  asserted against what the database actually holds. And the counting driver runs
  a second case where it returns `driver.ErrBadConn` **before** the statement is
  written: still one `ExecContext` call, and the outcome is `NotWritten` rather
  than `Unconfirmed`, so the two are told apart by evidence and not by the same
  answer twice.

#### UC-082 An append fails inside the caller's transaction  [edge]
- **Given** A bound transaction and a statement that errors — a constraint
  violation provoked directly, or a cancelled statement.
- **Then** `NotWritten` → `ErrBackend`; the caller's transaction is aborted and
  its obligation is to roll back.
- **Must not** `Unconfirmed` must never be returned from the joined path. There
  is no window in which a write inside somebody else's uncommitted transaction
  might have become durable.
- **Control** The same statement on the autocommit path with the connection
  killed answers `ErrUncertain`, so the two windows are told apart by evidence.

#### UC-083 An append is cancelled  [edge]
- **Given** (a) a context already cancelled on entry; (b) a context cancelled
  while the statement is in flight on the pool; (c) a server-side cancellation of
  the same statement — `statement_timeout`, or `pg_cancel_backend` from a second
  connection.
- **Then** (a) the bare cancellation sentinel and zero events. (c) `57014`
  arrives, the backend is alive, so `NotWritten` → `ErrBackend`. (b) is the case
  the driver may spell either way, and the assertion is the **rule**, not one
  answer: the test reads the cause through `event.CauseOf`, and asserts
  `ErrUncertain` when no SQLSTATE came back and `ErrBackend` when `57014` did. A
  cause that is neither fails the case.
- **Must not** The store must not report a cancellation for a statement it
  issued and could not confirm, nor uncertainty for one it never issued
  (§UC-057). It must not answer the same outcome for (b) and (c) regardless of
  what came back — pgx may surface a client-side cancel as `context.Canceled` or
  as a server `57014` depending on whether the cancel request was acknowledged in
  time, and those two observations have different truths: one proves rollback and
  the other proves nothing. A case that accepts either answer for either
  observation passes for a store that classifies at random.
- **Control** The uncancelled append in the same test succeeds; and (c) is the
  control for (b), because a store that answered `Unconfirmed` for every
  cancellation fails (c) while a store that answered `NotWritten` for every
  cancellation fails (b).

#### UC-084 A batch is appended  [happy]
- **Given** One append carrying several changes, including two identical ones.
- **Then** Consecutive dense versions, strictly ascending positions in the same
  order, one `recorded_at` for the whole batch from `statement_timestamp()`, one
  atomic unit.
- **Must not** No deduplication, no reordering, no per-row clock that makes two
  events of one append carry different instants. No parameter of the statement
  may be a Go slice bound as a PostgreSQL array (§1, §2.3).
- **Control** A batch at a stale version writes none of its rows; and a batch of
  exactly `event.MaxBatchCount` records is appended, so the widest statement the
  store can build is exercised rather than assumed — `3 × 1024 + 4 = 3076`
  parameters against PostgreSQL's 65535.

#### UC-085 Two subsystems prove they wrote in one transaction  [happy]
- **Given** An `eventpg` append and a `jobspg` placement in one `crud` transaction.
- **Then** `Repo.Authority(ctx)` and the receipt's `Authority()` compare `Same`
  across the operations; both landed or neither did.
- **Must not** The authority must not be derived from the data source, which two
  different transactions on one pool would share.
- **Control** Two operations in two different transactions compare not-`Same`.

#### UC-086 The context carries an executor that is not a transaction  [edge]
- **Given** A bound `crud` executor for this store's source that is not a
  transaction, or one no `*sql.Tx` can be taken from.
- **Then** `ErrAmbientNotTransaction` from every door, before any statement.
- **Must not** No fallback to autocommit, at any door, including the two reads.
- **Control** A bound transaction on the same source serves normally.

### Group R — Reads

#### UC-087 A stream is replayed  [happy]
- **Given** A stream longer than `StreamPage`.
- **Then** Pages of exactly `StreamPage` until a short page ends it; versions
  dense from `after+1`; positions ascending with versions; the folded state is the
  same through a second store value over the same backing.
- **Must not** A short page that is not the end truncates a history silently.
- **Control** The same replay at `StreamPage = 1` yields the same state.

#### UC-088 A consumer walks the log and resumes  [happy]
- **Given** A first-time consumer with no persisted checkpoint calling
  `ReadAll(ctx, "")`, and a cursor persisted by one process and resumed by
  another over the same database and schema.
- **Then** The empty cursor is the origin: the walk returns the log from its
  first settled position, not an `ErrCursor`. The resumed walk continues, skips
  nothing, repeats nothing within the pass, and each page's positions ascend. A
  walk from `""` and a walk resumed from a `Tail` cursor **tile the same log**:
  concatenated, the two cover every position exactly once.
- **Must not** `""` must not be refused as unparsable, and must not be answered
  by anything other than the origin — a store that treated it as "start from now"
  would silently skip a fresh consumer's whole backlog. The cursor must carry no
  per-instance nonce; a restart is a different store value over one log
  (§UC-053).
- **Control** A cursor from a *different schema in the same database* is refused
  as `ErrCursor`, which a `*sql.DB`-only backing identity would have accepted;
  and the tiling assertion is the control on the empty cursor, because a store
  that answered the origin for every cursor would fail it.

#### UC-089 A walk meets a position that is still in flight  [edge]
- **Given** Writer A holds an uncommitted append at position `p`; writer B has
  committed position `p+1`.
- **Then** The walk delivers nothing at or beyond `p` — not `p+1` — and returns a
  cursor that has not passed `p`. After A commits, the next walk delivers `p`
  then `p+1`, in that order.
- **Must not** A store must never return a cursor above its own watermark. This
  is the case a naive `position > cursor` store passes every conformance section
  while losing events in production.
- **Control** With no writer in flight the same walk delivers immediately, so a
  store that stalls forever fails.

#### UC-090 A walk meets a position burnt by a rollback  [edge]
- **Given** A rolled-back append between two committed ones, and no writer in
  flight.
- **Then** The walk stops at the gap, mints a bound, finds it already settled on
  the second snapshot of the same call, and delivers the events on both sides of
  it — one `ReadAll`, two round trips, no empty page.
- **Must not** The walk must not stop forever at a gap that can never be filled,
  and must not skip a gap it has not proven burnt. It must not mint a bound when
  a caller's transaction is bound.
- **Control** The same log walked while a writer holds an id stops at the gap and
  stays stopped, which is the pair that shows the settlement rule is doing the
  work rather than always saying yes.

#### UC-091 A cursor is foreign, unparsable or retired  [edge]
- **Given** A cursor minted over another log, one that is not this format, and one
  whose format tag this build no longer accepts. **The empty cursor is none of
  these** and is excluded from this case by name: it is the origin (§2.6,
  §UC-088).
- **Then** `ErrCursor` for all three; the walk does **not** restart from the
  beginning.
- **Must not** A store that reads from the start of its log for a checkpoint it
  could not parse re-applies every event it has ever written. And "not this
  format" must not be read as covering `""`: a store that refuses the empty
  cursor cannot be started by any consumer, and the failure arrives as
  `ErrCursor` at the first read of a fresh deployment.
- **Control** A cursor from this log resumes, and `""` resumes from the origin —
  the pair is what shows the refusal is discriminating rather than universal.

#### UC-092 A global walk runs inside the caller's transaction  [edge]
- **Given** A `ReadAll` on a context carrying a transaction of this backing.
- **Then** It runs on that transaction and sees its own uncommitted appends; its
  horizon does not advance, so an unsettled gap stays unsettled for the life of
  the transaction; the cursor it returns must be discarded with the transaction.
- **Must not** The store must not silently read on the pool while a transaction
  of its own backing is bound.
- **Control** The same walk outside a transaction settles and advances.

#### UC-093 A row that came out of the database is not what the schema promised  [edge]
- **Given** A row with a type name over 128 bytes, a revision of 0, a payload
  over `MaxPayload`, or a key over `MaxKey` — reachable when a check constraint
  was dropped, a dump was restored, or another writer shares the table.
- **Then** The read refuses; the envelope is never built; the offending values do
  not appear in the message.
- **Must not** A store must not hand the kernel a row it has not checked. The
  kernel re-checks what reaches a fold, and nothing re-checks what reaches a
  global consumer.
- **Control** The valid rows beside it read normally.

### Group S — Lifetime, health, evidence

#### UC-094 The composition root publishes readiness  [happy]
- **Given** A `health.Contribution` built by the application around `Store.Check`.
- **Then** A ready store passes; a store whose schema drifted, whose pool is
  down, or which was never prepared fails, and the report carries the code the
  **application** chose.
- **Must not** `eventpg` must not name an importance, a code or a check name, and
  must not import `health`.
- **Control** The same probe against a torn-down database fails, so a `Check` that
  returns nil unconditionally is visible.

#### UC-095 The store is closed  [edge]
- **Given** `Close()`, then every method, then `Close()` again.
- **Then** `Close` is idempotent and returns nil; it closes no `*sql.DB` and
  resolves no transaction; the three operations answer `Closed` → `ErrClosed`;
  `Transaction` still answers what the context carries.
- **Must not** Closing a store must not close a pool the composition root owns or
  a transaction a caller owns.
- **Control** A second store value over the same backing keeps serving.

#### UC-096 Someone tries to modify history  [edge]
- **Given** `UPDATE`, `DELETE` and `TRUNCATE` issued directly against the events
  table by a session with ordinary privileges.
- **Then** PostgreSQL refuses all three.
- **Must not** Append-only must not rest on this package containing no such
  statement.
- **Control** `INSERT` through the append path still works, so a trigger that
  refused everything would fail.

#### UC-097 A store implementer runs the conformance suite  [happy]
- **Given** `eventtest.Run(t, factory)` against a live database, with `New`,
  `Begin`, `Sibling`, `Fail`, `Tail` and `Unparsable` supplied.
- **Then** Every section reports a verdict; `transactions`, `durability` and
  `shared backing` are certified; `monotone visibility` is reported *not
  certified* with the store's own reason; the run certifies more than nothing.
- **Must not** No line of `event/eventtest` may change, and no capability may be
  claimed without its hook.
- **Control** §6.11's mutation harness: a deliberately broken store must fail the
  suite, and the section that catches each break is named.

#### UC-098 Another writer inserts into the events table  [edge]
- **Given** A session that is not `eventpg` — psql, a migration script, a
  different service — running `INSERT INTO <schema>.events (...) VALUES (...)`
  inside an open transaction it has not committed, having written nothing else in
  that transaction. A consumer walks the log concurrently.
- **Then** That writer's transaction has a transaction id **before** its position
  is drawn, because the `events_position_needs_xid` statement trigger assigned it
  (§2.1). The walk therefore stops at the gap and does not pass it; after the
  writer commits, the next walk delivers the row. Nothing is skipped.
- **Must not** The watermark must not rest on the shape of `eventpg`'s own append
  statement. A premise that holds only for one writer is not a premise, and the
  event it loses is lost silently — no error, no short page, nothing a
  conformance section or an audit query can see.
- **Control** Two, and both are needed. **(a)** The same insert in a scratch
  schema with the trigger dropped: a test-only `BEFORE INSERT ... FOR EACH ROW`
  trigger reports `pg_current_xact_id_if_assigned()`, which is `NULL` — the
  position was drawn with no xid — and is not `NULL` with the store's trigger in
  place. That is the premise measured directly, with no timing window, and it is
  the control that says the trigger is doing the work rather than the CTE.
  **(b)** The one writer shape that is still outside the guarantee, asserted as
  outside it rather than left unsaid: `SELECT nextval(...)` in one statement and
  `INSERT ... OVERRIDING SYSTEM VALUE` in a later one **is** skipped, which is why
  §2.6 bounds the claim to writers that let the identity column draw the position.

---

## 4. Invariants

Each states the property and **how it is falsified** — the test that fails when
the property stops holding, which is the only part of an invariant that does any
work.

#### INV-046 Admission is a conditional row update guarded by a unique index
- **Statement** No path in `eventpg` reads a stream's current version into Go and
  then decides whether to write. Admission is `WHERE s.version = $expected`
  evaluated by PostgreSQL against a locked row, and `UNIQUE (family, key,
  version)` is the constraint that holds even if that predicate were wrong.
- **Falsified by** §UC-077 with eight racing writers and exactly one winner; a
  source check that `eventpg` issues no `SELECT` of `streams.version`; and §6.9,
  which drops the unique constraint in a scratch schema and asserts verification
  refuses to start rather than that the race still works.

#### INV-047 The version advance and the event rows are one statement
- **Statement** One `Exec`, one round trip, one atomic unit, on the pool and
  inside a transaction alike. There is no second statement whose failure could
  leave the two halves disagreeing.
- **Falsified by** §UC-079's rollback audit; and a post-suite audit query over
  the whole schema asserting `streams.version = max(events.version)` for every
  stream and that no event row lacks a stream row — which a two-statement
  implementation fails under the concurrency section's load.

#### INV-048 History is append-only in the database
- **Statement** `UPDATE`, `DELETE` and `TRUNCATE` on the events table are refused
  by a trigger that is part of the schema and part of what verification compares.
- **Falsified by** §UC-096; and §UC-072's trigger-dropped mutation, which must
  refuse at `Prepare`.

#### INV-049 The store selects no isolation level
- **Statement** `eventpg` issues no `SET TRANSACTION`, opens no transaction, and
  is correct at `READ COMMITTED`, `REPEATABLE READ` and `SERIALIZABLE`. What
  differs between them is what the loser of a race is told, and that is
  documented rather than smoothed over.
- **Falsified by** §UC-080's matrix run at all three levels; and a source check
  for `SET TRANSACTION`, `BeginTx`, `Commit` and `Rollback` over the non-test
  files the eight methods reach. The migration file is excluded by name and by a
  named reason (§INV-051), never by widening the pattern until it matches
  nothing; and the check refuses if that file does not exist, so a rename cannot
  turn the exclusion into a blanket exemption.

#### INV-050 A conflict is a zero-row answer; a serialisation failure is not a conflict
- **Statement** `Conflict` is selected on `RowsAffected() == 0` and on nothing
  else. `40001`, `40P01` and `55P03` are `NotWritten` with a retryable cause.
- **Falsified by** §UC-077 and §UC-080 together — one produces `ErrConflict` and
  the other must not; and a classification table test that maps every SQLSTATE
  this store names to an outcome, run against real driver errors rather than
  hand-built ones.

#### INV-051 The store opens, commits and rolls back nothing
- **Statement** None of the eight methods, and no helper they reach, begins a
  transaction, commits one, rolls one back, or takes a savepoint. `Migrate` is
  outside the eight and is the one exception, stated rather than smoothed over:
  it pins a `*sql.Conn`, takes a session-level advisory lock on it, and runs the
  migration inside a transaction **on that same connection**, so the schema is
  created whole or not at all (§UC-069) and the lock actually serialises
  (§UC-073). That transaction is never a caller's and it is over before the store
  serves anything.
- **Falsified by** the source check of §INV-049, whose scope is the eight methods
  and their helpers and which must **exclude** the migration path by file rather
  than by hope — a check that also covered `Migrate` would either fail or be
  quietly loosened until it covered nothing; and §UC-079, where a caller's
  rollback removes everything, which is only true if the store never committed
  behind it.

#### INV-052 Inside a bound transaction, a failed append did not land
- **Statement** On the joined path the store never selects `Unconfirmed`. A
  statement that errored, was cancelled, or lost its connection leaves a
  transaction that cannot commit.
- **Falsified by** §UC-082's pair: the same failure produces `ErrBackend` inside
  a transaction and `ErrUncertain` on the pool. A store that returned one answer
  for both fails one of the two.

#### INV-053 Uncertainty is never resolved by guessing
- **Statement** The store never re-reads a stream to decide whether an append
  landed, never retries a statement whose fate is unknown, and never converts
  `Unconfirmed` into any other outcome. **The no-retry half is a choice of API,
  not a discipline**: every statement on the autocommit path is issued on a
  `*sql.Conn` the store checked out, because `(*sql.DB).ExecContext` retries
  `driver.ErrBadConn` on up to three connections and would execute one append
  three times (§2.5). The store contains no call to a statement method on
  `*sql.DB`.
- **Falsified by** §UC-081, asserting exactly one `ExecContext` reached the
  driver — counted by a `database/sql` driver that wraps the real one — and that
  the answer is `ErrUncertain`; the same counter run against a
  `driver.ErrBadConn` returned **before** the statement is sent, which must also
  be one call and must answer `NotWritten`, so the two are told apart rather than
  both passing on one number; a source check that no non-test file calls
  `ExecContext`, `QueryContext` or `QueryRowContext` on a `*sql.DB`; and §UC-034's
  step 1 executed live, asserting the retry's two outcomes against what the
  database holds.

#### INV-054 A cursor is a settled watermark, never the newest position
- **Statement** `ReadAll` returns no envelope at a position above which — or
  below which — an uncommitted writer could still commit, and the cursor it
  returns never passes such a position. It rests on **two** premises, both of
  which §2.2 level 3 compares as schema properties, and neither of which is a
  property of §2.3's statement: the identity sequence hands values out in
  increasing order (`INCREMENT 1`, `CACHE 1`, no `CYCLE`), and every transaction
  that inserts into `events` has an xid before a position is drawn (§INV-065).
- **Falsified by** §UC-089, which is the whole point of the invariant: a store
  that returns `position > F` with no settlement rule delivers `p+1`, and the
  test asserts it did not. §UC-090 is the control that the rule is not simply
  "never advance". And a third case that is not optional, because the rule it
  rules out is the one a reviewer proposes: a store whose settlement is
  `pg_snapshot_xmin = pg_snapshot_xmax` must **fail** §UC-089, which is what
  makes that case evidence rather than decoration (§2.6's measured falsification).

#### INV-055 Delivery order is position order is assignment order
- **Statement** Positions ascend within a page and across the pages of one walk;
  per stream, position order matches version order; nothing is renumbered.
- **Falsified by** the `global order`, `global paging` and `conservation`
  conformance sections; and an audit that for every stream the position order of
  its events equals their version order.

#### INV-056 A cursor names the log, not the store value or the pool
- **Statement** A cursor carries the `log` from `schema_meta`. Two store values
  over one database and schema accept each other's cursors; a store over another
  schema in the same database refuses them; a `*sql.DB` identity would not. The
  empty cursor is the one value that carries no log id: it is the origin of this
  store's log, it is never `ErrCursor`, and it is what every first walk and three
  `eventtest` call sites pass.
- **Falsified by** §UC-088's three halves — the sibling resumes, the other schema
  is `ErrCursor`, and `""` returns the log from its first settled position — and
  §UC-091, whose refusals must not catch `""`.

#### INV-057 Schema identity is compared at three levels, before any append
- **Statement** Version equality, fingerprint equality and catalog inspection all
  run at `Prepare`; any one of them failing leaves the store not-ready and every
  operation answering `Refused`. Level 3 compares **set equality**, not presence,
  for triggers, rules, row-level-security enablement and policies, column
  defaults and generated-ness, relation kind and inheritance — the classes whose
  extra member changes what a read returns or what a write records without
  raising anything. Every other class is exempt because its extra member fails
  loudly at write time, and §2.2 names which and why.
- **Falsified by** §UC-071, §UC-072 and §UC-074, each mutating exactly one level
  and asserting refusal; plus a control on the intact schema. §UC-072 mutates in
  both directions — removed and added — so a verifier that only enumerates
  required objects fails the added half. A verifier reduced to `return nil` fails
  all of them.

#### INV-058 The published bounds are the deployed schema's bounds
- **Statement** `Limits().MaxPayload` and `Limits().MaxKey` are the operands of
  the deployed check constraints, and are inputs to the fingerprint.
- **Falsified by** §UC-074; and a live test that a payload of exactly
  `MaxPayload` is accepted and one byte more is refused by the kernel before the
  database ever sees it.

#### INV-059 The backing is the database and the schema together
- **Statement** `Backing()` is minted over a comparable value carrying both. Two
  values over one database and schema compare `Equal`; two schemas do not.
- **Falsified by** a token minted through a store over schema A being refused by
  a repository over schema B with `ErrWrongStore`, plus the sibling control.

#### INV-060 Nothing starts, nothing is discovered, nothing is logged
- **Statement** `New` performs no I/O, starts no goroutine and reads no
  environment. No package-level mutable state exists. Nothing writes to a logger.
- **Falsified by** `scripts/`'s `startsNothing` walk extended to the new package;
  a source check for `log.`, `fmt.Print` and `os.Getenv` outside tests; and
  `New` against a `*sql.DB` whose DSN points nowhere returning a store rather
  than an error.

#### INV-061 Zero diffs under `event/` outside `event/eventpg`
- **Statement** Phase 2 changes no file under `event/` except inside
  `event/eventpg`, and the claim is executable rather than promised.
- **Falsified by** a `make check` arm running
  `git diff --stat <baseline> -- event/ ':(exclude)event/eventpg'` **and**
  `git status --porcelain` over the same pathspec, refusing when the baseline
  revision does not resolve; plus a self-test in `scripts/checks_test.go` that
  the arm reports a difference when one exists (§7.3).

#### INV-062 No sentinel of `eventpg`'s crosses the store seam
- **Statement** Everything `eventpg` returns from the eight methods is either
  nil, a bare context error, or an `event.Failure` carrying one of the seven
  outcomes. `ErrSchemaMismatch` and `ErrNotReady` are constructor and lifecycle
  errors and travel as an `event.Failure`'s *cause*, reachable through
  `event.CauseOf` and by neither `errors.Is` nor `errors.As` through a kernel
  sentinel.
- **Falsified by** a test that walks every error the eight methods can produce
  and asserts each is one of those three shapes; and the `store failure
  classification` conformance section run through the wrapping decorator.

#### INV-063 The store names no importance and imports no health package
- **Statement** `Check` answers an error and nothing else. The composition root
  supplies the name, the code and the importance ([[D-091]]).
- **Falsified by** the extension-cost check in `scripts/event_test.go`, whose
  single allowance for this package does not reach `health`; and §UC-094.

#### INV-064 An autocommit failure the store cannot pin down is `Unconfirmed`
- **Statement** `NotWritten` is selected only on proof — nothing was issued, or
  PostgreSQL answered with a SQLSTATE that is not a session-termination code
  (§2.5). Every other autocommit failure is `Unconfirmed`. **There is no default
  branch that answers `NotWritten`**, which is the direction phase 1's own sketch
  fell in and the one that lies about a write that landed.
- **Falsified by** a classification test driven from **real** driver errors, not
  hand-built ones, that walks the whole rule: a `23505`, a `40001`, a `57014`
  from `statement_timeout`, a `pg_terminate_backend`, a client-side context
  cancellation, a failed connection checkout against a DSN that resolves nowhere,
  and an injected error carrying no SQLSTATE. The last one is the case that fails
  for an implementation with a `NotWritten` default. And a source check that the
  classification function has no branch returning `NotWritten` that is not
  guarded by one of the two proofs — the shape a reviewer can read, since a
  `default:` clause is what the check is looking for.

#### INV-065 A transaction has its id before it draws a position
- **Statement** Every transaction that inserts into `events` is assigned a
  transaction id before the inserting statement draws its first position. It is a
  property of the **schema** — the `events_position_needs_xid` statement trigger —
  and therefore holds for every writer, not only for §2.3's statement, whose CTE
  would satisfy it by accident. Without it the settlement rule of §2.6 declares a
  live writer's gap burnt and the consumer never sees that event.
- **Falsified by** §UC-098's control (a): a test-only `BEFORE INSERT ... FOR EACH
  ROW` trigger in a scratch schema reports `pg_current_xact_id_if_assigned()` for
  a foreign `INSERT` from a transaction that has written nothing else. With the
  store's trigger it is not null; with the trigger dropped it **is** null, which
  is the hazard asserted directly, with no timing window and no flake. §UC-098's
  control (b) pins the one writer shape still outside the guarantee, so the claim
  is bounded by a test rather than by a sentence.

---

## 5. The exported Go surface

Every name below is what `make api` must show under
`## github.com/frostgrove/vv/event/eventpg`, and nothing else is exported.
Receiver name is `this` throughout.

```go
package eventpg

const (
	DefaultSchema = "frostgrove_events"
	SchemaVersion = 1

	DefaultMaxPayload = 64 << 10 // the events.payload check constraint's operand
	DefaultMaxKey     = 512      // the streams.key and events.key operand
	DefaultMaxBatch   = 64
	DefaultPage       = 256      // StreamPage and MaxRead, capped by event.ResidentPage
)

var (
	// This spec cannot be assembled into a store. Wiring class: a nil DB or
	// Source, a Source that is not the DB's data source, an illegal schema name,
	// a bound over a kernel ceiling.
	ErrSpec = errors.New("eventpg: this store cannot be assembled from this spec")

	// The deployed schema is not the one this build expects. Names the level
	// that disagreed and the two values; never a row, a credential or a DSN.
	ErrSchemaMismatch = errors.New("eventpg: the deployed schema is not the one this build expects")

	// Prepare has not returned nil. Every operation answers Refused until it has.
	ErrNotReady = errors.New("eventpg: this store has not verified its schema")
)

// The zero value is Unset and resolves to Verify. Nothing migrates by default.
type SchemaManagement uint8

const (
	UnsetSchemaManagement SchemaManagement = iota
	VerifySchema
	ManageSchema
)

func (this SchemaManagement) Valid() bool
func (this SchemaManagement) String() string

// The half of the configuration that is written into the deployed schema. Its
// zero value is the defaults. Two builds whose Schema values differ cannot share
// one deployed schema, and that is what the fingerprint is for.
type Schema struct {
	Name       string
	MaxPayload int
	MaxKey     int
}

func (this Schema) Resolved() (Schema, error)
func (this Schema) Fingerprint() (string, error)

// The statements a deployment's migration step runs, in order. Safe to run
// inside one transaction at schema version 1; MIGRATIONS.md says so and says
// what would change that.
func MigrationStatements(schema Schema) ([]string, error)

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

var _ event.Store = (*Store)(nil)

// Performs no I/O, starts nothing, reads no environment.
func New(spec Spec) (*Store, error)

// Migrates under ManageSchema, then verifies under both. Until it has returned
// nil the store refuses every operation.
func (this *Store) Prepare(ctx context.Context) error

func (this *Store) Migrate(ctx context.Context) error
func (this *Store) Verify(ctx context.Context) error

// health.Probe, structurally. Names no importance, no code and no check name.
func (this *Store) Check(ctx context.Context) error

func (this *Store) Schema() Schema
func (this *Store) SchemaManagement() SchemaManagement

func (this *Store) Capabilities() event.Capabilities
func (this *Store) Limits() event.Limits
func (this *Store) Backing() event.Backing
func (this *Store) Transaction(ctx context.Context) (event.Authority, error)
func (this *Store) ReadStream(ctx context.Context, s event.Stream, after event.Version) ([]event.Envelope, error)
func (this *Store) ReadAll(ctx context.Context, after event.Cursor) ([]event.Envelope, event.Cursor, error)
func (this *Store) Append(ctx context.Context, req event.AppendRequest) error
func (this *Store) Close() error
```

**What is deliberately absent, and why:**

| Not exported | Why |
|---|---|
| `Open(ctx, db, …)` | `jobspg.Open` exists for a zero-configuration development path and asks for `ManageSchema` by name. An event store's schema is history; the two-step `New` + `Prepare` is the only spelling, so nobody gets a migrating store by reaching for the short one. |
| `Tail(ctx)` | A cursor at the end of the log is sound only if every position below it is settled, and no O(1) reading proves that (§2.6). It costs a walk or a wait, and both belong to E2's projector. The conformance factory's `Tail` hook lives in the test package. |
| `Spec.Clock` | `recorded_at` is `statement_timestamp()`. One clock per database beats one per process, and a `Clock` field the store then ignored would be a lie. `RecordedAt` is not an ordering key (§INV-009). |
| `Spec.TxOptions` | None of the eight methods begins a transaction (§INV-051). `Migrate` does, on a pinned connection, at the session default level — a migration is DDL and there is no level at which that reads differently, so an option would be one nobody could use. |
| A cursor type, a position type, an outcome type | The kernel owns all three. A store that exported its own would be the second spelling §INV-045 exists to prevent. |
| A retry, backoff or circuit-breaker option | [[D-040]] and §UC-034. |

**Capabilities and limits this store publishes:**

```
Transactions:       Supported      (Source is required, so this is unconditional)
Persistence:        Supported
MonotoneVisibility: Unsupported    (§2.6 — a freshness claim this store will not make)
SharedBacking:      Supported

MaxPayload: Schema.MaxPayload                      MaxBatch:   Spec.MaxBatch  or 64
MaxKey:     Schema.MaxKey                          StreamPage: min(256, event.ResidentPage(MaxPayload))
                                                   MaxRead:    same
```

`New` refuses any configured bound above the kernel's ceiling
(`event.MaxPayloadBytes`, `MaxKeyBytes`, `MaxBatchCount`, `MaxPageCount`) and any
page above `event.ResidentPage(MaxPayload)`, naming the number the caller set and
the one it may not pass — one door earlier than the kernel would, exactly as
`eventmemory.New` does.

---

## 6. What the live suite must prove

The gate names its own command, because `make integration` runs `./test/...` only
and does not reach a satellite's tagged suite:

```
FROSTGROVE_EVENTPG_TEST_DSN='postgres://vv:vv@localhost:55432/vv?sslmode=disable' \
  go test -race -tags=integration ./event/eventpg/...
```

**An unset DSN fails the gate.** Not a skip. The suite's `TestMain` refuses with
a message naming the variable and the command above. `jobspg` skips, and the
roadmap is explicit that skipping is wrong here: a skipped run that prints `ok`
is what phase 1's report called evidence and was not.

A reviewer checks these off, and each has a control:

1. **The whole `eventtest.Run` conformance suite, unchanged**, twice: once at the
   defaults and once at deliberately narrow limits (`StreamPage: 4, MaxBatch: 2,
   MaxRead: 3`, `MaxKey: 40`), mirroring `eventmemory`'s two runs. Every section
   reports a verdict; `monotone visibility` is *not certified*; the run certifies
   more than nothing.
2. **Concurrent append** — §UC-077, on the pool: one winner, seven `ErrConflict`,
   one event in the stream, a single writer afterwards as the control.
3. **Rollback leaves no fragment** — §UC-079: no event row, no stream row, no
   advanced version, the positions burnt, the next append higher, and the
   committed variant as the control.
4. **Cancellation** — §UC-083, both windows, with the uncancelled append as the
   control.
5. **Commit uncertainty and the classification rule** — §UC-081, §UC-083,
   §INV-064: `pg_terminate_backend` from a second connection during an autocommit
   append gives `ErrUncertain`, with the cause reachable only through
   `event.CauseOf`; then §UC-034 step 1 for real, asserting the retry's outcome
   against what the database holds. Beside it, the rule's whole table driven from
   real driver errors — `23505`, `40001`, `57014` from `statement_timeout`, a
   client-side cancel, a checkout against a DSN that resolves nowhere, and an
   error with no SQLSTATE — each asserting the outcome §2.5 requires. The
   counting driver runs here too: exactly one `ExecContext` per append, and a
   `driver.ErrBadConn` injected before the send answering `NotWritten` on the same
   single call (§INV-053). The control is the no-SQLSTATE case, which is the one
   an implementation with a `NotWritten` default fails.
6. **A v1-to-v2 upcast over stored rows**: write revision-1 payloads through a
   declaration that has one reader, then load the same streams through a
   declaration with two readers and an upcaster, and assert the folded state and
   that the stored bytes were not rewritten. The control is a v1 load of the same
   rows folding to the v1 state.
7. **Schema verification refuses a wrong schema** — §UC-071, §UC-072, §UC-074, in
   a scratch schema per case, **in both directions**. *Removed or altered:*
   missing schema; version one below; version one above; fingerprint mismatch;
   unique constraint dropped; append-only trigger dropped; xid-first trigger
   dropped; a check constraint altered; the foreign key dropped. *Added:* row-level
   security enabled on `events` with a policy; a third `BEFORE INSERT` trigger on
   `events`; a `BEFORE INSERT` trigger on `streams`; a rule on `events`; a
   `DEFAULT` on `events.payload`; a child table inheriting `events`. Each refuses
   at `Prepare`, names the object, and issues no append. The intact schema is the
   control, and so is one *exempt* mutation — an extra non-unique index on
   `events` — which must **pass**, because a verifier that refuses everything
   proves nothing about the ones that matter.
8. **The ordered-read watermark and its two premises** — §UC-089, §UC-090,
   §UC-098: the in-flight case (nothing delivered past the gap, then both in
   order after the commit), the burnt case (passed within one `ReadAll`), and
   three negative controls. A decorator settling on
   `pg_snapshot_xmin = pg_snapshot_xmax` must fail the in-flight case, because
   that rule was measured false on PostgreSQL 17.9 (§2.6). A foreign
   `INSERT INTO events` held open in another session must not be skipped
   (§UC-098), and the same insert in a scratch schema with
   `events_position_needs_xid` dropped must report a null
   `pg_current_xact_id_if_assigned()` from a test-only row trigger — the premise
   measured directly rather than raced for. And the `nextval`-then-`OVERRIDING
   SYSTEM VALUE` writer must be shown to be skipped, so the boundary of the claim
   is a test result and not a caveat. This is the group a naive store fails
   silently.
9. **The database's own guarantees**: `UPDATE`/`DELETE`/`TRUNCATE` refused
   (§UC-096); a direct insert that would duplicate `(family, key, version)`
   refused by the unique index and classified `NotWritten`; the post-suite audit
   that `streams.version = max(events.version)` for every stream and that no
   event row lacks a stream row.
10. **The isolation matrix** — §UC-080: the same losing append inside `READ
    COMMITTED`, `REPEATABLE READ` and `SERIALIZABLE` caller transactions,
    asserting `ErrConflict` for the first and `ErrBackend`/`crud.ErrUnavailable`
    for the other two.
11. **The suite catches a defective store.** `eventtest` currently detects 27 of
    165 of its own section-level assertions (backlog P1 item 1), and phase 2's
    evidence is "eventpg passes eventtest". So the live gate carries a mutation
    harness: a table of decorators over the **real** store — a short page that is
    not the end; a page whose positions descend; admission at the wrong version;
    a cursor at the newest position instead of the watermark; a payload buffer
    reused across two calls; an envelope whose stream is not the one that was
    read — each run through `eventtest.Run` in a **subprocess** (the
    `TestHelperProcess` idiom, selected by an environment variable), asserting a
    non-zero exit and that the named section is the one that reported it. A
    mutation the suite does not catch is a `[high]` finding against the suite,
    recorded in the backlog under `## P2` with the section it should have been
    caught by.
12. **The savepoint half of the authority rule** — the debt Roadmap §15 names: a
    receipt taken inside a savepoint and one taken outside compare `Same`; a
    savepoint rollback discards the events while the parent authority stays live;
    and a receipt from a different transaction on the same pool compares
    not-`Same` as the control.
13. **Two store values over one backing, and the walk that starts from nothing**:
    `Sibling` resumes another's cursor, reads another's writes, and refuses a
    cursor minted over a second schema in the same database (§UC-088, §INV-056,
    §INV-059). Beside it, `ReadAll(ctx, "")` on a populated log returns the log
    from its first settled position rather than `ErrCursor`, and a walk from `""`
    tiles with a walk resumed from a `Tail` cursor — every position covered
    exactly once across the two. The control is §UC-091's refusals, which must
    still refuse.
14. **The gate itself fails when the DSN is unset**, proven by running the suite
    without it in the mutation harness's subprocess and asserting the failure and
    its message.
15. **The migration lock serialises** — §UC-073: two goroutines calling `Prepare`
    with `ManageSchema` against one empty scratch schema; `pg_locks` observed
    while the migration runs, showing one granted `advisory` row on the migrating
    backend; the lock, the DDL and the unlock all on **one** backend pid; one
    `log` afterwards; no `23505` on `pg_type_typname_nsp_index` in either caller,
    over enough repetitions that an unpinned lock would have raced. The controls
    are a single-process migration that also releases the lock, and a second
    attempt made while a `*sql.Conn` the test holds owns the lock, which must
    block — a run in which nobody ever waited proves nothing about waiting.

**A section is reported by its verdict, never by `go test` being green.** If a
`[critical]` or `[high]` survives its fix round, the report says so plainly.

---

## 7. Deliverables that are not code

### 7.1 The module in the workspace
`event/eventpg/go.mod` requiring `github.com/frostgrove/vv` and `pgx/v5`; the
`go.work` `use` line; the `replace` lines in `test/go.mod` and
`_examples/go.mod`. `make check-replaces` and `make check-tidy` catch these two;
nothing catches the rest.

### 7.2 Documentation, in the same change as the code
`docs/modules/en/eventpg.md` and `docs/modules/ru/eventpg.md` with their rows in
both `Index.md` files. Two rows in those pages are contracts rather than prose
and are named here so they cannot be summarised away: a **driver row** saying
exactly what a consumer's driver must support — any `database/sql` driver for
PostgreSQL, because the store binds only `string`, `int32`, `int64` and `[]byte`
and never an array (§1, §2.3) — and a **foreign-writer row** saying that the
global walk covers every writer that lets the identity column draw the position,
and does not cover one that draws a position with `nextval` and inserts it later
with `OVERRIDING SYSTEM VALUE` (§2.6, §UC-098). Then a new flow — the next free number, `FL-037`, "a recorded
fact becomes a PostgreSQL row" — with its row in `docs/ai/flows/Index.md`, its
question row in the reverse lookup, and a source-file row for every non-test file
in the package; the `UC-032` row in `docs/ai/usecases/Index.md` extended with
`eventpg`; `docs/roadmaps/Roadmap.md` §15 rewritten rather than annotated done;
`event/eventpg/MIGRATIONS.md`; and `make api` regenerated.

Two decision docs are owed, at the next free numbers after `D-125`:

- **The store chooses no isolation level, and admission is a database
  constraint** — because the next maintainer will be tempted by `SERIALIZABLE`
  and by `SELECT … FOR UPDATE`, and because the `40001`-is-not-a-conflict mapping
  is the kind of thing that gets "simplified" into a bug. Links [[D-118]],
  §INV-046, §INV-049, §INV-050.
- **An event schema is migrated on the same deployment-profile choice as the jobs
  schema** — [[D-101]] is written about `jobspg` by name; a second subsystem
  answering the same question needs it said once, in one place, with `D-101`'s
  *See also* updated in the same change.

### 7.3 The zero-diff check
A new `make check` arm, `check-event-kernel`, in `scripts/checks.sh` and in the
Makefile's `COMMANDS` and `case` blocks, added to `all`:

- the baseline revision is a single named constant in `checks.sh`, set to the
  commit that lands the phase-1 kernel;
- it runs `git diff --stat <baseline> -- event/ ':(exclude)event/eventpg'` **and**
  `git status --porcelain -- event/ ':(exclude)event/eventpg'`, so an untracked
  new file under `event/` is caught too;
- it **refuses** when the baseline does not resolve or git is unavailable, rather
  than reporting ok — a check that passes when it cannot run is backlog P1 item
  6, in a file that already has three of them;
- `scripts/checks_test.go` gains a case that the arm reports a difference when a
  file under `event/` differs from the baseline, and reports ok when only
  `event/eventpg` does.

> **Recorded tension, resolved.** `docs/roadmaps/Roadmap.md` §15 says this arm
> should exist "as a report, never as a `make check` arm". That objection is to a
> **tag**-dependent check: before the first tag there is no tag, so the arm would
> pass vacuously — the worst kind. Pinning a recorded *revision*, refusing when it
> does not resolve, and self-testing the arm removes the objection. The roadmap
> line is updated in the same change, and this paragraph is why.

### 7.4 The extension-cost row
`scripts/event_test.go`'s `charged` map gains
`eventExtension + "/eventpg": "./crud/adapter/crudsql"`. That allowance's
first-party closure is `crud`, `crud/catalog`, `crud/sqlfault`, `errs`,
`errs/sqlerr` and `utils` — everything this package needs and nothing more, which
is why it must not import `health`, `port` or `runtime`. `startsNothing` and
`noBaseSubsystemDependsOn` cover the package without change.

---

## 8. Tensions and open questions, left to the plan

Each is named so it is decided rather than discovered.

1. **`Factory.Fail` honesty.** `eventmemory`'s factory injects failures with a
   test-local decorator, which is honest because it refuses before forwarding.
   `eventpg` can do the same, but a decorator proves nothing about *this store's*
   classification of a *real* driver error. The plan decides how much of the
   `Fail` hook is driven from real failures (`Closed` and `Refused` are trivially
   real; `NotWritten` is reachable through a constraint; `Unconfirmed` needs
   `pg_terminate_backend`) and records the rest in §6.5's dedicated tests.
2. **Fingerprint canonicalisation.** Level 2 is only as good as the rendering it
   digests. The plan fixes the canonical form and pins it with a golden test, so
   a whitespace change in a constraint definition does not silently invalidate
   every deployed schema.
3. **Cursor derivability.** A base64 of three numbers and a log id is derivable
   by a determined caller, which §UC-053 says a cursor should not be. Phase 1
   already accepted that cost for `Cursor` being a defined string. `eventpg`
   validates structure, format tag and log identity and refuses everything else;
   a caller who forges a cursor skips its own events. **Recorded, not fixed**;
   an authenticated cursor is a `[medium]` for the backlog if anyone asks.
4. **Suite behaviour against a busy cluster.** `pg_snapshot_xmin` is
   cluster-wide, so a long transaction in *any* database of the same instance
   holds every gap unsettled and stalls a walking section — correctly, but a
   stalled section reads as a failure. The gate runs the eventpg suite alone. The
   plan decides what the test `Tail` hook does when settlement does not arrive
   inside the section window: it must report why rather than time out silently,
   and it must not settle by lowering the bar.
5. **`MaxBatch` and the shape of the batch parameters.** *Settled, and the earlier
   answer was wrong.* This item used to say that three arrays keep the batch below
   PostgreSQL's 65535-parameter limit and that this is why `unnest` is used. The
   limit does not bite: `event.MaxBatchCount` is 1024, so a row-per-record form
   costs `3 × 1024 + 4 = 3076` parameters. Nothing forced `unnest`, and it carried
   a cost §1 was denying — array binding is not `database/sql`, it needs a driver
   implementing `driver.NamedValueChecker`, and the store would have worked on
   `pgx` and failed on `lib/pq` while claiming any driver composes. §2.3 is now a
   `VALUES` list of scalars. What the plan owes instead: the statement text now
   varies with the record count, so it decides how many texts the prepared-
   statement cache may hold (bounded by `MaxBatch`), and it pins the widest batch
   — `event.MaxBatchCount` records in one append — as §UC-084's control.
6. **Schema-per-tenant.** Nothing here forbids it — the backing already carries
   the schema name, so a store per tenant schema is expressible today. Whether
   the tenancy extension should compose that way is not phase 2's question and no
   affordance for it is exported.
