# EVENTSOURCE PHASE 4 — THE PROJECTOR SUBSYSTEM: ES-01, ES-02, ES-03, ES-04, ES-06

**Status:** specification, phase 4 of the PostgreSQL event-sourcing roadmap.
**Written against:** `event`, `event/eventmemory`, `event/eventtest`,
`event/eventpg` and `event/projection` as they stand after phase 3; `runtime`,
`crud` and `jobs` as the accepted precedents; PostgreSQL 17.9.
**Numbering:** continues phase 3. Use cases start at UC-131, invariants at
INV-083, so a bare `UC-nnn` or `INV-nnn` is unambiguous across all four
documents.
**Sources:** the five appendices at
[`docs/roadmaps/2026-09-01-postgres-event-sourcing-roadmap.md`](../../../docs/roadmaps/2026-09-01-postgres-event-sourcing-roadmap.md)
lines 774–832, read in the original; the mechanisms at the other end of every
URL in them; [`EVENTSOURCE_P4_STUDY.md`](EVENTSOURCE_P4_STUDY.md), which is the
record of that reading; and
[`EVENTSOURCE_REFERENCE.md`](../gaps/EVENTSOURCE_REFERENCE.md), whose Reject
entries are binding and are not re-proposed here.

## 0. What this document is, and what it does not repeat

Phases 1–3 are frozen and are the input to this one. **Nothing already written
in them is restated here.** Where a rule exists it is referenced by number and
the reference is the whole of what this document says about it.

Phase 4 implements **ES-01, ES-02, ES-03, ES-04 and ES-06**. ES-05, ES-07, ES-08
and ES-09 are phase 5 and appear here only where phase 4 must not foreclose them
(§8).

**The appendices' own framing is binding on this phase**, restated once because
every section below is an application of it:

> Владельцы — опциональная event-подсистема и выбранный store, не framework
> kernel. Приложение передаёт обработчики, SQL transaction authority и
> read-model repository своего ORM; никакого обязательного ORM, DI, broker или
> межмодульного registry. Новые гарантии требуют отдельного контракта и тестов:
> нынешний [[UC-032]] ими не расширяется. DX ниже — эскизы, не существующие API.

So: every table this phase names is the **application's**, behind an interface
this framework declares and does not implement — the shape `Quarantines`
(`event/projection/classify.go`) already has. Every continuous thing is a
`runtime.Runner` the host supervises ([[D-092]]). Nothing here starts a
goroutine, opens a transaction or writes a log line.

**Delivery policy in force.** Only `[critical]` and `[high]` findings block.
`[medium]` and `[low]` are appended to
[`EVENTSOURCE_BACKLOG.md`](../gaps/EVENTSOURCE_BACKLOG.md) under `## P4` and left
alone. A gate never proceeds silently red.

**Phase 4 moves no kernel signature and one kernel obligation.** `Progress`
gains nothing and `Checkpoints` gains nothing; the only kernel-adjacent change in
code is that `event/projection` starts using three already-exported `event`
symbols (`Backing`, `NewAuthority`, `Authority.Same`) for a purpose they were not
written for, and every checkpoint call this phase adds goes through the
`event.Track` door rather than around it ([[D-129]]). But `eventtest`'s
`Checkpoints` conformance suite **is** widened (§5.3), which the signature
baseline cannot see — so it is announced in §5.1, §5.3 and §7.1 rather than
described as "nothing changes".

---

# 1. Six questions, answered before anything is specified

Each of these is a place where an appendix's *адаптация* paragraph makes a
demand that the shipped tree cannot meet by accident. They are answered here,
once, and the use cases and invariants below are the consequences.

---

## 1.1 Where the effect/checkpoint atomicity boundary sits, and what a foreign destination gets

**The question.** ES-01 part 3: *«выбранный SQL-профиль фиксирует effect +
checkpoint **одной** transaction authority»* — one. And its last sentence:
*«Чужая БД/HTTP требуют своей идемпотентности»*. Phase 3 shipped `InUnit` and
`checkUnit` (`event/projection/pass.go:551-570`), and the study's §0 found the
one clause that is asserted rather than held: `checkUnit` proves **two**
transactions exist and never that they are **the same one**.

### The boundary

**The boundary is the transaction `Spec.Unit` opened, and phase 4 makes "one
authority" a measured fact wherever `crud` can resolve it.** Three tiers, and
the whole of this phase's ES-01 work is that the middle one stops passing
silently.

| Tier | The wiring | What phase 4 does |
|---|---|---|
| **A — aligned** | the checkpoint store and `Spec.Destination` resolve, inside the unit, to **the same transaction value** | proved per pass, before the handler runs. This is `InUnit`'s promise and it is now measured rather than asserted |
| **B — divergent** | both resolve to a transaction, and they are **two different transactions** | **refused**, per pass, inside the unit, before the handler runs. `ErrSpec`, a halt, and it never reaches the classifier ([[D-130]]) |
| **C — unresolvable** | `Destination: projection.Unchecked` | accepted, unchanged. The alignment obligation stands and no check can see it |

Tier B passes every check in the tree today and gets `AfterApply` atomicity
under an `InUnit` spec, silently. That is ES-01's outstanding clause and it is
closed by comparison rather than by prose.

### How the comparison is made, and why it needs no new surface

`event.Authority` already carries a `Backing` and a transaction identity, and
`Authority.Same` already compares both through `crud.SameDataSource`. On
`eventpg` that identity is the `*sql.Tx` (`event/eventpg/executor.go:19-25`,
`boundTx`). And `crud.KeyOf` answers an executor's data-source identity, which
for a `crudsql.Executor` bound to a transaction is that same `*sql.Tx`
(`crud/adapter/crudsql/crudsql.go:58`). So the comparison is three shipped calls:

```go
authority, err := this.tracker.Transaction(inner)          // already there
executor, bound := crud.ExecutorFor(inner, this.spec.Destination)  // already there
mine, err := event.NewAuthority(this.tracker.Backing(), crud.KeyOf(executor))
aligned := err == nil && authority.Same(mine)
```

Three properties of that spelling are load-bearing:

- **`event/projection` may not import `crud/adapter/crudsql`.** Its dependency
  budget is `./runtime` on top of the vocabulary's `./crud` and `./errs`
  (`scripts/event_test.go:45-49`). `crud.KeyOf` is in `crud`, so the comparison
  fits the budget and `crudsql.Transaction` does not. The budget row does not
  move.
- **`event.NewAuthority` refuses a nil or non-comparable identity**
  (`event/authority.go:23-34`), so "not comparable" arrives as an error rather
  than as a silent `false`. It is refused for the same reason tier B is: an
  alignment that cannot be proven is not one this framework asserts, and
  `Unchecked` is how a composition says so out loud.
- **A checkpoint store whose authority identity is not the driver transaction is
  refused beside a resolvable destination, and that is correct.**
  `eventmemory`'s identity is a `txIdentity{log, nth}`
  (`event/eventmemory/transaction.go:39-43`), which no destination's key will
  ever equal — and an in-memory checkpoint store beside a SQL read model **is**
  two transactions. Every shipped in-memory case already passes `Unchecked`
  (`event/projection/unit_test.go`, `loop_test.go`), so nothing green goes red;
  the live `eventpg` case names the store's own `crud.Source`
  (`event/eventpg/projection_integration_test.go:258`) and becomes tier A.

### What a foreign destination gets, exactly

When the read model is **not** in the checkpoint store's database — a second
database, a document store, a search index, HTTP — the framework promises four
things and refuses to promise a fifth. This is the contract, and §4's INV-084 is
how it is falsified.

**Promised.**

1. Every committed event is delivered to the handler **at least once**.
2. Delivery is in position order, and one stream's events arrive in the order
   that stream holds them ([[D-128]], a law and not a store capability).
3. The resume point never skips a committed event: once a checkpoint carrying
   `Highest = P` is saved, no event at or below `P` is delivered to this
   projection **for the first time** again, and every event at or below `P` was
   delivered at least once.
4. Each envelope carries `(Stream, Version)`, which is unique and stable for
   every event ever written — across processes, restarts, store values,
   partitions and generations. That is the idempotency key, and handing it over
   is the whole of what the framework can contribute to a resource it cannot
   join.

**Refused.**

5. That the effect happened, that it happened once, or that a checkpoint which
   advanced means it did. **Whenever the destination is outside the unit, the
   checkpoint records scanning and not application.** There is no second commit
   and none is invented: [[D-118]] says a durable write inside the caller's bound
   transaction *is* this framework's outbox, and a foreign resource is by
   definition not in it.

**And HTTP is not a projection at all — nor is it an effect.** A handler that
dials out is not a handler that failed to be atomic. It is the *drain of a
stage*, and it belongs on the other side of a durable write: `Spec.Effects`
**stages** the intent inside the unit ([[D-118]]), and the thing that dials out
is whatever drains that stage afterwards — `jobs`, on its own goroutine, with its
own retry and its own idempotency. §1.6 is where the staging half is contracted.

This is ES-02 part 3's closing sentence — *«Внешний HTTP такой fence не
защищает»* — answered rather than repeated. The fence is ordinal and it protects
exactly one thing: **a write that rolls back with it.** Everything the pass does
inside the unit is protected, because a lost fence rolls the whole unit back. An
HTTP call made inside that unit is not a write, does not roll back, and is
therefore sent again on the next pass — and a lost fence is not exotic: [[D-133]]
makes it the expected outcome of every rolling deploy, and `ErrParkFull`, a later
envelope's failure and a caller's `Unit` retry each produce the same rollback
with the same call already sent. So the framework does not merely warn about a
dial-out inside the unit; the `Effects` contract refuses it (§1.6), and the
module page carries the sentence beside Reject 2's `AfterApply` asymmetry.

ES-01's *«Чужая БД/HTTP требуют своей идемпотентности»* splits accordingly: a
foreign **database** is tier C above, and foreign **HTTP** is the stage drain's,
which is why the study recorded it under ES-06.

### What is deliberately not done

- No `Destination` that names two resources, no multi-resource unit, no 2PC.
  One authority means one.
- No downgrade. Tier B is a refusal, never a silent fall back to `AfterApply`
  ([[D-130]] forbids the downgrade by name).
- No `MonotoneVisibility` work. `eventpg` still declares it `Unsupported` and
  nothing in ES-01 needs it (study §0 item 3).

---

## 1.2 What a sequence is, who names it, and what happens when the key is wrong

**The question.** ES-02 part 3: *«application задаёт ключ последовательности по
конфликтующей read model; aggregate ID подходит не для любой multi-stream
проекции»*.

### What it is

**A sequence is the set of events that must be applied in the order the log
holds them.** Axon states the rule as an equality and vv takes it unchanged:

> "If the return value of the `SequencingPolicy` function is equal for two
> distinct event messages, it means that those messages must be processed
> sequentially."

Two events with the same key are ordered with respect to each other. Two events
with different keys are not, and may be applied by two different runners at two
different times.

### Who names it

**The application, per projection, through `Spec.Sequence`.** The seam is one
method plus a name:

```go
type Sequencer interface {
	Name() string
	SequenceOf(envelope event.Envelope) string
}
```

and four shipped spellings, because the appendix's whole point is that the
aggregate id is a **default** and not the only possibility — Axon ships five
policies for exactly that reason:

| Constructor | Axon's name for it | What it is |
|---|---|---|
| `ByStream()` | `SequentialPerAggregatePolicy` | `envelope.Stream.String()`. The default when `Spec.Sequence` is nil |
| `Unordered()` | `FullConcurrencyPolicy` | every envelope its own sequence. The honest name for a projection with no ordering requirement, and a framework that only offers "by aggregate id" pushes applications into declaring an ordering they do not need |
| `OneSequence()` | `SequentialPolicy` | one key for the whole log. Useful with `Whole()` and with a park, where one poison event should stop everything |
| `SequenceBy(name, f)` | the custom policy | the application's own function of the envelope |

`Unordered()` still answers a *stable* key — the envelope's position rendered as
text — rather than a random one, because a re-delivery after a restart must land
in the same partition. Axon can hand a null-sequence event to any segment
because one coordinator owns them all; vv's partitions are separate processes
and a key that moved would be delivered twice or not at all.

### Three obligations the type cannot carry, stated as contract

1. **Total.** `SequenceOf` returns a string for every envelope and never fails.
   A sequencer that must decode the payload may (`event.Fact.Read` is there),
   but a decode failure inside it is **not** a handler failure: it happens
   before the envelope has been attributed to a sequence, so it cannot be parked
   in the sequence it belongs to. There is nowhere for it to go, which is why
   the signature has no error and why a panic out of a sequencer **halts** —
   `applyPage`'s panic recovery does not extend here, because assigning the
   wrong partition is corrupting rather than a page that failed.
2. **Pure.** A function of the envelope and of nothing else: no clock, no map
   iteration order, no pointer address, no process-local state.
3. **Stable for the life of the log.** The key an envelope produced in 2026 is
   the key it must produce in 2030, in every build, in every process.

### What the framework does when the key is wrong

**It cannot detect it, it says so, and it makes the only real remedy the only
supported one.**

Ordering requirements are semantic. The framework has no model of which two
events must be ordered, so a `SequenceBy` that puts `OrderCreated` and
`OrderPaid` in different partitions is invisible to it, and claiming otherwise
would be a lie. What phase 4 does instead is three things:

- **The remedy is a rebuild, and it is in this phase.** A sequencer is part of a
  generation's definition, exactly as its handler and its router are. Changing it
  under a running projection is undefined and the contract says so in one
  sentence; the supported change is a new generation (§1.5), which replays the
  whole log through the new key into its own tables and cuts over. ES-02's
  correctness problem therefore has an answer inside phase 4 rather than a
  caveat.
- **The one place a change *is* visible is checked.** `Sequencer.Name()` is
  recorded on every parked letter (§1.4), and a redrive whose spec names a
  different sequencer than the letter carries is refused (`ErrTopology`). That
  catches the case where an operator redrives a queue built under the old key.
- **The framework refuses to fake an aggregate answer.** With N partitions,
  `Progress.Highest` stays true **per checkpoint row** and becomes **false across
  rows**: partition 0 at position 900 and partition 1 at 1200 does not mean
  everything at or below 900 was delivered by the projection as a whole. Any
  aggregate "how far along is `orders`" is a **`min` over the partition set**,
  never a `max` and never a sum, and §1.5's `Observe` is the one function that
  computes it.

### What is deliberately not done

- No sequencer registry, no discovery, no per-fact `SequenceBy` annotation. It
  is a field on the spec.
- No run-time stability check. Calling the sequencer twice per envelope to
  compare would cost every healthy projection a doubled hot path to catch a
  defect that a rebuild fixes and a review sees. It is a contract, and §8
  records the un-taken option.

---

## 1.3 How a partition count is changed without a window where ordering breaks

**The question.** ES-02 part 3: *«Изменение числа partitions требует
согласованной передачи позиции, не замены `hash % N` на ходу»*.

### The count is never changed. A partition is split.

This is the mechanism every documentation page omits and the study recovered
from `Segment.java`. A partition is `(id, mask)` where the mask is always
`2^k − 1`, and a key belongs to the partition whose id equals the **low k bits**
of its hash:

```go
func (this Partition) Matches(sequence string) bool {
	return this.mask == 0 || this.mask&hash(sequence) == this.id
}
```

A split takes partition `S` at mask `m` to two partitions at mask `2m+1`: `S`
itself, and `S + (newMask ^ mask)`. **Every key that was in `S` is still in
exactly one of those two, and no key in any other partition moves.**

**Why `hash % N` is not merely inconvenient but corrupting**, taken from the
study verbatim because it is the sentence the whole mechanism exists for: under
`hash % N → hash % (N+1)`, roughly `N/(N+1)` of all keys change partition, and
each lands in a partition whose checkpoint is at an unrelated position. A key
moving from a partition at position 900 to one at 1200 never has events 900–1200
delivered; one moving the other way has them delivered twice, **out of order
relative to the events of the same key already applied**. `OrderPaid` before
`OrderCreated` is not hypothetical; it is the median outcome.

**There is no stored N.** What exists is a set of rows, one per live partition,
in the checkpoint table whose primary key is already the projection name
(`event/eventpg/schema.go:311-341`). Axon reconstructs the mask from the set of
token rows and vv does not need to: each runner is constructed with its own
`(id, mask)` and its name carries it (`orders#3.7`), so the topology is what the
host started, and a change to it is the handoff below.

### The set is the thing that has to be right, so the set is a type

The mask makes each partition correct in isolation and says nothing about the
**set**, which is where Axon's `validateSegment` earns its keep: it reads the
split and merge candidate rows and refuses a claim over a segment whose topology
moved. `initializeTokenSegments` guards a different failure and is answered in
§1.3.1 below — *"only the **first** start may choose the count"*, enforced by
throwing `UnableToClaimTokenException("Could not initialize segments. Some
segments were already present.")` rather than adopting whatever is there. The two
are not one mechanism and vv answers them in two places: the `Cover` below, and
the resume-time refusal in §1.3.1.

Two topologies a host can declare, both of which every per-partition check
passes:

- **A gap.** `{0,3}`, `{1,3}` and `{3,3}` are started and `{2,3}` is forgotten.
  Every key in that quarter is never delivered, for ever. Each runner is healthy,
  each checkpoint advances, `Ready` is green everywhere, and an aggregate `min`
  computed over the same wrong set reports a healthy projection.
- **An overlap.** `{1,3}` and `{1,7}` are two different names, so
  `runtime.Supervisor` admits both, their checkpoint rows never conflict, their
  fences never meet, and every key matching both is applied twice — out of order
  with respect to itself the moment their cursors diverge.

Neither is detectable from one partition's own row, so the absent-row halt
(step 4 below) cannot see either: it detects only that *this* runner's row was
retired. What closes it is a change of shape rather than a check bolted on:

```go
// A complete cover of the key space: every key matches exactly one member.
// This is the value a host declares its topology as, and the runners are built
// from its members — so a gap and an overlap are refused where the set is
// written down rather than discovered as missing data months later.
type Cover struct{ /* unexported */ }

func NewCover(partitions ...Partition) (Cover, error)
```

`NewCover` refuses on two exact arithmetic facts and nothing heuristic: two
members overlap when `(idA ^ idB) & min(maskA, maskB) == 0`, and a set with no
overlap covers the whole space when `Σ MaxPartitions/(mask+1) == MaxPartitions`.
Together those two are an exact cover, computed over `Partition` values and
touching no store.

**`Cover` is where every set-shaped answer in this phase comes from** —
`Observe`, `Reached` and `Cutover` take one rather than a `[]Partition`, so the
`min` they compute cannot be a `min` over a hole. And the module page's recipe is
`for _, part := range cover.Partitions()`, so the runners a host starts are the
members of a set that was checked.

What it does **not** do, stated because it is the half that stays open: a single
runner cannot see the set, so nothing refuses a process that starts one member of
a cover it never declared. The framework checks the set where the set exists, and
a host that assembles runners by hand rather than from a `Cover` has the same
freedom it always had. That sentence goes on the module page beside `Split`.

**The hash is part of the wire contract.** FNV-1a/32 over the UTF-8 bytes of the
sequence key, published, and never changed — changing it moves every key, which
is the `hash % N` failure by another door. It is the same rule as [[D-125]]: a
composed key is a wire format.

### The handoff, in five steps, and the two extra ones are the point

Axon's `SplitTask` is five steps and looks like three. Here is vv's, and each
step is a failure somebody hit:

1. **Drain the parent.** `Projection.Drain` is acknowledged **between passes**
   (`projection.go:151-164`), so a shutdown never lands between a handler's write
   and the advance that accounts for it. This is what Axon buys with
   `workPackage.abort(null)` plus a 60-second in-memory `releasesDeadline`, and
   vv already has it as a `runtime.Drainer`.
2. **In one transaction of the checkpoint store's backing** — the caller's
   `Unit`, `crud.InNewTx` being the one-line spelling — read the parent's row and
   **both children's** (`Load`), write both children (`Save` at advance 1)
   **carrying the parent's cursor byte for byte**, and retire the parent
   (`Forget`). Every one of those calls is made through `event.Track`, one
   tracker per identity, and never against the raw store: [[D-129]] says the
   tracker door is the only caller of a `Checkpoints`, and this phase does not
   make itself the exception. So a half-absent row, a row carrying another name's
   cursor, an empty cursor at a live advance and a cursor over the ceiling are
   refused on the way in, exactly as they are for a pass.
3. **Commit.** Both children now stand at the exact point the parent reached.
   No key moved out of the parent's half of the space, and no key in any other
   partition was touched.
4. **A parent that did not drain halts by itself at its next save.** Its row is
   absent, which [[D-133]]'s table calls "the name was forgotten, reset or
   restored under a running projection" and answers with a halt. That is the
   correct outcome — its work was taken over — and it is loud. **This is vv's
   replacement for Axon's `validateSegment`**, which detects a concurrent split
   by looking for the sibling row; vv's fence detects it by the row being gone,
   which needs no new store call and no `List` on the `Checkpoints` contract.
5. **The host starts the two children**, as two `runtime.Runner` values with two
   names. `runtime.Supervisor` refuses duplicates (`runtime/supervisor.go:64-68`),
   so a topology declared twice does not start.

Steps 2 and 4 are the two that look redundant and are not. Step 2 is one
transaction because two transactions leave a window in which one child exists
and the parent is gone — every key of the missing child undelivered, forever,
with no error on any path. Step 4 is what makes a forgotten drain loud instead
of a second writer applying the parent's half of the space beside its children.

**The whole handoff is expressible in the shipped `Checkpoints` surface** —
`Load`, `Save`, `Forget`, `Transaction` — run inside the caller's unit of work.
The framework contributes the arithmetic and the refusals, not a new store call.
That is [[D-118]] and [[D-130]] holding rather than being worked around.

### What `Split` does with `Progress`

The counters are cumulative and a split must preserve their **sum over the
partition set**, because that sum is what an operator reads. So:

- both children take the parent's `Cursor` and its `Highest`;
- the **lower-numbered** child takes the parent's `Applied` and `Quarantined`;
  the higher-numbered child starts both at zero;
- `At` is the split's own instant, which is the consumer's observation as
  `Progress.At` has always been.

### A parent with no row is refused, because the framework cannot tell which absence it is

An earlier draft of this section had a second arm: splitting a **fresh** parent
(no row, `Checkpoint.Fresh()`) wrote nothing and answered the two children, on
the reasoning that both then start at the origin. That arm is removed, and the
reason is [[D-133]]'s own table read back:

> no row at all, or one **behind** the fence → the name was forgotten, reset or
> restored under a running projection → **halt** … creating a fresh row at
> advance 1 would leave the read model holding events no checkpoint accounts for.

A row is absent for two reasons and the store cannot tell them apart: the
partition never ran, or its row was forgotten, retired by an earlier split,
dropped by an operator, or lost by a partial restore. The second one at position
5 000 000 turns a successful-looking `Split` into two children that walk **the
whole log from the origin** into a live read model — the loop halts loudly on
exactly this input and a `Split` that guessed would restart a whole key space
silently.

So **`Split` refuses an absent parent**, with `ErrTopology` naming both readings
and the two remedies: a partition that genuinely never ran needs no split at all
— declare the new `Cover` and start its members — and a partition whose row was
lost is [[D-133]]'s restore case, which is an operator's problem and not a
topology change.

The same load answers the ambiguous-commit question, and it is why step 2 reads
three rows rather than one:

- parent present, both children absent → the handoff;
- parent absent → refused, as above, and the message names any child row it
  found, because that is the "the first attempt committed after all" reading;
- parent present beside an existing child row → refused: a child of a live parent
  is a previous attempt that half-landed or a name in use, and neither is one to
  write over.

**And that makes `Split` correct under a `Unit` that runs its body more than
once**, which [[D-130]] says a caller's may. Every decision is derived from rows
read inside the run and nothing is carried between runs, so a body re-run after a
rollback sees the original three rows and does the same thing, and a body re-run
after a commit finds the parent gone and is refused rather than writing children
at the origin. Arity is answered by holding no state, not by counting attempts.

### A merge is refused, and here is the reason rather than the refusal

Axon merges two segments at **different** positions with a `MergedTrackingToken`
— *"keeps track of the progress of the two original halves, by advancing each
individually, until both halves represent the same position"* — which replays
`(min, max]` and delivers each event only to the half that had not passed it.
Taking the min re-delivers to the ahead half; taking the max skips for the
behind half; both are silent.

