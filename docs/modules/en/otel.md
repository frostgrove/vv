# otel (vvotel)

`github.com/frostgrove/vv/otel` (package `vvotel`) provides optional,
composable OpenTelemetry adapters for Frostgrove service, storage, cache,
authentication, health, runtime, remote, CRUD and durable-job boundaries.

Production code imports only stable OpenTelemetry API packages, including W3C
Trace Context propagation for durable jobs. The module imports no SDK, exporter,
Collector client, contrib instrumentation, `otelslog`, OTel Logs API or database
driver bridge. It borrows providers and leaves SDK bootstrap, native
instrumentation, export policy, flush and shutdown to the application.

The current generated contract is `ContractVersion = vv-otel/v2`; the
instrumentation scope is `github.com/frostgrove/vv/otel` at
`ScopeVersion = v0.1.0`. The checked-in registry, generated Go schema and wire
manifest contain 59 stable signal IDs, all with `implemented` availability. The
registry is the source of truth for names,
mappings, privacy classes, cardinality bounds, availability and migration
metadata; see [otel-spec.md](../otel-spec.md).

## What you get

- `vvotel.New` and `vvotel.Must`, accepting injected `trace.TracerProvider` and
  `metric.MeterProvider`;
- closed per-signal selection through `Config.Disable` and eager fail-fast
  construction of all enabled provider-compatible metric instruments;
- inference-friendly `vvotel.WrapService` and the lower-level `vvotel.Service`
  middleware for all `port.Service` commands;
- `vvotel.Store` for all `storage.Store` operations, including duration,
  successful persisted-size and cleanup-result measurements;
- opt-in `vvotel.WithStorageStreams` for lazy reader lifetime, terminal outcome
  and actual-byte telemetry, with `vvotel.StorageStream.Unwrap` as the raw escape;
- `vvotel.Cache` and `vvotel.CacheMemory` terminal observers;
- explicit `vvotel.CacheMemoryStats` and `vvotel.MustCacheMemoryStats` aggregate
  registration over 1–64 in-process memory backends;
- `vvotel.Auth`, `vvotel.AuthEvents`, `vvotel.Authenticator`, `vvotel.Health`,
  `vvotel.Runtime`, `vvotel.Periodic` and `vvotel.Remote` adapters;
- `vvotel.Source` for direct CRUD source, transaction, replica and admitted
  native-bulk boundaries;
- `vvotel.JobContext`, `vvotel.JobIdentity`, all four enqueue helpers,
  `vvotel.Job`, `vvotel.JobAdapter`, `vvotel.Workers` and `vvotel.Scheduler` for
  durable propagation and producer/consumer/control-plane telemetry;
- `vvotel.TraceHandler`, an independent stdlib `slog.Handler` decorator that
  adds an all-or-none `trace_id`/`span_id`/`trace_flags` triplet from a valid
  context without exporting logs or overwriting caller-owned fields;
- fail-safe operation recording: a broken trace path cannot suppress a working
  metric or alter the business call, and a broken metric cannot suppress the
  span;
- generated allow-list admission before every emitting OTel call, privacy-safe
  error classification, and bounded cardinality guarantees.

All 59 descriptors have an emitting adapter. Enabled metric instruments are
assembled eagerly so a broken provider fails at startup. `New` registers no
observable callback; cache-memory gauges begin only after the explicit
`CacheMemoryStats` call and stop after its returned registration is unregistered.

`Disable: vvotel.Signals{vvotel.SignalX, ...}` removes individual semantic
signals. Unknown and duplicate explicit IDs fail even when `Disabled` is true.
The legacy booleans remain exact aliases: command trace ID 2, command metric
ID 1, storage trace IDs 4 and 8, and cache metric IDs 9 and 12–23. They do not
disable cache span events 10 and 11.

With both providers, zero selection enables every signal family. A trace-only
configuration activates trace and context-only signals; a metric-only one
activates metric and context-only signals. With neither provider, `New` returns
`ErrNilProvider`. `Disabled` is the sole provider-free no-op and touches no OTel
API. Provider and instrument failures return a redacted `*AssemblyError` that
supports `errors.Is`; `Signal`, `Provider` and `SignalName` identify only the
closed registry location. Only a constructor-returned error is unwrapped.

Implemented signal ID 5, `storage_operation_bytes`, has only successful
`put/ok` and `stage/ok` variants. It records persisted size from returned
`Info.Size` or `Staged.Info.Size`; it never wraps or reads the source and does
not claim to measure bytes consumed from the reader.

