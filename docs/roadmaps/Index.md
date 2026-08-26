# Roadmaps

Roadmaps contain work that is not yet built. The live inventory stays short and
is the source of truth for whether an item remains open; dated delivery plans
turn that inventory into a proposed order of work without rewriting history.

| Document | Role |
|---|---|
| [Roadmap.md](Roadmap.md) | Live inventory of open work and decisions needed to close it |
| [2026-08-26-1522-product-roadmap.md](2026-08-26-1522-product-roadmap.md) | Proposed product and delivery sequence, including a 35-capability framework catalogue with declarative DX, happy paths, edge cases and intentional deferrals |
| [2026-08-26-1558-opentelemetry-roadmap.md](2026-08-26-1558-opentelemetry-roadmap.md) | Deep OpenTelemetry satellite roadmap: safe semantic schema, service/repository instrumentation, metrics, async causal links, privacy, release compatibility, and integration gates for storage, events, tenancy, audit and i18n |
| [2026-08-26-1558-storage-roadmap.md](2026-08-26-1558-storage-roadmap.md) | Filesystem and S3-compatible object-storage roadmap: portable key/stream/conditional-write contract, secure fs adapter, MinIO/AWS/R2 S3 conformance, staging, signing, versioning and lifecycle boundaries |
| [2026-08-26-1558-i18n-roadmap.md](2026-08-26-1558-i18n-roadmap.md) | Full i18n satellite roadmap: BCP 47 negotiation, immutable catalogues, declared message arguments, CLDR plurals, error rendering and language-neutral observability |
| [2026-08-26-1558-postgres-event-sourcing-roadmap.md](2026-08-26-1558-postgres-event-sourcing-roadmap.md) | PostgreSQL-only event-sourcing roadmap: aggregate append semantics, optimistic concurrency, projections/outbox, snapshots, event revisions, upcasters and release compatibility; studies the eugene-khyst PostgreSQL template and Axon versioning model |
| [2026-08-26-1558-multitenancy-roadmap.md](2026-08-26-1558-multitenancy-roadmap.md) | One-database row isolation and database-per-tenant roadmap: verified scope, complete query matrix, datasource routing, migration/lifecycle and safe cross-domain integration |
| [2026-08-26-1558-audit-log-roadmap.md](2026-08-26-1558-audit-log-roadmap.md) | Hibernate/Envers-inspired audit revision roadmap: declared resource/action policies, typed redacted diffs, transactional PostgreSQL evidence, protected audit queries and trace/event boundaries |
| [retired-sections.md](retired-sections.md) | Reference map for citations to two deleted roadmaps; not an active plan |

When an item is finished, remove it from the live inventory. A dated roadmap is
a record of the priorities at the time it was written; do not edit it to pretend
that later choices had already been made.
