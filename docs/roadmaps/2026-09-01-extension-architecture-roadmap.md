# Optional extension architecture roadmap — 2026-09-01

**Status:** current governing target for this roadmap set, pending the M0
architecture ADR. It governs package, module and composition proposals in the
current feature revisions, but accepted ADRs and implemented contracts remain
binding for production until amended—most notably [[D-074]]. It does not make a
proposed feature an accepted delivery commitment.

**Applies to:** storage, i18n, PostgreSQL event sourcing, multitenancy, audit,
jobs/cache and OpenTelemetry. The feature-specific revisions linked from the
[roadmap index](Index.md) own their domain semantics and delivery evidence.

This revision records one owner decision that the dated roadmaps did not apply
consistently: package count grows with independently selectable capabilities,
not with every intersection among them. A framework extension may adapt several
dependency-light base seams in one module. Existing modules never import that
extension merely to make the integration convenient.

## Decision in one page

1. The root and every pre-existing module expose no type from an optional
   extension and have no direct dependency on one.
2. A base package owns its dependency-neutral extension points: typed
   middleware, decorators, observers, strategies, factories and chain helpers.
3. One optional module represents one independently selectable framework
   extension or external ecosystem. It may provide several typed adapters to
   stable base seams when those adapters embody the same consumer decision.
4. That module may include factories and helpers that remove reusable
   Frostgrove-specific boilerplate. Importing it must not register globals,
   discover application components or activate unused adapters.
5. The application composition root chooses which extensions exist and their
   deterministic order. The first middleware listed by a chain is outermost;
   nil middleware is skipped.
6. Extension-to-extension imports are forbidden. Interaction crosses a typed
   base seam, a neutral capability/event owned by that base package, or explicit
   application wiring.
7. Packages for intersections such as `storageotel`, `auditotel`,
   `eventsourceotel`, `tenancyjwt`, `i18nhttp` or a combined
   `tenancy_eventsource_otel_jwt_redis` are not created.
8. A backend, transport, provider or container adapter gets its own module only
   when it isolates a genuinely independent third-party dependency decision.
   It adapts one base contract; it is not a bridge between two extensions.
9. Navigation, identity and description may use a bounded declared walk.
   Executable optional effects are preserved explicitly by the exact outer
   wrapper or fail closed before I/O; they never tunnel through an unknown
   wrapper.
10. There is no universal `Extension` registry, service locator,
    `map[string]any`, reflection-based heterogeneous chain or framework-wide
    telemetry/observer contract.
11. Consumer fixtures must prove linear growth: adding N independently selected
    extensions adds N decisions, not packages for their pairwise or higher-order
    combinations.

## Vocabulary and sizing rule

| Term | Meaning |
|---|---|
| Base seam | A dependency-neutral interface or typed hook owned by the subsystem whose behaviour is being wrapped |
| Extension module | One optional module selected for one framework capability or external ecosystem; it may adapt several base seams |
| Adapter module | A module that isolates one concrete third-party backend, transport, provider or container choice |
| Composition root | Application code that constructs base values, applies decorators and owns lifecycle/order |
| Combination package | A package whose identity is the intersection of two independently selectable extensions; forbidden |

The sizing test is about consumer decisions, not the number of `require` lines.
A gRPC binding may need gRPC, protobuf and genproto because they are one
inseparable protocol decision. OTel plus Gin, tenancy plus JWT, or audit plus an
event store are separate choices even when one application uses all of them.

Stdlib-only implementations do not receive a module merely for symmetry. A
package already in the root may stay there when it adds no third-party module
graph and obeys the contract tiers. A third-party dependency remains isolated
under [[D-033]], [[D-036]] and [[D-051]].

## Dependency direction

```text
                         application composition root
                                      |
                  constructs and orders ordinary typed values
                                      |
          +---------------------------+--------------------------+
          |                           |                          |
     base service                base storage              base jobs/cache
          ^                           ^                          ^
          | typed middleware          | typed decorator          | typed hook
          |                           |                          |
    tenancy / event / audit      audit / vvotel             vvotel / app policy
          ^                           ^                          ^
          +---------------------------+--------------------------+
                                      |
                       independent extension modules

base and non-extension modules --------X--------> optional extensions
extension A ----------------------------X--------> extension B
extension ----------------------imports---------> dependency-light base seams
adapter module -----------------imports---------> its owning base seam + one ecosystem
```

