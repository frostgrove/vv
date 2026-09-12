# EVENTSOURCE P5 — S2 (the wait) — GAPS

> **Both blocking findings are closed as of 2026-09-12.** GAP-1 → plan **P-20**, GAP-2 → plan
> **P-19**; each was reproduced against the shipped code before it was fixed, each left a test that
> reports a `--- FAIL:` line when the fix is reverted, and both contracts moved with the code —
> [SPEC] §5.2's exit table, its cancellation paragraph, its store-honesty paragraph, §INV-109,
> §UC-212, §UC-244 and the plan's `Committed` contract block. The checkpoint below was re-run in
> full afterwards and is `EXIT=0`; `docs/api/surface.md` is unchanged at 37 added lines, 0 removed.
> GAP-3 through GAP-6 are `[medium]`/`[low]`, stand in `EVENTSOURCE_BACKLOG.md` `## P5` as items
> 29–32, and were **not** fixed. The per-finding evidence is in each Status line below.

## Round 1 — econv-code-reviewer — 2026-09-12

**Two blocking findings, and both are things I drove rather than read.** GAP-1: the caller's own
context expiring or being cancelled **inside a poll** is answered as `ErrTopology` — *"this
topology change is not one this projection can make"* — so §5.2's deadline exit and its
cancellation exit both fail to fire and UC-213's `errors.Is(err, ErrNotVisible) { serveStale() }`
branch never runs. GAP-2: `WaitSpec.resolved` verifies only a page's **first** envelope where the
kernel's `Repo.checkPage` verifies every one; a page whose second envelope is another stream's
mints a mark at a foreign position carrying a foreign sequence key, and the wait then answers
`Reached: true` for a change whose own sequence is parked — verbatim
*«scan checkpoint после parking не доказывает применение события»*, the one sentence ES-05 exists
to forbid. Both are quoted from runs below.

**Everything else this section claims about itself reproduces.** The pasted checkpoint is real: I
re-ran every arm, including both counting arms, `make api`, the whole `event/projection` surface
diff and `check-event-kernel`. Four of the section's twenty claimed mutations were re-applied
independently — including the two the section names as the point of ES-05 — and **every one was
caught**, with no survivor. No ES-05 nuance the study records is dropped; the enumeration is
below. Thirteen hard states were constructed and run; eleven behaved exactly as §5.2 says and two
are the findings.

**The one foreign red is the one named at the head of the plan.** `go test -count=1 ./scripts/`
reports exactly one `--- FAIL:` line, `TestNoI18nPackageCostsMoreThanItsErrorSeam`. It is the
owner's i18n work, it is not this section's, and I touched neither it nor the allowlist.
**`make unit` is therefore not green** and this report does not claim it is.

---

## The pasted checkpoint output is real. Every line re-run at HEAD

```
gofmt -l .                                                                  silent (0 files)
go build ./...                                                              silent
go vet ./event/...                                                          silent

go test -list '^(the fifteen)$' ./event/projection/ | grep -c '^Test'       15   (plan: LISTED=15)
go test -race -count=1 -run '^(the fifteen)$' ./event/projection/  ok 4.622s  (plan pasted 4.614s)
go test -race -count=1 ./event/...   ok event 7.530s / eventmemory 1.510s
                                     / eventtest 4.420s / projection 5.549s
                                     (plan pasted 7.397 / 1.511 / 4.421 / 5.564)

make api                             regenerated; byte-identical to the tree's file
git diff docs/api/surface.md         37 added lines, 0 removed, 0 changed — the §5.2 block
                                     the plan pasted, verbatim, plus S1's three

go test -list '^(the scripts seven)$' ./scripts/ | grep -c '^Test'          7   (plan: 7)
go test -race -count=1 -run '^(the scripts seven)$' ./scripts/     ok 7.224s (plan pasted 7.098s)

./scripts/checks.sh event-kernel                                  check-event-kernel: ok
scripts/event_kernel.sha256                                       158 paths  (plan pasted 158)
git status --porcelain event/                                     exactly S1's ten + S2's eight
make check                                                        every arm ok, EXIT=0
make vet                                                          clean
go test -count=1 ./scripts/                                       exactly 1 `--- FAIL:` line:
                                                                  TestNoI18nPackageCostsMoreThan…
make unit                                                         green apart from that one arm
```

