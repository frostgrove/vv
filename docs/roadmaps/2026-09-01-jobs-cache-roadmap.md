# Jobs and cache roadmap — linear subsystem ownership — 2026-09-01

**Status:** current delivery plan. This revision supersedes the package/module,
integration and delivery-status claims in the
[2026-08-31 snapshot](2026-08-31-jobs-cache-roadmap.md). The older document
remains useful for its cache and activity-semantics analysis, failure matrices
and operational exercises; this document governs whenever the two differ.

This roadmap applies the
[optional extension architecture](2026-09-01-extension-architecture-roadmap.md)
to two independent root subsystems. Cache stores recreatable values. Jobs owns
durable activity delivery. Sharing Redis, PostgreSQL, a request, a transaction,
tenant policy or telemetry does not merge those ownership boundaries.

## Current-tree baseline

The current repository tree contains:

- `cache`, with typed declarations, activation, address/codec/policy handling,
  lookup and mutation, bounded shared-load coordination, typed observations with
  a bounded fan-out, transient budgets, a bounded execution memo, `ResolveMany`,
  a driver-extensible capability set and a neutral `Check` probe;
- `cache/cachememory`, a process-local backend with its own typed observer;
- `cache/cachetest`, backend conformance and deterministic test helpers;
- `jobs`, with typed definitions, codecs, enqueue and staged enqueue, invocation
  and attempt records, delivery/worker contracts, durable identity restoration,
  schedules, the `Admin`/`DeliveryView` contract, count-bounded `ListSpec` and
  redrive transition, a `WorkerObserver` vocabulary whose runtime emission is
  wired in `jobs/worker_observer_runtime.go` and composes through
  `jobs.WorkerObservers`, and a neutral `Check` probe;
- `jobs/jobsmemory`, the process-local sender/worker backend;
- `jobs/jobspg`, a code-present `database/sql` PostgreSQL sender/worker backend
  with transaction staging, bounded single-record Get/Redrive, count-bounded
  List/PurgeTerminal operator controls and a schema-management choice that no
  zero value can turn into a migration ([[D-101]]), that remains in the
  building release stage;
- `jobs/jobsfx`, an already-present nested module adapting the neutral jobs
  constructors and lifecycle to Fx; and
- `jobs/jobsredis`, a committed nested Redis sender/worker backend that remains
  in the building release stage.

`cache`, `cache/cachememory`, `cache/cachetest`, `jobs` and `jobs/jobsmemory`
are packages in the root module. They do not have a `go.mod` and do not receive
a `go.work` entry. A directory is not a separate module merely because it is a
backend package. `jobs/jobspg` was one of them until its own tagged fixtures
made the root module untidy; what moved it out is the isolated-tests paragraph
below, and nothing about its production dependencies.

During this audit the changing jobs worktree exposed a durable-record
round-trip regression, passed after its fix, and also crossed an intermediate
non-compiling fencing/repository shape before later scoped runs returned green.
No moving worktree snapshot is release evidence. PostgreSQL jobs remain
**building**, not baseline or ready, until one clean revision passes
base-driver conformance plus live PostgreSQL and crash/recovery suites.

`jobs/jobsredis` is now committed and listed in `go.work`. Its layout is the
correct independent-backend direction—it imports the root jobs contract and a
Redis client rather than cache or another extension—and its isolated
`GOWORK=off go test ./...` is green. It still remains **building** until the
published-module/no-`replace`, base conformance, live Redis and crash/lease
evidence pass; a Miniredis-backed unit suite alone is not delivery evidence.

Modified or untracked follow-on jobs work is concurrent work, not delivered or
status evidence until it is committed and passes the applicable gates.

The committed redrive behavior is provisional: `RedriveInvocation` constructs
a fresh invocation with the same ID, and `jobspg.Redrive` replaces the delivery
record. The prior attempt/outcome ledger is therefore no longer available
through authoritative Admin reads after redrive. This is code-present behavior,
not accepted activity-history semantics, and keeps the Admin slice building
until the identity/generation/evidence decision below is closed.

Admin List is not resource-bounded yet. `ListSpec` limits item/offset/filter
counts, but `jobspg.List` selects and materializes each full payload-and-ledger
record with no aggregate byte budget. Before release it needs a bounded summary
projection or explicit total-byte budget and continuation; a default or maximum
item count must not imply that worst-case payload materialization is safe.

