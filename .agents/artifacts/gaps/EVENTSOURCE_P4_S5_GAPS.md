# EVENTSOURCE P4 — S5 (the effect capability and its gate) — GAPS

## Round 1 — econv-code-reviewer — 2026-09-12

Four blocking findings. Two of them are the section's own headline claims measured
and found not to hold — **the two-sender boundary is not closed at any isolation
level this repository names** (GAP-1, driven against PostgreSQL 17.9), and **the
barrier is not a recorded position although [SPEC] §1.6 says both sides of the
comparison are checkpoint columns** (GAP-4). One is a falsifier that is missing on
exactly the path a degraded projection takes (GAP-2, a mutation that survived the
whole package). One is a door that accepts a wiring whose only outcome is the
permanent loss of an owed effect, while the other door refuses it (GAP-3, driven).

Everything else this section claims about itself reproduces, and the shape of the
capability — a value a `Handler` has no route to, three suppressors cheapest
first, the split per envelope, the effect following its envelope into the park —
is correct on every path I drove.

**The pasted checkpoint output is real.** Re-run line for line at HEAD:

```
gofmt -l .                                                          0
go build ./... ; go vet ./event/...                                clean
go test -list "$EFF" ./event/projection/ | grep -c '^Test'         13
ok  	github.com/frostgrove/vv/event/projection	1.049s   (the thirteen, -race)
go test -list '^TestEveryTrackerInTheProjectionPackageIsKeyedByAnIdentity$' ./scripts/ | grep -c '^Test'   1
event-kernel-baseline: 152 files recorded in scripts/event_kernel.sha256
ok  	github.com/frostgrove/vv/event	6.957s
ok  	github.com/frostgrove/vv/event/eventmemory	1.510s
ok  	github.com/frostgrove/vv/event/eventtest	4.345s
ok  	github.com/frostgrove/vv/event/projection	1.923s
check-event-kernel: ok
event-kernel-moved: ok   (the same seven files, in the same order)
```

The `1.049s` in the plan is the number this run produced, to the millisecond.
`check-deps`, `check-tiers`, `check-utils`, `check-triplets`, `check-todo`,
`check-replaces` all `ok`. Re-running `event-kernel-baseline` left
`scripts/event_kernel.sha256` byte-identical, so the manifest in the tree is the
one this section's files hash to.

**The one red is the one the section named.** `go test ./scripts/` fails on
`TestNoI18nPackageCostsMoreThanItsErrorSeam` — subject
`github.com/go-json-experiment/json/jsontext` reached from `i18n`, no file of
which is in this diff. The section's claim about the baseline reds is exact.
*What the pasted block does not show is the `FAIL	github.com/frostgrove/vv/scripts`
line belonging to the very command it quotes* — the `&&` chain as written stops
there and never reaches `event-kernel`. The prose two paragraphs down says so
explicitly, so this is disclosure with a filtered transcript rather than a
concealment; recorded as backlog `## P4` item 67 and not counted against the gate.

**Kernel boundary is exact.** `git status --porcelain event/` lists `doc.go`,
`errors.go`, `pass.go`, `projection.go`, `redrive.go`, `spec.go`, `spec_test.go`
modified and `effect.go`, `effect_test.go`, `generation.go`, `generation_test.go`
new — this section's seven plus S4's two, which `.git/event_kernel_before_s5`
already carries, so `event-kernel-moved` reports exactly the seven. Zero kernel
imports of `event/projection`; zero concrete-store, `net/http` or `jobs` imports
under `event/projection` ([[D-130]] holds); `effect.go` imports `context`,
`errors`, `fmt` and `github.com/frostgrove/vv/event` and nothing else, so the
dependency budget row does not move.

**Metrics counted.** `effect.go` 177 lines, six functions, longest `gate.stage`
at 19 lines, then `gate.past` 12 and `unstageable` 10; max nesting depth 2.
`grep -n "^var \|time.Now\|os.Getenv\|rand\.\|log\.\|go func"` over `effect.go`
→ zero matches: no goroutine, no global, no clock, no env read, no randomness,
no process logger, so [[D-092]] and [[D-062]] hold and nothing new starts.
`pass.go` is now 976 lines and `claimed` is the one function two orders run
through. New exported surface is exactly `Effect`, `Effects`, `EffectsFunc`,
`Spec.Effects`, `Spec.EffectsAfter`, `RedriveSpec.Effects`,
`RedriveSpec.EffectsAfter`, `RedriveSpec.Generations` — the plan's declared set,
nothing silently added. `Effect`'s three fields and `Effects`' one method match
the plan's contract block character for character.

