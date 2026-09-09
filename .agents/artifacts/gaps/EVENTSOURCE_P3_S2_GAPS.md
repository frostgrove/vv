# EVENTSOURCE_P3 — S2 (the two checkpoint stores, schema version 2, and the conformance runner) — GAPS

## Round 1 — econv code reviewer — 2026-09-08

Reviewed against [`EVENTSOURCE_P3_PLAN.md`](../plans/EVENTSOURCE_P3_PLAN.md) §S2, §D1–D2,
§D11–D13 and §"Contracts before code";
[`EVENTSOURCE_P3_USECASES.md`](../usecases/EVENTSOURCE_P3_USECASES.md) §3.1, §9.4, UC-100,
UC-102, UC-103, UC-122, UC-123, UC-124, UC-129 and INV-069/076/078/080/082; and the code as it
stands. Everything below was executed against PostgreSQL 17.9 at
`postgres://vv:vv@localhost:55432/vv` or run under `-race`, not read off the source.

**Round 2, 2026-09-08: all three `[high][immediate]` are CLOSED and the section is
green.** Each was reproduced first — the two interleavings by driving them rather
than reasoning about them — then fixed, then the fix was reverted to watch its own
test go red and restored. The closure evidence sits under each finding, and the
remediation is recorded in the plan's §S2 under "Round-1 remediation, 2026-09-08".
No `[medium]` or `[low]` was raised by round 2, so the backlog is unchanged. Round
1's verdict is left standing below, unedited.

---

**Three `[high][immediate]` are open. The section is NOT green.** The suite is green,
twice in a row, and `go test` being green is not the report: two of the three findings below
are defects the suite cannot see by construction, and the third is the suite itself.

---

### GAP-1 [high][immediate] The fenced save resurrects a row a `Forget` removed, so retiring a live projection is silently undone and the `EXISTS` guard is defeated

