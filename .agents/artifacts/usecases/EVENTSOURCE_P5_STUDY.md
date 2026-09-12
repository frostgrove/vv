# EVENTSOURCE P5 — the sources behind ES-05, ES-07, ES-08 and ES-09

**Purpose.** Read the four remaining appendices in the original Russian, fetch and read every
mechanism they point at *at the other end of the pointer*, and record what an implementer needs
that the appendix's one-line summary does not carry. Then reconcile against what phases 1–4
actually shipped, so phase 5 does not rebuild what exists.

**This is a study. It specifies nothing, decides nothing, and writes no code.**

**Appendices read:** `docs/roadmaps/2026-09-01-postgres-event-sourcing-roadmap.md`, section
`## Дополнительные приложения — 2026-09-08` (line 762). ES-05 at line 783, ES-07 at 793, ES-08 at
803, ES-09 at 813. Read in Russian; part 3 (адаптация) of each re-read several times, because that
is where the constraints are.

**Baseline reconciled against:** `event/`, `event/eventmemory/`, `event/eventpg/`,
`event/projection/`, `event/eventtest/` at `1fd671b`, after phase 4. The appendices were written
against `c938866` — before `event/projection` existed at all — so their «уже есть» sections are
stale in the places §0 corrects.

---

## Sources actually read, and what each would and would not yield

| Appendix | URL the appendix cites | Outcome |
|---|---|---|
| ES-05 | `https://martendb.io/events/projections/async-daemon.html#querying-for-non-stale-data` | **Read**, rendered page and the doc's own markdown source `JasperFx/marten/docs/events/projections/async-daemon.md`. Both are short: they give the API and the timeout modes and say nothing about what happens when the daemon is stopped. Recovered from three other places, named below. |
| ES-07 | `https://docs.kurrent.io/clients/python/v1.3/appending-events` | **Read.** It states the idempotence rule in two sentences and **does not state what happens when the same event ids arrive with different content** — the exact question ES-07's part 3 turns on. Recovered in full from the TCP client reference, which carries the three-way comparison table, plus the maintainers' own forum thread. |
| ES-08 | `https://martendb.io/events/projections/live-aggregates#time-travelling` | **Read**, rendered page and markdown source. Both show the two call shapes and **neither states the refusal behaviour**. Recovered from the Marten source, `src/Marten/Events/QueryEventStore.cs`, which is decisive and is quoted below. |
| ES-09 | `https://docs.axoniq.io/axon-framework-reference/4.10/tuning/event-snapshots/` | **Read** (the 4.10 path redirects into the current reference and serves; the 4.11 path 404s — noted, not substituted). The page names `SnapshotFilter` and `@Revision` and does not show either implementation. Recovered from the Axon source at `axon-4.11.x`. |

**Read from the implementations rather than the docs**, because the docs pages omit exactly the
parts the appendices' part 3 is about:

- `JasperFx/marten@master`, `src/Marten/Events/QueryEventStore.cs` — both `AggregateStreamAsync`
  overloads, quoted verbatim in §ES-08.
- `JasperFx/marten@master`, `src/EventSourcingTests/Aggregation/querying_with_non_stale_data.cs`
  — the test surface for `QueryForNonStaleData` / `WaitForNonStaleProjectionDataAsync`.
- `JasperFx/marten` discussion **#4953**, *"Async daemon can advance projection progress past
  committed events during concurrent appends"* — the race, and the maintainer's fix. This is the
  most important thing read for ES-05 and it is not in any documentation page.
- `JasperFx/marten` issue **#3912**, *"Automated Testing Support Improvements"* — why the wait
  times out and what it hides.
- `AxonFramework/AxonFramework@axon-4.11.x`,
  `eventsourcing/src/main/java/org/axonframework/eventsourcing/snapshotting/SnapshotFilter.java`
  and `RevisionSnapshotFilter.java` — quoted verbatim in §ES-09.
- `AxonFramework/AxonFramework@axon-4.11.x`,
  `eventsourcing/src/main/java/org/axonframework/eventsourcing/eventstore/AbstractEventStore.java`
  (`readEvents`) and `AbstractEventStorageEngine.java` (`readSnapshot`) — the fallback and where
  it is *not*.
- `docs.kurrent.io/clients/tcp/dotnet/21.2/appending` — the three-way expected-version/EventId
  comparison, which the Python page the appendix cites does not carry.
- `discuss.kurrent.io/t/…/1337` — Greg Young on why there is no check inside a batch.

**Nothing below is written from memory of a page that would not load.** The one 404 encountered
(`axon-framework-reference/4.11/tuning/event-snapshots/`) was replaced by the 4.10 path the
appendix itself cites plus the framework source, both named.

---

# §0 — What ES-05/07/08/09 already have, with file and line

Phase 4's ES-01 reconciliation found that appendix ~90 % stale. **These four are not.** Their
«уже есть» sections are broadly accurate; what changed underneath them is narrower and more
specific, and it is all in this section. Phase 5 rebuilds none of it.

## ES-05 — what exists

| Appendix claim (part 4) | Status at `1fd671b` |
|---|---|
| «`Commit.Stream/Last`» exists | **True and unchanged.** `event/token.go:29-47`. `git log -- event/token.go` shows two commits, both phase 1: the file is byte-identical to `c938866`. |
| «receipt внутри caller transaction ещё не доказывает её commit» | **Still true, and phase 4's authority work did not change it.** `Commit.Authority()` (`event/token.go:47`) names the transaction the append rode in; `Authority.Same` (`event/authority.go:36`) compares two such names. Phase 4 used exactly this to prove the *alignment* of two writes — `Projection.checkUnit` at `event/projection/pass.go:932-957` mints `event.NewAuthority(this.tracker.Backing(), crud.KeyOf(executor))` and refuses unless `authority.Same(mine)`. That proves **one transaction**, never **one commit**. Nothing in the tree observes a commit, and [[D-118]]/[[D-126]] are why: the framework opens, commits and rolls back nothing. |
| «Нет applied progress и ожидания проекции» | **Half wrong now.** Applied progress *does* exist: `Progress.Applied` (`event/checkpoint.go:27`), `Progress.Quarantined` (`:32`) and `Progress.Highest` (`:25`) ship, and `Progress` is durable per projection name through `event.Checkpoints`. What does not exist is the **wait**. `grep -rn "func .*Wait" event/` outside `event/eventtest` returns nothing. |

**What ES-05 gets for free that the appendix could not know about, because it is phase 3/4 work:**

- **[[D-128]] makes `Progress.Highest` a completeness watermark and a kernel law**, not a store
  capability. The ADR's invariant, verbatim: *"`Progress.Highest` is therefore a completeness
  watermark: once a checkpoint carrying `Highest = P` is saved, every event the log will ever hold
  at a position at or below `P` has already been delivered, and a read resumed from that
  checkpoint's cursor answers only positions above `P`."* `docs/ai/decisions/D-128-…md:7-11`. This
  is the whole substrate ES-05 needs, and §ES-05 below shows it is strictly stronger than what
  Marten's wait is built on.
- **A barrier value already exists**, as a generation-scoped value: `projection.Barrier`
  (`event/projection/generation.go:84-89`) with `Observe` (`:114`) folding *the lowest `Highest`
  across a cover*, and `Readiness{Reached, Behind, Quarantined, Holes}` (`:225-231`) answered by
  `Reached` (`:239`). ES-05's «store-issued barrier конкретного projection generation» is this
  value. What it is **not** is per-caller: `Reached` refuses a barrier observed from the same
  generation it is asked about (`:262-266`) and refuses the zero `Barrier` by construction, because
  a barrier a caller can invent is not evidence.
- **The "applied vs merely parked" distinction is already durable and already queryable.**
  `Progress.Quarantined` is the cumulative durable count; `Park.Holes(ctx, of Identity)`
  (`event/projection/park.go:78`) is what the queue holds now; and — the precise one — **`Park.Holds(ctx, of Identity, sequence string)`** (`:76`) answers whether **one named sequence** is parked. With the default `ByStream()` sequencer (`event/projection/sequence.go:33-37`) that sequence key is `event.Compose(family, key)`, which a caller can compute from `Commit.Stream()` without any new API. That is the "degraded" answer ES-05 part 3 asks for, for *this* caller's stream rather than as a global count.
- **`Position` is reachable after commit at the cost of one read.** `eventpg`'s `streamStatement`
  selects `position` (`event/eventpg/read.go:304-310`) and `storedEvent.envelope()` sets it
  (`:33-43`), so `Store.ReadStream(ctx, stream, V-1)` yields the `Position` of version `V`.
  `Commit` carries no `Position` and `Append` returns only an error (`event/store.go:211`), by
  design — see §ES-05 nuance 6.

