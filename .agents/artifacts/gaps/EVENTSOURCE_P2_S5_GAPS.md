# EVENTSOURCE_P2 — implementation S5 (the conformance run, the mutation harness, the checks, the docs) — GAPS

## Round 1 — econv-implementation-reviewer (clean context) — 2026-09-08

Reviewed against the **code**, not the plan's prose and not anyone's summary. Read in full:
[`EVENTSOURCE_P2_PLAN.md`](../plans/EVENTSOURCE_P2_PLAN.md) (§What this plan delivers, §What was
measured live, §The seven questions D1–D7, §Coverage matrix, §Contracts before code, §S5 in full,
§The module checklist, §The zero-diff proof, §Debt),
[`EVENTSOURCE_P2_USECASES.md`](../usecases/EVENTSOURCE_P2_USECASES.md) (UC-085, UC-094, UC-097;
INV-047, INV-055, INV-061, INV-062, INV-063),
[`EVENTSOURCE_BACKLOG.md`](EVENTSOURCE_BACKLOG.md) `## P2` §1–§60,
`event/eventpg/{conformance,mutation,audit,main}_integration_test.go`,
`event/eventpg/{append,read,cursor,executor,classify,config,verify,migration,schema}.go`,
`event/eventpg/MIGRATIONS.md`, `scripts/checks.sh`, `scripts/common.sh`, `scripts/vv`, `Makefile`,
`scripts/checks_test.go`, `docs/modules/{en,ru}/eventpg.md` and their index rows,
`docs/ai/flows/FL-037-…md` and its three entries in `docs/ai/flows/Index.md`,
`docs/ai/decisions/D-126-…md`, `D-127-…md`, `D-101`'s *See also*, `docs/roadmaps/Roadmap.md` §15,
`docs/api/surface.md`, the frozen kernel `event/{store,reader,errors,outcome,backing}.go`,
`crud/executor.go`, `CLAUDE.md`.

Everything below was produced in this worktree against the live PostgreSQL **17.9** at
`postgres://vv:vv@localhost:55432/vv` (`select version()` → `PostgreSQL 17.9 on x86_64-pc-linux-musl`).
**Three source mutations were applied and every one was restored byte-identical**
(`md5sum -c` before and after: `read.go` `fd157f4c…`, `classify.go` `e805187b…`, `append.go`
`ef9af044…`, `conformance_integration_test.go` `867b9b0e…`), and the tree was re-run green
afterwards: `go test -race -count=1 -tags=integration ./event/eventpg/` → `ok … 65.189s`,
`./scripts/checks.sh event-kernel` → `ok`, `git status --porcelain -- event/` → `?? event/eventpg/`
and nothing else.

---

### The zero-diff obligation — holds, and holds under the arm rather than under a weaker command

| Command | Result here |
|---|---|
| `git status --porcelain -- event/` | `?? event/eventpg/` and nothing else |
| `git status --porcelain -- event/ ':(exclude)event/eventpg'` | empty |
| `git diff --stat c798fc0b… -- event/` (no exclusion at all) | **empty** — every tracked file under `event/` is exactly where the phase-1 commit left it |
| `git diff --stat c798fc0b… -- event/ ':(exclude)event/eventpg'` | empty |
| `./scripts/checks.sh event-kernel` | `check-event-kernel: ok` |
| `make check` | ten arms, all `ok`, including `check-event-kernel` |
| `go test -count=1 ./scripts/` | `ok … 5.575s` (the four self-test cases and `TestTheEventKernelOfThisRepositoryIsWhereThePhaseOneCommitLeftIt`) |

The arm is real rather than nominal. `scripts/checks.sh:5-6` sources `common.sh` and `cd "$REPO_ROOT"`
where `REPO_ROOT` is derived from **the script's own path**, so `runCheckWithEnv`'s copy in
`t.TempDir()` runs git against the fixture and not against this repository — which is what makes the
four cases in `scripts/checks_test.go:369-419` constructible at all. `EVENT_KERNEL_BASELINE` is
overridable (`checks.sh:26`) and the override reaches bash: case 4 sets it to forty zeroes and gets
exit 1 naming the constant. `c798fc0b` resolves here (`feat: tenancy & eventsource added`).

