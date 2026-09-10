# Collector resilience contract

The production examples apply `memory_limiter` first and `batch` last. Each
push exporter has a finite `queue_size`, a finite worker count, and a retry
window with finite `max_elapsed_time`. Once those bounds are exhausted,
telemetry can be dropped; the application request must not be failed or retried
on that account.

Capacity must be derived rather than copied unchanged. At minimum record:

- peak accepted spans, metric points, and log records per second;
- average encoded batch size and `send_batch_max_size`;
- tolerated backend outage duration and maximum retry amplification;
- Collector memory limit, pod/container limit, queue slots, and disk quota;
- acceptable loss and the alerts that reveal it.

The in-memory production queue survives only transient sink outages. The WAL
variant places queued batches in `file_storage`; mount both storage directories
on a dedicated persistent encrypted volume. Bound that volume independently.
A WAL can replay a batch after an ambiguous acknowledgement, so the sink must
tolerate duplicates. A WAL can also lose data at its finite disk/retention
boundary. Telemetry delivery is never exactly-once here, and the WAL is not an
audit log.

Collector self-metrics are exposed on port 8888 with raw OTLP names: unit and
type suffixes are disabled deliberately. Scrape every Collector instance and
retain at least the `otelcol_exporter_queue_*`,
`otelcol_exporter_enqueue_failed_*`, `otelcol_exporter_send_failed_*`,
`otelcol_receiver_refused_*`, memory-limiter refusal, and tail-sampling metrics.
The shipped alerts treat queue exhaustion, refusal, and early tail eviction as
telemetry loss. Logs are JSON and may still contain backend or SDK diagnostics;
ship them under the deployment's ordinary secret-handling policy.

The business Prometheus endpoint on 9464 uses
`UnderscoreEscapingWithoutSuffixes`. Changing that translation strategy changes
every name consumed by the shipped rules and must be a reviewed migration.
Authentication, TLS trust, exporter headers, and backend endpoints are
deployment inputs and intentionally have no sample secret values in version
control.