**What remains for ES-05:** the wait itself, its deadline vocabulary (timeout vs degraded vs
explicit stale), and the mapping from a committed `(Stream, Version)` to the number a projection's
`Progress` is compared against.

## ES-07 — what exists

| Appendix claim (part 4) | Status |
|---|---|
| «`ErrUncertain`» | `event/errors.go:55`, in the *write* class beside `ErrConflict` (`:54`). Unchanged. |
| «возвращаемый `Commit`» | `event/token.go:29-47`. Unchanged. |
| «классификация SQL append outcomes без скрытого retry» | `event/eventpg/classify.go:27-39`. Unchanged, and **more decisive than the appendix credits** — see §ES-07 nuance 5: on the joined path a failure is `NotWritten`, not `Unconfirmed`. |
| «`Store.Append` намеренно не дедуплицирует» | `event/store.go:199-211`: *"It never retries, never chunks and never deduplicates."* Unchanged. |
| «Нет durable operation identity и lookup результата» | **True.** `grep -rn "receipt\|idempot\|operation key" event/ --include=*.go` outside tests returns only prose. |
| «Квитанция — отдельный SQL-профиль, не произвольные metadata в event envelope» | Consistent with the tree: `Envelope` (`event/store.go:87-111`) has no metadata map and `Record` (`:51-61`) has three fields. Phase 4 added none. |

**What ES-07 gets for free:**

