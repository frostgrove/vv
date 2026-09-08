# EVENTSOURCE_P2 — implementation S1 (`event/eventpg`: the module, the pure surface, the schema statements) — GAPS

## Round 1 — econv-implementation-reviewer (clean context) — 2026-09-08

Reviewed against the **code**, not the plan's prose. Read in full:
[`EVENTSOURCE_P2_PLAN.md`](../plans/EVENTSOURCE_P2_PLAN.md) (§What this plan delivers, §What was
measured live, D1–D7, §Coverage matrix, §Contracts before code for `doc.go`/`schema.go`/`config.go`/
`migration.go`, §S1, §The module checklist, §The zero-diff proof, §Debt),
[`EVENTSOURCE_P2_USECASES.md`](../usecases/EVENTSOURCE_P2_USECASES.md) (§2.1, §2.2, UC-069…UC-075,
INV-058…INV-063, §5), [`EVENTSOURCE_BACKLOG.md`](EVENTSOURCE_BACKLOG.md) `## P2` §1–§26,
`event/eventpg/{doc,schema,config,migration}.go`, `event/eventpg/{schema,config,sources}_test.go`,
`event/eventpg/testdata/fingerprint.golden`, `event/eventpg/go.mod`, `go.work`,
`scripts/event_test.go`, `scripts/extensions_test.go`, `event/bounds.go`, `event/backing.go`,
`crud/executor.go`, `jobs/jobspg/config.go`, `CLAUDE.md`.

Every number, transcript and mutation below was produced in this worktree against the live
PostgreSQL 17.9 at `postgres://vv:vv@localhost:55432/vv`. The one scratch file used to dump the
statement list was removed; `git status --porcelain event/` is `?? event/eventpg/` and nothing else,
and `gofmt -l event/eventpg` is silent.

---

### Checkpoint verification — the pasted transcripts are real

Both S1 checkpoint blocks were run here, verbatim.

| Clause | Result here |
|---|---|
| `test -f event/eventpg/go.mod && ./scripts/checks.sh workspace && go build ./event/eventpg/... && go vet ./event/eventpg/... && [ -z "$(gofmt -l event/eventpg)" ]` | `check-workspace: ok`, exit 0 |
| `go test -list '^(…seven names…)$' ./event/eventpg/ \| grep -c '^Test'` | **7** — matches the plan's `= 7` |
| `go test -race -count=1 ./event/eventpg/...` | `ok github.com/frostgrove/vv/event/eventpg 10.351s` (twice: 10.351s, 10.439s) |
| `go test -count=1 -run '^(TestNoEventPackageCostsMoreThanTheSeamItNames\|TestMerelyImportingTheEventExtensionStartsNothing\|TestNoBaseSubsystemDependsOnTheEventExtension)$' ./scripts/` | `ok github.com/frostgrove/vv/scripts 1.349s` |
| `./scripts/checks.sh workspace && ./scripts/checks.sh replaces && ./scripts/checks.sh tidy` | `check-workspace: ok`, `check-replaces: ok`, `check-tidy: ok` |
| `make check` (nine arms) | all `ok`; `./event/eventpg: 0 external packages` — pgx is test-only, as §the driver decision requires |

**The zero-diff obligation holds.** `git status --porcelain -- event/ ':(exclude)event/eventpg'` is
empty, and `git diff --stat c798fc0b -- event/ ':(exclude)event/eventpg'` is empty. The only
untracked path under `event/` is `event/eventpg/`. Nothing outside `event/eventpg` was touched;
the two files S1 changes elsewhere are `go.work` (+1 line, sorted position) and
`scripts/event_test.go` (the `charged` row plus a reworded comment and an `overreach` message that
now renders the allowance).

### Contract conformance — both directions, no drift

