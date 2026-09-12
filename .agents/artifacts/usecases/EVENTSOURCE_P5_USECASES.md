# EVENTSOURCE PHASE 5 — THE CALLER'S GUARANTEES: ES-05, ES-07, ES-08, ES-09

**Status:** specification, phase 5 of the PostgreSQL event-sourcing roadmap — the
last four appendices.
**Written against:** `event`, `event/eventmemory`, `event/eventtest`,
`event/eventpg` and `event/projection` at `1fd671b`, after phase 4; `crud`,
`jobs` and `runtime` as the accepted precedents; PostgreSQL 17.9.
**Numbering:** continues phase 4. Use cases start at **UC-204**, invariants at
**INV-108**, so a bare `UC-nnn` or `INV-nnn` is unambiguous across all five
documents.
**Sources:** the four appendices at
[`docs/roadmaps/2026-09-01-postgres-event-sourcing-roadmap.md`](../../../docs/roadmaps/2026-09-01-postgres-event-sourcing-roadmap.md)
lines 783–821, read in the original Russian; the mechanisms at the other end of
every URL in them, read at their implementations rather than their documentation
pages;
[`EVENTSOURCE_P5_STUDY.md`](EVENTSOURCE_P5_STUDY.md), which is the record of that
reading; and [`EVENTSOURCE_REFERENCE.md`](../gaps/EVENTSOURCE_REFERENCE.md),
whose **Reject** entries are binding and are not re-proposed here.

---

## 0. What this document is, and what it does not repeat

Phases 1–4 are frozen and are the input to this one. **Nothing already written in
them is restated here.** Where a rule exists it is referenced by number and the
reference is the whole of what this document says about it.

Phase 5 implements **ES-05, ES-07 and ES-08**, and delivers **ES-09 as an
accepted contract with no code** — §1.4 is the argument for that, and it is not a
deferral by silence.

**The appendices' own framing is binding on this phase**, restated once because
every section below is an application of it:

> Владельцы — опциональная event-подсистема и выбранный store, не framework
> kernel. Приложение передаёт обработчики, SQL transaction authority и
> read-model repository своего ORM; никакого обязательного ORM, DI, broker или
> межмодульного registry. Новые гарантии требуют отдельного контракта и тестов:
> нынешний [[UC-032]] ими не расширяется. DX ниже — эскизы, не существующие API.

So: every table this phase names is the **application's**, behind an interface
this framework declares and does not implement — the shape `Park` and
`Generations` (`event/projection`) already have. Nothing here starts a goroutine,
opens a transaction, or writes a log line ([[D-092]], [[D-118]], [[D-132]]).

**What phase 5 is not.** It is not more projector machinery. ES-01…ES-04 and
ES-06 built the thing that applies events; these four are what a **caller** may
conclude — that a read will not show the old state, that an append that vanished
into a broken connection can be asked about, that a version in the past can be
read back, and what an optimised load would have to prove before it may exist.
Every one of them is a promise to somebody outside the subsystem, which is why
each gets its own use case and none of them widens [[UC-032]] (§1.3, §1.4).

**Delivery policy in force.** Only `[critical]` and `[high]` findings block.
`[medium]` and `[low]` are appended to
[`EVENTSOURCE_BACKLOG.md`](../gaps/EVENTSOURCE_BACKLOG.md) under `## P5` and left
alone. A gate never proceeds silently red.

**Phase 5 moves three kernel names and widens one application contract.**
`event` gains `Repo.Digest`, `Repo.StateAt` and `ErrVersion` (§5.1);
`event/projection` gains the wait (§5.2); `event/receipt` is a new package under
`event/` (§5.3). All four are inside `scripts/event_kernel.sha256`'s reach and
need `make check-event-kernel-baseline` **in the same change as the code**.
`event/eventtest`'s closed inventory gains **nothing** — no store obligation
moves, so no third-party store's certified suite goes red (§5.4). What does move
is **one** sentence of `projection.Park`'s contract — `Holds`'s, and no other: a
wait asks `Sequences` and `Holds` and never `Holes` or `Park` (§1.1, §5.2,
§INV-110). A sentence of contract is what a signature baseline cannot see, so the
one that moves is announced in §1.1, §5.2 and §7.1 by name rather than
discovered, and the count is stated in all three places so that a second one
cannot arrive unannounced.

**One repository gate is red at HEAD and is foreign.**
`TestNoI18nPackageCostsMoreThanItsErrorSeam` (`./scripts`) fails because
`i18n/cmd/vv-i18n` reaches `github.com/go-json-experiment/json/jsontext`, which
is not on that test's allowlist. It arrived with the owner's i18n work, it is the
owner's call, and it is neither fixed nor allowlisted here. `make check` is also
red on `check-tidy` across the satellite modules, traced by
`EVENTSOURCE_P4_VERIFY.md` §1 to `56349ba` and likewise foreign. Neither is
touched, and neither is reported as green.

---

# 1. Four questions, answered before anything is specified

Each is a place where an appendix's *адаптация* paragraph makes a demand the
shipped tree cannot meet by accident. They are answered here, once; the use cases
and invariants below are the consequences.

---

## 1.1 What a caller waits ON

**The question.** ES-05 part 3: *«ждать подтверждённую stream/version либо
store-issued barrier конкретного projection generation, а не «пока lag станет
нулём». Scan checkpoint после parking не доказывает применение события. Deadline
возвращает timeout/degraded; stale-read разрешается явно. Успех даёт видимость до
барьера, не глобальную linearizability и не свежесть чужой read replica.»*

### The two designs that are foreclosed before the first line

`event/projection/doc.go:57-61` closes one of them in as many words:

> There is no head. A store that will not promise monotone visibility has no
> number that is the end of the log, so being caught up is a statement about the
> last read and never about the log: `PhaseFollowing` means the last read
> delivered nothing, and the next one may deliver events at positions a writer
> was still holding.

`eventpg` publishes `MonotoneVisibility: Unsupported`
(`docs/modules/en/eventpg.md:324`). So **"wait until lag reaches zero"** and
**"wait until the projection reports `PhaseFollowing`"** are both unimplementable
here, and the appendix's refusal of the first is not a preference — it is the
only thing the store contract permits. Marten's wait targets *"the highest event
sequence number assigned at the time this method is called"*, and the study
records what that costs: a gap left by a failed append makes the target
*"effectively unreachable"*, so an ordinary rollback turns a working wait into a
permanent timeout for every later caller. vv has no such number to reach for.

### A mark is a position that came out of a store, and a caller cannot invent one

The only number vv can compare is `Progress.Highest`, and [[D-128]] is what makes
it worth comparing:

> `Progress.Highest` is therefore a completeness watermark: once a checkpoint
> carrying `Highest = P` is saved, every event the log will ever hold at a
> position at or below `P` has already been delivered, and a read resumed from
> that checkpoint's cursor answers only positions above `P`.

