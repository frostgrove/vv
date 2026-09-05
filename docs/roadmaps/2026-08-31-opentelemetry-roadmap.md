# OpenTelemetry roadmap — 2026-08-31

**Status:** implementation landed in the working tree; publication remains gated
by the lockstep root/`otel` consumer check. Accepted [[D-048]] continues to
refuse a shared framework telemetry contract.

**Architecture:** governed by the
[2026-09-01 optional-extension architecture](2026-09-01-extension-architecture-roadmap.md),
including its linear-composition rule and migration treatment for pre-existing
combination satellites.

**Supersedes:** the delivery assumptions in
[the 2026-08-26 snapshot](2026-08-26-1558-opentelemetry-roadmap.md). That file
remains a historical record. For observability work, this revision also
replaces the illustrative F-27/F-28 API and naming in the dated product
roadmap; it does not rewrite that snapshot.

This revision starts from the current Frostgrove tree rather than from the
subsystems imagined by the earlier plan. Storage and cache now have concrete
contracts, and the service and CRUD surfaces have grown. It also resolves a
conflict in the dated product plan: F-27 proposed a shared neutral observer,
while accepted [[D-048]] refuses a framework telemetry contract because
OpenTelemetry already owns that ecosystem contract. The accepted ADR wins.

The first useful release is intentionally smaller than the old 24-card plan:
freeze one safe Frostgrove schema, add one optional Frostgrove OTel module with
reusable factories and typed decorators for current base seams, and adapt
existing subsystem-local cache events. Every module other than the explicitly
selected `otel/` extension remains usable without OTel.

## Decision in one page

1. Frostgrove does not add a shared root `observe` contract. Typed hooks stay
   local to a subsystem when that subsystem needs them, as cache and authjwt
   already demonstrate.
2. One optional cross-cutting module represents the consumer decision “use
   Frostgrove's OpenTelemetry integration.” The working layout is `otel/` with
   import path `github.com/frostgrove/vv/otel` and package name `vvotel`; M0
   records the final path/name and its narrow ADR amendments.
3. Existing base and satellite modules never import `vvotel` or OTel and expose
   no OTel types. The dependency arrow is one-way: `vvotel` may import stable,
   dependency-light root seams, while a consumer that does not import `vvotel`
   gets no OTel module graph.
4. Base packages own typed, dependency-neutral extension points. Existing
   `crud.Middleware`, `crud.Chain`, `cache.Observer` and backend options are the
   model; M0/M1 add only the missing service/storage decorator and observer
   composition points that at least two independent extensions can reuse.
5. `vvotel` exposes one configuration/factory surface and returns ordinary
   base middleware, decorators and observers. Package-level generic factories
   are used where Go cannot express generic methods. It neither registers
   itself globally nor requires a framework-wide `Extension` registry.
6. The first release puts the reusable integration boilerplate in `vvotel`:
   `vvotel.New` borrows already-built providers and typed factories build the
   actual service/storage decorators and cache observers. It never mutates OTel
   globals or shuts down borrowed providers. Application SDK bootstrap is shown
   in a runnable helper example; a production runtime factory is permitted as a
   later, explicit dependency/lifecycle decision rather than smuggled into M0.
7. Optional integrations compose linearly at base seams. `vvotel` does not
   import `authjwt`, `storageminio`, Gin, Fiber, gRPC, pgx, Redis or another
   extension, and packages such as `tenancyotel`, `eventsourceotel`,
   `storageminiootel` or any combination of them are not created.
8. The first release covers command spans, honest storage spans and an opt-in
   bounded cache-event counter. It does not create default repository spans,
   fake cache spans, OTel logs, or jobs propagation/spans before the current
   jobs lifecycle has its own accepted, dependency-neutral instrumentation
   seam and conformance evidence.
9. Standard transport and client instrumentation remains authoritative:
   `otelhttp`/`otelgrpc` at transport edges and application-selected
   database/remote-client instrumentation below Frostgrove.
10. Telemetry is diagnostic evidence, never audit evidence, authorization
   state, a retry trigger or a second source of business truth.

The intended flow is:

```text
application composition root
          |
          +--> base service
          |      -> tenancy.Service(...)
          |      -> audit.Service(...)
          |      -> vvotel.Service(factory, ...)
          |
          +--> base store -> audit.Store(...) -> vvotel.Store(factory, ...)
          |
          +--> cache.Observers(appObserver, vvotel.Cache(factory, ...))
          |
          +--> HTTP/gRPC + driver/client instrumentation selected upstream

vvotel.Factory --> injected OTel API providers --> application-owned SDK runtime
                                                     |
                                                     v
                                          exporter / Collector / backend
```

Each lifecycle decorator passes the context returned by
`trace.Tracer.Start` to the wrapped operation. Otherwise repository, driver or
remote-client spans produced during the operation cannot become its children.

## What changed since the previous snapshot

| Previous assumption | Current fact | Consequence |
|---|---|---|
| Product roadmap F-27 proposes one neutral observer before OTel | Accepted [[D-048]] refuses a framework-wide telemetry contract; current hooks are subsystem-local | Let `vvotel` directly decorate typed base seams and adapt subsystem-local hooks without inventing a common observer |
| A package per subsystem × OTel looks maximally isolated | The owner defines OTel as one optional ecosystem choice and explicitly rejects a package cross-product | Use one `vvotel` module over root seams; M0 narrowly amends [[D-035]], [[D-051]], [[D-058]] and [[D-074]], while [[D-033]], [[D-036]] and [[D-048]] remain intact |
| The application must implement each Frostgrove decorator and OTel mapping itself | One optional integration can own that reusable boilerplate while the host still owns SDK/export policy | Add `vvotel.New`, typed decorator/observer factories and a runnable explicit-lifecycle SDK example |
| `port.Service` has seven observed verbs | It now has `List`, `Count`, `Get`, `Create`, `Update`, `Replace`, `Delete`, `DeleteMany`, plus optional restore | Totality and optional-capability tests use the real surface |
| Repository instrumentation targets a short `port.Repository` list | The persistence seam is the larger `crud.Core`; native batch and restore capabilities are explicit | Default repository spans are deferred; any future wrapper must preserve every capability |
| Query options may be inspected by a decorator | Public calls take executable `...crud.Option` functions | Options are resolved exactly once; telemetry cannot replay them to infer query shape |
| Storage is a future `put/get/head/delete` abstraction | `storage.Store`, filesystem and MinIO implementations exist with ten methods | Instrument the real surface; `open` is not `get`, and stream lifetime is explicit |
| Cache is future work | `cache` and `cachememory` expose typed terminal events | Reuse them for counters; they do not contain enough lifecycle data for spans or latency |
| Jobs/outbox/events are near-term instrumentation targets | Jobs now has Definition → Invocation → Attempt contracts and active driver/worker work; event sourcing/outbox remain proposals | Jobs propagation and messaging spans wait for completed lifecycle/conformance evidence and a justified base-owned typed seam |
| One semantic-convention stability label is enough | Groups and individual attributes declare maturity independently | Pin and review every adopted item separately |
| Go traces, metrics and logs can share one maturity claim | In Go 1.46.0 traces and metrics are Stable; logs are Beta | No OTel Logs dependency or helper in the first release |
| Application chooses `InstrumentationName` | Instrumentation scope identifies the library producing telemetry | One scope name/version comes from the final `vvotel` path/version; service identity stays in application Resource |
| A call counter and duration histogram are both required | A histogram already exposes a count | Start with duration; add another counter only for an operator question the histogram cannot answer |