An extension may depend on several base packages in the root because those
imports do not force another optional choice. It may not import `authjwt`,
`storageminio`, `vvotel`, a router binding, a broker adapter or another optional
extension to offer a convenient preset.

An implementation adapter necessarily imports the contract it implements, even
when that contract belongs to an optional feature module. That owner edge is not
an extension combination. The adapter may not import a second concrete adapter:
a Redis revocation store must target an accepted dependency-neutral,
access-owned session/token revocation contract, not a JWT strategy; an Fx
provider targets a neutral constructor seam, not a MinIO-specific module.

## Base-owned composition points

The current tree already has some of the required shapes and is missing others:

| Base owner | Current seam | Required architecture action |
|---|---|---|
| `crud` | `Middleware`, `Chain`, `Base.Next` and typed optional effects; `ExistsUnscopedOf` currently walks through `Next` to an executable inner effect | Reuse the chain, but make unscoped existence exact-outer/explicitly forwarded or fail closed before adding new decorators |
| `port` | `Service` plus optional restore discovery | Add a typed service middleware/chain and preserve restore honestly |
| `storage` | `Store`, `Backend`, `Capabilities` | Add a typed Store middleware/chain; forward `Capabilities` exactly |
| `cache` | typed `Observer`, backend description/capabilities | Add deterministic bounded observer fan-out that preserves [[D-084]] shared-flight backpressure; do not add a generic backend chain until executable batch discovery is exact-outer |
| `cache/cachememory` | a distinct typed `Observer` | Add its own fan-out; do not merge facade and backend events |
| `jobs` | typed `Consumer`, handler binding, exact optional `Admin` executable capability and `WorkerObserver` vocabulary/config; runtime observer emission is not wired | Keep Admin selection exact/explicit and never tunnel through a driver wrapper; first wire/narrow the point-event contract, then add fan-out or a handler-lifecycle middleware only for justified independent uses |
| `errs` and `port` | `MessageSource`, explicit locale in context | Full i18n implements these seams; transport-specific i18n bridge packages are unnecessary |
| `auth` | `Authenticator`; `auth.Chain` means fallback | Do not reuse fallback order as decorator order; add a separately named typed middleware only when needed |
| `app` | `Ordered[H]` contributions; `module.Definition` files a context's constructors by deployment role and `module.Catalog` is the one list of them | Use for additive host contributions; a definition holds constructors as opaque values and imports no container, router or extension, and neither becomes a registry |

Base APIs are added only when at least two independent consumers or one current
consumer plus a concrete conformance obligation justify them. They contain no
extension type or dependency.

Two current executable-discovery shapes are blockers, not precedents:
`crud.ExistsUnscopedOf` walks through `Next`, and `cache.BatchReaderOf` walks
through backend wrappers. Each must become exact-outer, explicitly forwarded by
known built-ins or fail closed before a new cross-cutting decorator can rely on
it.

## Linear application composition

The target developer experience is ordinary Go construction. Names below show
the shape and are not accepted APIs:

```go
service := port.ChainService(
    baseService,
    tenancy.Service[Order, OrderID, OrderUpdate](tenantConfig),
    audit.Service[Order, OrderID, OrderUpdate](auditor),
    vvotel.Service[Order, OrderID, OrderUpdate](telemetry),
)

store := storage.Chain(
    storage.New(baseStorageConfig),
    audit.Store(auditor),
    vvotel.Store(telemetry),
)

renderer := porthttp.NewRenderer(
    porthttp.WithMessages(i18n.Messages(catalogue)),
)
```

No package above imports the next package in the list. Reordering is an explicit
application decision and conformance tests cover both orders where order affects
capability preservation. A convenience factory may return one extension's set
of middleware/observers, but it does not construct unrelated extensions.
`eventpg` remains a direct application-owned aggregate/UoW collaborator until
two real implementations justify a dependency-neutral event chain; it is not
forced into the service example merely for visual symmetry.

## Capability rules

Composition must preserve behaviour, not merely compile:

- every base method has an explicit forward/observe/refuse decision;
- navigation, identity, routing and description may walk only through a named,
  bounded `Next`/unwrap contract;
- repository, storage, queue, jobs-administration and cache executable effects
  use the exact outer authority; an unknown wrapper cannot be skipped to find a
  more capable inner value;
- a built-in wrapper may expose an optional effect verb and fail closed before
  I/O when the wrapped value cannot support it, as allowed by [[D-030]];
