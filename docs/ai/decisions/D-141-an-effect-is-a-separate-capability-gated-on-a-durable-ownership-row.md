# D-141 — An effect is a separate capability, gated on a durable ownership row

**Status:** accepted
**Invariant:** The capability to cause a side effect is a **value** — a
`projection.Effects` on the spec — and a `Handler` is handed a `Batch` that holds
no route to one. A rebuild is a spec with that field nil, so there is nothing to
withhold from it because there is nothing to hold. `Stage` is called inside the
transaction that commits the advance, is contracted to make a durable write and
nothing a rollback cannot take back, and is suppressed by three things checked
cheapest first: a nil `Effects`, `Spec.EffectsAfter` per envelope, and the
ownership row read through the ambient transaction. The framework contracts
against dispatch and does not sandbox it.

## The decision

### Why the capability is a value rather than a mode

A replay must not resend the emails the original delivery sent. Both references
answer this and they answer it differently.

Marten's answer is the safe default: side effects are opt-in per projection, and
a rebuild simply does not run them. Axon's is a flag — `ResetContext` /
`ReplayToken` — which the handler is expected to consult, and a handler that
forgets to consult it sends the mail. The difference is where the mistake lives:
in Marten's shape a rebuild that dispatches is a wiring somebody wrote down, and
in Axon's it is a missing `if` in one handler out of forty.

vv takes Marten's side and goes one step further. A mode is a thing a handler can
read and a handler can ignore. A **capability** is a thing a handler does not
have: `Handler.Apply` is handed a `Batch`, and a `Batch` carries a projection
name, an identity, envelopes and an attempt. There is no field on it through
which an effect could be caused, so "a rebuild does not get the effect-dispatch
capability" is not a rule the rebuild follows — it is the shape of the value it
was handed.

### `Stage`, not `Send`, and not `Dispatch`

The method is named for where it runs. It is called **inside** the transaction
that commits the advance, so what it does must roll back with it: a staged job
([[D-118]]), a row in the application's own tables. This framework's outbox is a
durable write inside the caller's bound transaction and never a second
durable-intent table.

An externally visible irreversible action here — an HTTP call, a payment, a mail,
a non-transactional publish — is sent again on **every** rollback the delivery
can take, and there are four:

1. **A lost fence** ([[D-133]]). This is the expected outcome of a rolling deploy
   rather than an exotic one: two live instances of one name for a few seconds,
   on purpose, and the loser's whole unit rolls back after `Stage` returned.
2. **A full park.** `ErrParkFull` ends the pass without advancing and without
   parking, and everything the pass did goes back with it.
3. **A later envelope's permanent failure** under `ParkSequence`, in the same
   page.
4. **A serialisation failure the caller's own `Unit` answers**, which [[D-126]]
   leaves to the caller precisely because the isolation level is theirs.

`TestAStageRolledBackByALostFence` measures the first of those live: the loser's
staged row is absent, its advance unmoved, its read-model rows gone, and the
winner's staged exactly once.

**This is a contract and not a sandbox.** Nothing here can stop a handler
dialling out; what the shape does is make the honest thing the easy thing and the
dishonest thing visible in a review. The half that *is* enforced is the
framework's own reach: `TestNoPackageOnTheProjectionPathCanDispatch` walks
`event/projection` and everything it reaches with `go/types` and asserts that no
package on that path imports `net`, `net/http`, `net/smtp` or `os/exec`,
transitively — with a fixture control importing `net/http` that the same walk
reports, so a walk that resolved nothing is not read as a clean tree.

### `AfterApply` is refused, and Reject 2's window is the reason

`Effects` beside `Advance: AfterApply` is `ErrSpec` at construction. Under
`AfterApply` there is no transaction to stage into: the handler's writes commit,
the stage commits separately, and the advance commits after both. A crash or a
broker outage between them leaves an effect that is owed and a checkpoint that
has moved past it, and nothing will ever offer that envelope again. That is the
dual-write window the capability exists to close, and admitting the weaker tier
would scope the guarantee to half the shipped matrix while the documentation
promised all of it.