`go doc -all` lists exactly the 22 exported names S1's **Realises** line promises and nothing more:
`DefaultSchema`, `SchemaVersion`, `DefaultMaxPayload`, `DefaultMaxKey`, `DefaultMaxBatch`,
`DefaultPage`, `ErrSpec`, `ErrSchemaMismatch`, `ErrNotReady`, `SchemaManagement` +
`UnsetSchemaManagement`/`VerifySchema`/`ManageSchema` + `Valid`/`String`, `Schema` +
`Resolved`/`Fingerprint`, `MigrationStatements`, `Spec`, `Store`, `New`, `Store.Schema`,
`Store.SchemaManagement`, `Store.Capabilities`, `Store.Limits`, `Store.Backing`, `Store.Close`.
Every signature matches §5 of the spec and the plan's `config.go`/`schema.go` blocks character for
character. `Migrate`, `Prepare`, `Verify`, `Check`, `Transaction`, `Append`, `ReadStream`,
`ReadAll` and `var _ event.Store` are absent, which is what S1 says. **No silent extra public
surface and nothing marked done that is missing.**

### Architecture metrics — counted

*Size.* Four non-test files, **641** lines: `schema.go` 314, `config.go` 200, `migration.go` 100,
`doc.go` 27. Tests 895 (`sources_test.go` 492, `schema_test.go` 203, `config_test.go` 200).
Threshold 400 — no breach.

*Functions.* 26 across the four files. Longest body **73** lines (`expected`, a flat data literal
with no branching), then `rendering` **42**, `New` **39**, `limitsOf` **29**, `migrationStatements`
**26**, `createTable` **19**, `validSchemaName` **16**. Maximum nesting depth **3**
(`rendering`'s `for table → for column → if identity`; `validSchemaName`'s `for → switch`).
Maximum parameter count **4** (`bound(name, chosen, byDefault, ceiling)`). No flag parameter, no
mode switch, no boolean argument on any exported signature.

*Coupling.* Internal (`github.com/frostgrove/vv/…`) imports per non-test file: `config.go` **2**
(`crud`, `event`), `schema.go` **1** (`event`), `migration.go` **0**, `doc.go` **0**. Threshold 5 —
no breach. Third-party imports in non-test files: **0** (`check-deps`: `./event/eventpg: 0 external
packages`). `_ "github.com/jackc/pgx/v5/stdlib"` appears only in `config_test.go:15`. No import
cycle; nothing in the tree imports `eventpg`.

*Extension-cost closure.* `go list -deps ./crud/adapter/crudsql` first-party closure is exactly
`crud`, `crud/adapter/crudsql`, `crud/catalog`, `crud/sqlfault`, `errs`, `errs/sqlerr`, `utils` —
`grep -E 'frostgrove/vv/(port|health|runtime)'` over it returns nothing. **INV-063's mechanism
holds by measurement**, and `costOverruns` really does take the allowance's transitive closure
(`scripts/extensions_test.go:68-77`), so the row is a bound and not a label.

*Global mutable state.* `packageLevelState` over the four non-test files reports nothing, and I
confirmed by reading: no package-level `var` at all outside the three `errors.New` sentinels.
`Store.closed` is `atomic.Bool` — backlog `## P2` §21's *Owed* line is satisfied by the code.

*Determinism.* No randomness, no clock, no map iteration in any rendering path. `rendering()` sorts
with `slices.Sort` and the golden is stable across runs. The only random value in the whole design
is `gen_random_uuid()`, minted inside PostgreSQL where §UC-069 puts it.

### Isolation and building blocks

`Schema` is a value object: validated at construction through `Resolved`, holds no repository, no
clock and no config source, and both its methods are pure. `expectation` and its five `expected*`
records are one immutable model with two renderers. `Store` is not yet a use case (three of the
eight doors do not exist); it holds a pool, a `crud.Source`, two resolved values and one atomic
flag. Every new object is unit-testable with **zero** fakes — the whole S1 suite runs with no
database, using a `*sql.DB` over a DSN nothing answers. Deleting `eventpg` costs one `go.work` line
and one `charged` row.

### Error hygiene

