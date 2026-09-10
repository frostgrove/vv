# UC-030 — Observe the framework with OpenTelemetry without surrendering control

**Actor:** the application operator and telemetry architect
**Covered by:** [[FL-034]]

## Scenario

An application needs traces, metrics and log correlation for framework-owned
semantics across synchronous requests, storage, caches, authentication, health,
background work and durable jobs. The common path should require one optional
integration and a few decorators. The application must still control the native
OpenTelemetry SDK and be able to omit or replace every adapter.

## What must hold

1. One optional integration covers every admitted framework seam. Core and
   unrelated optional components have no OpenTelemetry dependency or
   OpenTelemetry-typed contract.
2. The application owns deployment identity, providers, sampling, views,
   exemplars, processors, exporters, native transport/database/runtime
   instrumentation, Collector configuration, flush and shutdown. The integration
   borrows providers and mutates no global.
3. With trace and metric providers supplied, zero signal-selection configuration
   enables every compatible registered signal. An unknown selection or failure
   to construct an enabled instrument is reported during assembly; the disabled
   form performs no work.
4. Every emitted name, unit, bound, operation, outcome, reason, failure and
   attribute is defined by one generated versioned registry. An unknown enum
   value never becomes an arbitrary string label.
5. IDs, payloads, SQL, keys, URLs, headers, cookies, credentials, tenant/user/job
   identity, error text and stack traces never enter attributes created by the
   integration. An explicitly approved bounded logical name is trace-only and
   never a metric label. Filtering occurs before recording, including exemplars.
6. Each adapter invokes the wrapped business operation exactly once. Trace and
   metric failures are isolated from each other and from the operation's result.
   Success omits an error type and leaves span status unset; cancellation,
   timeout, bounded subsystem errors and panic remain distinct. A panic is
   observed, the original value is re-panicked, and an explicit goroutine exit
   is not reported as a panic.
7. A span-derived context reaches the wrapped operation and its metric record.
   Nested work therefore has the right parent and an eligible metric point can
   carry an exemplar for the exact span.
8. A service command has one logical INTERNAL span and duration measurement.
   Storage has a separate operation measurement; opening a stream ends at
   reader return, while optional stream observation covers actual reads and
   close without buffering, retrying or retaining a request context. Stable
   persisted-size signal has only successful direct-write and staged-write
   variants: it records authoritative size metadata from the successful return,
   never wraps or reads the source, emits no failed sample and makes no claim
   about bytes actually consumed from the reader.
9. Cache facade and backend events remain distinct. Closed reasons, outcomes,
   item counts and byte distributions are retained, while fast hits create no
   spans. Memory occupancy is an explicitly registered aggregate over a fixed
   set, and collection never calls the cache or creates a per-instance series.
10. Direct data-source calls may be observed without erasing transactions,
    replicas, source identity, nested transaction discovery or native bulk.
    Optional effects appear only when the wrapped value really has them. Query
    observation ends when rows are returned; complete client, pool and
    rows-lifetime telemetry stays with native driver instrumentation.
11. A logical remote call is distinct from its wire call: the framework emits
    one INTERNAL layer and the application-selected client instrumentation emits
    one CLIENT layer. Neither duplicates the other.
12. The complete authenticator chain is measured once. Refusals are counted by
    their closed reason and may annotate an active request span; credential,
    principal, scheme, detail and cause never travel. No authorization decision
    is inferred from an incomplete seam.
13. Health telemetry wraps only a probe the registry was already going to run.
    Metric collection never invokes a probe, changes freshness/coalescing or
    chooses importance. Runtime telemetry reports real run transitions and
    drainer completions, and periodic telemetry measures one pass rather than a
    process-lifetime loop.
14. Durable jobs carry W3C Trace Context without carrying baggage or treating
    trace data as identity. Adding or extracting correlation changes no durable
    tenant, actor, token, provenance, epoch, lineage or context-lifetime field.
15. Trace data written under the previous durable grammar remains valid job
    data. If native OpenTelemetry cannot parse it, only correlation is dropped
    or reduced; identity restoration and handler execution are not refused.
16. A non-transactional enqueue produces one PRODUCER operation. A
    transaction-bound enqueue reports an INTERNAL staged intent and never claims
    publication or commit. The producer context is captured into the durable
    record.
17. Each returned handler invocation has its own CONSUMER span linked to the
    producer by default. Retries create separate spans. The span covers the
    handler body only, not decode, identity restore, timeout arbitration,
    disposition or persistence of that disposition.
18. Worker and scheduler metrics use actual operation contexts and closed
   control-plane outcomes. They create no polling span, reconstruct no
   authoritative backlog and do not add workflow history or orchestration. A
   worker apply event takes its reason from a nonzero command disposition and
   otherwise from the command, preserving the exact root
   command/disposition/reason contract before telemetry maps it.
19. A context-aware log handler can add the current trace/span identifiers
   without exporting or redacting logs. Missing context or collision with a
   reserved key leaves the record unchanged, and existing attributes/groups
   remain caller-owned. Request code uses its caller-owned logger from context;
   long-lived runtime code keeps any existing application-owned logger, and
   every site passes its operation context whenever one exists. No binding gains
   a logger option.
20. Native OpenTelemetry APIs remain available at every layer. Omitting an
    adapter removes that semantic layer without replacing the underlying
    operation or native instrumentation.
21. Where the framework's own composition binding constructs a seam, an adapter
    is put around it by contributing the layer to that binding, not by taking
    the binding apart or by decorating its output. Two layers around one seam
    compose in the order the application wrote down, and an adapter the
    application asked for that never arrived — or one that arrived without being
    asked for — stops the start instead of leaving a deployment that quietly
    measures nothing.

## Out of scope

- SDK and Collector lifecycle hidden behind a framework bootstrap;
- audit or exactly-once delivery guarantees from telemetry;
- a generic observability bus or a decorator that lies about optional effects;
- durable workflow history, signals, replay or a Temporal substitute;
- event-store, event-repository and projection telemetry until its separate
  roadmap is implemented.