- **The reference already adjudicated the [[D-118]] question and the answer is yes.**
  `EVENTSOURCE_REFERENCE.md` W11 and `EVENTSOURCE_BACKLOG.md` `## From the reference` §5:
  *"**Not blocked by [[D-118]]** — a dedup claim is not a durable *intent*."* Phase 5 does not
  re-litigate this; it does owe the decision D-118 demands (*"If a job cannot express it, say why
  in a decision before writing the table"*, `D-118…md:99-101`).
- **vv already ships the exact three-way vocabulary one subsystem over.**
  `jobs.EnqueueOnceOutcome` = `EnqueueCreated` · `EnqueueExistingSamePayload` · `EnqueueConflict`
  (`jobs/queue.go:260-266`), decided by a payload-digest comparison against the existing row —
  `jobs/jobspg/driver.go:123-126`: `outcome := jobs.PlacementConflict; if samePayloadDigest(existing, placement.PayloadDigest()) { outcome = jobs.PlacementExistingSamePayload }`. `jobs.ProducerIntent` (`jobs/identity.go:102-113`) is the key type, with its own validity rules and a `String()` of `"[job intent]"` so the key never renders. ES-07's "operation identity + fingerprint → created / same / conflict" is **this shape, already reviewed and already shipped**. Diverging from it needs a reason.
- **A fourth table at a new schema version is a solved deployment question.** `eventpg` is at
  `SchemaVersion = 2` (`event/eventpg/schema.go:16`) and `MIGRATIONS.md` records the precedent
  verbatim: *"version 2 adds a table and alters none."* [[D-101]]/[[D-127]] own the profile choice.

**What remains for ES-07:** the receipt record itself, its fingerprint rule, its resolve door, its
retention bound, and the third answer (`unresolved`) that the appendix's «Отсутствующая квитанция
не доказывает rollback» demands.

## ES-08 — what exists

| Appendix claim (part 4) | Status |
|---|---|
| «постраничное чтение stream» | `Store.ReadStream(ctx, Stream, after Version)` (`event/store.go:197`), capped at `Limits().StreamPage`, first version `after+1`, each subsequent exactly one higher. |
| «retained revision chain» | `event/chain.go:10-14` (`Chain`), `:32` (`From`), `:42` (`Then`). `Fact.Read(Envelope) (E, error)` is exported (`docs/api/surface.md`, `event` section). |
| «полный replay» | `Repo.replay` (`event/repo.go:198-219`) — pages to the end, stops on a short page, issues no confirming read. `Repo.Load` (`:28-43`) is the only caller. |
| «Нет top-level ограниченного replay» | **True and precise.** `replay` has no version ceiling and no caller can supply one. |
| «[[UC-032]] пока требует полный `Load`» | **True, verbatim.** UC-032 §6: *"A load rebuilds the state from the whole history, in order, every time. There is no cache, no snapshot and no partial rehydration."* (`docs/ai/usecases/modules/event/UC-032-…md:71-75`). |

**One thing the appendix does not say and phase 5 must know:** the fold is **not** reachable from
outside the kernel. `Aggregate.Fold(ID, S, ...Change[S]) (S, error)` is exported but folds
*decided `Change` values*, not `Envelope`s; the `envelope → fold` map is `Repo.apply`
(`event/repo.go:260-272`) over the unexported `aggregate.facts`. So an application **cannot** build
an at-version replay out of the public surface without re-writing its own type switch and
duplicating every fold. ES-08 is a genuine kernel gap, not a convenience.

**Also relevant, and already recorded rather than open:** `EVENTSOURCE_BACKLOG.md`
`## From the reference` §7 states why an at-version read is a *correctness* requirement and not a
debugging convenience, with the reference's own code — an integration event is the aggregate
re-read **at the event's own version**, and reading head state instead makes a replayed stream
*"indistinguishable from N copies of the current state."*

## ES-09 — what exists, and the gate the appendix told us to find

| Appendix claim (part 4) | Status |
|---|---|
| «полный replay и revision readers» | as ES-08. |
| «snapshots нет» | **True, and it is enforced, not merely absent.** |
| «**E2 уже ставит performance/equivalence gate**» | **Found. It is [[D-132]]**, `docs/ai/decisions/D-132-a-snapshot-is-added-from-a-measured-need-and-this-package-writes-no-line.md`. |

**[[D-132]] is what ES-09 part 4 points at, and it is stronger than "a gate".** Its invariant:
*"Full replay is the only authority a folded state has: no snapshot, no memo and no cache of one is
declared anywhere under `event/`, and none appears in the exported-surface baseline."* Its three
load-bearing parts:

1. **The measurement.** `D-132:18-21` — full replay of a 10 000-event aggregate is 10.19–10.93 ms
   with a no-op codec and 16.25–17.72 ms with `event.JSON`; 100 000 events is 104.4–170.6 ms;
   paging is a sixth of it, ~45 µs a page. Corrected in the ADR against an optimistic figure
   carried in from the specification.
2. **The re-entry trigger, which is the gate.** `D-132:57-62` — *"a snapshot is built when a
   deployment measures its own p99 aggregate replay above **~50 ms in its own environment** — at
   these rates somewhere above 30 000 – 50 000 events, or an order of magnitude fewer over a 1 ms
   link."* The instrument is `BenchmarkStreamReplay` in `event/eventpg/replay_integration_test.go`.
   **The gate is a measured *need*, not a measured *cost*, and `D-132:49` records that there is no
   consumer.**
3. **The enforcement.** `TestNoSnapshotAuthorityIsDeclaredOrPromised`
   (`scripts/projection_test.go:261-294`) walks every package the surface baseline lists under
   `github.com/frostgrove/vv/event` — which **includes `event/eventpg`** — and fails on any
   identifier matching `(?i)snapshot` (`scripts/projection_test.go:402`), plus any line of
   `docs/api/surface.md` under that prefix. It carries its own fixture control, so it cannot go
   vacuous.

**Therefore: ES-09 as written cannot be shipped anywhere under `event/` — including inside
`eventpg`, which is where its own part 5 puts it — without [[D-132]] being superseded.** That is
the single largest finding of this study and §ES-09 §4 takes it apart.

**And what [[D-132]] already owes the later phase, so phase 5 does not re-derive it:** `D-132:69-75`
points at `EVENTSOURCE_BACKLOG.md` `## From the reference` §6, which records in full the five
things the reference gets right and the one it gets wrong. All five are reproduced and adjudicated
against the Axon sources in §ES-09 below.

## The two process facts phase 5 will hit

- **`event/` outside `event/eventpg` is frozen by sha256.** `scripts/event_kernel.sha256` carries
  152 paths, one per file, and `make check-event-kernel` (`scripts/checks.sh:541-565`) refuses a
  drift rather than reporting ok when it cannot ask. ES-08 touches `event/repo.go`; ES-05 and
  ES-07 may touch `event/checkpoint.go` and `event/token.go`. Each needs
  `make check-event-kernel-baseline` **in the same change as the code**, and
  `check-event-kernel-moved` reads a move against a predecessor manifest.
- **The conformance suite is a closed inventory.** `event/eventtest/inventory.go:25-46` — twenty
  named sections, each with an optional `needs` gate that reports *not certified* rather than
  *pass*. Any new store-side capability (a bounded read, a snapshot policy) needs its section and
  its `needs` there, or it is proven for `eventpg` alone.

---

# §ES-05 — waiting for a specific change in a read model

## 1. The mechanism, as the sources describe it

### What Marten waits ON

Two APIs, one mechanism. From `docs/events/projections/async-daemon.md`:

```cs
// Wait for all projections to reach the highest event sequence point
// as of the time this method is called
await theStore.WaitForNonStaleProjectionDataAsync(15.Seconds());
```

```cs
// This query operation will first "wait" for the asynchronous projection building the
// Trip aggregate document to catch up to at least the highest event sequence number assigned
// at the time this method is called
var latest = await session.QueryForNonStaleData<Trip>(5.Seconds())
    .OrderByDescending(x => x.Started)
    .Take(10)
    .ToListAsync();
```

The target is *"the highest event sequence number assigned at the time this method is called"* —
**the sequence, not the high water mark.** The rendered page is explicit that these are different
numbers and that the wait uses the higher one.

The wait *"polls progress tables in the database"*, which is `mt_event_progression`: one row per
async projection shard holding its last processed sequence, plus the high-water-mark row in the
same table. The read API for the same data is `store.Advanced.AllProjectionProgress()`, returning
`state.ShardName` and `state.Sequence` per shard. Because it polls the database, it *"works
regardless of where the async daemon runs"* — including when the daemon is in another process, and
including when it is not running at all.

### The deadline

Default is `NonStaleDataTimeoutMode.ThrowException` → `TimeoutException`. The alternative is
explicit:

```csharp
var latest = await session
    .QueryForNonStaleData<Trip>(5.Seconds(), NonStaleDataTimeoutMode.ReturnStaleData)
    .OrderByDescending(x => x.Started)
    .Take(10)
    .ToListAsync();
```

The rendered page's warnings, verbatim: *"You may need to be both cautious with using this in
general, and also cautious especially with the timeout setting"*, and *"Do note that this can time
out if the projection just can't catch up to the latest event sequence in time."*

### Scope

The store-level overload waits for **every registered async projection** to pass the target, not
one shard; there is a database-level overload scoped to one projection type
(`theStore.Storage.Database.WaitForNonStaleProjectionDataAsync(typeof(SimpleAggregate), 5.Seconds(), CancellationToken.None)`) and a
multi-tenant overload taking a tenant id or database name. It *"can only work on one database at a
time."*

### The part no documentation page carries: the high water mark was not a completeness watermark

Marten discussion **#4953**, *"Async daemon can advance projection progress past committed events
during concurrent appends."* The reporter: *"During the import, I noticed that an async projection
could report as caught up even though some committed events had not been projected."*

The maintainer's diagnosis: the `GapDetector` runs three statements in sequence — a leading-gap
probe, an interior-gap check and a max-sequence lookup — *"each statement sees its own snapshot
under READ COMMITTED"*, so commits landing between them defeat gap detection; and when a sequence
number is reserved by an in-flight transaction, *"if that boundary falls in a hole, the next Normal
detection starts its window inside the gap."* The consequence is exactly the one that matters here:
the high water mark jumps over a sequence number that is reserved but uncommitted, and that event
is **permanently ignored once it commits.**

The fix shipped in **Marten 9.16.1** as *"transaction-evidence gating"*, using PostgreSQL's own
tracking of live transactions: *"with the transaction-evidence gating on by default, the daemon
should now hold for your in-flight appends rather than skipping them."*

## 2. The nuances that do not survive re-derivation

1. **"Wait until the projection catches up" is only a correctness primitive if the number it
   compares is a completeness watermark.** Marten shipped the wait years before the number under it
   was one. Until 9.16.1 a caller could get a successful `QueryForNonStaleData` and then read a read
   model that would never contain the event it waited for. The mechanism to copy is not the polling
   loop — it is the gating underneath, and that is the same xid-evidence argument vv's watermark
   walk already makes (`event/eventpg/read.go:82-130`).

2. **It waits on a *sequence assigned at call time*, and a sequence gap makes that number
   unreachable.** The rendered page names the failure: in scenarios with *"gaps in the event
   sequence (left by a failed append)"* the high water mark becomes *"effectively unreachable"*, so
   **every** call throws even though usable stale data is available. The target being the highest
   *assigned* number rather than the highest *deliverable* one is what makes an ordinary rollback
   turn a working wait into a permanent timeout.

3. **The wait cannot tell "behind" from "broken", and Marten's own maintainers filed that as a
   defect.** Issue #3912: *"WaitForNonStaleProjectionDataAsync() timeout because the projections
   were encountering errors, which don't necessarily get shown in _some_ testing harness usages"* —
   the proposal is something like Wolverine's message tracking *"that will blow up w/ a textual
   report of all exceptions."* A bare `TimeoutException` is the same answer for a slow projection, a
   halted one, a stopped daemon and a poison event.

4. **Three further ways it times out with nothing wrong**, all from the search of the maintainers'
   own issue tracker: a rebuilt *inline* projection writes a row into `mt_event_progression` that is
   never updated in continuous mode, so every later wait times out at any timeout value (issue
   #4041); the daemon's own advisory-lock connections were counted as possible appenders and pinned
   the mark for every later daemon in the process (fixed 9.23; workaround
   `UseAdvisoryLockTransaction = false`); and a slow shard's progression write used to lock every
   shard row in one `UPDATE … FROM unnest(...)` batch (fixed 9.22.4). **Every one of these is a
   liveness defect in the progress substrate that surfaces only as a timeout at the wait.** A wait
   API is a magnifying glass on the checkpoint store.

5. **Waiting is not the only shape.** Marten's `FetchLatest` takes the other road for a single
   stream: for an `Async` projection it *"queries the current snapshot and any unapplied events to
   guarantee current state"* — it catches up **in the read** instead of waiting for the projector.
   The cost is that it only works where the read target is one stream's aggregate, which is the case
   ES-05 cares least about; the case it cannot serve is a multi-stream read model, which is the
   reason for waiting at all. Worth recording because it is the alternative a reviewer will raise.

6. **`Commit` carries no position, and the appendix's "wait for a committed stream/version" hides
   one read.** `Progress.Highest` is a `Position`; a caller holds a `(Stream, Version)`. The map
   between them is `Store.ReadStream(ctx, stream, V-1)` **after** the caller's commit — the column
   is selected (`event/eventpg/read.go:305`) and the value is drawn at `INSERT`, so it is stable.
   Before the commit it is not usable: `Envelope.Position`'s own contract
   (`event/store.go:91-98`) says that inside the transaction that wrote it and has not committed it,
   *"it is unspecified"*, and UC-032 §14 says *"an event's place in the log means nothing until the
   append carrying it has committed."* So any ES-05 design that tries to capture a position from the
   append is wrong by contract, and any design that reads it afterwards costs one round trip and
   must state that it does.

7. **The scope sentence in the appendix is the one Marten does not make.** «Успех даёт видимость до
   барьера, не глобальную linearizability и не свежесть чужой read replica.» Marten's store-level
   wait is *global across all registered async projections*, which reads as a much bigger promise
   than it is: it says nothing about a second database, nothing about a replica, and nothing about a
   projection registered in another process. The per-type overload is the one whose promise matches
   its name.

## 3. vv delta

**What vv already has that is strictly stronger than the source:**

- `Progress.Highest` is a completeness watermark **by kernel law** ([[D-128]], invariant at lines
  7–11), certified ungated by the conformance suite, and `Reader.checkPage`
  (`event/reader.go:91`) refuses a page that breaks the ascending arm for *every* store. Marten
  reached the same property in 9.16.1 for one store; vv made it unforgeable.
- `eventpg`'s walk cannot pass a burnt gap: `deliverable` (`event/eventpg/read.go:214`) delivers
  only while positions are contiguous or the whole gap lies at or below what is settled, and
  `settledAt` (`:232`) settles only when the cluster floor has passed a bound minted after the
  reach was drawn. That is nuance 2 closed structurally — the gap does not make the target
  unreachable, it makes delivery *wait*, and the waiting is bounded by the oldest live transaction
  rather than by a detector's three snapshots.
- The "behind vs broken" answer exists as a published value: `projection.State`
  (`event/projection/state.go:32-41`) with `PhaseHalted`, `PhaseDegraded`, `PhaseBlocked`,
  `PhaseRetrying` (`:22-30`) and `State.Err`, plus `Progress.Quarantined` and `Park.Holes`. Marten
  issue #3912 is asking for what vv's `Observer` already publishes.
- The "is *my* change parked" question is answerable today: `Park.Holds(ctx, identity, sequence)`
  (`event/projection/park.go:76`) with `ByStream()`'s key = `event.Compose(family, key)`
  (`event/projection/sequence.go:33-37`), computable from `Commit.Stream()`.
- `Readiness{Reached, Behind, Quarantined, Holes}` (`event/projection/generation.go:225-231`) is
  already the return shape a deadline-bearing wait wants. Note `Behind` is documented as *"an upper
  bound and a hint"* (`:80-83`) because a log burns positions for rollbacks and OCC losers — the
  same fact that breaks Marten's target.

**What is missing, exactly:**

1. No wait, no poll loop, no deadline vocabulary anywhere under `event/`.
2. No read-only door onto a projection's `Progress` that is not `event.Track` + `Tracker.Load`.
   `Tracker` (`event/checkpoint.go:110-117`) is the **writer's** door: it holds a fence, is
   documented "one goroutine at a time", and `Tracker.Save` presents `advance+1`. A fresh
   `Track()`+`Load()` per poll is read-only and admissible (`admit`'s fifth check is skipped while
   `loaded` is false, `:205-215`), but nothing says so and a waiter that reused one tracker across
   a save would corrupt the fence. If ES-05 polls, whether it does so through `Tracker` is a
   decision, not an implementation detail.
3. No mapping from `(Stream, Version)` to `Position` in the caller-facing surface — `Repo` does
   not expose its `Store` (`event/repo.go:11-16`, all fields unexported), so a waiter must be
   handed the store separately.
4. `Progress` has no per-stream or per-sequence dimension, so "up to the barrier" is the only
   granularity available. That is consistent with ES-05's own scope sentence and should be stated
   rather than discovered.

## 4. Conflicts with binding decisions

- **`event/projection/doc.go:57-61` forecloses one of the two obvious designs, in as many words:**
  *"There is no head. A store that will not promise monotone visibility has no number that is the
  end of the log, so being caught up is a statement about the last read and never about the log:
  `PhaseFollowing` means the last read delivered nothing, and the next one may deliver events at
  positions a writer was still holding."* `eventpg` publishes `MonotoneVisibility: Unsupported`
  (`docs/modules/en/eventpg.md:324`). **"Wait until lag reaches zero" and "wait until
  `PhaseFollowing`" are both unimplementable here**, which is precisely what ES-05 part 3 already
  demands — the appendix and the code agree, and the reason is in the code and not in the appendix.
- **[[D-128]]** forbids restating `Progress.Highest` as "the highest position of the page"
  (`What it forbids`, bullet 4): *"It is the same number and a different promise."* A wait's
  documentation is exactly where that restatement happens by accident.
- **[[D-092]]** — anything continuous is a `runtime.Runner` and constructors start nothing. A wait
  is *not* continuous (it is bounded by its caller's deadline and runs on the caller's goroutine),
  so it is not a runner; but a background poller that pre-warms or caches progress would be, and
  `scripts/extensions_test.go`'s `startsNothing` arm forbids a `go` statement in any non-test file
  of `event/projection`.
- **[[D-132]]** forbids `event/projection` emitting any log line, span or metric. A wait that
  times out therefore reports through its return value and nothing else.
- **[[D-129]]** — a checkpoint is a store-minted cursor, never a position. A wait compares
  `Progress.Highest` (a `Position`, legitimately) and must never compare, order or resume from
  `Cursor` bytes. `generation.go:66-73` states the same rule for `Barrier.At` and is the precedent
  to follow: *"nothing resumes from it, stores it as a checkpoint or turns it into a cursor."*
- **UC-032 is not silently widened.** Nothing in its 28 clauses promises freshness; clause 18 is
  the opposite (*"Anything the author builds on top of it … is at least once"*). ES-05 needs its own
  use case, and its "What must hold" must carry the scope sentence from part 3 verbatim.

---

# §ES-07 — finding out what happened to an uncertain append

## 1. The mechanism, as the sources describe it

### What the appendix's own citation says

`docs.kurrent.io/clients/python/v1.3/appending-events`, verbatim:

> "If a successful append operation is retried with the same consistency checks and the same event
> IDs, the operation will succeed without appending duplicate events."

> "KurrentDB does not enforce globally unique event IDs. Event IDs are used only to detect retries
> of the same append operation."

> "This allows clients to safely retry append operations when the outcome is uncertain, for example
> when a network failure occurs after the operation has been committed but before the response has
> been received."

Error vocabulary: `append_to_stream()` raises `WrongCurrentVersionError`; `append_records()` raises
`ConsistencyChecksFailedError`.

**This page does not say what happens when the ids match and the content does not.** That is the
question ES-07 part 3 turns on, and the appendix's cited source is silent on it.

### The behaviour table, from the TCP client reference

`docs.kurrent.io/clients/tcp/dotnet/21.2/appending`. The server compares `expectedVersion` to
`currentVersion`:

1. `expectedVersion > currentVersion` — *"a `WrongExpectedVersionException` is thrown."*
2. `expectedVersion == currentVersion` — *"events are appended and acknowledged."*
3. `expectedVersion < currentVersion` — *"the `EventId` of each event in the stream starting from
   `expectedVersion` are compared to those in the append operation."*

Case 3 has three outcomes:

- *"All events have been committed already"* — succeeds, nothing duplicated.
- *"None of the events were previously committed"* — throws `WrongExpectedVersionException`.
- *"Some events were previously committed"* — *"considered a bad request. If the append operation
  contains the same events as a previous request, either all or none of the events should have been
  previously committed."*

And the caveat: *"Idempotence is **not** guaranteed if you use `ExpectedVersion.Any`. The chance of
a duplicate event is small, but is possible."*

### The maintainers on the boundary

`discuss.kurrent.io/t/…/1337`. Greg Young: *"We don't check within a batch as idempotency makes no
sense within a batch."* The purpose is stated as narrowly as possible: *"The main purpose is the
ability to retry requests."* And the overlap case: *"you will also find odd behaviour (from the
outside) if you for instance try {A,B,C} {C,D,E,F} the whole second batch will be rejected."*

So: same ids in **separate** operations → deduplicated. Same ids **within one** operation → both
written, no check performed.

## 2. The nuances that do not survive re-derivation

1. **Idempotence is a property of `(stream, expectedVersion, the ordered set of event ids)`, not of
   an id.** It is the positional comparison from `expectedVersion` forward that decides, which means
   the retry must present the **same expected version** as the original. A caller that re-loads
   after the uncertainty and retries at the *new* version has left the idempotence window: case 2
   fires and the events are appended again. This is reported repeatedly by users
   (*"setting expected version to something other than Any appears to prevent idempotency checks"*)
   and it is the mechanism working as specified, not a bug. **An operation receipt keyed on the
   caller's own key does not have this failure mode, which is the whole argument for the appendix's
   adaptation.**

2. **A partial overlap is a `[bad request]`, not a success and not a conflict.** This is the
   sentence ES-07 part 3 compresses to «другое содержимое с тем же ключом — конфликт», and the
   source is more specific than the compression: partial overlap is its own answer, distinct from
   both "already done" and "version moved", because it means the two operations were never the same
   operation. A receipt whose fingerprint is over the *whole* range rather than per-event
   reproduces this for free; one that compares per-event does not.

3. **Disabling the consistency check disables the idempotence.** `ExpectedVersion.Any` keeps the
   id comparison but loses the version anchor, and the docs decline to promise the result. The
   transferable rule: **an idempotence claim is only as strong as the concurrency check it is
   anchored to.** vv has no "any version" append (`AppendRequest.Expected`'s own contract,
   `event/store.go:68-74`: *"Zero means the stream has never been appended to and never means 'at
   any version'"*), so vv is on the strong side of this by construction — and the receipt must not
   introduce a weak side.

4. **Nothing about this is a retry, and the source says so.** The mechanism makes a *client's* retry
   safe; the server never retries. ES-07's status line («не скрытый retry `Store.Append`») is the
   same rule, and `event/store.go:199-211` already forbids it at the contract.

5. **The uncertain window vv actually has is not `Append`'s — it is the caller's `COMMIT`, and vv
   structurally cannot see it.** `event/eventpg/classify.go:27-39`:

   ```go
   func outcomeOf(failure error, issued, joined bool) event.Outcome {
       switch {
       case joined:              return event.NotWritten
       case !issued:             return event.NotWritten
       case errors.Is(failure, driver.ErrBadConn): return event.NotWritten
       case backendSurvived(sqlfault.Extract(failure)): return event.NotWritten
       }
       return event.Unconfirmed
   }
   ```

   On the joined path — an append inside the caller's own transaction, which is the path ES-07 is
   about — a failed statement is **`NotWritten`**, because *"a statement that failed leaves a
   transaction PostgreSQL will now refuse to commit"* (`:16-21`). `ErrUncertain` from `Append`
   therefore essentially does not arise inside a caller's transaction. The uncertainty ES-07 exists
   for lives entirely in the caller's own `COMMIT`, which vv never issues ([[D-118]], [[D-126]],
   `Repo.Within` opens nothing, `event/repo.go:111-123`). **This reframes the feature: the receipt
   is not a lookup of vv's append, it is a lookup of the application's transaction.**

6. **"An absent receipt does not prove rollback" has a precise database meaning and no clean
   answer.** A resolver on a second connection sees no row for either of two reasons — the writing
   transaction rolled back, or it is still open — and PostgreSQL gives the reader nothing to tell
   them apart: there is no row to lock, so a `FOR SHARE` blocks on nothing. The honest resolution is
   a **third answer** (`unresolved`), not a boolean, plus the operational lever the reference's own
   documentation obligation already names for a different stall:
   `idle_in_transaction_session_timeout` on the application role
   (`EVENTSOURCE_REFERENCE.md` §Documentation obligations, item 1). Phase 5 inherits that lever; it
   does not get a new one.

7. **Retention is what makes the window finite, and neither the reference nor the port solved it.**
   `EVENTSOURCE_BACKLOG.md` §5, on the port's `ES_IDEMPOTENCY_KEY` table: *"the key table grows
   monotonically with no TTL (only a `CREATED_AT` for a future sweep), and the claim puts a
   PK-index serialisation point on the command path."* Both costs are real and both are the
   appendix's «Retention квитанций ограничивает окно проверки».

8. **The port's fail-closed cross-aggregate check is the nuance that looks like three steps and is
   five.** `IdempotencyRepository.kt:43-54` writes `(IDEMPOTENCY_KEY, AGGREGATE_TYPE, AGGREGATE_ID)`
   with `ON CONFLICT DO NOTHING` under `MANDATORY` propagation, and `CommandGateway.kt:181-195`
   **rejects a redelivery routed to a different aggregate** rather than returning the unrelated
   aggregate's state. The row records *which aggregate the key was spent on*. Two concurrent
   transactions racing one key serialise on the primary-key index: the loser blocks until the winner
   commits, then sees 0 rows and is a duplicate — *"There is no window in which both append."*

## 3. vv delta

**Already there:**

- `ErrUncertain` (`event/errors.go:55`) and the rule that reading `err != nil` as "nothing was
  written" is the inference it exists to refuse (`event/repo.go:64-67`,
  `docs/modules/en/event.md:258-260`).
- Honest classification: `NotWritten` selected only on four proofs, `Unconfirmed` as the residue,
  and *"the last line is deliberately not a default arm of the switch"*
  (`event/eventpg/classify.go:23-26`).
- The three-way outcome vocabulary and the payload-digest comparison, shipped and reviewed in
  `jobs` (`jobs/queue.go:260-266`, `jobs/jobspg/driver.go:123-126`).
- `Commit.Authority()` + `Authority.Same` to prove the receipt and the append shared one
  transaction (`event/token.go:47`, `event/authority.go:36`), used exactly this way at
  `event/projection/pass.go:948-956`.
- The one-database refusal as precedent for "the receipt lives where the events do" — [[D-118]]'s
  *"'Atomic' across two handles is a sentence with no meaning."*

**Missing:** the receipt record, the fingerprint rule, the resolve door, the third answer, the
retention bound, and the decision D-118 requires before a table is written.

**A naming hazard worth flagging now:** in vv, "receipt" already means `Commit` — the word is used
that way in `event/token.go:23`, `event/eventmemory/transaction.go:35-36`, `event/eventmemory/doc.go:24`
and throughout `event/eventtest`. ES-07's «квитанция» is a different object. Reusing the word will
make every one of those comments ambiguous.

## 4. Conflicts with binding decisions

- **[[D-118]] — resolved in advance, but it costs a decision.** `EVENTSOURCE_REFERENCE.md` W11:
  *"Not blocked by [[D-118]] — a dedup claim is not a durable *intent*."* D-118's own text
  (`:99-101`) still requires: *"Do not add a second durable-intent table for effects that are
  already expressible as a job. If a job cannot express it, say why in a decision before writing
  the table."* The answer is presumably that a receipt is never delivered, has no worker and no
  lease, and must be atomic with an append in the *event* store's database rather than the job
  store's — but that sentence belongs in an ADR, not in a study.
- **UC-032 §8 is the boundary the receipt must not cross:** *"Recovery is a fresh load and a fresh
  decision — never re-proposing the same facts at whatever version the store reports, and the
  refusal deliberately does not report that version."* A `receipts.Resolve` that answered "here is
  what you wrote, append it again" would be exactly the re-proposal this clause refuses. ES-07's
  «framework не переигрывает доменное решение» is the same sentence from the other side.
- **[[D-126]] — the store chooses no isolation level.** The port's "no window in which both append"
  rests on PK-index serialisation, which holds at every level; a receipt design that needs
  `SERIALIZABLE` would be choosing one. The `jobs` shape holds without choosing.
- **`Store.Append` stays non-deduplicating** (`event/store.go:199-211`), and the appendix agrees.
  The receipt is a second write beside the append, in the same transaction — not a change to the
  append.
- **[[D-101]]/[[D-127]]** — a fifth table is schema version 3 and migrates only on an explicit
  profile. `event/eventpg/MIGRATIONS.md` has the exact precedent for a version that adds a table
  and alters none.

---

# §ES-08 — historical state at a version

## 1. The mechanism, as the sources describe it

### What the docs show

`martendb.io/events/projections/live-aggregates`, verbatim:

```csharp
var roomsAvailabilityAtPointOfTime =
    await theSession.Events
        .AggregateStreamAsync<RoomsAvailability>(hotelId, timestamp: pointOfTime);
```

```csharp
var roomsAvailabilityAtVersion =
    await theSession.Events
        .AggregateStreamAsync<RoomsAvailability>(hotelId, version: specificVersion);
```

```csharp
await theSession.Events.AggregateStreamAsync(
    streamId,
    state: baseState,
    fromVersion: baseStateVersion
);
```

`fromVersion` is documented as the starting version *"typically incremented to avoid duplicate
application"* — the snapshot+tail composition, in the same method.

**Neither the rendered page nor the markdown source says what a time-travel read refuses.** That
came from the source.

### What the source does — `src/Marten/Events/QueryEventStore.cs`, verbatim

```csharp
public async Task<T?> AggregateStreamAsync<T>(Guid streamId, long version = 0,
    DateTimeOffset? timestamp = null, T? state = null, long fromVersion = 0,
    CancellationToken token = default) where T : class
{
    var events = await FetchStreamAsync(streamId, version, timestamp, fromVersion, token)
        .ConfigureAwait(false);
    if (!events.Any())
    {
        return state;
    }

    if (version != 0 && version > events[events.Count - 1].Version) return null;

    var aggregator = _store.Options.Projections.AggregatorFor<T>();
    var aggregate = await aggregator.BuildAsync(events, _session, state, token)
        .ConfigureAwait(false);

    if (aggregate == null)
    {
        return null;
    }

    if (_session.TryGetStorageForLiveAggregation<T>(out var storage))
    {
        storage.SetIdentityFromGuid(aggregate, streamId);
    }

    return aggregate;
}
```

Three behaviours fall out, and all three are refusals-by-`null`:

- **No events at all** → returns the supplied `state`, which is `null` by default. A missing stream
  and an empty stream are the same answer.
- **A version past the end of the stream** → `null`. Not an exception, not a partial state — a
  silent `null` a caller can easily read as "not found".
- **A timestamp** → filters the fetch. No refusal for any value, including one before the stream
  began (which lands in the `!events.Any()` arm and returns `null`) or after it ended (which
  returns head state, correctly but indistinguishably from a version read).

### The append-token distinction, which Marten does make

`martendb.io/events/projections/read-aggregates`: `FetchForWriting` is the API that yields the
version token you append with; `FetchLatest` is *"a little more lightweight in execution than
`FetchForWriting`"*, is read-only, and is *"only available off of `IDocumentSession` and not
`IQuerySession`."* For a `Live` projection `FetchLatest` *"behaves similarly to
`AggregateStreamAsync()`"*. So the division ES-08 part 3 asks for — a historical read yields no
append token — exists in Marten as a **separate method**, not as a flag.

## 2. The nuances that do not survive re-derivation

1. **The source refuses nothing and vv's appendix refuses everything, and the appendix is right.**
   ES-08 part 3: «Неподдерживаемая revision или отсутствующий префикс возвращают отказ, не частичное
   состояние.» Marten returns `null` for *four different situations* — no stream, empty stream,
   version past the end, timestamp before the first event — which a caller cannot tell apart. This
   is the single largest divergence in this study, and it is the appendix diverging from its own
   cited source deliberately. **Do not read `AggregateStreamAsync` for the refusal shape.**

2. **`version > lastEvent.Version → null` means Marten fetches the prefix and only then notices.**
   The check is after `FetchStreamAsync`, against the last fetched event. So the refusal costs a
   full prefix read. For a paged reader that matters: vv's `replay` loop
   (`event/repo.go:198-219`) stops on a short page and has no confirming read, so "the prefix is
   short of the requested version" is knowable at the same point and for the same money — but only
   if the loop is told what it is looking for.

3. **`fromVersion` + `state` is the snapshot API in disguise, and it is why ES-08 and ES-09 are one
   design.** The same call that time-travels is the call that resumes from a base state at a base
   version. Marten's `fromVersion` is exclusive-of-the-base (*"typically incremented"* is the doc's
   way of saying the caller does the arithmetic, which is a footgun); the reference's SQL does the
   same thing correctly and states why —
   `AND (:fromVersion IS NULL OR VERSION > :fromVersion) AND (:toVersion IS NULL OR VERSION <= :toVersion)`,
   with the asymmetry *"deliberate: the snapshot already includes its own version"*
   (`EVENTSOURCE_BACKLOG.md` §6(3)). **The half-open/half-closed asymmetry is the nuance.** Get it
   wrong by one and either the base event is applied twice or the ceiling event is dropped.

