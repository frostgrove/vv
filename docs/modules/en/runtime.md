# runtime — background work with an owner

```go
import "github.com/frostgrove/vv/runtime"              // the contract and the supervisor
import "github.com/frostgrove/vv/runtime/runtimefx"    // uber/fx: the runner group
import "github.com/frostgrove/vv/runtime/runtimecheck" // the D-092 guard, over any tree
```

**Module:** `runtime` and `runtimecheck` are in the root module — standard
library only.
`runtimefx` is a module of its own because it takes uber/fx ([[D-033]])
· **Depends on:** nothing

A sweep, a collector and a queue consumer are the same shape: something that
blocks until the process stops. This package gives that shape a name, starts it,
and makes a worker that dies as loud as one that never started.

---

## What you get

### `runtime` — the contract

| | |
|---|---|
| `Runner` | `Name() string`, `Run(ctx) error` — `Run` blocks until the context is done |
| `Drainer` | optional: `Drain(ctx) error`, called before cancellation |
| `Readier` | optional: `Ready(ctx) error` |
| `Declaring` / `DeclarationOf` | optional: what the runner promises, and the safe way to ask |
| `Declaration` | `Placement` (`PerReplica` · `Singleton`) and `Durability` (`NonDurable` · `Durable`) |
| `PerReplicaTimer` | the pair a process ticker answers |
| `Phase` | `PhaseIdle` · `PhaseRunning` · `PhaseStopped` · `PhaseFailed` |
| `RunnerState` | name, declaration, phase, error, start and end |
| `Observer` / `ObserverFunc` | the seam: every transition, panic-contained |

### `runtime` — the supervisor

| | |
|---|---|
| `Spec` | `Runners`, `DrainGrace`, `Logger`, `Observer` |
| `NewSupervisor(spec)` | the explicit constructor; refuses nil, unnamed and duplicate runners together |
| `Auto(runners…)` | the same thing with defaults |
| `Start(ctx)` / `Stop(ctx)` | the two lifecycle halves |
| `States()` | every runner's state, by name |
| `Ready(ctx)` | failed runners plus every `Readier`'s answer, joined |
| `DefaultDrainGrace` | 15s |
| `ErrDuplicateRunner` · `ErrRunnerReturned` · `ErrRunnerPanicked` · `ErrDrainDeadline` · `ErrNotRunning` · `ErrAlreadyStarted` · `ErrStillStopping` | |

### `runtime` — the periodic runner

| | |
|---|---|
| `PeriodicSpec` | `Name`, `Interval`, `Timeout`, `Immediate`, `Pass`, `Logger`, `Ticks` |
| `NewPeriodic(spec)` | the explicit constructor |
| `Every(name, interval, pass)` | the short form, same refusals |
| `Ticker` / `Ticks` / `SystemTicks` | the clock seam, so a test does not wait |

```go
sweeper, err := runtime.NewPeriodic(runtime.PeriodicSpec{
    Name:     "translation-debt",
    Interval: 5 * time.Minute,
    Timeout:  time.Minute,
    Pass:     debt.Sweep,
})
```

### `runtime` — the loop a component owns

| | |
|---|---|
| `LoopSpec` | `Name`, `Run`, `Logger`, `Observer`, `StopGrace` |
| `NewLoop(spec)` | a supervisor of one; it cannot fail, and the refusals arrive at `Start` |
| `Start(ctx)` / `Stop(ctx)` | the component's two halves — `Stop` waits for the goroutine |
| `State()` | the loop's `RunnerState`, for the owner that has to answer for it |

```go
this.loop = runtime.NewLoop(runtime.LoopSpec{
    Name:     "core.realtime.listener",
    Logger:   this.log,
    Observer: failure,
    Run:      this.listen,
})
```

### `runtimefx` — the runner group

| | |
|---|---|
| `AsRunner(ctor)` | annotates a constructor so its `runtime.Runner` joins the group |
| `Registered` | the group, an optional logger and an optional observer, as an `fx.In` |
| `FailurePolicy` | `ShutDownOnFailure` (the default) · `KeepRunningOnFailure` |
| `Spec` | `DrainGrace`, `OnFailure` |
| `Supervising(spec)` / `Auto()` | provides the supervisor and binds it to the lifecycle |
| `ShuttingDownOnFailure(shutdowner, log)` | the same failure policy on its own, for a component holding a `runtime.Loop` |

```go
fx.Options(
    fx.Provide(runtimefx.AsRunner(newDebtSweeper)),
    fx.Provide(runtimefx.AsRunner(newOrphanCollector)),
    runtimefx.Auto(),
)
```

### `runtimecheck` — the guard

| | |
|---|---|
| `Activation` | `File`, `Line`, `Name` — one `fx.Invoke` that activates by side effect |
| `EmptyInvokeActivations` | `(root string) ([]Activation, error)` — scan a tree with the default skip list |
| `Scanner` | `SkipDirectory func(name string) bool` — the same scan, with the caller's idea of what is not source |
| `SkipsHiddenAndVendored` | the default: `testdata`, `vendor`, `node_modules`, and any dot- or underscore-prefixed directory |

