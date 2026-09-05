# FL-034 — Command and storage telemetry lifecycle

**Entry point:** `otel/service.go:executeCommand`, `otel/storage.go:executeStorage`
**Implements:** [[UC-030]] · **Governed by:** [[D-114]] [[D-048]] [[D-061]]

This flow traces how an intercepted command or storage call is measured, wrapped in an INTERNAL OpenTelemetry span, classified on failure, and emitted to duration histograms and event counters.

## The path — a command execution

1. **`serviceDecorator.Get`** — `otel/service.go`
   The application calls `Service.Get(ctx, cmd)`. The decorator invokes `executeCommand`.

2. **Start Span and Measure**
   If tracing is enabled, `t.tracer.Start(ctx, "vv.command get", trace.WithSpanKind(trace.SpanKindInternal))` starts an INTERNAL span and returns a derived context. Base attributes (`vv.component`, `vv.operation.name`, and optional `vv.resource.name`) are attached.

3. **Panic Safety Gate**
   A deferred recovery function catches panics, marks the span as Error with `error.type=panic`, records duration, ends the span, and re-panics the original value.

4. **Underlying Service Invocation**
   The inner service receives the derived context. Any downstream driver or client spans become children of this command span.

5. **Outcome Recording and Metric Update**
   - On success (`err == nil`): span outcome is set to `ok`, status remains unset, and the duration is recorded in `vv.command.duration`.
   - On failure (`err != nil`): `classifyCommandError` checks `context.Canceled`, `context.DeadlineExceeded`, and `port.KindOf(err)` to derive bounded `error.type`. Span status is set to Error, and metric attributes include `error.type`.

6. **Span Completion**
   `span.End()` is called exactly once.
