# D-133 — A projection that loses the fence takes turns, and claims the row before it applies

**Status:** accepted
**Invariant:** A checkpoint save the fence refuses does not halt the projection.
The row the winner left is adopted, the reader is rebuilt from **that row's**
cursor, the page in hand is dropped, and the loop backs off — and the streak a
lost fence opens is cleared by a save that lands and by nothing else, so
`Ready` reports a projection that keeps losing. Under `InUnit` the advance is
presented **before** the handler runs, so on a checkpoint store that evaluates
its fenced save against a tuple it is holding — PostgreSQL, measured — the losing
instance never applies the page at all. Under `AfterApply` it already has, and
that is stated rather than prevented.

## The decision

Two live instances of one projection name is the case. `Projection.Declaration`
answers `{Singleton, Durable}`, and that is a promise about a deployment rather
than an enforcement: the checkpoint's fence is what actually holds when a
deployment ignores it. What the fence should *do* was the question, and the tree
answered it twice, differently.

- `event/projection/pass.go` sent an `ErrConflict` from the save to the same arm
  a closed store enters, so the loser **halted permanently**.
- `event/checkpoint.go`'s `Tracker.established` describes the opposite in as many
  words — *"taking its advance is what makes two processes take turns over one
  checkpoint, each re-reading the other's number and saving one above it"*.

Halting is defensible and it was the plan's position: a second writer at one name
is a deployment error, halting is loud, and the blast radius is one duplicated
page rather than a duplicate per page for ever. **It loses on one fact:
overlapping instances are not the exception, they are how every orchestrator
restarts a process.** A rolling deploy starts the new replica before the old one
has drained. Under the halting rule those few seconds permanently kill one of the
two projections — and if the loser is the new replica, the deployment finishes
with a projection that will never apply another event until somebody restarts the
process it is in. A framework whose singleton runner dies on every deploy is not
one a deployment can use.

So the loser takes its turn, and three things make that safe rather than merely
survivable.

**The row is re-read, and its cursor is the one the loop resumes from.** Not the
cursor of the page this pass held: the winner may have read further, and its row
is the point some instance actually delivered up to. Resuming there skips nothing
— every position at or below it was applied by whoever wrote it — and it is what
keeps the two instances from re-reading each other's pages for ever.

**It is re-read through a tracker of its own.** A `Tracker`'s window refuses a row
that moved under a live tracker, and here the loop already knows it moved: the
store refused the save over the fence, or never confirmed it. The window has
nothing left to catch, and what decides which moves are legal is
`Projection.settle`'s own table, which tells four apart where the window has one
refusal for all of them: the row at the presented advance, above it, one below
it, and anything else. The door's other re-checks — a half-absent row, an empty
cursor at a live advance, another name's row, a cursor over the ceiling — run
exactly as they do on the first resume, because those are about the answer's
shape rather than about a tracker's life.

**It is loud.** `ErrOvertaken` reaches `State.Err` on every lost fence, and
`Projection.settled` refuses to clear a contested streak, so the backoff
accumulates across passes that read and applied perfectly well. Once it passes
`Tolerate`, `Ready` answers `ErrOvertaken` and the replica reports unhealthy —
which is the signal a deployment acts on, and the reason the backoff is not
merely a delay: it is what bounds the duplicate rate to one page per
`Backoff.Max` while the contention lasts.

### Not every conflict is a second writer, and the table is what tells them apart

| The row the settling load answered | What it means | What the loop does |
|---|---|---|
| at the advance this pass presented, and the save was **refused** | another instance presented the same advance from the same fence and got there first | take the row, rebuild the reader from its cursor, back off |
| **above** the advance this pass presented | several passes of another instance are already past it | the same |
| at the advance this pass presented, unconfirmed, carrying a cursor this pass **never presented** | this pass's save did not land: another instance reached that advance the moment this pass's unit rolled back and released the row | the same |
| at the advance this pass presented, unconfirmed, carrying **this pass's own cursor** | this pass's own save landed | continue from the in-memory cursor |
| one below it, `InUnit`, unconfirmed | the whole unit rolled back and the page is where this projection left it | re-apply the page it holds |
| one below it, and the save was **refused** | the store refused a save over the very row its own fence admits | halt |
| no row at all, or one **behind** the fence | the name was forgotten, reset or restored under a running projection | halt |

