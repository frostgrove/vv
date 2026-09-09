# D-130 — A projection is a supervised runner, and the checkpoint advance rides in the caller's unit of work

**Status:** accepted
**Invariant:** `projection.New` starts nothing and performs no I/O; `Run` blocks
on the caller's goroutine and the package contains no `go` statement. The
framework opens, commits and rolls back no transaction: under `InUnit` the unit
is `Spec.Unit`, the application's, and it must run the work it is given **once
and no more**. The log is read **outside** every unit. `InUnit` is refused when it
cannot be honoured and never downgraded to `AfterApply`. A halted projection does
nothing at all and never clears.

## The decision

### It is a runner, because everything continuous here is

[[D-092]] is the rule and this is one more application of it: a background
activity is a `runtime.Runner` the host supervises, and a constructor starts
nothing. The reference implementation this design was adjudicated against
dispatches its drain with `@Async` onto an eight-thread pool
(`EventSubscriptionProcessor.java:26`). That is refused, and what vv proves in its
place is the property the pool was for: one `Runner` per projection name under
one `Supervisor`, so one slow handler delays only its own projection.
`runtime.Supervisor` also refuses two runners of one name at construction, which
is the boot failure the reference's Kotlin port had to add by hand.

`Run` returns `ctx.Err()` and never `ErrHalted`. A returning runner is a failure
the supervisor reports and by default takes the process down for, and one
projection that cannot apply one page is not a reason to stop serving.

### The framework opens no transaction, and `Spec.Unit` runs the work once

[[D-118]] and [[D-126]] are why: the store joins a transaction the caller opened
and opens none of its own, so the only party that can put the handler's writes
and the advance in one transaction is the application. `Spec.Unit` is that
transaction, `crud.InNewTx(ctx, source, work)` is the one-line spelling, and the
framework's whole contribution is to run its two writes inside the body it was
given.

**The obligation the signature cannot carry is that a unit runs the work exactly
once.** The shape that breaks it is ordinary: a caller's own retry around the
transaction, which is what [[D-126]] and [[D-040]] tell a caller to own because
the store may not. One pass presents one advance, so a second run inside one pass
would present one above a number the row may never have taken. The second run is
therefore refused before it writes anything — and the refusal is **not** a halt:
the settlement reads the row against the first run's advance and finds either
that advance, which lands, or the one below it, which re-delivers the page.
Halting there would be the rolling-deploy failure [[D-133]] refuses, by another
door.

### The read is outside every unit, and that is a correctness rule

An `eventpg` walk mints no settlement bound while a transaction of its backing is
bound, because on the caller's own transaction such a bound settles nothing — the
floor a snapshot of that transaction reports never passes an id the transaction
itself holds — and minting it on a second connection while the caller holds one
is how a pool at its limit deadlocks. So a walk inside the projection's own write
transaction stops at the first burnt gap and stays there for the life of that
transaction. A projection that opened the unit first would deadlock its own
progress on the first rolled-back append in the log, in production, with every
test over a gapless log green.

The reference has the mirror of this problem and solves it the other way: its
subscription transaction *is* the long-running transaction its own README warns
about, so a slow handler holds `xmin` down and freezes every other subscription.
vv's read is one statement outside every unit, so that cannot occur.

### `InUnit` is refused rather than downgraded, and its precondition is stated

A configuration that asks for one transaction and silently gets two is the shape
this framework refuses everywhere. So `New` refuses `InUnit` with a nil `Unit`,
with a checkpoint store that does not support transactions, and with a
`Destination` left unstated; and **every pass** re-checks, inside the unit and
before the handler runs, that `Tracker.Transaction(ctx)` answers a valid
authority and that `crud.ExecutorFor(ctx, Destination)` finds an executor
`crud.IsTransaction` accepts. Either failing rolls the unit back and halts,
without calling the classifier — a classifier an application supplied must not be
able to call a wiring refusal retryable and spin on it for ever.

**The precondition travels with the promise wherever the promise is stated.**
What `InUnit` makes atomic is the advance and *the writes the handler makes
through the context the unit gave it*. A handler writing to a second database is
outside that transaction, and no check in this framework can see it — which is
why `Destination: projection.Unchecked` exists as a value a composition writes
out loud rather than as a field left zero. Under `Unchecked` a crash between the
handler's commit and the unit's leaves the read model's rows in place while the
advance rolls back: `AfterApply` semantics under an `InUnit` spec, closed by the
handler's own idempotency and by nothing else.

### A halt is terminal, and it does nothing

A halted projection keeps running and does exactly nothing: no read, no save, no
handler call, no ticker. It publishes the transition once, records the failure in
`State`, and waits. There is no `Resume`, `Retry` or `Clear`, and the exit is a
new value in a new process after the operator has fixed what halted it. A halt
that could clear itself would be a retry loop with a longer period, and the
classifier already said the failure was permanent.