The tagged `jobspg` integration fixtures import pgx from inside `package
jobspg`, and `go mod tidy` reads every build configuration — so the tag hides
that import from a compiler and from nothing else. Whichever module holds those
files owes pgx a `require`, and while they sat in the root module that was the
dependency-free root: `make check` failed on it, and the fixtures resolved in
the workspace only because a sibling happened to supply pgx. The files are
internal tests, reaching `repository`, `newRepository` and the unexported
fixtures, so the unpublished `test` module cannot hold them without a rewrite of
what they test. `jobs/jobspg` therefore carries its own `go.mod`: pgx is
required by the module whose tests import it, the root stays third-party-free,
and the live profile resolves under `GOWORK=off`. Rewriting the fixtures against
the exported surface and moving them to `test` would make `jobspg` a root
package again, and that is the one reason to revisit this. Adding pgx to the
root to make the fixture pass stays forbidden.

The execution memo, `ResolveMany` and the base-owned observer fan-out have since
landed and are described by [[D-094]], [[D-095]] and [[D-096]]. The current tree
still does not implement a PostgreSQL cache backend, a Redis cache backend, job
batches/continuations, or an external workflow-engine adapter. Redis jobs is
committed building work rather than a released API. Those remain planned/building
work, not documentation of delivered contracts.

## Architectural decision

### Two base subsystems, no combination layer

The dependency-neutral contracts remain in the root module:

```text
github.com/frostgrove/vv
  cache/
  cache/cachememory/
  cache/cachetest/
  cache/cachepg/                 future; database/sql implementation, if accepted
  jobs/
  jobs/jobsmemory/
  jobs/jobstest/                 future; only if distinct helpers justify it

optional ecosystem modules
  jobs/jobsfx/                   current jobs→Fx owner adapter
  jobs/jobspg/                   building; database/sql implementation, a module
                                 because its own tests take a driver
  jobs/jobspg/jobspgfx/          building; jobspg→Fx adapter
  jobs/jobsredis/                building; Redis SDK implementation
```

`cachepg` is a root package while it uses only `database/sql` and root
contracts, and a directory is not a nested module merely because it is a backend
package: `database/sql` is standard library, and a consumer selects its SQL
driver in the application. `jobspg` is the case that rule did not cover. Its
production code still takes nothing but `database/sql`; its tests take a driver,
and a published `go.mod` cannot hide a test import.

A separately versioned module is justified only by a genuine optional external
implementation ecosystem. The current `jobs/jobsredis` module is that shape;
future examples are a distinct Redis cache adapter or a thin jobs adapter to an
external workflow engine. Each such module imports exactly its owning base seam
and its concrete SDK. Redis cache and Redis jobs are separate consumer choices
even if both use the same Go client; neither imports the other and no
framework-level shared Redis abstraction is introduced.

`jobs/jobsfx` is an existing owner-to-container adapter, not a precedent for
backend × container or extension × container packages. Its final keep/migrate
classification belongs to the common extension ADR. No new jobs/cache module is
created for DI convenience while that classification is open.

The allowed direction is:

```text
application composition root
       |
       +-- constructs cache and jobs independently
       +-- installs request/job execution memo explicitly
       +-- selects tenancy, telemetry and backend adapters independently
       |
       v
optional extension --------imports--------> root cache/jobs typed seams
backend adapter ------------imports--------> one owning root seam + one SDK

root cache/jobs ------------X--------------> optional extension
cache adapter --------------X--------------> jobs adapter
extension A ----------------X--------------> extension B
```

Do not create `jobsotel`, `jobstenancy`, `cacheotel`, `cachetenancy`,
`cacheredisotel`, `jobsredisotel`, `jobscache`, `cachejobs`, an auth revocation
store backed by cache semantics, or any provider × telemetry/tenancy/container
combination. An integration profile is application wiring plus tests, not a new
production package.

Production package growth must be additive:

```text
base cache + base jobs + selected backend modules + selected extensions
```

It must not grow as subsystem × backend × topology × telemetry × container.

### Ownership table