- a capability whose method-set presence itself promises availability, such as
  service restore, uses honest dynamic types or `(capability, bool)` discovery;
- wrappers do not replay executable options, callbacks, readers or payloads to
  inspect them;
- callback fan-out preserves the owning base lifecycle: in particular a
  terminal shared-cache observer remains synchronous before full flight release
  under [[D-084]], and fan-out adds no goroutine, queue or extra admission;
- method inventories and capability matrices fail when a base seam grows without
  a decorator decision, following [[D-030]] and [[D-061]].

## Current roadmap mapping

| Direction | Current module decision | Forbidden cross-product |
|---|---|---|
| Storage | Root `storage`/`storagefs`; `storageminio` isolates MinIO; decorators target `storage.Store` | `storageotel`, `storageaudit`, provider × telemetry packages |
| Full i18n | One optional `i18n` module implements root `errs.MessageSource` and locale seams | `i18nhttp`, `i18ngrpc`, `i18notel`, `i18ntenancy` |
| PostgreSQL event sourcing | One optional PostgreSQL event-source extension; outbound delivery accepts an application-supplied neutral sender until a broker-owned base seam is independently justified | `eventsourceotel`, `eventtenancy`, `eventaudit`, `eventkafka`, `eventnats`, broker × OTel packages |
| Multitenancy | One optional `tenancy` extension offers row/database topology factories over root seams | `tenancyjwt`, `tenancyotel`, `tenancyaudit`, one module per topology combination |
| Audit | One optional `audit` extension owns durable evidence and typed decorators; persistence is injected or isolated by a true backend choice | `auditotel`, `auditevent`, `audittenancy`, `auditstorage` |
| Jobs/cache | Root dependency-neutral contracts; external backend modules only for real backend SDK choices | `jobsotel`, `jobstenancy`, `cacheotel`, `cacheredisotel`, revocation/cache reuse |
| OpenTelemetry | One optional `vvotel` module adapts several base seams | all subsystem-specific OTel modules |

### Existing legacy combinations

The invariant is a target architecture, not a false claim about today's tree.
The current module graph contains four shapes that M0 must classify and migrate:

| Current module | Problem | Direction |
|---|---|---|
| `app/http/appfiber` | Bundles Fx, Fiber and an auth binding into one preselected application graph | Move product graph wiring to the application; keep only a genuine base transport adapter if one remains |
| `storage/storageminio/storageminiofx` | Binds one concrete backend adapter to an independently selected container | Deprecate the pairwise module; application wiring calls ordinary `storageminio` constructors |
| `auth/access/accessjwt` → `auth/authjwt` | The access JWT strategy imports a second concrete JWT authentication adapter as well as the JWT SDK | Keep one access→JWT ecosystem adapter, but replace the concrete-adapter edge with an accepted dependency-neutral token/claims seam or local implementation; record compatibility before removal |
| `auth/access/accessjwt/revokeredis` | Makes a Redis revocation store a child of the JWT strategy; the current root `access.RevocationSink` notification is not the same read/expiry-aware contract as `accessjwt.RevocationList` | M0 first designs or moves the appropriate dependency-neutral session/token revocation contract into the owning access boundary; only then move the Redis adapter so JWT and Redis become independent selections |

`app/appfx`, `crudsqlfx` and the now-landed `jobsfx` adapt dependency-neutral
root seams to one container and do not import another concrete optional
adapter. `jobsfx` must not grow imports of `jobspg`, `jobsmemory` or a Redis
adapter. The now-committed `jobsredis` module is the genuine backend-owner
shape—root jobs contract plus one Redis ecosystem—and must not import cache,
tenancy, OTel, Fx or another jobs backend. `accessfiber` and `accessgin` are
allowed owner edges from the access HTTP contract to one router ecosystem.
`accessfx` may remain only while it targets the owning access contract and
imports no concrete access strategy. M0 records exact keep/deprecate/move
decisions and a compatibility plan; this roadmap task does not move production
packages.

The words “integration with X” in a feature roadmap describe a behavioural
conformance scenario, not permission to publish an `extensionAextensionB`
package. The application may enable both extensions and test their composition
without either module importing the other.

## Relationship to accepted decisions

M0 records one architecture ADR before implementing a new extension:

- [[D-033]] and [[D-036]] remain unchanged: the root keeps no third-party
  requirement;