vv cannot build one. Two independent reasons, and either alone is sufficient:

1. **[[D-129]] forbids ordering two cursors.** A cursor is opaque bytes,
   compared for equality and emptiness and never ordered. `min` of two cursors
   has no legal spelling.
2. **Equality is not available either, on the store this phase is for.** An
   `eventpg` cursor is `vve1` plus a log identity plus `(from, bound, reach)`,
   where `bound` is `pg_snapshot_xmin(pg_current_snapshot())` **minted at read
   time** (`event/eventpg/cursor.go:29-47`, `read.go:317-327`). Two partitions
   that walked to the same logical position at different moments hold **different
   cursor bytes**. So "merge when the two cursors are equal" is a rule that would
   almost never fire and would fire non-deterministically when it did.

**So the supported way to reduce a partition count is a rebuild** — create the
target topology as a new generation and cut over (§1.5), which is in this phase
and needs no second mechanism. `Merge` is not exported and `ErrTopology` names
the refusal if one is ever attempted through `Split`'s inverse. What is refused is
the function; the *state* an operator can reach by hand is covered only in the
direction `Split` creates, and §1.3.1 says which half that is.

### The cost that must be stated, because it is the opposite of Axon's

Axon's `PooledStreamingEventProcessor` opens **one** stream in a coordinator and
fans out to work packages. vv cannot: `startsNothing`
(`scripts/extensions_test.go:147`, called from `scripts/event_test.go:66-68`)
forbids a `go` statement in any non-test file under `event/`, and [[D-092]] is
why. So **N partitions are N runners and N independent walks of the whole log.**

- Partitioning multiplies read traffic by N and does **not** reduce it. What it
  parallelises is the **handler**, which is the expensive half and the reason to
  do it at all.
- It multiplies the `xmin`-stall exposure by N, because each walk mints and waits
  on its own settlement bound. That is a new cost of an already-accepted
  mechanism and it belongs on the module page.
- EVENTSOURCE_REFERENCE §Reject 3 is the ceiling: a filter parameter on
  `Log.ReadAll` is refused, and the eventual answer is *"a shared reader fanned
  out to N routers — not a filter on the store contract"*. Phase 4 does not build
  it and does not foreclose it.

`MaxPartitions = 1024` is published as the ceiling, and the module page says the
useful range is 8–16.

### 1.3.1 Only the first start chooses the topology, and the migration nobody warns you about

Axon's `JpaTokenStore.initializeTokenSegments` throws rather than adopting an
existing set of segment rows, and the sentence behind it is *"only when a
streaming processor starts for the **first** time can it initialize the number of
segments to use"*. The `Cover` above does not answer that: a `Cover` is checked
where the set is written down, and a set that is internally perfect can still be
started over a projection that is already recording through one row.

**The migration an operator will actually perform.** Release *n* runs `orders`
unpartitioned to position 1 000 000. Release *n+1* adds `Partition:` to the spec
and deploys two replicas at `{0,1}` and `{1,1}`. Both are admitted. Both start at
the origin, because their rows — `orders#0.1` and `orders#1.1` — do not exist.
Both walk the whole log into a read model that is already at 1 000 000, and the
old row at `orders` is still there and still healthy. Three rows, all reporting
the same watermark, no error on any path, every dashboard green. This is
[[D-128]]'s watermark being *per row* and therefore true of each and false of the
set, reached by a door §1.2's `hash % N` argument does not cover.

**It survives the filter.** Once each partition reads only its own half, the
duplicate count halves; it does not go to zero. The read model still takes the
whole log a second time.

**So a resume refuses it.** A runner whose partition is not `Whole()` asks the
store, once, whether a row exists for any **coarser** share of its own key space —
`Identity.coarser()`, which is `{0,1}`'s ancestor `orders` and `{0,3}`'s
ancestors `orders` and `orders#0.1`, and which is empty for a projection that
named no partition. A row that is not `Fresh()` is a second writer over every key
of this runner's share, and the runner **halts** with `ErrTopology` naming the row
it found and naming `Split` as the route.

**And a resume refuses the mirror of it, which is the rollback of a bad deploy.**
`Split` retires the parent's row, so once the handoff is done there is no row for
release *n* to be refused by: redeploying the spec that was in production one
release ago — the same spec, unchanged, with no `Partition:` — finds nothing for
itself and nothing for any ancestor, resumes from the origin and walks the whole
log into the read model the two live children are filling. Three rows, all
reporting a healthy watermark, no error on any path. It is the same failure this
section is about, reached through the door `Split` itself opens, and it is not a
hand-declared anything.

So `Split` records the retirement durably, in the same transaction: a checkpoint
row named `<identity>#split`, carrying the cursor the parent was handed down from,
under a name no identity renders. **Every** runner asks about its own before it
resumes — one `Load` at start-up, for the partitioned and the unpartitioned
alike — and halts with `ErrTopology` naming the row when it is there. The way back
to one runner over that share is a new generation; `Forget`ting that row is the
deliberate act that re-admits the old topology, and the refusal says so. A
projection name within `len("#split")` of `event.MaxNameBytes` has no room for the
mark, so `Split` refuses such a name outright rather than retiring a share whose
retirement it could not record — which is what lets an absent record be read as
"no split happened".

Three reasons it is a resume and not a construction check: `New` performs no I/O
by contract; the answer is a fact about the store rather than about the spec; and
the failure it guards is a *deployment* rather than a *composition*, so the place
it must be visible is `Ready` and the observer.

The refusal clears itself the moment the handoff is done: `Split` retires the
parent's row in the same transaction as it writes the children, so a child
started after a split finds no coarser row and is admitted — and no retirement
record of its own, because a split records one for the parent it retires and never
for a child it creates. A partition set declared on a projection that has never
run finds neither. Those are the two admissions, and they are what keep the
refusal from being "no partitioned projection may ever start".

**What it does not cover, stated because it is the half that stays open.** A row
for a *finer* share this framework did not create — `orders#0.3` live while a
runner at `orders#0.1` starts, where `orders#0.3` was **hand-declared** rather
than reached through `Split` — is not detected, because the finer set is unbounded
and enumerating it would need a `List` the `Checkpoints` contract does not have
and this phase does not add. The finer shares `Split` itself creates *are* covered,
by the retirement record above, and that is the direction the default migration
reaches.

What §1.3 refuses is a `Merge` **function**: there is none, and there will be
none, because it would have to order two cursors. It does not refuse the *state* —
an operator can still hand-declare a coarser cover over a finer one, and nothing
reads it. The supported route down is a new generation.

---

## 1.4 What the park parks, how it is bounded, what overflow does, and what a skip marks

**The question.** ES-03 part 3: *«parking и scan checkpoint атомарны; успешная
applied-позиция считается отдельно. Очередь ограничена числом sequences/bytes;
overflow останавливает затронутую partition, не пропускает событие. Retry
обрабатывает sequence по порядку; skip — явная операторская операция с отметкой
неполноты проекции.»*

### What it parks

**A sequence, not an event.** The failing envelope, *and every later envelope of
the same sequence*, and the later ones **never reach a handler at all**. That is
the whole difference between a dead-letter queue and a skip list, and it is the
largest gap in the shipped tree: `oneAtATime` (`pass.go:245-267`) passes a
permanently-failing envelope to the sink and **continues to the next envelope of
the same stream**, so `OrderPaid` is applied over an order whose `OrderCreated`
was quarantined.

The dispatch, taken from `DeadLetteringEventHandlerInvoker.handle` and rewritten
in vv's vocabulary:

```
for each envelope of the page that this partition matches:
    sequence := Sequence.SequenceOf(envelope)
    if park holds sequence:            park this one too, do not call the handler
    else:                              call the handler; on a permanent failure,
                                       park it and every later envelope of that
                                       sequence in this page
```

**`Quarantines` is replaced by `Park`.** Two mechanisms where one blocks the
sequence and one does not is exactly the pair that produces the failure above,
so the weaker one goes. `Quarantined` (the struct) becomes `Letter`, and
`Failure`'s second value becomes **`ParkSequence`** — not `Park`, because
`type Park interface` and `const Park Failure` cannot coexist in one Go package
and a rename table that produced both would not compile. The verdict is named
for what it does that the old one did not: it parks the **sequence**, not the
envelope. `Progress.Quarantined` — the kernel field — keeps its name and its
meaning: envelopes the destination did not take. Nothing is tagged, so a removed
line is not yet a breaking change; §7.3 records all three renames with their
final spellings.

### The park is keyed by the projection and the generation, and never by the partition

A park keyed by the runner's full recorded name (`orders@2#1.1`) is wrong, and it
is wrong in a way that only appears when two of this phase's mechanisms meet.
Sequence `A` fails permanently, `A2 A3` are parked under `orders@2#1.1`, and an
operator then splits that partition — a supported, documented, first-class
operation of this same phase. The children are `orders@2#1.3` and `orders@2#3.3`;
each holds **zero** parked sequences; by the fast path below `Holds` is therefore
never called; and the first later event of sequence `A` goes straight to the
handler over a read model that never received `A2`. The letters are still there
and still true, and nothing can ever reach them again.

So the key drops the one part a split moves:

> **A park is keyed by `Identity.Whole()`** — the projection and its generation,
> with the partition dropped, rendering `orders@2`. The framework hands a `Park`
> and a `Redriver` nothing else, so an implementation that keys its table on the
> value it is given is correct by construction and there is no second plausible
> key to pick.

Three consequences, and the third is the cost:

- **A split needs to do nothing about the park**, because nothing moved. A parked
  sequence's key hashes into exactly one child, that child's `Holds` answers true,
  and the block survives the topology change with no re-keying transaction and no
  refusal to split.
- **A redrive is generation-wide**, which is what an operator wants: they drain
  `orders@2`, not `orders@2#3.3`, and they do not have to know which partition a
  poison order landed in. A `RedriveSpec` naming a partitioned identity is
  refused with `ErrTopology`.
- **A partition pays the existence check while any partition of its generation
  holds a parked sequence**, not only while its own does. That is a real cost and
  it is stated rather than hidden. It buys the two properties above and it never
  costs correctness: a sequence belongs to exactly one live partition, and the
  only thing that moves a sequence between partitions is a split, whose children
  are new runners and therefore start with a resume that re-reads the count.

### The existence check, and why vv pays less for it than Axon

`enqueueIfPresent` is the ordering primitive and is defined in terms of
`contains`, so the causal-order guarantee is **one existence check per event on
the hot path**. Axon bounds the cost with a `SequenceIdentifierCache` per
segment. vv has something exact instead of heuristic:

**While this generation holds zero parked sequences, `Holds` is never called.**
A healthy projection therefore pays **nothing**: no round trip, no cache, no
eviction policy.

**One clearing rule, stated once, because three of them is how an invariant
becomes false without anybody editing it.** The count is `Park.Sequences`, and it
is read:

- **once per resume** — a process start, or an `overtaken` that adopted another
  instance's row — whatever it answers;
- **and again at the start of every pass, while the count this loop believes is
  non-zero.**

That is the whole rule. A healthy projection pays one call per resume and nothing
per pass, which is the property the fast path exists for. A projection that has
parked something pays one extra call per pass — against a page, which is a read
of up to `MaxRead` envelopes and a handler call, so it is not a cost that shows
up — and the moment an operator's redrive empties the queue, the very next pass
reads zero, stops calling `Holds`, and returns to `PhaseFollowing`.

The rejected rule is "read once per resume and never again": under it the count
is monotone within a run, so a projection that parked one sequence and had it
redriven stays `PhaseDegraded` and pays a `Holds` per envelope for the life of
the process, `State.Parked` means "parked since this instance resumed" rather
than "blocked now", and an operator watching the phase to know when their drain
is finished never sees it clear. §INV-095 calls the count live, and this is what
makes it live.

The obligation this creates and which must be stated: **only the projection
parks, and only a redrive removes.** An operator inserting a letter by hand
leaves the in-memory count low until the next pass that re-reads it — which,
while the count is zero, is the next *resume*. The `Park` contract says so.

### The park is tier A only, and that is a construction refusal

The causal-order guarantee is not a property of the queue. It is a property of
the queue **and the read model committing together**: the loop's `Holds`, the
loop's park write, the loop's applies and a redrive's `Evict` all serialise
because they are rows in one transaction. Take the transaction away and the
guarantee goes with it, in a way no test in the suite would notice:

> A redrive is draining sequence `A`. Its eviction of the letter that was
> blocking the sequence commits, so a `Holds` now answers false — while its write
> for `A7` is still in flight in a transaction of its own. The loop, mid-page,
> reads that `false` and applies `A9`. `A9` lands before `A7` — the exact defect
> the queue exists to prevent, produced by two supported operations with no error
> on any path.

Under `AfterApply` there is no unit at all. Under `Unchecked` there is one, but
the read model is not in it, so the park row orders the bookkeeping and not the
writes. Both lose it.

**So `OnPermanentFailure: ParkSequence` requires `Advance: InUnit` and a
`Destination` that is not `Unchecked`, and `New` refuses the other combinations
with `ErrSpec`.** Tier B is then refused per pass by §1.1's comparison, so a park
that constructs is a park at tier A. The alternative — admitting the weaker
wiring with a scoped invariant — was rejected because §INV-090's whole falsifier
list would be tier A while half the shipped matrix silently did not hold it, and
an invariant that is false in a configuration the framework ships is worse than
no invariant at all.

What a foreign destination gets instead is `Halt`, which is honest: with the read
model outside the unit, "parked" and "applied" cannot be ordered against each
other, and a queue promising causal order there would be promising something it
cannot deliver. The module page says so beside the `Unchecked` section.

### How it is bounded

Two dimensions, exactly Axon's, and the check is `isFull(sequence)` and never
`isFull()` — a queue holding 1023 sequences of one letter each still accepts a
1024th letter **into an existing sequence**:

- a **new** sequence is refused when the queue already holds `MaxSequences`;
- an **existing** sequence is refused when it holds `MaxSequenceLetters`.

The appendix asks for *«sequences/bytes»*, so a third is named: a byte bound over
the sum of parked payloads, computable because `Limits.MaxPayload` bounds each
one. Axon has no byte bound; this is vv's addition and it is the one that matters
on a store where the payload is a `bytea`.

**The numbers belong to the implementation, and the shape belongs to the
contract.** `Park` is the application's table; it publishes its own bounds, and
what this framework declares is the sentinel (`ErrParkFull`), the two-dimensional
rule, and the reaction. Axon's 1024/1024 are on the module page as the numbers
that have been in production somewhere.

### What overflow does

`DeadLetterQueueOverflowException` is **not caught** in Axon's `invokeHandlers`.
It propagates, the unit of work rolls back, the token does not advance, and the
processing group stops making progress. The study's note is the point: *"it is
achieved by not writing a catch, which is the sort of thing a re-derivation gets
wrong by being tidy."*

vv's failure table is total by construction, so the arm is written rather than
absent, and it is a **third verdict** beside retryable and permanent:

> `ErrParkFull` from `Park.Park` ends the pass. The unit rolls back, the
> checkpoint does not advance, **no envelope is skipped**, nothing is parked, and
> the partition retries **without an attempt budget** — like `ErrBackend`,
> because an operator draining the queue is the fix and the projection must
> resume by itself when they do. `State.Phase` is `PhaseBlocked` and `Ready`
> fails once the accumulated backoff outlasts `Tolerate`.

"Nothing is parked" is a statement about a **unit**, and it is true here because
a park exists only at tier A. A page in which `A2` parked and `C5` then hit the
bound rolls `A2`'s letter back with everything else, so the retry re-parks it
rather than adding a second copy — which matters, because the retry has no
attempt budget by design and a retry that refilled the queue while an operator
drained it would never stop. That arm is written for one mode because there is
only one mode: `ParkSequence` outside tier A does not construct.

It is not a halt. A halt is terminal for the value's life ([[D-130]]) and would
need a redeploy to clear a condition an operator can clear with one `DELETE`.

### The state, so "caught up" is never a lie

ES-03: *«состояние показывает blocked/degraded, не ложное "догнал историю"»*.
Today `PhaseFollowing` is published whenever a read answers `!more`
(`pass.go:80`) — including when the last page parked half its envelopes. Two
phases are added and the appendix names both:

| Phase | When | `Ready` |
|---|---|---|
| `PhaseFollowing` | the whole log is read **and nothing is parked** | passes |
| `PhaseDegraded` | the whole log is read and N sequences are parked | **passes** |
| `PhaseBlocked` | the last pass ended in `ErrParkFull` | fails past `Tolerate` |

`Degraded` deliberately does not fail readiness. The whole point of a
dead-letter queue is that one broken order does not stop the projector, and a
replica reporting unhealthy for a poison order is a projector that stopped by
another route. What carries the fact instead is `State.Parked` (the live count)
and `Progress.Quarantined` (the durable, cumulative one).

### The applied position: refused, and what is given instead

*«успешная applied-позиция считается отдельно»* asks for a second, lower
watermark. **Phase 4 refuses to publish one**, and the refusal is the honest
answer rather than a shortcut:

- A number "every event at or below which was applied" is computable only while
  nothing has ever been **skipped**. The moment an operator evicts a letter
  unapplied, the hole is permanent and below any such watermark, and the number
  becomes a lie that is worse than no number.
- It could not be a `Cursor` and could not be turned into one ([[D-129]]), and
  publishing a second `Position` beside `Highest` invites exactly the reading
  [[D-128]] forbids.

What an operator gets instead is what `event/checkpoint.go:29-32` already says
and phase 4 makes true rather than approximate: **`Progress.Quarantined` is
non-zero exactly when the destination has holes, and that is what keeps `Highest`
from reading as a completeness claim.** Plus `State.Parked`, the live count of
blocked sequences, and the queue itself, which is the only place the identity of
what was not applied lives.

### The redrive

Not automatic. Axon states it plainly — *"Although this enables the processing to
continue in case of errors, it doesn't retry the failed events automatically"* —
and recommends a scheduled component *"with a large interval to not stress the
system too much"*. vv says the same and has no choice: a background retry is a
goroutine, and there are none under `event/`. The host wraps `Redrive.Any` in a
`runtime.Runner` if it wants one.

Three properties of the retry, each from the source and each load-bearing:

1. **One sequence per call, in insert order, stopping at the first letter that
   fails again.** `processLetterAndFollowing`: on success evict and advance to
   the next letter of the sequence; on failure requeue and **return**. A
   sequence is a queue, not a set.
2. **Rotation by "least recently tried".** `processAny` picks by
   `ORDER BY lastTouched ASC`. A loop that always picks the same failing sequence
   starves every other one; a loop that picks randomly loses the fairness. The
   clock lives in the application's table, which is where [[D-126]]'s posture puts
   it — the framework asks for the oldest and owns no duration.
3. **A redrive does not touch the checkpoint.** The fence admits one writer and
   the loop owns it. The redrive runs on the operator's goroutine, inside the
   operator's unit, and its evidence is the queue.
4. **A sequence is claimed, not merely read.** This is the one the study recorded
   (§ES-03 nuance 8) and an earlier draft of this document dropped. Axon claims a
   letter with
   `UPDATE … SET processingStarted = :now WHERE … (processingStarted IS NULL OR
   processingStarted < :now − claimDuration)` and selects only unclaimed
   sequences. Without it, `Oldest` is a read: two operators — or one operator and
   the `runtime.Runner` §1.4 explicitly invites a host to wrap `Any` in — take the
   same least-recently-tried sequence, load the same letters, and apply from the
   first, so `A3` can land before `A2` finishes. That is the queue's own ordering
   guarantee broken by the queue's own recovery path.

   So `Redriver.Oldest` becomes **`Redriver.Claim`**, one method for both
   entry points: `Claim(ctx, of, "")` takes the least recently tried *unclaimed*
   sequence, `Claim(ctx, of, "A")` takes that one if it is unclaimed, and both
   answer `found = false` rather than an error when everything is claimed.
   `Release` is called on every exit path, and a claim that is never released
   expires by **the application's** clock in the application's table — which
   [[D-126]] permits precisely because it is not a store's clock and not a fence.

**The redrive and the loop cannot both apply one letter**, because the park row,
the read model and the `Evict` are one transaction — the park exists only at tier
A, so this is a property of every configuration that constructs rather than of
the good half of a matrix.

**And the redrive's own writes are one transaction too.** `RedriveSpec` carries a
`Unit` and a `Destination`, and the tier check of §1.1 applies to a redrive call
exactly as it does to a pass: tier B is refused before the handler runs, and
`Unchecked` is refused at construction, for the reason above. Within one letter
the handler's write, the `Evict` and the `Touch` commit together; a crash between
them leaves the letter parked and nothing applied, and the next redrive starts
that sequence again from its first letter. A redrive is therefore at-least-once
per letter and never at-most-once, which is the same promise the loop makes and
the same reason `(Stream, Version)` is the idempotency key.

### What a skip marks

An eviction without application is the operator saying "this event will never be
applied". It marks **two** things and both are durable:

- the letter leaves the queue with an evicted state the `Redriver` records, so
  the operator's own table answers *what* was skipped;
- `Progress.Quarantined` was already raised when the letter was parked and is
  **never decremented** by anything — not by a successful redrive, not by an
  eviction. It is the projection's permanent statement that its destination has
  holes.

That asymmetry is deliberate and is stated: a successful redrive raises `Applied`
in nobody's counter, because the redrive is not the loop and may not write the
fence. The queue is the record of the redrive; the checkpoint is the record of
the scan.

### "Had a failure" and "has a hole now" are two questions, and only one of them gates a cutover

`Progress.Quarantined` answers the first and cannot answer the second: it is
cumulative by design and never falls, so a generation that parked one sequence
and then redrove it **completely** — a read model with no holes at all — carries
a non-zero `Quarantined` for ever. A cutover gated on that counter refuses the
ordinary recovery path (park, fix, redrive, cut over) and sends every operator
through `AcceptQuarantined`, the flag whose entire purpose is to admit a
*known-broken* generation. A check that must be overridden routinely has stopped
being a check.

So the second question is asked of the place that can answer it — the park:

```go
// The envelopes this generation parked and never applied: the letters queued
// now, plus the letters an operator evicted without applying. Zero means the
// destination has no holes, which a cumulative counter cannot say.
Holes(ctx context.Context, of Identity) (uint64, error)
```

One count over the application's own table, in the direction that can go down.
`Progress.Quarantined` keeps its meaning — the permanent statement that something
once failed — and `Holes` is the cutover's evidence (§1.5). Both are published,
neither is derived from the other, and §INV-096 is unmoved: no counter is
decremented, because `Holes` is a question rather than a counter.

---

## 1.5 What a generation is, what the cutover switches, and what keeps the old worker out

**The question.** ES-04 part 3: *«generation имеет отдельные данные, checkpoints
и claims; catch-up продолжается до согласованного барьера. Переключение read
target атомарно для заявленного набора таблиц. Старый worker не пишет в новое
поколение. Rollback допустим, пока старое поколение поддерживается актуальным
либо снова догнало историю. Rebuild получает отдельный ресурсный бюджет.»*

### What a generation is

**A generation is a number in the projection's recorded name.** Everything else
follows from that and costs nothing new:

- its **checkpoints** are its own, because the row key is the name
  (`orders@2`, `orders@2#3.7`);
- its **claims** are its own, because the fence is per row;
- its **data** are the application's own tables, chosen by the application from
  the ordinal it is handed on every `Batch`;
- its **park** is its own, because the park is keyed by `Identity.Whole()` — the
  projection *and* the generation (§1.4), which is a strictly finer key than the
  projection and a strictly coarser one than the partition.

This is already proven: `TestARebuiltProjectionDrainsBesideTheLiveOneAndTheRowsAgree`
(`event/eventpg/rebuild_integration_test.go`, UC-120) runs a second projection
name over one log into a second destination and asserts the rows agree, and its
comment states the design position — *"a rebuild is two projections over one log
with two destinations and nothing between them"*. Phase 4 does not rebuild that.
It adds the three things a name alone cannot give: a barrier, an atomic switch,
and an ownership boundary.

### The rebuild-by-name spelling, and the exact edge of the guarantee

A rebuild spelled the shipped way — a second `Spec.Name` — has
`Generation == Ungenerated`, so nothing this phase adds applies to it: no
ownership row is read, and `Effects` set beside it stages **every historical
event**. That is the failure ES-06 exists to prevent, at full historical volume,
reachable through the spelling the current module page teaches.

