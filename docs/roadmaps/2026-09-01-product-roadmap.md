# Product and delivery roadmap revision — 2026-09-01

**Status:** current coordination roadmap. The live inventory remains
[Roadmap.md](Roadmap.md).

**Supersedes:** delivery status, package topology and integration assumptions in
[the 2026-08-26 product snapshot](2026-08-26-1522-product-roadmap.md). Its
capability catalogue remains historical design input; this document does not
pretend that its proposed APIs were implemented.

**Architecture:** every item follows the
[optional extension architecture](2026-09-01-extension-architecture-roadmap.md).

## Current baseline

The 2026-08-26 roadmap predates several large implementation slices. The current
tree, not that snapshot, is the baseline:

| Area | Current state | Roadmap consequence |
|---|---|---|
| Root module | Still has no third-party requirement | Preserve [[D-033]]/[[D-036]] for all new work |
| CRUD, ports, auth, transports | Broad implemented surface with typed policies, transactions and optional capabilities | New extensions wrap stable seams; they do not copy transport/backend matrices |
| Storage | `storage.Store`, `storage.Backend`, `storagefs` and the separate `storageminio` module exist | The [storage revision](2026-09-01-storage-roadmap.md) retires the old “future satellite” layout and owns remaining composition/conformance |
| Cache | Typed facade, memory backend, declarations, bounded policy, observers and conformance helpers exist | Treat delivered contracts as base seams; do not recreate cache inside jobs/tenancy/OTel |
| Jobs | Typed definitions, queue, delivery/worker contracts, durable context, scheduling, Admin/redrive contracts, PostgreSQL operator controls, memory/PostgreSQL drivers, worker execution, Fx binding and a committed Redis backend module exist | Treat PostgreSQL and Redis backends as building, not release-ready, until clean conformance plus their isolated live-service/crash evidence passes; Admin remains exact optional, and List still lacks an aggregate byte budget |
| OpenTelemetry | No code/module exists; a current design revision exists, but its common architecture gate is not yet accepted | Implement the single `vvotel` extension only after common base seams are accepted |
| Full i18n | Root has `errs.MessageSource`, catalogues and explicit locale propagation only | The [i18n revision](2026-09-01-i18n-roadmap.md) starts with an evidence gate; any future module implements existing seams without bridges |
| Multitenancy | Root policy/repository scopes and examples exist; no tenancy extension/topology runtime exists | The [tenancy revision](2026-09-01-multitenancy-roadmap.md) reuses security/query/source seams; example tenant fields do not imply a shipped subsystem |
| Audit and event sourcing | No framework packages exist | The [audit](2026-09-01-audit-log-roadmap.md) and [event-sourcing](2026-09-01-postgres-event-sourcing-roadmap.md) revisions keep them proposed and independent |

Untracked or modified production files belong to concurrent user work and are
not completion evidence for this documentation revision.

## Product direction

Frostgrove should grow by adding a small number of trustworthy capabilities,
not by publishing a package for every stack combination. The product order is:

1. finish and verify work already present in the tree;
2. freeze one extension architecture and complete only the neutral base seams it
   demonstrably needs;
3. ship the first cross-cutting extension (`vvotel`) as proof of linear
   composition;
4. activate tenancy, audit, event sourcing or full i18n only from a concrete
   consumer requirement and its feature-specific readiness gate;
5. add external backend/transport/provider adapters only after the base contract
   has conformance evidence.

No horizon is a calendar promise. A current feature revision is a design and
evidence plan until the live inventory explicitly activates it.

## Package and module model

The old capability catalogue sketched a directory for almost every capability
and integration. The current rule is smaller:

