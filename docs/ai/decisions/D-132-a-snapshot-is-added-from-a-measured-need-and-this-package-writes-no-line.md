# D-132 — A snapshot is added from a measured need, not a measured cost, and this package writes no log line

**Status:** accepted
**Invariant:** Full replay is the only authority a folded state has: no snapshot,
no memo and no cache of one is declared anywhere under `event/`, and none appears
in the exported-surface baseline. What ships instead is the instrument —
`BenchmarkStreamReplay` — and a re-entry trigger a deployment measures for
itself. Separately: `event/projection` emits no log line, no span and no metric,
and everything an operator needs travels through `State`, the `Observer` and
`Ready`.

## The measurement, before the argument

Driven against PostgreSQL 17.9, on the deployed `eventpg` defaults (`StreamPage`
256, `MaxRead` 256), 120-byte payloads, five passes after a warm-up, `-race` off,
one connection, ~0.1 ms round trip:

| Aggregate | Full replay, no-op codec | Full replay, `event.JSON` | Read past the end | Raw one-statement scan |
|---|---|---|---|---|
| 10 000 events (40 pages) | 10.19 – 10.93 ms | 16.25 – 17.72 ms | 0.062 – 0.067 ms | 5.67 – 7.05 ms |
| 100 000 events (391 pages) | 104.4 – 105.7 ms | 163.4 – 170.6 ms | 0.062 – 0.067 ms | 86.5 – 87.9 ms |

**One figure carried in from the specification was optimistic and is corrected
here rather than repeated.** The claim was that the single-statement scan is
92 – 94 % of the paged replay, so "391 pages add about 6 ms". Measured on this
host it is **83 %**: 87.3 ms against 104.9 ms, so the 391 pages cost about
**17.6 ms**, roughly 45 µs a page. The conclusion is unchanged — paging is a
sixth of the replay and not its bulk — but the number is the one a deployment
multiplies, because on a 1 ms link that same term becomes about 390 ms. The raw
scan builds no envelopes and runs no fold, which accounts for part of the gap and
is said so the comparison is not read as an A/B it is not.

## The decision

**A snapshot is not added because a replay costs something.** 10 – 18 ms for a
10 000-event aggregate is inside the budget of a single request. An aggregate
that reaches 100 000 events has a modelling problem — a consistency boundary
drawn too wide — and a snapshot would hide it rather than fix it.

**What it costs is correctness, not code.** A snapshot is a second answer to the
question §INV-008 says has exactly one, and every part of it is a way to serve a
wrong state silently: a stale one, a corrupt one, one whose revision this build
cannot read, and — the one this framework structurally cannot detect — one taken
with a fold that has since changed. The reference implementation adjudicated for
this phase has no schema version on its snapshot table at all, which for a
long-lived deployment means renaming a field deserializes old snapshots with that
field silently null while the tail events replayed on top do not restore it.

**The gate is a measured *need*, not a measured *cost*.** There is no consumer.

**So the deferral gets an instrument and an owner rather than a hope.**
`BenchmarkStreamReplay` (`event/eventpg`) replays a stream of a caller-chosen
size against a live database and reports ns/event, so the trigger below is
something a deployment measures in its own environment rather than a number
argued in this repository.

**The recorded re-entry trigger:** a snapshot is built when a deployment measures
its own p99 aggregate replay above **~50 ms in its own environment** — at these
rates somewhere above 30 000 – 50 000 events, or an order of magnitude fewer over
a 1 ms link. The measurement is the deployment's, the instrument is the
benchmark, and both this file and `docs/roadmaps/Roadmap.md` carry the trigger so
the deferral has an owner.

**No memo and no cache is added instead.** §INV-042 already forbids the kernel
retaining an application value across a call boundary, and a per-request cache is
a snapshot with no version, no invalidation rule and no corruption story — the
same second authority with none of the four things that make one safe.

