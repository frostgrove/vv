# EVENTSOURCE_P2 — implementation S3 (`event/eventpg`: the transaction question, the append, the classification) — GAPS

## Round 1 — econv-implementation-reviewer (clean context) — 2026-09-08

Reviewed against the **code**, not the plan's prose and not anyone's summary. Read in full:
[`EVENTSOURCE_P2_PLAN.md`](../plans/EVENTSOURCE_P2_PLAN.md) (§What this plan delivers, §What was
measured live, D1–D7, §Coverage matrix, §Contracts before code for `executor.go` / `classify.go` /
`append.go`, §Sections preamble, §S3 in full, §The zero-diff proof),
[`EVENTSOURCE_P2_USECASES.md`](../usecases/EVENTSOURCE_P2_USECASES.md) (UC-075…UC-086, UC-095,
INV-046…INV-053, INV-060, INV-062, INV-064),
[`EVENTSOURCE_BACKLOG.md`](EVENTSOURCE_BACKLOG.md) `## P2` §1–§46,
`event/eventpg/{executor,classify,append,config,schema,verify,migration}.go`,
`event/eventpg/{main,append,transaction,uncertainty}_integration_test.go`,
`event/eventpg/sources_test.go`, the frozen kernel `event/{store,errors,outcome,authority,repo,
text,bounds}.go`, `event/eventmemory/append.go`, `crud/executor.go`,
`crud/adapter/crudsql/crudsql.go`, `crud/sqlfault/extract.go`, `errs/sqlerr/{classify,postgres}.go`,
`CLAUDE.md`.

Every number, transcript and mutation below was produced in this worktree against the live
PostgreSQL 17.9 at `postgres://vv:vv@localhost:55432/vv`. Five source mutations and one throwaway
probe test were applied and **everything was restored**: the three S3 files are byte-identical
(md5 `d2dff8817ecb92152aa1769dbd405a36` `append.go`, `bc15ad804e7fc509d06a9bff8a500007`
`classify.go`, `cc5d6f6942619806162c665c01dd2bd0` `executor.go`, verified before and after), the
probe file is deleted, and `git status --porcelain event/` is `?? event/eventpg/` and nothing else.

---

### The zero-diff obligation — holds

| Command | Result here |
|---|---|
| `git status --porcelain -- event/ ':(exclude)event/eventpg'` | empty |
| `git diff --stat c798fc0b -- event/ ':!event/eventpg'` | empty |
| `git status --porcelain event/` | `?? event/eventpg/` and nothing else |

### Checkpoint verification — the pasted transcript is real

The S3 phase-5 block was run here verbatim, in a shell where the variable is not exported.

| Clause | Result here |
|---|---|
| `go build ./event/eventpg/...`, `go vet -tags=integration ./event/eventpg/...` | exit 0, exit 0 |
| `FROSTGROVE_EVENTPG_TEST_DSN=x go test -tags=integration -list '.' \| grep -q '^Test'` | 40 names printed |
| `test "$(… -list '^(…sixteen names…)$' \| grep -c '^Test')" = 16` | **16** — the block as written passes |
| `go test -race -count=1 -tags=integration ./event/eventpg/` twice in a row | `ok … 43.708s`, `ok … 43.942s` |
| `go test -race -count=1 ./event/eventpg/` (untagged) | `ok … 36.242s` |
| `CHECKPOINT EXIT` | **0** |
| live gate, DSN **unset** | `FAIL … 0.002s`, message names `FROSTGROVE_EVENTPG_TEST_DSN` and prints the command — it **fails, it does not skip** |
| live gate, DSN set to the measured DSN | runs and passes, three times over this review |
| `go build ./...`, `go vet ./event/...`, `gofmt -l event/eventpg/` | exit 0, exit 0, silent |
| `go test -race -count=1 ./event/...` | `event`, `eventmemory`, `eventtest` all `ok` |
| `make check` | `check-deps`, `check-tiers`, `check-utils`, `check-triplets`, `check-todo`, `check-replaces`, `check-tidy`, `check-otel-schema`, `check-workspace` all `ok`; `./event/eventpg: 0 external packages` |

