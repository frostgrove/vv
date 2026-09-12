# EVENTSOURCE P4 — S4 (generations, the barrier and the cutover) — GAPS

## Round 1 — econv-code-reviewer — 2026-09-12

Two blocking findings, both driven rather than read, and both in the same seam:
**`Observe`'s all-members-fresh arm answers the origin, and `Cutover` guards only
the arriving side of that answer.** Everything else this section claims about
itself reproduces.

**The pasted checkpoint output is real.** Re-run line for line at HEAD:

```
gofmt -l .                                                          0
go build ./... ; go vet ./event/...                                clean
go test -list "$GEN" ./event/projection/ | grep -c '^Test'         10
ok  	github.com/frostgrove/vv/event/projection	1.329s   (the ten, -race)
go test -list '^TestEveryTrackerInTheProjectionPackageIsKeyedByAnIdentity$' ./scripts/ | grep -c '^Test'   1
ok  	github.com/frostgrove/vv/event	6.665s
ok  	github.com/frostgrove/vv/event/eventmemory	1.505s
ok  	github.com/frostgrove/vv/event/eventtest	4.330s
ok  	github.com/frostgrove/vv/event/projection	1.874s
check-event-kernel: ok
event-kernel-moved: ok   (the same seven files, in the same order)
```

`check-deps`, `check-tiers`, `check-utils`, `check-triplets`, `check-todo`,
`check-replaces`, `check-otel-schema`, `check-workspace` all `ok`. The live tagged
suite ran against PostgreSQL 17.9: `ok github.com/frostgrove/vv/event/eventpg
105.765s` (`-race -count=1 -tags=integration`), the plan's own 104.893s within run
noise. The one red in `./scripts/` is `TestNoI18nPackageCostsMoreThanItsErrorSeam`
— subject `i18n/cmd/vv-i18n`, arrived with `fefa3e9`/`939bcd9`, and this section
touches no file of it. The section's claim about the two baseline reds is exact.

**Kernel boundary is exact.** `git status --porcelain event/` lists
`errors.go`, `pass.go`, `projection.go`, `spec.go`, `spec_test.go` modified and
`generation.go`, `generation_test.go` new — the plan's file list, nothing else.
Zero kernel imports of `event/projection` (`grep -rn 'event/projection'
event/*.go event/eventmemory/ event/eventtest/` → 0); zero concrete-store imports
in `event/projection`'s non-test files → 0. `generation.go` imports
`context`, `errors`, `fmt` and `github.com/frostgrove/vv/event` and nothing else.

**Metrics counted.** `generation.go` 388 lines, longest function 34 lines
(`surveyed`), then 33 (`cutting`), 28 (`switching`), 28 (`reaching`), 22
(`Cutover`), 17 (`Observe`), 14 (`Reached`), 10 (`inTheCallersUnit`), three
4-line refusal builders. Max nesting depth 3. `grep -n "go func\|^var \|time.Now\|
os.Getenv\|rand\."` over `generation.go` → zero matches: no goroutine, no global,
no clock, no env read, no randomness, so [[D-092]] holds and every wait added to
the loop goes through `spec.Ticks`. New exported surface is exactly
`Generations`, `Barrier`, `Observe`, `Readiness`, `Reached`, `CutoverSpec`,
`Cutover`, `ErrRetired`, `Spec.Pace` — `go doc -all ./event/projection` shows no
symbol the plan did not declare.

**Mutations driven.** Four applied to the shipped tree, run, restored; every one
caught, and the tree verified back to `check-event-kernel: ok` afterwards.

| Mutation | Test | Result |
|---|---|---|
| `Reached` compares `>` instead of `>=` | four tests | **FAIL** — *"a generation whose lowest watermark is 900 answered {Reached:false …} against a barrier at 900, and the comparison is >="* |
| the `ready.Holes > 0 && !AcceptQuarantined` arm dropped | `TestACutoverRefusesOnHolesAndNotOnQuarantined` | **FAIL** — *"a cutover to a generation holding two parked letters answered `<nil>`"* |
| `inTheCallersUnit` never called | `TestACutoverTakesNoBarrierAndDerivesItsOwn` | **FAIL** — *"a cutover carrying a unit that opened no transaction was made"* |
| `read` sets `pacing` before the `!more` branch, so the read the poll releases is paced | `TestPaceThrottlesTheReadWhileDrainingAndNotWhileFollowing` | **FAIL** — *"the projection asked to wait 200ms, and over this window it was to ask for nothing at all"* |

