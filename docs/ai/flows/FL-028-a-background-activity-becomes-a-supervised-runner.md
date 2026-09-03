# FL-028 — A background activity becomes a supervised runner

**Entry points:** `runtime.NewSupervisor` / `runtime.Auto`,
`runtime.NewPeriodic` / `runtime.Every`, `runtime.NewLoop`,
`runtimefx.Supervising` / `runtimefx.Auto`
**Governed by:** [[D-037]] [[D-092]] [[D-108]]

What happens between a module contributing a background activity and that
activity running, failing loudly, or stopping cleanly.

## The contract

`runtime/runner.go`:

1. **`Runner`** — `Name() string` and `Run(ctx) error`. `Run` blocks until the
   context is done. That is the whole obligation.
2. **`Drainer`**, **`Readier`**, **`Declaring`** — optional interfaces, found by
   assertion on the value the supervisor holds. A runner that finishes in-flight
   work implements `Drain`; one that can say whether it is working implements
   `Ready`; one that wants its placement visible implements `Declaration`.
3. **`Declaration`** — `Placement` (`PerReplica` / `Singleton`) and `Durability`
   (`NonDurable` / `Durable`). `PerReplicaTimer` is the pair a ticker answers.
4. **`RunnerState`** and **`Observer`** — the neutral seam. `runtime` never
   learns what a metric, a tracer or a health registry is; something else
   subscribes.

## Starting

`runtime/supervisor.go`:

1. **`NewSupervisor`** — validates before anything runs: a nil runner, an
   unnamed runner, two runners sharing a name, a negative drain grace. Problems
   are joined, so `errors.Is(err, ErrDuplicateRunner)` works on the aggregate.
   Every runner starts in `PhaseIdle` with its declaration already recorded.
   `Auto(runners...)` is the same constructor with defaults.
2. **`Start`** — derives the run context with
   `context.WithCancel(context.WithoutCancel(ctx))`. An fx `OnStart` context is
   cancelled the moment start-up finishes; a worker handed it stops the instant
   it is ready, which looks exactly like a worker that was never started.
3. Each runner gets a goroutine calling **`invoke`**, which recovers a panic into
   `ErrRunnerPanicked` naming the runner. A second `Start` is `ErrAlreadyStarted`
   rather than a second set of goroutines.
4. `Start` also opens a generation: it clears the stopping flag and resets every
   `RunnerState`, so a supervisor started after a stop classifies returns the way
   a fresh one does and carries no error from the previous generation.
   **`previousGenerationFinished`** refuses a start while the goroutines of the
   previous one are still alive — the state `ErrDrainDeadline` reports — with
   `ErrStillStopping`, because a restart over them would run every runner twice.

## Returning

**`supervise`** classifies the return:

- while stopping, `nil` or `context.Canceled` is `PhaseStopped`;
- otherwise it is `PhaseFailed`, and a `nil` return becomes `ErrRunnerReturned`
  naming the runner — the case with no error to report is the one that hides
  best.

A failure is logged at `Error` and handed to the observer. One runner's failure
does not touch another's goroutine.

## Stopping

**`Stop`** marks the supervisor stopping, then:

1. **`drain`** — every `Drainer` concurrently, under `min(caller deadline,
   DrainGrace)`, each problem wrapped with the runner's name;
2. cancel the run context — after the drain, never before ([[D-092]]);
3. wait for every `Run` to return, or report `ErrDrainDeadline` naming, through
   `stillRunning`, the runners that are still holding the process.

`Stop` leaves the supervisor stopped either way; what an unfinished generation
costs is the next `Start`, not a silent second copy.

**`Ready`** is `ErrNotRunning` before start, and afterwards joins every failed
runner's error with every `Readier`'s answer. It is a seam, not a health check:
the composition root is what turns it into a `health.Contribution` ([[FL-027]]).

## The periodic runner

`runtime/periodic.go`:

1. **`NewPeriodic`** — aggregates its refusals (no name, non-positive interval,
   negative pass timeout, no pass) and defaults the pass timeout to the interval.
   **`Every(name, interval, pass)`** is the short form over the same
   constructor and the same refusals.
2. **`Run`** — optional immediate pass, then tick until the context is done,
   returning `ctx.Err()`. `Ticks` is the injection seam; `SystemTicks` wraps
   `time.Ticker` and a test supplies its own.
3. **`once`** — each pass gets `context.WithTimeout`, and **`attempt`** turns a
   panicking pass into an error. A failed or panicking pass is logged and the
   schedule continues: a sweep that could not reach the database at 03:00 must
   still run at 03:05.
4. **`Declaration`** answers `PerReplicaTimer`, so the difference between this
   and a durable `jobs` schedule is visible at the supervisor rather than only
   in the constructor.

## The loop a component owns

`runtime/loop.go`:

1. **`NewLoop(spec)`** wraps one `LoopSpec{Name, Run, Logger, Observer,
   StopGrace}` in a supervisor of one. It cannot fail, because the component
   that owns a loop builds it where it has nobody to report to; a nil body and
   the refusals `NewSupervisor` makes arrive at **`Start`** instead, which the
   owner is already returning an error from.
