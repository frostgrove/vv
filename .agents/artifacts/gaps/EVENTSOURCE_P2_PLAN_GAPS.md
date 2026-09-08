# EVENTSOURCE — phase 2 (`event/eventpg`) plan — GAPS

## Round 1 — plan auditor (coverage / checkpoints / mechanism reuse / structural checks) — 2026-09-08

Audited: [`EVENTSOURCE_P2_PLAN.md`](../plans/EVENTSOURCE_P2_PLAN.md) against
[`EVENTSOURCE_P2_USECASES.md`](../usecases/EVENTSOURCE_P2_USECASES.md), the frozen phase-1 code
(`event/store.go`, `event/errors.go`, `event/outcome.go`, `event/reader.go`, `event/repo.go`,
`event/backing.go`, `event/authority.go`, `event/bounds.go`, `event/eventmemory/*`,
`event/eventtest/*`), `jobs/jobspg/` (`config.go`, `repo.go:236`, `retention_migration.go:115`),
`crud/executor.go`, `crud/adapter/crudsql/crudsql.go`, `crud/sqlfault`, `errs/sqlerr`, `health`,
`scripts/checks.sh`, `scripts/common.sh`, `scripts/checks_test.go`, `scripts/event_test.go`,
`scripts/extensions_test.go`, `scripts/extensionlisting_test.go`, `scripts/modules.sh`, `Makefile`,
`go.work`, `docs/roadmaps/Roadmap.md` §15, `docs/ai/decisions/D-118`.

**What was verified and is clean, stated so a later round does not re-derive it.**

- **The zero-diff baseline is real.** `git rev-parse --verify c798fc0b28b270ec0368a918810ff3d7c17e6f8a^{commit}`
  resolves, it is an ancestor of HEAD, and both
  `git diff --stat c798fc0b… -- event/ ':(exclude)event/eventpg'` and
  `git status --porcelain` over the same pathspec are **empty right now**. The arm is green before
  the first line is written, exactly as the plan claims.
- **Every constructor the eight methods need exists outside `event/`.** `event.Failure` over the
  seven outcomes, `event.NewBacking` (`Backing.Equal` really is `crud.SameDataSource`,
  `event/backing.go:23`, and `SameDataSource` really is type-equality plus `==`,
  `crud/executor.go:562`), `event.NewAuthority`, the ceilings, `ResidentPage`. A
  `struct{db *sql.DB; schema string}` is comparable, so two values over one db+schema compare
  `Equal` and two schemas do not. **No signature in the plan forces a kernel edit.**
- **The structural checks survive the proposed file list.** Walked against `scripts/checks.sh`:
  `check-deps` lists the root with `./...`, which stops at a nested `go.mod`, so a pgx require in
  `event/eventpg/go.mod` is invisible to it; `check-tiers` names neither `event` nor `eventpg`;
  `check-utils` only walks `utils/`; `check-triplets` has no event set and eventpg is no HTTP
  binding; `check-todo` is unaffected; `check-replaces` passes given no replace of the library;
  `check-tidy` runs `tidy_diff` with a temporary local replace, which resolves; `check-workspace`
  needs the `./event/eventpg` line the plan's checklist row 3 already names. **None of the seven
  would fail on the proposed layout.**
- **The extension-cost row is measured, not guessed.** `go list -deps ./crud/adapter/crudsql ./event`
  is exactly `crud`, `crud/adapter/crudsql`, `crud/catalog`, `crud/sqlfault`, `errs`, `errs/sqlerr`,
  `event`, `utils` — the closure the plan's checklist row 6 claims, and it does not contain `health`,
  `port` or `runtime`. `packagesUnder` reaches nested modules (`extensionlisting_test.go:49-54`
  names `event/eventpg` by anticipation, verified), and `firstPartyDependenciesIn` asks
  `go list -deps <pkg>` without `-test`, so a pgx import confined to `_test.go` files is invisible
  to it while a production import would be reported.
- **The `go/types` source check is buildable across a module boundary.** Driven live: a throwaway
  `types.Config{Importer: importer.ForCompiler(fset, "source", nil)}` type-checked all 19 non-test
  files of `jobs/jobspg` (15327 typed expressions) with stdlib only. `TestEveryBoundParameterIsATypeEveryDatabaseSQLDriverAccepts`
  needs no second dependency. `event/sources_test.go` is the pattern.