4. **A timestamp read is not business time and is not commit order — and the reference proves it
   with an absence.** `EVENTSOURCE_REFERENCE.md` W15: *"**Nothing** in the write or read path uses
   time."* vv's `recorded_at` is `statement_timestamp()` — a *database* clock, so comparable across
   writers, which the reference's application clock is not — and [[D-128]]'s `What it forbids`
   closes the door explicitly: *"Do not order a global read by `recorded_at` … two writers can share
   an instant, and a commit can land long after the statement timestamp it carries."* ES-08's «первая
   граница — точная версия; timestamp не объявляется business-time» is that rule, restated for a
   per-stream read. If a timestamp boundary is ever added it can only be a *lookup that resolves to
   a version*, never an ordering.

5. **The reason an at-version read is correctness and not convenience is already recorded.**
   `EVENTSOURCE_BACKLOG.md` §7, from the reference's `OrderIntegrationEventSender.java:31-38`: an
   integration event is the aggregate re-read **at the event's own version**. Reading head state
   instead means *"with a large backlog (a new subscription replaying all history) **every**
   integration event carries head state — the entire replayed stream indistinguishable from N copies
   of the current state."* The Kotlin port dropped the `<= :version` clause and pays a full replay
   per historical read; for vv the clause is the point, not the cost.