```text
root module
  crud, port, storage, cache, jobs, errs, auth, app   dependency-neutral seams

optional extension modules
  otel/          one OTel ecosystem choice, several base adapters
  i18n/          one full-i18n choice, implements existing message/locale seams
  tenancy/       one tenancy choice, row/database topology factories
  audit/         one durable-audit choice, several typed decorators
  eventpg/       one PostgreSQL event-source choice

external adapter modules, only on real demand
  storage/storageminio/       MinIO SDK
  jobs/jobsredis/             Redis client; current module is BUILDING
  messaging/<broker adapter>/ one broker ecosystem targeting a neutral delivery seam, if justified
```

The names are working decisions pending the common architecture ADR. The
important part is the dependency direction. There is no `storageotel`,
`auditotel`, `tenancyjwt`, `eventaudit`, `jobsotel` or bundle module. A root
stdlib-only package does not get a nested module merely to make the diagram
symmetrical.

Genuine transport bindings such as CRUD × Gin remain valid adapter decisions:
they implement the transport-facing base contract and isolate Gin. They are not
permission for one optional business extension to import another.

## Composition model

Application code selects independent layers:

```go
service := port.ChainService(
    port.NewService(repository),
    tenancy.Service[Order, OrderID, OrderUpdate](tenantPolicy),
    audit.Service[Order, OrderID, OrderUpdate](auditRuntime),
    vvotel.Service[Order, OrderID, OrderUpdate](telemetry),
)

renderer := porthttp.NewRenderer(
    porthttp.WithMessages(i18n.Messages(catalogue)),
)
```

This is an illustrative shape. It becomes documentation API only after the
base service chain and each named extension are implemented. The application,
not a registry, owns ordering and lifecycle.

## Delivery horizons

### P0 — architecture and in-flight stabilization

1. Accept the common extension ADR and its narrow amendments to [[D-035]],
   [[D-051]], [[D-058]] and [[D-074]].
2. Inventory service, storage, cache, jobs, auth and CRUD extension seams.
3. Add only the approved dependency-neutral service/storage chain and observer
   fan-out helpers.
4. Resolve the current executable walks in `crud.ExistsUnscopedOf` and
   `cache.BatchReaderOf`; unknown wrappers must not tunnel to inner effects.
5. Stabilize the landed jobs worker/memory/PostgreSQL/Fx/Redis slices; keep their
   release evidence separate from this documentation change.
6. Add source/import checks for reverse extension edges and combination modules.
7. Record compatibility migrations for `appfiber`, `storageminiofx`, the
   `accessjwt` → `authjwt` concrete-adapter edge and the JWT-nested Redis
   revocation adapter; do not silently grandfather them.

Exit evidence:

- two unrelated fake extensions compose through each new base seam;
- optional effects remain exact or fail closed through opaque neighbours;
- root and every pre-existing module remain free of optional extension graphs;
- current jobs/cache tests and module checks are green from a clean reviewable
  state.
- every current satellite-to-satellite import is classified as an allowed
  adapter-to-owner edge or an explicit migration target.

### P1 — finish current jobs/cache commitments

Use the [current jobs/cache revision](2026-09-01-jobs-cache-roadmap.md) to split
delivered, in-progress and deferred work. Do not block a reliable queue/core on
batches, workflows, Redis or every scheduling feature.

Exit evidence:

- tracked APIs have deterministic/race/conformance coverage;
- memory and PostgreSQL drivers advertise only proven capabilities; wrappers do
  not tunnel to an inner `jobs.Admin` and applications authorize its
  payload-bearing views explicitly;
- the durable context seam and any later wired/accepted worker observation or
  lifecycle seam can be adapted without importing OTel or tenancy;
- remaining features are explicit future milestones rather than placeholder
  methods.

### P2 — prove the extension model with OpenTelemetry

Implement the [current OTel roadmap](2026-08-31-opentelemetry-roadmap.md): one
optional `vvotel` module, injected providers, typed factories and no SDK/exporter
or optional-satellite dependency in its first production graph.

This horizon is the conformance proof for every later cross-cutting extension.
If OTel requires a pairwise package, global registry or hidden capability walk,
the common architecture is not ready.

### P3 — activate independent feature extensions

