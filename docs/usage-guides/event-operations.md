# Run it: restore, rollback, capacity and the four incidents

[event-sourcing.md](event-sourcing.md) is the adoption guide — what you write and
in what order. This is the other half: what you do at three in the morning, and
what you set up beforehand so that three in the morning is shorter.

Every procedure here is the operator's. Nothing under `event/` starts a
goroutine, retries in the background, prunes a table or migrates a schema it was
not told to migrate, so every one of these is a command somebody runs.

---

## Part 0 — three settings, before anything else

Set these on the application role before the first projection starts. Two of them
are the only mitigation for the stall in Part 2, and neither is this library's to
apply.

| Setting | Why |
|---|---|
| `idle_in_transaction_session_timeout` | A session that opened a transaction and stopped holds the log walk's floor down for every projection over the schema. Without this, one leaked connection is an unbounded stall. |
| `statement_timeout` | The other shape of the same problem: a transaction that is not idle and is running one very long statement. |
| a lag alert on `Progress.At` **and** `Progress.Highest` | A `Highest` that stops moving while the log grows is either the stall or a halted projection, and at a glance they are the same picture. Alert on the pair. |

Two more that are not settings but are decided once:

- **`Spec.Generations` on the live projection, one release before the first
  cutover.** A rebuild is measured against a barrier derived from the retiring
  generation's own rows; a live projection with no `Generations` cannot be
  retired by `Cutover`.
- **A claim duration longer than the longest unit a redrive may take.** A shorter
  one turns every redrive into a sequence of lost claims and drains nothing.

---

## Part 1 — restore and replay

There is one restore procedure and it is also the rebuild procedure: a second
generation reads the whole log into a second destination beside the live one, and
one fenced write switches reads over.

### The recipe

1. **Start the arriving generation.** Same `Spec.Name`, `Generation: 2`, its own
   destination, its own park, and **`Effects` nil**. A rebuild that stages effects
   sends the mail for every historical event. The two generations share one log
   and have nothing between them — there is no API by which the arriving one could
   read, pause or reset the live one's checkpoint.
2. **Let it drain.** It replays the whole log through its handler, paged and
   resumable, competing with live traffic for the same pool. `Observe` answers the
   lowest `Highest` across its cover; `Reached` answers `Readiness{Reached,
   Behind, Quarantined, Holes}`.
3. **Read `Reached`, and read what it means.** `Reached` means **delivered**, not
   "the two destinations agree". `Behind` is an upper bound and a hint: the log
   burns a position for a rolled-back append and for an optimistic-concurrency
   loser, so the distance between two positions is not a count of undelivered
   events. If you want "the rows agree", compare the rows.
4. **Cut over inside your own unit of work.** `Cutover` observes its own barrier
   from the **retiring** generation's rows, measures the arriving one against it,
   refuses on `Holes`, and moves the ownership row once, fenced. There is no
   barrier field on `CutoverSpec` on purpose: a barrier a caller can invent is not
   evidence.
5. **Stop the retired runner.** Nothing stops it for you, and a retired
   generation that keeps running keeps reading the log and keeps costing a walk.

### The rollback

**The same call with `From` and `To` exchanged.** That is the whole of it, and it
is why the retiring generation's rows and destination are not deleted at cutover
time: a rollback needs them to still be there and still be current. Keep the old
generation's runner going until you are sure, or accept that rolling back means
replaying the gap.

`Cutover` never refuses on `Progress.Quarantined`. That count is cumulative and
never falls, so a generation that parked a sequence and redrove it completely
would be refused for ever by a check meant to guard it. What it reads is
`Park.Holes`, which is what the queue answers *now*.

### What the old generation leaves behind, and what cleaning it up means

Four things, and each is yours:

| What | Where | Safe to delete when |
|---|---|---|
| the checkpoint rows | your checkpoint table, one per cover member, keyed by the rendered identity | after the rollback window closes — deleting them is what makes a rollback a full replay |
| the park letters | your park table, keyed by `Identity.Whole()` — `orders@1` | the generation is gone for good; a letter is the only record of an envelope no handler applied |
| the destination rows | your read model | the same |
| the ownership row | your `Generations` table | never: one row per projection, rewritten by `Cutover` |

**Delete the checkpoint rows last.** An absent row is read as the origin, so a
generation whose rows you removed and whose runner is still up starts replaying
the log from zero.

### The mixed release, in both directions

