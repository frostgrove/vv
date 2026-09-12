# FL-042 — An applied envelope becomes a staged effect

**Entry points:** `projection.Spec.Effects` (the capability, as a field),
`projection.Effects.Stage` (the application's own sink),
`projection.Generations.Active` (the ownership row),
`projection.Redrive.Sequence` / `Redrive.Any` (the other place a stage happens)
**Governed by:** [[D-092]] [[D-118]] [[D-126]] [[D-130]] [[D-131]] [[D-133]]
[[D-141]]

What happens between an envelope a handler applied and a row in the
application's own table that says the world still has to be told — the three
suppressors checked cheapest first, the barrier that is per envelope and only
half durable, the ownership read that is the two-sender boundary, and the four
rollback paths that make `Stage` a durable write and nothing else.

[[FL-038]] is the loop this rides inside: the pass, the unit of work, the fence
and the settlement. [[FL-035]] is what drains a staged job afterwards, and the
dial-out is its, with its own retry and its own idempotency. This flow starts
where a handler has returned and ends where the unit commits.

## The capability is a field, and a Handler holds no route to one

`event/projection/spec.go` and `event/projection/page.go`.

`Spec.Effects` is a `projection.Effects` — one method, `Stage(ctx, Effect)`. A
`Handler` is handed a `Batch`, and a `Batch` carries a projection name, an
identity, envelopes and an attempt. **There is no field on it through which an
effect could be caused.** So "a rebuild does not get the effect-dispatch
capability" is not a rule a rebuild follows; it is the shape of the value it was
handed, and a rebuild is a spec with `Effects` nil.

`New` refuses four combinations before anything runs, and each names its field:

- `Effects` beside `Advance: AfterApply` — there is no transaction to stage into,
  so the stage, the handler's writes and the advance are three commits.
- `Effects` at a generation other than `Ungenerated` with no `Generations` —
  that pair is exactly what admits two senders.
- `EffectsAfter` with a nil `Effects` — a barrier with nothing to gate.
- `EffectsAfter` beside `OnPermanentFailure: ParkSequence` — a warm-up that
  parked an event produces a generation whose rows are not comparable to the live
  one, and the comparison is a cutover's only evidence.

## The gate, cheapest first

`event/projection/effect.go:gate.stage`, reached from
`event/projection/pass.go:Projection.staging` — **from inside the unit and from
nowhere else.**

1. **A nil `Effects` costs nothing.** No call, no allocation, no round trip. This
   is the rebuild's path and it is the common one.
2. **`Spec.EffectsAfter`, per envelope.** The applied envelopes at or below the
   barrier are dropped and the rest are kept. A page that straddles it calls
   `Stage` **once** with exactly the envelopes past it; a page entirely at or
   below it does not call `Stage` at all, **not even with an empty slice**, which
   is what makes a suppression free rather than a round trip that is told to do
   nothing. Per page would be wrong in both directions: staging the whole page
   double-fires over history the prior generation covered, and staging none of it
   loses the first real effects of the run.
3. **The ownership row**, and only when there is something left to stage. It is
   the one round trip the gate can cost, so it is last:
   `Generations.Active(ctx, spec.Name)` is asked through the context the unit
   gave it, and the delivery stages only when the answer equals this identity's
   own generation. A projection with no row answers `Ungenerated` and a nil
   error, which is this projection's own generation before any cutover — so it
   stages exactly as it would with no field at all, and stops the moment the row
   names another.

Only then is `Effects.Stage` called, with an `Effect` carrying the identity, the
envelopes this delivery **applied** past the barrier, and the attempt.

## The two obligations the gate cannot enforce

`event/projection/generation.go`, on `Generations`, and they are the
implementation's the way `Park`'s ordering obligations are.

**The read must go through the ambient transaction.** An implementation that
opens its own connection answers correctly and out of the unit. The framework
holds a method set and no resource, so it cannot compare that implementation's
transaction to its own, and a self-reported "am I in your transaction?" would be
answered by the same code that is wrong. What measures it is a rollback rather
than a self-report.

**`Active` must be a LOCKING read** — `SELECT active … FOR SHARE`, `FOR KEY
SHARE` — or run in a `SERIALIZABLE` unit. [[D-126]] leaves the isolation level to
the caller, and at `READ COMMITTED` a plain read of this row and a concurrent
`Activate` of it do not conflict: both commit, and a generation the row no longer
names commits the effect it staged anyway. `REPEATABLE READ` does not close it
either. Under a locking read the cutover's `UPDATE` waits behind every unit that
read the row, so the generation that staged is the generation that owned the row
for the whole of its unit.

## The barrier, and which half of it is durable

`event/projection/spec.go:Spec.EffectsAfter`.

The **envelope's** side is durable: it is where the resumed checkpoint left this
generation, so a warm-up interrupted below the barrier resumes suppressed rather
than firing from the beginning. The **barrier's** side is a constant the
deployment holds. Nothing stores the barrier a generation was warmed up under and
nothing compares a restart's value against it, so a release that lowers or drops
the field stages an effect for every envelope of the warm-up still below it, on
the first pass, with no error and no refusal.

Where an operator gets the number is the one shipped source: `Observe` answers
the mark the **prior** generation delivered to, as `Barrier.At`, and a human
writes it into the constant at deployment time. It is not read at run time and it
is not a resume point ([[D-129]]).

## The redrive stages what it applies, and an eviction stages nothing

`event/projection/redrive.go:Redrive.staging`, through the same `gate`.

An effect belongs to an applied envelope rather than to a page, so a parked
envelope is not staged now and is not lost either: its letter carries it, and the
redrive that applies it is what stages it. The apply, the stage and the `Evict`
ride **one** unit, so a crash anywhere between them leaves the letter parked,
nothing applied and nothing staged — which is the outcome the next redrive starts
from.

An **eviction stages nothing, ever.** A skip is an operator saying the event will
never be applied, and an effect for an event nothing applied is precisely the
thing the capability exists to prevent.

`RedriveSpec` carries `Effects` and `Generations` and **refuses**
`EffectsAfter`, at the door, because the field a composition root copies from a
`Spec` is the one it can only get wrong: a barrier over a queue suppresses an
effect that is owed and nothing will ever offer it again.

## Four rollbacks, which is why `Stage` is a durable write

A lost fence ([[D-133]], the expected outcome of a rolling deploy rather than an
exotic one); a full park (`ErrParkFull`); a later envelope's permanent failure in
the same page; and a serialisation failure the caller's own `Unit` answers. Every
one of them takes the staged row back with the advance and the read model's rows.
An HTTP call, a payment or a mail here is sent again on each of them.

## The contract is not a sandbox

Nothing here can stop a handler dialling out. What the shape does is make the
honest thing the easy thing and the dishonest thing visible in a review. The half
that *is* enforced is the framework's own reach, and it is a walk:
`TestNoPackageOnTheProjectionPathCanDispatch` type-checks `event/projection` and
everything it reaches and asserts that no package on that path imports `net`,
`net/http`, `net/smtp` or `os/exec`, transitively — with a fixture importing
`net/http` that the same walk reports.

## Traps

- **`Stage` is not `Send`.** The method is named for where it runs. Anything
  irreversible in it is sent again on four rollback paths.
- **The ownership row is per PROJECTION, not per generation.** It names the
  winner, so a row per generation could not.
- **A rebuild spelled as a second `Spec.Name` is not covered**, and stages an
  effect for every historical event. The recipe that is covered is
  `Spec.Generation`.
- **The suppressor is gated on `Generations` being supplied**, never on
  `Spec.Generation` — otherwise it would never run for the projection every
  deployment actually has, and the only migration one has is
  `Cutover(From: Ungenerated, To: 2)`.
- **Add `Generations` one release before the first cutover.** The blue/green
  ordering is recorded beside [[D-131]].

## Files

| File | What it holds |
|---|---|
| `event/projection/effect.go` | `Effect`, `Effects`, `EffectsFunc`, `EffectsFunc.Stage`, `gate`, `gate.stage`, `gate.past` — the capability as a value, and the three suppressors in one place beside the redrive that stages through the same one |
| `event/projection/generation.go` | `Generations` and its two obligations, `Generation`, `Ungenerated` — and, for [[FL-038]], the barrier, the readiness and the switch |
| `event/projection/spec.go` | `Spec.Effects`, `Spec.EffectsAfter`, `Spec.Generations` and the four refusals `New` collects for them |
| `event/projection/pass.go` | `Projection.staging`, `Projection.claimed` — where the gate is reached from, and why it sits directly after the handler in both orders |
| `event/projection/redrive.go` | `RedriveSpec.Effects`, `RedriveSpec.Generations`, `refusedBarrier`, `Redrive.staging` — the letter's own effect, staged in the unit that applies and evicts it |
| `event/projection/page.go` | `Batch`, which carries no route to an `Effects` |
| `scripts/projection_test.go` | `TestNoPackageOnTheProjectionPathCanDispatch` and its `net/http` fixture control |
| `_examples/event-generations/main.go` | the ownership row, the barrier, the cutover and the rollback, with the sink that stages a job and says what it may not be |

## Tests that walk this flow

Untagged, in `make unit`: `event/projection/effect_test.go` and
`event/projection/generation_test.go`; `scripts/projection_test.go` for the
import walk.

Behind `//go:build integration`, against a live PostgreSQL:
`event/eventpg/effect_integration_test.go` and
`event/eventpg/generation_integration_test.go`.

```sh
FROSTGROVE_EVENTPG_TEST_DSN='postgres://vv:vv@localhost:55432/vv?sslmode=disable' \
  go test -race -count=1 -tags=integration ./event/eventpg/
```

### Proved by

| What holds | Proved by |
|---|---|
| the live generation stages and the rebuild does not, and the two suppressors are isolated from each other | `TestTheLiveGenerationStagesAndTheRebuildDoesNot` |
| `Effects` is refused beside `AfterApply`, a barrier with nothing to gate is refused, and a barrier beside a park is refused | `TestEffectsAreRefusedBesideAfterApply`, `TestABarrierWithNothingToGateIsRefused`, `TestABarrierBesideAParkIsRefused` |
| the barrier is per envelope: a straddling page stages exactly what is past it and a page below it costs no call at all | `TestAStraddlingPageStagesExactlyTheEnvelopesPastTheBarrier`, `TestAStraddlingPageStagesExactlyThePastBarrierEnvelopesLive` |
| a parked envelope is not staged and is not lost, a redrive stages what it applies, and an eviction stages nothing | `TestAParkedEnvelopeIsNotStagedAndIsNotLost`, `TestARedriveStagesWhatItAppliesAndAnEvictionStagesNothing` |
| the ownership read costs nothing when there is nothing to stage | `TestTheOwnershipRowIsNotReadWhenThereIsNothingToStage` |
| one side of the barrier is a deployment-held constant, and a restart under a lower one re-stages the warm-up below it | `TestABarrierDroppedFromTheSpecRestagesTheWarmUpBelowIt`, `TestAnInterruptedWarmUpResumesSuppressed` |
| the ownership row is a boundary only when a cutover waits for it, measured live as one case two arms apart | `TestTheOwnershipReadIsABoundaryOnlyWhenACutoverWaitsForIt`, `TestARetiredGenerationStopsStagingAtTheCutover` |
| a row reached over a second pool loses the boundary, asserted rather than warned about | `TestAnOwnershipRowOverASecondPoolLeavesTwoSenders` |
| a generation-zero projection is retired by its first cutover, and the identical spec with no `Generations` keeps staging | `TestAnUngeneratedProjectionWithGenerationsStopsStagingAtTheCutover` |
| a rebuild spelled as a second `Spec.Name` stages every historical event, which is the measured edge of the guarantee | `TestASecondProjectionNameWithEffectsStagesEveryHistoricalEvent` |
| the staged job, the read model's rows and the advance commit together, and a lost fence takes all three back | `TestAStagedJobTheReadModelAndTheAdvanceCommitTogether`, `TestAStageRolledBackByALostFence` |
| no package on the projection path can dispatch | `TestNoPackageOnTheProjectionPathCanDispatch` |