`ResourceName` is an optional trace-only `ApprovedName`. String literals stay
concise; dynamic values use `vvotel.ApproveName` or
`vvotel.MustApproveName`. A direct `ApprovedName(value)` conversion is the
low-level escape hatch, but runtime validation still omits invalid values.
Names use letters, digits, `.`, `_` or `-`, are at most 64 bytes, and share one
32-distinct-value budget per `Telemetry` across service and storage adapters.
Duplicates cost no slot; a configured default is counted only when a trace
adapter binds it. Resource names never enter metrics or exemplars. IDs, keys,
URLs, payloads, messages and credentials are never telemetry labels. Unknown
cache values and application-specific error codes are omitted.

The command and storage decorators invoke the wrapped operation exactly once.
The span-derived context reaches both the operation and command histogram, so
an eligible exemplar points at the command span. Telemetry panics are isolated.
Business panics are recorded and re-panicked unchanged; `runtime.Goexit` ends a
span as `goroutine_exit` without an `error.type` or incomplete-operation metric.

The application owns Resource, SDK providers, exporters, readers/processors,
Views, sampling, native instrumentation, export projection, flush and shutdown.
Use the runnable
[`_examples/otel-production`](../../../_examples/otel-production/) composition
for OTLP and lifecycle ownership. The unpublished
[`test/otelnative`](../../../test/otelnative/) package contains explicitly
connected HTTP, Gin, Fiber, gRPC, HTTP-client, pgx, `database/sql`, pgxpool and
Go-runtime recipes and privacy canaries. `vvotel` never owns those contrib
integrations.

Maintenance uses `make generate` and `make check-otel-schema`; the latter is a
read-only freshness gate. `make version V=v0.1.0` updates the registry scope
version and regenerates both the Go schema and wire manifest. Before publishing,
run `make check-otel-consumer V=v0.1.0` in an environment where the lockstep root
and `otel` tags are available.

## Setup

```go
telemetry := vvotel.Must(vvotel.Config{
    TracerProvider: tracerProvider,
    MeterProvider:  meterProvider,
    ResourceName:   "products",
})

service := vvotel.WrapService(telemetry, baseService)

store := storage.Chain(
    baseStore,
    vvotel.Store(telemetry),
)

cacheRuntime.Observer = cache.MustObservers(
    existingObserver,
    vvotel.Cache(telemetry, vvotel.WithCacheSpanEvents(true)),
)

memoryObserver := vvotel.CacheMemory(telemetry, vvotel.WithCacheMemorySpanEvents(true))
memoryPrimary, err := cachememory.New(primaryLimits, cachememory.WithObserver(memoryObserver))
memorySecondary, err := cachememory.New(secondaryLimits, cachememory.WithObserver(memoryObserver))

stats := vvotel.MustCacheMemoryStats(telemetry, memoryPrimary, memorySecondary)
defer stats.Unregister()

logger := slog.New(vvotel.TraceHandler(slog.NewJSONHandler(os.Stdout, nil)))
```

The direct wrappers are the short path:

```go
source = vvotel.Source(telemetry, source)
authenticator = vvotel.Authenticator(telemetry, authenticator)
transport = vvotel.Remote(telemetry, transport)
pass = vvotel.Periodic(telemetry, pass)
```

The original middleware/observer APIs remain available when ordering or signal
selection needs to be explicit. Native transport and database instrumentation
receives the same application-owned providers; it is not hidden inside these
wrappers.

For a dynamic logical name:

```go
name, err := vvotel.ApproveName(config.ServiceName)
if err != nil {
    return err
}
telemetry := vvotel.Must(vvotel.Config{
    TracerProvider: tracerProvider,
    ResourceName:   name,
})
```

Use `New` instead of `Must` when the composition root returns startup errors.
`New` does not register callbacks, roll back providers, start goroutines or own
shutdown during assembly. `CacheMemoryStats` is the separate fallible callback
registration and returns its own explicit, idempotent cleanup handle. Validation,
duplicate registration and native callback failures match `ErrAssembly` plus
`ErrInvalidRegistration`, `ErrDuplicateRegistration` or
`ErrCallbackRegistration`; cleanup failures additionally match
`ErrCallbackUnregister`. `MustCacheMemoryStats` panics with that same typed
framework error.

Collector, sampling, PromQL and validation recipes are under
[`docs/operations/otel`](../../operations/otel/). Native HTTP, gRPC, database
and runtime composition is documented in
[`test/otelnative`](../../../test/otelnative/); production SDK/export lifecycle
is in [`_examples/otel-production`](../../../_examples/otel-production/).

For OTel log export, the application creates its native `otelslog` handler,
wraps that handler with its own redaction policy, then passes the result to
`vvotel.TraceHandler`. Correlation is therefore added before the redacting/export
chain sees the record. Applications that want native behavior only pass the
`otelslog` handler directly; `vvotel` neither imports it nor owns its provider.
