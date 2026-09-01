# Roadmaps

Roadmaps describe delivered baselines, remaining work and proposed features.
The live inventory is the source of truth for whether an item remains open.
Current revisions supersede obsolete status and package-layout assumptions;
older dated bodies remain historical records.

## Current roadmaps

| Document | Role |
|---|---|
| [Roadmap.md](Roadmap.md) | Live inventory of open work and decisions needed to close it |
| [2026-09-01-extension-architecture-roadmap.md](2026-09-01-extension-architecture-roadmap.md) | Governing package/module model: base-owned typed seams, one module per independent extension, linear application composition, exact capabilities and migration of legacy combination satellites |
| [2026-09-01-product-roadmap.md](2026-09-01-product-roadmap.md) | Current product/delivery coordination over the real tree, with delivered, in-progress, proposed and deferred slices separated |
| [2026-08-31-opentelemetry-roadmap.md](2026-08-31-opentelemetry-roadmap.md) | Current OpenTelemetry roadmap: one optional cross-cutting `vvotel` module, base-owned typed composition points, reusable factories/decorators, versioned schema, privacy/cardinality gates and no integration package cross-product |
| [2026-09-01-storage-roadmap.md](2026-09-01-storage-roadmap.md) | Current storage baseline and remaining extension work: root Store/filesystem, independent MinIO adapter, typed Store chain and `storageminiofx` migration |
| [2026-09-01-i18n-roadmap.md](2026-09-01-i18n-roadmap.md) | Current full-i18n proposal over existing `errs.MessageSource`/locale seams: one optional catalogue module and no transport/OTel/tenancy bridges |
| [2026-09-01-postgres-event-sourcing-roadmap.md](2026-09-01-postgres-event-sourcing-roadmap.md) | Current proposed PostgreSQL event-sourcing boundary: one `eventpg` module, direct aggregate/UoW API first, neutral delivery injection and no cross-extension bridges |
| [2026-09-01-multitenancy-roadmap.md](2026-09-01-multitenancy-roadmap.md) | Current proposed tenancy boundary: one optional public package with row/database topology factories over verified base-owned scopes |
| [2026-09-01-audit-log-roadmap.md](2026-09-01-audit-log-roadmap.md) | Current proposed durable-audit boundary: one extension, typed base decorators, explicit atomicity/redaction and no audit × extension packages |
| [2026-09-01-jobs-cache-roadmap.md](2026-09-01-jobs-cache-roadmap.md) | Current jobs/cache plan rebased on implemented contracts and code-present PostgreSQL operator controls, with unresolved Admin List byte budget, building PostgreSQL/Redis drivers, D-084 backpressure and exact-capability gates |

## Historical snapshots

| Document | Historical role |
|---|---|
| [2026-08-26-1522-product-roadmap.md](2026-08-26-1522-product-roadmap.md) | Historical 35-capability product catalogue; current status, sequencing and package topology are superseded by the 2026-09-01 product revision |
| [2026-08-26-1558-opentelemetry-roadmap.md](2026-08-26-1558-opentelemetry-roadmap.md) | Historical OpenTelemetry snapshot, superseded by the 2026-08-31 revision |
| [2026-08-26-1558-storage-roadmap.md](2026-08-26-1558-storage-roadmap.md) | Historical storage implementation proposal; the subsystem and public Store surface have since shipped in a different layout |
| [2026-08-26-1558-i18n-roadmap.md](2026-08-26-1558-i18n-roadmap.md) | Historical unimplemented full-i18n/bridge proposal; current root message and locale seams supersede its package grid |
| [2026-08-26-1558-postgres-event-sourcing-roadmap.md](2026-08-26-1558-postgres-event-sourcing-roadmap.md) | Historical PostgreSQL event-sourcing research and delivery proposal; its multi-satellite bridge topology is not current architecture |
| [2026-08-26-1558-multitenancy-roadmap.md](2026-08-26-1558-multitenancy-roadmap.md) | Historical tenancy topology/integration proposal; its per-topology and cross-extension modules are superseded |
| [2026-08-26-1558-audit-log-roadmap.md](2026-08-26-1558-audit-log-roadmap.md) | Historical audit research and bridge proposal; no audit module currently exists |
| [2026-08-31-jobs-cache-roadmap.md](2026-08-31-jobs-cache-roadmap.md) | Historical jobs/cache implementation plan, now outpaced by the current tree and active driver/worker work |
| [retired-sections.md](retired-sections.md) | Reference map for citations to two deleted roadmaps; not an active plan |

When an item is finished, remove it from the live inventory. Do not rewrite a
historical body to pretend later choices had already been made; a short banner
may point readers to its current revision.
