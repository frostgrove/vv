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
supervisor.Add(following)
```

`Spec.Log` is an `event.Log` and a value that is also an `event.Store` is
**refused**: a projector that can append is how a replay writes. `event.ReadOnly`
is the wrapper that takes the surface away.

## The seven rows this page is for

Everything else here is prose. These seven are the contract.

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
advances its checkpoint and calls no handler, so `Progress.Highest` is the read
page's last position rather than a claim about what was applied. A panic out of a
sequencer **halts**: an envelope with no sequence belongs to no partition.

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

## See also

- [event](event.md) — the vocabulary, the checkpoint seam and the refusals
- [eventmemory](eventmemory.md) · [eventpg](eventpg.md) — the two checkpoint
  stores that ship
- [eventtest](eventtest.md) — `RunCheckpoints`, the twelve sections a third one
  is proved by
- [[D-091]] · [[D-092]] · [[D-118]] · [[D-126]] · [[D-128]] · [[D-129]] ·
  [[D-130]] · [[D-131]] · [[D-132]] · [[D-133]] · [[FL-038]] · [[UC-032]]
