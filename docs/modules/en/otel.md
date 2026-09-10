# otel (vvotel)

`github.com/frostgrove/vv/otel` (package `vvotel`) provides optional
OpenTelemetry adapters. Its current runtime adapters cover `port.Service`,
`storage.Store`, `cache.Observer` and `cachememory.Observer`.

Current production code imports only the stable OpenTelemetry `trace`, `metric`,
`attribute` and `codes` API packages. [[D-128]] also permits `propagation`, but its
W3C Trace Context use belongs to the still-planned durable-jobs adapters. The
module imports no SDK, exporter, Collector client, contrib instrumentation,
`otelslog`, OTel Logs API or database-driver bridge. It borrows providers and
leaves SDK bootstrap, native instrumentation, export, flush and shutdown to the
application.

The current generated contract is `ContractVersion = vv-otel/v2`; the
instrumentation scope is `github.com/frostgrove/vv/otel` at
`ScopeVersion = v0.1.0`. The checked-in registry, generated Go schema and wire
manifest contain 59 stable signal IDs. Six are `implemented`; 53 are `planned`
and are not emitted yet. The registry is the source of truth for names,
mappings, privacy classes, cardinality bounds, availability and migration
metadata; see [otel-spec.md](../otel-spec.md).

## What you get

- `vvotel.New` and `vvotel.Must`, accepting injected `trace.TracerProvider` and
  `metric.MeterProvider`;
- closed per-signal selection through `Config.Disable` and eager fail-fast
  construction of all enabled provider-compatible metric instruments;
- `vvotel.Service` generic middleware for `port.Service`, emitting INTERNAL spans (`vv.command <op>`) and recording duration histogram (`vv.command.duration`);
- `vvotel.Store` middleware for `storage.Store`, emitting INTERNAL spans (`vv.storage <op>`);
- `vvotel.Cache` and `vvotel.CacheMemory` terminal event observers recording `vv.cache.operations` counters and optional span events;
- `vvotel.TraceHandler`, an independent stdlib `slog.Handler` decorator that
  adds an all-or-none `trace_id`/`span_id`/`trace_flags` triplet from a valid
  context without exporting logs or overwriting caller-owned fields;
- fail-safe operation recording: a broken trace path cannot suppress a working
  metric or alter the business call, and a broken metric cannot suppress the
  span;
- generated allow-list admission before every emitting OTel call, privacy-safe
  error classification, and bounded cardinality guarantees.

Those four adapters emit exactly the six signals marked `implemented`: command
span/duration, storage span, cache operation count, cache facade event and cache
backend event. The other 53 descriptors remain non-emitting, but their enabled
metric instruments are assembled eagerly so a broken provider fails at startup.
No observable callback is registered by `New`; aggregate cache-memory callback
registration remains a separate planned operation.

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

Planned signal ID 5, `storage_operation_bytes`, has only successful `put/ok` and
`stage/ok` variants. It will record persisted size from the returned
`Info.Size` or `Staged.Info.Size`; it will never wrap or read the source and
will not claim to measure bytes actually consumed from the reader.

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
Views, sampling, native instrumentation, flush and shutdown. A runnable stdout
SDK setup is in
[`_examples/otel-sdk-bootstrap`](../../../_examples/otel-sdk-bootstrap/).

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

service := port.ChainService[Product, string, Product](
    baseService,
    vvotel.Service[Product, string, Product](telemetry),
)

store := storage.Chain(
    baseStore,
    vvotel.Store(telemetry),
)

cacheRuntime.Observer = cache.MustObservers(
    existingObserver,
    vvotel.Cache(telemetry, vvotel.WithCacheSpanEvents(true)),
)

logger := slog.New(vvotel.TraceHandler(slog.NewJSONHandler(os.Stdout, nil)))
```

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
`vvotel` does not register callbacks, roll back providers, start goroutines or
own shutdown during assembly.

For OTel log export, the application creates its native `otelslog` handler,
wraps that handler with its own redaction policy, then passes the result to
`vvotel.TraceHandler`. Correlation is therefore added before the redacting/export
chain sees the record. Applications that want native behavior only pass the
`otelslog` handler directly; `vvotel` neither imports it nor owns its provider.