Every returned error wraps `ErrSpec` with `%w`, every test compares with `errors.Is`, and no
message carries SQL text, payload bytes, a key, a position, a cursor, a credential or a DSN. The
only caller-supplied value rendered is `Schema.Name` (`schema.go:48`), which is configuration and is
what the operator has to be told. `SchemaManagement` renders through `String()` in words, never
`%d`. Nothing writes to a process logger, nothing prints, nothing reads the environment — pinned by
`TestTheStoreStartsNothingAndReadsNoEnvironment` with a control fixture that must report `init`,
`go`, `log.`, `fmt.Print`, `os.Getenv` and four package-level shapes while leaving a sentinel, a
parameter *named* `log` and local state unreported.

### Mutation harness — what the S1 suite catches and what it does not

Ten mutations were applied one at a time to the real sources and the whole package suite run
(`go test -count=1 ./event/eventpg/`); every file was restored byte-identical afterwards
(`diff -q` against a pre-mutation copy) and the tree is green.

| Mutation | Suite |
|---|---|
| the xid trigger dropped from the expectation model | **FAIL** (golden + digest literal) |
| every `CONSTRAINT … CHECK` dropped from the generated DDL | **FAIL** (the narrow-bounds subtest) |
| `New` drops the `crud.SameDataSource` refusal | **FAIL** |
| the backing forgets the schema name | **FAIL** |
| `validSchemaName` returns true for everything | **FAIL** |
| the `Schema.MaxKey` ceiling is not enforced | **FAIL** |
| **every trigger created `AFTER` instead of the model's timing** | **ok** — survivor |
| **`NOT NULL` dropped from every generated column** | **ok** — survivor |
| **the append-only trigger function reduced to `RETURN NULL`** | **ok** — survivor |
| **`GENERATED ALWAYS AS IDENTITY (INCREMENT 1 CACHE 1 NO CYCLE)` reduced to `GENERATED ALWAYS AS IDENTITY`** | **ok** — survivor |
| **`UNIQUE (family, key, version)` dropped from the generated DDL** | **ok** — survivor |
| **`FOREIGN KEY (family, key)` dropped from the generated DDL** | **ok** — survivor |

Six survivors, and they are GAP-P2-S1-2 below.

---

### GAP-P2-S1-1 [high][immediate] Migration statement eleven stamps this build's fingerprint onto whatever schema it finds, so a re-run over a drifted or differently-bounded schema records a fact that is false

- **Where:** `event/eventpg/migration.go:35-40` (statement 11,
  `"UPDATE "+schema+"."+metaTable+" SET version = "+version+", fingerprint = "+quoted+" WHERE singleton"`),
  and the comment that describes it, `event/eventpg/migration.go:10-15`. Contract:
  `.agents/artifacts/plans/EVENTSOURCE_P2_PLAN.md:485`; use case: §UC-074, §2.2 level 2.
- **What:** statements 2–4 are `CREATE TABLE IF NOT EXISTS`, so a re-run against an existing schema
  changes **no** table, **no** constraint and **no** trigger. Statement 11 is not conditional on
  anything: it overwrites `schema_meta.fingerprint` with the running build's expectation
  unconditionally. A build whose `Schema` differs from the deployed one therefore *erases the
  evidence that they differ*. Driven live against PostgreSQL 17.9:

  ```
  # deployed by a build at Schema{Name:"review_events", MaxPayload:1024}
  fingerprint  sha256:a5f78bb5f2c7aecb78da0770aa8910d2a3eb1c1384dc65e04ff343c1a9b70434
  constraint   CHECK ((octet_length(payload) <= 1024))

  # the DEFAULT build (MaxPayload 65536) runs MigrationStatements(Schema{Name:"review_events"})
  # against it — psql --single-transaction, ON_ERROR_STOP=1, exit 0, no error of any kind
  fingerprint  sha256:86cf563919b12bf6f5022904cbcf505ff066d86242b5d545de5a137e149ea3b1   <-- rewritten
  constraint   CHECK ((octet_length(payload) <= 1024))                                    <-- unchanged
  ```

  The comment at `migration.go:10-15` says statement eleven "leaves … a drifted one loud at the
  next verification rather than silently rewritten to agree". It is the rewriting-to-agree.