It is not closed by a refusal, and the reason is that the framework cannot see
it: `orders-rebuild` is a `Spec.Name` like any other, and no signal distinguishes
"a rebuild of `orders`" from "a new projection that happens to read the same
log". Requiring a `Generations` beside every `Effects` was considered and
rejected: it would impose an ownership table on every single-sender projection
that has no generations at all, and it would still not gate the case, because the
second name's own ownership row answers `Ungenerated`, which is what the spec
asks for — the gate would pass.

So the answer is three things and none of them is magic:

- **The recipe changes.** A rebuild is `Spec.Generation`, and the module page's
  rebuild section teaches that spelling and only that one. UC-120's shipped test
  keeps working unchanged; it carries no `Effects` and never did.
- **The edge is written down** — here, on the module page, and in D-135: the
  effect gate covers a generation, and a second projection name is not one.
- **The edge is measured, not assumed.** §UC-184 asserts what a second name with
  `Effects` set actually does, in the shape §UC-160 already uses for the
  handler-ignores-its-batch boundary. A guarantee whose limit is asserted by a
  test is one a reader can trust; one whose limit is asserted by a sentence is
  one somebody edits away.

`Generation` is a `uint32`. `Ungenerated` (zero) renders nothing, so every
projection that exists today is generation zero and its name does not change.

### What the cutover switches, atomically

**Neither source ships an atomic read-target switch.** Marten's cutover is a
load-balancer switch between blue and green nodes; the tables are simply named
differently and each node's code reads its own version. There is no
`Activate(generation)` in either implementation, so ES-04's *«переключение read
target атомарно для заявленного набора таблиц»* is vv's own requirement with no
reference behind it.

**The answer is that the read target is a row, and a row switches atomically for
every table at once.**

```go
type Generations interface {
	Active(ctx context.Context, projection string) (Generation, error)
	Activate(ctx context.Context, projection string, from, to Generation) error
}
```

- **It is atomic over the declared set of tables** because it is *one* write, and
  the set is declared where it belongs: in the application's own code, which maps
  an ordinal to its tables. The framework never issues DDL, never learns a table
  name, and never renames anything.
- **`Activate` is fenced on `from`** — a compare-and-set, exactly the shape the
  checkpoint save already has. Two operators cutting over at once produce one
  winner and one `ErrConflict`.
- **The table is the application's**, behind the interface, for the same reason
  `Park` and `Quarantines` are. It must live in the checkpoint store's database,
  because §1.6 reads it inside the transaction that commits the advance — and
  that read must be a **locking** one, which is §1.6's second obligation and is
  stated on the interface beside this one.

**And the promise carries its precondition, in the same breath, wherever it
appears** — [[D-130]] makes that a house rule for exactly this shape:

> The switch is atomic for every table at once **for a reader that resolves the
> row in the same snapshot as the tables it then reads**: one transaction, or one
> statement that joins the row.

A read path that caches `Active` per process, per request or per connection — the
obvious implementation, since it is one row read on every query — is not that
reader, and its window is however long its cache lives. That is a legitimate
choice and the framework does not forbid it; what it may not do is call the
result atomic. The sentence goes on the `Generations` type, on the module page
and in D-134/D-135, not in one of the three.

**Retirement is ordered against readers, and the ordering is the operator's.**
Nothing in the framework drops a table, so nothing in the framework can drop one
too early — but the recipe can, and a reader still holding generation 1 that
meets dropped tables is a hard outage arriving from following this document. So
the rule is stated: a retired generation's tables, rows and letters may be
dropped only once **every reader that could still hold the old row has
re-resolved**. A deployment knows that when its readers resolve per request in
the same transaction (the window is one request) or after a grace period it chose
and wrote down. `Cutover` moves the row; retirement is a second, later,
deliberate step; and §UC-161's negative arm asserts that a reader which resolved
earlier keeps reading the retiring generation and that this is **correct** until
it is dropped.

### Readiness: a barrier is a `Position`, and that is legal

The study's third open question was that ES-04's readiness seems to need ES-05's
vocabulary. It does not, and the reason is a distinction [[D-129]] draws
precisely:

> **A `Cursor` may not be ordered. A `Position` may.** [[D-129]] forbids a
> function from a position to a cursor and forbids ordering two cursors. It says
> nothing against comparing two positions, and [[D-128]] makes `Progress.Highest`
> a completeness watermark rather than "the last position of the page".

So the barrier is a position:

```go
type Barrier struct {
	Projection string
	Generation Generation      // the generation it was observed from
	At         event.Position  // and it is not a resume point, ever
}
```

`Observe` reads it off the retiring generation's checkpoint rows and answers the
**lowest** `Highest` across its partitions — §1.2's `min` rule, because a
partition set is only as far along as its furthest-behind member. `Reached`
asks the same of the arriving generation and answers four facts rather than one:

```go
type Readiness struct {
	Reached     bool
	Behind      event.Position  // how far the furthest-behind partition still is
	Quarantined uint64          // the cumulative sum across its partitions
	Holes       uint64          // what it parked and never applied — the evidence
}
```

### A barrier a caller can invent is not evidence, so `Cutover` does not take one

An earlier draft put `Barrier` in `CutoverSpec`. Read as a caller sees it, that
contract says: *hand me the number I will check you against.*
`Cutover(ctx, CutoverSpec{From: 1, To: 2, Barrier: Barrier{}, …})` compiles, runs
and returns nil — a zero barrier is reached by anything, an arriving generation
that applied nothing has `Quarantined` zero, and every read moves to a set of
empty tables. No error, no warning, and the symptom is a live site serving an
empty read model.

**So the barrier is not a parameter. `Cutover` observes it itself**, from the
retiring generation's own rows, inside the same unit as the fenced write:

1. `Observe` the retiring generation over `spec.Retiring`;
2. `Reached` the arriving generation over `spec.Arriving` against that barrier;
3. refuse, or issue the one fenced write.

`CutoverSpec` therefore carries the two **covers** and no barrier at all, and the
forged value has no door left. `Observe` and `Reached` stay exported because an
operator's readiness loop needs to watch `Behind` before it commits to anything —
but they are an observation for a human, and `Cutover` re-derives rather than
trusting what one answered a minute ago.

**The second door is an absent row read as position zero**, and it is closed in
`Observe` rather than in its caller. A partition with no checkpoint row is not at
position zero; it is a partition that has not reported. So:

- every member of the cover fresh → the barrier is the origin, and that is the
  true answer: this generation has delivered nothing, so nothing is owed;
- some fresh and some not → `ErrTopology`, naming the member with no row, because
  a `min` over a set that is missing a member is not a `min`;
- the cover itself is a `Cover` (§1.3), so a set with a gap or an overlap never
  reaches either function.

### The evidence a cutover refuses on

**`Highest ≥ B` says every event at or below `B` was delivered, not that the rows
agree.** So `Cutover` refuses an arriving generation whose destination has holes.
That is Marten's asymmetry taken across: `RebuildErrors.SkipApplyErrors = false`
while `Errors.SkipApplyErrors = true`, *because a rebuild that skipped produces a
table that cannot be compared to the live one, and comparison is the only
evidence the cutover is safe*.

The number it reads is `Readiness.Holes` — §1.4's live question — and **not**
`Quarantined`, which is cumulative and would refuse a generation that recovered
completely. When the spec carries no `Park` at all, nothing could have been
parked, `Holes` is zero by construction, and `Quarantined` is the same number; the
two only part for a generation that parked and recovered, which is precisely the
case the counter gets wrong. A cutover that wants a holed generation anyway sets
`AcceptQuarantined` and says so at the call site — and now that flag means what
its name says instead of being the ordinary path.

### What keeps the old generation's worker out of the new one

Four things, weakest first, and only the last is durable:

1. **Different names, different checkpoint rows, different parks.** Free by
   construction — and the study is right that it deserves a test rather than an
   inference (§UC-160).
2. **`runtime.Supervisor` refuses duplicate runner names**, so two runners of one
   generation do not start in one process.
3. **The handler is handed its generation.** `Batch.Identity.Generation` is what
   the application resolves its table from, so a handler physically cannot write
   into another generation's table without ignoring the batch it was given.
4. **The effect capability is gated on the ownership row, read inside the
   committing transaction.** §1.6. This is the durable half, and it is the one
   neither Marten nor Axon has.

### Rollback

*«Rollback допустим, пока старое поколение поддерживается актуальным либо снова
догнало историю.»* Marten leaves this to the operator: the old tables are still
there and its daemon is still running, so rolling back is "stop switching
traffic". Stating the condition is the contribution, and phase 4 states it as a
refusal:

**A rollback is `Cutover` with `From` and `To` exchanged, and it is admitted only
when the target generation's checkpoint rows still exist and it has reached a
barrier observed from the generation now active.** A generation whose rows were
dropped is `ErrRetired`; one that stopped and fell behind must be started and
allowed to catch up first, which is the same `Reached` loop as a forward cutover.
Symmetry here is not elegance — it is what makes the rollback path exercised by
the same tests as the forward one.

### The overlap window, named and not closed

From Marten's own `rebuilding.md`, and it is the honest half of blue/green:

> **Accepted overlap window** — `N` is snapshotted when the new version starts.
> If an old version is still running and advances past `N` afterward, the new
> version can re-emit side effects for that `(N, old_final]` overlap. Stop the
> old version before (or as) the new one starts to avoid the window; fully
> coordinated drain-and-handoff is a separate concern.

That passage has two halves, and they get two different answers.

**The side-effect half vv closes**, and this is the strongest single thing phase 4
contributes over both sources: the effect gate reads the ownership row **inside
the transaction that commits the advance**, so the retired generation's next page
finds itself inactive and stages nothing. §1.6.

**The read-target half vv does not close, and names.** *(Written when the S4
implementation review was closed; it was answered nowhere before.)* `Cutover`
derives its barrier from the retiring generation's rows as they stand when it
reads them. That generation is a separate runner committing in its own
transaction: nothing claims, locks or fences its rows, and no isolation level
helps — [[D-126]] forbids the store choosing one, and `REPEATABLE READ` would make
the barrier the snapshot value, which is stale-low in the same direction. So if
the retiring generation is still advancing, the read target lands on a generation
standing where it stood a moment ago and **reads move backwards** by that
advance, until the arriving generation catches up.

The bound is Marten's own sentence with vv's terms in it: the regression is the
retiring generation's advance rate times the life of the caller's transaction,
and **stopping or draining the retiring generation before — or as — the switch
commits avoids it**. `Observe` it twice and see whether the barrier moved; that is
the whole of the check, and it needs no surface that does not already ship.
`Spec.Pace`, this phase's answer to ES-04's resource budget, slows the arriving
generation and therefore lengthens the recovery, so it is dropped before cutting
over — `Cutover`'s contract says so and `Spec.Pace`'s field comment says so.

**The alternative is refused in writing rather than left unmentioned.** Axon's
`resetTokens` is the one mechanism either source has for this: one transaction,
every segment, **processor stopped**, every token claimed. Neither half is
available here. `Cutover` cannot stop a runner in another process, and claiming a
live generation's checkpoint rows means *writing* them, which takes that runner's
fence away — an operator's write in the advance path of a runner nobody at this
call site supervises, bought to close a window an operator closes by draining.
Driven rather than argued: the mutation that adds the claim answers *"a checkpoint
row moved between the save this transaction staged and its commit"*. §UC-201 is
the case, and its control is the drained one.

### The resource budget

*«Rebuild получает отдельный ресурсный бюджет.»* Marten's numbers are per
**database** — `MaxConcurrentEventLoadsPerDatabase = 4`,
`MaxConcurrentBatchWritesPerDatabase = 4`, `MaxConcurrentRebuildsPerDatabase =
max(1, MaxPoolSize/8)` — and they are enforced by an agent scheduler vv does not
have and may not build ([[D-092]], `startsNothing`).

vv's answer is two things and a statement:

- **`Spec.Pace time.Duration`** — a minimum interval between reads while
  draining. Zero, the default, is no pacing. A rebuild is told to read slower
  than the live projection, which is the whole of what a concurrency cap buys
  when each runner is single-threaded anyway.
- **The pool is the budget.** A rebuild given its own `crud.Source` over a pool
  with a smaller `MaxOpenConns` is capped by the thing that actually runs out.
  This is a documented recipe, not an API.
- **And the arithmetic is stated**: a rebuild of a projection with N partitions
  is N more independent walks of the whole log against one database, on top of
  the live generation's N, each with its own `xmin`-stall exposure. That is
  Reject 3's accepted cost at its largest, and it is why a rebuild is a
  temporary state rather than a standing one.

### A failed rebuild is a new generation, never a reset

[[D-130]] refuses a `Resume`, `Retry` or `Clear` for a halt, and phase 3's
absent-surface table refuses `Reset`, `SetCheckpoint` and `Rewind`. So a
generation is created and never reset: a failed rebuild is generation `n+1` and
the failed `n`'s rows are dropped by an operator. That is coherent and it must be
written down, because "just reset it and try again" is what everybody will reach
for.

### [[D-131]] versus blue/green — the one hard conflict, decided

Marten says skipping unknown event types *"is important for 'blue/green'
deployment of system changes where a new application version introduces an
entirely new event type"*. vv's posture is the opposite: `ErrUnrouted` is
permanent ([[D-131]]) and a covered family's unclaimed type **halts**. During any
generation switch the old generation meets the new one's event types and halts.

**Decision: [[D-131]] stands, and the escape is a deployment ordering
requirement rather than a code change.** `projection.Ignore(router, family,
types...)` takes a **name**, deliberately, *"so a type this build declares no fact
for can be named too"* (`router.go:85-93`). So the ordering is:

1. deploy the **old** build with `Ignore(router, "orders", "orders.RefundIssued")`
   for every type the next build will introduce;
2. then deploy the new build that declares and routes them.

This is exactly the "expand then migrate" discipline every schema change already
needs, it costs one line per new type in one release, and it fails in the safe
direction — a forgotten `Ignore` halts loudly instead of dropping an event
silently, which is the trade [[D-131]] exists to make. It goes in writing beside
[[D-131]] and on the module page's blue/green section, and §UC-166 is the
case.

---

## 1.6 How a handler declares itself an effect, and what stops a rebuild from staging

**The question.** ES-06 part 3: *«отдельные projection/effect handlers; rebuild
не получает effect-dispatch capability. Начальный backfill тоже имеет явную
effect policy. При переключении поколения durable граница владения live effects
не допускает двух отправителей. Это не sandbox: произвольный HTTP внутри
пользовательского projection callback запрещается его контрактом и проверяется
тестом, не блокируется магией.»*

### It declares itself by being a different type in a different field

Not a flag, not an annotation, not a mode, not a `ReplayStatus` parameter. Axon's
default is that handlers **are** replayed unless annotated, which is why its
guide has to carry a warning about sending emails; Marten's default is
suppression with a named opt-in (`EnableSideEffectsOnInlineProjections`). **A
framework choosing today should choose Marten's**, and vv goes one step further:
the capability is not a default at all, it is a value.

```go
// What a projection handler is handed. It carries no way to reach an effect.
type Batch struct { … }

// What an effect handler is handed, and the value that IS the capability.
type Effect struct {
	Identity  Identity
	Envelopes []event.Envelope  // applied, past the barrier, and no others
	Attempt   int
}

// Stage, and not Send. The method runs inside the unit that commits the advance,
// so what it does must be a DURABLE WRITE that rolls back with it — a staged job
// ([[D-118]]), a row in your own tables. It must not perform an externally
// visible irreversible action: an HTTP call, a payment, a mail, a broker publish
// that is not itself transactional.
type Effects interface {
	Stage(ctx context.Context, effect Effect) error
}
```

**The method is called `Stage` because `Dispatch` is a lie about where it runs.**
Every rollback path this document already enumerates — a lost fence ([[D-133]],
expected on every rolling deploy), `ErrParkFull`, a later envelope's permanent
failure, a serialisation failure the caller's `Unit` retries — leaves a *sent*
call and an *unadvanced* checkpoint, and the next pass sends it again. A name that
invites a `POST` into that position is a contract defect, not a documentation
one, and the type is where a consumer reads the rule (§1.1 routes them here, and
routes the dial-out to the drain of the stage instead).

`Spec.Handler` is required and `Spec.Effects` is optional. **A rebuild is a spec
with `Effects` nil**, which is the whole of "a rebuild does not get the
effect-dispatch capability": there is nothing to withhold because there is
nothing to hold — for the generation spelling, which is the one the gate covers
(§1.5).

### The four things that stop a rebuild staging, in order of strength

**1. `Effects` is refused beside `AfterApply`.** EVENTSOURCE_REFERENCE §Reject 2:
*"an `AfterApply` pass that stages a job outside a unit has exactly the
reference's window"* — the broker outage that advances the checkpoint and loses
the event permanently. So the capability is `InUnit`-only, `New` refuses the
combination, and that is a contract rather than a caveat.

**2. The ownership row, read inside the committing transaction.** Before
staging, and only when there is something to stage, the pass asks
`Generations.Active(ctx, projection)` **through the checkpoint store's ambient
transaction** — the one the advance was already claimed in ([[D-133]]'s
claim-before-handler order). If it does not answer this generation, nothing is
staged and the pass is otherwise unchanged.

This is the durable ownership boundary the appendix asks for and **neither source
provides**. Marten names the two-sender window and calls closing it "a separate
concern". vv gives it a mechanism: one row, one writer, and the read of that row
made inside the transaction that commits the advance.

**Which read closes the window, stated exactly, because the mechanism alone does
not.** [[D-126]] forbids this framework choosing an isolation level, and at
`READ COMMITTED` — PostgreSQL's default and what every shipped example runs at —
a plain `SELECT active …` and a concurrent `Activate` of the same row do not
conflict: **both commit**, and a pass that read `1`, staged, and committed after
the row moved to `2` has staged beside the generation the row now names.
`REPEATABLE READ` does not close it either; a snapshot read of a row another
transaction updated raises nothing, and only `SERIALIZABLE` would.

So the boundary is an **obligation on the `Generations` implementation**, stated
in the interface's own doc comment the way `Park`'s ordering obligations are:
`Active` must be a **locking** read — `SELECT active … FOR SHARE` / `FOR KEY
SHARE` — or run in a `SERIALIZABLE` unit. Under a locking read the cutover's
`UPDATE` waits behind every unit that has read the row and not yet committed, so
the generation that staged is the generation that owned the row for the whole of
its unit. Driven against PostgreSQL 17.9, both ways, and §UC-202 is the pair of
recipes measured against each other.

**And what the boundary is not, under either read.** It does not promise that an
envelope the retiring generation staged is one the arriving generation will not
stage when it reaches it. The retiring generation goes on advancing past the
barrier the cutover was observed at, and every envelope in that overlap is one
the arriving generation has not applied yet and will stage when it does. That is
Marten's accepted overlap window in §1.5's terms, and the remedy is §1.5's:
**drain or stop the retiring generation before — or as — the switch commits.**
The ownership row closes the window between a read and a commit; the drain closes
the window between two generations' positions, and neither closes the other.

Consequences that must be stated:

- `Generations` is **required** beside `Effects` whenever
  `Spec.Generation != Ungenerated`, and optional otherwise: a projection that has
  no generations has one sender, and the fence already ensures that. §1.5 states
  what that leaves uncovered — a rebuild spelled as a second `Spec.Name` — and why
  requiring the row unconditionally would not have covered it either.

  **What is required and what the suppressor is gated on are two different
  questions, and only the first is answered by `Spec.Generation`.** The
  suppressor runs whenever a `Generations` is **supplied** — including at
  `Ungenerated`, where the row answers `Ungenerated`, which is the projection's
  own generation, so it stages. Gated on `Spec.Generation != Ungenerated`
  instead, it never runs for any projection that exists today, and the first
  `Cutover(From: Ungenerated, To: 2)` — the only migration a running deployment
  has — leaves the retiring projection staging beside the arriving one for ever.
  §UC-193 is that case, with the no-`Generations` control that measures the
  limit.
- The ownership row must live in the checkpoint store's database. Under tier A
  (§1.1) that is the read model's database too and everything is one
  transaction; under `Unchecked` it is not, and the application's read path must
  resolve a generation from a database it is not reading from. Stated, not
  hidden.
- **The alignment is not measured, and this is the one place this phase asserts
  one after §1.1 argued against asserting.** The difference is what is in hand.
  `Spec.Destination` is a value the framework *resolves* — `crud.ExecutorFor`
  answers an executor and `crud.KeyOf` answers its identity — so a comparison
  exists and declining to make it would be declining to look at something already
  held. `Generations` and `Park` are interfaces the application implements; the
  framework holds a method set and no resource. Any `AmIInYourTransaction()`
  method would be answered by the same code that is wrong, and a self-report is
  not a measurement.

  What is available instead is measurement **by consequence**, and it is exact: a
  `Generations` or a `Park` reached over a second pool does not roll back with the
  unit. So the falsifiers are rollback assertions rather than identity
  comparisons — §UC-149 for the park (an injected rollback leaves no letter),
  §UC-192 for the ownership row (a `Generations` over a second pool leaves the
  retired generation staging after a cutover, asserted as the boundary). Both run
  against PostgreSQL, and the recipe that passes them is the documented one:
  resolve through the context's ambient transaction.

**3. The barrier, and it is durable.** Marten's `GateSideEffectsBehindPriorVersion`
is the actually-hard part and the study's ES-06 nuance 1 is why: *replay-ness
must be durable*. An in-memory "we are rebuilding" flag fires every side effect
of the remaining history the first time the process restarts, and that failure
only shows up in production. Axon puts it in the token; Marten puts it in the
comparison `own progress < N` against a persisted mark.

vv has **one** of the two numbers durably, and says which:

```go
// Suppressed for every envelope at or below it. The ENVELOPE'S side of the
// comparison is a checkpoint column — it is where the resumed cursor left this
// generation — so an interrupted warm-up resumes suppressed rather than
// re-firing from the beginning. THIS side is a constant the deployment holds.
Spec.EffectsAfter event.Position
```

**The half that is not durable, named rather than implied.** Nothing records the
barrier a generation was warmed up under, nothing derives it from the prior
generation's mark the way Marten's `GateSideEffectsBehindPriorVersion` snapshots
`N`, and nothing refuses a restart that names a lower one. vv's shape therefore
moves the source's failure from a **process restart** to a **configuration
change** — a real improvement, and not the same as closing it. A release that is
rolled back, a config map that lost a key, or a second generation built by a spec
builder whose default is zero re-stages an effect for every envelope of the
warm-up still below the barrier, on the first pass, with no error and no record.
§UC-203 is that input, measured, with the field-still-set control beside it.

**Where an operator gets `N`:** it is the prior generation's mark, which
`Observe` answers as `Barrier.At`. Reading it there and writing it into the next
generation's `Spec.EffectsAfter` is a deployment-time act — a human reading a
barrier and writing a constant — and it is the only shipped source of the number.
That is the one use of `Barrier.At` beside the `>=` comparison, and its doc
comment says so rather than forbidding it.

Four behaviours, all four from the source and all four load-bearing:

- **Resume after interruption.** The trigger is *own progress `< N`*, not *own
  progress `== 0`*. vv gets this for free: the comparison is per envelope
  against a stored barrier, so a warm-up interrupted at `M < N` resumes
  suppressed over `(M, N]`.
- **The split is per envelope, not per page.** A page straddling `N` would
  otherwise either double-fire or lose the first real effects. `Effect.Envelopes`
  is the subset the handler applied with `Position > EffectsAfter`, and a page
  with no such envelope stages nothing at all (`Stage` is not called with an empty
  slice).
- **A failed warm-up must not become continuous execution.** In vv a failing pass
  never advances, so the loop cannot pass the barrier — except by *parking*. A
  spec with `EffectsAfter > 0` and `OnPermanentFailure: ParkSequence` is therefore
  **refused at construction**: a warm-up that parked an event produces a
  generation whose rows are not comparable to the live one, and the cutover's
  only evidence is the comparison. It is a refusal and not a silent policy
  switch.
- **No-op when not needed.** `EffectsAfter == 0` is no gate. `EffectsAfter` set
  with `Effects` nil is `ErrSpec` — a barrier with nothing to gate is a spec
  assembled wrong.

**4. The contract and the test, because it is not a sandbox.** The appendix is
explicit and correct: Go cannot prevent a handler from dialling out, and this
framework does not pretend to.

- **The contract is on the type.** A `Handler` writes to the read model through
  the context it is given and does nothing else — no network, no message send, no
  decision that depends on a clock. `Effects` is where the other thing goes, and
  it has its own type, its own field, its own gate and its own delivery identity.
