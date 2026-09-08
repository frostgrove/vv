# EVENTSOURCE_P2 — implementation S2 (`event/eventpg`: migrate, verify, prepare, check) — GAPS

## Round 1 — econv-implementation-reviewer (clean context) — 2026-09-08

Reviewed against the **code**, not the plan's prose and not anyone's summary. Read in full:
[`EVENTSOURCE_P2_PLAN.md`](../plans/EVENTSOURCE_P2_PLAN.md) (§What this plan delivers, §What was
measured live, D1–D7, §Coverage matrix, §Contracts before code for `migration.go` /
`verify.go` / `catalog.go`, §S1, §S2, §The module checklist, §The zero-diff proof),
[`EVENTSOURCE_P2_USECASES.md`](../usecases/EVENTSOURCE_P2_USECASES.md) (UC-069…UC-075, UC-094,
UC-096, UC-098, INV-046…INV-051, INV-057, INV-058, INV-063, INV-065),
[`EVENTSOURCE_BACKLOG.md`](EVENTSOURCE_BACKLOG.md) `## P2` §1–§36,
[`EVENTSOURCE_P2_S1_GAPS.md`](EVENTSOURCE_P2_S1_GAPS.md),
`event/eventpg/{migration,verify,catalog,schema,config,doc}.go`,
`event/eventpg/{main,migration,schema}_integration_test.go`,
`event/eventpg/{schema,config,sources}_test.go`, `jobs/jobspg/retention_migration.go`,
`scripts/checks.sh`, `CLAUDE.md`.

Every number, transcript and mutation below was produced in this worktree against the live
PostgreSQL 17.9 at `postgres://vv:vv@localhost:55432/vv`. Two source mutations and three live
schema probes were applied and **every file was restored byte-identical** (md5 verified);
`git status --porcelain event/` is `?? event/eventpg/` and nothing else.

---

### The zero-diff obligation — holds

| Command | Result here |
|---|---|
| `git status --porcelain -- event/ ':(exclude)event/eventpg'` | empty |
| `git diff --stat c798fc0b -- event/ ':!event/eventpg'` | empty |
| `git status --porcelain event/` | `?? event/eventpg/` and nothing else |

### Checkpoint verification — the pasted transcripts are real

Both S2 checkpoint blocks were run here, verbatim, in a shell where the variable is not exported.

| Clause | Result here |
|---|---|
| `go build ./event/eventpg/... && go vet -tags=integration ./event/eventpg/...` | exit 0 |
| phase-4 `-run '^TestMigrationStatementsBuildTheSchemaTheFingerprintDescribes$'` under `-race` | `ok … 1.045s`, exit 0 |
| `FROSTGROVE_EVENTPG_TEST_DSN=x go test -tags=integration -list '.' \| grep -q '^Test'` | 19 names printed — `TestMain` lists before it refuses |
| `test "$(… -list '^(…ten names…)$' \| grep -c '^Test')" = 10` | **10** — matches the plan's `= 10` |
| `go test -race -count=1 -tags=integration ./event/eventpg/` **twice in a row** | `ok … 16.330s`, `ok … 16.032s` |
| `! env -u FROSTGROVE_EVENTPG_TEST_DSN … \| grep -q '^ok'` | passes — the run is `FAIL … 0.002s` |
| `env -u … \| grep -q FROSTGROVE_EVENTPG_TEST_DSN` | passes — the message names the variable and the command to set it |
| `./scripts/checks.sh {workspace,replaces,tidy,deps,tiers,utils,triplets,todo}` | all `ok`; `./event/eventpg: 0 external packages` |
| `gofmt -l .` | silent |
| `go test -race -count=1 ./event/eventpg/` (untagged) | `ok … 11.117s` |
| `go test -race -count=1 ./event/...` | `event`, `eventmemory`, `eventtest` all `ok` |
| `go test -run '^(TestNoEventPackageCostsMoreThanTheSeamItNames\|TestMerelyImportingTheEventExtensionStartsNothing\|TestNoBaseSubsystemDependsOnTheEventExtension)$' ./scripts/` | `ok … 1.355s` |

