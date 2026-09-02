# D-092 — A background activity is a supervised runner, and a runner that returns is a failure

**Status:** accepted
**Invariant:** Background work implements `runtime.Runner` and is started by a
supervisor, not by its own constructor. `Run` blocks until its context is done;
returning earlier — with an error, with `nil`, or by panicking — is recorded as a
failure, reported, and by default takes the process down. Two runners may not
share a name. Nothing is activated by an `fx.Invoke` whose body is empty. A
supervisor started again after a stop judges its runners by the new generation,
not the one before it.

## The decision

The pattern this replaces has two halves, and both are ways of losing a worker
without noticing.

The first half is activation by side effect. A constructor appends an `fx.Hook`
and starts a goroutine, so the worker exists if and only if something asked the
container for its type — and since nothing does, the composition root writes
`fx.Invoke(func(*DebtSweeper, *OrphanCollector, *StructureSweeper) {})`. That
line is load-bearing and looks like a leftover. Delete a parameter during a
refactor and the sweep stops running, in production, silently, with every test
still green. Contributing to a value group instead makes the activation the
declaration: a runner in the group runs, and the supervisor is the one thing
that has to be invoked.

The second half is the unwatched goroutine. `go this.run(ctx)` has no return
value and no owner. A panic inside it kills the process with a stack trace nobody
maps back to the sweeper; a `return nil` from a loop that hit an unexpected
branch is completely silent. The supervisor recovers the panic, names the runner,
records the failure and — because a process that has quietly stopped doing half
its job is worse than one that restarted — asks for shutdown. `KeepRunningOnFailure`
exists for the deployment that has decided otherwise, and it is a named value,
not a `false`.

A stop is bounded to the generation it stopped. The supervisor tells an expected
return from a silent death by a flag it raises while stopping, and a flag raised
and never lowered turns the guarantee above off for the rest of the process's
life: after one stop-then-start every `nil` return is filed as an orderly stop,
readiness keeps answering ready, and the worker is gone. So `Start` opens a
generation — the flag down, every runner's recorded state back to what a fresh
supervisor holds — and refuses to open one over goroutines the last stop never
got back.

Drain runs before cancel. A runner given a cancelled context can only abandon
what it holds; one told to drain first can commit its last unit of work and then
observe the cancellation. A runner still running when the grace expires is named
in the error, because "shutdown took 30 seconds" without a name is a bug report
nobody can act on.

## Per-replica and non-durable, said out loud

`runtime.Periodic` is a ticker. Three replicas mean three passes, and a pass
interrupted by a deploy is lost. That is often correct — an idempotent repair
sweep does not care — and it is never obvious from the call site, which looks
exactly like a scheduled job. `Declaration{PerReplica, NonDurable}` is what the
runner answers, and the supervisor's state carries it, so the difference between
this and a durable `jobs` schedule is visible without reading the constructor.
Work that must happen once per cluster, or must survive losing the replica that
was running it, does not belong here.

## What this rules out

- `fx.Invoke(func(*Worker) {})` as an activation.
- A constructor that starts a goroutine.
- A supervisor that cancels before it drains, or one that reports success while
  a runner is still holding the process.
- A restart that inherits the previous generation's shutdown flag, its recorded
  failures, or its goroutines.
- A periodic runner presented as a schedule.

## Where it lives

[[FL-028]].

## Proven by

`runtime/supervisor_test.go` — *a runner that returns on its own is reported
instead of disappearing*, *a panicking runner fails alone and the others keep
running*, *two runners with one name are refused before anything starts*, *a
runner outlives the start-up that launched it*, *stop drains before it cancels*,
*stop names the runner that ignored the drain grace*, *a runner that dies after a
restart is still reported as a failure*, *a restart forgets the failure that
preceded it*, *a start is refused while the runners of the last stop are still
alive*.
`runtime/periodic_test.go` — *a failed pass does not end the schedule*, *a
panicking pass does not end the schedule*, *a periodic runner runs on every
replica and survives nothing*.
`runtime/runtimefx/runtimefx_test.go` — *a runner in the group runs without an
invoke that names it*, *a dead runner takes the process down with it*, *an
application may say that a runner's death is survivable*.