### The barrier is per envelope, and per page is wrong

`Spec.EffectsAfter` suppresses every envelope **at or below** it, per envelope
and never per page. A page that straddles the barrier is the case that decides
it: staging the whole page double-fires over history the prior generation already
covered, and staging none of it loses the first real effects of the run.
`TestAStraddlingPageStagesExactlyThePastBarrierEnvelopesLive` asserts `Stage` is
called **once** with exactly the envelopes past the barrier while the handler is
called with all of them, and its control asserts that a page entirely below the
barrier does not call `Stage` at all — not even with an empty slice, which is
what makes a suppression free rather than a round trip that is told to do
nothing.

**One side of that comparison is durable and the other is not, and the two are
stated in the same breath.** The envelope's side is where the resumed checkpoint
left this generation, so a warm-up interrupted below the barrier resumes
suppressed rather than firing from the beginning
(`TestAnInterruptedWarmUpResumesSuppressed`). The barrier's own side is a
**constant the deployment holds**: nothing stores the barrier a generation was
warmed up under and nothing compares a restart's value against it, so a release
that lowers or drops the field stages an effect for every envelope of the warm-up
still below it, on the first pass, with no error and no refusal. That is asserted
rather than left implied — the same restart with `EffectsAfter` dropped stages
all of `(K, N]` — because without it the interrupted-warm-up property is read as
a durability the barrier does not have.

Where an operator gets the number is the one shipped source: `Observe` answers
the mark the **prior** generation delivered to, as `Barrier.At`, and a human
writes it into the constant at deployment time. It is not read at run time, and
it is not a resume point: a position is an ordinal the log assigned and a cursor
is bytes a store minted ([[D-129]]).

