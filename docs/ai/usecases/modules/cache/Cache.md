# cache · cachememory — typed disposable values with bounded work

**Covers:** `github.com/frostgrove/vv/cache`, `github.com/frostgrove/vv/cache/cachememory`
**Sweep:** happy paths · edge cases · release readiness
**Verdict:** ready for bounded in-process caching. Shared PostgreSQL/Redis
backends, request memoization and `ResolveMany` remain later capabilities and
are not implied by this verdict.

## What a consumer is trying to do

An application has an expensive read whose result can be recreated. The author
wants to declare the value once, receive a complete production policy, and keep
the loader in application code. They do not want handlers to invent key strings
or know whether storage is memory, PostgreSQL or Redis. They also do not want a
cache miss storm or pathological serialization shape to become an unbounded
resource claim.

The core product boundary is [[UC-024]]: cache values are disposable, typed and
bounded. The backend is storage mechanics. Domain authorization happens before
cache access, and domain data required by a loader is explicit rather than
smuggled through whichever request context arrived first.

## Happy cases

### H-CACHE-01 — Declare a hot typed cache

`Auto` plus `Define` captures name, namespace generation, scope, key codec and
value codec. `Hot` is the no-argument profile. `Set` and `Activate` validate all
declarations and providers before an atomic publication. `Describe` exposes the
declared and activated form, including resolved transient reservation. The
direct `New` constructor remains the equivalent opt-out from top-level
declarations.

### H-CACHE-02 — Coalesce a popular miss

One address has one local member per load group. All waiters receive the same
typed outcome, while caller cancellation removes only that caller. Flight and
wait saturation are bounded. `FinishLoader` may let a shared load finish after
the last waiter; `CancelLoader` cancels it. Neither path inherits a request
principal or transaction.

### H-CACHE-03 — Serve stale deliberately

Freshness and retention are separate. `RefreshBlocking`,
`ServeWhileRefreshing` and `ServeOnLoaderError` have distinct observable
results. Negative caching has its own duration. Jitter only subtracts from a
fresh interval and never extends it. An invalidated or overwritten generation
cannot be restored by an earlier loader.

### H-CACHE-04 — Read a batch without changing its meaning

`LookupMany` checks key count, aggregate key bytes and result bytes, preserves
input order and duplicates and uses `BatchReader` when the backend declares it.
Fallback reads have the same result semantics. Capability discovery is bounded
and refuses wrapper cycles.

### H-CACHE-05 — Disable storage safely

`Disabled` invokes the loader directly and stores nothing. The loader still has
a finite timeout, caller cancellation wins with the same classification as the
enabled path, context values are hidden and transient admission happens first.

### H-CACHE-06 — Use bounded process memory

`cachememory` enforces entry, item and charged-byte limits, copies bytes in both
directions and evicts the least recently used live entry. Expiry, cancellation,
batch limits, reset, close, observers and accounting are deterministic and
covered by its backend conformance suite.

## Edge cases that define the contract

- A declaration is inactive until the entire activation graph commits.
- Shared providers require bounded clock skew; process memory uses the process
  clock and capacity-only retention is allowed only where the backend supports
  it.
- Every operation acquires transient admission before coordination or timers.
  Waiter slots are backed by permanent reservations and cannot overcommit the
  byte budget.
- `MaxBytes` constrains the encoded wire value. `MaxDecodedBytes` constrains the
  conservative decoded allocation model. Escaped wire text may therefore be
  larger than the decoded text without violating either bound.
- Safe JSON refuses reachable interfaces, JSON/text hooks, unsupported kinds
  and `time.Time`; `RFC3339UTC` handles time. Both JSON modes reject raw invalid
  UTF-8. `TrustedJSON` permits controlled hooks/interfaces but explicitly drops
  the bounded-work promise for its dynamic encode graph/traversal and for every
  trusted encode/decode hook body; its writer and postflight still bound the
  resulting wire/decode contract.
  Safe JSON is refused at activation under `GOEXPERIMENT=jsonv2` until that
  runtime's allocation behaviour has its own proof.
- Load observer callbacks see detached, value-blind context. They run
  synchronously and must be bounded, non-blocking and non-reentrant for the
  emitting cache. Panic is contained; `Stats` re-entry is safe.
- Pre-existing runtime pools and caller-owned object graphs are outside the
  transient ownership claim. Cache-created copies, destinations, timers,
  coordination and registered work are inside it.
- Application loader and observer bodies own their own work and allocations;
  transient admission covers the cache-created execution shell around them.
- Consumer key functions, partitioners, backends, clocks and randomness
  providers own their extension work; admission covers the cache-created values
  and call shell, not arbitrary code behind those interfaces.

## Release evidence

The core coordination, stale, mutation, batch, activation, runtime and
transient suites exercise fake-clock boundaries, cancellation races, saturation
and repeated shuffled/race runs. JSON tests cover type-graph limits, hooks,
interfaces, cycles, UTF-8, field selection, wire/decoded separation and
round-trip admissibility. `cachememory` runs both its implementation suite and
the shared backend conformance suite.

The remaining cache roadmap is additive. A shared backend must pass the same
backend conformance contract and may not move SQL into the facade. Memoization
and bulk resolve need their own ownership and bounded-work decisions before this
sweep can call them ready.