6. **The result must not be appendable, and the reason is not tidiness.** An `At[S]` token minted at
   version 7 of a stream that is now at version 30 would be admitted by nothing —
   `AppendRequest.Expected` is compared against the current version and would conflict — so the
   danger is not a silent overwrite. The danger is the *caller's* reasoning: a value that looks like
   a load token invites a `Load → Decide → Append` shape whose decision was made against 23 events
   of missing history and which fails only at the store, if the stream happens not to have moved
   back. ES-08's «Результат не выдаёт append token» is about what a caller may be tempted to do, and
   Marten's answer — a different method entirely, on a different session type — is the shape.

## 3. vv delta

**Already there:** paged stream reads with a lower bound (`event/store.go:197`), the retained
revision chain and its upcasters (`event/chain.go`), `Fact.Read(Envelope)`, the full replay
(`event/repo.go:198-219`) and the page shape check that refuses a store answering out of order
(`event/repo.go:235-250`).

**Missing, precisely:**

1. No version ceiling on `replay`, and no caller-facing way to supply one.
2. **No public envelope→state fold.** `Repo.apply` (`event/repo.go:260-272`) and
   `aggregate.facts` are unexported; `Aggregate.Fold` folds `Change` values, not `Envelope`s. An
   application cannot build this outside the kernel without duplicating every fold.