**The live gate behaves as the roadmap demands.** With the DSN unset the suite **fails**, it does
not skip: `FAIL github.com/frostgrove/vv/event/eventpg 0.002s`, with the message naming the
variable. With the DSN set it runs and passes, twice.

### Contract conformance — both directions, no drift

`go doc -all ./event/eventpg` adds exactly four names over S1's surface — `Store.Migrate`,
`Store.Prepare`, `Store.Verify`, `Store.Check`, each `func(context.Context) error` — and nothing
else. `Check` is `health.Probe` structurally and is assigned to a
`func(context.Context) error` in the test to prove it. **No silent extra public surface; nothing
marked done that is missing.** The six "contract changes S2 made" in the plan are all present in
the code: the checks are authored as `octet_length(x) >= 1 AND octet_length(x) <= n`
(`schema.go:176-178`), p/u/f are compared structurally and only `c` by definition
(`catalog.go:356-378`), the definition comparison strips whitespace *and* parentheses
(`catalog.go:495-509`), `Check` compares the log out of the row it already reads
(`verify.go:88-90`), all of `Migrate`/`Verify`/`Check` issue on a checked-out `*sql.Conn`
(`migration.go:180`, `verify.go:48,79`), and `Migrate` maps the `EVPG1` raise onto
`ErrSchemaMismatch` naming this build's fingerprint (`migration.go:241-251`).

### Architecture metrics — counted

*Size.* Six non-test files, **1463** lines: `catalog.go` 509, `schema.go` 338, `migration.go` 251,
`config.go` 201, `verify.go` 137, `doc.go` 27. Tests **2223** lines. Threshold 400 — no breach.
*Longest functions:* `readCatalog` 76, `expected` 73 (a data table), `withMigrationLock` 44,
`rendering` 42, `New` 39. Nesting never exceeds 3.
*Import fan-out.* The whole non-test closure is `crud`, `crud/catalog`, `crud/sqlfault`, `errs`,
`errs/sqlerr`, `utils` — `go list -deps`. No `health`, no `port`, no `runtime`: **INV-063 holds**,
and it is the scripts test that says so, not this paragraph. `migration.go`, `verify.go` and
`catalog.go` import between them exactly `crud/sqlfault` and `event` of the first party.
*Global mutable state.* None — `Store.closed` is an `atomic.Bool` and `Store.state` an
`atomic.Pointer[readiness]`; readiness and the log are **one** pointer, so they cannot be read
half-set (`verify.go:23-25`). No package-level `var` of a mutable type; the S1 AST check holds.
*Cycles / layer direction.* None; `eventpg` is a leaf satellite.

### PostgreSQL correctness — read statement by statement

- **Every statement of `Migrate` is inside one transaction** on one pinned `*sql.Conn`
  (`migration.go:224-239`), the advisory lock is taken and released on that same connection, and a
  `deploy` helper in the test uses the operator's own list so the deploying code and the verifying
  code share nothing. `TestARefusedMigrationLeavesTheTablesItCreatedRolledBack` proves a refusal
  leaves no fragment.
- **An unlock that answers false is an error and the connection is discarded**
  (`migration.go:192-202`) — `jobspg/retention_migration.go:115-159` copied faithfully.
- **The `EVPG1` raise rolls the whole transaction back** and is classified by SQLSTATE, never by
  message text (`migration.go:242`, `sqlfault.Extract`). `errors.Is` against `ErrSchemaMismatch`,
  `ErrSpec`, `ErrNotReady`, `event.ErrClosed`, `sql.ErrNoRows` everywhere; **no string comparison
  of an error anywhere in the three files.**
- **Error hygiene.** No statement text, no payload bytes, no DSN and no credential is placed in a
  returned error. `migrationFailure` names `statement N of 11` and never the statement;
  `readRows` names the schema and never the query. Grepped every `fmt.Errorf` in the three files.