| Behaviour | Owner | Permitted collaborator | Forbidden shortcut |
|---|---|---|---|
| Cache address, codec, policy, envelope and recreatable-value rules | `cache` | Application supplies typed keys/loaders | Jobs, auth or tenancy defines cache identity |
| Shared load admission, flight lifetime and terminal callback | `cache` | Base-owned observer fan-out | Extension releases a flight, starts a goroutine or bypasses admission |
| Memory eviction and backend events | `cache/cachememory` | Its own observer composition | Merge facade and backend events into one hook |
| Backend I/O and native batch support | Selected cache backend | Base cache exact capability contract | A generic decorator tunnels to an inner executable capability |
| Job definition, placement, invocation, attempt and worker lifecycle | `jobs` | Application supplies handlers and neutral policies | Cache or an extension becomes the job state store |
| Durable identity capture/restore | `jobs` contract | Tenancy/application implements the neutral seam | Jobs imports tenancy or trusts raw context/payload values |
| Transaction-bound enqueue | `jobs.Stager` plus a backend-specific stager when that backend supports it; currently `jobspg.TxStager` | Application UoW supplies the verified backend transaction | A cross-subsystem transaction package guesses commit or assumes every backend can stage |
| Administrative inspect/redrive/purge | Exact `jobs.Admin` value; currently `jobspg.Driver` implements it | Application supplies authorization and deliberately handles payload-bearing `DeliveryView` values | A generic driver wrapper tunnels to an inner Admin or assumes every backend supports it |
| Telemetry | Base typed observer/middleware seam | One optional `vvotel` extension returns adapters | Per-subsystem OTel packages or hidden global providers |
| Cross-subsystem ordering | Application composition root | Explicitly constructs and closes selected pieces | Framework service locator, implicit registry or global lifecycle |

### Illustrative linear composition

This application sketch intentionally contains no jobs × cache × tenancy × OTel
package. `cache.New`, `jobs.NewQueue`, `jobs.NewWorkers`, `cache.Observers` and
`jobs.WorkerObservers` are current entry points. The extension factories and
their exact option names remain illustrative until the corresponding milestones
are accepted:

```go
cacheRuntime.Observer = cache.MustObservers(
    applicationCacheObserver,
    vvotel.Cache(telemetry, cacheTelemetryPolicy),
)
ordersCache, err := cache.New(
    cacheRuntime, cacheBackend, orderScope, orderKeys, orderValues, cachePolicy,
)

queue, err := jobs.NewQueue(jobs.QueueSpec{
    Namespace: jobsNamespace,
    Catalog:   jobCatalog,
    Sender:    postgresJobs,
    Context:   tenancy.JobContext(tenantResolver),
})

workers, err := jobs.NewWorkers(jobs.WorkersSpec{
    Namespace: jobsNamespace,
    Catalog:   jobCatalog,
    Driver:    postgresJobs,
    Identity:  tenancy.JobIdentity(tenantResolver),
    Observer:  jobs.MustWorkerObservers(applicationJobObserver, exporter),
}, consumers...)
```

The application selects `jobspg`, tenancy and `vvotel`, owns `ordersCache`,
`queue` and `workers`, and declares lifecycle explicitly. `jobs` imports none of
those optional choices; tenancy factories target neutral jobs contracts; the
OTel cache factory targets the base cache observer. A future jobs telemetry
wrapper joins the same application only after the typed handler seam gate—it
does not justify `jobsotel`.

## Cache composition constraints

### Base-owned observer fan-out preserves D-084

**Landed.** `cache.Observers`/`MustObservers` and
`jobs.WorkerObservers`/`MustWorkerObservers` are the two base-owned helpers, each
over its own event vocabulary; the memory backend keeps its distinct observer
slot. [[D-096]] is the accepted decision. The requirements below are what they
satisfy, not what they owe.

The maximum child count is eight per helper. Each helper has these mandatory
semantics:

- construction copies and validates a finite list, skips nil/typed-nil entries
  and rejects an over-limit list before runtime work;
- callbacks run synchronously in registration order;
- each child panic is isolated, later children still run and no panic changes
  the cache operation result;
- fan-out starts no goroutine, queue, retry, timer or second admission path;
- callback inputs remain value-blind and preserve the owning event vocabulary;
- the facade terminal shared-load event runs before the admitted flight is fully
  released, exactly as [[D-084]] requires;
- the callback therefore continues to consume that bounded flight slot and to
  apply backpressure; fan-out must not release the slot early or move a child
  outside it; and
- every child remains bounded and non-blocking and must not re-enter `Resolve`
  on the emitting cache. `Stats` inspection remains allowed.

`TestReviewBlockingObserverConsumesAFlightSlot`,
`TestBackgroundLeaseCoversSynchronousObserver` and the ordered multi-observer
tests in `cache/observers_test.go` and `jobs/worker_observers_test.go` are
non-negotiable regression evidence. Changing the synchronous
contract requires a separate decision that explicitly supersedes [[D-084]]; it
cannot be smuggled in as telemetry convenience.

### Exact `BatchReader` blocker

`cache.BatchReaderOf` currently discovers `BatchReader` through a
`BackendWrapper` walk. That is safe only while every traversed wrapper is known
to preserve the executable batch effect. A new generic backend middleware could
observe/intercept scalar `Get` while a walked batch call bypasses it and reaches
an inner backend directly.