### Each attempt is handed its own page

§INV-021 is stated over the hand-off — *whoever hands mutable memory to another
party says whether anybody will write it again*, and a sender that is still a
reader must make the hand-off safe. Across a retry the projection is still a
reader, because it re-applies the page it holds rather than re-reading. So the
projection pays: a fresh slice and a fresh copy of every payload per attempt, the
first included, with the log's own page kept untouched beside them. It is the
**eighth** hand-off of that enumeration and the first whose sender re-reads what
it handed over. The alternative — narrowing the grant to "yours for the duration
of this call" — makes a handler that keeps a page, which §INV-038 blesses, lose
events with no error on any path.

### Why this package re-spells three `jobs` names instead of importing them

`Backoff`, `Attempts` and the permanent/retryable verdict are `jobs.BackoffPolicy`,
`jobs.RetryLimit` and `jobs.Permanent` under other names. Importing them would
drag the whole job runtime into the closure of a package every consumer of a
projection compiles: `scripts/event_test.go` charges `event/projection` exactly
`./runtime` and nothing else, and that row is the argument. A projection is not a
queue — there is no enqueue, no lease, no visibility timeout and no per-item
attempt row — so what would be shared is three shapes and a dependency, and the
dependency is the part that is not free.

## What it forbids

- Do not start a goroutine in this package. `scripts/extensions_test.go`'s
  goroutine arm forbids a `go` statement in any non-test file of it.
- Do not open, commit or roll back a transaction anywhere in `event/projection`.
- Do not read the log inside a unit of work.
- Do not downgrade `InUnit` to `AfterApply` on a store that cannot support it,
  and do not make `Unchecked` reachable by leaving `Destination` zero.
- Do not state `InUnit`'s atomicity without its precondition in the same breath.
- Do not add a `Resume`, `Retry` or `Clear` for a halt.
- Do not hand two attempts one page, and do not narrow the hand-off grant to
  make that safe.
- Do not import `jobs` from `event/projection`.

## Where it lives

- `event/projection/projection.go` — `Run`, `Drain`, `Ready`, `follow`,
  `backoff`, `stop`, and `Declaration`.
- `event/projection/spec.go` — `New`, its refusal set, the defaults, and the two
  fields whose obligation the type cannot carry.
- `event/projection/pass.go` — `insideAUnit`, `outsideAUnit`, `claimed`,
  `checkUnit`, and the failure table.
- `event/projection/page.go` — `copyOf`, the eighth hand-off.
- `scripts/event_test.go` — the `charged` row that is this package's dependency
  budget.

## Proven by

- `TestTheProjectionStartsNothingAndReadsNoEnvironment` and
  `TestTheSupervisorHoldsAProjectionAndNewStartsNothing` — the runner half.
- `TestEveryReadArrivesOutsideEveryUnit` — every read the loop issues, on a
  context carrying no transaction of the checkpoint store's backing.
- `TestInUnitIsRefusedAndNeverDowngraded` and `TestNewRefusesEverySpecItCannotAssemble`
  — the refusal set at construction and per pass.
- `TestAUnitThatRunsTheWorkTwiceIsRefusedAndThePageIsRedelivered` — the arity
  obligation, and that breaking it re-delivers rather than halts.
- `TestTheAdvanceAndTheHandlersRowsCommitTogetherInMemory` and
  `TestThreeWiringsToASecondDatabaseAreToldApart` (`event/eventpg`, live) — what
  `InUnit` makes atomic, and the three wirings to a second database told apart:
  `AfterApply` works, `InUnit` naming the read model's own source is refused
  before the handler runs, and `InUnit` with `Unchecked` is accepted and leaves
  the read model's rows behind when the unit rolls back.
- `TestTheTwoModesLeaveDifferentStateAtOneKillPoint` (`event/eventpg`, live) —
  the two modes at one kill point, which is the measurement the mode names are
  about.
- `TestAHaltedProjectionIssuesNothingAndReportsThroughReady` and
  `TestADrainFinishesThePassInFlightAndAHaltedOneReturnsAtOnce` — a halt is
  terminal, silent and drainable.
- `TestARetryReAppliesTheLogsOwnPageAfterABackoff` and
  `TestARedeliveryCarriesTheSameIdentitiesInMemoryOfItsOwn` — the page per
  attempt.
- `TestNoEventPackageCostsMoreThanTheSeamItNames` — the closure this package is
  charged for.

## See also

[[D-040]] [[D-092]] [[D-118]] [[D-126]] [[D-128]] [[D-133]] [[FL-038]] [[UC-032]]
