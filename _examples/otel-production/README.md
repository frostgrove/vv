# Production OTLP ownership recipe

`NewTelemetry` creates application-owned trace and metric SDK providers and one
OTLP trace/metric exporter pair. It never changes OpenTelemetry globals.

Frost signals are projected by the built-in schema projector. Native signals
must be connected explicitly with one trace layer and/or metric layer. A layer
declares the exact instrumentation scope tuple (name, version, schema URL and
scope attributes) that its projector owns:

```go
httpScope := instrumentation.Scope{
	Name:    otelhttp.ScopeName,
	Version: otelhttp.Version,
}

telemetry, err := NewTelemetry(ctx, Config{
    ServiceName:       "orders-api",
    ServiceVersion:    "1.0.0",
    ServiceNamespace:  "commerce",
    FrameworkResource: vvotel.MustApproveName("orders"),
    TraceProjectionLayers: []TraceProjectionLayer{{
        Scopes: []instrumentation.Scope{httpScope},
        Wrap: func(next sdktrace.SpanExporter) (sdktrace.SpanExporter, error) {
            return newHTTPSpanProjection(next), nil
        },
    }},
    MetricProjectionLayers: []MetricProjectionLayer{{
        Scopes: []instrumentation.Scope{httpScope},
        Wrap: func(next sdkmetric.Exporter) (sdkmetric.Exporter, error) {
            return newHTTPMetricProjection(next), nil
        },
    }},
    Views: httpMetricViews(),
})
```

Each layer receives only its declared scopes. Sibling scopes bypass it, while
the layer's output is restricted to its own scopes. A final exact-scope gate
drops undeclared and near-match scopes before OTLP. `Wrap` must return a
synchronous exporter decorator and forward `ForceFlush`/`Shutdown` to `next`
exactly once. Duplicate scopes, empty scope sets, invalid scopes and nil
wrappers are rejected. The configuration and nested scope slices are copied.

Pass `telemetry.TracerProvider`, `telemetry.MeterProvider` and
`telemetry.Propagator` directly to native middleware and database hooks. Native
metric filtering and aggregation stay in `Config.Views`; exporter layers are
the final privacy/contract boundary.

OTLP endpoints and credentials use `OTEL_EXPORTER_OTLP_*`. The default sampler
is `ParentBased(TraceIDRatioBased(0.1))`; `Sampler` overrides it. Frost Views
filter attributes before aggregation and install schema buckets. The periodic
reader uses trace-based exemplars.

Shutdown order is: stop admission, drain work, finish response bodies and rows,
close pools, unregister callbacks, call `ForceFlush` with its own timeout, then
call `Shutdown` with a fresh timeout even when flushing failed. Both methods
attempt trace and metric providers independently.
