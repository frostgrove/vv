# EVENTSOURCE P5 — S4 (the wait, live) — GAPS

## Round 2 — remediation — 2026-09-12

**Both blockers are closed. GAP-1 and GAP-2 are `Status: closed`; all four close criteria of each
are met.** Each was reproduced first against live PostgreSQL 17.9, then fixed, and each now has a
test on **both** tiers that fails when the fix is reverted — shown by reverting it, on both tiers,
and pasting what came back. The contract moved with the code in both cases: §1.1, §UC-212, §UC-244,
§UC-245 and §6 items 5 and 10 of [SPEC], and **P-15** and **P-20** of [PLAN], which also carries the
remediation record and the re-run checkpoint (§ *S4 — remediation*).

`event/projection/wait.go` and `event/projection/wait_test.go` moved, so the manifest was
re-baselined and S4's `diff` fence is deliberately no longer empty; its whole content is those two
lines. No exported name changed and `make api` regenerates `docs/api/surface.md` byte-identical.

**GAP-3 to GAP-6 were not touched** — `[medium]`/`[low]`, standing in the backlog as items 44–47.

---

## Round 1 — econv-code-reviewer — 2026-09-12

**Verdict: RED. Two `[high][immediate]`, four recorded to the backlog.** Both blockers were
**driven, not read**: each is a running PostgreSQL 17.9 transcript pasted below, and for the first
one the one-line correction was applied, shown to flip the driven answer, and shown to leave every
existing unit and live test green — which is also the proof that **no test in the tree asserts
either behaviour**, in either direction.

- **GAP-1 `[high][immediate]`** — a wait that reaches on its **first** poll never re-reads the
  ownership row, so a cutover committing inside that poll answers `Reached: true` for a read model
  the caller is no longer reading. That is the exact outcome §UC-245's *"must not"* names and the
  one ES-05 exists to forbid, on the path a healthy deployment takes every time.
- **GAP-2 `[high][immediate]`** — an ordinary deadline that lands **inside** a poll is reported as
  `ErrTopology` wrapping `event.ErrBackend` — *"the store failed"* — against a store that did not
  fail. Measured at **5 of 8** rounds over a healthy store and a healthy schema. It contradicts
  `event/projection/errors.go`'s own shipped sentence (*"ErrNotVisible is a deadline and never a
  defect … What kind of not-yet it was travels on `Visibility` rather than in the sentinel"*) and
  inverts §UC-244's *"a deadline reached over failing polls must not look like a deadline reached
  over slow ones"*.

**Four `[medium]`/`[low]` go to [`EVENTSOURCE_BACKLOG.md`](EVENTSOURCE_BACKLOG.md) `## P5` as items
44–47 and are NOT fixed**, per the 2026-09-08 delivery policy.

**The pasted checkpoint output is real.** Every arm re-run at HEAD, with
`FROSTGROVE_EVENTPG_TEST_PSQL` set exactly as the section's record says it was. Transcript in §1.

**The two most important tests were attacked and both held.** Two mutations of
`event/projection/wait.go` that the section's own table does not list were applied and each was
killed by three and four named tests respectively; `wait.go` is byte-identical to its original
afterwards (sha256 `a06423ab…`, which is also its manifest line).

**No ES-05 nuance the study records is dropped.** Seven for seven, each located in the code or in
the doc that owes it; §3 is the enumeration. Nuances 1 and 2 — the ones the study says *"do not
survive re-derivation"* — were driven live and hold structurally (§4, PROBE B).

**The one red arm is foreign and is named.** `make unit` reports exactly one `--- FAIL:` line,
`TestNoI18nPackageCostsMoreThanItsErrorSeam` in `./scripts`. It is the owner's i18n work, untouched
here — **so `make unit` is not green and this report does not claim it is.** `make check` is green
on all **eleven** arms (not fourteen — GAP-6).

---

## 1. The pasted checkpoint is real — every arm re-run at HEAD

`export FROSTGROVE_EVENTPG_TEST_DSN='postgres://vv:vv@localhost:55432/vv?sslmode=disable'`,
`FROSTGROVE_EVENTPG_TEST_PSQL='docker exec -i vv-postgres-1 psql -U vv -d vv'`.

