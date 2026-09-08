# EVENTSOURCE_P2 — implementation S4 (`event/eventpg`: the read path, the watermark, the cursor) — GAPS

## Round 1 — econv-implementation-reviewer (clean context) — 2026-09-08

Reviewed against the **code**, not the plan's prose and not anyone's summary. Read in full:
[`EVENTSOURCE_P2_PLAN.md`](../plans/EVENTSOURCE_P2_PLAN.md) (§What this plan delivers, §What was
measured live, D1–D7, §Coverage matrix, §Contracts before code for `executor.go` / `classify.go` /
`append.go` / `read.go` / `cursor.go`, §Sections preamble, §S4 in full),
[`EVENTSOURCE_P2_USECASES.md`](../usecases/EVENTSOURCE_P2_USECASES.md) (UC-075, UC-087…UC-093,
UC-095, UC-098; INV-054…INV-056, INV-059, INV-062, INV-065),
[`EVENTSOURCE_BACKLOG.md`](EVENTSOURCE_BACKLOG.md) `## P2` §1–§54,
`event/eventpg/{read,cursor,executor,config,verify,classify,append}.go`,
`event/eventpg/{read,cursor,watermark,transaction,main}_integration_test.go`, the frozen kernel
`event/{store,reader,errors,outcome,repo,authority,backing}.go`, `CLAUDE.md`.

Every number, transcript and mutation below was produced in this worktree against the live
PostgreSQL 17.9 at `postgres://vv:vv@localhost:55432/vv`. Two source mutations and one throwaway
probe test were applied and **everything was restored**: `read.go`, `cursor.go` and `executor.go`
are byte-identical to the tree as reviewed (sha256 `b94af084…` `read.go`, `6b5c5f9e…` `cursor.go`,
`2424bae1…` `executor.go`, verified before and after), the probe file is deleted, and
`git status --porcelain event/` is `?? event/eventpg/` and nothing else.

---

### The zero-diff obligation — holds

| Command | Result here |
|---|---|
| `git status --porcelain event/` | `?? event/eventpg/` and nothing else |
| `git diff --stat HEAD -- event/ ':!event/eventpg'` | empty (HEAD `2199910`) |
| `git diff --stat c798fc0b -- event/ ':!event/eventpg'` | empty |
| `grep -rl eventpg --include='*.go' event/ \| grep -v '^event/eventpg/'` | 0 files — the kernel names the extension nowhere |
| `grep -rn jackc --include='*.go' event/eventpg/ \| grep -v _test.go` | 0 — the driver is test-only |

### Checkpoint verification — the pasted transcript is real

The S4 phase-4 and phase-5 blocks were run here verbatim, in a shell where the variable is not
exported.

| Clause | Result here |
|---|---|
| `go build ./event/eventpg/...` | exit 0 |
| `grep -q 'var _ event.Store = (\*Store)(nil)' event/eventpg/config.go` | present, `config.go:64` |
| `FROSTGROVE_EVENTPG_TEST_DSN=x go test -tags=integration -list '.' \| grep -q '^Test'` | names printed |
| `test "$(… -list '^(…twelve names…)$' \| grep -c '^Test')" = 12` | **12** — the block as written passes |
| `go test -race -count=1 -tags=integration ./event/eventpg/` twice in a row | `ok … 45.818s`, `ok … 45.646s` (the plan pasted 45.705 s / 45.833 s) |
| live gate, DSN **unset** | `FAIL … 0.002s`; the message names `FROSTGROVE_EVENTPG_TEST_DSN` and prints the command — it **fails, it does not skip** |
| live gate, DSN set to the measured DSN | runs and passes, four times over this review |
| `gofmt -l event/eventpg/` · `go vet ./event/...` | silent · exit 0 |
| `go test -race -count=1 ./event/...` (untagged, root module) | `event`, `eventmemory`, `eventtest` all `ok` |
| `make check` | `check-deps`, `check-tiers`, `check-utils`, `check-triplets`, `check-todo`, `check-replaces`, `check-tidy`, `check-otel-schema`, `check-workspace` all `ok`; `./event/eventpg: 0 external packages` |