- **Why this severity:** three things the design sells rest on `schema_meta.fingerprint` being a
  description of the schema it sits in, and all three break on the transcript above.
  1. **§UC-074's *Then* is not what happens under `ManageSchema`.** "A schema migrated with
     MaxPayload 64 KiB and a process configured for 1 MiB → the fingerprint differs; `Prepare`
     refuses and names both numbers." `Prepare` migrates first (§2.2), so by the time level 2 runs
     the fingerprint has been made to agree, level 2 passes, and only level 3 can refuse — with a
     message about a `CHECK` definition, not about two payload bounds. The store still fails
     closed, but by the wrong level with the wrong message, and it has already written a false row.
  2. **`Check(ctx)` is levels 1 and 2 only** (plan §verify.go). A readiness probe on a store whose
     database was re-stamped answers healthy for a schema that does not match — the level that was
     supposed to catch "the right version migrated by a different build" is the one that was just
     overwritten.
  3. **The exported `Fingerprint()`'s stated reason to exist is defeated.** The plan justifies
     exporting it with "during an incident an operator compares a build's expectation against
     `schema_meta.fingerprint` with a two-line program". After any `ManageSchema` start-up or any
     re-run of the operator list, that comparison returns *match* on the exact database it was
     invented to catch.
  A recorded fact that is false, produced with no error, is worse than the absence of the fact.
- **Why this timing:** the statement list is S1's deliverable and the fingerprint is already pinned
  by `testdata/fingerprint.golden` and by the literal at `schema_test.go:13`. S2 builds `Migrate`,
  `Verify` and `Check` directly on this list and S5 ships module pages telling operators to compare
  the value. Every schema migrated between now and the fix carries a stamp nobody can trust, and
  changing the rule after `MIGRATIONS.md` ships is a schema-version bump rather than an edit.
- **Close criteria:**
  - [x] Statement 11 writes nothing at all. It is now a `DO` block that reads `schema_meta` and
        raises SQLSTATE `EVPG1` unless the row holds this version at this fingerprint, which rolls
        the whole migration back (`event/eventpg/migration.go`, `assertMeta`). `EVPG1` rather than
        plpgsql's default `P0001`, so a caller can tell this refusal from any other `RAISE` without
        comparing message text.
  - [x] The rule is written down and is general — *a statement list may record a description only
        of a schema it can produce* — in `migration.go`'s header comment, in the plan's
        §`MigrationStatements` block and in §2.2 of the spec. It names what version N does instead:
        an `UPDATE` guarded on the version it migrates from, followed by the same assertion.
  - [x] The comment now says statement eleven records nothing; the plan's statement-11 line is
        rewritten in the same change.
  - [x] `TestAMigrationOverASchemaItDidNotBuildRefusesRatherThanRestampingIt`
        (`migration_integration_test.go`, live, PG 17.9): deploy at `MaxPayload` 1024, run the
        default build's list — it must fail with SQLSTATE `EVPG1` naming both digests,
        `schema_meta.fingerprint` must still be the deployed one, and `events_payload_check` must
        still read 1024. Control: the same build re-running its own list succeeds, moves the
        fingerprint nowhere and reissues no `log`.
        `TestARefusedMigrationLeavesTheTablesItCreatedRolledBack` adds the half-built case: drop
        `events`, run another build's list, and the table it created is gone again.
        Reverting statement eleven to the unconditional `UPDATE` fails both the untagged and the
        tagged suite; before the fix the same transcript passed with exit 0.
  - [x] §UC-074 is rewritten: it now names **which** door refuses under which profile — level 2
        under `VerifySchema`, the migration's own last statement under `ManageSchema`, before level
        2 is reached — and its Must-not carries the rule that a list may not record a description it
        cannot produce.
- **Status:** closed 2026-09-08