3. No "prefix is short of the requested version" refusal. `replay` stops on a short page and
   returns what it has (`:215-217`) — correct for a load to head, wrong for a bounded one.
4. No second token type, and `At[S]` is the only thing `Append` accepts (`event/repo.go:68`).
   A historical read that returns `At[S]{}` returns a *forged-looking* token whose refusal path is
   `checkKey` on an empty key (`:69`, `:168-173`) — an accidental refusal rather than a designed
   one.
5. `Store.ReadStream` has no upper bound parameter, so a bounded replay either over-reads the last
   page and truncates in Go, or the store contract grows a parameter. **The latter is a contract
   change to a frozen file and needs its own argument** — and the [[D-128]] Reject entry for
   `Log.ReadAll`'s filter parameter (`EVENTSOURCE_REFERENCE.md` §Reject 3) is the precedent for how
   that argument is made and how it can go wrong.

## 4. Conflicts with binding decisions

- **UC-032 §6 says the opposite of ES-08 and the appendix knows it:** *"A load rebuilds the state
  from the whole history, in order, every time. There is no cache, no snapshot and no partial
  rehydration."* ES-08 needs **its own use case**; UC-032 is not widened. The appendix says this in
  part 4 and it is correct.
- **UC-032's `Out of scope` is the guard rail to keep:** *"A query language over history, an
  ordinary list-and-filter endpoint over events, or anything that makes a fact history behave like a
  collection."* ES-08's «читать полный префикс до указанной версии, не произвольный фильтр» is the
  same line. A `version` ceiling is a prefix; a predicate is a query language.
- **UC-032 §10:** *"The two reads are one aggregate's stream in order, and the whole log in the
  order it was committed."* A bounded prefix read is the first of those two with a ceiling — not a
  third read — which is the framing that keeps this inside the existing contract's spirit.
- **The kernel freeze.** `event/repo.go` is in `scripts/event_kernel.sha256`. ES-08 is the appendix
  most likely to touch frozen files, and the baseline must be re-recorded in the same change.
- **No conflict with [[D-132]].** A bounded replay is the same authority applied to a prefix, not a
  second authority. `TestNoSnapshotAuthorityIsDeclaredOrPromised` matches on the word `snapshot`
  only, so an `AtVersion` read trips nothing — provided it does not gain a base-state parameter,
  which is where ES-08 and ES-09 touch.

---

# §ES-09 — snapshots that are compatible, with a safe fallback

## 1. The mechanism, as the sources describe it

### `SnapshotFilter`, verbatim (`axon-4.11.x`, `eventsourcing/…/snapshotting/SnapshotFilter.java`)

```java
/**
 * Functional interface defining an {@link #allow(DomainEventData)} method to take snapshot data into account when
 * loading an aggregate. When providing an instance of this, take the following into account:
 * <ol>
 *     <li> Only return {@code false} if the snapshot data belongs to the corresponding aggregate <b>and</b> it does no conform to the desired format.</li>
 *     <li> Return {@code true} if the snapshot data belongs to the corresponding aggregate and conforms to the desired format.</li>
 *     <li> Return {@code true} if the snapshot data <b>does not</b> correspond to the desired aggregate.</li>
 * </ol>
 */
@FunctionalInterface
public interface SnapshotFilter extends Predicate<DomainEventData<?>> {
    default boolean allow(DomainEventData<?> snapshotData) { return test(snapshotData); }
    default SnapshotFilter combine(SnapshotFilter other) {
        return snapshotData -> this.allow(snapshotData) && other.allow(snapshotData);
    }
    static SnapshotFilter allowAll() { return snapshotData -> true; }
    static SnapshotFilter rejectAll() { return snapshotData -> false; }
}
```

### `RevisionSnapshotFilter`, the default

```java
public boolean test(DomainEventData<?> domainEventData) {
    String type = domainEventData.getType();
    String revision = domainEventData.getPayload().getType().getRevision();
    if (!Objects.equals(type, this.type)) {
        return true;
    }
    return Objects.equals(revision, this.revision);
}
```

From the reference guide: *"The `@Revision` annotation has a dedicated, automatically configured
`SnapshotFilter` implementation. This implementation is used to filter out non-matching snapshots
from the Repository's loading process."* And: *"When the `@Revision` on an aggregate is missing a
`RevisionSnapshotFilter` is configured for revision `null`."*

```java
@Revision("1")
public class GiftCard { }
```

### Where the filter runs — `AbstractEventStorageEngine.readSnapshot`

```java
@Override
public Optional<DomainEventMessage<?>> readSnapshot(@Nonnull String aggregateIdentifier) {
    return readSnapshotData(aggregateIdentifier)
            .filter(snapshotFilter::allow)
            .map(snapshot -> upcastAndDeserializeDomainEvents(Stream.of(snapshot),
                                                              getSnapshotSerializer(),
                                                              upcasterChain))
            .flatMap(DomainEventStream::asStream)
            .findFirst()
            .map(event -> (DomainEventMessage<?>) event);
}
```

