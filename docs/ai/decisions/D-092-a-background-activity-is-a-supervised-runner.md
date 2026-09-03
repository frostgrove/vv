# D-092 — A background activity is a supervised runner, and a runner that returns is a failure

**Status:** accepted
**Invariant:** Background work implements `runtime.Runner` and is started by a
supervisor, not by its own constructor. `Run` blocks until its context is done;
returning earlier — with an error, with `nil`, or by panicking — is recorded as a
failure, reported, and by default takes the process down. Two runners may not
share a name. Work whose lifetime is a component's rather than the process's is
a `runtime.Loop` — a supervisor of one, started by the component's own `Start` —
and never a bare goroutine. Nothing is activated by an `fx.Invoke` whose function
has an empty body — named or not — and a test walks the tree to say so. A
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

## A component may be the supervisor of its own loop

Some background work is not the process's. A bus that holds one `LISTEN`
connection, a cache that reclaims what expired: the loop starts when the
component starts, stops when it stops, and its absence is already visible
through the component's own API — a subscription is refused, a read misses.
Handing one of those to the process's runner group buys nothing and costs two
things. It starts the loop after start-up instead of during it, so a component
whose `Start` is a readiness gate loses the gate. And it makes the work depend
on a graph the component is not built with: a module that provides the component
without a supervisor then holds a loop nothing runs, which is the failure the
group exists to prevent.

`runtime.Loop` is that exception, and it is not an exemption. It is a supervisor
of one, so the loop keeps everything the group would have given it — a name, a
recovered panic, a return before the stop recorded as a failure, a state its
owner can read, and a failure handed to whatever observer the composition root
chose. `runtimefx.ShuttingDownOnFailure` is the failure policy on its own, for a
component that wants a dead loop to cost the process what a dead runner costs.

What the exception does not permit is `go this.run()`. A goroutine with no owner
is what this decision is about, and a component owning one is not the same as
nobody owning it.

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

- `fx.Invoke(func(*Worker) {})` as an activation, and `fx.Invoke(reached)` over
  a `func reached(*Worker) {}` declared elsewhere in the package — the same
  thing wearing a name, and the form a guard that only knows function literals
  never sees. `runtime/runtimecheck` parses a tree as packages and reports both;
  `runtime/activation_test.go` points it at this repository and fails on one, so
  the rule is checked rather than remembered. Its `tolerated` map is the list of
  files that still do it and why — debt with an owner, not an exemption, and an
  entry that stops matching fails the test instead of rotting. The scanner is
  exported because a composition root that is not this repository holds the same
  invariant and cannot hold it with a test living here.
- A constructor that starts a goroutine, and a `Start` that starts a bare one.
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
`runtime/loop_test.go` — *a panic in a loop is recovered and reported under its
name instead of taking the process down*, *a loop that returns before its owner
stops it is a failure*, *stopping a loop waits for the goroutine and is not a
failure*, *a loop stopped before it started and one with nothing to run are both
answered*, *a loop is per-replica and survives nothing*.
`runtime/periodic_test.go` — *a failed pass does not end the schedule*, *a
panicking pass does not end the schedule*, *a periodic runner runs on every
replica and survives nothing*.
`runtime/runtimefx/runtimefx_test.go` — *a runner in the group runs without an
invoke that names it*, *a dead runner takes the process down with it*, *an
application may say that a runner's death is survivable*.
`runtime/activation_test.go` — *nothing in the tree is activated by an empty
invoke*.
`runtime/runtimecheck/emptyinvoke_test.go` — *an invoke that names an empty
function is an activation even though it is not a literal*, *an empty function
literal passed to invoke is an activation*, *an invoke whose function has a body
is ordinary wiring*, *a test file may activate whatever it likes*, *the default
scan leaves testdata and hidden directories alone*, *a scanner may say which
directories are not part of the tree*, *source that does not parse is reported
instead of passing*.
`jobs/jobsfx/activation_test.go` — *an enabled role runs under the supervisor
and is named there*, *an enabled role refuses to start when no supervisor holds
it* ([[D-108]] is the role half of the same change).
`jobs/jobspg/jobspgfx/jobspgfx_test.go` — *the module contributes housekeeping as
a supervised runner*, *disabled housekeeping contributes no runner*.
`jobs/jobspg/jobspgfx/retention_test.go` — *housekeeping refuses to start when no
supervisor knows the runner*, *a retention runner can be built without the
module*.
