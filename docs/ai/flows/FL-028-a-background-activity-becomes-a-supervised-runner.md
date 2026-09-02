# FL-028 — A background activity becomes a supervised runner

**Entry points:** `runtime.NewSupervisor` / `runtime.Auto`,
`runtime.NewPeriodic` / `runtime.Every`, `runtimefx.Supervising` /
`runtimefx.Auto`
**Governed by:** [[D-037]] [[D-092]]

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

## Fx wiring

`runtime/runtimefx/runtimefx.go`:

1. **`AsRunner`** annotates a constructor into the `vv.runtime.runners` group.
2. **`Supervising(spec)`** provides the supervisor and appends the lifecycle
   hook. **This invoke is the activation** — there is no `fx.Invoke(func(*Sweeper)
   {})` anywhere, and a runner runs because it is in the group.
3. **`watching`** builds the observer: the graph's own `runtime.Observer` if one
   was provided, plus a `stopper` unless the spec says
   `KeepRunningOnFailure`. `stopper` turns `PhaseFailed` into
   `shutdowner.Shutdown(fx.ExitCode(1))`.
4. **`observers`** fans out deterministically and contains each observer's
   panic, so a broken metrics sink cannot take the supervisor with it.

## Where the decisions bite

- Handing runners the `OnStart` context stops them at the end of start-up.
- Treating a `nil` return as a clean stop loses the worker silently. [[D-092]].
- Leaving the stopping flag set across a restart does the same thing for the
  rest of the process's life. [[D-092]].
- Cancelling before draining costs the last unit of work. [[D-092]].
- Presenting `Periodic` as a schedule: it is per-replica and non-durable, and
  work that must happen once per cluster belongs in `jobs`. [[D-092]].

## Files

| File | What it holds |
|---|---|
| `runtime/runner.go` | `Runner`, `Drainer`, `Readier`, `Declaring`, `Placement`, `Durability`, `Declaration`, `DeclarationOf`, `Phase`, `RunnerState`, `Observer`, the sentinels |
| `runtime/supervisor.go` | `Spec`, `Supervisor`, `NewSupervisor`, `Auto`, `Start`, `previousGenerationFinished`, `supervise`, `invoke`, `Stop`, `drain`, `States`, `Ready`, `transition` |
| `runtime/periodic.go` | `Ticker`, `Ticks`, `SystemTicks`, `PeriodicSpec`, `NewPeriodic`, `Every`, `periodic`, `once`, `attempt` |
| `runtime/runtimefx/runtimefx.go` | `AsRunner`, `Registered`, `FailurePolicy`, `Spec`, `Supervising`, `Auto`, `watching`, `observers`, `stopper` |

## Tests that walk this flow

`runtime/supervisor_test.go`, `runtime/periodic_test.go`,
`runtime/runtimefx/runtimefx_test.go`.
