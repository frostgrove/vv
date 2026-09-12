# FL-038 — A settled cursor becomes a durable checkpoint

**Entry points:** `projection.New` (the composition root), `projection.Projection.Run`
(the loop), `event.Track` (the door onto a checkpoint store),
`eventmemory.NewCheckpoints` / `eventpg.NewCheckpoints` (the two shipped stores),
`eventtest.RunCheckpoints` (a third implementation's proof),
`projection.WaitOf` / `projection.WaitSpec.Committed` / `projection.Wait` (the
read of these rows from a request path)
**Governed by:** [[D-092]] [[D-118]] [[D-126]] [[D-128]] [[D-129]] [[D-130]]
[[D-131]] [[D-132]] [[D-133]] [[D-140]] [[D-144]]

What happens between a page the log answered and a row in a checkpoint table that
says a consumer finished with it — the fence that admits one save at a time, the
unit of work the advance rides in, the retry that re-applies the page it holds,
the isolation pass that buys envelope granularity, and the bounded resolution
that reads an unconfirmed or refused save off the row rather than guessing at it.

[[FL-036]] is the half below this one: the declaration, the append, the log walk
and the six classes a refusal belongs to. [[FL-037]] is where the fourth table
lives on PostgreSQL. This flow starts where a `*event.Reader` has a page.

`event/projection` is a package of the **root module**. It reaches `crud` for the
executor question `InUnit` asks, `errs` through the vocabulary, and `runtime` for
the runner contract — and nothing else. `scripts/event_test.go` is what holds
that.

## Construction, and what New refuses

`event/projection/spec.go`:

1. `New(spec)` builds `NewIdentity(spec.Name, spec.Generation, spec.Partition)`
   and hands **its rendering** to `event.Track` — the row key, the runner name and
   `Batch.Identity` are all that one string. It refuses a nil `Log`, `Handler` or
   `Checkpoints`; a `Log` that is also an `event.Store`, so a projector cannot
   assert its way back to the append surface; an `Advance` outside the enum;
   `InUnit` with a nil `Unit`, with a checkpoint store whose `Transactions` is not
   `Supported`, or with `Destination` unstated; a `Unit` supplied beside
   `AfterApply`; `ParkSequence` with no `Park`, outside `InUnit`, or beside a
   `Destination` of `Unchecked` — the three that make the queue's ordering a
   property every shipped configuration has rather than half of them; a `Sequence`
   that does not name itself or is a `SequenceBy` over a nil function; a backoff
   that shrinks; and a negative `Idle`, `Attempts` or `Tolerate`.
   `NewIdentity` refuses `@` and `#` in a name, and spells the kernel's own
   identifier rule a second time — empty, over the bound, invalid UTF-8, a control
   character, a bracket — because an `Identity` is an input at doors that never
   reach `event.Track`. The two spellings are pinned to one another by
   `TestNewIdentityRefusesEveryNameTheKernelRefuses`.
2. **Every refusal wraps `ErrSpec` and they are collected rather than reported
   one at a time**, because a spec assembled wrong is usually assembled wrong in
   more than one place.
3. Defaults are each the one that promises less: `AfterApply`, `Idle` 1 s,
   `Backoff` `{250 ms, 30 s}` doubling **without jitter** (a projection is a
   singleton per name, so there is no herd), `Attempts` 10, `Tolerate` 1 min,
   `Classify`, `runtime.SystemTicks`, `Halt`. `Destination` has no default under
   `InUnit`: `projection.Unchecked` is how a composition says out loud that it
   cannot be resolved, and it is not reachable by leaving a field zero.
4. `New` performs no I/O, starts nothing and reads no environment. The first
   `Tracker.Load` and the first `Reader.Next` both happen on the first pass of
   `Run`.

## The door onto a checkpoint store

`event/checkpoint.go`:

1. `Track(checkpoints, projection)` refuses a nil store with `ErrWrongStore` and
   a name the kernel's identifier rule refuses with `ErrDeclaration`. Nothing
   calls a `Checkpoints` raw, so a second consumer inherits the re-checks rather
   than re-deriving them.
2. `Tracker.Load` re-checks five things about the answer, in a fixed order, each
   `ErrWrongStore`: **absence is total** (an advance of zero beside a cursor, a
   name or a progress is a store that did not answer the question); **presence is
   total in the one field that resumes** (the empty cursor *is* the origin of a
   log, so a row carrying one at a live advance restarts a consumer at the
   beginning against a live destination with no error anywhere); the answer is
   about **the name that was asked for**; the cursor is within `MaxCursorBytes`;
   and the row has not moved outside this tracker's own window.
3. The window is two numbers rather than one. `advance` is the fence — a save
   presents one above it — and `floor` is the lowest advance the row can still be
   at, which is the last one this tracker watched a store commit for itself. They
   part when a save leaves the row unresolved (the store did not say whether it
   wrote) or staged (it wrote inside a transaction this tracker does not commit).
4. `Tracker.Save` presents `advance + 1`, so `Checkpoint.Advance` is a field the
   store reads rather than one a caller computes, and it **answers the number it
   presented** whatever the store said — a caller that re-derived it would hold a
   second copy of one fence. It refuses a save before a load, a save over an
   unresolved one, the empty cursor, and a cursor over the ceiling.
5. `Tracker.Load` enters at the **read** door and `Save`/`Forget` at the
   **append** door, which decides one thing: an unclassified failure is
   `ErrBackend` at the first (nothing was written) and `ErrUncertain` at the
   second (a write whose fate is unknown).
6. `Forget` does not reset the fence, and neither does the load that follows it.
   Retiring a live projection's row is meant to be visible.

## One pass

`event/projection/pass.go`, `event/projection/projection.go`:

1. **Resume**, once: `Projection.unclaimed`, then `Tracker.Load`, then
   `event.Read(log, held.Cursor)`. The reader is held for the loop's life and
   the cumulative counts are seeded from the row, because `Applied` and
   `Quarantined` are persisted columns and a dashboard that resets on every
   deploy is one nobody can read. `unclaimed` is the topology half and asks two
   questions. `Projection.unretired` costs one `Tracker.Load` for **this**
   identity's retirement row — `<identity>#split`, the record `Split` leaves —
   and halts with `ErrTopology` when it is there: redeploying the release that
   ran before a split is the ordinary rollback of a bad deploy, and without that
   record it resumes from the origin into the read model the children are
   filling. Then one `Tracker.Load` per **coarser** share of this runner's key
   space — `Identity.coarser()`, which is empty for a projection that named no
   partition: a live row at `orders` beside a runner at `orders#0.1` is two
   writers over every key of that half, so it halts with `ErrTopology` naming
   `Split`.
2. **Read**, outside every unit of work. An empty page is `PhaseFollowing`: the
   cursor stays in the reader, **nothing is saved**, and the loop waits on the
   ticker, on `Spec.Wake` or on the context. A non-empty page is `PhaseDraining`,
   and the page and its cursor are captured together — a retry re-applies the
   page it holds and issues no read. `Projection.matching` decides this runner's
   share of that page once, at the read, on the key `Spec.Sequence` answers; a
   panic out of a sequencer **halts** rather than failing the page, because an
   envelope with no sequence belongs to no partition and there is nothing for a
   retry or a park to be about.
3. **Deliver.** Outside a unit the order is the delivery guarantee itself: handle
   first, advance last. Inside one, `Spec.Unit` is called and the body presents
   the advance **before** the handler runs ([[D-133]]) — the two writes commit
   together, so their order is the lock manager's business alone, and a fenced
   save takes the checkpoint row's lock.
4. **`InUnit`'s three per-pass checks** run inside the unit and before the
   handler: `Tracker.Transaction(ctx)` must answer a valid authority; unless
   `Destination` is `Unchecked`, `crud.ExecutorFor` must find an executor
   `crud.IsTransaction` accepts; and the two must be **the same** transaction —
   `event.NewAuthority(Tracker.Backing(), crud.KeyOf(executor))` compared
   `Same` against the authority the store answered. The third is the one that
   tells an aligned unit from a divergent one: two transactions under one unit
   pass both of the others and buy `AfterApply` atomicity under an `InUnit`
   spec. Any of the three failing rolls the unit back and halts **without
   calling the classifier**, per pass and never cached.
5. **`Progress` is built at the save and nowhere else**, so it describes a page
   that was applied or quarantined rather than one that was merely delivered:
   `{Highest: the page's last position, Applied: seeded + the envelopes this
   partition matched, Quarantined: seeded + what the queue took, At: now}`. The
   watermark is the **read** page's last position and not the matched one's, so a
   partition that matched nothing in a page still advances past it rather than
   re-walking the log at every restart.
6. **The count is read once per resume and again per pass while it is non-zero**
   (`Projection.counted`), and while it is zero `Park.Holds` is never called at
   all — the fast path is exact rather than a cache, and the loop's own park is
   what makes it believe. `counted` runs from `once`, **before `deliver` opens
   the unit**, so `Park.Sequences` is the one method of the interface called
   outside it; a healthy projection would otherwise open a transaction per pass
   to be told the queue is still empty, and an implementation that requires the
   ambient transaction there postpones for ever. A pass over a non-empty queue
   delivers through `unblockedPage`, which asks `Holds` once per matched envelope
   **inside** the unit and parks a held one without calling the handler. The
   whole path is reachable only under `OnPermanentFailure: ParkSequence`:
   `withDefaults` drops `Spec.Park` beside any other policy, in one place rather
   than at each of the three that reach for it, because the field is accepted
   there (a composition root builds one spec for a live generation and a rebuild)
   and asking the queue at a tier that opens no unit is the blocking path outside
   a transaction.
7. **The isolation pass** (`Projection.sequenceBySequence`) is how a park buys
   sequence granularity without a second handler signature: the page is delivered
   again one envelope to a `Batch`, in position order; one that applies is
   applied; one whose failure the classifier calls permanent goes to the `Park`
   with its cause **and every later envelope of the same sequence goes with it
   without reaching the handler**; a **retryable** failure ends the pass and
   returns the whole page to retrying under the same attempt budget — and once
   that budget is spent the same rule that made the page's failure permanent
   makes this one permanent too, so an outage lasting `Attempts` passes parks
   every sequence of the page it was on and an operator's redrive is what clears
   them. The delivery and the queue are one unit — `ParkSequence` constructs at
   no other tier, and a letter written outside it is the one write that survives
   the rollback of the advance. `ErrParkFull` is the third verdict: the unit rolls
   back, nothing is parked, no envelope is skipped, and the partition retries
   without an attempt budget at `PhaseBlocked`.
8. **Each attempt is handed its own page.** `event/projection/page.go:copyOf`
   takes a fresh slice and a fresh copy of every payload, the first attempt
   included, and keeps the log's own page untouched beside them. It is the
   eighth hand-off of §INV-021 and the first whose sender re-reads what it handed
   over.

## The settlement, and the four outcomes it tells apart

`event/projection/pass.go:settle`:

A save whose fate its own answer did not carry — `ErrUncertain`, `ErrConflict`,
or a caller's unit answering an error after the save inside it returned nil — is
**held** rather than resolved on the spot, so a backend that goes away during the
resolution is retried *as* the resolution. One `Tracker.Load`, through a tracker
of its own, and the advance **and the cursor** the row carries decide:

| The row | What it means | What the loop does |
|---|---|---|
| at the presented advance, save refused | another instance got there first | take the row, rebuild the reader from **its** cursor, back off |
| above the presented advance | another instance is several passes past it | the same |
| at the presented advance, unconfirmed, carrying a cursor this pass never presented | this pass's save did not land; another instance reached that advance | the same |
| at the presented advance, unconfirmed, carrying this pass's own cursor | this pass's save landed | continue |
| one below, `InUnit`, unconfirmed | the whole unit rolled back | re-apply the page it holds |
| one below, save refused | the store refused a save over the very row its own fence admits | halt |
| absent, or behind the fence | the name was forgotten, reset or restored under a running projection | halt |

`event/projection/pass.go:anothers` is the cursor comparison, and it is what
keeps a settlement from attributing another instance's row to itself — which
would lose every event between the two cursors, permanently and silently.

## The handoff

`event/projection/topology.go`, and it is the one topology change there is:
`Split(ctx, SplitSpec{Checkpoints, Identity, Unit})` answers the two identities a
`Cover` is then declared from. It runs on the operator's own goroutine, opens
nothing, and every statement it issues goes through `event.Track` — one tracker
per identity, never the raw store, so the advance a child is created at is the
one the door's fence derived from that child's own `Load` and never a number this
call computed.

1. `children` decides what the call would create **before** the unit opens:
   `Partition.Split` for the arithmetic, `NewIdentity` twice for the names. A
   ceiling, a nil `Checkpoints`, a nil `Unit` and the zero `Identity` are refused
   here, without a transaction being opened for them.
2. `inACallersTransaction` asks `Tracker.Transaction(ctx)` inside the unit and
   before anything is read: a `Unit` that bound none would leave four writes the
   store commits one at a time.
3. `handOver` loads the parent, **both children**, and the retirement row of all
   three. A parent that already holds one is `ErrTopology` naming the children it
   found: this split already happened, and the rows tell that absence from every
   other one. An absent parent with no such record is `ErrTopology` naming both
   readings and both remedies — a partition that never ran needs no split, one
   whose row was lost is a restore — and naming any child row it found, which is
   the reading that says an earlier attempt committed. A child row beside a live
   parent is `ErrTopology` too, and so is a child that was itself retired by a
   split (`recordingFiner`): that share is already being recorded by a finer
   topology. A name with no room for the mark is refused before any of it
   (`retirable`, `unrecordable`), because a split whose retirement cannot be
   recorded is one nothing can refuse afterwards.
4. Both children are saved at advance 1 carrying the parent's cursor **byte for
   byte** and its `Progress.Highest`, which is enumerated because `Tracker.admit`
   deliberately does not make `Progress` total — a child at `Highest: 0` is a
   legal row nothing refuses, and every barrier derived from the set would be
   trivially reached. The lower child takes the parent's `Applied` and
   `Quarantined` and the higher starts at zero, so the sum across the set is
   unchanged. Then the retirement is recorded — a row at `<identity>#split`
   carrying the parent's own cursor — and only then is the parent `Forget`ten.
   Four writes, and the record is the one that makes the handoff one way:
   `Forget` deletes the only evidence the retired share was ever recorded, and
   the resume above reads rows.
5. Nothing is carried between two runs of the body, so a `Unit` that runs it
   twice ([[D-130]]) performs one split or answers a refusal. What the unit
   answers is read against what the body reached, never against its text: a body
   that refused travels as that refusal even when the unit answers `nil`.

The parent is not told. A running parent whose row is split away finds it absent
at its next save and **halts** — [[D-133]]'s absent-row arm, unchanged — which is
why the operator's order is drain, split, start the children. There is no
`Merge`: it would have to order two cursors, and a cursor answers equality and
emptiness only ([[D-129]]).

## The cutover

`event/projection/generation.go`, and it is the other half of what a generation
is for: [[FL-042]] is where the effect gate reads the same row, and this section
is where the read target moves.

1. `Observe(ctx, checkpoints, of, over)` answers the **lowest `Highest`** across a
   cover, which is the only aggregate a set of checkpoint rows has: `Highest` is a
   completeness watermark for its own row and says nothing across rows, so a max,
   a sum or an average over a partition set is a number no partition ever reached.
   All members fresh and none retired answers the origin with a nil error — the
   generation delivered nothing, so nothing is owed. **Some** fresh and some not
   is `ErrTopology` naming one that holds no row, and so is a member with no row
   beside a row recording that a split retired it: read as position zero, an
   absent row answers a barrier at the origin for a generation that is live on
   three partitions out of four, and every arriving generation clears it.
2. `Reached(ctx, checkpoints, park, barrier, arriving, over)` compares `>=`
   against the lowest `Highest` of the arriving generation's own cover and does
   nothing else with the number. `Holes` is asked **once**, of the whole
   generation, because a park is keyed by an identity with the partition dropped;
   a nil park answers zero. `Quarantined` is summed and is not what a cutover
   refuses on: it is cumulative and never falls, so a generation that parked a
   sequence and redrove it completely would be refused by it for ever.
3. `Cutover(ctx, spec)` is six steps inside one transaction of the caller's, and
   the order is the argument. `inACallersTransaction`'s question first, because a
   unit that opened nothing makes the evidence and the switch two snapshots. Then
   the barrier is observed from the **retiring** generation's own rows —
   `CutoverSpec` has no barrier field, because a barrier a caller can invent is
   not evidence and the zero value of one admits a generation that has delivered
   nothing. A retiring generation with no rows at all is `ErrRetired`; so is an
   arriving one. Holes are refused unless `AcceptQuarantined` says otherwise, which
   is an operator's deliberate act reachable by no default. And then `Activate`
   moves the row, once, fenced: it answers an error wrapping `event.ErrConflict`
   when the row does not hold `from`, so two operators cutting over at once leave
   one winner and one refusal.
4. **A rollback is the same call with `From` and `To` exchanged**, and the two
   covers with them. That is not elegance: it is what makes the rollback path
   exercised by the tests the forward one is.

Two windows are named here and neither is closed by the transaction. **The covers
a cutover declares must be the ones those generations record at**, and this call
cannot check that — it reads the rows the cover names and no others, so a live
four-partition generation declared as `Whole()` answers silence and silence is
refused rather than folded into a barrier at the origin. And **the retiring
generation is a separate runner**: if it is still advancing it goes past the
barrier while this unit is open, and reads then move backwards by exactly that
much until the arriving generation catches up. What closes it is an operator
draining or stopping the retiring generation before, or as, the switch commits —
`Observe` it twice and see whether the barrier moved. Axon's `resetTokens` is the
one mechanism either reference has, and neither half of it is taken: this call
cannot stop a runner in another process, and claiming a live generation's rows
means writing them, which takes that runner's fence away.

## A wait, which is a read of these same rows from a request path

`event/projection/wait.go` and `event/projection/mark.go`. Nothing here writes: a
wait is the checkpoint rows this flow produces, read on a caller's own goroutine,
until they say the caller's own change has been delivered.

**The value first.** `Mark` has unexported fields only and two minting doors, both
of which take a number a store produced. `WaitSpec.Committed` reads the commit's
**whole range** back with `Store.ReadStream` after the caller's transaction has
committed — `Commit` carries no position by design — and attaches the distinct
sequence keys `spec.Sequence` answered, in first-appearance order. `MarkOf` takes
a `Barrier` that `Observe` folded from a generation's own rows and attaches none.
Both record `Of.Projection()` / `Barrier.Projection`, and `Wait` refuses a mark
whose projection is not `spec.Of`'s before any store call: the position is global
so a census would reach, while the park would be asked under a key no letter of
this projection was written under ([[D-144]]).

`WaitSpec.resolved` does not trust the page it is handed. `honest` reproduces all
three arms of `event/repo.go:checkPage` in this package's vocabulary — no more
envelopes than the published `StreamPage`, every envelope the stream that was
asked for, every envelope one version above the one before it — because a store
answering another stream's row mints a mark at a foreign position carrying a
foreign sequence key, and the park is then asked about somebody else's event and
answers no.

**`WaitOf(spec, over)` derives the five facts a wait needs from the projection's
own `Spec`, through the same `withDefaults` `New` applies** rather than through a
second spelling of two of its clauses. Three of the five are silently wrong-able
by a request handler, and `WaitOf` is what makes supplying them a derivation.

**One poll, in order**, and the order is the whole of ES-05:

1. `Generations.Active`, on poll 1 where `Generations` is supplied
   (`waiter.resolves`);
2. `Park.Sequences`, and `Park.Holds` per mark key only behind a non-zero count
   (`waiter.parked`) — **before** the census and on **every** poll, because the
   caller's change can be parked in one partition while another merely lags;
3. `surveyed` (`event/projection/generation.go`), the lowest `Progress.Highest`
   across the cover — the same census `Observe` folds a barrier from;
4. `Generations.Active` again, on the poll that would answer reached.

That second ownership read is unconditional: a caught-up deployment reaches on
poll 1 every time, so exempting it would leave the protection only for waits that
were going to be slow anyway. **Two reads over the wait, not two polls.**

`Park.Holds` is asked **outside** a unit of work here, which is the one sentence
of `Park`'s contract this phase widened; `Park.Holes` is asked by no wait at all.

**Five exits** (`waiting`): reached; `ErrParked` and `ErrGeneration`, both
terminal because they are conclusions drawn from rows that were read; a refusal
that could not be made, terminal on poll 1 and polled through after it; and the
context — `ErrNotVisible` wrapping `context.DeadlineExceeded` with the last
readable `Visibility`, or `ctx.Err()` bare and the zero `Visibility` on a
cancellation. **The context outranks the poll number**, and `expired` is what
keeps a store's own classification of a done context out of the deadline's wrap:
it asks `errors.Is(refusal, ctx.Err())` **and** `errors.Is(event.CauseOf(refusal),
ctx.Err())`, because a refusal answers false for `context.DeadlineExceeded` by
design and the cause is the only door onto it.

A wait starts nothing, saves nothing and forgets nothing. `startsNothing`
(`scripts/extensions_test.go`) is what holds the first of those over these two
files.

## The two shipped checkpoint stores

`event/eventmemory/checkpoints.go` keeps its rows **on the `*Log`**, so two
`Checkpoints` values over one log are one checkpoint store, exactly as two
`Store` values over one log are one store. A save inside a `Tx` is staged in a
second staging area beside the appends, revalidated against the fence at commit,
and discarded by a rollback — and `Rollback`'s position arithmetic reads only the
first area, because a checkpoint draws no position.

`event/eventpg/checkpoints.go` is the fourth table of the same schema and a
resource of its own: a `Store` and a `Checkpoints` over one schema are two
resources at one schema version, so a deployment migrates once and each verifies
at its own `Prepare`. Its save is **two statements of which exactly one is
issued** — `INSERT … ON CONFLICT DO NOTHING` at advance 1, `UPDATE … WHERE
projection = $1 AND advance = $3 - 1` above it — because a single
`INSERT … ON CONFLICT` asks whether the row is there against its own snapshot and
which row it collides with against the live index, so a `DELETE` committing
between those two moments would bring a retired row back at an advance no first
save ever created.

## Where the decisions bite

- **[[D-128]] — the log delivers in position order.** `Progress.Highest` is a
  completeness watermark and not the page's last position by coincidence, and
  `presentSave` fills it from the last envelope of the page **because** of that
  law.
- **[[D-129]] — a checkpoint is a cursor.** There is no position on a
  `Checkpoint`, no function from one to a cursor, and no ordering of two cursors.
- **[[D-130]] — the framework opens no transaction and starts no goroutine.**
  `Spec.Unit` is the caller's, runs the work exactly once, and the read is
  outside it.
- **[[D-131]] — a forgotten route halts.** Inside a covered family an unrouted
  type is `ErrUnrouted`; outside every covered family it is skipped and counted.
- **[[D-132]] — no snapshot, and no log line.** `State`, the `Observer` and
  `Ready` are the whole of what reaches an operator.
- **[[D-133]] — a lost fence is a turn and not a halt.** The row the winner left
  is adopted, the reader is rebuilt from that row's cursor, and the streak only a
  landed save clears is what makes `Ready` report `ErrOvertaken`.
- **[[D-092]] — nothing starts in a constructor.** `New` starts nothing, `Run`
  blocks on the caller's goroutine, and the package holds no `go` statement.
- **[[D-118]] / [[D-126]] — the store joins a transaction it did not open.** A
  checkpoint store on a bound transaction of its own source writes inside it; one
  on an ambient executor that is **not** a transaction refuses before any
  statement.

## Traps

- **A walk inside the projection's own write transaction settles no gap.** This
  is why the read is outside every unit, and a projection that opened the unit
  first would deadlock its own progress on the first rolled-back append in the
  log — in production, with every test over a gapless log green.
- **`InUnit` promises atomicity only for writes made through the context the
  unit gave the handler.** A read model in a second database is outside it, no
  check can see that, and `Destination: projection.Unchecked` is where a
  composition says so.
- **A closed `Spec.Wake` channel is permanently ready.** The loop stops waiting
  on it and follows on `Idle` alone; without that guard a closed channel is an
  unbounded read of the log.
- **A halt is terminal for that value's life.** There is no `Resume`, `Retry` or
  `Clear`: the exit is a new value in a new process.
- **`Placement: Singleton` is a promise to the deployment rather than an
  enforcement.** The checkpoint's fence is what actually holds when two
  instances run, and under `AfterApply` both apply the overlapping page.
- **A new projection reads the whole log.** Adding one to a live deployment
  replays every event ever written through its handler, a page at a time.
- **`event/` outside `event/eventpg` is frozen.** `make check-event-kernel`
  compares the tree against `scripts/event_kernel.sha256`, and a deliberate move
  is recorded with `make check-event-kernel-baseline` in the same change as the
  code.

## Files

| File | What it holds |
|---|---|
| `event/checkpoint.go` | `Progress`, `Progress.zero`, `Checkpoint`, `Checkpoint.Fresh`, `CheckpointCapabilities`, `Checkpoints`, `Track`, `Tracker` and its `Projection`, `Capabilities`, `Backing`, `Transaction`, `Load`, `admit`, `established`, `autocommits`, `Save`, `Forget` — the contract, the door and the fence, and the window that is two numbers rather than one |
| `event/bounds.go` | `MaxCursorBytes` — the seventh ceiling, and the one that bounds what a store **mints** rather than what it accepts |
| `event/reader.go` | `Reader.checkPage`, which takes the cursor beside the page: an over-ceiling cursor and an empty one beside a non-empty page are `ErrBackend`, and the ascending arm is one half of [[D-128]] |
| `event/fact.go` | `Fact.Family`, `Fact.Read` — the typed reading seam a route closes over, decoding through the fact's own chain and every declared upcaster |
| `event/eventmemory/checkpoints.go` | `CheckpointSpec`, `Checkpoints`, `NewCheckpoints`, its `Capabilities`, `Backing`, `Transaction`, `Begin`, `Load`, `Save`, `Forget`, `Close`, `held`, `refusable` — the rows live on the `*Log` |
| `event/eventmemory/transaction.go` | `stageSave`, `revalidateSaves`, `checkpointHeld` — the second staging area, and why `Rollback`'s position arithmetic reads only the first |
| `event/eventpg/checkpoints.go` | `CheckpointSpec`, `Checkpoints`, `NewCheckpoints`, `Prepare`, `Check`, `Transaction`, `opened`, `Load`, `Save`, `Forget`, `on`, `promisedCheckpoint`, `refusable`, `loadStatement`, `saveStatement`, `forgetStatement` — the fourth table, and the two fenced statements its save is |
| `event/eventtest/checkpoints.go` | `CheckpointFactory`, `RunCheckpoints`, `tracking`, `checkpoints`, `admitCheckpoints`, `admitInstant`, `missingCheckpointHook`, `checkpointName`, `sameCheckpoint` — the runner a third implementation is proved by, under the same three anti-vacuity rules the store suite uses |
| `event/eventtest/sections_checkpoints.go` | `checkpointInventory`, `needsCheckpointTransactions`, `needsCheckpointPersistence`, `cursorOfWidth`, `firstDifference`, `forgetsInAUnit`, `forgetRacingASave` and twelve of the fourteen section bodies |
| `event/eventtest/sections_topology.go` | `topologySection`, `topologyHandoffSection`, `checkpoints.handOver`, `checkpoints.absentOutside` — the other two, and the only place the suite asks what a split rests on: one cursor written under two names, a save at advance 1 over a live row, and a read, two saves and a removal that are one transaction or none |
| `event/eventtest/defects_checkpoints.go` | `checkpointDefect`, `checkpointDefects`, `unfenced`, `ahead`, `ambient`, `pedantic`, `narrow`, `verbose`, `closing`, `stale`, `absent`, `oneName`, `detaching`, `namespaced`, `recomposed`, `adopting`, `retiring`, `kept`, `beside` — the fifteen broken stores the runner is falsified with, at least one per section: `kept` answers only for the rows the value that wrote them holds, which is the `durability` one and is why no section is exempted from carrying a defect any more |
| `event/projection/doc.go` | the package sentence: at least once in both modes, one name is one writer, there is no head, and nothing here writes a line |
| `event/projection/errors.go` | `ErrSpec`, `ErrHalted`, `ErrOvertaken`, `ErrUnrouted`, `ErrTopology`, `ErrParkFull`, `ErrClaimLost`, `ErrRetired`, `ErrNotVisible`, `ErrParked`, `ErrUncommitted`, `ErrGeneration` — twelve, none of which crosses a store seam; the last four arrived with the wait |
| `event/projection/spec.go` | `Advance` and its three values, `Advance.Valid`, `Advance.String`, `Backoff`, `Spec` — `Sequence`, `Partition` and `Generation` among its fields — `unchecked`, `Unchecked`, `New`, `namedRefusal`, `refusedAdvance`, `refusedPark`, `refusedNumbers`, `withDefaults`, `appends`, `absent` — the whole refusal set, collected rather than reported one at a time, and the three the park costs are about the tier rather than about the queue |
| `event/projection/page.go` | `Batch` and its `Identity`, `Handler`, `HandlerFunc`, `copyOf` — the page per attempt, which is §INV-021's eighth hand-off |
| `event/projection/classify.go` | `Verdict`, `Retryable`, `Permanent`, `Classifier`, `Classify`, `Failure`, `Halt`, `ParkSequence` — the history class and `ErrUnrouted` are permanent and everything else is retryable, and the second verdict parks the sequence rather than the envelope. A `RedriveSpec` carries no `Classifier`: a letter that fails again is requeued with its new cause whichever class it is in, because giving up on one removes it without applying it and that is an operator's act through `Evict` |
| `event/projection/park.go` | `Letter`, `Park` — the queue a permanent failure parks a whole sequence in, keyed by `Identity.Whole()` so a split moves nothing, bounded per sequence rather than per queue, and counted once per resume so a healthy projection pays nothing |
| `event/eventtest/park.go` | `ParkFactory`, `RunPark`, `parking`, `park`, `admitPark`, `parkName`, `widestBound` — the runner a consumer's own queue is proved by, and the two bounds it declares |
| `event/eventtest/sections_park.go` | `parkInventory`, `needsADeclaredBound`, `outsideAUnitSection`, `parkUnitSection`, `committedStateSection`, `parkCountsSection`, `parkIdentitySection`, `parkBoundsSection`, `park.full` — six sections, one per tier the four methods run at, with `committed state` for the clause the wait widened |
| `event/eventtest/defects_park.go` | `parkDefect`, `parkDefects`, `queue`, `overPark`, `staleHolds`, `holesAreSequences`, `dropsTheGeneration`, `ungenerated`, `bareRefusal`, `cachedCount` — the eight broken queues the runner is falsified with |
| `event/projection/redrive.go` | `Claim`, `Redriver`, `Retried`, `RedriveSpec`, `Redrive`, `NewRedrive`, `errLetterRanTwice`, `refusedBarrier`, `refusedDestination`, `Redrive.Sequence`, `Redrive.Any`, `Redrive.claimed`, `Redrive.drain`, `Redrive.sequenced`, `Redrive.letter`, `Redrive.apply`, `Redrive.requeued`, `Redrive.checkUnit` — the operator's half: one unit per letter, in insert order, stopping at the first that fails again, claimed rather than read, and touching no checkpoint |
| `event/projection/state.go` | `Phase` and its seven values, `PhaseDegraded` and `PhaseBlocked` among them, `State` and its `Parked`, `State.Identity`, `Observer`, `ObserverFunc`, `observing`, `Projection.State`, `Projection.transition`, `Projection.progressed`, `Projection.counting`, `Projection.seed`, `Projection.publish` — published on a change and never on every pass, and a panicking observer does not take the loop down |
| `event/projection/router.go` | `Foreign`, `SkipForeign`, `RefuseForeign`, `routeKey`, `Router`, `NewRouter`, `On`, `TryOn`, `Ignore`, `TryIgnore`, `Router.declare`, `Router.ignore`, `Router.unclaimed`, `Router.Apply`, `Router.claims`, `Router.foreignTo`, `Router.unrouted`, `Router.seal`, `Router.Skipped`, `refusedName` |
| `event/projection/projection.go` | `Projection`, `newProjection`, `Projection.Name`, `Projection.Declaration`, `Projection.Run`, `Projection.Drain`, `Projection.Ready`, `Projection.until`, `Projection.follow`, `Projection.backoff`, `Projection.delay`, `Projection.acknowledge`, `Projection.stop` — the loop, and the six properties of it that are load-bearing and invisible from its shape |
| `event/projection/pass.go` | `step` and its four values, `Projection.once`, `Projection.counted`, `Projection.resume`, `Projection.unclaimed`, `Projection.read`, `Projection.followed`, `Projection.matching`, `tally`, `applier`, `Projection.deliver`, `Projection.outsideAUnit`, `Projection.insideAUnit`, `Projection.claimed`, `Projection.delivering`, `Projection.matchedPage`, `Projection.unblockedPage`, `Projection.sequenceBySequence`, `Projection.parking`, `Projection.applyPage`, `Projection.presentSave`, `Projection.applyFailed`, `Projection.permanent`, `Projection.saveFailed`, `pending`, `Projection.unretired`, `Projection.awaiting`, `Projection.settle`, `fenced`, `Projection.anothers`, `answered`, `Projection.confirmed`, `Projection.rolledBack`, `Projection.overtaken`, `Projection.refused`, `Projection.haltedBy`, `Projection.redeliver`, `Projection.postpone`, `Projection.stalled`, `Projection.landed`, `Projection.settled`, `Projection.checkUnit`, `panicked`, `asPanic`, `stopping` — one pass, the two modes, the partition filter, the blocking dispatch, the retry, the isolation pass and the settlement |
| `event/projection/identity.go` | `Generation`, `Ungenerated`, `Identity`, `NewIdentity`, `unnameable`, `ParseIdentity`, `parseGeneration`, `Identity.Projection`, `Identity.Generation`, `Identity.Partition`, `Identity.Whole`, `Identity.coarser`, `retirable`, `Identity.String` — the one place a recorded name is built, and the kernel's identifier rule spelled a second time because an Identity is an input at doors that never reach `event.Track` |
| `event/projection/partition.go` | `MaxPartitions`, `Partition`, `Whole`, `NewPartition`, `ParsePartition`, `parseNumber`, `Partition.Matches`, `Partition.Split`, `Partition.Mask`, `Partition.ID`, `Partition.Count`, `Partition.Whole`, `Partition.String`, `Partition.described`, `hash` — a mask and never a modulus, and the published FNV-1a/32 that makes it reproducible in a second process |
| `event/projection/cover.go` | `Cover`, `NewCover`, `Cover.Partitions`, `Cover.Count`, `gapIn` — the set is the thing that has to be right, checked by two exact arithmetic facts |
| `event/projection/sequence.go` | `Sequencer`, `ByStream`, `Unordered`, `OneSequence`, `SequenceBy`, `sequencer`, `sequencer.Name`, `sequencer.SequenceOf`, `unusable` — who names a sequence, and the three obligations the type cannot carry |
| `event/projection/topology.go` | `SplitSpec`, `Split`, `children`, `handOver`, `recordingFiner`, `tracking`, `retired`, `loaded`, `inACallersTransaction`, `noParent`, `besideAnAbsentParent`, `alreadySplit`, `unrecordable`, `alreadyFiner` — the one topology change there is, six steps in the caller's own transaction, the record that makes it one way, and nothing carried between two runs of it |
| `event/projection/mark.go` | `Mark`, `Mark.At`, `Mark.Zero`, `Mark.String`, `MarkOf` — the value a wait waits for, with unexported fields only: both minting doors take a number a store produced, and both record the projection they minted from, so a mark of one projection waited on another is refused at `Wait`'s door |
| `event/projection/wait.go` | `WaitOf`, `WaitSpec`, `WaitSpec.Committed`, `WaitSpec.resolved`, `honest`, `Visibility`, `Wait`, `waitable`, `polled`, `waiter`, `waiting`, `waiter.poll`, `waiter.resolves`, `waiter.parked`, `expired`, `waiter.stopped`, `defaultEvery` — the read of these same rows from a request path: the park before the census on every poll, the census's minimum, the two ownership reads, and the five exits |
| `event/projection/generation.go` | `Generation`, `Ungenerated`, `Generations`, `Barrier`, `Observe`, `Readiness`, `Reached`, `Cutover`, `CutoverSpec` and their refusals — the barrier is the lowest `Highest` across a checked set, the evidence is derived inside the unit rather than supplied by the caller, and the switch is one fenced write |
| `scripts/projection_test.go` | the surface, AST and comment walks the invariants name: no exported function from a position or a progress to a cursor, no ordering of a cursor, no comment promising exactly-once delivery, no snapshot declared or published, no transaction opened, no published topology predicate left without a caller, and no door taking a `Cover` or an `Identity` without asking whether it was built |
| `scripts/event_test.go` | the `projection` row of `charged`: this package costs the vocabulary plus `runtime` and nothing else |
| `_examples/event-checkpoints-elsewhere/main.go` | a complete `event.Checkpoints` over a database this framework ships no store for, and the `InUnit` wiring that is accepted and cannot be checked |

Every non-test `.go` file under `event/projection/` has a row above, `doc.go`
included: the reverse index in `docs/ai/flows/Index.md` is what an agent reads
before editing a file, and a file with no row there reads as a file outside every
flow.

## Tests that walk this flow

Untagged, in `make unit`: `event/checkpoint_test.go` and `event/tracker_test.go`
(the door, the fence and the window), `event/fact_test.go` (the typed reading
seam), `event/reader_test.go` (the page and the cursor checked together),
`event/eventmemory/checkpoints_test.go` and `event/eventmemory/transaction_test.go`
(the memory store and its staging), `event/eventtest/checkpoints_test.go` (the
runner's own falsification), `event/eventtest/park_test.go` (the queue harness's,
against the reference queue in `event/eventtest/fixtures_park_test.go`), and the
files of `event/projection` — including
`event/projection/mark_test.go` and `event/projection/wait_test.go`, which drive
the wait against a fake clock so no test of it sleeps either.

Behind `//go:build integration`, against a live PostgreSQL:
`event/eventpg/checkpoints_integration_test.go`,
`event/eventpg/projection_integration_test.go`,
`event/eventpg/projectioncase_integration_test.go`,
`event/eventpg/router_integration_test.go`,
`event/eventpg/rebuild_integration_test.go`,
`event/eventpg/replay_integration_test.go`,
`event/eventpg/partition_integration_test.go`,
`event/eventpg/topology_integration_test.go`,
`event/eventpg/park_integration_test.go`,
`event/eventpg/generation_integration_test.go`,
`event/eventpg/cost_integration_test.go` and
`event/eventpg/wait_integration_test.go` (the ten cases of ES-05: a confirmed
command read back without a sleep, the parked pair, a mark minted inside the
writing transaction, the call-count budget, and a wait polling beside the
projection's own loop). The gate names its own command:

```sh
FROSTGROVE_EVENTPG_TEST_DSN='postgres://vv:vv@localhost:55432/vv?sslmode=disable' \
  go test -race -count=1 -tags=integration ./event/eventpg/
```

### Proved by

| What holds | Proved by |
|---|---|
| the door refuses what it cannot hold, and absence and presence are total | `TestTrackRefusesWhatItCannotHold`, `TestAbsenceIsTotalAndProgressIsComparedFieldByField`, `TestPresenceIsTotalAndAnEmptyCursorIsNeverAResumePoint` |
| the fence is the door's, a save before a load is refused, and a load never re-seats a fence this tracker established | `TestAFreshTrackerAnswersTheZeroCheckpointAndSavesAtAdvanceOne`, `TestASaveBeforeALoadIsRefused`, `TestALoadNeverReSeatsAFenceThisTrackerEstablished` |
| each checkpoint door maps its unclassified failure its own way | `TestEachCheckpointDoorMapsItsUnclassifiedFailureItsOwnWay` |
| the reader checks the cursor beside the page | `TestAReaderRefusesACursorOverTheCeiling`, `TestAReaderRefusesAnEmptyCursorBesideANonEmptyPage`, `TestAConsumerReadsThroughPagesTheStorePublished` |
| a fact reads a stored envelope through its own chain and refuses the four history classes | `TestFactReadDecodesThroughItsOwnChainAndRefusesTheFourHistoryClasses` |
| both shipped checkpoint stores satisfy the contract, and the memory one's rows are the log's | `TestTheCheckpointStoreSatisfiesTheContract`, `TestTwoCheckpointValuesOverOneLogAreOneStore`, `TestARolledBackUnitStagedBothAndBurntOnlyTheAppends` |
| the conformance runner detects every defect it was built to detect, every section it dispatches is named by one, and it declines rather than passes what it cannot certify | `TestEveryCheckpointDefectIsReportedByItsOwnSection`, `TestEveryCheckpointSectionIsNamedByADefectThatBreaksIt`, `TestAStoreThatDoesNotPersistDeclinesDurabilityAndCertifiesTheOtherThirteen`, `TestACheckpointFactoryClaimingPersistenceWithNoSiblingFailsBeforeASectionRuns` |
| a spec assembled wrong is refused whole, and `InUnit` is never downgraded | `TestNewRefusesEverySpecItCannotAssemble`, `TestInUnitIsRefusedAndNeverDowngraded`, `TestASpecCarryingAStoreIsRefusedAndReadOnlyIsAccepted` |
| a name is built in one place, renders injectively and is refused wherever the kernel refuses it | `TestAnIdentityRendersAndRoundTrips`, `TestADelimiterInAProjectionNameIsRefusedAtConstruction`, `TestTwoDistinctIdentitiesNeverRenderOneName`, `TestNewIdentityRefusesEveryNameTheKernelRefuses` |
| a partition is a mask, a declared set covers the space exactly once, and neither type's zero value passes for a checked one | `TestASplitAtTheCeilingIsRefusedAndOneBelowItSucceeds`, `TestAMaskMovesNoKeyOutOfTheParentsHalfOfTheSpace`, `TestACoverWithAGapIsRefusedAndACompleteOneIsNot`, `TestACoverWithAnOverlapIsRefusedByMaskArithmeticAndNotByName`, `TestTheZeroCoverAndTheZeroIdentityAreTellableFromEveryCheckedOne` |
| four partitions apply every event once, a page one matches nothing in still advances, and a sequencer panic halts | `TestFourPartitionsApplyEveryEventOnceAndKeepEachKeyInOrder`, `TestAPageThatMatchesNothingAdvancesAndCallsNoHandler`, `TestEverySequencerIsTotalPureAndStable`, `TestASequencerPanicHaltsAndAHandlerPanicDoesNot` |
| only the first start chooses a topology, and a partitioned runner beside a live coarser row is refused | `TestAPartitionedRunnerBesideALiveCoarserRowIsRefused` |
| a split is four writes in one transaction at the parent's cursor, refused without a parent row and over a child that already has one, and holds nothing between two runs of one unit | `TestASplitWritesTwoChildrenAtTheParentsCursorInOneTransaction`, `TestASplitOfAParentWithNoRowIsRefusedForBothAbsences`, `TestASplitOverAnExistingChildRowIsRefused`, `TestASplitUnderATwiceRunUnitIsOneSplitOrARefusal` |
| a split is one way: the release that ran before it is refused when it is redeployed, a second split of a retired parent is refused, and a name with no room for the record is refused rather than retired unrecorded | `TestTheReleaseThatRanBeforeASplitIsRefusedWhenItIsRedeployed`, `TestASplitOfAnAlreadyRetiredParentIsRefused`, `TestASplitOfANameWithNoRoomForItsRetirementIsRefused` |
| every checkpoint call of a runner is keyed by its own identity, on the settlement as on the resume | `TestALostFenceIsSettledOnTheRowOfThisRunnersOwnIdentity`, `TestAUnitThatRollsBackLeavesTheAdvanceOnThisRunnersOwnRow`, `TestEveryTrackerInTheProjectionPackageIsKeyedByAnIdentity` |
| the two sequencers with no ordering requirement and with a total one are told apart by where the log landed, and a re-delivery lands where it landed before | `TestUnorderedSpreadsAndOneSequenceConcentrates` |
| the advance and the handler's writes are one transaction authority, compared inside the unit and before the handler | `TestTheAlignmentIsComparedInsideTheUnitBeforeTheHandler`, `TestTwoTransactionsUnderOneUnitAreRefusedAndOneIsAccepted`, `TestUncheckedMakesNoComparisonAtAll` |
| the loop drains, follows, and issues no write while it is idle | `TestADrainingProjectionReachesFollowingAndStaysThere`, `TestNIdlePollsIssueZeroSavesAndNRoundTrips`, `TestAFirstRunStartsAtTheOriginAndASecondResumes` |
| the read is outside every unit, and the advance is claimed before the handler only inside one | `TestEveryReadArrivesOutsideEveryUnit`, `TestTheAdvanceIsClaimedBeforeTheHandlerInsideAUnitAndAfterItOutside`, `TestTheAdvanceAndTheHandlersRowsCommitTogetherInMemory` |
| a unit runs the work once, and a second run re-delivers rather than halts | `TestAUnitThatRunsTheWorkTwiceIsRefusedAndThePageIsRedelivered` |
| a retry re-applies the page it holds, in memory of its own | `TestARetryReAppliesTheLogsOwnPageAfterABackoff`, `TestARedeliveryCarriesTheSameIdentitiesInMemoryOfItsOwn` |
| a permanent failure halts, and `ParkSequence` is sequence-granular | `TestAPermanentFailureHaltsAndParkSequenceIsSequenceGranular`, `TestAHistoryClassFailureHaltsAndNamesNoData`, `TestAParkIsSequenceGranular` |
| a poison event parks its sequence and the events behind it, a later page parks what the queue already holds, and the fast path costs nothing | `TestAPermanentFailureParksItsSequenceAndTheEventsBehindIt`, `TestALaterPageParksWhatTheQueueAlreadyHolds`, `TestTheFastPathCallsHoldsNeverAndSequencesOncePerResume` |
| a `Park` beside a policy that does not name it is never asked, and each method of the interface is called where its contract says it is | `TestAParkIsInertBesideAPolicyThatDoesNotNameIt`, `TestEachParkMethodIsCalledWhereItsContractSaysItIs` |
| a spent attempt budget parks a transient failure, and a redrive is what clears the page | `TestASpentAttemptBudgetParksATransientFailureAndARedriveClearsIt` |
| the park write rolls back with its unit, a full park blocks without skipping, and the bound is two-dimensional | `TestAParkWriteRollsBackWithItsUnitAndAnOvertakenRereadsTheCount`, `TestAFullParkBlocksTheAdvanceAndSkipsNothing`, `TestTheParksBoundIsPerSequenceAndNotPerQueue`, `TestAParkFailureThatIsNotFullHaltsAndAReadFailurePostpones` |
| a redrive is ordered, rotating, exclusive, one sequence at a time, and touches no checkpoint | `TestARedriveDrainsASequenceInInsertOrderAndTouchesNoCheckpoint`, `TestARedriveStopsAtTheFirstLetterThatFailsAgain`, `TestARedriveRotatesByLeastRecentlyTried`, `TestTwoGatedRedrivesNeverProcessOneSequence`, `TestAnExpiredClaimAppliesEvictsAndReleasesNothing`, `TestARedriveNamingAnotherSequencerOrAPartitionIsRefused` |
| a parked projection is degraded and still ready, an eviction leaves a hole, and a park is keyed by the generation | `TestAParkedProjectionIsDegradedAndStillReady`, `TestAnEvictionLeavesAHoleAndDecrementsNothing`, `TestTwoGenerationsShareAParkAndSeeNoneOfEachOthersLetters`, `TestParkSequenceIsRefusedOutsideTierA` |
| a halt is terminal and silent, and a drain finishes the pass in flight | `TestAHaltedProjectionIssuesNothingAndReportsThroughReady`, `TestADrainFinishesThePassInFlightAndAHaltedOneReturnsAtOnce` |
| a store failure is retried without limit and reported through `Ready` only past `Tolerate` | `TestAClosedStoreHaltsAndATransientBackendRecovers`, `TestASingleFailureFollowedByASuccessNeverReportsUnhealthy` |
| an unreadable cursor halts and never restarts at the origin | `TestAnUnreadableCursorHaltsAndNeverRestartsAtTheOrigin` |
| a lost fence is a turn, and the row's own cursor is the resume authority | `TestTwoLiveInstancesOfOneNameTakeTurnsAndNeitherHalts`, `TestASettlementResumesFromTheRowsCursorAndNeverFromItsOwn`, `TestTwoLiveInstancesUnderInUnitApplyEachEventOnce` |
| a refused save over the row the fence admits halts, and so does a forgotten row | `TestACheckpointStoreThatRefusesASaveItsFenceAdmitsHalts`, `TestAForgottenCheckpointRefusesTheNextSaveAndHalts` |
| an unconfirmed save is settled by one load and never by a second save | `TestAnUnconfirmedSaveIsResolvedByOneLoadAndNeverBySaving` |
| a wake is a hint, and a closed one stops waking | `TestAClosedWakeStopsWakingAndAnOpenOneStillDoes` |
| the router routes, declares and refuses the unclaimed | `TestARouterRoutesDeclaresAndRefusesTheUnclaimed` |
| nothing starts in a constructor and the supervisor owns the loop | `TestTheProjectionStartsNothingAndReadsNoEnvironment`, `TestTheSupervisorHoldsAProjectionAndNewStartsNothing` |
| a projection does not pass a position a writer could still commit, and passes a burnt gap | `TestAProjectionDoesNotPassAPositionAWriterCouldStillCommit`, `TestAProjectionInAUnitPassesABurntGap` |
| two live instances of one name over one schema behave as the two modes promise | `TestTwoLiveInstancesOfOneNameOverOneSchema`, `TestASettlementTakesTheRowsCursorWhenAShorterWinnerLeftIt` |
| the two modes leave measurably different state at one kill point | `TestTheTwoModesLeaveDifferentStateAtOneKillPoint` |
| three wirings to a second database are told apart | `TestThreeWiringsToASecondDatabaseAreToldApart` |
| a rebuild drains beside the live projection and the rows agree | `TestARebuiltProjectionDrainsBesideTheLiveOneAndTheRowsAgree`, `TestAGenerationDrainsBesideTheLiveOneAndTheRowsAgree` |
| the barrier is observed from the retiring generation's own rows, a generation that holds rows and still stands below it answers `Reached: false` with the distance and a cutover onto it is refused, the switch is one fenced write, and the rollback is the same call exchanged | `TestABarrierIsObservedAndReached`, `TestACutoverTakesNoBarrierAndDerivesItsOwn`, `TestTwoCutoversLeaveOneWinnerAndOneConflict`, `TestARollbackIsTheSameCallExchangedAndErrRetiredWhenTheRowsAreGone`, `TestTheBarrierTheCutoverAndTheRollback`, `TestTwoOperatorsCuttingOverAtOnce` |
| a cutover refuses on holes and never on the cumulative count, and a cover no member of which holds a row is refused | `TestACutoverRefusesOnHolesAndNotOnQuarantined`, `TestACutoverRefusesARetiringCoverNoMemberOfWhichHoldsARow`, `TestACutoverCannotBeHandedABarrier` |
| the switch is atomic for every table at once for a reader in one snapshot, and a reader that resolved the row first keeps reading the retiring generation | `TestTheCutoverSwitchesEveryTableAtOnceForAReaderInOneSnapshot` |
| the overlap window is what the retiring generation advanced under the switch | `TestTheCutoverWindowIsWhatTheRetiringGenerationAdvancedUnderIt`, `TestACancelledRebuildResumesFromItsRowAndNeverFromTheOrigin` |
| a generation is a name, and the retiring one cannot be told to write into the arriving one | `TestARetiringGenerationCannotBeToldToWriteIntoTheArrivingOne` |
| four partitions over one live log keep every key in order, and the modulus that replaced them reorders and skips | `TestFourPartitionsOverOneLogAndTheModulusControl` |
| a split's writes are one transaction live, a running parent halts, and an absent parent writes nothing | `TestASplitsThreeStatementsAreOneTransaction`, `TestARunningParentHaltsWhenItsRowIsSplitAway`, `TestASplitWithNoParentRowWritesNothing` |
| the park write and the advance are one commit live, the fast path costs nothing, a full park blocks and one DELETE clears it, and a redrive saves nothing | `TestTheBlockingTestAndTheAdvanceAreOneCommit`, `TestTheFastPathCostsNothingLive`, `TestAFullParkBlocksAndOneDeleteClearsIt`, `TestARedriveStopsAtTheRepeatFailureAndSavesNothing`, `TestTwoOperatorsRedrivingAtOnce` |
| a split leaves the parked letters reachable, and an empty park costs the children nothing | `TestASplitLeavesTheParkedLettersReachable`, `TestASplitOverAnEmptyParkLeavesBothChildrenPayingNothing` |
| tier A is proved and tier B is refused live, and a foreign destination gets four promises and not the fifth | `TestTierAIsProvedAndTierBIsRefusedLive`, `TestAForeignDestinationGetsFourPromisesAndNotTheFifth` |
| N partitions x M generations cost N x M walks, recorded as a number | `TestEightWalksCostEightTimesOneProjectionsReads` |
| a projection resumes through a second value over one backing | `TestAProjectionResumesThroughASecondValueOverOneBacking` |
| the replay benchmark measures what the snapshot deferral rests on | `TestTheReplayBenchmarkMeasuresTwoOrdersApart` |
| no exported function turns a position or a progress into a cursor, no cursor is ordered, no comment promises exactly-once delivery, and no snapshot is declared or published | `TestNoExportedFunctionTakesAPositionAndAnswersACursor`, `TestNoConstructorTakesAProgressAndAnswersACursor`, `TestCursorIsNeverCompared`, `TestNoCommentInTheProjectionPackagePromisesExactlyOnce`, `TestNoSnapshotAuthorityIsDeclaredOrPromised` |
| no exported function takes two cursors, and no modulus is applied to a sequence hash | `TestNoExportedFunctionOrdersOrTakesTwoCursors`, `TestNoModulusIsAppliedToASequenceHash` |
| this package opens no transaction, no published topology predicate is inert, no door takes an unchecked `Cover` or `Identity`, and every file of it is named by the reverse index | `TestNothingInTheProjectionPackageOpensATransaction`, `TestEveryPublishedTopologyPredicateHasACaller`, `TestEveryDoorTakingACoverOrAnIdentityRefusesItsZeroValue`, `TestEveryProjectionSourceFileIsNamedByTheFlowReverseIndex` |
| a mark is minted only from a number a store produced, renders nothing, and is refused on another projection's spec at both doors | `TestAMarkIsMintedOnlyFromANumberAStoreProduced`, `TestCommittedRefusesTheSixItCannotMint`, `TestAMarkMintedInsideTheWritingTransactionIsRefused` |
| a wait reaches on its first poll and never sleeps, and the derived spec answers what three hand-written ones get wrong | `TestAWaitReachesOnItsFirstPollAndNeverSleeps`, `TestWaitOfDerivesTheSameDefaultsNewApplies`, `TestTheDerivedSpecAnswersWhatThreeHandWrittenOnesGetWrong`, `TestTheDerivedSpecAgainstThreeHandWrittenOnesLive` |
| the park is asked before the census on every poll, a healthy wait never asks `Holds` or `Holes`, and a parked sequence is named rather than waited out | `TestTheParkIsAskedBeforeTheCensusOnEveryPoll`, `TestAHealthyWaitAsksSequencesAndNeverHoldsOrHoles`, `TestABarrierMintedMarkIsRefusedBesideAPark`, `TestTheParkedPairIsTheWholeOfTheAppendix`, `TestParkedInOnePartitionWhileAnotherLags` |
| a deadline says which kind of not-yet it was, a poll that cannot be made is terminal first and polled through after, and no field turns a refusal into a success | `TestADeadlineSaysWhichKindOfNotYetItWas`, `TestAPollThatCannotBeMadeIsTerminalFirstAndPolledThroughAfter`, `TestAPollThatFailsFirstAndAPollThatFailsFourth`, `TestSlowVersusStopped`, `TestThereIsNoFieldThatTurnsARefusalIntoASuccess` |
| a cutover under a wait is refused rather than answered, and the ownership row is read exactly twice | `TestACutoverUnderAWaitIsRefusedRatherThanAnswered`, `TestACutoverThatCommitsWhileAWaitIsRunning` |
| a wait starts nothing, saves nothing and costs one count per poll, beside the projection's own loop under `-race` | `TestAWaitStartsNothingAndSavesNothing`, `TestTheCallCountBudgetAndItsPlacement`, `TestAWaitBesideTheProjectionsOwnLoop`, `TestAConfirmedCommandIsVisibleWithoutASleep` |
| no refusal of a wait names a position or a key, and a commit spanning two sequences carries both | `TestNoRefusalOfAWaitNamesAPositionOrAKey`, `TestCommittedReadsTheCommitsOwnRangeAndNothingElse` |
| the projection pages state the three obligations a wait cannot check and do not restate `Highest` as a page's last position | `TestTheThreeObligationsAWaitCannotCheckAreStatedTogether`, `TestNoProjectionGuideRestatesHighestAsThePagesLastPosition` |
| this package costs the vocabulary plus `runtime` and nothing else | `TestNoEventPackageCostsMoreThanTheSeamItNames` |