**The advance alone cannot say who wrote the row, and the cursor is what settles
it.** Rows three and four were one row until S4's review drove the split: the
fence admits one writer at each advance, so a row standing at the advance this
pass presented is this pass's own save *or* a second instance's — and a rolling
deploy produces the second every time the loser's unit rolls back a claim the
winner then takes. This pass presented its cursor beside that advance, so a row
carrying any other cursor is provably somebody else's, and resuming from this
pass's own instead skips every position between the two: applied by nobody,
behind the checkpoint at the next save, `Quarantined` zero, no error on any path.
Measured on PostgreSQL 17.9 with two `Projection` values of one name — the read
model ended holding `[s-1 s-4]` over a log of four while the row stood at advance
2, highest 4.

The other direction is not provable and does not need to be: a second instance
that read the same page presents the same cursor, and the two resume at the same
point, so nothing is decided by telling them apart.

What the comparison rests on is a property the conformance suite already
certifies rather than a new obligation: `roundTripSection` and
`cursorBoundsSection` in `event/eventtest/sections_checkpoints.go` require the
cursor that went in to be the one that comes back, byte for byte, through a
second value. A store that re-encodes it is already refused there.

The last two rows are what keeps this from being "conflicts are ignored". A
forgotten row still halts: creating a fresh row at
advance 1 would leave the read model holding events no checkpoint accounts for,
and re-seating a live fence downward is a silent restart at the origin against a
live destination.

## Claiming the row before the handler runs

The reference implementation this was adjudicated against
(`github.com/eugene-khyst/postgresql-event-sourcing`, `EventSubscriptionRepository.java:33-44`)
claims its subscription row with `SELECT … FOR UPDATE SKIP LOCKED` **before** it
reads any event, so a second instance never reads and never handles. vv cannot
take that lock: `Log.ReadAll` is outside every unit of work on purpose
(`event/projection/projection.go`), and nothing here opens a transaction
([[D-126]]).

It can take the same lock one step later, and inside a unit that costs nothing.
The advance and the handler's writes commit together, so the order they are
written in is invisible to everyone except the lock manager — and a fenced
`UPDATE … WHERE advance = $3 - 1` takes the checkpoint row's lock. Presenting the
advance **first** therefore blocks the second instance on that row before its
handler runs; when the winner commits, the loser's fence matches nothing, its
unit rolls back, and it never applied anything. That is the reference's mechanism
with the guard the reference's unguarded `UPDATE` never had.

**At advance 1 there is no row to lock, and that half was measured rather than
argued.** The store splits the first save into `INSERT … ON CONFLICT DO NOTHING`
(`saveStatement`, `event/eventpg/checkpoints.go:353-364`), so what holds the loser there is
PostgreSQL's *speculative insertion* — a second inserter of one key waits on the
first inserter's uncommitted tuple, and when that transaction commits its own
`ON CONFLICT DO NOTHING` inserts nothing and reports zero rows. Driven on 17.9 in
`TestTwoLiveInstancesOfOneNameOverOneSchema`, with both instances held at their
first save until both had issued one: the handlers of two instances were called
for exactly 24 envelopes over a log of 24, and the page they both claimed is in
**one** row of the read model. Reordering `claimed` to apply before it saves
takes that to 26. So the claim holds at advance 1 and above, for two different
reasons in the server, and neither is this package's.

Outside a unit the same reordering would be an advance a crash could leave over
an unapplied page, so there the order is the delivery guarantee itself — handle
first, advance last — and it does not move.

**Three things this costs, all of them stated rather than discovered later.**

1. Under `AfterApply` two instances both apply the overlapping page. There is no
   lock to hold across a handler call outside a transaction, at-least-once
   already obliges the handler to tolerate a duplicate, and the module page says
   so beside the delivery row.