The following are independently selectable, not one required stack:

| Extension | Activation signal | First accepted slice |
|---|---|---|
| [Tenancy](2026-09-01-multitenancy-roadmap.md) | A consumer needs framework-owned topology beyond existing policy scopes | Verified scope plus one row-isolation topology; database-per-tenant remains a separately tested factory in the same extension |
| [Audit](2026-09-01-audit-log-roadmap.md) | A named resource needs durable, authorized evidence | One exact repository mutation plus the PostgreSQL persistence profile; a service factory waits for the base chain and atomicity gate |
| [PostgreSQL event sourcing](2026-09-01-postgres-event-sourcing-roadmap.md) | A named aggregate needs append/replay/versioning | One aggregate stream, optimistic append and schema/upcaster registry |
| [Full i18n](2026-09-01-i18n-roadmap.md) | Error catalogues are insufficient for plural/select application messages | Immutable catalogue plus `errs.MessageSource`; no new transport binding |

One extension may ship before another when its readiness gate is satisfied.
Audit does not require event sourcing; event sourcing does not imply audit;
tenancy is not authentication; i18n is not transport routing.

### P4 — backend and ecosystem breadth

Add a backend/transport/provider module only when:

1. a real consumer selects that ecosystem;
2. the base interface is already stable and conformance-tested;
3. the adapter introduces one independent dependency decision;
4. it imports only its owning stable contract and the selected ecosystem, never
   an unrelated optional extension or another concrete adapter;
5. its absence leaves every other feature fully usable.

## Disposition of the old capability catalogue

| Old cards | Current disposition |
|---|---|
| F-01–F-03 events/outbox/inbox | Re-evaluated by the PostgreSQL event-source revision; no generic package tree is pre-created |
| F-04/F-06 jobs and scheduling | Rebased onto the implemented/in-progress `jobs` surface |
| F-05 broker adapters | Deferred until one broker is a real independent consumer choice |
| F-07 workflow bridge | Still external-engine territory; no jobs/workflow combination package |
| F-09–F-14 operation policies | Typed decorators only at owning base seams; no universal heterogeneous registry |
| F-15/F-16 cache | Rebased onto the current cache implementation and its remaining gaps |
| F-21 audit | Replaced by the [current audit revision](2026-09-01-audit-log-roadmap.md) |
| F-25/F-26 runtime | Application-owned lifecycle and existing `app` seams; no global extension runtime |
| F-27/F-28 neutral observer/OTel | Superseded by [[D-048]] and the current single-module OTel roadmap |
| F-31–F-35 developer tooling | Remain independent product candidates and do not justify extension packages |

The rest of the historical catalogue remains research input, not an implied
module manifest.

## Dependency and composition gates

Every active product slice must prove:

- root production imports remain third-party-free;
- a new extension module imports root seams and its one ecosystem, never another
  extension;
- a new adapter module imports one base contract and one external ecosystem;
- container/backend/revocation wiring does not import a second concrete adapter;
- service/storage/jobs/cache composition is deterministic and preserves error
  identity, contexts and optional effects;
- disabled/absent extensions preserve existing behaviour and module graphs;
- application examples contain no framework-specific decorator boilerplate that
  belongs in the selected extension's factory;
- `GOWORK=off` fixtures cover root-only, extension-only and representative
  multi-extension applications;
- module discovery, `go.work`, tags and dependency snapshots remain exhaustive.

## Definition of done

This product revision is implemented only when:

1. P0 architecture and base-seam evidence is accepted;
2. the live roadmap reflects the real delivered/in-progress/proposed states;
3. jobs/cache and OTel meet their feature-specific definitions of done;
4. every activated optional feature has one module boundary and no pairwise
   integration package;
5. historical snapshots are visibly linked to current revisions;
6. examples prove hand-wired linear composition and explicit lifecycle;
7. dependency, race, capability, privacy and release gates pass for the exact
   promises made by each shipped feature.