2. **`Start`** / **`Stop`** are the component's two halves, not the process's:
   the loop starts when its owner starts and stops when its owner stops, and
   `Stop` waits for the goroutine the way the supervisor does. **`State`** is
   the one runner's `RunnerState`, so the owner can read what happened to it.
3. **`loopBody`** is the `Runner` the supervisor sees, and its `Declaration` is
   per-replica and non-durable: a loop inside a component is one per process and
   survives nothing.
4. The point is that the exception in [[D-092]] is still supervision. A panic is
   recovered and named, a return before the stop is `ErrRunnerReturned`, and the
   observer the owner passed is told. `runtimefx.ShuttingDownOnFailure` is the
   failure policy of `Supervising` handed out on its own, for a component that
   wants a dead loop to cost the process what a dead runner costs.

## Fx wiring

`runtime/runtimefx/runtimefx.go`:

1. **`AsRunner`** annotates a constructor into the `vv.runtime.runners` group.
2. **`Supervising(spec)`** provides the supervisor and appends the lifecycle
   hook. **This invoke is the activation** — there is no `fx.Invoke(func(*Sweeper)
   {})` anywhere, and a runner runs because it is in the group.
3. **`watching`** builds the observer: the graph's own `runtime.Observer` if one
   was provided, plus a `stopper` unless the spec says
   `KeepRunningOnFailure`. `stopper` turns `PhaseFailed` into
   `shutdowner.Shutdown(fx.ExitCode(1))`. **`ShuttingDownOnFailure`** is that
   same `stopper` for a caller that has no group to supervise — a component
   holding a `runtime.Loop`.
4. **`observers`** fans out deterministically and contains each observer's
   panic, so a broken metrics sink cannot take the supervisor with it.

## What contributes runners: the jobs roles

`jobs/jobsfx/jobsfx.go` and `jobs/jobsfx/runner.go`:

1. **`Spec.Consuming`** and **`Spec.Scheduling`** are `Activation` values.
   `Module` adds a `runtimefx.AsRunner` provider for a role it was told to wire
   and nothing for a role it was not ([[D-108]]).
2. **`Spec.deployment`** checks the role against what the container holds: a
   consumer or a schedule present with no activation named is refused there, and
   so is an activation named with nothing to run. The `*queueLifecycle` provider
   depends on it, so the refusal happens in every graph rather than only in one
   that asks for `*jobs.Workers`.
3. **`workersRunner`** and **`schedulerRunner`** are the adapters:
   `Name`/`Run`/`Drain`/`Ready` over `jobs.Workers`, `Name`/`Run` over
   `jobs.Scheduler`, and a `Declaration` each — per-replica and durable for the
   pool, singleton and durable for the clock. `WorkersRunner` and
   `SchedulerRunner` are the same adapters for a graph assembled by hand.
4. **`queueLifecycle.start`** runs **`verifySupervision`** first — every runner
   the module enabled must be one the supervisor knows by name — then prepares
   the backend, activates the queue and opens the gate the runners wait on.
   **`stop`** asks the supervisor to stop before it closes the activation, so a
   draining worker's follow-up enqueue is not answered with `ErrNotActivated`.
5. **`workersRunner.Run`** reads `ErrConflict` after its own `Drain` as an
   orderly stop: a supervisor that drained before the pool reached
   `jobs.Workers.Run` stopped a pool that never started.

## What contributes runners: PostgreSQL retention housekeeping

`jobs/jobspg/jobspgfx/retention.go` and `jobs/jobspg/jobspgfx/jobspgfx.go`:

1. **`Module`** adds `runtimefx.AsRunner(newRetentionRunner)` unless
   `HousekeepingSettings.Disabled` says the deployment sweeps nothing.
   **`RetentionRunner(sweeper, settings)`** is the same adapter for a graph
   assembled by hand.
2. **`retentionRunner.Run`** measures the interval from the end of a drain, not
   from a tick: a sweep that outlasts its own period would otherwise find the
   next one already queued. Its `Declaration` is `PerReplicaTimer`.
3. **`retentionRunner.sweep`** returns `nil` for the two failures that must not
   end the schedule — the pass that spent its own `sweepTimeout`, and a backend
   answering `jobspg.ErrNotReady` — and the error itself for the rest, which the
   supervisor turns into `PhaseFailed` and `stopper` into exit code 1.
4. **`bindRetentionSupervision`** takes the supervisor as optional in the graph
   and mandatory at start: a container holding the runner while no supervisor
   knows it is refused by name. Its `OnStop` republishes a `PhaseFailed`
   runner's error, so the failure is still on the shutdown path and not only in
   the log.

## The guard against activation by side effect

`runtime/runtimecheck/emptyinvoke.go` is the half of [[D-092]] that no type can
express, so it is parsed instead:

1. **`EmptyInvokeActivations(root)`** walks a tree and reports every
   `fx.Invoke` whose argument has an empty body. **`Scanner{SkipDirectory: …}`**
   is the same walk with the caller's own idea of what is not part of the tree;
   the bare function is that `Scanner` with `SkipsHiddenAndVendored`.
