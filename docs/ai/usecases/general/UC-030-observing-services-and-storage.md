# UC-030 — Observe services, storage and cache without changing core contracts

**Actor:** the application operator and telemetry architect
**Covered by:** [[FL-034]]

## Scenario
An operator wants distributed tracing and latency metrics across service commands and storage operations, plus bounded counters for cache hits, misses, and evictions. They want standard OpenTelemetry instruments, but refuse:
- third-party telemetry dependencies in core library packages;
- combinatorial satellite modules;
- leaking PII, raw SQL, passwords, or unbounded IDs into trace and metric backends.

## What must hold

1. Core packages (`port`, `storage`, `cache`) remain stdlib-only and define neutral typed composition seams.
2. The `vvotel` module provides decorators that map commands and storage calls into INTERNAL OpenTelemetry spans.
3. The derived context produced by `trace.Tracer.Start` is passed down to inner operations so nested spans become children.
4. Telemetry attributes follow a closed low-cardinality schema. PII, SQL statements, storage keys and payload bytes are never recorded as attributes.
5. Errors are classified into closed error types (`not_found`, `invalid`, `forbidden`, `conflict`, `stale_version`, `canceled`, `timeout`, `internal`, `panic`).
6. Successful operations omit `error.type` and leave span status unset.
7. Panics are recorded safely with status Error, end the span, and are re-panicked without modification.