- **The test is over the framework's own code.** A source and import walk proving
  that no package on the projection path reaches anything that can dispatch —
  `net`, `net/http`, `net/smtp`, `os/exec` — transitively, with a **control** that
  the same walk reports such an import when a fixture adds one, so a walk that
  resolved nothing is not read as a clean tree. `scripts/` already owns this class
  of check (`costsNoMoreThanItNames`, `startsNothing`).
- **And the honest sentence, on the module page:** the framework makes the honest
  thing the easy thing and the dishonest thing visible in review. It does not make
  it impossible, and a page that claimed otherwise would be worse than one that
  does not.

### Where the effect goes, and what it may not be

[[D-130]] forbids importing `jobs` from `event/projection`, so the sink is an
interface the **application** implements with the composition root wiring
`jobs.Stager` behind it — exactly the shape `Park` and `Quarantines` have. And
[[D-118]] forbids a second durable-intent table: an effect handler that stages
inside the `InUnit` transaction is compliant; one that keeps its own "effects to
send" table is not.

EVENTSOURCE_REFERENCE §Reject 2 records what must still be answered in its place,
and ES-06 inherits all three: **the ordering guarantee (events of one aggregate
reach the sink in version order), the retry, and the dead-letter behaviour are
questions `jobs` must answer for a staged integration event, not questions that
disappear.** Phase 4 states them on the module page as the consumer's, and does
not answer them here.

### The initial backfill has the same policy, because it is the same case

*«Начальный backfill тоже имеет явную effect policy.»* A **new** projection over a
full log is a backfill, and `Checkpoint.Fresh()` is where it starts. It gets the
same two spellings and nothing special: `Effects: nil` to backfill silently, or
`EffectsAfter: P` to backfill suppressed up to `P` and stage past it. There is
no third mode and no "initial" flag, which is what keeps a backfill and a rebuild
from drifting into two mechanisms.

### The park and the rebuild, resolved by construction

Axon's `DeadLetteringEventHandlerInvoker.performReset` clears the queue on a
reset, conditionally on `allowReset`, and neither appendix mentions the
interaction. Keeping the letters is a lie about the new run; clearing them
silently loses the operator's only record of what was skipped.

**vv has neither problem, because a generation is a name and the park is keyed by
it.** The old generation's letters stay parked under the old name — the operator's
record, still true — and the new generation starts with an empty park, because
nothing was ever parked under its name. Both records exist and neither is a lie.
When the old generation is dropped, its letters are dropped with it, by the same
operator operation. This is strictly better than either source and it costs one
sentence.

### An effect follows its envelope, including into the park

The interaction the appendices do not ask about and that has no defensible
default: a page in which `A2` failed permanently and `A3` was parked behind it
without reaching the handler still *contains* `A2` and `A3`. Both readings of
"the page's envelopes" are production defects.

- **If a parked envelope's effect is staged**, the framework tells a customer
  about an order state its own read model refused to apply. That is ES-06's
  purpose — *«replay не повторяет письма и платежи»* — inverted: the effect fires
  for an event nothing applied, and fires again if a redrive later applies it.
- **If it is silently dropped**, the effect is lost for ever: the loop advanced
  over those positions and will never see them again, and a permanently-failing
  park quietly drops an unbounded set of live effects with a *read-model*
  completeness counter as the only trace.

**So an effect is not a property of a page. It is a property of an applied
envelope, and it goes wherever the envelope goes.**

- `Effect.Envelopes` is the envelopes of this page the handler **applied**, past
  the barrier. A parked envelope is not among them — on **either** applier: the
  isolation pass that parks the envelope whose handler failed, and the blocking
  pass a degraded projection runs on every later page, which parks an envelope
  of a held sequence *without calling the handler at all* (§UC-147).
- A letter carries its effect into the park with it. `RedriveSpec` therefore
  carries `Effects` and `Generations` — checked the same way the loop checks
  them — and a redrive that applies a letter stages that letter's effect in the
  same transaction as the apply and the `Evict`.
- **A `RedriveSpec` carries `EffectsAfter` to refuse it, and that is the one
  place the two doors differ.** A letter is in the queue because a loop parked
  it; a loop parks only under `OnPermanentFailure: ParkSequence`; and `New`
  refuses that beside a barrier. So every letter a redrive can ever drain was
  parked by a generation that had **no** barrier and is owed its effect, and the
  only thing a barrier can do on this side is suppress that effect **for ever** —
  the loop advanced over the position and will never read it again, which is the
  second of the two failures this section enumerates. `NewRedrive` refuses a
  non-zero value naming that rule; the field exists rather than being absent
  because the spec builder that serves a loop and a redrive is exactly the shape
  that copies it across, and a refusal at that call site says why while a missing
  field says nothing.
- **An eviction stages nothing, ever.** A skip is the operator saying "this will
  never be applied", and an effect for an event that will never be applied is the
  first reading above. The eviction record and `Progress.Quarantined` are its
  trace, and the module page says out loud that a skipped event's effect is not
  sent.

§INV-101 states the gate over both ends, and both ends are now defined.

### Which suppressor suppressed, and in what order

Three things stop a stage, they are checked in this order, and the order is
cheapest-first for a reason a rebuild pays every page:

1. **`Effects` is nil.** Nothing is called, nothing is read, no round trip
   (§UC-174). This is the rebuild's answer and it costs nothing.
2. **`EffectsAfter`**, per envelope, against a stored position. A page with no
   applied envelope past the barrier stops here, still without a round trip.
3. **The ownership row**, once per pass and only when 1 and 2 left something to
   stage, inside the transaction that commits the advance.

A suppression is never an error and never a phase: it is the normal, expected
state of every rebuild and of every retired generation, and a `State` field
reporting "I did not stage" would be published continuously by healthy
projections. What an operator has instead is the ownership row itself — their own
table, one row, answering which generation owns the effects — and a recording
sink in tests, which is what tells the three apart in §UC-169.

---

# 2. Scope and non-goals

## In scope

**ES-01 (residual).**
- The alignment comparison in `checkUnit`: tier B refused, tier A proved (§1.1).
- The foreign-destination contract, stated as four promises and one refusal,
  with a use case and an invariant each.

**ES-02.**
- `Sequencer`, `ByStream`, `Unordered`, `OneSequence`, `SequenceBy`.
- `Partition` as a mask, `Split`, `Matches`, `Whole`, `MaxPartitions`, the
  published hash.
- `Cover` and `NewCover` — the declared set, refused when it leaves a gap or an
  overlap, and the value every set-shaped answer is computed over.
- `Identity` — the one place a recorded name is built and parsed, opaque so that
  a name carrying a delimiter cannot be assembled.
- Per-partition filtering in the loop; the empty-kept-set page that still
  advances.
- The five-step handoff, expressed entirely in the shipped `Checkpoints` surface
  and made through the `event.Track` door.

**ES-03.**
- `Park`, `Letter`, `ErrParkFull`, the blocking test, the zero-cost fast path,
  `Holes`.
- `Redriver` with its claim, `Redrive`, `Retried` — one sequence per call, in
  order, rotating, and claimed so two operators do not collide.
- `PhaseDegraded`, `PhaseBlocked`, `State.Parked`.
- `Quarantines` → `Park`; `Quarantined` (struct) → `Letter`; `Failure`'s
  `Quarantine` → `ParkSequence`.
- The tier-A construction refusal that the causal-order guarantee rests on.

**ES-04.**
- `Generation`, `Ungenerated`, `Generations`.
- `Barrier`, `Observe`, `Reached`, `Readiness`, `Cutover`, `CutoverSpec` — with
  the barrier derived by `Cutover` rather than supplied to it.
- Rollback as the same call exchanged, with `ErrRetired`.
- `Spec.Pace`.

**ES-06.**
- `Effect`, `Effects` (`Stage`), `EffectsFunc`, `Spec.Effects`,
  `Spec.EffectsAfter`, `Spec.Generations`; `RedriveSpec.Effects` and
  `RedriveSpec.Generations` checked the same way, and `RedriveSpec.EffectsAfter`
  **refused** (§1.6).
- The ownership read inside the committing transaction.
- The staging-only contract, and the import walk with its control.

**Cross-cutting.**
- Two decisions: one for the partition/topology law, one for the effect
  capability and its gate. §7.2.
- Module pages, flows, `docs/api/surface.md` regenerated, and the live suite.

## Non-goals — named, not specified

- **ES-05, ES-07, ES-08, ES-09.** Phase 5. In particular there is no
  `projection.Wait`, no store-issued barrier token, no operation receipt, no
  `history.AtVersion` and no snapshot. §1.5 shows ES-04's readiness does **not**
  need ES-05, which was the study's third open question.
- **A merge.** §1.3, with both reasons.
- **A shared reader fanned out to N routers.** Reject 3's eventual answer;
  phase 4 states the N× cost and does not foreclose it.
- **A filter parameter on `Log.ReadAll`.** Reject 3, refused.
- **A library-started thread pool, coordinator or agent scheduler.** Reject 1,
  [[D-092]], `startsNothing`.
- **A second durable-intent table.** [[D-118]]. A parked letter is not a durable
  *intent* and is therefore permitted; an "effects to send" table is not.
- **A time-based claim, a `claimTimeout`, or a clock in a store.** [[D-126]].
  vv's fence is ordinal and needs none; the letter-claim clock is the
  application's, in the application's table.
- **A `Position → Cursor` function, or any ordering of two cursors.** [[D-129]].
  This is what refuses a merge and what makes the barrier a `Position`.
- **`Reset`, `Rewind`, `SetCheckpoint`, `Resume`, `Retry`, `Clear`.** Phase 3's
  absent-surface table and [[D-130]]. A failed rebuild is a new generation.
- **Skipping unknown event types.** [[D-131]] stands; the escape is `Ignore` in
  the previous release (§1.5).
- **An applied position.** §1.4, refused with its reason.
- **Sandboxing a handler.** §1.6. Contract and test, not magic.
- **DDL, table names, or any knowledge of the read model's shape.** The
  framework never learns a table name.

---

# 3. Use cases

Format is phase 2's and phase 3's: **Given**, **Then**, **Must not**,
**Control**. Numbers are allocated in order within each group, and are
append-only: the cases added when the round-1 audit was closed (UC-179 onward)
carry the next free numbers and sit beside the case they qualify rather than at
the end, so a number is out of order where a subject would otherwise be split.

## Group AA — one transaction authority (ES-01)

#### UC-131 The checkpoint store and the destination are one transaction  [happy]
- **Given** `Advance: InUnit`, a `Unit` opening one `crud` transaction over the
  read model's source, an `eventpg` checkpoint store on that same source, and
  `Destination` naming it.
- **Then** Inside the unit and before the handler runs, the tracker's authority
  and the authority built from `crud.KeyOf` of the destination's executor compare
  `Same`; the pass proceeds; the handler's rows and the advance commit together;
  a rollback leaves neither.
- **Must not** The comparison must not be made outside the unit, must not be
  made once and cached across passes, and must not reach the classifier if it
  fails — it is a wiring refusal ([[D-130]]).
- **Control** The same wiring with `AfterApply` makes no comparison at all, so
  the check is `InUnit`'s and not a new universal requirement.

#### UC-132 A unit opens two transactions, one per resource  [edge]
- **Given** `Advance: InUnit` and a `Unit` that opens a transaction on the
  checkpoint store's source **and a second one** on the read model's source,
  binding both, with `Destination` naming the second.
- **Then** Both are transactions and both resolve, so every check that ships
  today passes — and the alignment comparison finds two different `*sql.Tx`
  values, refuses with `ErrSpec` naming both resources, and halts before the
  handler runs. Nothing was applied and the advance did not move.
- **Must not** It must not be accepted, must not be downgraded to `AfterApply`,
  and must not be reported as a handler failure.
- **Control** The identical composition with **one** transaction bound for both
  is accepted and drains the log, so the refusal is discriminating rather than
  universal. This pair is the whole of ES-01's residual work.

#### UC-133 The destination cannot be resolved through `crud` at all  [edge]
- **Given** `Destination: projection.Unchecked` beside a checkpoint store that
  is transactional — the `_examples/event-checkpoints-elsewhere` wiring.
- **Then** Accepted. No comparison is attempted. The advance rides in the
  checkpoint database's transaction; the handler writes to a second pool; the
  handler's upsert on `(Stream, Version)` is what closes the window that opens.
- **Must not** `Unchecked` must not be reachable by leaving `Destination` zero,
  and the promise must not be stated without its precondition in the same breath.
- **Control** A kill between the handler's commit and the unit's leaves the read
  model's rows in place while the advance rolls back — `AfterApply` semantics
  under an `InUnit` spec, which is the cost the value's name exists to make
  visible.

#### UC-134 A handler reaches a foreign resource and the framework's promise is read back  [edge]
- **Given** A handler writing to a second database or an HTTP endpoint under
  `Unchecked`, and a process killed at N random points across a drain of a
  known log.
- **Then** Every committed event reached the handler at least once; the order
  within each stream is the stream's; the resume point skipped nothing; and every
  delivery carried a `(Stream, Version)` that is unique across the whole run.
  Nothing else is asserted about the foreign resource, deliberately.
- **Must not** The suite must not assert that the foreign effect happened once —
  that is the promise §1.1 refuses, and a test asserting it would freeze a
  guarantee the framework does not make.
- **Control** The same run under tier A asserts the effect **is** present exactly
  once, so the two tiers are told apart by measurement rather than by prose.

## Group AB — sequences and partitions (ES-02)

#### UC-135 Four partitions drain one log and each key stays in order  [happy]
- **Given** One log, `Sequence: ByStream()`, four runners at
  `Partition{id: 0..3, mask: 3}`, one destination table, and a log in which each
  of many streams has several events.
- **Then** Every event is applied exactly once across the four; the events of any
  one stream are applied in that stream's order; the four checkpoints advance
  independently; and no two partitions ever see one envelope.
- **Must not** No envelope may be applied by two partitions, and no partition may
  apply an envelope whose key does not match it.
- **Control** The same four runners with the sequencer replaced by one that
  answers a **fresh** key per call (the wrongness §1.2 refuses) leaves events
  applied twice or not at all — the control asserts the breakage exists, so a
  passing positive case is known to be proving something.

#### UC-136 A page none of whose envelopes belong to this partition  [edge]
- **Given** A partition whose predicate matches nothing in the page just read.
- **Then** The checkpoint advances once for that page — `Highest` is the page's
  last position, `Applied` rises by zero — and the handler is not called at all.
- **Must not** It must not re-read the same page for ever, must not call the
  handler with an empty batch, and must not skip the save (which would make a
  partition re-walk the whole log at every restart).
- **Control** A partition that matched every envelope of the same page advances
  by the same cursor with `Applied` risen by the page's length, so the two ends
  agree on the cursor and differ only in what was applied.

#### UC-137 `Unordered` and `OneSequence` are told apart  [happy]
- **Given** One log and two projections: one with `Unordered()` over four
  partitions, one with `OneSequence()` over four partitions.
- **Then** `Unordered` spreads the log across all four; `OneSequence` puts every
  envelope in one partition and leaves the other three applying nothing while
  still advancing their checkpoints (§UC-136).
- **Must not** `Unordered` must not answer a random or process-local key — the
  same envelope re-read after a restart must land in the same partition.
- **Control** A restart mid-drain under `Unordered` re-delivers the surviving
  page to the **same** partition, asserted by the destination rows rather than by
  the key.

#### UC-138 A partition is split, and every key lands in exactly one child  [happy]
- **Given** A drained partition `{id: 1, mask: 1}` at a known cursor with known
  counters, and `Split` run inside a `crud.InNewTx` over the checkpoint store's
  source.
- **Then** In one transaction: the parent's row and both children's are read
  through `event.Track`, two rows are written at advance 1 carrying the parent's
  cursor byte for byte, and the parent's row is removed. The children are
  `{1, 3}` and `{3, 3}`; `{0, 1}` and every key in it is untouched; the
  lower-numbered child inherits `Applied` and `Quarantined` and the higher starts
  both at zero, so the sum across the set is unchanged.
- **Must not** No key that was in `{0, 1}` may move. No child may start from the
  origin or from any cursor but the parent's. The three writes must not be three
  transactions, and none of them may reach the store other than through the
  tracker door ([[D-129]]).
- **Control** A `hash % N` re-partitioning of the same log, run as a fixture,
  moves roughly `N/(N+1)` of the keys and delivers `OrderPaid` before
  `OrderCreated` — asserted, so the mask's whole value is measured rather than
  believed.

#### UC-139 A split is attempted while the parent is still running  [edge]
- **Given** A running parent partition and a `Split` committed under it.
- **Then** The parent's next save finds no row at its advance, is refused, and
  the parent **halts** — [[D-133]]'s "absent row" arm, unchanged. `Ready` reports
  it. The two children run and cover the parent's whole key space.
- **Must not** The parent must not create a fresh row at advance 1 and continue
  beside its children, and the split must not be reported as contention
  (`ErrOvertaken`), which does not halt.
- **Control** A split of a **drained** parent produces no halt anywhere, so the
  halt is attributable to the missed drain rather than to `Split`.

#### UC-140 A split of a partition with no checkpoint row  [edge]
- **Given** A partition whose row is absent, reached twice: once because it has
  genuinely never run, once because it stood at position 5 000 000 and its row was
  forgotten, dropped or lost to a partial restore.
- **Then** `Split` refuses **both** with `ErrTopology` and writes nothing. The
  message names the two readings and their two remedies: a partition that never
  ran needs no split — declare the `Cover` and start its members — and a partition
  whose row was lost is [[D-133]]'s restore case. The framework cannot tell them
  apart from the rows, and guessing "fresh" would start two children at the origin
  against a live read model.
- **Must not** It must not answer the two children and write nothing, which is
  the arm that made the dangerous reading indistinguishable from the safe one. It
  must not write a row at advance 1 carrying the empty cursor.
- **Control** A split of a partition that *has* run writes exactly three
  statements, counted, and answers its children.

#### UC-141 A split at the published ceiling  [edge]
- **Given** A partition whose mask is `MaxPartitions - 1`.
- **Then** `Split` refuses with `ErrTopology`, naming the ceiling, and writes
  nothing.
- **Must not** The mask must not wrap, and a partition beyond the ceiling must
  not be constructible by any exported route.
- **Control** A split one below the ceiling succeeds.

#### UC-185 A split runs under a `Unit` that runs its body twice  [edge]
- **Given** A `Unit` that executes its work function twice — once rolled back and
  once committed, which [[D-130]] says a caller's may do — around one `Split`.
- **Then** The rolled-back run leaves the three rows exactly as it found them, so
  the second run reads the same parent and does the same thing; the result is one
  split. A run that follows a **committed** one finds the parent absent and the
  children present and is refused with `ErrTopology`, naming the children it
  found.
- **Must not** `Split` must not carry a decision between runs, must not count
  attempts, and must not take the fresh arm on its second run — there is no fresh
  arm (§UC-140).
- **Control** One run alone writes exactly three statements, so the arity
  behaviour is attributable to the second run rather than to the shape.

#### UC-186 A declared partition set leaves a gap  [edge]
- **Given** `NewCover({0,3}, {1,3}, {3,3})` — `{2,3}` forgotten.
- **Then** It is refused, naming the uncovered quarter of the key space. No
  `Observe`, `Reached` or `Cutover` can be reached with that set, because none of
  them takes a `[]Partition`.
- **Must not** The framework must not answer a `min` over an incomplete set, which
  reads as healthy while a quarter of the log is never delivered to anything.
- **Control** `NewCover({0,3}, {1,3}, {2,3}, {3,3})` is admitted and its four
  runners apply every event of a known log exactly once.

#### UC-187 A declared partition set overlaps  [edge]
- **Given** `NewCover({1,3}, {1,7})` beside the members that would otherwise
  complete the space.
- **Then** It is refused, naming the two members that both match a key. The pair
  is exactly the one nothing else catches: two different names, so
  `runtime.Supervisor` admits both, two checkpoint rows, so no fence ever meets,
  and every key matching both applied twice and out of order once their cursors
  diverge.
- **Must not** The refusal must not be by name comparison — `{1,3}` and `{1,7}`
  are different names and that is the point — but by the mask arithmetic
  `(idA ^ idB) & min(maskA, maskB) == 0`.
- **Control** `{0,3}, {1,3}, {2,3}, {3,7}, {7,7}` — the same space with one
  member split — is admitted, so the check discriminates rather than refusing
  every set whose masks differ.

#### UC-142 A merge is asked for  [edge]
- **Given** Two sibling partitions an operator wants to combine.
- **Then** There is no exported spelling. The documented path is a new generation
  at the target topology, rebuilt and cut over (§UC-158–UC-165).
- **Must not** No API may take two cursors, order two cursors, or answer a
  "lower" one. `scripts/projection_test.go`'s surface walk covers the new package
  surface for exactly that signature ([[D-129]]).
- **Control** The same walk must still find `event.Read`, which takes a cursor
  and answers a reader, so a walk that resolved nothing is not read as clean.

#### UC-143 A partitioned projection's aggregate progress is asked for  [happy]
- **Given** Four partitions at `Highest` 900, 1200, 1150 and 1300, declared as one
  `Cover`.
- **Then** `Observe` answers 900. Every event at or below 900 was delivered by
  the projection as a whole; nothing above it was.
- **Must not** Nothing may answer a `max`, a sum or an average; no doc may call
  any per-partition `Highest` the projection's progress; and `Observe` must not
  take a bare `[]Partition`, which is how a `min` over a hole is computed.
- **Control** One partition alone — `Whole()`, which is a `Cover` of one — answers
  its own number, so the `min` is not a constant.

#### UC-144 A sequencer panics  [edge]
- **Given** A `SequenceOf` that panics on one envelope.
- **Then** The projection **halts**, naming the sequencer, without calling the
  handler for that page. It is not recovered into a page failure and is not
  parked.
- **Must not** It must not be treated as a handler panic (`applyPage`'s recovery
  does not reach here), because an envelope that could not be assigned to a
  partition has no sequence to be parked in.
- **Control** A handler panic on the same envelope **is** recovered into a
  permanent page failure and reaches the configured policy, so the two panics are
  told apart.

#### UC-145 A partition name is built and parsed  [happy]
- **Given** `NewIdentity("orders", 2, Partition{3, 7})`.
- **Then** `String()` answers `orders@2#3.7`, `ParseIdentity` round-trips it, it
  passes `event.Track`'s `checkName` (within `MaxNameBytes`, valid UTF-8, no
  control character, no bracket), and it is what the checkpoint row and the runner
  name use; `Whole()` of it renders `orders@2` and is what the park uses.
- **Must not** A caller must not assemble the name by hand anywhere — `Identity`
  has no exported fields for exactly that reason — and the rendering must not use
  `[` or `]`.
- **Control** `NewIdentity("orders", Ungenerated, Whole())` renders `orders` —
  every projection that exists today keeps its name and its row.

#### UC-188 A projection is legally named `orders@2` today  [edge]
- **Given** A deployment whose live projection name is `orders@2`, which passes
  `event.Track`'s `checkName` today and always has.
- **Then** `NewIdentity` refuses it, naming the delimiter and the field, and `New`
  collects that refusal as `ErrSpec` at construction — at boot, before a single
  page is read. The message names the migration: the projection is renamed, which
  is a new checkpoint row and therefore a rebuild (§1.5), or the operator renames
  the row in their own store as they would for any rename.
- **Must not** The rendering must not escape the delimiter instead. `orders@2`
  would then render as `orders%402`, its checkpoint row key would change under a
  live projection, and it would resume from the origin against a live read model —
  a silent version of the failure the refusal makes loud.
- **Control** `orders.v2` is accepted: `.` is not a delimiter of the composition,
  because only `@` and `#` separate the parts and the partition's own `.` is
  reached after both.

#### UC-194 A partitioned runner is started beside a live coarser checkpoint row  [edge]
- **Given** `orders` has run unpartitioned to a known position and its checkpoint
  row is live. A release adds `Partition:` to the spec and starts `{0,1}` and
  `{1,1}` — or, one level down, `{0,3}` is started while `orders#0.1` is live.
- **Then** Each partitioned runner **halts at its first resume**, before it reads
  a page, with `ErrTopology` naming the coarser row it found and naming `Split`
  as the route that hands a parent's cursor to its children. Its own checkpoint
  row is never written, and no handler is called.
- **Must not** It must not adopt the coarser row's cursor, must not start at the
  origin beside it, and must not report the condition as contention
  (`ErrOvertaken`), which does not halt. The check must cost nothing for a
  projection that named no partition.