Therefore generic cache backend middleware/chain work is blocked until an
accepted capability decision and executable tests make batch authority
exact-outer. The accepted shape must do one of the following without ambiguity:

1. assert `BatchReader` only on the exact outer backend; or
2. require each transparent built-in wrapper to implement and explicitly
   forward `GetMany`, while an opaque/unknown wrapper fails closed to the
   bounded scalar fallback.

The gate must prove that an unknown wrapper is never bypassed, a refusing
wrapper remains authoritative, an explicit forwarding wrapper observes the
batch call, and capability reporting agrees with executable behavior. Until
then, OTel and other extensions use the typed facade observer; no roadmap may
claim a generic cache backend decorator.

Observer fan-out is not blocked by this issue because it composes the existing
terminal hook rather than wrapping backend executable effects.

### Execution-scoped memo

**Landed** as `cache.Memo` / `cache.WithMemo`; [[D-094]] is the accepted
decision. The L0 memo is owned by `cache`, installed explicitly at an execution
entry point, and bounded by entries and retained bytes. It is not a process
global, identity map, authorization cache or replacement backend.

Application HTTP middleware installs a fresh memo for a request. Application
job entry wiring installs a fresh memo around handler execution. The jobs
package neither imports cache nor implicitly creates a memo; direct job and
cache callers continue to work without one. A scope is closed deterministically
by its owner, and `Close` empties the container, so detached work that kept the
context retains nothing.

The memo distinguishes a backend miss — never remembered, because a concurrent
writer may be filling it — from an application-confirmed clean absence, which is
stored as a negative envelope and remembered like any other. Errors, corrupt and
oversized envelopes are never remembered. It holds copied encoded envelopes, so
freshness is recomputed on every read and no two callers share a value. Loads
never consult it, and `Put`, `Forget`, `Resolve` and `ResolveMany` drop the
entry for every address they touch, which is what makes a superseded mutation
safe without a commit signal.

Transaction-local uncommitted data still enters no memo: there is no transaction
seam here yet, and until `cachepg` supplies one the rule stands unchanged — when
commit/rollback cannot be observed, mutation of the memo is skipped.

### `ResolveMany` and mutation

**Landed**; [[D-095]] is the accepted decision. `ResolveMany` reuses the cache's
typed address and budget machinery, preserves input order and duplicates,
deduplicates addresses, calls the typed `BatchLoader` at most once, validates
its answer count and presences, proves the cumulative encoded bound before the
first write and fails as a whole. It does not assume a native batch is atomic.

It deliberately does **not** join per-address flights: coalescing a batch across
callers means holding coordination state for every address across one loader
call, and that is a lock-ordering problem. Per-address `Resolve` remains the API
that coalesces. `ResolveMany` fills `Miss`; a `Stale` entry is returned stale and
refreshed by `Resolve`, because a batch loader cannot express per-key stale
fallback without the partial-failure semantics the all-or-error contract
refuses.

Mutation remains explicit. Same-source PostgreSQL cache staging may use a
cache-owned transaction hook implemented by `cachepg`. External invalidation is
performed after commit or through an application-owned outbox. There is no
`crudcache`, `jobscache` or generic cross-subsystem UoW package.

## Jobs composition constraints

### Activity, not workflow, semantics

The durable model remains:

```text
Definition -> Invocation -> Attempt 1 -> Attempt 2 -> ...
```

An attempt is physical handler execution. Admission deferral, unsupported
worker build and a claim lost before handler entry are not handler attempts.
Retries, timeouts, lease renewal, cancellation, checkpoint and schedule work
must preserve that distinction and exact terminal ownership.

Current same-ID redrive is an explicit exception awaiting a decision: it rebases
the invocation and overwrites the stored bounded attempt ledger. Before release,
choose and prove either a new invocation ID with a predecessor link, a durable
generation/history ledger under a stable logical identity, or a deliberately
accepted and documented loss/retention contract. Until then `Admin.Get` is
authoritative only for the current stored generation, not the pre-redrive
attempt trace.

Event-history replay, signals, queries, updates, child workflows, patch markers
and continue-as-new do not enter the jobs core under weaker names. A future
external-engine adapter is a genuine independently selected implementation
module and states the external engine's guarantees honestly.

### Neutral extension seams