### GAP-P2-S1-2 [high][immediate] `MigrationStatements` is exported operator-facing API and no S1 test reads what it says; six DDL mutations that destroy INV-046, INV-048 and INV-065 leave the whole suite green

- **Where:** `event/eventpg/migration.go:16-93` (`migrationStatements`, `createTable`,
  `expectedColumn.definition`, `createFunction`, `createTrigger`) against
  `event/eventpg/schema_test.go:102-203` (`TestMigrationStatementsAreOrderedTransactionalDDL`), and
  the claim at `event/eventpg/schema.go:95-98` that the migration and the fingerprint "cannot drift
  apart".
- **What:** the test reads the statements' **first 60 characters** (a prefix table), the absence of
  the word `CONCURRENTLY`, two substrings of the `schema_meta` insert, the fingerprint literal in
  statements 10 and 11, two `CHECK` substrings in a narrow-bounds variant, four illegal schema
  names and the lock function. It never reads the column definitions, the key and constraint
  clauses, the trigger clauses or the function bodies. The `expectation` model is shared, but the
  two *renderings* of it are independent code — `rendering()` in `schema.go` and
  `definition()`/`createTable`/`createTrigger` in `migration.go` — so the fingerprint moves for a
  model change and stays put for a DDL-rendering change. Measured, one mutation at a time, whole
  suite green each time:

  | Mutation to the generated DDL | What it destroys | Suite |
  |---|---|---|
  | `CREATE TRIGGER … AFTER …` instead of `trigger.timing` | §INV-065 premise 2 — an `AFTER INSERT` statement trigger takes the transaction id after the positions are drawn, and the global walk's watermark rests on the opposite | `ok` |
  | `events_are_append_only()` body → `RETURN NULL;` | §INV-048 — `UPDATE` and `DELETE` on `events` succeed | `ok` |
  | `CONSTRAINT events_stream_version_key UNIQUE (family, key, version)` dropped | §INV-046 — the one mechanism that makes two events at one version unrepresentable, and §2.1's own "what it replaces: a Go comparison, which holds only for writers that ran the comparison" | `ok` |
  | `CONSTRAINT events_stream_fkey FOREIGN KEY …` dropped | §2.1 — an event row with no stream row becomes representable | `ok` |
  | `NOT NULL` dropped from every column | §2.1 | `ok` |
  | `(INCREMENT 1 CACHE 1 NO CYCLE)` dropped from the identity | §2.6 premise 1 (harmless at PostgreSQL's defaults, which is why it is listed last) | `ok` |

  Only the seventh mutation — dropping every `CHECK` — fails, and only because one subtest greps
  for two constraint substrings in a narrow-bounds variant.
- **Why this severity:** this is the shape the delivery policy names by name from phase 1 —
  "`sameValue` … could be replaced by `return true` with the entire suite green". S1 is marked
  `[x]`, the coverage matrix names `TestMigrationStatementsAreOrderedTransactionalDDL` as UC-069's
  S1 proof, and that test survives a mutation that deletes the unique constraint the entire
  concurrency contract is built on. The statements are not an internal detail: `MigrationStatements`
  is exported precisely so a deployment runs them from its own migration step ([[D-101]]), which
  means the text is the contract. A reviewer, a rebase or an "optimisation" that touches
  `createTable` gets a green light from the only gate that exists until S2.
- **Why this timing:** the whole point of an S1 that ships no I/O is that the pure artefacts are
  pinned before anything is built on them. S2, S3 and S4 all migrate scratch schemas from this list;
  every live proof they claim is a proof about whatever DDL this generator happened to emit that
  day. Pinning it costs one golden file — the precedent (`testdata/fingerprint.golden`) is already
  in this package and already works.
- **Close criteria:**
  - [x] `testdata/migration.golden` (default `Schema`) and `testdata/migration_narrow.golden`
        (`{narrow_events, 1024, 40}`) pin the list byte for byte, read by
        `TestMigrationStatementsAreOrderedTransactionalDDL`'s first subtest exactly the way the
        fingerprint golden is read, with the same "read it as a diff" failure message.
  - [x] All six mutations were re-applied one at a time to the fixed sources and every one fails
        both suites; a control run with nothing mutated is green, so the failures are the mutations
        and not the harness. The table is in the plan's §S1 under **Mutation evidence**. Two more
        were added — statement eleven back to the unconditional `UPDATE`, and the schema name
        emitted unquoted — and both fail too.
  - [x] The claim is corrected: `schema.go`'s comment on `expectation` now says one model with two
        independent renderings, that a model change moves both while a rendering change moves only
        itself, and names the two goldens as what holds the DDL.
  - [x] `expectedIdentity{generation, increment, cache, cycle}` is one fact. `clause()` renders it
        into the DDL and `rendering()` into the fingerprint; there is no second literal. The
        fingerprint is byte-identical to before the change, so no deployed schema is invalidated.
- **Status:** closed 2026-09-08

### GAP-P2-S1-3 [high][immediate] `validSchemaName` admits every PostgreSQL reserved word, so `New`, `Resolved`, `Fingerprint` and `MigrationStatements` all accept a schema name whose DDL PostgreSQL refuses with a syntax error

- **Where:** `event/eventpg/schema.go:78-93` (`validSchemaName`), used by `schema.go:47-49`
  (`Resolved`) and therefore by `Fingerprint`, `MigrationStatements` and `New`. The refusal message
  is `schema.go:48`.
- **What:** the rule is "1 to 63 bytes of `[a-z][a-z0-9_]*`" and the message tells the caller that
  this is "the only identifier this store deploys into unquoted". It is not: roughly a hundred
  PostgreSQL reserved words match that pattern and cannot appear unquoted after `CREATE SCHEMA`.
  Every statement in the list interpolates the name unquoted (`migration.go:18,48-60,78-88,36-39`).
  Driven live on PostgreSQL 17.9:

  ```
  user       ERROR:  syntax error at or near "user"
  table      ERROR:  syntax error at or near "table"
  select     ERROR:  syntax error at or near "select"
  order      ERROR:  syntax error at or near "order"
  group      ERROR:  syntax error at or near "group"
  all        ERROR:  syntax error at or near "all"
  default    ERROR:  syntax error at or near "default"
  do         ERROR:  syntax error at or near "do"
  ```

  `MigrationStatements(Schema{Name: "user"})` returns eleven statements and a nil error;
  `New(Spec{DB, Source, Schema: Schema{Name: "user"}})` returns a `*Store` and a nil error;
  `Schema{Name: "user"}.Fingerprint()` answers a digest for a schema that cannot exist.
- **Why this severity:** it is a validator that reports success for an input it cannot serve, and
  the exported message states a guarantee it does not provide. The plan's own contract is that
  `New` refuses "an illegal `Schema.Name` … one door earlier than the kernel would"; for this class
  the refusal arrives instead at `Migrate`, as a raw PostgreSQL syntax error carrying the statement
  text — which then collides with the store's own error-hygiene rule (no SQL in a returned error).
  The failure is loud rather than silent, which is why this is `[high]` and not `[critical]`, but
  the shape — a hand-rolled identifier rule copied from `jobspg` and never checked against the
  grammar it claims to encode — is exactly the universality defect this repository refuses: the
  rule was validated against the names somebody happened to try.
- **Why this timing:** the rule is a `Resolved` input, so it is upstream of the fingerprint, the
  statement list, `New` and every scratch schema S2–S5 build. It is also cheap now and awkward
  later: quoting the identifier changes the DDL text and therefore, if GAP-P2-S1-2's golden lands
  first, the golden too.
- **Close criteria:**
  - [x] Quoting is chosen. `quoteIdentifier` doubles any `"` and every statement emits the schema
        name as `"<name>"` — `CREATE SCHEMA`, all three `CREATE TABLE`s, the `REFERENCES` clause,
        both `CREATE OR REPLACE FUNCTION`s, all three trigger `DO` blocks, the `INSERT` and the
        assertion. A reserved-word table was rejected: PostgreSQL's list changes between versions
        and the store serves whichever server it is pointed at, so a table would be the same defect
        in a different costume.
  - [x] The message now states the rule it actually enforces: the pattern is a narrowing this store
        chooses so that a name reads the same quoted and unquoted and an operator can type it back —
        not a claim about the PostgreSQL grammar.
  - [x] `TestASchemaNamedForAReservedWordDeploys` (live, PG 17.9) migrates into `user`, `table`,
        `select`, `order`, `group`, `all`, `default` and `do`, and checks each records its own
        fingerprint. Its control keeps `User`, `1events`, `events; DROP TABLE x` and `events"`
        refused, so quoting is not read as permission to take any name. The untagged suite gains
        "a reserved word is a name this store deploys into, because every statement quotes it",
        which walks the rendered list for whole-token mentions of the name and fails on any that is
        not wrapped in quotes; reverting the quoting fails it.
  - [x] `New`, `Resolved`, `Fingerprint` and `MigrationStatements` accept exactly the set the DDL
        deploys. No name reaches PostgreSQL as a syntax error: the eight that did are now the live
        test's happy cases.
- **Status:** closed 2026-09-08

---

### What was checked and is clean, so a later round does not re-derive it

- **The zero-diff obligation.** Measured twice, both pathspec commands empty. §INV-061 holds.
- **The exported surface.** 22 symbols, byte-identical signatures to §5 and to the plan. No extra,
  none missing, no `event.Store` assertion (S4's, correctly absent).
- **`New`'s refusals.** All six classes the plan names are implemented and all ten cases in
  `TestNewRefusesEverySpecItCannotAssemble` assert both `errors.Is(err, ErrSpec)` and that the
  message names the value set *and* the value it may not pass. Removing the `SameDataSource` arm,
  the `MaxKey` ceiling or the name rule each fails the suite.
- **The backing.** `backing{db *sql.DB; schema string}` is comparable, `event.NewBacking` accepts
  it, and `Backing.Equal` is `crud.SameDataSource` = type equality plus `==`
  (`crud/executor.go:562-571`). Two values over one db+schema compare equal; two schemas in one
  database and one schema in two databases do not. Forgetting the schema name fails the suite.
  Backlog `## P2` §20's *Owed* ("the backing's schema name is the resolved one") is satisfied by
  `config.go:108`.
- **Limits and capabilities.** `MaxPayload`/`MaxKey` from the resolved `Schema`; `MaxBatch` 64;
  `StreamPage`/`MaxRead` `min(256, event.ResidentPage(MaxPayload))`, and at a 1 MiB payload bound
  both collapse to `event.ResidentPage(1<<20)` = 64, asserted. `Transactions`/`Persistence`/
  `SharedBacking` Supported, `MonotoneVisibility` Unsupported. Matches §5 exactly.
- **The fingerprint.** The golden file, the digest literal and the "three header lines, sorted
  body, printable ASCII, newline-terminated" shape are all asserted, in both directions, plus five
  one-value perturbations that must move the digest and a control that the zero `Schema` and the
  same schema written out in full agree. Dropping one trigger from the model fails both arms.
- **The bounds are the kernel's, not this package's.** `128` is `event.MaxNameBytes` read at
  `schema.go:165`, the ceilings are `event.MaxPayloadBytes`/`MaxKeyBytes`/`MaxBatchCount`/
  `MaxPageCount`, the resident page is `event.ResidentPage`. No literal in the package is justified
  by "the sample looked like that": every one is a documented default (`frostgrove_events`, 64 KiB,
  512, 64, 256), a PostgreSQL fact (63-byte identifier limit), or a derived constant.
- **The migration is transactional and idempotent, live.** The eleven statements ran under
  `psql --single-transaction -v ON_ERROR_STOP=1` against PostgreSQL 17.9 with exit 0; the second
  run left `schema_meta.log` byte-identical (`02e127d9ff40447894f5752727aa692e`). The deployed
  catalog matches the expectation model: `attidentity='a'` on `position` and empty on the other
  seven columns, `attnotnull` true on all eight, the identity sequence at
  `seqincrement=1, seqcache=1, seqcycle=f`, and exactly three non-internal triggers on `events`
  with `tgenabled='O'`, none on `streams` or `schema_meta`.
- **The key bound fits the index.** At the kernel ceiling (`family` 128 bytes, `key` 2048) a row
  inserts into both the `streams` primary key and the `events` unique index without tripping
  PostgreSQL's btree row-size limit. Verified live.
- **Error hygiene, determinism, no ambient reach.** Covered above; the two `go/types` source checks
  each carry a control fixture that must be reported, so neither passes through an unfalsified body.
  `TestEveryBoundParameterIsATypeEveryDatabaseSQLDriverAccepts` currently has nothing to check in
  the store (no statement is issued yet) and its control is what keeps it honest until S3.
- **[[D-118]] and the ambient-transaction question** are S3's `executor.go`; nothing in S1
  pre-empts them, and `Spec.Source` is required with the nil case refused, which is the half of
  D-118 S1 can carry.
- **Module hygiene.** `go.mod` requires `github.com/frostgrove/vv v0.0.0-20260829132449-bc1e4c0b1038`
  — the same pseudo-version `jobs/jobspg/go.mod` carries — and `github.com/jackc/pgx/v5 v5.10.0`,
  the version `jobspg` already names, with no `replace` of the library. `go.work` carries the line
  in sorted position. `check-replaces` and `check-tidy` are green.

Medium and low findings are recorded in [`EVENTSOURCE_BACKLOG.md`](EVENTSOURCE_BACKLOG.md) under
`## P2` §27–§34 and are left alone, per the delivery policy in force.


---

## Round 2 — closing the three blocking findings — 2026-09-08

All three `[high][immediate]` findings are **closed in the code**, each reproduced first and each
left with a test that fails when the fix is reverted. Nothing outside `event/eventpg/` was touched:
`git status --porcelain -- event/ ':(exclude)event/eventpg'` is empty.

| Finding | Reproduced | Closed by | Fails again if reverted |
|---|---|---|---|
| GAP-P2-S1-1 | live, PG 17.9: the default build's list over a schema deployed at `MaxPayload` 1024 restamped `schema_meta.fingerprint` and left `CHECK ((octet_length(payload) <= 1024))` in place, exit 0 | statement eleven records nothing; it asserts and raises `EVPG1`, rolling the migration back | `TestAMigrationOverASchemaItDidNotBuildRefusesRatherThanRestampingIt`, `TestARefusedMigrationLeavesTheTablesItCreatedRolledBack`, and the untagged "statement eleven records nothing" subtest |
| GAP-P2-S1-2 | six DDL mutations applied one at a time, whole suite `ok` each time | `testdata/migration.golden` + `testdata/migration_narrow.golden`, read byte for byte; `expectedIdentity` makes the identity one fact | all six mutations now FAIL both suites; control run with nothing mutated is green |
| GAP-P2-S1-3 | live: `user`, `table`, `select`, `order`, `group`, `all`, `default`, `do` each `syntax error at or near` | the schema name is quoted in every statement; the refusal message states the real rule | `TestASchemaNamedForAReservedWordDeploys` (live) and the untagged quoting subtest |

**What this changed outside the code.** `main_integration_test.go` moves from S2 to S1 — two of S1's
own claims are claims about PostgreSQL — and carries the `TestMain` that **fails** on an unset
`FROSTGROVE_EVENTPG_TEST_DSN` rather than skipping. S1 therefore has a live checkpoint of its own,
pasted in the plan. §UC-074 now names which door refuses under which profile; §2.2 carries the rule
that a statement list may record only a description it can produce, and the quoting rule.

Medium and low findings in `EVENTSOURCE_BACKLOG.md` `## P2` §27–§34 were **not** touched, per the
delivery policy.
