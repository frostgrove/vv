# FL-038 — A settled cursor becomes a durable checkpoint

**Entry points:** `projection.New` (the composition root), `projection.Projection.Run`
(the loop), `event.Track` (the door onto a checkpoint store),
`eventmemory.NewCheckpoints` / `eventpg.NewCheckpoints` (the two shipped stores),
`eventtest.RunCheckpoints` (a third implementation's proof)
**Governed by:** [[D-092]] [[D-118]] [[D-126]] [[D-128]] [[D-129]] [[D-130]]
[[D-131]] [[D-132]] [[D-133]]

What happens between a page the log answered and a row in a checkpoint table that
says a consumer finished with it — the fence that admits one save at a time, the
unit of work the advance rides in, the retry that re-applies the page it holds,
the isolation pass that buys envelope granularity, and the bounded resolution
that reads an unconfirmed or refused save off the row rather than guessing at it.

[[FL-036]] is the half below this one: the declaration, the append, the log walk
and the six classes a refusal belongs to. [[FL-037]] is where the fourth table
lives on PostgreSQL. This flow starts where a `*event.Reader` has a page.

`event/projection` is a package of the **root module**. It reaches `crud` for the
executor question `InUnit` asks, `errs` through the vocabulary, and `runtime` for
the runner contract — and nothing else. `scripts/event_test.go` is what holds
that.

## Construction, and what New refuses

`event/projection/spec.go`:

1. `New(spec)` calls `event.Track(spec.Checkpoints, spec.Name)` and refuses a nil
   `Log`, `Handler` or `Checkpoints`; a `Log` that is also an `event.Store`, so a
   projector cannot assert its way back to the append surface; an `Advance`
   outside the enum; `InUnit` with a nil `Unit`, with a checkpoint store whose
   `Transactions` is not `Supported`, or with `Destination` unstated;
   a `Unit` supplied beside `AfterApply`; `Quarantine` as a policy with no sink;
   a backoff that shrinks; and a negative `Idle`, `Attempts` or `Tolerate`.
2. **Every refusal wraps `ErrSpec` and they are collected rather than reported
   one at a time**, because a spec assembled wrong is usually assembled wrong in
   more than one place.
3. Defaults are each the one that promises less: `AfterApply`, `Idle` 1 s,
   `Backoff` `{250 ms, 30 s}` doubling **without jitter** (a projection is a
   singleton per name, so there is no herd), `Attempts` 10, `Tolerate` 1 min,
   `Classify`, `runtime.SystemTicks`, `Halt`. `Destination` has no default under
   `InUnit`: `projection.Unchecked` is how a composition says out loud that it
   cannot be resolved, and it is not reachable by leaving a field zero.
4. `New` performs no I/O, starts nothing and reads no environment. The first
   `Tracker.Load` and the first `Reader.Next` both happen on the first pass of
   `Run`.

## The door onto a checkpoint store

`event/checkpoint.go`:

1. `Track(checkpoints, projection)` refuses a nil store with `ErrWrongStore` and
   a name the kernel's identifier rule refuses with `ErrDeclaration`. Nothing
   calls a `Checkpoints` raw, so a second consumer inherits the re-checks rather
   than re-deriving them.
2. `Tracker.Load` re-checks five things about the answer, in a fixed order, each
   `ErrWrongStore`: **absence is total** (an advance of zero beside a cursor, a
   name or a progress is a store that did not answer the question); **presence is
   total in the one field that resumes** (the empty cursor *is* the origin of a
   log, so a row carrying one at a live advance restarts a consumer at the
   beginning against a live destination with no error anywhere); the answer is
   about **the name that was asked for**; the cursor is within `MaxCursorBytes`;
   and the row has not moved outside this tracker's own window.
3. The window is two numbers rather than one. `advance` is the fence — a save
   presents one above it — and `floor` is the lowest advance the row can still be
   at, which is the last one this tracker watched a store commit for itself. They
   part when a save leaves the row unresolved (the store did not say whether it
   wrote) or staged (it wrote inside a transaction this tracker does not commit).
4. `Tracker.Save` presents `advance + 1`, so `Checkpoint.Advance` is a field the
   store reads rather than one a caller computes, and it **answers the number it
   presented** whatever the store said — a caller that re-derived it would hold a
   second copy of one fence. It refuses a save before a load, a save over an
   unresolved one, the empty cursor, and a cursor over the ceiling.
5. `Tracker.Load` enters at the **read** door and `Save`/`Forget` at the
   **append** door, which decides one thing: an unclassified failure is
   `ErrBackend` at the first (nothing was written) and `ErrUncertain` at the
   second (a write whose fate is unknown).
6. `Forget` does not reset the fence, and neither does the load that follows it.
   Retiring a live projection's row is meant to be visible.

## One pass

`event/projection/pass.go`, `event/projection/projection.go`:

1. **Resume**, once: `Tracker.Load`, then `event.Read(log, held.Cursor)`. The
   reader is held for the loop's life and the cumulative counts are seeded from
   the row, because `Applied` and `Quarantined` are persisted columns and a
   dashboard that resets on every deploy is one nobody can read.
2. **Read**, outside every unit of work. An empty page is `PhaseFollowing`: the
   cursor stays in the reader, **nothing is saved**, and the loop waits on the
   ticker, on `Spec.Wake` or on the context. A non-empty page is `PhaseDraining`,
   and the page and its cursor are captured together — a retry re-applies the
   page it holds and issues no read.
3. **Deliver.** Outside a unit the order is the delivery guarantee itself: handle
   first, advance last. Inside one, `Spec.Unit` is called and the body presents
   the advance **before** the handler runs ([[D-133]]) — the two writes commit
   together, so their order is the lock manager's business alone, and a fenced
   save takes the checkpoint row's lock.
4. **`InUnit`'s two per-pass checks** run inside the unit and before the handler:
   `Tracker.Transaction(ctx)` must answer a valid authority, and — unless
   `Destination` is `Unchecked` — `crud.ExecutorFor` must find an executor
   `crud.IsTransaction` accepts. Either failing rolls the unit back and halts
   **without calling the classifier**.
5. **`Progress` is built at the save and nowhere else**, so it describes a page
   that was applied or quarantined rather than one that was merely delivered:
   `{Highest: the page's last position, Applied: seeded + this page's envelopes,
   Quarantined: seeded + what the sink took, At: now}`.
6. **The isolation pass** is how quarantine buys envelope granularity without a
   second handler signature: the page is delivered again one envelope to a
   `Batch`, in position order; one that applies is applied; one whose failure the
   classifier calls permanent goes to the sink with its cause and is passed; a
   **retryable** failure ends the pass and returns the whole page to retrying
   under the same attempt budget. Under `InUnit` the whole isolation pass is one
   unit, the sink included — a sink called outside it is the one write that
   survives the rollback of the advance.
7. **Each attempt is handed its own page.** `event/projection/page.go:copyOf`
   takes a fresh slice and a fresh copy of every payload, the first attempt
   included, and keeps the log's own page untouched beside them. It is the
   eighth hand-off of §INV-021 and the first whose sender re-reads what it handed
   over.

## The settlement, and the four outcomes it tells apart

`event/projection/pass.go:settle`:

A save whose fate its own answer did not carry — `ErrUncertain`, `ErrConflict`,
or a caller's unit answering an error after the save inside it returned nil — is
**held** rather than resolved on the spot, so a backend that goes away during the
resolution is retried *as* the resolution. One `Tracker.Load`, through a tracker
of its own, and the advance **and the cursor** the row carries decide:

| The row | What it means | What the loop does |
|---|---|---|
| at the presented advance, save refused | another instance got there first | take the row, rebuild the reader from **its** cursor, back off |
| above the presented advance | another instance is several passes past it | the same |
| at the presented advance, unconfirmed, carrying a cursor this pass never presented | this pass's save did not land; another instance reached that advance | the same |
| at the presented advance, unconfirmed, carrying this pass's own cursor | this pass's save landed | continue |
| one below, `InUnit`, unconfirmed | the whole unit rolled back | re-apply the page it holds |
| one below, save refused | the store refused a save over the very row its own fence admits | halt |
| absent, or behind the fence | the name was forgotten, reset or restored under a running projection | halt |

`event/projection/pass.go:anothers` is the cursor comparison, and it is what
keeps a settlement from attributing another instance's row to itself — which
would lose every event between the two cursors, permanently and silently.

## The two shipped checkpoint stores

`event/eventmemory/checkpoints.go` keeps its rows **on the `*Log`**, so two
`Checkpoints` values over one log are one checkpoint store, exactly as two
`Store` values over one log are one store. A save inside a `Tx` is staged in a
second staging area beside the appends, revalidated against the fence at commit,
and discarded by a rollback — and `Rollback`'s position arithmetic reads only the
first area, because a checkpoint draws no position.

`event/eventpg/checkpoints.go` is the fourth table of the same schema and a
resource of its own: a `Store` and a `Checkpoints` over one schema are two
resources at one schema version, so a deployment migrates once and each verifies
at its own `Prepare`. Its save is **two statements of which exactly one is
issued** — `INSERT … ON CONFLICT DO NOTHING` at advance 1, `UPDATE … WHERE
projection = $1 AND advance = $3 - 1` above it — because a single
`INSERT … ON CONFLICT` asks whether the row is there against its own snapshot and
which row it collides with against the live index, so a `DELETE` committing
between those two moments would bring a retired row back at an advance no first
save ever created.

## Where the decisions bite

- **[[D-128]] — the log delivers in position order.** `Progress.Highest` is a
  completeness watermark and not the page's last position by coincidence, and
  `presentSave` fills it from the last envelope of the page **because** of that
  law.
- **[[D-129]] — a checkpoint is a cursor.** There is no position on a
  `Checkpoint`, no function from one to a cursor, and no ordering of two cursors.
- **[[D-130]] — the framework opens no transaction and starts no goroutine.**
  `Spec.Unit` is the caller's, runs the work exactly once, and the read is
  outside it.
- **[[D-131]] — a forgotten route halts.** Inside a covered family an unrouted
  type is `ErrUnrouted`; outside every covered family it is skipped and counted.
- **[[D-132]] — no snapshot, and no log line.** `State`, the `Observer` and
  `Ready` are the whole of what reaches an operator.
- **[[D-133]] — a lost fence is a turn and not a halt.** The row the winner left
  is adopted, the reader is rebuilt from that row's cursor, and the streak only a
  landed save clears is what makes `Ready` report `ErrOvertaken`.
- **[[D-092]] — nothing starts in a constructor.** `New` starts nothing, `Run`
  blocks on the caller's goroutine, and the package holds no `go` statement.
- **[[D-118]] / [[D-126]] — the store joins a transaction it did not open.** A
  checkpoint store on a bound transaction of its own source writes inside it; one
  on an ambient executor that is **not** a transaction refuses before any
  statement.

## Traps

- **A walk inside the projection's own write transaction settles no gap.** This
  is why the read is outside every unit, and a projection that opened the unit
  first would deadlock its own progress on the first rolled-back append in the
  log — in production, with every test over a gapless log green.
- **`InUnit` promises atomicity only for writes made through the context the
  unit gave the handler.** A read model in a second database is outside it, no
  check can see that, and `Destination: projection.Unchecked` is where a
  composition says so.
- **A closed `Spec.Wake` channel is permanently ready.** The loop stops waiting
  on it and follows on `Idle` alone; without that guard a closed channel is an
  unbounded read of the log.
- **A halt is terminal for that value's life.** There is no `Resume`, `Retry` or
  `Clear`: the exit is a new value in a new process.
- **`Placement: Singleton` is a promise to the deployment rather than an
  enforcement.** The checkpoint's fence is what actually holds when two
  instances run, and under `AfterApply` both apply the overlapping page.
- **A new projection reads the whole log.** Adding one to a live deployment
  replays every event ever written through its handler, a page at a time.
- **`event/` outside `event/eventpg` is frozen.** `make check-event-kernel`
  compares the tree against `scripts/event_kernel.sha256`, and a deliberate move
  is recorded with `make check-event-kernel-baseline` in the same change as the
  code.

## Files

| File | What it holds |
|---|---|
| `event/checkpoint.go` | `Progress`, `Progress.zero`, `Checkpoint`, `Checkpoint.Fresh`, `CheckpointCapabilities`, `Checkpoints`, `Track`, `Tracker` and its `Projection`, `Capabilities`, `Backing`, `Transaction`, `Load`, `admit`, `established`, `autocommits`, `Save`, `Forget` — the contract, the door and the fence, and the window that is two numbers rather than one |
| `event/bounds.go` | `MaxCursorBytes` — the seventh ceiling, and the one that bounds what a store **mints** rather than what it accepts |
| `event/reader.go` | `Reader.checkPage`, which takes the cursor beside the page: an over-ceiling cursor and an empty one beside a non-empty page are `ErrBackend`, and the ascending arm is one half of [[D-128]] |
| `event/fact.go` | `Fact.Family`, `Fact.Read` — the typed reading seam a route closes over, decoding through the fact's own chain and every declared upcaster |
| `event/eventmemory/checkpoints.go` | `CheckpointSpec`, `Checkpoints`, `NewCheckpoints`, its `Capabilities`, `Backing`, `Transaction`, `Begin`, `Load`, `Save`, `Forget`, `Close`, `held`, `refusable` — the rows live on the `*Log` |
| `event/eventmemory/transaction.go` | `stageSave`, `revalidateSaves`, `checkpointHeld` — the second staging area, and why `Rollback`'s position arithmetic reads only the first |
| `event/eventpg/checkpoints.go` | `CheckpointSpec`, `Checkpoints`, `NewCheckpoints`, `Prepare`, `Check`, `Transaction`, `opened`, `Load`, `Save`, `Forget`, `on`, `promisedCheckpoint`, `refusable`, `loadStatement`, `saveStatement`, `forgetStatement` — the fourth table, and the two fenced statements its save is |
| `event/eventtest/checkpoints.go` | `CheckpointFactory`, `RunCheckpoints`, `tracking`, `checkpoints`, `admitCheckpoints`, `admitInstant`, `missingCheckpointHook`, `checkpointName`, `sameCheckpoint` — the runner a third implementation is proved by, under the same three anti-vacuity rules the store suite uses |
| `event/eventtest/sections_checkpoints.go` | `checkpointInventory`, `needsCheckpointTransactions`, `needsCheckpointPersistence`, `cursorOfWidth`, `firstDifference`, `forgetsInAUnit`, `forgetRacingASave` and the twelve section bodies |
| `event/eventtest/defects_checkpoints.go` | `checkpointDefect`, `checkpointDefects`, `unfenced`, `stale`, `absent`, `oneName`, `detaching` — the five broken stores the runner is falsified with |
| `event/projection/doc.go` | the package sentence: at least once in both modes, one name is one writer, there is no head, and nothing here writes a line |
| `event/projection/errors.go` | `ErrSpec`, `ErrHalted`, `ErrOvertaken`, `ErrUnrouted` — four, none of which crosses a store seam |
| `event/projection/spec.go` | `Advance` and its three values, `Advance.Valid`, `Advance.String`, `Backoff`, `Spec`, `unchecked`, `Unchecked`, `New`, `namedRefusal`, `refusedAdvance`, `refusedNumbers`, `withDefaults`, `appends`, `absent` — the whole refusal set, collected rather than reported one at a time |
| `event/projection/page.go` | `Batch`, `Handler`, `HandlerFunc`, `copyOf` — the page per attempt, which is §INV-021's eighth hand-off |
| `event/projection/classify.go` | `Verdict`, `Retryable`, `Permanent`, `Classifier`, `Classify`, `Failure`, `Halt`, `Quarantine`, `Quarantined`, `Quarantines` — the history class and `ErrUnrouted` are permanent and everything else is retryable |
| `event/projection/state.go` | `Phase` and its five values, `State`, `Observer`, `ObserverFunc`, `observing`, `Projection.State`, `Projection.transition`, `Projection.progressed`, `Projection.seed`, `Projection.publish` — published on a change and never on every pass, and a panicking observer does not take the loop down |
| `event/projection/router.go` | `Foreign`, `SkipForeign`, `RefuseForeign`, `routeKey`, `Router`, `NewRouter`, `On`, `TryOn`, `Ignore`, `TryIgnore`, `Router.declare`, `Router.ignore`, `Router.unclaimed`, `Router.Apply`, `Router.claims`, `Router.foreignTo`, `Router.unrouted`, `Router.seal`, `Router.Skipped`, `refusedName` |
| `event/projection/projection.go` | `Projection`, `newProjection`, `Projection.Name`, `Projection.Declaration`, `Projection.Run`, `Projection.Drain`, `Projection.Ready`, `Projection.until`, `Projection.follow`, `Projection.backoff`, `Projection.delay`, `Projection.acknowledge`, `Projection.stop` — the loop, and the six properties of it that are load-bearing and invisible from its shape |
| `event/projection/pass.go` | `step` and its four values, `Projection.once`, `Projection.resume`, `Projection.read`, `tally`, `applier`, `Projection.deliver`, `Projection.outsideAUnit`, `Projection.insideAUnit`, `Projection.claimed`, `Projection.wholePage`, `Projection.oneAtATime`, `Projection.quarantine`, `Projection.applyPage`, `Projection.presentSave`, `Projection.applyFailed`, `Projection.permanent`, `Projection.saveFailed`, `pending`, `Projection.awaiting`, `Projection.settle`, `fenced`, `Projection.anothers`, `answered`, `Projection.confirmed`, `Projection.rolledBack`, `Projection.overtaken`, `Projection.refused`, `Projection.haltedBy`, `Projection.redeliver`, `Projection.postpone`, `Projection.landed`, `Projection.settled`, `Projection.checkUnit`, `panicked`, `asPanic`, `stopping` — one pass, the two modes, the retry, the isolation pass and the settlement |
| `scripts/projection_test.go` | the five surface, AST and comment walks four invariants name: no exported function from a position or a progress to a cursor, no ordering of a cursor, no comment promising exactly-once delivery, and no snapshot declared or published |
| `scripts/event_test.go` | the `projection` row of `charged`: this package costs the vocabulary plus `runtime` and nothing else |
| `_examples/event-checkpoints-elsewhere/main.go` | a complete `event.Checkpoints` over a database this framework ships no store for, and the `InUnit` wiring that is accepted and cannot be checked |

Every non-test `.go` file under `event/projection/` has a row above, `doc.go`
included: the reverse index in `docs/ai/flows/Index.md` is what an agent reads
before editing a file, and a file with no row there reads as a file outside every
flow.

## Tests that walk this flow

Untagged, in `make unit`: `event/checkpoint_test.go` and `event/tracker_test.go`
(the door, the fence and the window), `event/fact_test.go` (the typed reading
seam), `event/reader_test.go` (the page and the cursor checked together),
`event/eventmemory/checkpoints_test.go` and `event/eventmemory/transaction_test.go`
(the memory store and its staging), `event/eventtest/checkpoints_test.go` (the
runner's own falsification), and the eight files of `event/projection`.

Behind `//go:build integration`, against a live PostgreSQL:
`event/eventpg/checkpoints_integration_test.go`,
`event/eventpg/projection_integration_test.go`,
`event/eventpg/projectioncase_integration_test.go`,
`event/eventpg/router_integration_test.go`,
`event/eventpg/rebuild_integration_test.go` and
`event/eventpg/replay_integration_test.go`. The gate names its own command:

```sh
FROSTGROVE_EVENTPG_TEST_DSN='postgres://vv:vv@localhost:55432/vv?sslmode=disable' \
  go test -race -count=1 -tags=integration ./event/eventpg/
```

### Proved by

| What holds | Proved by |
|---|---|
| the door refuses what it cannot hold, and absence and presence are total | `TestTrackRefusesWhatItCannotHold`, `TestAbsenceIsTotalAndProgressIsComparedFieldByField`, `TestPresenceIsTotalAndAnEmptyCursorIsNeverAResumePoint` |
| the fence is the door's, a save before a load is refused, and a load never re-seats a fence this tracker established | `TestAFreshTrackerAnswersTheZeroCheckpointAndSavesAtAdvanceOne`, `TestASaveBeforeALoadIsRefused`, `TestALoadNeverReSeatsAFenceThisTrackerEstablished` |
| each checkpoint door maps its unclassified failure its own way | `TestEachCheckpointDoorMapsItsUnclassifiedFailureItsOwnWay` |
| the reader checks the cursor beside the page | `TestAReaderRefusesACursorOverTheCeiling`, `TestAReaderRefusesAnEmptyCursorBesideANonEmptyPage`, `TestAConsumerReadsThroughPagesTheStorePublished` |
| a fact reads a stored envelope through its own chain and refuses the four history classes | `TestFactReadDecodesThroughItsOwnChainAndRefusesTheFourHistoryClasses` |
| both shipped checkpoint stores satisfy the contract, and the memory one's rows are the log's | `TestTheCheckpointStoreSatisfiesTheContract`, `TestTwoCheckpointValuesOverOneLogAreOneStore`, `TestARolledBackUnitStagedBothAndBurntOnlyTheAppends` |
| the conformance runner detects every defect it was built to detect, and declines rather than passes what it cannot certify | `TestEveryCheckpointDefectIsReportedByItsOwnSection`, `TestAStoreThatDoesNotPersistDeclinesDurabilityAndCertifiesTheOtherEleven`, `TestACheckpointFactoryClaimingPersistenceWithNoSiblingFailsBeforeASectionRuns` |
| a spec assembled wrong is refused whole, and `InUnit` is never downgraded | `TestNewRefusesEverySpecItCannotAssemble`, `TestInUnitIsRefusedAndNeverDowngraded`, `TestASpecCarryingAStoreIsRefusedAndReadOnlyIsAccepted` |
| the loop drains, follows, and issues no write while it is idle | `TestADrainingProjectionReachesFollowingAndStaysThere`, `TestNIdlePollsIssueZeroSavesAndNRoundTrips`, `TestAFirstRunStartsAtTheOriginAndASecondResumes` |
| the read is outside every unit, and the advance is claimed before the handler only inside one | `TestEveryReadArrivesOutsideEveryUnit`, `TestTheAdvanceIsClaimedBeforeTheHandlerInsideAUnitAndAfterItOutside`, `TestTheAdvanceAndTheHandlersRowsCommitTogetherInMemory` |
| a unit runs the work once, and a second run re-delivers rather than halts | `TestAUnitThatRunsTheWorkTwiceIsRefusedAndThePageIsRedelivered` |
| a retry re-applies the page it holds, in memory of its own | `TestARetryReAppliesTheLogsOwnPageAfterABackoff`, `TestARedeliveryCarriesTheSameIdentitiesInMemoryOfItsOwn` |
| a permanent failure halts, and quarantine is envelope-granular | `TestAPermanentFailureHaltsAndQuarantineIsEnvelopeGranular`, `TestAHistoryClassFailureHaltsAndNamesNoData`, `TestAQuarantineIsEnvelopeGranular` |
| a halt is terminal and silent, and a drain finishes the pass in flight | `TestAHaltedProjectionIssuesNothingAndReportsThroughReady`, `TestADrainFinishesThePassInFlightAndAHaltedOneReturnsAtOnce` |
| a store failure is retried without limit and reported through `Ready` only past `Tolerate` | `TestAClosedStoreHaltsAndATransientBackendRecovers`, `TestASingleFailureFollowedByASuccessNeverReportsUnhealthy` |
| an unreadable cursor halts and never restarts at the origin | `TestAnUnreadableCursorHaltsAndNeverRestartsAtTheOrigin` |
| a lost fence is a turn, and the row's own cursor is the resume authority | `TestTwoLiveInstancesOfOneNameTakeTurnsAndNeitherHalts`, `TestASettlementResumesFromTheRowsCursorAndNeverFromItsOwn`, `TestTwoLiveInstancesUnderInUnitApplyEachEventOnce` |
| a refused save over the row the fence admits halts, and so does a forgotten row | `TestACheckpointStoreThatRefusesASaveItsFenceAdmitsHalts`, `TestAForgottenCheckpointRefusesTheNextSaveAndHalts` |
| an unconfirmed save is settled by one load and never by a second save | `TestAnUnconfirmedSaveIsResolvedByOneLoadAndNeverBySaving` |
| a wake is a hint, and a closed one stops waking | `TestAClosedWakeStopsWakingAndAnOpenOneStillDoes` |
| the router routes, declares and refuses the unclaimed | `TestARouterRoutesDeclaresAndRefusesTheUnclaimed` |
| nothing starts in a constructor and the supervisor owns the loop | `TestTheProjectionStartsNothingAndReadsNoEnvironment`, `TestTheSupervisorHoldsAProjectionAndNewStartsNothing` |
| a projection does not pass a position a writer could still commit, and passes a burnt gap | `TestAProjectionDoesNotPassAPositionAWriterCouldStillCommit`, `TestAProjectionInAUnitPassesABurntGap` |
| two live instances of one name over one schema behave as the two modes promise | `TestTwoLiveInstancesOfOneNameOverOneSchema`, `TestASettlementTakesTheRowsCursorWhenAShorterWinnerLeftIt` |
| the two modes leave measurably different state at one kill point | `TestTheTwoModesLeaveDifferentStateAtOneKillPoint` |
| three wirings to a second database are told apart | `TestThreeWiringsToASecondDatabaseAreToldApart` |
| a rebuild drains beside the live projection and the rows agree | `TestARebuiltProjectionDrainsBesideTheLiveOneAndTheRowsAgree` |
| a projection resumes through a second value over one backing | `TestAProjectionResumesThroughASecondValueOverOneBacking` |
| the replay benchmark measures what the snapshot deferral rests on | `TestTheReplayBenchmarkMeasuresTwoOrdersApart` |
| no exported function turns a position or a progress into a cursor, no cursor is ordered, no comment promises exactly-once delivery, and no snapshot is declared or published | `TestNoExportedFunctionTakesAPositionAndAnswersACursor`, `TestNoConstructorTakesAProgressAndAnswersACursor`, `TestCursorIsNeverCompared`, `TestNoCommentInTheProjectionPackagePromisesExactlyOnce`, `TestNoSnapshotAuthorityIsDeclaredOrPromised` |
| this package costs the vocabulary plus `runtime` and nothing else | `TestNoEventPackageCostsMoreThanTheSeamItNames` |
