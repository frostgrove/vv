# Jobs and cache roadmap — 2026-08-31

This roadmap selects the useful parts of Laravel 12, Rails 8.1, Spring
Framework/Boot/Batch and Temporal without importing their accidental semantics.
It refines the jobs and cache work in the product roadmap; it does not claim
that the packages described here are already complete.

The direction is deliberately magic-first. A normal application declares a
typed cache or hands an application handler to `jobs.Auto`, while generation
materializes stable names, versions and policies in checked-in manifests. The
same system remains constructible through `Define`, `New`, `On`, explicit
registries, drivers and runners. The short path and the explicit path describe
the same contracts and run the same conformance suites.

## Evidence and selection rule

The useful source concepts are:

- Laravel cache memoization, stale-while-revalidate and queue middleware,
  uniqueness, chains and batches:
  [cache](https://laravel.com/docs/12.x/cache),
  [queues](https://laravel.com/docs/12.x/queues), and
  [scheduler](https://laravel.com/docs/12.x/scheduling);
- Rails execution-oriented cache operations, bulk enqueue and Active Job
  continuations:
  [cache store](https://api.rubyonrails.org/v8.1/classes/ActiveSupport/Cache/Store.html),
  [Active Job](https://guides.rubyonrails.org/active_job_basics.html), and
  [continuations](https://api.rubyonrails.org/v8.1.3.1/classes/ActiveJob/Continuation.html);
- Spring typed cache operations, synchronized loading, transaction-aware cache
  mutation and the Batch domain model:
  [cache annotations](https://docs.spring.io/spring-framework/reference/integration/cache/annotations.html),
  [transaction-aware cache](https://docs.spring.io/spring-framework/docs/current/javadoc-api/org/springframework/cache/transaction/package-summary.html), and
  [Spring Batch domain](https://docs.spring.io/spring-batch/reference/domain.html);
- Temporal's separation of deterministic workflows from activities, activity
  retries, timeouts, heartbeats, task queues, schedules and worker versions:
  [workflow definition](https://docs.temporal.io/workflow-definition),
  [activities](https://docs.temporal.io/activities),
  [failure detection](https://docs.temporal.io/encyclopedia/detecting-activity-failures),
  [task queues](https://docs.temporal.io/task-queue),
  [schedules](https://docs.temporal.io/schedule), and
  [worker versioning](https://docs.temporal.io/worker-versioning).

A feature is selected only when it removes repeated application infrastructure,
has a precise failure model and can be tested deterministically. Familiar
branding is not evidence. A feature that needs event history, replay or a new
source of truth does not enter the queue or cache package under a weaker name.

## Architectural boundary

### Cache

A cache stores only recreatable values whose early disappearance is acceptable.
Revocations, rate-limit accounting, job intents, leases, audit evidence and
workflow state are different subsystems even when Redis happens to store them.

The cache facade owns typed address construction, codecs, policy, envelopes,
local load coordination and mutation fences. A driver owns storage mechanics.
Application wrappers own domain key composition, authorization-before-read and
the decision that a result is safe to cache. SQL stays in a repository/driver.

### Jobs

A Frostgrove job has Activity semantics, not Workflow semantics. The durable
model is:

```text
Definition
  -> Invocation
       -> Attempt 1
       -> Attempt 2
       -> Attempt 3
```

`Definition` is the stable typed protocol. `Invocation` is one logical request,
including its intent, deadline and terminal result. `Attempt` is one physical
handler execution by one worker build. An admission deferral, an unsupported
worker revision or a claim that never enters the handler is not a handler
attempt.

Event history, deterministic replay, signals, queries, updates, child workflows,
patch markers and continue-as-new are not Frostgrove jobs features. If a product
needs them, Frostgrove may later provide a thin integration with Temporal or an
equivalent external engine. Building an in-process or database-backed Temporal
clone is a non-goal. A dependency edge or checkpoint may not advertise workflow
guarantees.

Queue consumers are infrastructure adapters around application use cases. They
decode, establish verified context, call one application port and classify the
result. Business branching and multi-use-case orchestration stay in application
use cases. HTTP and admin handlers remain transport-only. Driver SQL stays in
`jobspg` repositories.

## Cache feature set

### M0 typed cache

The explicit core remains small:

```go
c, err := cache.New(runtime, backend, scope, keys, values, policy)
```

The normal application path is declarative:

```go
var Documents = cache.Auto[ProjectionKey, string](cache.Hot)
```

Generation records the logical name, application/environment namespace,
partition decision, key and value versions, provider, limits, freshness,
retention, stale/negative policy, error policy and activation surfaces. A
custom `Backend`, codec or policy makes only that inferred choice back off. Two
equally eligible providers are a startup error, never an ordering accident.

The common contract includes typed hit/miss/negative/stale/loaded results,
bounded keys and values, local singleflight, immutable versioned addresses,
explicit read/write/invalidate failure policies and resource-bounded memory.
Provider-only abilities remain capabilities and fail at graph construction when
a declaration requires one the provider lacks.

Before the M0 API freezes, the explicit core must also satisfy these gates:

- A declaration and an activated cache are different states. Duplicate names,
  missing providers/codecs, unconfirmed scope and unsupported capabilities fail
  during generation or startup. An unactivated package variable is never first
  discovered by a production request.
- `Profile` carries a provider class as well as policy. `Hot`, `Warm`, `Durable`
  and `Disabled` do not become string switches in an Fx module. An explicit
  provider overrides selection without changing unrelated inferred fields.
- A miss/stale lookup cannot finish after another flight has already refreshed
  the address and then start a second loader from the obsolete observation.
  Lookup and flight registration use a generation/recheck protocol, proven with
  a blockable backend.
- An envelope is bounded by the current declaration, not only by its stored
  timestamps. Fresh, stale, negative and retention deadlines cannot exceed
  `written_at` plus the currently accepted policy windows.
- A batch backend receives `MaxItems`, `MaxItemBytes` and `MaxTotalBytes` before
  materialization. Fallback reads charge bytes incrementally and reject unknown
  returned addresses. `N * MaxItemBytes` is never allocated before the total
  cap is checked.
- The core rechecks encoded key/value lengths even when a custom codec violates
  its interface contract. Generated key codecs and HMAC wrappers encode through
  a bound; duration/size arithmetic is overflow checked before use.
- Physical expiry is relative/server-authoritative for a shared backend.
  Backend description states process/shared topology and clock authority. An
  explicit single-process clock and a bounded shared-clock policy are different
  values; a shared provider cannot silently inherit the process default.
- Loader, backend operation and cleanup lifetimes are separate. Built-in and
  conforming drivers obey finite operation contexts; a custom backend that
  ignores cancellation fails conformance and cannot be forcibly stopped by Go.
  A loader deadline does not remain advertised after its timer is stopped. A
  cancelled `Forget` does not spawn unbounded detached cleanup, and coordination
  state is not released while a potentially late delete/write can still violate
  the address fence.
- Observer operations, outcomes and byte fields are stable typed values. A
  descriptor exposes logical namespace/version/provider/policy without exposing
  raw cache keys or relying on the physical address digest.

The physical backend address is a fixed tuple of namespace, partition and key
digests. The human-readable namespace and version fields live in the definition
descriptor and manifest, not in Redis keys or the comparable backend address.

### Execution-scoped memo

HTTP and job entry adapters install an optional bounded L0 memo for one
execution. It stores a copied encoded envelope by `(cache instance, Address)`,
so a second lookup in the same request or job does not call Redis or PostgreSQL
but still decodes a fresh value. Direct callers work without it; low-level code
can begin or disable a scope explicitly.

Required semantics:

- the final scoped address plus an opaque cache-core identity is the key;
  tenant or principal is never inferred from the execution context;
- limits include entries, exact logical encoded charge and fixed per-entry
  overhead; an oversized item is simply not memoized;
- errors are not memoized;
- fresh, stale and retention deadlines are re-evaluated on every read;
- successful `Put`, `Forget` and loader commit update or invalidate L0 according
  to the mutation outcome;
- different executions share nothing; `Close` atomically clears entries and
  makes future writes no-ops, even if a derived context retains the empty scope;
- concurrent use is race-safe and cannot bypass the enclosing cache limits.

An ordinary backend miss is not memoized. An application-confirmed clean absence
may be memoized for the execution even when durable negative caching is disabled;
it is an explicit execution-only marker rather than a fabricated durable
envelope. A stored envelope is copied and decoded per caller, so mutable slices,
maps or pointers are not shared through L0.

Shared/background loader contexts must not inherit the first waiter's execution
memo. Only a waiter that successfully receives the completed result records it
in its own still-open execution scope; a cancelled waiter is detached and never
memoizes later. A closed scope makes writes no-ops. This is an optimization, not
an identity map. It does not make transaction-local uncommitted state visible
and does not extend backend TTL.

Mutation outcomes distinguish stored, ignored-write, superseded and staged.
Stored and logically successful ignored-write results may enter the current
execution memo; propagated errors and superseded writes may not. `Forget`
evicts L0 before backend deletion and stays evicted even if deletion fails. A
same-source transactional mutation is staged and cannot enter L0 until commit;
when the integration cannot observe commit/rollback, memo mutation is bypassed.

### `ResolveMany`

`LookupMany` avoids repeated backend reads. `ResolveMany` additionally invokes
typed batch loader groups for unresolved addresses:

```go
type BatchLoader[K, V any] func(context.Context, []K) ([]LoadResult[V], error)

func (c *Cache[K, V]) ResolveMany(
    context.Context,
    []K,
    BatchLoader[K, V],
) ([]Result[V], error)
```

The contract is:

- output order and cardinality equal input order and cardinality;
- duplicate physical addresses are loaded once and fan out deterministically;
- each newly reserved loader group receives unique unresolved keys in first-seen
  order; an address is loaded at most once per group;
- hits and negative hits do not enter the loader; stale entries follow the
  declared stale policy rather than one universal rule;
- single-key and batch resolution share one per-address member flight and the
  same mutation/invalidation fence; members loaded together reference one
  loader group;
- `MaxBatchKeys`, total key bytes and group/flight capacity are checked before
  invoking application code; encoded value and cumulative result bounds are
  checked before backend writes;
- the portable form is all-or-error and returns no unmarked partial result;
- an optional per-key form is a different API, not a mode bit;
- physical chunking and `BatchReader`/`BatchWriter` do not imply atomicity;
- cancellation, saturation and last-waiter behavior match single-key resolve.

`MaxFlights` counts loader groups, while key/byte limits bound members inside a
group. Registration of uncovered addresses and the saturation decision happen
under one coordination lock; overlapping single and batch calls join existing
members and load only the uncovered set. Cancellation of one member does not
cancel siblings. A group is cancelled only when no live member has a waiter and
the declared last-waiter policy permits cancellation.

The loader must return exactly one valid result per supplied unique key. Every
result is validated and encoded, including the cumulative result budget, before
the first backend write for that new group. The framework cannot bound transient
memory allocated inside an arbitrary application loader; its adapter must be
configured from the declaration's published limits and remains responsible for
its own resource bounds. A global load or validation error starts no writes for
that group. A read error follows
the declaration's read-failure policy. A concurrent `Forget`
invalidates only its member and prevents every older batch commit from
resurrecting that value. Sequential driver writes may partially persist if a
later write fails; the portable caller still receives all-or-error and no
atomicity claim. Atomic batch storage is a separate capability.

`RefreshBlocking` loads stale and missing members in a foreground group.
`ServeWhileRefreshing` can create two groups for one mixed call: foreground
misses and detached stale refresh. `ServeOnLoaderError` loads stale and missing
members together; a global loader error can be hidden only when every member has
an allowed stale fallback. If even one member is a true miss, the portable call
returns an error and no partial result.

### Freshness and conditional caching

Fresh/stale/expired windows remain explicit. Stale-while-revalidate uses the
same bounded local flight system; a durable refresh is a separately declared
job. No cache API promises one loader across replicas. Stale deadlines are never
extended indefinitely after failures.

Typed `When(key)` and `StoreIf(value)` policies may be added after the two real
consumers require them. They are Go functions recorded by a stable generated
binding, not runtime expression strings. Tiered caches are also a separate
declaration with explicit lookup, backfill, mutation and failure order; listing
several cache names does not silently create a hierarchy.

Mutation after a business transaction is not guessed. A same-source `cachepg`
operation may join a verified transaction. An external cache mutation occurs
after commit or through an outbox owned by the application transaction/runtime
integration. A generic `Put` cannot manufacture atomicity with SQL.

### Deferred cache features

- Encryption is a codec decorator with authenticated encryption, key revision,
  rotation window and decoded-size bounds. It is opt-in and never stores a
  secret in a manifest.
- Tags are an optional capability only after a real consumer. Strict tags
  require atomic value/membership mutation; best-effort tags say so. Immutable
  revisions and generation rollover remain the default invalidation strategy.
- Distributed regeneration locks remain a lease/admission subsystem with
  fencing. `SET NX` is not promoted into a portable cache guarantee.

## Jobs feature set

### Attempts, retry and policy pipeline

Every invocation retains a bounded attempt ledger. `AttemptOrdinal` increments
only when the typed handler actually starts. A separate retry-budget counter is
charged by the typed classifier's outcome policy; admission, overlap and
incompatible-build deferrals are not attempts. Shutdown, lease loss and operator
cancellation have explicit accounting. `MaxElapsed` bounds every otherwise-free
redelivery or deferral. A typed classifier chooses success, retry with optional
delay, permanent failure, discard, quarantine, dependency deferral or
cancellation. Backoff, elapsed time and jitter use injected time and randomness.

Infrastructure policies form a fixed, generated, ordered pipeline around
execution:

- tracing and verified context restoration;
- invocation and attempt deadlines;
- batch cancellation;
- per-key concurrency;
- queue, tenant/workload and dependency rate limits;
- shared dependency exception throttling;
- panic containment and typed classification.

Application workflow logic is forbidden in this pipeline. A low-level typed
interceptor may wrap the application handler but never sees raw delivery, ack,
lease or fence controls. Policy keys are typed, versioned and bounded. A
deferral caused by admission, overlap or rate limits increments its own counter
and does not consume a handler attempt. Unknown/unsupported payload revisions
are compatibility dispositions, not transient handler failures.

### Timeout, progress and cancellation model

One `Timeout` is insufficient. A definition can declare:

- `AttemptTimeout` for one handler execution;
- `MaxElapsed` measured from the first `eligible_at`, including post-due queue
  wait, attempts, retry delays and dependency deferrals; intentional delay
  before `eligible_at` does not consume it;
- optional absolute `StartBefore` for work that becomes worthless before it
  starts;
- optional `ProgressTimeout` for a handler that emits heartbeats;

Queue wait is normally an SLO (`oldest_ready_age`), not a failure. Driver
`LeaseTTL` and renew cadence are internal recovery settings and never aliases
for business timeouts.

Progress heartbeats are lossy and may be coalesced; they renew liveness and
deliver cancellation but do not acknowledge resumable state. A separate typed,
versioned, size-bounded `Checkpoint.Commit` is durable and fenced by invocation,
attempt and lease epoch. A stale worker cannot overwrite it, and the next
attempt receives only the last successfully committed cursor.

Queued cancellation becomes `cancelled`. Running cancellation first becomes
`cancel_requested`; `RequestCancel` durably records intent, cancels the handler
context and waits for cooperative acknowledgement. `Terminate` revokes the
lease/fence and ends as `terminated`, not `cancelled`. It cannot physically kill
a goroutine or an external call, but the old worker cannot acknowledge or pass a
fenced write. Neither operation undoes completed external effects.

### Application-owned resumable cursor

Long application work may resume from a checkpoint, but jobs do not own the
business steps. The application defines a narrow typed progress/checkpoint port;
the jobs adapter implements it for the current delivery. The application use
case owns cursor interpretation, idempotency and orchestration. A retry still
enters the use case from the beginning; local variables and completed trace
steps are never restored or skipped by jobs.

Checkpoint codec, schema revision, maximum bytes and compatibility window enter
the manifest. Resume count and elapsed time are bounded by profile. A side
effect before a checkpoint may repeat after a crash, so downstream idempotency
or a fence remains mandatory.

`jobstest` must deterministically interrupt before/after an application
checkpoint, after a side effect before checkpoint persistence, during cursor
progress, after lease loss and during cancellation. It must also test old
checkpoint upcasters. These helpers do not create replayable event history.

### Bulk enqueue and batches

`EnqueueMany` is bounded by items, per-item bytes and total bytes. Input/result
ordering is stable and every item has an explicit outcome. Drivers advertise an
atomic bulk capability separately from partial/per-item bulk. A missing bulk
capability may optimize as bounded individual calls only when the same policy
pipeline still runs; it never bypasses enqueue policy or claims atomicity.

A durable batch is an invocation group, not a serialized callback closure. It
tracks logical jobs rather than delivery attempts and distinguishes pending,
succeeded, failed, discarded and cancelled. Cancellation is cooperative and
does not roll back completed work. Terminal callbacks are typed job definitions,
enqueued atomically with terminalization. The application orchestration use case
selects members, callbacks and expansion through an application-owned port; the
consumer adapter and policy pipeline never branch on business state. Dynamic
expansion is bounded per call and per batch, and only an authorized running
member can add children. Adding children and terminalizing that member are one
fenced transaction, so pending cannot transiently reach zero. A later external
append would require a separate producer token plus explicit sealing and is not
part of the first batch contract.

Producer intent, uniqueness and retention still apply inside batches. A stalled
batch sweeper, bounded metadata, progress counters, operator inspection and
purge policy are part of the feature, not follow-up cleanup. A terminal batch
generation is immutable. Retrying failed members creates a new generation linked
to the old one, with its own counters and terminal callback intents; it never
reopens a generation whose terminal callbacks have already been committed.

Jobs do not own chains or a dependency graph. An application orchestration use
case may use a driver-neutral atomic ack-plus-enqueue/outbox port to request the
next typed job after success. The framework persists that intent but does not
decide the graph. More complex orchestration stays in the application or an
explicitly chosen external workflow engine.

### Scheduling, routing and visibility

A schedule is a versioned durable entity that creates job invocations. It owns
timezone, parser version, jitter, overlap, bounded catch-up, backfill,
pause/resume and pause-on-failure policy. Each nominal occurrence has an intent
derived from schedule identity, revision and nominal UTC time. A process ticker
is explicitly per-replica and non-durable.

Workers expose stable deployment and binding identities, immutable build ID,
unique process incarnation, supported codec windows and catalog fingerprint.
Every attempt records the worker build and handler revision. Rolling deployment
does not require exact catalog equality. A claim filter is preferred; when a
driver cannot filter by compatibility, the worker performs a fenced
non-consuming release before the typed handler. No attempt is created or charged.
The initial implementation requires compatibility checks and graceful drain;
pinned/ramping version routing can wait for a multi-build consumer.

An authoritative `Get(invocationID)` is separate from bounded operational
`List`. Search fields are registry-defined and redacted; raw payload, error,
tenant ID and arbitrary metadata do not become metric labels. Operator ports
cover inspect, pause, drain, cancel, terminate, retry/requeue, quarantine,
batch/schedule operations and stale recovery. HTTP/CLI are thin adapters over
application/admin use cases.

Priority is paired with weighted fairness so a high-volume tenant cannot starve
the rest of a queue forever. Fairness keys come from verified context, are
stored as bounded digests and have explicit driver capability checks. Global
and per-key RPS are distinct from concurrency.

## PostgreSQL topology

Primary/replica is not a framework requirement and is not a safe implicit
default.

- `jobspg` takes one verified writable source for enqueue, claim, heartbeat,
  checkpoint, cancellation and acknowledgement. A replica cannot decide or
  confirm queue state.
- `cachepg` takes one source by default. A separate read source is an optional
  policy only when the declaration permits stale reads and defines deletion,
  transaction and outage behavior.
- A dedicated jobs or cache database is an isolation/deployment choice, not a
  second mandatory connection and not a replica architecture.
- Generated resource reports count the actual sources and pinned connections;
  they do not reserve a fictional primary/replica pair.

This makes a single PostgreSQL deployment the normal working configuration.
Applications that deliberately add replicas keep the explicit low-level path
and accept the documented consistency trade-off.

## Rejected imports

The following concepts are intentionally not copied:

- Spring AOP/proxy interception and string expressions;
- ORM entities, GlobalID/Eloquent models or implicit database reloads in job
  payloads and cache keys;
- job uniqueness implemented by an evictable cache lock;
- automatic queue failover to a different backend or synchronous execution;
- automatic cache failover that creates divergent hidden state;
- unlimited retries, resumptions, batch expansion or stale extension by default;
- persisted closures, eval, shell commands or HTTP ping callbacks as schedules;
- generic cache locks/counters/tags as coordination primitives;
- exactly-once, global FIFO or "Temporal-like" claims without the machinery;
- a built-in workflow engine or a Temporal-compatible service clone;
- a mandatory primary/replica topology;
- a dashboard or autoscaler inside core before stable admin ports and budgets.

## Delivery order

### Cache first

1. Stabilize the explicit typed core and mutation/flight invariants.
2. Build byte-bounded `cachememory` and deterministic `cachetest` conformance.
3. Add `Auto`, generated manifest and provider back-off rules; this completes
   the M0 magic-first surface.
4. Add execution memo and `ResolveMany`, sharing the same address coordination.
5. Replace the project's projection cache without changing authorization or
   output semantics.
6. Add `cachepg`, schema checks and bounded cleanup; migrate translation through
   its domain wrapper and batch loader.
7. Add the isolated Redis cache deployment/driver after the common contract is
   proven by memory and PostgreSQL.

### Jobs second

1. Define typed definitions, invocations, attempts, dispositions, timeout and
   policy accounting in the stdlib-only core.
2. Build virtual-time `jobsmemory`/`jobstest`, including crash and cancellation
   controls.
3. Extract `jobspg` with schema checks, payload compatibility windows,
   worker-build gating, intent, fence, retries, visibility and retention.
4. Migrate project producers and consumers definition by definition. Existing
   domain-owned resumability stays domain state; attempt trace remains
   operational evidence.
5. Add the application checkpoint port only for a consumer that needs a generic
   fenced cursor beyond its existing domain state.
6. Add bounded bulk enqueue and durable batches when fan-out progress is a real
   consumer requirement.
7. Add schedules and operator use cases.
8. Add outbox/inbox, then the isolated Redis jobs driver.

Each numbered feature is committed only after race tests, deterministic unit
tests and the relevant live-driver tests pass. Architecture review must verify
that transport adapters contain no business decisions, application orchestration
does not move into queue middleware, and driver SQL does not leak into use cases.