Marten reached that property in 9.16.1 for one store, by transaction-evidence
gating, after a maintainer diagnosed a high water mark that jumped a reserved but
uncommitted sequence number and ignored the event **permanently** once it
committed (discussion #4953). In vv it is a kernel law rather than a store
capability, certified ungated by the conformance suite, and `Reader.checkPage`
(`event/reader.go:91`) refuses a page that breaks it for *every* store. **That is
the whole substrate ES-05 needs, and phase 5 adds nothing to it.**

So a wait's target is an `event.Position`, and the one thing that must not be
possible is a caller inventing one. `Cutover` already refuses that — *"THERE IS
NO BARRIER FIELD. A barrier a caller can invent is not evidence"*
(`generation.go:299-302`) — and it can, because it derives its own. A wait cannot:
the caller is the only party that knows which change it is waiting for. So the
target is a **minted value**, `projection.Mark`, with unexported fields and
exactly two minting doors:

- **`spec.Committed(ctx, store, commit)`** — the caller's own append, resolved to
  a position. This is the appendix's «подтверждённая stream/version». It is a
  method on `WaitSpec` rather than a free function, and §"the park question"
  is why: the park is asked under a key only the projection's own sequencer can
  answer, the spec is where that sequencer is, and a free function would have
  taken one as an argument — which is one more value a request handler can get
  wrong.
- **`MarkOf(barrier)`** — the `Barrier` `Observe` folded out of a generation's own
  checkpoint rows. This is the appendix's «store-issued barrier конкретного
  projection generation», and it is already shipped
  (`event/projection/generation.go:84-89, 114`).

**What a `Mark` carries differs by door, and the difference decides what a wait
can promise.** A `Mark` is a position, plus whatever evidence its door could
honestly attach:

| Door | Position | Sequence keys | Projection |
|---|---|---|---|
| `spec.Committed` | the position of the commit's last event, read back | the distinct keys `spec.Sequence.SequenceOf` answers for **every** envelope of the commit's own range, in the order they first appear | `this.Of.Projection()` — the spec whose sequencer answered those keys |
| `MarkOf` | `Barrier.At` | **none** — a barrier is folded from checkpoint rows and there is no envelope to ask | `Barrier.Projection` — the projection the barrier was observed from |

A mark therefore holds a position a store produced and a handful of short strings
the application's own sequencer produced; it holds **no `Envelope` and no
payload**. That is deliberate. The alternative — retaining the envelopes and
applying the sequencer at every poll — would put an application payload inside a
value the kernel hands around, would run application code once per poll instead
of once per mark, and would render that payload out of any `%v` of a struct whose
fields are unexported. `Mark.String()` answers `"[mark]"` for the same reason
`Backing` and `Authority` do.

The zero `Mark` is refused by `Wait` with `ErrSpec`, and the discriminator is
exact: positions are drawn from an identity sequence starting at 1, so
`at == 0` is *never* a real position and is exactly "not minted". That is the
same closed-door argument `Identity` makes with `Projection() == ""` and `Cover`
with `Count() == 0` — *a value that carries a proof refuses its own zero*,
[[D-140]]'s second added clause — and it needs no marker field.

**A mark carries the projection it was minted from, whichever door minted it, and
`Wait` refuses one whose projection is not `spec.Of`'s.** One rule, both doors,
the sentence `reaching` already uses: *"this barrier was observed from %q and it
is being asked about %q, which is another projection over another set of rows"*
(`generation.go:261-263`). The sentinel is `ErrSpec`, because what differs is a
name a spec chose and `errors.go`'s own rule says a name a spec names is
`ErrSpec`; the comparison is on the **projection name only**, never the
generation, because a barrier of another generation of the same projection is the
cutover case §UC-211 admits.

**It would be easy to give `Committed` the other rule, and it would be a defect.**
The argument for it is true and insufficient: a position *is* a fact about the
log, and every projection over that log reads the same one. But a `Committed` mark
carries more than a position — it carries the **sequence keys `spec.Sequence`
answered**, and those are one projection's alone. A host that runs two projections
over one log, which is the deployment §UC-204's own control stands up, can write

```go
mark, _ := orders.Committed(ctx, store, commit)   // keys: orders' sequencer's
invoices.Until = mark
vis, _ := projection.Wait(ctx, invoices)          // asks invoices' park under orders' keys
```

with every value of the right type and nothing to refuse it. The position is
global, so the census reaches; the park is asked under a key no letter of
`invoices` was ever written under, `Holds` answers false, and the caller is told
`Reached: true` for an event `invoices` parked — which is verbatim
«scan checkpoint после parking не доказывает применение события», reached by the
one route §UC-206's `Park`-nil control does not cover. A caller that genuinely
wants to wait on a second projection derives that projection's own `WaitSpec` and
calls **its** `Committed`, which costs one more `ReadStream` and asks the right
park the right question.

### `Commit` carries no position, so the map costs a read, and the read is named

`Commit` (`event/token.go:29-47`) holds the stream, the version range, the count
and the authority — and no `Position`, by design.
`Envelope.Position`'s own contract (`event/store.go:91-98`) says why: inside the
transaction that wrote it and has not committed it, the position *"is
unspecified"*, because a store drawing from a sequence has one already and a
store assigning at commit answers zero, and both are conformant. UC-032 §14 says
the same from the caller's side: *"an event's place in the log means nothing until
the append carrying it has committed."*

So `Committed` reads the commit's **own range** back after the caller's
transaction has committed — `Store.ReadStream(ctx, commit.Stream(), commit.First()-1)`,
taking envelopes up to `commit.Last()` — and the position of the last of them is
the mark. `eventpg` selects the column (`event/eventpg/read.go:305`) and the value
is drawn at `INSERT`, so it is stable. **The round trip is stated rather than
hidden**: any design that captures a position from the append is wrong by
contract, and any design that reads it afterwards costs a round trip and must say
so.

**It reads the range and not just the last event, and the reason is the park.** A
`Sequencer` is any total, pure, stable function of an envelope (`sequence.go:13-19`, `:23-26`),
so a commit of three facts can belong to three sequences — `SequenceBy` over a
field of the payload is a supported spelling and `Unordered()` gives every
envelope its own key by construction. A mark that carried only the last event's
key would ask the park about one of the three and report *applied* while the first
two sat in the queue. The cost is the same one round trip for any commit that fits
a page (`StreamPage` is 256 for `eventpg`, and a commit is a command's batch), and
one more read per page beyond that; the arithmetic is on the module page.

`Committed` has five refusals, and the second is the one that matters:

1. **An empty commit** — `commit.Empty()`. Nothing was written, so there is
   nothing to be visible. `ErrSpec`.
2. **A transaction of this store's is bound to the context** —
   `store.Transaction(ctx)` answers a valid authority. A mark minted inside the
   transaction that wrote it is a mark for an append that may still roll back:
   against `eventpg` the read succeeds, the position is real, and a `Wait` on it
   never reaches, because a rolled-back append burns its position and no
   projection will ever deliver it. This refusal is cheap, uses only the
   already-exported `Store.Transaction`, and closes the mistake structurally for
   every store. `ErrSpec`.
3. **The store does not show version `commit.Last()`** — a short or empty page.
   Two causes and the reader cannot tell them apart: the writing transaction has
   not committed, or it rolled back. `ErrUncommitted`, which is ES-07's
   `Unresolved` one level down and says so.
4. **The envelope's `Position` is zero** — a store that assigns positions at
   commit, read before the commit. `ErrUncommitted`.
5. **`spec.Sequence` is nil while `spec.Park` is not** — the mark would carry no
   sequence key and the wait would have to answer *delivered* to a caller that
   supplied a queue. `ErrSpec`, at the mint rather than at the wait, because that
   is where it can still be fixed by naming the sequencer. `WaitOf` never produces
   this spec: it takes the sequencer off the projection's own `Spec` and applies
   `ByStream()` where that is nil, which is the same default `New` applies.

Plus the store-honesty check the kernel makes on every read and this one cannot
borrow, because `Repo.checkPage` is unexported: **all three of its arms**, not its
first. No page longer than the `StreamPage` the store itself published; **every**
envelope of a page the stream that was asked for; and **every** envelope one
version above the one before it. Every, and not the first — a store answering
another stream's row in the second position mints a mark at a foreign position
carrying a **foreign sequence key**, so the park is then asked about somebody
else's event, answers no, and the caller is told `Reached: true` for a change of
its own that is sitting in the queue. That is *«scan checkpoint после parking не
доказывает применение события»* reached through the mint rather than through the
census, and it is a silent `Reached: true` on a nil error, with nothing for a
caller to branch on. The refusal is `event.ErrBackend`, because it is a store's
answer being wrong and the vocabulary is the one `event` already publishes.

### What the wait compares, and that it is `Observe`'s own fold

`Wait` polls **`surveyed`** — the unexported walk `Observe` and `Cutover` already
share (`event/projection/generation.go:159-190`). One fresh `event.Track` plus
`Tracker.Load` per member per poll, which is read-only and admissible because
`admit`'s fifth check is skipped while `loaded` is false
(`event/checkpoint.go:205-215`). Phase 5 **writes no second door onto
`Progress`**, and the rule that was previously true by accident is written down:
a waiter creates a tracker per poll and never reuses one across a save, because
`Tracker` is the writer's value and holds a fence
(`event/checkpoint.go:100-117`).

Reusing `surveyed` buys the two refusals a hand-rolled poll would lose: a cover
whose members hold rows for some partitions and not others is refused rather than
folded to the origin, and a member with no row beside a row recording that a
split retired it is refused as a handoff rather than read as position zero
(`generation.go:185-216`). Both are the difference between a barrier every
generation clears and one that means something.

The comparison is `lowest >= mark.At()` and nothing else, where `lowest` is the
**minimum** `Progress.Highest` across the cover — the only aggregate a set of
checkpoint rows has, for the reason `Observe` already states.

### The generation a wait names must be the one the read resolves to

`Wait` takes an `Identity`, which is a projection **and a generation**, and a
cutover moves which generation a read path resolves to (`generation.go:10-23`).
Both directions of naming the wrong one are silent and one of them is the exact
outcome ES-05 exists to forbid:

- the caller names the **retiring** generation, which is ahead. The wait answers
  `Reached: true`; the caller's read resolves to the **arriving** generation,
  which has not applied the change. **A stale read with a successful wait.**
- the caller names the **arriving** generation before its cutover. The read still
  resolves to the retiring one, which was current all along, and the wait burns
  the whole deadline for nothing.

So `WaitSpec` takes an optional **`Generations`**, and when it is supplied `Wait`
reads `Generations.Active(ctx, of.Projection())` twice: on the **first** poll, and
again on the poll that would otherwise answer `Reached`. An answer that is not the
generation `Of` names is `ErrGeneration`, terminal, with the `Visibility` of that
poll. The caller's recovery is to read `Active` and wait again, which is one row
read and a fresh mark it already holds.

**Twice and not every poll**, because those are the two moments the answer
changes anything: the first catches a caller that named a generation reads do not
resolve to *now* and costs it nothing instead of a deadline, and the last catches
a cutover that completed while the wait was running, which is the case that would
otherwise return a false success. Polling it in between would buy a faster
refusal for a case the caller cannot act on any sooner.

**Twice over the wait, and not on two different polls.** A caught-up deployment
reaches on poll 1 every time, and both reads land there: the first before the
park, the second after the census. The window between them is the park round trip
and the census round trip of that one poll, which is one to three live statements
against a row a deployment tool moves in a single committed transaction — so it
is the same window the later polls have, not a smaller one. Exempting the first
poll would leave the protection to the waits that were going to be slow anyway
and take it away from the path a healthy deployment takes, which is the stale read
this clause exists to forbid arriving by the common route. The cost of not
exempting it is one `SELECT` on the ownership row for a wait that reaches at once.

**What it does not close**, stated because the phase-4 precedent is to name the
window rather than imply it is shut: the row can move between the wait's last read
of it and the caller's own read of the read model. That is the same overlap window
`Cutover` already names from the other side (`generation.go:53-61`), it is the
length of the caller's own gap between the two calls, and nothing in a
`Generations` that a read path also resolves can make it shorter. A nil
`Generations` is the honest spelling for a caller that knows which generation it
is reading — a deployment tool waiting for an arriving generation on purpose —
and there INV-113's scope sentence is the whole of the promise.

### The park question, asked first, every poll — this is the point of ES-05

*«Scan checkpoint после parking не доказывает применение события.»*

A parked sequence is the one case where `Highest` is past the mark and the read
model does not hold the event. The scan advanced; the envelope went to the
queue; `Progress.Quarantined` rose and `Highest` rose with it. A wait that
compares only `Highest` returns **reached** for an event that was never applied.
That is the exact failure the appendix names, and it is the one thing a naive
implementation gets wrong.

So `Wait` asks the park **before** the census, on **every** poll, and the order
is fixed:

1. `Park.Sequences(ctx, of.Whole())` — one count. Contractually callable outside
   a unit ([`park.go:46-54`](../../../event/projection/park.go)), so it costs one
   round trip and nothing else. Zero means nothing is parked anywhere in this
   generation and the question is closed.
2. Only when that count is non-zero: `Park.Holds(ctx, of.Whole(), key)` for
   **each** key the mark carries, in order, stopping at the first true one. The
   keys are the ones `spec.Sequence` answered for the commit's own envelopes when
   the mark was minted, so a commit that spans three sequences asks three times
   and a wait over a healthy queue asks none. True is terminal: `ErrParked`,
   immediately, with `Visibility{Parked: true}`. Waiting longer cannot help; a
   redrive can.

**A mark that carries no key cannot be asked this question at all**, and that is
the `MarkOf` door: a barrier is folded from checkpoint rows, there is no envelope,
and there is no sequence the caller is waiting for — a barrier is a statement
about a whole generation. So `Wait` **refuses a barrier-minted mark beside a
non-nil `Park`** with `ErrSpec`, rather than skipping the question and answering
`Reached` to a caller that supplied a queue precisely because it wanted
*applied*. The three alternatives and why they lose:

- *Skip the park and let `Reached` mean delivered.* It is the same silent answer
  §"what success does NOT give" item 4 exists to make impossible, given to a
  caller that did everything right.
- *Ask `Park.Holes(ctx, of.Whole()) == 0` instead.* It answers a different
  question — has this generation's read model **any** hole, anywhere, at any
  position — so a queue holding an unrelated sequence would refuse a wait that is
  perfectly satisfied. It also drags a second sentence of `Park`'s contract
  outside a unit of work, which is the cost §5.2 announces for `Holds` alone.
- *Mint the barrier mark with a sequencer too.* There is nothing to apply it to.

A caller that wants to wait on a barrier over a parking projection has the shipped
pair — `Observe` then `Reached`, which is a cutover's question and takes a `Park`
inside a unit because it is one.

**Asking the park on every poll and not only once reached** is deliberate.
`lowest` is a minimum across the cover and the caller's event lives in one
partition: that partition can have parked the event while another lags, so
`lowest < mark` and `parked` are simultaneously true. A wait that asked only
after reaching would burn its whole deadline and answer `ErrNotVisible` for a
condition it could have named on the first poll.

**Exactly one contract consequence, announced rather than discovered.**

- **`Park.Holds` is widened by one sentence.** Its contract today says `Holds` and
  `Park` run *inside* the caller's unit of work (`park.go:41-50`), and that `Holes`
  is a cutover's question no pass asks (`park.go:49-50`). `Wait` has no
  unit, so `Holds` is also asked outside one, and an implementation must answer
  the committed state there. What the inside-a-unit call buys — ordering a
  redrive's eviction against the loop's blocking test — is a *pass's* property
  and is unchanged; a wait needs no ordering, only a committed answer. The
  `Sequences`-first gate limits the blast radius to deployments that have
  actually parked something: a healthy projection never reaches the widened
  clause at all.
- **And `Park.Holes` is not the second one, because a wait never asks it.** An
  earlier draft of this section put the queue's current depth on `Visibility`, and
  that is a `Holes` call from a caller's goroutine with no unit — a second widening
  of a contract third parties implement, on a path neither `make api` nor
  `check-event-kernel` can see, announced nowhere. It is dropped rather than
  announced: `Holes` is a cutover's question, the number a waiting request handler
  acts on is `Parked` and not a generation-wide depth, and `Visibility.Quarantined`
  already carries what the census read for free. **`Sequences` and `Holds` are the
  only two methods a wait calls**, and `Sequences` was already outside a unit by
  contract.

### Where a `WaitSpec` comes from, and why a request handler does not write one

A wait needs five facts the projection's own `Spec` already declares —
`Checkpoints`, `Park`, `Sequence`, the projection name and its generation — plus
the cover those rows are recorded at. Making a request handler restate them is
making it restate the projector's wiring, and three of the five are wrong in ways
nothing can catch:

- **`Sequence`.** Name another and the key is one no letter was ever parked under,
  `Holds` answers false, and the wait reports reached for a parked event.
- **`Park: nil`** beside a parking projection. It is the field's zero value, and
  §UC-206's control proves it answers `Reached: true` for a parked event.
- **`Over`.** A cover that is not the one the rows were recorded at. `surveyed`
  catches a *partial* cover; a plausible wrong one folds a minimum over the wrong
  set.

So the spelling the module page gives is **`WaitOf(spec, over)`**, which takes the
`projection.Spec` the host built its runners from and the `Cover` it built them
out of, and derives all five — including `ByStream()` where `Spec.Sequence` is nil,
which is the same default `New` applies and which a hand-written `WaitSpec` gets
wrong by leaving the field nil. It performs no I/O, starts nothing, and refuses a
zero `Cover` and a spec that names no identity. The host builds one `WaitSpec` at
the composition root and hands it to the request path, which fills in `Until` — the
one field that is the caller's own question — and calls `Wait`.

`Cutover` is the precedent for why derivation rather than declaration: it refuses
a caller-supplied barrier *because a value a caller can invent is not evidence*,
and `Identity`, `Cover` and `Mark` all exist so a caller cannot write the dangerous
value by hand. `WaitOf` is that argument applied to the last three fields that were
still plain.

**The struct stays assemblable by hand**, for the reason `Cover` already gives for
its own: *"a host that assembles runners by hand keeps the freedom it always had"*
(`cover.go:16-18`). What that host keeps is the obligation — `WaitSpec.Sequence`
must be the sequencer the projection runs, and nothing here can check it, because
`Wait` holds a `Checkpoints` and a `Park` and never a `Spec`. It is stated in the
same breath as `Sequencer`'s existing three (`sequence.go:13-21`), INV-111 is
falsified by the test that drives it wrong, and `WaitOf` is what makes the
obligation avoidable rather than merely documented.

### The verdict, the deadline, and why there is no stale-read flag

`Wait` answers `(Visibility, error)`. `Visibility` is the whole record of the
wait and is filled in on every path that made at least one poll:

| Field | What it is |
|---|---|
| `Reached` | `lowest >= mark.At()` on the last poll |
| `At` | that `lowest` — the generation's own watermark as this wait last read it |
| `Behind` | `mark.At() - At` when not reached, zero otherwise. **An upper bound and a hint**, for the reason `Readiness.Behind` already carries: a log burns a position for a rolled-back append and for an OCC loser, so the distance between two positions is not a count of undelivered events |
| `Moved` | whether `At` changed across this wait's polls |
| `Quarantined` | the cumulative durable count summed across the cover, as the census read it |
| `Parked` | one of this mark's own sequences is parked |
| `Polls` | how many polls this wait made, whether or not each of them could be read |

`Moved` is the answer to the thing Marten's own maintainers filed as a defect
(issue #3912: a bare `TimeoutException` is the same answer for a slow projection,
a halted one, a stopped daemon and a poison event). A wait that returns
`Reached: false, Moved: false` after N polls is a projector that is not running;
one that returns `Moved: true` is a projector that is behind. vv can answer that
because it is free — the census already read the number twice.

The deadline is the **caller's context**, not a field. Five exits:

- **Reached** → `(Visibility{Reached: true, …}, nil)`.
- **Parked** → `(Visibility{Parked: true, Polls: n}, err)` where
  `errors.Is(err, ErrParked)`. The census is not read on this path: `Holds` said
  true, and `Behind` is not what the caller acts on.
- **Generation** → `(Visibility{…that poll…}, err)` where
  `errors.Is(err, ErrGeneration)`, when a supplied `Generations` answers a
  generation other than the one `Of` names.
- **Deadline** → `(Visibility{…the last poll that could be read…}, err)` where
  `errors.Is(err, ErrNotVisible)` **and** `errors.Is(err, context.DeadlineExceeded)`
  — and, when the last poll could *not* be read, that poll's own refusal as a
  third `%w`, so `errors.Is(err, ErrTopology)` answers for a caller that asks.
  A caller that branches on either of the first two gets what it expects and a
  caller that asks *why* gets the reason rather than a timeout that means four
  things.
- **Unreadable** → `(Visibility{Polls: 1}, err)` — the **first** poll could not be
  made **while the caller's own context was still live**. Terminal, with the
  refusal the call answered, unwrapped and unreclassified: `ErrTopology` from
  `surveyed`, whatever `Park.Sequences` or `Park.Holds` gave, whatever
  `Checkpoints.Load` gave through `Tracker.Load`.

**When the context rule and the poll-number rule collide, the context wins**, and
the collision is the common case rather than the exotic one: a per-request budget
shorter than a round trip expires *during the first* round trip, so the poll that
could not be made is that budget running out and not the caller asking wrong. A
first poll that ends with `ctx.Err() != nil` therefore takes the **Deadline** exit
or the cancellation exit, exactly as the fourth does — and a handler written as
§UC-213 specifies gets its `ErrNotVisible` branch at the moment a deployment most
wants it, instead of a topology refusal pointing its operator at a cover
misconfiguration that does not exist. A poll that **answered** still answers: the
**Parked** and **Generation** exits are conclusions drawn from rows that were
read, and they outrank a budget that ran out while they were being read, because
a caller can act on a redrive and cannot act on a timeout.

**A failing poll is terminal on the first poll and is polled through after it**,
and the split is the whole of the rule. There is no error classification, no retry
budget and no knob — the poll number is the discriminator, and it separates the
two causes exactly:

- **A poll that never worked is the caller asking wrong.** A cover that is not the
  one the rows are recorded at, a `Checkpoints` pointing at another database, a
  `Park` that refuses outside a unit: each fails the first poll and every poll
  after it, so waiting is burning a deadline on a refusal that is already final.
  The caller gets it at once, on the round trip it was going to pay anyway.
- **A poll that stops working is the deployment moving.** `Observe`'s contract
  refuses a cover whose members hold rows for some partitions and not others, and
  a member whose row is missing beside a split's retirement row
  (`generation.go:185-216`) — both are states a **supported** operation passes
  through, and a split takes as long as it takes. A read of the queue or of a
  checkpoint row can blink; the projection's own loop treats exactly that as *"a
  read of your own table that blinked"* and retries. So the wait keeps polling and
  the deadline decides, which is what a caller asked for when it set one.

What it must not do is either plausible reading taken alone. Terminal everywhere
aborts a caller's wait on an operator's split with a topology error the request
path cannot act on. Retried-forever everywhere hides a checkpoint store that is
down behind a timeout reading as *the projector is behind* — which is Marten issue
#3912 with a new spelling, and `Moved` is this document's answer to that complaint,
not a licence to reproduce it. Wrapping the last refusal into the deadline is what
keeps the second half honest.

A **cancellation** is different and returns `ctx.Err()` bare with the zero
`Visibility`: the caller stopped asking, and UC-032 §12's rule — a cancelled
request stays a cancelled request — is the kernel's and is not bent here. It is
not bent by the first-poll rule either: a cancellation that lands *inside* a poll
is the same cancellation as one that lands between two, and the kernel already
answers `context.Canceled` with `errors.Is(err, event.ErrBackend) == false`
through `Tracker.Load`, so a wait that reclassified it would be bending a rule one
layer below it.

**The first poll is immediate.** A wait whose projection is already past the mark
must not sleep for an interval first, and a test must not need `sleep`
(ES-05 part 2). A context that is *already* done reaches no store at all and
answers `ctx.Err()`, because an expired context is not a poll.

**There is no `AllowStale` field, and that is the design.** *«stale-read
разрешается явно»* is served by the return shape: a wait that did not reach
returns a non-nil error **and** a filled-in `Visibility`, so reading anyway is
code the caller wrote — an `if errors.Is(err, ErrNotVisible) { serveStale() }` a
reviewer can see. A boolean that turns the refusal into a success is the field
every caller ends up passing, after which the wait's name is a lie. Marten ships
`NonStaleDataTimeoutMode.ReturnStaleData` and its own page warns *"you may need
to be both cautious with using this in general"*; vv makes the caution structural
instead.

**Polling cost is named.** One `Sequences` count plus one `Load` per cover member
per poll. `Every` defaults to 50 ms, so a four-partition cover costs
`5 × 20 = 100` checkpoint reads per second per waiting request. Two reads of the
ownership row over the whole wait when `Generations` is supplied, and one `Holds`
per sequence the mark carries per poll **only** while the queue is non-empty — a
healthy projection pays neither. A deployment with many concurrent waiters raises
`Every`; the module page carries the arithmetic rather than leaving it to be
discovered under load. `Ticks` is `runtime.Ticks`, so a test drives the interval
without sleeping.

### What success does NOT give, stated plainly

1. **Not global linearizability.** It is a statement about **one** named
   projection generation over **one** named cover, resolved against **one**
   checkpoint store. A second projection of the same log, a second database, a
   second generation and anything outside the cover are all unaddressed.
2. **Not another replica's freshness.** The wait reads the checkpoint rows
   through the context it was given. If that context reaches a read replica, the
   wait reports that replica's view, and the read the caller then performs may go
   somewhere else again. Visibility up to the mark holds for a reader that
   resolves the read model on the authority the projection wrote it through, and
   for no other reader.
3. **Not "the projection is caught up".** There is no head. `Reached` says the
   watermark passed the mark and says nothing about anything above it.
4. **Not "the event was applied", unless the park was asked.** With `Park` nil,
   `Reached` means *delivered* — and delivered is not applied for a projection
   that parks. A projection with a queue must supply its `Park` here or the wait
   answers a question the appendix says it must not. `WaitOf` is what makes that
   a derivation rather than a thing to remember, and a barrier-minted mark cannot
   be asked the question at all, so it is refused beside a `Park` rather than
   quietly answered at this weaker level.
5. **Not "the generation I am about to read".** With `Generations` nil, `Reached`
   is about the generation `Of` names and the caller is the only party that knows
   whether reads resolve to it. With one supplied, the wait refuses a generation
   that is not the active one at its first and last poll — and the gap between
   that last read and the caller's own read is the cutover's named overlap window,
   seen from the read path's side.

### What is deliberately not done

- **No background poller, no cached progress, no pre-warming.** A wait runs on
  its caller's goroutine, is bounded by its caller's deadline, and costs nothing
  when nobody is waiting. Anything continuous would be a `runtime.Runner`
  ([[D-092]]) and `scripts/extensions_test.go`'s `startsNothing` arm forbids a
  `go` statement in any non-test file of `event/projection`.
- **No `Cursor` comparison, ever.** [[D-129]]: a checkpoint is a store-minted
  cursor, never a position. The wait compares `Progress.Highest`, which is a
  `Position` and legitimately comparable; nothing here orders, resumes from or
  persists a `Cursor`. `Barrier.At`'s own comment (`generation.go:66-73`) is the
  precedent and the rule.
- **No restatement of `Highest` as "the last position of the page".** [[D-128]]'s
  `What it forbids` names that as the sentence that destroys the promise — *"It is
  the same number and a different promise"* — and a wait's documentation is
  exactly where it happens by accident. INV-108 is falsified by a doc check.
- **No `FetchLatest`.** Marten's other road catches up *in the read*, for one
  stream's aggregate. It cannot serve a multi-stream read model, which is the
  only case worth waiting for, and for the single-stream case vv already has
  `Repo.Load`. Recorded because a reviewer will raise it.
- **No log line, span or metric.** [[D-132]] forbids `event/projection` all
  three, and a wait that times out therefore reports through its return value and
  nothing else. `Visibility` is that report.

---

## 1.2 What an operation receipt is, and what it can honestly answer

**The question.** ES-07 part 3: *«operation identity + fingerprint + диапазон
событий записываются рядом с append в той же caller-owned SQL-транзакции. Повтор
проверяет прежнюю квитанцию; другое содержимое с тем же ключом — конфликт.
Отсутствующая квитанция не доказывает rollback, пока исходная транзакция не
разрешилась. Retention квитанций ограничивает окно проверки; framework не
переигрывает доменное решение.»*

### The window this exists for is not `Append`'s

`event/eventpg/classify.go:27-39` is more decisive than the appendix credits. On
the **joined** path — an append inside the caller's own transaction, which is the
only path ES-07 is about — a failed statement is `NotWritten`, because *"a
statement that failed leaves a transaction PostgreSQL will now refuse to commit"*.
`ErrUncertain` from `Append` therefore essentially does not arise inside a
caller's transaction.

**So the uncertainty ES-07 exists for lives entirely in the caller's own
`COMMIT`, which vv never issues** ([[D-118]], [[D-126]], `Repo.Within` opens
nothing). That reframes the whole feature and is the sentence the rest of this
section is built on: **a receipt is not a lookup of vv's append, it is a lookup
of the application's transaction.**

### Where it lives, and why it is the application's table

A receipt must be atomic with the append, and [[D-118]] says what that means:
*"'Atomic' across two handles is a sentence with no meaning."* So the row lives
in the event store's own database, written through the caller's own transaction,
which is [[D-118]]'s rule rather than an exception to it.

It does **not** live in `eventpg`'s schema. It is the application's table behind
an interface this framework declares and does not implement — the shape `Park`
and `Generations` already have, and the shape the appendix's own framing demands
(«Приложение передаёт… read-model repository своего ORM»). That choice buys three
things:

- `eventpg` stays at `SchemaVersion = 2`; no fifth table, no migration, no
  profile decision ([[D-101]]/[[D-127]]), and a deployment that does not want
  receipts deploys nothing.
- `eventmemory` and any third-party store get receipts for free, because the
  mechanism is not the store's.
- The atomicity becomes a **checked** property rather than a hope: `Claim`
  compares `Ledger.Transaction(ctx)` against `Store.Transaction(ctx)` with
  `Authority.Same` and refuses unless they are one transaction. That is phase 4's
  `checkUnit` (`event/projection/pass.go:932-957`) reused for a second purpose,
  and it is what makes "beside the append, in the same transaction" enforceable
  rather than documented.

The package is **`event/receipt`**. The naming hazard the study flagged is real —
`event/token.go:23`, `event/eventmemory/doc.go:24` and
`event/eventmemory/transaction.go:35-36` use "receipt" for `event.Commit` — and it
is closed at the cheapest point: those **three comments** are reworded to say
"commit" or "commit token", and `event/eventtest`'s local variables named
`receipt` are left alone, because they are locals in a package that never imports
this one and renaming eleven identifiers would put a large no-behaviour diff into
the baseline re-record beside the real changes. Renaming the package instead
(`event/operation` was the candidate) was costed and refused: every call is an
operation, while a receipt is specifically the durable record of one, and the
owner's own DX sketch names it.

### What makes two appends "the same operation"

**The key, and the key is the caller's.** `receipt.Key` is minted before the
command from whatever the caller's own request identity is — an idempotency
header, a workflow activity id, a message id — and it travels with the retry.
This is `jobs.ProducerIntent` (`jobs/identity.go:102-113`) one subsystem over and
the port's `IDEMPOTENCY_KEY` in the reference.

The framework **never derives it**. A key derived from the payload would make two
genuinely different commands one operation — two identical "credit 10" are two
operations and both must land — and a key derived from a request id is the
caller's to supply. `Key.String()` renders `"[operation key]"` and never the
value, following `ProducerIntent`'s precedent and UC-032 §11.

**This is why a receipt is stronger than the mechanism the appendix cites.**
KurrentDB's idempotence is a property of `(stream, expectedVersion, the ordered
set of event ids)`: the retry must present the **same expected version** as the
original, so a caller that re-loads after the uncertainty and retries at the
*new* version has left the window and the events are appended again — reported
repeatedly by users and working as specified. A receipt keyed on the caller's own
key has no such window, and that is the whole argument for the appendix's
adaptation. **The next section is where that sentence is either kept or thrown
away**, because a fingerprint that covers the expected version reinstates the same
window under a different name.

### What the fingerprint covers

SHA-256 over a **length-prefixed** encoding of, in this order:

1. the stream, as `event.Compose(family, key)` — the wire format, [[D-125]];
2. for each record in the batch, in order: the type name, the revision, and the
   payload bytes.

Length-prefixing is the nuance, and it is [[D-125]]'s own argument: without it
`"a"+"bc"` and `"ab"+"c"` digest alike and two different operations share a
fingerprint.

**The expected version is NOT in it, and this is the load-bearing decision of
ES-07.** An earlier draft of this document put it in, on the strength of the
study's nuance ES-07/3 — *an idempotence claim is only as strong as the
concurrency check it is anchored to* — and that draft's mechanism never fires in
the scenario the appendix wrote it for. Worked, because the argument is only
convincing at this resolution:

> A payment handler mints key `K`, loads at version 5, decides, digests, claims,
> appends versions 6–7, completes, and the transaction commits. The connection
> dies before the response. The client retries with `K`, in a **new process**,
> holding nothing but the key. The handler loads — the store answers version 7,
> because the first attempt committed — decides the same facts, and digests. With
> the version inside, `F₂ ≠ F₁`, the claim answers `Collided`, and the caller
> refuses its own operation under a sentinel whose own documentation says *the key
> was spent on another operation*.

The public surface mints an `At[S]` exactly one way, `Repo.Load`, and a load after
the first attempt committed answers the moved version. So «после потери соединения
на commit узнать, записана ли именно эта операция» is unanswerable by design: the
one input the retry cannot reproduce was made part of the identity. That is
KurrentDB's window — *"the retry must present the same expected version as the
original"* — reinstated, and with a worse ending, since KurrentDB appends twice
while this refuses.

**What the version was said to be buying, and why it was not.** The reference's
weak side is `ExpectedVersion.Any`, which weakens **the append itself**: the write
is admitted at whatever version the stream holds, so a duplicate can land. Nothing
here does that. `AppendRequest.Expected` still has no "any version" spelling
(`event/store.go:68-74`), `Repo.Append`'s optimistic-concurrency check is
untouched, and a `Repeated` verdict **appends nothing at all**, so there is no
write for a weak anchor to weaken. The anchor is not lost either: it is
**recorded** — for a receipt that carries a range, `First - 1` is the version the
first attempt was decided at, which a `Resolve` shows and an operator can read. (A
receipt with no range recorded no anchor, because nothing was appended to anchor;
`Version` is a `uint64` and the subtraction is one a reader does, not one this
package publishes.) What leaves is the version's participation in the *identity
comparison*, and that is the only thing that was breaking the feature.

**What the version would have caught, named exactly.** One case, and only one: the
same records, under the same key, decided at two different versions. Under this
document's own definition of an operation — *the key is the caller's own identity
for one operation, and two identical credits are two operations that carry two
keys* — that is a retry, which is what it is now reported as. A caller that
genuinely wants both writes has said so with two keys, and if it reuses one key
for two different decisions the records differ and `Collided` fires on the
records, which is where «другое содержимое с тем же ключом — конфликт» actually
puts it.

**What else it does not cover, and why.** Positions and recorded instants, because
the store assigns them and the caller cannot reproduce them on a retry. The key,
because the key is the lookup and not the content. The backing, because a
backing's identity is a live handle (`event/backing.go:10`) that does not survive
a process and therefore cannot be a digest input — what binds a receipt to a log
is the table it is in, which is [[D-118]]'s one-database rule.

**The fingerprint is over the whole range, not per event.** The study's nuance
ES-07/2: KurrentDB's partial-overlap case (`{A,B,C}` then `{C,D,E,F}`) is its own
answer, a `[bad request]`, distinct from both "already done" and "version moved",
because the two operations were never the same operation. A whole-range
fingerprint reproduces that for free; a per-event comparison does not.

### The obligation that makes the whole mechanism fire: a decision must be byte-reproducible under one key

The fingerprint is over *the payload bytes*, which are the caller's codec's
output. So the mechanism has a precondition the framework cannot check and must
state, in the shape INV-111 uses for the sequencer: **under one operation key, the
same domain decision must encode to the same bytes, in every process and at every
version.** A codec that records `time.Now()`, a freshly minted UUID, a map
serialised in Go's iteration order, or anything else not byte-stable produces a
different fingerprint on every attempt, and for that aggregate *no retry ever
matches*.

Three things make this liveable and they are all stated on the module page:

- **The failure is fail-closed and loud, not silent and wrong.** A drifting
  fingerprint answers `Collided`, which is a refusal with a published sentinel —
  never a duplicate append and never a false "already done". A deployment whose
  codec is not stable sees every retry refused, which is an alarm on the first
  retry rather than a corruption discovered later. §UC-247 pins that shape.
- **The discipline is ordinary.** A fact that needs an instant or an id takes it
  from the command, from the state, or from the operation key itself — which the
  caller has on the retry and `time.Now()` is not. This is the same rule
  `Sequencer` already carries (*"no clock, no map iteration, no process-local
  state"*, `sequence.go:14`), for the same reason, one subsystem over.
- **A store-assigned value is never in the bytes.** Positions and `recorded_at`
  are the store's and are already excluded, so the ordinary sources of drift are
  the caller's own and are under the caller's control.

Deriving the fingerprint from the domain command instead was reconsidered here and
still refused: it is a fingerprint over a thing the store never sees, so two
commands that encode identically would be two operations and a codec change would
silently re-key every in-flight retry.

### One key covers one append, to one stream

`ClaimSpec` carries one `Stream`, `Receipt` carries one `First`/`Last` pair, and
`Repo.Digest` is a method on one typed `Repo` over one `At[S]`. The framework
supports two `Repo.Append` calls to two aggregates in one caller transaction —
that is what [[D-118]]'s one-transaction rule buys — so the question has to be
answered rather than left to the shape: **an operation key covers exactly one
append, to the stream the claim names.**

What a caller with two aggregates does instead is **one key per append**, and it
loses nothing: the two claims, the two appends and the two completions are in the
caller's one transaction, so they commit together or not at all, and a retry
claims both and is told `Repeated` for both. The atomicity was never the receipt's
to provide — it is the transaction's.

Two of the three ways to get this wrong are refused structurally, at `Complete`:

- a commit whose `Stream()` is not the claim's — the caller completed one claim
  with another aggregate's commit;
- a second `Complete` on one `Held` — which would otherwise overwrite the range,
  so an operation that appended twice would record one.

The third cannot be refused and is an obligation: a caller that claims for stream
A, appends to A **and** to B, and completes the claim with A's commit leaves a
receipt that is honest about A and silent about B. A second operation under that
key sharing A's batch and differing at B compares equal and answers `Repeated`,
appending nothing and reporting done. Nothing in `receipt` can see the second
append — `event` does not know this package exists and will not be made to — so it
is stated beside the byte-reproducibility obligation, and §UC-250 pins the hole
with an inverted control in the `gate_relscope_test.go` shape: the one-key arm
asserts the wrong answer **is** there, so the day something closes it the control
fails and says the positive cases now prove something else.

### `Repo.Digest` — the one kernel addition, and why the caller cannot do it

The bytes that will be written are not reachable from outside `event`.
`Change[S]` carries an encoded payload in unexported fields and `Repo.records`
(`event/repo.go:175-192`) is where a batch becomes `[]Record`. So a caller cannot
fingerprint what it is about to append, and a fingerprint over something else —
the domain command, say — is a fingerprint over a thing the store never sees.

`Repo.Digest(at, changes...) ([32]byte, error)` runs the first four of `Append`'s
six steps — the token's key, every change's stream and aggregate, each change's
own carried refusal, and the store's bounds — and then digests. It **issues no
store call, opens nothing and writes nothing**, and it answers the same refusals
`Append` would, which gives a caller that digests first the useful property that
a malformed append is refused before anything is claimed.

Alternatives considered and refused: putting the claim inside `Repo.Append`
(the appendix forbids it by name — *«не скрытый retry Store.Append»* and
*«Store.Append намеренно не дедуплицирует»*); and a caller-supplied content
digest (weaker, and it puts the one part of the mechanism that must be exact into
the place the framework cannot see).

### The order: claim, decide, append, complete — and the order is owned by a call, not by a paragraph

The published spelling is one call. `receipt.Once` opens nothing, starts nothing
and runs on the caller's goroutine inside the caller's transaction; it claims,
runs the work **only** when the claim took the key, and completes with the commit
the work answers:

```go
key, err := receipt.NewKey(requestID)              // before the command

err = crud.InNewTx(ctx, source, func(ctx context.Context) error {
    state, at, err := repo.Load(ctx, id)
    changes := decide(state)                        // the domain's, not this framework's
    digest, err := repo.Digest(at, changes...)
    print, err := receipt.NewFingerprint(digest)

    held, err := receipt.Once(ctx, receipt.ClaimSpec{
        Ledger: ledger, Store: store, Key: key, Fingerprint: print, Stream: at.Stream(),
    }, func(ctx context.Context) (event.Commit, error) {
        _, commit, err := repo.Append(ctx, at, changes...)
        return commit, err
    })
    if err != nil { return err }                    // ErrCollision, ErrIncomplete, a store's refusal
    answer = held.Receipt()                         // the range, written by this call or found by it
    return nil
})
```

**What `Once` owns is exactly the part a paragraph was owning before**: the claim
happens before the append, the append happens only on `Recorded`, the completion
happens in the same transaction as the append, and each of those is now a place in
the control flow rather than a step a caller remembers. The load and the decision
stay outside it, because the fingerprint is over the decision and there is nothing
to claim under until it exists — and because the hazard the order exists for is an
append before a claim, never a load before one.

A repeat therefore costs the load, the decision, the digest and **two statements
that write nothing** (§1.2), and appends nothing: `held.Verdict()` is `Repeated`, the work was never called,
and `held.Receipt()` carries the range the first attempt wrote. `Once` returns the
same `Held` the open-coded form returns, already completed, so a second
`Complete` on it is refused rather than overwriting anything.

**The open-coded form stays**, because a caller may want to answer its client
differently for a repeat than for a first write, and because a framework that
publishes only a combinator publishes a wall as soon as somebody needs a step in
the middle: `Claim` → switch on `Held.Verdict()` → `Repo.Append` → `Held.Complete`
is the same four steps with the order in the caller's hands. What it does not have
is an ignorable refusal — `Claim` answers a **non-nil error** for both `Collided`
and an unresolved prior claim (§"the three verdicts"), so the worst code that
compiles,

```go
held, err := receipt.Claim(ctx, spec)
if err != nil { return err }
at, commit, err := repo.Append(ctx, at, changes...)
```

refuses the collision instead of spending a stranger's key, and the one verdict
still ignorable is `Repeated`. That one is caught one call later: `Held.Complete`
refuses a `Held` whose verdict is not `Recorded`, the caller's transaction carries
the refusal, and `crud.InNewTx` rolls the second copy of the events back. **The
transaction is the enforcement**, which is the only enforcement this package could
honestly have — it opens none of its own.

**The claim comes before the append and this is not cosmetic.** A receipt written
only *after* the append is almost safe — two writers at one expected version
cannot both be admitted, so the OCC loser never reaches its receipt — but two
retries at *different* expected versions (a retry after a re-load, which is
exactly what a caller does when it does not know the outcome) both append and
then collide at the receipt, with both sets of events already in the log. Too
late is not a failure mode a receipt may have.

**And a `Claim` issued after a `Repo.Append` in the same unit is that failure,
with nothing in this package able to see it.** `receipt` cannot ask whether an
append has already happened on this context: `event` does not import it, `Repo`
publishes no such question, and adding one would put the kernel in the business of
tracking a subsystem it does not know about. So the misordering is refused by
construction in `Once` — the work cannot run before the claim, because `Once` is
what calls it — and is an obligation in the open-coded form, pinned by §UC-251
with the inverted control that asserts the events land twice.

**The retry protocol, end to end, for a retry that arrives holding only the key.**
This is the flow ES-07 exists for, and it is stated once here so that no use case
has to imply it:

1. The retry arrives in a new process with the key and no token, no version and no
   state.
2. It opens its own transaction, loads — the store answers the **moved** version,
   and that is fine — decides the same facts, and digests. The digest covers the
   stream and the records and not the version, so it equals the first attempt's.
3. It calls `Once` (or `Claim`) with the same key, the same fingerprint and the
   same stream. The ledger holds the row the first attempt committed: the claim
   inserts nothing, reads that row, and the prints compare equal.
4. The verdict is `Repeated`. The work never runs, nothing is appended, and
   `held.Receipt()` carries the range the first attempt wrote.
5. The caller answers its client from that range. Its own transaction commits or
   rolls back with no durable effect either way, because it wrote nothing.

The one thing the retry must reproduce is **the fingerprint**: the same key, the
same stream, the same records, byte for byte (§"the obligation that makes the
whole mechanism fire"). It is reproduced from a fresh decision at whatever version
the store now holds, which is the only thing a new process can do, and which is
exactly what the version-in-the-digest design made impossible.

`Ledger.Claim` is `INSERT … ON CONFLICT (key) DO NOTHING` **followed by a
`SELECT` of the same key, in that order, in the caller's one transaction**, and
the serialisation is the primary-key index's. PostgreSQL's speculative-insertion
path makes `DO NOTHING` **wait** on a conflicting uncommitted tuple and then
insert or not depending on how that transaction ended. Two concurrent
transactions racing one key therefore serialise at READ COMMITTED: the loser
blocks until the winner commits, its `INSERT` reports zero rows, and its `SELECT`
— a second statement, and therefore a **second snapshot** — reads the row the
winner committed. *There is no window in which both append*, and that sentence is
the one property that does hold at every level: what the level changes is whether
the loser repeats or rolls back, never whether it may append.

**The ordering is the mechanism and the statement count is not.** Measured
against PostgreSQL 17.9 (§1.2.1): the `SELECT`-**first** spelling is the defect —
its `SELECT` runs before the block and sees nothing, so the caller decides it is
first and appends, and the `INSERT`'s `0` arrives after the decision. And a
single statement that tries to do both — a CTE whose `INSERT … DO NOTHING` feeds
a `UNION ALL` branch reading the same table — **cannot work at READ COMMITTED**,
because the whole statement shares one snapshot taken before the winner
committed and the speculative wait does not refresh it: the loser gets *zero
rows*, which is neither a win nor a repeat. The read must be a statement of its
own.

**The two stricter levels do not block; they abort.** At REPEATABLE READ and
SERIALIZABLE the loser's `INSERT` waits on the index and then raises SQLSTATE
**`40001`** — *could not serialize access due to concurrent update* — which kills
the caller's whole unit: claim, append and completion roll back together. Nothing
is half-written, nothing is appended twice, and **the retry answers `Repeated`**,
because by then the winner's row is in the retry's own snapshot. [[D-126]] is
respected in the sense that matters — *this framework chooses no isolation level,
and none of these paths sets one* — but the mechanism is **not** level-independent
and this specification no longer says it is. 40001 is already `errs.Retryable()`
in this tree (`event/eventpg/classify.go:69-79`), and the retry is the caller's:
`crud.InNewTx` gives the caller the loop, and a deployment that runs its commands
at REPEATABLE READ owes one.

**And that gives the cleanest possible statement of the split:** `Claim` never
answers `Unresolved`, because the index makes it wait — the row it sees is a row
some transaction committed. That is also why `ErrIncomplete` is not the same state
under another name: `Unresolved` is an **absent** row, which may still arrive;
`ErrIncomplete` is a **present** row whose claim nobody resolved, which never
will. `Unresolved` is `Resolve`'s alone.

The `INSERT`-then-`SELECT` spelling is the `Ledger` implementation's obligation
and is given in full on the module page and in the example:

```sql
-- 1. The claim. Blocks on the primary-key index against a concurrent claimant.
INSERT INTO receipts (key, fingerprint, family, stream_key, recorded_at)
VALUES ($1, $2, $3, $4, statement_timestamp())
ON CONFLICT (key) DO NOTHING;

-- 2. The read, in the same transaction and never before the insert. At READ
--    COMMITTED this takes a fresh snapshot, which is what lets it see the row
--    the winner committed while this transaction was blocked.
SELECT key, fingerprint, family, stream_key, first_version, last_version, complete, recorded_at
  FROM receipts WHERE key = $1;
```

`won` is the **first statement's affected-row count**: one is a win, zero is a
repeat. It is not derived from the row the `SELECT` returned, and it is not
`xmax = 0`.

### 1.2.1 Why not the one-statement `DO UPDATE` form, which also works

`INSERT … ON CONFLICT (key) DO UPDATE SET key = receipts.key RETURNING …,
(xmax = 0) AS won` **is** one statement and **does** answer the loser correctly
at READ COMMITTED — measured, not assumed. It is refused anyway, and the reason
was measured too: `DO UPDATE` takes a row lock on the winner's receipt, and a
claim runs inside the caller's unit of work, so the **repeat holds that lock for
the whole of its own command**. A third presentation of the same key then blocks
on a caller that is doing nothing with the row.

| Spelling, loser's side | READ COMMITTED | REPEATABLE READ / SERIALIZABLE | a third caller, while a repeat holds its unit open 4 s |
|---|---|---|---|
| CTE, `DO NOTHING` + `UNION ALL` | **`(0 rows)`** — neither win nor repeat | `40001`, rollback | — |
| `DO UPDATE … (xmax = 0) AS won` | the held row, `won = false` | `40001`, rollback | **blocked 3035 ms** |
| **`DO NOTHING`; then `SELECT`** | `INSERT 0 0`, then the held row | `40001`, rollback | **34 ms** |

PostgreSQL 17.9, two `psql` sessions, the winner holding its transaction open 3 s
so the overlap is certain; the loser blocked 2037–2040 ms on every correct arm;
one row survived for the key on every arm and it was always the winner's. The
retry after a `40001` answered the held row at both stricter levels.

Two smaller reasons, recorded so the choice is not re-litigated: `DO UPDATE`
writes a dead tuple and fires row triggers on **every** repeat, which is the load
a retry storm produces; and its discriminator is a system column, while
`DO NOTHING`'s is the affected-row count every driver and every dialect already
has. The one thing the two-statement form gives up is a window between the two
statements in which a retention sweep could delete the row the `SELECT` is about
to read — which would surface as `won == false` beside no receipt and be refused
with `ErrLedger`. A horizon that is shorter than the age of a row written
milliseconds ago is not a horizon, so the window is closed by §"retention"
rather than by the statement.

**`recorded_at` is `statement_timestamp()` and never a parameter**, which is
`eventpg`'s own choice for the same column (§1.3) and is what keeps every instant
in the ledger on one clock. A `Receipt` handed to `Ledger.Claim` therefore carries
a **zero** `RecordedAt` on the way in and the stored instant on the way back; a
ledger that takes it from its own process clock has made the horizon a comparison
between three clocks instead of two, and §"retention" is where that costs
something.

`held.Complete(ctx, commit)` is the `UPDATE` that writes the range —
`commit.First()`, `commit.Last()` — onto the row the claim took. Two statements,
one transaction, and a rollback takes both back.

### The three verdicts of a claim, and the refusal that is not one of them

| Verdict | When | What the caller does |
|---|---|---|
| `Recorded` | no row existed; this claim took the key | decide, append, complete |
| `Repeated` | a row exists, it is **complete**, and its fingerprint compares equal | **do not append.** `held.Receipt()` carries the range the first attempt wrote, which is the answer to the original request |
| `Collided` | a row exists and its fingerprint differs | nothing: `Claim` itself answers `ErrCollision`. The key was spent on another operation |

`Collided` is the appendix's «другое содержимое с тем же ключом — конфликт», and
it is fail-closed on purpose: the port's `CommandGateway.kt:181-195` rejects a
redelivery routed to a different aggregate rather than returning the unrelated
aggregate's state, and a receipt whose fingerprint covers the stream reproduces
that and more.

**`Collided` travels as an error and not only as a verdict.** `Claim` returns
`(Held, error)` with `errors.Is(err, ErrCollision)`, and the `Held` still carries
the verdict for a caller that wants to branch. The reason is DX law rather than
taste: a verdict is a value it is legal to discard, and the worst code that
compiles — `held, err := Claim(...)`, `if err != nil`, then `Append` — must not be
the code that spends somebody else's key. `jobs` returns its three-way
`PlacementOutcome` from a call that *performs* the placement, so there is nothing
left to ignore; here the placement is the caller's next statement, so the refusal
has to be in the channel the caller is already checking.

**And a fourth state of the row is a refusal, not a fourth verdict.** A row that
exists and is **not complete** is a claim that committed without a resolution: the
caller returned early without rolling back, or it appended and never completed, or
an implementation broke the atomicity rule. `Claim` cannot conclude anything from
it — the events may be in the log or may not, and the row is the same either way —
so it answers `ErrIncomplete` and concludes nothing, rather than reading the
completed-repeat branch and telling a retry *already done* out of a row whose range
is `0–0`. The recovery is `Resolve`, which names the same state as `Incomplete`,
and then a human: this is a defect report, and a retry that is silently told
success is how it stays undiscovered.

**A decision that yields no changes is not that state**, and it has its own
spelling so that it cannot become one. A domain that answers *"the order is
already paid, there is nothing to do"* appends nothing, and `Repo.Append` with an
empty batch returns an **empty `Commit`** — the token's stream, `First` 0, `Last`
the version it went in at, `Count` 0 (`event/token.go:29-38`). `Held.Complete`
takes that commit and writes `complete = true` with a zero range, so the row says
*this operation is finished and it wrote nothing*, and the next retry is told
`Repeated` with an empty range, which is the true answer. Under `Once` this needs
no thought at all: the work returns the empty commit and the completion happens
anyway. **`Complete` is not optional on any path**, and a claim that reaches its
transaction's commit without one is the defect above rather than an ordinary
outcome — which is what keeps §"`Resolve`'s" `Incomplete` an honest defect report.

An empty commit is also the one `Complete` cannot check the authority of: an
append that wrote nothing was written through nobody's transaction and carries the
invalid `Authority` by design (`event/token.go:29-38`). The stream check still
runs, the transaction the completion is issued on is still checked, and the doc
says which check is absent and why rather than leaving the gap to be found.

**The framework does not replay the domain decision.** On `Repeated` the caller
returns what the receipt records; it does not re-propose the same facts at
whatever version the store now holds. UC-032 §8 is the boundary:
*"Recovery is a fresh load and a fresh decision — never re-proposing the same
facts at whatever version the store reports."* A `Resolve` that answered "here is
what you wrote, append it again" would be exactly that re-proposal.

### `Resolve`, and the hard clause

`Resolve` is the **other** door: a different connection, after a lost response,
asking *did operation K happen?* It writes nothing, joins nothing, and answers
four standings.

| Standing | The row | What it means |
|---|---|---|
| `Found` | present, complete | the operation certainly happened, and here is the range — which is empty when the decision wrote nothing |
| `Incomplete` | present, not complete | the claim committed and the completion did not. **Not an ordinary state** — one transaction holds both, and a decision that wrote nothing completes too — so it names a caller that claimed and then returned without completing, or an implementation that broke the atomicity rule |
| `Unresolved` | absent | **nothing may be concluded.** The writing transaction rolled back, or it is still open |
| `Expired` | absent, and the ledger's horizon is after the instant the caller says it issued the operation | the framework does not know and will never know |

**`Resolve` refuses to run where its own answer would be a lie**, and this is the
mirror of the refusal `Committed` makes one section over. A `Resolve` issued on
the claiming transaction's own context reads that transaction's **uncommitted**
row and answers `Found` for an operation that can still roll back — and "resolve
first, then decide, in one unit" is an ordinary retry shape, so it is reachable by
a caller doing something reasonable. So: if a transaction of the store's **or** of
the ledger's is bound to the context, `Resolve` answers `ErrSpec` before it reads
anything. `ResolveSpec.Store` is there for exactly that question and for no other
— it is why UC-221's control asserts *zero calls to the event store other than the
transaction question* — and the ledger's own `Transaction` closes the case where
the two are different handles and only the ledger's is bound. That is the whole
of the placement contract: **`Claim` and `Complete` refuse outside one
transaction; `Resolve` refuses inside any.**

**`Unresolved` is the whole point of ES-07, and the naive version is exactly
wrong.** "No row means it did not happen" is the inference this standing exists
to refuse. It has a precise database meaning and no clean answer: a resolver on a
second connection sees no row for either of two reasons, and PostgreSQL gives it
nothing to tell them apart — there is no row to lock, so `FOR SHARE` blocks on
nothing.

**What a caller does with `Unresolved`:**

1. **Do not re-issue the command without the key.** A bare `Load → Decide →
   Append` writes the decision twice and nothing catches it, because nothing was
   asked. A re-issue **under the same key** is a different matter and is not the
   failure this list used to say it was: the claim meets the in-flight row and
   PostgreSQL's speculative insertion makes it *block* until the original
   transaction ends, after which it is `Recorded` if that transaction rolled back
   and `Repeated` if it committed — at READ COMMITTED. At REPEATABLE READ or
   SERIALIZABLE the block ends in `40001` instead and the re-issue rolls back
   whole, which is the same two answers one retry later (§1.2). That is the
   correct answer in every direction — the fingerprint no longer moves with the
   version, so the moved load does not turn the retry into a stranger. What it
   costs is a connection held for as long as the original transaction lives,
   which on a hung one is until `idle_in_transaction_session_timeout`, and that
   is why the next item is still the advice for a request path.
2. **Report the operation as in flight**, not as failed and not as succeeded — an
   HTTP 202-shaped answer. It may still land, and answering 202 costs nothing
   while blocking a request thread behind an unknown transaction costs a thread.
3. **Re-resolve after the deployment's `idle_in_transaction_session_timeout`.**
   That setting is what bounds how long `Unresolved` can persist, and it is the
   operational lever the reference's documentation obligations already name
   (`EVENTSOURCE_REFERENCE.md` §Documentation obligations item 1). Phase 5
   inherits that lever and does not invent a second one. After it has elapsed, an
   `Unresolved` that persists is a long-running transaction, which is an
   operational alarm rather than a request-path state.
4. **Never read `Unresolved` as a rollback.** INV-115 is falsified by the test
   that leaves a transaction open and asserts the standing.

**The fourth answer that was designed and refused.** PostgreSQL *can* prove an
absent row is a rollback: mint a bound with `pg_current_xact_id()` at the first
look and poll `pg_snapshot_xmin(pg_current_snapshot())` until the floor passes
it, which proves every transaction live at that moment has ended — the identical
argument `eventpg`'s `walk.settledAt` (`read.go:232-237`) already makes, with the
identical global-`xmin` stall attached. It is refused because the numbers are
`eventpg`-internal and surfacing them means a new method on the frozen `Store`
interface that **every** store must implement to answer a question only one
dialect can ask, in exchange for a state a configuration setting already bounds.
Recorded here so it is not re-proposed, and so the choice is visible as a choice.

### Retention, and why an expired receipt does not read as a rollback

A sweep makes the check window finite; the reference and the port both grow the
table monotonically and neither solved it (backlog §5, §6). The danger is
specific: once a row is swept, it is absent, and absent is `Unresolved` — safer
than `Rolled`, but now permanently wrong about an operation that certainly
happened.

The fix is that **the ledger publishes a horizon and `Resolve` refuses to
conclude below it.** `Ledger.Horizon(ctx)` answers the oldest instant this ledger
still holds rows for. If there is no row and the caller's `Issued` is before the
horizon, the standing is **`Expired`** and no conclusion is available at all.

**The horizon is a database instant, and the contract says so.** `Horizon` is the
table's own `MIN(recorded_at)`, or `now() − retention` **computed in SQL** for a
table that may be empty — never a `time.Now()` in the ledger's own process. The
column it is drawn from is `statement_timestamp()` (§"the order"), so the whole
ledger is on the database's clock, which is the same thing §1.3 praises `eventpg`
for and the same reason [[D-128]] gives for never ordering by an application one.
A ledger that answers the zero instant claims to hold everything for ever, which
is what a table with no sweep is, and the doc says so.

**`Issued` is the caller's clock, it is the one value that cannot be moved to the
database's, and it is optional.** The canonical ES-07 caller is an HTTP handler
that received an idempotency key in a header on a retry: it has the key and **not**
the instant the key was minted, and the appendix's own DX sketch
(«`receipts.Resolve(ctx, operationKey)`») asks for nothing else. Requiring it and
refusing a zero one with `ErrSpec` directs that caller at `time.Now()`, which is
always after any horizon, which permanently suppresses `Expired` and reinstates
the failure the horizon exists to prevent — with no error anywhere. So:

- **`Issued` absent** → `Resolve` never answers `Expired`. An absent row is
  `Unresolved`, and `Resolution.Horizon` carries the ledger's instant, so the
  caller — or the operator reading its request log, which *does* have the date —
  can make the comparison the framework was not given the input for.
- **`Issued` present** → the comparison runs, and its error term is the skew
  between the caller's host and the database. Stated rather than assumed.

**The skew decides between two non-conclusions, and that is why the error term is
tolerable.** `Unresolved` and `Expired` both refuse to say whether the operation
happened; they differ in what the caller does next — re-resolve, or stop. A clock
behind the database's answers `Expired` for an operation that is in flight, and
the caller gives up early on something it could have learned; a clock ahead
answers `Unresolved` for a swept row, and the caller waits for an answer that is
never coming. **No reading of the skew produces a false `Found` or a false "it did
not happen"**, which are the two answers that would be dangerous. An
implementation that wants the residue gone publishes a `Horizon` a little older
than the oldest row it holds, and the contract permits exactly that: `Horizon` is
*an instant at or before the oldest row this ledger answers for*, so being
conservative is conformant and being optimistic is not.

**The framework prunes nothing.** The sweep is the ledger's, on the application's
own schedule — a `jobs` periodic is the obvious spelling and is named on the
module page rather than implemented here.

### [[D-118]], answered

D-118 requires: *"Do not add a second durable-intent table for effects that are
already expressible as a job. If a job cannot express it, say why in a decision
before writing the table."* `EVENTSOURCE_REFERENCE.md` W11 already adjudicated
the question — *"Not blocked by [[D-118]] — a dedup claim is not a durable
intent"* — and the decision D-118 demands is still owed. **D-142** is it, and the
argument is three sentences:

- A receipt is **never delivered**. It has no worker, no lease, no attempt count,
  no backoff and no dead-letter; it is read by a query and not by a drain, so
  every mechanism `jobs` exists to provide is dead weight on it.
- It must be atomic with an append in the **event store's** database.
  `jobs.Stager` stages in the job store's, and [[D-118]]'s own sentence about two
  handles is why that is not the same thing.
- The three-way verdict it needs is already reviewed and shipped in `jobs`
  (`PlacementCreated` · `PlacementExistingSamePayload` · `PlacementConflict`,
  `jobs/queue.go:229-241`), so the shape is borrowed rather than invented and the
  divergence is only where the domains differ — a receipt's fourth and terminal
  state, `Unresolved`, has no meaning for a queue.

### What ES-07 does not do

- **`Store.Append` is unchanged**, and stays non-deduplicating and non-retrying
  (`event/store.go:199-211`). The receipt is a second write beside the append.
- **Nothing retries.** UC-032's `Out of scope` — *"Retrying anything… the unit of
  a retry is the caller's transaction"* — is untouched.
- **No metadata reaches the envelope.** `Envelope` has no metadata map and
  `Record` has three fields; the appendix says the receipt is «отдельный
  SQL-профиль, не произвольные metadata в event envelope», and this keeps it so.
- **No `Outcome` member is added.** `event.Outcome` (`event/outcome.go:5-13`) is
  the closed store-to-kernel channel and has seven members; `Unresolved` is not
  one of them and is not `Unconfirmed`. It is `receipt`'s own vocabulary, in
  `receipt`'s own type, because the two doors answer two different questions
  (§5.3).

---

## 1.3 What a historical read refuses

**The question.** ES-08 part 3: *«читать полный префикс до указанной версии, не
произвольный фильтр событий. Результат не выдаёт append token. Первая граница —
точная версия; timestamp не объявляется business-time и не заменяет порядок
commit. Неподдерживаемая revision или отсутствующий префикс возвращают отказ, не
частичное состояние.»*

### Why this is a kernel gap and not a convenience

The fold is not reachable from outside the kernel. `Aggregate.Fold` is exported
but folds decided `Change` values, not `Envelope`s; the envelope→fold map is
`Repo.apply` (`event/repo.go:260-272`) over the unexported `aggregate.facts`. So
an application **cannot** build an at-version replay out of the public surface
without re-writing its own type switch and duplicating every fold.

And it is a correctness requirement rather than a debugging aid, which is already
recorded (backlog §7): an integration event is the aggregate re-read **at the
event's own version**, and reading head state instead makes a replayed stream
*"indistinguishable from N copies of the current state."*

### The shape: two returns, and the absence is the design

```go
func (this *Repo[S, ID]) StateAt(ctx context.Context, id ID, version Version) (S, error)
```

**There is no second value, and that is the strongest possible reading of
«Результат не выдаёт append token».** A `Prefix` or `Provenance` struct was
designed and refused: everything it could honestly carry, the caller already has
(the stream it asked about, the version it asked for, and — because a short
prefix is a refusal — the fact that it got exactly that version), and the one
thing it could not carry without a second read is the stream's current head,
which `replay`'s own rule forbids (*"the loop stops on a short page and issues no
confirming read"*). So what is left is a value whose only job is to be **not** an
`At[S]`, and a value that does not exist does that job better than one that does.

Marten's answer to the same problem is a different method on a different session
type — `FetchForWriting` yields the token, `FetchLatest` is read-only and is
*"only available off of `IDocumentSession` and not `IQuerySession`"* — so the
division ES-08 asks for exists there as a separate call rather than a flag, which
is what this is.

The danger the rule guards against is not a silent overwrite: an `At[S]` minted
at version 7 of a stream now at version 30 is refused by `Append`'s own version
comparison. The danger is the **caller's reasoning** — a value that looks like a
load token invites a `Load → Decide → Append` shape whose decision was made
against 23 events of missing history, and which fails only at the store, and only
if the stream happens not to have moved back.

### The four refusals

1. **`version == 0`** → `ErrVersion`. Version zero is the empty stream, and
   answering the zero state hides the caller's off-by-one. A refusal here costs a
   caller one line and saves the reading of a state that was never anybody's.
2. **The prefix is short of the version asked for** → `ErrVersion`. This is
   Marten's `version > events[^1].Version → null`, turned into a refusal. The
   paged loop already knows: a short page is the end of the stream by
   construction, and the accumulated version at that moment is the head.
3. **A stream with no events at all** → `ErrVersion`, the same refusal, because
   it *is* case 2 with a head of zero. Marten returns `null` for four different
   situations — no stream, empty stream, version past the end, timestamp before
   the first event — which a caller cannot tell apart. vv gives one refusal that
   covers three of them honestly and says why the three are not told apart: a
   stream with no events and a stream that never existed are one thing in an
   append-only log, and `Store.ReadStream` surfaces no registry a reader could
   consult. (`eventpg` has a `streams` table and *could* tell them apart;
   surfacing it is a store-contract change, which §"the contract is not widened"
   refuses for the same reason it refuses the version ceiling.)
4. **An unsupported revision, an unknown type, a refusing upcaster, an oversized
   recorded payload** → `ErrRevision`, `ErrUnknownType`, `ErrUpcast`, `ErrPayload`
   — **unchanged, and inherited rather than re-implemented.** `replay`'s error
   path already returns `this.nothing(err)`, the zero state and never the
   accumulator (`event/repo.go:198-224`), which is exactly *«возвращают отказ, не
   частичное состояние»*. Phase 5 shares that loop; it does not write a second
   one. UC-232 pins the inheritance and INV-118 names the shared code path.

**No refusal names a number.** UC-032 §11 — *"No key, payload, version, position,
cursor or identity reaches an error message"* — and §8's precedent, where the
conflict refusal *"deliberately does not report that version"*. So the refusal
for a short prefix says the stream is shorter than the version this read was
bounded at, and names neither.

### No timestamp boundary, ever

*«Первая граница — точная версия; timestamp не объявляется business-time и не
заменяет порядок commit.»*

`recorded_at` is `statement_timestamp()` — a database clock, so comparable across
writers, which is already better than the reference's application clock. It is
still not an ordering, and [[D-128]]'s `What it forbids` closes the door by name:
*"Do not order a global read by `recorded_at` … two writers can share an instant,
and a commit can land long after the statement timestamp it carries."*
`EVENTSOURCE_REFERENCE.md` W15 records the absence from the other side:
*"**Nothing** in the write or read path uses time."*

So there is no timestamp parameter, no timestamp overload and no timestamp
option. If one is ever added it can only be a **lookup that resolves to a
version**, never an ordering, and it needs its own contract — stated here so the
next person starts from the conclusion rather than the temptation.

### The store contract is not widened, and the arithmetic is the argument

`Store.ReadStream` has no upper bound and does not get one. `StateAt` pages with
the existing call and truncates the last page in Go.

**The over-read is bounded by one page, ever.** A bound at version 7 of a
100 000-event stream reads one page of `StreamPage` (256 for `eventpg`) and
discards 249 envelopes; a bound at version 99 999 reads 391 pages either way. The
waste is never proportional to the stream. So a ceiling on the contract would buy
at most one partial page of I/O, in exchange for: a change to an interface every
store implements, a new conformance section, a `check-event-kernel` re-baseline
of the contract file, and a third-party store whose certified suite goes red. The
[[D-128]] Reject entry for `Log.ReadAll`'s filter parameter is the precedent for
how that trade is argued (`EVENTSOURCE_REFERENCE.md` §Reject 3), and it lands the
same way.

**Two orderings inside the loop are load-bearing and are stated as such:**

- **Check, then truncate.** `checkPage` runs on the page the store returned,
  entire, before anything is discarded. Truncating first would hide a store that
  answered `[v3, v1, v2]` — a page that folds to a state that is wrong at the
  right version, with the right count and no refusal anywhere
  (`event/repo.go:229-234`).
- **Truncate, then fold.** Folding the discarded envelopes and then "stopping"
  produces a state at the wrong version. The loop stops reading the moment the
  accumulated version reaches the bound, and folds nothing above it.

### Why this is not a widening of [[UC-032]]

UC-032 §6 is the clause that looks like it forbids this:

> A load rebuilds the state from the whole history, in order, every time. There
> is no cache, no snapshot and no partial rehydration: a load that fails anywhere
> returns nothing, never a state built from the events that happened to arrive
> first.

`StateAt` is not a `Load`, and it satisfies §6's three prohibitions literally:

- **Not a cache.** Nothing is retained across a call; §INV-042's rule that the
  kernel retains no application value is untouched.
- **Not a snapshot.** There is no second authority. The state is folded from
  events, in order, every time. [[D-132]]'s invariant — *"Full replay is the only
  authority a folded state has"* — is true of this read, over its prefix.
- **Not a partial rehydration.** §6's "partial" means a load that skipped events.
  A prefix skips none: it is dense from version 1 to the bound, and a gap is a
  refusal at `checkPage`.

And it is not the first half of a `Load → Decide → Append`, because it returns
nothing to append with.

UC-032's `Out of scope` line that stays true verbatim: *"A query language over
history, an ordinary list-and-filter endpoint over events, or anything that makes
a fact history behave like a collection."* **A version ceiling is a prefix; a
predicate is a query language**, and that sentence is the whole of the boundary.
UC-032 §10 — *"The two reads are one aggregate's stream in order, and the whole
log in the order it was committed"* — is the framing that keeps this inside the
existing contract's spirit: a bounded prefix is the first of those two with a
ceiling, and not a third read.

**So ES-08 gets its own use cases (UC-228…UC-236) and UC-032 gains no clause.**
The appendix's part 4 reads two ways — *«[[UC-032]] … должен отдельно описать
новый read-only сценарий»* — and the framing paragraph decides it: *«нынешний
[[UC-032]] ими не расширяется»*. The one edit UC-032 receives is a `See also`
pointer to the new use case, because leaving a shipped call unmentioned in the
document a reader trusts is the drift this repository treats as a defect. No
clause of `What must hold` changes, and `Out of scope` gains no line, because
nothing that was out of scope came in.

---

## 1.4 What a snapshot is bound to, and what invalidates one

**The question.** ES-09 part 3: *«snapshot привязан к backing/stream/version и
версии вычисления состояния, независимой от payload revision. Загружается только
подтверждённая версия; затем проигрывается весь хвост.
Несовместимость/повреждение snapshot ведёт к полному replay; unreadable event не
скрывается fallback. Нужны measured benefit и проверка равенства snapshot+tail
полному replay. Историю не удаляем.»*

### The gate, and why phase 5 ships a contract instead of code

[[D-132]] forbids ES-09 outright, everywhere ES-09 could live. Its invariant:
*"no snapshot, no memo and no cache of one is declared anywhere under `event/`,
and none appears in the exported-surface baseline."* It is enforced by
`TestNoSnapshotAuthorityIsDeclaredOrPromised` (`scripts/projection_test.go:261-294`),
which walks **every** package the surface baseline lists under
`github.com/frostgrove/vv/event` — including `event/eventpg`, which is exactly
where ES-09's own part 5 puts the policy — fails on any identifier matching
`(?i)snapshot`, and carries its own fixture control so it cannot go vacuous.

And the gate is not met. `D-132:49`: *"The gate is a measured **need**, not a
measured cost. **There is no consumer.**"* The trigger is a deployment measuring
its own p99 aggregate replay above ~50 ms in its own environment, with
`BenchmarkStreamReplay` as the instrument. There are three honest routes and no
fourth:

- **(a) Produce the measurement.** Unavailable. The measurement must be a
  deployment's own p99; this repository is not a deployment, and
  `BenchmarkStreamReplay` measures *cost*, which D-132 refuses by name as a
  reason.
- **(b) Supersede [[D-132]].** ES-09's own «зачем» is *«ускорить длинные
  streams»* — the cost argument D-132 already refutes. **The appendix supplies no
  argument that supersedes it.**
- **(c) Deliver ES-09's design — the contract, the compatibility rule, the
  fallback, the equivalence obligation — as an accepted decision without code.**
  This is what part 4 literally asks for: *«Оптимизированный load требует
  отдельного принятого контракта, а не незаметного изменения гарантий
  [[UC-032]]»*.

**Phase 5 takes (c).** [[D-132]] is **amended, not superseded**: its invariant
stands, the enforcement test stays green and un-loosened, and **D-145** is the
contract its re-entry trigger opens into. That is only honest if the contract is
complete enough that the phase which eventually writes the code implements rather
than re-derives — so the rest of §1.4 is written to that standard, and §3's
Group BD cases are stated as obligations on that phase rather than as tests phase
5 runs.

The surface consequence: **ES-09 exports nothing.** `docs/api/surface.md` gains no
line under `event`, so the enforcement test is untouched. D-145 lives in
`docs/ai/decisions/`, which that test does not read — it walks Go identifiers and
the surface baseline, and neither is a decision file.

### What a snapshot is bound to — five things, compared before deserialisation

The ordering is the mechanism, and the source proves it. Axon's
`AbstractEventStorageEngine.readSnapshot` reads

```java
return readSnapshotData(aggregateIdentifier)
        .filter(snapshotFilter::allow)
        .map(snapshot -> upcastAndDeserializeDomainEvents(...))
```

— `.filter(…)` **precedes** `.map(deserialise)`. The compatibility decision is
made on the stored row's metadata, before a single byte of the payload is read
back, because a filter that had to deserialise first would fail on exactly the
snapshots it exists to exclude. **Any design that stores a version inside the
snapshot payload has the mechanism inverted**, and that is the first clause of
D-145.

The five, all columns beside the bytes:

1. **The backing.** `event.Backing`, compared with `Backing.Equal`. A snapshot
   restored from another database's dump is refused. Neither Axon nor the
   reference nor the port has this; vv has the value already.
2. **The stream.** Family and key, composed ([[D-125]]).
3. **The version.** The exact version the fold had consumed. The tail then begins
   at `version + 1`.
4. **The snapshot payload's own revision.** A snapshot has a codec and a wire
   shape like a `Fact` does, and it carries the revision it was written at. But
   **a snapshot is never upcast**: a revision that is not the current one is
   refused and the load falls back. This is a deliberate divergence from how
   `event` treats facts, and the reason is the asymmetry that makes a snapshot
   safe at all — a fact is irreplaceable, so carrying it forward through a
   declared chain is worth the risk; a snapshot is regenerable from the log, so
   an upcaster bug on one serves a wrong state silently where the same bug on an
   event is caught by every replay. The port reached the same conclusion
   (backlog §6(4)); Axon's `RevisionSnapshotFilter` is the same comparison with
   no chain behind it.
5. **The state-computation version.** §"the subtle part", below.

### The state-computation version: who declares it, and what nothing detects

Axon's `@Revision` is a **serialized-form** version, not a state-computation one.
It is bumped by a human when the aggregate's serialized shape changes. It is
**not** bumped when an `@EventSourcingHandler` body changes while the fields stay
the same — so a build whose fold has changed loads its predecessor's snapshots
and serves a state no current code would compute, with no error anywhere.
[[D-132]] already names this as *"the one this framework structurally cannot
detect"* (`D-132:43-44`).

**So ES-09's «версия вычисления состояния, независимая от payload revision» is a
requirement the cited source does not meet, and it cannot be copied from Axon.**
What is available is the shape — a constant compared before deserialisation —
with a different meaning and a different discipline for bumping it. D-145 binds a
snapshot to **two** components, and the split is the design:

**The automatic half — the declared-shape digest.** SHA-256, length-prefixed,
over the family, the state type's name, and the sorted list of
`(fact wire name, number of retained revisions)` the declaration holds. It is
computed by the framework from the `Aggregate` value, costs nothing, and catches
the class of change an author most often forgets: **a fact added, a fact removed,
a revision appended to a chain.** Every one of those changes what a replay
computes and none of them changes the state struct.

**The human half — the author's own integer**, declared on the aggregate beside
the family. It is bumped when a **fold body** changes, when an upcaster's
behaviour changes, or when the state struct's meaning changes without its shape
changing. It exists because the automatic half cannot see any of those.

**Who declares it: the application author.** The framework cannot derive it. Three
derivations were considered and all three are refused, recorded so the next phase
does not re-derive them:

- **Hashing compiled function pointers** — not stable across builds, so a no-op
  rebuild would invalidate every snapshot in the deployment.
- **Hashing the source** — not available at run time.
- **Hashing the closure's behaviour** — not a thing.

**What happens when somebody forgets to bump it: nothing detects it.** A changed
fold body, with no new fact and no new revision, produces a binding that compares
equal. The snapshot loads. The state served is one no current code would compute,
and there is no error, no counter and no signal. That is the honest answer and
D-145 states it in those words rather than implying a guarantee.

**What the suite proves instead**, since it cannot prove that:

1. **The equivalence proof** (§"the two gates"), which makes the mechanism's
   arithmetic provable even though its bumping discipline is not.
2. **A drift fixture**: a snapshot written under computation version 1 and read
   under a declaration at version 2 falls back to full replay and produces the
   **correct** state, not the snapshot's. Control: at version 1 it loads from the
   snapshot, proven by a counting store reporting how many envelopes were read —
   without that control the test passes whether or not the snapshot was ever
   consulted.
3. **A control that pins the gap.** A test that changes a fold body *without*
   bumping the version and asserts the framework serves the **stale** state. It
   is the `test/integration/gate_relscope_test.go` pattern inverted: a control
   whose job is to assert that the hole is there, so that the day somebody closes
   it the control fails and says so. A documented hole with a failing-on-repair
   test is a different thing from an undocumented one.

### The fallback, which must be observable, and the unreadable event that must not be hidden

Axon's fallback is silent: one log line at the framework's own logger
(`"Error reading snapshot for aggregate [{}]. Reconstructing from entire event
stream."`) and nothing in the return value distinguishing "loaded from a
snapshot" from "the snapshot was unreadable and I replayed everything". A
deployment whose codec quietly changed can have *every* snapshot failing, paying
a full replay on every load, with the performance the snapshot was added for gone
and no signal but log volume.

**vv cannot even log** — [[D-132]] forbids `event/projection` a line and the same
argument applies to a kernel that publishes values instead. So D-145 requires the
fallback to be **in the return value**: an optimised load answers how the state
was obtained — from a snapshot, or from a full replay — and a deployment
measuring 100 % full replay learns that its snapshot mechanism is not working,
which is the failure mode neither source can see.

**And the sharp clause:** *«unreadable event не скрывается fallback»*. The
fallback is entered on the **snapshot's own** failure and on no other. An
unreadable *event* in the tail — `ErrUnknownType`, `ErrRevision`, `ErrUpcast`,
`ErrPayload` — is a refusal and must be returned. The discriminator is **which
call failed**, not which error class came back, and the failure mode if it is got
wrong is subtle rather than loud: the load falls back, replays from version 1,
hits the same unreadable event, and returns a refusal with the snapshot's
involvement erased — so an operator debugs a replay problem that is really a tail
problem, and the snapshot that was working perfectly is the thing they suspect.

Axon's `catch (Exception | LinkageError e)` has a transferable half even though
Go has no class-loading failure: **the set of ways a stored snapshot can fail to
become a value is larger than the set of ways the code that wrote it expects.**
A snapshot decode that panics falls back; a snapshot decode that returns a
plausible zero value does not and cannot, which is why the codec round-trip check
`event.Fact.RoundTrip` already provides is owed for a snapshot's codec too.

### What else D-145 fixes, from the two shipped implementations

Recorded because the study extracted them and the next phase must implement
rather than rediscover (backlog §6):

- **The snapshot is written INSIDE the append transaction.** A snapshot committed
  beside the append leaves, after a rollback, a snapshot at version 10 for a
  stream whose head is version 9 — and every later load reads it, reads
  `WHERE version > 10`, gets nothing, and returns state derived from events that
  never committed. *Permanently wrong, never repaired, because the snapshot is
  preferred over the log.* This is the one part of ES-09 that fits the existing
  architecture perfectly: it is [[D-118]]'s rule, and `eventpg`'s single-statement
  CAS already holds the stream row, so no two writers race a snapshot for one
  aggregate without anyone choosing an isolation level ([[D-126]]).
- **The cadence is `finalVersion % N == 0` with `N >= 2`, checked twice** — at
  bind time and at write time. `N == 1` turns the event store into a state store
  with an audit log attached; `N == 0` is a divide-by-zero on the write path. And
  the consequence nobody states: **a multi-event command jumps the boundary, so
  worst-case replay length is not bounded by N.**
- **A load at a version takes the newest snapshot AT OR BELOW it** —
  `AND (:version IS NULL OR s.version <= :version) ORDER BY s.version DESC LIMIT 1`
  — and the forward read is `version > :from AND version <= :to`. The
  half-open/half-closed asymmetry is deliberate: the snapshot already includes
  its own version. Drop the `<= :version` clause, as the Kotlin port did, and a
  read at version 12 of an aggregate snapshotted at 30 returns *future state
  labelled as version 12*. **This is the same clause ES-08 needs**, which is why
  the two are one design and why ES-08 shipping first, without a base-state
  parameter, is the sequencing that keeps ES-08 clear of [[D-132]].
- **Do not read the reference's snapshot code for the shape.** `AggregateStore.java:53-58`
  calls the snapshot `INSERT` inside the per-event append loop while testing the
  aggregate's *final* version, against a bare `INSERT` with no `ON CONFLICT` — so
  a two-event command on a boundary violates the primary key and aborts the
  command transaction. Invisible only because every sample command emits exactly
  one event.
- **Neither implementation prunes.** One full-state row per N events, for ever,
  and for a large aggregate the snapshot table can exceed the event log.

### History is never deleted; a snapshot is not history

*«Историю не удаляем»* is about **events**, and Axon's storage note is the thing
ES-09 must refuse outright: *"Snapshot events are stored automatically in the
event store, replacing prior events during normal operations."* That is the
snapshot becoming the authority, which is precisely the second answer [[D-132]]
and UC-032 §6 exist to forbid.

A snapshot is not history, so **replacing** one is legitimate, and the port's
argument for it is right: the write is
`ON CONFLICT (stream, version) DO UPDATE SET …` and not `DO NOTHING`, so a
re-snapshot genuinely replaces a drifted one, and a drifted row does not make
every later load re-detect it and pay a full replay for ever. Axon leaves the row
and pays that cost; the two shipped implementations disagree and D-145 picks the
port's side with the reason attached.

### The two gates, stated so a later phase can meet them

**Gate 1 — measured benefit.** A **named deployment's own p99** `Repo.Load` above
~50 ms, measured with `BenchmarkStreamReplay` in that deployment's environment,
recorded in D-145 with the number and the date. Not this repository's benchmark,
not a synthetic stream, and not a cost figure. `D-132`'s trigger is unchanged and
is the gate.

**Gate 2 — equivalence.** For a stream of N events and **every** cadence
boundary, `snapshot + tail` folds to a state deeply equal to `full replay`, over
a corpus that includes at least: a multi-event command that jumps a boundary; a
fact with a retained revision needing an upcast in the tail; and an aggregate
whose fold is order-dependent, so a tail applied in the wrong order fails rather
than passing by symmetry. Green twice in a row, under `-race`, against the live
database. Falsified by moving the half-open boundary by one in either direction —
apply the base event twice, or drop the ceiling event — and watching it fail.

**A snapshot that ships without both is out of scope.** Neither shipped
implementation has Gate 2: both assert the property and test neither.

---

# 2. Scope and non-goals

## In scope

**ES-05 — waiting for a specific change in a read model.**
`projection.Mark` with its two minting doors (`WaitSpec.Committed`, `MarkOf`);
`projection.Wait` with `WaitSpec`, `WaitOf` and `Visibility`; four sentinels
(`ErrNotVisible`, `ErrParked`, `ErrUncommitted`, `ErrGeneration`); the park
question asked first on every poll, under the keys the mark carries; the optional
`Generations` that scopes a wait to the generation reads resolve to; one widened
sentence in `Park.Holds`'s contract and no second one; the cost arithmetic on the
module page.

**ES-07 — finding out what happened to an uncertain append.**
The new package `event/receipt`: `Key`, `Fingerprint`, `Receipt`, `Ledger`,
`Claim`/`Once`/`Held`/`Verdict`, `Resolve`/`Resolution`/`Standing`, and four
sentinels. In `event`, one method: `Repo.Digest`. A reference `Ledger` over `crud`
in `_examples`, with the `INSERT`-then-`SELECT` claim. The [[D-118]] decision the table
owes.

**ES-08 — historical state at a version.**
In `event`, one method and one sentinel: `Repo.StateAt` and `ErrVersion`. The
bounded page loop with check-then-truncate and truncate-then-fold. No store
contract change.

**ES-09 — snapshots that are compatible, with a safe fallback.**
**D-145 only — an accepted contract with no code.** The five bindings, the two
components of the state-computation version, the observable fallback, the
unreadable-event rule, the five inherited constraints, and the two gates.

**Across all four.** Four decisions (§7.2); four new use cases in
`docs/ai/usecases/modules/event/`; one `See also` line added to [[UC-032]] and no
clause changed; the module pages; the flows; the kernel baseline re-record.

## Non-goals — named, not specified

- **Exactly-once anything.** UC-032 §18 and its `Out of scope` are unchanged. A
  wait that reaches does not make delivery once, and a receipt does not make an
  append idempotent — it makes a *caller's retry* safe, which is a different
  sentence and the one the source makes too (*"The main purpose is the ability to
  retry requests"*).
- **A head, a lag metric, or "caught up".** §1.1.
- **A timestamp boundary on a historical read.** §1.3.
- **A snapshot, a memo or a cache of a folded state.** §1.4, and [[D-132]]'s
  invariant is unchanged.
- **A filter, a predicate or a query language over history.** UC-032's
  `Out of scope` line stands; a version ceiling is a prefix.
- **A version ceiling parameter on `Store.ReadStream`**, or any other change to
  the store contract. §1.3, and Reject 3's precedent.
- **A fourth `Resolve` standing that proves a rollback.** §1.2, refused with its
  cost.
- **A retention sweep.** The ledger's, on the application's schedule.
- **A new `event.Outcome` member.** The closed store-to-kernel channel does not
  grow.
- **An OTel span, metric or log line anywhere in this phase.** ES-10 owns that
  and is not phase 5.
- **A wait or a receipt in `eventpg`.** Neither is a store capability;
  `eventpg` gains nothing and `SchemaVersion` stays at 2.

---

# 3. Use cases

Format is phases 2–4's: **Given**, **Then**, **Must not**, **Control**. Numbers
are allocated in order within each group and are append-only.

---

## Group BA — waiting for a change to be visible (ES-05)

#### UC-204 A confirmed command is read back and the read does not show the old state  [happy]
- **Given** A caller that committed a transaction containing one `Repo.Append`,
  a `Commit` from it, a live single-partition projection over the same log with
  `Advance: InUnit`, a `WaitSpec` built by `WaitOf(spec, cover)`, and a context
  carrying a one-second deadline.
- **Then** `spec.Committed` issues exactly one `ReadStream` for a commit that fits
  a page and returns a `Mark` at the position of `commit.Last()`. `Wait` polls, the
  first poll that finds `lowest >= mark.At()` returns
  `Visibility{Reached: true, At: lowest, Behind: 0}`
  and a nil error, and the read the caller then performs against the read model
  through the same source shows the change.
- **Must not** The test must not `sleep`, must not poll a business table, and
  must not assert anything about wall-clock duration.
- **Control** The same wait against a projection that is not running returns
  `ErrNotVisible` with `Reached: false` and `Moved: false`, so the happy case is
  proving the wait and not proving that time passes. **And a second projection of
  the same log is stood up beside it**, with two arms: the wait is asserted not to
  address it, and a mark minted from **its** `WaitSpec` and handed to the first
  one's is asserted to be refused with `ErrSpec` (§5.2, §INV-108) — the failure
  that would otherwise report `Reached: true` for an event the second projection
  parked.

#### UC-205 A wait reaches on its first poll and never sleeps  [happy]
- **Given** A projection already drained past the mark, and `Wait` with
  `Every: time.Hour` and a `Ticks` that records every interval it is asked for.
- **Then** `Visibility.Polls == 1`, `Reached` is true, and the recorded `Ticks`
  either was never asked for an interval or was asked and never read from.
- **Must not** It must not wait one interval before the first poll — a caller
  whose projection is already past the mark must pay one round trip and no
  latency.
- **Control** The same wiring with the projection one event short takes at least
  two polls, so `Polls == 1` is a measurement and not a constant.

#### UC-206 A parked sequence is named rather than waited out  [edge]
- **Given** A projection with `OnPermanentFailure: ParkSequence` that has parked
  the sequence the caller's own stream belongs to, a `Progress.Highest` already
  past the mark because the scan advanced over the quarantined envelope, and a
  `Wait` supplied with that `Park` and the projection's own `Sequence`.
- **Then** The wait returns on its first poll with
  `Visibility{Parked: true, Polls: 1}` and an error satisfying
  `errors.Is(err, ErrParked)`. It does **not** return `Reached: true`.
- **Must not** It must not wait for the deadline, must not report reached, and
  must not read the census on this path.
- **Control** The identical wiring with `Park` **nil** returns `Reached: true`
  for the same parked event — which is the failure this case exists to pin, and
  the reason a projection that parks must supply its queue here. The pair is the
  whole of *«scan checkpoint после parking не доказывает применение события»*.

#### UC-207 A sequence parked in one partition while another lags  [edge]
- **Given** A four-partition cover; the caller's stream hashes into partition 2,
  which parked it; partition 0 is a hundred positions behind, so
  `lowest < mark.At()`.
- **Then** The wait answers `ErrParked` on its **first** poll, from the park
  question, because the park is asked before the census and on every poll rather
  than only once the mark is reached.
- **Must not** It must not burn the deadline and answer `ErrNotVisible`, which
  is what an implementation that asked the park only after reaching would do.
- **Control** With partition 2 not parked, the same wiring waits for partition 0
  and reaches, so the early answer is discriminating.

#### UC-208 A healthy projection pays one extra count per poll, and a wait never asks `Holes`  [happy]
- **Given** A recording `Park` counting its four methods **and recording whether a
  transaction of the destination's was bound to the context of each call**, a
  projection that has parked nothing, and a wait that makes three polls before
  reaching.
- **Then** `Sequences` was called three times, `Holds` zero times, `Holes` zero
  times, and `Park` zero times. Every recorded call carries **no** bound
  transaction, which is the placement the widened `Holds` sentence and the
  unchanged `Sequences` one both promise.
- **Must not** `Holds` must not be called while the queue is empty, and **`Holes`
  must not be called at all** — not on this generation, not on one with
  `Quarantined > 0`, and not on the reaching poll. A wait that asks it has moved a
  second sentence of `Park`'s contract outside a unit of work on a path no
  baseline can see.
- **Control** The same wait against a generation with `Quarantined > 0` — where an
  earlier draft read `Holes` on the reaching poll — makes the **same** three
  calls and no more, so the absence is unconditional and is not a skip that some
  other census would undo. A recording `Park` asserting `Holes` was never
  constructed-for is the only thing that can see this: `make api` sees a method set
  and `check-event-kernel` sees a digest, and neither sees which methods a caller
  calls.

#### UC-209 A mark minted inside the transaction that wrote it  [edge]
- **Given** `spec.Committed` called on a context that still carries the `*sql.Tx` the
  append rode in, against `eventpg` — where the read succeeds, the row is
  visible to its own transaction and the position is a real one.
- **Then** `ErrSpec`, from `store.Transaction(ctx)` answering a valid authority,
  before any read is issued.
- **Must not** It must not mint the mark, which would be a mark for an append
  that can still roll back and which no projection would ever deliver.
- **Control** The same call on a fresh context after the commit mints the mark,
  so the refusal is about the bound transaction and not about the store.

#### UC-210 A commit that is not readable back  [edge]
- **Given** `spec.Committed` on a fresh connection while the writing transaction is
  still open — the store shows no row at `commit.Last()`.
- **Then** `ErrUncommitted`, and the refusal says the two indistinguishable
  causes in one sentence: the transaction has not committed, or it rolled back.
- **Must not** It must not report the commit as rolled back, and it must not
  return a zero `Mark` beside a nil error.
- **Control** After the writing transaction rolls back, the same call answers the
  same refusal — which is the point: `ErrUncommitted` is ES-07's `Unresolved` one
  level down and tells the two apart no better, deliberately.

#### UC-211 An empty commit, a zero mark, and a barrier of the generation being waited on  [edge]
- **Given** Four calls: `spec.Committed` with `commit.Empty()`; `Wait` with the
  zero `Mark`; `Wait` with a **nil `Park`** whose `Of` names the generation the
  `Barrier` was observed from; and the same barrier mark in a spec whose `Park` is
  **not** nil.
- **Then** `ErrSpec` for the first two. The third **is admitted**, and this is the
  one place the wait's rule differs from `Reached`'s: `Reached` refuses a barrier
  of the generation it is asked about because that generation clears its own
  watermark by arithmetic, but a caller waiting for its own generation to pass a
  mark *it observed earlier* is asking a question that moves. The doc states the
  difference rather than copying the refusal. The fourth is **`ErrSpec`**: a
  barrier carries no envelope, so the mark carries no sequence key, so the park
  cannot be asked about the change this caller is waiting for — and `Reached` at
  that mark would mean *delivered* to a caller that supplied a queue precisely
  because it wanted *applied*.
- **Must not** The zero `Mark` must not read as position zero and clear every
  wait immediately, and the barrier-minted mark must not be admitted beside a
  `Park` with the park question silently skipped.
- **Control** A `Mark` from `MarkOf` of a non-zero barrier is admitted at every
  door the zero one is refused at, and the same wait with `Park` nil reaches — so
  the fourth arm's refusal is about the missing sequence key and not about the
  barrier.

#### UC-212 A deadline elapses, and the answer says which kind of not-yet it was  [edge]
- **Given** Two waits with a 200 ms deadline: one against a projection that is
  advancing but slow, one against a projection whose runner is stopped.
- **Then** Both return an error satisfying both `errors.Is(err, ErrNotVisible)`
  and `errors.Is(err, context.DeadlineExceeded)`, with `Reached: false` and a
  filled-in `Visibility`. The slow one carries `Moved: true` and a `Behind` that
  shrank between polls; the stopped one carries `Moved: false` and a constant
  `Behind`.
  A third wait whose budget runs out **inside its first poll** — a checkpoint
  store whose round trip outlasts the caller's deadline — answers the same two
  sentinels with `Visibility{Polls: 1}`, so the exit does not depend on which poll
  the clock landed in. That poll's refusal is **not** wrapped into the deadline:
  a store reached with a done context answers its own classification, so the
  census names the member it could not read and the read door calls it a backend
  failure, and an ordinary timeout would arrive carrying `ErrTopology` over
  `event.ErrBackend` for a cover that is right and a database that is fine. A
  refusal is carried into the deadline only when it is **not** the caller's own
  context coming back — read through the chain and through `event.CauseOf`,
  because a store that classified its failure carries the context error in the
  cause and `errors.Is` never reaches it.
- **Must not** It must not return a bare `context.DeadlineExceeded`, must not
  return the zero `Visibility`, and must not claim `Behind` is a count of
  undelivered events. The third wait must not answer `ErrTopology` or
  `event.ErrBackend` at all — the classes §5.2 reserves for the caller asking
  wrong and for a store in trouble — for a store that is fine and a budget that is
  short. This is the common shape of a short per-request deadline, not a corner:
  measured over a healthy store at a real interval it was the answer on three
  rounds in eight.
- **Control** A **cancelled** context — not a deadline — returns `ctx.Err()` bare
  with the zero `Visibility`, so the two are told apart and UC-032 §12's rule
  holds; and the same holds for a cancellation landing **inside** a poll rather
  than between two. Two more controls keep the new rule from swallowing the old
  ones: the same first poll failing while the caller's budget is **live** still
  answers `ErrTopology` with `Visibility{Polls: 1}`, and a **parked** answer that
  arrives after the budget elapsed is still `ErrParked` and is not rendered as a
  deadline.

#### UC-213 A caller serves stale data, and it is code somebody wrote  [edge]
- **Given** A handler that calls `Wait`, branches on
  `errors.Is(err, ErrNotVisible)`, and serves the read model anyway with a
  staleness header derived from `Visibility.Behind`.
- **Then** It compiles and works, and the staleness is explicit at the call site.
- **Must not** There must be no field, option or mode on `WaitSpec` that turns
  the refusal into a success — a check that can be switched off is one every
  caller switches off.
- **Control** A grep over the specified surface finds no `AllowStale`, `Stale`,
  `OnTimeout` or equivalent, and the module page states the design rather than
  leaving the absence to be read as an oversight.

#### UC-242 The three fields a hand-written `WaitSpec` gets wrong, and the derived one that does not  [edge]
- **Given** One projection with a queue, one parked sequence and a four-member
  cover, and four `WaitSpec`s over it: one from `WaitOf(spec, cover)`, and three
  written by hand that differ from it in exactly one field each — a `Sequence` that
  is not the projection's, a nil `Park`, and an `Over` that names two of the four
  members as a two-member cover.
- **Then** The derived spec answers `ErrParked`. The wrong `Sequence` answers
  `Reached: true` for the parked event. The nil `Park` answers `Reached: true` for
  the parked event. The wrong `Over` answers `Reached: true` from a minimum over
  two rows while a third member is behind the mark.
- **Must not** The three hand-written arms must not be presented as defects the
  framework catches: nothing here can check any of them, and the test asserts the
  wrong answers so that the day one becomes checkable the arm fails and says so.
  `WaitOf` must not read the environment, open a transaction or start anything.
- **Control** The derived spec is the control for all three: it is built from the
  same `Spec` the runner was built from, it applies `ByStream()` where
  `Spec.Sequence` is nil, and it is the one spelling the module page gives. A
  fourth arm asserts `WaitOf` refuses the zero `Cover` and a `Spec` that names no
  identity.

#### UC-243 A commit that spans two sequences  [edge]
- **Given** A projection whose `Sequence` keys on a field of the payload rather
  than on the stream, a commit of three facts whose keys are `A`, `B` and `A`, and
  a park holding `B`.
- **Then** `spec.Committed` mints a mark carrying the distinct keys `A` and `B` in
  the order they first appear, and `Wait` answers `ErrParked` from the second
  `Holds` call.
- **Must not** The mark must not carry only the last envelope's key, which would
  ask about `A`, find nothing parked, and report the change applied while a third
  of it sits in the queue. The mark must not carry the envelopes themselves.
- **Control** With the park holding neither key the same wait reaches, and a
  recording `Park` asserts `Holds` was called twice per poll — once per distinct
  key and not once per envelope.

#### UC-244 A poll that cannot be made  [edge]
- **Given** Two waits over a `Checkpoints` that can be made to fail on demand: one
  whose **first** `Load` fails, one whose fourth fails after three that succeeded,
  and a third wait over a cover that is mid-split — some members holding rows and
  some not — which is what `surveyed` refuses with `ErrTopology`.
- **Then** The first returns at once with `Visibility{Polls: 1}` and that store's
  refusal, unwrapped and unreclassified — **the caller's own context being live is
  part of that**, since a first poll that failed with the budget already gone is
  the deadline exit and not this one (§UC-212). The second keeps polling; when the
  failure clears before the deadline it reaches and returns nil; when it does not,
  the deadline's error satisfies `errors.Is(err, ErrNotVisible)`,
  `errors.Is(err, context.DeadlineExceeded)` **and** the last poll's own refusal,
  and the `Visibility` is the last poll that could be read. The third behaves as
  the first or the second according to whether the split had started when the wait
  did.
- **Must not** A first-poll refusal must not be turned into a deadline — burning a
  caller's whole deadline on a misconfiguration it could have been told about on
  the first round trip. A later refusal must not be terminal — an operator's split
  must not abort a request path's wait. And a deadline reached over failing polls
  must not look like a deadline reached over slow ones — **nor the converse**, and
  the converse is the more damaging direction, because the failing-poll case is
  rare and the slow one is not: a deadline reached over polls the store answered
  late must not look like a deadline reached over polls it could not answer at
  all. A refusal a poll collected from a round trip made with the caller's own
  context already done is that budget and not that poll, and it is not wrapped in
  (§UC-212).
- **Control** The same three waits against a healthy `Checkpoints` reach, and the
  second wait's `Polls` is asserted to be greater than the number of failures, so
  "polled through" is measured rather than assumed.

#### UC-245 A cutover completes while a wait is running  [edge]
- **Given** A `Generations` whose `Active` can be moved by the test, and four
  waits with it supplied: one naming the **retiring** generation, which is ahead
  of the mark, with the cutover committing between the first poll and the poll
  that would reach; one naming the retiring generation **while it is already past
  the mark**, so the wait reaches on poll 1, with the cutover committing inside
  that poll — between its ownership read and its census read; one naming the
  **arriving** generation before its cutover; and one naming the active generation
  with no cutover happening.
- **Then** The first answers `ErrGeneration` on the poll that would otherwise have
  answered `Reached: true`. The second answers `ErrGeneration` with
  `Visibility{Polls: 1}` and an `At` at or above the mark — the census reached and
  the ownership read is what refused. The third answers `ErrGeneration` on its
  **first** poll, before it has waited at all. The fourth reaches.
- **Must not** The first two must not answer `Reached: true` — a true statement
  about a read model the caller is no longer reading is the stale read ES-05
  exists to forbid. The second must not be exempted from the ownership read
  because its first poll is also its last: a caught-up deployment reaches on poll
  1 every time, so an implementation that reads the row only on the polls after
  the first protects every wait except the common one. The third must not burn its
  deadline.
- **Control** The same waits with `Generations` **nil** answer `Reached: true`,
  `Reached: true`, `ErrNotVisible` and `Reached: true` — which is the behaviour
  the field exists to change, and pins that a nil one scopes the promise rather
  than widening it. A recording `Generations` asserts `Active` was read exactly
  twice over the whole wait in the fourth arm, and **twice on the one poll** of a
  wait that reaches at once.

---

## Group BB — the operation receipt (ES-07)

#### UC-214 A command is claimed, appended and completed in one transaction  [happy]
- **Given** A `Ledger` over the same `crud.Source` the event store writes
  through, a fresh `Key`, a `Fingerprint` from `Repo.Digest`, and one
  `crud.InNewTx` around claim, append and complete.
- **Then** `Claim` answers `Recorded`; the append is admitted; `Complete` writes
  `commit.First()` and `commit.Last()` onto the claimed row; and after the commit
  one row exists in the ledger with the fingerprint, the stream and the range.
- **Must not** The claim must not commit separately from the events, and
  `Complete` must not insert a second row.
- **Control** Rolling the unit back instead of committing leaves **no** row at
  all — so the row's existence is evidence about the transaction and not about
  the claim.

#### UC-215 The same key is presented again with the same content  [happy]
- **Given** The completed receipt from UC-214, and the identical command replayed
  with the same key **after the stream has moved** — a fresh `Repo.Load`, which
  answers the version the first attempt left, and a fresh decision of the same
  facts. This is the only `Given` a retry in a new process can have.
- **Then** `Claim` answers `Repeated`, `held.Receipt()` carries the range the
  first attempt wrote, and the caller returns that without appending. The stream
  is at the same version afterwards.
- **Must not** The framework must not append, must not re-decide, and must not
  report the existing version on a refusal (UC-032 §8). The case must not be
  written against a retained token: a `Given` that keeps the original `At[S]` is a
  `Given` the public surface cannot reproduce after a process restart, and a
  mechanism proven only there is one that never fires where it is needed.
- **Control** Two. The same replay with the receipt row deleted answers `Recorded`
  and appends, so `Repeated` is the row's answer and not a guess. And the
  fingerprints of the two attempts are asserted **equal** while their expected
  versions are asserted **different**, which is the whole of the digest decision,
  measured.

#### UC-216 The same key is presented with different content  [edge]
- **Given** The receipt from UC-214, and a second command under the same key
  whose batch differs in exactly one byte of one payload.
- **Then** `Claim` answers `Collided`, returns `ErrCollision` as its own error,
  and the caller's `if err != nil` propagates it. No event is appended.
- **Must not** It must not be reported as `Repeated`; the refusal must name
  neither the key nor either fingerprint's preimage; and the collision must not
  travel as a verdict alone, because a verdict is a value it is legal to discard.
- **Control** Two variants must each collide and are run as a table: a different
  stream with identical records, and the same records in a different order. Each
  is a different operation. **A third variant must NOT collide**: identical records
  under the same key at a different expected version answers `Repeated`, because
  the expected version is deliberately not in the digest — and that arm is what
  makes the other two mean "the content decided it" rather than "something
  decided it".

#### UC-217 Two callers race one key  [edge]
- **Given** Two transactions, both claiming one fresh key, both then appending to
  the same stream at the same expected version, started so that the claims
  overlap. Run once under each of READ COMMITTED, REPEATABLE READ and
  SERIALIZABLE, the level bound **by the caller** through
  `crudsql.DB.WithTxOptions`.
- **Then** *At READ COMMITTED:* one claims and commits; the other **blocks**
  inside `Ledger.Claim` on the primary-key index, its insert reports zero rows,
  its following `SELECT` reads the winner's row, and it answers `Repeated` with
  the winner's range. *At REPEATABLE READ and SERIALIZABLE:* the loser blocks and
  then **fails with SQLSTATE `40001`**, its whole unit rolls back, and **its retry
  answers `Repeated`**. Exactly one append lands at every level. Verified by
  counting rows in `psql`, not by trusting Go.
- **Must not** Both must not append; neither must receive `Unresolved` from a
  claim; **nothing in this framework's path may set an isolation level**
  ([[D-126]]) — the test binds one from the caller's side, which is the only side
  that may; and a level must not be *dropped* from the test instead of asserted,
  because the two stricter arms are the only evidence that a 40001 loser is a
  rollback rather than a second append.
- **Control** The same race with the winner **rolling back** leaves the loser
  free to insert and append, so the block resolved on the writer's outcome and
  not on a timeout.

#### UC-218 A ledger on a second pool  [edge]
- **Given** A `Ledger` over a `*sql.DB` that points at the same database as the
  event store but is a different handle, inside one `crud.InNewTx` on the store's
  source.
- **Then** `Claim` refuses with `ErrSpec`, from
  `Ledger.Transaction(ctx).Same(Store.Transaction(ctx))` being false, before it
  writes anything.
- **Must not** It must not be accepted with a warning, and it must not be
  downgraded to "best effort" — a receipt that is not atomic with its append is a
  row that outlives a rollback and reports an operation that never happened.
- **Control** The identical composition with one handle for both is accepted, so
  the refusal is discriminating. This pair is [[D-118]]'s *"'Atomic' across two
  handles is a sentence with no meaning"*, measured.

#### UC-219 A claim outside any transaction, and against a store with none  [edge]
- **Given** Two calls: `Claim` on a context carrying no transaction of the
  store's; and `Claim` against a store whose
  `Capabilities().Transactions != Supported`.
- **Then** `ErrSpec` for both, at the door, before the ledger is called.
- **Must not** Neither may fall back to autocommit, which would commit a claim
  whose events can still fail.
- **Control** The same calls inside a transaction of a transactional store are
  admitted.

#### UC-220 An absent receipt while the writing transaction is still open  [edge]
- **Given** A transaction that claimed a key and has not committed, and
  `Resolve` on a second connection with an `Issued` after the ledger's horizon.
- **Then** `Standing == Unresolved`. The resolve does not block, returns
  promptly, and carries the horizon.
- **Must not** It must not answer that the operation did not happen; it must not
  block waiting for the writer; and the caller must not be given anything that
  invites a re-issue.
- **Control** The identical call after the writer commits answers `Found` with
  the range; after the writer rolls back it answers `Unresolved` **again** — and
  the case asserts that the two absences are indistinguishable, which is the
  whole of ES-07's hard clause. This is the one control in this group whose job
  is to pin a limitation rather than a capability.

#### UC-221 A receipt that is present and complete  [happy]
- **Given** The committed receipt from UC-214 and a `Resolve` on any connection.
- **Then** `Standing == Found`, and `Resolution.Receipt` carries the key, the
  fingerprint, the stream and `First`/`Last`, which is enough to answer the
  original request without re-reading the aggregate.
- **Must not** `Resolve` must not read the stream, must not fold anything, and
  must not return an `At[S]` or anything that would authorise an append.
- **Control** A recording store asserts `Resolve` issued zero calls to the event
  store other than the transaction question.

#### UC-222 A claim that reached its commit with nobody resolving it  [edge]
- **Given** A caller that claims, then returns early on a domain refusal **without
  rolling the unit back and without calling `Complete`**, so the claim commits with
  `complete = false`; and a retry of that key afterwards.
- **Then** `Resolve` answers `Standing == Incomplete`, and the module page names
  it as a defect report rather than an ordinary state, with its causes. The
  retry's `Claim` answers **`ErrIncomplete`** and no verdict: whether that
  operation's events reached the log is not a question its row answers, so the
  claim concludes nothing.
- **Must not** `Incomplete` must not be reported as `Found` (it has no range) and
  must not be reported as `Unresolved` (there is a row). **The retry must not be
  told `Repeated`** — a caller that answered its client from a receipt whose range
  is `0–0` has reported success for events that may or may not exist, with no
  error on any path and a durable row that gives every later retry the same wrong
  answer.
- **Control** Two. The same caller rolling the unit back leaves no row at all and
  answers `Unresolved`, so `Incomplete` distinguishes a committed claim from an
  absent one. And the caller of §UC-246 — which decides nothing and **completes**
  — leaves a complete row with an empty range whose retry answers `Repeated`, so
  `ErrIncomplete` is about the missing resolution and not about the missing
  events.

#### UC-223 A receipt older than the ledger's retention, and the clock the horizon is on  [edge]
- **Given** A ledger whose `Horizon` is one hour ago, a key whose row was swept,
  and four `Resolve`s: `Issued` two hours ago; `Issued` ten minutes ago; `Issued`
  zero; and `Issued` two hours ago against a ledger whose `Horizon` is derived from
  its own `MIN(recorded_at)` rather than from a process clock.
- **Then** `Expired` for the first, `Unresolved` for the second, **`Unresolved`
  for the third** — a caller that cannot date its own key gets no conclusion and
  `Resolution.Horizon` to make one with — and `Expired` for the fourth. Every
  standing carries the horizon.
- **Must not** An expired receipt must not read as a rollback, and must not read
  as `Unresolved` when the caller *did* supply an instant before it. **A zero
  `Issued` must not be refused with `ErrSpec`**: the canonical caller is an HTTP
  handler holding a retried idempotency key and not the instant it was minted, and
  a refusal there directs it at `time.Now()`, which is after every horizon and
  disables `Expired` for ever with no error anywhere.
- **Control** Two skew arms, run against the live database with the caller's
  instants deliberately offset from the ledger's: one caller clock behind the
  database's by more than the offset between `Issued` and the horizon, one ahead.
  Each is asserted to produce the standing the arithmetic says, and the case
  states in words that **both** outcomes are non-conclusions — neither a false
  `Found` nor a false "it did not happen" is reachable through skew, which is why
  the error term is tolerable and is written down rather than hidden.

#### UC-224 A fingerprint is the bytes that would be written, not the command  [edge]
- **Given** Two commands whose domain inputs differ but whose encoded records are
  byte-identical (a field the codec does not record), and two whose domain inputs
  are identical but whose encoded records differ (a codec that records a
  timestamp).
- **Then** The first pair produces one fingerprint; the second produces two.
- **Must not** `Repo.Digest` must not digest the `Change` values' Go
  representation, and must not reach the store. The second pair must not be
  presented as correct behaviour on its own: two fingerprints for one decision is
  the arithmetic working and **the mechanism disabled**, and §UC-247 is where that
  consequence is pinned.
- **Control** A recording store asserts `Digest` issued no call at all, and a
  malformed batch — a change decided for another stream, **a decision its
  declared codec could not encode**, a payload over `MaxPayload` — is refused by
  `Digest` with the same sentinel `Append` would give it. The carried refusal is
  named because it is the one step whose absence is a **collision** rather than a
  missing refusal: a change that never encoded carries no payload, so two
  different decisions of one fact digest alike and a key spent on one answers the
  other already done. The control drives that pair and asserts both refused.

#### UC-225 A key that renders  [edge]
- **Given** A `Key` built from a value carrying a request id, formatted into an
  error, a log line and a `%v`.
- **Then** Every rendering is `[operation key]`. `NewKey` refuses an empty
  string, one over the bound, one that is not valid UTF-8, one carrying a control
  character, and one carrying a bracket — the kernel's own identifier rule.
- **Must not** The value must not reach any message anywhere (UC-032 §11), and
  `Key` must not be comparable to a bare string at a door.
- **Control** The refusal messages themselves are asserted to contain none of the
  rejected inputs.

#### UC-226 A ledger that answers something no ledger answers  [edge]
- **Given** A `Ledger` returning, in turn: `won == true` beside a receipt whose
  key is a different one; `won == false` beside no receipt at all; and a
  `Horizon` in the future.
- **Then** Each is `ErrLedger`, refused at the door of `Claim` or `Resolve`, and
  the caller's transaction is left for the caller to roll back.
- **Must not** A ledger's answer must not be trusted for a rendering or for a
  comparison before it is checked — the same rule `Repo.apply` applies to a
  store's strings and numbers.
- **Control** A conformant ledger passes every check, so the arm is not refusing
  everything.

#### UC-227 A retry after a lost connection, end to end  [happy]
- **Given** A caller that mints a key, issues the command, and has its connection
  killed between the `COMMIT` and the response; then a **second process**, holding
  the key and nothing else, which loads (and is answered the moved version),
  decides the same facts, digests, and claims.
- **Then** The retry's `Claim` answers `Repeated` with the original range, and the
  caller answers its own client from the receipt. The stream holds exactly one
  copy of the events. Row counts are read in `psql`.
- **Must not** The retry must not re-load, re-decide and **append** at the new
  version — the shape that writes the decision twice with no error anywhere. It
  must not be given the first attempt's token, which no retry in a new process can
  have. And it must not answer `Collided`: a caller told *this key was spent on
  another operation* about its own operation has a refusal it cannot act on and a
  mechanism that never fires in the scenario it was built for.
- **Control** Two. The identical retry **without** a key, taking the naive path, is
  run and asserted to write the events twice, so the mechanism's value is measured
  rather than asserted. And the two attempts' fingerprints are asserted equal
  across the process boundary, which is the property the whole flow rests on.

#### UC-246 A decision that yields no changes  [happy]
- **Given** A domain that answers *"the order is already paid, there is nothing to
  do"*: a claim, a `Repo.Append` with an empty batch — which returns an **empty
  `Commit`** carrying the token's stream, `First` 0 and the invalid authority — and
  a `Held.Complete` with it, all in one unit that commits. Then a retry of the same
  key.
- **Then** `Complete` is admitted and writes `complete = true` with a zero range.
  `Resolve` answers `Found` with an empty range, and the retry's `Claim` answers
  `Repeated` with the same empty range, which the caller reports as *done, nothing
  changed*.
- **Must not** The empty commit must not be refused: a no-op is an ordinary
  outcome and refusing it leaves the row incomplete, which is the defect state.
  The authority comparison must not be run on it — an append that wrote nothing was
  written through nobody's transaction and carries the invalid authority by design
  — while the stream comparison and the transaction check still run.
- **Control** The same caller **without** the `Complete` leaves an incomplete row
  and its retry answers `ErrIncomplete` (§UC-222), so completing an empty commit
  is what distinguishes a finished no-op from an abandoned claim. `Once` is run as
  a second arm and is asserted to complete the empty commit without the caller
  doing anything.

#### UC-247 A codec that does not encode the same bytes twice  [edge]
- **Given** An aggregate whose fact records `time.Now()` in its payload, and the
  canonical retry: mint a key, claim, append, complete, commit; then retry the same
  operation with the same key.
- **Then** The two fingerprints differ, `Claim` answers `Collided`, and
  `ErrCollision` reaches the caller. **Nothing is appended twice and nothing is
  reported as done that was not**, on any arm.
- **Must not** The drift must not produce a duplicate append, a `Repeated`, or a
  silent success. The framework must not attempt to detect the non-determinism —
  it cannot, and a check that cannot be written must not be implied.
- **Control** The identical aggregate with the instant taken from the command
  instead of the clock retries to `Repeated`, so the case measures the obligation
  rather than the codec. The test is the one that makes the obligation visible:
  fail-closed and loud is the failure mode, and a deployment sees it on its first
  retry rather than in its data later.

#### UC-248 A completion that is not the claim's  [edge]
- **Given** One claim, and five completions of it: on a context carrying **no**
  transaction; on a context carrying a **second, different** transaction of the
  same store; with a commit whose `Stream()` is another aggregate's; a **second**
  `Complete` after a first that succeeded; and a `Complete` on a `Held` whose
  verdict is `Repeated`.
- **Then** `ErrSpec` for all five, before the ledger is written.
- **Must not** None of them may write: a completion that lands outside the claim's
  transaction writes `complete = true` and a range that survives the rollback of
  the events it names, which is the row §UC-218 exists to prevent produced by the
  call that follows it. A second `Complete` must not silently overwrite the range,
  and completing a repeat must not overwrite the first attempt's range with this
  one's.
- **Control** The claim's own transaction, its own commit, once, on a `Recorded`
  verdict, is admitted — the four refusals are discriminating and not a blanket
  one. A `psql` row count after each refused arm asserts the row still carries the
  range the first attempt wrote, or none at all.

#### UC-249 A resolve issued inside the writing transaction  [edge]
- **Given** Three `Resolve`s for a key whose claim has committed nothing yet: one
  on the claiming transaction's own context; one on a context carrying a
  transaction of the ledger's but not the store's; and one on a fresh context.
- **Then** `ErrSpec` for the first two, before the ledger is read. `Unresolved` for
  the third.
- **Must not** The first must not answer `Found`. A resolve that reads its own
  uncommitted row proves an operation that can still roll back, and "resolve first,
  then decide, in one unit" is an ordinary retry shape — so the refusal has to be
  at the door rather than in the documentation.
- **Control** The same three after the transaction commits: `ErrSpec`, `ErrSpec`,
  `Found`. The refusal is about the placement and not about the row, which is what
  makes it the mirror of `Committed`'s second refusal (§UC-209).

#### UC-250 One key, two streams  [edge]
- **Given** An operation that appends to two aggregates in one transaction under
  **one** key — claim for A, append to A, append to B, complete with A's commit —
  and then a second operation under that same key whose A batch is identical and
  whose B batch differs.
- **Then** The second answers `Repeated`, appends **nothing**, and is reported to
  its client as already done. That is the wrong answer and the case asserts it.
- **Must not** The case must not be written as if the framework catches this.
  Nothing in `receipt` can see the second append: `event` does not import this
  package and will not be made to.
- **Control** Two, and they are what give the inverted assertion its meaning. The
  same operation written the way the module page says — **a key per append**, two
  claims, two completions, one transaction — answers `Repeated` for A and
  `Collided` for B on the retry, which is the right answer. And completing A's
  claim with **B's** commit is `ErrSpec` (§UC-248), so the detectable half of the
  hazard is refused and only the undetectable half is pinned here. The day
  something closes it, this case fails and says the positive arms now prove
  something else.

#### UC-251 A caller that ignores a verdict, and one that claims too late  [edge]
- **Given** Two open-coded callers. The first claims, is told `Repeated`, appends
  anyway, and calls `Complete`. The second appends first and claims afterwards, in
  one unit, twice — two retries of one operation at two expected versions.
- **Then** The first's `Complete` answers `ErrSpec` (the verdict is not
  `Recorded`), the caller's `crud.InNewTx` rolls back, and `psql` shows **one**
  copy of the events — the transaction is the enforcement, which is the only
  enforcement a package that opens no transaction can have. The second's two
  claims both succeed and `psql` shows **two** copies: claim-after-append is
  refused by nothing, and the case asserts the damage.
- **Must not** The second arm must not be presented as caught. `receipt` cannot ask
  whether an append has already happened on a context, and adding a question to
  `Repo` to let it would put the kernel in the business of a subsystem it does not
  know about.
- **Control** The same two operations through `Once` — where the work cannot run
  before the claim, because `Once` is what calls it — leave one copy each, which is
  what makes `Once` the spelling the module page gives and this case the argument
  for it. A first-arm variant that appends after `Repeated` and **never** calls
  `Complete` is run too, and asserted to leave two copies: the refusal is at
  `Complete`, so a caller that skips it skips the enforcement.

---

## Group BC — historical state at a version (ES-08)

#### UC-228 The state before a disputed change  [happy]
- **Given** A stream with twelve events and `StateAt(ctx, id, 7)`.
- **Then** The state is what the first seven events fold to, in order, by the same
  folds `Load` uses. Reading version 12 gives the same value `Load` gives.
- **Must not** It must not consult a cache, must not stop early on a fold that
  succeeds, and must not return a second value of any kind.
- **Control** The same read at version 6 differs from the read at version 7 in
  exactly the way the seventh fact's fold says, so the boundary is inclusive and
  is measured rather than assumed.

#### UC-229 The bound is respected across a page boundary  [edge]
- **Given** A store whose `StreamPage` is 4 and a stream of 11 events, read at
  versions 1, 4, 5, 8 and 11.
- **Then** Each answers the fold of exactly that prefix. At version 5 the loop
  reads two pages and discards three envelopes of the second; at version 4 it
  reads one page and discards none; at version 11 it reads three pages, the last
  short.
- **Must not** It must not read a page it does not need: a recording store asserts
  the page count for each bound, and the count at version 5 is two rather than
  three.
- **Control** The same reads through `Load` cost three pages each, so the bound is
  doing work.

#### UC-230 A version past the end of the stream  [edge]
- **Given** A stream of five events and `StateAt(ctx, id, 9)`.
- **Then** `ErrVersion`, and the zero state. The refusal names neither 5 nor 9.
- **Must not** It must not return the state at version 5, must not return the zero
  state beside a nil error, and must not cost a second confirming read.
- **Control** `StateAt(ctx, id, 5)` on the same stream succeeds, so the refusal is
  about the bound and not about the stream.

#### UC-231 A stream with no events, and version zero  [edge]
- **Given** `StateAt(ctx, id, 1)` against an identity that has never been
  appended to; and `StateAt(ctx, id, 0)` against a stream with twelve events.
- **Then** `ErrVersion` for both. The first is UC-230 with a head of zero; the
  second is refused because version zero is the empty stream and answering the
  zero state hides the caller's off-by-one.
- **Must not** Neither may answer the zero state beside a nil error, which is the
  answer Marten gives for four different situations a caller cannot tell apart.
- **Control** The refusal for the empty stream and the refusal for a missing
  aggregate are asserted to be the **same** refusal, and the module page says why
  the two are not told apart.

#### UC-232 An unreadable event inside the prefix  [edge]
- **Given** A stream in which version 4 carries a type this build does not
  declare, and `StateAt(ctx, id, 9)`.
- **Then** `ErrUnknownType`, and the **zero** state — never the accumulator
  holding versions 1–3.
- **Must not** It must not return a partial state, and it must not be a new code
  path: the case asserts the refusal comes from the loop `Load` already uses.
- **Control** The same three variants — an undeclared revision, a refusing
  upcaster, a recorded payload over `MaxPayload` — each give their own sentinel
  and the zero state, and the identical stream read through `Load` gives the
  identical refusal, so ES-08 inherited the behaviour rather than reimplementing
  it.

#### UC-233 A store that answers a page out of order  [edge]
- **Given** A store returning `[v3, v1, v2]` for the first page, and
  `StateAt(ctx, id, 2)`.
- **Then** `ErrBackend` from `checkPage`, which ran on the whole page **before**
  anything was truncated.
- **Must not** The truncation must not happen first — truncating to two envelopes
  and then checking would pass `[v3, v1]` through a check that only looks at what
  it was given, and fold a state that is wrong at the right version with no
  refusal anywhere.
- **Control** The same store returning the page in order is accepted at the same
  bound.

#### UC-234 The result cannot be appended with  [happy]
- **Given** The specified signature of `StateAt`.
- **Then** It returns `(S, error)`. There is no token, no provenance value and
  nothing that `Repo.Append` would accept.
- **Must not** A later change must not add one: a compile-time test in
  `_examples` attempts `repo.Append(ctx, /* the second return */ …)` in a
  commented form and the surface baseline is the check.
- **Control** `Load`'s signature is asserted unchanged in the same test, so the
  absence is a property of this call and not of the type.

#### UC-235 There is no timestamp boundary  [edge]
- **Given** The specified surface and the module page.
- **Then** No parameter, overload or option takes a time. The module page states
  that a timestamp is neither business time nor commit order, cites `recorded_at`
  being `statement_timestamp()`, and says that if one is ever added it can only
  be a lookup that resolves to a version.
- **Must not** The page must not describe `recorded_at` as something a caller may
  order by, and no example may sort by it.
- **Control** A doc check asserts the sentence is present, so a later rewrite
  that drops it fails rather than passing quietly.

#### UC-236 The store contract did not move  [happy]
- **Given** `make api` after the phase, and `event/eventtest`'s inventory.
- **Then** `Store` and `Log` have the same methods with the same signatures;
  `Limits` has the same five fields; `eventtest.inventory()` has the same twenty
  sections with the same `needs` gates; and a store certified before this phase
  is certified after it with no change.
- **Must not** No version ceiling, filter or predicate may appear on
  `ReadStream`.
- **Control** The `event` section of `docs/api/surface.md` grows by exactly three
  lines — `Repo.Digest`, `Repo.StateAt`, `ErrVersion` — and the diff is read by a
  person, as it always is.

---

## Group BD — the snapshot contract (ES-09)

These four are **obligations on the phase that eventually writes the code**, not
tests phase 5 runs. They are stated as use cases so that phase implements a
decision rather than re-deriving one, and so the gate below them is a document a
reviewer can check rather than an intention.

#### UC-237 A snapshot is refused before its payload is read  [edge, contract]
- **Given** A stored snapshot whose row differs from the current declaration in
  exactly one of: the backing, the stream, the snapshot payload revision, the
  declared-shape digest, or the author's computation version.
- **Then** Each of the five is refused on the row's own columns, before a byte of
  the payload is deserialised, and the load falls back to full replay and returns
  the correct state.
- **Must not** The version may not be stored inside the snapshot payload — a
  filter that must deserialise first fails on exactly the snapshots it exists to
  exclude.
- **Control** A row matching on all five loads from the snapshot, proven by a
  counting store reporting how many envelopes the tail read. Without that control
  the five refusals pass whether or not the snapshot was ever consulted.

#### UC-238 A fold changes and nobody bumps the version  [edge, contract]
- **Given** A build whose fold body changed, with no fact added, no revision
  appended and no bump of the computation version.
- **Then** The snapshot loads and the state served is one no current code would
  compute. **Nothing detects it.**
- **Must not** The documentation must not imply otherwise, and no test may assert
  a detection that does not exist.
- **Control** This case's test **asserts the stale state**, so the day somebody
  closes the hole the control fails and says the positive cases now prove
  something different. It is the `gate_relscope_test.go` pattern used to pin an
  absence.

#### UC-239 An unreadable event in the tail is not hidden by the fallback  [edge, contract]
- **Given** A valid snapshot at version 20 and an event at version 23 carrying a
  type this build does not declare.
- **Then** `ErrUnknownType`, reported as a tail failure. The load does **not**
  fall back to full replay.
- **Must not** The fallback may not be entered on a failure of a read or a fold
  of an event. Entering it replays from version 1, hits the same event, and
  returns a refusal with the snapshot's involvement erased — so an operator
  debugs a replay problem that is really a tail problem and suspects the snapshot
  that was working.
- **Control** Corrupting the **snapshot** row instead, at the same stream and the
  same tail, falls back and returns the correct state — so the discriminator is
  which call failed and not which error class came back.

#### UC-240 Snapshot plus tail equals full replay  [happy, contract]
- **Given** A corpus containing at least a multi-event command that jumps a
  cadence boundary, a fact with a retained revision needing an upcast in the
  tail, and an aggregate whose fold is order-dependent.
- **Then** For every cadence boundary of every stream in the corpus,
  `snapshot + tail` is deeply equal to `full replay`. Green twice in a row, under
  `-race`, against the live database.
- **Must not** The tail's lower bound must not be inclusive of the snapshot's own
  version, and its upper bound must not be exclusive of the version asked for.
- **Control** Moving the half-open boundary by one in either direction — applying
  the base event twice, or dropping the ceiling event — must fail this case. A
  proof that survives both perturbations proves nothing.

#### UC-241 The gate is a document, and phase 5 has not met it  [edge, contract]
- **Given** The repository at the end of phase 5.
- **Then** `TestNoSnapshotAuthorityIsDeclaredOrPromised` is green and
  un-loosened; `docs/api/surface.md` publishes no snapshot name; [[D-132]]'s
  invariant is unchanged; and **D-145** exists, states the contract in full, and
  records that neither gate is met and that no line may be written until both
  are.
- **Must not** [[D-132]] must not be marked superseded, and the enforcement
  test's regexp and package list must not be narrowed.
- **Control** The fixture inside that test still reports its planted snapshot
  type and baseline line, so the check has not gone vacuous while nobody was
  looking at it.

---

# 4. Invariants

Each states the property and **how it is falsified**.

#### INV-108 A wait's target is a minted position, and `Highest` is not restated
- **Statement** `projection.Mark` has unexported fields and exactly two minting
  doors, both of which take a number that came out of a store — an
  `Envelope.Position` read back after the caller's commit, or a `Barrier.At`
  folded from checkpoint rows. `WaitSpec.Committed` additionally carries the
  distinct sequence keys the spec's sequencer answered for the commit's own
  envelopes, and `MarkOf` carries no key at all; **both carry the projection the
  mark was minted from, and `Wait` refuses with `ErrSpec` a mark whose projection
  is not `spec.Of`'s**; neither carries an `Envelope` or a payload, and `String()`
  answers `"[mark]"`. The zero `Mark` is refused at `Wait`'s door, because a
  position is never zero. Nothing in the wait's surface, doc or module
  page describes `Progress.Highest` as "the highest position of the page": it is
  the same number and a different promise ([[D-128]]).
- **Falsified by** §UC-211's zero-mark refusal beside §UC-204's admitted one;
  §UC-243's two-sequence mark; **a mark minted from one `WaitSpec` and waited on a
  second whose `Of` differs, refused, beside the control that the same mark on its
  own spec reaches**; a compile-level check that `Mark` has no exported
  field and no third minting door, and that `%v` of one renders no payload;
  and a doc check over `docs/modules/*/projection.md` for the forbidden
  restatement, with a fixture paragraph that must be reported.

#### INV-109 A mark is resolved after the commit, never inside the transaction that wrote it
- **Statement** `WaitSpec.Committed` refuses when `Store.Transaction(ctx)` answers
  a valid authority, refuses an empty commit, refuses a store that does not show
  `commit.Last()`, refuses a zero `Position`, refuses a page longer than the store's
  own `StreamPage` and one **any** envelope of which is another stream's or is not
  one version above the envelope before it, and refuses a nil `Sequence` beside a
  non-nil `Park`. It reads the commit's own range and nothing else: one
  `ReadStream` for a commit that fits a page, one more per page beyond it, and no
  other call of any kind.
- **Falsified by** §UC-209's pair (bound context refused, fresh context admitted)
  and §UC-210's pair (open transaction and rolled-back transaction giving the
  same `ErrUncommitted`), plus a recording store asserting the read count against
  the commit's size.