**Interleavings driven, not read** (probe module at `/tmp/genprobe`, `-race`):

- *a rebuild draining beside the live generation while the log grows, then a
  cutover* — 40 events, both generations, `Pace: 2ms` on the rebuild, appends
  continuing under both. **Handled.** Each generation applied all 40 exactly once
  and in position order; no drop, no reorder, no double-apply; the cutover
  answered nil and the row moved once.
- *a cutover while the rebuild is still behind* — the rebuild had applied 2 of 40.
  **Handled.** `ErrTopology`, *"has not delivered everything the barrier …"*, the
  row unmoved.
- *a cutover with the retiring generation advancing under it* — GAP-2, below.
  **Wrong:** admitted, and reads move backwards.
- *a cutover after the live generation was `Split`* — GAP-1, below. **Wrong:**
  admitted unconditionally, silently.
- *a claim expiring mid-batch*, *a partition count change with a sequence in
  flight*, *a parked sequence receiving a later event* are S1–S3's subjects and
  were driven there; *a cutover with a reader mid-query* needs a transactional
  read path and is `TestTheCutoverSwitchesEveryTableAtOnceForAReaderInOneSnapshot`,
  which the plan schedules for S6. Not counted against this section.

**Binding decisions.** [[D-129]] holds: `Barrier.At` is an `event.Position`,
compared with `>=` against `Progress.Highest` and with nothing else; nothing
mints a cursor from it and `TestABarrierIsObservedAndReached` counts
`saves == 0`. [[D-128]] holds: the `min` fold is the only aggregate a completeness
watermark has across rows, and comparing two generations' watermarks over one log
is exactly what D-128's invariant licenses. [[D-118]] holds: `Activate` is a
durable write inside the caller's bound transaction and there is no second
intent table. [[D-092]] holds: nothing starts, `Cutover`/`Observe`/`Reached` are
functions. [[D-130]] and [[D-133]] are untouched. [[D-126]] is the one decision
that *bears on* a defect rather than being broken by one — see GAP-2.

**ES-04 nuance conformance.** Nuance 1 (no reference ships an atomic read-target
switch; it is vv's own) — carried, with the reader-snapshot precondition written
into the `Generations` comment. Nuance 2 (no reference ships a rollback; stating
the condition is the contribution) — carried and tested. Nuance 4 (a rebuild is
stricter than continuous execution) — adapted rather than dropped: the strictness
moved from the run to the gate as the `Holes` refusal, which is defensible, with
one opt-out recorded below. Nuance 5 ([[D-131]] versus blue/green) — the
mechanism is carried and falsified; the *writing* is S6's. Nuance 6 (a rebuild
needs its own budget) — `Spec.Pace` carries the read half and its own field
comment names what it is not, and Marten's second dial has no analogue, which is
already open as backlog `## P4` item 7. Nuance 7 (a cancelled rebuild is a defined
state) — UC-165, scheduled S6. **Nuance 3 is the one dropped half**, GAP-2.

---

### GAP-1 [critical][immediate] A retiring generation whose declared cover holds no row makes the barrier the origin, and the cutover then admits anything

- **Where:** `event/projection/generation.go:110-143` (`surveyed`, the
  `found.Fresh() → continue` arm and the `held.recorded > 0 && held.recorded <
  over.Count()` refusal), `event/projection/generation.go:326-353` (`switching`,
  where `held.recorded == 0` guards the **arriving** census and no equivalent
  exists for the retiring one), `event/projection/generation.go:78-94`
  (`Observe`, which discards the census and answers `Barrier{At: 0}` with a nil
  error).
- **What:** `surveyed` refuses a *partially* reported cover (UC-180) and answers
  `lowest = 0` for a *wholly* unreported one. `Cutover` calls `Observe` on the
  retiring cover and never asks how many of its members reported. So a
  `Retiring` cover none of whose rows exist yields `Barrier.At = 0`, `Reached` is
  true for any arriving generation by `x >= 0`, and the read target moves with
  no error. The identical mistake one field over — an `Arriving` cover none of
  whose rows exist — is loud: `held.recorded == 0 → ErrRetired`. The guard was
  written; it was applied to one side.