A release that rolls back meets its own writes. Both directions are rehearsed
live, and the second is the one a rollback takes:

- **v1 writes, v2 reads.** The stored bytes carry revision 1 for ever and the
  upcaster is what makes them the current shape. Nothing is rewritten on read.
  `TestStoredRowsAreUpcastAndNeverRewritten`.
- **v2 writes, v1 reads.** The old build meets a revision it does not retain and
  **refuses the whole stream**: the zero state, no version handed back, and
  therefore no append either — the stale version it last saw is a conflict. What
  it keeps is the bounded read: the prefix up to the version before the first
  revision-2 row still folds, because every row in it is a revision that build
  retains. `TestARolledBackBuildMeetingRevisionTwoRefusesRatherThanFolding`.

So a rollback across a revision bump is **safe and loud**, not safe and quiet.
Plan for the aggregates that were written at the new revision to be unreadable by
the old build until it is rolled forward again, and do not plan for the old build
to keep writing them.

---

## Part 2 — the `xmin` stall, the one that stops everything at once

This is the most expensive thing in this subsystem to learn during an incident,
so learn it now. The mechanism is on [eventpg](../modules/en/eventpg.md); this is
the operator's summary.

The PostgreSQL log walk must not hand out a position a writer could still commit,
so it waits on `pg_snapshot_xmin(pg_current_snapshot())` — **the oldest running
transaction in the whole cluster**, not in this schema. Any session anywhere that
has written something and gone idle in transaction holds that floor down: an
unrelated slow report, a leaked connection, a paused debugger.

**Nothing is lost.** Every event is still there and every one is delivered once
the floor moves. What stops is delivery, and **every projection over the schema
stops together**, because they all wait on the same number.

It has two faces on the request path and they are one session:

- a `projection.Wait` burns its whole deadline and answers `ErrNotVisible` — the
  *slow* answer, not the *stopped* one, with `Moved` false across every poll;
- a `receipt.Resolve` for an operation whose writer is that very session answers
  `Unresolved`, for exactly as long.

An operator seeing waits time out and receipts stay unresolved at the same moment
is looking at **one idle session**, not at two subsystems.

### What to do

```sql
SELECT pid, state, xact_start, now() - xact_start AS held, query
  FROM pg_stat_activity
 WHERE state = 'idle in transaction'
 ORDER BY xact_start;
```

Terminate the oldest, then set `idle_in_transaction_session_timeout` so that the
next one terminates itself. Set it **before** a rebuild rather than after: N
partitions times M generations is N times M independent walks of the log, all
waiting on that same floor, and a rebuild is precisely when a deployment is
running the most of them. The multiplication is measured, at exactly 8.0x for
eight walks — `TestEightWalksCostEightTimesOneProjectionsReads`.

---

## Part 3 — the dead-letter queue

### What a parked sequence means

A page that failed permanently is redelivered one envelope at a time, in position
order. One that applies is applied; one whose failure is permanent goes to your
park with its cause — **and so does every later envelope of the same sequence,
which never reaches the handler at all**. Every other sequence in the page carries
on.

That is the difference between a dead-letter queue and a skip list: a handler is
never handed `OrderPaid` for an order whose `OrderCreated` was parked. The cost is
that one poison event stops one sequence indefinitely, and only you restart it.

A **retryable** failure is not a park: it ends the pass and returns the whole page
to retrying under the same attempt budget. A database that went away is not a
corrupt payload — but a spent attempt budget parks a transient failure too, which
is the trade `ParkSequence` buys over `Halt`.

### Reading the queue

The queue is yours, keyed by `Identity.Whole()` — the projection and its
generation with the partition dropped, rendering `orders@2`. So one query answers
the generation, not the partition:

```sql
SELECT sequence, count(*) AS letters, min(parked_at), max(attempt), max(cause)
  FROM park
 WHERE identity = 'orders@2'
 GROUP BY sequence
 ORDER BY min(parked_at);
```

Three numbers tell you different things and are easy to confuse:

| Number | Where | Means |
|---|---|---|
| `State.Parked` | live, in the process | how many sequences are held **now** |
| `Progress.Quarantined` | durable, on the checkpoint row | how many envelopes were ever quarantined — cumulative, **never decremented**, not by a redrive and not by an eviction |
| `Park.Holes` | derived, from your rows | letters queued now **plus** letters evicted without being applied — the number a cutover refuses on |