**What the later phase must not re-derive** is recorded in
`.agents/artifacts/gaps/EVENTSOURCE_BACKLOG.md`, `## From the reference` §6, in
full: the snapshot is written **inside** the append transaction, the cadence is
`finalVersion % N == 0` with `N >= 2`, a load at a version takes the newest
snapshot **at or below** it, snapshots are versioned and never upcast but deleted
on drift, and the reference's own latent bug — a snapshot `INSERT` inside the
per-event append loop testing the aggregate's final version — is not copied.

## And this package writes no log line

`event/projection` emits nothing. Its first-party closure is `crud`, `errs`,
`runtime` and `utils`, which is exactly what `scripts/event_test.go` charges it,
and that is a fact about the dependency graph rather than a style preference.

Two alternatives were available and both are refused.

- **`port.Logger(ctx)`** is [[D-062]]'s own answer and would be the right one if
  a line were wanted. It is refused because it widens that `charged` row from one
  package pattern to a list — changing the shape of the check for one more
  spelling of what `Ready` and `Observer` already carry.
- **`Spec.Logger *slog.Logger`** needs no import at all and is refused because it
  makes a package that publishes a typed `State` publish an untyped duplicate of
  it. A composition root that wants lines writes a five-line `ObserverFunc`.

**The cost is stated rather than hidden**, because "this package deliberately
emits nothing" is exactly the kind of claim the first person debugging a halt
quietly reverses: a deployment that supplies no `Observer` learns a handler
panicked only from `Ready`, and learns nothing at all of a retry streak below
`Tolerate`.

## What it forbids

- Do not declare a snapshot type, field, column or function under `event/`, and
  do not publish one in `docs/api/surface.md`.
- Do not add a memo or a cache of a folded state instead.
- Do not implement a snapshot without the five constraints recorded in the
  backlog, and do not read the reference's snapshot code for the shape.
- Do not call a logger from `event/projection`, and do not add a `Logger` field
  to its spec.
- Do not remove the sentence saying what the silence costs.

## Where it lives

- `event/repo.go` — `Repo.Load`, the full replay that is the only authority.
- `event/eventpg/replay_integration_test.go` — `BenchmarkStreamReplay`, the
  instrument.
- `event/projection/state.go` — `State`, `Observer`, `ObserverFunc` and
  `observing`, which recovers a panicking observer.
- `event/projection/projection.go` — `Ready`, the readiness half.
- `docs/roadmaps/Roadmap.md` — the trigger, beside the item it defers.

## Proven by

- `TestNoSnapshotAuthorityIsDeclaredOrPromised` — every identifier the extension
  defines and every line of the surface baseline under it, with a fixture
  declaring one that must be reported.
- `TestNoPackageLevelStateIsEverMutated` — the §INV-008 walk, which reaches
  `event/projection` because it walks every directory of the extension rather
  than a list.
- `TestAStreamWithHistoryFoldsToItsCurrentState` and
  `TestAFoldIsPureOverTheStateItIsGiven` — replay is the authority and is pure.
- `TestTheReplayBenchmarkMeasuresTwoOrdersApart` (`event/eventpg`, live) — the
  instrument measures what it says it measures.
- `TestTheProjectionStartsNothingAndReadsNoEnvironment` — the package writes
  nothing and reads nothing on import.
- `TestAHaltedProjectionIssuesNothingAndReportsThroughReady` — what an operator
  gets instead of a line.

## See also

**[[D-145]] is the contract this file's re-entry trigger opens into**, and it
**amends** this decision rather than superseding it: the invariant above stands
verbatim, `TestNoSnapshotAuthorityIsDeclaredOrPromised` stays green and
un-narrowed, and nothing here is loosened. What D-145 adds is the design a later
phase implements once the trigger is met — the five bindings compared before
deserialisation, the state-computation version this file already names as the one
thing this framework structurally cannot detect, the observable fallback, and the
two gates. It also re-measured the instrument on 2026-09-12 and found the crossing
at ~45 000 events, inside the 30 000 – 50 000 recorded above, so **the trigger
does not move**.

[[D-062]] [[D-091]] [[D-130]] [[D-145]] [[FL-038]] [[UC-032]]