### Checkpoint verification — the pasted transcript is real

The S5 block (`EVENTSOURCE_P2_PLAN.md:2028-2041`) was run here, arm by arm.

| Clause | Plan pasted | Result here |
|---|---|---|
| `FROSTGROVE_EVENTPG_TEST_DSN=x … -list '.' \| grep -q '^Test'` | `list arm: ok` | names printed, exit 0 |
| the six-name count | `6` | `6` |
| `./scripts/checks.sh event-kernel` | `ok` | `check-event-kernel: ok` |
| `go test -race -count=1 -tags=integration ./event/eventpg/` ×2 | `65.358s`, `65.715s` | `ok … 65.053s`, `ok … 65.329s` — **green twice in a row** |
| `grep -c 'eventtest: .*: passed' \| grep -qx 19` | `nineteen passed: ok` | **19**, and the twentieth line is `monotone visibility: not certified — this store does not promise monotone visibility` |
| `make unit` · `make vet` · `gofmt -l .` | ok · ok · silent | exit 0 · exit 0 · empty |
| `make check` | ten arms ok | `check-deps`, `check-tiers`, `check-utils`, `check-triplets`, `check-todo`, `check-replaces`, `check-tidy`, `check-otel-schema`, `check-workspace`, `check-event-kernel` — all `ok`; `./event/eventpg: 0 external packages` |
| `go test -count=1 ./scripts/` | `5.791s` | `ok … 5.575s` |
| `make api && git diff --stat -- docs/api/surface.md` | `109 insertions(+), 2 deletions(-)` | **`109 insertions(+), 2 deletions(-)`**, byte-for-byte the same shape |

The `## github.com/frostgrove/vv/event/eventpg` section of `docs/api/surface.md` is exactly the
surface §Contracts before code fixes — `DefaultSchema` and the constants, the three sentinels,
`MigrationStatements`, `Schema`, `SchemaManagement`, `Spec`, `Store`, `New` — and nothing else.

### The live gate — it fails, it does not skip

| Run | Result |
|---|---|
| `env -u FROSTGROVE_EVENTPG_TEST_DSN go test -count=1 -tags=integration -run '^TestTheStoreSatisfiesTheContract$' ./event/eventpg/` | `FAIL … 0.002s`, message names the variable and prints the command, **no `SKIP` anywhere** |
| the same with `FROSTGROVE_EVENTPG_TEST_DSN='postgres://vv:vv@localhost:55432/vv?sslmode=disable'` | runs and passes, five separate times over this review |
| `TestTheGateFailsWhenTheDSNIsUnset` (both subtests, live) | `PASS` — including its own control, the same command with the DSN set and no test selected, exit 0 |

### The two runs — measured at both configurations, not only at the one the checkpoint counts

| Run | passed | not certified |
|---|---|---|
| `TestTheStoreSatisfiesTheContract` | **19** | `monotone visibility` |
| `TestTheStoreSatisfiesTheContractAtNarrowerLimits` | **19** | `monotone visibility` |

`store failure classification`, `durability`, `shared backing`, `resumption` and `transactions` are
all `passed` at both, as the plan predicted before running.

### The mutation harness — six mutations, all caught, and the harness is not vacuous

`TestTheConformanceSuiteCatchesADefectiveStore` `PASS (9.44s)`: the unmutated control exits 0, and
each of `short-stream-page`, `reversed-log-page`, `stale-admission`, `newest-position-cursor`,
`pooled-payload-buffer`, `crossed-stream` exits non-zero **with the named section reporting
`failed`** (1.24 s–1.40 s each). `TestTheAuditOverTheWholeSchemaHolds` `PASS` with all five
subtests; `TestStoredRowsAreUpcastAndNeverRewritten` `PASS`.

### The two mutations I drove into the implementation

