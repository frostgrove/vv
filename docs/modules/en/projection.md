# event/projection — a log becomes a read model, one checkpointed consumer at a time

```go
import "github.com/frostgrove/vv/event/projection"
```

**Module:** the root module. A projection needs a log, a checkpoint store and a
runner contract, and none of the three costs a third-party dependency
([[D-121]]) · **Depends on:** [event](event.md) for the log and the checkpoint
seam, [crud](crud.md) for the one executor question `InUnit` asks,
[runtime](runtime.md) for `Runner` · **Depended on by:** nothing

You give it a log to read, a checkpoint store to record through and a handler to
apply pages to. It walks the log from where it left off, hands each page to the
handler, records that it finished with the page, and keeps following. It opens no
transaction, starts no goroutine, retries nothing you did not ask it to retry and
writes no log line.

---

## What you get

| | |
|---|---|
| `New(Spec)` | a `*Projection`. Performs no I/O, starts nothing, reads no environment |
| `Projection.Run(ctx)` | the loop, on **your** goroutine. Blocks until the context is done and answers `ctx.Err()` |
| `Projection.Drain(ctx)` | stop after the pass in flight. Returns at once when there is none |
| `Projection.Ready(ctx)` | the readiness answer — a halt, or a retry streak past `Tolerate`. Names no importance ([[D-091]]) |
| `Projection.State()` | phase, progress, attempt, failure, instant |
| `Projection.Name()` · `Projection.Declaration()` | `vv.event.projection.<name>`, and `{Singleton, Durable}` |
| `NewRouter(Foreign)` · `On` · `TryOn` · `Ignore` · `TryIgnore` | the fan-out: one typed applier per declared fact |
| `Router.Skipped()` | envelopes skipped because their family is not one this router routes |
| `Batch` · `Handler` · `HandlerFunc` | what a handler is handed and what it implements |
| `Classify` · `Classifier` · `Verdict` | which failures are permanent, and how to say otherwise |
| `Failure` · `Halt` · `ParkSequence` | stop, or park the failing envelope's whole sequence |
| `Park` · `Letter` · `ErrParkFull` | the queue a parked sequence goes to, what one entry is, and the third verdict |
| `Redriver` · `Claim` · `ErrClaimLost` · `NewRedrive` · `Redrive.Sequence` · `Redrive.Any` · `Retried` · `RedriveSpec` | the operator's half: drain one sequence, or the least recently tried one |
| `State` · `Phase` · `Observer` · `ObserverFunc` | what an operator reads |
| `Partition` · `NewPartition` · `Whole` · `ParsePartition` · `Cover` · `NewCover` · `MaxPartitions` | a share of the key space, and the set that was checked |
| `Sequencer` · `ByStream` · `OneSequence` · `Unordered` · `SequenceBy` | who names a sequence |
| `Identity` · `NewIdentity` · `ParseIdentity` · `Split` · `SplitSpec` · `ErrTopology` | the one recorded name, and the one topology change there is |
| `Generation` · `Ungenerated` · `Generations` · `Barrier` · `Observe` · `Readiness` · `Reached` · `Cutover` · `CutoverSpec` · `ErrRetired` | a rebuild beside the live one, the evidence it is measured on, and the switch |
| `Effect` · `Effects` · `EffectsFunc` | the live side effects, which are not the projection |
| `Unchecked` | the destination this framework cannot resolve, said out loud |

```go
router := projection.NewRouter(projection.SkipForeign)
projection.On(router, Open, balances.opened)
projection.On(router, Credit, balances.credited)

following, err := projection.New(projection.Spec{
	Name:        "balances",
	Log:         event.ReadOnly(store),
	Checkpoints: checkpoints,
	Handler:     router,
	Advance:     projection.InUnit,
	Unit:        func(ctx context.Context, work func(context.Context) error) error {
		return crud.InNewTx(ctx, source, work)
	},
	Destination: source,
})

supervisor, err := runtime.Auto(following)   // Spec.Runners at the composition root; there is no Add
```

`Spec.Log` is an `event.Log` and a value that is also an `event.Store` is
**refused**: a projector that can append is how a replay writes. `event.ReadOnly`
is the wrapper that takes the surface away.

## Four names changed with the park, and you meet each as a compile error

There is no alias and no deprecation window, so they are written here as renames
rather than only documented under their new names: a consumer who upgrades reads
`undefined: projection.Quarantines` and needs this table, not the prose.

| Was | Is | Why |
|---|---|---|
| `Quarantines` (interface) | `Park` | it blocks the sequence now; the old name described a sink that did not |
| `Quarantined` (struct) | `Letter` | it is a queue entry with an order, not a record of a skip |
| `Failure`'s `Quarantine` (const) | `ParkSequence` | one mechanism, and a name that can coexist with the interface: a package-level `const Quarantine` beside a package-level `type Park` would read as two. It also says the thing that is new — the **sequence** is what is parked |
| `Spec.Quarantine` (field) | `Spec.Park` | a field rather than a package-level name, so it collides with nothing |

**`Progress.Quarantined` keeps its name and its meaning.** It is the kernel's
field — the durable cumulative count of what the destination did not take, which
never falls — and it did not move. It is the one piece of the old vocabulary that
is still correct, and finding it on this page is not evidence that the four above
are still there.

## The nine rows this page is for

Everything else here is prose. These nine are the contract.

### 1. A checkpoint is a cursor

What is recorded is the `event.Cursor` the log minted, and nothing else resumes
anything. `event.Progress` — the watermark, the two counts and the instant — is
an observation an operator reads; there is no function from a `Position` to a
`Cursor` anywhere in `event/…`, and adding one is [[D-129]] being reversed.
`Checkpoint.Advance` is a **fence** and not a position: a save lands if and only
if the stored advance is one below the one presented.

### 2. Delivery is at least once, in both modes

The framework deduplicates nothing, and **no mode delivers exactly once**. What
it hands a handler instead is the identity that makes idempotency cheap and that
it was already carrying: `(Stream, Version)` is unique and stable for every event
ever written, so an upsert keyed on it is the whole of what most handlers need.

| Mode | What it is |
|---|---|
| `AfterApply` (the default) | handle first, advance last. A crash between the two re-delivers the page |
| `InUnit` | the advance rides in `Spec.Unit`, the transaction **you** opened |

**`InUnit`'s promise carries a precondition, and the two are one sentence.** What
it makes atomic is the advance and *the writes the handler makes through the
context the unit gave it*. `Destination` names the handle those writes go
through, and the framework resolves it inside the unit, before the handler runs,
on every pass.

There are exactly two divergences, and only one of them is invisible:

- **A `Destination` bound to a different transaction than the checkpoint store's
  is seen, and refused.** The two authorities are compared for identity, and a
  unit that opened one transaction for the read model and another for the
  checkpoint row **halts** the pass with `ErrSpec` naming the projection, without
  calling your classifier and without downgrading to `AfterApply`. The remedy is
  one of two: bind both to the same transaction, or say `Unchecked` out loud.
- **A handler that writes past an aligned `Destination`** — straight to a second
  pool, a document store, a search index — is still outside that transaction and
  no check in this framework can see it. What you have there is `AfterApply`
  semantics under an `InUnit` spec.

