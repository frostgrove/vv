# D-145 — A snapshot's contract, accepted without code

**Status:** accepted — **in force from the phase that meets [[D-132]]'s gate**
**Amends [[D-132]], and does not supersede it.** D-132's invariant stands
verbatim, its enforcement test stays green and un-narrowed, and this file is the
contract its re-entry trigger opens into.
**Invariant:** A snapshot is bound to **five** things — backing, stream, version,
payload revision and a state-computation version — **all five compared before a
byte of its payload is deserialised**. Only a confirmed version loads and the
whole tail is then replayed. Incompatibility or corruption of the snapshot means
a full replay, **observable in the return value**; an unreadable **event** is a
refusal and is never hidden by that fallback. History is never deleted. **A
snapshot that ships without both gates below is out of scope.**

## Why this is a contract and not code

[[D-132]] forbids a snapshot outright, everywhere one could live, and its gate is
*"a measured **need**, not a measured **cost**. **There is no consumer.**"*
ES-09's own «зачем» is *«ускорить длинные streams»*, which is the cost argument
D-132 refuses by name — so superseding it has no argument to stand on.

The gate was **asked before this file was written**, because deferring on an
unasked gate is not a deferral, it is an omission.

**Measured 2026-09-12**, PostgreSQL 17.9 at `localhost:55432`, Intel i9-10900K,
with `BenchmarkStreamReplay` — the instrument [[D-132]] names — over a 120-byte
payload, a no-op codec and a no-op fold, three runs each:

| Stream | `-benchtime` | ns/op, run by run | Full replay | ns/event |
|---|---|---|---|---|
| **10 000 events** | `20x`, `-count 3` | 14 881 024 · 13 050 254 · 12 914 297 | **12.91 – 14.88 ms** | 1 291 – 1 488 |
| **100 000 events** | `10x`, `-count 3` | 110 687 007 · 111 301 799 · 108 429 160 | **108.4 – 111.3 ms** | 1 084 – 1 113 |

Three readings, and the third is the one that decides.

1. **The instrument is stable, and where it is not the difference is stated
   rather than rounded away.** [[D-132]] recorded 10.19–10.93 ms at 10 000 with a
   no-op codec and 16.25–17.72 ms with `event.JSON`, and 104.4–170.6 ms at
   100 000. Today's 100 000-event figure sits inside that band. Today's
   10 000-event figure, on the same no-op codec, is 20–45 % above D-132's no-op
   number and below its `event.JSON` one — a differently loaded machine on a
   different day and not a regression. The per-event cost at 10 000
   (1.29–1.49 µs) against 100 000 (1.08–1.11 µs) is the per-call fixed cost
   amortising, and the two agree within 30 % across a factor of ten in stream
   length. That agreement is D-132's own control that the benchmark measures a
   replay rather than a constant, and it holds.
2. **The threshold lands where [[D-132]] said it does.** Against the re-entry
   trigger of a p99 `Repo.Load` above ~50 ms, a 10 000-event aggregate is 3.4–3.9×
   **under** it and a 100 000-event one is 2.2× **over**. At 1.08–1.11 µs an event
   the crossing is **~45 000 events**, inside D-132's recorded *"somewhere above
   30 000 – 50 000 events"*. **The trigger does not move**, and the slightly
   slower 10 000-event figure does not move it either: the crossing is set by the
   per-event cost at length, which is the 100 000-event number.
3. **And the gate does not open.** Everything above is this repository's
   benchmark over a loopback socket, on a synthetic stream, with a no-op fold.
   Gate 1 asks for **a named deployment's own p99, in its own environment, with
   its own payloads**, and no such deployment exists.

So this phase takes the route the appendix literally asks for — *«Оптимизированный
load требует отдельного принятого контракта, а не незаметного изменения гарантий
[[UC-032]]»* — and ships the contract. The cost of deferring is bounded and is
stated: an aggregate must exceed roughly **45 000 events** before a snapshot pays
anything at all, and `Repo.StateAt` ([[D-144]]) already gives a caller the
bounded prefix that removes the most common reason to want one.
`BenchmarkStateAt`'s numbers are recorded in `EVENTSOURCE_BACKLOG.md` `## P5`
rather than here, so Gate 1 has both halves of the arithmetic and this file does
not slowly acquire the cost argument D-132 refused it.

**The surface consequence: ES-09 exports nothing.** `docs/api/surface.md` gains
no line, no identifier matching `(?i)snapshot` is declared anywhere under
`event/`, and `TestNoSnapshotAuthorityIsDeclaredOrPromised` is untouched — this
file lives in `docs/ai/decisions/`, which that test does not read.

## The contract