**Mutation A — `classify.go`'s last line, `return event.Unconfirmed` → `return event.NotWritten`**
(the "a default that guesses NotWritten" lie the file's own comment exists to refuse).
`TestTheStoreSatisfiesTheContract` went **red**:

```
eventtest: store failure classification: failed — [outcome unconfirmed] injected at the append door
through the store itself answered event: the store failed where the kernel maps that classification
to event: the append was issued and its outcome was never confirmed
--- FAIL: TestTheStoreSatisfiesTheContract (0.92s)
```

That is the finding that matters most in the other direction: **D1's `Unconfirmed` hook is real**.
The `store failure classification` verdict is a measurement of this store's own classifier against a
real driver error, not a fabricated `event.Failure`. §INV-062's evidence row stands.

**Mutation B — `read.go`'s `deliverable` gutted to `return len(fetched)`** (the naive
`position > cursor` walk: deliver everything above the cursor, gap or no gap). The conformance run
reported **19 passed / 1 not certified, identical to the unmutated run, exit 0**. The full tagged
suite caught it emphatically — `TestAWalkDoesNotPassAPositionAWriterCouldStillCommit`,
`TestTheXminEqualsXmaxRuleFailsTheInFlightCase`, all four subtests of
`TestABurntGapIsPassedInsideOneReadAllAndALiveOneIsNot`,
`TestAWalkInsideATransactionMintsNoBoundAndSettlesNoGap`, and both subtests of
`TestAForeignWriterIsNotSkippedAndTheOverridingWriterIs` — so **the store is protected**; it is
`eventtest` that is blind here. That blindness is already recorded, measured and owned:
backlog `## P2` §59, `[high]`, owner *the phase that unfreezes the kernel*, and §INV-061 forbids
phase 2 from patching it. What my measurement adds to §59 is that it is not only the *cursor* that
can cross a live gap invisibly — the *delivered page* can too, and the suite still says `passed`.
§59's owed remedy (a `resumptionSection` case that walks while the late writer's transaction is
open) closes both. **Reported in words, per the delivery policy; not raised as a new blocker,
because it is not this section's to close.**

### Contract conformance — both directions

Plan → code, S5's own deliverables:

| Plan says | Code |
|---|---|
| `New`: schema-per-store, own pool, `t.Cleanup` drops | `conformance_integration_test.go:45-46,67-74` ✔ |
| `SetMaxOpenConns(6)`, `SetMaxIdleConns(1)` (S5 change 1) | `:102-103` ✔ |
| `Begin`: `crudsql` at READ COMMITTED, `crud.BindExecutor` | `:108-119` ✔ — `crud.BeginnerOf`, nil `TxOptions`, bound under the store's own `source`; `bindingFor` keys on `KeyOf`, so the fresh `crudsql.Postgres(pool)` and the store's `source` resolve to the same `*sql.DB` |
| `Sibling`: a second `*Store` over the first's pool **and schema** | `:51-54` ✔ |
| `Fail`: seven outcomes, none fabricated | `:233-263` ✔ — verified real for `Unconfirmed` by mutation A |
| `Tail` deliberately absent (D4) | absent ✔ |
| `Window: 30s` | `:63` ✔ |
| six mutations, one per named section | `mutation_integration_test.go:34-43` ✔ — exactly those six, those sections |
| subprocess runs the narrow configuration (S5 change 4) | `:19` ✔ |
| "the named section is among those that failed", printing the others (S5 change 5) | `:76-79`, `sectionsThatFailed` ✔ |
| the audit runs in three places (S5 change 3) | factory cleanup `:71,330-335`; own contended load `audit_integration_test.go:29-42`; the shared schema `:87-99` ✔ (but see GAP-P2-S5-2) |
| `MIGRATIONS.md`: profile rule, one-transaction property, poison-row remedy | all three present, `MIGRATIONS.md:3-14,18-23,63-83` ✔ |
| `check-event-kernel` in `checks.sh`, `all`, `scripts/vv`, `Makefile`, self-tested four ways | `checks.sh:333-359,372,384`; `vv:26,53`; `Makefile:8`; `checks_test.go:369-419` ✔ |
| module pages ×2 + index rows; FL-037 + 3 index entries + 11 file rows; UC-032 row; Roadmap §15 rewritten; D-126, D-127 + index rows; D-101 *See also* | all present and checked ✔ (`docs/ai/flows/Index.md:59,112,508-518`; `docs/ai/usecases/Index.md:107,142`; `docs/ai/decisions/Index.md:181,182`; `D-101…md:94`; `Roadmap.md:323-375`, with all three phase-1 debts disposed of by name) |
| eleven non-test source files | eleven, and FL-037's file table names all eleven with their symbols ✔ |