## Contributing is the activation

There is no `fx.Invoke(func(*DebtSweeper, *OrphanCollector) {})`. A runner runs
because it is in the group; the only thing invoked is the supervisor. That
matters because the empty invoke is load-bearing and looks like a leftover:
delete a parameter and the sweep stops running in production with every test
still green. [[D-092]].

`runtime/runtimecheck` is what holds it. `EmptyInvokeActivations(root)` parses a
tree as packages and reports every `fx.Invoke` whose function has an empty body
— the literal, and the named `func reached(*Client) {}` that a per-file walk
would miss because the invoke and the function are usually in different files.
`Scanner{SkipDirectory: …}` is the same walk when the caller's tree has its own
directories that are not source.

`runtime/activation_test.go` points it at this repository. Its `tolerated` map
lists the files that still do it and why, and an entry that stops matching fails
the test rather than staying behind.

The scanner is exported because the invariant belongs to whoever owns a
composition root. An application holds it over its own tree with:

```go
func TestNothingInTheTreeIsActivatedByAnEmptyInvoke(t *testing.T) {
    found, err := runtimecheck.EmptyInvokeActivations("../..")
    if err != nil {
        t.Fatal(err)
    }
    if len(found) > 0 {
        t.Fatalf("activated by an empty fx.Invoke: %v", found)
    }
}
```

## A runner that returns is a failure

`Run` blocks. Returning early — with an error, with `nil`, or by panicking — is
recorded against the runner's name, logged, handed to the observer and, by
default, shuts the process down. `nil` is the case that hides best, so it gets
its own sentinel, `ErrRunnerReturned`.

A deployment that has decided a dead runner is survivable says
`OnFailure: runtimefx.KeepRunningOnFailure`. It is a named value rather than an
omitted `true`.

## The loop a component owns is still supervised

A bus holding one `LISTEN` connection and a cache reclaiming what expired are
background work, and neither belongs in the process's group: the loop starts
when the component starts, and a graph holding the component without a
supervisor would hold a loop nothing runs. `runtime.Loop` is that case — a
supervisor of one, started by the component's own `Start`, stopped by its
`Stop`. The loop keeps the name, the recovered panic, the early return recorded
as `ErrRunnerReturned` and the failure handed to an observer;
`runtimefx.ShuttingDownOnFailure` is the group's failure policy on its own, for
an owner that wants a dead loop to cost the process what a dead runner costs.
What stays ruled out is `go this.run()`. [[D-092]].

## Start-up context, drain order, grace

`Start` derives its own context. An fx `OnStart` context is cancelled as soon as
start-up finishes, and a worker handed that one stops the moment it is ready —
which looks exactly like a worker that never started.

`Stop` drains before it cancels: a runner told to finish can commit its last unit
of work, one that is cancelled first can only abandon it. What has not returned
within `DrainGrace` is named in `ErrDrainDeadline`.

A supervisor may be started again after it stopped, and each `Start` opens a
fresh generation: the shutdown flag that tells an expected return from a silent
death is cleared, and so is the state every runner ended the last generation
with. Without that, one stop switches `ErrRunnerReturned` off for the life of
the process — every later death is filed as an orderly stop and readiness keeps
saying ready. A start while the goroutines of the previous generation are still
alive — the case `ErrDrainDeadline` reports — is `ErrStillStopping` rather than
a second copy of every runner.

## Per-replica, non-durable, and said so

`Periodic` is a ticker in this process. Three replicas run three passes, and a
pass interrupted by a deploy is lost. That is often right and never obvious from
the call site, so the runner answers
`Declaration{PerReplica, NonDurable}` and the supervisor's state carries it.

Work that must happen once per cluster, or must survive losing the replica
running it, belongs in the `jobs` subsystem and its durable schedule instead.
`jobsfx` contributes its worker pool and its scheduler to this group, under the
names `vv.jobs.workers` and `vv.jobs.scheduler`, when the spec says the
deployment runs those roles ([[D-108]]). `jobspgfx` contributes retention
housekeeping as `vv.jobspg.retention` unless the settings switch it off — that
one is a per-replica ticker, which is why it is here and not a durable schedule.

A pass that fails or panics is reported and the schedule continues — a sweep that
could not reach the database at 03:00 must still run at 03:05 — and a pass that
overruns its budget is cancelled rather than delaying every pass after it.

## Readiness without knowing what health is

`Supervisor.Ready` joins the failures with every `Readier`'s answer and returns
an error. `runtime` does not import `health`, and `health` does not import
`runtime`: the composition root is what makes one a `health.Contribution`.

```go
healthfx.AsCheck(func(supervisor *runtime.Supervisor) health.Contribution {
    return health.Contribution{
        Name: "runtime.workers", Code: "workers",
        Importance: health.Degrading,
        Probe:      health.ProbeFunc(supervisor.Ready),
    }
})
```

## See also

- [health](health.md) — the registry that check reaches
- [app](app.md) — the rest of what `main()` assembles
- [[D-037]] · [[D-092]] · [[D-108]] · [[FL-028]] · [[UC-026]]