**Mutations driven.** Three applied to the shipped tree, run, restored; the tree
verified byte-identical (`md5sum`) afterwards.

| Mutation | Test | Result |
|---|---|---|
| suppressor 3 gated on `this.of.Generation() != Ungenerated` | `TestAnUngeneratedProjectionWithGenerationsStopsStagingAtTheCutover` | **FAIL** — *"the retiring projection staged [A1 A2 A3] after the row moved"* |
| the isolation pass appends the **parked** envelope to `held.owed` | `TestAParkedEnvelopeIsNotStagedAndIsNotLost` | **FAIL** — *"the sink took [[A1] [A2 B1]] … telling the world about it is the inversion the capability exists to prevent"* |
| `unblockedPage` sets `held.owed = this.matched` instead of `free`, so every envelope it **parked** is staged | — | **SURVIVED.** `go test ./event/projection/` green. GAP-2 |

**Interleavings driven, not read** (probe file added to `event/projection`, run
under `-race`, removed; `git status` identical before and after):

- *a later page meets a sequence the queue already holds, with a sink wired* —
  **handled.** Sink pages `[[A1] [B1]]`, read model `[A1 B1]`, two letters under
  `orders/a`. A3 is parked without reaching the handler and without reaching the
  sink, and the letter carries its effect. The code is right; nothing in the
  suite says so — GAP-2.
- *a stage followed by a lost fence* (§UC-183's shape, one page behind a
  non-empty queue so the loop takes the applier that saves last) — **handled.**
  `Stage` was called once, `ErrOvertaken` was published, and the sink landed
  nothing: the staged row went back with the unit.
- *a claim expiring mid-drain, after the letter was applied and its effect
  staged* — **handled.** `Evict` answered `ErrClaimLost`, the unit rolled back,
  `Retried{Applied: 0, Left: 2}`, the sink landed nothing and both letters are
  still parked. No double-send on the next redrive.
- *a rebuild draining beside the live generation, then a cutover* — handled, and
  it is the section's own §UC-169 arm; re-run and reproduced.
- *the ownership row moving between the gate's read and the unit's commit* —
  **wrong.** GAP-1.
- *a redrive carrying its own barrier* — **wrong.** GAP-3.
- *a partition count change with a sequence in flight* and *a cutover with a
  reader mid-query* are S1–S2's and S4's subjects and were driven there; the
  second needs a transactional read path and is S6's
  `TestTheCutoverSwitchesEveryTableAtOnceForAReaderInOneSnapshot`. Not counted
  against this section.

**Binding decisions.** [[D-118]] holds: `Stage` writes inside the caller's bound
transaction and this package owns no intent table. [[D-092]] holds: nothing in
`effect.go` starts, and the gate is a value built per delivery. [[D-129]] holds:
`EffectsAfter` is an `event.Position` compared with `>`; no cursor is ordered or
minted. [[D-128]] holds: the per-envelope split is licensed by position order
being a kernel law. [[D-133]] holds and was measured — the loser's stage rolls
back with its unit. [[D-130]] holds: the sink is an interface the application
implements. **[[D-126]] is the decision that bears on GAP-1 rather than being
broken by it**: the framework chooses no isolation level, so the guarantee §1.6
claims has to be carried by the `Generations` contract, and it is not.

**ES-06 nuance conformance** (study §ES-06 part 2, nuance by nuance):

- **N1 replay-ness must be durable** — half carried. The comparison is per
  envelope against a position rather than against an in-memory flag, and a
  warm-up interrupted at `M < N` resumes suppressed. What is not durable is the
  barrier itself. GAP-4.
- **N2 the replay must end at a recorded position** — the replay ends at a fixed
  position; nothing records it and nothing derives it from the prior
  generation's mark, which is the half Marten automates. GAP-4.
