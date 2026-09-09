# Roadmaps

Roadmaps describe delivered baselines, remaining work and proposed features.
The live inventory is the source of truth for whether an item remains open.
Current revisions supersede obsolete status and package-layout assumptions;
older dated bodies remain historical records.

## Current roadmaps

| Document | Role |
|---|---|
| [Roadmap.md](Roadmap.md) | Live inventory of open work and decisions needed to close it |
| [2026-09-08-magic-first-dx-mechanics.md](2026-09-08-magic-first-dx-mechanics.md) | Focused plan for three verified gaps: branded root binding of generated contribution kinds, migration of `crudsqlfx` to an application-owned pool, and atomic client-revision preconditions. The former framework feature catalogue is explicitly out of scope |
| [2026-09-01-extension-architecture-roadmap.md](2026-09-01-extension-architecture-roadmap.md) | Governing package/module model: base-owned typed seams, one module per independent extension, linear application composition, exact capabilities and migration of legacy combination satellites |
| [2026-09-01-product-roadmap.md](2026-09-01-product-roadmap.md) | Current product/delivery coordination over the real tree, with delivered, in-progress, proposed and deferred slices separated |
| [2026-09-08-opentelemetry-maximal-roadmap.md](2026-09-08-opentelemetry-maximal-roadmap.md) | Current maximal OpenTelemetry delivery: reliable instruments, real-SDK exemplars, service/storage/CRUD/cache/remote/auth/health/runtime/jobs adapters, durable jobs propagation, log correlation and app-owned SDK/Collector operations without a central bootstrap |
| [2026-09-01-storage-roadmap.md](2026-09-01-storage-roadmap.md) | Current storage baseline and remaining extension work: root Store/filesystem, independent MinIO adapter, typed Store chain and `storageminiofx` migration |
| [2026-09-01-i18n-roadmap.md](2026-09-01-i18n-roadmap.md) | Implemented optional i18n core and CLI: pinned MF2 profile, source/review/tooling with conservative type-aware extraction, immutable snapshots, resolver/rendering, typed/public contracts, error and CRUD transport seams, controller and explicit application-owned lifecycle limits |
| [2026-09-01-postgres-event-sourcing-roadmap.md](2026-09-01-postgres-event-sourcing-roadmap.md) | Delivered root event vocabulary/store seam and PostgreSQL store under `event/eventpg`; historical E0–E4 retained. Research appendices 2026-09-08: 9 explicitly unimplemented subscription/projection/replay/recovery mechanisms; no workflow engine |
| [2026-09-01-multitenancy-roadmap.md](2026-09-01-multitenancy-roadmap.md) | Current tenancy boundary, delivered through M3: one optional public package in the root module with row/database topology factories over verified base-owned scopes, its published profile, and what M2/M4/M5 still owe |
| [2026-09-01-audit-log-roadmap.md](2026-09-01-audit-log-roadmap.md) | Current audit delivery: the executable S0 contract and trace authority are present; S1–S7 owe the four-product root kernel, usable core and sealed CRUD/Frostgrove-integration alpha before advanced history and attempts, nested PostgreSQL and final hostile-edge hardening |
| [2026-09-01-jobs-cache-roadmap.md](2026-09-01-jobs-cache-roadmap.md) | Current jobs/cache plan rebased on implemented contracts and code-present PostgreSQL operator controls, with unresolved Admin List byte budget, building PostgreSQL/Redis drivers, D-084 backpressure and exact-capability gates |

## Historical snapshots

| Document | Historical role |
|---|---|
| [2026-08-26-1522-product-roadmap.md](2026-08-26-1522-product-roadmap.md) | Historical 35-capability product catalogue; current status, sequencing and package topology are superseded by the 2026-09-01 product revision |
| [2026-08-26-1558-opentelemetry-roadmap.md](2026-08-26-1558-opentelemetry-roadmap.md) | Historical OpenTelemetry snapshot, superseded by the 2026-08-31 revision |
| [2026-08-31-opentelemetry-roadmap.md](2026-08-31-opentelemetry-roadmap.md) | Historical first `vvotel` delivery, superseded by the maximal 2026-09-08 roadmap |
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