**`.filter(…)` precedes `.map(upcastAndDeserialize…)`.** The compatibility decision is made on the
stored row's metadata, before a single byte of the snapshot payload is deserialized. There is **no
try/catch in this method.**

### The fallback — `AbstractEventStore.readEvents`

```java
@Override
public DomainEventStream readEvents(@Nonnull String aggregateIdentifier) {
    Optional<DomainEventMessage<?>> optionalSnapshot;
    try {
        optionalSnapshot = storageEngine.readSnapshot(aggregateIdentifier);
    } catch (Exception | LinkageError e) {
        optionalSnapshot = handleSnapshotReadingError(aggregateIdentifier, e);
    }
    DomainEventStream eventStream;
    if (optionalSnapshot.isPresent()) {
        DomainEventMessage<?> snapshot = optionalSnapshot.get();
        eventStream = DomainEventStream.concat(DomainEventStream.of(snapshot),
                                               storageEngine.readEvents(aggregateIdentifier,
                                                                        snapshot.getSequenceNumber() + 1));
    } else {
        eventStream = storageEngine.readEvents(aggregateIdentifier);
    }
    Stream<? extends DomainEventMessage<?>> domainEventMessages = stagedDomainEventMessages(aggregateIdentifier);
    return DomainEventStream.concat(eventStream, DomainEventStream.of(domainEventMessages));
}
```

Log message: `"Error reading snapshot for aggregate [{}]. Reconstructing from entire event stream."`
Tail begins at `snapshot.getSequenceNumber() + 1`. Staged (uncommitted, in-unit-of-work) events are
concatenated last.

### Triggers and the storage warning

`EventCountSnapshotTriggerDefinition(snapshotter, 500)` *"triggers snapshot creation when the number
of events needed to load an aggregate exceeds a certain threshold"*, wired per aggregate.
`AggregateSnapshotter` creates `AggregateSnapshot` instances holding the aggregate itself; the
`Executor` *"defaults to synchronous execution; async recommended for production"*, with the note
that an executor on another thread needs its own transaction management.

And the two warnings, verbatim: *"Do make sure the `Serializer` instance you use (which defaults to
the `XStreamSerializer`) is capable of serializing your aggregate"*, and — the one that matters most
— *"Snapshot events are stored automatically in the event store, replacing prior events during
normal operations. Archived events can be retained if you need to reconstruct pre-snapshot aggregate
states."*

## 2. The nuances that do not survive re-derivation

1. **The compatibility check runs on stored metadata, before deserialization, and that ordering is
   the mechanism.** A filter that had to deserialize first would fail on exactly the snapshots it
   exists to exclude. `readSnapshot` reads `DomainEventData.getType()` (the aggregate type string)
   and `getPayload().getType().getRevision()` (the `SerializedType`'s revision, a column beside the
   bytes). Any vv design that stores a revision *inside* the snapshot payload has the mechanism
   inverted.

2. **Axon's `@Revision` is a *serialized-form* version, not a *state-computation* version — and
   nothing in Axon closes the gap the appendix names.** `@Revision("1")` is bumped by a human when
   the aggregate's serialized shape changes. It is **not** bumped when an `@EventSourcingHandler`
   body changes while the fields stay the same. A build whose fold has changed therefore loads its
   predecessor's snapshots and serves a state no current code would compute, with no error anywhere.
   [[D-132]] already names this as *"the one this framework structurally cannot detect"*
   (`D-132:43-44`). **So ES-09's «версия вычисления состояния, независимая от payload revision» is
   a requirement the cited source does not meet, and phase 5 cannot copy it from Axon.** What is
   available is the shape — a human-set constant, compared before deserialization — with a
   different meaning attached and a different discipline for bumping it.

3. **The javadoc's rule 3 is a composition safety rule, and getting it backwards disables every
   other aggregate's snapshots.** *"Return `true` if the snapshot data **does not** correspond to
   the desired aggregate"* — because `combine` is AND across every registered filter. A filter that
   returns `false` for anything it does not recognise silently turns off snapshotting for the whole
   application. Any vv per-aggregate policy assembled from several parts inherits this hazard.

4. **A non-matching snapshot is ignored, not deleted — and the two live implementations disagree
   about that.** Axon filters it out and reconstructs from events; the row stays and every later
   load pays the same filter plus the same full replay, for ever. The reference's Kotlin port
   **deletes** on drift, and `EVENTSOURCE_BACKLOG.md` §6(4) records why: *"The delete is what stops
   every subsequent load re-detecting the same stale row and paying a full replay."* It also writes
   with `ON CONFLICT (AGGREGATE_ID, VERSION) DO UPDATE SET JSON_VERSION = EXCLUDED.JSON_VERSION,
   JSON_DATA = EXCLUDED.JSON_DATA` — *"not `DO NOTHING`"* — so a re-snapshot genuinely replaces a
   drifted one. ES-09's «Историю не удаляем» is about *events*; a snapshot is not history, and the
   study records that this is an open design question with two shipped answers, not a settled one.

5. **The fallback catches `Exception | LinkageError`, and the `Error` half is not defensive
   padding.** A snapshot of an aggregate class that has been renamed or removed fails with
   `NoClassDefFoundError`, which is an `Error` and not an `Exception`. A `catch (Exception e)` would
   take down the load. Go has no analogue of the class-loading failure, but the transferable rule
   holds: **the set of ways a stored snapshot can fail to become a value is larger than the set of
   ways the code that wrote it expects.**

6. **The fallback is silent.** One log line at the framework's own logger, and nothing in the
   return value distinguishes "loaded from a snapshot" from "the snapshot was unreadable and I
   replayed everything." A deployment whose serializer quietly changed can have *every* snapshot
   failing, paying full replay on every load, with the performance the snapshot was added for gone
   and no signal but log volume. **vv cannot even log** — [[D-132]] forbids `event/projection` a
   line, and the same argument applies to a kernel that publishes `State` instead. So ES-09's
   «Несовместимость/повреждение ведёт к полному replay» needs an *observable* fallback: a counter, a
   returned provenance, or an `Observer`-shaped signal. The source gives none and the appendix's
   «Нужны measured benefit» is the same requirement from the measurement side.

7. **The tail is half-open at `sequenceNumber + 1`, and the two SQL clauses that make it right are
   already recorded.** `EVENTSOURCE_BACKLOG.md` §6(3): the snapshot lookup is the newest **at or
   below** the requested version — `AND (:version IS NULL OR s.VERSION <= :version) ORDER BY
   s.VERSION DESC LIMIT 1` — and the forward read is `VERSION > :fromVersion AND VERSION <=
   :toVersion`. Dropping the `<= :version` clause (which the Kotlin port did) makes a read at
   version 12 of an aggregate snapshotted at 30 return *"future state labelled as version 12."*
   **This is the same clause ES-08 needs, which is why the two must be designed together.**

8. **The snapshot must be written INSIDE the append transaction, and the failure mode if it is not
   is permanent.** `EVENTSOURCE_BACKLOG.md` §6(1): a snapshot committed beside the append leaves,
   after a rollback, *"a snapshot at version 10 for a stream whose head is version 9. Every later
   load reads it, then reads `WHERE version > 10` and gets nothing, and returns state derived from
   events that never committed — permanently wrong, never repaired, because the snapshot is
   preferred over the log."*

9. **The cadence has a consequence nobody states.** §6(2): `finalVersion % N == 0` with `N >= 2`,
   enforced twice (`@Min(2)` at bind time and a runtime `nthEvent > 1` check). `N == 1` turns the
   event store into a state store with an audit log attached; `N == 0` is a divide-by-zero on the
   write path. And **a multi-event command jumps the boundary, so worst-case replay length is not
   bounded by N.**

10. **Do not read the reference's snapshot code for the shape.** §6(5): `AggregateStore.java:53-58`
    calls the snapshot insert inside the per-event append loop while testing the aggregate's final
    version, so a 2+-event command on a boundary executes the `INSERT` once per event with identical
    `(AGGREGATE_ID, VERSION)` against a bare `INSERT` with no `ON CONFLICT` — violating the primary
    key and aborting the command transaction. *"Invisible only because every sample command emits
    exactly one event."*

11. **Neither implementation prunes.** §6, closing: *"`ES_AGGREGATE_SNAPSHOT` grows one full-state
    row per N events forever and for a large aggregate can exceed the event log."*