One deviation, cosmetic and recorded as backlog `[low]`: the plan's own transcript
(§S3 "Run, 2026-09-08") shows the list arm at `= 15` while the command pasted above it asserts
`= 16`. Both are true of this tree — the sixteen-name pattern lists sixteen — so the transcript is
of a slightly different command than the one printed.

### Contract conformance — both directions, no drift

`grep` over the non-test sources: S3 adds exactly **two** exported symbols over S2's surface,
`(*Store).Append` and `(*Store).Transaction`, both with the signatures the plan declared
(`executor.go:19`, `append.go:29`). `executor`, `run`, `onExecutor`, `outcomeOf`, `causeOf`,
`payloadOf`, `appendStatement`, `recordRows`, `recordRow` and the three sentinels are unexported,
exactly as the plan says. `var _ event.Store` is absent, as the plan reserves it for S4
(`grep -rn "var _ event.Store" event/eventpg/` → no hits).

`Append`'s order is the fixed one the plan declares, step for step: `ctx.Err()` (30) → closed (33) →
ready (36) → `Transaction` (39) → empty batch (42) → `Expected > math.MaxInt64` (45) → build →
`onExecutor` → `Exec` → `RowsAffected` (59–82). The `RowsAffected` table is the plan's three rows and
no fourth (74–82). `outcomeOf`'s five rules are in the plan's order with **no default arm**
(`classify.go:27–39`). `causeOf` is the plan's `switch`, not `errs.StandardCodes()` (67–77). All six
of S3's declared contract changes are present in code, including change 3's `errStreamAhead`
(`append.go:21,45`), change 4's `payloadOf` (88–93) and change 6's row-count injection
(`main_integration_test.go:372–389`).

The empty-batch shortcut returning `nil` before `Expected` is examined matches `eventmemory`
(`event/eventmemory/append.go:38–40`) and the kernel's own "an empty append still costs no store
call at all" (`event/store.go:117`, `event/repo.go:60`), so the two implementations agree.

### PostgreSQL correctness — read, and driven

