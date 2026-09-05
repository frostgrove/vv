# OpenTelemetry Specification & Seams for Frostgrove VV

This document records the method inventory, observation boundaries, error classifications, and privacy requirements for `github.com/frostgrove/vv/otel` (`vvotel`).

The checked-in registry is `internal/otelreg/registry.json`; `cmd/vv-otel-gen`
generates `otel/schema_gen.go`. `ContractVersion` (`vv-otel/v1`) identifies the
telemetry schema, while `ScopeVersion` follows the published `otel` module
version and is updated by `make version V=...`. Registry changes require a
regenerated file, migration metadata and a consumer-gate review.

## Method and Seam Inventory

### 1. Service Seam (`port.Service`)
8 observed operations emitting INTERNAL spans:
- `List(ctx, cmd)` -> `vv.command list`
- `Count(ctx, cmd)` -> `vv.command count`
- `Get(ctx, cmd)` -> `vv.command get`
- `Create(ctx, cmd)` -> `vv.command create`
- `Update(ctx, cmd)` -> `vv.command update`
- `Replace(ctx, cmd)` -> `vv.command replace`
- `Delete(ctx, cmd)` -> `vv.command delete`
- `DeleteMany(ctx, cmd)` -> `vv.command delete_many`

2 forwarded methods emitting NO spans:
- `Meta()`
- `Paths()`

2 optional restorable operations (observed only if underlying service satisfies `RestorableService` via `port.RestorableOf`):
- `Restore(ctx, cmd)` -> `vv.command restore`
- `RestoreMany(ctx, cmd)` -> `vv.command restore_many`

### 2. Storage Seam (`storage.Store`)
9 observed operations emitting INTERNAL spans:
- `Put(ctx, key, reader, opts)` -> `vv.storage put`
- `Open(ctx, key)` -> `vv.storage open` (span ends when stream is returned)
- `Head(ctx, key)` -> `vv.storage head`
- `Delete(ctx, key)` -> `vv.storage delete`
- `Stage(ctx, reader, opts)` -> `vv.storage stage`
- `Promote(ctx, stageID, key, opts)` -> `vv.storage promote`
- `Abort(ctx, stageID)` -> `vv.storage abort`
- `CleanupExpired(ctx, opts)` -> `vv.storage cleanup_expired`
- `TemporaryURL(ctx, key, opts)` -> `vv.storage temporary_url`

1 forwarded method emitting NO span:
- `Capabilities()`

### 3. Cache Seams
- Facade: `cache.Observer.Observe(ctx, Event)`
- Memory backend: `cachememory.Observer.Observe(ctx, Event)`
Emits span events when active recording span exists; records counter `vv.cache.operations`.

The cache operation and outcome mappings are closed. Unknown values are omitted.
The counter bound covers the union of facade and memory-backend operation/outcome
combinations; facade and backend layers remain separate attributes.

The command duration histogram uses the explicit registry boundaries and records
only closed operation/outcome/error-type values. `vv.error.code` is span-only and
is emitted only for the registry allow-list.

## Error and Status Mapping
- Success (`err == nil`): span status unset (not `Ok`), `error.type` omitted.
- `context.Canceled`: `outcome="canceled"`, `error.type="canceled"`, `Status=Error`.
- `context.DeadlineExceeded`: `outcome="timeout"`, `error.type="timeout"`, `Status=Error`.
- `errs.Kind` / `storage.Kind`: mapped to bounded `error.type` string (`not_found`, `invalid`, `forbidden`, `conflict`, `stale_version`, `internal`).
- Panic: `outcome="error"`, `error.type="panic"`, `Status=Error`, ends span and re-panics original value.

## Privacy Rules
Forbidden from spans and metrics:
- IDs (entity, tenant, user, stage, session, request);
- Storage keys, namespaces, URLs;
- SQL statements and bind values;
- Request/response payloads, headers, cookies, credentials;
- Error messages, stack traces, wrapped causes;
- Arbitrary unvalidated string labels.

`vv.resource.name` is trace-only and accepts a construction-time logical name of
at most 64 bytes with letters, digits, `.`, `_` or `-`. Invalid or oversized
names are omitted. It is never attached to metrics.