- **Injection.** Every identifier is `quoteIdentifier`-ed and `validSchemaName` narrows the name to
  `[a-z][a-z0-9_]*`; every catalog read binds the schema and the three table names as `$n`
  parameters (`catalog.go:232-252`), and `pg_get_serial_sequence` takes the qualified name as a
  parameter rather than as text.
- **Level 3's positive coverage is real, not decorative** — mutation M1 below.

### Mutation evidence produced by this round (not the plan's)

Two mutations, applied one at a time, the tagged suite run for each, both files restored
byte-identical afterwards (`md5sum` before/after identical), and the suite green again after the
restore (`ok … 16.140s`).

| # | Mutation | Result |
|---|---|---|
| M1 | `catalog.go:143` `inspect` returns nil — level 3 skipped entirely | **FAIL**, 22 of the 27 verification cases fail (the other 5 are levels 1 and 2) |
| M2 | `migration.go:206` the advisory lock is taken on `this.db` rather than on the pinned `conn` | **FAIL** in *both* lock tests: `TestTwoConcurrentPreparesTakeOneLockOnOneBackend` (`this session did not hold it, so the lock never serialised anything`) and `TestASecondMigrationBlocksAgainstAHeldLock` |

Both survive-checks pass: the two most important tests of this section do catch the
implementation being broken under them.

---

### GAP-P2-S2-1 [high][immediate] Level 3 compares what a trigger *is* and never what it *does*: the trigger function body, the trigger's `WHEN` clause and the table's persistence are read by nothing, so a schema on which `UPDATE` and `DELETE` succeed verifies clean

- **Where:** `event/eventpg/catalog.go:58-65` (`triggersQuery` selects `tgname`, `tgtype`,
  `tgenabled`, `proname`, the function's namespace — and neither `tgqual` nor anything of
  `pg_proc.prosrc`), `event/eventpg/catalog.go:128-133` (`deployedTrigger` has four fields),
  `event/eventpg/catalog.go:380-402` (`compareTriggers`), `event/eventpg/catalog.go:26-30` +
  `284-297` (`relationsQuery` / `compareRelation` read `relkind`, `relrowsecurity`,
  `relforcerowsecurity`, `relhassubclass` — not `relpersistence`),
  `event/eventpg/schema.go:290-293` (the fingerprint's `trigger` line carries timing, events,
  level and the function *name*), `event/eventpg/migration.go:46-49` (the two bodies that carry
  the invariants).
- **What:** the three levels of verification agree that a schema is "the one this build expects"
  while the two plpgsql functions that *are* INV-048 and INV-065 have been replaced with no-ops,
  while a `WHEN (false)` has been welded onto the append-only trigger, or while the history table
  has been made `UNLOGGED`. Driven here, not argued — three probes against live scratch schemas
  deployed by the store's own list:

  ```
  CREATE OR REPLACE FUNCTION @.events_are_append_only()   … BEGIN RETURN NEW;  END
  CREATE OR REPLACE FUNCTION @.events_assign_writer_xid() … BEGIN RETURN NULL; END
    -> Verify PASSED, Check PASSED
    -> UPDATE @.events SET payload = '\x02'  SUCCEEDED
    -> DELETE FROM @.events                  SUCCEEDED

  CREATE TRIGGER events_append_only_row BEFORE UPDATE OR DELETE ON @.events
      FOR EACH ROW WHEN (false) EXECUTE FUNCTION @.events_are_append_only()
    -> Prepare PASSED
    -> DELETE FROM @.events                  SUCCEEDED

  ALTER TABLE @.events SET UNLOGGED; ALTER TABLE @.streams SET UNLOGGED
    -> Prepare PASSED while Capabilities().Persistence is [support supported]
  ```

  The same blindness is in the **fingerprint**: `rendering()` digests the trigger's name, timing,
  event set, level and function name, so two `eventpg` builds whose `events_are_append_only` or
  `events_assign_writer_xid` bodies differ produce **the same** `sha256:` digest and verify each
  other's schemas without a word. The migration's `CREATE OR REPLACE FUNCTION` then silently
  changes behaviour at schema version 1 with the fingerprint unmoved — which is the one event the
  fingerprint exists to make loud.