Jobs already owns `TrustedContextProvider`, `TrustedIdentityRestorer`, durable
partition/provenance/epoch values and a `WorkerObserver` event vocabulary/config
slot. Tenancy may return implementations of the capture/restore interfaces;
`vvotel` may eventually return a typed observer or handler wrapper. Neither
warrants `jobstenancy` or `jobsotel`, and jobs never imports either extension.

`WorkerObserver` is not a handler terminal hook: its vocabulary includes point
events for worker operations such as run/drain start as well as completion and
driver steps, and the current runtime does not emit it. M0 must either wire and
freeze complete bounded emission semantics or narrow/remove the unused config
surface before an integration relies on it. Even when wired, it cannot
retroactively create a correctly parented span around handler execution. If
tracing needs handler duration and parentage, the base jobs package must first
own an explicit typed handler middleware/wrapper seam with fixed ordering,
panic/error semantics and method inventory. Add it only when a second
independent consumer or concrete conformance obligation justifies it. Do not
document tracing as an implicit step in the fixed worker pipeline before that
seam exists.

Jobs observer fan-out follows the same ownership rule: add it in `jobs` only
after two real observers need the single slot. If accepted, the base fixes
ordering, nil, panic and boundedness semantics; `vvotel` does not own the chain.

### Transactions, outbox and cache

Jobs already owns `Stager` and `TransactionContext`; a backend-specific stager
may implement that contract for its exact source when supported, and currently
`jobspg.TxStager` does. `EnqueueIn` stages a placement but does not claim the
application transaction committed. A cache backend owns only its corresponding
cache staging capability. Application UoW/outbox code coordinates business
data, staged jobs and post-commit invalidation without either subsystem
importing the other.

A job handler may call cache through an injected application service, and its
entry wrapper may install a cache memo. This is ordinary consumer composition,
not evidence for a `jobscache` package or a durable dependency from jobs to a
cache backend.

## Backend-adapter rules

Every backend/engine adapter must be a real implementation choice and meet all
of these rules:

- it imports one owning base contract plus one concrete client/SDK ecosystem;
- it does not import another cache/jobs backend, tenancy, `vvotel`, auth, a
  router, a container binding or an application module;
- its `go.mod` has an exact direct-require allow-list and no unrelated SDK;
- it exposes ordinary constructors/factories with explicit client ownership;
- import and construction do not open connections, start workers, register
  globals or mutate OTel/default SDK state;
- start/readiness/drain/close are explicit, idempotent where promised, bounded
  by caller contexts and owned by the application;
- the base conformance suite runs against it, plus live tests for the real
  service and corruption/crash/lease/outage cases that a fake cannot prove; and
- generated manifests/configuration describe selected capabilities honestly;
  unsupported optional effects fail closed or use the documented bounded
  fallback.

Executable backend capabilities such as `jobs.Admin` are selected on the exact
backend value or explicitly forwarded by a known wrapper. A generic driver
decorator does not walk through an opaque wrapper to discover administrative
authority. A forwarding wrapper preserves all four Admin methods or the
capability is absent/fails closed before I/O; it is never widened into mandatory
`Sender`, `DeliveryDriver` or `jobsfx.Backend` behaviour. `DeliveryView` contains
encoded job payload, so transport, authorization, redaction and audit policy
remain explicit application concerns rather than implicit backend middleware.

Redis cache and Redis jobs modules must have separate consumer fixtures proving
that either can be selected without the other. A Redis revocation store must
target an appropriate dependency-neutral session/token revocation contract
accepted in the owning auth/access boundary; the current notification sink is
not automatically that contract, and revocation must not reuse cache semantics
merely because both happen to use Redis.

Sharing one Redis between them is now a refusal rather than a convention.
`cache.ActivationSpec.Resources` declares which tenants a resource identity
carries, and `Activate` refuses a cache resolved onto a resource that holds
durable work or durable security; only those two may share one resource, behind
`SharedDurableSecurity(reason)` ([[D-104]]). A root that sets
`RequireDeclaredResources` refuses an undeclared resource instead of reading
silence as separation. `cachefx` now carries that activation into a
running graph and requires the declarations by default, and a Redis revocation
store or Redis jobs module is counted as a tenant without importing `cache`,
because the declaration it lives behind is a value the composition root or a
neutral group contribution supplies ([[D-111]]). What remains open is reach: nothing yet compares the
endpoint identity a Redis client was actually built with. The boot doctor now
exists for the composition root — `module.Doctor` describes a deployment profile
over the module catalog without activating it ([[D-106]]) — and collecting each
subsystem's schema, readiness and resource identity into that diagnosis is the
step this needs.

## Delivery plan