- **`crudsql`'s savepoint really answers its parent's `*sql.Tx`** (`crud/adapter/crudsql/crudsql.go:232`),
  so §2.4's savepoint claim is true — which is why GAP-P2-6 below is about the missing test and not
  about the mechanism.
- **The migration lock is reused, not reinvented.** `jobspg.repository.withMigrationLock`
  (`retention_migration.go:115`) is a `*sql.Conn`, `pg_try_advisory_lock` with 250 ms + jitter,
  an unlock on a detached context, and `conn.Raw(… driver.ErrBadConn)` on a false unlock — line for
  line what §migration.go describes. `MigrationStatements` mirrors `jobspg/repo.go:236`. **The plan
  reinvents no jobspg machinery.**
- **`FL-037` is the next free flow number** and `D-126`/`D-127` the next free decisions.
- **Universality: no finding.** Nothing in the plan is fitted to one aggregate, no keyword list, no
  regex tuned to one layout, no fixed taxonomy. Every threshold is either configuration
  (`DefaultSchema`, the four `Default*` bounds, `Spec.MaxBatch/StreamPage/MaxRead`) or test-local
  (`Factory.Window`, D4's 50 ms poll, the migration backoff copied from `jobspg`).

**Seven blocking findings follow.** Medium and low go to
[`EVENTSOURCE_BACKLOG.md`](EVENTSOURCE_BACKLOG.md) `## P2` §14-§23 under the 2026-09-08 policy and
are left alone.

---

### GAP-P2-1 [high][immediate] `ReadAll` cannot answer `ErrAmbientNotTransaction`, so "all four doors" is unsatisfiable and the named test will be written weaker

- **Where:** plan §`event/eventpg/executor.go` (the third table row, "at all four doors"); §S3's
  `TestAnAmbientNonTransactionRefusesAtAllFourDoors`; spec §UC-086 ("**Then** `ErrAmbientNotTransaction`
  from every door") and §2.4 ("An ambient executor that is not a transaction refuses at all four
  doors, not only at `Append`").
- **What:** `event.ErrAmbientNotTransaction` is produced by exactly one function, `refuseTransaction`
  (`event/errors.go:181`), called from exactly three places: `Repo.Within`, `Repo.Authority` and
  `Repo.transaction` (`event/repo.go:115`, `:132`, `:147`). `ReadAll` reaches a consumer only through
  `Reader.Next` → `refuseRead(err)` → `refuse(err, readDoor)` (`event/errors.go:220-248`), whose
  entire map is the seven outcomes plus the two context sentinels. And `refusal.Is`
  (`event/errors.go:101-107`) answers **false** for any target that is in the kernel vocabulary and
  is not the refusal's own sentinel. So:
  - `ReadAll` returning `event.Failure(Refused, …)` renders `ErrRefused`;
  - `ReadAll` returning `event.Failure(Unclassified, …)` renders `ErrBackend`;
  - `ReadAll` returning the bare `event.ErrAmbientNotTransaction` falls to
    `readDoor.unclassified` → `backendRefusal` → a refusal whose sentinel is `ErrBackend` and
    whose `Is(ErrAmbientNotTransaction)` is false because that target is `inVocabulary`.

  There is no third route: `event.Read(log, cursor)` calls `admit(log)` and nothing else, and
  `*Repo` exposes no global-read method.
- **Failure scenario:** a consumer binds a `crudsql` non-transaction executor for the store's source
  (`crud.BindExecutor(ctx, source, crudsql.From(conn))`) and drains with
  `event.Read(store, "").Next(ctx)`. The plan promises `ErrAmbientNotTransaction` — "no fallback to
  autocommit, at any door" is the whole point of §UC-086's Must-not — and the caller's
  `if errors.Is(err, event.ErrAmbientNotTransaction)` branch never fires. Meanwhile the test named
  in S3 has to be written against *something*, and the something will be whichever sentinel the
  implementation happened to pick; the "all four doors" claim then holds by the test being adjusted
  to it rather than the other way round. That is the shape phase 1 shipped 29 red verdicts under.
- **Why it is not a wording nit:** the read door is the one door where the refusal actually matters
  operationally. A projector draining through a bound non-transaction gets `ErrBackend` — which the
  kernel documents as "the store failed", i.e. retry — for a wiring error that will never clear.
- **Close criterion:** the plan states, per door, the exact `event.Outcome` `ReadAll` selects for an
  ambient non-transaction **and the sentinel a consumer sees**, and `TestAnAmbientNonTransactionRefusesAtAllFourDoors`
  asserts `ErrAmbientNotTransaction` at `Transaction`/`ReadStream`/`Append` and that named sentinel
  at `ReadAll`, with the cause reachable through `event.CauseOf` asserted at all four. If the plan
  instead judges that a `Log` door with no wiring class is a genuine kernel gap, it says so out loud
  under the §INV-061 rule rather than widening the test.

---

### GAP-P2-2 [high][immediate] D1's `NotWritten` hook answers `Refused`, so phase 2's central evidence fails on its own factory

- **Where:** plan §D1, the `NotWritten` row; §`event/eventpg/append.go` ("Order, and it is fixed:
  `ctx.Err()` → closed → **ready** → `Transaction(ctx)` → … → `onExecutor`"); §`verify.go` ("Until it
  has returned nil the store answers `event.Failure(event.Refused, ErrNotReady)` from `Append`,
  `ReadStream` and `ReadAll`").
- **What:** D1 produces `NotWritten` by forwarding the next `Append` to "a real `*eventpg.Store`
  over a real `*sql.DB` opened on a DSN that resolves nowhere; the checkout fails and the store's
  own classifier selects `NotWritten` by proof 1". `Prepare` on that store can never return nil —
  it cannot reach a database to read `schema_meta`. The readiness gate is two steps before
  `onExecutor`, so the checkout is never attempted and the forwarded `Append` answers
  `event.Failure(Refused, ErrNotReady)`.
- **Failure scenario, exact:** `storeFailureSection` (`event/eventtest/sections_lifecycle.go:284`)
  drives `{outcome: NotWritten, door: "append", is: event.ErrBackend, isNot: event.ErrUncertain}`
  first in its table. `probe.classified` gets `event.ErrRefused`, `errors.Is(err, ErrBackend)` is
  false, and `this.refuse(…)` panics `abort{}` → the `store failure classification` section verdict
  is **`failed`** → `eventtest.Run` calls `t.Error`. `TestTheStoreSatisfiesTheContract` — the plan's
  named central evidence, S5's whole point — goes red on the factory, not on the store.
- **Close criterion:** D1's `NotWritten` row names a source that is reachable on a **prepared**
  store with nothing issued. The obvious one, and it is real rather than fabricated: a sibling
  `*eventpg.Store` prepared against the shared schema over a `*sql.DB` the test opened and then
  closed, so `db.Conn(ctx)` answers `sql.ErrConnDone` — proof 1, a real `database/sql` error,
  through the real classifier. The plan says which, and says the store is prepared before it is
  broken.

---

### GAP-P2-3 [high][immediate] D1 surrenders the whole `store failure classification` section on a premise S3 disproves, and INV-062's evidence row is wrong about what the suite does

- **Where:** plan §D1, the `Unconfirmed` row ("it cannot be produced deterministically inside a
  hook … the suite reports the clause *not certified*"); the coverage matrix's INV-062 row
  ("`store failure classification` through the wrapping decorator").
- **What, measured:** `probe.unable` appends to `probe.unmet` and `probe.verdict()`
  (`event/eventtest/report.go:56-64`) returns `notCertified` for the **section**, not for a clause.
  Driven live against the existing store: `go test -run TestTheMemoryStoreSatisfiesTheContract -v
  ./event/eventmemory/` prints
  `eventtest: store failure classification: not certified — [outcome unconfirmed] cannot be produced by this store`.
  Two consequences the plan does not carry:
  1. **The section is not certified for eventpg too**, so eventpg's conformance run certifies
     nineteen sections, exactly as eventmemory's does, on the one section where a PostgreSQL store
     has something eventmemory structurally cannot say. The plan's S5 prose lists only
     `monotone visibility` as not certified, and §UC-097 lists the certified sections as if this one
     were among them.
  2. `storeFailureSection`'s inner loop does `if !this.classified(ctx, injected) { break }` — the
     `break` exits the `through` loop, so the **wrapping-decorator variant of the `Unconfirmed` case
     never runs** — the one clause where a decorator forwarding an uncertain commit could lose the
     classification. The coverage matrix's INV-062 row names "`store failure classification` through
     the wrapping decorator" as its proof without saying that one of the eight cases is exempt from
     it and that the section's verdict is not `passed`.
- **And the premise is false for this store.** S3 already builds a `database/sql` driver that wraps
  pgx's and injects (`TestABadConnBeforeTheSendIsOneCallAndNotWritten` returns `driver.ErrBadConn`
  *before the send*). The same wrapper returning a **bare error with no SQLSTATE after the send** is
  classified `Unconfirmed` by the store's own rule 5 (§classify.go), deterministically, on the real
  classifier — precisely as "real" as D1's `NotWritten` hook is meant to be and as `BadCursor` is.
  D1 therefore gives up a section it can have.
- **Failure scenario:** the S5 report says "the suite passed"; a reviewer reading the verdict lines
  sees one section not certified and assumes it is the monotone-visibility one the plan warned
  about. Nobody notices that the store's own classification of a real commit-window failure — the
  one property §2.5 spends four pages on — was never driven through the conformance seam at all.
- **Close criterion:** either D1 drives `Unconfirmed` from the injecting driver and the section is
  reported `passed` (and S5's prose says so), or the plan states in words that
  `store failure classification` is reported **not certified** for `eventpg`, removes it from
  INV-062's evidence row, and names what proves INV-062 in its place. A gate never proceeds silently
  red, and a section silently downgraded to *not certified* is that.

---

### GAP-P2-4 [high][immediate] `go test -list` runs `TestMain`, so four of the five phase-5 checkpoints cannot execute

- **Where:** the "Every checkpoint names its tests and counts them before running them" rule; the
  `test "$(FROSTGROVE_EVENTPG_TEST_DSN=x go test -tags=integration -list '…' ./event/eventpg/ | grep -c '^Test')" = N`
  arm in S2, S3, S4 and S5; §S2's Files line placing "the `TestMain` refusal, the DSN, **the shared
  schema**, the scratch-schema harness, the counting driver's registration" in
  `main_integration_test.go`.
- **What, measured:** `go test -list` compiles and runs the test binary, and `TestMain` runs before
  `m.Run()` handles `-test.list`. Driven live on a throwaway module: a `TestMain` that exits 1 for a
  bad DSN under `-list` prints its own message and `FAIL`, and **no test names at all**. So
  `grep -c '^Test'` is `0`, the `= N` assertion fails, and the checkpoint aborts before a single
  real assertion has run.
- **Failure scenario:** the implementer runs the S3 checkpoint, sees `0 != 15`, and — under a
  delivery policy that says medium and low are deferred and core mechanics come first — deletes the
  count arm as "checkpoint noise". The count arm is the *only* thing standing between a `-run`
  pattern that matches nothing and a green report; the plan says so itself.
- **Close criterion:** the plan states that `TestMain` validates only the **presence** of
  `FROSTGROVE_EVENTPG_TEST_DSN`, performs **no I/O before `m.Run()`**, and defers the shared schema,
  the scratch-schema harness and the driver registration to a lazily-initialised helper the tests
  call; **or** the count arms are respelled to use the real DSN. Either way the plan adds one arm
  that proves the counting arm itself is not vacuous — the `-list` output is non-empty before it is
  counted.

---

### GAP-P2-5 [high][immediate] The `check-event-kernel` self-test cannot be written against the constant the plan specifies, so the arm joins the unfalsified checks it was written to avoid

- **Where:** §The zero-diff proof — "**The baseline** is a single named constant in
  `scripts/checks.sh`: `EVENT_KERNEL_BASELINE=c798fc0b28b270ec0368a918810ff3d7c17e6f8a`"; and the
  four self-test cases below it; §Debt's claim that "the new `check-event-kernel` arm is self-tested
  (four cases) so it does not join them [backlog P1 §6]".
- **What:** `scripts/checks_test.go:fixture` (verified) builds a `t.TempDir()`, copies `common.sh`
  and `checks.sh` **into it**, and `runCheck` executes the copy — so `REPO_ROOT` is the fixture, not
  this repository. A bare assignment travels into the fixture unchanged, and
  `c798fc0b…` does not exist in a fixture repository created by `git init` + one commit. Every
  fixture case therefore takes the arm's own "the baseline does not resolve → refuse" branch and
  exits 1 for the wrong reason.
- **Consequence:** three of the four specified cases are unconstructible — "a file under `event/`
  differing from the baseline → exit 1, naming the file", "**only** `event/eventpg` differing →
  exit 0", and "an **untracked** file under `event/` → exit 1". Only "a baseline that does not
  resolve → exit 1" can be written, and it is the one case that proves nothing about the diff. The
  arm then reports through an unfalsified body, which is backlog P1 §6 verbatim, in the file that
  the plan itself says "already has three of them".
- **Failure scenario:** somebody later widens the pathspec, or drops the `git status --porcelain`
  arm, or lets `git diff` compare against `HEAD` instead of the baseline. `make check` stays green,
  §INV-061 — phase 2's second framing obligation — is no longer measured, and the first person to
  notice is whoever reads `event/` a phase later and finds it changed.
- **Close criterion:** the constant is written overridable
  (`EVENT_KERNEL_BASELINE=${EVENT_KERNEL_BASELINE:-c798fc0b…}`), the self-test sets it per fixture,
  and the plan states that the exit-0 case ("only `event/eventpg` differs") must **fail on a gutted
  arm** — a `check_event_kernel` reduced to `echo ok` passes that case, so it needs the exit-1 cases
  beside it and the plan says which of the four is the control for which.

---

### GAP-P2-6 [high][immediate] The plan strikes the roadmap's savepoint debt with no evidence, and is silent on the first of the three debts §15 assigns to phase 2

- **Where:** checklist row 17 — "`docs/roadmaps/Roadmap.md` §15 **rewritten, not annotated done**;
  … the savepoint debt struck"; against `docs/roadmaps/Roadmap.md:342-346`, spec §6.12 and §2.4.
- **What:** Roadmap §15 names **three** debts inherited by phase 2: (a) "no decode-side depth or
  size bound beyond the byte cap (`eventpg` must add its own)", (b) the git-diff arm of the
  zero-diff check, (c) "the savepoint half of the authority rule, which is `crudsql` vocabulary the
  memory store has none of". The plan addresses (b) at length in §7.3. It addresses **neither (a)
  nor (c)** and instructs that (c) be deleted from the ledger.
  - `grep -i savepoint` over the whole plan returns nothing but checklist row 17. There is no
    savepoint test in S1, S2, S3, S4 or S5, and no savepoint row in the coverage matrix. Spec §6.12
    is a numbered live-gate item ("a receipt taken inside a savepoint and one taken outside compare
    `Same`; a savepoint rollback discards the events while the parent authority stays live; and a
    receipt from a different transaction on the same pool compares not-`Same`") and no section
    delivers it.
  - The mechanism is verified and the test is cheap: `crudsql.savepoint.Tx()` returns
    `this.parent.tx` (`crud/adapter/crudsql/crudsql.go:232`), and `Authority.Same` is
    `crud.SameDataSource` over that pointer plus backing equality (`event/authority.go:37`). Three
    assertions and one `crudsql.Tx.Begin`.
  - (a) is neither delivered, nor argued to belong to the kernel's codec rather than to the store,
    nor written to the backlog. It simply does not appear.
- **Failure scenario:** the roadmap line is deleted in S5. A consumer wraps an append in a savepoint,
  rolls the savepoint back, and keeps a receipt whose `Authority()` still compares `Same` with the
  live parent — the exact confusion §2.4 predicts — and the ledger says the question was settled.
- **Close criterion:** a named test in S3 or S4 that (i) a receipt taken inside a savepoint and one
  taken outside compare `Same`, (ii) a savepoint rollback discards the events while the parent
  authority stays live and a `ReadStream` on the parent no longer sees them, (iii) a receipt from a
  second transaction on the same pool compares not-`Same` as the control; the coverage matrix gains
  its row; and one line in the plan disposes of debt (a) — delivered, argued to the kernel by name,
  or written to the backlog with a severity.

---

### GAP-P2-7 [high][immediate] D4's `Tail` walks the whole log, and no shared-schema lifecycle is stated, so the live gate's cost grows with every run and expires naming the wrong cause

- **Where:** §D4 ("`Factory.Tail` walks from `""` to an empty page … On expiry it **`t.Fatalf`s**
  with the oldest running transaction it can name"); §S5's factory (`New:` "… **never migrates,
  never truncates**"); §S2's Files ("the shared schema").
- **What:** `Factory.Tail` exists precisely so a store whose backing outlives the run does **not**
  pay a full walk. Its own contract says so (`event/eventtest/suite.go`, the `Tail` field: "Supply
  it for a store whose log holds what this run did not write — a database nothing truncates between
  runs, where reading to the end costs the whole log once per walking section"). D4 implements it as
  the full walk the hook exists to avoid, and `probe.tail` is called by five sections
  (`sections_read.go:16,72,162,255`, `sections_ownership.go:24`, `sections_resumption.go:14`).
  Nothing in the plan says the shared schema is ever dropped, and the store's own append-only
  trigger makes `DELETE` and `TRUNCATE` impossible, so `DROP SCHEMA` is the only way it could be.
- **Failure scenario, concrete:** each suite run leaves permanent gaps in the shared log — the
  `transactions` and `lifecycle` sections roll back appends whose positions are burnt for good
  (§INV-009). Every subsequent run's `Tail` walk must **settle each of those gaps**: two round trips
  and one minted transaction id per gap (§read.go steps 7-8), plus one page per `MaxRead` rows of
  accumulated history, five times per run. After a few hundred CI runs the walk exceeds
  `Factory.Window / 2` and D4 `t.Fatalf`s with "the oldest running transaction it can name" from
  `pg_stat_activity where backend_xid is not null` — which on an otherwise idle cluster is **empty
  or irrelevant**. The gate goes hard red with a message that names a cause that is not the cause,
  on a suite that passed the day before with no code change. That is the failure shape phase 1 died
  of, arriving on a timer.
- **Close criterion:** one of, and the plan says which:
  - `TestMain` creates the run's schema fresh (a run-unique name, or `DROP SCHEMA … CASCADE` then
    `Migrate`) so the log the suite walks is the log the run wrote, and the plan states that the
    shared schema is per-run; **or**
  - `Tail` is O(1) plus a bounded wait instead of a walk — the test lives in `package eventpg`, so
    it can mint a bound with `SELECT pg_current_xact_id()`, read `max(position)`, wait for
    `pg_snapshot_xmin` to pass the bound, and return the cursor directly, which is the "walk **or a
    wait**" §2.6 non-goal 5 already names and is what the hook is for.

  Either way, D4's expiry message names the accumulated-gap case as well as the long-transaction
  case, and the plan states a bound on the round trips one conformance run costs.

---

## Verdict

Seven `[high]` findings are open. None is a design defect in the store: the schema, the append
statement, the classification rule and the watermark all hold up against the code and against the
measurements the plan records. All seven are places where the plan's **evidence** does not do the
work it claims — two make phase 2's central conformance run go red or silently uncertified
(GAP-P2-2, GAP-P2-3), one is unsatisfiable as specified (GAP-P2-1), two are anti-vacuity guards that
cannot run (GAP-P2-4, GAP-P2-5), one deletes a recorded debt without proving it and loses a second
(GAP-P2-6), and one is a gate that degrades into a red naming the wrong cause (GAP-P2-7).

Ten `[medium]`/`[low]` findings are recorded in
[`EVENTSOURCE_BACKLOG.md`](EVENTSOURCE_BACKLOG.md) `## P2` §14-§23 and are **left alone** under the
2026-09-08 policy.

---

## Round 2 — closure, 2026-09-08

All seven `[high]` findings are closed in
[`EVENTSOURCE_P2_PLAN.md`](../plans/EVENTSOURCE_P2_PLAN.md). Four of the seven turned on behaviour
of the toolchain or of `event/eventtest` that had been read rather than driven, so it was driven;
the transcripts are in the plan's new **What the round-1 gate measured** section. Three contracts
changed rather than three tests being widened, which is what the findings asked for.

| Finding | Closed by | Contract changed? |
|---|---|---|
| GAP-P2-1 | §executor.go gains a per-door table: `ErrAmbientNotTransaction` at `Within`/`Authority`, `Load` and `Append`; **`event.Failure(Refused, …)` → `ErrRefused` at `ReadAll`**, chosen because the two `ErrBackend` routes are the retry classes and a wiring error never clears. One cause at all four doors through `event.CauseOf`; the **Must not** measured with the statement counter at zero. §UC-086's **Then** amended, the kernel asymmetry reported out loud | yes — §UC-086's **Then** |
| GAP-P2-2 | D1's `NotWritten` row: a sibling **`Prepare`d against this store's own schema** over a second `*sql.DB` the factory then **closes**, so the readiness gate is satisfied and `onExecutor`'s checkout fails. Driven: `db.Conn` on a closed pool answers `sql: database is closed`, `database/sql`'s own `errDBClosed`, **not** `sql.ErrConnDone` as the finding guessed — either way `issued == false`, proof 1 | yes — D1 |
| GAP-P2-3 | D1's `Unconfirmed` row drives the injecting driver S3 already builds, returning a bare no-SQLSTATE error **after the send** → rule 5. The section is expected **`passed`**, all eight cases through both variants; S5's checkpoint asserts **nineteen** `passed` verdicts rather than reading twenty log lines | yes — D1 |
| GAP-P2-4 | `TestMain` validates **presence only** and performs no I/O before `m.Run()`; the schema, the harness and the pools move to a `sync.Once` the tests call, driver registration to an `init`. Every counting arm is preceded by `-list '.' \| grep -q '^Test'`, and the two unset-DSN arms use `env -u` | yes — S2's `TestMain` |
| GAP-P2-5 | `EVENT_KERNEL_BASELINE=${EVENT_KERNEL_BASELINE:-c798fc0b…}`; `runCheckWithEnv` added beside `runCheck`; the fixture becomes a real git repository and hands the arm its own sha. Four cases with a table saying which is the control for which — and that the exit-0 case is the one a gutted `echo ok` passes, so it is never run alone | no |
| GAP-P2-6 | `TestASavepointWritesUnderItsParentsAuthorityAndARollbackDiscardsThem` in S3 — `Same` inside, discard-on-rollback with the parent live, not-`Same` from a second transaction as the control — with rows in the UC-085 and INV-051 matrix lines and the S3 count raised 15 → 16. The decode-side bound is **argued to the kernel by name** (`eventpg` decodes nothing) and written to the backlog rather than delivered or dropped | yes — Roadmap §15's assignment of the decode-side bound |
| GAP-P2-7 | D4 rewritten: **no `Tail` hook at all**, because D1's `New` gives every store value its own migrated scratch schema, dropped at section cleanup. Nothing accumulates across runs, so the walk-to-the-end is one empty page. The round-trip bound is stated: ~45 schemas × ~20 round trips per run | yes — D1's `New`, D4 |

### One `[high]` the round-1 gate did not find, raised and recorded here

Closing GAP-P2-2 exposed a defect neither the plan nor round 1 had: **a factory whose `New` answers
a store over a backing an earlier `New`'s store already wrote to cannot pass
`store failure classification` at all**, whatever `Fail` returns. Driven against `eventmemory` over
one shared log:

```
eventtest: store failure classification: failed — [outcome not written] injected at the append door
through a decorator that wraps the store's own error left 2 events on a stream that held one, and
the store said the write certainly did not land
```

`event/eventtest` is frozen by §INV-061, so it is reported rather than patched:
**backlog `## P2` §25, `[high]`, owner the phase that unfreezes the kernel.** Phase 2's escape is
not a dodge — `durability` and `shared backing` read `Factory.Sibling`, not `New` — and it certifies
two clauses a shared backing loses. Backlog `## P2` §24 and §26 carry the other two dispositions.

**Nothing is left silently red.** `[medium]` and `[low]` §14–§23 are untouched, and the plan's Debt
names the two a `[high]` fix brushed: §18 stays **open** — §executor.go names *one* ambient-executor
value rather than which one, so the contract holds either way — and §19 falls out closed, because
S5's checkpoint now runs `./scripts/checks.sh event-kernel` instead of the weaker hand-rolled
`git diff --quiet` it was criticised for, which cost one word.