Code → plan: nothing in the three S5 test files exports a symbol or adds a public surface;
`docs/api/surface.md` gains only the declared set. No drift found in this direction except the one
recorded as GAP-P2-S5-2.

### PostgreSQL correctness, [[D-118]], error hygiene — re-read at this round, nothing new

- **Admission serialises in the database, not in Go.** `append.go:111-127` is one statement: the CTE
  is a conditional row update (`ON CONFLICT … DO UPDATE … WHERE s.version = $3`), the outer `INSERT`
  selects from it, and a lost race is `RowsAffected() == 0` rather than a `23505`. No stream version
  is read into Go anywhere. `ORDER BY record.ord` is what makes one batch ascend in caller order.
- **Every statement is in one transaction or on one checked-out connection.** `onExecutor`
  (`executor.go:84-98`) is the only seam; a bound `*sql.Tx` is used as it stands, otherwise a
  `*sql.Conn` this call checked out. Nothing ever calls a statement method on the `*sql.DB` (the
  three-retry path). `migration.go` is the only file naming `BeginTx`/`Commit`/`Rollback`, and it is
  outside the eight store methods.
- **A rollback leaves no fragment.** One statement means the version advance and the rows are one
  atomic unit at every isolation level; the positions it burnt stay burnt, which is what the
  watermark exists for.
- **Serialisation failures are classified, not surfaced raw.** `classify.go:69-79` maps
  `40001`/`40P01`/`55P03` to `errs.Retryable()`, everything recognised to `errs.Internal()`, and
  `backendSurvived` refuses class `08` and `57P01/02/03` as proof of anything.
- **[[D-118]] holds at all four doors.** `bound()` (`executor.go:50-60`) answers
  `crudsql.Transaction(held)`; an ambient executor of this store's data source that is **not** a
  transaction yields `errAmbientNotTransaction` and `opened()` refuses **before** a statement is
  built, at every operating door. `New` refuses a `Source` whose `crud.KeyOf` is not
  `SameDataSource` as `Spec.DB` (`config.go:96-98`). No path places an ambient executor on
  autocommit.
- **Error hygiene.** No returned error renders a key, a payload, a position or a cursor:
  `promised`/`outside` name the *column* and nothing else (`read.go:255-277`); `causeOf` wraps the
  driver error as a **cause**, and `event.Failure(...).Error()` renders the outcome only — pinned by
  `classifiedAs` (`main_integration_test.go:462-470`). Comparison is `errors.Is` against the
  exported sentinels throughout the S5 files (`audit_integration_test.go:133,145`); the one string
  comparison in the section is `strings.Contains(output, "eventtest: …: failed")` over a
  **subprocess's stdout**, which is not an error value and fails loudly if the wording moves.
- **No forbidden shortcut.** `grep -rn "t.Skip\|TODO\|FIXME\|nolint" event/eventpg/` → zero;
  `grep -rn "t.Parallel()" event/eventpg/` → zero (the cluster and the counting driver's arming
  state are process-global, so this matters); `gofmt -l event/eventpg/` silent.
- **Housekeeping is real.** After this whole review the cluster holds exactly one `eventpg%` schema
  (`eventpg_shared`, which each run drops and rebuilds); the ~45 run-unique `eventpg_c<hex>_N`
  conformance schemas are all dropped by their `t.Cleanup`.

### The naive contract this section still allows — the worst thing that compiles, runs, returns no error and is wrong

**A `Factory` hook that stops working downgrades a conformance section from `passed` to
`not certified`, and every gate in the repository stays green.** This is not a hypothetical; it is
GAP-P2-S5-1 below and it was driven. It is the S5-shaped instance of the phase-1 failure the
delivery policy names by name: the suite is green, the plan's prose says the store is certified, and
the clause that a PostgreSQL store can state and an in-memory one structurally cannot is no longer
being asked at all.

Two lesser ones, both scheduled rather than fixed: the shared-schema audit runs fifth of thirty-odd
tests rather than after them (GAP-P2-S5-2), and both module pages promise "PostgreSQL 14+" while
level 3 compares `pg_get_constraintdef` and `pg_proc.prosrc` text against Go literals rendered to
match what 17.9 returns (backlog `## P2` §62).