- **Admission is not a Go-side comparison.** The whole append is one statement
  (`append.go:123–139`): the CTE's `INSERT … ON CONFLICT (family, key) DO UPDATE SET version =
  s.version + $4 WHERE s.version = $3` is a predicate PostgreSQL evaluates against the row it has
  locked, and the outer `INSERT … SELECT … FROM admitted` writes nothing when the CTE returned no
  row. `TestNoStatementReadsTheStreamVersionIntoGo` walks the type-checked package for a
  `QueryContext`/`QueryRowContext` whose statement text names `streams` and finds none; the fixture
  beside it proves the walk reports one when it exists.
- **It serialises.** Eight goroutines at version 0 leave one winner and seven `ErrConflict`; the
  loser of a fresh-stream race takes the speculative-insertion path, blocks, fails
  `WHERE s.version = $3` against the winner's committed row and answers zero rows rather than a
  unique violation. Mutating the predicate to `WHERE s.version >= 0` made the unique index the only
  floor and turned seven conflicts into seven `ErrBackend` — the test caught it (below).
- **Every statement is one statement.** `TestAnAppendOnThePoolIsAtomicWithNothing/one append is one
  statement and no query at all` counts `execs == 1, queries == 0` on a `database/sql` driver that
  wraps pgx's, so "atomic with nothing" is measured, not argued.
- **Nothing is issued on the pool.** `grep -n "this\.db\." event/eventpg/*.go` over non-test files
  returns four hits and every one is `Conn(ctx)` (`executor.go:69`, `verify.go:48,79`,
  `migration.go:178`). `TestNoStatementIsIssuedOnTheDatabaseHandle` re-proves it through `go/types`
  rather than through names.
- **A rollback leaves no fragment.** Driven at both halves — no event row, no `streams` row for a
  stream created inside, no advanced version for one that existed — with the committed variant as
  its control, and with the burnt positions asserted to be never reissued.
- **Serialisation failures are classified, not surfaced raw.** The matrix runs the identical losing
  append at `READ COMMITTED` (→ `ErrConflict`), `REPEATABLE READ` and `SERIALIZABLE` (→ `ErrBackend`
  matching `crud.ErrUnavailable`, with `errs.AsFault(event.CauseOf(err)).Kind == KindRetryable`
  asserted). Each level is the other's control.
- **[[D-118]] holds and has one spelling.** `bound` (`executor.go:27–37`) is the only place the
  question is asked: `crud.ExecutorFor(ctx, this.source)` then `crudsql.Transaction(held)` — never a
  bare type assertion, so a savepoint reached through a decorator still answers its parent's
  `*sql.Tx` ([[D-061]]). Nothing found → the pool; a transaction → it is used as it stands and
  closed by nobody; anything else → `errAmbientNotTransaction` **before** a statement is built, at
  every door, with `execs == 0, queries == 0` counted across all five entries of
  `TestAnAmbientNonTransactionRefusesAtAllFourDoors`. There is no autocommit fallback anywhere in
  the file. `New` refuses a Spec whose Source is not the same data source as its DB
  (`config.go:94`).

### Metrics — counted, not eyeballed

| Metric | Value |
|---|---|
| `wc -l` `append.go` / `classify.go` / `executor.go` | 162 / 77 / 75 |
| longest function (`Append`, `append.go:29–83`) | 55 lines |
| max nesting depth in the three files | 2 |
| max parameters (`outcomeOf`) | 3 |
| exported symbols added by S3 | 2 |
| package-level mutable state in non-test files | 0 (three `error` sentinels, two `int` consts) |
| internal import fan-out | `append.go` → 1 (`event`), `executor.go` → 3, `classify.go` → 4 |
| import cycles | none — satellite module, one direction |
| kernel purity: `grep -rn eventpg event/*.go event/eventmemory/*.go event/eventtest/*.go` | **0 hits** |
| `go test -list '.'` names in the package | 40 |
| S3's own sixteen-name pattern | 16 |
| `TODO` / `FIXME` / `nolint` / `t.Skip` / `t.Parallel` in `event/eventpg` | 0 |

### Mutation campaign — what I broke, and what noticed

Each mutation applied alone, the whole tagged suite run, the file restored and md5-checked.

| Mutation | Result |
|---|---|
| admission predicate `WHERE s.version = $3` → `WHERE s.version >= 0` | **caught** — `TestEightWritersAtOneVersionLeaveOneWinnerAndSevenConflicts` (seven writers told `ErrBackend`), `TestAnAppendOnThePoolIsAtomicWithNothing/a second append at the stale token conflicts` |
| `outcomeOf`'s tail `return event.Unconfirmed` → `return event.NotWritten` | **caught** — six tests: `TestNoNotWrittenBranchIsUnguarded`, `TestAFailureInsideABoundTransactionIsNeverUnconfirmed`, `TestEachCancellationWindowIsAnsweredByTheRuleAndNotByOneOutcome`, `TestAKilledBackendAnswersUncertainAfterExactlyOneExecContext`, `TestTheRetryWithTheSameTokenResolvesTheUncertainty`, `TestEverySQLStateThisStoreNamesSelectsTheOutcomeTheRuleRequires` |
| `backendSurvived`'s session-termination arm `57P01/02/03` neutralised | **caught** — `TestAKilledBackendAnswersUncertainAfterExactlyOneExecContext` |
| `backendSurvived`'s connection-class arm `strings.HasPrefix(state, "08")` neutralised | **SURVIVED — whole suite `ok`** (GAP-P2-S3-1; caught after the fix) |
| `causeOf`'s retryable arm narrowed to `errs.CodeSerializationFailure` alone | **SURVIVED — whole suite `ok`** (GAP-P2-S3-1; caught after the fix) |

---

### GAP-P2-S3-1 [high][immediate] Two arms of the classification rule can be deleted with the whole suite green

- **Where:** `event/eventpg/classify.go:48–59` (`backendSurvived`, the `08` arm at line 53) and
  `event/eventpg/classify.go:67–77` (`causeOf`, the retryable arm at line 73); the tests that owe
  them are `event/eventpg/uncertainty_integration_test.go:300–437`
  (`TestEverySQLStateThisStoreNamesSelectsTheOutcomeTheRuleRequires`) and
  `transaction_integration_test.go:206–264`.
- **What:** `TestEverySQLState…` drives six observations — `23505`, `40001`, `57014`, a terminated
  backend, a failed checkout and an error with no SQLSTATE — and none of them reaches SQLSTATE class
  `08`, `40P01` or `55P03`. Measured, not read: replacing `strings.HasPrefix(fault.SQLState, "08")`
  with `strings.HasPrefix(fault.SQLState, "zz")` leaves the whole tagged suite `ok` (13.6 s), and so
  does narrowing `causeOf`'s `case errs.CodeSerializationFailure, errs.CodeDeadlock,
  errs.CodeLockTimeout, errs.CodeUnavailable:` to `case errs.CodeSerializationFailure:`. Both are
  named in the plan's own rule (§classify.go rules 4 and the retryability paragraph) and INV-064's
  **Falsified by** demands a classification test "that walks the whole rule".
- **Why this severity:** the severity table calls a missing test for a stated invariant `high`, and
  this is the invariant the store's whole uncertainty contract rests on. Concretely: an autocommit
  append that fails with `08007 transaction_resolution_unknown` — the SQLSTATE whose name *is* "I do
  not know whether it committed" — or with `08006 connection_failure` is `Unconfirmed` today only
  because of the arm at line 53. Delete or reorder that arm and the same input answers
  `NotWritten` → `ErrBackend`, the caller follows §UC-034 and re-appends at the same token, and the
  decision is written **twice** while every test in the section still prints `ok`. That is precisely
  the `sameValue` shape the delivery policy names: a load-bearing predicate that can be replaced by
  a constant with the suite green. The retryable arm is the milder half — a `40P01` deadlock or a
  `55P03` lock timeout would be answered `ErrBackend` **without** `crud.ErrUnavailable`, so a caller
  that reads retryability off the fault is told to give up on a failure the framework classifies as
  retryable — but it is unexercised by exactly the same hole.
- **Why this timing:** S5's coverage matrix claims INV-050, INV-053 and INV-064 are "Proved by"
  `TestEverySQLStateThisStoreNamesSelectsTheOutcomeTheRuleRequires`, and S5's mutation harness is
  advertised as showing the suite catches a broken store. Signing S3 off with these two arms
  unexercised makes that claim false at the moment it is written, and the fix belongs in S3's own
  test file — closing it after S5 means reopening a section already reported done. The mechanism is
  already built: `arm(t, 1, true, err)` takes any error, so a two-line test type carrying a
  `SQLState() string` method is the whole cost.
- **Close criteria:**
  - [x] `TestEverySQLStateThisStoreNamesSelectsTheOutcomeTheRuleRequires` grows a case that injects,
        through the counting driver on the autocommit path, an error carrying
        `SQLState() string == "08006"` and one carrying `"08007"`, and asserts `event.Unconfirmed`
        via `classifiedAs` plus what the stream holds.
  - [x] It grows a case for `40P01` and one for `55P03` (real or injected) asserting
        `errs.AsFault(event.CauseOf(err)).Kind == errs.KindRetryable` and `errors.Is(err,
        crud.ErrUnavailable)`.
  - [x] Neutralising the `"08"` prefix arm fails at least one named test, and narrowing `causeOf`'s
        switch to `errs.CodeSerializationFailure` fails at least one named test; both transcripts
        pasted into the plan's mutation table.
  - [x] `errs.CodeUnavailable` is either removed from `causeOf`'s switch or shown to be reachable —
        `errs/sqlerr/postgres.go:5–18` never produces it (see backlog `## P2` §49).
  - [x] `FROSTGROVE_EVENTPG_TEST_DSN=… go test -race -count=1 -tags=integration ./event/eventpg/`
        green twice in a row afterwards, and `go test -race -count=1 ./event/eventpg/` green.
- **Status:** closed 2026-09-08

#### How it was closed

Both defects were reproduced first, on this tree, before anything was written.

| Mutation, on the code as reviewed | Whole tagged suite |
|---|---|
| `strings.HasPrefix(fault.SQLState, "08")` → `"zz"` (`classify.go:53`) | `ok … 13.523s` — **survived** |
| `causeOf`'s arm narrowed to `case errs.CodeSerializationFailure:` (`classify.go:73`) | `ok … 13.537s` — **survived** |

`TestEverySQLStateThisStoreNamesSelectsTheOutcomeTheRuleRequires` grew four cases, and the same two
mutations now fail:

| Mutation, after the fix | Result |
|---|---|
| the `"08"` arm neutralised | `FAIL` — `an append answered 08006 reported "event: the store reported [outcome not written]" rather than a failure the store classified [outcome unconfirmed]`, and the same line for `08007` |
| `causeOf` narrowed to `CodeSerializationFailure` | `FAIL` — `a 55P03 answered event: the store failed, where the code says the failure clears on its own and the caller may try the same append again`, and the same line for `40P01` |

- **`08006` / `08007`** — injected through the counting driver on the autocommit path with
  `send = true`, so the statement is written and only its answer is lost. Each asserts
  `event.Unconfirmed` through `classifiedAs` **and** that the stream holds the two rows, which is
  what a store that guessed `NotWritten` would contradict. Injected and not driven: pgx reports a
  connection that broke under it as a Go error with no SQLSTATE, so no live arrangement makes a
  server name a class-`08` code and then stop talking. The test error is two methods — `Error()` and
  `SQLState() string` — and reaches the rule through `sqlfault.Extract` exactly as a driver's does.
- **`55P03`** — **real**: `SET lock_timeout = '400ms'` on the store's own single-connection pool,
  the stream row held by another session, the append blocked on it. 0.42 s.
- **`40P01`** — **real**, and a genuine cycle rather than an injection: another session inserts the
  event row the append is about to write and leaves it uncommitted, the append takes the `streams`
  row and then blocks on that uncommitted event row, and 300 ms later the other session asks for the
  `streams` row the append is holding. The append entered the wait first, so its own
  `deadlock_timeout` is the one that expires with the cycle complete and it is the statement
  PostgreSQL aborts. 1.02 s, which is the 1 s `deadlock_timeout` — the wait is the evidence that the
  server, not the test, decided this.
- Both new retryable cases assert **both** spellings the framework has for retryability —
  `errors.Is(err, crud.ErrUnavailable)` and `errs.AsFault(event.CauseOf(err)).Kind ==
  errs.KindRetryable` — because a store that answered one and not the other tells half its callers
  to give up on a failure that clears.
- **`errs.CodeUnavailable` is removed** from `causeOf`'s switch. `errs/sqlerr/postgres.go` maps
  thirteen SQLSTATEs and none to it, so for `postgresDialect` the arm was an arm no case can walk —
  the same defect as the two above, one step further along. The comment now says so. This also
  closes backlog `## P2` §49.

**Gate after the fix**, DSN `postgres://vv:vv@localhost:55432/vv?sslmode=disable`: the fifteen-name
list arm counts 15; `go test -race -count=1 -tags=integration ./event/eventpg/` `ok 45.272s` then
`ok 44.953s`; `go test -race -count=1 ./event/eventpg/` `ok 36.263s`; `gofmt -l .` silent;
`go build ./...`, `go vet ./event/...` exit 0; `go test -race -count=1 ./event/...` `ok` for `event`,
`eventmemory`, `eventtest`; `make check` all nine checks `ok` with `./event/eventpg: 0 external
packages`. `git status --porcelain -- event/ ':(exclude)event/eventpg'` empty.

---

### What is clean, and what I could not break

- The zero-diff obligation, the live gate (fails on an unset DSN, passes on a set one), and the
  pasted checkpoint all reproduce here exactly.
- The two most important tests of the section were attacked at the implementation they cover and
  both failed as they should: admission (`TestEightWriters…`, `TestAnAppendOnThePool…`) and the
  classification tail (six tests). The `joined`-is-proof and session-termination arms are covered
  too.
- No SQL text, no payload byte and no identity reaches a returned error: `refusal.Error()` renders
  the kernel sentinel only (`event/errors.go:98`), the only store-built message that names anything
  is the row-count mismatch (two integers) and the checkout failure (the schema name), and
  `TestAKilledBackendAnswersUncertain…` asserts the driver's text is absent from the rendered
  refusal while `event.CauseOf` still reaches it. Comparisons are `errors.Is` against exported
  sentinels throughout; nothing compares an error by string.
- No goroutine, no process-wide logger, no environment read, no ORM or driver type crosses the
  store's boundary (`check-deps`: `./event/eventpg: 0 external packages`).
- Six `[medium]`/`[low]` findings are recorded in
  [`EVENTSOURCE_BACKLOG.md`](EVENTSOURCE_BACKLOG.md) `## P2` §47–§52 and left alone under the
  delivery policy of 2026-09-08.