2. **`scanPackage`** reads a directory as one package rather than each file
   alone. `fx.Invoke(reached)` and `func reached(Client) {}` are routinely
   written in different files, and a per-file walk sees the invoke without ever
   learning that the function it names does nothing — which is how the form the
   decision is really about escapes a guard that only knows function literals.
3. **`Activation{File, Line, Name}`** names the call site, so the failure says
   which line to delete rather than which file to search.
4. `runtime/activation_test.go` points it at this repository. Its `tolerated`
   map is the debt list: a file in it is expected to still have one, and an
   entry that stops matching fails the test instead of rotting.

The scanner is exported because the pattern belongs to whoever owns a
composition root, not to this repository's tree. A consuming application holds
the same invariant with a test that scans its own source.

## Where the decisions bite

- Handing runners the `OnStart` context stops them at the end of start-up.
- Treating a `nil` return as a clean stop loses the worker silently. [[D-092]].
- Leaving the stopping flag set across a restart does the same thing for the
  rest of the process's life. [[D-092]].
- Cancelling before draining costs the last unit of work. [[D-092]].
- Starting a component's own loop with `go this.run()`: no name, no recovered
  panic, and nothing to read afterwards. [[D-092]].
- Presenting `Periodic` as a schedule: it is per-replica and non-durable, and
  work that must happen once per cluster belongs in `jobs`. [[D-092]].
- Reading a deployment role off the graph that happens to hold a consumer.
  [[D-108]].

## Files

| File | What it holds |
|---|---|
| `runtime/runner.go` | `Runner`, `Drainer`, `Readier`, `Declaring`, `Placement`, `Durability`, `Declaration`, `DeclarationOf`, `Phase`, `RunnerState`, `Observer`, the sentinels |
| `runtime/supervisor.go` | `Spec`, `Supervisor`, `NewSupervisor`, `Auto`, `Start`, `previousGenerationFinished`, `supervise`, `invoke`, `Stop`, `drain`, `States`, `Ready`, `transition` |
| `runtime/periodic.go` | `Ticker`, `Ticks`, `SystemTicks`, `PeriodicSpec`, `NewPeriodic`, `Every`, `periodic`, `once`, `attempt` |
| `runtime/loop.go` | `LoopSpec`, `Loop`, `NewLoop`, `Start`, `Stop`, `State`, `loopBody` — the supervisor of one a component starts itself |
| `runtime/runtimefx/runtimefx.go` | `AsRunner`, `Registered`, `FailurePolicy`, `Spec`, `Supervising`, `Auto`, `ShuttingDownOnFailure`, `watching`, `observers`, `stopper` |
| `runtime/runtimecheck/emptyinvoke.go` | `Activation`, `Scanner`, `EmptyInvokeActivations`, `SkipsHiddenAndVendored`, `scanPackage`, `emptyFunctions`, `emptyInvokesIn`, `hasEmptyBody`, `isProductionSource`, `isFxInvoke` — the parse that finds an activation by empty `fx.Invoke`, in any tree the caller names |
| `runtime/activation_test.go` | `tolerated` — this repository's own tree pointed at the scanner, the guard that holds the empty-invoke half of [[D-092]] |
| `jobs/jobsfx/jobsfx.go` | `Activation`, `Enabled`, `Disabled`, `Spec.Consuming`, `Spec.Scheduling`, `Module`, `deployment`, `Spec.workers`, `Spec.scheduler`, `queueLifecycle`, `bindQueue`, `bindSupervisedQueue`, `verifySupervision` |
| `jobs/jobsfx/runner.go` | `WorkersRunnerName`, `SchedulerRunnerName`, `WorkersRunner`, `SchedulerRunner`, `newWorkersRunner`, `newSchedulerRunner`, `supervisedWorkers`, `supervisedScheduler`, `workersRunner`, `schedulerRunner`, `awaitReady` |
| `jobs/jobspg/jobspgfx/application.go` | `ApplicationSettings.Consuming`, `ApplicationSettings.Scheduling` — the roles a PostgreSQL application passes through |
| `jobs/jobspg/jobspgfx/jobspgfx.go` | `Module` — the driver contracts, and the runner contribution housekeeping makes unless it is switched off |
| `jobs/jobspg/jobspgfx/retention.go` | `RetentionRunnerName`, `RetentionRunner`, `newRetentionRunner`, `retentionRunner`, `Run`, `sweep`, `drainRetention`, `sweepRetention`, `supervision`, `bindRetentionSupervision`, `retentionSupervision` |

## Tests that walk this flow

`runtime/supervisor_test.go`, `runtime/periodic_test.go`, `runtime/loop_test.go`,
`runtime/runtimefx/runtimefx_test.go`, `runtime/activation_test.go`,
`runtime/runtimecheck/emptyinvoke_test.go`,
`jobs/jobsfx/activation_test.go`, `jobs/jobsfx/jobsfx_test.go`,
`jobs/jobspg/jobspgfx/retention_test.go`, `jobs/jobspg/jobspgfx/jobspgfx_test.go`.