- **N3 suppression is the default and the exception is named** — carried, and
  better than the source: `Effects: nil` is the rebuild's answer and there is no
  opt-in flag to forget.
- **N4 a failed warm-up must not become continuous execution** — carried by
  construction: a failing pass never advances, and the one way past the barrier
  without applying — parking — is refused beside `EffectsAfter`
  (`spec.go:292-294`), falsified by `TestABarrierBesideAParkIsRefused` with both
  controls.
- **N5 the durable ownership boundary neither source provides** — the mechanism
  is present and is the right one; the boundary it draws is not closed. GAP-1.
- **N6 a contract and not a sandbox** — carried on this section's half
  (`TestABatchCarriesNoRouteToAnEffect`, four fields, zero methods, an AST walk
  over every non-test file for `context.WithValue`/`Value`, with a fixture
  control). The import walk (§UC-177) is S6's.
- **N7 a reset clears the dead-letter queue** — answered by construction, and the
  answer is written down in [SPEC] §1.6: a generation is a name, the park is keyed
  by it, so the old letters stay under the old name and the new generation starts
  empty. Nothing here contradicts it.

**Contract conformance.** `Effect`, `Effects`, `EffectsFunc`, the three new
`Spec` fields, the three new `RedriveSpec` fields, the four refusals and the
suppressor order all match the plan. Two drifts, both spec→code: §1.6's "Both
sides of the comparison are checkpoint columns" (GAP-4) and §1.6's "the loser's
whole unit rolls back, its staged row with it" (GAP-1).

---

### GAP-1 [high][immediate] The two-sender boundary is open at every isolation level this repository permits, and the code says it is closed

