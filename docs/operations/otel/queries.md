# Prometheus query contract

The rules assume OTLP metrics are exposed by the sample Collector Prometheus
exporter using `UnderscoreEscapingWithoutSuffixes`. Dots in metric and attribute
names become underscores; no unit or counter suffix is appended. Histograms
still expose `_bucket`, `_count`, and `_sum` series required by PromQL.

Every ratio divides events from one instrument and one semantic layer. No rule
uses `or vector(0)`, `clamp_min`, or another zero fill. An absent numerator or
denominator therefore produces no result; a present zero denominator produces
NaN or infinity. Dashboards must display those states as unknown, and alerting
must use the explicit missing-family alert rather than coloring them healthy.

| Recording rule | Numerator | Denominator and layer |
| --- | --- | --- |
| `vv:command:error_ratio5m` | command histogram observations with `error` or `timeout` | every `vv.command.duration` observation |
| `vv:storage:error_ratio5m` | storage operations with `error` or `timeout` | every storage-operation observation; stream lifecycle is excluded |
| `vv:storage_stream:failure_ratio5m` | stream terminal `error` observations | every stream terminal observation |
| `vv:http:server_error_ratio5m` | HTTP 5xx request observations without the exact management marker | all HTTP request observations with the same marker exclusion |
| `vv:cache:{hit,stale,negative}_ratio5m` | the named facade result | facade load/lookup decision results only; memory-backend events never enter this denominator |
| `vv:health:failure_ratio5m` | failing checks | all checks of the same `vv_health_importance` |
| `vv:runtime:operation_failure_ratio5m` | `run`/`drain` error or timeout | all outcomes of the same operation |
| `vv:runtime:periodic_failure_ratio5m` | periodic error or timeout | all periodic passes |
| `vv:jobs:handler_failure_ratio5m` | handler error or timeout | all handler terminal observations |
| `vv:jobs:worker_failure_ratio5m` | failed or timed-out `renew`/`apply` | all outcomes of the same worker operation |
| `vv:jobs:admission_saturation_ratio5m` | saturated admission observations | all worker admission observations |
| `vv:jobs:scheduler_failure_ratio5m` | scheduler error or timeout | all scheduler cycles |
| `vv:jobs:scheduler_placement_ratio5m` | placed-item histogram sum | due-item histogram sum from the same scheduler layer |

Command/storage and HTTP p95 records use cumulative histogram bucket rates.
Cache occupancy divides current charged bytes or entries by the corresponding
current configured limit. Auth refusal shift analysis starts with
`vv:auth_refusal:reason_share5m`, compares it with the same reason's
`vv:auth_refusal:reason_share1h`, and records the percentage-point difference as
`vv:auth_refusal:reason_share_shift5m`. Missing short or long input leaves the
shift unknown. It never mixes authentication duration from a different layer.
Eviction is an event rate, not a success ratio.

The Collector queue alert divides `otelcol_exporter_queue_size` by its directly
matching `otelcol_exporter_queue_capacity` series. It retains exporter,
data-type, Collector instance and scrape identity; rollout series with different
capacities are never combined through independent aggregates.

The default instrumentation keeps native request spans and metrics for
management endpoints, and health instrumentation independently keeps probe
calls, durations, and parentage. The net/http recipe writes the reserved bounded
metric attribute `vv.http.management="true"` only after the sealed router
executes an exact path-only `/live` or `/ready` handler, and writes `false` on
every other server datapoint. Its late policy value overrides a same-key
`otelhttp.Labeler` value. The HTTP SLO rules use `true` to remove the exact
management samples from both numerator and denominator.
They never infer management traffic from `http.route`, so an upstream projection of a method-qualified
`GET /live` or host-qualified `status.example/ready` remains in the SLO.

A metrics-only exclusion belongs in a Collector/backend datapoint filter and
must leave request spans and health parentage intact. Native instrumentation's
`WithFilter` is a different, explicit both-signals choice: it suppresses both
the request span and request metric after exact sealed-handler resolution. It
must not be presented as a metrics-only switch.
