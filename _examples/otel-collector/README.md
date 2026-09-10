# OpenTelemetry Collector operations example

The example is pinned to Collector Contrib `0.160.0`, multi-platform image
digest `sha256:799dc6cf12c96192af37b5bdba804da8c10b3bc563b43cb90c3f3c58d9572ad6`.
The digest, not a mutable tag, is the executable dependency.

Run the development receiver with `docker compose up`. It accepts OTLP on
localhost ports 4317 and 4318, exposes business metrics on 9464, Collector
self-metrics on 8888, and health on 13133. The debug exporter is development
only: telemetry values can be sensitive even when Frostgrove's own attributes
are bounded.

`collector.production.yaml` shows a memory-limited pipeline and a finite
in-memory exporter queue. Replace `telemetry-backend.example:4317`, configure
TLS/authentication at deployment time, and size every numeric bound from the
declared ingest rate and outage budget. `collector.production-wal.yaml` adds a
`file_storage`-backed queue. Its directory must be a dedicated persistent,
encrypted volume with an explicit disk quota and retention/backup policy. The
WAL improves survival across restarts; it does not make export exactly once and
must not be used as an audit store.

`collector.tail.yaml` is a separate tail-sampling tier. Read
[`sampling.md`](../../docs/operations/otel/sampling.md) before using it. A
single production process should not be made both an arbitrary load balancer
and an unsharded tail decision-maker.

`collector.metrics-only-management-filter.yaml` is the explicit metrics-only
variant. It removes native HTTP server datapoints carrying the bounded
`vv.http.management="true"` marker while the trace and log pipelines remain
unfiltered. The recipe writes authoritative `false` on every other server
datapoint, including method- and host-qualified routes that upstream projects
to `/live` or `/ready`; a handler Labeler cannot spoof either value.

Validate syntax and component availability with `make check-otel-collector`.
That target starts the pinned image's validator and is deliberately outside the
network-free `make check`. The normal offline gate is
`make check-otel-operations`.