#### INV-110 The park is asked before the census, on every poll, under the keys the mark carries, and nothing else of the park is asked
- **Statement** Each poll asks `Park.Sequences` first and, when that count is
  non-zero, `Park.Holds` once per **distinct sequence key the mark carries**,
  stopping at the first true answer. A true answer returns `ErrParked` immediately
  with `Visibility{Parked: true}` and reads no census. A mark that carries no key
  — every `MarkOf` mark — is refused at `Wait`'s door beside a non-nil `Park`
  rather than admitted with the question skipped. **`Park.Holes` and `Park.Park`
  are never called by a wait**, on any generation and at any `Quarantined` count,
  so exactly one sentence of `Park`'s contract moves and it is `Holds`'s.
- **Falsified by** §UC-206's pair — `Park` supplied answers `ErrParked`, `Park`
  nil answers `Reached: true` for the same parked event — §UC-207, where the
  park answers on the first poll while the census is still behind, §UC-211's
  fourth arm (a barrier mark beside a `Park` refused, beside a `Park`-nil control
  that reaches), and §UC-243's two-key commit. §UC-208's recording park pins the
  call counts in both directions **and the absence of a bound transaction on every
  call it did make**.

#### INV-111 A wait's five declared facts are derived from the projection's own spec, and a hand-written one carries the obligation
- **Statement** `WaitOf(spec, over)` derives `Checkpoints`, `Park`, `Sequence`
  (applying `ByStream()` where the spec's is nil), `Generations` and `Of` from the
  `projection.Spec` the runner was built from, and takes the cover it was built
  out of. It performs no I/O and starts nothing. A `WaitSpec` assembled by hand
  stays legal, for the reason `Cover` gives for its own set, and it carries three
  obligations nothing here can check: `Sequence` must be the sequencer the
  projection runs, `Park` must be supplied when the projection parks, and `Over`
  must be the cover the rows are recorded at. The framework holds no route from a
  `Checkpoints` to a `Spec`, so it checks none of them and claims to check none;
  the obligations are stated beside `Sequencer`'s existing three (total, pure,
  stable). The same statement covers the mark and the projection being over one
  log: a `Checkpoints` may legitimately live in another database from the log, so
  the backing comparison that would catch it would refuse a correct composition.
