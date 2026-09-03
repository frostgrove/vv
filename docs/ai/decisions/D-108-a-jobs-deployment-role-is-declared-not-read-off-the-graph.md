# D-108 — A jobs deployment role is declared, not read off the graph

**Status:** accepted
**Invariant:** `jobsfx.Module` wires the queue every deployment needs and
nothing else by default. Consuming jobs and owning the schedule clock are two
deployment roles, each named by an `Activation` on the spec: `jobsfx.Enabled`
wires the role, `jobsfx.Disabled` does not, and a spec that names neither while
the container holds a consumer or a schedule is refused. What an enabled role
wires is a `runtime.Runner` in the supervisor's group, never a goroutine the
module starts ([[D-092]]).

## The decision

The version this replaces read the role off the object graph: if any
`jobs.Consumer` had reached the container, the module built a worker pool and
started it. That inference is wrong in both directions, and both are silent.

An API replica that imports its own job bindings has consumers in its graph —
`jobsfx.Bundle` contributes one per registration, because the same package
declares the job and handles it. Under the old rule every API replica also
became a worker replica, claiming deliveries it was never sized for. Going the
other way, a worker deployment whose last consumer moved to another module keeps
building a graph, keeps passing its health check and processes nothing, because
the pool it no longer builds is not something the process can miss.

Naming the role turns both into a value. `module.Profile` already says which
roles a deployment runs ([[D-106]]); this is the same answer at the one seam
`module` cannot reach, since `jobsfx.Module` is configured by the composition
root rather than filed as a contribution.

**Unstated is a refusal, not a default.** A zero value that means *disabled*
stops a worker fleet the day the field is added, and one that means *enabled*
changes nothing and leaves the inference in place. So the zero value means
*nobody said*, and it is only an error when it matters: a container holding no
consumer and no schedule is a producer, which is what the queue alone already
is. The refusal names the count it found and both constants, because the fix is
one word and the reader should not have to look it up.

**An enabled role that has nothing to do is also a refusal.** `Consuming:
Enabled` with no consumer contributed is a worker replica that would poll an
empty plan forever, and it is nearly always a bundle that was not imported.

## What an enabled role wires

`jobsfx.WorkersRunner` and `jobsfx.SchedulerRunner` adapt the pool and the
scheduler to `runtime.Runner`; the module contributes them through
`runtimefx.AsRunner`, and `runtimefx.Supervising` is what starts them. Nothing
in `jobsfx` starts a goroutine any more, and the `fx.Shutdowner` it used to hold
in order to react to a worker that died is gone — the supervisor's observer owns
that ([[D-092]]).

Two things follow, and both are deliberate:

- **The runners wait for the queue.** The pool must not reach the backend before
  `Prepare` has run and the queue is activated, and hook order across two
  modules is a property of the root's option list, not something either module
  can see. So the module's lifecycle hook opens a gate on activation and the
  runners block on it; `WorkersRunner` takes that gate as an ordinary
  `<-chan struct{}` so a hand-wired consumer has the same seam and can pass
  `nil` for "start at once".
- **A role nobody supervises is refused at start.** A graph that provides a
  `*runtime.Supervisor` built by hand, without the runner group, would take the
  runner contribution and drop it — the exact silence this decision is about. So
  the hook checks that every runner it enabled is one the supervisor knows by
  name before it activates the queue.

Draining before closing the activation is the other order that matters:
`jobs.Go` resolves through the activation, so closing it under a draining worker
turns that worker's follow-up step into `ErrNotActivated`. The module asks its
supervisor to stop before it closes, which is a no-op in the usual order and the
correction in the other one.

## What this rules out

- A worker fleet that exists because a handler package was imported.
- A worker deployment that quietly consumes nothing.
- `jobsfx` starting, watching or shutting down anything itself.
- A worker pool reaching the backend before the schema the backend prepares.
- An enabled role whose runner reached no supervisor.

## Where it lives

[[FL-028]].

## Proven by

`jobs/jobsfx/activation_test.go` — *a container that happens to hold a consumer
is not a worker replica*, *a container that happens to hold a schedule does not
own the clock*, *a role turned off leaves the queue and builds no worker pool*,
*an enabled role runs under the supervisor and is named there*, *an enabled role
refuses to start when no supervisor holds it*, *the explicit runners wait for the
queue the module activates*.
`jobs/jobsfx/jobsfx_test.go` — *a scheduler that dies is named by the supervisor
and takes the process down*, *a worker pool that dies is named by the supervisor
and takes the process down*.