A projection holding parked sequences is `PhaseDegraded` and **passes readiness**:
a replica reporting unhealthy for one poison order is a projector that stopped by
another route. `PhaseBlocked` — the last pass ended in `ErrParkFull` — fails past
`Tolerate`, and that one is an outage.

### Redriving

```go
retried, err := redrive.Any(ctx)              // least recently tried sequence
retried, err := redrive.Sequence(ctx, "order-7")
```

- **One sequence per call, in insert order, stopping at the first letter that
  fails again.** The letter that failed is requeued with its new cause and a
  raised attempt, and the call answers `Retried{Applied, Left, Cause}` beside a
  **nil** error: the redrive did not fail, the letter did.
- **One unit per letter**, and inside it the handler's write and the eviction. A
  crash between them leaves the letter parked and nothing applied, and the next
  redrive starts that sequence again from there.
- **It touches no checkpoint.** A successful redrive raises nobody's `Applied`.
- **A sequence is claimed, not merely read**, so two operators — or one operator
  and a scheduled runner — never process one sequence. On `ErrClaimLost` the
  redrive stops that sequence at once and answers what it applied before the loss.
- **A redrive across a sequencer change is refused** with `ErrTopology` naming
  both. If you changed the sequencer, drain the queue under the old one first.

Nothing redrives on a schedule unless you wrap `Redrive.Any` in a
`runtime.Runner` with a large interval. That is the supported way to have one, and
a host that does it clears a transient-failure page without an operator once the
database is back.

### Skipping, and what a skip marks

An eviction without an apply is an operator saying *"this will never be
applied"*. It marks two things and both are durable: your own table answers
**what** was skipped, and `Park.Holes` still counts it, because an eviction
without an apply **is** a hole. A held generation with holes is refused by
`Cutover` — which is the point. A skipped envelope is a permanent statement that
this destination is not a function of the log alone, and the next rebuild is what
repairs it.

### Overflow

Bound the queue in two dimensions and ask **per sequence**: a new sequence is
refused when the queue is at its own limit; an existing one when it holds its own
limit of letters. Answer `ErrParkFull` and the pass blocks rather than skipping —
`PhaseBlocked`, the unit rolls back so nothing at all is parked, no advance, no
envelope skipped, no attempt consumed.

What it costs is worth knowing before it happens: at the limit the queue refuses
the events *behind* a blocker it is already holding, so the whole partition sits
in `PhaseBlocked` while the blocked sequence's own room goes unused, and only a
`DELETE` clears it. There is no `isFull` on the `Park` interface, so a
one-dimensional bound is a mistake nothing in this framework catches.

---

## Part 4 — capacity

### The log walk

One walk per runner, and a runner is one partition of one generation. **N
partitions times M generations is N times M walks of the whole log**, and the
read traffic multiplies with it — measured at exactly 8.0x for eight walks. Size
the pool for the runners you start.

Partition when the **handler** is the bottleneck, not when the read is. The useful
range is 8–16. Only the first start chooses the topology; the route from a
projection that has already run is a `Split`.

**A new projection reads the whole log.** Adding one to a live deployment replays
every event ever written through its handler. It is paged and resumable, so it is
bounded work rather than one enormous transaction — but it is the log's whole
length of it.

### A wait

Per poll, per waiting caller, at `Every` (50 ms by default, so twenty polls a
second):

| Read | How many | When |
|---|---|---|
| `Park.Sequences` | 1 | every poll, while a `Park` is supplied |
| `Park.Holds` | 0, or up to one per sequence key the mark carries | only while that count is **not** zero |
| checkpoint `Load` | 1 per cover member | every poll |
| checkpoint `Load` again | 1 per cover member that holds **no row yet** | the census asks an absent row whether a split handed it down |
| `Generations.Active` | 2 over the **whole wait**, not per poll | the first poll, and the poll that would otherwise answer `Reached` |

So `1 + |cover|` a poll healthy and `1 + 2 x |cover|` while the generation's
members hold no rows:

| Situation, four-member cover | Reads a poll | `SELECT`s a second, per waiter |
|---|---|---|
| healthy, nothing parked | 5 | **100** |
| that generation's park queue not empty | 6 | **120** |
| rebuilding, no rows recorded yet | 9 | **180** |
| rebuilding with a non-empty queue | 10 | **200** |

Fifty concurrent waiters on a healthy four-member cover is **five thousand a
second**. **The lever is `Every`**: a request path that can accept 200 ms writes
200 ms. A wait starts nothing, writes nothing and costs exactly nothing when
nobody is waiting.