- **Falsified by** §UC-242's four arms — the derived spec answering `ErrParked`
  beside three hand-written ones that each report reached for a change that is not
  in the read model — and by a doc check that the module page states the three
  obligations together rather than implying a check.

#### INV-112 A wait is read-only, starts nothing, and reuses no tracker across a save
- **Statement** `Wait` runs on its caller's goroutine, opens no transaction,
  starts no goroutine, writes no log line, span or metric, and costs nothing when
  nobody is waiting. Each poll builds a fresh `event.Track` per cover member and
  calls `Load`; no tracker is retained between polls and none is ever saved
  through.
- **Falsified by** `scripts/extensions_test.go`'s `startsNothing` arm over the
  new files; a recording `Checkpoints` asserting no `Save`, no `Forget` and one
  `Load` per member per poll; [[D-132]]'s no-line test extended over the wait; and
  a case that runs `Wait` concurrently with the projection's own loop under
  `-race` and asserts neither fence moves.

#### INV-113 What a reached wait promises, what it does not, and what every other exit means
- **Statement** `Reached` means: the minimum `Progress.Highest` across the named
  cover of the named generation is at or above the mark, so every event at or
  below the mark has been **delivered** to that generation — and, when a `Park`
  and a `Sequence` were supplied, that none of the mark's sequences is parked, so
  it was **applied**. It promises nothing about a second projection, a second
  database, a read replica, or anything above the mark. About a second
  **generation** it promises nothing either, and with a `Generations` supplied it
  refuses rather than promising: the generation `Of` names was the one reads
  resolved to at the wait's first poll and at its last, and the gap between that
  last read and the caller's own read is the cutover's named overlap window.
  `Behind` is an upper bound and a hint, never a count of undelivered events.
  **A wait that returns neither `Reached` nor a deadline has concluded nothing
  about the read model**: `ErrParked` says the change will not arrive without a
  redrive, `ErrGeneration` says the question was asked about the wrong generation,
  and a poll that could not be made is that store's own refusal, unreclassified —
  none of the three is a statement that the mark was or was not delivered.