- **Why this severity:** two arms driven, both admitted, both silent.
  **(a) After a `Split`.** S1's `Split` retires the parent's row and `Forget`s
  it, leaving `orders#split` as the record. A live `orders` drained to position
  1000 is split into `orders#0.1`/`orders#1.1`; the operator cuts over with the
  `Retiring` cover they had before the split:

  ```
  after the split: "orders" fresh=true, retirement row fresh=false, children "orders#0.1" and "orders#1.1"
  Observe over the pre-split cover answered {Projection:orders Generation:0 At:0} / <nil>
  the cutover answered <nil>; the read target holds generation 2
  ADMITTED: the live generation runs at "orders#0.1"/"orders#1.1" past position 1000
            and reads now resolve to a generation that applied 1
  ```

  `Projection.unretired` (`pass.go:160-169`) halts a *runner* started at a
  retired share, with a comment arguing that redeploying the pre-split release
  "is not an exotic path — it is the one an operator reaches for first".
  `Observe` and `Cutover` never ask the retirement question that argument is
  about. **(b) Without a split.** The live generation runs at four partitions
  (`orders#0.3`…`orders#3.3`) and the operator declares `Retiring: Whole()` —
  the natural mistake when the two generations have different topologies, which
  is much of the reason a rebuild exists. Driven: cutover answered `<nil>`,
  read target moved to a generation standing at 3 where the live one stands at
  1000. The same spec with the mistake moved to `Arriving` answered
  `ErrRetired`, quoted in full.
  The damage is a live read model regressing to near-empty for every reader, with
  no error on any path, and the obvious rollback — `Cutover(From: 2, To:
  Ungenerated, Arriving: Whole())` — is then itself refused with `ErrRetired`,
  because the pre-split `orders` row is the one `Split` removed.
- **Why this timing:** it is a two-feature interaction *inside this phase* —
  S1's `Split` composes with S4's `Cutover` into a silent unconditional switch —
  and S5 puts `Generations` on `Spec` and gates effect staging on the same
  ownership row, so a read target that moved wrongly also moves which generation
  sends. The fix is local to `surveyed`/`switching` and costs nothing later; a
  cutover semantics changed after S5 and S6 have built on it costs both.
- **Close criteria:**
  - [x] `Cutover` refuses a retiring cover no member of which holds a checkpoint
        row, naming the retiring identity, with the same force as the arriving
        arm — a barrier of zero derived from silence is never used as evidence.
        `switching` folds the retiring census out of one walk (`observed`, of
        which `Observe` is the public half) and refuses `recorded == 0` with
        `ErrRetired` through `unobservable`, which names both readings the rows
        cannot tell apart.
  - [x] `surveyed` (or its callers) consults `retired(...)` the way `handOver`
        does, so a cover whose members were retired by a `Split` is told apart
        from one that never ran, and refused. `neverHandedDown` asks it of an
        absent member and of no other — one `Load` per absent member, none at all
        for a cover whose rows are there — and refuses with `ErrTopology` naming
        the member and the retirement row. `Observe` answers it too, which is
        §UC-180 one level down.
  - [x] A test drives arm (a): a drained `orders` at a non-zero watermark is
        `Split`, and a `Cutover` with the pre-split `Retiring` cover is refused
        and writes nothing. **Control:** the same cutover with the children's
        cover proceeds, so the refusal is the retirement and not the cover size.
        `TestACutoverRefusesARetiringCoverNoMemberOfWhichHoldsARow`, subtest
        *"a cover whose member a split retired"*.
  - [x] A test drives arm (b): a four-partition retiring generation declared as
        `Whole()` is refused. **Control:** UC-159's deliberate case — a
        generation that genuinely never ran, with no retirement row and no
        finer rows — still answers the origin from `Observe`, so this closes the
        cutover door without moving `Observe`'s documented answer. Same test,
        subtest *"a live generation declared through a cover it does not record
        at"*, with a second control over the cover it does record at.
  - [x] Backlog `## P4` item 52's mirror is written down: what a cutover *away
        from* a rowless generation means, decided rather than inherited. Backlog
        `## P4` item **59**, and §UC-200 is the case.