### M0 — freeze boundaries and truthful status

1. Accept the common extension ADR and record root-package versus nested-module
   placement for every current and proposed package.
2. Record source-import, direct-require and transitive dependency allow-lists
   for the root packages, `jobsfx`, and each genuine external adapter.
3. Freeze cache observer fan-out bounds and the complete [[D-084]] lifecycle,
   panic, re-entry and backpressure contract.
4. Resolve the exact-outer `BatchReader` authority before accepting a generic
   backend wrapper or chain.
5. Inventory jobs handler and observer composition needs. Add no middleware or
   fan-out only to make an OTel diagram symmetrical.
6. Record implemented/building/planned status from executable tests. Keep
   `jobspg` building until the observed durable round-trip regression is pinned
   and the complete release evidence passes.

Exit evidence:

- one reviewed module graph with every edge and prohibition above;
- current API and optional-capability inventories for cache and jobs;
- regression specifications for D-084 and exact batch authority; and
- no current roadmap/status table describes `jobspg` or another planned backend
  as ready; retained historical snapshots are visibly superseded.

### M1 — stabilize the shipped root surfaces

1. Keep cache, cachememory and cachetest conformance green under unit, race,
   cancellation, budget and callback-panic tests.
2. Keep jobs and jobsmemory green under codec, placement, attempt, recovery,
   lease, shutdown, durable-context and schedule tests.
3. Finish or explicitly narrow the current `jobspg` slice. Its record
   round-trip test, base driver conformance and live PostgreSQL suite all pass
   before the status changes from building.
4. **Done.** `jobsfx` states its deployment roles instead of reading them off
   the graph, and what an enabled role wires is a `runtime.Runner` in the
   supervisor's group rather than a goroutine the module starts ([[D-108]],
   [[D-092]]). `jobspgfx`'s retention housekeeping went the same way: `Module`
   contributes `vv.jobspg.retention` to the runner group unless the settings
   switch housekeeping off, and a container that holds the runner while no
   supervisor knows it is refused by name at start. No backend-specific Fx
   module was added, and no Fx module in `jobs` starts a goroutine of its own.

### M2 — cache composition and execution locality

1. **Done.** Bounded base-owned observer fan-out in `cache` (`cache.Observers`),
   preserving D-084 flight-slot backpressure; `jobs.WorkerObservers` is its
   counterpart. `cache/cachememory` keeps its own observer slot and still owes
   its own fan-out.
2. **Done.** The bounded execution memo (`cache.Memo`, `cache.WithMemo`); the
   application still owns entry and close wiring.
3. **Done.** `ResolveMany` over the existing address, budget and mutation
   machinery with exact budgets, duplicate ordering and all-or-error semantics.
4. Expand `cachetest` so all backend implementations prove scalar/batch
   capability honesty and callback safety.

### M3 — durable cache backends

1. Add `cache/cachepg` as a root package only if its `database/sql` contract,
   schema/readiness/cleanup and transaction behavior are justified by a real
   application profile.
2. Add a Redis cache nested module only for the concrete Redis SDK choice.
3. Prove each backend independently; do not add cache × OTel, tenancy, auth,
   jobs or container packages.

### M4 — jobs hardening and composition

1. Complete `jobspg` conformance and live crash/lease/recovery evidence.
2. Add `jobs/jobstest` only if helpers cannot live cleanly in the root tests and
   at least two backend implementations consume the public suite.
3. Add a typed handler wrapper/chain only if two independent consumers and an
   ordering/capability matrix justify it.
4. Wire and prove the existing `WorkerObserver` operation vocabulary, or narrow
   the unused config surface, before adding an adapter; add fan-out only after
   two real observers require it.
5. Keep hardening the landed scheduler, including panic containment,
   no-overlap and deterministic occurrence/restart evidence.
6. Keep tenancy restoration and OTel instrumentation as independent consumer
   profiles over base-owned seams.

### M5 — advanced delivery without hidden workflow claims

1. Add checkpoints, continuations, batches/chains and dependencies only with
   bounded state, crash points, cancellation and exact attempt semantics.
2. Harden the committed `jobs.Admin`/`jobspg` inspect, redrive and terminal-purge
   slice with authorization-facing, payload-exposure, concurrency, retention and
   live crash evidence. Freeze redrive identity, predecessor/generation and
   history-retention semantics; same-ID ledger replacement is not release-ready
   by accident. Give List a bounded summary projection or explicit total-byte
   budget and continuation so it never materializes an item-count-multiplied
   worst-case payload. Additional retention/operator verbs and outbox/inbox
   remain future work and require one durable owner plus explicit application
   transaction composition.