- **Falsified by** §UC-204's control (a second projection of the same log is
  asserted to be unaddressed by the same wait); §UC-206's `Park`-nil arm, where
  delivered is not applied; §UC-245's three cutover arms with their `Generations`-nil
  control; §UC-244's first-poll and later-poll failures; and a doc check that the
  five negative sentences are on the module page.

#### INV-114 A claim is one transaction with its append at all three of its doors, or it is refused
- **Statement** `Claim` refuses unless `Ledger.Transaction(ctx)` and
  `Store.Transaction(ctx)` compare `Same`, unless the store claims transactions,
  and unless a transaction of the store's is bound. **`Held.Complete` refuses the
  same three, plus a transaction that is not the claim's own, a commit whose
  stream is not the claim's, a commit whose authority is not the claim's (except
  an empty one, which has none by design), a second call, and a verdict that is not
  `Recorded`.** **`Resolve` refuses the mirror image**: a transaction of the
  store's or of the ledger's bound to its context at all, because a resolve inside
  the writing transaction reads its own uncommitted row. The claim precedes the
  append; the completion follows it and is not optional; all three are in the
  caller's one transaction and a rollback takes them back. The serialisation is the
  primary-key index's, nothing in this framework's path chooses an isolation
  level, and what the level decides is how a loser fails — a repeat at READ
  COMMITTED, a `40001` rollback whose retry repeats at the two stricter ones
  (§1.2).
