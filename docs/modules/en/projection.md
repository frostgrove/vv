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
| `Failure` · `Quarantines` · `Quarantined` | halt or quarantine, and where a passed envelope is recorded |
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

## The six rows this page is for

Everything else here is prose. These six are the contract.

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
context the unit gave it*. A handler that writes anywhere else — a second
database, a document store, a search index — is outside that transaction, no
check in this framework can see it, and what you have is `AfterApply` semantics
under an `InUnit` spec. `Destination: projection.Unchecked` is how a composition
says that out loud; a field left zero is refused rather than read as it.
[`_examples/event-checkpoints-elsewhere`](https://github.com/frostgrove/vv/tree/main/_examples/event-checkpoints-elsewhere)
is that wiring, compiled.

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
| `Permanent` | `Apply` | `Halt`, or the isolation pass when a `Quarantine` sink is supplied |
| anything else | read or save | **halt**, naming the door and the sentinel |

The last row is what makes the table total. A pass that cannot classify its own
failure must not fall through to the retryable arm, which is how a structurally
impossible write becomes an infinite loop.

**Quarantine is envelope-granular and costs a re-delivery.** When a page fails
permanently and `OnPermanentFailure` is `Quarantine`, the page is delivered again
one envelope to a `Batch`, in position order: one that applies is applied, one
whose failure is permanent goes to the sink with its cause and is passed, and a
**retryable** failure ends the pass and returns the whole page to retrying under
the same attempt budget — a database that went away is not a corrupt payload.
Under `InUnit` the whole isolation pass is one unit, the sink included: a sink
called outside it is the one write that survives the rollback of the advance.

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

## See also

- [event](event.md) — the vocabulary, the checkpoint seam and the refusals
- [eventmemory](eventmemory.md) · [eventpg](eventpg.md) — the two checkpoint
  stores that ship
- [eventtest](eventtest.md) — `RunCheckpoints`, the twelve sections a third one
  is proved by
- [[D-091]] · [[D-092]] · [[D-118]] · [[D-126]] · [[D-128]] · [[D-129]] ·
  [[D-130]] · [[D-131]] · [[D-132]] · [[D-133]] · [[FL-038]] · [[UC-032]]
