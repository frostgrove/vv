# otel (vvotel)

`github.com/frostgrove/vv/otel` (package `vvotel`) provides OpenTelemetry decorators and adapters for base seams of the framework (`port.Service`, `storage.Store`, `cache.Observer`, `cachememory.Observer`).

The module imports only the OpenTelemetry trace and metric APIs (GA). It imports no SDKs, exporters or logging bridges, borrows injected providers, and leaves SDK bootstrap and shutdown to the application.

The current contract is `ContractVersion = vv-otel/v1` and instrumentation scope
`github.com/frostgrove/vv/otel` at `ScopeVersion = v0.1.0`. The generated registry
is the source of truth for names, mappings, privacy classes, cardinality bounds
and migration metadata; see [otel-spec.md](../otel-spec.md).

## What you get

- `vvotel.New` factory handle accepting injected `trace.TracerProvider` and `metric.MeterProvider`;
- `vvotel.Service` generic middleware for `port.Service`, emitting INTERNAL spans (`vv.command <op>`) and recording duration histogram (`vv.command.duration`);
- `vvotel.Store` middleware for `storage.Store`, emitting INTERNAL spans (`vv.storage <op>`);
- `vvotel.Cache` and `vvotel.CacheMemory` terminal event observers recording `vv.cache.operations` counters and optional span events;
- Safe closed schema, privacy-safe error classification, and bounded cardinality guarantees.

`ResourceName` is trace-only, optional, and accepted only as a short logical
name using letters, digits, `.`, `_` or `-` (maximum 64 bytes). IDs, keys, URLs,
payloads, messages and credentials are never telemetry labels. Unknown cache
values and application-specific error codes are omitted.

The application owns SDK exporters, readers/processors, flush and shutdown. A
runnable stdout SDK setup is in [`_examples/otel-sdk-bootstrap`](../../../_examples/otel-sdk-bootstrap/).

Maintenance uses `make generate` and `make check-otel-schema`; the latter is a
read-only freshness gate. `make version V=v0.1.0` updates the registry scope
version and regenerated source. Before publishing, run
`make check-otel-consumer V=v0.1.0` in an environment where the lockstep root and
`otel` tags are available.

## Setup

```go
telemetry, err := vvotel.New(vvotel.Config{
    TracerProvider: tracerProvider,
    MeterProvider:  meterProvider,
    ResourceName:   "products",
})
if err != nil {
    log.Fatal(err)
}

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
```