### What a snapshot is bound to — five things, compared before deserialisation

**The ordering is the mechanism, and the source proves it.** Axon's
`AbstractEventStorageEngine.readSnapshot` reads

```java
return readSnapshotData(aggregateIdentifier)
        .filter(snapshotFilter::allow)
        .map(snapshot -> upcastAndDeserializeDomainEvents(...))
```

— `.filter(…)` **precedes** `.map(deserialise)`. The compatibility decision is
made on the stored row's metadata, before a single byte of the payload is read
back, because a filter that had to deserialise first would fail on exactly the
snapshots it exists to exclude. **Any design that stores the version inside the
snapshot payload has the mechanism inverted.** That is the first clause of this
contract.

The five, all columns beside the bytes:

1. **The backing.** `event.Backing`, compared with `Backing.Equal`. A snapshot
   restored from another database's dump is refused. Neither Axon, nor the
   reference, nor its Kotlin port has this; vv has the value already.
2. **The stream.** Family and key, composed ([[D-125]]).
3. **The version.** The exact version the fold had consumed. The tail then begins
   at `version + 1`.
4. **The snapshot payload's own revision.** A snapshot has a codec and a wire
   shape like a `Fact` does and carries the revision it was written at. But **a
   snapshot is never upcast**: a revision that is not the current one is refused
   and the load falls back. This is a deliberate divergence from how `event`
   treats facts, and the asymmetry is what makes a snapshot safe at all — a fact
   is irreplaceable, so carrying it forward through a declared chain is worth the
   risk; a snapshot is regenerable from the log, so an upcaster bug on one serves
   a wrong state silently where the same bug on an event is caught by every
   replay.
5. **The state-computation version.** Below.

### The state-computation version: two components, and who declares each

Axon's `@Revision` is a **serialised-form** version, not a state-computation one.
It is bumped by a human when the aggregate's serialised shape changes, and it is
**not** bumped when a fold body changes while the fields stay the same. A build
whose fold has changed therefore loads its predecessor's snapshots and serves a
state no current code would compute, with no error anywhere. [[D-132]] already
names this as *"the one this framework structurally cannot detect"*.

**So ES-09's «версия вычисления состояния, независимая от payload revision» is a
requirement the cited source does not meet and cannot be copied from.** What is
available is the *shape* — a constant compared before deserialisation — with a
different meaning and a different discipline. This contract binds a snapshot to
**two** components, and the split is the design:

**The automatic half — the declared-shape digest.** SHA-256, length-prefixed,
over the family, the state type's name, and the sorted list of `(fact wire name,
number of retained revisions)` the declaration holds. It is computed by the
framework from the `Aggregate` value, costs nothing, and catches the class of
change an author most often forgets: **a fact added, a fact removed, a revision
appended to a chain.** Every one of those changes what a replay computes and none
of them changes the state struct.

**The human half — the author's own integer**, declared on the aggregate beside
the family. It is bumped when a **fold body** changes, when an upcaster's
behaviour changes, or when the state struct's meaning changes without its shape
changing. It exists because the automatic half can see none of those.

**Who declares it: the application author.** The framework cannot derive it, and
three derivations were considered and all three refused — recorded so the
implementing phase does not re-derive them:

- **Hashing compiled function pointers** — not stable across builds, so a no-op
  rebuild would invalidate every snapshot in the deployment.
- **Hashing the source** — not available at run time.
- **Hashing the closure's behaviour** — not a thing.

**What happens when somebody forgets to bump it: nothing detects it.** A changed
fold body, with no new fact and no new revision, produces a binding that compares
equal. The snapshot loads. The state served is one no current code would compute,
and there is no error, no counter and no signal. That is the honest answer, and
it is written in those words rather than implied away.

**What the suite proves instead**, since it cannot prove that:

1. **The equivalence proof** (Gate 2), which makes the mechanism's arithmetic
   provable even though its bumping discipline is not.
2. **A drift fixture**: a snapshot written under computation version 1 and read
   under a declaration at version 2 falls back to full replay and produces the
   **correct** state, not the snapshot's. **Control:** at version 1 it loads from
   the snapshot, proven by a counting store reporting how many envelopes were
   read — without that control the test passes whether or not the snapshot was
   ever consulted.
3. **A control that pins the gap.** A test that changes a fold body *without*
   bumping the version and asserts the framework serves the **stale** state. It is
   `test/integration/gate_relscope_test.go`'s pattern inverted: a control whose
   job is to assert that the hole is there, so that the day somebody closes it the
   control fails and says so. A documented hole with a failing-on-repair test is a
   different thing from an undocumented one.

### The fallback is observable, and an unreadable event is not hidden by it

Axon's fallback is silent: one log line at the framework's own logger and nothing
in the return value distinguishing *"loaded from a snapshot"* from *"the snapshot
was unreadable and I replayed everything"*. A deployment whose codec quietly
changed can have **every** snapshot failing, paying a full replay on every load,
with the performance the snapshot was added for gone and no signal but log
volume.

**vv cannot even log** — [[D-132]] forbids `event/projection` a line and the same
argument applies to a kernel that publishes values instead. So the fallback must
be **in the return value**: an optimised load answers how the state was obtained —
from a snapshot, or from a full replay — and a deployment measuring 100 % full
replay learns that its snapshot mechanism is not working. That is the failure
mode neither source can see.

**And the sharp clause:** *«unreadable event не скрывается fallback»*. The
fallback is entered on the **snapshot's own** failure and on no other. An
unreadable *event* in the tail — `ErrUnknownType`, `ErrRevision`, `ErrUpcast`,
`ErrPayload` — is a refusal and must be returned.

**The discriminator is which call failed, not which error class came back**, and
the failure mode if it is got wrong is subtle rather than loud: the load falls
back, replays from version 1, hits the same unreadable event, and returns a
refusal with the snapshot's involvement erased — so an operator debugs a replay
problem that is really a tail problem, and the snapshot that was working
perfectly is the thing they suspect.

Axon's `catch (Exception | LinkageError e)` has a transferable half even though
Go has no class-loading failure: **the set of ways a stored snapshot can fail to
become a value is larger than the set of ways the code that wrote it expects.** A
snapshot decode that panics falls back; a snapshot decode that returns a plausible
zero value does not and cannot, which is why the codec round-trip check
`event.Fact.RoundTrip` already provides is owed for a snapshot's codec too.

### The write, the cadence and the lookup — five constraints inherited

Recorded because they were extracted from two shipped implementations and the
implementing phase must implement rather than rediscover them.

1. **The snapshot is written INSIDE the append transaction.** A snapshot
   committed beside the append leaves, after a rollback, a snapshot at version 10
   for a stream whose head is version 9 — and every later load reads it, reads
   `WHERE version > 10`, gets nothing, and returns state derived from events that
   never committed. *Permanently wrong, never repaired, because the snapshot is
   preferred over the log.* This is the one part of ES-09 that fits the existing
   architecture perfectly: it is [[D-118]]'s rule, and `eventpg`'s
   single-statement CAS already holds the stream row, so no two writers race a
   snapshot for one aggregate without anyone choosing an isolation level
   ([[D-126]]).
2. **The cadence is `finalVersion % N == 0` with `N >= 2`, checked twice** — at
   bind time and at write time. `N == 1` turns the event store into a state store
   with an audit log attached; `N == 0` is a divide-by-zero on the write path. And
   the consequence nobody states: **a multi-event command jumps the boundary, so
   worst-case replay length is not bounded by `N`.**
3. **A load at a version takes the newest snapshot AT OR BELOW it** — `AND
   (:version IS NULL OR s.version <= :version) ORDER BY s.version DESC LIMIT 1` —
   and the forward read is `version > :from AND version <= :to`. The
   half-open/half-closed asymmetry is deliberate: the snapshot already includes
   its own version. Drop the `<= :version` clause, as the Kotlin port did, and a
   read at version 12 of an aggregate snapshotted at 30 returns *future state
   labelled as version 12*. **This is the same clause [[D-144]] needed**, which is
   why the two are one design and why the bounded read shipping first, without a
   base-state parameter, is the sequencing that keeps it clear of [[D-132]].
   [[D-144]]'s two warnings about the loop's seed — the accumulated version and
   `checkPage`'s `after` argument — are the other half of this row.
4. **Do not read the reference's snapshot code for the shape.**
   `AggregateStore.java:53-58` calls the snapshot `INSERT` inside the per-event
   append loop while testing the aggregate's *final* version, against a bare
   `INSERT` with no `ON CONFLICT` — so a two-event command on a boundary violates
   the primary key and aborts the command transaction. Invisible only because
   every sample command emits exactly one event.
5. **Neither implementation prunes.** One full-state row per `N` events, for
   ever, and for a large aggregate the snapshot table can exceed the event log.

### History is never deleted; a snapshot is not history

*«Историю не удаляем»* is about **events**, and Axon's storage note is the thing
this contract refuses outright: *"Snapshot events are stored automatically in the
event store, replacing prior events during normal operations."* That is the
snapshot becoming the authority, which is precisely the second answer [[D-132]]
and UC-032 §6 exist to forbid.

A snapshot **is not history**, so replacing one is legitimate, and the port's
argument for it is right: the write is `ON CONFLICT (stream, version) DO UPDATE
SET …` and not `DO NOTHING`, so a re-snapshot genuinely replaces a drifted one,
and a drifted row does not make every later load re-detect it and pay a full
replay for ever. Axon leaves the row and pays that cost; the two shipped
implementations disagree and this contract takes the port's side with the reason
attached.

### The two gates, stated so a later phase can meet them

**Gate 1 — measured benefit.** A **named deployment's own p99** `Repo.Load` above
~50 ms, measured with `BenchmarkStreamReplay` in that deployment's environment,
recorded in this file with the number and the date. Not this repository's
benchmark, not a synthetic stream, and not a cost figure. [[D-132]]'s trigger is
unchanged and **is** the gate.

**Gate 2 — equivalence.** For a stream of N events and **every** cadence
boundary, `snapshot + tail` folds to a state deeply equal to `full replay`, over a
corpus that includes at least: a multi-event command that jumps a boundary; a fact
with a retained revision needing an upcast in the tail; and an aggregate whose
fold is order-dependent, so a tail applied in the wrong order fails rather than
passing by symmetry. Green **twice in a row**, under `-race`, against the live
database. Falsified by moving the half-open boundary by one in either direction —
apply the base event twice, or drop the ceiling event — and watching it fail.

**Neither gate is met at the time this file is written**, and neither shipped
implementation has Gate 2: both assert the property and test neither.

## Open questions this contract deliberately leaves open

Named rather than answered, because a contract that invents an answer nobody
needed yet is worse than one that says where its edge is.

- **Composition of several compatibility policies.** Axon's javadoc rule 3 is a
  composition safety rule and getting it backwards disables every other
  aggregate's snapshots: *"return `true` if the snapshot data **does not**
  correspond to the desired aggregate"*, because `combine` is AND across every
  registered filter, so a filter that returns `false` for anything it does not
  recognise silently turns snapshotting off for the whole application. This
  contract specifies **five column comparisons and no composition rule** — one
  policy per aggregate, assembled by the framework — and a design that lets an
  application contribute its own predicate inherits that hazard and owes its own
  sentence.
- **Whether an operator may discard a snapshot, and what that means.** ES-09
  part 5 says *«оператор может отбросить snapshot»*. Deleting a row is safe by
  construction here — the log is the authority — but the property is not stated in
  a form a test could take, and the invariant belongs to the phase that writes the
  code.
- **What "a snapshot decode that panics falls back" rests on.** A `recover` on a
  decode path is not something any decision in this tree authorises today, and the
  clause above stands unargued rather than settled.

## What it forbids

Everything [[D-132]] forbids, unchanged, **until both gates are met**. After
that:

- Do not deserialise a snapshot payload before all five bindings have been
  compared, and do not store any of the five inside that payload.
- Do not upcast a snapshot.
- Do not make the fallback silent: a load that fell back must say so in its
  return value.
- Do not enter the fallback on an unreadable **event**, and do not discriminate
  by error class instead of by which call failed.
- Do not write the snapshot outside the append's transaction, do not allow
  `N < 2`, and do not drop the `<= :version` clause from the lookup.
- Do not let a snapshot replace, shadow or stand in for a recorded fact.
- Do not ship without Gate 1 and Gate 2.

## Where it lives

Nowhere yet, and that is the point. When it does:

- `event/repo.go` — the optimised load, seeded from `replay`'s existing lower
  bound ([[D-144]]).
- `event/aggregate.go` — the declared-shape digest and the author's integer.
- `event/eventpg/schema.go` — the table, on a `SchemaVersion` step and a
  deployment-profile choice ([[D-101]], [[D-127]]).
- `docs/api/surface.md` — the first line under `event` that names one.

## Proven by (owed)

Falsified, for as long as no code exists, by this file omitting any of: the five
bindings, the before-deserialisation ordering, the two components of the
state-computation version, the sentence that nothing detects a forgotten bump, the
observable fallback, the unreadable-event rule, the five inherited constraints, or
either gate.

Owed by the implementing phase:

- the equivalence suite of Gate 2, green twice in a row under `-race`, with the
  boundary falsified by one in each direction;
- the drift fixture and its counting-store control;
- the inverted control that asserts the undetectable-bump hole is still there;
- a fallback-provenance assertion over a corpus of deliberately incompatible
  snapshots, including one whose payload decode panics.

Standing, and green today:

- `TestNoSnapshotAuthorityIsDeclaredOrPromised` — un-narrowed, with its fixture
  control still reporting.

## See also

[[D-101]] [[D-118]] [[D-125]] [[D-126]] [[D-127]] [[D-132]] [[D-144]]
[[FL-036]] [[UC-032]]