- **Control** Two of them, and both are needed: the same release after the
  handoff — both children at the parent's cursor, the parent retired — is
  admitted and applies nothing a second time; and a partition set declared on a
  projection that has **never** run is admitted and drains, so the refusal is
  about a live coarser row rather than about being partitioned.

#### UC-195 A `Cover` or an `Identity` nobody built reaches a door  [edge]
- **Given** `projection.Cover{}` and `projection.Identity{}` — legal composite
  literals in any package, because neither type has an exported field.
- **Then** Every door of this phase that takes one refuses it: a `Cover` whose
  `Count()` is zero with `ErrTopology`, an `Identity` whose `Projection()` is
  empty with `ErrSpec`, each naming the field. The two predicates are exact
  discriminators rather than heuristics — the zero value is the only value
  reachable without the constructor, and it is the one value the constructor
  never answers beside a nil error, so no marker field is carried.
- **Must not** No aggregate may be folded over a set nobody checked: a `min` over
  zero members is either the origin or `MaxUint64`, and the second makes every
  barrier trivially reached and every cutover proceed on no evidence. No park row,
  redrive or observation may be keyed by a name nobody built.
- **Control** The same doors admit the values `NewCover` and `NewIdentity`
  answered, and a source walk asserts no exported function takes either as a
  parameter without asking — vacuous while no such door exists, and red on the
  first one that lands without the question.

#### UC-196 A name the kernel's identifier rule refuses is handed to `NewIdentity`  [edge]
- **Given** `NewIdentity("orders]", …)`, `NewIdentity("a\x00b", …)`, an invalid
  UTF-8 name, an empty one, and one past `event.MaxNameBytes`.
- **Then** Each is refused with `ErrSpec` naming the field, at the constructor —
  not at `event.Track`, which an `Identity` reaches only on the `Spec` path and
  never from a parked letter, a redrive, an observation or a cutover.
- **Must not** The rule must not be delegated to a door those four do not cross,
  and it must not be *stricter* than the kernel's either: a name of two spaces is
  one the kernel takes, because the kernel does no trimming and no case folding,
  and this constructor takes it too. The two deliberate additions are `@` and `#`
  (§UC-188).
- **Control** A test walks both spellings over one table and over every byte a
  name can carry, and reports a name only one of them takes; `orders.v2`,
  `orders/paid` and a non-ASCII name are admitted by both.

## Group AC — the park (ES-03)

#### UC-146 A poison event parks its sequence and the following events of it  [happy]
- **Given** A page holding, in position order, `A1 A2 B1 A3 B2`, where `A2`
  fails permanently, `OnPermanentFailure: ParkSequence`, `Sequence: ByStream()`,
  and the tier A wiring `ParkSequence` requires.
- **Then** `A1` is applied. `A2` is parked with its cause. `A3` is parked
  **without ever reaching the handler**, with a nil cause. `B1` and `B2` are
  applied. The checkpoint advances once for the page, `Quarantined` rises by two,
  and `State.Parked` is 1.
- **Must not** `A3` must not reach the handler and must not be applied — that is
  the failure the shipped isolation pass has and the whole reason ES-03 exists.
- **Control** The same page with `A2` succeeding applies all five and parks
  nothing, so the blocking is attributable to the failure.

#### UC-147 A later page meets a sequence that is already parked  [happy]
- **Given** The park holding sequence `A`, and a later page holding `A4 C1`.
- **Then** `A4` is parked without reaching the handler; `C1` is applied. One
  `Holds` call per envelope, made inside the unit. With `Effects` set, `Stage`
  takes `C1` and not `A4` — an effect is owed for what the handler applied, and
  the handler was never called with `A4` (§UC-181, §INV-106).
- **Must not** The blocking test must not be made outside the unit under
  `InUnit`, where the park write and the advance are one commit. And the effect
  rule must not be pinned on the isolation pass alone: this is the applier a
  degraded projection runs on **every** page until the queue is drained, and the
  two appliers hold the owed envelopes in two different variables.
- **Control** With the park empty, `Holds` is not called at all for that page
  (§UC-148).

#### UC-148 A healthy projection pays nothing for the park  [happy]
- **Given** `Spec.Park` set and no sequence ever parked.
- **Then** `Park.Sequences` is called exactly once per resume — not once per pass
  — and `Park.Holds` is called **zero** times over a full drain, counted on a
  recording `Park`.
- **Must not** The fast path must not be a cache with an eviction policy, a TTL
  or a size — the count is exact and durable, which is what makes it safe.
- **Control** After one park, `Sequences` is called once per **pass** and `Holds`
  once per matching envelope for the rest of the drain; after a redrive empties
  the queue, the next pass reads zero and both counts return to the healthy ones.
  That is the one clearing rule (§1.4), measured at both ends.

#### UC-149 A second instance parks while this one holds a zero count  [edge]
- **Given** Two live instances of one identity, one of which parks a sequence.
- **Then** Under `InUnit` the loser's whole unit rolls back, including its park
  write, so no letter exists that the winner does not know about. On an
  `overtaken` the survivor re-reads `Sequences` from the row it adopted and picks
  the count up.
- **Must not** The in-memory count must not survive an `overtaken` unchanged —
  that is the one path where another writer's park is real.
- **Control** One instance alone never re-reads the count within a run.

#### UC-150 The park is full  [edge]
- **Given** A `Park` whose `Park` answers `ErrParkFull` — once because the
  sequence holds `MaxSequenceLetters`, once because the queue holds
  `MaxSequences` and the sequence is new, once because the byte bound is reached.
- **Then** All three end the pass identically: the unit rolls back — there is
  always a unit, because `ParkSequence` constructs only at tier A (§UC-190) — the
  checkpoint does not advance, nothing is parked including the letters earlier
  envelopes of the same page parked successfully, **no envelope is skipped**,
  `State.Phase` is `PhaseBlocked`, the retry has no attempt budget, and `Ready`
  fails once the accumulated backoff outlasts `Tolerate`.
- **Must not** It must not skip the envelope, must not halt (which would need a
  redeploy to clear a condition one `DELETE` clears), and must not consume an
  attempt.
- **Control** An operator evicting one letter lets the very next pass advance,
  with no restart — which is what distinguishes a block from a halt.

#### UC-151 A queue with 1023 single-letter sequences accepts a 1024th letter  [edge]
- **Given** A `Park` at `MaxSequences = 1024` holding 1023 sequences of one
  letter each.
- **Then** A letter for an **existing** sequence is accepted; a letter for a
  **new** sequence is accepted (1024th); a letter for a second new sequence is
  `ErrParkFull`. The bound is `isFull(sequence)` and never `isFull()`.
- **Must not** A one-dimensional bound must not be specified, documented or
  implemented in the example.
- **Note, and it is what the arm can and cannot prove.** There is no `isFull` on
  the `Park` interface: the bound is the implementation's, so this is an
  **obligation on an implementation** and not a property of the framework, and an
  arm asserting the two dimensions of a queue asserts them of that queue. What is
  measurable here is the **cost** of getting it wrong, and it is measured: a
  projection over a queue at its sequence bound goes on parking the events behind
  a blocker it already holds, stays `PhaseDegraded` and keeps advancing, while
  the same projection over a queue that asked `isFull()` without the sequence
  sits in `PhaseBlocked` with the blocked sequence's own room unused and applies
  nothing at all.
- **Control** The same queue at `MaxSequenceLetters` refuses a letter for the
  full sequence while accepting one for another, so the two dimensions are told
  apart; and the one-dimensional queue above, driven through a running
  projection, so the difference is a phase an operator sees rather than an
  assertion about a fixture.

#### UC-152 A parked sequence is redriven and drains  [happy]
- **Given** A park holding `A2 A3 A4` for sequence `A`, and the cause fixed.
- **Then** `Redrive.Sequence(ctx, "A")` applies `A2`, evicts it, applies `A3`,
  evicts it, applies `A4`, evicts it, and answers `Retried{Applied: 3, Left: 0}`.
  The checkpoint is untouched. `State.Parked` drops to zero at the loop's next
  **pass** — the count is re-read every pass while it is non-zero — and the phase
  returns to `following`.
