# Tail-sampling boundary

`collector.tail.yaml` keeps error traces, traces at least one second long, and
a rate-limited baseline. Its numbers are sizing examples, not universal
defaults: `decision_wait`, `num_traces`, `expected_new_traces_per_sec`, decision
cache sizes, maximum trace bytes, and baseline rate must be reviewed together
from measured arrival and lateness distributions.

Every SDK whose spans enter this managed tail-sampling path must use the literal
`AlwaysSample` sampler. `ParentBased(AlwaysSample())` is insufficient: it obeys
an unsampled remote parent and can discard a local span before export. Ratio
head sampling has the same irrecoverable boundary. A Collector can decide only
over spans it receives and cannot reconstruct a span dropped by an SDK or by an
upstream Collector.

The 30-second decision wait is the time allowed for a trace to assemble before
evaluation, not a promise that all spans have arrived. The sampled and
non-sampled decision caches make later spans follow a remembered decision only
while that trace ID remains cached. A span later than both the decision window
and cache residency can be evaluated as a new partial trace. Alert on
`otelcol_processor_tail_sampling_sampling_trace_dropped_too_early`, late-span
age, traces in memory, oversized trace drops, and policy errors before changing
the bounds.

All spans for a trace must reach the same tail-sampler instance. With more than
one instance, place a distinct gateway tier in front and use the Collector load
balancing exporter with trace-ID routing to a stable headless-service endpoint.
Do not round-robin SDKs directly across tail samplers. Scale and roll the gateway
and decision tiers independently; DNS churn, cache eviction, and a reshard can
split in-flight traces, so rollout duration must exceed the lateness window and
be tested under load.

A durable job retry is a separate trace linked to the earlier attempt. The two
trace IDs deliberately receive independent tail decisions; a link does not make
them one sampling unit. Tail sampling is an observability optimization, not an
audit or durable job-history mechanism.