2. The isolation pass saves **after** it runs, because what it quarantined is
   what its own run discovered, and a claim carrying a count of what was passed
   before the sink was called would be a number nobody measured. A losing
   instance inside an isolation pass therefore applies and rolls back like any
   other.
3. Whether the loser is refused before its handler runs is a property of the
   *store*, and the two shipped ones differ. A fenced save the server evaluates
   against a tuple it is holding refuses it — PostgreSQL, confirmed live at both
   advance 1 and above — and one staged optimistically and revalidated at commit
   lets both apply and rolls the loser's half back (`eventmemory`). Both are
   correct; only the first avoids the wasted work, and a third-party store owes
   neither. Under `AfterApply` the question does not arise for either: there is
   no transaction to hold across a handler call, so both instances apply the
   overlapping page and the read model holds it twice — measured, not inferred.

## What it forbids

- Do not send `ErrConflict` from a checkpoint save to the halting arm again. A
  deployment that restarts one process must not lose a projection.
- Do not resume a contended pass from the reader's own cursor. The row's cursor
  is the resume authority, and using the loser's would re-read the winner's pages
  every turn.
- Do not read a matching advance as proof that this pass's own save wrote the
  row. Compare the cursor. A settlement that attributes another instance's row to
  itself loses every event between the two cursors, permanently and silently.
- Do not clear the retry streak on a read while the projection is contested.
  A losing instance reads and applies perfectly well every pass; the streak is
  the only thing that makes it visible.
- Do not present the advance before the handler outside a unit of work.
- Do not keep a second copy of the advance beside the tracker's. The door
  presents it and answers what it presented (`Tracker.Save`), and the table above
  is decided by comparing that number against the row: a pass that re-derived it
  would decide on a number no store ever saw the moment the two parted.
- Do not let one pass present two advances. A caller's `Unit` may run the work
  more than once — its own retry around the transaction is the shape [[D-126]]
  tells it to own — and the second run is refused before it writes anything, so
  the settlement reads the row against the first run's advance and the page is
  re-delivered. Halting there is the rolling-deploy failure again, by another
  door.
- Do not treat an absent row or one behind the fence as contention. Those halt.
- Do not add a "resume", "retry" or "clear" for a halt, and do not make repeated
  contention halt after N turns: that is the rolling-deploy failure with a
  counter in front of it.

## Where it lives

- `event/projection/pass.go` — `settle`, `anothers`, `overtaken`, `confirmed`,
  `rolledBack`, the `pending` value the resolution is held in, `claimed` and the
  `applier` it reads, and `settled`.
- `event/projection/errors.go` — `ErrOvertaken`, the sentinel a deployment reads
  with `errors.Is` off `State.Err` and off `Ready`.
- `event/projection/projection.go` — the `contested` and `unsettled` fields the
  loop carries between passes.
- `event/checkpoint.go` — `Tracker.established`, whose window this table now
  agrees with rather than contradicts, and `Tracker.Save`, which answers the
  advance it presented so that the table has one number to read.
- `event/eventpg/projection_integration_test.go` — the live two-instance case
  §3 is measured by.

## Proven by

- `TestALostFenceIsSettledOnTheRowOfThisRunnersOwnIdentity` — the same claim for
  a runner whose `Identity` renders as something other than its `Spec.Name`: a
  partition and a generation each lose the fence on every page, and each takes
  turns rather than halting. The settlement re-reads the row keyed by the
  identity, and a settlement keyed by the name would read another topology's row —
  absent in the ordinary case, so the lost fence fell to the arm that halts and
  this decision was inverted for every partitioned deployment. **Control:** the
  same case at `Ungenerated` over the whole key space, where the two render alike
  and it passes whichever row is read.