- **Must not** The redrive must not touch the checkpoint (the fence is the
  loop's), must not process the sequence out of insert order, and must not
  process a second sequence in one call.
- **Control** A redrive of a sequence that is not parked answers
  `Retried{Applied: 0}` and no error, so an empty redrive is not a failure.

#### UC-153 A redrive stops at the first letter that fails again  [edge]
- **Given** `A2 A3 A4` parked and `A3` still failing.
- **Then** `A2` is applied and evicted; `A3` is requeued with its new cause and
  its attempt raised; `A4` is **not** touched. `Retried{Applied: 1, Left: 2}` and
  the cause travel back.
- **Must not** It must not continue past `A3` — that is the causal-order
  guarantee the whole queue exists for, on the retry path.
- **Control** The same sequence with `A3` fixed drains fully (§UC-152).

#### UC-154 A redrive rotates rather than starving  [happy]
- **Given** Three parked sequences, one of them permanently failing, and
  `Redrive.Any` called repeatedly.
- **Then** Each call **claims** the least-recently-tried unclaimed sequence and
  releases it on every exit path, so the two fixable ones drain and the failing
  one is retried once per rotation rather than every call.
- **Must not** It must not always pick the same sequence (starvation) and must
  not pick randomly (which loses the fairness the rotation buys).
- **Control** `Redrive.Any` on a park holding one failing sequence returns the
  same sequence every time, so the rotation is visible only when there is
  something to rotate.

#### UC-191 Two operators redrive at once  [edge]
- **Given** Two concurrent `Redrive.Any` calls over one park holding sequences
  `A`, `B` and `C`, driven through a gate so the contention is caused rather than
  hoped for.
- **Then** Exactly one of them processes `A`: `Claim` admits one caller and
  answers the other a different sequence, or `found = false` when everything is
  claimed. No letter is applied twice and no sequence is processed out of order.
- **Must not** `Claim` must not be a read — an `Oldest` that only *selects* hands
  both callers the same letters, both apply from the first, and `A3` lands before
  `A2` finishes, which is the ordering the whole queue exists for, broken by its
  own recovery path.
- **Control** One caller alone behaves exactly as §UC-154 states, so the claim
  costs the single-operator case nothing.

#### UC-155 A redrive is attempted across a sequencer change  [edge]
- **Given** Letters parked under sequencer name `by-order`, and a `RedriveSpec`
  naming `by-customer`.
- **Then** `ErrTopology`, naming both, before any handler is called. A
  `RedriveSpec` whose `Identity` carries a partition is refused the same way: a
  park is keyed by `Identity.Whole()` and a redrive is generation-wide.
- **Must not** It must not apply a letter whose sequence was computed by another
  function — the ordering that letter's position encodes is not the ordering the
  new sequencer would give it.
- **Control** The same redrive with the matching name proceeds.

#### UC-156 A projection that has parked something does not report "caught up"  [edge]
- **Given** A projection that has read the whole log and holds two parked
  sequences.
- **Then** `State.Phase` is `PhaseDegraded`, `State.Parked` is 2,
  `Progress.Quarantined` is non-zero, and `Ready` **passes**.
- **Must not** It must not report `PhaseFollowing`, and `Ready` must not fail —
  one broken order stopping the replica is the projector stopping by another
  route.
- **Control** The same projection after the letters are redriven reports
  `PhaseFollowing` at its next pass, with `Quarantined` still non-zero — the
  durable mark does not clear, and the phase does.

#### UC-157 An operator skips a letter  [edge]
- **Given** A letter an operator evicts without applying it.
- **Then** The `Redriver` records the eviction in the application's own table;
  `Progress.Quarantined` is unchanged (it was raised at the park and is never
  decremented by anything); the sequence unblocks when its last letter leaves;
  `Park.Holes` still counts the evicted letter, because an eviction without an
  apply *is* a hole; and no effect is ever staged for it (§1.6).
- **Must not** No counter may be decremented by a redrive or an eviction, and no
  "applied position" may be published (§1.4).
- **Control** A sequence that was parked and then **redriven to completion**
  leaves `Quarantined` non-zero and `Holes` zero — the two numbers answer two
  questions and only one of them gates a cutover (§UC-163).

#### UC-179 A partition holding a parked sequence is split  [edge]
- **Given** Sequence `A` parked at `A2 A3` under `orders@2`, `Quarantined` at 2,
  and the partition holding `A`'s key space split into two children.
- **Then** The letters are untouched and still reachable: the park is keyed by
  `Identity.Whole()`, which a split does not move. The child whose mask matches
  `A` reads a non-zero `Sequences`, calls `Holds`, and parks `A4 A5 A6` without
  ever calling the handler; the other child sees `Holds` answer false for its own
  keys and applies them.
- **Must not** No later event of `A` may reach a handler, and `A2` must not be
  applied by a redrive **after** `A4..A6` were applied — the read-model corruption
  in the other direction, and the reason the letters must stay reachable rather
  than merely surviving.
- **Control** The same split with an **empty** park changes nothing: neither child
  calls `Holds` at all, and the fast path is still free.

#### UC-189 Two generations of one projection share one `Park` implementation  [edge]
- **Given** Generation 1 live with sequence `A` parked, generation 2 rebuilding,
  and one application `Park` table behind both.
- **Then** Generation 2's `Sequences` answers zero: the key it is handed is
  `orders@2`, and generation 1's letters are under `orders@1`. It calls `Holds`
  never, parks nothing it did not fail on, and reaches the cutover with `Holes`
  zero.
- **Must not** The park must not be keyed by the bare projection name — the live
  generation's letters would then block the rebuild, which would end with a
  non-zero `Holes` and could never be cut over, while §INV-097 asserts the
  opposite.
- **Control** Two **partitions** of one generation *do* share a park, deliberately
  (§1.4): a sequence parked by one is seen by the other, which costs a `Holds`
  per envelope and is what keeps a split from orphaning letters.

#### UC-190 `ParkSequence` beside a wiring that cannot order it  [edge]
- **Given** Three specs with `OnPermanentFailure: ParkSequence`: one at
  `Advance: AfterApply`, one at `InUnit` with `Destination: Unchecked`, and one at
  `InUnit` with a resolvable destination.
- **Then** `New` refuses the first two with `ErrSpec`, naming the field and the
  reason — the park row and the read model must commit together or a redrive and
  the loop can apply two letters of one sequence out of order — and accepts the
  third.
- **Must not** The weaker wiring must not be accepted with a scoped invariant:
  §INV-090's falsifiers would all be tier A while half the shipped matrix silently
  did not hold it.
- **Control** The same two specs with `OnPermanentFailure: Halt` are accepted, so
  the refusal is the park's and not the mode's.

#### UC-197 The attempt budget is spent on a retryable failure under `ParkSequence`  [edge]
- **Given** `Attempts: 2`, a page holding one envelope each of four orders, and a
  handler failing with a retryable error — a pool the read model could not reach
  — for as long as the budget lasts.
- **Then** The page is retried under the budget, and once it is spent the same
  rule that makes an exhausted retry permanent parks **all four sequences**, each
  letter carrying that transient cause at the attempt the isolation pass ran on.
  The checkpoint advances once, the phase is `PhaseDegraded`, `Ready` passes, and
  the projection goes on with every later sequence. The recovery is an operator's
  redrive — or the `runtime.Runner` a host wrapped `Redrive.Any` in — after which
  the next pass reads a zero count and returns to `PhaseFollowing` with
  `Progress.Quarantined` still at four.
- **Must not** It must not be answered by a per-failure enqueue policy: the
  mechanism's fourth decision, "do not enqueue", advances the scan over an event
  no handler applied, which §INV-092 refuses. And it must not be left unstated —
  the trade against `Halt`, where the same outage stops the projection instead,
  is what a consumer chooses between.
- **Control** The same page under a **permanent** failure parks on the attempt
  the first pass opened with, spending no attempt and publishing no `PhaseRetrying`
  at all, so the budget is what the transient failure consumed.

#### UC-198 A `Park` is supplied beside a policy that does not name it  [edge]
- **Given** A spec carrying a `Park` — the one field a composition root fills in
  once for a live generation and a rebuild — beside `OnPermanentFailure: Halt`,
  at `AfterApply` and again at `InUnit`, over a queue that is already holding a
  sequence the incoming page belongs to.
- **Then** `New` accepts both, and the queue is never reached: zero
  `Park.Sequences`, zero `Park.Holds`, zero letters written, the page applied or
  halted exactly as the named policy says, and the queue holding what it held.
- **Must not** The blocking path must not be gated on the field alone. Gated
  there, `AfterApply` asks `Holds` per envelope and writes letters with no
  transaction to roll them back, so a postponed pass parks the same envelope
  twice — a duplicate a later redrive applies twice and a `Park.Holes` a cutover
  reads inflated for ever.
- **Control** The same queue and the same page under `ParkSequence` at tier A ask
  `Holds` once per envelope and park what the queue already holds, so the
  inertness is the policy's and not the page's.

#### UC-199 Which unit of work each `Park` method is called in  [edge]
- **Given** A `Park` that records, per method, whether the context it was handed
  carried the caller's unit, over a projection that parks and then meets a later
  page.
- **Then** `Holds` and `Park` are called **inside** the unit, always; `Sequences`
  is called **outside** it, always; and `Holes` is called by no pass at all. The
  published contract states the four separately rather than collectively.
- **Must not** The contract must not say "every method runs inside the caller's
  unit". Its own argument invites an implementation that requires the ambient
  transaction, and such a `Sequences` fails on every pass.
- **Control** A `Sequences` that refuses when no unit is bound leaves the
  projection at `PhaseRetrying` with the row at advance zero and the handler
  never called — a postpone with nothing to clear it, which is what the wrong
  sentence costs.

## Group AD — generations and cutover (ES-04)

#### UC-158 A generation is built beside the running one  [happy]
- **Given** Generation 1 live and serving reads, and generation 2 started over
  the same log with its own name, its own checkpoint rows, its own destination
  tables and `Effects: nil`.
- **Then** Both drain. Generation 1 keeps serving. Generation 2 replays the whole
  log from the origin and its rows agree with generation 1's at the barrier.
- **Must not** Generation 2 must not read, pause or reset generation 1's
  checkpoint — there is no API by which it could.
- **Control** UC-120's shipped assertion that the two destinations hold the same
  rows, re-run under the generation naming, so the naming change is proved not to
  alter the result.

#### UC-159 A barrier is observed and reached  [happy]
- **Given** Generation 1 over a four-member `Cover` at `Highest` 900/1200/1150/1300,
  and generation 2 catching up.
- **Then** `Observe` answers 900. `Reached` answers `Reached: false, Behind: n`
  while generation 2's minimum is below 900, and `Reached: true` once it is at or
  above it, with `Quarantined` summed across its partitions and `Holes` asked
  once of the generation's park.
- **Must not** The barrier must not be a cursor, must not be compared to one, and
  must not be used to resume anything.
- **Control** A generation **all** of whose rows are absent answers a barrier at
  the origin, and that is the true answer rather than a vacuous one: it delivered
  nothing, so nothing is owed. The discriminating pair is §UC-180 — the same
  origin barrier is unobtainable from a generation that has live rows.

#### UC-180 A barrier is asked of a set with a member that has not reported  [edge]
- **Given** A generation whose cover is four partitions, three of which have
  checkpoint rows at 900/1200/1150 and one of which has none.
- **Then** `Observe` refuses with `ErrTopology`, naming the member with no row. It
  does **not** read the absent row as position zero and answer a barrier of zero
  from a fully-live generation.
- **Must not** No caller may supply a barrier to `Cutover` — `CutoverSpec` has no
  such field, and a barrier a caller can invent is not evidence. `Cutover(…,
  From: 1, To: 2, …)` observes it from the retiring generation's own rows inside
  the same unit, so a cutover to an empty generation is refused by arithmetic
  rather than by trust.
- **Control** The same cover with all four rows present answers 900 (§UC-143), and
  a cover all of whose rows are absent answers the origin (§UC-159's control) —
  the three arms are told apart by the rows rather than by the caller.

#### UC-200 A cutover declares a retiring cover no member of which holds a row  [edge]
*Added when the S4 implementation review was closed. §UC-180 is the same rule on
one member; this is the rule on all of them, and it is the half `Cutover` was
missing.*
- **Given** Two arms. **(a)** A live generation drained to a non-zero watermark
  and then `Split`, so the parent's row is gone and its retirement row stands,
  and a cutover declaring the cover the operator had **before** the split.
  **(b)** A live generation running at four partitions, and a cutover declaring
  its retiring cover as `Whole()` — the natural mistake when the two generations
  have different topologies, which is much of the reason a rebuild exists.
- **Then** Both are refused and nothing is written. **(a)** is `ErrTopology`
  naming the member and the row that records its retirement, from `Observe` as
  well as from `Cutover`: the rows say that share ran and handed its cursor to two
  children, so it is not a member at position zero. **(b)** is `ErrRetired` naming
  the generation, from `Cutover`: the barrier folded from silence is the origin,
  every arriving generation clears it by `x >= 0`, and a read target is not moved
  on no evidence. The refusal names both readings the rows cannot tell apart — a
  cover that is not the one this generation records at, and a generation nothing
  ever recorded for.
- **Must not** The barrier of zero must not be reachable as *evidence* through any
  path. The refusal must not be reachable by an override — the arriving arm has
  none and this is the same absence. And standing a read target up where nothing
  preceded it must not be smuggled in through this door: that is a row the
  application's own `Generations` writes.
- **Control** Three, and they are what keep the refusal from being about the
  cover's shape. The same cutover over the cover the retiring generation actually
  records at proceeds, in **both** arms — the children's cover in (a), the four
  partitions in (b). And a generation that genuinely never ran, with no rows and
  no retirement row, **still answers the origin from `Observe` with a nil error**
  (§UC-159's control, unmoved): the door that closed is `Cutover`'s, and
  `Observe`'s documented answer did not change.

#### UC-201 The retiring generation advances between the barrier and the switch  [edge]
*Added when the S4 implementation review was closed. The read-target half of
Marten's accepted overlap window, which §1.6 answered only for side effects.*
- **Given** A retiring generation and an arriving one at the same watermark, a
  cutover in the caller's unit, and the retiring generation — a separate runner,
  committing in its own transaction — advancing past the barrier after it was
  read and before the ownership row commits.
- **Then** The cutover is **admitted**, and that is the contract rather than a
  defect: nothing here claims, locks or fences the retiring generation's rows, and
  no isolation level closes the window ([[D-126]] forbids choosing one, and
  `REPEATABLE READ` would make the barrier the snapshot value, which is also
  stale-low). Reads move backwards at the switch by exactly what that generation
  advanced over the life of the transaction, and recover when the arriving
  generation catches up. The window is **named where an operator reads it** —
  `Cutover`'s own contract — with what closes it: drain or stop the retiring
  generation before, or as, the switch commits, and `Observe` it twice to see
  whether the barrier moved. `Spec.Pace` on the arriving generation lengthens the
  recovery and its own field comment says to drop it first.
- **Must not** No comment may claim the caller's unit makes the evidence and the
  switch one snapshot with respect to a *running* retiring generation; what the
  unit buys is the arriving generation's rows and the ownership row moving
  together, and the two are stated apart. The cutover must not write, claim or
  fence any checkpoint row — Axon's `resetTokens` claims every token with the
  processor shut down, and neither half is available to a call that cannot stop a
  runner in another process. Taking a live runner's fence away to buy a window an
  operator closes by draining is refused, in writing.
- **Control** The same cutover with the retiring generation at rest leaves the
  two generations at the same watermark and the regression at zero, so what the
  positive arm measures is the advance and not the switch. And the retiring row's
  advance is asserted to have moved only by its own runner's writes, so a cutover
  that started claiming rows fails this case and the decision above is revisited
  rather than eroded.

#### UC-160 The retiring generation cannot write into the arriving one  [edge]
- **Given** Both generations running, and a deliberate attempt to make the old
  one write into the new one's tables.
- **Then** It cannot: the checkpoint rows differ by name, the parks differ by
  name, and the handler resolves its table from `Batch.Identity.Generation`, so a
  handler ignoring the batch it was given is the only route and it is visible in
  the handler's own source.
- **Must not** The framework must not claim it *prevents* a handler from writing
  anywhere — it prevents it from being *told* to.
- **Control** A fixture handler that ignores `Batch.Identity` and writes to a
  fixed table does write to the wrong one, asserted, so the guarantee's boundary
  is measured rather than assumed.

#### UC-161 The cutover switches the read target atomically  [happy]
- **Given** Generation 2 at or past the barrier with `Quarantined` zero, and a
  `Generations` row holding 1.
- **Then** `Cutover` re-checks `Reached`, refuses if it is false, and issues one
  fenced write from 1 to 2 inside the caller's unit. Every read path resolving
  through that row moves at the commit, for every table at once.
- **Must not** The framework must not rename a table, issue DDL, or learn a table
  name. The switch must not be N writes.
- **Control** A reader held open across the commit sees generation 1's tables
  before and generation 2's after, with no interleaving — the property "atomic
  for the declared set" is measured by a reader rather than argued from the row
  count. **And the negative arm, which is the precondition made visible:** a
  reader that resolved the row *before* the commit and then reads the tables
  afterwards keeps reading generation 1, and that is **correct** until generation
  1 is retired. It is why retirement is a second, later step ordered against
  readers (§1.5) and why the atomicity sentence never appears without "in the same
  snapshot".

#### UC-162 Two operators cut over at once  [edge]
- **Given** Two concurrent `Cutover` calls from 1 to 2.
- **Then** One commits; the other's fenced `Activate` matches nothing and answers
  `ErrConflict`. The row holds 2 exactly once.
- **Must not** Neither may read the row and then write it in two statements.
- **Control** A sequential pair produces one success and one `ErrConflict` for
  the second, because the row no longer holds `from`.

#### UC-163 A cutover is refused because the arriving generation has holes  [edge]
- **Given** Generation 2 at the barrier, reached three ways: with two letters
  still parked; with one letter evicted unapplied; and with everything it parked
  **redriven to completion**, so `Quarantined` is 2 and `Holes` is 0.
- **Then** The first two are refused, naming `Holes` and the sequences behind it,
  and write nothing. The **third proceeds without the override**: a generation
  that recovered completely has a read model with no holes, and the ordinary
  recovery path — park, fix, redrive, cut over — must not run through
  `AcceptQuarantined`, whose whole purpose is admitting a known-broken generation.
- **Must not** The refusal must not read `Progress.Quarantined`, which never falls
  and would refuse the third case for ever. The override must not be a default or
  reachable by leaving a field zero, and it must not become the path operators
  take by habit — a check that is always overridden has stopped being a check.
- **Control** The same cutover with nothing ever parked proceeds, and `Holes` and
  `Quarantined` are both zero, so the two numbers agree exactly where they cannot
  disagree.

#### UC-164 A rollback  [happy]
- **Given** The row holding 2, generation 1 still running and still current.
- **Then** `Cutover` with `From: 2, To: 1` re-checks that generation 1 has
  reached a barrier observed from generation 2, and issues the fenced write back.
- **Must not** A rollback must not skip the readiness check — a generation that
  was stopped and fell behind is not one reads may be pointed at.
- **Control** A rollback to a generation whose checkpoint rows were dropped
  answers `ErrRetired` and writes nothing.

#### UC-165 A rebuild is cancelled  [edge]
- **Given** A running generation-2 rebuild and a `Drain` followed by a context
  cancellation.
- **Then** The checkpoint row is either unchanged or at the actual partial
  position the rebuild reached — never torn, because the drain is acknowledged
  between passes. Starting the same generation again resumes from that row with
  no manual intervention.
- **Must not** There is no reset, so the resumption must be from the row rather
  than from the origin, and a failed generation must not be "cleared" — the exit
  is generation 3.
- **Control** A cancellation landing inside a pass under `InUnit` leaves neither
  the handler's rows nor the advance (the shipped `TestTheTwoModesLeaveDifferentStateAtOneKillPoint`
  property, re-asserted under a generation).

#### UC-166 The old generation meets the new one's event type  [edge]
- **Given** Generation 1's build routing `orders.Created` and `orders.Paid`, and
  generation 2's build introducing `orders.RefundIssued` on the same family, with
  both generations live.
- **Then** Without preparation, generation 1 halts on the first `RefundIssued`
  with `ErrUnrouted` — [[D-131]], unchanged. With
  `Ignore(router, "orders", "orders.RefundIssued")` shipped in generation 1's
  **own** build first, it skips them and keeps serving.
- **Must not** The framework must not skip an unclaimed type of a covered family
  to make blue/green convenient — that is [[D-131]] being reversed, and the cost
  is a read model silently missing an event type for ever.
- **Control** A type of an **uncovered** family is skipped and counted by
  `Router.Skipped()` with no declaration at all, so the `Ignore` requirement is
  scoped to covered families rather than universal.

#### UC-167 A rebuild is paced  [happy]
- **Given** `Spec.Pace: 200ms` on a rebuilding generation and zero on the live
  one, driven through the injected `Ticks`.
- **Then** The rebuild issues at most one read per pace interval while draining;
  the live projection is unpaced; neither's correctness changes.
- **Must not** `Pace` must not be a concurrency cap, must not be presented as one,
  and must not apply while following (where `Idle` already governs).
- **Control** `Pace: 0` reads as fast as the store answers, so the throttle is
  measurable.

#### UC-168 A generation's cost is read off the database  [edge]
- **Given** Two generations at four partitions each, over one log, on one
  database.
- **Then** Eight independent walks are issued, each minting its own settlement
  bound, and the read traffic is eight times a single projection's. This is
  measured and recorded, not merely stated.
- **Must not** No filter may be added to `Log.ReadAll` to reduce it (Reject 3),
  and no shared reader may be introduced in this phase.
- **Control** One generation at one partition is the baseline the eight is
  measured against.

## Group AE — effects (ES-06)

#### UC-169 A live projection stages effects and a rebuild does not  [happy]
- **Given** Generation 1 with `Effects` set, `Generations` wired and
  `Advance: InUnit`; generation 2 with `Effects: nil`; the ownership row holding
  1; both draining one log.
- **Then** Generation 1 stages once per applied envelope past its barrier, inside
  the unit; generation 2 stages nothing at all. The effect sink records exactly
  the live generation's envelopes.
- **Must not** The rebuild must not be given a capability and told not to use it,
  and there must be no run-time "am I rebuilding" flag.
- **Control** The ownership row is flipped to 2 and generation 2 is restarted
  **with `Effects` set**: it stages, and generation 1 — still holding `Effects` —
  stages nothing. That isolates the two suppressors from each other, which the
  earlier control could not: "generation 2 with `Effects` set dispatches" while
  the row still names 1 is unsatisfiable, because §1.6 requires `Generations`
  beside `Effects` at a non-zero generation and §UC-173 makes the row decide. The
  precedence is stated in §1.6 and this pair measures it.

#### UC-170 `Effects` beside `AfterApply`  [edge]
- **Given** A spec with `Effects` set and `Advance: AfterApply`.
- **Then** `New` refuses with `ErrSpec`, naming the mode, and the message says
  why: an effect staged outside a unit has the window a broker outage turns into
  a permanently lost event.
- **Must not** It must not be accepted with a warning, and it must not be
  silently upgraded to `InUnit`.
- **Control** The same spec at `InUnit` is accepted.

#### UC-171 A warm-up is interrupted and resumes suppressed  [edge]
- **Given** `EffectsAfter: N`, a generation at `M < N`, and a process killed
  mid-warm-up at `K` where `M < K < N`.
- **Then** The restart resumes at `K` and continues **suppressed** over `(K, N]`,
  dispatching nothing, and begins dispatching at the first envelope past `N`.
- **Must not** The trigger must not be "own progress == 0" — that is the failure
  that only shows up in production, and it is the reason the barrier is a stored
  position rather than a flag.
- **Control** The same generation started fresh dispatches nothing over `(0, N]`
  and everything past it, so the resumed and fresh cases agree on the boundary.

#### UC-172 A page straddles the barrier  [edge]
- **Given** A page whose positions run `N-2 N-1 N N+1 N+2`.
- **Then** `Stage` is called once with exactly `N+1` and `N+2`. The handler is
  called with all five.
- **Must not** The split must not be per page — staging the whole page double-fires
  over history the prior generation covered, and staging none of it loses the
  first two real effects.
- **Control** A page entirely at or below `N` does not call `Stage` at all (not
  even with an empty slice), and a page entirely above it stages all of it.

#### UC-181 A page in which one envelope is parked and one is applied  [edge]
- **Given** A page holding `A2 B1`, `EffectsAfter: 0`, `Effects` set, sequence `A`
  failing permanently under `ParkSequence`, and `B1` applying cleanly.
- **Then** `Stage` is called once with exactly `B1`. `A2` is parked with its
  effect: it is not staged now, and it is not lost — the letter carries it.
- **Must not** A parked envelope's effect must not be staged (the framework would
  be telling the world about a state its own read model refused) and must not be
  dropped (a permanently-failing park would silently lose an unbounded set of live
  effects, with a read-model counter as the only trace).
- **Control** The same page with `A2` succeeding stages both, so the exclusion is
  attributable to the park rather than to the barrier.

#### UC-182 A redrive stages the effect of the letter it applies  [happy]
- **Given** `A2 A3` parked, with `Effects` and `Generations` on the
  `RedriveSpec`, and the cause fixed.
- **Then** Applying `A2` stages `A2`'s effect in the **same transaction** as the
  apply and the `Evict`; the same for `A3`; a crash between them leaves the letter
  parked, nothing applied and nothing staged.
- **Must not** A redrive must not apply a letter without staging its effect (that
  is where the effect would be lost for ever, since the loop advanced over those
  positions and will never see them again), and an **eviction** must not stage
  anything at all, ever — a skip is the operator saying the event will never be
  applied. And `NewRedrive` must not **accept** an `EffectsAfter`: it is the one
  wiring whose only outcome is that loss, and it is refused at the door (§1.6).
- **Control** The same redrive with `Effects` nil applies both letters and stages
  nothing, and a redrive whose identity's generation does not own the row stages
  nothing either — the same two suppressors the loop has, checked the same way.
  The refusal has two of its own: the identical spec with the field at zero
  constructs, and the loop's door accepts the very barrier this one refuses, so
  the refusal is the queue's and not the field's.

#### UC-173 A retired generation stops dispatching at the cutover  [happy]
- **Given** Generations 1 and 2 both running with `Effects` set, a `Generations`
  whose `Active` is the documented **locking** read, and a cutover from 1 to 2
  committed mid-drain.
- **Then** Generation 1's next pass reads the ownership row inside the
  transaction that commits its advance, finds 2, and stages nothing. Its handler
  keeps running and its checkpoint keeps advancing. Generation 2 begins staging.
  **No envelope is staged by both — and the read that makes that true is the
  locking one** (§1.6, §UC-202): a pass that read the row before the cutover and
  commits after it stages beside the arriving generation, and nothing in this
  framework can order the two for the implementation.
- **Must not** The ownership read must not be outside the committing transaction,
  and it must not be cached across passes.
- **Control** Without the ownership row (a spec at `Ungenerated`), both would
  stage — asserted as a fixture, so the row is shown to be what closes the window
  Marten calls "a separate concern".

#### UC-192 The ownership row is reached over a second pool  [edge]
- **Given** A `Generations` implementation that opens its own connection instead
  of resolving through the context's ambient transaction, and a cutover from 1 to
  2 committed mid-drain.
- **Then** Generation 1 keeps staging past the cutover, and both generations stage
  the same envelopes for as long as the retired one runs. This is asserted, as one
  measured boundary of the guarantee: the framework holds a method set and no
  resource, so it cannot compare that implementation's transaction to its own, and
  a self-reported "am I in your transaction?" would be answered by the same code
  that is wrong.
- **Must not** A second pool must not be presented as the **only** way to lose
  the boundary. A `Generations` in the caller's own transaction whose `Active` is
  a plain unlocked read loses it too, at every isolation level this repository
  names — §UC-202 measures that one, and a live case that cuts over between two
  passes passes by scheduling luck rather than by the boundary holding. The spec
  must not claim the alignment is measured (§1.6 says which three alignments are
  asserted and why the `Destination` argument does not reach them), and the
  module page must not state the two-sender boundary without the obligation that
  carries it.
- **Control** The documented implementation — resolving through the context, with
  a locking read — stops staging at the same cutover (§UC-173), so the two are
  told apart by a rollback rather than by prose.

#### UC-202 The cutover commits between two passes' ownership reads  [edge]
- **Given** Generations 1 and 2 draining one log with `Effects` set, and the one
  interleaving that decides the boundary: the retiring pass sits between its
  ownership read and its commit, the arriving pass reads the same row, and the
  cutover's write is issued across both. Run twice — once with a `Generations`
  whose `Active` is the documented locking read, once with a plain one.
- **Then** Under the **locking** read the cutover's `UPDATE` waits behind every
  unit that read the row: both passes read `1`, generation 1 stages the envelope,
  generation 2 stages nothing, and the cutover returns only after the unit that
  staged has committed. **The envelope is staged exactly once.** Under the
  **plain** read the cutover commits between the two reads: generation 1 commits
  a staged effect under a row that already names 2, generation 2 reads 2 and
  stages the same envelope, and **one envelope is staged by both**.
- **Must not** The locking arm must not be read as a framework guarantee — the
  obligation is the implementation's ([[D-126]]), and what the pair measures is
  which recipe carries it. It must also not be read as closing the overlap of two
  generations at different positions: that is §1.5's window and §UC-201's, and it
  is closed by draining the retiring generation.
- **Control** Each arm is the other's: the same interleaving, the same specs, one
  clause of SQL apart, and opposite outcomes.

#### UC-203 The same generation is restarted with `EffectsAfter` dropped  [edge]
- **Given** A generation warmed up under `EffectsAfter: N` and interrupted at
  `M < N`, restarted from its own rows by a release that no longer names the
  barrier — a rollback of the release that added it, a config map that lost a
  key, a spec builder whose default is zero.
- **Then** It resumes at `M` and **stages** an effect for every envelope over
  `(M, N]` as well as for everything past `N`, on the first pass, with no error,
  no refusal and no record. This is asserted, as the measured half of the barrier
  that is **not** durable: one side of the comparison is a checkpoint column and
  the other is a constant the deployment holds (§1.6).
- **Must not** The document must not claim both sides are durable, and a comment
  on `Spec.EffectsAfter` or on the gate must not say the interrupted-warm-up
  property holds for a restart under a lower barrier. Nor may it be answered by
  `Barrier.At` being read at run time: a position is not a resume point
  ([[D-129]]) and `Observe` answers the **prior** generation's mark, which is
  what an operator writes into the constant.
- **Control** The same restart with the barrier still named resumes suppressed
  over `(M, N]` and stages only what is past `N` (§UC-171), so what the arm
  measures is attributable to the dropped constant.

#### UC-193 A generation-zero projection is retired by the first cutover  [edge]
- **Given** The projection every deployment actually has: `orders` at
  `Generation: Ungenerated` with `Effects` set, running against a live read model.
  Its operator adds a `Generations` to its spec one release before anything else
  changes, builds `orders@2` beside it, and commits
  `Cutover(From: Ungenerated, To: 2)`.
- **Then** Before the cutover, `Generations.Active("orders")` answers
  `Ungenerated` — a projection with no ownership row answers `Ungenerated` and a
  nil error — which is `orders`'s own generation, so it goes on staging exactly
  as it did with no `Generations` at all. After the cutover the row answers 2,
  and `orders`'s very next pass reads it inside the transaction that commits its
  own advance and stages **nothing**, while its handler keeps applying and its
  checkpoint keeps advancing. `orders@2` stages. No envelope is staged by both.
- **Must not** The suppressor must not be gated on `Spec.Generation`. Gated
  there, it never runs for a projection at `Ungenerated`, which is every
  projection that exists today (§1.5), and
  `Cutover(From: Ungenerated, To: 2)` — the *only* migration a running
  deployment has, because renaming `orders` to `orders@1` changes its checkpoint
  row key and resumes it from the origin against a live read model (§UC-188) —
  leaves both generations staging every event, for ever, with no error on any
  path.
- **Control** The identical spec with `Generations: nil` keeps staging after the
  same cutover. That is the stated limit of the boundary, measured rather than
  written down: the framework holds no route to an ownership row it was not
  given, and requiring one beside every `Effects` was already rejected in §1.5.
  The remedy is the release ordering above, which is the blue/green shape
  [[D-131]]'s amendment records for `Ignore`.

#### UC-174 The ownership read costs nothing when there is nothing to stage  [happy]
- **Given** A generation whose page holds no applied envelope past the barrier, or
  a spec with `Effects: nil`.
- **Then** `Generations.Active` is not called at all, counted on a recording
  implementation — the three suppressors are checked cheapest-first (§1.6) and
  this is what that ordering buys.
- **Must not** The row must not be read once per pass unconditionally — a rebuild
  would then pay a round trip per page for a capability it does not have.
- **Control** A page with one applied envelope past the barrier reads it exactly
  once.

#### UC-175 `EffectsAfter` beside `Park`  [edge]
- **Given** A spec with `EffectsAfter > 0` and `OnPermanentFailure: ParkSequence`.
- **Then** `New` refuses with `ErrSpec`, saying that a warm-up which parked an
  event produces a generation whose rows cannot be compared to the live one and
  that a cutover's only evidence is the comparison.
- **Must not** The policy must not be silently switched to `Halt` below the
  barrier and back above it.
- **Control** `EffectsAfter > 0` with `Halt` is accepted, and `ParkSequence` with
  no barrier is accepted, so the refusal is the combination's.

#### UC-176 `EffectsAfter` with no `Effects`  [edge]
- **Given** A spec with `EffectsAfter` set and `Effects` nil.
- **Then** `New` refuses: a barrier with nothing to gate is a spec assembled
  wrong.
- **Must not** It must not be accepted and ignored.
- **Control** `Effects` with no `EffectsAfter` is accepted and stages from the
  first envelope.

#### UC-177 The projection path reaches nothing that can dispatch  [edge]
- **Given** A `go/types` walk over `event/projection` and everything it reaches.
- **Then** No package on that path imports `net`, `net/http`, `net/smtp` or
  `os/exec`, transitively.
- **Must not** The walk must not be presented as a sandbox — a handler may still
  dial out, and the module page says so in the same paragraph.
- **Control** A fixture package that imports `net/http` is reported by the same
  walk, so a walk that resolved nothing is not read as a clean tree.

#### UC-178 An effect sink is a staged job  [happy]
- **Given** An `Effects` implementation whose `Stage` calls `jobs.EnqueueIn`
  inside the unit's context, wired by the composition root.
- **Then** The job row, the read model's rows and the advance commit together;
  a rollback leaves none of the three. The HTTP call is the job's, made by
  whatever drains the stage, with its own retry and its own idempotency.
- **Must not** `event/projection` must not import `jobs` ([[D-130]]), and the
  effect must not keep its own "effects to send" table ([[D-118]]).
- **Control** The same sink under `AfterApply` is refused at construction
  (§UC-170), which is where the reference's dual-write window is closed.

#### UC-183 A stage is followed by a lost fence  [edge]
- **Given** Two live instances of one identity, `Effects` staging a job row, and
  the loser losing the fence ([[D-133]] `ErrOvertaken`) after `Stage` returned.
- **Then** The loser's whole unit rolls back: the staged row is gone, the advance
  did not move, the read model's rows are gone, and the winner's pass stages the
  same envelopes exactly once. Nothing outside the database happened, because
  `Stage` is contracted to make a durable write and nothing else.
- **Must not** The contract must not admit an externally visible irreversible
  action here — an HTTP call, a payment, a mail — because every rollback path the
  document enumerates (a lost fence, `ErrParkFull`, a later envelope's permanent
  failure, a caller's `Unit` retry) leaves it sent and the checkpoint unadvanced,
  and the next pass sends it again. A lost fence is the expected outcome of a
  rolling deploy, not an exotic one.
- **Control** The same stage on a committing pass leaves both the staged row and
  the advance, so the rollback is attributable to the fence.

#### UC-184 A rebuild spelled as a second `Spec.Name` carries `Effects`  [edge]
- **Given** `Spec.Name: "orders-rebuild"` at `Generation: Ungenerated`, with
  `Effects` set, replaying a full log — the rebuild shape the shipped module page
  teaches and UC-120 demonstrates.
- **Then** It stages an effect for **every historical event**, asserted. This is
  the measured edge of ES-06's guarantee: the framework cannot distinguish a
  second projection name from a rebuild of the first, so the gate does not reach
  that spelling, and the document says so rather than implying a coverage it does
  not have (§1.5).
- **Must not** The gap must not be closed by requiring `Generations` beside every
  `Effects`: it would impose an ownership table on every single-sender projection
  and would still admit this case, because the second name's own row answers
  `Ungenerated`, which is what the spec asks for.
- **Control** The same rebuild spelled as `Generation: 2` with `Effects` set and
  the ownership row holding 1 stages nothing (§UC-169), so the recipe the module
  page teaches after this phase is the one the gate covers.

---

# 4. Invariants

Each states the property and **how it is falsified**.

#### INV-083 `InUnit` means one transaction authority, and two are refused
- **Statement** Under `Advance: InUnit` with a resolvable `Destination`, the
  authority the checkpoint store answers inside the unit and the authority built
  from the destination's bound executor compare `Same` before the handler runs.
  When they do not — or when the destination's identity is not comparable — the
  pass refuses with `ErrSpec` and halts, without reaching the classifier and
  without downgrading.
- **Falsified by** §UC-132's pair: a `Unit` opening two transactions is refused
  and the identical one-transaction composition drains. Plus a recording
  checkpoint store asserting the comparison happens **inside** the unit and
  **before** the handler, per pass and not once.

#### INV-084 What a foreign destination is promised, and what it is not
- **Statement** For any destination outside the unit, the framework promises
  at-least-once delivery, position order with per-stream order preserved, a
  resume point that never skips a committed event, and a `(Stream, Version)` that
  is unique and stable across processes, restarts, stores, partitions and
  generations. It promises nothing about the effect, and a checkpoint that
  advanced is a statement about scanning and not about application.
- **Falsified by** §UC-134's kill campaign asserting the four and asserting that
  no test anywhere asserts the fifth; and §UC-133's control, in which the read
  model's rows survive a rolled-back advance.

#### INV-085 A sequence key is total, pure and stable, and the framework cannot check it
- **Statement** `SequenceOf` returns a string for every envelope, reads nothing
  but the envelope, and answers the same key for one envelope for the life of the
  log. The framework makes no run-time check of any of the three; it refuses to
  claim otherwise, halts on a panic out of a sequencer, and names a rebuild as the
  only supported way to change a key.
- **Falsified by** §UC-135's control, where an unstable sequencer leaves events
  applied twice or not at all and the case asserts that breakage; §UC-144's halt
  against the handler-panic control; and a doc check that no page claims the key
  is validated.

#### INV-086 A partition is a mask, a key never moves except by a split, and no count is stored
- **Statement** A partition is `(id, mask)` with `mask = 2^k − 1`. A key belongs
  to the partition whose id equals the low k bits of FNV-1a/32 over its UTF-8
  bytes. A split takes one partition to two at mask `2m+1`, both starting at the
  parent's exact cursor, and moves no key out of the parent's half of the space.
  No partition count is stored anywhere, and nothing computes one from a modulus.
- **Falsified by** §UC-138 against its `hash % N` control, which asserts the
  reordering exists; §UC-141's ceiling; and a source check that no exported or
  unexported spelling of `%` is applied to a hash of a sequence key.

#### INV-087 A topology change is one transaction, is refused without a parent row, and holds no state between runs
- **Statement** A split reads the parent's row **and both children's**, writes
  both children at advance 1 carrying the parent's cursor byte for byte, and
  retires the parent — in one transaction of the checkpoint store's backing,
  opened by the caller, through the `event.Track` door and never against the raw
  store. An absent parent is refused with `ErrTopology` whatever the reason for
  the absence, and so is a child row beside a live parent. A parent that was not
  drained halts at its next save because its row is absent, and it never creates a
  fresh row beside its children. Every decision is derived from rows read inside
  the run, so a `Unit` that runs the body twice produces one split or a refusal
  and never two children at the origin.
- **Falsified by** §UC-138 counting the statements and asserting one commit;
  §UC-139 asserting the parent halts and does not contend; §UC-140's two absences
  against its has-run control; §UC-185's twice-run unit; and an injected failure
  between the two child writes asserting neither child exists.

#### INV-105 A declared partition set covers the key space exactly once
- **Statement** A `Cover` admits a set of partitions only when every key matches
  exactly one member: no two members overlap
  (`(idA ^ idB) & min(maskA, maskB) != 0` for every pair) and the members' shares
  sum to the whole space. `Observe`, `Reached` and `Cutover` take a `Cover` and
  never a `[]Partition`, so no aggregate answer in this phase is computed over a
  set with a gap. What it does not cover is stated: a single runner cannot see the
  set, so a host that assembles runners by hand rather than from a `Cover` is
  unchecked. And the guarantee is carried by the **type**, which means the zero
  value has to be refused where it is taken: `Cover{}` is a legal composite
  literal in any package, `Count() == 0` tells it from every checked set exactly,
  and every door asks.
- **Falsified by** §UC-186's gap and §UC-187's overlap, each against the
  admitted-set control that applies every event of a known log exactly once;
  §UC-180, where a member with no checkpoint row is refused rather than read as
  position zero; and §UC-195, which constructs both zero values from outside the
  package.

#### INV-107 Only the first start of a projection chooses its topology
- **Statement** A runner whose partition is not `Whole()` refuses to resume while
  a checkpoint row exists for any **coarser** share of its own key space. The
  question is asked once per runner life, at the resume, costs one `Load` per
  ancestor and none at all for a projection that named no partition, and is
  answered from rows rather than from a stored count. A row for a *finer* share is
  outside it and stated as such: enumerating the finer set would need a `List` the
  `Checkpoints` contract does not have, and that direction is a merge, which §1.3
  refuses outright.
- **Falsified by** §UC-194's two-release sequence against both of its controls —
  the same release after the handoff, and a partition set on a projection that
  never ran — so the refusal is measured to discriminate rather than to refuse
  every partitioned start.

#### INV-088 A cursor is never ordered, and a merge has no spelling
- **Statement** Nothing added by this phase compares two cursors for anything but
  equality and emptiness, answers a "lower" cursor, or takes two cursors at all.
  No `Merge` is exported.
- **Falsified by** §UC-142's surface walk over the phase's new packages with its
  `event.Read` control; and the ordering-operator source check [[D-129]] already
  runs, extended to the new surface.

#### INV-089 A partitioned projection's progress is a `min`, never anything else
- **Statement** `Progress.Highest` is a completeness watermark per checkpoint row
  and says nothing across rows. Every aggregate answer this phase publishes is the
  minimum across the partition set.
- **Falsified by** §UC-143 with unequal partitions and a single-partition
  control; and a doc check that no page calls a per-partition `Highest` the
  projection's progress.

#### INV-090 A park blocks the sequence, and the following events never reach a handler
- **Statement** When an envelope's failure is permanent under `OnPermanentFailure:
  ParkSequence`, that envelope and every later envelope of the same sequence — in
  this page, in every later page, and **across a split of the partition that held
  it** — are parked without being applied, until the sequence drains. Envelopes of
  other sequences are unaffected. It holds in every configuration that constructs,
  because `ParkSequence` constructs only at tier A: `InUnit` with a resolvable
  destination, so the park row, the read model and a redrive's `Evict` are one
  transaction. It is not scoped to a subset of the shipped matrix; the rest of the
  matrix is refused. A `Park` supplied beside any other policy is **accepted and
  inert** — the loop never counts the queue, never asks `Holds` and never writes a
  letter — so there is no configuration that constructs in which a letter is
  written outside a unit.
- **Falsified by** §UC-146 asserting that `A3` never reached the handler, with the
  control in which `A2` succeeds and all five are applied; §UC-147 for the
  cross-page half; §UC-179 for the across-a-split half, with its empty-park
  control; §UC-190, which asserts the wirings that cannot order it are refused
  rather than admitted; and §UC-198, which asserts the accepted-and-inert half
  with its `ParkSequence` control.

#### INV-091 The park, the read model and the scan checkpoint commit together
- **Statement** The park write, the handler's writes and the advance are in one
  transaction: a rollback leaves none of the three, and the `Holds` a later
  envelope of the same page makes sees the park write the earlier one made. The
  same holds for a redrive: the apply, the `Evict`, the `Touch` and the staged
  effect commit together, and a crash between them leaves the letter parked. This
  is the measurement §1.6 relies on in place of an identity comparison for a
  `Park` — an implementation reached over a second pool fails it.
- **Falsified by** an injected rollback after the park asserting the queue is
  empty and the advance unmoved; the same injection inside a redrive asserting the
  letter is still parked and nothing applied; and §UC-190's refusal, which is why
  there is no second mode to compare against.

#### INV-092 A full park stops the partition and skips nothing
- **Statement** `ErrParkFull` ends the pass without advancing, without parking —
  including the letters earlier envelopes of the same page parked successfully,
  which roll back with the unit — without skipping any envelope and without
  consuming an attempt. There is always a unit, because `ParkSequence` constructs
  only at tier A, so the retry cannot refill the queue whose fullness is blocking
  it. The phase is `PhaseBlocked`, the retry is unbounded, and `Ready` fails past
  `Tolerate`. It is not a halt.
- **Falsified by** §UC-150's three bounds; §UC-151's two-dimensional control; and
  the assertion that a pass after an operator's eviction advances with no restart.

#### INV-093 The park's bound is two-dimensional and per sequence
- **Statement** A new sequence is bounded by the queue's sequence count; an
  existing one by its own letter count; and a byte bound applies over the sum of
  parked payloads. `isFull` takes a sequence and never nothing.
- **Falsified by** §UC-151, which admits a 1024th letter into an existing
  sequence in a queue at its sequence bound and refuses a new one — **of the
  reference queue, because there is no `isFull` on the interface for the
  framework to call** — and by the same use case's running-projection control,
  which is the framework-visible half: the one-dimensional queue blocks the
  partition where the two-dimensional one keeps advancing.

#### INV-094 A redrive is ordered, rotating, exclusive, one sequence at a time, and touches no checkpoint
- **Statement** A redrive processes one sequence per call, in insert order,
  stopping at the first letter that fails again; `Any` **claims** the least
  recently tried unclaimed sequence and releases it on every exit path, so two
  concurrent redrives never process one sequence; and no redrive path issues a
  `Checkpoints.Save`.
- **Falsified by** §UC-153 asserting `A4` was untouched; §UC-154's rotation
  against its single-sequence control; §UC-191's two gated concurrent callers,
  asserting exactly one processes a sequence and no letter is applied twice; and a
  recording `Checkpoints` asserting zero saves across a redrive campaign.

#### INV-095 "Caught up" is never published while a sequence is parked, and the count is live
- **Statement** `PhaseFollowing` is published only when the whole log is read
  **and** the live parked count is zero. Otherwise the phase is `PhaseDegraded`
  or `PhaseBlocked`. `Ready` fails for `Blocked` past `Tolerate` and never for
  `Degraded`. The count is live rather than monotone: `Park.Sequences` is read
  once per resume, and again at the start of every pass while it is non-zero, so a
  drained queue clears the phase at the next pass and a projection does not stay
  `Degraded` for the life of the process.
- **Falsified by** §UC-156, with the control that the phase clears after a
  redrive while `Progress.Quarantined` does not; §UC-152's next-pass drop; and
  §UC-148's call counts at both ends, which are what tell the one clearing rule
  from the two rejected ones.

#### INV-096 No counter is ever decremented, and no applied position is published
- **Statement** `Progress.Applied` and `Progress.Quarantined` rise and never
  fall, by any path — not a redrive, not an eviction, not a split. No field,
  method or column anywhere answers "the position below which everything was
  applied". `Park.Holes` is not a counter and does not contradict this: it is a
  question answered from the queue's own rows — what was parked and never applied
  — and it is the number a cutover reads, precisely because the cumulative one
  cannot fall.
- **Falsified by** §UC-157; §UC-138's split arithmetic asserting the sum across
  the partition set is unchanged; and a surface walk asserting no exported
  `Position` is added beside `Progress.Highest`.

#### INV-097 A generation is a name, its rows and tables are its own, and two identities never render one name
- **Statement** A generation's checkpoint rows, parked letters and destination
  tables are reached only through its own identity: the checkpoint row and the
  runner are keyed by the full `Identity`, the park and the redrive by
  `Identity.Whole()` — the projection and the generation, never the partition.
  `Ungenerated` renders nothing, so every projection that exists today keeps its
  name and its row. The rendering is **injective**: two distinct `Identity` values
  never render one string, which is what makes a name safe as a primary key, and
  it is held by refusing `@` and `#` in a projection name rather than by escaping
  them, so no existing row's key ever changes. The kernel's identifier rule is
  held **on the constructor** rather than delegated to `event.Track`, because an
  `Identity` is an input at doors that never reach it, and the duplication is
  pinned to the kernel's spelling by a test that walks both.
- **Falsified by** §UC-145's rendering and round trip; §UC-188's `orders@2`
  refusal with its `orders.v2` control; §UC-196's two-door walk; §UC-158's two generations over one log;
  §UC-189's shared park; and §UC-160, whose control shows what a handler that
  ignores its batch can still do — the boundary measured rather than assumed.

#### INV-098 A cutover derives its own evidence, is one fenced write, and is refused without it
- **Statement** `Cutover` takes no barrier. Inside the caller's unit it observes
  one from the retiring generation's own rows over its `Cover`, checks the
  arriving generation against it over its own, and then issues exactly one fenced
  write from `from` to `to`. It refuses an arriving generation that has not
  reached the barrier, one whose `Holes` is non-zero unless `AcceptQuarantined` is
  set, and a target whose rows were retired. It never refuses on
  `Progress.Quarantined`, which never falls and would refuse a fully recovered
  generation for ever. Its atomicity is stated only with its precondition: the
  reader resolves the row in the same snapshot as the tables. **Amended when the
  S4 implementation review was closed:** it also refuses a *retiring* cover no
  member of which holds a row, because a barrier folded from silence is the origin
  and every arriving generation clears it — evidence is derived or the call does
  not happen. And the guarantee is bounded on the side it cannot hold: the
  caller's unit makes the *arriving* generation's rows and the ownership row one
  snapshot, and does nothing about a retiring generation still advancing, which is
  a named window rather than a silent one.
- **Falsified by** §UC-161 counting one write, measuring atomicity from a reader
  and asserting the stale-reader arm; §UC-162's race; §UC-163's three arms
  including the recovered one that proceeds without the override; §UC-164's
  `ErrRetired`; §UC-180, which asserts there is no field through which a
  barrier of zero can be handed **in**; §UC-200, which asserts there is no cover
  through which one can be **derived**, with three controls that keep the refusal
  from being about a cover's shape; and §UC-201, which measures the window and
  asserts the cutover writes no checkpoint row of the generation it retires.

#### INV-099 A barrier is a `Position`, and never a resume point
- **Statement** `Barrier.At` is an `event.Position`. Nothing turns it into a
  `Cursor`, resumes from it, or stores it as a checkpoint. It is compared with
  `>=` against `Progress.Highest` and with nothing else.
- **Falsified by** §UC-159 and its fresh-generation control; §UC-200's split arm,
  where a share that ran and handed its cursor down is refused rather than folded
  in at the origin; and the position→cursor surface walk [[D-129]] already runs,
  extended to this phase's packages.

#### INV-100 An effect capability is a value, a rebuild does not have one, and `Stage` is a write
- **Statement** A `Handler` is handed a `Batch` and has no route to an effect. An
  `Effects` is a separate field of the spec, refused beside `AfterApply`, and a
  rebuild **spelled as a generation** is a spec with it nil. There is no mode,
  flag or context value that turns a projection handler into an effect handler.
  `Effects.Stage` is contracted to make a durable write inside the unit and to
  perform no externally visible irreversible action, because every rollback path
  the pass has would otherwise leave one sent and the checkpoint unadvanced. The
  statement is scoped where it is scoped: a rebuild spelled as a second
  `Spec.Name` carries no generation, so the ownership gate does not reach it, and
  §UC-184 asserts what it does instead.
- **Falsified by** §UC-169 with its ownership-row control; §UC-170's refusal;
  §UC-183's lost fence; §UC-184's measured edge; and a surface check that `Batch`
  carries no `Effects`, no dispatcher and no context key.

#### INV-101 The effect gate rests on three things, and what each of them rests on is stated
- **Statement** The gate is decided under three conditions, and what each of them
  rests on is stated rather than averaged into "durable": suppression below the
  barrier compares a stored position against **the spec's own constant**, per
  envelope, so an interrupted warm-up resumes suppressed and a restart under a
  lower constant does not (§UC-203); ownership reads the generations row inside
  the transaction that commits the advance, and is a boundary exactly when that
  read is the locking one the `Generations` contract asks for (§UC-202); and
  **the park** decides whether there is an applied envelope to stage at all — a
  parked envelope's effect is not staged now and is not lost, because the letter
  carries it and a redrive stages it in the transaction that applies it. An
  evicted letter's effect is never staged, and that is stated on the module page
  rather than left to be discovered.
- **Falsified by** §UC-171's interrupted warm-up and §UC-203's dropped constant;
  §UC-172's straddling page; §UC-181's parked-and-applied page on **both**
  appliers (§UC-147); §UC-182's redrive and its refused barrier; §UC-173's
  mid-drain cutover with its no-row fixture control, §UC-202's two recipes and
  §UC-192's second-pool boundary; and §UC-174's call count.

#### INV-106 An effect belongs to an applied envelope, not to a page
- **Statement** `Effect.Envelopes` is exactly the envelopes of the page that the
  handler applied and whose position is past `EffectsAfter` — never a parked one,
  never one the handler did not see. An effect is staged once per applied envelope
  over the life of the log: by the loop if the loop applied it, by a redrive if a
  redrive did, and by nothing at all if it was evicted unapplied.
- **Falsified by** §UC-181 asserting exactly which of a mixed page reaches
  `Stage` — on the isolation pass and on the blocking one (§UC-147), which are
  two code paths holding the owed envelopes in two variables — each with its
  nothing-parked control; §UC-182 asserting the redrive stages what it applies,
  stages nothing for an eviction and refuses the barrier that would drop one; and
  §UC-172's straddling page for the barrier half.

#### INV-102 The framework contracts against dispatch and does not sandbox it
- **Statement** No package on the projection path reaches anything that can
  dispatch. The `Handler` contract says a projection handler does not. The module
  page says, in the same paragraph, that a handler may still dial out and that
  this is a contract rather than an enforcement.
- **Falsified by** §UC-177's walk with its `net/http` fixture control, and a doc
  check that the sentence is present.

#### INV-103 Nothing this phase adds starts anything, opens a transaction, or writes a line
- **Statement** No `go` statement in any non-test file under `event/`. No
  `Begin`, `Commit` or `Rollback` anywhere in `event/projection`. No
  `log.Printf`, no `port.Logger` — the package's published property is that it
  writes no line at all, and an effect dispatcher must not quietly break it.
  Every constructor added here performs no I/O and reads no environment.
- **Falsified by** the shipped `startsNothing` arm extended over the new files;
  the transaction source check [[D-130]] already runs; the dependency budget row
  (`./runtime` on top of `./crud` and `./errs`) unchanged; and a construction test
  asserting `Split`, `NewRedrive`, `Observe` and `Cutover` issue nothing until
  called with a context.

#### INV-104 Every refusal added here names its field and wraps a published sentinel
- **Statement** Construction refusals wrap `ErrSpec` and are collected rather
  than reported one at a time — `NewIdentity`'s delimiter refusal among them,
  because a name is something a spec names; refusals about a set of partitions or
  a change to one wrap `ErrTopology` — `NewCover`'s gap and overlap, `Split`'s
  absent parent and existing child, `Observe`'s unreported member, a `RedriveSpec`
  carrying a partitioned identity or the wrong sequencer; a full park wraps
  `ErrParkFull`; a retired generation wraps `ErrRetired`. A store's refusal
  travels as the sentinel `event` already publishes, so a consumer reads one
  vocabulary rather than two.
- **Falsified by** the shipped `TestNewRefusesEverySpecItCannotAssemble` extended
  with this phase's combinations, asserting every one names its field and that a
  spec wrong in three places reports three problems.

---

# 5. The exported Go surface

Every name below is what `make api` must show, and nothing else is exported.
Receiver name is `this` throughout.

## 5.1 `github.com/frostgrove/vv/event` — no signature moves, and one obligation does

No signature moves, no type gains a field, no sentinel is added or removed, and
`docs/api/surface.md`'s `event` section is byte-identical after this phase. What
changes is that `event/projection` begins using `Backing`, `NewAuthority` and
`Authority.Same` for the alignment comparison (§1.1) — three symbols already
exported for stores, used for a purpose they were not written for, which is a
comment in `pass.go` and not a contract change.

**But "the surface does not move" is not "the store contract does not move", and
saying only the first would be a lie by omission.** §5.3 widens what a
`Checkpoints` implementation must do. `check-event-kernel` watches exported
signatures and cannot see that, which is exactly the class of change a freeze
exists to catch, so it is announced here rather than discovered by a third-party
store author whose green suite goes red.

## 5.2 `github.com/frostgrove/vv/event/projection` — additions and three renames

### Renames

| Was | Is | Why |
|---|---|---|
| `Quarantines` (interface) | `Park` | it blocks the sequence now; the old name described a sink that did not |
| `Quarantined` (struct) | `Letter` | it is a queue entry with an order, not a record of a skip |
| `Failure`'s `Quarantine` (const) | `ParkSequence` | one mechanism, and a name that can coexist with the interface: a package-level `const Park` and a package-level `type Park` cannot. It also says the thing that is new — the **sequence** is what is parked |

`Progress.Quarantined` — the kernel field — keeps its name and its meaning.
`Spec.Quarantine` becomes `Spec.Park`, a field rather than a package-level name,
so it collides with nothing.

### ES-02

```go
const MaxPartitions = 1024

var ErrTopology = errors.New("projection: this topology change is not one this projection can make")

// A sequence is the set of events that must be applied in the order the log
// holds them: two envelopes with one key are ordered with respect to each
// other, and two with different keys are not.
//
// SequenceOf is total, pure and stable for the life of the log. It reads the
// envelope and nothing else — no clock, no map iteration, no process-local
// state — because the key an envelope produced in one release is the key it
// must produce in every later one. There is no error: an envelope that could
// not be assigned to a sequence has no sequence to be parked in, so a panic
// out of one halts the projection rather than failing its page.
//
// Name is recorded on every parked letter, and a redrive whose spec names a
// different sequencer than the letter carries is refused.
type Sequencer interface {
	Name() string
	SequenceOf(envelope event.Envelope) string
}

func ByStream() Sequencer                                        // the default
func Unordered() Sequencer                                       // stable, not random
func OneSequence() Sequencer
func SequenceBy(name string, of func(event.Envelope) string) Sequencer

// A fraction of the key space, and a mask rather than a modulus. A key belongs
// to the partition whose id is the low k bits of FNV-1a/32 over the UTF-8 bytes
// of its sequence key — published, because changing it moves every key, which
// is the failure a modulus has by another door.
//
// The mask is always 2^k - 1. A split takes one partition to two at mask 2m+1
// and moves no key out of the parent's half of the space. No count is stored
// anywhere: what exists is a set of checkpoint rows, one per live partition.
type Partition struct{ /* id, mask uint32 */ }

func Whole() Partition
func NewPartition(id, mask uint32) (Partition, error)
func ParsePartition(text string) (Partition, error)

func (this Partition) Matches(sequence string) bool
func (this Partition) Split() (Partition, Partition, error)
func (this Partition) Mask() uint32
func (this Partition) ID() uint32
func (this Partition) Count() int          // mask+1, for a reader; never stored
func (this Partition) Whole() bool
func (this Partition) String() string      // "3.7", and "" for Whole

// The set a host declares its topology as: every key matches exactly one
// member. A mask makes each partition correct on its own and says nothing about
// the set, which is where the two silent failures live — a forgotten member,
// whose quarter of the log is never delivered to anything, and two members that
// overlap, which are two different names, two checkpoint rows, no fence
// conflict, and every key matching both applied twice.
//
// Build your runners from Partitions(): a set that was checked is the one thing
// that makes those two unconstructible. A single runner cannot see the set, so
// a host that assembles runners by hand keeps the freedom it always had.
type Cover struct{ /* unexported */ }

func NewCover(partitions ...Partition) (Cover, error)

func (this Cover) Partitions() []Partition
func (this Cover) Count() int

// Rendered into every name a projection records through — the checkpoint row
// key and the runner name — and parsed back out of one. Built here and nowhere
// else: a name is what two processes agree on, and a composed key is a wire
// format ([[D-125]]).
//
// It has no exported fields, because Identity{Projection: "orders@2"} would
// render a string that parses back as another identity, and that string is the
// primary key of a checkpoint row, a park and a runner at once. NewIdentity
// refuses "@" and "#" in a projection name for the same reason, rather than
// escaping them: escaping would change the rendered name of a projection that
// is legal today, which moves its checkpoint row and restarts it at the origin.
// A deployment that has one renames the projection, which is a rebuild.
//
// "orders", "orders@2", "orders#3.7", "orders@2#3.7". Within event.MaxNameBytes,
// and carrying neither bracket, so it passes the kernel's identifier rule.
type Identity struct{ /* unexported */ }

func NewIdentity(projection string, generation Generation, partition Partition) (Identity, error)
func ParseIdentity(text string) (Identity, error)

func (this Identity) Projection() string
func (this Identity) Generation() Generation
func (this Identity) Partition() Partition
func (this Identity) String() string

// The same projection and generation without the partition — "orders@2". It is
// what a park and a redrive are keyed by, and the reason is a split: a
// partition-keyed park orphans its letters the moment the partition holding
// them is split, and the following events of a parked sequence then go straight
// to a handler.
func (this Identity) Whole() Identity

// The handoff, and the only topology change there is. In ONE transaction of the
// checkpoint store's backing — the caller's, crud.InNewTx being the one-line
// spelling — it reads the parent's row AND both children's through event.Track,
// writes both children at advance 1 carrying the parent's cursor byte for byte,
// and retires the parent.
//
// Drain the parent first. One that is still running halts at its next save,
// because its row is gone; that is loud and correct, and it is this framework's
// answer to a concurrent topology change.
//
// A parent with NO row is refused. A row is absent because the partition never
// ran or because it was forgotten, retired or lost in a restore, the store
// cannot tell those apart, and starting two children at the origin against a
// live read model is the failure [[D-133]] halts for. A partition that never ran
// needs no split: declare the Cover and start its members.
//
// Every decision comes from rows read inside the run, so a Unit that runs its
// work twice produces one split or a refusal, never two children at the origin.
//
// There is no Merge. A cursor may not be ordered ([[D-129]]) and two independent
// walks of one log mint different cursor bytes for one position, so neither
// "the lower of two" nor "when the two are equal" has a spelling. Fewer
// partitions is a new generation.
type SplitSpec struct {
	Checkpoints event.Checkpoints
	Identity    Identity
	Unit        func(ctx context.Context, work func(context.Context) error) error
}

func Split(ctx context.Context, spec SplitSpec) (Identity, Identity, error)
```

### ES-03

```go
var ErrParkFull = errors.New("projection: this park has no room for another letter, and the partition stops rather than skipping one")

// A parked envelope. Cause is the failure for the letter that could not be
// applied and nil for every letter parked behind it: those never reached a
// handler, which is the whole of what the queue is for.
//
// Identity is a whole identity — projection and generation, no partition — and
// it is the key of the queue this letter is in.
type Letter struct {
	Identity  Identity
	Sequencer string
	Sequence  string
	Envelope  event.Envelope
	Cause     error
	Attempt   int
}

// The queue, and the table is yours: a park is a write inside the caller's own
// transaction, which is this framework's outbox ([[D-118]]).
//
// It is keyed by the identity it is handed, which is always a whole one: a park
// keyed by a partition loses its letters to a split. Two generations of one
// projection therefore share no letters, and two partitions of one generation
// share all of them.
//
// Sequences is read once per resume, and again at the start of every pass while
// it is non-zero. While it answers zero, Holds is never called and a healthy
// projection pays nothing at all — exact rather than a bounded cache, which is
// why only this projection may park and only a redrive may remove. A letter
// inserted out of band leaves the count low until the next resume.
//
// Park bounds itself in two dimensions and the check is per sequence: a new
// sequence is refused when the queue holds its sequence bound, an existing one
// when it holds its letter bound, and a byte bound applies over the sum of
// parked payloads. Refuse with an error satisfying errors.Is(err, ErrParkFull);
// the partition then stops advancing and skips nothing.
//
// Holes answers what a cumulative counter cannot: the envelopes this generation
// parked and never applied — queued now, plus evicted without being applied. It
// is a cutover's evidence, and it falls when a redrive drains a sequence.
type Park interface {
	Sequences(ctx context.Context, of Identity) (uint64, error)
	Holds(ctx context.Context, of Identity, sequence string) (bool, error)
	Park(ctx context.Context, letter Letter) error
	Holes(ctx context.Context, of Identity) (uint64, error)
}

// The operator's half, kept apart so a Park on the hot path need not implement
// it.
//
// Claim is the rotation AND the exclusion. With an empty sequence it takes the
// one whose last attempt is oldest, so a retry loop neither starves the others
// nor loses their fairness; with a named one it takes that. Either way it takes
// it only if nobody else holds it, because two operators redriving at once
// would otherwise load one sequence twice and apply its letters out of order.
// The clock that expires an abandoned claim is yours, in your table: [[D-126]]
// rules out a store's clock and a fence, not this.
//
// SUPERSEDED BY THE PLAN'S P-8. The signature below answers a sequence name and
// carries no ownership token, which cannot coexist with the expiry rule above:
// an expired claim taken by a second caller leaves the first applying letters it
// no longer owns and releasing a grant that is not its. The shipped protocol
// answers a Claim value that Sequence, Evict, Touch and Release all carry, and a
// write whose claim no longer owns the sequence is refused with ErrClaimLost.
type Redriver interface {
	Claim(ctx context.Context, of Identity, sequence string) (claimed string, found bool, err error)
	Sequence(ctx context.Context, of Identity, sequence string) ([]Letter, error)
	Evict(ctx context.Context, letter Letter) error
	Touch(ctx context.Context, of Identity, sequence string, cause error) error
	Release(ctx context.Context, of Identity, sequence string) error
}

// Identity is whole: a redrive drains a generation, not a partition, and one
// carrying a partition is refused with ErrTopology.
//
// Effects and Generations are two of the loop's three, checked the same way and
// for the same reason: a letter carries its effect into the park, so the redrive
// that applies it is what stages it. An eviction stages nothing.
//
// EffectsAfter is the third, and NewRedrive REFUSES a non-zero one: every letter
// a redrive can drain was parked by a generation that had no barrier — New
// refuses one beside ParkSequence — so a barrier here can only suppress an
// effect that is owed, for ever. The field is present to be refused because a
// spec builder serving both doors is what copies it across.
//
// Unit and Destination carry the same tier obligation as a pass (§1.1): the
// apply, the Evict, the Touch and the staged effect are one transaction, and
// Unchecked is refused here for the same reason a park is refused there.
type RedriveSpec struct {
	Identity     Identity
	Handler      Handler
	Sequencer    Sequencer
	Park         Redriver
	Unit         func(ctx context.Context, work func(context.Context) error) error
	Destination  any
	Classifier   Classifier
	Effects      Effects
	EffectsAfter event.Position
	Generations  Generations
}

// Runs on your goroutine, on your schedule. It starts nothing: a retry that
// happened by itself is one nobody asked for, and the host wraps Any in a
// runtime.Runner if it wants one. It issues no checkpoint save — the fence is
// the loop's.
type Redrive struct{ /* unexported */ }

func NewRedrive(spec RedriveSpec) (*Redrive, error)

// One sequence, claimed, in insert order, stopping at the first letter that
// fails again.
func (this *Redrive) Sequence(ctx context.Context, sequence string) (Retried, error)

// The least recently tried unclaimed sequence, and only one.
func (this *Redrive) Any(ctx context.Context) (Retried, error)

type Retried struct {
	Sequence string
	Applied  int
	Left     int
	Cause    error
}

const (
	PhaseDegraded Phase = "degraded"   // the whole log read, and sequences are parked
	PhaseBlocked  Phase = "blocked"    // the park refused, and nothing advances
)
```

### ES-04

```go
var ErrRetired = errors.New("projection: this generation's checkpoints are gone, so nothing may be pointed back at it")

// Rendered into every name a projection records through. Zero is a projection
// with no generations at all, and renders nothing, so every projection that
// exists today keeps its name and its row.
type Generation uint32

const Ungenerated Generation = 0

// The row your read path resolves its tables through, and the one write a
// cutover is. One row per projection across all its generations — it names the
// winner, so a row per generation could not — which is why this is keyed by a
// projection name and a Park is keyed by an Identity.
//
// It is atomic for the whole declared set of tables because it is one row and
// the set is declared in your own code; this framework never issues DDL and
// never learns a table name. THE PRECONDITION TRAVELS WITH THE PROMISE: the
// switch is atomic for a reader that resolves this row in the same snapshot as
// the tables it then reads. A read path that caches the answer per process, per
// request or per connection is a legitimate choice with a window the length of
// its cache, and what it may not be called is atomic.
//
// It must live in the checkpoint store's database: the effect gate reads it
// inside the transaction that commits the advance, which is what stops two
// generations staging one envelope. That alignment is asserted rather than
// measured — the framework holds a method set and no resource — and it is
// falsified by a rollback rather than by a comparison.
//
// AND ACTIVE MUST BE A LOCKING READ — SELECT active … FOR SHARE — or run in a
// SERIALIZABLE unit. [[D-126]] leaves the isolation level to the caller, and at
// READ COMMITTED a plain read of this row and a concurrent Activate of it do not
// conflict: both commit, and a pass that read the old generation stages under a
// row that already names the new one. The obligation is the implementation's,
// the way Park's ordering obligations are.
//
// Activate is fenced on from, so two operators cutting over at once produce one
// winner and one Conflict.
type Generations interface {
	Active(ctx context.Context, projection string) (Generation, error)
	Activate(ctx context.Context, projection string, from, to Generation) error
}

// A position the retiring generation had finished with, and the number the
// arriving one must reach. A Position orders and a Cursor does not ([[D-129]]),
// which is why this is the one and not the other. It resumes nothing and is
// never turned into a cursor.
//
// It is an observation for an operator's readiness loop. Cutover does not take
// one: a barrier a caller can invent is not evidence, and a zero one would point
// every read at an empty generation.
type Barrier struct {
	Projection string
	Generation Generation
	At         event.Position
}

// The LOWEST Highest across a generation's partitions, because a partition set
// is only as far along as its furthest-behind member. Of is the generation —
// a whole identity — and each member of the cover composes one row key with it.
//
// A member with no checkpoint row has not reported and is not at position zero:
// a cover whose members are all fresh answers the origin, and one with some
// fresh and some not is refused with ErrTopology naming the member.
func Observe(ctx context.Context, checkpoints event.Checkpoints, of Identity, over Cover) (Barrier, error)

type Readiness struct {
	Reached     bool
	Behind      event.Position
	Quarantined uint64          // cumulative, and never falls
	Holes       uint64          // parked and never applied — what a cutover reads
}

// Reached says every event at or below the barrier was DELIVERED to this
// generation. Holes non-zero says its rows have holes NOW and cannot be
// compared to the live ones, which is a cutover's only evidence — it is one
// question asked of the arriving generation's park, because a park is keyed by
// a whole identity and not by a partition. A nil park is a projection that could
// not have parked anything, and answers zero.
func Reached(ctx context.Context, checkpoints event.Checkpoints, park Park, barrier Barrier, arriving Identity, over Cover) (Readiness, error)

// Retiring and Arriving are the two generations' live covers, and there is no
// Barrier: Cutover observes one from the retiring generation's own rows, inside
// the caller's unit, and checks the arriving generation against it there.
type CutoverSpec struct {
	Checkpoints event.Checkpoints
	Generations Generations
	Park        Park            // optional: without one nothing could have been parked
	Projection  string
	From, To    Generation
	Retiring    Cover
	Arriving    Cover
	Unit        func(ctx context.Context, work func(context.Context) error) error

	// A generation whose destination has holes now is not comparable to the one
	// it replaces. Set this to take it anyway, out loud. A generation that parked
	// and then redrove completely does NOT need it.
	AcceptQuarantined bool
}

// Derives its own evidence inside the caller's unit and then issues exactly one
// fenced write. A rollback is this call with From and To exchanged, and it is
// admitted only while the target's checkpoint rows still exist and it has
// reached a barrier observed from the generation now active.
//
// It moves the row. Retiring the old generation's tables and letters is a
// second, later, deliberate step, ordered against readers by the deployment:
// drop them only once every reader that could still hold the old row has
// re-resolved.
func Cutover(ctx context.Context, spec CutoverSpec) error
```

### ES-06

```go
// What an effect handler is handed, and the value that IS the capability: a
// projection Handler has no route to one. The envelopes are the ones this page
// APPLIED, past the barrier, and no others — a parked envelope is not among
// them and is not lost either: its letter carries its effect, and the redrive
// that applies it is what stages it. An evicted letter's effect is never staged.
//
// A page with no such envelope does not call Stage at all.
type Effect struct {
	Identity  Identity
	Envelopes []event.Envelope
	Attempt   int
}

// Stage, not Send. It is called INSIDE the transaction that commits the advance,
// so what it does must roll back with it: a staged job ([[D-118]]), a row in
// your own tables. An externally visible irreversible action here — an HTTP
// call, a payment, a mail, a non-transactional publish — is sent again on every
// rollback the pass can take, and a lost fence ([[D-133]]) makes that the
// expected outcome of a rolling deploy. The dial-out belongs to whatever drains
// the stage, with its own retry and its own idempotency.
type Effects interface {
	Stage(ctx context.Context, effect Effect) error
}

type EffectsFunc func(ctx context.Context, effect Effect) error

func (this EffectsFunc) Stage(ctx context.Context, effect Effect) error
```

### `Spec`, `Batch` and `State`

```go
type Spec struct {
	// … every existing field, unchanged, except:
	//   Quarantine Quarantines  ->  Park Park

	// The key two events share when they must be applied in order. Nil is
	// ByStream(), which is the aggregate id and is Axon's default too.
	Sequence Sequencer

	// The fraction of the key space this runner reads: every page is filtered
	// by it before the handler sees one, on the key Sequence answers. The zero
	// value is Whole(), which matches everything, renders nothing and calls no
	// sequencer at all. Take it from a Cover: a runner cannot see the set it
	// belongs to.
	//
	// Split is the only route from a projection that has run. A partitioned
	// runner started beside a live checkpoint row for a coarser share of the
	// same key space is refused at its first resume.
	Partition Partition

	// Rendered into this projection's recorded name. Ungenerated renders
	// nothing.
	Generation Generation

	// The queue. Required by OnPermanentFailure: ParkSequence, which is itself
	// refused outside InUnit with a resolvable Destination — the park row, the
	// read model and a redrive's Evict must be one transaction or the ordering
	// the queue exists for is not there.
	Park Park

	// The ownership row. Required beside Effects when Generation is not
	// Ungenerated, because that is the pair that admits two senders.
	Generations Generations

	// The live effects, and they are not the projection. Refused beside
	// AfterApply: an effect staged outside a unit has the window a broker
	// outage turns into a permanently lost event.
	Effects Effects

	// Suppressed for every envelope at or below it, per envelope and not per
	// page. The envelope's side of the comparison is a checkpoint column, so an
	// interrupted warm-up resumes suppressed rather than re-firing from the
	// beginning; THIS side is a constant the deployment holds, so a restart that
	// drops it stages the rest of the warm-up and nothing refuses that. Observe
	// answers the number as Barrier.At. Refused with a nil Effects, and refused
	// beside OnPermanentFailure: ParkSequence.
	EffectsAfter event.Position

	// A minimum interval between reads while draining. Zero is no pacing. It is
	// a throttle and not a concurrency cap: a rebuild is told to read slower
	// than the live generation, and the pool is what actually caps concurrency.
	Pace time.Duration
}

type Batch struct {
	Projection string
	Identity   Identity   // new: the generation and partition this page is for
	Envelopes  []event.Envelope
	Attempt    int
}

type State struct {
	Projection string
	Identity   Identity   // new
	Phase      Phase
	Progress   event.Progress
	Parked     uint64     // new: live parked sequences, zero when nothing is blocked
	Attempt    int
	Err        error
	At         time.Time
}
```

## 5.3 `event/eventtest` — one addition, and it is a widening of the store contract

`RunCheckpoints` gains a **`topology`** section. It is a section rather than a new
suite because a split is expressed entirely in the shipped `Checkpoints` surface
— no new method, no `List`, §1.3 is why — but it adds three **store
obligations**, and they are written here as obligations rather than as test
names:

1. **Three statements of one split commit atomically** in a transaction the
   caller opened: a `Load`, two `Save`s at advance 1 and a `Forget`, all or
   nothing.
2. **A save at advance 1 over a live row is refused** by the store's own fence,
   not only by the tracker's.
3. **A cursor written under one identity is read back unchanged under another.**
   A store may not bind a cursor to the name that saved it.

The third is genuinely new. Nothing in phases 1–3 forbids a store from binding a
cursor to its row's name, and [[D-129]]'s round-trip requirement is stated
through a second *value*, not a second *identity*. A store certified under phase
3 can therefore go red here with no signature having changed — so it is
**mandatory rather than capability-gated**, because a split has no other spelling
and a store that cannot do it cannot host a partitioned projection at all. What
such a store gets is a refusal it can read: `Split` against it fails its own
conformance run before a deployment ever reaches it, and the store author's note
in §7.3 names the obligation and the release it arrived in.

Gating it on a new `CheckpointCapabilities` flag was considered and rejected:
that is a kernel type, §5.1 keeps the kernel's signatures frozen, and a
capability would make "this store cannot be partitioned" a run-time discovery
instead of a conformance failure.

## 5.4 `_examples` — two additions

- `event-partitions` — four runners over one log, a `SequenceBy` that is not the
  stream, and the split performed with `crud.InNewTx`.
- `event-generations` — a `Generations` and a `Park` implemented over PostgreSQL
  with `crudsql`, a rebuild beside a live generation, the barrier loop, the
  cutover and the rollback.

Both are unpublished (`_examples` is its own module), so neither becomes a
dependency of anything.

---

# 6. What the live suite must prove

Green unit tests are not evidence for any of this. Every item below runs against
PostgreSQL 17.9 through
`FROSTGROVE_EVENTPG_TEST_DSN`, and rows are checked in the database rather than
in Go.

1. **Tier B is refused live** (§UC-132). Two pools, two transactions, one spec —
   the halt happens before the handler runs, asserted by an empty destination
   table and an unmoved checkpoint row read with `psql`.
2. **Four partitions over one log, and the `hash % N` control** (§UC-135,
   §UC-138). The positive case asserts each key's events landed in one partition
   in order; the control asserts the modulus re-partitioning reorders them, so a
   green positive case is known to be proving something.
3. **The split's three statements in one transaction** (§UC-138), with a failure
   injected between the two child writes asserting neither child row exists.
4. **A running parent halts on a split committed under it** (§UC-139), against a
   drained-parent control that halts nothing.
5. **The blocking test end to end** (§UC-146, §UC-147): `A3` never reaches the
   handler, `B` keeps flowing, and the park row and the checkpoint row moved in
   one commit — verified by a rollback that leaves neither.
6. **The fast path costs nothing** (§UC-148): `Holds` call count is zero over a
   full drain with an empty park, and rises after the first park.
7. **A full park blocks and clears** (§UC-150): `PhaseBlocked`, no advance, no
   skip; then one `DELETE` and the very next pass advances, with no restart.
8. **A redrive stops at the first repeat failure and touches no checkpoint**
   (§UC-153, §INV-094), with a recording `Checkpoints` asserting zero saves.
9. **A rebuild beside the live generation, the barrier, the cutover and the
   rollback** (§UC-158, §UC-159, §UC-161, §UC-164), with a reader held open
   across the cutover commit proving the switch is atomic for the whole declared
   set.
10. **Two operators cutting over at once** (§UC-162), driven with a gate so the
    contention is caused rather than hoped for — the shape
    `TestTwoLiveInstancesOfOneNameOverOneSchema` already uses.
11. **The retired generation stops dispatching at the cutover** (§UC-173), with
    the no-ownership-row fixture asserting both *would* dispatch. **Driven, not
    awaited:** the cutover commits while a retiring pass sits between its
    ownership read and its commit, because a cutover taken between two passes
    passes by scheduling luck and would be read afterwards as evidence the window
    is closed. Both recipes run (§UC-202) — the locking read, asserting the
    envelope is staged once and that the `UPDATE` waited; and the plain one as
    the control, asserting two senders. That pair is the evidence, and §UC-192's
    second pool is a third way to lose the same boundary rather than the only one.
12. **The interrupted warm-up** (§UC-171): killed at `K` between `M` and `N`,
    restarted, and the effect sink holds nothing from `(K, N]` — and the same
    restart with `EffectsAfter` dropped (§UC-203) asserting it stages all of
    `(K, N]`, so what item 12 proves is the checkpoint half and not a durability
    the barrier does not have.
13. **The straddling page** (§UC-172): `Stage` called once with exactly the
    envelopes past `N`.
14. **The N× read cost** (§UC-168): eight walks measured against a
    one-projection baseline, recorded as a number rather than a claim.
15. **A split of a partition holding a parked sequence** (§UC-179): the letters
    stay reachable under `orders@2`, the matching child blocks the sequence's
    later events, and the empty-park control leaves both children paying nothing.
16. **A split with no parent row is refused** (§UC-140), against the has-run
    control, with the checkpoint rows read in `psql` to prove nothing was
    written.
17. **Two operators redriving at once** (§UC-191), gated so the contention is
    caused: one claim wins, no letter is applied twice, and the sequence's order
    holds — read off the destination rows rather than off the Go values.
18. **A stage rolled back by a lost fence** (§UC-183): two instances of one
    identity, the loser's staged job row absent and its advance unmoved after the
    `ErrOvertaken`, the winner's present exactly once.
19. **A cutover that cannot be handed a barrier** (§UC-180, §UC-163): a cover with
    a member that has no row is refused; a generation that parked and redrove
    completely cuts over with no override; and one holding a live letter does
    not.

Every one of these must run twice in a row. A test that passes once and fails on
rerun is a real defect (`CLAUDE.md`), and several of these drive contention.

---

# 7. Deliverables that are not code

## 7.1 The kernel baseline

`docs/api/surface.md`'s `event` section does not move, so
`make check`'s `check-event-kernel` arm is not re-baselined and not loosened.
The `event/projection` section grows by everything in §5.2. `make api` is run and
the diff is a question for a person, as it always is.

The `Checkpoints` **conformance** contract does move (§5.3), and the baseline
cannot see it. So the widening is written where a store author reads: the
`eventtest` package doc, `docs/modules/en/eventtest.md`, and the release note.

## 7.2 Two decisions

- **D-134 — a partition is a mask, and a topology change is a handoff.** The
  mask arithmetic, why `hash % N` is corrupting rather than inconvenient, the
  five-step handoff, the absent-row halt as the concurrent-split detector, the
  refusal of `Merge` with **both** reasons ([[D-129]] and the `eventpg` cursor's
  read-time `bound`), and the N× read cost with Reject 3 as its ceiling. Links
  [[D-092]] [[D-125]] [[D-128]] [[D-129]] [[D-133]].

  Three clauses added by S1's review, each carrying its plan decision:
  **only the first start chooses a topology** (§1.3.1, plan **P-10**) — the
  resume-time refusal over every coarser ancestor, why it is a resume and not a
  construction check, and the finer direction stated as not covered because it
  would need a `List` the contract does not have and it is a merge anyway;
  **a value that carries a proof refuses its own zero** (plan **P-11**) — why
  `Count() == 0` and `Projection() == ""` are exact discriminators and no marker
  field is carried; and **`NewIdentity` spells the kernel's identifier rule a
  second time** (plan **P-12**) — because §5.1 freezes the `event` surface and an
  `Identity` is an input at five doors that never reach `event.Track`, with the
  drift pin named and with the deliberate difference stated at exactly two
  characters, `@` and `#`, and no more: the rule is not made stricter than the
  kernel's anywhere else.
- **D-135 — an effect is a separate capability, gated on a durable ownership
  row.** Why the capability is a value rather than a mode, why Marten's default
  is the safe one and Axon's is not, the `AfterApply` refusal with Reject 2's
  window as its reason, the per-envelope barrier and why per-page is wrong, the
  ownership read inside the committing transaction as the two-sender boundary
  neither source has, and the contract-not-sandbox sentence with the test that
  backs it. Links [[D-092]] [[D-118]] [[D-126]] [[D-130]] [[D-133]].

Plus an amendment recorded **beside [[D-131]]**, not inside it: the blue/green
deployment ordering (§1.5), which is a use of `Ignore` rather than a change to
the rule.

## 7.3 Documentation, in the same change as the code

- `docs/modules/en/projection.md` and its localised twin — the six contract rows
  become nine: sequences and partitions, the park, generations and effects. The
  three renames are written as renames with their reasons, because a consumer
  meets each as a compile error: `Quarantines` → `Park` (the interface),
  `Quarantined` → `Letter` (the struct), and `Failure`'s `Quarantine` →
  `ParkSequence` (the constant, spelled so it can coexist with the interface).
  The rebuild recipe changes to `Spec.Generation`, and the page says what a
  rebuild spelled as a second `Spec.Name` does not get (§1.5).
- `docs/modules/en/eventtest.md` — the `topology` section's three store
  obligations, and that a phase-3-certified store may go red on the third.
- `docs/modules/en/eventpg.md` — the N× walk cost and the multiplied `xmin`-stall
  exposure, beside the stall section phase 3 wrote.
- The flows: [[FL-038]] gains the split, the park and the cutover; a new flow for
  the effect gate. The reverse index in `docs/ai/flows/Index.md` gains every file
  this phase touches.
- `docs/ai/usecases/` — [[UC-032]] is **not** widened. The appendix says so
  explicitly and this phase's guarantees get their own contract.
- `docs/roadmaps/Roadmap.md` — ES-01, ES-02, ES-03, ES-04 and ES-06 are removed
  from the open list rather than annotated done.
- `_examples/README.md` — two rows.

---

# 8. Tensions left to the plan

Recorded rather than resolved, because each is a real choice the plan must make
and none of them changes a use case above.

1. **`Sequencer.Name()` is checked at the park and nowhere else.** A sequencer
   changed under a running partitioned projection is invisible to the checkpoint
   row. Making it visible needs a column in a table this phase does not own —
   either the kernel's `Checkpoint`, which phase 3 froze, or the application's,
   which the framework does not read. The plan may decide it stays a contract, or
   may propose the column with its own decision.
2. **`State.Parked` is a count and not a list.** Marten records what it skipped
   over (`mt_high_water_skips`, opt-in via `EnableAdvancedAsyncTracking`) so an
   operator can answer "which streams may be impacted". vv's answer is the queue
   itself, which is richer — but only for letters that are still there. The plan
   should decide whether an evicted letter's identity survives in the example
   `Redriver` and say so on the module page.
3. **The byte bound over parked payloads is the implementation's and is
   uncertified.** The two count bounds are exercised by §UC-151; the byte bound
   is stated in the `Park` contract and proved only in the example. A
   `parktest`-style harness is the shape that would close it and is not in this
   phase.
4. **`Pace` is a throttle, and Marten's budget is a governor.** They are not the
   same thing and the module page must not imply they are. If N concurrent
   rebuilds against one pool turns out to be the binding cost, the answer is the
   host's supervisor or the pool, never `event/projection`.
5. **Reject 3's shared reader is the next lever and this phase makes it more
   valuable.** N partitions × M generations is N×M walks. The plan should record
   the measurement from §UC-168 in the backlog under `## P4` so the eventual
   decision has a number rather than an intuition.
6. **`Park.Holes` is a fourth method on an interface the application writes.**
   It is one count over rows the implementation already keeps, and it is what
   made the cutover's evidence honest — but it is also the method whose wrong
   answer is invisible until a cutover admits a holed generation. The plan should
   decide whether the example `Park` derives it from the queue and the eviction
   log (two counts) or keeps a column, and the module page should say which
   guarantees rest on it.
7. **A `Cover` is checked where the set is written down and nowhere else.** A
   host that builds runners by hand is unchecked, by construction: a runner sees
   its own partition and no other. The plan may decide the `_examples` show only
   the cover-driven spelling, which is the cheapest way to make the checked path
   the obvious one.
8. **A cutover's readiness is "delivered", not "the rows agree".** `Reached` plus
   `Quarantined == 0` is the strongest statement available without ES-05's
   barrier token, and it is honest. A consumer that wants "the rows agree" runs
   the comparison UC-120 already demonstrates, and the module page should say so
   rather than let `Reached` be read as more than it is.