### Contract conformance — both directions

`go doc -all ./event/eventpg` lists exactly the surface the plan declares and nothing else. S4 adds
three exported symbols over S3's surface — `(*Store).ReadStream`, `(*Store).ReadAll` and the
`var _ event.Store = (*Store)(nil)` assertion — with the declared signatures. `walk`, `mintCursor`,
`readCursor`, `possible`, `deliverable`, `reached`, `settledAt`, `spent`, `promised`, `outside`,
`number`, `streamStatement`, `logStatement`, `fetch`, `storedEvent` are all unexported; no cursor,
position or outcome type is exported (plan §cursor.go).

All five of S4's declared contract changes are in the code: `Store.opened` is one function in
`executor.go:33` and `Append` calls it (`append.go:27`); no `ctx.Err()` arm exists on either read
failure path; both read statements are package-level functions (`read.go:292,305`);
`ReadStream` answers an empty page above `math.MaxInt64` rather than refusing (`read.go:55`);
`kernelStore` is gone (`grep -rn kernelStore event/eventpg/` → 0) and
`TestAnAmbientNonTransactionRefusesAtAllFourDoors` drives the real fourth door through
`event.Read(store, "")` (`transaction_integration_test.go:523-533`).

Two places where the **plan** is wrong about the code it describes are findings below: §read.go
step 8 (GAP-P2-S4-1) and S4 change 2's "a branch no test can falsify" (backlog `## P2` §55).

### Metrics, counted