- **Where:** `event/eventpg/checkpoints.go:337-348` (`saveStatement`, the
  `WHERE $3::bigint = 1 OR EXISTS (...)` pre-check at :341-342),
  `event/eventpg/checkpoints.go:350-352` (`forgetStatement`),
  `event/eventtest/sections_checkpoints.go:434-446` (`forgetsInAUnit`, which certifies that a
  `Forget` rides in a caller's unit of work).

- **What:** the save is one statement but two decisions. The `EXISTS` sub-select decides
  *whether the row is there* against the statement's READ COMMITTED snapshot; the
  `ON CONFLICT … DO UPDATE WHERE c.advance = $3 - 1` decides *whether the row moves* against
  the live index. When a `DELETE` commits between those two moments, the `EXISTS` says yes,
  `ON CONFLICT` finds no live tuple, and PostgreSQL performs a plain `INSERT` — creating a row
  at an arbitrary advance that no advance-1 save ever created. Driven live, with the `Forget`
  held inside an open transaction, which is exactly the shape §S2's departure 6 added to the
  `transactions` section:

  ```
  row before: orders.v1 advance 5
  session A: BEGIN; DELETE … WHERE projection='orders.v1'; pg_sleep(3); COMMIT   -- DELETE 1
  session B (1 s later): the shipped save statement at advance 6
      INSERT 0 1      Time: 1994.752 ms          <- blocked on A, then landed
  row after:  orders.v1  cursor 06  advance 6
  ```

  The projector's `Save` answered **nil**. The plan's own measurement table
  ("advance 3, no row for the name → `INSERT 0 0` — `EXISTS` is false and nothing is created")
  is true sequentially and false under this interleaving; I reproduced both, in the same psql
  session, on the DDL the golden renders.

  `eventmemory` is immune — `Save` and `Forget` both take `log.mutex`, so the two orderings are
  the only two outcomes — which makes this a divergence between the two shipped stores as well
  as a defect in one.

- **Why this severity:** §3.1's justification for leaving `Forget` unfenced *is* the property
  this breaks: "It is not fenced and it does not need to be: a running projection's next save
  finds no row at its advance, is refused, and halts — which is the correct and visible outcome
  of retiring a live projection, rather than a silent reset". §UC-102's Must-not is the same
  sentence from the other side. Concretely, in the cutover flow S4 is about to build (UC-120,
  UC-121): an operator retires `orders.v1` after starting `orders.v2`, the old projector has a
  pass in flight, the retirement is silently undone, and the old projector goes on writing into
  the read model that was just cut over — with a successful `Forget`, a successful `Save`, no
  error and no observer transition anywhere. That is a read model corrupted by a retirement an
  operator watched succeed. It is `[high]` rather than `[critical]` only because no event is
  dropped or double-applied in the log itself.

- **Why this timing:** the statement is the section's central artefact, it is what
  `_examples/event-checkpoints-elsewhere` (D8) will be copied from, and `RunCheckpoints` is the
  contract a third-party store is written against — a store author who copies the shipped
  statement inherits the race and is certified. Changing the statement after S3 and S4 are
  written against its `RowsAffected` semantics is a change to the one thing both of them branch
  on.

- **Close criteria:**
  - [ ] The create path and the move path are separated so the advance > 1 path cannot create a
        row: an `UPDATE … WHERE projection = $1 AND advance = $3 - 1` for advance > 1 (zero rows
        is `Conflict`, and an `UPDATE` cannot resurrect a deleted row) and an
        `INSERT … ON CONFLICT DO NOTHING` reachable only at advance 1 — or another mechanism
        that makes "is the row there" and "does the row move" one decision against one snapshot,
        with the argument recorded.
  - [ ] `event/eventpg/checkpoints_integration_test.go` carries the race as a case: a `Forget`
        held in an open transaction, a save at advance N > 1 issued against it, and an assertion
        that the row is absent afterwards, read out of the table with `psql`-equivalent SQL
        rather than from the store's account of itself. **Control:** the same save with no
        concurrent `Forget` lands.
  - [ ] `eventtest.RunCheckpoints` gains an arm — in `forget` or `concurrency` — that races a
        `Forget` against a save at advance N > 1 and refuses a store that leaves a row behind,
        so a third implementation is told rather than certified. The section it lands in is
        named in the plan's defect table.
  - [ ] The four sequential paths the plan measured still answer `INSERT 0 1 / 0 0 / 0 1 / 0 0`,
        and the eight-saver case and `TestEightSaversAtOneAdvanceLeaveOneWinnerAndSevenConflicts`
        still report one winner and seven conflicts.
  - **Status: CLOSED (round 2, 2026-09-08).** Every box above is ticked.
    - **Reproduced.** `psql` against a scratch schema carrying the DDL
      `testdata/migration.golden` renders: a row at advance 5, session A holding
      `BEGIN; DELETE …; pg_sleep(3); COMMIT`, session B issuing the shipped save at
      advance 6 one second in — `INSERT 0 1`, `Time: 2001.544 ms`, and
      `orders.v1 | 06 | 6` left in the table. Then through the store's own API:
      `TestASaveAboveAdvanceOneCannotResurrectARowAForgetRemoved` failed with "a save
      at advance 6 against a row a committed forget removed was admitted".
    - **Fixed.** `saveStatement` takes the advance and answers one of two statements:
      `INSERT … ON CONFLICT (projection) DO NOTHING` at advance 1, and
      `UPDATE … WHERE projection = $1 AND advance = $3 - 1` above it. Seven parameters
      either way and the same `RowsAffected` semantics, so nothing downstream branches
      differently. The same interleaving now answers `UPDATE 0` after 1997 ms and
      leaves **zero** rows.
    - **The paths remeasured**, six rather than four: `INSERT 0 1` / `INSERT 0 0` /
      `UPDATE 1` / `UPDATE 0` / `UPDATE 0` with nothing created for a name that has no
      row / `checkpoints_cursor_check` for the empty cursor. Contention unmoved: a
      writer holding advance 3 open blocks the second, which answers `UPDATE 0` at the
      winner's cursor (1296 ms), or `UPDATE 1` where the first rolled back (1299 ms).
      Eight concurrent first saves at advance 1 against an empty table:
      `1 × INSERT 0 1`, `7 × INSERT 0 0`, one row at advance 1.
      `TestEightSaversAtOneAdvanceLeaveOneWinnerAndSevenConflicts` is green.
    - **Tests left behind.** `TestASaveAboveAdvanceOneCannotResurrectARowAForgetRemoved`
      (`event/eventpg/checkpoints_integration_test.go`) drives the interleaving through
      the store and counts the rows with SQL rather than reading the store's account of
      itself, with the uncontended save as its control; and the suite arm
      `forgetRacingASave` in the **`forget`** section, declining with `unable` for a
      store that does not claim `Transactions`.
    - **No sixth mutation row, and the plan says so where the defect table is.** The
      defect is an interleaving, and every decorator that expresses one deterministically
      also trips `fence` sequentially, which would make the harness's evidence about the
      wrong section. The arm is falsified directly instead: with the single-statement
      form restored, both the case **and**
      `TestTheCheckpointStoreSatisfiesTheContract/forget` fail live; with it back, both
      pass.
    - **The contract moved with the code.** [SPEC] §3.1's SQL block is now the two
      statements and records the measurement that forced them.

---

### GAP-2 [high][immediate] `RunCheckpoints` enforces an undeclared microsecond round-trip of `Progress.At` and fails 9 of its 12 sections for a store that is correct in every rule the contract states

- **Where:** `event/eventtest/checkpoints.go:176-187` (`progress`, which mints
  `time.Now().UTC().Truncate(time.Microsecond)`), `event/eventtest/checkpoints.go:257-268`
  (`sameCheckpoint`, which compares `Progress.At` with `Equal`),
  `event/eventtest/checkpoints.go:19-48` (`CheckpointFactory`, which has no hook for it),
  `event/checkpoint.go:13-27` (`Progress`, whose doc says nothing about it).

- **What:** every section that reads a row back compares `Progress.At` for exact equality.
  Microsecond is PostgreSQL's `timestamptz` grain and nothing else's. Neither the kernel's
  `Checkpoints` contract, nor [SPEC] §3.1, nor `CheckpointFactory` states that a store must
  round-trip `At` at all, let alone to what precision. I built a checkpoint store that honours
  every rule §3.1 does state — the fence, absence, names, the two cursor bounds, the seven
  outcomes, `Forget`, `Close` — and differs only in the grain of its instant column, and ran
  `eventtest.RunCheckpoints` against it:

  ```
  microsecond grain (the control)  — 12 sections, run passes
  millisecond grain               — FAIL: round trip, fence, forget, names, bounds,
                                    refusal classes, lifecycle, concurrency, durability
  second grain                    — the same nine
  ```

  Nine of twelve. The store is not certified for the fence, for `Forget`, for the name check or
  for durability, none of which it gets wrong.

- **Why this severity:** `RunCheckpoints` is the deliverable of §UC-123 — "a checkpoint-store
  implementer runs the conformance suite" — and it is the framework's only published statement
  of what a `Checkpoints` must do. This is a value justified by nothing but the shape of the one
  store the suite was developed against, promoted into a contract obligation that appears
  nowhere in the contract. A store over MySQL `DATETIME(3)`, SQLite, a Redis hash of Unix
  millis, or any JSON encoding that rounds is reported broken on nine counts, and the messages
  (GAP-2's sibling, backlog `## P3` §46) name the fence and the cursor rather than the instant,
  so the implementer is told their fence refuses everything. "A framework used by absolutely
  different applications" cannot ship a conformance suite fitted to one column type.

- **Why this timing:** it is a property of the suite S3, S4 and S5 all report against, and the
  eventpg census (`checkpointCensus()`) pins twelve `passed` lines that would move with it. It
  is also the moment at which the obligation is cheap to state: either the kernel declares it
  (a `Checkpoints` round-trips `Progress.At` exactly, and a store whose column is coarser says
  so) or `CheckpointFactory` gains the hook `Cursor` already is — supplied because only the
  store knows what its own backing can hold.

- **Close criteria:**
  - [ ] Either `event/checkpoint.go`'s `Progress`/`Checkpoints` doc states the round-trip
        obligation on `At` in as many words, and the suite's refusal names it; or
        `CheckpointFactory` gains a declared grain (or an `Instant func(t) time.Time` hook) that
        `progress` mints through and `sameCheckpoint` compares at, with absence defaulting to
        exact equality.
  - [ ] A case in `event/eventtest/checkpoints_test.go` runs a fixture whose column grain is
        coarser than the suite's own and asserts the outcome the chosen answer prescribes —
        certified, or `not certified` with a reason that names the instant. **Control:** the
        same fixture at the suite's own grain certifies twelve.
  - [ ] `eventpg` still certifies twelve and the census is unchanged.
  - **Status: CLOSED (round 2, 2026-09-08).** The second of the two answers was taken.
    - **Reproduced.** A decorator truncating `Progress.At` on `Save`, over this
      package's own honest fixture, through `CertifyCheckpoints`: microsecond — 0
      sections not passed; millisecond — **10** (round trip, fence, forget, names,
      bounds, refusal classes, lifecycle, concurrency, transactions, durability);
      second — the same ten. One more than the nine reported, because this decorator
      rounds inside a unit of work too.
    - **Fixed.** `CheckpointFactory.Instant func(minted time.Time) time.Time` declares
      the grain the store's own instant column keeps. `progress` mints through it and
      `sameCheckpoint` compares what it minted, so what a section asserts is the round
      trip and not one backing's precision; absent means exact. It carries `Cursor`'s
      own anti-vacuity rules, both fatal at the door: it must round rather than invent
      (idempotent), and it must keep two instants a second apart apart. The obligation
      itself is now written on `event.Progress.At` — a store answers the instant it was
      handed, at whatever grain its backing keeps one at, and this kernel names none.
    - **Tests left behind.**
      `TestACoarserInstantIsCertifiedWhenTheFactoryDeclaresItsGrainAndNotWhenItDoesNot`
      runs the millisecond and the second fixture both ways — declared certifies twelve,
      undeclared does not — with the exact fixture certifying twelve as the control; and
      two cases added to
      `TestACheckpointFactoryClaimingPersistenceWithNoSiblingFailsBeforeASectionRuns`
      for the two admission rules, watched from a subprocess like the other four.
    - **Falsified.** With `progress` minting `Truncate(time.Microsecond)` again and
      ignoring the hook, the declared arm reports "certified 2 of twelve" at both
      grains; restored, twelve.
    - **`eventpg` declares a microsecond and still certifies twelve, and
      `checkpointCensus()` is unchanged.**

---

### GAP-3 [high][immediate] `eventmemory.Checkpoints.Load` answers a half-absent checkpoint inside a unit that staged a `Forget`, the kernel door refuses it, and no section asks the question

- **Where:** `event/eventmemory/checkpoints.go:131-158` (`Forget`, which stages
  `event.Checkpoint{Projection: projection}` at :156), `event/eventmemory/checkpoints.go:65-84`
  (`Load`) and `:168-173` (`held`), `event/eventmemory/transaction.go:199-206`
  (`Tx.checkpointHeld`, which answers the staged entry); refused at
  `event/checkpoint.go:157-163` (`Tracker.admit`); unasked at
  `event/eventtest/sections_checkpoints.go:78-104` (`absenceSection`, which only reads outside
  a unit) and `:434-446` (`forgetsInAUnit`, which never `Load`s inside the unit).

- **What:** §3.1's first sentence about `Load` is "answers the zero `Checkpoint` when there is
  none". Inside a unit of work that has staged a `Forget`, the memory store answers a
  `Checkpoint` at advance 0 **with the projection name set** — a row that is, in the kernel's
  own words, "half-absent … a store that did not answer the question". Driven under `-race`:

  ```
  STORE ANSWERED: {Projection:orders.v1 Cursor: Advance:0 Progress:{...}} (Fresh=true)
  THE DOOR REFUSED IT: event: this store is not the one this value was minted over … :
      "orders.v1" was answered no advance beside a cursor, a name or a progress,
      and a row that is half-absent is not a fresh start
  ```

  `eventpg` is correct here (the `DELETE` is visible to the same transaction's `SELECT`, so the
  row is truly absent), so the two shipped stores answer differently for one input. The
  `eventtest` fixture checkpoint store carries the same shape
  (`event/eventtest/checkpoints_test.go:147-156`) and is certified twelve times over, which is
  the proof that no section asks: `absence` reads outside every unit, and `forgetsInAUnit`
  rolls back or commits without ever loading inside.

- **Why this severity:** it is a shipped store breaking the one sentence of the contract that
  `Fresh()` exists for, and the conformance suite — the artefact whose entire purpose is to
  catch exactly this class before a third party ships it — is blind to it because it never
  combines the two capabilities it certifies separately. The reachable consequence is a
  consumer under `InUnit` that retires a name and re-reads inside the same unit taking
  `ErrWrongStore` and halting where the contract says it sees a fresh start; that is loud rather
  than silent, which is why this is `[high]` and not `[critical]`. What makes it blocking is the
  second half: the suite's `absence` section is now known to certify a store that has this
  defect, and S3's whole `InUnit` proof runs over the store that has it.

- **Why this timing:** S3 is written against `eventmemory.Checkpoints` as the store that makes
  the `InUnit` path reachable with no database, and the shape of a staged `Forget` (a save at
  advance zero, `event/eventmemory/log.go:130-138`) is the thing S3's rebuild and retirement
  cases will be written on top of. A suite section added after S3 is a section S3 was not
  measured by.

- **Close criteria:**
  - [ ] `eventmemory.Checkpoints.Load` answers the zero `Checkpoint` for a name whose only
        staged entry is a removal — the staged-removal sentinel is told apart from a row inside
        `held`/`checkpointHeld` rather than handed to the caller.
  - [ ] `absenceSection` (or `forgetsInAUnit`) asks the question inside a unit of work: stage a
        `Forget`, `Load` inside the same unit, and refuse a store that answers anything but the
        zero `Checkpoint`. Gated on `Transactions` like the rest of that section.
  - [ ] The `eventtest` fixture store is fixed with it, and
        `TestEveryCheckpointDefectIsReportedByItsOwnSection`'s control still certifies the
        honest fixture on every section.
  - [ ] A case in `event/eventmemory/checkpoints_test.go` drives `Save → Begin → Forget → Load`
        and asserts `Fresh()` with an empty `Projection`; **control:** the same `Load` with no
        staged `Forget` answers the row.
  - **Status: CLOSED (round 2, 2026-09-08).** Every box above is ticked.
    - **Reproduced** under `-race`:
      `TestALoadInsideAUnitThatStagedAForgetAnswersTheZeroCheckpoint` failed with
      `{Projection:orders.v1 Cursor: Advance:0 Progress:{…}}` — the name beside no
      advance.
    - **Fixed.** `Tx.checkpointHeld` answers the zero `Checkpoint` where the last staged
      entry for the name is the advance-zero removal, rather than handing that entry
      back. The `eventtest` fixture store carried the identical shape and is fixed with
      it. Nothing else moves: the stage-time fence and `revalidateSaves` already read a
      removal as advance zero, so a save at advance 1 after one is admitted exactly as
      before.
    - **Tests left behind.** The case above, whose control is a load inside a unit that
      staged nothing; and the arm in `forgetsInAUnit` — inside the `transactions`
      section, gated on `Transactions` with the rest of it — which refuses anything but
      the zero `Checkpoint`.
    - **Falsified.** With `checkpointHeld` reverted,
      `TestTheCheckpointStoreSatisfiesTheContract/transactions` fails for `eventmemory`;
      with the fixture store reverted too,
      `TestEveryCheckpointDefectIsReportedByItsOwnSection`'s control and
      `TestAStoreThatDoesNotPersistDeclinesDurabilityAndCertifiesTheOtherEleven` both
      fail on the `transactions` section — which is the proof that the section now asks.
      Restored, green.

---

## What was counted, run and broken — the numbers behind the verdict

**Counted.**

- `scripts/event_kernel.sha256`: **114** recorded files — the number the plan's pasted output
  claims. `./scripts/checks.sh event-kernel` → `check-event-kernel: ok`.
- The manifest fence, re-run against `.git/event_kernel_before_s2` with S2's own allowed and
  required sets: **16 moved paths**, byte-identical to the list pasted in the plan, and
  `event-kernel-moved: ok`, exit 0. No path outside the allowed set; every required path
  present. Nothing under `event/projection/` and none of S1's four files appear.
- New non-test files, lines / functions / longest function:
  `event/eventmemory/checkpoints.go` 186 / 11 / 37 (`Save`);
  `event/eventpg/checkpoints.go` 352 / 19 / 47 (`Load`);
  `event/eventtest/checkpoints.go` 268 / 19 / 23; `sections_checkpoints.go` 466 / 18 / 42
  (`checkpointConcurrencySection`); `defects_checkpoints.go` 139 / 9 / 20;
  `event/eventmemory/transaction.go` 242 / 18 / 19 (`Commit`); `log.go` 138 / 9 / 23. No
  function over 47 lines, no file over 466.
- First-party import fan-out: `event` 2, `event/eventmemory` 1, `event/eventtest` 1,
  `event/eventpg` 6. `go list -deps` over `./event`, `./event/eventmemory` and
  `./event/eventtest` names `utils`, `crud`, `errs`, `event` and the package itself and
  **nothing else** — the kernel imports no concrete store and `eventtest` imports neither
  `eventmemory` nor `eventpg`. Zero, as the microkernel rule requires.
- Package-level mutable state in the new non-test files: **zero** (`grep '^var '` finds only
  sentinel blocks and `var _ event.Checkpoints = …` assertions). One in a test file
  (`event/eventtest/checkpoints_test.go:184`), recorded in the backlog.
- `go` statements in the new files: **one**, in `checkpointConcurrencySection`, in a package
  that imports `testing` and is therefore exempt from `startsNothing`. No `init()`, no
  goroutine in any constructor.
- Exactly-once creep: `grep -rni 'exactly.once'` over `event/` and `docs/` finds nothing in the
  new code or the new prose — the six hits are `sql.Tx`/`*sql.Conn` execution-count comments and
  D-118's own refusal.
- Untagged count arm: 7 of 7. Live count arm: 14 of 14. `go vet ./event/...` and
  `go vet -tags=integration ./event/eventpg/...` clean; `gofmt -l event` silent.

**Run.**

- `go test -race -count=1 ./event/...` — green (`event` 5.9 s, `eventmemory` 1.3 s,
  `eventtest` 2.5 s).
- `FROSTGROVE_EVENTPG_TEST_DSN=… go test -race -count=1 -tags=integration ./event/eventpg/...`
  — green **twice in a row**, 95.7 s and 95.5 s.
- `! env -u FROSTGROVE_EVENTPG_TEST_DSN go test -tags=integration ./event/eventpg/ | grep -q '^ok'`
  — exit 0: an unset DSN still fails the gate rather than skipping it.
- `make check` — every arm ok, including `check-event-kernel`. `make api` — additions only
  (14 lines: `Checkpoint`, `CheckpointCapabilities`, `Checkpoints`, `Progress`, `Tracker`,
  `Track`, and the three packages' `CheckpointSpec`/`Checkpoints`/`NewCheckpoints`/
  `RunCheckpoints`/`CheckpointFactory`); no line disappeared.

**Driven live against PostgreSQL 17.9, by `psql` against the table rather than through Go.**

- The four sequential paths of the shipped save statement, on the DDL
  `testdata/migration.golden` renders: `INSERT 0 1` / `INSERT 0 0` / `INSERT 0 1` /
  `INSERT 0 0`, and the empty cursor refused by `checkpoints_cursor_check`. The plan's
  measurement reproduces.
- **A writer that draws its decision before another commits.** Session A holds a transaction
  open with a save at advance 3; session B issues the same save. B **blocked 1.99 s** and then
  answered `INSERT 0 0` — one winner, one conflict, the row at A's cursor. With A rolling back
  instead, B waited and **landed** (`INSERT 0 1`), the row at B's cursor. No lost update in
  either direction.
- **Eight concurrent first saves at advance 1 against an empty table** (the fresh-start race
  the suite's `concurrency` section does not cover, since it saves at advance 1 alone first):
  `7 × INSERT 0 0`, `1 × INSERT 0 1`, one row at advance 1. The fence holds on the create path.
- **A `Forget` racing a save** — GAP-1. Reproduced, row resurrected at advance 6.

**Driven under `-race` against `eventmemory`, in a scratch module outside the repository
(removed afterwards; `check-event-kernel` verified ok after).**

- **Two projectors sharing one checkpoint**: eight goroutines over two `Checkpoints` values on
  one log, all at advance 2 — **1 landed, 7 refused**, row at advance 2. No race detected.
- **A restart mid-batch / a crash between the write and the checkpoint**: a unit that staged two
  appends and a checkpoint save and then rolled back burns **two** positions, not three, and
  leaves the checkpoint row untouched; the next append lands at position 3.
- **A staged save overtaken by a second writer**: `Commit` answered
  `eventmemory: a checkpoint row moved between the save this transaction staged and its commit`
  and the row kept the winner's cursor. The stage-time fence is re-validated at commit and does
  not overwrite.
- **`Load` inside a unit after a staged `Forget`** — GAP-3. Reproduced.

**Broken deliberately, then restored.**

1. `event/eventpg/checkpoints.go` — the `WHERE c.advance = $3::bigint - 1` predicate deleted.
   `TestTheCheckpointStoreSatisfiesTheContract` failed `fence`, `refusal classes` and
   `concurrency` ("8 of 8 savers at one advance landed"), and
   `TestEightSaversAtOneAdvanceLeaveOneWinnerAndSevenConflicts` failed. Restored → green.
2. `event/eventmemory/transaction.go` — `Rollback`'s arithmetic changed to
   `len(this.staged) + len(this.saves)`. `TestARolledBackUnitStagedBothAndBurntOnlyTheAppends`
   failed with "the append after the rollback took position 6 where the log stood at 2".
   Restored → green.
3. `event/eventtest/sections_resumption.go` — `heldWriter` reverted to the phase-2 shape (the
   late writer's transaction committed **before** the resumed walk).
   `TestTheNewestPositionCursorNowFailsResumption` then **failed**: the `newestFetched` mutant
   passed all twenty sections, printing `resumption: passed`. Restored → green, and
   `check-event-kernel: ok` at the recorded manifest. That is the direct proof that INV-080's
   evidence is the held-open transaction and not the section's name.

**Verified as claimed rather than trusted.** The plan's pasted S2 checkpoint output — the two
95 s live passes, the three untagged package lines, `114 files recorded`,
`check-event-kernel: ok`, and the sixteen-path `event-kernel-moved: ok` list — reproduces
exactly, and the fence's moved set is the same sixteen paths in the same order. Departure 1
(`store.go` byte-identical), departure 3 (the in-flight case is a seventh mutation,
`mutations 6 → 7`), departure 5 (the seven-outcome half only), departure 6 (the `transactions`
section's `Forget` arm) and departure 7 (both stores refuse an unnamed projection) all check out
against the code. Departure 2's claim that `sweep`/`probe`/`report` needed only a signature
change is true: `sweep` is one body generic over a two-method `running` interface, and neither
`probe` nor `report` grew a second body — which also settles backlog `## P3` §19 in the opposite
direction from the way it was written.

**Not re-reviewed here.** The test suite itself is phase 5's (`econv-test-reviewer`); §S3–S5
material (`event/projection`, the live kill-point pair, the module pages) is not this section's.
