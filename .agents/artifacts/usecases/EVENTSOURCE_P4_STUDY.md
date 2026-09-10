# EVENTSOURCE P4 — the sources behind ES-01, ES-02, ES-03, ES-04 and ES-06

**Purpose.** Read the five appendices in the original, read every mechanism they point at *at the
other end of the pointer*, and record what an implementer needs that the appendix's one-line
summary does not carry. This is a study. It specifies nothing, decides nothing and writes no code.

**Appendices read:** `docs/roadmaps/2026-09-01-postgres-event-sourcing-roadmap.md`, section
`## Дополнительные приложения — 2026-09-08`, lines 760–862. ES-01 (lines 776–782), ES-02 (784–792),
ES-03 (794–802), ES-04 (804–812), ES-06 (824–832). Read in Russian; part 3 (адаптация) of each
re-read several times, because that is where the constraints are.

**Baseline reconciled against:** `event/`, `event/eventmemory/`, `event/eventpg/`,
`event/projection/`, `event/eventtest/` as they stand after phase 3, plus
`.agents/artifacts/plans/EVENTSOURCE_P3_PLAN.md` and
`.agents/artifacts/gaps/EVENTSOURCE_REFERENCE.md`. The appendices were written against `c938866`,
**before `event/projection` existed** — so their "уже есть" sections are stale for ES-01 in
particular, and §0 below is the correction.

---

## Sources actually read, and the two that would not load as documentation

| Appendix | URL the appendix cites | Outcome |
|---|---|---|
| ES-01, ES-02, ES-06 | `https://docs.axoniq.io/axon-framework-reference/4.12/events/event-processors/streaming/` | **Read, twice, and both times the fetcher's summariser truncated it** — the Replay API section came back as "the full content appears to be truncated", the segment-mask scheme as "specific bitmask details aren't elaborated". Recovered in full from two other places, named below. |
| ES-01, ES-05 | `https://martendb.io/events/projections/async-daemon.html` | Read. Also read the doc's own markdown source, `JasperFx/marten/docs/events/projections/async-daemon.md`, which is longer than the rendered page's summary. |
| ES-03 | `https://docs.axoniq.io/dead-letter-queue-guide/4.13/` and its three subpages `implementing/`, `retrying/`, `advanced/` | All four read. The guide is deliberately introductory; it does not state overflow behaviour, the redrive ordering rule or the token interaction. Those came from the 4.11 reference page and the Axon 4.12.x sources. |
| ES-04 | `https://jeremydmiller.com/2025/03/26/projections-consistency-models-and-zero-downtime-deployments-with-the-critter-stack/` | Read. **It does not discuss atomic cutover, rollback, or shard/agent restart at all.** That absence is a finding, not a gap in the reading — see ES-04 §3. |
| ES-06 | `https://martendb.io/events/projections/side-effects` | Read (the page is served at `…/side-effects.html`), and its markdown source `docs/events/projections/side-effects.md`, which carries the blue/green gate paragraph the rendered summary compresses. |
| — | `https://apidocs.axoniq.io/latest/org/axonframework/eventhandling/Segment.html` | **HTTP 404.** Not substituted from memory: the class was read from source, `AxonFramework/AxonFramework@axon-4.12.x`, `messaging/src/main/java/org/axonframework/eventhandling/Segment.java`, 284 lines, quoted below. |

**Primary sources read from the implementations rather than the docs**, because the docs pages
omit exactly the parts the appendices' part 3 is about. All at tag/branch `axon-4.12.x` unless
noted, all under `messaging/src/main/java/org/axonframework/`:

`eventhandling/Segment.java` · `eventhandling/tokenstore/AbstractTokenEntry.java` ·
`eventhandling/tokenstore/jpa/JpaTokenStore.java` · `eventhandling/pooled/Coordinator.java` ·
`eventhandling/pooled/SplitTask.java` · `eventhandling/pooled/MergeTask.java` ·
`eventhandling/pooled/PooledStreamingEventProcessor.java` · `eventhandling/MergedTrackingToken.java` ·
`eventhandling/ReplayToken.java` · `messaging/deadletter/SequencedDeadLetterQueue.java` ·
`messaging/deadletter/DeadLetterQueueOverflowException.java` ·
`eventhandling/deadletter/DeadLetteringEventHandlerInvoker.java` ·
`eventhandling/deadletter/jpa/JpaSequencedDeadLetterQueue.java`;
plus `AxonIQ/reference-guide@master`, `axon-framework/events/event-processors/streaming.md`;
plus `JasperFx/marten@master`, `docs/events/projections/{async-daemon,rebuilding,side-effects}.md`.

---

# §0 — ES-01 reconciled against what phase 3 actually shipped

ES-01's part 4 says: *«Постоянного subscriber state и SQL-projector нет.»* **That is no longer
true.** Phase 3 shipped both. Phase 4 must not rebuild them.

## What ES-01 asks for, clause by clause, against the tree