- **Where:** `event/projection/effect.go:95-126` — the comment *"the ownership row
  is read through the ambient transaction that commits the advance, so two
  generations cannot both believe they own the live effects — the loser's whole
  unit rolls back and takes its staged row with it"*, and the unlocked read
  `this.generations.Active(ctx, this.of.Projection())` it describes;
  `event/projection/generation.go:44-47` (the `Generations` contract, which asks
  nothing of `Active` beyond answering); [SPEC] §1.6 part 2 (*"the loser's whole
  unit rolls back, its staged row with it"*) and §UC-173's **Then** (*"No envelope
  is staged by both"*).
- **What:** `Active` is a plain read. Nothing in the interface, the doc comment or
  the module recipe requires it to take a row lock, and [[D-126]] forbids this
  framework choosing an isolation level. At `READ COMMITTED` — PostgreSQL's
  default and the one every shipped example runs at — a transaction that read
  `active = 1` and a concurrent `Cutover` that writes `active = 2` do not conflict:
  **both commit**. Driven against PostgreSQL 17.9 with two sessions:

  ```
  T1: BEGIN; select active from own_row where projection='orders';   ->  1
  T2:        update own_row set active=2 where projection='orders';  ->  UPDATE 1   (committed)
  T1: insert into staged_effects ...; COMMIT;                        ->  COMMIT     (no error)
      select count(*) from staged_effects                            ->  1
      select active from own_row                                     ->  2
  ```

  The staged effect is committed by a generation the row no longer names, and
  nothing rolled back. The same interleaving at the Go level, with a `Generations`
  whose row moves between the read and the commit:
  `PROBE P4 staged=[A1 A2] while the row moved to 2 before the unit committed`.
- **Why this severity:** the arriving generation is, by construction, behind the
  retiring one at the moment of cutover — that is what the barrier measures — so
  the envelopes the retiring generation stages inside this window are envelopes
  the arriving generation has *not yet applied*. When it reaches them it holds the
  row, and it stages them too. Two staged jobs for one envelope is one duplicate
  email or one duplicate payment per envelope in flight at the cutover, which is
  the exact failure ES-06 exists to prevent and the one §1.6 says vv closes where
  *"Marten names the two-sender window and calls closing it a separate concern"*.
  The window is one pass's transaction, and a cutover is precisely when passes are
  long. `REPEATABLE READ` does not close it either — a snapshot read of a row
  another transaction updates raises nothing in PostgreSQL; only `SERIALIZABLE`
  would, through the rw-antidependency between this read and `Cutover`'s own read
  of the checkpoint rows, and nothing asks for it.
- **The general mechanism that closes it**, driven on the same two sessions: a
  **locking** read of the ownership row (`SELECT … FOR SHARE` / `FOR KEY SHARE`)
  in `Active`. The cutover's `UPDATE` then blocks until the staging transaction
  commits, and the generation that staged is the generation that owned the row at
  commit time:

  ```
  T1: BEGIN; select active from own_row where projection='orders' for share;  ->  1
  T2:        update own_row set active=2 ...                                   ->  BLOCKED
  T1: insert into staged_effects ...; COMMIT;                                  ->  COMMIT
  T2:                                                                          ->  UPDATE 1 (after T1)
  ```

  That is an obligation on the application's `Generations`, which is where
  [[D-126]] puts it and where `Park`'s ordering obligations already live — it is
  a sentence in the contract and a line in the recipe, not a change to this
  package.
- **Why this timing:** S6 writes
  `TestARetiredGenerationStopsStagingAtTheCutover` (§6.11) and
  `TestAnOwnershipRowOverASecondPoolLeavesTwoSenders` (§UC-192) against this
  claim. Written before the obligation exists, the first will pass by scheduling
  luck — a live test that cuts over between passes never enters the window — and
  will be read afterwards as evidence the boundary is closed. §UC-192 is
  advertised as *the* measured boundary of the guarantee, and it measures the
  wrong one: a second pool, not a first pool read without a lock.
- **Close criteria:**
  - [ ] The `Generations` doc comment states what `Active` must do for the
        suppressor to be a boundary: read the row under a lock that a concurrent
        `Activate` waits on, or be called inside a `SERIALIZABLE` unit — named as
        an obligation on the implementation, the way `Park`'s ordering obligations
        are.
  - [ ] `event/projection/effect.go`'s *"the loser's whole unit rolls back"*
        sentence is replaced by one that is true of an unlocked read, or by one
        that states the obligation it depends on.
  - [ ] [SPEC] §1.6 part 2 and §UC-173's **Then** say which read closes the
        window; §UC-192's boundary is restated so that "reached over a second
        pool" is not the only way to lose it.
  - [ ] A live case drives the interleaving rather than hoping to miss it: the
        cutover committed while a retiring pass is open between its ownership read
        and its commit, asserting either one staged effect per envelope across
        both generations, or — if the obligation is left to the application — that
        the documented locking recipe is what makes that true, with the unlocked
        recipe as the control that shows two.

---

### GAP-2 [high][immediate] The blocking applier's staging arm has no falsifier, and it is the arm a degraded projection runs on every page

- **Where:** `event/projection/pass.go:475-502` (`unblockedPage`, `held.owed =
  free`), against `event/projection/effect_test.go:293-363`
  (`TestAParkedEnvelopeIsNotStagedAndIsNotLost`), which reaches
  `sequenceBySequence` and never reaches `unblockedPage`.
- **What:** there are two paths on which the loop parks an envelope. The isolation
  pass (`sequenceBySequence`, taken on the page whose handler failed permanently)
  is falsified: appending the parked envelope to `held.owed` there fails
  §UC-181 with *"the sink took [[A1] [A2 B1]]"*. The blocking path
  (`unblockedPage`, taken on **every later page while the queue holds anything**,
  which is §UC-147's shape) is not falsified by anything. Mutating
  `held.owed = free` to `held.owed = this.matched` — staging an effect for every
  envelope the page **parked without calling the handler** — leaves
  `go test ./event/projection/` green. A probe of §UC-147's shape catches it at
  once:

  ```
  PROBE P1 sink pages: [[A1] [A3 B1]]     <- A3 was parked, never applied, and staged
  PROBE P1 read model: [A1 B1]
  PROBE P1 letters of a: 2
  ```
- **Why this severity:** the property left unpinned is §INV-106 and §UC-181's
  **Must not** — *"the framework would be telling the world about a state its own
  read model refused"* — on the path a projection with a parked sequence takes for
  as long as the operator has not drained it, which may be hours. The mutation
  above is not exotic: `free` and `this.matched` are the two slices in scope three
  lines apart, and the whole point of the applier is that they differ. The plan's
  own "Falsified rather than asserted" table lists *"a parked envelope's effect is
  staged with the applied ones → FAIL"*, so the section believes this arm is
  covered; it is the other arm that is.
- **Why this timing:** it is one `t.Run` in a file this section owns, and the
  probe that catches it is fifteen lines. Left open, S6 builds the live park cases
  on the assumption the pair is pinned, and the first change to `unblockedPage` —
  which ES-03's hot path invites — ships green.
- **Close criteria:**
  - [ ] An arm of `TestAParkedEnvelopeIsNotStagedAndIsNotLost` (or a sibling
        named for §UC-147) delivers a page to a projection whose queue **already**
        holds the sequence, so `unblockedPage` runs, and asserts `Stage` took the
        free envelopes and not the parked one.
  - [ ] The mutation `held.owed = this.matched` in `unblockedPage` is applied,
        run, and reported failing that arm by name, with the message quoted.
  - [ ] The control that the same page with an empty queue stages both, so the
        exclusion is attributable to the queue.

---

### GAP-3 [high][immediate] A redrive accepts a barrier, and the only thing a barrier can do there is drop an effect that is owed

- **Where:** `event/projection/redrive.go:110-118` (`RedriveSpec.EffectsAfter`),
  `redrive.go:144` (`unstageable(...)`, which carries **two** of the loop's
  refusals), `redrive.go:308-319` (`Redrive.staging`), against
  `event/projection/spec.go:292-294`, where the loop refuses `EffectsAfter > 0`
  beside `OnPermanentFailure: ParkSequence`.
- **What:** a letter exists only because a loop parked it, and a loop can only
  park under `ParkSequence`, which `New` refuses to combine with a barrier. So
  every letter a redrive can ever drain was parked by a generation that had **no**
  barrier, and every one of them is owed an effect. `NewRedrive` nonetheless
  accepts `EffectsAfter`, and what it then does is suppress exactly those effects,
  permanently — the loop advanced over those positions and will never see them
  again. Driven:

  ```
  PROBE P5 retried={Sequence:orders/a Applied:2 Left:0 Cause:<nil>} err=<nil>
  PROBE P5 applied rows=[A2 A3] staged=[] calls=0
  ```

  Two letters applied into the read model, the sink not called once, no error on
  any path. `grep -rn EffectsAfter --include=*_test.go` shows no test sets the
  field on a `RedriveSpec`: the field shipped with no arm at all, while §UC-182's
  **Given** names it explicitly and the section's *Realises* line claims
  "`RedriveSpec`'s three".
- **Why this severity:** this is the second of the two failures [SPEC] §1.6
  enumerates — *"the effect is lost for ever … a permanently-failing park quietly
  drops an unbounded set of live effects with a read-model completeness counter as
  the only trace"* — reachable through a field this phase added, with the symmetric
  door refusing the symmetric wiring. `effect.go:163-167` states the rule as *"a
  redrive stages through the same three suppressors a pass does, so it refuses the
  same two wirings"*; the wiring it does not refuse is the one that is true of
  **every** redrive, because a redrive always has a queue. The same plan text that
  motivates it — *"a composition root builds one spec for a live generation and a
  rebuild"* — is the shape that puts one `EffectsAfter` into two spec builders.
- **Why this timing:** it is a public field of a public spec. Refusing it later is
  a breaking change to a shipped surface; refusing it now, in the same `unstageable`
  the two other refusals already live in, costs one condition and one arm. S6
  regenerates `docs/api/surface.md`, after which the field is a published line.
- **Close criteria:**
  - [ ] Either `NewRedrive` refuses a non-zero `EffectsAfter` — naming the rule
        (a letter is parked by a generation that could not have had a barrier, so
        a barrier here can only drop an effect that is owed) — or the field is
        removed and §UC-182's **Given** is corrected.
  - [ ] If it is kept in any form, an arm of
        `TestARedriveStagesWhatItAppliesAndAnEvictionStagesNothing` exercises it
        and states what it is for, with a control.
  - [ ] `TestNewRefusesEverySpecItCannotAssemble`'s redrive sibling counts the new
        refusal, inside S5's counted `EFF` pattern.

---

### GAP-4 [high][immediate] The barrier is a spec field, not a recorded position, and [SPEC] says it is a checkpoint column

- **Where:** `event/projection/spec.go:145-151` (`EffectsAfter`, *"Both sides of
  the comparison are durable"*), `event/projection/effect.go:75-93` (`gate.past`,
  *"Both sides of that comparison are durable — a checkpoint column against a
  spec's own position"*), [SPEC] §1.6 part 3 (*"Both sides of the comparison are
  **checkpoint columns**, so an interrupted warm-up resumes suppressed rather than
  re-firing from the beginning"*), and `event/projection/generation.go:56-59`
  (`Barrier.At`, *"compared with `>=` against another generation's
  `Progress.Highest` and with nothing else"*).
- **What:** one side of the comparison is a checkpoint column — the envelope's
  position, reached through the resumed cursor. The other is a constant in the
  caller's deployment configuration. Nothing records which barrier a generation
  was warmed up under, nothing derives it from the prior generation's mark the way
  Marten's `GateSideEffectsBehindPriorVersion` snapshots `N`, and nothing refuses a
  restart whose `EffectsAfter` is lower than the one the same row was warmed up
  with. The one shipped value that answers "where did the prior generation get to"
  is `Barrier.At`, whose own comment forbids this use.
- **Why this severity:** study §ES-06 nuance 1 is the nuance the appendix calls the
  hard part, and its failure mode is named: *"fires every side effect of the
  remaining history the first time the process is restarted, and that failure only
  shows up in production."* vv's shape moves that failure from a restart to a
  configuration change, which is a real improvement and is not the same as closing
  it: a redeploy that drops `EffectsAfter` (a rollback of the release that added
  it, a config map that lost a key, a second generation built from a spec builder
  whose default is zero) re-stages an effect for **every** event of the warmed-up
  history at once, with no error, no refusal and no record. The concrete input is
  ordinary: `EffectsAfter: 9` on Monday, absent on Tuesday, and the whole log
  below position 9 is staged on Tuesday's first pass. Two shipped comments and one
  [SPEC] paragraph say this cannot happen because both sides are durable.
- **Why this timing:** S6 writes `TestAnInterruptedWarmUpResumesSuppressed`
  (§6.12, §UC-171) against the process-kill half, which the current shape does
  satisfy; shipped beside a sentence claiming the whole property, it will be read
  as proof of the half that is missing. And if the answer is to record the
  barrier, it is a checkpoint-row question, which is a kernel conversation better
  had before S6 freezes the live surface.
- **Close criteria:**
  - [ ] Either the barrier a generation is warmed up under is **recorded** where
        the framework can read it back and compare, or the two code comments and
        [SPEC] §1.6 state plainly that `EffectsAfter` is a deployment-held constant
        and that lowering or dropping it re-stages the history below it.
  - [ ] If it stays a constant, [SPEC] names where an operator gets `N` — and
        `Barrier.At`'s *"compared with … nothing else"* sentence is reconciled with
        it, because `Observe` is the only shipped way to read a generation's mark.
  - [ ] A use case with the concrete input: the same generation restarted with
        `EffectsAfter` dropped to zero, asserting what is staged, so the boundary
        is measured rather than implied — the way §UC-184 and §UC-193 measure
        theirs.

---

### Recorded to the backlog, not blocking

`## P4` items **63–68**: the `FL-038` reverse-index row for `effect.go` pointing
at a flow whose body never mentions it, beside `FL-039` already being i18n's while
S6's plan promises that number to the effect flow (63); `Spec.Classifier` now
classifying the sink's failures as well as the handler's, undocumented (64);
`Effects` accepted beside `Destination: Unchecked`, which §1.6 says must be
"stated, not hidden", and no shipped comment states it (65); `EffectsFunc`
exported and exercised by nothing (66); the checkpoint transcript filtered of the
`scripts` failure of the command it quotes (67); and
`TestAnUngeneratedProjectionWithGenerationsStopsStagingAtTheCutover`'s failure
message claiming the row is read "inside the transaction that commits its own
advance", which the `ownership` fake — it ignores the context entirely — cannot
show (68).

---

## Round 2 — closures — 2026-09-12

All four blocking findings are closed. Each was **reproduced first** — the two
interleaving ones driven rather than reasoned about — then fixed, then left with
a test that fails if the fix is reverted. Nothing was rejected and nothing was
deferred; the six `[medium]`/`[low]` items (63–68) stay in the backlog under
`## P4` and were not touched.

### GAP-1 — closed

**Reproduced twice.** In `psql` on PostgreSQL 17.9, two live sessions over
`own_row`/`staged_effects`:

```
T1 read active=1 at 22:18:22.871   (plain)     T2 update active=2  -> UPDATE 1, committed at once
T1 insert staged_effect; COMMIT    -> COMMIT   staged_effects=1, active=2, nothing rolled back

T1 read active=1 at 22:18:22.871   (for share) T2 cutover issued at 22:18:23.810
T1 commits        at 22:18:25.814              T2 cutover returned at 22:18:25.815
```

The locking read is what orders the two: the cutover returned one millisecond
**after** the staging unit committed, and under the plain read it did not wait at
all. At the Go level the same interleaving is now a test rather than a probe.

**Fixed as a contract, because the boundary is the application's** — [[D-126]]
forbids this framework choosing an isolation level and `Generations` is an
interface the application implements:

- `event/projection/generation.go` — the `Generations` doc comment now states
  what `Active` must do for the suppressor to be a boundary: a locking read
  (`FOR SHARE` / `FOR KEY SHARE`) or a `SERIALIZABLE` unit, named as an
  obligation on the implementation the way `Park`'s ordering obligations are,
  with the reason (`READ COMMITTED` does not conflict; `REPEATABLE READ` does not
  either) and with the measurement-by-consequence sentence.
- `event/projection/effect.go` — *"the loser's whole unit rolls back and takes
  its staged row with it"* is gone. What replaces it says the row this delivery
  staged under is the row its unit commits under **when the implementation's read
  is the locking one**, and then says what the boundary is **not**: it does not
  promise the arriving generation will not stage the same envelope when it
  reaches it. That overlap is Marten's accepted window, it is §1.5's, and
  draining closes it.
- `event/projection/doc.go` — the package sentence carries the same two halves.
- [SPEC] §1.6 part 2 rewritten: which read closes the window, why the mechanism
  alone does not, and the overlap it never addressed. §1.5's `Generations` bullet
  and §5's `Generations` block point at it. §UC-173's **Then** now says which
  read makes "no envelope is staged by both" true. §UC-192's **Must not** now
  says a second pool is not the only way to lose the boundary.
- **§UC-202 is new** and is the pair of recipes measured against each other.

**Left behind:** `TestTheOwnershipReadIsABoundaryOnlyWhenACutoverWaitsForIt`,
two arms, each the other's control. The interleaving is driven with channels and
a `sync.Cond`, not a sleep: the retiring pass is held inside `Active`, the
cutover is issued from a goroutine, the arriving pass reads across it. Locking →
`retiring=[A1 A2 A3]`, `arriving=[]`, and the cutover's write returned with A3
already staged. Plain → `retiring=[A1 A2 A3]`, `arriving=[A3]`, and the write
returned with only `[A1 A2]` staged. Twenty-five runs under `-race`, green.
Mutating the third suppressor away fails **both** arms with *"the arriving
generation staged [A1 A2 A3] beside the retiring one, and no envelope is staged
by both"*.

### GAP-2 — closed

**Reproduced:** `held.owed = free` → `held.owed = this.matched` in
`unblockedPage`, `go test ./event/projection/` **green**, exactly as reported.

**Fixed with the missing arm** rather than with code — the code was right. A
third arm of `TestAParkedEnvelopeIsNotStagedAndIsNotLost`, named for §UC-147,
delivers `A3 C1` to a projection whose queue already holds `a`: `unblockedPage`
runs, `A3` is parked without reaching the handler, `Stage` takes `C1` alone, the
read model holds `[A1 B1 C1]`, and the queue holds two letters of `a` with `A3`
second. The existing control arm gained the same third page with an empty queue,
so both exclusions are attributable to the queue. [SPEC] §1.6's "an effect
follows its envelope" bullet and §UC-147's **Then** now name both appliers, and
§INV-101/§INV-106's **Falsified by** lists say which is which.

**Left behind:** with the mutation applied the arm fails with *"the sink took
[[A1] [B1] [A3 C1]], where the page A3 C1 owes an effect for C1 alone — A3 never
reached the handler at all"*; restored, it is green under `-race`.

### GAP-3 — closed, by refusing the field rather than removing it

**Reproduced:** `PROBE P5 retried={Sequence:orders/a Applied:2 Left:0
Cause:<nil>} err=<nil>` with `applied rows=[A2 A3] staged=[] calls=0` — two
letters applied, the sink never called, no error on any path.

**Fixed at the door.** `NewRedrive` now refuses a non-zero `EffectsAfter`,
naming the rule: a letter is in the queue because a loop parked it, a loop parks
only under `ParkSequence`, `New` refuses that beside a barrier, so every letter a
redrive can drain was parked by a generation that had none and is owed its
effect. `unstageable` keeps the one refusal both doors share and its comment says
where they part; the barrier-with-nothing-to-gate refusal moved to
`refusedEffects`, which is the loop's alone. [SPEC] §1.6, §5's `RedriveSpec`
block, §UC-182 and the plan's `redrive.go` contract block are corrected from "the
same three".

**Kept rather than removed, deliberately.** A compile error would be stronger,
but reverting the fix would then be invisible to every test, and the field is
exactly what a spec builder serving both doors copies across — a refusal at that
call site names the rule where a missing field names nothing. The trade is
written down in `RedriveSpec`'s own comment.

**Left behind:** an arm of `TestARedriveStagesWhatItAppliesAndAnEvictionStagesNothing`
with two controls — the identical spec at zero constructs, and `New` accepts the
very barrier this door refuses. Dropping `refusedBarrier` fails it with *"a
redrive carrying a barrier was accepted, and what it then does is apply a letter
and stage nothing"*.

### GAP-4 — closed by naming the constant, and the alternative is refused on the record

**Reproduced:** a generation warmed up under `EffectsAfter: 4` staged nothing
over positions 1–2; the same generation restarted from its own rows with the
field dropped staged `[A3 A4 A5]` — two of those below the barrier it was warmed
up under — with no error, no refusal and no record.

**Recording the barrier is refused here, and this is the argument.** The close
criterion offers it first and it is the better answer in the abstract: it is what
Marten's `GateSideEffectsBehindPriorVersion` does. It is out of scope for this
phase for a reason that is not convenience — the only place the framework could
read a barrier back from is a checkpoint column, and §5.1 freezes the `event`
surface for the whole of phase 4 (`docs/api/surface.md`'s `event` section
byte-identical, `check-event-kernel` over every top-level kernel file). The other
candidate, widening `Generations` with a barrier method, puts a durable write and
a new obligation on every application that stages effects, and it cannot cover
the `Ungenerated` and second-`Spec.Name` spellings where `Generations` is
optional — so it would close the case the document already measures and miss the
ones it does not. Deferring it is therefore a scope statement, not a
simplification, and it is recorded here rather than in the backlog because the
*second* half of the criterion is delivered in full:

- `Spec.EffectsAfter`, `gate.past` and `doc.go` now say plainly that the
  envelope's side of the comparison is a checkpoint column and **this side is a
  deployment-held constant**, that nothing records what a generation was warmed
  up under, and that lowering or dropping it stages the rest of the warm-up with
  nothing refusing it.
- [SPEC] §1.6 part 3 is rewritten to match, and it names where an operator gets
  `N`: the prior generation's mark, which `Observe` answers as `Barrier.At`.
- `Barrier.At`'s *"compared with … and with nothing else"* is reconciled with
  that — the sentence is now about what **this package** does with it, and the
  one operator use is named as a deployment-time act rather than a call.
- §INV-101's title and statement no longer say "durable at every end it has".

**Left behind:** `TestABarrierDroppedFromTheSpecRestagesTheWarmUpBelowIt`, two
arms — the field dropped stages `[A3 A4 A5]`, the field still set stages `[A5]` —
so the boundary is measured rather than implied. §UC-203 is the use case, and
§6.12 now carries the same arm live so the interrupted-warm-up test is not read
as proof of a durability the barrier does not have.

**Status: closed** — GAP-1, GAP-2, GAP-3, GAP-4. Six `[medium]`/`[low]` items
(63–68) remain in `EVENTSOURCE_BACKLOG.md` under `## P4`, untouched.