- **Falsified by** §UC-218's pair (two pools refused, one pool admitted),
  §UC-219's two door refusals, §UC-248's five completion refusals with their
  admitted control, §UC-249's resolve-inside-the-unit refusal with its
  after-the-commit control, §UC-214's rollback control, and §UC-217's race
  with its rollback control — read in `psql`, not from Go.

#### INV-115 An absent receipt is never a rollback, and the horizon is the ledger's own clock
- **Statement** `Resolve` answers `Unresolved` for an absent row within the
  ledger's horizon, and that answer is the same whether the writing transaction
  is still open or has rolled back. Nothing in the surface offers a fourth
  standing that proves a rollback, and the module page names
  `idle_in_transaction_session_timeout` as the operational bound on how long
  `Unresolved` can persist rather than inventing a second mechanism.
  Every instant this package stores or publishes comes from the **database's**
  clock — `recorded_at` is `statement_timestamp()` and never a parameter, and
  `Horizon` is `MIN(recorded_at)` or `now() - retention` computed in SQL — so the
  only cross-clock comparison is `ResolveSpec.Issued`, which is **optional**. An
  absent `Issued` answers `Unresolved` and carries the horizon; it is never
  `ErrSpec`, because a refusal there sends the canonical caller to `time.Now()`
  and turns `Expired` off for ever.
- **Falsified by** §UC-220's three arms — open, committed, rolled back — with the
  case asserting that the first and third are indistinguishable; §UC-223's four
  standings and its two skew arms; a check that the published claim statement binds
  no instant as a parameter; and a doc check that no page claims otherwise.

#### INV-116 A fingerprint covers one append's whole range and nothing a store or a load assigns
- **Statement** The digest is SHA-256 over a length-prefixed encoding of the
  composed stream and each record's type, revision and payload in order. It
  excludes positions, recorded instants, the backing, the key **and the expected
  version the append was decided at** — the last because a retry in a new process
  loads the version its own first attempt moved the stream to, and an identity it
  cannot reproduce is one that never matches. It covers **one** `Repo.Append` to
  **one** stream: the digest of a second append in the same unit is a second
  fingerprint under a second key. It is computed by `Repo.Digest` from the bytes
  that would be written, and that call issues no store call and writes nothing.
  **The encoding is frozen.** A fingerprint outlives the build that computed it —
  it is compared against one a later build recomputes, and inside a retention
  window a deployment is routine — so the field order, the eight big-endian bytes
  of each length and of each revision, and the composed stream that opens the
  preimage are part of the contract and not an implementation choice. A change to
  any of them recomputes a different value for every key still in the window and
  answers each legitimate retry as a collision on an operation nobody performed.
- **Falsified by** §UC-216's collision table — a byte, a stream, an order, each of
  which must collide — beside its **non**-colliding version variant; §UC-215's
  assertion that two attempts at different versions digest equal; §UC-224's two
  pairs; a recording store asserting `Digest` made no call; and six golden 32-byte
  vectors, which are the only absolute assertions about the value — every other
  arm is `A != B` inside one process, and a wholesale change of layout preserves
  injectivity and passes them all.

#### INV-117 A repeat answers from a complete receipt, and an unresolved one answers nothing
- **Statement** `Repeated` is answered only for a row that exists, is **complete**
  and compares equal, and on it the framework appends nothing and offers nothing to
  append with: `held.Receipt()` is a record of what happened, carries no `At[S]`,
  and no call takes one. Its range may be empty, which is a decision that yielded
  no changes and completed. A row that exists and is **not** complete is
  `ErrIncomplete` and no verdict at all — whether that operation's events reached
  the log is not a question its row answers, and a retry told *already done* from a
  `0–0` range is the report of a success that may never have happened. UC-032 §8's
  rule — recovery is a fresh load and a fresh decision, never re-proposing the same
  facts at whatever version the store reports — is unchanged.
- **Falsified by** §UC-215 (stream version unchanged after the repeat), §UC-222's
  pair (an unresolved claim refuses the retry; the same caller rolling back leaves
  no row), §UC-246's completed empty range answering `Repeated`, §UC-227's
  end-to-end retry with its naive control that writes twice, and a surface check
  that no `receipt` type exposes an `At[S]`.

#### INV-118 A bounded read refuses rather than returning a partial state
- **Statement** `StateAt` returns the fold of the complete prefix from version 1
  to the version asked for, or the zero state and a refusal. A version of zero, a
  version past the head and a stream with no events are all `ErrVersion`; an
  unreadable event anywhere in the prefix is its own sentinel and the zero state.
  The failure path is `replay`'s own — `this.nothing(err)` — and is not a second
  implementation.
- **Falsified by** §UC-230, §UC-231 and §UC-232, the last of which asserts the
  identical stream read through `Load` gives the identical refusal; plus
  §UC-233's out-of-order page, which pins that `checkPage` runs on the whole page
  before anything is truncated.