`EffectsAfter` is refused with a nil `Effects` (a barrier with nothing to gate is
a spec assembled wrong) and refused beside `ParkSequence` (a warm-up that parked
an event produces a generation whose rows are not comparable to the live one, and
the comparison is a cutover's only evidence).

### An effect follows its envelope, including into the park

`Effect.Envelopes` are the ones this delivery **applied**, past the barrier, and
no others. A parked envelope is not among them and is not lost either: its letter
carries it, and the redrive that applies it is what stages it, in the same
transaction as the apply and the eviction. An **eviction stages nothing, ever** —
a skip is an operator saying the event will never be applied, and an effect for
an event nothing applied is precisely the thing the capability exists to prevent.

`RedriveSpec` therefore carries `Effects` and `Generations`, and **refuses**
`EffectsAfter` at the door. The field a composition root copies from a `Spec`
into a `RedriveSpec` is the one it can only get wrong: a barrier over a queue
suppresses an effect that is owed and nothing will ever offer it again.
`NewRedrive` says so at the call site; a missing field would say nothing at all.

### The ownership row, and the boundary neither reference has

Two generations of one projection both running with `Effects` is the case both
references leave open. Marten calls the overlap "a separate concern"; Axon's
replay flag is per processor and says nothing about a second processor. vv closes
it with a durable row.

`Generations.Active(ctx, projection)` is read **inside the transaction that
commits the advance**, and the delivery stages only when the row names this
generation. A retiring generation therefore stops staging in the same breath as
it commits its own advance — its handler keeps applying and its checkpoint keeps
advancing, and only the capability goes away.

**The precondition travels with the promise, and it is two sentences and not
one.**

First: the row must be read **through the ambient transaction**. An
implementation that opens its own connection answers correctly and out of the
unit, and the framework holds a method set and no resource, so it cannot compare
that implementation's transaction to its own — a self-reported "am I in your
transaction?" would be answered by the same code that is wrong.
`TestAnOwnershipRowOverASecondPoolLeavesTwoSenders` asserts that loss rather than
warning about it.

Second, and it is the sentence the paragraph above cannot be written without:
**`Active` must be a LOCKING read** — `SELECT active … FOR SHARE`, `FOR KEY
SHARE` — or run in a `SERIALIZABLE` unit. [[D-126]] leaves the isolation level to
the caller, and at `READ COMMITTED` a plain read of this row and a concurrent
`Activate` of it do not conflict: both commit, and a generation the row no longer
names commits the effect it staged anyway. `REPEATABLE READ` does not close it
either; a snapshot read of a row another transaction updated raises nothing.
Under a locking read the cutover's `UPDATE` waits behind every unit that read the
row, so the generation that staged is the generation that owned the row for the
whole of its unit.

That pair is measured, live, as one case with two arms one clause of SQL apart
(`TestARetiredGenerationStopsStagingAtTheCutover`): the retiring pass is held
between its ownership read and its commit, the arriving pass reads the same row,
and the cutover's write is issued across both. Under the locking read the cutover
**waits** and the envelope is staged exactly once; under the plain read the
cutover commits between the two reads and **one envelope is staged by both**. The
interleaving is driven rather than awaited, because a cutover taken between two
passes passes by scheduling luck and would be read afterwards as evidence the
window is closed.

`Activate` is fenced and is one statement. A read followed by a write is the one
spelling it may not have: two operators would both read `from`, both find it, and
both write.

### What the boundary does NOT cover, and it is four things

Stated as a section rather than a footnote, because each of them is a live
deployment somebody will assemble.

1. **A rebuild spelled as a second `Spec.Name`.** `Spec.Name: "orders-rebuild"`
   at `Generation: Ungenerated` with `Effects` set stages an effect for every
   historical event. The framework cannot distinguish a second projection name
   from a rebuild of the first, so the gate does not reach that spelling. Closing
   it by requiring `Generations` beside every `Effects` would impose an ownership
   table on every single-sender projection and would still admit this case,
   because the second name's own row answers `Ungenerated`, which is what the
   spec asks for. The recipe the module page teaches after this phase is
   `Spec.Generation`, which the gate does cover.
2. **A `Generations` reached over a second pool**, above.
3. **A `Generations` whose `Active` does not lock**, above.
4. **A live projection carrying `Effects` with no `Generations` at all.** The
   framework holds no route to an ownership row it was not given.

And one overlap the row never addressed: a retiring generation still advancing
past the observed barrier stages envelopes the arriving one has not reached. That
is the read-target window `Cutover` names in its own contract, and what closes it
is draining or stopping the retiring generation before, or as, the switch
commits — `Observe` it twice and see whether the barrier moved.

### The first cutover of every deployment that exists today

The suppressor is gated on `Generations` being **supplied** and never on
`Spec.Generation`. Gated on the generation it would never run for a projection at
`Ungenerated`, which is every projection that exists today, and
`Cutover(From: Ungenerated, To: 2)` — the *only* migration a running deployment
has, because renaming `orders` to `orders@1` changes its checkpoint row key and
resumes it from the origin against a live read model — would leave both
generations staging every event, for ever, with no error on any path.

So a projection at `Ungenerated` whose spec carries a `Generations` reads the row
before the cutover, finds `Ungenerated` — a projection with no ownership row
answers `Ungenerated` and a **nil error**, because that is the state of every
deployment that has never cut over — and stages exactly as it did without the
field. After the cutover the row answers 2 and it stops. One code path for both
cases.

What remains is the operator's, and it is a **release ordering**: add
`Generations` to the live `Ungenerated` spec one release *before* the first
cutover, or that cutover runs two senders until the retiring projection is
stopped. It is the same blue/green shape [[D-131]]'s amendment records for
`Ignore`: the release that prepares comes first, and the release that switches
comes second.

## What this forecloses

- **No mode flag on `Batch` a handler could consult**, and no field on it through
  which an effect could be caused.
- **No `Effects` under `AfterApply`**, at any tier, for any caller.
- **No per-page barrier**, and no `Stage` call with an empty slice.
- **No `EffectsAfter` on a `RedriveSpec`.**
- **No sandbox.** The walk asserts what the framework itself reaches; a handler's
  own imports are its own.
- **No requirement of `Generations` beside every `Effects`**, which is why the
  four uncovered cases above are written down rather than closed.

## Where it lives

- `event/projection/effect.go` — `Effect`, `Effects`, `EffectsFunc`, and `gate`,
  the three suppressors in one place.
- `event/projection/spec.go` — `Spec.Effects`, `Spec.EffectsAfter`,
  `Spec.Generations`, and the refusals beside `AfterApply`, a nil `Effects` and
  `ParkSequence`.
- `event/projection/generation.go` — `Generations`, `Generation`, `Ungenerated`,
  `Barrier`, `Observe`, `Readiness`, `Reached`, `Cutover`, `CutoverSpec`.
- `event/projection/pass.go` — `staging`, where the gate is reached from inside
  the unit and from nowhere else.
- `event/projection/redrive.go` — `RedriveSpec.Effects`,
  `RedriveSpec.Generations`, the `EffectsAfter` refusal, and the stage that rides
  the same unit as the apply and the eviction.

## Proven by

- `TestARetiredGenerationStopsStagingAtTheCutover` (`event/eventpg`, live) — the
  locking arm, the plain-read control, and the no-ownership-row fixture in which
  both senders stage.
- `TestAnOwnershipRowOverASecondPoolLeavesTwoSenders` (`event/eventpg`, live).
- `TestTheReferenceOwnershipRowIsCertified`,
  `TestTheOwnershipHarnessStillDetectsEveryDefectItWasBuiltToDetect` and
  `TestASerializableUnitIsDeclinedRatherThanCertified` (`event/eventtest`) — the
  locking read as a section a consumer can run over its own implementation, the
  six defects the five sections are falsified against, and the declaration that
  is declined rather than skipped.
- `TestTheLiveOwnershipRowSatisfiesTheContract` and
  `TestTheApplicationHarnessesCatchADefectiveImplementation` (`event/eventpg`,
  live) — the same five sections over PostgreSQL, and `plain-ownership-read`
  reported by `locking read`.
- `TestAnInterruptedWarmUpResumesSuppressed` (`event/eventpg`, live) — with the
  fresh-generation control and the dropped-barrier arm.
- `TestAStraddlingPageStagesExactlyThePastBarrierEnvelopesLive`
  (`event/eventpg`, live) — with the entirely-below control.
- `TestAStageRolledBackByALostFence` and
  `TestAStagedJobTheReadModelAndTheAdvanceCommitTogether` (`event/eventpg`,
  live).
- `TestNoPackageOnTheProjectionPathCanDispatch` (`scripts`) — the `go/types`
  import walk, with the `net/http` fixture control.
- `TestTheLiveGenerationStagesAndTheRebuildDoesNot`,
  `TestEffectsAreRefusedBesideAfterApply`,
  `TestAParkedEnvelopeIsNotStagedAndIsNotLost`,
  `TestARedriveStagesWhatItAppliesAndAnEvictionStagesNothing`,
  `TestTheOwnershipRowIsNotReadWhenThereIsNothingToStage`,
  `TestABarrierDroppedFromTheSpecRestagesTheWarmUpBelowIt`,
  `TestASecondProjectionNameWithEffectsStagesEveryHistoricalEvent`,
  `TestAnUngeneratedProjectionWithGenerationsStopsStagingAtTheCutover` and
  `TestTheOwnershipReadIsABoundaryOnlyWhenACutoverWaitsForIt`
  (`event/projection`).

## See also

[[D-092]] [[D-118]] [[D-126]] [[D-130]] [[D-131]] [[D-133]] [[D-140]]
[[FL-038]] [[FL-042]]