12. **Axon's storage warning is the thing ES-09 must refuse outright.** *"Snapshot events are stored
    automatically in the event store, replacing prior events during normal operations."* That is the
    snapshot becoming the authority, which is precisely the second answer [[D-132]] and UC-032 §6
    exist to forbid. ES-09's «Историю не удаляем» is the refusal; the source is the reason it has to
    be written down.

## 3. vv delta

**Already there:** the full replay as the only authority (`event/repo.go:198-219`); per-event
revision with a declared upcaster chain (`event/chain.go`, `revision integer NOT NULL CHECK
(revision > 0)` at `event/eventpg/schema.go:258`), which is *"strictly more than the reference
(none) and more than the port (one `JSON_VERSION` per row)"* (`EVENTSOURCE_REFERENCE.md` W10); the
byte-faithful `payload bytea` (W13); the measurement, the instrument and the trigger ([[D-132]]);
and the five constraints plus the latent bug, recorded in full so they are not re-derived
(`EVENTSOURCE_BACKLOG.md` §6).

**Missing:** everything else — and deliberately.

**Missing that the sources do not supply either, and that phase 5 must invent:**

1. A **state-computation version** distinct from payload revision (nuance 2). Nothing in Axon, the
   reference or the port has one.
2. An **observable fallback** (nuance 6). [[D-132]] forbids the log line the source relies on.
3. The **snapshot+tail ≡ full replay equivalence proof** ES-09 part 3 demands. No source has one;
   both shipped implementations assert the property and test neither.
4. A **measured benefit** in a real deployment — which is [[D-132]]'s gate, and which has no owner.

## 4. Conflicts with binding decisions

**This is the appendix with a hard conflict, and it is not soluble by careful wording.**

- **[[D-132]] forbids ES-09 outright, everywhere ES-09 could live.** Invariant: *"no snapshot, no
  memo and no cache of one is declared anywhere under `event/`, and none appears in the
  exported-surface baseline."* `What it forbids`: *"Do not declare a snapshot type, field, column or
  function under `event/`, and do not publish one in `docs/api/surface.md`."* Enforced by
  `TestNoSnapshotAuthorityIsDeclaredOrPromised` (`scripts/projection_test.go:261-294`) over
  `checkedEventPackages`, which walks **every** package the surface baseline lists under
  `github.com/frostgrove/vv/event` — `event`, `event/eventmemory`, `event/eventtest`,
  `event/projection` **and `event/eventpg`** (`scripts/projection_test.go:549-579`). ES-09's own
  part 5 puts the policy on «конкретный event store», i.e. `eventpg`, which is inside the ban.
- **The gate is a measured *need*, and it is not met.** `D-132:49`: *"The gate is a measured need,
  not a measured cost. **There is no consumer.**"* The trigger is a deployment measuring its own p99
  aggregate replay above ~50 ms with `BenchmarkStreamReplay`. **This study found no such
  measurement anywhere in the repository.** Phase 5 has three honest routes and no fourth:
  (a) produce the measurement and let the trigger open the door; (b) write the ADR that supersedes
  [[D-132]] on a different argument — note ES-09's own «зачем» is *"ускорить длинные streams"*,
  which is the measured **cost** [[D-132]] explicitly refuses as a reason; (c) deliver ES-09's
  *design* — the contract, the compatibility rule, the fallback, the equivalence obligation — as a
  decision record without code, which is what «оптимизированный load требует отдельного принятого
  контракта» literally asks for.
- **UC-032's `Out of scope` is explicit:** *"Snapshots, and any second answer to what an aggregate's
  state is. Full replay is the only authority; the measured trigger for reconsidering that is
  recorded rather than left open."* ES-09 part 4 agrees: it needs its own accepted contract, not a
  quiet change to UC-032's guarantees.
- **[[D-118]]** — a snapshot written inside the caller's append transaction (nuance 8) is a durable
  write in the caller's transaction, which is the rule, not an exception to it. No conflict; it is
  the one part of ES-09 that fits the existing architecture perfectly.
- **[[D-126]]** — the CAS in `eventpg`'s single-statement append already holds the stream row
  (`event/eventpg/append.go`), which is what makes "no two writers race a snapshot for one
  aggregate" true here without choosing an isolation level, exactly as `EVENTSOURCE_BACKLOG.md`
  §6(1) says it is true for the reference.
- **[[D-101]]/[[D-127]]** — a snapshot table is a schema version bump on an explicit profile.

---

# Cross-cutting

## Where these four meet `EVENTSOURCE_REFERENCE.md`'s Reject entries

| Reject | Bears on | How |
|---|---|---|
| **1. `@Async` drain dispatch** ([[D-092]]) | ES-05 | A wait is not continuous and is not a runner. But any *pre-warming* or *cached progress* poller would be, and `startsNothing` forbids a `go` statement in a non-test file of `event/projection`. A wait must run on its caller's goroutine and cost nothing when nobody is waiting. |
| **2. A second durable-intent table** ([[D-118]]) | ES-07 | Already adjudicated as **not** blocking (W11, backlog §5) — a dedup claim is not a durable intent. The ADR D-118 demands is still owed. |
| **3. A filter parameter on `Log.ReadAll`** | ES-08 | Not the same call, but the precedent for how a read-contract parameter is argued and how it goes wrong: *"If that ever becomes the binding cost, the answer is a shared reader fanned out to N routers — not a filter on the store contract."* A version ceiling on `ReadStream` must be argued on the same terms. |
| **`SELECT … FOR UPDATE` before the write** ([[D-126]]) | ES-07, ES-09 | Both are tempted by it — a receipt claim and a snapshot write both "want" the row. `eventpg`'s append is **one statement**, which needs no transaction the store is forbidden to open, and the CAS already holds the row. |
| **Choosing an isolation level** ([[D-126]]) | ES-07 | The port's "no window in which both append" rests on PK-index serialisation, which holds at all three levels. A receipt design needing `SERIALIZABLE` would be choosing one. |

## Documentation obligations inherited, not created

`EVENTSOURCE_REFERENCE.md` §Documentation obligations item 1 — **the global `xmin` stall** — is the
operational lever both ES-05 and ES-07 depend on and neither can fix.
`pg_snapshot_xmin(pg_current_snapshot())` is the oldest running transaction id *in the whole
database*, so any session anywhere that has written and gone idle-in-transaction freezes the
watermark walk, and therefore freezes any ES-05 wait built on it, and is the same condition under
which an ES-07 receipt stays unresolved. The mitigations named there —
`idle_in_transaction_session_timeout` and `statement_timeout` on the application role — are the
whole answer, and phase 5 should say so rather than invent a second one.

## What a planner must decide before writing the phase-5 spec

Stated as questions, because this study decides nothing.

1. **ES-09's gate.** Measure, supersede [[D-132]], or ship the contract without the code. The
   appendix's own «зачем» is the cost argument [[D-132]] refuses by name, so route (b) needs a
   different argument than the one the appendix supplies.
2. **Whether ES-08 and ES-09 are one design or two.** Nuance ES-08/3 and ES-09/7 are the same SQL
   clause, and `fromVersion`+`state` is the same method in Marten. Designing the bounded replay
   without the base-state parameter, then adding it later, is the shape that keeps ES-08 clear of
   [[D-132]] — and it is also the shape that risks getting the half-open boundary wrong twice.
3. **Whether ES-05 polls through `Tracker` or through a new read-only door.** `Tracker` is the
   writer's value with a fence; a waiter creating one per poll works today and is documented
   nowhere.
4. **Whether the ES-05 wait's input is a `Position` the caller obtained (one extra `ReadStream`) or
   a `(Stream, Version)` the wait resolves itself (one extra `ReadStream`, inside).** The cost is
   the same; the difference is whether `Repo` has to expose its store.
5. **What the third ES-07 answer is called.** `unresolved` is not `NotWritten` and not
   `Unconfirmed`; the closed `event.Outcome` vocabulary (`event/outcome.go:5-13`) has seven members
   and adding one is a breaking change to the store-to-kernel channel.
6. **Whether "receipt" is the word.** It already means `Commit` in four files.
7. **Which of the four touch frozen kernel files**, and therefore need
   `make check-event-kernel-baseline` in the same change: ES-08 certainly (`event/repo.go`), ES-05
   and ES-07 probably.

## Gate state at the time of this study

`make unit` is **RED** on one arm: `TestNoI18nPackageCostsMoreThanItsErrorSeam` in `./scripts`,
because `i18n/cmd/vv-i18n` reaches `github.com/go-json-experiment/json/jsontext`, which is not on
that test's allowlist. **Foreign to this work**, arrived with the owner's i18n change, and not to be
fixed or allowlisted here. `make check` is also RED on `check-tidy` across 29 satellite modules,
traced by `EVENTSOURCE_P4_VERIFY.md` §1 to `56349ba` and likewise foreign. Neither was touched.