## Current baseline

### Frostgrove

| Surface | State on this snapshot | Observation seam |
|---|---|---|
| Root module | No OpenTelemetry dependency | Must remain unchanged |
| `port.Service` | Implemented, including bulk delete and optional restore | `vvotel.Service` decorates the seam without OTel types in root |
| `crud.Core` | Implemented with `Meta` plus fifteen operation methods | Middleware exists; no observation contract |
| `remote`/`remotehttp` | Implemented; caller can inject an HTTP client | Upstream `otelhttp` can own client spans |
| `storage`, `storagefs`, `storageminio` | Implemented | `vvotel.Store` decorates the seam; backend/client spans remain external |
| `cache` | Implemented/in progress with typed terminal events; built-in emitters use bounded known values | `cache.Observer.Observe` |
| `cachememory` | Implemented/in progress with its own typed terminal events; built-in emitters use bounded known values | `cachememory.Observer.Observe` |
| `authjwt` | Has a narrow degraded-JWKS observer | Point event only |
| `jobs` | Core contracts, durable context, scheduling, Admin/redrive contracts, PostgreSQL operator controls, memory/PostgreSQL drivers and worker execution have landed; Admin List is count-bounded but lacks an aggregate byte budget; Redis is a committed building backend module; `WorkerObserver` vocabulary/config exists but runtime emission is not wired | No first-release `vvotel` adapter; Admin is not a handler-lifecycle seam, so reassess only after clean conformance/live-backend gates and a justified typed lifecycle seam |
| Event sourcing, outbox, broker eventing | Not implemented in the framework | Defer |
| Shared `observe` package | Refused by current [[D-048]] | Not planned |
| Optional `vvotel` module | Implemented in working tree; not yet published | Pass strict `GOWORK=off` consumer gate, then publish lockstep tags |

The cache facade and memory backend are different observation layers. A single
public call may emit multiple facade phases and touch the backend. Their event
counters keep operation and layer explicit; dashboards do not sum them as one
“public cache calls” series.

### OpenTelemetry ecosystem

This table records the review baseline, not a promise to import every listed
component.