- **Status:** **closed, 2026-09-12.** Reproduced first, on the shipped tree, both
  arms: *"Observe over the pre-split cover answered {Projection:orders
  Generation:0 At:0} / <nil>"* then *"the cutover answered <nil>; the read target
  holds generation 2"*, and *"ADMITTED: the read target moved to a generation
  standing at 3 where the live one stands at 1000"*. After the fix, arm (a) is
  `ErrTopology` naming `"orders#split"` and arm (b) is `ErrRetired` naming
  `"orders"`, both with the ownership row unmoved. Four mutations driven and every
  one caught: the `recorded == 0` refusal dropped (**FAIL** — *"a four-partition
  generation declared as Whole() answered `<nil>`"*); the `neverHandedDown` call
  dropped (**FAIL** — *"observing the pre-split cover of a generation standing at
  36 answered `<nil>`"*); `neverHandedDown` refusing every absent row rather than
  a retired one (**FAIL** at §UC-159's control, *"a cover all of whose rows are
  absent"*, which is what keeps `Observe`'s documented answer where it was); and
  the retiring refusal made unconditional (**FAIL** at both new controls, *"the
  cover the retiring generation actually records at was refused too"*).
  `TestACutoverTakesNoBarrierAndDerivesItsOwn`'s `legal` fixture gained the
  retiring row it never had — a legal cutover has a retiring generation that ran,
  and that subtest was asserting eight refusals over a spec that was itself
  unobservable.

---

### GAP-2 [high][immediate] The read-target overlap window is neither closed nor named, and the code comment claims the caller's unit closes it

- **Where:** `event/projection/generation.go:355-369` (`inTheCallersUnit` and its
  comment), `event/projection/generation.go:247-263` (`Cutover`'s comment,
  "Five steps inside one transaction of the caller's"),
  `event/projection/generation.go:36-38` (the `Generations` comment, "the
  evidence a cutover reads and the switch it writes are one snapshot or the
  arriving generation can fall behind between them");
  `.agents/artifacts/usecases/EVENTSOURCE_P4_STUDY.md:757-766` (ES-04 nuance 3).
- **What:** the comment states a guarantee the mechanism cannot give. The
  retiring generation is a *separate runner committing in its own transaction*.
  Nothing claims, locks or fences its checkpoint rows, so the barrier is a
  measurement at read time and is stale by however far that generation advanced
  before the `Activate` commits. No isolation level closes it and [[D-126]]
  forbids choosing one anyway: `REPEATABLE READ` would make the barrier the
  snapshot value, which is *also* stale-low. The study records the only
  reference mechanism for this — Axon's `resetTokens`, "one transaction, every
  segment, processor stopped, **every token claimed**" — and neither half is
  taken. Marten's own answer, quoted in the study, is to name the window:
  "**Accepted overlap window** … Stop the old version before (or as) the new one
  starts to avoid the window". `EVENTSOURCE_P4_USECASES.md:1212-1225` quotes that
  passage and answers only its *side-effect* half ("vv closes it … the effect
  gate reads the ownership row inside the transaction that commits the
  advance"). The read-target half — reads moving **backwards** at the switch —
  is answered nowhere: `grep -ni "overlap window\|monotonic\|moves backwards"`
  over the plan, the usecases and `event/projection/*.go` returns only that one
  ES-06 passage.
- **Why this severity:** driven deterministically. Retiring `orders@1` at
  `Highest 1000`, arriving `orders@2` at `Highest 1000`, one caller-opened
  transaction, the live generation committing an advance to 1050 after the
  barrier was read and before the row moved:

  ```
  cutover answered <nil>; the retiring generation stands at 1050 and the read target holds 2
  ADMITTED: reads moved from a generation at 1050 to one at 1000
            — 50 positions of read model disappear
  ```

  On a projection taking 10k events/min, a cutover whose transaction lives 20ms
  regresses the read model by ~300 events for every reader: a customer who
  placed an order 200ms ago sees it gone. It self-heals only when the arriving
  generation catches up — and this phase's own answer to nuance 6 is
  `Spec.Pace`, so the generation reads *slower* precisely when the regression is
  open. Nothing tells an operator to drop `Pace` before cutting over, because
  nothing says the window exists. A `Pace` of 5s holds the regression open for
  at least one interval.
- **Why this timing:** it is a contract statement, not an implementation detail.
  S5 gates effect staging on the same ownership row and S6 writes D-134/D-135
  and the module page's blue/green section from these sentences; a comment that
  over-promises becomes a decision record that over-promises. And if the answer
  is "claim the retiring rows" rather than "name the window", it changes
  `CutoverSpec` — a public contract two sections are about to depend on.
- **Close criteria:**
  - [x] The `inTheCallersUnit` / `Cutover` / `Generations` comments no longer
        claim the caller's unit makes the evidence and the switch one snapshot
        with respect to a *running* retiring generation. What the unit buys —
        the arriving generation's own rows and the ownership row moving together
        — is stated, and what it does not buy is stated beside it. All three
        rewritten; `inTheCallersUnit`'s refusal text was rewritten with them, so
        the message an operator actually reads says the same thing.
  - [x] The window is named where an operator reads it, in Marten's own shape:
        the regression is bounded by the retiring generation's advance rate times
        the cutover's duration; the way to avoid it is to stop or drain the
        retiring generation before (or as) the switch commits; `Pace` on the
        arriving generation lengthens it. `Cutover`'s contract carries it under
        THE OVERLAP WINDOW IS NAMED AND NOT CLOSED, with *"Observe it twice and
        see whether the barrier moved"* as the check that needs no new surface;
        `Spec.Pace`'s own field comment says to drop it before cutting over.
  - [x] The alternative is adjudicated in writing rather than left unmentioned:
        either `Cutover` takes the retiring rows under a lock/claim the way
        Axon's reset does, or the reason vv refuses to (it would put an operator
        call in the advance path of a live runner) is recorded. **Refused, with
        the reason:** this call cannot stop a runner in another process, and
        claiming a live generation's rows means writing them, which takes that
        runner's fence away. Written in `Cutover`'s contract, in the plan's
        contract block, and in `EVENTSOURCE_P4_USECASES.md`'s overlap-window
        section, which now answers both halves rather than only the effect one.
  - [x] A test drives it: the retiring generation commits an advance between the
        barrier and the `Activate`, and whatever the chosen answer is —
        refusal, or a documented and measured window — is asserted.
        **Control:** the same cutover with the retiring generation stopped.
        `TestTheCutoverWindowIsWhatTheRetiringGenerationAdvancedUnderIt`.
- **Status:** **closed, 2026-09-12 — answered as a named window, not a refusal.**
  Reproduced first, on the shipped tree: *"cutover answered `<nil>`; the retiring
  generation stands at 1050 and the read target holds 2"*. The answer is Marten's
  and not Axon's, so what the test pins is the **bound** rather than a refusal:
  the read target lands exactly 50 positions behind, the retiring row stands at
  advance 2 (its own runner's two writes and no third), and the cutover issues
  **zero** checkpoint saves. The control — the same cutover with the retiring
  generation at rest — leaves the two at one watermark, so the measurement is of
  the advance and not of the switch. The adjudication is falsifiable rather than
  asserted: the mutation that adds Axon's claim to `switching` answers *"a
  checkpoint row moved between the save this transaction staged and its commit"*,
  which is the fence fight the refusal is about, and the case goes red if anyone
  ships it.

---

## Recorded, not blocking

Appended to `.agents/artifacts/gaps/EVENTSOURCE_BACKLOG.md` under `## P4` as
items 53–58 and left alone, per the delivery policy:

- **53** `[medium]` `CutoverSpec.Park` is optional, so the entire `Holes` check is
  disarmed by omitting one field — silently, and with `AcceptQuarantined` still
  false, which is the field that was supposed to be the only way past it.
- **54** `[medium]` a `Unit` that runs its body twice makes `Cutover` report
  `ErrConflict` for a switch that committed — driven; the mirror of backlog item
  46, which is the redrive version, and the shape `pass.go` refuses explicitly
  with `errUnitRanTwice` while `Split` is idempotent under it by construction.
- **55** `[medium]` `inTheCallersUnit` is a second spelling of
  `inACallersTransaction` in the same Go package, kept in sync by nobody.
- **56** `[medium]` the plan and the usecases assign the two new event decisions
  the numbers `D-134` and `D-135`, which the otel/i18n merge already took —
  `EVENTSOURCE_P4_PLAN.md:265` already cites "D-135" for the effect boundary
  while `docs/ai/decisions/D-135-*` is i18n. S6 writes those records.
- **57** `[low]` `cutting` joins `ErrSpec` and `ErrTopology` refusals into one
  error, so `errors.Is` answers true for both and a caller cannot branch.
- **58** `[low]` `TestNothingInTheProjectionPackageOpensATransaction` and
  `TestEveryProjectionSourceFileIsNamedByTheFlowReverseIndex` both floor at
  "sixteen … outside its tests"; the package now holds seventeen.

`FL-038`'s own file table has no row for `generation.go` and still says "seven"
sentinels, "four values" of `step`, and "Every non-test `.go` file under
`event/projection/` has a row above". That is **not** filed: the plan schedules
`docs/ai/flows/FL-038-*` and `docs/api/surface.md` for S6 explicitly, and S4's
one-row reverse-index edit is the part a red test forced forward. It is listed
here so S6's checklist can be checked against it.
