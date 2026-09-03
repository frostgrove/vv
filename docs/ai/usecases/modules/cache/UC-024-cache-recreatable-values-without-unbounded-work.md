# UC-024 — Cache recreatable values without unbounded work

**Actor:** the application author serving a read-heavy use case
**Covered by:** [[FL-025]]

## Scenario

The author can recreate a product card, feature description or derived summary,
but doing so for every request is too expensive. They want ordinary declarations
to select a sensible local, shared or durable cache policy without spreading
serialization, key prefixes, stale rules and stampede protection through their
business logic.

The value is disposable. An early eviction is acceptable and a miss simply
runs the application's loader again. At the same time, a popular missing key,
an oversized value or a maliciously shaped JSON document must not create an
unbounded number of loaders, waiters, timers or allocations.

Requests may belong to different principals and transactions. The first request
to miss must not accidentally donate those values to work later shared with
another request. The author also needs a switch that disables storage during an
incident without silently removing cancellation and resource limits.

## What must hold

1. One typed declaration names the key, value schema, namespace and operational
   profile. Ordinary defaults are complete; a consumer with their own
   composition system can build the same contract explicitly.
2. Application, environment, purpose, generation, partition, key version and
   value schema separate values that must never collide. An ambiguous provider
   or physical namespace is refused before any cache is published.
3. A hit, a clean absence, a stale value and a newly loaded value remain
   distinguishable. Negative caching and stale fallback happen only when the
   declared profile permits them.
4. Concurrent misses for one address share one local load. The number of active
   load groups and waiting callers is finite and visible; admission and
   cancellation deadlines are finite. A loader or backend must cooperate with
   its cancellation signal. Nothing advertises a distributed lock.
5. Reads, batches, writes, invalidations, keys, envelopes, wire values, decoded
   values and all newly attributable transient work have finite declared
   bounds. Admission failure starts no loader and publishes no partial mutation.
6. Wire size and decoded allocation are separate limits. The safe structured
   codec rejects types and values whose work it cannot bound before an
   application hook runs; an explicitly trusted escape is available and named
   as such.
7. A loader shared by unrelated callers receives cancellation and a finite
   deadline but no principal, transaction, request body or other caller value.
   Required domain data is an explicit key or dependency.
8. A write or invalidation that races an older load wins. The older result may
   return to its existing waiter only when safe, but cannot overwrite the newer
   backend state.
9. Disabling storage preserves caller cancellation, loader timeout, context
   isolation and transient admission. It changes storage behaviour, not the
   safety model of application code.
10. The in-process backend owns its bytes, has finite entry/item/total limits,
    evicts deterministically and does not publish a requested write after that
    write is cancelled or rejected. Already completed expiry cleanup or access
    promotion may remain observable when later cancellation wins.
11. A declaration may require what a backend can do beyond storing bytes, and a
    provider that cannot do it is refused at start-up. A capability the core
    itself calls is only ever satisfied by the backend implementing it; a
    capability only the application uses is the driver's to publish.
12. One execution may install a bounded remembering scope so repeated reads of
    the same address within it cost one backend read. It remembers only what the
    backend actually stored and only a read the subsystem confirmed was still
    current — never a miss, never an error, never a read a concurrent write
    superseded — its owner closes it, and after that it answers nothing. Two
    reads in one execution never move backwards in time. Any write in that
    execution drops what it held for that address.
13. Filling many missing addresses at once asks the application once, keeps
    caller order and duplicates, proves the whole batch fits before storing any
    of it, and fails as a whole rather than returning some answers and an error.
14. The application can compose several independent observers over one event
    stream without one of them being able to swallow another, reorder them or
    move them off the operation, and can ask the subsystem whether it can serve
    without the subsystem deciding what that answer is worth.
15. A physical storage resource holds caching or durable state, never both. The
    composition root says which tenants live on each resource identity, and a
    cache placed on one that holds queued work or revoked sessions is refused at
    start-up rather than left to a memory-pressure event. Only two durable
    tenants may share one resource, and only with a written reason. A root that
    cannot afford an undescribed resource can require every resource a cache
    lands on to be declared, and start-up then fails on the omission.
16. Wiring the subsystem into a dependency graph is what activates it. The
    caches a deployment declares, the backends it built and the tenants of each
    resource are contributions the graph carries rather than a call the root
    has to remember; the declaration a durable subsystem lives behind is a
    value, so nothing has to import the cache to be counted as a tenant of the
    storage it shares; and a deployment that would share an eviction domain
    fails to start instead of failing to check.

## Out of scope

- A cache is not a source of truth, a revocation list, rate limiter, job queue,
  lease service, audit log or workflow store, and it does not share their
  eviction domain.
- Load coalescing is local to one process. Cross-process coordination needs a
  subsystem with its own failure model.
- A generic context-value projector is not provided. It would make security and
  memory ownership implicit again.
- The dynamic encode graph, traversal and hooks chosen through the trusted codec
  escape are application code and cannot be covered by the cache's bounded-work
  or allocation guarantee.
- Work and allocations inside application loaders and observers are outside the
  byte guarantee; the cache-owned wrappers, timers and retained results around
  them remain bounded.
- Work and allocations inside custom key encoders, partitioners, backends,
  clocks and randomness providers are owned by those application/driver
  extensions rather than by transient admission.

## Status

Covered by the typed facade and bounded in-process backend, including the
execution memo, batch resolve, the driver-declared capability set and the
observer and probe seams. The uber/fx binding carries the sets,
providers and resource declarations and activates with declared resources
required, so the eviction-domain rule runs in a deployment rather than only in
the subsystem's own tests. Shared PostgreSQL and Redis storage remain separate
features; their absence does not weaken this use case's local bounded-work
contract.