```
gofmt -l . | wc -l                                                   0        (plan: 0)
go vet -tags=integration ./event/eventpg/                            silent   (exit 0)
go test -tags=integration -list '^(the ten)$' … | grep -c '^Test'    10       (plan: LISTED=10)
go test -race -count=1 -tags=integration -run '^(the ten)$' …        ok 8.357s    (plan pasted 8.574s)
go test -race -count=1 -tags=integration ./event/eventpg/            ok 136.106s  (plan pasted 128.108s)
go test -race -count=1 -tags=integration ./event/eventpg/  (again)   ok 129.620s  (plan pasted 139.634s)
./scripts/checks.sh event-kernel                                     check-event-kernel: ok
diff -u .git/event_kernel_before_s4 scripts/event_kernel.sha256      EMPTY  (this section's own fence)
make check                                                           11 arms, all ok
make unit                                                            exactly one --- FAIL:
                                                                     TestNoI18nPackageCostsMoreThanItsErrorSeam (foreign)
select version()  (through psql, not Go)                             PostgreSQL 17.9 … Alpine
```

Green twice in a row, under `-race`, with the psql cross-check armed — so `destination.rows`,
`livePark.letters` and `liveGenerations.recorded` all ran their comparison rather than skipping it.
The ten named tests and their 23 `t.Run` arms all passed; the `-list` arm's `= 10` correction the
section records is right (the pattern names ten).

**Rows checked with psql rather than through Go.** I reconstructed the parked pair in a throwaway
probe and read every row the verdict rests on out of `psql` directly:

```
psql checkpoints:  [probepsql advance=1 highest=1 quarantined=1]
psql park:         [probepsql eventpg.probe.psql/probepsql%2FA @1]
psql read model:   []
psql log:          [1 v1]                        (the mark is at 1)
the wait answered: {Reached:false At:0 Behind:0 Moved:false Quarantined:0 Parked:true Polls:1}
                   projection: the sequence this change belongs to is parked …
```

The watermark **is** past the mark, the read model **is** empty, the letter **is** the caller's own
sequence, and the wait refuses on poll 1 with `At: 0` — no census was read at all. The appendix's
central sentence is true against rows PostgreSQL printed.

**The kernel boundary holds.** `git status --porcelain event/` lists 21 paths; the two this section
owns are `event/eventpg/wait_integration_test.go` (new) and `event/eventpg/park_integration_test.go`
(modified, correction 1). Every other path is claimed by S1–S3 in § *Contracts before code*.
`event/eventpg` is excluded from `event_kernel_manifest` by construction, so the empty
`diff` against `.git/event_kernel_before_s4` is the assertion the section says it is, and
`check-event-kernel` is green with `event/projection/wait.go` at `a06423ab…`.

**Metrics counted, not eyeballed.** `event/projection/wait.go` 467 lines, longest function body 43
(`waiting`), then 42 (`resolved`), 37 (`poll`), 23, 21, 19; imports are `context`, `errors`, `fmt`,
`time`, `github.com/frostgrove/vv/event`, `github.com/frostgrove/vv/runtime` — stdlib plus two
first-party, so the root module's zero-dependency rule is intact. `event/eventpg/wait_integration_test.go`
1414 lines, 10 `func Test`, 23 `t.Run`, longest test body 186 lines.

---

## 2. Blocking findings

### GAP-1 [high][immediate] A wait that reaches on its FIRST poll never re-reads the ownership row, and a cutover inside that poll answers `Reached: true` for the read model the caller is no longer reading

- **Where:** `event/projection/wait.go:372-408` —

  ```go
  func (this *waiter) poll(ctx context.Context) polled {
      first := this.seen.Polls == 1
      if first {
          if answered := this.resolves(ctx); answered.refusal != nil { return answered }
      }
      … park … census …
      if !first {
          if answered := this.resolves(ctx); answered.refusal != nil { return answered }
      }
      return polled{reached: true}
  }
  ```

  The `if !first` guard means the poll that reaches on **poll 1** performs the ownership read
  *before* the park and the census and never again. The contract it drifts from is [SPEC] §1.1
  *"The generation a wait names must be the one the read resolves to"*
  (`EVENTSOURCE_P5_USECASES.md:320-324`): *"`Wait` reads `Generations.Active(ctx, of.Projection())`
  **twice**: on the **first** poll, and again on the poll that would otherwise answer `Reached`"* —
  and from the plan's own **P-15** (`EVENTSOURCE_P5_PLAN.md:337-340`): *"What may not move is the
  **last** read: that one turns a false `Reached` into a refusal."* On this path that read does not
  happen at all. The function's own doc comment (`wait.go:364-371`) states the order the code does
  not keep: *"Then the ownership row again, on the poll that would otherwise answer `Reached` — the
  read that turns a false success into a refusal when a cutover committed while this wait was
  running."*
