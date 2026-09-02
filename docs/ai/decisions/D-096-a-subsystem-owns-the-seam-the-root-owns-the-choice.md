# D-096 — A subsystem owns the seam, the composition root owns the choice

**Status:** accepted
**Invariant:** `cache` and `jobs` each publish two dependency-neutral seams and
neither publishes a policy: a bounded, ordered, panic-isolated observer fan-out
that composes their own event vocabulary, and a `Check(ctx) error` probe that
says whether the subsystem can serve. Importance, transport, aggregation and
process globals are the composition root's, and no subsystem grows a
telemetry or health package of its own.

## The decision

Two things were missing at the same seam and they have the same answer.

**Fan-out.** A cache has one `Observer` slot and workers have one
`WorkerObserver` slot. An application that wants its own metrics *and* an
exported tracer had to write the chain itself, and the obvious chain is wrong in
three ways: it lets one child's panic swallow the ones after it, it invites a
goroutine "so the slow child does not block", and once it lives in the telemetry
package it owns the ordering of everybody else's callbacks. `cache.Observers`
and `jobs.WorkerObservers` are therefore base-owned. They copy and validate a
finite list at construction, refuse more than eight children before any runtime
work, skip nil and typed-nil entries, run children synchronously in registration
order, and isolate each child's panic so the later ones still run and the
operation's result never changes. They start no goroutine, queue, retry or
timer — which is what keeps [[D-084]]'s terminal shared-load event inside the
admitted flight slot, still applying backpressure.

**Probe.** The subsystems knew things nobody could ask them: a cache knew
whether its backend answered, and workers had a latched fatal driver failure and
a run state. `Cache.Check` and `Workers.Check` publish exactly that and nothing
more — one error, or nil. They do not name themselves, do not carry an
importance, do not choose a status code and do not register anywhere. The
composition root wraps the method it wants in a `health.Contribution` and picks
the importance, because the same cache is required in one program and merely
degrading in another; that is [[D-091]], and this decision is what gives it
something to wrap.

The two seams are one decision because the alternative is the same in both
cases: a `cacheotel`, a `jobshealth`, a package per subsystem per concern. The
base owns a typed seam; the application composes.

`Cache.Check` answers from the backend's `HealthChecker` capability when it has
one ([[D-093]]) and passes when there is nothing to probe — an activated cache
with a backend that cannot be asked is not a failing cache. The driver's own
message never reaches the caller; the error is the sanitized `ErrBackend`
category, as everywhere else in the package.

## What it forbids

- Do not compose observers inside a telemetry or driver package.
- Do not run a fan-out child on a goroutine, and do not release a flight slot
  before the terminal callback returns.
- Do not let a subsystem decide its own health importance, name or public code.
- Do not add a health or telemetry package per subsystem.
- Do not render a driver's error text through a probe.

## Where it lives

- `cache/observers.go`, `jobs/worker_observers.go` — the bounded fan-outs and
  their `Must` forms.
- `cache/health.go`, `jobs/health.go` — the probes and the workers' readiness
  reading of the run state and the fatal latch.
- `cache/capability.go`, `cache/cachememory/backend.go` — the backend health
  capability the cache probe consults.

## Proven by

- `cache/observers_test.go` and `jobs/worker_observers_test.go` — every child
  runs in registration order through a panicking sibling; an over-limit list is
  refused by the constructor and panics through `Must`; absent children are
  skipped; a composed observer reaches the live cache runtime.
- `cache/health_test.go` — an unactivated cache refuses; a failing backend is
  reported as `ErrBackend` without its own message; a backend with nothing to
  probe still answers.
- `jobs/health_test.go` — workers are not ready before `Run`, are ready while
  running, are not ready after they stop, and report a latched driver failure.

## See also

[[D-084]] [[D-091]] [[D-092]] [[D-093]] [[FL-025]] [[FL-027]]