- **Why this severity:** INV-048 states that `UPDATE`, `DELETE` and `TRUNCATE` are "refused by a
  trigger that is part of the schema **and part of what verification compares**", and UC-072's
  Must-not is "the store must not start on the fingerprint alone, and must not check only for
  absence". Both are false as shipped, and the demonstration above is the counterexample rather
  than a hypothetical. The two failures it admits are the two worst this subsystem has:
  (1) history is mutable — an ORM, a support script or another service sharing the schema can
  `UPDATE` or `DELETE` an event row and every replica's `Prepare` and every `Check` keeps saying
  the schema is intact, so the corruption is discovered by a fold that no longer reproduces;
  (2) `events_assign_writer_xid` neutered removes the premise the S4 read watermark rests on —
  a writer draws a position before its transaction has an id, the walk passes the gap, and the
  event is lost with **no error, no short page and nothing an audit query can see** (§UC-098's own
  Must-not, word for word). Level 3 is the only mechanism in the design that was supposed to catch
  a hand-edited or foreign-built schema; comparing the five properties that identify a trigger and
  none of the two that decide whether it acts is the `sameValue`-could-be-`return true` shape the
  delivery policy names by name.
- **Why this timing:** S4 builds the global read on top of INV-065, and S5 wires `Check` into a
  `health.Contribution` — both inherit a verification that certifies a premise it never measured,
  and the fingerprint that the module pages, `MIGRATIONS.md` and the incident-comparison export of
  `Fingerprint()` all describe as "the schema this build expects" will have been published to
  operators before the field it omits is added. Adding `prosrc` to the rendering later **moves
  every deployed fingerprint**, which after S5's documentation is a migration for consumers rather
  than a golden-file diff read by one person.
- **Close criteria:**
  - [x] the expectation model carries the two function bodies as data — `expectation.functions`,
        a `[]expectedFunction{name, body []string}` that `migrationStatements` renders into
        statements 5 and 6 and `rendering()` emits as
        `function <name> <body, whitespace-collapsed>`. The schema name is **not** repeated on the
        line: the header's `schema <name>` already binds the whole rendering to one, and no other
        object line carries it. `testdata/fingerprint.golden`, both `testdata/migration*.golden`
        and the pinned digest moved in the same change and the diff was read — the DDL is
        byte-identical, the only movement in the two migration goldens is the digest literal in
        statements 10 and 11 (`sha256:5a50f94e…` → `sha256:fadfff74…`).
  - [x] level 3 reads `pg_proc.prosrc` — through `pg_trigger.tgfoid`, so it is the body of the
        function the trigger will actually run and not of a same-named one — and compares it
        whitespace-collapsed against the model; and it reads `t.tgqual IS NOT NULL`, refusing any
        trigger that carries a `WHEN` condition this build did not author.
        `pg_get_expr(tgqual, tgrelid)` was measured and rejected: it raises `expression contains
        variables of more than one relation` for a condition naming both `OLD` and `NEW`, which
        would turn a refusal into a catalog-read error.
  - [x] level 3 reads `pg_class.relpersistence` and refuses anything but `'p'` on all three tables.
  - [x] **five** live cases, not three, each its own scratch schema, each reaching
        `ErrSchemaMismatch` at `Prepare` and each naming the object: both functions replaced
        (`RETURN NEW` and `RETURN NULL`), the trigger re-created `WHEN (false)`, the trigger
        re-created `BEFORE UPDATE OF recorded_at`, and `ALTER TABLE events SET UNLOGGED`. The
        fourth is a fifth hole of the same family found while closing this one and measured live:
        `tgtype` is unchanged by `UPDATE OF`, so the shipped comparison passed it, and
        `UPDATE events SET payload = …` was carried out (`UPDATE 1`) with the trigger in place.
  - [x] the control still passes — the intact schema, the extra index and the unrelated table all
        verify — and a control was **added** in
        `TestTheDatabaseRefusesUpdateDeleteAndTruncateAndAdmitsInsert`: with the function replaced
        by one that returns, the `UPDATE` lands and the stored payload reads back `0x02`, so the
        case that refuses that schema guards a hole that is there.
  - [x] each new branch is mutation-checked, `md5sum` identical before and after, and the five are
        recorded in the plan's mutation table beside the existing eighteen:
        `relpersistence` → `the history is unlogged`; `tgattr` → `the append-only trigger watches
        one column`; `tgqual` → `the append-only trigger fires only when a condition holds`;
        `prosrc` → both function-replaced cases; `rendering()` emitting a function's name without
        its body → `a function body is an input`, untagged.