---

### GAP-P2-S5-1 [high][immediate] The conformance census that makes a green run mean anything is asserted only by a shell line in a plan document — a section downgraded from `passed` to `not certified` is silently green everywhere

- **Where:** `.agents/artifacts/plans/EVENTSOURCE_P2_PLAN.md:2036-2037` (the `grep -c 'eventtest:
  .*: passed' | grep -qx 19` arm, which exists nowhere in the repository);
  `event/eventpg/conformance_integration_test.go:24-35` (the two runs, which assert nothing about
  their own verdicts); `event/eventpg/mutation_integration_test.go:34-43,63-82`
  (`defects()` is a plain slice and nothing asserts its length); `Makefile`, `scripts/checks.sh`
  (no arm covers either); contrast `scripts/checks.sh:333-359` + `scripts/checks_test.go:369-419`,
  where the **other** obligation of exactly this kind, §INV-061, was made executable and
  self-tested with four cases.
- **What:** `eventtest.Run` calls `t.Error` for a section that **failed** and only `t.Log`s one that
  is **not certified** — the plan states this at `:2050-2055` and it is why the census arm was
  written. But the census arm lives in a markdown checkpoint block, was run by hand once, and is not
  reachable from `go test`, `make unit`, `make check` or any CI entry point. The same is true of the
  mutation inventory: `TestTheConformanceSuiteCatchesADefectiveStore` ranges over `defects()`, so a
  `defects()` returning fewer entries — or none — runs its unmutated control, loops zero times and
  reports `PASS`. The checkpoint counts six **test names**, not six mutations. And the census arm,
  even when run, covers only `TestTheStoreSatisfiesTheContract`; the narrow-limits run's verdicts
  are never counted by anything.
- **Why this severity — driven, not argued.** I replaced the `event.Unconfirmed` arm of
  `failing.wayTo` (`conformance_integration_test.go:257-258`) with `return nil`, which is exactly
  what a future regression looks like: the counting driver's `init` moving, `arm` ceasing to fire,
  `storeOver` over `countingDriverName` failing `Prepare`, or someone "simplifying" the hook.
  Measured:

  ```
  eventtest: store failure classification: not certified — [outcome unconfirmed] cannot be produced by this store
  --- PASS: TestTheStoreSatisfiesTheContract (1.34s)
  ok  	github.com/frostgrove/vv/event/eventpg	1.338s
  ```

  and then the **whole tagged suite**, with the same edit in place:
  `ok github.com/frostgrove/vv/event/eventpg 28.323s` — green, against `28.872s` green for the
  restored tree. `probe.unable` downgrades the **entire** section, not the clause, so all eight
  cases through both the store and the wrapping decorator stop running; §INV-062's evidence row
  ("`store failure classification` through the wrapping decorator") becomes a claim about a section
  nobody executed; and the one clause where a PostgreSQL store has something an in-memory one
  structurally cannot say is silently surrendered. `durability`, `shared backing`, `transactions`
  and `resumption` are downgradable the same way — every one of them reads a `Factory` hook, and
  every one of them fails **soft**. This is the shape the delivery policy names as the phase-1
  failure ("29 of 32 review verdicts were red while the suite passed the whole time"), reproduced
  inside the section whose entire job is to stop it.
- **Why this timing:** S5 *is* the evidence, and the plan itself pre-declares that any count other
  than nineteen is a `[high]` finding — so the count is load-bearing by the section's own statement.
  The asymmetry with §INV-061, made executable in the same section, is the argument: an obligation
  that "everybody remembers" has already stopped holding in this repository (`CLAUDE.md`, on
  `check-triplets`). The mechanism costs nothing to build because S5 already built it:
  `runs(t, pattern, environment)` (`mutation_integration_test.go:115-128`) runs this binary as a
  `-test.v` subprocess and returns its combined output; a census test is that call plus a count.
  Doing it after phase 3 means re-opening a section reported done and re-running a 65 s live gate to
  find out whether the census still held while nobody was counting.