#### INV-119 A historical read yields nothing that can append, and the boundary is half-open at neither end
- **Statement** `StateAt` returns `(S, error)`. There is no token, no provenance
  value, and no second return of any kind. The prefix is inclusive of the version
  asked for and begins at version 1; the loop stops reading the moment the
  accumulated version reaches the bound, folds nothing above it, and issues no
  confirming read.
- **Falsified by** §UC-234's signature check beside `Load`'s unchanged one;
  §UC-228's version-6-against-version-7 comparison; and §UC-229's recorded page
  counts, where a bound at the last version of a page costs one page and not two.

#### INV-120 The store contract and the conformance inventory do not move
- **Statement** `Log`, `Store`, `Checkpoints`, `Limits`, `Capabilities`,
  `Envelope`, `Record`, `AppendRequest` and `Outcome` are unchanged in every
  field and every method. `eventtest.inventory()` holds the same twenty sections
  with the same `needs` gates. A store or checkpoint store certified before this
  phase is certified after it, unchanged.
- **Falsified by** §UC-236; `make api`'s diff over the `event` section being
  exactly three added lines; and `eventtest`'s own inventory test.

#### INV-121 What a snapshot is bound to, and when the comparison is made  *(contract, D-145)*
- **Statement** A snapshot is bound to the backing, the stream, the version, its
  own payload revision, and a state-computation version with two components — a
  framework-computed digest over the declaration's shape, and an author-declared
  integer. All five are compared on the stored row's own columns, before any byte
  of the payload is deserialised. A snapshot is never upcast. A changed fold body
  with no other change is undetectable and the contract says so in those words.
- **Falsified by** §UC-237's five refusals with its counting-store control, and
  §UC-238, whose test asserts the stale state so that closing the hole fails the
  control. Falsified for phase 5 itself by D-145 not existing, or by its stating
  a detection it does not have.

#### INV-122 The fallback is the snapshot's alone, and it is observable  *(contract, D-145)*
- **Statement** A full replay is entered when reading, decoding or validating the
  **snapshot** fails, and never when reading or folding an **event** fails. An
  optimised load reports in its return value how the state was obtained, because
  [[D-132]] forbids the log line the sources rely on. A drifted snapshot is
  replaced on its next write and no event is ever deleted; a snapshot never
  replaces an event, which is the storage behaviour Axon documents and this
  contract refuses.
- **Falsified by** §UC-239's pair — a bad tail event refuses, a bad snapshot row
  falls back — and §UC-240's equivalence proof with its two perturbations. For
  phase 5, by D-145 omitting either clause.

#### INV-123 Nothing this phase adds starts anything, opens a transaction, or writes a line
- **Statement** No constructor in `event/receipt` or in the wait — `NewKey`,
  `NewFingerprint`, `WaitOf`, `MarkOf` — performs I/O, reads the environment,
  starts a goroutine or mutates anything outside the value it returns. Nothing opens, commits or rolls back a transaction — the
  transaction is the caller's throughout ([[D-118]], [[D-126]]). Nothing emits a
  log line, a span or a metric ([[D-132]]).
- **Falsified by** `scripts/extensions_test.go`'s `startsNothing` and
  no-package-state arms extended over the new files;
  `scripts/event_test.go`'s dependency charge, which must show `event/receipt`
  reaching `crud`, `errs` and `utils` and nothing else; and a recording source
  asserting no `Begin`, `Commit` or `Rollback` on any path.

#### INV-124 Every refusal names its field, wraps a published sentinel, and names no data
- **Statement** Every refusal added by this phase wraps one of the sentinels §5
  publishes, names the field or the rule it is about, and carries no key, no
  payload, no version, no position, no cursor and no identity (UC-032 §11).
  `receipt.Key` renders `[operation key]`; `event.Position` values appear in no
  message.
- **Falsified by** a refusal-message test over every new error path, in the shape
  `event`'s `refusalmessages_test.go` already has, asserting the input never
  appears in the output; and §UC-225's rendering table.

#### INV-125 A decision must encode to the same bytes under one key, and nothing here can check it
- **Statement** The fingerprint is over the payload bytes the caller's codec
  produced, so two attempts at one operation are the same operation **only if they
  encode identically**, in every process and at every version. A codec that
  records a clock, a freshly minted id or a map in iteration order disables the
  mechanism for its aggregate. The framework cannot detect that and does not claim
  to; it states the obligation beside `Repo.Digest` in the shape INV-111 uses, and
  it makes the failure safe rather than silent — a drifting fingerprint is
  `Collided`, which is a refusal with a published sentinel, never a duplicate
  append and never a false *already done*.
- **Falsified by** §UC-247's pair — a timestamp-recording codec whose retry is
  refused, beside the same aggregate taking its instant from the command and
  retrying to `Repeated` — and by a doc check that the obligation is on the module
  page beside `Sequencer`'s three.

#### INV-126 One operation key covers one append, to one stream
- **Statement** A receipt records one stream and one range, and a key covers the
  one append the claim names. A caller whose operation writes two aggregates
  carries two keys, and its one transaction is what makes them atomic. Two of the
  three ways to get this wrong are refused at `Complete` — a commit of another
  stream, and a second completion of one claim — and the third, a second append the
  claim never named, is an obligation: nothing in `receipt` can see an append it
  was not handed, because `event` does not import this package.
- **Falsified by** §UC-250's inverted assertion (one key over two streams reports
  `Repeated` for an operation that differs at the second, and the case asserts that
  wrong answer) beside its two controls — the key-per-append spelling answering
  `Collided`, and the cross-stream completion refused — and by a doc check that the
  module page carries the key-per-append recipe.

---

# 5. The exported Go surface

Every name below is what `make api` must show, and nothing else is exported.
Receiver name is `this` throughout. Comments follow the house rule: one only
where a complex function, a non-trivial algorithm or an invariant the code cannot
make visible needs it.

## 5.1 `github.com/frostgrove/vv/event` — two methods and one sentinel

```go
// The bytes this append would write, digested: SHA-256 over a length-prefixed
// encoding of the composed stream and each record's type, revision and payload in
// order. Length-prefixed because "a"+"bc" and "ab"+"c" are two different batches
// and one preimage otherwise.
//
// It runs the first four of Append's six steps — the token's key, every change's
// stream and aggregate, each change's own carried refusal, the store's bounds —
// and answers the same refusals Append would, so a caller that digests first
// learns a malformed append before it claims anything. It issues no store call
// and writes nothing.
//
// THE VERSION THE TOKEN WAS LOADED AT IS NOT IN IT, and a retry is why. A retry
// that arrives in a new process holding an operation key loads what the store now
// holds, which is the version the first attempt moved the stream to, so a digest
// over the version cannot be reproduced by the one caller the mechanism exists
// for. Append's own concurrency check is untouched and is where the anchor
// belongs; what the digest answers is whether two attempts are the same
// operation, and an operation is its stream and its records.
//
// Two attempts are the same only if they encode the same bytes. A codec that
// records a clock, a fresh id or a map in iteration order encodes differently
// every time, and under one operation key that is a refusal on every retry rather
// than a duplicate append — loud, and never wrong. Take such a value from the
// command, the state or the operation key, the way a Sequencer takes none of them
// from a clock.
func (this *Repo[S, ID]) Digest(at At[S], changes ...Change[S]) ([32]byte, error)

// The state this aggregate held at a version, folded from the complete prefix
// and nothing else. It returns no token, and that is the design rather than an
// omission: a value that looks like one invites a Load-Decide-Append whose
// decision was made against the history this call left out.
//
// The prefix is dense from version 1 and inclusive of the version asked for. A
// version of zero, a version past the end of the stream and a stream with no
// events are all ErrVersion — the three are one answer because an append-only
// log tells a stream that is empty and a stream that never existed apart nowhere
// a reader can see. An event the declaration cannot read is its own refusal and
// the zero state, never the prefix that happened to fold first.
//
// It pages through the same ReadStream a load does and truncates the last page
// in Go: the over-read is at most one page whatever the stream's length, so a
// ceiling on the store contract would buy one partial page of I/O and cost every
// store author a signature. The page is checked whole before it is truncated,
// because a page that arrives out of order folds to a state that is wrong at the
// right version with the right count and no refusal anywhere.
func (this *Repo[S, ID]) StateAt(ctx context.Context, id ID, version Version) (S, error)

// errors.go, in the bad-request class beside ErrKey
var ErrVersion = fmt.Errorf("event: the version this read was bounded at is not one this stream holds: %w", crud.ErrBadRequest)
```

**Three lines, and no field, method or signature moves.** `Store`, `Log`,
`Checkpoints`, `Envelope`, `Record`, `AppendRequest`, `Limits`, `Capabilities`
and `Outcome` are untouched, so `eventtest`'s twenty sections are untouched and a
third-party store's certified suite is untouched (INV-120).

**Three comments are reworded and nothing else in the kernel changes.**
`event/token.go:23`, `event/eventmemory/doc.go:24` and
`event/eventmemory/transaction.go:35-36` use "receipt" for `event.Commit`; they
become "commit" or "commit token", so the word has one meaning once
`event/receipt` exists. `event/eventtest`'s local variables named `receipt` are
left alone — they are locals in a package that never imports the new one, and
renaming eleven identifiers would put a large no-behaviour diff into the baseline
re-record beside the changes a reviewer needs to read.

## 5.2 `github.com/frostgrove/vv/event/projection` — the wait

```go
// The position a wait is waiting for, and it is minted rather than named: the
// fields are unexported and both minting doors take a number a store produced. A
// caller that could write one would be waiting for evidence it invented, which is
// the same door Cutover closes by having no barrier field.
//
// What else it carries differs by door, and decides what a wait may promise.
// WaitSpec.Committed attaches the sequence keys the spec's own sequencer answered
// for every envelope of the commit — so the park can be asked about this caller's
// own change — and MarkOf attaches none, because a barrier is folded from
// checkpoint rows and there is no envelope to ask.
//
// Both doors record the projection the mark was minted from, and Wait refuses one
// whose projection is not spec.Of's. One rule and not two: a barrier of another
// projection is evidence about another log, and a Committed mark's sequence keys
// are one projection's sequencer's answers, so asking a second projection's park
// under them answers false for an event it parked and reports Reached.
//
// It holds no Envelope and no payload: the keys are computed once, at the mint,
// rather than by running the application's sequencer on every poll. String()
// answers "[mark]".
//
// The zero value is the one a caller can build and it is refused at Wait's door:
// positions are drawn from an identity sequence starting at one, so zero is
// never a real position and is exactly "not minted".
type Mark struct { ... }

func (this Mark) At() event.Position
func (this Mark) Zero() bool
func (this Mark) String() string

// The five the projection's own Spec already declares, taken from it instead of
// restated by the request path: a wrong Sequence reports reached for a parked
// event, a nil Park reports delivered where the caller asked applied, and a
// cover that is not the one the rows are recorded at folds a minimum over the
// wrong set. It applies ByStream() where Spec.Sequence is nil, which is the
// default New applies and the one a hand-written spec forgets.
//
// It performs no I/O, starts nothing and refuses the zero Cover and a spec that
// names no identity. Until, Every and Ticks are the caller's to fill in; Until is
// the only field a request path has any business writing.
func WaitOf(spec Spec, over Cover) (WaitSpec, error)

// The position the last event of this commit went in at, read back from the
// store AFTER the caller's transaction committed. Commit carries no position by
// design — inside an uncommitted transaction an envelope's position is
// unspecified, and one store has it already while another answers zero — so the
// map from a version to a position costs a read and this call is where that round
// trip is, rather than hidden inside the wait.
//
// It reads the commit's whole range and not only its last event, because a
// Sequencer is any function of an envelope and a commit of three facts can belong
// to three sequences: a mark that carried the last one's key would ask the park
// about one third of what it is waiting for. One ReadStream for any commit that
// fits a page, and one more per page beyond it.
//
// It is a method and not a free function because the sequence keys must come from
// the sequencer the projection runs, and the spec is where that is. Six refusals:
// an empty commit; a transaction of this store's bound to ctx, which would mint a
// mark for an append that can still roll back; a store that does not show
// commit.Last(), which is ErrUncommitted and tells "not committed yet" from
// "rolled back" no better than a second connection can; a zero position, which is
// a store that assigns at commit read before the commit; a page this store's own
// answer is not honest about — longer than the stream page it publishes, or
// carrying an envelope that is another stream's or not one version above the one
// before it — which is ErrBackend; and a nil Sequence beside a non-nil Park,
// which would mint a mark the park cannot be asked about.
func (this WaitSpec) Committed(ctx context.Context, store event.Store, commit event.Commit) (Mark, error)

// The mark a barrier was observed at. Observe folds it from a generation's own
// checkpoint rows, so it is store-issued in the only sense that matters here. It
// carries no sequence key, so Wait refuses it beside a non-nil Park rather than
// answering "delivered" to a caller that asked for "applied".
func MarkOf(barrier Barrier) Mark

// Of names a projection and a generation at Whole(); Over is the cover its rows
// are recorded at, checked by NewCover. Park is optional and a nil one asks no
// park question — but a projection that parks and is waited on without its queue
// is answered "delivered" where the caller asked "applied", which is what
// Sequence beside Park exists to prevent. Sequence must be the sequencer the
// projection runs and nothing here can check that, which is why WaitOf is the
// spelling the module page gives and this struct is the one a host that assembles
// its runners by hand keeps.
//
// Generations is optional and is what scopes a wait to the generation a read
// resolves to: supplied, Wait reads Active on its first poll and again on the
// poll that would answer Reached, and refuses with ErrGeneration when it is not
// the generation Of names. Nil says the caller knows which generation it is
// reading, which a deployment tool waiting on an arriving one does.
//
// Every defaults to 50ms and the cost is one Sequences count plus one checkpoint
// Load per cover member per poll, per waiting caller. Ticks is runtime.Ticks so a
// test drives the interval rather than sleeping.
//
// The deadline is the caller's context. There is no stale-read field: a wait that
// did not reach returns a filled-in Visibility beside a refusal, so serving stale
// data is a branch somebody wrote rather than a flag everybody passes.
type WaitSpec struct {
	Checkpoints event.Checkpoints
	Park        Park
	Sequence    Sequencer
	Generations Generations
	Of          Identity
	Over        Cover
	Until       Mark
	Every       time.Duration
	Ticks       runtime.Ticks
}

// What the wait saw, filled in on every path that made a poll.
//
// Behind is an upper bound and a hint, for the reason Readiness.Behind already
// carries: a log burns a position for a rolled-back append and for an
// optimistic-concurrency loser, so the distance between two positions is not a
// count of undelivered events.
//
// Moved is whether At changed across this wait's polls, and it is the one field
// that tells a slow projector from a stopped one. It costs nothing — the census
// read the number anyway — and it is the answer to the thing a bare timeout
// cannot give, which is the same complaint the mechanism this appendix cites has
// open against itself.
type Visibility struct {
	Reached     bool
	At          event.Position
	Behind      event.Position
	Moved       bool
	Quarantined uint64
	Parked      bool
	Polls       int
}

// Polls until the named generation has delivered everything at or below the mark,
// on the caller's own goroutine, starting nothing.
//
// Each poll asks the park before the census and asks it every time, because the
// caller's event lives in one partition and that partition can have parked it
// while another lags: a wait that asked only after reaching would burn its
// deadline on a condition it could have named at once. A parked sequence is
// terminal — waiting longer cannot help and a redrive can. Sequences and Holds
// are the only two Park methods a wait calls; Holes is a cutover's question and
// this is not one.
//
// The first poll is immediate. A context that is already done reaches no store
// and answers ctx.Err(), because an expired context is not a poll.
//
// A POLL THAT CANNOT BE MADE IS TERMINAL ON THE FIRST POLL AND IS POLLED THROUGH
// AFTER IT, and the poll number is the whole of the rule. A cover that is not the
// one the rows are recorded at, a Checkpoints pointing elsewhere and a Park that
// refuses outside a unit fail the first poll and every poll after it, so the
// refusal is returned at once rather than after a deadline. A refusal that appears
// later is the deployment moving — a split in progress, a read of a table that
// blinked — and the deadline decides, with that last refusal wrapped into it, so
// errors.Is answers ErrTopology for a caller that asks why.
//
// A deadline answers ErrNotVisible wrapping context.DeadlineExceeded, with the
// last readable poll's Visibility. A cancellation answers ctx.Err() bare and the
// zero Visibility: the caller stopped asking, and a cancelled request stays one.
// When the two rules collide the context wins over the poll number: a poll that
// could not be made while the caller's own context was going done is that budget
// running out rather than the caller asking wrong, so the first poll takes the
// deadline exit or the cancellation exit exactly as the fourth does — and that
// refusal is not wrapped into the deadline either, because it is the budget
// itself: a store reached with a done context answers a store class, and a
// deadline carrying one is indistinguishable from a deadline reached over polls
// that really failed. A poll that
// ANSWERED still answers — a parked sequence and a generation reads do not
// resolve to are conclusions drawn from rows that were read.
//
// Before any of that, three refusals at the door and no store call: the zero
// Mark, a mark whose projection is not spec.Of's, and a barrier-minted mark
// beside a non-nil Park. All three are ErrSpec, and the second is the one a
// deployment running two projections over one log reaches by assembling values
// that are each individually right.
func Wait(ctx context.Context, spec WaitSpec) (Visibility, error)
```

Four sentinels join the eight in `event/projection/errors.go`, and its opening
comment moves from "Eight" to "Twelve":

```go
// ErrNotVisible is a deadline and never a defect: the generation had not
// delivered the mark when the caller stopped being able to wait. What kind of
// not-yet it was travels on Visibility rather than in the sentinel.
//
// ErrParked is the answer a scan checkpoint cannot give. The sequence this
// change belongs to is blocked, so the watermark passed the event and the read
// model never received it, and no amount of waiting changes that.
//
// ErrUncommitted is about the caller's own transaction and not about a store: a
// store that answers no row at the version a Commit reports is a store whose
// writer has not committed or has rolled back, and the two are indistinguishable
// from a second connection. It is receipt.Unresolved one level down and says so.
//
// ErrGeneration is the wait's own scope, refused rather than answered: the
// generation this wait names is not the one the ownership row resolves reads to,
// so reaching the mark in it would be a true statement about a read model this
// caller is not reading.
var (
	ErrNotVisible  = errors.New("projection: this change was not visible in this generation's read model within the deadline this wait was given")
	ErrParked      = errors.New("projection: the sequence this change belongs to is parked, so the scan passed it and the read model never received it")
	ErrUncommitted = errors.New("projection: this store shows no event at the version this commit reports, so the transaction that wrote it has not committed or it rolled back")
	ErrGeneration  = errors.New("projection: this wait names a generation that is not the one reads of this projection resolve to")
)
```

**One contract sentence moves, and a baseline cannot see it.**
`Park`'s doc comment says `Holds` runs inside the caller's unit of work; it gains
that `Wait` also asks it outside one and that an implementation must answer the
committed state there. Nothing else about `Park` changes: `Sequences`'s
outside-a-unit rule is unchanged, **`Holes` is not asked by a wait at all** — the
draft that put the queue's depth on `Visibility` would have moved a second
sentence on a path nothing can see, and it is dropped rather than announced — and
`Park` itself is a pass's write. The `Sequences`-first gate means a projection
that has parked nothing never reaches the widened clause. It is announced here, on
the module page and in the release note, because `check-event-kernel` watches file
digests and `make api` watches signatures, and neither can see a sentence of
contract. **One sentence, named: `Holds`.**

## 5.3 `github.com/frostgrove/vv/event/receipt` — the new package

Package `receipt`. Its first-party closure is `event`, `crud`, `errs` and
`utils` — no third-party import, so the root module is unchanged ([[D-033]]).

```go
// The caller's own identity for one operation, minted before the command and
// carried with every retry of it. The framework never derives one: a key derived
// from the payload makes two genuinely different commands one operation, and two
// identical credits are two operations that must both land.
//
// It renders "[operation key]" and never its value, because a key is a request
// identity and a refusal carries no data. Value is the one named door the value
// comes back out of, and it exists because a Ledger has to bind it as a
// parameter — jobs.LegacyIntent.Value one subsystem over, for that reason and no
// other (plan P-21).
type Key struct { ... }

func NewKey(raw string) (Key, error)
func (this Key) Zero() bool
func (this Key) String() string
func (this Key) Value() string

// SHA-256 over what Repo.Digest digested. It is safe to render — it names no
// data — and it is not a secret: over a small closed payload space a preimage is
// guessable, so it authenticates nothing.
type Fingerprint struct { ... }

// ParseFingerprint is the other half of String, and a Ledger is why: the text
// String renders is what a column holds, and the rendering stays frozen in the
// one package that owns it rather than being re-derived by every implementation
// (plan P-21). Three refusals, all ErrSpec: no prefix, text that is not
// hexadecimal, and a digest that is not thirty-two bytes.
func NewFingerprint(digest [32]byte) (Fingerprint, error)
func ParseFingerprint(raw string) (Fingerprint, error)
func (this Fingerprint) Equal(other Fingerprint) bool
func (this Fingerprint) Zero() bool
func (this Fingerprint) String() string // "sha256:" + hex, the eventpg prefix

// The durable row, and it is the application's table. This package declares the
// shape, reads it and writes none of it.
//
// First and Last are the range the operation wrote and are both zero when it
// wrote nothing, which is a decision that yielded no changes and is a completed
// operation like any other. For a range that exists, First - 1 is the version the
// operation was decided at: recorded, never compared, and the answer to "what was
// the anchor" for an operator who asks.
//
// RecordedAt is the ledger's own database clock — statement_timestamp(), not a
// parameter — so every instant this package compares comes from one clock except
// ResolveSpec.Issued, which is the caller's and cannot be anything else. It is
// zero on the value handed to Ledger.Claim and filled on the one handed back.
type Receipt struct {
	Key         Key
	Fingerprint Fingerprint
	Stream      event.Stream
	First, Last event.Version
	Complete    bool
	RecordedAt  time.Time
}

// The application's own table, in the database the events are in, behind an
// interface this framework does not implement — the shape Park and Generations
// already have.
//
// WHERE EACH METHOD RUNS IS PART OF THIS CONTRACT. Claim and Complete run INSIDE
// the caller's transaction, through the context they are given, and that is what
// makes a receipt and its events one commit. Find and Horizon run OUTSIDE one,
// on a second connection, because the whole point of resolving is that the first
// connection is gone.
//
// Claim is INSERT ... ON CONFLICT (key) DO NOTHING and then a SELECT of the same
// key, in that order, in the caller's one transaction. The order is the whole
// mechanism: the insert is what blocks on the primary-key index, and the select
// is a second statement and therefore a second snapshot, which is what lets a
// loser at READ COMMITTED read the row the winner committed while it was
// blocked. A select placed first sees nothing and makes its caller append a
// second copy; a single statement that tries to do both shares one snapshot and
// answers the loser zero rows, which is neither a win nor a repeat. won is the
// insert's affected-row count and nothing else.
//
// Nothing here chooses an isolation level. What the level decides is how a loser
// fails, not whether it may append: at READ COMMITTED it blocks and repeats, and
// at REPEATABLE READ or SERIALIZABLE it blocks and then raises SQLSTATE 40001,
// which rolls the caller's whole unit back and whose retry repeats. PostgreSQL's
// speculative insertion makes DO NOTHING wait on a conflicting uncommitted tuple
// and then insert or not on that transaction's outcome — which is why a claim
// never has to answer "unresolved" and Resolve does.
//
// Horizon is an instant at or before the oldest row this ledger still answers
// for, and it is the ledger's own database clock: MIN(recorded_at), or
// now() - retention computed in SQL for a table that may be empty, and never a
// clock read in the ledger's process. Answering an instant older than the truth
// is conformant and costs a caller one more Unresolved; answering a newer one is
// not, because it reads a row that was swept as one that never existed. A ledger
// that answers the zero instant claims to hold everything for ever, which is what
// a table with no sweep is. The sweep is the application's; nothing here prunes.
type Ledger interface {
	Transaction(ctx context.Context) (event.Authority, error)
	Claim(ctx context.Context, receipt Receipt) (held Receipt, won bool, err error)
	Complete(ctx context.Context, receipt Receipt) error
	Find(ctx context.Context, key Key) (Receipt, bool, error)
	Horizon(ctx context.Context) (time.Time, error)
}

// A claim's three answers, and there is no fourth: the index makes a claim wait
// rather than guess, so a claim never has to answer the Unresolved a Resolve does.
// The two states a caller must not proceed from carry no verdict of their own —
// Collided is returned beside ErrCollision, and a row whose claim nobody resolved
// is ErrIncomplete with no verdict at all, because a state nothing may be
// concluded from is a refusal and not an answer.
type Verdict uint8

const (
	Recorded Verdict = iota + 1 // no row existed and this claim took the key
	Repeated                    // a complete row exists and its fingerprint compares equal
	Collided                    // a row exists and its fingerprint differs
)

func (this Verdict) Valid() bool
func (this Verdict) String() string

type ClaimSpec struct {
	Ledger      Ledger
	Store       event.Store
	Key         Key
	Fingerprint Fingerprint
	Stream      event.Stream
}

// What a claim took, and the door to completing it. It carries the claim's own
// transaction and whether it has been completed, so Complete can refuse the calls
// that would undo what Claim proved — which is why it holds an unexported pointer
// and a copy of one is the same claim rather than a second.
type Held struct { ... }

func (this Held) Verdict() Verdict
func (this Held) Receipt() Receipt

// Claims the key inside the caller's transaction, before the decision is
// appended. Refused unless the ledger's transaction and the store's compare
// Same, unless the store claims transactions, and unless one is bound: a receipt
// that is not atomic with its append is a row that outlives a rollback and
// reports an operation that never happened.
//
// It answers a non-nil error for the two states a caller must not proceed from: a
// row whose fingerprint differs is ErrCollision, and a row that exists and is not
// complete is ErrIncomplete, because whether that operation's events reached the
// log is not a question its row answers. A verdict is a value it is legal to
// discard and a refusal is not, and the worst code that compiles must refuse
// rather than spend somebody else's key.
//
// The order is claim, decide, append, complete, and the claim is first on
// purpose. A receipt written only after the append is safe against two writers at
// one expected version, because exactly one of those is admitted — and not
// against two retries at different expected versions, which is what a caller does
// when it does not know the outcome. Both append, and the collision is found with
// both sets of events already in the log. Nothing in this package can see an
// append that already happened on this context, so Once is the spelling that
// makes the order unwritable and this one carries the obligation.
func Claim(ctx context.Context, spec ClaimSpec) (Held, error)

// Claim, append, complete — with the order owned by the call rather than by the
// caller. The work runs only on Recorded, runs once, and runs inside the caller's
// transaction through the context it is given; its commit is completed in that
// same transaction. A Repeated key never reaches it, so a retry costs one
// statement and no append. Opens nothing, starts nothing, and returns the Held it
// claimed, already completed.
func Once(ctx context.Context, spec ClaimSpec, work func(context.Context) (event.Commit, error)) (Held, error)

// Writes the range onto the row this claim took, in the same transaction, and it
// is not optional on any path: a claim that reaches its transaction's commit
// without one is the Incomplete row Resolve reports as a defect.
//
// Five refusals, and each of them is a way to undo what Claim proved. A verdict
// that is not Recorded — completing a repeat would overwrite the range the first
// attempt wrote with this one's. A second call on one Held — an operation that
// appended twice would otherwise record one range. A context carrying no
// transaction of the store's, or one that is not the claim's: a completion that
// lands outside the claim's transaction writes a range that survives the rollback
// of the events it names. And a commit whose Stream() is not the claim's, which is
// one claim completed with another aggregate's append.
//
// The commit's own Authority is compared with the claim's too, except on an empty
// commit: an append that wrote nothing was written through nobody's transaction
// and carries the invalid authority by design. An empty commit is how a decision
// that yielded no changes is recorded, and it completes the row with a zero range.
func (this Held) Complete(ctx context.Context, commit event.Commit) error

// What a resolve found, and it is a different vocabulary from a claim's because
// the two doors can answer different questions: a claim holds the index and never
// sees Unresolved, and a resolve writes nothing and never sees Recorded. One
// enum with seven members would let each door return the other's.
type Standing uint8

const (
	Found      Standing = iota + 1 // present and complete: here is the range
	Incomplete                     // present with no range — a claim that committed without its append
	Unresolved                     // absent, and nothing may be concluded
	Expired                        // absent, and older than this ledger answers for
)

func (this Standing) Valid() bool
func (this Standing) String() string

type Resolution struct {
	Standing Standing
	Receipt  Receipt
	Horizon  time.Time
}

// Store is what this spec asks the placement question of, and that is the whole
// of its purpose: a Resolve issued on the claiming transaction's own context
// reads its own uncommitted row and answers Found for an operation that can still
// roll back. A transaction of the store's or of the ledger's bound to ctx is
// ErrSpec.
//
// Issued is the instant the caller minted the key, in the caller's own clock, and
// it is OPTIONAL. An HTTP handler holding a retried idempotency key has the key
// and not the instant, and requiring one sends it to time.Now(), which is after
// every horizon and silently turns Expired off for ever. Absent, an absent row is
// Unresolved and Resolution.Horizon carries the ledger's instant so the caller can
// make the comparison with a date it does have. Present, the comparison runs and
// its error term is the skew between this host and the database — which decides
// between two non-conclusions and can produce neither a false Found nor a false
// "it did not happen".
type ResolveSpec struct {
	Ledger Ledger
	Store  event.Store
	Key    Key
	Issued time.Time
}

// Asks what happened to an operation, on a connection that is not the one that
// issued it. It writes nothing and reads no event.
//
// AN ABSENT ROW IS NOT A ROLLBACK. A resolver on a second connection sees no row
// for either of two reasons — the writing transaction rolled back, or it is still
// open — and PostgreSQL gives it nothing to tell them apart: there is no row to
// lock, so a share lock blocks on nothing. Unresolved is that state named rather
// than guessed at, and what bounds it is idle_in_transaction_session_timeout on
// the application role, which is the deployment's lever and not this package's.
//
// What a caller does with Unresolved: it does not re-issue the command, because a
// fresh load and a fresh decision at the new version is a different operation to
// this key and nothing would catch the second write. It reports the operation as
// in flight, and it resolves again once that timeout has elapsed.
func Resolve(ctx context.Context, spec ResolveSpec) (Resolution, error)

var (
	ErrSpec       = errors.New("receipt: this operation cannot be recorded from this spec")
	ErrCollision  = errors.New("receipt: this operation key was spent on another operation")
	ErrIncomplete = errors.New("receipt: this operation key holds a claim nobody resolved, so whether its events reached the log is not a question this row answers")
	ErrLedger     = errors.New("receipt: this ledger answered something no ledger answers")
)
```