- `TestAUnitThatRollsBackLeavesTheAdvanceOnThisRunnersOwnRow` — the other
  settlement the same wrong row corrupts, and permanently: `confirmed`,
  `rolledBack` and `overtaken` all adopt the tracker they were handed, so one
  rollback would move every later checkpoint of a partitioned runner onto the
  coarse row. Every save the store sees carries this runner's own row key.
  **Control:** the retrying state carries the handler's own failure, so the
  rolled-back arm was reached rather than skipped.
- `TestEveryTrackerInTheProjectionPackageIsKeyedByAnIdentity` (`scripts`) — the
  structural half: every `event.Track` call in `event/projection` is keyed by an
  expression that mentions an `Identity`, so the next one written from `Spec.Name`
  is reported at the call rather than discovered as a read model that took the log
  twice. **Control:** a fixture with one call of each shape reports exactly one.
- `TestTwoLiveInstancesOfOneNameTakeTurnsAndNeitherHalts` — a second writer takes
  the advance this projection presents on every page: it never halts, it applies
  the whole log through the rows the winner left, and `Ready` answers
  `ErrOvertaken` once the losing streak outlasts `Tolerate`. **Control:** one
  instance alone drains the same log, stays healthy and contends with nothing.
- `TestTwoLiveInstancesUnderInUnitApplyEachEventOnce` — two `Projection` values
  of one name, both running, over one log: under `InUnit` against a fenced save
  taken under the row's lock, no event reaches a handler twice and neither
  instance halts; under `AfterApply` every event reaches one and neither
  instance halts. The mutual exclusion is the test's own, because `eventmemory`
  is §3's other kind of store, which is what the live case below is for.
- `TestTwoLiveInstancesOfOneNameOverOneSchema` (`event/eventpg`, live) — §3
  itself, against the database the claim is about: two instances of one name over
  one schema, **both modes**, with each instance's first save held until both
  have issued one, so the contention is driven rather than hoped for. Under
  `InUnit` the page they both claimed is in one row of the read model and the
  handlers were called for exactly the log's length; under `AfterApply` that page
  is in two. **Controls:** the gate must have opened and `ErrOvertaken` must have
  been published, so a run where one instance drained the log before the other
  woke fails rather than passes; and one instance alone drains the same log,
  applies every event once, contends with nothing and reaches `PhaseFollowing`.
- `TestASettlementResumesFromTheRowsCursorAndNeverFromItsOwn` — rows three and
  four of the table, told apart by the cursor alone: the same unconfirmed save
  against a row at the same advance, once carrying a cursor a shorter reader
  minted and once carrying the one this pass presented, delivers everything above
  that cursor again in the first case and nothing again in the second. The second
  arm is the control, and it fails if every settlement is read as contention.
- `TestASettlementTakesTheRowsCursorWhenAShorterWinnerLeftIt` (`event/eventpg`,
  live) — the same rule against the database, with two `Projection` values of one
  name whose page sizes differ: the new replica claims advance 1 over the whole
  log and its unit takes the claim back, the old replica takes that advance with
  its own one-event cursor and stops, and the settling load is held at a gate
  until that row is committed. Every event of the log is in exactly one row of the
  read model afterwards. **Controls:** the two cursors must differ and exactly one
  save must have been issued, so a run where the two read the same page fails
  rather than passes; and one instance alone over the same log applies every event
  once.
- `TestTheAdvanceIsClaimedBeforeTheHandlerInsideAUnitAndAfterItOutside` — the
  order itself, read off a recording checkpoint store and the handler beside it.
- `TestAForgottenCheckpointRefusesTheNextSaveAndHalts` — the row that is not
  contention: `Forget` under a running projection still halts it, and the fence
  is not re-seated.
- `TestTheAdvanceAndTheHandlersRowsCommitTogetherInMemory` — the claim-first
  order does not weaken the atomicity it rides in: a unit lost on its way to
  committing leaves neither the write nor the advance.

## See also

[[D-092]] [[D-118]] [[D-126]] [[D-128]] — and
`.agents/artifacts/gaps/EVENTSOURCE_REFERENCE.md`, technique R4/R6/R28 and
§Adopt A2, which is the adjudication this decision answers.