- **Close criteria:**
  - [x] A test in `event/eventpg` asserts the verdict census of `TestTheStoreSatisfiesTheContract`:
        exactly nineteen lines matching `eventtest: <section>: passed`, exactly one
        `not certified`, and that the one is `monotone visibility`. Not a `grep` in a plan file.
  - [x] The same census is asserted for `TestTheStoreSatisfiesTheContractAtNarrowerLimits`.
  - [x] The census test is shown non-vacuous: with one `Factory` hook forced to answer `false`
        (`wayTo`'s `event.Unconfirmed` arm returning `nil` is the exact edit measured above) the
        census test goes **red** while the rest of the suite stays green, and the transcript of that
        failure goes in the plan's mutation table beside the ten already there.
  - [x] `TestTheConformanceSuiteCatchesADefectiveStore` asserts how many mutations it ran, so an
        emptied or shortened `defects()` is red rather than invisible.
  - [x] The plan's checkpoint block cites the tests rather than the `grep -qx 19` shell line, and
        §S5's "Any other count is a `[high]` finding reported in words" names what now reports it.
  - [x] `FROSTGROVE_EVENTPG_TEST_DSN=… go test -race -count=1 -tags=integration ./event/eventpg/`
        green twice in a row afterwards; `make check` and `go test -count=1 ./scripts/` green.
- **Status:** closed — 2026-09-08

#### How it was closed

`event/eventpg/census_integration_test.go` is new; `mutation_integration_test.go`
and `conformance_integration_test.go` changed. Nothing under `event/` outside
`event/eventpg` was touched — `check-event-kernel: ok`,
`git status --porcelain event/` is `?? event/eventpg/` and nothing else.

**The census.** `census()` is a twenty-row table of section → verdict, in the order
`eventtest`'s inventory dispatches them: nineteen `passed` and
`monotone visibility: not certified — this store does not promise monotone
visibility`, reason included.
`TestBothConformanceRunsCertifyEverySectionButTheOneThisStoreDeclines` drives
**both** runs as `-test.v` subprocesses of this binary through the `runs` helper
the harness already had, parses their verdict lines back out with `verdictIn`, and
asserts the table. A downgraded section, a section reported twice, a section that
reported nothing at all because a hook left through `t.Fatal`, and a suite that
grew or lost a section are all red. There is no `Missing`/`SectionNames` seam
reachable from here — `eventtest`'s is in `export_test.go` — so the twenty names
are eventpg's own written-down expectation, which is also what makes an upstream
section change a decision somebody has to take rather than a silent one.

**The census's own falsification.**
`TestADowngradedSectionIsCaughtByTheCensusAndByNothingElse` withdraws three hooks
the factory really supplies, selected by `EVENTPG_DOWNGRADE`, and asserts of each
that the subprocess exits **0** and the census is **red** — a row that turned the
run red would prove nothing about the census, so that is a failure too:

| Row | Section it takes down | The mechanism |
|---|---|---|
| `no-fail-hook` | `store failure classification` | `needsFailHook` — the `needs` gate before the section runs |
| `no-unconfirmed-failure` | `store failure classification` | `Fail` answering `false` for `Unconfirmed`, which is `wayTo` returning `nil`; `probe.unable` inside the section |
| `no-unparsable-cursor` | `resumption` | `Factory.Unparsable` absent; `probe.unable` inside the section |

**Both inventories are counted.** `sized` is the `eventtest`-style size assertion
with the one-row-shorter control: `defects()` is pinned at six and `downgrades()`
at three, and `TestTheConformanceSuiteCatchesADefectiveStore` also counts the
mutations it actually drove and caught (`t.Run`'s own return), so a loop that ran
zero times is red twice over. `sectionsThatFailed` now reads `censusOf` rather than
carrying a second parser of the same format.

**Driven, in this worktree, against PostgreSQL 17.9 — every file restored
byte-identical afterwards (`md5sum -c`):**

| Mutation | Before | After |
|---|---|---|
| `wayTo`'s `event.Unconfirmed` arm → `return nil` | 18 `passed`, `store failure classification: not certified`, `--- PASS`, `ok … 1.352s`; whole tagged suite **green** | `--- FAIL` on **both** census subtests, naming the section and both verdicts; `FAIL … 76.964s`, and it is the **only** test in the tagged suite that reports it |
| `defects()` → `return nil` | `--- PASS: TestTheConformanceSuiteCatchesADefectiveStore (1.44s)`, zero mutations driven | `--- FAIL … this harness holds 0 store defects where it names 6` (0.00s) |
| `certifies` → `return nil` | — | `--- FAIL` on all three `EVENTPG_DOWNGRADE` rows, each naming the section and the reason its run reported |
| one row deleted from `census()` | — | `--- FAIL` on both census subtests — *a run reported 20 sections where this store is certified on 19* |

**Gates, after.** The checkpoint block ran `CHECKPOINT EXIT=0`: the six-name list
arm `6`, `check-event-kernel: ok`, the tagged suite `-race` **green twice in a
row** at `78.521s` and `78.832s`, `make unit`, `make vet`, `gofmt -l .` silent,
`make check` all ten arms including `./event/eventpg: 0 external packages`,
`go test -count=1 ./scripts/` `ok … 5.705s`, and `make api` leaving
`docs/api/surface.md` at the same `109 insertions(+), 2 deletions(-)` — the census
is a test file and exports nothing. `go build ./...`, `go vet ./event/...` and
`go test -race -count=1 ./event/...` are clean.

**One thing this closure also did, said out loud rather than left implicit.**
`sectionsThatFailed` was rewritten onto the shared parser rather than kept beside
a second one, which incidentally removes the negative-slice-index panic recorded
as backlog `## P2` §63 `[low]`. The backlog entry is **left untouched** per the
delivery policy; whoever works it will find it already gone and should strike it
then.

---

### Medium and low — scheduled, not forgiven

Written to [`EVENTSOURCE_BACKLOG.md`](EVENTSOURCE_BACKLOG.md) `## P2` §61–§65 and **left alone**,
per the 2026-09-08 delivery policy:

| Entry | Severity | One line |
|---|---|---|
| §61 | medium | the shared-schema audit runs fifth of the tagged suite, not after it, and its own guard cannot fail — the plan says "every other case of the live suite wrote to" and one file of five had |
| §62 | medium | both module pages promise "PostgreSQL 14+" and only 17.9 was measured, while level 3 compares catalog text against Go literals |
| §63 | low | `sectionsThatFailed` can panic on a negative slice index for a line whose `: failed` precedes its `eventtest: ` |
| §64 | low | the `Conflict` hook is honest only because the suite injects at the stream's current version; `Expected+1` at a stale one would **succeed** |
| §65 | low | the `failing` wrapper overrides three methods, so no mutation of `Capabilities`, `Limits`, `Backing`, `Close` or `Transaction` can be expressed by the harness |

---

### What is clean, with the numbers

- **Zero diffs under `event/`**, verified three ways (status, diff-with-exclusion, diff-without-exclusion).
- **`eventtest` runs unchanged**, twice, 19/1 at both configurations, `-race`, green twice in a row
  at 65.053 s and 65.329 s and once more at 65.189 s after every mutation was restored.
- **The `Unconfirmed` hook is real** — proved by breaking `outcomeOf` and watching the section fail.
- **The audit's three queries are non-vacuous** — each of the three planted-violation subtests
  asserts findings are non-empty, and all five subtests pass; the conformance factory's cleanup
  audit demonstrably runs, because a schema already dropped would make `rowCount` fail `3F000`.
- **The `check-event-kernel` arm refuses rather than reporting ok** when git is absent, when this is
  not a work tree, and when the baseline does not resolve; all four self-test cases pass, and the
  third alone (which a gutted `echo ok` would pass) is never run alone.
- **`check-deps` still reports `./event/eventpg: 0 external packages`** — pgx is test-only.
- **The module checklist is complete**: `go.mod`, `go.sum`, the `go.work` line, no `replace` in
  `test/`or `_examples/` (deliberate, and `check-tidy` is what would go red), the `scripts/event_test.go`
  charge row, both module pages with the driver, foreign-writer, xid and drain rows, both index rows,
  FL-037 with eleven file rows and eleven reverse-index rows, the UC-032 row, `MIGRATIONS.md`,
  Roadmap §15 rewritten with all three phase-1 debts disposed of by name, D-126 and D-127 with their
  index rows and D-101's *See also* updated, and a regenerated surface baseline whose diff reads
  exactly as the plan describes it.