### Replay

`BenchmarkStreamReplay` is the instrument, and it runs against your own database
rather than against a number on a page. The reason the number matters: there is no
snapshot in this framework, and the recorded trigger for reconsidering that is a
deployment measuring its own p99 aggregate replay above ~50 ms — somewhere above
45 000 events at the rates this repository measures.

---

## Part 5 — four incidents

### A projection will not advance

Read `Progress.At` and `Progress.Highest` first, then work down:

1. **Is `Highest` moving at all, for *every* projection over the schema?** If none
   of them is moving, it is Part 2. Look for an idle-in-transaction session before
   anything else.
2. **Is the phase `PhaseBlocked`?** The queue is full. Part 3, Overflow.
3. **Is it halted?** A halt is terminal: the runner stops and the supervisor does
   not restart it, because whatever produced it is still there. An unclaimed event
   type of a routed family halts by design — a family you registered and a type you
   did not is a deployment that is missing a handler, and advancing over it would
   be silent data loss.
4. **Are two instances of one name running?** One name is one writer, and the
   fence is what holds when it is not. The loser is overtaken and adopts, rather
   than both writing.
5. **Is the handler simply slow?** Then `At` moves and `Highest` does not lag it
   by much, and the answer is partitions or a faster handler, not this list.

### A receipt stays `Unresolved`

`Unresolved` is **not** a rollback and this framework will never turn it into
one: a resolver on a second connection cannot tell a transaction that rolled back
from one that is still open. Report it to the caller as *in flight* and do
**not** re-issue the command.

1. **Is the writing transaction still open?** That is the whole answer most of
   the time, and it is Part 2's session again.
2. **Has the horizon passed?** The window is bounded by the ledger's own database
   clock through `Horizon`. Past it, the absence means the retention window
   expired, not that the write is pending.
3. **Is the key's content stable?** A codec that is not byte-stable — a clock, a
   fresh id, a map in iteration order in a payload — makes every retry a
   collision rather than a repeat. That shows up as `ErrCollision`, not as
   `Unresolved`, but it is the same class of mistake and is found here.

### A schema fails verification

The store refuses **before** any door: an unprepared or mismatched schema answers
`ErrNotReady` or `ErrSchemaMismatch` and issues no append. That is the design, not
the incident.

1. **Did a deployment skip its migration?** `MigrationStatements` is the
   operator's own list and nothing runs it for you. No start-up migrates a schema
   it was not told to migrate.
2. **Do the configured bounds match the deployed ones?** `MaxPayload` and `MaxKey`
   are `CHECK` constraint operands, so the numbers the store publishes are the
   numbers the deployed schema enforces. Two builds configured differently against
   one schema is a mismatch and is reported as one.
3. **Is it the schema version?** A build expecting a newer schema than is deployed
   refuses rather than writing rows the deployed constraints do not cover.

Do not "fix" it by switching the store to manage its own schema. That is a
deployment profile, and choosing it under incident pressure migrates a production
schema on a process start.

### A conflict storm

Every append names the version its decision was made at, so a stream under
contention produces conflicts by design: one writer is admitted and the rest are
told the stream moved.

1. **A conflict is not retryable by the framework and never will be.** A stale
   proposed event list is not re-decidable; the caller reloads and decides again.
   If you built a retry loop, it must go back through `Load → Decide → Append`.
2. **A storm means the aggregate boundary is wrong.** Many writers at one stream
   is a consistency boundary drawn too wide. That is a design change, not a tuning
   knob.
3. **Do not confuse it with `Unconfirmed`.** A conflict is a certainty — the write
   did not land. An `Unconfirmed` is a connection lost around commit and the
   outcome is unknown; no layer guesses it, and the answer to it is a receipt, not
   a retry.

---

## Where to go next

| You want | Read |
|---|---|
| how to adopt any of this | [event-sourcing.md](event-sourcing.md) |
| the walk, the schema, the stall, the measured replay | [eventpg](../modules/en/eventpg.md) |
| the park, the redrive, generations, the wait | [projection](../modules/en/projection.md) |
| what `Unresolved` is and is not | [receipt](../modules/en/receipt.md) |
| why any of it is shaped this way | [[D-126]] · [[D-128]] · [[D-130]] · [[D-140]] · [[D-141]] · [[D-143]] · [[D-144]] · [[D-145]] |
| what a consumer is promised | [[UC-036]] · [[UC-037]] · [[UC-038]] |