3. Stabilize the current Redis jobs module only as a genuine jobs implementation
   and prove it is selectable without any Redis cache module.
4. If demanded, add a thin external workflow-engine adapter instead of
   extending the jobs core into an incomplete replay engine.

## Dependency and consumer gates

| Gate | Required proof |
|---|---|
| Root dependency | With `GOWORK=off`, `go list -deps` for `cache/...` and dependency-neutral `jobs/...` contains only standard library and `github.com/frostgrove/vv/...`; root packages have no nested `go.mod` |
| Source imports | AST/import scan enforces a per-package allow-list; root cache/jobs never import `vvotel`, tenancy or an external adapter; an adapter imports only its owner plus approved SDK |
| Direct requirements | Each nested adapter `go.mod` has exactly the owning Frostgrove module and its declared ecosystem dependencies; no subsystem-combination requirement appears |
| Transitive graph | A stored normalized `go list -deps`/`go mod graph` allow-list detects a new SDK family, extension edge or provider introduced indirectly |
| Extension independence | Graph scan rejects extension-to-extension and adapter-to-adapter imports, including forbidden `jobsotel`, `jobstenancy`, `cacheotel` and `cacheredisotel` paths |
| Redis independence | Two `GOWORK=off` consumer fixtures select Redis cache only and Redis jobs only; neither dependency graph contains the other module |
| Base-only consumers | Fresh `GOWORK=off` fixtures import cache-only, jobs-only and both root packages without OTel, tenancy, Redis, Fx or workflow SDK modules in source/direct/transitive graphs |
| Extension consumers | Separate fixtures select `vvotel`, tenancy, and both together through application wiring; production extensions never import each other |
| Workspace integrity | `go.work` entries equal real nested modules, excluding documented example/test-only modules; root packages are absent; committed nested modules have no local `replace` |
| Isolated PostgreSQL tests | Tagged jobspg live conformance resolves and runs under `GOWORK=off` from the module that holds it, whose `go.mod` names the driver; a workspace-supplied pgx import or skipped DSN is not evidence, and root `go.mod` stays third-party-free |
| Additive package count | Repository scan rejects subsystem × extension/backend/container combination directories and records the justification for every new `go.mod` |
| Capability honesty | Method inventory and executable matrices prove exact forwarding/refusal/fallback for every optional effect; unknown wrappers cannot tunnel to inner `BatchReader` or `jobs.Admin` authority |
| D-084 safety | Blocking/panicking/ordered fan-out tests prove the terminal shared-load callback stays synchronous before full flight release, consumes the admitted slot, adds no goroutine/queue/admission and cannot suppress cleanup or later children |
| Jobs context safety | Durable capture/restore tests prove protected references, provenance and epoch are revalidated; no raw tenant/auth/context value becomes routing authority |
| Redrive evidence | Identity/predecessor or generation semantics are explicit; tests prove what attempt/outcome history survives redrive, concurrent redrive and each crash/commit window |
| Admin resource bound | List returns a bounded summary or enforces explicit aggregate bytes/continuation; tests use maximum records/payloads and prove no count × record-size materialization blow-up |
| Lifecycle | Import/construct/start/readiness/drain/close tests prove no hidden globals, goroutines or connection ownership; cancellation and repeated close are deterministic |
| Backend release | Unit/conformance/race tests and a live real-service suite pass for each backend; a directory or schema alone is not release evidence |

These are three different dependency assertions and all are required:

1. a **source-import** gate catches a forbidden code edge even when a dependency
   is already present elsewhere in the build list;
2. a **direct-require** gate catches an optional SDK named unnecessarily in one
   module's `go.mod`; and
3. a **transitive-graph** gate catches an optional ecosystem pulled through an
   apparently acceptable direct dependency.

`scripts/checks.sh` currently enumerates only a subset of subsystems. M0 must
either include cache/jobs in the same checks or add an equivalent authoritative
gate; a green script that never inspected these packages is not evidence.

## Conformance profiles

The matrix is test topology, not production package topology:

| Profile | Mandatory evidence |
|---|---|
| Cache base | typed address/codec/policy, budgets, lookup/mutation, shared-flight cancellation, D-084 observer lifecycle |
| Cache memory | byte/entry bounds, expiry, eviction, callback behavior and cachetest conformance |
| Cache PostgreSQL | real schema/readiness, transaction identity, cleanup and live driver conformance |
| Cache Redis | independent module graph, real server outage/timeout/size/TTL behavior and live conformance |
| Jobs base | definitions/codecs, enqueue/stage, invocation/attempt ledger, redrive identity/provenance/history-retention decision, context restore, schedules, worker shutdown |
| Jobs memory | deterministic virtual-time delivery/recovery and jobs conformance |
| Jobs PostgreSQL | durable record round-trip, fencing, claim/recover/renew/apply, transaction staging, exact Admin Get/redrive/purge plus byte-bounded/continued List, terminal/intent conflicts, redrive generation/history and crash windows, payload non-serialization plus application-authorization fixture, and isolated live crash tests |
| Jobs Redis | independent module graph, real server lease/recovery/atomicity behavior and live conformance |
| Jobs + tenancy | unpublished consumer fixture with injected neutral provider/restorer; stale lifecycle/epoch refuses before handler |
| Jobs/cache + OTel | unpublished consumer fixture using one `vvotel` module and base-owned seams; no subsystem OTel package |
| Jobs + cache | application entry installs bounded memo explicitly; neither root package imports the other |

## Current status and definition of done

| Surface | Current status |
|---|---|
| `cache`, `cache/cachememory`, `cache/cachetest` | Implemented base packages; preserve their current tests and accepted decisions |
| Cache observer fan-out, execution memo, `ResolveMany` | Implemented in `cache`; see [[D-094]], [[D-095]], [[D-096]]. `cache/cachememory`'s own backend-event fan-out remains planned |
| Cache capability set beyond `BatchReader` | Implemented: five more built-in interfaces plus a driver-declared set; see [[D-093]] |
| Cache and jobs `Check` probes | Implemented as neutral seams the composition root wraps; see [[D-096]] |
| `cachepg`, Redis cache | Planned; no shipped package/module yet |
| `jobs`, `jobs/jobsmemory` | Implemented base surface with continuing hardening; `WorkerObserver` vocabulary, config and runtime emission all exist, and several observers compose through `jobs.WorkerObservers` |
| `jobs.Admin`, `DeliveryView`, redrive transition | Implemented dependency-neutral contract; only `jobspg.Driver` implements it and memory/Redis do not. `jobsfx.Backend` does not require or manufacture it; `AsBackend` republishes the constructor's declared result type through `fx.Self`, so a concrete driver remains selectable only when that is the declared result and every `jobs.Admin` binding stays explicit. Current List is count-bounded but not byte-bounded, and same-ID redrive replaces the stored attempt ledger |
| `jobs/jobspg` | **Building**; Get/List/Redrive/PurgeTerminal controls are committed alongside sender/worker/staging, but List byte bounds, redrive provenance/history, clean base conformance, authorization-facing/payload-safety review and isolated live PostgreSQL/crash evidence remain incomplete; the tagged fixtures now resolve pgx from the module's own `go.mod` instead of the workspace |
| `jobs/jobsfx` | Present; lifecycle audit done — `Spec.Consuming` and `Spec.Scheduling` name the deployment roles, an unstated role over a container that holds a consumer or a schedule is refused, and the worker pool and scheduler are supervised runners ([[D-108]]). Common-ADR classification remains |
| `jobs/jobspg/jobspgfx` | Present; retention housekeeping is the supervised runner `vv.jobspg.retention` rather than a goroutine its own start hook launches, and `RetentionRunner` is the explicit constructor under `Module` ([[D-092]]) |
| `jobs/jobsredis` | **Building** committed independent backend module; isolated unit tests are green, but published dependency/no-`replace`, base conformance, live Redis and crash/lease gates remain |
| External workflow adapter | Planned |
| `jobsotel`, `jobstenancy`, `cacheotel`, `jobscache` | Forbidden, not backlog items |

The roadmap is complete only when:

- base cache and jobs remain independently usable without optional modules;
- every optional external backend is independently selectable and its full
  source/direct/transitive dependency graph matches the allow-list;
- no root, adapter or extension edge creates a subsystem cross-product;
- cache fan-out demonstrably preserves D-084's bounded flight-slot backpressure;
- generic backend decoration remains absent until exact `BatchReader` authority
  is resolved and executable;
- jobs, cache, tenancy and one `vvotel` module compose only in the application or
  unpublished consumer tests;
- live backend suites, crash/cancellation matrices, method inventories,
  lifecycle tests and privacy scans pass; and
- status tables are regenerated from evidence. In particular, intermediate
  failing/green/build-changing scoped runs are not readiness; clean-revision
  base conformance plus live PostgreSQL/crash evidence remains required, and
  the committed Redis adapter remains building until its independent live and
  release gates pass.