- [[D-051]] is clarified: one independently selected extension may adapt several
  root seams, but may not bundle another optional choice;
- [[D-058]] continues to place genuine subsystem transport/backend adapters
  under their owner, while gaining a narrow top-level exception for a
  framework-wide extension such as `otel/`;
- [[D-035]] keeps collision-based names; its hypothetical i18n × router grid is
  not implementation approval and is removed when the ADR is amended;
- [[D-048]] remains intact: local typed domain hooks do not become a shared
  framework telemetry or extension contract;
- [[D-061]] governs discovery versus executable effects;
- [[D-074]] must be narrowed: a container adapter may bind its owning neutral
  seam, but may not import another concrete backend/transport/feature adapter.
  Its existing `appfiber` and `storageminiofx` approvals become migration cases.

Until that ADR is accepted, the names in current revisions are working names.
The dependency direction and no-cross-product invariant are owner decisions and
are not reopened by choosing a different final path.

## Delivery plan

### M0 — freeze the common architecture

1. Accept the architecture ADR and its narrow amendments.
2. Inventory each base seam, every optional capability and every existing
   discovery helper.
3. Freeze chain order, nil behaviour, error identity and panic policy.
4. Define the module-sizing test and source/direct/transitive dependency gates.
5. Add a method inventory and capability matrix template used by all extensions.
6. Inventory existing satellite-to-satellite edges and accept a compatibility
   plan for the legacy combinations above.

### M1 — complete dependency-neutral base seams

1. Add only the accepted service/storage chains and bounded observer fan-out
   helpers; cache fan-out preserves [[D-084]] rather than moving callbacks
   outside their admitted flight.
2. Prove two unrelated fake extensions compose in both relevant orders.
3. Correct any executable capability walk that can skip an unknown wrapper.
4. Keep every new base file standard-library/first-party only.

### M2 — implement one module per selected extension

Each feature revision owns its implementation sequence. Every extension starts
with one public package and organizes adapters by files. A public subpackage or
nested module is added only for a separately selected third-party dependency,
not to mirror each base seam.

### M3 — consumer and release evidence

1. Build isolated `GOWORK=off` fixtures for base-only, each extension alone and
   representative multi-extension compositions.
2. Prove package/module count is additive and no combination package exists.
3. Run dependency diffs, race tests, method inventories and capability matrices.
4. Publish one hand-wired example; container examples remain separate satellites
   and import no other container binding.

## Verification matrix

| Area | Required proof |
|---|---|
| Base optionality | Root and every non-extension module compile outside the workspace with no optional-extension graph |
| One decision | Each extension/adapter `go.mod` has an explicit consumer-decision statement and reviewed dependency graph |
| No reverse edge | Base packages import no extension path or extension-owned type |
| No extension edge | Source/import checks reject extension-to-extension imports |
| Legacy migration | Each pre-existing pairwise satellite has a keep/move/deprecate decision consistent with the owner-edge rule |
| Linear composition | Two fake extensions plus the real extension stack through base-owned typed seams in declared order |
| Capability safety | Unknown wrappers never expose an inner executable effect; built-ins preserve or fail closed before I/O |
| Hook safety | Fan-out preserves subsystem lifecycle/backpressure, isolates panic and creates no unbounded queue or goroutine |
| Activation | Import/factory construction has no global registration or hidden lifecycle |
| Package growth | No pairwise, nested or combined extension package/module appears in discovered modules |
| Workspace/release | Module discovery and `go.work` membership are set-equal; published modules have no `replace`; tags remain lockstep |
| Documentation | Every example labels implemented APIs separately from illustrative shapes |

## Definition of done

This architecture work is complete only when:

1. the common ADR is accepted and current roadmap revisions link to it;
2. all pre-existing base/non-extension modules remain independent of every
   optional extension;
3. base-owned typed seams compose two unrelated fake extensions without
   reflection, a registry or a combination package;
4. exact-effect and method-set capability tests pass for opaque neighbours and
   every built-in decorator order;
5. each current roadmap names one extension boundary, any genuine backend
   choices and its forbidden package cross-products;
6. isolated module graphs prove optionality with `GOWORK=off`;
7. the live roadmap and index point to current revisions while dated snapshots
   remain visibly historical;
8. existing pairwise modules have an accepted migration/deprecation plan rather
   than being silently grandfathered;
9. `git diff --check` and `make check` pass without modifying production code as
   part of the roadmap update.