A **foreign destination gets four promises and not a fifth.** Every committed
event reaches your handler at least once; the order within each stream is the
stream's; the resume point skips nothing; and every delivery carries a
`(Stream, Version)` that is unique across the whole run. What is *not* promised is
that the foreign effect happened exactly once — that is what `Unchecked` costs,
and the upsert on `(Stream, Version)` is the whole of what closes it. A
`Destination` the unit bound no executor for, or bound a non-transaction for, is
neither: it is `ErrSpec` and a **halt**, because an alignment that cannot be
proven is not one this framework asserts.

`Destination: projection.Unchecked` is how a composition says the handle cannot
be resolved through `crud`'s binding at all; a field left zero is refused rather
than read as it.
[`_examples/event-checkpoints-elsewhere`](https://github.com/frostgrove/vv/tree/main/_examples/event-checkpoints-elsewhere)
is that wiring, compiled.

> **A wiring that was accepted before and halts now.** A shipped `Advance: InUnit`
> spec whose `Unit` opens one transaction for `Destination` and another for the
> checkpoint store used to run and quietly deliver `AfterApply` atomicity. It now
> halts on its first pass. Align the unit, or say `Unchecked`.

### 3. Each attempt is handed its own page

`Batch.Envelopes` is yours from the moment it is handed over — indefinitely, from
any goroutine, capacity included, and you may write into it. A retry re-applies
the page it holds rather than re-reading, so the projection is a sender that is
still a reader and it pays for that: every attempt gets a fresh slice and a fresh
copy of every payload, the first included, with the log's own page kept untouched
beside them. It is the **eighth** hand-off of the framework's ownership rule and
the first whose sender re-reads what it handed over. `Batch.Attempt` is 1 on the
first delivery and rises on each retry.

### 4. An unclaimed type of a routed family halts

A `Router`'s covered families are the families of the facts registered on it.
Inside a covered family every recorded type must be routed by `On` or declared by
`Ignore`; anything else is `ErrUnrouted`, which the default classifier calls
permanent, and the projection halts. Outside every covered family an envelope is
skipped and counted by `Router.Skipped()`. So what a projection did not apply is
declared, or a family it has no business with, or a halt — and never a silence
([[D-131]]). `NewRouter(projection.RefuseForeign)` turns the skip into the same
refusal, for a log that is this projection's alone.

### 5. The log delivers in position order, and that is a law

Positions ascend within a page and across the pages one cursor tiles, and one
stream's events reach a handler in the order that stream holds them. Neither is a
capability a store may deny: `event.Capabilities` has no member for either and
will not get one, because a projector folding per stream against a store that
denied the second is silently wrong — in the read model rather than in the log.
A store that cannot hold both is not a conformant store ([[D-128]]).

### 6. `Progress.Highest` is a completeness watermark

One testable sentence: once a checkpoint carrying `Highest = P` has been saved,
a read resumed from that checkpoint's cursor answers only positions above `P`,
and no event at or below `P` is ever delivered to that projection for the first
time. *Delivered* — not *applied*: `Progress.Quarantined` is non-zero exactly
when the destination has holes, and that is what keeps `Highest` from reading as
a claim about the read model. *For the first time* — delivery is at least once,
so an event at or below `P` may certainly arrive again after a crash.

### 7. A partition is a share of the log, and only the first start chooses it

`Spec.Partition` is the fraction of the key space this runner reads. Every page
is filtered by it before your handler sees one, on the key `Spec.Sequence`
answers — `ByStream()` by default, which is the stream's family and key composed.
The zero value is `Whole()`, so a projection that names no partition is one
runner over everything and calls no sequencer at all.

A partition is `(id, mask)` with `mask = 2^k - 1`, and a key belongs to the one
whose id is the low k bits of FNV-1a/32 over the key's UTF-8 bytes. **It is a mask
and never a modulus**, and the difference is the whole point: under
`hash % N -> hash % (N+1)` roughly `N/(N+1)` of all keys change partition and each
lands in one whose checkpoint is at an unrelated position, so events of one key
are skipped in one direction and re-delivered out of order in the other. A mask
moves no key out of the parent's own share.

Declare the set as a `Cover` and build the runners from its members —
`NewCover` refuses a gap and an overlap by arithmetic, and those are the two
failures no single runner can see. A page this partition matches nothing in still
advances its checkpoint and calls no handler, so a watermark that moved is **not**
a claim that this runner applied anything — §6 is the promise it does make, and
the filter is why *delivered* and *applied* come apart here as well as at the
park. A panic out of a sequencer **halts**: an envelope with no sequence belongs
to no partition.

**Only the first start chooses the topology.** A partitioned runner started beside
a live checkpoint row for a coarser share of the same key space — the migration
"add `Partition:` to the spec and deploy" — is two writers over every key of that
share, so it is refused at its first resume with `ErrTopology` naming the row it
found. The route from a projection that has run is a split, which hands the
parent's cursor to its children and retires the parent; a topology that never ran
has no row to retire and starts wherever you declare it.

**N partitions are N runners and N independent walks of the log.** Partitioning
multiplies read traffic by N and parallelises the handler, which is the expensive
half and the reason to do it. The useful range is 8–16; `MaxPartitions` is 1024.

### The split, which is the whole of that route

```go
lower, higher, err := projection.Split(ctx, projection.SplitSpec{
	Checkpoints: checkpoints,
	Identity:    parent,                                    // NewIdentity("orders", 0, part)
	Unit:        func(ctx context.Context, work func(context.Context) error) error {
		return crud.InNewTx(ctx, source, work)
	},
})
```

One partition becomes two, both starting at the parent’s exact cursor, and the
parent’s row is retired — four writes inside the transaction **you** open, on
your goroutine. The framework opens nothing and starts nothing.

Drain the parent first. A running parent whose row is split away finds it absent
at its next save and **halts**, which is the correct and visible outcome of
retiring a live row; the order is drain, split, start the children from a `Cover`
of the two identities it answered.

It is refused with `ErrTopology` when the parent has **no** row — whether it never
ran or its row was lost, because the rows tell those apart from nothing and
guessing "fresh" starts two children at the origin against a live read model —
and when a child row is already there beside a live parent, which is a finer
topology already recording. Nothing is carried between two runs of the body, so a
`Unit` that runs it twice performs one split or answers a refusal.

Both children take the parent’s cursor **and** its `Progress.Highest`; the
lower-numbered one takes its `Applied` and `Quarantined` and the higher starts at
zero, so the sum across the set is unchanged. There is no merge: it would have to
order two cursors, and a cursor answers equality and emptiness only.

**A split is one way, and putting the previous release back is refused.** The
fourth write is the record of the retirement: an ordinary checkpoint row named
`<identity>#split`, carrying the cursor the parent was handed down from. Deploying
release *n* again — the same spec, with no `Partition:` — halts at its first
resume with `ErrTopology` naming that row. Without the record that release finds
no row of its own and none for any coarser share either, resumes from the origin,
and walks the whole log a second time into the read model the live children are
filling — with three rows all reporting a healthy watermark and no error on any
path. The same holds one level down: `orders#1.1` redeployed after it was itself
split into `orders#1.3` and `orders#3.3` is refused the same way.

Two ways out, and both are deliberate. The supported one is a **new generation**:
declare the target topology at `Generation: n+1`, rebuild it beside the live one
and cut over. The other is to `Forget` the `#split` row, which re-admits the
coarser topology exactly as it stood — the refusal names the row so an operator
can, and nothing else in this package writes or removes it.

A projection name within six bytes of `event.MaxNameBytes` has no room for the
mark, and `Split` refuses such a name with `ErrTopology` rather than retiring a
share whose retirement it could not record. The migration for a name that long is
a rename, which is a rebuild.

A third-party `event.Checkpoints` owes two obligations a split rests on, and both
are certified by `eventtest.RunCheckpoints`’ `topology` and `topology handoff`
sections: a cursor written under one projection name reads back unchanged under
another, and a save at advance 1 over a live row is refused by the store’s own
fence.

### 8. A generation is a name, and a cutover is one fenced write

`Spec.Generation` is a number in the recorded name and nothing else: `orders` at
`Ungenerated` renders `orders`, and `Generation: 2` renders `orders@2`. Two
generations of one projection are two checkpoint rows, two parks, two
destinations and two runners over one log, with **nothing between them** — there
is no API by which the arriving one could read, pause or reset the live one's
checkpoint.

**This is the rebuild recipe, and it replaced the second-`Spec.Name` one.** A
rebuild spelled as `Spec.Name: "orders-rebuild"` still works and still replays
the whole log, and what it does **not** get is the effect gate below: the
framework cannot distinguish a second projection name from a rebuild of the
first, so a second name carrying `Effects` stages an effect for every historical
event. `Spec.Generation` is the spelling the gate covers.

The evidence is derived rather than supplied, and there is **no barrier field on
`CutoverSpec`**: a barrier a caller can invent is not evidence, and the zero value
of one admits a generation that has delivered nothing.

- `Observe(ctx, checkpoints, of, over)` answers the **lowest `Highest`** across a
  cover, which is the only aggregate a set of rows has. All members fresh answers
  the origin with a nil error; **some** fresh and some not is `ErrTopology`
  naming one that holds no row, because an absent row read as position zero is a
  barrier every arriving generation clears.
- `Reached(...)` answers `Readiness{Reached, Behind, Quarantined, Holes}`.
  **`Reached` means DELIVERED, not "the rows agree".** It is `>=` against the
  lowest `Highest` of the arriving generation's own cover, plus `Holes == 0`, and
  that is the strongest statement available without a barrier token. A consumer
  who wants "the rows agree" runs the comparison
  `TestAGenerationDrainsBesideTheLiveOneAndTheRowsAgree` demonstrates.
  `Behind` is an upper bound and a hint: a log burns a position for a rolled-back
  append and for an optimistic-concurrency loser, so the distance between two
  positions is not a count of undelivered events.
- `Cutover(ctx, spec)` observes its own barrier from the retiring generation's
  rows, measures the arriving one against it, refuses on `Holes`, and moves the
  row once, fenced — all inside the unit **you** open. A rollback is the same call
  with `From` and `To` exchanged.

`Cutover` never refuses on `Progress.Quarantined`. That count is cumulative and
never falls, so a generation that parked a sequence and redrove it completely
would be refused by it for ever — the ordinary recovery path closed by the check
meant to guard it. What it reads is `Park.Holes`, which is what the queue answers
now.

### 9. The live effects are not the projection

`Spec.Effects` is the capability to cause a side effect, **as a value**. A
`Handler` is handed a `Batch`, and a `Batch` carries a name, an identity,
envelopes and an attempt — there is no field on it through which an effect could
be caused. A rebuild is a spec with `Effects` nil, which is the whole of "a
rebuild does not get the effect-dispatch capability".

`Stage` is named for where it runs: **inside the transaction that commits the
advance**. What it does must roll back with it — a staged job ([[D-118]]), a row
in your own tables. An HTTP call, a payment, a mail or a non-transactional publish
here is sent again on every rollback the delivery can take, and there are four: a
lost fence (which is what a rolling deploy does on purpose), a full park, a later
envelope's permanent failure in the same page, and a serialisation failure your
own `Unit` answers.

**An HTTP call inside the unit is not protected by the fence.** The fence decides
which writer's rows land; it decides nothing about a socket that is already open.
A losing instance that dialled out inside its unit has dialled out, and its rows
are gone.

**A skipped event's effect is never sent.** An effect follows its envelope: a
parked envelope is not staged and is not lost, because its letter carries it and
the redrive that applies it is what stages it. An **eviction** stages nothing, ever
— a skip is you saying the event will never be applied.

Three things suppress a stage, checked cheapest first: a nil `Effects`, which
costs nothing; `Spec.EffectsAfter`, per envelope and never per page; and the
ownership row, read inside the committing transaction. A page entirely at or
below the barrier does not call `Stage` at all — not even with an empty slice.

`Effects` is refused beside `AfterApply`, and that is the **outbox asymmetry** to
read twice. Under `InUnit` the stage, the handler's rows and the advance are one
commit. Under `AfterApply` they would be three, and the window between them is
the one a broker outage turns into a permanently lost event: the advance moves
past an envelope whose effect was never staged, and nothing will ever offer that
envelope again. `AfterApply` remains a legitimate mode for a projection with no
effects; it is not one for a projection with them.

This is a **contract and not a sandbox**. Nothing here can stop your handler
dialling out; what the shape does is make the honest thing the easy thing and the
dishonest thing visible in a review. The half that is enforced is the framework's
own reach: no package `event/projection` reaches, transitively, imports `net`,
`net/http`, `net/smtp` or `os/exec`.

## Waiting for a change to be visible

After a confirmed command the next read must not show the old state, and a test
must not need a `sleep`. A wait polls **one** named generation over **one** cover
until everything at or below a minted position has been delivered — and, where you
supply a `Park`, **applied**.

| | |
|---|---|
| `WaitOf(spec, over)` | a `WaitSpec` derived from the projection's own `Spec`. No I/O, starts nothing |
| `WaitSpec` | `Checkpoints` · `Park` · `Sequence` · `Generations` · `Of` · `Over` · `Until` · `Every` · `Ticks` |
| `WaitSpec.Committed(ctx, store, commit)` | the `Mark` for an append that has committed |
| `MarkOf(barrier)` | the `Mark` a `Barrier` was observed at |
| `Mark` | `At()` · `Zero()` · `String()` — `"[mark]"`, and no field a caller can write |
| `Wait(ctx, spec)` | polls on **your** goroutine until reached, refused, or the context is done |
| `Visibility` | `Reached` · `At` · `Behind` · `Moved` · `Quarantined` · `Parked` · `Polls` |
| `ErrNotVisible` · `ErrParked` · `ErrUncommitted` · `ErrGeneration` | the four answers that are not "reached" |

```go
waiting, err := projection.WaitOf(spec, cover)     // once, at the composition root
...
// in the handler, after crud.InNewTx has COMMITTED:
mark, err := waiting.Committed(ctx, store, commit)
if err != nil {
	return err
}
waiting.Until = mark

vis, err := projection.Wait(ctx, waiting)
switch {
case err == nil:
	return serveFresh()
case errors.Is(err, projection.ErrParked):
	return serveParked()                       // a redrive is the fix; waiting is not
case errors.Is(err, projection.ErrNotVisible):
	return serveStale(vis.Behind)              // a branch you wrote, not a flag you passed
default:
	return err
}
```

**`WaitOf` is the spelling this page gives**, and the struct stays assemblable by
hand for the host that assembles its runners by hand. It derives the five facts a
wait needs from the `Spec` the runner was built from, through the same defaults
`New` applies, and refuses the zero `Cover` and a spec that names no identity.
`Until`, `Every` and `Ticks` are yours to fill in — and `Until` is the only one a
request path has any business writing. `Every` and `Ticks` are deliberately **not**
taken from the `Spec`: a projection's interval is its idle policy and a wait's is a
request path's latency budget.

### What a mark is, and why you cannot write one

Both doors take a number a store produced. A mark you could build is evidence you
invented — the same door `Cutover` closes by having no barrier field.

`Committed` reads the commit's **whole range** back from the store, after your
transaction has committed. `Commit` carries no position by design: inside an
uncommitted transaction an envelope's position is unspecified, and one store has it
already while another answers zero. So the map from a version to a position costs
one `ReadStream` for a commit that fits a page, and this call is where that round
trip is rather than hidden inside the poll loop. It reads the whole range and not
only the last event because a `Sequencer` is any function of an envelope: a commit
of three facts can belong to three sequences, and a mark carrying only the last
one's key would ask the park about one third of what it is waiting for.

Six refusals, and each is a mark that would have lied: an **empty commit**; a
**transaction of this store's bound to `ctx`**, because a position read inside the
transaction that wrote it belongs to an append that can still roll back — and a
rolled-back append burns its position, which no projection ever delivers, so a
wait on such a mark never reaches; a store that does **not show `commit.Last()`**
(`ErrUncommitted`, which tells "not committed yet" from "rolled back" no better
than a second connection can); a **zero position** (`ErrUncommitted`); a page the
store's own answer is **not honest about** — longer than its published
`StreamPage`, or carrying any envelope that is another stream's or is not one
version above the one before it (`event.ErrBackend`); and a **nil `Sequence`
beside a non-nil `Park`** (`ErrSpec`).

`MarkOf(barrier)` is the other door — a barrier `Observe` folded from a
generation's own checkpoint rows. It carries **no sequence key**, because a
barrier has no envelope to ask a sequencer about, so `Wait` refuses it beside a
non-nil `Park` rather than quietly answering *delivered* to a caller that asked
*applied*.

**A mark minted from one projection is refused on another**, at `Wait`'s door and
before any store call, with `ErrSpec`. Two projections over one log is an ordinary
deployment, and the values are each individually right:

```go
mark, err := orders.Committed(ctx, store, commit)   // keys: orders' sequencer's
invoices.Until = mark                               // compiles, and is a lie
```

The position is global, so the census would reach; the park would be asked under a
key no letter of `invoices` was ever written under, `Holds` would answer false, and
the caller would be told *reached* for an event `invoices` parked. **To wait on a
second projection, derive that projection's own `WaitSpec` and call its
`Committed`** — one more `ReadStream`, and the right park asked the right
question. The comparison is on the **projection name alone** and never the
generation, because a barrier of another generation of the same projection is the
cutover case a wait admits.

### What one poll does, in order

1. **The ownership row**, on the first poll, where `Generations` is supplied. A
   wait scoped to a generation reads do not resolve to is asking about a read
   model this caller is not reading, and every other answer would be beside the
   point.
2. **The park**, before the census and on **every** poll. Your change may live in
   one partition that parked it while another partition merely lags; a wait that
   asked only after reaching would burn its deadline on a condition it could have
   named at once. `Sequences` first, and `Holds` only behind a non-zero count.
3. **The census** — the lowest `Highest` across the cover's members, which is the
   only aggregate a set of checkpoint rows has.
4. **The ownership row again**, on the poll that would otherwise answer reached.
   That read turns a false success into a refusal when a cutover committed while
   this wait was running.

**Two ownership reads over the whole wait, and never one per poll.** A per-poll
read buys a faster refusal for a case the caller cannot act on any sooner, at one
more `SELECT` per interval per waiter on a row every read path of the deployment
already contends for. *Over the wait* and not *on two polls*: a caught-up
deployment reaches on its first poll every time, so both reads land on that one
poll and the cost is one extra `SELECT`. What may not move is the **last** read.

**A wait scoped to no generation asks nothing.** `Generations` nil says the caller
knows which generation it is reading, which a deployment tool waiting on an
*arriving* generation on purpose does.

### The exits, and what each one is not

| Exit | Error | What it means |
|---|---|---|
| reached | `nil` | everything at or below the mark was delivered in this generation — and applied, if a `Park` was supplied |
| parked | `ErrParked` | one of the mark's sequences is held in this generation's queue. **Terminal**: waiting longer cannot help and a redrive can |
| another generation | `ErrGeneration` | reads of this projection resolve elsewhere. Read `Active` and wait again on the generation it answers |
| deadline | `ErrNotVisible` wrapping `context.DeadlineExceeded` | not yet, and `Visibility` carries what the last readable poll saw |
| cancelled | `ctx.Err()` bare, zero `Visibility` | the caller stopped asking |
| unreadable | `ErrTopology` / `event.ErrBackend` | the poll could not be made at all |

**Five things `Reached` is not**, and they are on this page because each is a
sentence somebody would otherwise assume:

1. **Not global linearizability.** It is a statement about one named projection
   generation, over one named cover, resolved against one checkpoint store. A
   second projection of the same log, a second database, a second generation and
   anything outside the cover are all unaddressed.
2. **Not another replica's freshness.** The wait reads through the context it was
   given; if that reaches a read replica, it reports that replica's view. Visibility
   up to the mark holds for a reader that resolves the read model on the authority
   the projection wrote it through, and for no other reader.
3. **Not "the projection is caught up".** There is no head. `Reached` says the
   watermark passed the mark and says nothing about anything above it.
4. **Not "the event was applied", unless the park was asked.** With `Park` nil,
   `Reached` means *delivered*, and delivered is not applied for a projection that
   parks. `WaitOf` is what makes supplying it a derivation rather than a thing to
   remember.
5. **Not "the generation I am about to read".** With `Generations` nil, `Reached`
   is about the generation `Of` names, and the gap between the last ownership read
   and your own read is the cutover's named overlap window seen from the read
   path's side.

**There is no stale-read field.** No `AllowStale`, no `OnTimeout`, nothing that
turns a refusal into a success. A wait that did not reach returns a **filled-in**
`Visibility` beside its refusal, so serving stale data is a branch a reviewer can
see rather than a flag every caller ends up passing.

**`Moved` is exactly what it says.** It is whether `At` changed across this wait's
polls, and it is the one field that tells a slow projector from a stopped one. It
costs nothing — the census read the number anyway. **With `Polls` below two it is
always false and means nothing**, because one observation cannot show a change;
and a projector between two slow passes has not moved either.

**`Behind` is an upper bound and a hint**, for the reason `Readiness.Behind`
already carries: a log burns a position for a rolled-back append and for an
optimistic-concurrency loser, so the distance between two positions is not a count
of undelivered events.

**A poll that cannot be made is terminal on the first poll and polled through
after it**, and the poll number is the whole of that rule: a cover that is not the
one the rows are recorded at, a `Checkpoints` pointing elsewhere and a `Park` that
refuses outside a unit are wrong on poll 1 and wrong for ever, so they come back at
once instead of after a deadline; a refusal that appears later is the deployment
moving, and the deadline decides with that last refusal wrapped into it. **The
caller's context outranks the poll number**: a poll whose refusal arrives with the
caller's own budget already done takes the deadline exit or the cancellation exit
on poll 1 exactly as on poll 4 — and that refusal is *not* wrapped into the
deadline, because a store reached with a done context answers a store class and a
deadline carrying one reads as a cover misconfiguration that does not exist. A
poll that **answered** still answers: `ErrParked` and `ErrGeneration` are
conclusions drawn from rows that were read, and a caller can act on a redrive and
cannot act on a timeout.

**A generation with no checkpoint rows at all is not an error.** The census has a
third answer beside *a number* and *a partial cover*: no member has a row, so
`At` is zero, nothing is folded, and the wait simply does not reach — which is
what a wait on a generation that has never started should do.

### What a wait costs

Count it per poll, per waiting caller, and `Every` defaults to **50 ms**, so a
poll is twenty of these a second:

| Read | How many | When |
|---|---|---|
| `Park.Sequences` | 1 | every poll, while a `Park` is supplied |
| `Park.Holds` | 0, or up to one per sequence key the mark carries | only while that count is **not** zero |
| checkpoint `Load` | 1 per cover member | every poll |
| checkpoint `Load` again | 1 per cover member **that holds no row yet** | the census asks an absent row whether a split handed it down |
| `Generations.Active` | 2 over the **whole wait**, not per poll | the first poll, and the poll that would otherwise answer `Reached` |

So `1 + |cover|` while nothing is parked, `1 + 2 x |cover|` while the generation's
members hold no rows. **A four-member cover at the default interval is 100 small
indexed `SELECT`s a second per waiting caller**, 120 while that generation's queue
is not empty, and **180 during a rebuild** — 200 with a non-empty queue. Fifty
concurrent waiters on a healthy four-member cover is **five thousand a second**.
Cheap until it is not. **The lever is `Every`**: a request path that wants 200 ms
writes 200 ms, and the default is not raised because a default cannot be
*invisible* and this arithmetic can be on the page instead. A wait during a
rebuild is the expensive case — a rebuilding generation's members have no rows
yet, so each poll pays the doubled census.

A wait **starts nothing and writes nothing**: no goroutine, no `Save`, no
`Forget`, no transaction, no log line, span or metric. It costs exactly nothing
when nobody is waiting, which is why there is no shared poller.

### Three obligations a wait cannot check, beside `Sequencer`'s three

A hand-assembled `WaitSpec` carries three obligations nothing at any door can
see, and each is silent when it is broken. `WaitOf` discharges all three by
derivation, which is why it is the spelling this page gives.

1. **`Sequence` must be the sequencer the projection runs.** Another one produces
   keys no letter was parked under, so the park answers false for a change that is
   held and the wait reports reached.
2. **`Park` must be the projection's queue, if it has one.** Nil against a
   projection that parks answers *delivered* where the caller asked *applied*.
3. **`Over` must be the cover the rows are recorded at.** A minimum folded over
   the wrong set is a minimum across a hole.

They sit beside the three `Sequencer` already carries, for the same reason —
**none of the three is checked at run time**, and the supported way to change a
key is a new generation:

1. **Total.** Every envelope gets a key. An envelope with no sequence has no
   sequence to be parked in, so a panic out of a sequencer halts the projection
   rather than failing its page.
2. **Pure.** It reads the envelope and nothing else: no clock, no map iteration,
   no process-local state.
3. **Stable for the life of the log.** The key an envelope produced in one release
   is the key it must produce in every later one.

## What you own, and what goes wrong when you do not

Six obligations, none of which this package can check, each with the cost of
missing it.

**1. Add `Spec.Generations` to the live projection one release BEFORE the first
cutover.** The suppressor is gated on the field being supplied, never on
`Spec.Generation` — gated there it would never run for a projection at
`Ungenerated`, which is every projection that exists today. A projection at
`Ungenerated` whose spec carries a `Generations` reads the row, finds
`Ungenerated` (a projection with no ownership row answers `Ungenerated` and a
**nil error**), and stages exactly as it did without the field. Miss the ordering
and the first `Cutover(From: Ungenerated, To: 2)` runs **two senders** until the
retiring projection is stopped.

**2. `Generations.Active` must be a LOCKING read** — `SELECT active … FOR SHARE`,
`FOR KEY SHARE` — or run in a `SERIALIZABLE` unit, and it must resolve the
**ambient** transaction rather than a pool of its own. [[D-126]] leaves the
isolation level to you, and at `READ COMMITTED` a plain read of that row and a
concurrent `Activate` of it do not conflict: both commit, and a generation the row
no longer names commits the effect it staged anyway. `REPEATABLE READ` does not
close it either. What goes wrong when it is not met is a retiring pass committing
a staged effect under a row that already names the arriving generation, with both
units committing and neither rolling back. The two-sender boundary is stated only
with this sentence beside it. **`eventtest.RunGenerations` measures it** — its
`locking read` section holds one unit open over the row and asserts that a
concurrent `Activate` waits, and a factory that declares
`eventtest.SerializableUnit` is told what the harness cannot measure rather than
passed for it.

**3. `Spec.EffectsAfter` is a deployment-held constant, and only half of the
barrier is durable.** The envelope's side is where the resumed checkpoint left
this generation, so an interrupted warm-up resumes suppressed. The barrier's own
side is the number you wrote in the spec — nothing stores what a generation was
warmed up under and nothing compares a restart's value against it. A release that
lowers or drops the field re-stages the whole warm-up below it, on the first
pass, with no error and no refusal. You get the number from `Observe`'s
`Barrier.At` on the generation that is live now, and a rollback of the release
that set it is a rollback of the barrier too.

**4. The covers a cutover declares must be the ones those generations record
at.** `Cutover` reads the rows the cover names and no others. A live generation
running at four partitions declared here as `Whole()` answers silence, and silence
is `ErrRetired` rather than a barrier at the origin; a cover whose members a
`Split` retired is `ErrTopology`. Print the identities, not the shape you
remember.

**5. Drain or stop the retiring generation before, or as, the switch commits.**
The barrier is that generation's watermark as `Cutover` read it, and that
generation is a separate runner committing in its own transaction. If it is still
advancing it goes past the barrier while the unit is open, and the read target
then moves to a generation standing where the retiring one stood a moment ago:
**reads move backwards**, by that generation's advance over the life of the
transaction, and stay there until the arriving generation catches up. `Observe` it
twice and see whether the barrier moved — that is the whole of the check. Drop
`Spec.Pace` on the arriving generation first, because pacing lengthens exactly
the recovery this window needs.

**6. The claim duration must exceed the longest unit a redrive may take.** A
`Claim.Until` shorter than one letter's apply turns every redrive into a run of
lost claims that drains nothing, and `ErrClaimLost` is what you see when it is.
The framework reads no clock and enforces no expiry: the duration is your table's.

## One name is one writer, and the fence is what holds when it is not

`Declaration()` answers `{Singleton, Durable}`, and that is a promise to the
deployment rather than an enforcement. Every rolling restart runs two instances
of a singleton on purpose for a few seconds, so the interesting question is what
the second one does.

**A save the fence refuses does not halt the projection.** It takes the row the
winner left, rebuilds its reader from **that row's** cursor, drops the page it
held and backs off. `ErrOvertaken` reaches `State.Err` on every lost fence, the
streak it opens is cleared by a save that lands and by nothing else, and `Ready`
reports it once the accumulated backoff outlasts `Tolerate` — which is the signal
a deployment acts on, and the reason the backoff is not merely a delay: it bounds
the duplicate rate to one page per `Backoff.Max` while the contention lasts
([[D-133]]).

**What the two modes cost while two instances overlap** is different, and it is
measured rather than argued:

- Under **`InUnit`** the advance is presented *before* the handler runs. Against a
  checkpoint store that evaluates its fenced save against a tuple it is holding —
  PostgreSQL, at advance 1 by speculative insertion and above it by the row lock —
  the losing instance blocks there and is refused before it applies anything.
- Under **`AfterApply`** there is nothing to hold across a handler call, so
  **both instances apply the overlapping page**. The handler must be idempotent.
  That is not a defect; it is what at least once means here.
- A store that stages its save optimistically and revalidates at commit — the
  in-memory one — lets both apply under `InUnit` too and rolls the loser's half
  back. Both behaviours are conformant; only the first avoids the wasted work.

**What settles an unconfirmed save is the cursor the row carries, never the
advance alone.** The fence admits one writer at each advance, so a row standing at
the advance this pass presented is this pass's own save *or* a second instance's,
and the cursor beside it is what tells them apart. A third-party `Checkpoints`
therefore owes a byte-faithful cursor round trip — the `round trip` and `bounds`
sections of `eventtest.RunCheckpoints` are where that obligation is certified, and
a store that re-encodes a cursor is refused there rather than discovered here.

Two things still halt, because they are not contention: an **absent** row, and one
**behind** the fence. Both mean the name was forgotten, reset or restored under a
running projection, and creating a fresh row at advance 1 over a live read model
is a silent restart at the origin.

## The failure table

| What answered | Where | What the pass does |
|---|---|---|
| `context.Canceled` / `DeadlineExceeded` | anywhere | `Run` returns |
| `ErrBackend` | read or save | retried without limit; the streak feeds `Ready` |
| `ErrClosed`, `ErrRefused` | read or save | halt — retrying a closed store is a busy loop that never clears |
| `ErrCursor` | read | halt, naming the projection. **Never a restart from the origin** |
| `ErrWrongStore` | resume or save | halt; a terminal wiring refusal |
| `ErrConflict` | save | the row is read once and the settlement decides: a turn, a re-delivery, or a halt |
| `ErrUncertain` | save | the same one load. A second save could only guess |
| the handler's own error | `Apply` | `Classifier` decides |
| a handler panic | `Apply` | recovered, permanent, rendered by your observer and by no line of ours |
| `Retryable`, `Attempts` exhausted | `Apply` | becomes permanent |
| `Permanent` | `Apply` | `Halt`, or the isolation pass when `OnPermanentFailure` is `ParkSequence` |
| `ErrParkFull` | `Park.Park` | the third verdict: the unit rolls back, nothing is parked, no envelope is skipped, `PhaseBlocked`, retried **without an attempt budget** |
| anything else | `Park.Park` | **halt**, naming the park — a policy with nowhere to record is a skip with extra words |
| any error | `Park.Sequences`, `Park.Holds` | retried without limit and without an attempt, like `ErrBackend`: a read of your own table that blinked is not a reason to stop a live projection |
| anything else | read or save | **halt**, naming the door and the sentinel |

The last row is what makes the table total. A pass that cannot classify its own
failure must not fall through to the retryable arm, which is how a structurally
impossible write becomes an infinite loop.

## The park: what a permanent failure blocks, and who unblocks it

**`ParkSequence` parks a sequence, not an event.** When a page fails permanently
under it, the page is delivered again one envelope to a `Batch`, in position
order: one that applies is applied, one whose failure is permanent goes to your
`Park` with its cause — **and so does every later envelope of the same sequence,
which never reaches the handler at all**. Every other sequence in the page carries
on. That is the whole difference between a dead-letter queue and a skip list: a
handler is never handed `OrderPaid` for an order whose `OrderCreated` was parked.
A **retryable** failure ends the pass and returns the whole page to retrying under
the same attempt budget — a database that went away is not a corrupt payload.

One clause is exact and is stated rather than implied: the whole-page attempt
that **discovers** the failure had every envelope of that page in one batch and
rolled it back as one. From the moment the failure is known, an envelope behind a
parked one is never delivered again — and an envelope whose sequence the queue was
**already** holding never reaches the handler at all.

```go
Advance:            projection.InUnit,      // required
Destination:        source,                 // required, and not Unchecked
OnPermanentFailure: projection.ParkSequence,
Park:               yourQueue,              // your table, your transaction
```

**It constructs at that tier and no other, and `New` refuses the rest.** The order
a park promises is not a property of the queue; it is a property of the queue
**and the read model committing together**. Take the transaction away and a
redrive that evicts the blocking letter while its write for the next one is still
in flight lets the loop read a `Holds` of false and apply the one after that — the
exact defect the queue exists to prevent, from two supported operations, with no
error on any path. What a foreign destination gets instead is `Halt`, which is
honest.

**A `Park` supplied beside any other policy is accepted and inert.** One
composition root builds one spec for a live generation and a rebuild, so the
field arrives beside `Halt`; the loop then never counts the queue, never asks
`Holds` and never writes a letter, and the projection behaves exactly as one with
no `Park` at all. The field alone does not turn the blocking path on — only
`OnPermanentFailure: ParkSequence` does, and that constructs at one tier.

**Where each method runs is part of the contract, and the four differ.**
`Park.Holds` and `Park.Park` are called **inside** your unit of work — that is
what orders a redrive's eviction against the loop's blocking test. `Park.Sequences`
is called **outside** it, before the pass opens one, because a healthy projection
would otherwise open a transaction every pass to be told the queue is still empty.
`Park.Holes` is a cutover's question and no pass asks it. So do not require the
ambient transaction in `Sequences`: a refusal there is read as a postpone, and a
projection whose count can never be read retries for ever without advancing and
without halting.

**One sentence of that contract widened when the wait landed, and it is
`Holds`'s: a wait also asks it OUTSIDE a unit of work, and an implementation must
answer the committed state there.** `Wait` has no unit — it runs on a request
path's own goroutine — and `Sequences` and `Holds` are the only two `Park` methods
it calls. What the inside-a-unit call buys is a pass's property and is unchanged;
a wait needs no ordering, only a committed answer, and the `Sequences`-first gate
means a projection that has parked nothing never reaches the widened clause. **One
sentence, and it is `Holds`'s** — `Sequences`'s outside-a-unit rule is unchanged,
`Park` is still a pass's write, and `Holes` is asked by no wait at all
([[D-144]]). **`eventtest.RunPark` certifies all of it** — six sections, one per
tier the four methods run at, with `committed state` for the widened clause and
`bounds` for the two numbers that are yours. See [eventtest](eventtest.md).

**A healthy projection pays nothing.** `Park.Sequences` is read once per resume —
a process start, or an overtaken that adopted another instance's row — and again
at the start of every pass while the count is non-zero. While it is zero,
`Park.Holds` is never called: no round trip, no cache, no eviction policy, no TTL.
A degraded projection pays one count per pass and one existence check per
envelope, and the pass after a drain empties the queue reads zero and returns to
the healthy cost. Only the projection parks and only a redrive removes, so a
letter you insert by hand leaves the count low until the next pass that reads it.

**The queue is keyed by `Identity.Whole()`** — the projection and its generation,
with the partition dropped, rendering `orders@2`. The framework hands your `Park`
and your `Redriver` nothing else, so keying your table on the value you are given
is correct by construction. A split therefore needs to do nothing about the queue,
a redrive is generation-wide, and the cost is stated rather than hidden: a
partition pays the existence check while **any** partition of its generation holds
a parked sequence.

**Bound it in two dimensions, and ask per sequence.** A new sequence is refused
when the queue is at its own limit; an existing one when it holds its own limit of
letters — so a queue holding 1023 sequences of one letter each still takes a
1024th letter into an existing sequence. Axon's numbers are 1024 and 1024 and have
been in production somewhere. A byte bound over the sum of parked payloads is
computable because `Limits.MaxPayload` bounds each one, and it is the bound that
matters where the payload is a `bytea`. Answer `ErrParkFull` and the pass blocks
rather than skipping: `PhaseBlocked`, the unit rolls back so nothing at all is
parked including the letters earlier envelopes of that page wrote, no advance, no
envelope skipped, no attempt consumed, and one `DELETE` clears it.

**That bound is yours and this framework can neither call it nor certify it.**
There is no `isFull` on the `Park` interface — what this package declares is
`ErrParkFull` and what a pass does about it — so a one-dimensional `isFull()` is
a mistake nothing here catches. What it costs is measured rather than asserted:
at 1024 sequences it refuses the events *behind* a blocker it is already holding,
so the whole partition sits in `PhaseBlocked` while the blocked order's own room
goes unused, and only an operator's `DELETE` clears it. Ask the bound of the
sequence.

**A spent attempt budget parks a transient failure too, and that is the trade
`ParkSequence` buys over `Halt`.** `Attempts` bounds handler failures without
regard to their class once it is spent, so a database that goes away for that many
passes parks every sequence of the page the projection was on, each letter
carrying the transient cause. Under `Halt` the same outage stops the projection
instead. Neither loses an envelope; the difference is which one an operator
clears. The recovery is a redrive, and a host that wraps `Redrive.Any` in a
`runtime.Runner` clears such a page without an operator once the database is back.
Raise `Attempts` if your outages are longer than your budget.

**Two phases, and only one of them fails readiness.**

| Phase | When | `Ready` |
|---|---|---|
| `PhaseFollowing` | the whole log is read and nothing is parked | passes |
| `PhaseDegraded` | the whole log is read and N sequences are parked | **passes** |
| `PhaseBlocked` | the last pass ended in `ErrParkFull` | fails past `Tolerate` |

`Degraded` deliberately does not fail readiness: a replica reporting unhealthy for
one poison order is a projector that stopped by another route. What carries the
fact is `State.Parked`, the live count, beside `Progress.Quarantined`, the durable
cumulative one — which is **never decremented**, not by a successful redrive and
not by an eviction. It is the permanent statement that this destination once had
holes. Whether it has one **now** is a different question, and it is asked of
`Park.Holes`: the letters queued now plus the letters evicted without being
applied.

### The redrive

Not automatic, and it never will be: a background retry is a goroutine, and there
are none under `event/`. Wrap `Redrive.Any` in a `runtime.Runner` with a large
interval if you want a schedule.

```go
redrive, err := projection.NewRedrive(projection.RedriveSpec{
	Identity:    projection.NewIdentity("orders", 2, projection.Whole()),
	Handler:     router,
	Park:        yourQueue,                                  // a Redriver
	Unit:        func(ctx context.Context, work func(context.Context) error) error {
		return crud.InNewTx(ctx, source, work)
	},
	Destination: source,
})
retried, err := redrive.Any(ctx)   // or redrive.Sequence(ctx, "order-7")
```

- **One sequence per call, in insert order, stopping at the first letter that
  fails again.** A sequence is a queue and not a set. The letter that failed is
  requeued with its new cause and a raised attempt, and the call answers
  `Retried{Applied, Left, Cause}` beside a **nil** error: the redrive did not
  fail, the letter did.
- **One unit per letter**, and inside it the handler's write and the eviction. A
  crash between them leaves the letter parked and nothing applied, and the next
  redrive starts that sequence again from there. At least once per letter, never
  at most once — the same promise the loop makes, and the same reason
  `(Stream, Version)` is the idempotency key.
- **It touches no checkpoint.** The fence admits one writer and the loop owns it,
  so the queue is the record of the redrive and the checkpoint stays the record of
  the scan. A successful redrive raises nobody's `Applied`.
- **`Any` rotates by least recently tried.** A loop that always picked the same
  failing sequence would starve every other one; one that picked at random would
  lose the fairness. The order lives in your table, where its clock does.
- **A sequence is claimed, not merely read.** `Claim(ctx, of, "")` takes the least
  recently tried unclaimed sequence and `Claim(ctx, of, "A")` takes that one; both
  answer `found = false` rather than an error when there is nothing to take. Two
  operators — or one operator and the `runtime.Runner` you wrapped `Any` in — never
  process one sequence.
- **A grant expires, so it travels on every write it authorises.** Refuse a
  `Sequence`, `Evict`, `Touch` or `Release` whose claim you no longer hold with an
  error wrapping `ErrClaimLost`. `Evict` runs inside the same unit as the apply, so
  a refusal there rolls the apply back with it — which is why the token has to be
  on the write and not only on the read. On `ErrClaimLost` the redrive stops that
  sequence at once, issues no `Touch` and no `Release` (it owns neither), and
  answers what it applied before the loss beside an error wrapping the sentinel.
  **The suppressed `Release` is this framework's and not yours**: nothing here
  requires `Release` to compare the token, so an idempotent `DELETE … WHERE
  sequence = $1` is a fair spelling of it, and the call is the only thing between
  a loser and freeing the winner's sequence mid-drain.
  **The claim duration you choose must exceed the longest unit a redrive may
  take**: a duration shorter than one letter's apply turns every redrive into a
  sequence of lost claims and drains nothing.
- **`Release` runs on every exit path, including a panic.** A redrive runs on your
  goroutine, so a handler panic reaches you rather than being recovered into a
  letter's failure.
- **A redrive across a sequencer change is refused** with `ErrTopology` naming
  both: the ordering a letter's place in its sequence encodes is not the ordering
  another key would give it. `Sequencer.Name()` is what a letter records and what
  this compares — a contract, checked here and nowhere else, because making it
  visible would need a column in the kernel's frozen `Checkpoint` or in a table
  this framework does not read.

- **There is no `Classifier` on a `RedriveSpec` and no per-failure enqueue
  policy.** A letter that fails again is requeued with its new cause whichever
  class it is in, because the alternative — giving up on it — removes a letter
  without applying it, and that is an operator's act through `Evict` and never a
  policy's. The mechanism's four-way decision maps onto this: *enqueue* is
  `ParkSequence`; *requeue with diagnostics* is what a repeat failure already
  does; *evict* is your operator's; and *do not enqueue* would advance the scan
  over an event no handler applied, which is the one answer this framework does
  not have. Its worked retry-counter policy is `Letter.Attempt` against your own
  table, which is where the queue lives.

**An eviction without an apply is an operator saying "this will never be
applied".** It marks two things and both are durable: your own table answers *what*
was skipped, and `Park.Holes` still counts it, because an eviction without an
apply **is** a hole. Derive `Holes` as two counts over the rows rather than as a
column, so it cannot drift from what it summarises: its wrong answer is invisible
until a cutover admits a holed generation.

## Following is a statement about the last read

There is no head. A store that will not promise monotone visibility has no number
that is the end of the log, so `PhaseFollowing` means *the last read delivered
nothing* and never *we are caught up*. Draining and following are one loop with
one branch, and an idle projection issues **no writes at all** — the empty page's
cursor stays in the reader and is never saved.

**`Spec.Wake` is a hint and never the delivery mechanism.** `Idle` polls whether
or not anything sends, so a producer that stops sending costs latency and nothing
else. **Closing the channel says there will be no more hints**: the loop stops
waiting on it and follows on `Idle` alone, because a closed channel is
permanently ready and receiving from one is an unbounded read of the log.

**`Spec.Unit` runs the work it is given once.** A unit that retries its own
transaction — the ordinary `40001` loop [[D-126]] tells a caller to own — answers
the failure instead: one pass presents one advance, so a second run inside one
pass is refused before it writes anything, and the page is re-delivered after a
backoff rather than saved over twice. The refusal is not a halt.

**A new projection reads the whole log.** Adding one to a live deployment replays
every event ever written through its handler, a page at a time, from the origin —
`Checkpoint.Fresh()` is what says the row is absent, and absence is the origin.
That is the feature, and it is also a load nobody schedules by accident.

## A halt is terminal

A halted projection keeps running and does exactly nothing: no read, no save, no
handler call, no ticker, ever again. It publishes the transition once, records the
failure in `State`, keeps answering `State()` and `Ready(ctx)`, and returns from
`Drain` at once. `Run` answers `ctx.Err()` and never `ErrHalted`, because a
returning runner is a failure the supervisor reports and one projection that
cannot apply one page is not a reason to stop serving.

There is no `Resume`, `Retry` or `Clear`. The exit is a new value in a new process
after the operator has fixed what halted it: a halt that could clear itself is a
retry loop with a longer period, and the classifier already said the failure was
permanent.

## Nothing here writes a line

No log line, no span, no metric ([[D-132]]). What an operator needs travels
through three values: `State` on demand, the `Observer` on every transition, and
`Ready` for a probe. A blocking observer blocks the loop; a panicking one does
not take it down.

```go
Observer: projection.ObserverFunc(func(state projection.State) {
	slog.Info("projection", "name", state.Projection, "phase", state.Phase,
		"applied", state.Progress.Applied, "err", state.Err)
}),
```

The cost is stated rather than hidden: a deployment that supplies no `Observer`
learns a handler panicked only from `Ready`, and learns nothing of a retry streak
below `Tolerate`.

## The defaults, each the one that promises less

| Field | Default |
|---|---|
| `Advance` | `AfterApply` |
| `Idle` | 1 s, and the wait is **after** the pass, so a slow pass cannot overlap itself |
| `Backoff` | `{250ms, 30s}`, doubling, **without jitter** — a projection is a singleton per name, so there is no herd |
| `Attempts` | 10, bounding handler failures only. A store failure costs no attempt |
| `Tolerate` | 1 min of accumulated backoff before `Ready` reports |
| `Classifier` | `Classify` — the history class and `ErrUnrouted` are permanent, everything else is retryable |
| `Ticks` | `runtime.SystemTicks`, injectable so a test drives the schedule |
| `OnPermanentFailure` | `Halt` |
| `Park` | none. `ParkSequence` without one is refused |
| `Destination` | none under `InUnit`. "I cannot check this" has to be written |
| `Sequence` | `ByStream()`, the kernel's own composition of the family and the key |
| `Partition` | `Whole()`, so a spec that names no partition is one runner over everything |
| `Generation` | `Ungenerated`, which renders nothing at all, so no existing name or row moves |
| `Pace` | 0 — as fast as the store answers, which is every projection that exists today |
| `Effects` · `EffectsAfter` · `Generations` | none. A rebuild is a spec with the first of them nil |

## What is deliberately absent

| Not exported | Why |
|---|---|
| a snapshot | full replay is the only authority; the trigger for reopening that is measured and recorded ([[D-132]]) |
| `Projection.Resume` / `Retry` / `Clear` | a halt is terminal for that value's life |
| `Spec.Logger` | a package that publishes a typed `State` does not publish an untyped duplicate of it |
| a `Position` on the checkpoint | [[D-129]] |
| a head, a lag number or a `Tail()` | there is no head to compute one against |
| a retry, backoff or circuit breaker around your `Unit` | [[D-040]]; the unit is yours, and it runs once |
| an `event.Store` accepted as `Spec.Log` | a projector that can append is how a replay writes |
| a per-failure enqueue policy, and a `Classifier` on `RedriveSpec` | the framework's two answers are park and halt; it removes nothing, so a decision that could drop a letter is an operator's |
| a `Merge` of two partitions | it would have to order two cursors, and a cursor is a store's own bytes rather than a point on a line ([[D-129]], [[D-140]]). The route to a coarser topology is a new generation |
| a barrier field on `CutoverSpec` | a barrier a caller can invent is not evidence, and the zero value of one admits a generation that has delivered nothing |
| an `EffectsAfter` on `RedriveSpec` | it is the one wiring whose only outcome is a lost effect: a barrier over a queue suppresses one that is owed and nothing will ever offer it again |
| a partition count anywhere | there is no count; there is a set of rows and the mask each runner was built with ([[D-140]]) |
| a `Log.ReadAll` filter, or a reader shared between runners | N partitions x M generations are N x M independent walks, and the cost is paid and recorded rather than traded for a contract the log does not have |
| `AllowStale`, `Stale` or `OnTimeout` on a `WaitSpec` | a wait that did not reach returns a filled-in `Visibility` beside its refusal, so serving stale data is a branch a reviewer can see |
| a shared poller, a cached progress or any pre-warming behind `Wait` | anything continuous is a `runtime.Runner` the host has to supervise ([[D-092]]), and this costs nothing when nobody is waiting |
| a settable field on a `Mark` | a target a caller can write is evidence it invented; both doors take a number a store produced |
| `Park.Holes` asked by a wait | a hole is a cutover's question. A wait asks `Sequences` and `Holds` and nothing else of a `Park` |

## See also

- [event](event.md) — the vocabulary, the checkpoint seam and the refusals
- [eventmemory](eventmemory.md) · [eventpg](eventpg.md) — the two checkpoint
  stores that ship
- [eventtest](eventtest.md) — `RunCheckpoints`, the fourteen sections a third one
  is proved by, two of which a split rests on
- [`_examples/event-partitions`](../../../_examples/event-partitions/) — four
  runners from one `Cover`, and the split that takes one of them to two
- [`_examples/event-generations`](../../../_examples/event-generations/) — the
  barrier, the cutover, the rollback and the effect gate
- [`_examples/event-wait`](../../../_examples/event-wait/) — an append, a mark, a
  wait and the read, with the parked branch written out
- [receipt](receipt.md) — the other half of the same request path: what happened
  to the command whose outcome you never heard
- [event-operations.md](../../usage-guides/event-operations.md) — the runbook:
  the rebuild recipe, reading and redriving the queue, and what the numbers above
  cost in a deployment
- [[D-091]] · [[D-092]] · [[D-118]] · [[D-126]] · [[D-128]] · [[D-129]] ·
  [[D-130]] · [[D-131]] · [[D-132]] · [[D-133]] · [[D-140]] · [[D-141]] ·
  [[D-144]] · [[FL-038]] · [[FL-042]] · [[FL-043]] · [[UC-032]] · [[UC-036]]