- **Status:** closed 2026-09-08. Reproduced first — all five cases failed with
  `verified with <nil>` against the shipped code — then fixed, and the tagged suite is green twice
  in a row under `-race` (15.806s, 16.020s) with `git status --porcelain event/` still `?? event/eventpg/`.

---

### What was checked and is clean, so a later round does not re-derive it

- **[[D-118]] and the ambient-transaction question are S3's**, not S2's: `executor.go` does not
  exist yet and `Transaction`/`Append`/`ReadStream`/`ReadAll` are absent from the surface, which is
  what S2's **Realises** line says. Nothing in `migration.go`, `verify.go` or `catalog.go` consults
  `crud.ExecutorFor`, and none of the three joins or refuses a caller's transaction — they run on a
  `*sql.Conn` of their own by design, and `Migrate` is the only file naming `BeginTx`, `Commit` or
  `Rollback` (grep: 1 file, 3 occurrences, all in `migrateOn`). Reviewed and deferred to S3 rather
  than reported as absent.
- **UC-073's lock is observed rather than inferred.** `TestTwoConcurrentPreparesTakeOneLockOnOneBackend`
  reads `pg_locks` **while** the migration runs and asserts the advisory-lock holder pid and the
  relation-waiter pid are the *same* backend; M2 above confirms the assertion is load-bearing.
  `TestASecondMigrationBlocksAgainstAHeldLock` supplies the "somebody actually waited" control and
  the "nobody waits when nobody holds it" counter-control.
- **UC-096 is proved by the database, not by the package's vocabulary** —
  `TestTheDatabaseRefusesUpdateDeleteAndTruncateAndAdmitsInsert` issues the three verbs directly
  and asserts the message names the verb and `append-only`, with an `INSERT` beside them as the
  control (row count 2). *Within the deployed function body*, which GAP-P2-S2-1 is about.
- **UC-070 / [[D-101]]**: `New` resolves the zero to `VerifySchema` (`config.go:113-116`),
  `Migrate` refuses under anything but `ManageSchema` with `ErrSpec` naming both profiles by their
  `String()` (`migration.go:157-160`), and the missing-schema case asserts the schema still does
  not exist afterwards. Nothing migrates unless asked.
- **UC-071 / INV-057**: the version is compared with `!=`, not `>=`, in one place
  (`verify.go:108`), and the message carries both numbers and the schema and no row, credential or
  DSN.
- **UC-094**: `Check` is levels 1 and 2 plus the log, one round trip, and it is proved to be
  `health.Probe`-shaped by assignment; `eventpg` imports nothing of `health` and names no
  importance, no code and no check name.
- **INV-058**: `TestLimitsAreTheDeployedConstraintOperands` reads the deployed
  `pg_get_constraintdef` operand and compares it with `Limits()`, then drives the kernel's own
  refusal one byte over through `eventmemory` at eventpg's published limits.
- **No goroutine is started anywhere in the package** (grep `go ` statements in non-test files: 0),
  nothing is logged (`log.` : 0), no environment is read outside `_test.go` ([[D-092]], [[D-062]],
  INV-060 — the S1 AST check still holds with S2's three new files in the walk).
- **Determinism**: the only randomness is the advisory-lock backoff jitter, which is `jobspg`'s
  and is the correct use of it; every rendered artefact (the statement list, the fingerprint
  rendering) is sorted or fixed-order and pinned by a golden.