The manifest fence is honest in the strong direction too. `git status --porcelain event/` lists
`doc.go`, `errors.go`, `harness_test.go`, `mark.go`, `mark_test.go`, `park.go`, `wait.go`,
`wait_test.go` under `event/projection` and **nothing else** under `event/` beyond S1's ten — no
`event/eventtest/`, no `event/eventpg/`, no `^event/[a-z_]*\.go$`. The section's own claim that
`event` and the conformance suite are *"held still by the fence rather than by intention"* is true
at HEAD. The two files outside `event/` are `scripts/projection_test.go` (P-4, P-5 — I read the
diff, it is the two `< 18` → `< 20` guards and the `"WaitSpec"` row and nothing more) and
`docs/ai/flows/Index.md` (correction 4 — two rows, both `FL-038`).

`make api`'s diff is the question for a person the section says it is, and the section's answer —
no queue depth on `Visibility` — is the one §5.2 argues for. I agree with it and record that the
`Holes`-never-called arm in `TestAHealthyWaitAsksSequencesAndNeverHoldsOrHoles` is what holds it,
not the surface.

## Metrics counted, not eyeballed

| Metric | Value |
|---|---|
| `event/projection/wait.go` | 441 lines; `mark.go` 52 |
| longest function body | `resolved` 42 lines (143–184); `waiting` 41; `poll` 37; `parked` 23; `waitable` 21; `Committed` 19; `WaitOf` 18; `resolves` 13; `Wait` 9; `stopped` 9 |
| max nesting depth | **3** (`for` → `for` → `if`), in `resolved` |
| imports of the two new files | `wait.go` 6 — four stdlib (`context`, `errors`, `fmt`, `time`) + two internal (`event`, `runtime`); `mark.go` 1 internal. **Zero third-party**, so the root module still resolves nothing ([[D-033]]) |
| internal fan-out | 2 packages, both already reached by `event/projection` |
| import cycles added | 0 |
| exported symbols added to `event/projection` | 12 surface entries — `Mark` (+`At`, `Zero`, `String`), `MarkOf`, `WaitSpec` (+`Committed`), `WaitOf`, `Visibility`, `Wait`, and 4 sentinels. **Exactly the section's *Realises* list; nothing extra, nothing missing, nothing removed or changed** |
| new globals | 1 unexported const (`defaultEvery = 50ms`), 0 mutable |
| package-level mutable state | 0 |
| `go` statements in the two new files | **0** (`startsNothing`, and grepped: no `go `, no `go func`) |
| `Cursor` mentions in the two new files | **0** ([[D-129]]) |
| `Begin` / `Commit(` / `Rollback` in the two new files | **0** ([[D-118]], [[D-126]]) |
| `time.Now` / `rand.` / `os.Getenv` in the two new files | **0** — the only clock is the caller's `Ticks` seam |
| non-test files in `event/projection` | 20, which is what both widened `scripts/` guards now assert |
| mutations applied independently | 4; **4 caught, 0 survivors** |
| hard states driven | 13 |

## The four mutations I re-applied, and what caught each

Every one was applied to a copy-protected `event/projection/wait.go`, run, and reverted; the file
was `diff`ed against its saved original afterwards and is byte-identical.

| Mutation | Result |
|---|---|
| **the park asked after the census and only on the poll that would reach** — the point of ES-05 | `--- FAIL: TestTheParkIsAskedBeforeTheCensusOnEveryPoll` **and** `--- FAIL: TestAHealthyWaitAsksSequencesAndNeverHoldsOrHoles`. Two independent witnesses |
| **the second `Generations.Active` read dropped** (P-15) | `--- FAIL: TestACutoverUnderAWaitIsRefusedRatherThanAnswered` |
| **the cross-projection refusal disabled** (P-18) | `--- FAIL: TestAMarkIsMintedOnlyFromANumberAStoreProduced` **and** `--- FAIL: TestNoRefusalOfAWaitNamesAPositionOrAKey` |
| **the mint's key de-duplication removed** | `--- FAIL: TestCommittedReadsTheCommitsOwnRangeAndNothingElse` |

The section's own record of *two* first-pass survivors that "failed by hanging" is exactly right
and is the right thing to have written down rather than fixed quietly; both arms now run under a
bounded context they must not reach, and I confirmed the bound is on the line in
`TestAPollThatCannotBeMadeIsTerminalFirstAndPolledThroughAfter:434` and in
`TestNoRefusalOfAWaitNamesAPositionOrAKey:904`.

## The hard states this section makes possible, driven

