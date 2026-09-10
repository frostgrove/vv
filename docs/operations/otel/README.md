# OpenTelemetry operations

These artifacts turn Frostgrove's generated wire contract into an explicit
operator contract. They do not bootstrap an SDK, install global providers, or
change request behavior. The application still owns providers, exporters,
sampling, lifecycle, and shutdown.

- [`collector.md`](collector.md) covers bounded Collector queues, retry, WAL,
  self-telemetry, and validation.
- [`sampling.md`](sampling.md) defines the deployment boundary for tail
  sampling.
- [`queries.md`](queries.md) defines Prometheus translation, denominators,
  authoritative `true`/`false` management-route marking, and missing-data
  semantics.
- [`prometheus-rules.yaml`](prometheus-rules.yaml) and
  [`prometheus-tests.yaml`](prometheus-tests.yaml) contain recording rules,
  alerts, and adversarial rule fixtures.
- [`validation.md`](validation.md) separates offline Frostgrove validation,
  optional upstream Weaver checks, Collector startup validation, and release
  publication checks.

`make check-otel-operations` is network-free. Collector, Prometheus, and Weaver
checks are opt-in because they require a pinned container image or network
access. No artifact in this directory represents an exactly-once delivery or
audit guarantee.