## 5.4 `event/eventtest` — nothing, and that is the finding

Phase 4 widened the `Checkpoints` conformance contract and had to announce it.
**Phase 5 widens nothing.** `Wait` uses `Checkpoints.Load` and
`Store.ReadStream`/`Transaction`; `WaitSpec.Committed` uses `ReadStream` and
`Transaction`; `StateAt` uses `ReadStream`; `receipt` uses `Store.Transaction` and
a `Ledger` that is not a store's. All of those are inside the contract as it
stands, so the closed inventory keeps its twenty sections and their `needs` gates,
and a store certified at the end of phase 4 is certified at the end of phase 5
with no change. (`Park` is not a store's contract and is not in this inventory,
which is exactly why its one moved sentence has to be announced in prose.)

A `Ledger` is an application interface with obligations a suite could check — the
`INSERT`-then-`SELECT` claim in that order, the horizon's monotonicity, `Find`
seeing committed rows only. A `receipttest` harness is the shape that would close that, and it is
**not** in this phase; §8 records it, with the same reasoning that left the
`Park` byte bound uncertified.

## 5.5 `_examples` — two additions

- **`event-wait`** — a command that appends, builds its spec with `WaitOf`, mints
  a mark with `spec.Committed`, waits
  on a live single-partition projection and reads the read model, with the
  parked-sequence branch written out. It is the example a reader copies when they
  want a test without a `sleep`.
- **`event-receipts`** — a `Ledger` over `crud` with the `INSERT`-then-`SELECT` claim in
  full and its `statement_timestamp()`, the `UPDATE` that completes it, a
  `Horizon` computed in SQL from a configured retention, a `jobs` periodic that
  sweeps, `Once` as the shape the command uses, and the open-coded form beside it
  for the handler that answers a repeat differently — with `ErrCollision` and
  `ErrIncomplete` both branched on, and a `Resolve` path that handles `Unresolved`
  by reporting in-flight rather than retrying and works from a key with **no**
  `Issued`, which is what an HTTP retry actually holds.

`_examples/README.md` gains two rows. Both are in the unpublished module, so
neither becomes a dependency of anything ([[D-033]]).

---

# 6. What the live suite must prove

Against PostgreSQL 17.9, tagged `integration`, with
`FROSTGROVE_EVENTPG_TEST_DSN` set — an unset DSN **fails** the tagged suite
rather than skipping it. Green twice in a row; a test that passes once and fails
on rerun is a real defect. Rows are checked with
`docker compose exec -T postgres psql -U vv -d vv` and not by trusting Go.

**ES-05, in `event/eventpg`:**

1. **A confirmed command is visible without a sleep** (§UC-204) — the whole
   appendix in one test, with the not-running control.
2. **The parked pair** (§UC-206) — `Park` supplied answers `ErrParked`, `Park`
   nil answers `Reached: true` for the same parked event. This is the one test
   that would have been written wrong by a reasonable implementer, and its
   control is what makes the positive arm mean anything.
3. **Parked in one partition while another lags** (§UC-207), against a live
   four-partition cover.
4. **A mark minted inside the writing transaction is refused, and the same call
   after the commit mints it** (§UC-209) — against `eventpg` specifically,
   because that is the store where the naive version *works* and produces a mark
   nothing will ever deliver.
5. **Slow versus stopped** (§UC-212) — two waits, `Moved` telling them apart,
   beside a cancellation that returns `ctx.Err()` bare, and a third whose budget
   runs out **inside** a census read: neither arm's deadline may carry
   `ErrTopology` or `event.ErrBackend`, and the control beside it — a poll that
   really failed — must carry both.
6. **The call-count budget and its placement** (§UC-208) — a recording `Park` and
   a recording `Checkpoints`, asserting `Sequences` per poll, `Holds` never while
   the queue is empty, **`Holes` never at all**, one `Load` per member per poll,
   and no bound transaction on any `Park` call the wait made.
7. **A wait running concurrently with the projection's own loop under `-race`**,
   asserting neither fence moves and the checkpoint row's advance is the
   projection's alone.
8. **The derived spec against the three hand-written wrong ones** (§UC-242),
   against a live parking projection and a live four-member cover — the three
   wrong arms assert the wrong answers, which is what makes `WaitOf` the spelling
   the module page gives.
9. **A poll that fails on the first try and a poll that fails on the fourth**
   (§UC-244), including a cover taken mid-split against the live checkpoint table,
   so the terminal/polled-through split is proven on the two states a deployment
   actually reaches.
10. **A cutover that commits while a wait is running** (§UC-245), both directions,
    with the `Generations`-nil control that answers `Reached: true` for the read
    model the caller is no longer reading — and a third arm where the cutover
    commits **inside the first poll's census**, which is the poll a caught-up
    deployment reaches on and the one an implementation is most likely to exempt.

**ES-07, in `event/eventpg`:**

11. **Claim, append, complete in one transaction, and the rollback control**
    (§UC-214) — row counts read in `psql`.
12. **Two callers race one key** (§UC-217) — the loser blocks on the index and
    then, *per level*, either answers `Repeated` (READ COMMITTED) or fails with
    SQLSTATE `40001` whose **retry** answers `Repeated` (REPEATABLE READ,
    SERIALIZABLE); exactly one append lands at every level; plus the rollback
    variant where the loser proceeds. Run with each of the three bound by the
    caller through `crudsql.DB.WithTxOptions`, because what each level does to a
    loser is the thing §1.2 states and a level that is dropped rather than
    asserted withdraws the only evidence for it.
13. **The two-pool refusal beside the one-pool acceptance** (§UC-218), and the
    five completion refusals beside the admitted one (§UC-248), each followed by a
    `psql` read of the row.
14. **The collision table** (§UC-216) — a byte, a stream, an order — **beside the
    version variant that must NOT collide**, which is the arm that proves the
    digest's contents rather than assuming them. Live it is the table travelling
    through a real `Ledger` row into a verdict: the three that differ answer
    `Collided` with `ErrCollision`, the version variant answers `Repeated` with
    the first attempt's range, and `psql` shows one copy of the events on every
    arm. The pure half — the digests themselves — is §UC-216's untagged arm in
    `event`, and the two together are what make "the content decided it" a
    measured sentence at both levels of the design.
15. **`Unresolved` while a transaction is open, and after it rolled back**
    (§UC-220), asserting the two are indistinguishable. The open-transaction arm
    holds a second session idle-in-transaction and is the live proof of the hard
    clause. Beside it, the `Resolve` issued **inside** the writing transaction and
    refused (§UC-249), which is the failure that arm would otherwise mask.
16. **`Expired`, `Unresolved`, a zero `Issued` and the two skew arms** across a
    configured horizon (§UC-223), with the horizon taken from the table's own
    `MIN(recorded_at)` in one arm and from `now() - retention` in SQL in the other.
17. **The lost-connection retry, end to end, across a process boundary, with the
    naive control that writes twice** (§UC-227). The retry arm re-loads at the
    moved version, digests, and is asserted to produce the **same** fingerprint —
    the property the whole feature rests on — and the naive arm is what turns "the
    mechanism is valuable" from an assertion into a measurement.
18. **An unresolved claim refusing its retry** (§UC-222) beside a completed empty
    range answering `Repeated` (§UC-246), which is the pair that keeps `Incomplete`
    an honest defect report.
19. **The one-key-two-streams hole and its key-per-append control** (§UC-250), and
    the two misordered callers (§UC-251) with their `Once` control — row counts in
    `psql` on every arm, because the whole point is how many copies of the events
    exist.

**ES-08, in `event/eventpg`:**

20. **The prefix at every boundary of a real stream** (§UC-228, §UC-229) across
    `StreamPage` boundaries, with recorded page counts.
21. **Past the end, empty stream, version zero** (§UC-230, §UC-231).
22. **The unreadable-event table** (§UC-232), with the assertion that `Load` on
    the same stream gives the identical refusal.
23. **The out-of-order page** (§UC-233), which needs a deliberately broken store
    and therefore runs in `event` against `eventmemory`'s defect harness as well.
24. **`BenchmarkStateAt`** beside `BenchmarkStreamReplay`, reporting the cost of a
    bound at 1 %, 50 % and 100 % of a 100 000-event stream. It is not a gate; it
    is the number ES-09's Gate 1 would eventually be compared against, and
    recording it now costs one benchmark and saves a re-derivation.

**ES-09:** nothing runs, because nothing ships. §UC-241 is checked by
`make unit` — `TestNoSnapshotAuthorityIsDeclaredOrPromised` green, its fixture
control still reporting — and by D-145 existing.

**Everywhere:** `make unit`, `make vet`, `make fmt`, `make tidy`, `make check`
and `make api`, with the two foreign red arms named in §0 reported as foreign
rather than folded into a green claim.

---

# 7. Deliverables that are not code

## 7.1 The kernel baseline

`scripts/event_kernel.sha256` moves, and the move is recorded with
`make check-event-kernel-baseline` **in the same change as the code**. What moves
and why, written into `scripts/checks.sh`'s re-baselining comment beside phase
3's and phase 4's entries:

| Path | Why it moved |
|---|---|
| `event/repo.go` | `Digest` and `StateAt` — the fold and the encoded records are unexported and are reachable nowhere else |
| `event/errors.go` | `ErrVersion`, in the bad-request class |
| `event/token.go`, `event/eventmemory/doc.go`, `event/eventmemory/transaction.go` | three comments reworded off the word "receipt", so it has one meaning |
| `event/projection/*` | `Mark`, `Wait`, `WaitOf`, `WaitSpec`, `Visibility`, four sentinels, and one sentence of `Park`'s contract — `Holds`'s, and no second one |
| `event/receipt/*` | the new package |

`docs/api/surface.md` grows by three lines under `event`, by the §5.2 block under
`event/projection`, and by a new `event/receipt` section. `make api` is run and
the diff is a question for a person, as it always is.

**The conformance contract does not move** (§5.4), which is the opposite of phase
4 and is stated rather than left to be inferred from an unchanged file.

## 7.2 Four decisions

- **D-142 — an operation receipt is the application's table beside the append.**
  The [[D-118]] decision that file demands before a durable table is written: why
  a job cannot express a receipt (never delivered, no worker, no lease, no
  dead-letter, read by a query), why it must be in the event store's database and
  not the job store's, and why the three-way verdict is borrowed from
  `jobs.PlacementOutcome` rather than invented. Plus the claim-before-append
  order with the two-retries-at-different-versions failure it exists for, and
  `Once` as the call that owns it; the index as the serialisation point and
  therefore no isolation level chosen by this framework, beside what each level
  does to a loser ([[D-126]], §1.2); **the fingerprint's contents and the
  deliberate absence of the expected version from them**, with the worked retry
  that the presence of it made unanswerable and the statement that the anchor is
  recorded as `First - 1` rather than compared; the byte-reproducibility
  obligation and its fail-closed failure mode; the one-key-one-append rule and
  the key-per-append recipe; and `Store.Append` staying non-deduplicating. Links
  [[D-118]] [[D-122]] [[D-125]] [[D-126]] [[UC-032]].

- **D-143 — an absent receipt is not a rollback, and the third answer is the
  point.** Why `Resolve` has four standings and a claim has three; what
  `Unresolved` means in PostgreSQL terms and why no lock closes it; what a caller
  does with it; why a claim answers `ErrIncomplete` rather than a fourth verdict
  and why `Resolve` refuses inside the writing transaction; how retention bounds
  the window through a horizon rather than by letting a swept row read as a
  rollback; **which clock each instant comes from** — `statement_timestamp()` for
  the row, SQL for the horizon, the caller's own for the optional `Issued` — with
  the skew's error term and the argument that it decides only between two
  non-conclusions; and the **fourth standing that was designed and refused** — the
  `pg_current_xact_id`/`pg_snapshot_xmin` proof `eventpg`'s walk already makes —
  with its cost stated, so it is not re-proposed. Names
  `idle_in_transaction_session_timeout` as the inherited lever. Links [[D-126]]
  [[D-128]] and `EVENTSOURCE_REFERENCE.md` §Documentation obligations item 1.

- **D-144 — a bounded read is a read and never a load.** Why `StateAt` returns no
  token and why the absence of a second return is the mechanism; why version
  zero, a version past the end and an empty stream are one refusal; why it is not
  a widening of UC-032 §6, clause by clause; why there is no timestamp boundary
  and what one would have to be; and why `Store.ReadStream` does not grow a
  ceiling — with the one-page arithmetic and Reject 3 as the precedent. Links
  [[D-128]] [[D-132]] [[UC-032]].

- **D-145 — a snapshot's contract, accepted without code.** The whole of §1.4:
  the five bindings and the before-deserialisation ordering; the two components
  of the state-computation version, who declares each, and the sentence saying
  nothing detects a forgotten bump; the observable fallback and the rule that it
  is the snapshot's alone; the five constraints inherited from the reference and
  the port with the latent bug not copied; history never deleted and a snapshot
  not being history; and **the two gates, with the statement that neither is met
  and that no line may be written until both are.** It **amends** [[D-132]]
  rather than superseding it: D-132's invariant and its enforcement test stand,
  and D-145 is the contract its re-entry trigger opens into. A `See also` line is
  added to D-132 pointing at it.

## 7.3 Documentation, in the same change as the code

- **`docs/modules/en/projection.md`** and its localised twin — a wait section:
  what a caller waits on, the two minting doors and what each mark carries, the
  **five** things success does not give, the park question and why it is asked
  first and under which keys, the five exits and the first-poll/later-poll rule
  for a failing one, the generation scope and what a nil `Generations` leaves to
  the caller, the polling-cost arithmetic, `WaitOf` as the spelling a request path
  uses **with the three obligations a hand-written `WaitSpec` carries stated
  together**, and the absence of a stale-read flag as a design rather than an
  omission. Plus the one widened sentence on `Park.Holds`, written as a widening,
  and the sentence that a wait never asks `Holes`.
- **`docs/modules/en/receipt.md`** — new, and its localised twin. The whole
  package, `Once` as the spelling the order is owned by, the `INSERT`-then-`SELECT`
  claim in SQL with its `statement_timestamp()` and the sentence saying why the
  read is a statement of its own, the horizon's clock and the sweep the
  application owes, the four standings with what a caller does with each,
  `ErrIncomplete` and what an operator does about it, **the two obligations the
  framework cannot check** — a byte-reproducible decision and one key per append,
  with the key-per-append recipe for a two-aggregate operation — and one opening
  sentence giving the word its meaning here beside `event.Commit`'s.
- **`docs/modules/en/event.md`** — `Digest` and `StateAt`; the four refusals; the
  no-token rule; **what the digest covers and what it deliberately leaves out**,
  with the byte-reproducibility obligation stated beside it in the shape
  `Sequencer`'s three are; the sentence that a timestamp is neither business time
  nor commit order; and the note that the over-read is bounded by one page.
- **`docs/modules/en/eventpg.md`** — nothing new is owed by this phase, and the
  global `xmin` stall section phase 3 wrote gains one cross-reference, because it
  is the condition under which an ES-05 wait freezes and an ES-07 receipt stays
  unresolved.
- **`docs/ai/usecases/modules/event/`** — three new use cases (the wait, the
  receipt, the historical read) and one `See also` line added to [[UC-032]]. **No
  clause of UC-032's `What must hold` changes and its `Out of scope` gains
  nothing**, because nothing that was out of scope came in.
- **The flows** — [[FL-038]] gains the wait, because it is the checkpoint flow
  and a wait is a read of those rows; [[FL-036]] gains `StateAt` and `Digest`,
  because its file table already names `event/repo.go` down to `replay`,
  `checkPage` and `records`; and the receipt's two doors get a flow of their own,
  since neither is on the decision-to-fact path. The reverse index in
  `docs/ai/flows/Index.md` gains every file this phase touches, including the
  three whose only change is a comment.
- **`docs/roadmaps/Roadmap.md`** — ES-05, ES-07 and ES-08 are removed from the
  open list rather than annotated done. **ES-09's row stays open** and its text
  changes from "conditional snapshot mechanics" to "the contract is D-145; the
  two gates are unmet", so the open item carries its own definition of done.
- **`_examples/README.md`** — two rows.

---

# 8. Tensions left to the plan

Recorded rather than resolved, because each is a real choice the plan must make
and none of them changes a use case above.

1. **`Wait`'s polling cost is linear in waiting callers × cover size.** Fifty
   concurrent waiters on a four-partition cover at the default 50 ms is a
   thousand checkpoint reads a second, and they are `SELECT`s on one small table
   so they are cheap — until they are not. A shared poller with fan-out is the
   obvious answer and it is a `runtime.Runner`, which the host would have to
   supervise and which `Wait`'s "costs nothing when nobody is waiting" property
   would lose. The plan may decide the default `Every` is higher, or that the
   module page carries the arithmetic and nothing else. Reject 1's shape applies:
   a background thing is a supervised runner or it does not exist.

2. **`Visibility.Moved` is a heuristic dressed as a field.** It is exactly true —
   the watermark did or did not change across these polls — and it is read as
   "the projector is running", which it is not: a projector between two slow
   passes has not moved either. The module page must say what it is, and the plan
   should decide whether one poll is enough to set it or whether it needs two
   observations at least `Idle` apart.

3. **A `Ledger`'s obligations are uncertified.** The claim's two statements and
   their order, the horizon's monotonicity and `Find` seeing only committed rows
   are contract and
   are proven only in `_examples`. A `receipttest` harness in the shape of
   `eventtest` is what would close it, and it is not in this phase — the same
   call, with the same reasoning, that left the park's byte bound uncertified in
   phase 4. The plan should decide whether the example ledger is the reference
   implementation or an illustration, and say which on the module page.

4. **Receipt retention has no default and no guidance.** The horizon makes an
   expired receipt honest; it does not say how long a deployment should keep one.
   The answer is a function of how long a caller may retry, which is the
   application's, and the plan should decide whether the module page gives a
   worked number (a day, matching a typical idempotency-key window) or refuses to
   guess.

5. **`Repo.Digest` exposes a hash of the payload bytes to a caller that could not
   otherwise read them.** It is a digest and not the bytes, and the payload is
   the caller's own — it just handed them in as `Change` values — so nothing
   leaks. But it is the first method on `Repo` that answers a question about an
   append without making one, and the plan should check that no policing
   decorator in a consumer's path is bypassed by it.

6. **ES-08 and ES-09 are one design and only one of them ships.** The half-open
   `version > :from AND version <= :to` clause is the same clause, and phase 5
   implements only the `<= :to` half. The phase that ships ES-09 must get the
   `> :from` half right against a boundary this phase never exercises, which is
   the shape that risks getting the asymmetry wrong once rather than twice. The
   plan should record that `StateAt`'s loop is where the base-state parameter
   would go, so the later phase extends rather than writes a second loop.

7. **`BenchmarkStateAt` measures a cost nobody has a budget for.** It is recorded
   because ES-09's Gate 1 will want it, and D-132 is explicit that a measured
   cost is not a reason. The plan should put the numbers in
   `EVENTSOURCE_BACKLOG.md` under `## P5` rather than in a decision, so the
   eventual gate has a number and the deferral does not acquire an argument it
   was refused.

8. **How often a wait should re-read the ownership row.** The generation question
   is **answered** rather than left here — `WaitSpec.Generations` is optional,
   `Wait` reads `Active` on its first poll and on the poll that would reach, and a
   mismatch is `ErrGeneration` (§1.1, §UC-245) — but *twice* is a judgement about
   where the answer changes anything, not a law. The plan may decide that a wait
   long enough to span several cutovers should read it per poll, at one more small
   `SELECT` per interval. What it may not do is drop the last read: that one is
   what turns a false `Reached` into a refusal.

9. **`Once` and `Claim` are two doors onto one mechanism.** `Once` owns the order
   and `Claim` leaves it to the caller, and the module page says which to reach
   for. Two doors is two things to keep true of each other, and the plan should
   confirm that every refusal `Claim` has is reachable through `Once` and that the
   example uses `Once` — the open-coded form exists for the caller that must
   branch, not as the shape a reader copies first.
