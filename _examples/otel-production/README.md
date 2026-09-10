# Production OTLP ownership recipe

`NewTelemetry` builds application-owned trace and metric SDKs and returns the
providers explicitly. Pass those providers to Frostgrove adapters and every
native transport/database integration. The recipe never calls `otel.Set*`.

```go
telemetry, err := NewTelemetry(ctx, Config{
    ServiceName:       "orders-api",
    ServiceVersion:    "1.0.0",
    ServiceNamespace:  "commerce",
    FrameworkResource: vvotel.MustApproveName("orders"),
})
```

OTLP endpoints and credentials use the upstream `OTEL_EXPORTER_OTLP_*`
environment variables. The default sampler is `ParentBased(TraceIDRatioBased(0.1))`;
set `Sampler` for full low-level control. `Views` are passed unchanged to the
metric provider in addition to schema-derived Frostgrove Views. Those defaults
filter attribute keys before aggregation and use the registry's histogram
buckets. Set `FrostgroveViewsDisabled` only when replacing them deliberately.
The periodic reader uses trace-based exemplars.

`ForceFlush` attempts traces and metrics with independent time budgets.
`Shutdown` first rejects new flushes, waits for admitted flushes, then attempts
both provider shutdowns with independent budgets. The first caller owns cleanup;
concurrent callers may time out without canceling it, and completed callers all
receive the cached cleanup result.

Application shutdown order is: stop admission; drain HTTP, gRPC, jobs and
runtime work; finish response bodies and database rows; close pools; unregister
owned callbacks; flush traces and metrics; shut down both providers. Bound every
step and continue cleanup after errors.

Native middleware, database hooks, route/RPC Views and export-time privacy
projection remain application modules. Removing this recipe or any native
integration does not alter `vvotel` or the framework kernel.