| Measure | Value |
|---|---|
| `read.go` / `cursor.go` / `executor.go` | 315 / 92 / 98 lines |
| longest function | `ReadAll` 46 lines, `fetch` 34, `ReadStream` 33, `readCursor` 25, `number` 21 |
| max nesting depth | 4 (`ReadAll`'s closure → `if count < len` → `if count == 0` → `if err`) |
| parameters, max | 4 (`deliverable`, `reached` — 3; `fetch` — 4 incl. ctx) |
| package-level mutable state in non-test files | 0 (`grep -n '^var ' *.go` → three `errors.New` sentinel blocks only) |
| exported symbols added by S4 | 3, all named in the plan |
| internal import fan-out of the package | `context`, `database/sql`, `database/sql/driver`, `errors`, `fmt`, `math`, `strconv`, `strings`, `sync/atomic`, `time` + `crud`, `crud/adapter/crudsql`, `crud/sqlfault`, `errs`, `errs/sqlerr`, `event` — 0 external packages (`make check` line) |
| statement methods called on `*sql.DB` in the eight store methods | 0 (`grep -n 'this.db\.' *.go` → `db.Conn` in `executor.go:92`, `verify.go:48,79`, `migration.go` only) |

### Isolation

`walk` is a value type with three `uint64` fields and three total methods; `cursor.go` has no
dependency on `Store` at all and could be moved to a package of its own unchanged. `read.go`'s
statement builders take `(schema string, page int)` and nothing else. The two read doors are
testable against a `*sql.Conn` and one schema — the live tests use two fakes at most (the counting
driver and a scratch schema).

### Universality

Every literal in the S4 code was traced. `cursorTag = "vve1"` is the format's own version and is
tested through the retired-tag case; `cursorBytes = logBytes + 3*8` is derived; `math.MaxInt64` and
`math.MaxInt32` are the `bigint` and `int` column widths; `strconv.ParseUint(…, 10, 64)` is `xid8`'s
width; `settled+1` is algebra, not tuning. `mintStatement` and `floorStatement` are PostgreSQL
catalogue functions, which is the dialect this module is named for. **No magic threshold, no
layout-tuned pattern, no fixture path under `src/`, no `if id == "…"`, no `len(rows) == N`
assumption.** The one number a reviewer would challenge — the page `LIMIT` — is `Limits().MaxRead`
and `Limits().StreamPage`, published by the same store.

---

### GAP-P2-S4-1 [critical][immediate] The second snapshot settles a fresh bound against rows read from an older snapshot, so a committed event is skipped for good

- **Where:** `event/eventpg/read.go:144-149` — the step-8 block inside `ReadAll`'s callback; the
  stale value is `fetched`, read at `read.go:133` before `mintStatement` (139) and
  `floorStatement` (145) were issued. The rule it re-applies is `walk.settledAt`
  (`read.go:220-225`). The doc comment that states the unsound rule is `read.go:116-119`, and the
  plan states the same rule at [`EVENTSOURCE_P2_PLAN.md:945-951`](../plans/EVENTSOURCE_P2_PLAN.md).
- **What:** the watermark is sound only because the floor and the rows come out of **one**
  statement, i.e. one snapshot (`logStatement`, `read.go:305-315`): a position at or below `reach`
  that is missing from a snapshot taken *after* `floor > bound` was observed must have been burnt,
  because anything committed before that snapshot is visible in it. Step 8 breaks that pairing. It
  mints a new `bound` (139), sets `reach` to the highest position of the **old** page (143), reads a
  **new** floor in a separate statement (145), and then re-runs `deliverable` over the **old**
  `fetched` slice (148). A transaction that had drawn a position at or below `reach` before the
  fetch, was still running when the fetch ran — so its row is absent from `fetched` — and committed
  before the floor statement satisfies `floor > bound`; the gap it left is declared settled, the row
  above it is delivered, and the cursor advances past a position that is committed and present in
  the table. Driven, not argued: a throwaway `database/sql` driver that commits the holding writer
  when it sees `floorStatement` (the same shape as `main_integration_test.go`'s counting driver)
  produced, on this tree, against the live 17.9 —

  ```
  REVIEW: first page positions=[2] payloads=[the high position]
  REVIEW: rest positions=[] payloads=[]
  REVIEW: the table holds positions [1 2]
  REVIEW: the walk delivered [the high position] and never the committed row at the low position
  ```

  — the walk delivered position 2, returned the cursor `(from:2, bound:0, reach:0)`, and position 1
  is committed, present, and unreachable from that cursor forever.
- **Why this severity:** this is silent, permanent event loss on the one path the section exists to
  make safe, and it is the exact failure §UC-089 and §INV-054 are written against
  ("a walk does not pass a position a writer could still commit"). A projector that drains the log
  loses an event and reports nothing; a read model rebuilt from that walk is wrong forever and
  cannot be repaired by replaying from the persisted cursor. The window is not exotic: it is the
  round trip of one `SELECT pg_current_xact_id()` under any concurrent writer, and step 8 is entered
  on **every** first encounter with a head gap — the normal shape of a polling projector when a
  writer holds the next position. `TestAWalkDoesNotPassAPositionAWriterCouldStillCommit` and
  `TestABurntGapIsPassedInsideOneReadAllAndALiveOneIsNot` both miss it because their in-flight
  writer never commits inside the mint→floor window and their burnt writer never commits at all.
- **Why this timing:** the store's whole claim over a naive `position > cursor` store is this
  watermark; shipping S4 with it means S5's conformance run, the module page and every downstream
  phase are written on top of a walk that loses events. The fix is small and does not change the
  round-trip budget the tests assert: re-issue `logStatement` (the fetch) instead of the bare
  `floorStatement`, so the second floor and the second row set are one snapshot again — still three
  queries for the head-gap case, which is what `watermark_integration_test.go:240` counts. The rows
  it returns are a superset of the old ones (positions only grow), so `reach` stays valid.
- **Close criteria:**
  - [x] Step 8 re-reads the page and the floor in **one** statement (or is removed), so no
        `deliverable` call ever mixes a floor with rows from a different snapshot; a source-level
        assertion or a comment naming the invariant is not enough on its own.
  - [x] A live test in `watermark_integration_test.go` reproduces the arrangement above — a writer
        holding the low position that commits **between** the mint and the second snapshot, through
        an injecting `database/sql` driver — and asserts the committed low position is delivered by
        that call or a later one. It must fail on the code as reviewed; the transcript of that
        failure goes in the plan's mutation table.
  - [x] The mutation table gains a row for "the second settle runs against the rows the first
        statement returned" with the test that now catches it.
  - [x] `ReadAll`'s doc comment step 8 (`read.go:116-119`) and the plan's §read.go step 8 both state
        that the rows and the floor of a settle are always one snapshot.
  - [x] The head-gap round-trip assertions (`watermark_integration_test.go:240,258`) still hold at
        their stated counts, or the plan says what they are now and why.
  - [x] `FROSTGROVE_EVENTPG_TEST_DSN=… go test -race -count=1 -tags=integration ./event/eventpg/`
        green twice in a row afterwards, and `go test -race -count=1 ./event/eventpg/` green.
- **Status:** closed 2026-09-08

#### How it was closed

**Reproduced first, on the tree as reviewed.** The window is opened by the counting driver rather
than by a throwaway one: `main_integration_test.go` grew an `interception` — a statement text and a
func that runs on the connection that issued it, before the statement reaches the server, so the
call cannot proceed past it. `interceptOnce(t, mintStatement, …)` commits the writer holding the low
position, which lands the commit exactly between the walk's two looks at the log.

| Mutation | Result |
|---|---|
| the code as reviewed (step 8 reads a bare floor and re-settles `fetched`) | `FAIL … the walk delivered [2] where the table holds [1 2], and a committed position a walk passed is never read by that consumer again` |
| after the fix | `ok` |
| the fix reverted to the bare floor, with the whole S4 suite | `FAIL` twice: the case above **and** `rows that arrive between the two looks are delivered and the cursor stays readable` — *the walk answered [after the burnt head] where its second look at the log holds both of those* |

**The fix** is the one the finding names: step 8 re-issues `logStatement` through `this.fetch`, so
the second floor and the second row set are one snapshot again. `floorStatement` is deleted — no
statement in the package reads a floor on its own any more, which is the property, not a comment.
`at.reach` still comes from the **first** page's highest position, because a row the second snapshot
adds may have been drawn after the mint. Three queries for the head-gap case as before: read, mint,
read.

**And a second defect the fix itself would have introduced, found by walking the consequences.** The
second look can return rows the first did not, so the walk can now deliver *past* the reach its bound
was minted for. Carried into the cursor, that is `(from > reach)` — a triple `walk.possible()`
refuses, so the very next call answers `BadCursor` and the consumer is dead for good. A bound the
walk has delivered past settles nothing above what the cursor already carries, so it is discharged:
`if count < len(fetched) && next.from < at.reach`. Driven, not argued — with that clause removed and
three rows arriving inside the window, `TestABurntGapIsPassedInsideOneReadAllAndALiveOneIsNot/rows
that arrive between the two looks…` reports *a walk of the log answered event: the store reported
[outcome bad cursor]*.

Both cases are subtests of the tests that already own their use cases (§UC-089 and §UC-090), so the
checkpoint's twelve-name count is unchanged. The round-trip assertions still read 2 and 3 and still
pass.

### GAP-P2-S4-2 [high][immediate] A walk on a context that carries a transaction of this backing never mints a bound, so after the first burnt position it stops delivering — silently, with no error, for good

- **Where:** `event/eventpg/read.go:138` (`&& !on.joined`), `read.go:220-225` (`settledAt`),
  `read.go:112-115` (the doc comment stating the rule);
  `event/eventpg/watermark_integration_test.go:273-333` pins one call of it as intended behaviour.
- **What:** inside a bound transaction the store never mints a bound, so `walk.bound` stays whatever
  the cursor carried. A consumer that always reads on a context carrying a transaction of this
  backing therefore never acquires a bound at all, `settledAt` returns `from` on every call, and the
  first gap the log ever holds — one rolled-back append anywhere in the system, which §INV-009 says
  is normal and the `transactions` and `lifecycle` conformance sections produce deliberately — stops
  the walk permanently. Every subsequent `ReadAll` answers an empty page and the same cursor.
  `Reader.Next` returns `(false, nil)`; the consumer reads "no new events" and there is no error, no
  log line and no capability to inspect. The store refuses an ambient executor that is **not** a
  transaction loudly (`ErrAmbientNotTransaction`, `executor.go:14`) and degrades silently for the
  ambient case that is one — the asymmetry is the trap.
- **Why this severity:** a context carrying a transaction is not something the consumer opts into
  here; in this framework it is inherited from whatever bound an executor upstream
  (`crud.BindExecutor`), so a projector drained from inside a transactional app-usecase gets it
  without writing a line about transactions. The outcome is a consumer that stops consuming, forever,
  with a green health check — "a shape that lets a caller silently do the wrong thing", which
  `CLAUDE.md` names as wrong even when it is simpler. The kernel's own warning
  (`event/reader.go:62-69`) is about the **cursor** being unsafe to persist, not about the walk
  ceasing to advance, so a consumer that reads the documentation still walks into it.
- **Why this timing:** it is a property of the read door S4 delivers, and it decides what S5's module
  page must say and what the `resumption` conformance clause means when the suite binds a
  transaction. Recording it after S5 means reopening a section reported done; and the cheap
  mechanism — mint the bound on a connection of the store's own pool rather than on the caller's
  transaction, which taints nothing and is what the `!on.joined` guard was protecting against —
  belongs beside the code it changes.
- **Close criteria:**
  - [ ] Either: a bound is minted on a connection the store checks out of its own pool when a
        transaction is bound, with a live test showing a walk inside a `READ COMMITTED` transaction
        passing a burnt gap and `pg_current_xact_id_if_assigned()` still null on the caller's
        transaction afterwards (the assertion `watermark_integration_test.go:293-300` already makes);
        — **not taken, and the two measurements below are why**
  - [x] or: the refusal to mint is kept and a live test pins the **permanence** — three successive
        `ReadAll` calls, each in its own bound transaction, over a log with one burnt position, all
        answering an empty page — so the behaviour is a test result rather than a side effect, and
        the plan states which of the two it chose and why.
  - [x] `docs/modules/{en,ru}/eventpg.md` (S5) states the consequence in the consumer's own words:
        a walk on a bound transaction does not advance past a gap, and a drain belongs outside the
        write. — the sentence is written out as row 11a of the plan's S5 documentation checklist, so
        S5 carries it rather than re-deriving it
  - [x] The `REPEATABLE READ` case is stated either way: the frozen snapshot cannot settle a bound
        minted after it, so that level cannot pass a gap at all. — stated in `ReadAll`'s step 7 and
        the plan's step 7, and the third of the three permanence walks runs at that level
  - [x] `FROSTGROVE_EVENTPG_TEST_DSN=… go test -race -count=1 -tags=integration ./event/eventpg/`
        green twice in a row afterwards.
- **Status:** closed 2026-09-08 — the second branch taken, with the first one measured and rejected

#### How it was closed, and why the alternative was rejected rather than skipped

The finding's mechanism was tried first, as a mutation: `!on.joined` dropped from the condition and
the mint issued on `this.db.Conn(ctx)` when a transaction is bound. It works — and the two things
measured on the live 17.9 while it worked are what decide against it.

1. **On the caller's own transaction a bound settles nothing, and for a *writing* transaction no
   bound settles anything wherever it is minted.** `floor` is `pg_snapshot_xmin` read through the
   caller's transaction, and a transaction holding an id keeps the cluster's floor at or below that
   id for as long as it is open, so `floor > bound` is unreachable for any bound minted after it.
   `a transaction that has written holds the floor at or below its own id` measures both halves: the
   writing transaction's floor never passes a bound minted beside it on the pool, and a transaction
   that wrote nothing carries no id at all. The finding's own scenario — a projector drained from
   inside a transactional app-usecase — is the *writing* one, so the proposed mechanism would not
   have closed it; it would have closed only the read-only-caller case.
2. **That remaining case is bought with a second connection checked out while the caller holds
   one.** `a second connection while a transaction holds the first is what a pool at its limit
   refuses` measures it: a pool at `MaxOpenConns(1)` with a transaction open answers `db.Conn` with
   `context.DeadlineExceeded` and nothing else, with the control that a pool with a spare connection
   serves the same checkout at once. Every joined poll that meets a gap would take a second
   connection; under N concurrent callers on a pool of N that is a deadlock, not a stall. A silent
   stalled reader is a defect; a stalled pool is that defect plus everything else on the pool.

So the refusal stands and is now a decision with three live cases behind it, in
`TestAWalkInsideATransactionMintsNoBoundAndSettlesNoGap`:

| Case | What it pins |
|---|---|
| `three walks, each in its own transaction, all stop at the same burnt gap` | the permanence, exactly as the criterion words it — three `ReadAll`s over a log with one burnt position, each in its own bound transaction, the third at `REPEATABLE READ`, all answering an empty page and the origin cursor. The control is that the cursor **they answered** delivers the event outside a transaction, so they measure a walk that cannot settle *there* rather than one that never advances |
| `a transaction that has written holds the floor at or below its own id` | why the caller's transaction is not the place, and why the writing case is closed to every place |
| `a second connection while a transaction holds the first is what a pool at its limit refuses` | what the alternative costs, with the spare-connection control |

The permanence case is not vacuous: with the alternative applied (mint on a pool connection when
joined) it reports *walk 1 inside a transaction delivered [after the burnt head], and a store that
mints a bound outside the caller's transaction is what passes this gap* — so if a later change takes
the mechanism after all, this case is what says the decision changed with it.

**Not closed by silence, and the residue is named.** A consumer that always walks inside a
transaction still reads `(false, nil)` for ever with no error and no log line. The store does not
refuse there, because a short page at a gap is honest per call — the gap may still be filled — and
refusing would break the legitimate walk that reads a transaction's own uncommitted events
(`a walk inside a transaction runs on it and the pool does not see what it wrote`). What makes it
non-silent is documentation, not code: `ReadAll`'s step 7, the plan's step 7, and row 11a of the S5
documentation checklist, which words it for the consumer.

---

### What was verified and is clean

**Two mutations, applied one at a time to `read.go`, both caught.** The unmutated control is green,
so the failures are the mutations, and both files were restored byte-identical (sha256 re-verified).

| Mutation | Result |
|---|---|
| `deliverable`'s stop condition `position > settled+1` → `settled+2` (a one-position off-by-one, not the naive-store mutation the plan already lists) | `TestAWalkDoesNotPassAPositionAWriterCouldStillCommit`: *the walk delivered [2] while a writer still held position 1*; `TestTheXminEqualsXmaxRuleFailsTheInFlightCase` also failed |
| step 8's re-settle removed (the floor is still read, its answer discarded) | `TestABurntGapIsPassedInsideOneReadAllAndALiveOneIsNot/a gap at the head of the page settles inside the same call`: *the walk answered []*; `TestAWalkInsideATransactionMintsNoBoundAndSettlesNoGap/the same walk outside the transaction settles and advances` also failed |

**PostgreSQL correctness, read statement by statement.**

- `logStatement` (`read.go:305`) carries the floor and the rows in one `LEFT JOIN LATERAL`, so
  step 3's settle — the one on the normal path — pairs a floor with the rows of the same snapshot.
  That is what makes it sound, and it is exactly what step 8 does not do (GAP-P2-S4-1).
- The watermark's premise holds as stated: a position below `reach` was drawn before `reach` was
  drawn (identity at `INCREMENT 1 CACHE 1 NO CYCLE`, verified at level 3), the statement trigger
  assigns the transaction id before the row draws its position, so every such writer's xid is below
  a bound minted after the fetch, and `floor > bound` means all of them have finished.
- `streamStatement`'s `LIMIT` is `Limits().StreamPage` itself, so a short page is the end of the
  stream by construction (`read.go:292-298`); the kernel re-checks density (`event/repo.go:244`).
- Neither read door opens, commits or rolls back anything; every statement is issued on a `*sql.Tx`
  the caller bound or on a `*sql.Conn` this call checked out and returns (`executor.go:84-98`).
  There is no statement method on `*sql.DB` anywhere in the eight methods.
- [[D-118]]: an ambient transaction of this store's source is joined (`executor.go:50-60`,
  `onExecutor` hands the `*sql.Tx` over and closes nothing), and an ambient executor that is not a
  transaction is refused before any statement at all four doors —
  `TestAnAmbientNonTransactionRefusesAtAllFourDoors` asserts one cause at the four and zero
  statements, with the fourth door now the store's own `ReadAll`. The optional interface is reached
  through `crud.ExecutorFor` + `crudsql.Transaction`, never a bare type assertion ([[D-061]]).
- Cursor: fixed width 58, tagged, log-bound; `readCursor` refuses in the declared order and
  `possible()` refuses the two structurally impossible triples; every cursor the store mints
  satisfies `possible()` by construction (`from` is a delivered position, `reach ≥ from`, and a
  cursor with no bound carries no reach).
- Error hygiene: no payload, key, type name, version or DSN reaches a returned error;
  `outside(column)` names the column and nothing else, and
  `TestARowOutsideTheSchemasPromisesRefusesTheWholeRead` asserts the planted values are absent from
  the rendering. Comparisons are `errors.Is` against sentinels throughout; no string matching on
  driver text. Nothing writes to a process-wide logger (`grep -n 'log\.' event/eventpg/*.go` → 0).
- Determinism: no randomness, no clock, no environment read in a non-test file
  (`grep -n 'os.Getenv\|time.Now\|math/rand' *.go` outside `_test.go` → 0); `recorded_at` is
  `statement_timestamp()`.
- Input mutation: `Envelope.Payload` is a fresh `[]byte` per row (`database/sql` clones into a
  `*[]byte` destination and the destination is declared inside the scan loop), so no buffer is
  reused across rows or calls.

**The naive contract a reviewer must name.** Beyond GAP-P2-S4-2, the worst thing a caller can do
that compiles, runs and returns no error is to persist a cursor obtained from a walk that ran on a
bound transaction and then roll that transaction back: the walk read through the transaction and
delivered its own uncommitted events, and the persisted cursor is already past them, so those events
are never read again by that consumer. The kernel documents it on `Reader.Cursor`
(`event/reader.go:62-69`) and the store cannot detect it, so it is not a finding against S4 — it is
named here because it is the trap adjacent to the one that is.

### Deferred to the backlog (`## P2` §55–§58), not fixed

`[medium]` a read cancelled mid-statement renders as `ErrBackend` and `errors.Is(err,
context.Canceled)` is false — and S4 change 2's justification that the arm "no test can falsify" is
factually wrong. `[low]` `errNoRow` carries the statement text into a returned error value. `[low]`
`fetch` re-parses the horizon once per row and silently keeps the last. `[low]` both read doors ask
the ambient-executor question twice per call, extending backlog §52 from `Append` to all three.

**Untouched**, per the delivery policy: only `[critical]` and `[high]` block, and the four above stay
in `EVENTSOURCE_BACKLOG.md` `## P2` §55–§58 where round 1 filed them.

---

## The gate after the two closures — 2026-09-08

Nothing here proceeded silently: every clause was run and its output read.

| Clause | Result |
|---|---|
| `grep -q 'var _ event.Store = (\*Store)(nil)' event/eventpg/config.go` | present |
| the twelve-name `-list` arm | **12** — the four new cases are subtests, so the count is unchanged |
| `go test -race -count=1 -tags=integration ./event/eventpg/` twice in a row | `ok … 46.266s`, `ok … 46.521s` |
| `gofmt -l .` | silent |
| `go build ./...` | exit 0 |
| `go vet ./event/...` | exit 0 |
| `go test -race -count=1 ./event/...` | `event`, `eventmemory`, `eventtest` all `ok` |
| `make check` | `check-deps`, `check-tiers`, `check-utils`, `check-triplets`, `check-todo`, `check-replaces`, `check-tidy`, `check-otel-schema`, `check-workspace` all `ok`; `./event/eventpg: 0 external packages` |
| `git status --porcelain event/` | `?? event/eventpg/` and nothing else — the zero-diff obligation still holds |

Changed by the closures: `event/eventpg/read.go` (step 8, the discharged bound, the doc comment,
`floorStatement` deleted), `event/eventpg/watermark_integration_test.go` (four cases and three
helpers), `event/eventpg/main_integration_test.go` (`interception` and `interceptOnce`). No exported
symbol was added or removed, so `docs/api/surface.md` is unaffected — `eventpg` enters that baseline
in S5.