| ES-01 clause (part 3 / part 5) | Where it is satisfied today | Verdict |
|---|---|---|
| «выбранный SQL-профиль фиксирует effect + checkpoint одной transaction authority» | `event/projection/spec.go:49` `Advance: InUnit`; `event/projection/pass.go:158-190` `insideAUnit`; `event/projection/pass.go:205-225` `claimed` — handler and `Tracker.Save` both run inside `Spec.Unit` | **satisfied** |
| the checkpoint store must be *able* to ride the caller's transaction | `event/projection/spec.go:175-177` — `InUnit` is refused when `Checkpoints.Capabilities().Transactions != event.Supported`, and never downgraded ([[D-130]]) | **satisfied** |
| «обработчик пишет effect/checkpoint в предоставленной SQL-транзакции» — and the framework opens nothing | `event/projection/spec.go:52-64` (`Unit` is the caller's, `crud.InNewTx` is the spelling); `event/projection/pass.go:551-570` `checkUnit` verifies, per pass and before the handler runs, that the checkpoint store's ambient executor is a transaction **and** that `Spec.Destination` resolves to a transaction | **satisfied, with one hole — see below** |
| «у текущего `eventpg` log читается вне write transaction, чтобы не принять собственные staged rows за подтверждённую историю» | `event/projection/projection.go:86-93` states it as a load-bearing property of the loop; `event/eventpg/read.go:110-120` (step 7) is the store half — a bound transaction mints no settlement bound | **satisfied** |
| «Курсор не проходит незавершённый append, `MAX(sequence)` не доказательство видимости» | `event/eventpg/read.go:131-177` `ReadAll` — the `(from, bound, reach)` walk; `read.go:214-223` `deliverable`; `read.go:232-237` `settledAt`; `logStatement` at `read.go:317-327` reads `pg_snapshot_xmin(pg_current_snapshot())` as the floor. Certified live by `event/eventtest` §resumption's held writer (backlog §59, closed by P3 S2) | **satisfied** |
| persistent subscriber state, keyed by a name | `event/checkpoint.go:52-57` `Checkpoint`; `:76-85` `Checkpoints`; `:90-98` `Track`; `event/eventpg/schema.go:311-341` the `checkpoints` table at schema version 2 (`projection` PK, `cursor bytea`, `advance bigint`, `highest/applied/quarantined`, `updated_at`) | **satisfied** |
| a crash between effect and checkpoint loses nothing and re-applies no committed SQL effect | `InUnit`: one commit. `AfterApply`: `pass.go:126-139` handle-first/advance-last, re-delivery on crash. `event/checkpoint.go:243-268` `Tracker.Save` + `event/eventpg/checkpoints.go:353-364` `saveStatement`'s fenced `UPDATE … WHERE advance = $3 - 1` and the split `INSERT … ON CONFLICT DO NOTHING` at advance 1 | **satisfied** |
| «после crash запуск продолжает сохранённую позицию» | `pass.go:55-68` `resume` → `tracker.Load` → `event.Read(spec.Log, held.Cursor)` | **satisfied** |
| a save whose outcome could not be read off its answer | `pass.go:352-411` `pending` / `settle`, and the four-way table in [[D-133]] | **satisfied, beyond ES-01** |
| «Чужая БД/HTTP требуют своей идемпотентности» | `docs/modules/en/projection.md:75-96` — the delivery row, `InUnit`'s precondition, and `projection.Unchecked`. `Quarantines` (`event/projection/classify.go`) is called **inside** the unit for the same reason | **satisfied as documentation**; unproven as a *contract* — see below |

## What ES-01 still owes after phase 3

Four items, and only the first two are ES-01's own.

1. **`checkUnit` proves two transactions exist; it does not prove they are the same one.**
   `event/projection/pass.go:551-570` asks `this.tracker.Transaction(ctx)` (the checkpoint store's
   backing) and `crud.ExecutorFor(ctx, this.spec.Destination)` (the handler's) and requires each to
   be a transaction — separately. A composition whose `Unit` opens two transactions, one per
   resource, passes every check in the package and gets `AfterApply` atomicity under an `InUnit`
   spec, silently. `spec.go:52-64` states the obligation in prose (*"it must open ONE transaction"*)
   and `docs/modules/en/projection.md:88-96` states it again; nothing measures it. ES-01's whole
   sentence is *«одной transaction authority»* — **one**. This is the one clause of ES-01 that is
   asserted rather than held. (Whether it *can* be held is a real question: `event.Authority`
   carries a `Backing` and `crud.ExecutorFor` answers an `executor`; comparing them across the two
   seams is the design work, and `Unchecked` is the escape hatch that must survive it.)

2. **Nothing states, in a testable form, what a handler that reaches a foreign resource owes.**
   ES-01 part 3's last sentence — *«Чужая БД/HTTP требуют своей идемпотентности»* — is on the module
   page as advice. ES-06 part 3 turns the same sentence into an obligation with teeth
   (*«произвольный HTTP внутри пользовательского projection callback запрещается его контрактом и
   проверяется тестом, не блокируется магией»*). So this clause is not ES-01's remaining work; it
   is ES-06's, and it is recorded there.

3. **`Capabilities().MonotoneVisibility` is still `Unsupported` on `eventpg`** (ES-01 part 4 says
   so and it is unchanged). Nothing in ES-01 requires it; recorded so nobody reads its absence as
   an omission.

4. **The two operational facts ES-01's mechanism carries are both already on the module pages** —
   checked, not assumed. The global `xmin` stall is documented (backlog §"From the reference" item
   3, closed by P3 S5), and *"A new projection reads the whole log"* is at
   `docs/modules/en/projection.md:231-232` and `docs/modules/en/eventpg.md:267-268`, beside
   `Checkpoint.Fresh()` (`event/checkpoint.go:59`). **ES-04 inherits that second one verbatim**, and
   should cite it rather than restate it: a generation is a new projection name, so a rebuild
   replays every event ever written through the new generation's handler.

**Conclusion for planning: ES-01 is ~90% shipped. Phase 4's ES-01 work is item 1 (make
`one transaction authority` a check or an explicitly recorded impossibility) and nothing else.**
Everything the appendix's part 5 sketches — «имя подписчика + read-only log + обработчик batch в
предоставленной SQL-транзакции; после crash запуск продолжает сохранённую позицию» — compiles today.

---

# §ES-01 — durable subscriptions and an atomic SQL projection

## 1. The mechanism, as the sources describe it

### Axon: the tracking token, the claim, and why the two commit together

A `TrackingToken` "specifies the position of the event on the overall stream". It is stored through
a `TokenStore` — `JpaTokenStore`, `JdbcTokenStore`, `MongoTokenStore`, `InMemoryTokenStore` (the
last "is *not* recommended in most production scenarios since it cannot maintain the progress
through application shutdowns").

The reference guide's recommendation is the whole of ES-01 in one sentence:

> "Where possible, we recommend using a token store that stores tokens in the same database as to
> where the event handlers update the view models. This way, changes to the view model can be
> stored atomically with the changed tokens."

The *enforcement* is not a transaction manager — it is a fence in the `UPDATE`.
`JpaTokenStore.storeToken` is:

```sql
UPDATE TokenEntry te SET te.token = :token, te.tokenType = :tokenType, te.timestamp = :timestamp
 WHERE te.owner = :owner AND te.processorName = :processorName AND te.segment = :segment
```

and when that matches zero rows it falls back to `loadToken(...)`, which throws
`UnableToClaimTokenException` if the entry is owned by somebody else. The guide's own account of
what that produces:

> "Axon Framework will combine committing the event handling task with updating the token. As the
> token claim is required to update the token, the original thread will fail the update. Following
> this, a rollback occurs on the Unit of Work. […] if you store the projection in the same database
> as the token, the rollback will ensure the change is not persisted. Thus, the consequence of
> token stealing is limited to wasting processor cycles."

### Marten: the daemon, the high water mark, and `mt_event_progression`

The daemon commits "the projection changes and the progression update to `mt_event_progression` in
a single database transaction". Progress is per shard, per projection, per tenant database.

The high water mark is Marten's spelling of vv's settled watermark, and its doc says why the naive
version is wrong:

> "The high water mark is the point below which the daemon knows every event is committed and
> safely ordered. It advances contiguously, so it stops under any hole in the event sequence.
> Most holes fill in within milliseconds […] Some never fill: a rolled-back `SaveChangesAsync`, or
> an optimistic-concurrency loser, burns its sequence numbers permanently, because PostgreSQL
> sequences are non-transactional. The daemon cannot tell those two apart by looking at the hole,
> so it asks a different question: is any transaction still running that could have reserved it?
> While the answer is yes, the mark holds. Once no such transaction remains, the gap is proven dead
> and the daemon skips the entire dead span in one step, recording it in `mt_high_water_skips`."

That is ES-01's *«`MAX(sequence)` не доказательство видимости»*, stated by the other implementation
that hit it.

## 2. The nuances that do not survive re-derivation

1. **The claim is *in the same `UPDATE`* as the progress.** Axon does not lock, then write. The
   `WHERE te.owner = :owner` clause is both the mutual exclusion and the write. vv's fenced
   `WHERE advance = $3 - 1` is the same shape with a monotone counter instead of a node id, and the
   counter is strictly better: a node id is stable across a process's whole life, so an owner that
   is *slow* rather than dead keeps writing; an advance that is one behind cannot.
2. **Claim expiry is time-based and steals rather than blocks.** `claimTimeout` defaults to 10 s;
   `AbstractTokenEntry.mayClaim` is `this.owner == null || owner.equals(this.owner) || expired(claimTimeout)`,
   and `expired` is `timestamp().plus(claimTimeout).isBefore(clock.instant())`. The timestamp is
   refreshed at every batch commit and by `extendClaim`. So **a batch longer than `claimTimeout`
   loses the claim mid-batch** and the losing write rolls back the projection changes with it. The
   knob for that is `claimExtensionThreshold` (default 5000 ms, pooled) / `eventAvailabilityTimeout`
   (default 1000 ms, tracking) — i.e. the framework refreshes the claim *while idle*, not while
   busy. A handler slower than 10 s is a design error in Axon's model, and the doc never says so.
3. **The recovery from a stolen claim is different in the two processors, and both are documented
   as back-off rather than halt.** Tracking: "incremental back-off period. It will start at 1
   second and double after each attempt until it reaches the maximum wait time of 60 seconds per
   attempt." Pooled: "simply aborts the failed part of the process", with the same back-off in the
   same JVM. **Neither halts.** vv reached the same answer independently in [[D-133]], and the
   reason given there — a rolling deploy runs two instances of a singleton on purpose — is the
   reason Axon's model needs a timeout at all.
4. **Marten's default is to skip errors in continuous mode and to fail them in rebuild mode.**
   `SkipApplyErrors`/`SkipSerializationErrors`/`SkipUnknownEvents` default `true` continuous and
   `false` on rebuild. "In all cases, if a serialization, apply, or unknown error is encountered and
   Marten is not configured to skip that type of error, the individual projection will be paused. In
   the case of projection rebuilds, this will immediately stop the rebuild operation." The
   asymmetry is deliberate and is worth carrying into ES-04: **a rebuild that skips is a rebuild
   whose output is not comparable to the live table**, so a rebuild must be stricter than the
   thing it is rebuilding.
5. **Marten skips an empty progression write on an idle poll** in the same way vv does — and
   Marten records what it skipped over (`mt_high_water_skips`, opt-in via
   `EnableAdvancedAsyncTracking`) so an operator can answer "which streams and projections may be
   impacted by a skip". vv's equivalent is `Progress.Quarantined` (a count, not a list). Recorded
   under ES-03.
6. **Marten offers an explicit, dangerous escape from the stall vv also has.**
   `SkipStaleGapsDespiteLiveTransactionsAfter` and `AdvanceHighWaterMarkToLatestAsync()`, each with
   a warning that "an append that commits inside the skipped range will never be projected", and a
   recommendation that "PostgreSQL's own `idle_in_transaction_session_timeout` is usually the better
   backstop, since it removes the cause rather than working around it". vv has neither knob and
   documents the same cause. If ES-01 ever grows one, this is the shape and the warning text.

## 3. vv delta

See §0. The delta is one item: `checkUnit` (`event/projection/pass.go:551-570`) checks *two*
transactions, ES-01 asks for *one authority*.

## 4. Conflicts with binding decisions

- Axon's `claimTimeout` is a **time-based** steal. vv cannot adopt it: [[D-126]] forbids the store
  a clock-driven policy, and `Progress.At` is explicitly "the consumer's own observation" with "no
  grain named here" (`event/checkpoint.go:33-40`). vv's fence is ordinal, which needs no clock.
  **What vv must prove instead:** that an instance which loses the fence resumes from the winner's
  cursor and keeps working — already proved, [[D-133]] `TestTwoLiveInstancesOfOneNameTakeTurnsAndNeitherHalts`.
- Marten's `AdvanceHighWaterMarkToLatestAsync` is a store-side "skip everything below" operator
  command. Any vv analogue is a `Position → Cursor` function, which [[D-129]] forbids by name and
  `scripts/projection_test.go` walks the surface for.

---

# §ES-02 — parallel subscribers that keep order within a sequence

## 1. The mechanism

### The sequencing policy

`SequencingPolicy` is one method: `Object getSequenceIdentifierFor(T event)`. The rule:

> "If the return value of the `SequencingPolicy` function is equal for two distinct event messages,
> it means that those messages must be processed sequentially."

Shipped policies: `SequentialPerAggregatePolicy` (the default — the aggregate id),
`FullConcurrencyPolicy`, `SequentialPolicy`, `PropertySequencingPolicy`, `MetaDataSequencingPolicy`.
That list is itself the appendix's point — *«aggregate ID подходит не для любой multi-stream
проекции»* — the default is the aggregate id and Axon ships four ways to say it is not.

### The segment, and why it is a mask and not a modulus

This is the part every documentation page omits and the whole of why ES-02's part 3 says *«не
замены `hash % N` на ходу»*. From `Segment.java`:

```java
/** A {@link Segment} is a fraction of the total population of events.
 *  The 'mask' is a bitmask to be applied to an identifier, resulting in the segmentId of the {@link Segment}. */
public static final Segment ROOT_SEGMENT = new Segment(0, ZERO_MASK);

protected Segment(int segmentId, int mask) {
    Assert.isTrue(mask == 0 || mask + 1 == Integer.highestOneBit(mask + 1),
                  () -> "Invalid mask. It must end on a consecutive series of 1s");
    …
}

public boolean matches(int value)    { return mask == 0 || (mask & value) == segmentId; }
public boolean matches(Object value) { return mask == 0 || matches(Objects.hashCode(value)); }

public Segment[] split() {
    if ((mask << 1) < 0) { throw new IllegalStateException("…the mask exceeds the max mask size."); }
    int newMask    = ((mask << 1) + 1);
    int newSegment = segmentId + (mask == 0 ? 1 : newMask ^ mask);
    return new Segment[]{ new Segment(segmentId, newMask), new Segment(newSegment, newMask) };
}

public int mergeableSegmentId() { int parentMask = mask >>> 1; int firstBit = mask ^ parentMask; return segmentId ^ firstBit; }
public boolean isMergeableWith(Segment other) { return this.mask == other.mask && mergeableSegmentId() == other.getSegmentId(); }
public Segment mergedWith(Segment other) { return new Segment(Math.min(this.segmentId, other.segmentId), this.mask >>> 1); }
```

So: a segment is `(id, mask)` where the mask is always `2^k − 1`; a key belongs to the segment whose
id equals the *low k bits* of its hash. A split takes segment `S` at mask `m` to two segments at
mask `2m+1`: `S` and `S + (newMask ^ mask)`. **Every key that was in `S` is still in exactly one of
those two, and no key in any other segment moves.** A merge is the exact inverse and is only legal
between the two halves of one split — `isMergeableWith` demands identical masks and a difference in
exactly the mask's leading bit.

`Segment.computeSegment(int segmentId, int... availableSegmentIds)` reconstructs the mask from the
set of ids currently in the token store. There is no stored "N".

### The claim protocol during a split or a merge

`Coordinator.splitSegment(int)` / `mergeSegment(int)` return `CompletableFuture<Boolean>`. The
sequence in `SplitTask`:

1. `releasesDeadlines.put(segmentId, now + 60s)` — "Block the segment from being claimed by the
   Coordinator while we perform the split. This prevents a race condition where the Coordinator
   might claim a segment that's being split."
2. `workPackages.remove(segmentId)` — "Remove WorkPackage so that the CoordinatorTask cannot find
   it to release its claim upon impending abortion."
3. if this node was processing it: `workPackage.abort(null)` and wait; otherwise
   `tokenStore.fetchSegments(name)` → `Segment.computeSegment(segmentId, segments)`.
4. inside **one** transaction: `fetchToken` (which claims), `TrackerStatus.split(...)`,
   `tokenStore.initializeSegment(splitStatuses[1].token, name, splitStatuses[1].segmentId)`,
   `tokenStore.releaseClaim(name, splitStatuses[0].segmentId)`.
5. `finally { releasesDeadlines.remove(segmentId); }`

`MergeTask` is the same shape with two segments and one extra rule:

```java
int tokenToDelete = mergedSegment.getSegmentId() == thisSegment.getSegmentId()
        ? thatSegment.getSegmentId() : thisSegment.getSegmentId();
TrackingToken mergedToken = thatSegment.getSegmentId() < thisSegment.getSegmentId()
        ? MergedTrackingToken.merged(thatToken, thisToken)
        : MergedTrackingToken.merged(thisToken, thatToken);
transactionManager.executeInTransaction(() -> {
    tokenStore.deleteToken(name, tokenToDelete);
    tokenStore.storeToken(mergedToken, name, mergedSegment.getSegmentId());
    tokenStore.releaseClaim(name, mergedSegment.getSegmentId());
});
```

`MergedTrackingToken` is *"Special Wrapped Token implementation that keeps track of two separate
tokens, of which the streams have been merged into a single one. This token keeps track of the
progress of the two original 'halves', by advancing each individually, until both halves represent
the same position."* Its `advancedTo` collapses back to a plain token only when both halves have
advanced to the same value.

### The guard against a *concurrent* split or merge

`JpaTokenStore.loadToken(String, Segment, EntityManager)` — this is the piece with no documentation
page at all:

> "If a token has been claimed, the `segment` will be validated by checking the database for the
> split and merge candidate segments. If a concurrent split or merge operation has been detected,
> the claim will be released and an `UnableToClaimTokenException` will be thrown."

and `validateSegment` reads: *"If the segment has been split concurrently, the split segment
candidate will be found, indicating that we have claimed an incorrect `segment`. If the segment has
been merged concurrently, the merge candidate segment will no longer exist, also indicating that we
have claimed an incorrect `segment`."*

## 2. The nuances that do not survive re-derivation

1. **The segment count is never stored and never compared.** What is stored is a *set of segment
   ids*, one token row each, `PRIMARY KEY (processorName, segment)`. The mask is derived from the
   set. A node that has stale beliefs about the topology discovers it by claiming a row and then
   failing `validateSegment` — not by reading a version number. **This is the mechanism ES-02's
   part 3 asks for**: *«Изменение числа partitions требует согласованной передачи позиции»* — the
   handover *is* "write the new row at the old row's position, then release the old claim, in one
   transaction".
2. **Why `hash % N` is not merely inconvenient but corrupting.** Under a mask, a split moves keys
   only *within* the segment being split, and the new segment starts at the *old segment's exact
   token*. Under `hash % N` → `hash % (N+1)`, roughly `N/(N+1)` of all keys change partition, and
   each lands in a partition whose checkpoint is at an unrelated position. For a key that moves
   from a partition at position 900 to one at position 1200, events 900–1200 of that key are never
   delivered; for one moving the other way they are delivered twice, and — this is the part ES-02
   names — **out of order relative to the events of the same key already applied.** `OrderPaid`
   before `OrderCreated` is not hypothetical; it is the median outcome.
3. **Only the *first* start may choose the count.** "Only when a streaming processor starts for the
   first time can it initialize the number of segments to use. This requirement follows from the
   fact each token represents a single segment." Defaults: `TrackingEventProcessor` 1,
   `PooledStreamingEventProcessor` 16. `JpaTokenStore.initializeTokenSegments` throws
   `UnableToClaimTokenException("Could not initialize segments. Some segments were already present.")`
   rather than adopting whatever is there.
4. **Split and merge require the claim, and fail rather than wait.** "either the streaming
   processor already has a claim on the segments or can claim the segments. Without the claims, the
   processor will simply fail the split or merge operation." And the balancing advice is
   asymmetric: "a split is ideally performed on the largest segment […] a merge is ideally
   performed on the smallest segment."
5. **A merge of two halves at different positions is not `min(a, b)`.** It is a composite token
   that replays `(min, max]` and delivers each event only to the half that had not passed it.
   Taking the min would re-deliver to the ahead half; taking the max would skip for the behind half.
   Both are silent.
6. **A 60-second block, held in memory, is what keeps the coordinator from re-claiming a segment
   mid-topology-change.** It is not durable, and it is not the correctness mechanism — the
   `validateSegment` check is. The block is a latency optimisation over a race that would otherwise
   resolve by an exception. Worth copying the *layering*, not the 60 seconds.
7. **`FullConcurrencyPolicy` exists and is the honest name for "this projection has no ordering
   requirement".** A framework that only offers "sequence by aggregate id" pushes applications into
   declaring an ordering they do not need and paying for it.

## 3. vv delta

| ES-02 asks | vv today |
|---|---|
| a partitioned subscriber | Nothing. One `Projection` = one loop = one cursor (`event/projection/projection.go:22-52`). |
| `SequenceBy(key)` chosen by the application | Nothing. `Batch` (`event/projection/page.go:12-22`) carries the whole page in position order and the handler decides. `Router` (`event/projection/router.go`) fans out by `(family, type)` and not by key. |
| a partition count that cannot be changed by swapping `hash % N` | Nothing — and note vv is currently *safe by absence*, which is a different thing from being safe by construction. |
| «SQL commit проверяет актуальное поколение claim вместе с effect/checkpoint» | The half that exists: the fenced save, `event/eventpg/checkpoints.go:353-364`, and [[D-133]]'s claim-before-handler order (`pass.go:205-225`). What does not exist is a *claim generation* distinct from the advance. |
| ordered stream/log reads | `event/store.go:125` (`ReadAll` contract), [[D-128]] — a law, ungated. |
| a host-owned runner | `runtime/runner.go:11-16`, `:26-51` — `Declaration{Placement, Durability}` with **only** `PerReplica` and `Singleton`. |

**Three structural constraints ES-02's design must be written around, none of them negotiable:**

- **`event/projection` may not start a goroutine.** `scripts/extensions_test.go:147` `startsNothing`,
  called from `scripts/event_test.go:66-68` for the whole `event/` prefix, forbids a `go` statement
  in any non-test file of any package under `event/` that does not import `testing`. So there is no
  coordinator thread, no worker pool, and no in-process fan-out. N partitions is N
  `runtime.Runner` values the *host* supervises. This is [[D-092]] and the reference's `@Async`
  reject (EVENTSOURCE_REFERENCE §Reject 1) arriving at the same place from a third direction.
- **`runtime.Supervisor` refuses duplicate runner names** (`runtime/supervisor.go:64-68`,
  `ErrDuplicateRunner`), and `Projection.Name()` is `"vv.event.projection." + spec.Name`
  (`projection.go:71`). So each partition needs its own `Spec.Name`, which is also its checkpoint
  row key (`checkpoints` PK is `projection`, `event/eventpg/schema.go:311-341`). A name carrying the
  segment survives that unchanged — but it must pass `checkName` (`event/text.go:62-70`): within
  `MaxNameBytes`, valid UTF-8, no control characters, **no `[` or `]`**. `orders#3/8` is legal;
  `orders[3/8]` is not.
- **`Declaration.Placement` has no vocabulary for "one of N".** `Singleton` per partition name is
  the truthful answer and probably the right one; but it means `Placement` is answering a question
  about a *name*, not about a *projection*, and the module page's "one name is one writer" section
  (`docs/modules/en/projection.md:138-180`) is written about the projection.

## 4. Conflicts with binding decisions

- **A library-started thread pool** — rejected, [[D-092]] + `startsNothing`. *What vv must prove
  instead:* that N supervised runners over N disjoint key sets deliver each key's events in order
  and each event to exactly one runner, with a control showing the ordering breaks when the
  partitioning does.
- **`FOR UPDATE SKIP LOCKED` before the read** — [[D-126]] forbids the store a transaction, and
  `Log.ReadAll` is outside every unit ([[D-130]]). Axon's claim is not `SKIP LOCKED` either; it is
  the fenced `UPDATE`, which vv already has. *What vv must prove instead:* that the claim generation
  (if ES-02 grows one) is checked in the same statement as the advance — [[D-133]]'s
  claim-before-handler ordering already establishes the technique.
- **A `Position → Cursor` function** — [[D-129]]. This bites ES-02 hard: Axon's split gives both
  halves *the same token*, and its merge builds a composite from two. vv's cursor is opaque bytes
  that may only be compared for equality and emptiness. **A split in vv is therefore "copy the
  cursor bytes into a second checkpoint row"** — legal, and it works precisely because the cursor is
  the store's own encoding. **A merge is the hard one**: vv has no `MergedTrackingToken` and no way
  to build one without ordering cursors, which [[D-129]] forbids. The honest options are (a)
  partition counts only ever grow, (b) a merge is "retire both rows and rebuild the merged partition
  from the *lower* of the two cursors, tolerating re-delivery to the ahead half" — which is legal at
  least-once and must be *stated*, not discovered, or (c) a merge is refused. Whichever is chosen,
  the reason Axon needed a composite token must be in the decision.
- **`Progress.Highest` as a completeness watermark** ([[D-128]]) is per checkpoint row, so it stays
  true per partition and becomes **false across partitions**: partition 0 at position 900 and
  partition 1 at 1200 does not mean everything ≤ 900 was delivered by the projection as a whole. Any
  aggregated "how far is `orders` along" number is a `min` over partitions, and saying so is
  cheaper than a support ticket.

---

# §ES-03 — a dead-letter queue that preserves causal order

## 1. The mechanism

The guide's own statement of the problem, verbatim:

> "the default configuration for event processors is simply to log any errors that occur. In a
> production environment, developers have the option to modify the default behavior to throw the
> error instead. Please note, however, that this can cause an event processor to stop processing
> events altogether, due to the fact that it will keep trying to process the same event until it
> stops causing an exception.
>
> To prevent this behavior, it's possible to configure the use of a sequenced Dead Letter Queue
> (DLQ). With a DLQ, instead of retrying to process the event over and over unsuccessfully, the
> framework simply places the failed event in a queue. In order to maintain the correct order of
> such events, the framework also places all further events having the same sequence identifier
> *(by default, the sequence identifier is the aggregate id)* directly into the queue. Such an
> approach ensures that all events for the same aggregate must wait on the event that is causing an
> exception. As a result, the event processor doesn't consume resources continuously trying to
> process unprocessable events."

### The two-branch dispatch, from `DeadLetteringEventHandlerInvoker.handle`

```java
Object sequenceIdentifier = super.sequenceIdentifier(message);
boolean mightBePresent = mightBePresent(sequenceIdentifier, segment);
if (isPresent(mightBePresent, sequenceIdentifier, message)) {   // the sequence is already parked
    markEnqueued(sequenceIdentifier, segment);                   // park this one too, no handler call
} else {
    if (mightBePresent) { markNotEnqueued(sequenceIdentifier, segment); }
    invokeHandlers(message, segment, sequenceIdentifier);
}
```

and the failure branch:

```java
try { super.invokeHandlers(message); }
catch (Exception e) {
    DeadLetter<EventMessage<?>> letter = new GenericDeadLetter<>(sequenceIdentifier, message, e);
    EnqueueDecision<EventMessage<?>> decision = enqueuePolicy.decide(letter, e);
    if (decision.shouldEnqueue()) {
        markEnqueued(sequenceIdentifier, segment);
        queue.enqueue(sequenceIdentifier, decision.withDiagnostics(letter.withCause(cause)));
    }
}
```

### The queue contract

`SequencedDeadLetterQueue<M>`: `enqueue`, `enqueueIfPresent`, `evict`, `requeue`, `contains`,
`deadLetterSequence`, `deadLetters`, `isFull`, `size`, `sequenceSize`, `amountOfSequences`,
`process(Predicate, Function)`, `process(Function)`, `clear`. Its javadoc:

> "It is highly recommended to use the `process` operation (or any of its variants) to consume
> letters from the queue for retrying. This method ensure sequences cannot be concurrently accessed,
> thus protecting the user against handling messages out of order."
>
> `deadLetterSequence(id)` — "Return all the dead letters for the given `sequenceIdentifier` **in
> insert order**."
>
> `process(...)` — "Will pick the oldest available sequence based on the `DeadLetter#lastTouched()`
> field from every sequence's first entry. Note that only a *single* matching sequence is processed!
> Furthermore, only the first dead letter is validated, because it is the blocker for the
> processing of the rest of the sequence."

### The bound and the overflow

`JpaSequencedDeadLetterQueue`: `maxSequences = 1024`, `maxSequenceSize = 1024` (builder defaults,
each asserted `>= 128`).

```java
public boolean isFull(Object sequenceIdentifier) {
    long numberInSequence = sequenceSize(id);
    return numberInSequence > 0 ? numberInSequence >= maxSequenceSize : amountOfSequences() >= maxSequences;
}

public void enqueue(Object id, DeadLetter<? extends M> letter) throws DeadLetterQueueOverflowException {
    if (isFull(id)) { throw new DeadLetterQueueOverflowException("No room left to enqueue […] since the queue is full."); }
    …
    Long sequenceIndex = getNextIndexForSequence(id);   // per-sequence monotone index
    entityManager().persist(new DeadLetterEntry(processingGroup, id, sequenceIndex, entry, enqueuedAt, lastTouched, cause, diagnostics, …));
}
```

**`DeadLetterQueueOverflowException` is not caught in `invokeHandlers`.** It propagates out of
`handle()`, the unit of work rolls back, the token does not advance, and the processing group stops
making progress. That is exactly ES-03's *«overflow останавливает затронутую partition, не
пропускает событие»* — and it is achieved by *not writing a catch*, which is the sort of thing a
re-derivation gets wrong by being tidy.

### The redrive

```java
private boolean processLetterAndFollowing(JpaDeadLetter<M> firstDeadLetter, Function<…> processingTask) {
    JpaDeadLetter<M> deadLetter = firstDeadLetter;
    while (deadLetter != null) {
        EnqueueDecision<M> decision = processingTask.apply(deadLetter);
        if (!decision.shouldEnqueue()) {
            DeadLetterEntry next = findNextDeadLetter(deadLetter);   // by (sequenceIdentifier, index > previousIndex)
            if (next != null) { deadLetter = toLetter(next); claimDeadLetter(deadLetter); } else { deadLetter = null; }
            evict(oldLetter);
        } else {
            requeue(deadLetter, l -> decision.withDiagnostics(l).withCause(...));
            return false;                                            // stop at the first one that fails again
        }
    }
    return true;
}
```

Letter-level claiming is time-based, like the token claim:

```sql
UPDATE DeadLetterEntry dl SET dl.processingStarted = :time
 WHERE dl.deadLetterId = :id AND (dl.processingStarted IS NULL OR dl.processingStarted < :processingStartedLimit)
```

with `processingStartedLimit = now − claimDuration`, and sequence selection:

```sql
SELECT dl FROM DeadLetterEntry dl
 WHERE dl.processingGroup = :pg
   AND dl.sequenceIndex = (SELECT min(dl2.sequenceIndex) FROM DeadLetterEntry dl2
                            WHERE dl2.processingGroup = dl.processingGroup AND dl2.sequenceIdentifier = dl.sequenceIdentifier)
   AND (dl.processingStarted IS NULL OR dl.processingStarted < :processingStartedLimit)
 ORDER BY dl.lastTouched ASC
```

The `EnqueuePolicy` returns an `EnqueueDecision` built by `Decisions.enqueue(cause)`,
`Decisions.doNotEnqueue()`, `Decisions.evict()`, `Decisions.requeue(cause, modifier)`. The guide's
worked retry-counter policy:

```java
final int retries = (int) letter.diagnostics().getOrDefault("retries", -1);
if (retries < 5) { return Decisions.requeue(cause, l -> l.diagnostics().and("retries", retries + 1)); }
return Decisions.evict();
```

`DeadLetter` carries `message`, `cause: Optional<Cause>`, `enqueuedAt`, `lastTouched`,
`diagnostics: MetaData`.

## 2. The nuances that do not survive re-derivation

1. **The checkpoint keeps advancing over parked events.** `handle()` returns *normally* after
   `markEnqueued`. The token moves; the DLQ is the record of what was not applied. So "how far have
   we scanned" and "what has actually been applied" are two different facts, and the queue is the
   only place the second one lives. **ES-03 part 3 says exactly this** — *«parking и scan checkpoint
   атомарны; успешная applied-позиция считается отдельно»* — and vv currently has the count
   (`Progress.Quarantined`) but no *position*.
2. **The blocking test is on the sequence, not the event**, and the following events are parked
   *without ever reaching a handler*. That is the difference between a DLQ and a skip list: the
   handler is never given `OrderPaid` for an order whose `OrderCreated` failed.
3. **`enqueueIfPresent` is the ordering primitive and is defined in terms of `contains`.** Its
   default body is `if (!contains(id)) return false; enqueue(id, builder.get()); return true;` —
   i.e. the whole causal-order guarantee is one existence check per event on the hot path, which is
   why `SequenceIdentifierCache` exists (`mightBePresent`, per segment, bounded). Skipping the cache
   costs a DB round trip per event; skipping the *check* loses the guarantee.
4. **The redrive processes one sequence per call and rotates.** `processAny()` "rotates the sequence
   to try based on when it was last tried" — the `ORDER BY lastTouched ASC` above. A retry loop that
   always picks the same failing sequence starves every other one; a retry loop that picks randomly
   loses the fairness. And the retry is **not automatic**: "Although this enables the processing to
   continue in case of errors, it doesn't retry the failed events automatically." The recommended
   driver is a scheduled component with "a large interval to not stress the system too much"
   (`@Scheduled(fixedDelay = 30_000, initialDelay = 30_000)` in the guide's example).
5. **The overflow bound is two-dimensional and the check is `isFull(sequenceIdentifier)`, not
   `isFull()`.** A sequence that already exists is bounded by `maxSequenceSize`; a *new* sequence is
   bounded by `amountOfSequences() >= maxSequences`. So a queue with 1023 sequences of 1 letter each
   still accepts a 1024th letter into an existing sequence. ES-03 says *«sequences/bytes»* — a byte
   bound is *not* what Axon does, and a byte bound over an event payload is a different (and for vv,
   given `payload bytea` and `MaxPayload`, a computable) thing.
6. **Idempotency is a precondition, stated bluntly.** "Before configuring a `SequencedDeadLetterQueue`,
   validate that your event handling functions are idempotent. A processing group consists of
   several Event Handling Components, and some handlers may succeed while others fail; since a
   configured DLQ does not stall event handling, a failure in one component does not roll back
   others. Because dead-letter support is at the processing group level, dead-letter processing
   invokes *all* event handlers for that event within the group." And: "You cannot share a
   dead-letter queue between different processing groups." And: "There is no support for using a
   dead-letter queue for sagas" — because a saga's event ordering across associations is not
   expressible as one sequence identifier.
7. **A replay clears the queue**, conditionally:
   ```java
   public void performReset() {
       if (allowReset) { transactionManager.executeInTransaction(queue::clear); }
       super.performReset(null);
   }
   ```
   This is the ES-03 × ES-06 interaction and neither appendix mentions it. If a rebuild is going to
   re-derive the read model from scratch, the parked letters of the *old* run describe events the
   *new* run will meet again — keeping them is a lie and clearing them silently is a loss of the
   operator's only record of what was skipped.
8. **The letter claim is a second time-based claim with its own duration**, independent of the token
   claim. Two operators redriving at once do not process one sequence twice.

## 3. vv delta

| ES-03 asks | vv today |
|---|---|
| park the failing event | `event/projection/pass.go:245-267` `oneAtATime` + `Quarantines` sink (`classify.go`), reached by `OnPermanentFailure == Quarantine`. |
| **and the following events of the same sequence** | **Nothing.** `oneAtATime` re-delivers the page one envelope at a time in position order; an envelope that fails permanently is passed to the sink and the loop **continues to the next envelope of the same stream**. So `OrderPaid` *is* applied over an order whose `OrderCreated` was quarantined. This is the single largest gap in ES-03. |
| other sequences continue | Free — vv never blocks a stream in the first place. |
| «parking и scan checkpoint атомарны» | Held under `InUnit`: the sink is called inside the unit (`classify.go`, the `Quarantines` doc comment says why) and the save rides the same transaction. Under `AfterApply` it is not, and that asymmetry is stated. |
| «успешная applied-позиция считается отдельно» | Partly: `Progress.Quarantined` is a **count** (`event/checkpoint.go:26-32`) and its comment already says the right thing — *"Non-zero means that destination has holes, which is what keeps `Highest` from reading as a completeness claim."* There is no applied *position*, and no list of what was passed. |
| a bound queue, overflow stops the partition | **Nothing.** `Quarantines` is an application-supplied sink with no bound, no size question and no back-pressure. A sink that accepts everything turns a poison stream into a silently half-applied read model. |
| ordered redrive, `RetrySequence(reference)` | **Nothing.** There is no reference, no queue to read back, and no way to re-deliver a quarantined envelope. The framework's answer today is "the sink is yours". |
| state shows blocked/degraded rather than a false "caught up" | `PhaseFollowing` is published by `pass.go:80` whenever a read answers `!more` — **including when the last page quarantined half its envelopes.** `State.Progress.Quarantined` is non-zero, so the information is there, but `Phase` says `following` and `Ready()` (`projection.go:170-183`) answers nil. ES-03's *«не ложное "догнал историю"»* is a real complaint about the current tree. |
| classes of unreadable history | `event/errors.go` — `ErrUnknownType`, `ErrRevision`, `ErrPayload`, `ErrUpcast`, and `projection.Classify` (`classify.go:19-31`) makes exactly those plus `ErrUnrouted` permanent. |
| bounded pages | `Limits.MaxRead`, every read (`event/eventpg/read.go:317-327`). |

## 4. Conflicts with binding decisions

- **A DLQ table is not a second durable-intent table**, so [[D-118]] does not forbid it — the same
  argument EVENTSOURCE_REFERENCE W11 makes for an idempotency key ("a dedup claim is not a durable
  *intent*"). But note that under `InUnit` a DLQ write **is** a write inside the caller's
  transaction, which is what [[D-118]] says the outbox is; so a DLQ that lives in the application's
  own database is the framework's shape, and one that lives in the store's schema is a second
  opinion about where the caller's data goes. Which one owns the table is a decision, not a detail.
- **Blocking a sequence requires knowing what the sequence *is*** — i.e. ES-03 depends on ES-02's
  `SequenceBy(key)` even in a single-partition deployment. Without it the only available sequence
  identifier is `Envelope.Stream`, which is `SequentialPerAggregatePolicy` by another name and is
  the default Axon also chose. That is a defensible default and should be recorded as one rather
  than as the only possibility.
- **`Progress.Highest` must not be redefined.** [[D-128]] forbids restating it as "the highest
  position of the page". A second, lower "applied" position is a *new* field, not a re-reading of
  this one — and it is not a resume point either ([[D-129]]), so it may not be a `Cursor` and may not
  be turned into one.
- **A time-based letter claim** conflicts with nothing directly, but it is a clock in a store, and
  [[D-126]]'s posture ("the store chooses no isolation level", the store owns no policy) plus
  `Progress.At`'s "the consumer's own observation" say the duration belongs to the caller.

---

# §ES-04 — rebuilding a projection beside the running one

## 1. The mechanism

### Marten / Wolverine versioned projections

The blog post's whole mechanism is one property:

```csharp
public class IncidentProjection: SingleStreamProjection<Incident>
{
    public IncidentProjection()
    {
        // THIS is the magic sauce for side by side execution
        // in blue/green deployments
        ProjectionVersion = 2;
    }
}
```

> "use completely different database tables for the `Incident` projection version 1 and version 2"

and the deployment procedure, from `docs/events/projections/rebuilding.md`:

> 1. **Increment `ProjectionVersion`** on your projection class to create a new version that writes
>    to separate database tables from the previous version
> 2. **Use Async lifecycle** for the new version so it can "catch up" to the current event sequence
>    while the old version continues serving requests
> 3. **Deploy new nodes** ("green") running the updated code alongside existing nodes ("blue"). The
>    green nodes build the new projection version while blue nodes continue serving traffic
> 4. **Switch traffic** to the green nodes once the new projection has caught up

The three lifecycles it is set against: `Live` (folded in memory, strongly consistent), `Inline`
(written in the same transaction as the append, strongly consistent), `Async` (the daemon,
eventually consistent "but there's a technical wrinkle where Marten can 'fast forward' asynchronous
projections to still be strongly consistent on demand").

Rolling-deployment requirements, verbatim from the post:

> 1. "only use `FetchForWriting()` or `FetchLatest()` when you need strongly consistent access to
>    any kind of single stream projection"
> 2. "make every single newly revised projection run under the `Async` lifecycle"
> 3. `m.UseWolverineManagedEventSubscriptionDistribution = true`

Cleanup:

> "As 'blue' nodes are pulled offline, it's safe to drop the Marten table storage for the projection
> versions that are no longer used. Sorry, but at this point there's nothing built into the Critter
> Stack, but you can easily do that through PostgreSQL by itself with pure SQL."

### Rebuild-in-place, for contrast

`await daemon.RebuildProjectionAsync("Shop", CancellationToken.None)`. It tears the rows down first;
"after the projection's existing rows are torn down, the entire rebuild write path is
**insert-only** — the rebuild is authoritative by definition" (which is what makes the experimental
`RebuildWithBulkCopy = true` binary-COPY path possible). Cancellation has a stated contract:

> - "Cancelling an in-flight rebuild leaves the cell's `mt_event_progression` row in a consistent
>   state — either unchanged from before the rebuild or at the actual partial position the rebuild
>   reached. Never a torn, in-between state."
> - "A subsequent `RebuildProjectionAsync` on the same (projection, tenant) cell completes
>   successfully with no manual intervention — a rebuild always starts by resetting the cell, so a
>   cancelled rebuild can simply be retried."

Resource budget (9.13): `opts.Projections.MaxConcurrentEventLoadsPerDatabase = 4` and
`MaxConcurrentBatchWritesPerDatabase = 4`, which "caps how many agents may load pages of events" and
"commit their SQL concurrently" against one database; under `UseTenantPartitionedEvents` "the cap is
a per-database governor: when N pooled shard databases rebuild concurrently, each shard's peak
tracks the cap independently".

### The nearest thing to an atomic multi-row cutover in either source

Not Marten. Axon's `PooledStreamingEventProcessor.resetTokens`:

```java
public <R> void resetTokens(@Nonnull TrackingToken startPosition, R resetContext) {
    Assert.state(supportsReset(), () -> "The handlers assigned to this Processor do not support a reset.");
    Assert.state(!isRunning(),    () -> "The Processor must be shut down before triggering a reset.");
    transactionManager.executeInTransaction(() -> {
        int[] segments = tokenStore.fetchSegments(getName());
        TrackingToken[] tokens = Arrays.stream(segments)
                                       .mapToObj(segment -> tokenStore.fetchToken(getName(), segment))
                                       .toArray(TrackingToken[]::new);
        eventHandlerInvoker().performReset(resetContext);
        IntStream.range(0, tokens.length)
                 .forEach(i -> tokenStore.storeToken(
                         ReplayToken.createReplayToken(tokens[i], startPosition, resetContext), getName(), segments[i]));
    });
}
```

One transaction, every segment, processor stopped, every token claimed (`fetchToken` claims), and
the whole thing refused up front if any handler does not support a reset.

## 2. The nuances that do not survive re-derivation

1. **Neither source ships an atomic read-target switch.** Marten's cutover is a *load-balancer*
   switch between blue and green nodes; the tables are simply named differently and the application
   code on each node reads its own version. There is no `Activate(generation)`. **ES-04's *«переключение
   read target атомарно для заявленного набора таблиц»* is therefore vv's own requirement, with no
   reference implementation behind it.** The nearest technique in either source is Axon's
   one-transaction-over-all-rows reset above; the nearest technique in PostgreSQL is a transactional
   `ALTER … RENAME` / view swap, which is a `search_path` or a view indirection and is a real design
   choice, not an obvious one.
2. **Neither source ships a rollback.** The blog post "contains no discussion of rollback
   procedures". Marten's answer is implicit: the old version's tables are still there and its
   daemon is still running, so rolling back is "stop switching traffic". **ES-04's *«Rollback
   допустим, пока старое поколение поддерживается актуальным либо снова догнало историю»* is
   precisely the condition Marten leaves to the operator**, and stating it is the contribution.
3. **The overlap window is *named and accepted*, not closed.** From `rebuilding.md`:
   > "**Accepted overlap window** — `N` is snapshotted when the new version starts. If an old
   > version is still running and advances past `N` afterward, the new version can re-emit side
   > effects for that `(N, old_final]` overlap. Stop the old version before (or as) the new one
   > starts to avoid the window; fully coordinated drain-and-handoff is a separate concern."
   ES-04 part 3's *«Старый worker не пишет в новое поколение»* is the easy half of this. The hard
   half is ES-06's *«durable граница владения live effects не допускает двух отправителей»*, and
   Marten explicitly does **not** provide it.
4. **A rebuild is stricter than continuous execution, by default.** `RebuildErrors.SkipApplyErrors
   = false` vs `Errors.SkipApplyErrors = true`. Because: a rebuild that skipped would produce a
   table that cannot be compared to the live one, and comparison is the only evidence the cutover
   is safe. vv's analogue is `OnPermanentFailure`, and a rebuild that runs with `Quarantine` has the
   same defect.
5. **Skipping *unknown event types* is what makes blue/green possible at all**, and Marten says so:
   "Skipping unknown event types is important for 'blue/green' deployment of system changes where a
   new application version introduces an entirely new event type." vv's posture is the opposite —
   `ErrUnrouted` is **permanent** (`projection/classify.go:19-31`, [[D-131]]) and a covered family's
   unclaimed type halts the projection. During a blue/green window the *old* generation meets the
   *new* generation's event types and halts. **This is a live conflict between [[D-131]] and ES-04
   and it must be decided, not discovered.** ([[D-131]]'s design already has the escape:
   `projection.Ignore(router, family, types...)` takes a **name**, deliberately, "so a type this
   build declares no fact for can be named too" — so the old generation's router can be told, in the
   old build, to ignore a type it has never heard of. That is a deployment ordering requirement and
   belongs in writing.)
6. **A rebuild needs its own resource budget or it starves the live one**, and Marten's numbers are
   per *database*, not per projection — 4 concurrent event loads, 4 concurrent batch writes. vv has
   no equivalent: each `Projection` is its own runner reading through its own connection checkout
   (`event/eventpg/executor.go:86-104`), and N rebuilds is N × `MaxRead` rows in flight against
   whatever the pool allows. `Spec.Idle` and `Backoff` are the only throttles and neither is a
   concurrency cap.
7. **Cancelling a rebuild must be a defined state, not a killed goroutine.** Marten's contract —
   either unchanged or at the actual partial position, never torn, and retryable with no manual
   intervention — is exactly what vv's `Drain` (`projection.go:151-164`, "acknowledged between
   passes, so a shutdown never lands between a handler's write and the advance") already delivers
   *per pass*. What it does not deliver is the "reset the cell before rebuilding" half, because vv
   has no reset ([[D-129]], and the P3 plan's absent-surface table lists `Reset`/`SetCheckpoint`/
   `Rewind` as deliberately unexported).

## 3. vv delta

| ES-04 asks | vv today |
|---|---|
| a generation with its own data, checkpoints and claims | **A generation is already expressible and already proven**: `event/eventpg/rebuild_integration_test.go` `TestARebuiltProjectionDrainsBesideTheLiveOneAndTheRowsAgree` (UC-120) runs a second projection *name* over the same log into a second destination table, and asserts the two destinations hold the same rows. The comment states the design position: *"a rebuild is two projections over one log with two destinations and nothing between them: the framework offers no API by which the new one could reset, pause or read the live one's checkpoint"*. |
| catch-up to an agreed barrier | Nothing. The test asserts `new.Highest >= old.Highest` and says in its own comment that this "is a different claim — a statement about consumption and not about the rows". There is no barrier primitive, and ES-05 (the `Wait`) is phase 5. |
| an atomic read-target switch over a declared set of tables | Nothing, and no reference implementation to copy — see nuance 1. |
| the old worker must not write to the new generation | Free by construction: different names, different checkpoint rows, different destinations. Worth an explicit test rather than an inference. |
| rollback | Nothing beyond "the old generation is still running". |
| a separate resource budget for the rebuild | Nothing — see nuance 6. |
| `Rebuild(projection, newGeneration)` → check → `Activate(newGeneration)`; old generation dropped separately | Nothing. And note the P3 plan deliberately does **not** export `Reset`, `Rewind`, `Head`, `Tail`, `Lag`, `WaitUntilCaughtUp` — so a `Rebuild`/`Activate` API is new surface that must be argued against that list rather than added beside it. |
| full log walk | `event/reader.go:28-34` `Read` + `Reader.Next`; `Checkpoint.Fresh()` at the origin. |

## 4. Conflicts with binding decisions

- **`Activate` cannot be spelled as "move the new generation's checkpoint to the old one's
  position"** — [[D-129]] forbids a position→cursor function and forbids ordering two cursors. A
  barrier must therefore be expressed as *the store's own answer*, e.g. "the new generation's cursor
  equals a cursor the old generation also reached", or as a store-issued barrier token — which is
  ES-05's territory and is phase 5. **ES-04 cannot define readiness without borrowing from ES-05,
  and that dependency should be stated now rather than discovered mid-phase.**
- **[[D-131]] vs blue/green** — nuance 5 above. This is the one hard conflict in ES-04.
- **[[D-130]]'s "no `Resume`, `Retry` or `Clear` for a halt"** and the P3 absent-surface table's
  refusal of `Reset` mean a generation is created, never reset. A failed rebuild is a *new*
  generation, and the old failed one's rows are dropped by an operator. That is a coherent position
  and it must be written down, because "just reset it and try again" is what everybody will reach
  for.
- **The rebuild's read cost** — EVENTSOURCE_REFERENCE §Reject 3 already accepted that "every
  projection reads every event, so N projections cost N × the read traffic", and recorded the
  eventual answer ("a shared reader fanned out to N routers — not a filter on the store contract").
  A rebuild is exactly the case that makes N large and temporary. That reject entry is the ceiling
  ES-04's resource budget must live under.

---

# §ES-06 — a replay that does not resend emails or take payments

## 1. The mechanism

### Marten: side effects are a separate method, and it is not called during a rebuild

`RaiseSideEffects(IDocumentOperations operations, IEventSlice<TDoc> slice)` — overridable on
`SingleStreamProjection` and `MultiStreamProjection`; inside it you may `slice.AppendEvent(...)`,
`slice.AppendEvent(streamId, ...)`, `slice.PublishMessage(...)`, `operations.Store(...)`. The rule,
verbatim:

> "The `RaiseSideEffects()` method is only called during _continuous_ asynchronous projection
> execution, and will not be called during projection rebuilds or `Inline` projection usage **unless
> you explicitly enable this behavior**"

and three consequences the docs state and a re-derivation would miss:

> - "Events emitted during the side effect method are _not_ immediately applied to the current
>   projected document value by Marten"
> - "You _can_ alter the aggregate value or replace it yourself in this side effect method to reflect
>   new events, but the onus is on you the user to apply idempotent updates to the aggregate based on
>   these new events in the actual handlers for the new events when those events are handled by the
>   daemon in a later batch"
> - `opts.Events.EnableSideEffectsOnInlineProjections = true` is the opt-in, i.e. the suppression is
>   the default and the exception is named.

### The blue/green gate — the part that is actually hard

```csharp
public class TripProjection : SingleStreamProjection<Trip, Guid>
{
    public TripProjection()
    {
        Version = 3;
        // Only fire side effects for events the prior version (V2) never processed
        GateSideEffectsBehindPriorVersion = true;
    }
}
```

> "When the flag is on and a new version starts behind the highest prior version's persisted
> progression mark `N`, the daemon does the catch-up in two phases:
> 1. **Warm-up** — replay `(current, N]` in **Rebuild** mode, so `RaiseSideEffects()` is suppressed
>    while the new version's documents are brought up to the same point the prior version reached.
> 2. **Continuous** — hand off to normal continuous execution from `N`, so side effects fire only
>    for events past `N` — the ones the prior version never saw."

with four behavioural notes, all of them load-bearing:

> - "**Resume after interruption** — the trigger is *own progress `< N`*, not *own progress `== 0`*.
>   If the warm-up is interrupted (a crash or restart at `M < N`), the next start resumes the
>   suppressed replay over `(M, N]` rather than re-emitting side effects from the beginning."
> - "**Failed warm-up leaves the shard paused** — if the warm-up replay throws, the shard is left
>   `Paused` with the exception attached and continuous execution does **not** start (so no side
>   effects fire over history the prior version already covered)."
> - "**Incompatible with `SubscribeFromPresent`** — subscribing from 'present' deliberately ignores
>   persisted progression, so there is no prior mark to gate against. The gate is skipped and a
>   warning is logged."
> - "**No-ops when not needed** — the gate never adds a warm-up phase for `Version == 1`, when the
>   flag is off, or when the new version's own progress is already at/past the prior mark."
> - "**Accepted overlap window**" — quoted in full under ES-04 nuance 3.

### Axon: replay is a property of the *token*, not of a flag in memory

`ReplayToken` — *"Token keeping track of the position before a reset was triggered. This allows for
downstream components to detect messages that are redelivered as part of a replay."* It wraps
`tokenAtReset` (where the processor was when the reset happened) and the current position, and:

```java
public TrackingToken advancedTo(TrackingToken newToken) {
    if (tokenAtReset == null || isStrictlyAfter(newToken, tokenAtReset)) {
        // we're done replaying
        …
        return newToken;
    }
    …
}
```

The replay ends by itself, at the position it started from, and — because the token is what the
`TokenStore` persists — **"am I replaying" survives a crash, a restart and a redeploy.**

The declaration surface: `@AllowReplay` "can be used, situated either on an entire class or an
`@EventHandler` annotated method. It defines whether the processor should invoke the given class or
method when a replay is in transit"; `@DisallowReplay` is the negative; a handler may take a
`ReplayStatus` parameter (`REGULAR` or `REPLAY`) "for more fine-grained control on what (not) to do
during a replay"; `@ResetHandler` methods run before the replay starts and may take the
`ResetContext` payload passed to `resetTokens(R)`; `@ReplayContext` injects that payload into a
handler. The reset itself requires `supportsReset()` and `!isRunning()` (quoted under ES-04).

## 2. The nuances that do not survive re-derivation

1. **Replay-ness must be durable.** Axon puts it in the token; Marten puts it in the comparison
   `own progress < N` against a persisted mark. Both survive a restart. An in-memory "we are
   rebuilding" flag fires every side effect of the remaining history the first time the rebuild
   process is restarted, and that failure only shows up in production.
2. **The replay must end at a *recorded* position, not "when we catch up".** Axon: `tokenAtReset`,
   captured at the reset. Marten: `N`, "snapshotted when the new version starts". "Until lag is
   zero" is not a boundary — the log keeps growing, so it never terminates deterministically, and
   the first event past the boundary is exactly the one whose side effect must fire.
3. **Suppression is the default and the exception is opt-in with a name.** Marten:
   `EnableSideEffectsOnInlineProjections`. Axon: handlers are replayed unless annotated — the
   opposite default, and the reason Axon's guide carries the warning that "handlers performing side
   effects (such as sending emails) should exercise caution during replay". **Marten's default is
   the safe one and Axon's is not**; a framework choosing today should choose Marten's.
4. **The failure mode of a failed warm-up is `Paused`, not "carry on continuously".** If the
   warm-up throws, continuous execution must not start — otherwise the projection resumes at a
   position it never actually reached and fires effects for history the old version covered.
5. **Neither implementation closes the two-sender window.** Marten names it and tells you to stop
   the old version first. ES-06's *«durable граница владения live effects не допускает двух
   отправителей»* asks for the thing Marten calls "a separate concern". **The mechanism vv already
   owns for exactly this is the fenced checkpoint**: one row, one writer, ordinal, and [[D-133]]'s
   claim-before-handler order means the loser never runs the handler at all under `InUnit`. An
   effect-dispatch capability gated on *holding the fence in the committing transaction* is the
   durable ownership boundary neither source has. That is the strongest thing phase 4 could
   contribute here and it is a direct extension of shipped code.
6. **The "no arbitrary HTTP in a projection callback" rule is a contract, not a sandbox** —
   ES-06 part 3 says so explicitly and is right: Go cannot prevent a handler from dialling out. What
   can be done is what the appendix asks for: state it in the contract and **prove it with a test**
   over the framework's own code (no `net/http` in the effect-free path, no import that could
   dispatch), plus the shape that makes the honest thing easy — a separate effect handler with its
   own delivery identity.
7. **A reset clears the dead-letter queue** (`DeadLetteringEventHandlerInvoker.performReset`,
   quoted under ES-03 nuance 7). The rebuild/DLQ interaction is a decision.

## 3. vv delta

| ES-06 asks | vv today |
|---|---|
| a projection cannot append | **Held, structurally.** `event.ReadOnly` (`event/reader.go:15-23`) is "deliberately the one wrapper in this repository with no `Next`, and that is the point of the rule rather than a breach of it", and `projection.New` refuses a `Log` that is also an `event.Store` (`event/projection/spec.go:130-132`: *"a projector that can append is how a replay writes"*). |
| pure rehydration | `event/repo.go` folds; `event/chain.go` upcasts. |
| separate projection / effect handlers | **Nothing.** There is one `Handler` (`event/projection/page.go:19-21`) and one `Router`. |
| a rebuild does not get the effect-dispatch capability | **Nothing** — there is no capability to withhold, and no notion of "this run is a rebuild". A rebuild today is a second `Spec.Name` (UC-120), and the *only* thing distinguishing it from the live projection is which `Handler` the composition passed. |
| the initial backfill also has an explicit effect policy | Nothing. Note this is the same case: a *new* projection over a full log is a backfill, and `Checkpoint.Fresh()` (`event/checkpoint.go:59`) is where it starts. |
| a durable ownership boundary for live effects across a generation switch | Nothing named — but the fenced save is the mechanism (nuance 5). |
| «staged jobs/outbox» as the effect sink | [[D-118]] says a durable write inside the caller's bound transaction **is** the outbox, and `jobs.Stager` is the shipped one. |

## 4. Conflicts with binding decisions

- **[[D-130]] — "Do not import `jobs` from `event/projection`."** So the effect seam cannot be
  `jobs.Stager`; it must be an interface the *application* implements, with the composition root
  wiring `jobs` behind it — exactly the way `Quarantines` (`event/projection/classify.go`) is already
  shaped. This is not a problem, it is the answer, and it should be written down before somebody
  reaches for the convenient import.
- **[[D-118]]** forbids a second durable-intent table. An effect handler that stages inside the
  `InUnit` transaction is compliant; one that keeps its own "effects to send" table is not.
  EVENTSOURCE_REFERENCE §Reject 2 already records what must still be answered in its place: *"the
  reference's ordering guarantee (events of one aggregate reach the sink in version order), its
  retry, and its dead-letter behaviour are questions `jobs` must answer for a staged integration
  event, not questions that disappear."* ES-06 inherits all three.
- **`AfterApply` cannot carry the effect guarantee at all.** EVENTSOURCE_REFERENCE §Reject 2 again:
  "an `AfterApply` pass that stages a job outside a unit has exactly the reference's window". So
  ES-06's capability is `InUnit`-only, and saying so is part of the contract rather than a caveat.
- **[[D-092]] / `startsNothing`** — an effect dispatcher that "sends in the background" is a
  goroutine, and there are none under `event/`. Dispatch is either synchronous inside the handler
  (and therefore inside the unit) or it is a staged job some other runner drains.
- **[[D-062]]** — the effect path must log through `port.Logger(ctx)` or not at all;
  `docs/modules/en/projection.md:250` ("Nothing here writes a line") is a published property that an
  effect dispatcher must not quietly break.

---

# Cross-cutting: where these mechanisms meet EVENTSOURCE_REFERENCE's Reject entries

The reference file's three rejects and two "not adopted" notes all recur in these five appendices.
Re-proposing one wastes a section, so:

| Mechanism read this round | Already rejected as | What vv must prove instead |
|---|---|---|
| Axon's `Coordinator` + worker-executor thread pools; Marten's `MaxConcurrentEventLoadsPerDatabase` agents | **Reject 1** (`@Async` drain, [[D-092]]) — no library-started thread pool, and `startsNothing` forbids `go` under `event/` | N supervised runners, one per partition/generation name, each its own loop; one slow handler delays only its own name. ES-02 and ES-04 both need a *cap* on total concurrency, and the cap has to live in the host's supervisor or in the pool, not in `event/`. |
| Axon's DLQ table; Marten's `mt_doc_deadletterevent` | **Not** Reject 2 — a parked letter is not a durable *intent*. But under `InUnit` it is a write in the caller's transaction, so [[D-118]] decides *whose database it lives in* | that a park and its checkpoint commit together, and that the queue's owner is named. |
| Marten's `RaiseSideEffects` → `slice.PublishMessage` → Wolverine | **Reject 2** ([[D-118]]) — no second durable-intent table; `jobs.Stager` is the outbox | ordering per stream at the sink, retry, and dead-lettering — the three questions Reject 2 says "do not disappear". |
| `SELECT … FOR UPDATE SKIP LOCKED` on the subscription row (Axon does *not* do this; the PG reference did) | **Not adopted**, [[D-126]] | already proved: the fenced `UPDATE` under `InUnit` takes the row lock one step later, [[D-133]]. |
| Axon's `claimTimeout` / Marten's advisory-lock leader election | **Not adopted** — a store-chosen policy and a clock, [[D-126]] | that the ordinal fence produces take-turns rather than a stuck singleton — [[D-133]], proved live. |
| Marten's `AdvanceHighWaterMarkToLatestAsync`, `SkipStaleGapsDespiteLiveTransactionsAfter` | **Reject-adjacent**: a position→cursor operator command, [[D-129]] | the documented `xmin` stall and its PostgreSQL-level mitigations (`idle_in_transaction_session_timeout`, `statement_timeout`) — already closed by P3 S5. |
| A filter on the log read, to make N generations cheaper | **Reject 3** — a contract freeze, with the reference's own evidence against it | that N projections × N reads is affordable, or that the answer is one shared reader fanned out to N routers. ES-04 is the case that makes N spike. |
| The `(writer_xid, position)` tuple read | **Refused**, [[D-128]] §A1 | nothing new here — but note that ES-02's per-partition cursors and ES-04's per-generation cursors both multiply the number of independent watermark walks over one log, which multiplies the `xmin`-stall exposure. That is a new cost of an old accepted mechanism and belongs in the module page when either lands. |

---

# The three things a planner should decide before writing the phase-4 spec

Recorded here because each is a conflict between a source mechanism and a binding vv decision, and
none of them can be resolved by implementation.

1. **[[D-131]] vs blue/green (ES-04 nuance 5).** A covered family's unclaimed type is a permanent
   failure. During any generation switch the old generation meets the new one's event types.
   `projection.Ignore(router, family, "new.type")` in the *old build* is the escape and it is a
   deployment-ordering requirement, not a code change. Decide whether that is the answer, and write
   it beside [[D-131]].
2. **A merge of two partitions has no legal spelling under [[D-129]] (ES-02 §4).** Axon needs
   `MergedTrackingToken` and vv may not order cursors. Either partition counts only grow, or a merge
   is "rebuild the merged partition from the lower cursor and accept re-delivery to the ahead half",
   or a merge is refused. All three are defensible; leaving it unstated is not.
3. **ES-04's readiness barrier needs ES-05's vocabulary, and ES-05 is phase 5.** "The new generation
   has caught up" cannot be expressed with what exists: `Progress.Highest` is not a resume point,
   cursors do not order, and `WaitUntilCaughtUp` was deliberately not exported by phase 3. Either
   ES-04 ships with a weaker, honest readiness statement (e.g. "both generations' checkpoints carry
   the same cursor bytes, observed twice"), or a piece of ES-05 moves forward into phase 4 on
   purpose. Deciding this late means discovering it in S3 of the implementation.