Each was constructed and run against the real code; the answer is quoted, not inferred.
(ES-07's `Resolve` standings, receipt expiry, and ES-09's snapshot fold are S3/S5/S6 and are not
reachable — there is no `receipt` package and no snapshot in the tree. ES-08's historical read at
an unsupported revision is S1's and was driven there.)

| State | What happened |
|---|---|
| a wait whose mark's **second** sequence key is parked (commit `A,B,A`, queue holds `B`) | `ErrParked`, `Visibility{Parked:true, Polls:1}`, two `Holds` calls, both outside a unit. UC-243's own "Then", which no shipped arm asserts — GAP-3 |
| the same across **three pages of one envelope** (`StreamPage` forced to 1) | 3 reads, same position, same two keys, same `ErrParked`. A page boundary costs the mint nothing |
| a mark minted on generation 2, waited on generation **3** of the same projection | `Reached: true` — the cutover case §UC-211 admits. The comparison really is on the projection name alone |
| `Generations.Active` **failing** (not disagreeing) on the poll that would reach | not `Reached`; polled through to the deadline, which answers `ErrNotVisible` + `DeadlineExceeded` + the row's own refusal, `Visibility{At:6, Moved:true, Polls:2}`. Correct on all four counts |
| `Committed` with **another store's** transaction bound to the ctx | mints. Correct: refusal 2 is about *this* store's transaction, and `eventmemory` answers an invalid authority for a foreign one |
| a page whose **second** envelope is another stream's | **mints, `err=<nil>`, at the foreign stream's position.** The kernel's `Repo.Load` refuses the identical page with `ErrBackend` — GAP-2 |
| …and the consequence at the park | the caller's own key `B` is parked and the wait answers `Visibility{Reached:true, At:104}`, `err=<nil>` — GAP-2 |
| `WaitOf` over a `Spec` with a **nil `Checkpoints`** | **admitted** with no error; the refusal lands on poll 1 as `ErrSpec` from `tracking`, reported as `Visibility{Polls:1}` for a poll that made no store call — GAP-4 |
| `Park.Holds` failing on the first poll | that park's own error, bare, satisfying none of the twelve sentinels, `Visibility{Polls:1}`. §5.2's "unwrapped and unreclassified" — correct |
| a sequencer answering `""` for every envelope | mints a one-key mark and asks `Holds(…, "")`. Symmetric with `pass.go:245`, which parks under the same key — not a defect |
| `%+v` of a `WaitSpec` | `… Until:[mark] …` — the position never renders, `Mark.String()` holds |
| the caller's **deadline** elapsing inside the **first** poll | `ErrTopology: the member "orders@2" could not be read: context deadline exceeded`; `ErrNotVisible=false` — GAP-1 |
| the caller's **cancellation** landing inside the **first** poll | `ErrTopology: … context canceled`, `Visibility{Polls:1}` — not `ctx.Err()` bare and not the zero `Visibility` — GAP-1 |

I also confirmed the layer the misclassification belongs to: the kernel's own `Tracker.Load`
answers `context.Canceled` with `errors.Is(err, event.ErrBackend) == false`, so **the kernel
honours UC-032 §12** and it is `Wait`'s first-poll rule that bends it.

## Nuance conformance — every ES-05 nuance the study records

| Study nuance (`EVENTSOURCE_P5_STUDY.md`) | Where it is in the code |
|---|---|
| §2.1 — the wait is only a correctness primitive if the number under it is a **completeness watermark**; Marten shipped the loop years before the gating | not re-derived: `Progress.Highest` is [[D-128]]'s kernel law, `Reader.checkPage` (`event/reader.go:91`) enforces the ascending arm for every store, and `wait.go` adds nothing to the substrate. `poll` compares `held.lowest >= spec.Until.At()` and nothing else (`wait.go:372`) |
| §2.2 — waiting on a *sequence assigned at call time* makes a gap left by a failed append **permanently unreachable** | structurally closed: the target is the position of the caller's **own committed** event, read back (`resolved:164`), never a head or a max. There is no `max(position)` anywhere in the package — grepped |
| §2.3 — the wait cannot tell *behind* from *broken*, filed against the source as a defect | `Visibility.Moved` (`wait.go:206`), set only from a second observation (`waiter.read`, `:291-293`, `:365-368`), and P-9's sentence that below two polls it means nothing is in the doc (`:195-197`). Driven both ways by `TestADeadlineSaysWhichKindOfNotYetItWas` |
| §2.4 — three ways it times out with nothing wrong; a wait API is a **magnifying glass on the checkpoint store** | the poll-number rule (`waiting:316-326`) plus the last refusal `%w`-wrapped into the deadline (`stopped:438`), so a checkpoint store that is down never reads as *the projector is behind*. Driven in `TestAPollThatCannotBeMadeIsTerminalFirstAndPolledThroughAfter` and re-driven by me on the fourth-poll arm. **GAP-1 is the one hole left in exactly this nuance**: the store is fine and the *caller's* budget is short, and the answer says topology |
| §2.5 — `FetchLatest` is the other road and a reviewer will raise it | explicitly not taken; §"what is deliberately not done" records it and `Repo.Load` is the single-stream answer. No catch-up-in-the-read code exists |
| §2.6 — `Commit` carries no position, so the map costs one read, **after** the commit | `Committed` is exactly that read (`wait.go:112-130`), the round trip is stated in the doc (`:87-98`) rather than hidden, and the bound-transaction refusal (`:126-128`) closes the "capture it from the append" design for every store |
| §2.7 — the scope sentence the source does **not** make | `doc.go:60-66`: *"Reached means DELIVERED to the generation and the cover the wait was given, and APPLIED only where a Park and a Sequence were supplied; it says nothing about a second projection, a second database, a read replica or anything above the mark."* All five negatives present |
| §3 delta 2 — whether polling goes through `Tracker` is a **decision, not an implementation detail** | decided and written: `Wait` polls `surveyed`, which builds a fresh `event.Track` per member per poll and never saves; `TestAWaitStartsNothingAndSavesNothing` measures 4 loads / 0 saves / 0 forgets, and 8 for a cover that has recorded nothing (P-7). I re-ran both arms |
| §3 delta 4 — *"up to the barrier" is the only granularity, and it should be stated rather than discovered* | stated in `doc.go` and in `Wait`'s own doc; `Visibility` carries no per-stream field |
| §4 — *"wait until lag reaches zero" and "wait until `PhaseFollowing`" are unimplementable here* | neither exists; no `head`, no `lag`, no `caught up` identifier in the package — grepped |
| §4 — [[D-128]]'s forbidden restatement of `Highest` as *"the last position of the page"* | absent from `wait.go`, `mark.go` and the `doc.go` addition — grepped for `highest`, `last position`; the only `page` mentions are `resolved`'s own paging |
| §4 — [[D-092]]: a wait is not a runner, but a background poller would be | 0 `go` statements; `Wait` runs on the caller's goroutine and the ticker is created **after** the first poll (`waiting:327-329`), which `TestAWaitReachesOnItsFirstPollAndNeverSleeps` measures by the intervals never asked for |
| §4 — [[D-132]]: no line, span or metric | no logging import; `check-deps` green |
| §4 — UC-032 is not silently widened | ES-05 has its own use cases and its own `Visibility`; `event/eventtest` did not move and `make api`'s `event` section is S1's three lines |

**Nothing in this list is missing.** Both blocking findings are about behaviour the study's own
§2.4 and §2.6 predict, not about nuances nobody thought of.

## Binding decisions

| Decision | Verdict |
|---|---|
| [[D-128]] the log delivers in position order; `Highest` is a completeness watermark | honoured and not restated. `poll` compares positions, never cursors, and no comment in the two new files describes `Highest` as the page's last position |
| [[D-129]] a checkpoint is a store-minted cursor, never a position | zero `Cursor` identifiers in `wait.go` and `mark.go`; `TestCursorIsNeverCompared` and `TestNoExportedFunctionOrdersOrTakesTwoCursors` both in the counted seven and green |
| [[D-130]] a projection is a supervised runner over the caller's unit of work | untouched — `Projection`, `New`, `Run` unmoved; the wait is beside the loop, not inside it, and `doc.go:57` says so |
| [[D-133]] a fence loser takes turns rather than halting | untouched; no fence code in this section, and `TestAWaitStartsNothingAndSavesNothing` asserts no tracker is ever saved through, which is what keeps a waiter out of the fence |
| [[D-118]] a durable write inside the caller's transaction IS the outbox | no table, no durable-intent row; `Wait` writes nothing at all, and `Committed` only reads |
| [[D-092]] anything continuous is a `runtime.Runner`; constructors start nothing | `WaitOf` and `MarkOf` perform no I/O and start nothing (asserted by `TestWaitOfDerivesTheSameDefaultsNewApplies` and by `startsNothing`); `Wait` is bounded by the caller's context and costs nothing when nobody waits |
| [[D-126]] the store chooses no isolation level | no transaction is opened. `Committed` **reads** `store.Transaction(ctx)` to refuse a bound one, which is the opposite of choosing; `TestNothingInTheProjectionPackageOpensATransaction` widened to 20 files and green |
| [[D-101]] migrating is a deployment profile choice | no migration, no `SchemaVersion` move — `event/eventpg` has no modified file |
| [[D-132]] no log line, span or metric from `event/projection` | none added |
| UC-032 §12 *a cancelled request stays a cancelled request* | **bent — GAP-1.** A cancellation landing inside a poll is answered as `ErrTopology` with a non-zero `Visibility`, where §5.2 promises `ctx.Err()` bare and the zero one |

## Contract conformance, both ways

Signatures match [SPEC] §5.2 exactly: `Mark` with three unexported fields and `At`/`Zero`/`String`;
`MarkOf(Barrier) Mark`; `WaitOf(Spec, Cover) (WaitSpec, error)`;
`(WaitSpec).Committed(context.Context, event.Store, event.Commit) (Mark, error)`;
`Wait(context.Context, WaitSpec) (Visibility, error)`; `WaitSpec`'s nine fields and `Visibility`'s
seven **in the declared order**; four sentinels with §5.2's wording character for character.
Receiver `this` throughout. **No silent extra public surface** — `make api`'s diff and the
`Realises` list are the same twelve entries. Nothing marked done is missing from the tree.

The five declared departures are each written into the section rather than absorbed, and I
checked each against the code: correction 1 (the nil-store refusal — present at `wait.go:119`,
tested as the seventh subtest), correction 2 (`Observe`'s three restated at `waitable:256-264` with
the comment naming whose they are), correction 3 (`TestTheDerivedSpecAnswersWhatThreeHandWrittenOnesGetWrong`
over two setups — I confirmed `NewCover` really does refuse two members at mask 3, so §UC-242's
single setup is genuinely unconstructible), correction 4 (`docs/ai/flows/Index.md`, two rows), and
correction 5 (the string-equality assertion — I re-ran it; `Observe` over the same wiring answers
the byte-identical sentence).

Two drifts, both small, both recorded below: `WaitOf`'s doc no longer names `ByStream()` where
§5.2's published comment does, and `ErrUncommitted`'s doc drops §5.2's last sentence. GAP-6.

---

### GAP-1 [high][immediate] The caller's own deadline or cancellation, landing inside a poll, is answered as `ErrTopology` — so neither the deadline exit nor the cancellation exit fires

- **Where:** `event/projection/wait.go:313-335` (`waiting`'s loop), specifically the first-poll
  arm —

  ```go
  case answered.refusal != nil && held.seen.Polls == 1:
      return held.seen, answered.refusal
  ```

  — which never consults `ctx.Err()`, and `wait.go:433-441` (`stopped`), which is the only
  place that knows how to tell a deadline from a cancellation and is reachable **only** from
  the `select` at `:330-334`. The contracts it fails: `wait.go:235-237` and
  [SPEC] §5.2 (`EVENTSOURCE_P5_USECASES.md:3075-3077`) —
  *"A deadline answers ErrNotVisible wrapping context.DeadlineExceeded… A cancellation answers
  ctx.Err() bare and the zero Visibility"*; §UC-212's **Must not** (*"must not return a bare
  `context.DeadlineExceeded`, must not return the zero `Visibility`"*) and its **Control**
  (*"A cancelled context — not a deadline — returns `ctx.Err()` bare with the zero
  `Visibility`, so the two are told apart and UC-032 §12's rule holds"*); and UC-032 §12 itself
  (`docs/ai/usecases/modules/event/UC-032-…md:94-97`).
- **What:** driven, against the shipped code, with a `Checkpoints` whose `Load` blocks until the
  caller's own context is done and then returns `ctx.Err()` — which is what a real checkpoint
  store does when a request budget is shorter than a round trip:

  ```
  deadline (30ms) inside the FIRST poll
    vis = {Reached:false At:0 Behind:0 Moved:false Quarantined:0 Parked:false Polls:1}
    err = projection: this topology change is not one this projection can make:
          the member "orders@2" could not be read: context deadline exceeded
    ErrNotVisible=false   DeadlineExceeded=true   ErrTopology=true

  cancellation inside the FIRST poll
    vis = {Reached:false At:0 … Polls:1}          <- not the zero Visibility
    err = projection: this topology change is not one this projection can make:
          the member "orders@2" could not be read: context canceled
    Canceled=true   ErrTopology=true              <- not ctx.Err() bare

  the same deadline landing on the FOURTH poll, for contrast
    ErrNotVisible=true   DeadlineExceeded=true   ErrTopology=true   Polls=3   Behind=5
  ```

  So the exact same physical event — the caller's budget running out — answers two different
  classes depending on which poll it lands in, and the class it answers on poll 1 is the one
  §5.2 reserves for *"the caller asking wrong"*. The kernel is not the culprit: the same
  cancellation through `event.Tracker.Load` answers `context.Canceled` with
  `errors.Is(err, event.ErrBackend) == false`, so `Tracker` honours UC-032 §12 and it is
  `surveyed`'s `ErrTopology` wrap arriving at a **request path** through `Wait`'s first-poll rule
  that bends it.
- **Why this severity:** this is the failure mode the whole ES-05 design is built around,
  reached from the other side. A request handler written exactly as §UC-213 specifies —
  `vis, err := Wait(ctx, spec); if errors.Is(err, ErrNotVisible) { serveStale(vis.Behind) }` —
  will **not** serve stale when its budget expires on the first poll. It will fall through to
  whatever it does with an unknown error, holding a `Visibility` whose `Behind` is `0` and whose
  `At` is `0`, and its operator will read *"this topology change is not one this projection can
  make"* and go looking for a cover that is not the one the rows are recorded at. The condition
  is not exotic: it is a tight per-request budget against a loaded checkpoint store, which is
  precisely the moment a deployment most wants the stale-read branch, and under load it is the
  **common** case rather than the rare one, because a short budget expires during the first
  round trip. §5.2 argues at length that the poll number separates *"the caller asking wrong"*
  from *"the deployment moving"* — an expired or cancelled context is neither, and the five-exit
  table already has the right home for it.
- **Why this timing:** `Wait` is published in `docs/api/surface.md` at this section and S4 is
  about to certify its behaviour live, against `eventpg`, where a context deadline *does* reach
  the driver and this path becomes the ordinary one rather than a constructed one. S4's
  `TestSlowVersusStopped` and `TestAPollThatFailsFirstAndAPollThatFailsFourth` will both run
  with real deadlines against a real pool and will pass or fail on whichever poll the clock
  happens to land in — a flake whose cause is this rule. Fixing it after S4 means re-running the
  live gate; fixing it now costs three lines and two test arms.
- **Close criteria:**
  - [ ] `waiting` consults `ctx.Err()` before returning a poll's refusal: a `context.Canceled`
        answers `ctx.Err()` **bare** with the **zero** `Visibility`, and a
        `context.DeadlineExceeded` goes through `stopped` so the answer satisfies `ErrNotVisible`,
        `context.DeadlineExceeded` and the poll's own refusal — on poll 1 exactly as on poll 4.
  - [ ] `TestADeadlineSaysWhichKindOfNotYetItWas` gains an arm whose deadline elapses **inside
        the first poll**, asserting `ErrNotVisible` and `DeadlineExceeded`, beside a control that
        the same wiring with a refusal that is **not** a context error still answers
        `ErrTopology` on poll 1 — so the new branch is shown to discriminate rather than to
        swallow the first-poll rule.
  - [ ] That test's cancellation control gains the in-poll arm, asserting `ctx.Err()` bare and
        `Visibility{}`.
  - [ ] Both arms are re-verified as mutation-sensitive: removing the new `ctx.Err()` check makes
        each report a `--- FAIL:` line naming the test rather than hanging.
  - [ ] `Wait`'s doc says which of the two rules wins when they collide, in one sentence, since
        neither §5.2 nor the plan currently does.
- **Status:** **closed 2026-09-12.** Reproduced first, at `wait.go:322` as shipped, with a
  `Checkpoints` whose `Load` blocks until the caller's context is done and then answers `ctx.Err()`:
  a 30 ms deadline in poll 1 answered `ErrTopology` with `ErrNotVisible=false`; a cancellation in
  poll 1 answered the same class with `Visibility{Polls:1}`; the identical deadline in poll 4
  answered `ErrNotVisible`. Fixed by one case in `waiting`'s switch —
  `case answered.refusal != nil && ctx.Err() != nil: return held.stopped(ctx, answered.refusal)` —
  placed **after** the `reached` and `terminal` arms, so a poll that answered still answers. After:
  the poll-1 deadline satisfies `ErrNotVisible`, `context.DeadlineExceeded` **and** the poll's own
  `ErrTopology` with `Visibility{Polls:1}`, and the poll-1 cancellation answers `context canceled`
  bare with `Visibility{}`. All five criteria met: `TestADeadlineSaysWhichKindOfNotYetItWas` gained
  the first-poll deadline arm, the live-budget control (still `ErrTopology`, still
  `Visibility{Polls:1}`), the in-poll cancellation arm, and a **fourth** control the criteria did
  not ask for — a parked answer arriving after the budget elapsed is still `ErrParked` and is not
  rendered as a deadline, which is the only thing pinning the placement. Removing the branch and
  moving it before `terminal` were both re-applied and both answered `--- FAIL:` lines naming the
  test in under two seconds; neither hung. `Wait`'s doc carries the tie-break in one sentence, and
  it moved into [SPEC] §5.2's exit table, its cancellation paragraph, §UC-212 and §UC-244 in the
  same change, with **P-20** in the plan carrying the argument.

---

### GAP-2 [high][immediate] The mint verifies only a page's first envelope where the kernel verifies every one, and a page whose second envelope is another stream's mints a mark for somebody else's event

- **Where:** `event/projection/wait.go:156-158` —

  ```go
  if read[0].Stream != commit.Stream() || read[0].Version != at+1 {
      return Mark{}, fmt.Errorf("%w: this commit's own range was asked for and the first
          envelope of the page this store answered is another stream's or another version's,
          and a mark minted from it would be a mark for somebody else's event", event.ErrBackend)
  }
  ```

  against the kernel's own `event/repo.go:374-389`, which walks **every** envelope for the same
  two properties and also caps the page at `limits.StreamPage`. The doc comment immediately
  above, `wait.go:132-138`, calls these *"the kernel's own"* checks. The frozen contract is
  [SPEC] §5.2 and `EVENTSOURCE_P5_USECASES.md:271-276`, which specifies the weaker sentence —
  so this is a defect the spec carries and the code faithfully reproduced, not a drift from it.
- **What:** driven. A store whose `ReadStream` answers the right first envelope and another
  stream's second one — the shape `Repo.checkPage`'s own comment exists for, *"a shared
  database, a restored dump or another service's writer"*:

  ```
  commit a-17 v1..v2, tags A then B; stream b-42 holds ZZZ
  the page answered: [ a-17 v1 (A) , b-42 v2 (ZZZ) ]

  spec.Committed(...)         -> mark.At()=4   err=<nil>       <- a foreign position
  the kernel's Repo.Load over the identical page
                              -> event: the store failed: [stream waits.order] was read and
                                 [stream waits.order] answered          <- ErrBackend, refused
  ```

  and then the consequence at the park, which is the whole of ES-05:

  ```
  the caller's own sequence "B" is parked in this generation's queue
  projection.Wait(...) -> {Reached:true At:104 Behind:0 Moved:false Quarantined:0
                           Parked:false Polls:1}   err=<nil>
  ErrParked=false
  ```

  The mark lost `B` — the key its own commit produced — and picked up `ZZZ`, so `Park.Holds` was
  asked a question about another stream's sequence, answered false, and the caller was told its
  change is **applied** while half of it sits in the queue.
- **Why this severity:** that last block is verbatim
  *«Scan checkpoint после parking не доказывает применение события»* — the one sentence §1.1
  calls *"the point of ES-05"* and *"the one thing a naive implementation gets wrong"* — reached
  through a route neither §UC-206's `Park`-nil control nor P-18's cross-projection refusal
  covers. The wrong output is a silent `Reached: true` on a nil error: there is nothing for a
  caller to branch on. It also mints a position (`104`) that is not the commit's, so a caller
  that retries the wait, or that logs `Visibility.At`, is reasoning about another stream's place
  in the log. The premise — a store answering a page it should not — is the same premise the
  kernel declined to assume away eleven lines earlier in the same repository, and the cost of
  matching it is one `for` loop over `read` instead of one index into it. *"Ours is simpler"* is
  not available here: the kernel is the reference, it is in this tree, and its stronger check is
  four lines.
- **Why this timing:** S4 certifies the mint live and S3 makes `Repo.Digest`'s sibling value
  durable; a mark minted at another stream's position is the input to both. More immediately,
  `Committed` takes an `event.Store` **the caller hands it**, which nothing compares against the
  store the `Commit` was minted over — so the weak check is the only thing standing between a
  two-store deployment and a mark resolved against the wrong log, and it is standing at one
  envelope out of `StreamPage`. Closing it after S4 means re-running the live gate and moving a
  published sentence of §5.2 at the same time.
- **Close criteria:**
  - [ ] `resolved` verifies **every** envelope of each page for stream and for
        `Version == after + offset + 1`, and refuses a page longer than
        `store.Limits().StreamPage`, with `event.ErrBackend` and the existing wording — matching
        `Repo.checkPage`'s three arms rather than its first.
  - [ ] `TestCommittedRefusesTheSixItCannotMint`'s *"a page whose first envelope is another
        stream's"* subtest gains a sibling whose **second** envelope is the foreign one, and a
        third whose second envelope's version repeats or skips, each asserting `event.ErrBackend`.
  - [ ] A driven arm asserts the consequence rather than only the refusal: with the check in
        place, the same wiring answers `ErrParked` for the parked key `B` instead of
        `Reached: true`.
  - [ ] The comment at `wait.go:132-138` stops calling the check *"the kernel's own"* unless it
        is, or says in one clause which of `Repo.checkPage`'s arms it reproduces and which it
        does not.
  - [ ] [SPEC] §5.2 and `EVENTSOURCE_P5_USECASES.md:271-276` move from *"each page's first
        envelope"* to the per-envelope wording in the same change, so the document and the code
        do not disagree in the other direction.
  - [ ] Mutation-checked: reverting to the `read[0]`-only form makes the new arms report
        `--- FAIL:` lines naming them.
- **Status:** **closed 2026-09-12.** Reproduced first: for commit `a-17 v1..v2` tagged `A`,`B` with
  the page's second envelope replaced by `b-42`'s at the version asked for, `spec.Committed`
  answered `mark.At()=3, err=<nil>` where the commit's own last event is at 2, while
  `Repo.Load` over the identical page refused with `event: the store failed: [stream waits.order]
  was read and [stream waits.order] answered`; with `B` parked, `Wait` then answered
  `{Reached:true At:13}` and a nil error. Fixed by an unexported `honest(read, stream, after, page)`
  carrying **all three** of `Repo.checkPage`'s arms — the page cap against `Limits().StreamPage`,
  every envelope's stream, every envelope's `after+offset+1` — which `resolved` now calls where it
  indexed `read[0]`. All six criteria met: `TestCommittedRefusesTheSixItCannotMint` gained a
  second-envelope arm, a repeated-version arm and an over-long-page arm; the consequence is driven
  rather than only the refusal — the mark this commit **does** mint carries `B` and the queue
  holding it answers `ErrParked`; the comment at `wait.go:132-138` now names `Repo.checkPage` and
  says it reproduces all of it; and [SPEC] §5.2, §INV-109 and the plan's `Committed` contract moved
  to the per-envelope wording in the same change, with **P-19** carrying the argument. Three
  mutations were re-applied — the whole check reverted to `read[0]`, the cap alone dropped, the walk
  alone reduced — and each was caught by exactly the arms it should be, with no survivor.
  `TestNoRefusalOfAWaitNamesAPositionOrAKey` gained the row for the nineteenth refusal path in the
  same change, so the fix did not widen GAP-5's hole.

---

## Recorded, not fixed — routed to `EVENTSOURCE_BACKLOG.md` under `## P5`

Per the delivery policy these do not block and are **not** to be fixed in this section. They are
appended to the backlog as items 29–32.

- **GAP-3 `[medium]`** — §UC-243's *"Then"* has no arm. The spec requires *"`Wait` answers
  `ErrParked` from the second `Holds` call"*; `TestCommittedReadsTheCommitsOwnRangeAndNothingElse`
  ships only the **Control** (the park holding neither key, `Holds` counted twice). I drove the
  missing half and the behaviour is correct, so this is a coverage hole and not a defect — but the
  claim that a commit spanning three sequences is protected in *all* of them currently rests on a
  call count rather than on an answer.
- **GAP-4 `[medium]`** — `WaitOf` admits a `Spec` whose `Checkpoints` is nil. It refuses the zero
  `Cover` and an unnamed identity and derives `Checkpoints` from the same `Spec`, and `New` refuses
  a nil one at construction; here the refusal is deferred to `tracking` on poll 1 and reported as
  `Visibility{Polls: 1}` for a poll that issued no store call at all. Driven.
- **GAP-5 `[medium]`** — `TestNoRefusalOfAWaitNamesAPositionOrAKey`'s table covers 15 of the
  section's 19 new refusal paths. The four it does not reach are `WaitOf`'s two, `Committed`'s
  `store.Transaction` error wrap, and `resolved`'s zero-position `ErrUncommitted`. I read all four
  messages by hand and none renders a key, a position, a version or a cursor, so INV-124 holds in
  fact; what is missing is the arm that keeps it holding.
- **GAP-6 `[low]`** — two published doc sentences moved. `WaitOf`'s comment no longer names
  `ByStream()` (§5.2: *"It applies `ByStream()` where `Spec.Sequence` is nil"*), so a consumer
  reading GoDoc learns the `Park` clause and not the `Sequence` default; and `ErrUncommitted`'s
  comment drops §5.2's closing sentence *"It is `receipt.Unresolved` one level down and says so"* —
  understandable at S2, since `event/receipt` does not exist yet, and owed back by S3.