- **What:** driven, against the live store. A generation-2 projection, drained past the mark, with
  `Generations` supplied; the cutover to 3 commits inside poll 1's census read:

  ```
  PROBE A: vis={Reached:true At:1 Behind:0 Moved:false Quarantined:0 Parked:false Polls:1}
           err=<nil>   active=3
  PROBE A RESULT: Reached:true while the ownership row already resolves to generation 3
  ```

  The same probe after removing the two-line `if !first` guard — i.e. reading the row on the poll
  that reaches, which is what [SPEC] says and what the comment claims:

  ```
  PROBE A: vis={Reached:false … Polls:1}
           err=projection: this wait names a generation that is not the one reads of this
                projection resolve to …                                          active=3
  ```

  and with that change **`go test ./event/projection/` is green (4.460s) and all ten live tests
  pass**, including `TestACutoverThatCommitsWhileAWaitIsRunning` and the unit arm that asserts
  `Active` was read *exactly twice*. Nothing in the tree pins the current behaviour, and nothing in
  the tree pins the corrected one either — the guard is invisible to the whole suite.
- **Why this severity:** `[high]`. The wrong output is the one this appendix exists to prevent: a
  successful `Wait` followed by a read of a read model that does not hold the change, because the
  caller's read path resolves through the generation the cutover installed. The window is not
  theoretical — it is the park round trip plus the census round trip of poll 1, i.e. one to three
  live PostgreSQL statements, against an ownership row a deployment tool moves in a single
  committed transaction. It is also on the **common** path: a healthy deployment whose projector is
  caught up reaches on poll 1 every time, so the protection §UC-245 specifies exists only for the
  waits that were going to be slow anyway. The residual window [SPEC] admits (*"the row can move
  between the wait's last read of it and the caller's own read"*) is a different, unavoidable one;
  this is an extra window the design says it closed.
- **Why this timing:** `[immediate]`. S4 is the **checkpoint** for UC-245 and INV-113 (plan
  § *Use cases — Group BA* and § *Invariants*, both marked **S4**), so this is the section whose
  job was to prove the clause, and its live test proves it only for a reach on poll ≥ 2. The fix is
  the deletion of a two-line guard, its cost is one extra `SELECT` on the ownership row for a wait
  that reaches at once, and every arm that could conceivably regress was run above and is green.
- **Close criteria:**
  - [x] The ownership read on the reaching poll is unconditional. The `if !first` guard is deleted
        from `poll`; `resolves` runs after the census on every poll that would answer `Reached`.
        `wait.go`'s own paragraph gains the sentence that says why the first poll is not exempt.
  - [x] `TestACutoverThatCommitsWhileAWaitIsRunning` gains *"the retiring generation, cut over
        inside the census of the first poll"*: a `countedCheckpoints` holds census load 1 open, the
        cutover commits inside that window, and the arm asserts `ErrGeneration`, `Polls == 1`,
        `At >= mark` — so the census is proven to have reached and the refusal to be the ownership
        read's — and that the row now resolves to 3. Reverted, it answers
        `{Reached:true At:2 … Polls:1}` and `<nil>`.
  - [x] `TestACutoverUnderAWaitIsRefusedRatherThanAnswered` gains *"cut over inside the census of
        the first poll"* with a nested control — the same first-poll reach with no cutover — and
        both assert `Active` was read **twice on that one poll**. Reverted, the first fails with
        `{Reached:true At:6 … Polls:1}` and `<nil>`.
  - [x] [PLAN] P-15 keeps *"what may not move is the last read"* and gains the paragraph that says
        it is twice over the **wait** and not twice over two polls, with the window and the cost
        named; [SPEC] §1.1 gains the same in its own words, and §UC-245 gains the arm and the
        *"must not"* that forbids the exemption.
- **Status:** closed — 2026-09-12

### GAP-2 [high][immediate] An ordinary deadline landing inside a poll is reported as `ErrTopology` wrapping `event.ErrBackend` — "the store failed" for a store that did not

- **Where:** `event/projection/wait.go:337-352` and `:459-467`, composed with
  `event/projection/generation.go:170-172`:

  ```go
  case answered.refusal != nil && ctx.Err() != nil:
      return held.stopped(ctx, answered.refusal)
  …
  return this.seen, fmt.Errorf("%w: … and the last poll it made could not be read at all: %w: %w",
      ErrNotVisible, context.DeadlineExceeded, last)
  ```

  `surveyed` turns **any** `Tracker.Load` error into
  `fmt.Errorf("%w: the member %q could not be read: %w", ErrTopology, member, err)`, and
  `eventpg`'s `Checkpoints.Load` (`event/eventpg/checkpoints.go:205-207`) turns any query failure —
  including the caller's own `context.DeadlineExceeded` — into
  `event.Failure(event.Unclassified, …)`, i.e. `event.ErrBackend`. So the caller's expiring budget
  comes back as a topology refusal over a backend failure.
- **What:** driven, over a **healthy** store, a healthy schema and a recorded checkpoint row, with a
  stopped projector and eight budgets between 40 ms and 89 ms at `Every: 1ms`:

  ```
  PROBE E: the row stands at advance 1 highest 1
  PROBE E round 0: {Reached:false At:1 Behind:1 Moved:false … Polls:40}
      projection: this change was not visible … : the generation this wait names had not delivered
      everything at or below its mark when the deadline elapsed, and the last poll it made could
      not be read at all: context deadline exceeded: projection: this topology change is not one
      this projection can make: the member "probeinside" could not be read: event: the store failed
  PROBE E: 5 of 8 deadlines over a HEALTHY store answered ErrTopology, 5 answered ErrBackend
  ```

  The same shape appeared unprompted in a second probe with a different scenario (PROBE B, a wait
  behind an in-flight gap: `Polls:36`, same three sentinels). The published sentence it falsifies is
  in the shipped code, not only in the spec — `event/projection/errors.go:39-41`: *"`ErrNotVisible`
  is a deadline and never a defect … **What kind of not-yet it was travels on `Visibility` rather
  than in the sentinel.**"* Here `ErrTopology` — which §5.2 reserves for *the caller asking wrong* —
  and `event.ErrBackend` — *the store failed* — both travel in the sentinel chain on a wait where
  nothing was wrong with either.
- **Why this severity:** `[high]`. This is a wrong verdict handed to the caller on the feature's
  most common non-happy path, not a cosmetic message. A request handler written to §5.2's own
  vocabulary — `errors.Is(err, ErrTopology)` means *my cover or my wiring is wrong, refuse and page
  an operator*; `errors.Is(err, event.ErrBackend)` means *the database is in trouble* — takes the
  operator branch on roughly three timeouts in five, for a database that is fine and a deployment
  that is correctly wired. It also falsifies the one advantage the study claims over the mechanism
  ES-05 cites: nuance 3 records that Marten *"cannot tell behind from broken"* and that its
  maintainers filed that as a defect (issue #3912), and §ES-05/3 says vv already publishes the
  answer. vv publishes it on `Visibility` correctly (`Moved:false`, `At`, `Behind` were all right in
  every round) and then contradicts it in the error. §UC-244's *"must not"* is *"a deadline reached
  over failing polls must not look like a deadline reached over slow ones"*; the converse now holds
  and is the more damaging direction, because the failing-poll case is rare and the slow one is not.
- **Why this timing:** `[immediate]`. S4 is the **checkpoint** for UC-212 (plan § *Use cases —
  Group BA*), and `TestSlowVersusStopped` asserts only `ErrNotVisible` and `context.DeadlineExceeded`
  on both arms — it cannot see this, and neither can the S2 unit test, which drives a fake clock and
  never lets a real round trip race a real deadline. Shipping the live gate leaves the classification
  frozen and the only test that could catch it unwritten. The correction is local to `waiting`/
  `stopped`: a refusal whose cause is the caller's own context is the deadline, not a poll that
  could not be made, and must not be wrapped as one.
- **Close criteria:**
  - [x] A deadline that elapses inside a poll answers `ErrNotVisible` + `context.DeadlineExceeded`
        and neither `ErrTopology` nor `event.ErrBackend`. `waiting` carries the poll's refusal into
        `stopped` only when `expired` says it is not the caller's own context coming back —
        `errors.Is(refusal, ctx.Err())` or `errors.Is(event.CauseOf(refusal), ctx.Err())`, the
        second because a refusal answers false for `context.DeadlineExceeded` by design.
  - [x] `TestSlowVersusStopped`'s two arms assert `!ErrTopology` and `!event.ErrBackend` over the
        healthy store, and a third arm lands the budget inside a census read. Its nested control
        drives a poll that really failed and asserts the deadline **does** carry `ErrTopology`,
        `event.ErrBackend` and `errBlinkedCheckpointRow` as its cause. The unit tier carries the
        same pair: `TestADeadlineSaysWhichKindOfNotYetItWas`'s first-poll arm, with a new control
        beside its two existing ones.
  - [x] Made deterministic rather than raced, and against a **real** round trip: the live arm holds
        the checkpoint table in `ACCESS EXCLUSIVE` from a second connection, so the wait's own
        `SELECT` blocks until the caller's deadline elapses and `eventpg` classifies what pgx then
        says — `cause=&pgconn.errTimeout{err:context.deadlineExceededError{}}`. The eight-round
        interval probe is the *measurement* (3 of 8 before, 0 of 8 after) and is not the assertion.
  - [x] §UC-244's *"must not"* gains its converse, and says why the converse is the more damaging
        direction; §UC-212's third wait no longer asks for the poll's refusal to be wrapped in and
        says what is wrapped instead; [PLAN] P-20 gains the half it missed.
- **Status:** closed — 2026-09-12

---

## 3. Nuance conformance — ES-05, seven for seven

| # | The nuance (`EVENTSOURCE_P5_STUDY.md` §ES-05/2) | Where it is in the code | Verdict |
|---|---|---|---|
| 1 | a wait is a correctness primitive only if the number under it is a completeness watermark; Marten shipped it years before that was true | [[D-128]] as kernel law; `event/reader.go:91` `checkPage`; `event/eventpg/read.go:214` `deliverable` and `:232` `settledAt` | **present, and driven** (§4 PROBE B) |
| 2 | a sequence gap makes the target permanently unreachable in the source | closed structurally: a hole makes delivery *wait* and the wait is bounded by the oldest live transaction, not by a detector's three snapshots | **present, and driven** (§4 PROBE B, both halves) |
| 3 | the wait cannot tell "behind" from "broken" | `Visibility.Moved`/`At`/`Behind`/`Quarantined`/`Parked`; `TestSlowVersusStopped` pins it | **present on `Visibility`, contradicted in the error — GAP-2** |
| 4 | three further ways it times out with nothing wrong; a wait is a magnifying glass on the checkpoint store | the inherited global-`xmin` stall, `docs/modules/en/eventpg.md:236-268`, with the `projection.md` cross-reference assigned to **S6** | present, S6 owes the cross-reference |
| 5 | `FetchLatest` is the other road and a reviewer will raise it | [SPEC] §1.1 *"What is deliberately not done"*, *"No `FetchLatest`"* | present, refused with a reason |
| 6 | `Commit` carries no position, so the appendix's wording hides one read | `WaitSpec.Committed`'s doc comment (`wait.go:87-98`) says the round trip is here and not hidden; `TestAConfirmedCommandIsVisibleWithoutASleep` counts it and asserts **1** | present, measured |
| 7 | the scope sentence the source does not make | `event/projection/doc.go:57-67`: *"it says nothing about a second projection, a second database, a read replica or anything above the mark"*; the live second-projection arm measures it | present, measured |

**Binding decisions.** No contradiction found for [[D-128]] (nothing restates `Highest` as the
page's last position; the wait compares `Progress.Highest` and nothing else), [[D-129]] (grep of
`wait.go`/`mark.go` for `Cursor`: **0 hits**; the only cursor the section touches is in the
`leftover` fixture, which writes a cursor *this store minted* and never orders or compares it),
[[D-130]], [[D-092]] (`Wait` runs on the caller's goroutine; `go` statements in the new code are
test-file only; `startsNothing` green), [[D-118]] (the wait writes nothing: `saves == 0`,
`forgets == 0` asserted live), [[D-126]] (the wait opens no transaction and chooses no level — the
recording park asserts `bound == false` on every call it saw), [[D-101]] (no schema change,
`eventpg` untouched outside its test files), [[D-133]] (untouched).

---

## 4. The hard states, driven

Six were constructed and run against the shipped code and a live PostgreSQL 17.9. Four behaved as
specified; two are the blockers above.

- **PROBE A — a wait across a cutover, on the poll that reaches.** `Reached: true` while `Active`
  already answers 3. **GAP-1.**
- **PROBE B — a wait whose target sits above a position a still-open transaction reserved.** With
  the holder open: `{Reached:false At:0 Behind:2 Polls:36}` and a refusal; the read model empty.
  After `tx.Commit`: `{Reached:true At:2 Behind:0 Moved:true Polls:2}` and both rows applied. The
  watermark did not jump the hole and the wait was not fooled — study nuances 1 and 2, live. (The
  refusal's *class* on the first half is GAP-2.)
- **PROBE C — a wait on a projection whose queue holds somebody *else's* parked sequence.**
  `{Reached:true At:2 Behind:0 Moved:true Quarantined:1 Parked:false Polls:2}` with calls
  `[Sequences bound:false, Holds bound:false, Sequences bound:false, Holds bound:false]`. The
  `Sequences`-then-`Holds` gate is right, the widened `Holds` clause answers the committed state
  outside a unit, and an unrelated letter does not refuse a satisfied wait — which is [SPEC]
  §1.1's own argument against using `Holes` for this. **No S4 test covers this shape (GAP-3).**
- **PROBE D — a commit larger than the store's own stream page.** Unreachable against `eventpg`:
  `StreamPage=256` and `MaxBatch=64`, so one commit can never span two pages here. The multi-page
  loop in `resolved` is covered instead by `watchedStore.page` in the unit suite
  (`event/projection/harness_test.go:546-560`). Recorded, not a finding.
- **PROBE E — a deadline landing inside a poll against a healthy store.** 5 of 8. **GAP-2.**
- **PROBE F — the parked pair with every row read through `psql`.** §1.

Three of the prompt's hard states are **not this section's**: a `Resolve` against an unresolved
transaction and an expired-versus-absent receipt are ES-07 (S3 shipped the code, **S5** owes the
live proof), and a snapshot whose fold changed and a historical read at an unsupported revision are
ES-09/ES-08 (**S1** and **S5**; ES-09 ships no code at all by design). Nothing in S4 makes any of
them constructible.

---

## 5. Tests attacked — two mutations, no survivor

Both were chosen because the section's own mutation table does **not** list them, and both were
applied to `event/projection/wait.go` and reverted; the file's sha256 afterwards is
`a06423ab3532d9560e49708490aa2716e8b5ef28dd5c8f9feee4891234477a02`, which is its line in
`scripts/event_kernel.sha256`.

| Mutation | Result |
|---|---|
| `parked()` ignores a true `Holds` answer (`return true, nil` → `return false, nil`) — the mutation that makes the *whole appendix* a lie | **killed by three.** `TestTheParkedPairIsTheWholeOfTheAppendix` (*"answered `<nil>`"*), `TestParkedInOnePartitionWhileAnotherLags` (burned its deadline instead of `ErrParked`), `TestTheDerivedSpecAgainstThreeHandWrittenOnesLive` |
| the reach comparison off by one (`held.lowest < Until.At()` → `held.lowest+1 < …`) — does the happy test prove it waits for *the caller's* event, or merely for progress? | **killed by four.** `TestAConfirmedCommandIsVisibleWithoutASleep` (`At:0` against a mark this generation has passed), `TestSlowVersusStopped/stopped` (*"the deadline answered `<nil>`"*), both standings of `TestTheCallCountBudgetAndItsPlacement` |

A third experiment was the **inverse** of a mutation: GAP-1's correction was applied and the whole
unit package plus the ten live tests stayed green, which is how I know the guard has no witness in
either direction.

---

## 6. Recorded to the backlog — not fixed

Appended to [`EVENTSOURCE_BACKLOG.md`](EVENTSOURCE_BACKLOG.md) `## P5` as items **44–47**.

### GAP-3 [medium][next] UC-208's bound-transaction assertion never covers `Holds`, so the widened clause has no live witness

- **Where:** `event/eventpg/wait_integration_test.go:779-892`. Both standings of
  `TestTheCallCountBudgetAndItsPlacement` run over an **empty** queue — the second one drains it
  (`queue.clear`) precisely so that only `Quarantined` is non-zero — so `watched.counted(t,
  "Holds", 0)` is asserted and the `for _, call := range watched.made() { if call.bound … }` loop
  only ever walks `Sequences` calls.
- **What:** [SPEC] §UC-208's *"Then"* reads *"Every recorded call carries **no** bound transaction,
  which is the placement **the widened `Holds` sentence** and the unchanged `Sequences` one both
  promise."* The widened sentence is the one thing S4's correction 1 had to change a live fixture
  for (`livePark.reading`), and it is the half no arm records. I drove it (PROBE C) and it is
  correct — `Holds bound:false`, twice — so this is a missing proof, not a defect.
- **Why this severity:** `[medium]`. The behaviour is right today and the failure needs a future
  regression on a path `make api` and `check-event-kernel` both agree they cannot see — which is
  exactly the argument [SPEC] gives for why this test exists at all.
- **Why this timing:** `[next]`. It is one more standing on an existing test (a queue holding an
  unrelated sequence), and it belongs beside S6's module-page sentence about where each `Park`
  method runs — backlog item 43's neighbour.
- **Close criteria:**
  - [ ] A standing of `TestTheCallCountBudgetAndItsPlacement` (or a sibling) runs with a
        **non-empty** queue holding a sequence that is not the mark's, asserts `Holds` was called
        once per poll and `Sequences` once per poll, and asserts `bound == false` on the `Holds`
        calls.
  - [ ] The same arm asserts the wait still reaches, so an unrelated letter is proven not to refuse
        a satisfied wait.
- **Status:** open

### GAP-4 [medium][next] The cross-spec `Mark` guard compares the projection name alone, and the recorded reason covers only barrier marks

- **Where:** `event/projection/mark.go:22-24` — *"The comparison is on the projection name alone
  and never on the generation, because a barrier of another generation of the same projection is
  the cutover case a wait admits"* — enforced at `wait.go:292-294`.
- **What:** the sentence argues the exemption for `MarkOf` (a barrier carries **no** sequence key,
  so there is nothing to mis-ask). It does not argue it for `WaitSpec.Committed`, whose mark
  carries *"one projection's sequencer's answers"* — the doc's own words, two sentences earlier.
  A generation that re-keys its `Sequencer` (a legitimate reason to run one: a re-shaped fold, a
  different partition key) plus a mark minted from the other generation's `WaitSpec` gives
  `Holds(ctx, orders@3, <gen-2 key>)` → false → `Reached: true` for a change gen 3 parked. That is
  §UC-242's wrong-`Sequence` failure reached through the generation door instead of the hand-written
  one. The shape is not hypothetical: `TestACutoverThatCommitsWhileAWaitIsRunning`'s two controls
  both mint under one spec and wait under another (`unscoped.Until = waiting.Until`, lines 1355 and
  1406) — they are safe only because both generations there share `ByStream()`.
- **Why this severity:** `[medium]`, not `[high]`: it needs a caller to cross two `WaitSpec`s **and**
  a sequencer change across generations, and the natural call shape (`WaitOf(spec, over)` then
  `spec.Committed`) cannot reach it. But it is silent when it does happen, and the deliberate
  design note is what would stop a reader from noticing.
- **Why this timing:** `[next]`. Closing it means `Mark` carries the generation (or the sequencer's
  identity) for the `Committed` door only — a surface change, so it belongs with S6's page and a
  decision rather than inside a live-proof section.
- **Close criteria:**
  - [ ] `mark.go`'s paragraph distinguishes the two doors: a barrier mark travels across
        generations, a `Committed` mark's keys do not — or `Mark` records what it needs to refuse
        the cross-generation case and `Wait` refuses it.
  - [ ] A test mints under a spec whose `Sequence` differs from the waiting spec's *at another
        generation of the same name* and asserts the documented answer, with the same-sequencer
        control beside it.
- **Status:** open

### GAP-5 [medium][next] The checkpoint rows the whole census rests on are read through `database/sql`, not through `psql`

- **Where:** `event/eventpg/checkpoints_integration_test.go:525-549` (`storedCheckpoint` /
  `maybeStoredCheckpoint`) — a second pool and a hand-written `SELECT`, with no `psqlAnswers`
  cross-check, unlike `destination.rows`, `livePark.letters` and `liveGenerations.recorded`.
- **What:** [SPEC] §6's preamble is *"Rows are checked with `docker compose exec -T postgres psql`
  … and not by trusting Go"*, and the S4 section head repeats it. The rows S4's own assertions rest
  on most — *"the checkpoint row stands at or above the mark"* (`wait_integration_test.go:464`),
  the lagging member at `:570`, `vis.At == row.highest` at `:750` and `:1188`, `row.advance ==
  saves` at `:962` — all come from that helper. The section's record is careful and names only the
  three helpers that *do* cross-check, so this is [SPEC]-vs-harness drift rather than a false claim.
- **Why this severity:** `[medium]`. The second-pool read is already an independent observation
  (its own connection, its own statement, no store code), so the marginal value of `psql` is ruling
  out a driver-level artifact — real, but small. The helper predates S4.
- **Why this timing:** `[next]`. `psqlAnswers` already exists and the addition is ~6 lines, but it
  touches a phase-3 helper every tagged file in the package uses, so it belongs in one change with
  its own rerun of the whole tagged suite.
- **Close criteria:**
  - [ ] `maybeStoredCheckpoint` cross-checks against `psqlAnswers` when the variable is set, in the
        shape `livePark.letters` already has, and the whole tagged suite is green twice with it
        armed.
  - [ ] Or [SPEC] §6's sentence is narrowed to the rows it means, so a later section does not read
        it as covering the census.
- **Status:** open

### GAP-6 [low][backlog] The section's record says `make check` is "fourteen checks"; it is eleven

- **Where:** `EVENTSOURCE_P5_PLAN.md:2225-2226` — *"`make check` (fourteen checks, including
  `check-event-kernel`)"*.
- **What:** `make check 2>&1 | grep -cE "^check-[a-z-]+: ok"` answers **11**: deps, tiers, utils,
  triplets, todo, replaces, tidy, otel-schema, otel-module, workspace, event-kernel. S3's own
  report counted *"eleven arms"* for the same command on the same tree.
- **Why this severity:** `[low]`. The gate is green either way and nothing depends on the number.
- **Why this timing:** `[backlog]`. It is a number in a record, corrected when the record is next
  edited.
- **Close criteria:**
  - [ ] The sentence says eleven, or names the four extra `ok` lines (`scripts`, `otel`,
        `otel-wire-validator`, the local consumer module) it was counting.
- **Status:** open

---

## 7. What was checked and is clean

- **§6 items 1–10 → tests.** All ten present, all ten in the counted `-list` pattern, all ten green
  under `-race` twice. Each test carries the §-reference and the UC in its comment.
- **UC coverage claimed for S4** — UC-204 (including the second-projection arm and P-18's
  cross-spec refusal), UC-206 (the `Park`-nil control), UC-207 (the four-partition cover with its
  not-parked control), UC-208 (the call-count budget, both standings — except GAP-3), UC-209/210
  (the bound-transaction refusal, the still-open and rolled-back arms asserted
  character-for-character indistinguishable, the after-the-commit control), UC-212 (`Moved` telling
  slow from stopped, the cancellation control — except GAP-2), UC-242 (three hand-written arms
  asserting the **wrong** answers), UC-244 (first-poll terminal, fourth-poll polled through, the
  live mid-split cover in both directions with its children-cover control), UC-245 (both directions
  with the two `Generations`-nil controls — except GAP-1).
- **INV-109/110/112/113** — the mint refusals are pre-read (`counted.reads == 0`), the park is
  asked before the census on every poll (`Sequences == 3` over three driven polls, `Holds == 0`
  over an empty queue, **`Holes == 0`** unconditionally, `Park == 0`), six concurrent waits polled
  the same `Checkpoints` the loop saved through under `-race` with `row.advance == saves` and
  `forgets == 0`, and the scope sentence is measured by the second-projection arm.
- **`WaitOf` derivation** — `TestTheCallCountBudgetAndItsPlacement:827` asserts the derived spec
  did **not** drop the queue, which is P-1's failure mode caught at the live tier too.
- **No sleep, no business-table poll, no wall-clock assertion in UC-204's test** — verified by
  grep: `TestAConfirmedCommandIsVisibleWithoutASleep` contains no `time.Sleep`, no `time.After` and
  no `waitFor`. The two `time.Sleep` calls in the file are handler pacing in `TestSlowVersusStopped`
  and `TestAWaitBesideTheProjectionsOwnLoop`, and the two `time.After` calls are the harness's own
  30 s dead-man timers.
- **Corrections 1–3 are real and each is load-bearing.** `livePark.reading` is what makes the three
  parked arms fail for the code rather than for the fixture; `paced` is what makes the exact poll
  counts a measurement rather than a race; and the census-read count (`points.loads`) is what makes
  "a poll is over" different from "a poll was released" — I re-ran the two arms that depend on it
  five times with no flake.
- **The two live shapes the plan refused to force** — UC-242's wrong `Over` written through
  `event.Track`/`Save` with a store-minted cursor, and UC-244's mid-split cover as a real
  `projection.Split` — are both honestly labelled in the test comments as an operator's leftover
  and a real split, respectively.
- **The flow-doc debt is recorded, not hidden.** `docs/ai/flows/Index.md` maps `wait.go` and
  `mark.go` to FL-038 while FL-038's own file table names neither; the plan states this at
  `:1751-1757` and assigns it to S6. Not re-filed here.