| Component | Current released baseline | Relevance |
|---|---:|---|
| [OpenTelemetry specification](https://github.com/open-telemetry/opentelemetry-specification/releases/tag/v1.60.0) | 1.60.0 | Signal and library requirements |
| [Semantic Conventions](https://github.com/open-telemetry/semantic-conventions/releases/tag/v1.44.0) | 1.44.0 | Attribute/span/metric vocabulary |
| [OpenTelemetry Go](https://github.com/open-telemetry/opentelemetry-go/releases/tag/v1.46.0) | 1.46.0 GA | Traces Stable, Metrics Stable, Logs Beta |
| [OpenTelemetry Go contrib](https://github.com/open-telemetry/opentelemetry-go-contrib/releases/tag/v1.46.0) | 1.46.0 release; instrumentation line 0.71.0 | Transport/client examples only |
| [OTLP/proto](https://github.com/open-telemetry/opentelemetry-proto/releases/tag/v1.11.0) | 1.11.0 | Application/exporter concern |
| [Collector distributions](https://github.com/open-telemetry/opentelemetry-collector-releases/releases/tag/v0.159.0) | 0.159.0 | Deployment concern, outside the library |

`opentelemetry-go` 1.47.0-rc.1 exists, but an RC is not a production or
compatibility baseline. Revisit Go Logs after a GA release and only if a
Frostgrove-specific operator question remains after normal `slog` correlation.

Semantic-convention groups and individual attributes have independently
declared maturity:

| Group | Status relevant to this plan | Rule |
|---|---|---|
| [Database](https://opentelemetry.io/docs/specs/semconv/db/) | Mixed; [database client spans](https://opentelemetry.io/docs/specs/semconv/db/database-spans/) are Stable | Prefer driver/client spans; do not duplicate them |
| [Messaging](https://opentelemetry.io/docs/specs/semconv/messaging/) | Development | Do not freeze public jobs/outbox telemetry against it yet |
| [Object stores](https://opentelemetry.io/docs/specs/semconv/object-stores/) | Development | Use a versioned Frostgrove logical schema; review migration on each bump |
| [`error.type`](https://opentelemetry.io/docs/specs/semconv/registry/attributes/error/) | Stable registry attribute; the general recording-errors guidance is Development | Present only on failed operations and kept consistent across spans/metrics |

No document may say simply “semantic conventions are stable.” A registry entry
records the exact upstream group, version and maturity used for that field.

Primary design references:

- [instrumentation libraries use the API and leave SDK ownership to the
  application](https://opentelemetry.io/docs/concepts/instrumentation/libraries/);
- [OpenTelemetry Go manual instrumentation](https://opentelemetry.io/docs/languages/go/instrumentation/);
- [instrumentation scope identity](https://opentelemetry.io/docs/concepts/instrumentation-scope/);
- [how to write semantic conventions](https://opentelemetry.io/docs/specs/semconv/how-to-write-conventions/);
- [general naming rules](https://opentelemetry.io/docs/specs/semconv/general/naming/);
- [W3C Trace Context](https://www.w3.org/TR/trace-context/).

## Architectural boundary

### One cross-cutting OTel module

The working layout is one public package in one published module:

```text
otel/                         MODULE github.com/frostgrove/vv/otel
  go.mod
  doc.go                     package vvotel
  telemetry.go               shared configuration/factory
  service.go                 port.Service decorator factory
  storage.go                 storage.Store decorator factory
  cache.go                   cache.Observer adapter factory
  cachememory.go             cachememory.Observer adapter factory
  repository.go              only after separate approval
  schema_gen.go              generated schema/mapping constants
  internal/                  non-public implementation support
```

The path and names are working decisions until M0 accepts the ADR. `vvotel` is
the expected package qualifier because bare `otel` collides with the upstream
package and no single subsystem can supply a prefix. Splitting the files above
into `crudotel`, `storageotel`, `cacheotel` or provider-specific public packages
would recreate the rejected cross-product.

The dependency direction is:

```text
vvotel ----imports----> OTel-free root seams
   |
   +------imports----> accepted OpenTelemetry API profile
   |
   +----------X------> optional Frostgrove satellites

root and non-OTel modules ---X------> vvotel or OpenTelemetry
```

The root and every published module other than `otel/` remain OTel-free, expose
no OTel types and never import `vvotel`. `vvotel` may import dependency-light seams from
the root module: initially `port`, `storage`, `cache` and `cache/cachememory`;
`crud` enters only if the deferred repository adapter is approved. It may not
import `authjwt`, `storageminio`, Gin, Fiber, gRPC, pgx, Redis, Fx or another
optional extension. Logical storage telemetry wraps `storage.Store`;
HTTP/gRPC/database/backend telemetry composes through the upstream integration
selected by the application.

Importing `vvotel` deliberately selects the whole Frostgrove OTel integration.
Go compiles the package's files, so the roadmap does not pretend that a
storage-only caller avoids compiling command/cache adapter code. The guarantees
are instead: no OTel at all for a non-importer, one external ecosystem decision
for an importer, and no signal or wrapper activated until its factory is called.

M0 records a narrow “one ecosystem choice may adapt several base seams” ADR:

- [[D-033]] and [[D-036]] remain intact because OTel still has its own module
  and the root remains third-party-free;
- [[D-048]] remains intact because `vvotel` is an OTel implementation, not a
  new framework telemetry contract;
- [[D-035]] is amended to record `vvotel` as the collision case with no single
  subsystem prefix;
- [[D-051]] is amended to define the consumer decision here as the Frostgrove
  OTel integration across base seams, while still forbidding OTel plus an
  unrelated router/backend/provider choice;
- [[D-058]] gains one explicit cross-cutting-extension exception for `otel/`;
- [[D-074]] must be narrowed by the common architecture ADR: an Fx binding may
  target its owning dependency-neutral seam, while concrete adapter × container
  bundles such as `storageminiofx` and the current product-level `appfiber`
  graph become explicit compatibility/migration cases.

This exception does not create a top-level `extensions/` dumping ground. A
future cross-cutting module needs its own ADR and must connect one ecosystem
choice only to dependency-light base seams. Extension-to-extension imports and
combined names such as `tenancy_eventsource_otel_jwt_redis` are forbidden.

The growth rule is linear: one module represents one independently selectable
extension/ecosystem and may provide several typed adapters to stable base seams.
Adding tenancy, event sourcing and OTel therefore adds those choices, not every
intersection among them. Cross-extension behaviour is expressed by decorator
order at the application composition root or by a new neutral base seam; it is
never published as a bridge or combination package.

### Base-owned extension points and linear composition

The base packages, not OTel, own the shapes at which optional behaviour stacks:

| Seam | Current entry point | M0/M1 action |
|---|---|---|
| `crud.Core` | `crud.Middleware` and `crud.Chain` | Reuse; every decorator preserves discovery and explicitly decides optional effects |
| `port.Service` | Direct interface wrapping only | Add a typed service middleware/decorator and chain helper for OTel, tenancy, event-source and other independent layers |
| `storage.Store` | Direct interface wrapping only | Add a typed middleware/decorator and chain helper for OTel, audit and other independent layers |
| Cache facade | One `cache.Observer` slot | Add deterministic observer composition so application and integrations can coexist |
| `cachememory` backend | One `WithObserver` option | Add the equivalent typed composition without merging facade/backend events |
| `auth.Authenticator` | Interface plus fallback-oriented `auth.Chain` | Do not overload fallback order; add a decorator type only when a second wrapping use exists |

Every new point uses only its subsystem vocabulary and the standard library.
There is no generic `Extension`, `Install`, service locator, `map[string]any` or
global registry. Application code chooses order explicitly. Illustrative DX,
with names to be frozen in M0:

```go
telemetry, err := vvotel.New(vvotel.Config{
    TracerProvider: tracerProvider,
    MeterProvider:  meterProvider,
})

service := port.ChainService(
    baseService,
    tenancy.Service[Model, ID, Update](tenantConfig),
    vvotel.Service[Model, ID, Update](telemetry, serviceConfig),
)
store := storage.Chain(
    baseStore,
    audit.Store(auditConfig),
    vvotel.Store(telemetry, storeConfig),
)
runtime.Observer = cache.Observers(
    runtime.Observer,
    vvotel.Cache(telemetry, cacheConfig),
)
```

This is shape, not accepted API, and `tenancy`/`audit` stand for independent
extensions rather than required packages. Package-level generic factories are
intentional: Go does not permit a non-generic `Telemetry` value to have generic
methods.

All base chain APIs share these rules:

- the first listed middleware is outermost and nil middleware is skipped;
- observer fan-out follows registration order and isolates each child panic so
  one optional observer cannot suppress later observers or alter the operation;
- generic factories are instantiated at the typed binding; heterogeneous
  decorators are not erased into `any` or reflection;
- navigation, identity and descriptive capabilities may use an explicitly
  bounded declared walk;
- executable optional effects are visible only on the exact outer layer and
  must be forwarded/observed explicitly or fail closed;
- when method-set discovery itself promises availability, as with service
  restore, an honest wrapper uses two concrete dynamic types or a provider
  returning `(capability, bool)`; it does not always advertise the method and
  later say unsupported;
- optional executable CRUD effect verbs follow [[D-030]] and [[D-061]] instead:
  an outer wrapper explicitly implements and enforces/forwards the verb or
  fails closed before I/O, and lookup never tunnels through an unknown layer;
  this rule does not require a combinatorial wrapper type per effect subset;
- embedding a base interface carries a D-030-style method inventory and a
  capability matrix across opaque neighbours and both decorator orders.

The first OTel release adds no generic cache backend middleware. The current
cache backend discovery walks to executable `BatchReader`, which could bypass an
unknown tenancy/encryption/telemetry layer. Observer fan-out is safe; a future
backend chain first needs a separate exact-outer/fail-closed capability decision
analogous to [[D-061]].

Decorators must preserve the whole base interface, optional capabilities,
discovery and error identity. An integration-specific lifecycle that cannot be
expressed through a dependency-light base seam is deferred or motivates a
neutral subsystem hook; it does not motivate a pairwise package. For example,
`vvotel` does not import `authjwt` merely to adapt its JWKS event.

[[D-048]] still refuses a shared `observe` contract. Subsystem-local typed hooks
remain legitimate because they describe cache, auth or another domain rather
than traces/metrics/logs. A future shared observer still requires a separate ADR
that explicitly amends D-048.

### Lifecycle decorators and point hooks

There are two instrumentation shapes:

```text
decorator call: start span -> pass derived context -> call next -> end span
local hook:     Observe(ctx, typed terminal event)
```

The decorator shape represents work with duration and nested calls. The local
hook represents a point occurrence such as a cache hit or a degraded JWKS
refresh. A terminal event is not retroactively converted into a zero-duration
span or latency measurement.

Every implementation must satisfy these rules:

- operation, outcome, component and failure values come from the closed schema;
- there is no `map[string]any`, arbitrary attribute callback or public
  string-label escape hatch;
- the context returned by `trace.Tracer.Start` is passed to the wrapped call;
- the span ends exactly once whenever the wrapped call returns or panics,
  including a return caused by cancellation; no end is promised while the
  underlying call remains blocked;
- a panic ends the span with a closed safe outcome and is then re-panicked
  unchanged;
- duration is measured once;
- standard transport/client instrumentation is composed without duplicate
  sibling spans;
- sampling is an application SDK decision and does not suppress metrics;
- provider/observer panic cannot replace the wrapped call's result;
- point-hook callbacks preserve the lifecycle contract owned by their base
  subsystem. In particular, the shared-flight terminal load observer stays
  synchronous and runs before its admitted flight is fully released under
  [[D-084]]; adapters neither release that slot early nor move work to an
  unowned goroutine. Other callbacks must not extend unrelated transient or
  cleanup-critical leases beyond the base contract.

`vvotel` makes synchronous calls through injected OTel API interfaces.
An application-supplied provider or synchronous processor can block and may
export during `Span.End`; Frostgrove cannot bound that latency. Per-call timeout
goroutines would leak under a stuck provider and are forbidden. The enforceable
boundary is that instrumentation adds no coordination capacity. A point-hook
adapter acquires none beyond what the owning base callback already holds: the
D-084 terminal load deliberately retains its already admitted flight slot until
every bounded synchronous child returns. No helper silently changes
process-global state.

### Factory, helpers and lifecycle

The first required convenience layer is API-only and removes Frostgrove wiring
boilerplate rather than application policy. One immutable, concurrency-safe
handle:

- accepts `trace.TracerProvider` and `metric.MeterProvider` explicitly;
- validates shared schema/privacy configuration once;
- creates the fixed instrumentation scope and lazily creates only instruments
  requested by a decorator/observer factory;
- produces typed service/storage middleware and cache/cachememory observers;
- permits each signal to be disabled explicitly rather than obtaining a global
  provider as a fallback;
- never calls `otel.SetTracerProvider`, `otel.SetMeterProvider` or a global
  propagator setter;
- returns constructor errors for invalid schema/configuration instead of
  silently falling back.

The first release uses the lean profile: `vvotel.New` accepts existing providers
and production source imports the OTel trace/metric APIs only. It borrows those
providers. It never discovers `Shutdown` by type assertion, never closes them
and never returns lifecycle ownership for objects it did not create. A runnable
unpublished example demonstrates Resource, SDK, reader/processor, exporter and
shutdown assembly, so consumers reuse Frostgrove instrumentation rather than
reimplementing it while retaining application policy.

A production `NewRuntime` helper is not forbidden forever. Adding it requires a
separate ADR because SDK requirements enter `otel/go.mod` for every `vvotel`
importer, even when the helper is not called. If accepted, it stays in the same
public `vvotel` package, accepts explicit caller policy, owns only objects it
creates and returns explicit idempotent shutdown for them. It sets no globals,
reads no ambient environment and selects no exporter protocol implicitly.
Putting it in an unimported subpackage or behind a build tag does not isolate
module requirements. Concrete exporter presets remain application-owned or in
an unpublished example; they do not enter production `vvotel` or create another
public package/module.

Production code in the first release does not depend on an OTel SDK, exporter,
`otelhttp`, `otelgrpc`, a database driver, router, broker, cloud SDK, optional
Frostgrove satellite or logging bridge. M0 records exact source-import and
direct-`require` allow-lists. The full transitive graph is reviewed separately:
a dependency pulled by the pinned OTel API release is not mislabelled as a
direct Frostgrove choice, and `go mod why -m` plus dependency diffs expose its
origin and later changes.

M0 fixes one instrumentation scope from the final module import path:

```text
name:    github.com/frostgrove/vv/otel (or the ADR-selected final path)
version: the vvotel module version
```

The application name and version belong in OTel Resource attributes such as
`service.name` and `service.version`; deployment identity belongs in
`deployment.environment.name`. They are not duplicate `vv.*` attributes.

`SchemaURL` stays empty until Frostgrove publishes an immutable, retrievable
versioned schema URL. A local identifier such as `vv-otel/v1` is a telemetry
contract version, not an OTel schema URL.

## Signal ownership and topology

| Layer | Integration owner | Initial signal | Span kind / ownership | Disposition |
|---|---|---|---|---|
| Inbound HTTP/gRPC | Application-selected instrumentation | Standard server span | SERVER, upstream instrumentation | Document composition; do not wrap again |
| `port.Service` command | `vvotel` service decorator | Lifecycle span; duration histogram in M3 | INTERNAL, Frostgrove | First release |
| `crud.Core` | Future `vvotel` repository middleware | None by default | A future logical span would be INTERNAL | Deferred pending an operator question |
| SQL/database driver | Application-selected instrumentation | Standard database client span | CLIENT, driver instrumentation | Reuse; [[D-062]] applies |
| `remotehttp` transport | Application-injected `otelhttp` client | Standard HTTP client span | CLIENT, upstream instrumentation | Integration example |
| Cache facade | `vvotel` cache observer | Terminal event-occurrence counter; optional event on an already recording span | No standalone span from terminal data | First metrics slice after safety gate |
| `cachememory` backend | Separate `vvotel` observer factory | Separate terminal event counter if explicitly enabled | No standalone span | Optional; keep layer distinct or omit the factory |
| `storage.Store` | `vvotel` storage decorator | Logical span whose span duration covers the method call | INTERNAL; backend/client instrumentation owns CLIENT | First release; duration histogram deferred |
| Policy/security | No adapter until a typed subsystem seam exists | Command outcome only | No named policy span | Deferred |
| JWKS degradation | No adapter until a generic auth seam exists | Point event/counter | No fake refresh span from terminal data | Deferred; do not couple OTel to JWT |
| Jobs/outbox/broker | Future `vvotel` adapter only after a dependency-light typed lifecycle seam exists | None | Jobs contracts exist but are not release-ready and lack the required handler seam; outbox/broker contracts do not exist | Deferred |
| Logs | Application-owned `slog`/bridge | Existing application logs may correlate with active trace | No Frostgrove span | No Frostgrove OTel Logs API |

An INTERNAL Frostgrove span describes a logical operation. It does not claim to
be a network CLIENT span. This prevents the command → repository → SQL stack
from showing three nearly identical client spans and avoids falsely applying a
protocol convention at the wrong layer.

## Versioned telemetry schema

M0 creates a checked-in machine-readable registry for the emitted OTel schema
and generates mapping constants into `vvotel` plus reference documentation.
Subsystem-local enums such as `cache.Operation` remain the source facts of their
own packages. Because exported Go string types can receive values not emitted by
the built-in implementation, each generated mapping is total and tested over
known and unknown inputs: unknown values collapse to a bounded fallback or are
omitted, and never become raw attribute values. Hand-written OTel names in
multiple adapter files are not the source of truth.

Every registry entry contains:

- Frostgrove contract version;
- signal and instrument/span/event name;
- type, unit and description;
- exact allowed values or the declaration that bounds them;
- whether it may appear on a metric;
- privacy class;
- source subsystem and operation;
- upstream semantic-convention group, version, maturity and migration note;
- deprecation/replacement metadata.

New Frostgrove conventions begin in development. A release may stabilize them
only after integration evidence and one real consumer query. An upstream
semconv upgrade never silently changes emitted names in a patch release.

### Candidate spans

Names are low-cardinality and do not contain resource names, IDs or routes.
The M0 registry may refine them.

| Name | Allowed operations | Notes |
|---|---|---|
| `vv.command <operation>` | `list`, `count`, `get`, `create`, `update`, `replace`, `delete`, `delete_many`, `restore`, `restore_many` | INTERNAL; optional operations exist only when the wrapped service exposes them |
| `vv.storage <operation>` | `put`, `open`, `head`, `delete`, `stage`, `promote`, `abort`, `cleanup_expired`, `temporary_url` | INTERNAL logical Store call; `capabilities` is not timed work |

`DeleteMany` is the Go service method and `delete_many` is the telemetry token.
The transport term `bulk_delete` is not a third synonym. M0 publishes the
complete Go method → schema token table and tests it for totality.

### Candidate attributes

| Concept | Candidate mapping | Metrics? | Rule |
|---|---|---:|---|
| Component | `vv.component` | yes | Closed: initially `command`, `cache`, `cache_backend`, `storage` |
| Operation | `vv.operation.name` | yes | Registry allow-list per component; unknown source values collapse or are omitted |
| Logical resource family | `vv.resource.name` | only if predeclared/bounded | Construction-time declaration, never record ID |
| Outcome | `vv.operation.outcome` | yes | Registry allow-list; unknown source values collapse or are omitted; distinct from Go `err != nil` |
| Failure class | `error.type` | yes, on failure only | Derived from bounded `errs.Kind`/`storage.Kind`; absent on success |
| Detailed Frostgrove code | `vv.error.code` | traces only, if allow-listed | `errs.Code` is extensible; unknown values collapse or are omitted |
| Item count | measurement/event value | no attribute on aggregate metrics | Integer value, not a label |
| Byte count | measurement/event value | no attribute on aggregate metrics | Integer value, never payload |
| Cache layer | `vv.cache.layer` | yes | Closed `facade` or `memory_backend` |

Names are candidates until M0 records the schema decision. Before adding a
custom key, search the pinned upstream registry. Upstream keys retain their
upstream meaning; Frostgrove does not put a merely similar value under a
standard name.

### Errors, outcomes and status

The previous rule “any non-nil Go error means an Error span” is removed.
Semantic success/failure is defined per operation. A not-found result may be an
expected negative lookup in one seam and a failed command in another. A policy
decision may be a successful decision event even though the command it refuses
fails.

M0 freezes a table with, for every operation and termination path:

```text
return/error, cancellation or panic -> vv.operation.outcome -> error.type? -> span Status
```

The invariant rules are:

- successful spans leave status unset; they are not explicitly marked `Ok`;
- failed operations set `error.type` to one bounded class and use the same
  value on the corresponding duration metric;
- `error.type` is absent on success;
- `context.Canceled` and `context.DeadlineExceeded` are recognized before
  `port.KindOf` because its current fallback would misclassify them as
  `errs.KindInternal`; M0 assigns their exact outcome, status and safe
  `error.type`;
- other command failures reuse `port.KindOf`/`KindOfWith`, and storage failures
  reuse `storage.KindOf`, rather than creating another classifier;
- a panic maps to outcome `error`, `error.type=panic` and `Status=Error`,
  records no panic value or stack, ends the span, and re-panics the original
  value unchanged;
- `errs.Code` is trace-only and allow-listed because it is extensible;
- error identity returned to the caller is unchanged;
- `err.Error()`, `Fault.Message`, `Fault.Detail`, violations, wrapped causes
  and stack traces are never recorded;
- `RecordError` is not called because it would serialize error details outside
  the allow-list;
- status descriptions, if used at all, come from closed safe constants.

### Metrics

The first metric candidate is:

| Instrument | Type | Unit | Attributes |
|---|---|---|---|
| `vv.command.duration` | histogram | `s` | operation, bounded resource, outcome, failure-only `error.type` |

Its histogram count already answers throughput. Do not also publish
`vv.command.total`. If a separate monotonic counter later answers a distinct
question, use a plural noun such as `vv.command.operations`, not a `.total`
suffix.

Duration instruments provide explicit boundary advice. The provisional
boundary candidate below must be accepted or replaced in M0 using
representative command-duration data; similarity to common HTTP boundaries
does not make it a production default:

```text
0.005, 0.01, 0.025, 0.05, 0.075, 0.1, 0.25, 0.5,
0.75, 1, 2.5, 5, 7.5, 10 seconds
```

The application may override aggregation with SDK Views. `WithUnit("s")` does
not select useful buckets by itself. Boundaries change only after recorded
distribution evidence.

Cache terminal events may later feed a `vv.cache.operations` counter with unit
`{operation}`. It records `1` per terminal phase event; `Event.Items` is not its
measurement. A miss followed by load legitimately records distinct `lookup`
and `load` operations, and a memory put may also record separate evictions.
Any item-count instrument needs its own `{item}` unit, name and operator
question. Cache name is a metric attribute only when it comes from a validated,
predeclared descriptor and a cardinality stress test proves the bound. Item and
byte values are measurements, never attribute values. Storage duration and
cache item/byte histograms remain deferred until an operator question and
bucket proposal exist.

## Privacy and cardinality contract

The forbidden list applies to spans, events, metrics, links and log-correlation
helpers:

- model/entity IDs, cache keys, storage namespace/key/stage ID and signed URL;
- tenant, principal, session, request, invocation, event and delivery IDs;
- raw route values, query filters, pagination cursors and sort expressions;
- SQL text, bind values, remote URLs with identifiers and response bodies;
- request/response headers, cookies, tokens, credentials and baggage values;
- model, event, job, cache or object payloads;
- error strings, wrapped causes, stack traces, validation values and policy
  refusal reasons;
- storage metadata, content type, ETag and backend version identifiers;
- arbitrary application tags or callbacks that can add attributes.

Hashing a forbidden value does not make it low-cardinality or safe. Truncation
does not make a secret safe. A value is emitted only when the registry declares
its source, bound and privacy class.

Allowed initial data is deliberately small: declared component, operation,
logical resource family, closed outcome, bounded failure kind, counts, byte
measurements and duration.

Telemetry is not an audit trail. Sampling, exporter loss and SDK shutdown make
it unsuitable for compliance or durable evidence.

## Subsystem implementation plans

### Commands (`vvotel` service decorator)

The `vvotel` command decorator observes exactly:

```text
List Count Get Create Update Replace Delete DeleteMany
```

`Meta` and `Paths` are forwarded exactly and emit no span. The totality table
records those deliberate non-observation decisions as well as the eight
observed methods.

Restore discovery on the wrapped service uses `port.RestorableOf`, not only a
direct `RestorableService` assertion: the standard `*port.DefaultService`
publishes restore through the unexported `port.restorableProvider` interface.
The interface name is private, but its `Restorable` method and return type are
exported, so an external wrapper can satisfy it structurally. M0 freezes one of
two honest representations: a provider-forwarding wrapper returning the
discovered `(capability, bool)`, or two concrete dynamic types behind
`port.Service`, where only the restorable type directly implements and observes:

```text
Restore RestoreMany
```

Whichever representation is accepted must make `port.RestorableOf` return the
same availability as the wrapped service and delegate to the discovered
restorer. A direct method-set wrapper never advertises restore and then returns
“unsupported”; a provider wrapper reports absence with `false`. The decorator
must not manufacture or erase an optional capability. A method
inventory/compile assertion fails when `port.Service` grows without a schema
decision, following [[D-030]]. The derived context is passed to the underlying
service so repository, SQL, remote HTTP and storage child spans attach to the
command.

Only a declared logical resource family is observable. Although `Meta` is
forwarded, its model/table data is not mined for attributes. Model values,
command bodies, IDs, callbacks and query requests are not inspected either.

### Repository and database (future `vvotel` middleware, if justified)

No repository span is emitted by default in the first release. A command span
plus database/remote client spans normally answers where time went without a
duplicated logical layer. Direct `crud.Core` users can motivate a later
opt-in wrapper with an operator query and trace-shape evidence.

If that wrapper is approved, it forwards `Meta` without a span and covers all
fifteen current operation methods:

```text
GetByID Get GetAll First Save SaveOnly Update UpdateAll Aggregate
SaveAll Delete DeleteAll Count Exists Tx
```

It must also:

- explicitly preserve `BatchInserter.InsertBatch` when supported;
- explicitly preserve or fail closed for `Restorer`, `ScopedRestorer`,
  `TombstoneLoader`, `ScopedSaver`, `ScopedSaveOnlyer` and `ScopedDeleter`
  according to their exact public discovery rules;
- explicitly preserve `RestoreSupport.SupportsRestore` whenever it exposes
  restore;
- expose `Next`/source identity exactly as required by [[D-061]] and [[D-062]];
- resolve the current `ExistsUnscopedOf` walk before instrumentation: because
  `UnscopedExister` executes a repository effect, the default M0 direction is
  exact-outer discovery, explicit preservation by each built-in decorator and
  fail-closed behaviour at an unknown wrapper; keeping a walk would require an
  explicit amendment to [[D-061]] and a proof that it bypasses no policy or
  observability layer;
- never infer or tunnel an executable capability through an unknown wrapper;
- apply `...crud.Option` functions exactly once;
- receive query shape only from the point where resolved `*crud.Options`
  already exists, or omit shape entirely;
- keep `Tx` callback context and transaction-handle instrumentation honest;
- add and pass a decorator-specific D-030-style method-set obligation test.

Database spans belong below transaction handles to the driver/client
instrumentation selected by the application. The roadmap does not call
`otelsql` or pgx instrumentation “official OpenTelemetry instrumentation”:
those choices require their own dependency and privacy review.

`remotehttp.WithClient` is the supported composition seam for an
`otelhttp`-instrumented client. Frostgrove does not record `ProtocolError.Body`,
raw URLs or error text.

### Cache (`vvotel` observer factories)

The current `cache.Event` and `cachememory.Event` are typed terminal phase
events. Built-in emitters use a bounded known vocabulary, but their exported
string types are extensible by callers. After the safety gate below, a total
allow-list mapping can honestly produce bounded event counters and, when a
recording span already exists, safe span events. Unknown operation, outcome or
reason values collapse or are omitted; raw unknown strings never become
attributes. The terminal events cannot produce:

- operation duration;
- a correctly parented operation span;
- loader/backend phase timing;
- causal nesting reconstructed after the call.

Those signals require instrumentation at the operation start/end point and
must not be invented from a timestamp taken inside the terminal adapter.

The current hooks are synchronous. Their timing is part of the base cache
contract, not something the OTel adapter may rewrite. Most importantly, the
shared-flight terminal load observer deliberately runs before the admitted
flight is fully released: [[D-084]] uses that bounded slot as backpressure and
forbids both releasing it first and spawning an unowned goroutine. Before an
OTel adapter is attached, a regression matrix must prove that fan-out preserves
that rule, panic cannot suppress cleanup or later observers, and non-flight
paths do not retain unrelated transient or cleanup-critical capacity. There is
no goroutine per event and no unbounded adapter queue. An application provider
may delay the current caller and, for a shared terminal load, its already
bounded flight slot; this is why every observer must be bounded and
non-reentrant.

Facade and backend counters are opt-in separately and carry a closed layer.
One `vvotel` package exposes separate facade and memory-backend factories backed
by distinct private adapter types: both interfaces call their method `Observe`
but accept different event types, so one Go type cannot implement both. If the
backend signal is not independently useful, its factory and mapping are omitted
without creating or deleting a module. `lookup`, `load`, backend `put` and
eviction remain distinct event occurrences; they are not presented as a
one-per-public-call metric.

### Storage (`vvotel` storage decorator)

Instrumentation targets the real `storage.Store`:

```text
Put Open Head Delete Stage Promote Abort CleanupExpired TemporaryURL
```

`Capabilities` is a description, not an operation span. The wrapper preserves
it exactly.

`Put` and `Stage` consume their readers inside the call, so their logical span
may cover that work. `Open` returns an `io.ReadCloser`; its initial span ends
when `Open` returns the stream and metadata. It does not claim to measure later
body reads. Wrapping the reader to extend span lifetime is deferred because it
changes close/error/parentage semantics and leaks spans when callers forget to
close.

The logical wrapper is backend-neutral and imports neither MinIO nor a cloud
SDK. `storage.KindOf` supplies failure class. Namespace, key, stage ID, URL,
Info metadata, content type, ETag and version remain redacted even though some
of their `String` methods are already defensive.

Object-store semconv is Development. Initial spans therefore use the versioned
Frostgrove logical schema; backend/client instrumentation may separately use
the upstream convention.

### Policy, auth and audit

The current security policy does not expose a stable policy name or typed
observer result; denial reasons are free text. The initial command wrapper may
classify its final result as forbidden, but there is no policy-name attribute,
policy span or reason event.

The existing degraded-JWKS hook remains deferred until a dependency-light base
auth seam can carry its closed event without making `vvotel` import `authjwt`.
A schema entry alone is insufficient. If that seam is accepted, principal,
issuer URL, key ID and token material are not emitted.

Audit remains a separate durable contract. An OTel event never satisfies an
audit requirement.

### Jobs, outbox and propagation

Framework jobs now has its Definition/Invocation/Attempt model and initial
memory/PostgreSQL execution path. Its `WorkerObserver` vocabulary/config is not
wired by the runtime and is not a handler lifecycle wrapper. Outbox/event
sourcing still does not exist, and jobs telemetry is deliberately not pulled
into the first `vvotel` release merely because code has landed. The current
vocabulary is:

```text
Definition -> Invocation -> Attempt
```

Admission deferral before the handler is not an attempt. Before a jobs adapter
is accepted, the jobs roadmap must finish its clean conformance/live-backend
gates and justify a dependency-neutral typed handler-lifecycle seam; `vvotel`
does not import a jobs backend or a tenancy extension. Propagation design must
then specify a bounded carrier, validation, size and age limits,
trusted/untrusted extraction, retry/replay semantics and parent-versus-link
policy. A stored trace ID alone is not propagation.

The exact optional `jobs.Admin` contract is backend administration, not that
handler-lifecycle seam. Its `DeliveryView` exposes encoded application payload,
so the first `vvotel` release neither adapts Admin nor records those values.
Current same-ID PostgreSQL redrive also replaces the stored attempt ledger;
telemetry cannot turn that provisional record into durable operational evidence
or reconstruct the pre-redrive trace.

Any later Admin decorator asserts the exact outer value. A known wrapper must
explicitly forward all four methods; an opaque wrapper makes the capability
absent/fail-closed. This still does not place Admin in the first release.

Messaging semantic conventions are Development on this snapshot. A replay or
long-delayed invocation may require a link rather than a misleading live
parent. That choice is reviewed against the pinned convention and the actual
job contract; it is not frozen by this roadmap.

No baggage is propagated by default.

### Logs

Applications may correlate their existing logs from the active span and may
choose `otelslog`. Frostgrove does not:

- configure an OTel LoggerProvider;
- import the Go Logs API or SDK;
- install a logging bridge;
- duplicate every span/event as a log;
- create a helper that hides application Resource and shutdown ownership.

Go Logs being stable in a future release would remove a version risk, not
create a Frostgrove requirement.

## Delivery plan

### M0 — freeze decisions and schema

Deliverables:

1. ADR for one cross-cutting OTel module, its final path/package and the
   “one ecosystem choice over several base seams” rule. It records the narrow
   amendments to [[D-035]], [[D-051]], [[D-058]] and [[D-074]], preserves
   [[D-033]], [[D-036]] and [[D-048]], and forbids extension-to-extension
   imports.
2. Inventory of current base extension points and the minimum missing typed
   service/storage middleware and observer-composition APIs. It fixes ordering,
   nil/error handling, discovery and optional-capability rules without OTel
   types or a generic extension registry.
3. `vvotel.New` factory shape, shared immutable configuration and
   package-level generic decorator factories.
4. Exact lean dependency allow-list and provider ownership: `vvotel.New`
   borrows injected providers and never shuts them down. The acceptance rules
   for any later production runtime helper are recorded separately.
5. Exact method/operation inventory for command, cache facade/backend and
   storage, including forwarding-only methods and optional capabilities.
6. Decorator start/end and local point-event semantics, derived-context rule,
   panic/re-panic behaviour and provider blocking policy.
7. Outcome/error/status matrix using existing classifiers plus explicit
   cancellation/deadline handling.
8. Machine-readable schema registry with constants generated once into
   `vvotel` plus reference documentation.
9. Cardinality and privacy budget, including declared logical resource names.
10. Benchmark baselines, a representative trace topology, minimum supported
    OTel Go GA and duration bucket advice.

Exit evidence:

- no public API is added by prose alone;
- every candidate field has a source, bound, destination and privacy class;
- the registry totally maps command/storage operations and current cache point
  enums without making root packages import `vvotel` constants or creating a
  common Observer interface;
- the ADR shows one `otel/go.mod`, no per-subsystem/pairwise OTel modules, no
  optional-satellite imports, and no OTel in any non-OTel module;
- two independent fake extensions plus an OTel-shaped fake can be expressed by
  the typed base composition points without a combination package;
- reviewers can explain why each planned span is not a duplicate.

### M1 — base extension points, hook safety and conformance

Deliverables:

1. Typed service and storage decorator/middleware chain helpers accepted in M0,
   with explicit order tests and no third-party imports.
2. Deterministic cache/cachememory observer composition, plus recorders and
   exhaustive terminal-event golden tests.
3. Preserve each cache callback's accepted lifecycle contract. The terminal
   shared-load callback remains synchronous before full flight release under
   [[D-084]] and consumes only that already bounded slot; other callbacks do
   not retain unrelated transient-budget leases or cleanup-critical state.
4. Saturation/reentrancy/panic tests for every cache emission path.
5. Command decorator conformance fixtures covering `Meta`, `Paths`, all eight
   operations, underlying provider/direct restore discovery, non-restorable
   services and the provider-forwarding or dynamic-type representation selected
   in M0.
6. Storage decorator conformance fixtures covering all methods and exact
   `Capabilities` forwarding.
7. Decorator-specific D-030/D-061-style method and optional-capability
   obligation tests, including chains with two unrelated fake decorators.

Exit evidence:

- the root `go.mod` still has no third-party requirement;
- every non-OTel module and base-only consumer fixture has an
  OTel-free module graph when inspected outside the workspace;
- base composition works for independent layers in declared order without an
  `Extension` registry, service locator or pairwise package;
- no local hook changes a returned value/error or optional capability;
- a blocked terminal shared-load hook consumes its already admitted bounded
  flight slot exactly as [[D-084]] specifies, while fan-out creates no extra or
  unbounded admission; other hooks do not retain unrelated transient capacity;
- callback panic, reentrancy and ordering tests pass;
- no shared telemetry facade or framework lifecycle contract is introduced;
- uninstrumented and local-hook benchmarks stay inside the M0 budget.

### M2 — one OTel module, factory and traces

Deliverables:

1. Exactly one published `otel/` module and one public `vvotel` package,
   organized by files rather than public per-seam subpackages.
2. Immutable `vvotel.New` factory handle with fixed scope name/version,
   explicit signal enablement and no global fallback.
3. The lean injected-provider profile; `vvotel` does not own or shut down the
   providers supplied to it.
4. A `vvotel` middleware/decorator for `port.Service` and `storage.Store`, both
   emitting INTERNAL spans through base-owned composition points.
5. Separate `vvotel` observer factories for safe cache and cachememory events,
   installed only when explicitly enabled; they add a span event only when a
   span is recording, while later metric emission is independent of trace
   sampling.
6. API-fake unit tests in `vvotel`; recording SDK tests stay in a separate
   unpublished test module and do not add SDK requirements to `otel/go.mod`.
7. HTTP, gRPC, remote HTTP and database composition examples in a separate
   unpublished example module, using upstream or application-selected
   instrumentation. gRPC examples use
   `otelgrpc.NewClientHandler`/`NewServerHandler` as stats handlers, not retired
   interceptor APIs.

Exit evidence:

- `vvotel` production source imports and direct requirements match the exact
  lean allow-list; no SDK, exporter, optional Frostgrove satellite, contrib transport,
  concrete backend/router/client or unselected exporter enters the module;
- the full transitive graph matches the reviewed pinned profile and every
  surprising edge has a `go mod why -m` explanation;
- isolated fixtures prove root/base-only and every non-OTel module stay
  OTel-free, while service-only, storage-only, cache-only and all-seam OTel
  examples reuse the same one `vvotel` module and factory handle;
- importing `vvotel` has no side effect and unused adapter factories register
  no instruments;
- no package reads or mutates OTel globals;
- trace tests prove request → command → client/driver parentage;
- `Meta`, `Paths`, restore discovery and storage `Capabilities` are preserved;
- a storage `Open` span does not pretend to cover later reads;
- repository and database spans are not duplicated;
- a PII canary cannot enter exported attributes/events;
- disabled and unsampled paths preserve behaviour and meet the budget.

### M3 — metrics

Deliverables:

1. Command duration histogram in `vvotel` with explicit boundary advice.
2. Failure-only `error.type` consistent with trace mapping.
3. An opt-in cache facade operation counter from the `vvotel` facade observer,
   implemented after layer/cardinality/safety gates; the memory-backend counter
   remains a separately selected factory in the same package.
4. Cardinality stress harness and golden metric schema.
5. Documentation for application Views and independent signal enablement.

Exit evidence:

- no `.total` duplicate of histogram count;
- success series omit `error.type`;
- unknown extensible codes cannot create new metric series;
- exact counts/bytes are measurements rather than attributes;
- trace sampling does not suppress metrics;
- cache operations record `1` per terminal phase event rather than
  `Event.Items`;
- cache callback timing remains identical to the base contract, including the
  bounded pre-release terminal-load callback from [[D-084]]; nonblocking,
  bounded observers remain an application integration requirement.

### M4 — evidence-led expansion

These are separate follow-ups, not first-release promises:

1. repository logical spans, only if direct-Core use produces a demonstrated
   diagnostic gap;
2. storage duration metric and cache byte/size histograms, with real bucket
   evidence;
3. policy and JWKS point events after typed source vocabularies exist;
4. jobs spans/propagation after its release gates and typed seam are accepted,
   and outbox/messaging signals only after those contracts ship;
5. composition evidence for a new storage backend and its application-selected
   client instrumentation; no backend-specific mapping or import enters
   `vvotel`.

Each follow-up adds its own schema review, privacy fixtures, compatibility
tests and operator query. A new base seam adds a factory or mapping to the same
`vvotel` package only when that seam is dependency-light. A lifecycle owned by
an optional satellite is either projected onto a neutral base hook or deferred;
it never creates `satelliteotel` or makes `vvotel` import that satellite. A
subsystem being implemented is necessary but not sufficient reason to
instrument every method.

### M5 — release and maintenance

Deliverables:

1. Compatibility tests for `vvotel` against the minimum supported and current
   OTel Go GA.
2. `go list -m all`, dependency-diff and `govulncheck` evidence for the
   production OTel module and separate test/example modules.
3. Upgrade note for the pinned semantic-convention version.
4. Runnable SDK example with explicit Resource, exporter/reader policy and
   shutdown, isolated in its own unpublished module; it injects the resulting
   providers into `vvotel.New`.
5. Generated schema reference and migration policy.
6. Changelog entries for signal/schema changes.
7. A workspace-membership set-equality check between `go.work` and all
   discovered modules intended for the workspace (with `_examples` explicitly
   excluded); discovery/release scripts remain exhaustive.

Release policy:

- never select an RC as the supported baseline;
- the lean API-only production dependency profile remains a hard gate;
- the one published OTel module has tag `otel/vX.Y.Z`, no `replace`, and a
  version lockstep with the root;
- known-vulnerable versions are absent even from copied examples;
- a semconv or OTel dependency bump receives trace/metric golden review;
- one previous supported minor is tested during a deprecation window when
  feasible; the exact window is decided before the first tag;
- Collector/exporter compatibility is documented by the application example,
  not claimed as a library dependency matrix.

## Verification matrix

| Area | Required proof |
|---|---|
| Base optionality | With `GOWORK=off`, root, every base-only fixture and every published module other than `otel/` has neither `github.com/frostgrove/vv/otel` nor `go.opentelemetry.io/*`/contrib in its graph |
| Single extension | Exactly one published `otel/go.mod` and one public `vvotel` package exist; no per-seam, pairwise or nested OTel modules |
| Extension imports | Production imports match the exact M0 lean profile: OTel-free root seams plus accepted OTel API packages only; no SDK, exporter, optional Frostgrove satellite, contrib transport, backend/router/client or Logs API |
| Direct requirements | Direct `otel/go.mod` requirements match the lean allow-list and contain no SDK, exporter or unrelated ecosystem |
| Transitive graph | `go mod why -m` and a dependency diff prove every upstream edge against the reviewed pinned profile |
| Linear composition | Two unrelated fake extensions and `vvotel` compose through base-owned typed chains in declared order; optional capabilities and discovery stay exact |
| Activation | Import and factory construction have no side effects; only explicitly requested decorators/observers create instruments or emit signals |
| Consumer selection | Service-only, storage-only, cache-only and all-seam fixtures use the same OTel module; the plan makes no false per-file compilation claim |
| Workspace/release | Discovered intended workspace modules and `go.work` membership are set-equal; published `go.mod` files have no `replace`; root and `otel/` tags are lockstep |
| Globals/lifecycle | No global getters/setters; injected providers are borrowed and never shut down by `vvotel` |
| Surface totality | Every current service/storage verb is mapped; additions fail tests |
| Capabilities | Restore, batch, scoped effects, tombstone loading, `Next`, source and storage `Capabilities` remain exact |
| Options | A side-effecting `crud.Option` executes once if repository work is later added |
| Errors | Identity preserved; cancellation handled before classifier; success omits `error.type` |
| Parentage | Derived context makes client/driver spans children of command/storage work |
| Duplication | No second transport, DB, facade/backend or repository span by default |
| Streaming | `Open` timing ends at method return and is labelled accordingly |
| Hook safety | Panic isolated; D-084 shared-flight backpressure preserved; no extra admission, unbounded queue or goroutine leak |
| Privacy | Canary IDs, keys, SQL, payload, headers and error text absent from export |
| Cardinality | Declared bound enforced; unknown codes/resources collapse or fail construction |
| Metrics | Units, descriptions, boundaries and attributes match registry |
| Performance | No-op, disabled, unsampled and recording benchmarks stay within budget |
| Compatibility | Minimum/current GA, race tests, dependency diff and vulnerability scan pass |
| Documentation | Examples build and own SDK providers explicitly, inject them into `vvotel.New` and shut them down in the application |

The performance budget is set from M0 measurements rather than invented here.
It separately covers the uninstrumented baseline, factory construction,
disabled decorators, non-recording span, recording span and local cache-event
adapter paths.

## Explicit non-goals

The initial release does not include:

- per-subsystem or pairwise OTel modules;
- an extension-to-extension import or a generic `extensions/` bundle;
- a global `Install` API, a first-release production SDK/runtime factory or
  hidden SDK/exporter/Collector lifecycle; a later explicit runtime factory
  requires its own ADR and reviewed module-graph expansion;
- build tags as a substitute for module isolation;
- combined dependency cells such as `crudginotel`, `storageminiootel`,
  `tenancyotel` or `eventsourceotel`;
- a shared framework `Observer` facade while [[D-048]] remains in force;
- OTel Logs or a mandatory `slog` bridge;
- automatic HTTP, gRPC, database, router, broker or cloud instrumentation;
- default repository spans or SQL/query-shape parsing;
- arbitrary custom attributes, baggage, payload capture or error recording;
- trace-derived authorization, audit, billing or retry decisions;
- tail sampling, profiling or OTel Profiles integration;
- jobs propagation before release-ready handler-lifecycle/propagation seams
  exist, and outbox propagation before outbox contracts exist;
- reader-wrapping storage spans;
- background export queues inside first-release Frostgrove production code;
- generated dashboards, alerts or SLOs without an operator-owned objective;
- a promise that all OTel semantic conventions are stable.

## Disposition of the old O-cards

| Old cards | New disposition |
|---|---|
| O-01–O-04 | Replaced by M0–M2: one optional `vvotel` module, one shared factory, base-owned typed composition points and direct current-interface decorators; no shared observer or package cross-product |
| O-05 | Rebased onto the current command surface and optional restore |
| O-06–O-07 | Deferred repository work; options-once and capability gates added |
| O-08 | Use existing `port`/`storage` classifiers; schema/status matrix moves to M0 |
| O-09 | Deferred until policy has a typed observation source |
| O-10–O-11, O-20 | Jobs uses the newer Definition/Invocation/Attempt model, but instrumentation remains deferred until its release/seam gates; outbox remains unimplemented |
| O-12 | Composition guide only; injected upstream HTTP/gRPC/client instrumentation owns protocol spans |
| O-13 | Promoted from hypothetical to current `storage.Store` work |
| O-14–O-17 | Still deferred because those framework contracts do not exist |
| O-18 | Rewritten as M3; duration first, no redundant `.total`, failure-only `error.type` |
| O-19 | Application-owned logs; no first-release OTel Logs integration code |
| O-21–O-24 | Consolidated into M0/M2/M3/M5 verification and compatibility gates |

## Initial release definition of done

The first release is done only when:

1. the ADR and generated telemetry schema are accepted;
2. OTel-free base composition points stack two unrelated fake integrations and
   the OTel-shaped decorator without a pairwise package or lost capability;
3. the one `vvotel` module's service/storage decorators and cache terminal
   adapters pass their safety, capability, privacy and performance suites;
4. one immutable factory handle produces correctly parented command/storage
   spans and the command duration metric, while unused factories activate no
   signals;
5. the opt-in cache counter preserves the already admitted D-084 flight slot
   until its bounded synchronous callback returns, acquires/retains no extra
   coordination capacity and never merges facade/backend observations;
6. root and every published module other than `otel/` remain OTel-free;
   `otel/go.mod` and source imports match the lean API allow-list with no SDK,
   exporter, optional satellite or combined backend/router/client; its
   transitive graph matches the reviewed pinned profile;
7. the provider-injection path works, borrowed providers are never shut down by
   `vvotel`, and the application-owned SDK example has explicit idempotent
   shutdown without touching globals;
8. minimum/current GA compatibility, race, vulnerability and cardinality
   checks pass;
9. an example application makes Resource, SDK, exporter and shutdown ownership
   explicit rather than rebuilding framework decorator boilerplate;
10. base-only and every non-OTel-module fixture proves no OTel graph, while
    every OTel seam uses the same one module;
11. repository spans, logs, jobs propagation and unimplemented outbox/broker
   systems remain visibly deferred rather than represented by placeholder APIs.

When these conditions hold, remove the OpenTelemetry item from the live
[Roadmap.md](Roadmap.md). A later signal expands the same `vvotel` module only
through a stable base seam and a new schema review; a new external ecosystem
choice gets its own roadmap/ADR rather than a combination package.
