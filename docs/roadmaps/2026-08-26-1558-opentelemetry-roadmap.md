# OpenTelemetry roadmap — 2026-08-26 15:58 +05

This is a dedicated, implementation-facing roadmap for deep OpenTelemetry
integration in vv. It is deliberately separate from the product roadmap: it
has a different dependency decision, a different compatibility surface and a
large cross-cutting verification burden.

It is a proposal, not a promise that tracing alone makes an application
observable. Each card identifies the concrete vv seam, the operator question
it answers, the safe default, the non-goal and the evidence needed before a
public API is considered stable.

## Status and architectural decision

**Status:** proposed; no public OpenTelemetry module exists yet.

[[D-048]] refuses `obsotel` as a root contract: OpenTelemetry already owns the
telemetry contract. [[D-051]] requires a satellite to isolate exactly one
consumer dependency decision. Consequently the first delivery is a dedicated
module such as `github.com/frostgrove/vv/otel`, whose only consumer choice is
OpenTelemetry. It must not quietly also choose Gin, gRPC middleware, a log
backend, a metric exporter, a broker, a database driver or a cloud provider.

The root module remains stdlib-only and knows nothing about `trace.Tracer`,
`metric.Meter`, global providers or exporter setup. A consumer that does not
import the satellite gets exactly today's behaviour, including allocation and
error behaviour. A consumer that does import it decides where and how data is
exported.

`otel` may contain vv-aware decorators and helpers. Protocol-specific packages
are separate satellites when they imply a second dependency decision:

| Candidate | Decision | Initial disposition |
|---|---|---|
| `vv/otel` | use OpenTelemetry to observe vv operations | planned |
| `vv/httpotel` | use an HTTP router/instrumentation choice | defer; use upstream HTTP instrumentation first |
| `vv/grpcotel` | use gRPC and its instrumentation | defer; keep outside `vv/otel` |
| `vv/pgxotel` | use pgx instrumentation | defer; use upstream driver instrumentation |
| `vv/natsotel` | use NATS instrumentation | defer until the outbox/broker contract exists |
| `vv/otellog` | bridge a particular log implementation | defer; logs are not a vv contract |

## Intended operator experience

An application operator should be able to answer these questions without
learning vv internals:

1. Which request, job or message produced this failed command?
2. Which resource action was attempted, and did policy allow it?
3. Did the repository, remote service, object store or broker dominate latency?
4. Did an event leave the transactional boundary, and was it consumed once?
5. Is the failure systemic, tenant-local, release-local or an input error?
6. Can a trace be followed across HTTP, a worker, an outbox message and a
   webhook without leaking an end-user's private data?

Telemetry is diagnostic evidence, not a secondary authorization database. It
must therefore be useful when sampled, safe when copied to a third party, and
incapable of changing a request's result.

## Source material and vocabulary

The satellite follows OpenTelemetry's Go API and semantic conventions rather
than inventing a competing vocabulary. Traces and metrics are stable in the Go
ecosystem; logs should be treated as an optional correlation output while the
log signal remains less mature. Standard inbound and outbound instrumentation
should be reused where it exists, with vv instrumentation describing only the
application operations that upstream libraries cannot know.

Primary references:

- [OpenTelemetry for Go](https://opentelemetry.io/docs/languages/go/)
- [OpenTelemetry semantic conventions](https://opentelemetry.io/docs/concepts/semantic-conventions/)
- [OpenTelemetry Go instrumentation libraries](https://opentelemetry.io/docs/languages/go/libraries/)
- [W3C Trace Context](https://www.w3.org/TR/trace-context/)

Terms used below:

| Term | Meaning in this roadmap |
|---|---|
| trace | causal tree/graph of one operation across boundaries |
| span | timed unit of work with a name, status, attributes and events |
| resource | immutable identity of the producing process/service |
| baggage | caller-propagated cross-boundary context; untrusted input |
| metric | low-cardinality numeric aggregation for alerting and trends |
| log correlation | trace/span identifiers attached to an existing log event |
| link | causal relationship when parent/child would be untrue, especially fan-in/out |
| semantic convention | upstream-stable attribute and span-name vocabulary |

## Non-negotiable invariants

1. **No hidden global setup.** vv never calls `otel.SetTracerProvider`, installs
   an exporter, changes a propagator, or starts a background processor.
2. **No correctness dependency.** A missing provider, dropped span, exporter
   outage or sampler decision cannot change CRUD, policy, transaction, retry or
   error semantics.
3. **Context only.** Parentage propagates through the caller's `context.Context`.
   No goroutine-local state, global request map or ambient transaction lookup.
4. **No sensitive payloads.** Raw bodies, SQL text with values, authorization
   headers, cookies, principal claims, object keys containing user data and full
   query filters are excluded by construction.
5. **Bounded cardinality.** Metric attributes have a closed vocabulary. Trace
   attributes may be richer, but values are capped and reviewed as data egress.
6. **One operation, one owner.** Exactly one vv layer owns each vv span. A
   decorator never duplicates the transport or driver's span.
7. **Versioned vocabulary.** A dashboard or alert must never silently change
   meaning because an attribute name or classification moved.
8. **Tenant isolation remains policy.** Telemetry can annotate a request with a
   safe tenant class only after policy has established it; it cannot narrow a
   query, select a database or authorize a request.
9. **Failures are classified once.** The public error contract stays in `errs`.
   Telemetry records a bounded error category and cause position; it does not
   reinterpret driver errors into a new public API.
10. **No silent propagation across asynchronous work.** Every hand-off records
    its carrier, creation time and explicitly chosen parent/link relationship.

## Target topology

```
caller context
      |
HTTP / gRPC / CLI / worker instrumentation (upstream)
      |
vv application span             vv/otel satellite
      +-- port command span      Decorate(Service)
      |     +-- policy event     narrow, stable attributes
      |     +-- repository span  Decorate(Repo)
      |            +-- driver span (upstream SQL instrumentation)
      |
      +-- outbox enqueue event   future integration seam
      +-- storage span           future storage satellite adapter
      +-- audit event            future audit sink correlation
      |
exporter chosen and owned by application
```

The graph intentionally has no direct root-to-exporter edge. The application
chooses provider, sampler, processor, exporter, propagation, resources and
shutdown. vv receives `trace.TracerProvider` and `metric.MeterProvider` as
ordinary dependencies only at satellite construction time.

## Naming model

Span names are an operational API. They must be discoverable, stable and small.
Use an action-oriented name with a bounded resource segment:

| Operation | Span name | Notes |
|---|---|---|
| Service Get | `vv.service.get <resource>` | resource is declared application name, not table name by default |
| Service Create | `vv.service.create <resource>` | one logical command, including mapper/policy time |
| Repository Get | `vv.repository.get <resource>` | does not replace SQL client spans |
| Repository aggregate | `vv.repository.aggregate <resource>` | records aggregate kind only if allow-listed |
| Policy denial | event `vv.policy.denied` | event on owning command span, not a standalone span |
| Object write | `vv.storage.put` | future storage adapter, bucket not emitted by default |
| Event append | `vv.eventstore.append` | future PostgreSQL event store only |
| Tenant resolution | `vv.tenant.resolve` | future resolver; no raw tenant identifier |
| Audit write | `vv.audit.record` | future audit writer; never records old/new values |

The `<resource>` is one configuration-time declaration such as `invoice`,
`billing.invoice`, or `identity.member`. It is not derived from Go reflection,
database table names, generic types or user input. If an application has no
safe public resource name, it configures `unknown` and improves later.

## Attribute registry policy

All vv attributes carry the `vv.` prefix unless an upstream OpenTelemetry
semantic convention already exists. The registry is a package-owned allow-list,
not a convenience map passed through arbitrary decorators.

| Attribute | Signal | Allowed values | Reason |
|---|---|---|---|
| `vv.resource.name` | trace + metric | configured enum | groups generic operations |
| `vv.command.name` | trace + metric | `get`, `list`, `create`, `update`, `replace`, `delete`, `bulk_delete` | preserves port vocabulary |
| `vv.outcome` | trace + metric | `ok`, `refused`, `invalid`, `not_found`, `conflict`, `error`, `cancelled`, `deadline` | bounded result class |
| `vv.error.code` | trace + metric | documented `errs` code enum | links diagnostics to public shape |
| `vv.policy.result` | trace | `allowed`, `denied`, `not_applicable`, `error` | explains decision point |
| `vv.policy.name` | trace | configured enum | only explicit, reviewed names |
| `vv.query.shape` | trace | `by_id`, `filtered`, `paged`, `aggregate`, `bulk` | no client filter text |
| `vv.retry.attempt` | trace | small integer | per-attempt diagnostic only |
| `vv.idempotency.state` | trace + metric | fixed lifecycle enum | future contract |
| `vv.tenant.mode` | trace + metric | `single`, `row`, `schema`, `database` | future topology, not identity |
| `vv.release.version` | resource/trace | bounded deployment value | supplied by application |

The following values are forbidden in all signals unless a later, explicit
security decision approves a redaction scheme:

- tenant IDs, database names, schema names, customer names and account IDs;
- principal subject, e-mail, roles, claims, bearer token, cookie and session;
- URL query values, request body, response body, mapped command or model;
- SQL statement values, raw database error text and stack traces;
- object key, original filename, bucket name and pre-signed URL;
- event payload, aggregate identifier, event identifier and correlation ID;
- arbitrary `error.Error()` output, unless an application-owned scrubber has
  separately proven it non-sensitive.

Hashing is not a general escape hatch: stable hashes preserve a tracking key.
For an approved support-only diagnostic, use a per-deployment rotating keyed
hash, document its retention and ensure it cannot enter metrics.

## Configuration shape

The public construction must make responsible choices explicit and cheap:

```go
provider := sdktrace.NewTracerProvider(/* app owns exporter and sampler */)
meterProvider := sdkmetric.NewMeterProvider(/* app owns reader */)

telemetry, err := vvotel.New(vvotel.Config{
    TracerProvider: provider,
    MeterProvider:  meterProvider,
    InstrumentationName: "example.com/billing",
    ResourceName: "billing.invoice",
    RecordPolicy: true,
})
if err != nil { /* fail at bootstrap, before serving traffic */ }

svc := vvotel.DecorateService(telemetry, invoices)
repo := vvotel.DecorateRepo(telemetry, invoiceRepo)
```

This is illustrative, not a committed API. The final API must satisfy the
following decisions:

- accept interfaces/providers, never a concrete SDK exporter;
- reject an empty instrumentation name or unbounded resource name at startup;
- use the provider passed to `New`; do not consult a global provider later;
- enable metrics and per-operation detail independently;
- permit a no-op instance for tests and feature flags without a nil maze;
- expose `Shutdown` only if vv itself owns no application exporter work; the
  expected answer is normally **no shutdown method**;
- have no config field for raw-body, raw-query, payload or SQL recording.

## Delivery order

The satellite is large because cross-cutting observability is easy to make
plausible and hard to make truthful. Ship in strict order:

1. bootstrap, naming and attribute registry;
2. service/repository command traces with in-memory tests;
3. error/policy classifications and low-cardinality metrics;
4. context propagation and asynchronous hand-off conventions;
5. integration guides for the future event, storage, tenant and audit modules;
6. release-compatible dashboards, migration rules and performance gates.

Do not add a broker, cloud or transport adapter merely to demonstrate phase 4.
Each is a separate consumer dependency decision and must first have a real vv
contract to observe.

---

## O-01 — bootstrap without global ownership

**Decision.** `vv/otel` accepts caller-owned providers and creates named
instrumentation. It owns no exporter, sampler, propagator, resource detector,
batch processor or lifecycle goroutine.

### Top-level declarative DX

```go
telemetry, err := vvotel.New(vvotel.Config{
    TracerProvider: provider,
    MeterProvider: meterProvider,
    InstrumentationName: "acme.example/orders",
    ResourceName: "orders.order",
})
```

### Happy use cases

1. An application installs an OTLP exporter and a resource on its own provider,
   passes that provider to vv, then receives vv spans under the same trace as
   its HTTP server span.
2. A CLI uses an always-on simple processor while tests use an in-memory span
   recorder. The same vv configuration API works for both.
3. A service that intentionally does not export metrics passes a no-op meter
   provider and still receives traces.
4. Two independent vv services in one binary choose distinct instrumentation
   names and resource names without modifying a process-global provider.
5. A consumer bootstraps providers before handler construction, validates the
   configuration once, and fails its own startup deterministically on error.

### Edge use cases

1. `TracerProvider` is nil. Construction returns a configuration error; it does
   not panic or silently read the global provider.
2. `InstrumentationName` is blank. Construction refuses it because unnamed
   instrumentation cannot be selectively enabled or diagnosed.
3. `ResourceName` contains a tenant ID. The configured-name validator refuses
   invalid characters/length and documentation makes identity prohibited.
4. The application uses a custom sampler that drops every vv span. Commands
   still run identically and metric recording follows the meter's own policy.
5. An exporter blocks or returns errors. It cannot be awaited on the command
   path by vv; SDK processor behaviour remains the application's choice.
6. A test constructs two telemetry instances with two recorders. Neither leaks
   spans to the other, even when both execute concurrently.

### Invariants and acceptance evidence

- no package init installs telemetry state;
- `go list -deps` confirms no exporter, protocol, router, database or cloud SDK
  leaks into `vv/otel` beyond the chosen OpenTelemetry API/SDK decision;
- a race test runs instances with distinct providers and sees isolated spans;
- a no-provider/misconfiguration matrix is covered without a global reset;
- a benchmark proves disabled tracing does not allocate on the ordinary command
  path beyond the upstream API's unavoidable context check.

### First implementation slice

Create configuration validation, a small `Telemetry` value, a no-op internal
implementation, and in-memory-recorder tests. Do not decorate a repository or
service until cardinality and name validation are already executable rules.

---

## O-02 — application resource identity

**Decision.** vv does not construct the OpenTelemetry `Resource`; the host
service owns service name, service version, deployment environment, host and
cloud resource detection. vv adds only instrumentation scope and an explicit,
bounded logical resource name on operation spans.

### Top-level declarative DX

```go
orders := vvotel.WithResource(telemetry, "orders.order")
ordersService := vvotel.DecorateService(orders, service)
```

### Happy use cases

1. `service.name=checkout-api` is supplied once by the host; all vv spans join
   it automatically through the passed provider.
2. One binary hosts `billing.invoice` and `identity.member`; operators can
   group their vv operations by `vv.resource.name` without confusing it with
   deployment resource attributes.
3. A release adds a resource name to a previously generic service. Historical
   dashboards retain the old `unknown` series while new telemetry is queryable.
4. A library consumer chooses `acme.payment` as resource name even though its
   Go type and database table have internal names.

### Edge use cases

1. A resource rename would fracture an SLO. The release checklist treats it as
   telemetry schema migration, not a harmless refactor.
2. Hundreds of dynamically registered models exist. The app must map them to
   an allow-listed family or decline fine-grained metrics; reflection cannot
   create unbounded metric labels.
3. A model's user-provided display name resembles a resource name. It is never
   used, even if convenient in a span UI.
4. A package forgets to name its resource. It emits `vv.resource.name=unknown`
   only when the app selected an explicit compatibility mode; strict mode fails
   construction.

### Invariants and acceptance evidence

- no vv code reads `service.name`, environment variables or cloud metadata;
- all recordable resource names pass one length/character/enum validator;
- test recorder assertions prove the configured name survives every command;
- migration guide includes rename, merge and removal of a resource series.

### First implementation slice

Define `ResourceName` as a validated, opaque value. Keep it independent from
`crud.Core` generic types and database metadata. Publish the validator's limits
as a compatibility contract before anyone builds a dashboard on it.

---

## O-03 — a central semantic and privacy registry

**Decision.** Attribute keys, enumerated values, span names, event names and
their privacy class live in one reviewed registry. Decorators cannot assemble
their own arbitrary `attribute.KeyValue` lists.

### Top-level declarative DX

```go
span := telemetry.Start(ctx, vvotel.ServiceCreate, resource)
span.SetOutcome(vvotel.OutcomeConflict)
span.RecordErrorCode(errs.CodeConflict)
```

### Happy use cases

1. Service and repository decorators report the same bounded outcome value for
   a conflict, so a metric does not split because one layer chose `conflict` and
   another chose `already_exists`.
2. A policy decorator emits the reviewed `vv.policy.denied` event on its parent
   command span, with a configured policy name and no principal details.
3. A future event store uses the registry's `vv.eventstore.append` name and
   adds only its explicitly accepted attributes.
4. A review can find every vv-emitted key with `rg 'vv\.'` and compare it to
   the registry test that rejects undocumented keys.
5. A documentation generator renders the registry into this roadmap's appendix
   and a machine-readable schema for dashboard linting.

### Edge use cases

1. A developer wants `filter=%s` to debug a production issue. The registry has
   no arbitrary-string API; the replacement is a safe query-shape enum.
2. A third-party error has useful text. Only the bounded public error code and
   class are permitted; raw text stays in the application's access-controlled
   logs if it chooses to record it.
3. A future metric wants `tenant_id` to find a noisy customer. Metrics reject
   it absolutely; operational support uses an audited, separately designed
   diagnostic path instead.
4. An upstream convention introduces a standard key. The registry may add it
   with a schema version and deprecation plan, but it cannot rename existing vv
   keys retroactively.

### Invariants and acceptance evidence

- a registry test rejects unregistered keys and values in emitted vv signals;
- every metric label has a finite documented set and a cardinality budget;
- an adversarial corpus ensures command, URL, error, model and tenant strings
  do not appear in recorded spans or metric points;
- public docs classify every attribute as stable, experimental or deprecated.

### First implementation slice

Implement closed enums for operation, outcome and error-code projection. Do
not expose an `Attributes ...attribute.KeyValue` escape hatch. If an application
needs custom attributes, it starts its own enclosing span and owns its own data
governance.

---

## O-04 — inbound context and vv command composition

**Decision.** Upstream transport instrumentation creates the server span and
extracts W3C context. vv creates a child command span at the first
transport-neutral service call. vv never parses trace headers itself.

### Top-level declarative DX

```go
// Router instrumentation has already put remote parentage in ctx.
result, err := tracedOrders.Create(ctx, createOrder)
```

### Happy use cases

1. An `otelhttp` server span is active. `Service.Create` emits one child
   `vv.service.create orders.order` span and preserves trace/span parentage.
2. A gRPC handler has upstream instrumentation. The same decorated service
   creates an identical vv child span, proving the service is transport-neutral.
3. A CLI starts a root span deliberately and invokes the service; vv works
   without an HTTP request or a router dependency.
4. A test passes a context with a known span context and asserts the command
   span uses it as parent.
5. A compatibility handler has no active span. The passed provider determines
   whether vv creates a root span; it never mutates external propagation state.

### Edge use cases

1. An untrusted caller sends malformed or oversized trace headers. Upstream
   instrumentation decides extraction; vv sees a normal context and cannot
   panic on carrier text.
2. A handler accidentally starts a manual vv-like span. The docs explain that
   only the decorator owns the service operation to prevent double nesting.
3. Context is cancelled before the command enters vv. The command span records
   cancellation outcome, ends exactly once and does not manufacture success.
4. A background goroutine receives `context.Background()` by mistake. It loses
   parentage visibly; no global fallback reconnects it to a random request.
5. A legacy B3 propagator is installed by the app. vv remains neutral because
   extraction and injection belong to the host's propagator configuration.

### Invariants and acceptance evidence

- only the caller/provider determines root versus remote-parent semantics;
- an in-memory trace fixture covers HTTP-like, gRPC-like, CLI and no-parent
  contexts without importing any transport package;
- cancellation, deadline and panic recovery tests show one ended span and the
  same command result as the undecorated service;
- documentation distinguishes propagator configuration from vv decoration.

### First implementation slice

Decorate the narrow `port.Service` seam first. Start/finish a span around one
public command call and record its operation, resource and bounded outcome.
Leave transport methods, request routes and HTTP status entirely to upstream
instrumentation.

---

## O-05 — service-command traces

**Decision.** The service decorator owns the single application-level span for
each public command. It measures the complete transport-neutral use case:
mapping, validation, policy, repository work and resulting error projection.
It must not pretend that a CRUD verb contains application semantics that live
above the service seam.

### Top-level declarative DX

```go
orders := vvotel.DecorateService(
    vvotel.WithResource(telemetry, "orders.order"),
    ordersService,
)

order, err := orders.Update(ctx, updateOrder)
```

### Happy use cases

1. `Get` starts `vv.service.get orders.order`, records `by_id` as the query
   shape and finishes as `ok` when the repository returns a visible model.
2. `List` records the command duration once even when it executes a page query
   and a count query below it; child spans explain the split only when needed.
3. `Create` includes mapping the input command and policy evaluation before the
   repository call, making a slow rule visible as application latency.
4. `Update` produces `not_found` when the service's existing public behaviour
   maps absence to an `errs` code; it does not call it a database error.
5. `BulkDelete` reports the bounded `bulk` shape, not the number or identities
   of selected victims.
6. An application wraps a custom service that implements `port.Service`; the
   decorator preserves its concrete errors and only projects a safe category.
7. A nested service call receives the same context. Its own declared operation
   produces a child span, accurately exposing deliberate orchestration.

### Edge use cases

1. A service calls its own decorated interface recursively. This is an
   application bug that creates nested spans, but every span is still valid and
   no recursion guard changes business behaviour.
2. A command contains an invalid 30-MB field. Telemetry records `invalid` and
   length-free shape only; no command serialization is attempted.
3. A caller omits context. Go signatures prevent it; a nil context passed by a
   faulty adapter must be rejected at the adapter boundary rather than repaired
   by the telemetry decorator.
4. A service returns a typed nil model and nil error contrary to its contract.
   The decorator preserves that output and reports `ok`; telemetry must not
   introduce a speculative `not_found` classification.
5. An interceptor recovers a panic above the decorator. The span sees whatever
   defer ordering guarantees; panic capture must be explicit and optional, not
   a second hidden recovery policy.
6. A command runs for hours under an admin import. The application sampler may
   choose a different policy; vv does not add time-based sampling or truncate
   an in-flight command span.
7. A highly concurrent `List` stream invokes the service per element. The
   guide tells callers to model one parent batch span; vv will not fuse unrelated
   command spans based on matching resource names.

### Outcome mapping

| Observed result | `vv.outcome` | Span status | Error event |
|---|---|---|---|
| nil error | `ok` | unset | none |
| invalid command/request | `invalid` | error | bounded code only |
| policy refusal | `refused` | error | optional policy event |
| absent model | `not_found` | error | bounded code only |
| uniqueness/version conflict | `conflict` | error | bounded code only |
| context cancelled | `cancelled` | error | cancellation class |
| deadline exceeded | `deadline` | error | timeout class |
| unexpected failure | `error` | error | classified cause/code when safe |

An HTTP 404, gRPC code, SQLSTATE and error message are deliberately not service
span attributes. Their own layers own their own semantics. A diagnostic backend
can correlate them by trace ID when the host decides to attach it.

### Invariants and acceptance evidence

- one outer service invocation creates no more than one service span;
- every port command maps to a documented span name and outcome matrix;
- recorder tests prove return value and exact error identity are unchanged;
- nil/no-op providers preserve functional test output byte-for-byte;
- a benchmark reports allocations and nanoseconds for decorated no-recording,
  sampled and unsampled paths separately;
- no command field or model field appears in a golden exported span corpus.

### First implementation slice

Support `Get`, `List`, `Create`, `Update`, `Replace`, `Delete` and `Count` with
one generic decorator. Leave optional custom service methods to an explicit
manual helper until a narrow extension seam is proven. Adding reflection to
discover methods is forbidden.

---

## O-06 — repository and database traces without duplicate SQL

**Decision.** The repository decorator owns logical persistence spans; the
database driver instrumentation owns database-client semantic-convention spans.
The two are intentionally nested and answer different questions.

### Top-level declarative DX

```go
repo := vvotel.DecorateRepo(
    vvotel.WithResource(telemetry, "orders.order"),
    postgresRepo,
)
```

### Happy use cases

1. `GetByID` produces `vv.repository.get orders.order`; upstream `otelsql` or
   driver instrumentation below it shows connection/statement timing according
   to that driver's own security configuration.
2. `GetAll` records `filtered` or `paged` shape from typed options, while the
   driver records the database system and operation convention it supports.
3. `Save` includes one logical upsert even if the adapter needs multiple SQL
   statements; the child driver spans reveal the implementation details.
4. An application uses a repository adapter without SQL. The logical span still
   works and accurately says nothing about a database system.
5. `Tx` produces a repository transaction span only when it invokes a unit of
   work; nested driver transaction spans remain owned by driver instrumentation.
6. An aggregate query reports an allow-listed aggregate kind (`count`, `sum`,
   `min`, `max`, `avg`) if that does not reveal product semantics.
7. A test uses a fake repository. It can assert the logical trace tree without
   importing a real SQL driver or changing the fake's interface.

### Edge use cases

1. Driver instrumentation is absent. The logical repository span remains
   useful; the doc explicitly says a database waterfall will be incomplete.
2. Driver instrumentation is present twice, through both the pool and the
   driver. vv must not try to deduplicate child SQL spans because it cannot
   reliably identify third-party spans; the integration guide prevents this
   configuration instead.
3. SQL includes an unredacted literal because a host configuration is unsafe.
   vv never sees or forwards SQL text, but the security checklist flags the
   driver configuration as application-owned risk.
4. A repository runs a read after context cancellation because the driver
   ignores it. vv records the actual returned result and cancellation state,
   but does not falsely claim the query was cancelled.
5. An N+1 preload creates 500 SQL child spans. vv's one repository span makes
   the shape obvious; exporter span limits are host policy, while tests must
   demonstrate no unbounded vv span multiplication.
6. A repository opens a transaction on a different datasource than its query.
   Existing vv scoping safeguards own the failure; telemetry records the
   resulting class but does not infer transactional correctness from parentage.
7. A stale replica returns an old row. The span says `ok` unless the repository
   contract detects staleness; observability cannot manufacture consistency.

### Attribute policy

The repository span may contain `vv.resource.name`, `vv.command.name`,
`vv.query.shape`, `vv.outcome` and bounded public error code. It must not
contain ID, filter, sort field, projection list, page cursor, row count,
database name, SQL text or model snapshot. Row-count metrics need an explicitly
designed bounded histogram later, not an unreviewed attribute.

### Invariants and acceptance evidence

- service and repository spans have a deterministic parent-child relationship;
- no import path to a concrete driver or SQL instrumentation appears in
  `vv/otel`;
- golden trace tests cover no driver span, one driver span and many driver spans;
- all repository method errors preserve identity and wrapping relationship;
- test data with hostile filter and ID strings proves no dynamic values escape.

### First implementation slice

Decorate `port.Repository` directly. Build a compile-time method-to-operation
map, rather than parsing method names at runtime. Start with `GetByID`, `Get`,
`GetAll`, `Save`, `Update`, `Delete`, `Count`, `Exists` and `Tx`; add bulk and
aggregate only once their current semantics are pinned by integration tests.

---

## O-07 — query-shape evidence, not query capture

**Decision.** vv reports the complexity class selected by its typed query
options, never caller-supplied predicates, JSON, URL grammar or generated SQL.
This makes telemetry safe enough for normal exporter pipelines while still
revealing unexpected expensive request shapes.

### Top-level declarative DX

```go
opts := crud.Options{/* caller-built and validated */}
rows, err := tracedRepo.GetAll(ctx, opts)
// trace: vv.query.shape=filtered (or paged/aggregate/bulk)
```

### Happy use cases

1. A simple primary-key lookup records `by_id`, allowing an operator to compare
   its latency distribution against filtered lists without learning the ID.
2. A paged list records `paged`; the offset/cursor value and page size stay out
   of the span.
3. A query compiler detects a full-text search and emits a bounded shape
   extension such as `search` only after a registry review.
4. An aggregate records `aggregate` plus an allow-listed operation kind, useful
   to distinguish a count endpoint from a potentially costly percentile query.
5. An admin bulk operation records `bulk`, making a long-running class visible
   even though its exact victim count is suppressed.
6. A request has no filters because policy supplies all narrowing. It remains
   `filtered` if the resulting operation is logically filtered; telemetry does
   not claim a public unrestricted query.

### Edge use cases

1. A filter value includes an access token. Since values are never inspected or
   formatted, it cannot reach telemetry through this feature.
2. A new option composes search, page, preload and aggregate. The classifier
   chooses documented precedence or a composite enum only after an explicit
   compatibility decision; it does not concatenate arbitrary feature names.
3. An adapter mutates options after the decorator examines them. The span is an
   input-shape observation, not a certified SQL proof; repositories should keep
   options immutable by contract where possible.
4. Query decoding fails before a repository call. The transport/service span
   owns `invalid`; no repository span exists and no half-formed filter is logged.
5. An application creates options manually and bypasses external decoders. The
   classifier handles the same typed values; safety never depends on the route.
6. A developer asks for selected field names to debug a projection. Fields can
   expose schema/product data and explode variants; defer until a fixed
   allow-list and threat model justify an attribute.

### Invariants and acceptance evidence

- one table-driven corpus maps each option combination to exactly one shape;
- fuzzed filters, cursors and search text never influence emitted attributes;
- query-shape additions have a versioned dashboard migration entry;
- an SLO query can group by shape without exceeding the label budget.

### First implementation slice

Place classification beside typed vv options, not in HTTP/query decoder code.
Return an internal closed enum. Treat unknown combinations as `other` on traces
and omit them from metrics until they are deliberately classified.

---

## O-08 — error recording that preserves the public contract

**Decision.** `errs` remains the source of public code/message semantics.
OpenTelemetry records a bounded projection of an error's classification and the
operation stage; it does not serialize arbitrary error strings or replace error
handling.

### Top-level declarative DX

```go
if err != nil {
    span.RecordOutcome(vvotel.OutcomeFromError(err))
    return value, err
}
```

### Happy use cases

1. A constraint violation mapped by `errs` becomes `vv.outcome=conflict` and a
   documented code. The service returns the identical error it returned before.
2. A malformed query becomes `invalid`; a support dashboard sees a rate change
   without collecting the invalid query text.
3. A policy refusal becomes `refused`; a span event identifies the configured
   policy name if the application opted into policy observability.
4. A context deadline becomes `deadline`, permitting a latency/timeout alert
   independent of driver-specific error wording.
5. An unknown driver failure becomes `error` with an optional bounded origin
   (`repository`, `mapper`, `policy`, `transport`) but no guessed SQLSTATE class.
6. An error wraps a sentinel several layers deep. One classifier walks known vv
   errors safely and records the outer public projection once.

### Edge use cases

1. `errors.Join` contains a cancellation and a database failure. The outcome
   follows an explicit precedence table; raw joined messages are forbidden.
2. A driver returns a huge error containing SQL. The span records only `error`
   and optional known code; its event has no exception message field by default.
3. An application-defined error implements a misleading `Code()` method. Only
   the documented `errs` extraction API is trusted, not duck typing.
4. A panic is recovered by the app. The telemetry integration must make panic
   events opt-in and scrubbed; recording stacktrace automatically is prohibited.
5. A cancelled parent context is returned alongside a useful domain conflict.
   The command contract chooses the error; telemetry mirrors that result rather
   than examining context and overwriting it.
6. A new public error code is added. Telemetry treats it as `error` until its
   metric-cardinality and dashboard meaning are accepted in the registry.

### Invariants and acceptance evidence

- classification never calls `Error()` on an unknown error;
- recorder assertions prove error objects and `errors.Is/As` behaviour survive;
- the classification precedence table is tested with joins/wraps/nil values;
- forbidden-string tests inject secrets in every error layer and find none in
  span attributes or events;
- new metric label values require an explicit compatibility review.

### First implementation slice

Implement an internal `Classify(error) Outcome` over the current `errs` public
surface and context cancellation. Emit status and bounded attributes only. Do
not call the general OpenTelemetry exception recorder until its message/stack
behaviour is configured to meet the privacy model.

---

## O-09 — policy evidence without authorization leakage

**Decision.** Policy remains the enforcement seam. Telemetry may add an event
to the owning service span after a policy decision; it may never export a
principal, a row, a generated predicate, a denied field or an internal reason
that tells an attacker how to bypass a rule.

### Top-level declarative DX

```go
orders := vvotel.DecorateService(vvotel.WithPolicyEvents(
    telemetry,
    vvotel.PolicyNames("orders.owner", "orders.approver"),
), ordersService)
```

### Happy use cases

1. A declared `orders.owner` policy permits an update. The command span has a
   `vv.policy.checked` event with `result=allowed` and policy name.
2. The same policy refuses a cross-tenant update. The span records `refused`;
   the caller continues to receive the existing generic public refusal.
3. An operation has no policy configured. It records `not_applicable` only
   when the application asked for coverage visibility, avoiding noisy events.
4. A policy narrows a list query. The service span records that policy checked;
   it does not emit the resulting SQL scope, relation path or owner key.
5. A support operator compares allow/deny rates by policy name, where policy
   names were supplied at configuration time from a small known enum.
6. A policy implementation returns an unexpected internal error. The event
   records `result=error` and the command uses the normal bounded error class.

### Edge use cases

1. Policy names embed tenant, role or field input. `PolicyNames` refuses names
   outside the registry's character and count limits.
2. A policy returns an explanation with a principal email or a predicate. The
   satellite has no field to record it; application logs need a distinct,
   access-controlled audit design.
3. A policy runs once per row in a bulk operation. The command emits one
   summary event (`checked`, `denied` or `error`), not one event per victim.
4. A policy has a transient dependency failure. Telemetry does not retry it,
   label it an authorization denial, or disguise availability as security.
5. Policy evaluation happens in a repository middleware rather than service.
   The integration must attach to the active service span; it must not create a
   separate root or rely on global mutable request state.
6. An unauthorized user obtains a trace ID from a response header. The trace
   must still be safe if later viewed, hence zero identities and reasons.
7. An application wants to audit all successful reads. This card records only
   operational evidence; durable audit is delegated to the audit roadmap.

### Event schema

| Field | Value | Privacy class |
|---|---|---|
| event name | `vv.policy.checked` / `vv.policy.denied` | stable |
| `vv.policy.name` | configured allow-list member | reviewed trace-only |
| `vv.policy.result` | allowed/denied/not_applicable/error | stable |
| `vv.command.name` | port command enum | inherited stable |
| principal/tenant/model/filter | never | prohibited |

### Invariants and acceptance evidence

- removing telemetry leaves every allow/deny decision unchanged;
- all events remain on the service span, bounded to a documented maximum per
  operation;
- a malicious policy explanation corpus produces no emitted sensitive value;
- all declared names have a registry test and dashboard ownership;
- an adversarial control test verifies a denied response remains indistinguish-
  able to the client with and without telemetry enabled.

### First implementation slice

Introduce an optional observer adapter at the existing transport-neutral policy
seam only after that seam exposes decision outcome without an internal reason.
If the seam cannot do so safely, record only the final `refused` command
outcome; do not widen policy interfaces merely for observability.

---

## O-10 — transactional outbox, publication and causal links

**Decision.** Future event/outbox work uses spans to distinguish *recorded in
the transaction*, *claimed for publication*, *accepted by broker* and
*observed by a consumer*. A publisher span cannot truthfully claim delivery.
Use span links where fan-out/fan-in makes parent-child misleading.

### Top-level declarative DX

```go
// Future event module; illustrative hand-off metadata only.
envelope := events.New("orders.order.created").WithTrace(ctx)
err := outbox.Enqueue(ctx, envelope)
```

### Happy use cases

1. `Create` commits a model and outbox row atomically. Its command span has an
   `vv.outbox.enqueued` event after the database transaction confirms commit.
2. A publisher worker extracts the recorded context, starts
   `vv.outbox.publish`, and uses the command span as a causal parent or link
   according to the explicit delivery model.
3. A broker accepts the message. The publisher span ends `ok` and records
   `accepted`; no claim about consumer execution is emitted.
4. A consumer extracts context and starts a processing span. One domain event
   fan-out creates sibling consumer traces or linked spans as documented.
5. A batch publisher claims 100 rows. It creates a batch span plus one linked
   publication span per message only if the host sampling budget permits it.
6. A message is retried. Each attempt uses a new span with bounded attempt
   number, while all attempts link to the original enqueue context.
7. The future audit module records the enqueue and consumed action with trace
   correlation only, not payload content.

### Edge use cases

1. Transaction rolls back after enqueue. No durable outbox event exists; a
   transient in-memory span event must be marked abandoned or omitted.
2. A publisher crashes after broker acceptance but before marking sent. Retry
   may duplicate delivery; trace topology must reveal retry/duplicate state,
   not paper it over as exactly once.
3. Trace context in an outbox row is corrupt, stale or oversized. Extract into
   a safe context, start a new trace if required, and record a bounded carrier
   validity class; never store arbitrary baggage.
4. Sampling drops the originating command span. The carrier can still preserve
   trace flags/context, but workers must work correctly with no recorded parent.
5. One command emits 10,000 events. Use a capped event/span budget and batch
   summary metric; do not allocate an unbounded local trace tree.
6. A message is redriven weeks later. Parent-child duration would be absurd;
   create a fresh processing root with a link to original context if available.
7. A broker replaces headers or rejects invalid values. Header injection and
   carrier length checks belong to explicit adapter code, never generic maps.
8. A `traceparent` could be attacker supplied through a webhook. Treat it as
   untrusted propagation input under the host propagator policy.

### Required carrier contract

The future event envelope must specify, before implementation:

- carrier representation and maximum byte count;
- allowed propagator keys and baggage policy (default: trace context only);
- transaction point where the carrier is frozen;
- retry attempt and first-enqueue timestamps as bounded diagnostic fields;
- how dead letter, manual replay and batch fan-out choose parent versus link;
- what survives a payload redaction or archive move;
- how carrier parsing failure is observable without recording its contents.

### Invariants and acceptance evidence

- outbox instrumentation never makes broker delivery part of transaction success;
- no payload, event ID, aggregate ID or header value appears in telemetry;
- retry and replay fixtures prove one causal topology for each documented case;
- a crash window suite covers before publish, after accept, after acknowledgement
  and duplicate consumer delivery;
- a test ensures propagation works with trace-only carrier and empty baggage.

### First implementation slice

Do not implement this until an outbox contract exists. First write an event
envelope propagation specification and test it against an in-memory carrier.
Then add enqueue/publish decorators owned by the event satellite, with only
small bridge interfaces in `vv/otel` if [[D-051]] still permits that split.

---

## O-11 — workers, queues and scheduled jobs

**Decision.** A worker hand-off begins a new execution span at claim time. It
records queue/job category, attempt and outcome from bounded configuration;
it never publishes raw job arguments, queue name generated from tenant data or
internal worker lease tokens.

### Top-level declarative DX

```go
worker := jobsotel.Decorate(workerRuntime, jobsotel.Config{
    Name: "billing.reconcile",
    Telemetry: telemetry,
})
```

This is a future satellite example, not an API for `vv/otel`: a job runtime is
an independent external dependency decision.

### Happy use cases

1. A scheduled `billing.reconcile` job starts a root execution span when there
   is no causal request, with host resource attributes supplied normally.
2. An outbox-triggered worker extracts the originating trace and starts a
   processing span linked to its enqueue operation.
3. A retry records attempt 2 and a retriable outcome; the job runtime's own
   backoff remains the single source of schedule truth.
4. A worker batch processes 50 independent messages. The worker has a batch
   span and child or linked per-message spans within an explicit span budget.
5. A job reaches a dead letter queue. Its last attempt records a bounded
   `dead_lettered` lifecycle event and the durable job system owns persistence.
6. A manual operator replay is tagged as `manual_replay` by fixed enum, letting
   an operator distinguish recovery work from normal customer traffic.

### Edge use cases

1. A lease expires while work continues. The telemetry outcome follows actual
   handler result; it must not claim lease ownership or duplicate prevention.
2. The job runtime delivers at least once. The trace documents each execution;
   idempotency belongs to the job/domain contract, not span deduplication.
3. An attempt number is unbounded for a poison message. Trace attribute keeps a
   small integer, metrics bucket attempts into a fixed histogram and logs can
   hold broader diagnostics under host controls.
4. One worker name is dynamically built from a customer name. Configuration
   validation rejects it; use an operation family plus safe tenant mode instead.
5. A job is delayed for days. Queue latency is a metric/histogram measured by
   runtime timestamps, not a giant sleeping span between enqueue and execution.
6. Cancellation asks a worker to stop but handler ignores it. vv records result
   without asserting the runtime terminated work it could not control.
7. A worker spawns detached goroutines. Each needs explicit context transfer;
   silently copying task context into arbitrary goroutines is forbidden.

### Invariants and acceptance evidence

- no job runtime dependency enters `vv/otel`;
- execution spans are started/ended exactly once despite retries and panics;
- queue delay, handler duration and retry count are separate signals;
- sampling/lifetime tests prove no long-lived span is used as a scheduler;
- replay/retry/dead-letter traces remain privacy-safe with hostile arguments.

### First implementation slice

Publish generic guidance and a carrier fixture, not an adapter. Build a runtime
adapter only after vv adopts one job contract or a consumer independently asks
for a narrowly scoped satellite.

---

## O-12 — external calls and webhooks

**Decision.** Upstream client/server instrumentation owns protocol spans. vv
can define a domain-level hand-off event only when it knows the durable semantic
boundary, such as “webhook delivery scheduled”; it must not wrap every HTTP
client just to add another span.

### Top-level declarative DX

```go
// HTTP client instrumentation belongs to the host.
// Future webhook module emits vv.webhook.scheduled and domain lifecycle data.
```

### Happy use cases

1. An application uses upstream HTTP instrumentation for an outbound payment
   request; vv service span is the parent and standard HTTP client span is the
   child.
2. A future webhook module writes a durable delivery record, emits a
   `vv.webhook.scheduled` event and later runs a delivery attempt span.
3. A remote timeout is diagnosed through standard network attributes while vv
   records its command-level `deadline`/`error` outcome.
4. A receiver's upstream server instrumentation extracts trace context. Its vv
   service span correctly continues the trace without a webhook-specific root.
5. Delivery retry uses a new attempt span and bounded attempt number; the
   original scheduling span is linked when parentage would misrepresent time.

### Edge use cases

1. A third-party endpoint sends back a malicious `traceparent`. The host's
   propagation/security policy owns extraction; vv cannot elevate it to trust.
2. A URL has signed query parameters. vv never records route, URL, host or
   query; upstream instrumentation must be configured under the same privacy
   review.
3. HTTP status 429 causes a host retry. vv must not invent another retry layer
   or double-count a remote call metric.
4. A remote call returns a user-containing error body. It may be handled by the
   application but cannot be recorded by vv telemetry.
5. One business command fans out to ten webhook endpoints. A span budget keeps
   the trace bounded; a batch metric gives aggregate operations visibility.
6. A delivery succeeds remotely but the local acknowledgement write fails. The
   topology must show separate remote acceptance and local state outcomes.
7. The host intentionally does not propagate trace headers outside its trust
   boundary. vv respects this choice; delivery tracing becomes separate roots.

### Invariants and acceptance evidence

- `vv/otel` contains no HTTP client/server middleware imports;
- documented example has exactly one standard HTTP client span per call;
- a signed URL/error body corpus verifies vv signal purity;
- retries show one span per actual attempt and no false success semantics;
- cross-trust-boundary propagation defaults to host policy and is reviewable.

### First implementation slice

Write the integration guide using upstream `otelhttp`/gRPC examples, with no
vv code. Defer a webhook bridge until the outbox/webhook contract defines
durability, retry and security semantics.

---

## O-13 — object storage instrumentation contract

**Decision.** Storage is a future abstraction with filesystem and S3-compatible
implementations. Its own satellite/adapters own backend operations; `vv/otel`
defines only the common safe vocabulary and trace relationship. Backend bucket,
endpoint, object key, version ID and pre-signed URL are never common attributes.

### Top-level declarative DX

```go
objects := storageotel.Decorate(store, storageotel.Config{
    Telemetry: telemetry,
    StoreName: "documents",
})
```

### Happy use cases

1. A document upload creates `vv.storage.put` below the service span. It
   records store family and outcome while the S3 SDK's own instrumentation, if
   chosen by the host, describes remote network timing.
2. A filesystem-backed test store creates the same logical storage span, proving
   that observability does not expose the implementation as a public contract.
3. A multipart S3 upload records one logical operation and bounded byte-size
   histogram, rather than a trace span per arbitrary-sized part by default.
4. A download streams successfully. The span ends when the store has created
   the stream contract it owns; consumer read duration is separately defined,
   not silently assumed.
5. An object deletion maps an absence/conditional failure to storage's future
   public error class and then to a bounded vv outcome.
6. A copy promotes a staged object. The span reports `copy`/`promote` operation
   family without source/destination key disclosure.

### Edge use cases

1. An object key is an e-mail, UUID or slash-delimited tenant ID. It never
   appears in span, event, metric or log correlation emitted by vv.
2. S3 multipart retry causes several SDK calls. The logical store span reports
   final operation result; detailed HTTP spans remain the host's decision.
3. A filesystem path escapes its configured root. Store policy refuses it;
   telemetry records a bounded invalid/refused outcome but not the path.
4. Pre-signed URL generation exposes a sensitive capability. Its span carries
   only operation and outcome; expiry, query, object identity and URL remain out.
5. Object-store eventual consistency yields a post-write read miss. Telemetry
   records observed error; it cannot claim an atomic storage semantic.
6. An S3 endpoint is a tenant-specific URL. Endpoint must not become a metric
   label or trace attribute; connection instrumentation needs separate review.
7. A stream is never closed by the caller. The storage implementation owns its
   resource safety; tracing must not hold spans indefinitely hoping to detect it.

### Common storage attributes

| Attribute | Values | Signal policy |
|---|---|---|
| `vv.storage.operation` | `put`, `get`, `head`, `delete`, `copy`, `sign` | trace + metric |
| `vv.storage.kind` | `filesystem`, `s3` | trace; metric only if bounded deployment choice |
| `vv.storage.outcome` | bounded storage result | trace + metric |
| `vv.storage.size.bucket` | fixed log2/semantic bucket | metric, optional trace |
| store name | configured allow-list only | trace optional; never backend bucket |

### Invariants and acceptance evidence

- same logical operation has identical vv vocabulary under fs and S3 adapters;
- all key/path/url/version values are absent from a hostile golden corpus;
- long-stream contract has explicit span start/end semantics in the storage
  roadmap before code exists;
- S3 SDK/MinIO/AWS/R2 integrations do not enter the core telemetry module;
- multipart and stream tests cap local span/event count.

### First implementation slice

Coordinate with the storage roadmap on a narrow store interface first. Add only
an adapter decorator when `Put`, `Get`, `Head`, `Delete` and stream ownership
semantics are stable. Avoid choosing an S3 SDK in `vv/otel`.

---

## O-14 — event store telemetry for PostgreSQL-only event sourcing

**Decision.** The future event store is PostgreSQL-only. Event-store spans
describe append/load/snapshot/projection work at a logical level, while database
instrumentation owns SQL spans. Aggregate IDs, event payloads, stream names
containing customer values and expected versions are never exported as raw
attributes.

### Top-level declarative DX

```go
events := eventotel.Decorate(eventStore, eventotel.Config{
    Telemetry: telemetry,
    StreamFamily: "orders.order",
})
```

### Happy use cases

1. Append to `orders.order` emits `vv.eventstore.append` with bounded outcome;
   nested PostgreSQL client spans show transaction latency if host driver
   instrumentation is enabled.
2. Optimistic concurrency rejects an old expected version. The logical span
   records `conflict`, allowing operators to distinguish contention from an SQL
   outage without exposing the version value.
3. Load reads a stream and applies events in memory. One logical span measures
   operation, and a fixed histogram records event-count/age buckets later.
4. Snapshot read or write records `snapshot.read` / `snapshot.write` as bounded
   operation extension after the snapshot contract is accepted.
5. A projector consumes a batch. It links each source event context within a
   cap, reports checkpoint outcome and does not claim exactly-once processing.
6. An event upcaster converts an old revision. It emits a bounded schema
   transition metric (`v1_to_v2`) only for declared revisions, not payload data.
7. A release changes event handler code but not event wire schema. Resource
   `service.version` remains host-owned and lets traces be compared by rollout.

### Edge use cases

1. A stream has millions of events. Trace reporting uses load range/count
   buckets; one span/event is prohibited.
2. An event type string is dynamically generated. Event names must be declared
   allow-list members or mapped to a stable family; they cannot label metrics.
3. Upcasting fails on a historic malformed event. The span records a bounded
   `upcast_failed` outcome plus declared source/target revision family; payload
   and primary key stay out.
4. A projection replay runs for days. It is a sequence of checkpoint/batch
   spans with links, never one unbounded trace across the whole replay.
5. A PostgreSQL serialization failure is retried. Every attempt is visible and
   causally connected, but retry policy comes from event-store implementation.
6. A snapshot is corrupt. The event store chooses fallback/failure semantics;
   telemetry records the observed path without pretending snapshots are truth.
7. An aggregate ID must be found for a support case. This requires a dedicated,
   access-controlled audit/support workflow, not a high-cardinality trace tag.

### Required event-store telemetry schema

| Dimension | Allowed expression |
|---|---|
| operation | append/load/snapshot/project/upcast/checkpoint |
| stream family | configured bounded logical resource |
| result | ok/conflict/error/cancelled/deadline/retry_exhausted |
| batch size | fixed histogram bucket only |
| event revision | declared bounded source/target enum, trace-only initially |
| projection | configured projection family, never consumer instance ID |
| database | upstream resource/driver conventions, not vv custom labels |

### Invariants and acceptance evidence

- all database work remains child driver spans, not duplicated raw SQL;
- no event payload, aggregate/event ID, stream sequence or checkpoint value
  occurs in a trace export corpus;
- optimistic conflict, serialization retry, snapshot fallback, upcast failure
  and replay have explicit topology fixtures;
- metric cardinality remains bounded even with unbounded aggregate population;
- version/upcast observability is tested across mixed historic revisions.

### First implementation slice

Defer adapter code until the PostgreSQL event-store contract and release model
exist. Add static vocabulary and trace fixtures alongside the event-source
roadmap's first append/load implementation, not before.

---

## O-15 — multitenancy without a tenant telemetry leak

**Decision.** Tenant selection and isolation are security/data-routing concerns.
Telemetry may report only a bounded deployment topology (`row`, `schema`,
`database`) and resolution result. It never reports tenant identity, selected
database/schema or connection string.

### Top-level declarative DX

```go
tenantStore := tenantotel.Decorate(resolver, tenantotel.Config{
    Telemetry: telemetry,
    Mode: tenantotel.DatabasePerTenant,
})
```

### Happy use cases

1. A one-database application emits `vv.tenant.mode=row` resource/operation
   context, so an operator can compare topology-wide behaviour without a tenant
   label.
2. A database-per-tenant resolver selects a pooled datasource and records
   `vv.tenant.resolve` as `ok`; database identity remains solely in secure host
   configuration.
3. An unknown tenant is refused by resolver policy. The span reports bounded
   `not_found` or `refused` according to public contract, not raw host name.
4. A migration controller runs a tenant database migration. It uses a separate
   deployment operation family and reports aggregate progress metrics, not one
   metric series per tenant.
5. An audit entry gets trace correlation after tenant resolution, allowing an
   authorized audit viewer to join its own tenant-scoped data to a trace ID.
6. A tenant-aware event append uses an allowed topology value and service-level
   resource name, with no per-tenant stream label.

### Edge use cases

1. A tenant ID is a UUID. Hashing it would still create high cardinality and
   durable tracking; it is prohibited.
2. Connection acquisition fails for one database. Telemetry can report a
   topology-level error but cannot make a fleet dashboard identify the customer
   through datasource/db name attributes.
3. A buggy resolver returns database A for tenant B. Existing isolation tests
   detect the security fault; telemetry must not be treated as proof of routing.
4. A super-admin action legitimately crosses tenants. It records an explicit
   bounded `cross_tenant_admin` mode, but individual tenants still remain out.
5. A request arrives with an unrecognized tenant header. No header value may
   appear in sampled error traces.
6. A database-per-tenant fleet has 50,000 databases. Resource detection and
   metric labels must not materialize a timeseries/resource per database.
7. A tenant is deleted, reused or renamed. Telemetry has no stored identifier
   that could accidentally keep its historical identity alive.

### Invariants and acceptance evidence

- tenant IDs, schemas, datasource names and connection strings are rejected by
  registry tests in every signal type;
- one-db and database-per-tenant pass one common trace topology suite;
- malicious resolver/header test vectors are absent from exported payloads;
- dashboards group only by tenant mode, resource, outcome and deployment;
- tenant resolver failure preserves the application's public error result.

### First implementation slice

Wait for the tenancy contract. Define a `TenantMode` enum in the tenancy
satellite and a telemetry mapping there; keep `vv/otel` independent of resolver
interfaces, datasource pools and migration tools.

---

## O-16 — audit-log correlation, not audit replacement

**Decision.** Audit is a durable, authorization-controlled record of a data
action. Trace data is an ephemeral diagnostic signal. The two can share a
correlation token when the audit system deliberately stores it, but neither
becomes a substitute for the other.

### Top-level declarative DX

```go
audited := audit.WithTraceCorrelation(auditWriter, telemetry)
service := audit.DecorateService(audited, tracedService)
```

### Happy use cases

1. A successful `Update` writes an audit revision with a trace ID and span ID
   extracted from current context, then operators can pivot from an authorized
   audit record to short-retention trace data.
2. A failed policy decision creates no normal mutation audit record, but may
   create an explicitly designed security audit event; telemetry independently
   records the bounded refusal outcome.
3. A batch write emits one audit transaction/revision and one command trace;
   the audit design chooses whether subjects are individual rows, while trace
   avoids them entirely.
4. An asynchronous outbox publication stores its enqueue trace context in the
   durable envelope; later audit actions can preserve causal correlation.
5. A regulatory retention policy keeps audit for years while trace retention is
   days. Broken trace links are accepted as normal and never invalidate audit.
6. An audit sink outage causes an operation to fail or buffer according to the
   audited command's explicit contract; telemetry records the result but cannot
   silently make audit best effort.

### Edge use cases

1. A trace backend is available to broader staff than audit data. Trace must
   not contain audit snapshots, actor identity or before/after values.
2. A trace ID might be considered personal data in a correlation context. The
   audit roadmap must define retention, access and rotation; telemetry does not
   give it special durability.
3. Sampling drops the active span. Audit gets an empty/no-recorded correlation
   representation and remains a complete auditable record.
4. An audit transaction happens after the service span ends due to a separate
   async writer. It starts a new trace/link as documented; no invalid child span.
5. An auditor needs original object storage key or aggregate ID. The audit
   record may hold it subject to access controls; the linked trace never does.
6. A developer attempts to dump revision diffs as span events. The attribute
   registry makes this impossible through normal APIs and review rejects it.
7. Audit itself is multitenant. Its access/routing contract owns tenant scope;
   telemetry may carry only mode/outcome, never audit tenant key.

### Invariants and acceptance evidence

- audit records remain complete when tracing is disabled or unsampled;
- trace exports remain free of audit subject, actor and value snapshots;
- correlation is optional, length-validated and has no effect on audit write;
- retention/access documentation distinguishes audit and telemetry systems;
- test fixture covers sampled, unsampled, no-context and corrupted-context cases.

### First implementation slice

Do not create an audit bridge yet. The audit roadmap must first define atomicity
and revision identity. Once it does, add a tiny trace-context extractor owned by
the audit satellite and test it against no-op/sampled contexts.

---

## O-17 — localized errors and diagnostics

**Decision.** `errs.MessageSource` remains the existing, right-sized error
catalogue. The future full i18n satellite owns locale/plural formatting.
Telemetry records the stable error code and outcome, never the localized string,
requested locale tag or message interpolation values.

### Top-level declarative DX

```go
// Locale has already been selected at the boundary.
result, err := tracedService.Create(ctx, command)
// trace: vv.error.code=invalid, vv.outcome=invalid
// never: translated text, locale or command value
```

### Happy use cases

1. The same invalid command returns English for one caller and Kazakh for
   another; both traces have the same stable error-code metric value.
2. A future pluralized message is rendered after service failure. The command
   span measures operation outcome, not locale-catalogue lookup timing by
   default.
3. An operator detects a rise in `invalid` errors across all locales without
   treating language as a high-cardinality performance dimension.
4. A support ticket supplies a trace ID and a localized user screenshot. The
   stable public code bridges them without trace collection of message content.
5. Catalogue fallback happens from a regional tag to base language. It remains
   an application/i18n concern; telemetry only sees final success/error class.

### Edge use cases

1. A translation interpolation includes a customer name or a rejected e-mail.
   No localized message is recorded, so it cannot leak through telemetry.
2. A malformed locale header has attacker-controlled length. It never becomes
   a span attribute, metric label or event value.
3. A catalogue is unavailable and error rendering falls back to a safe code.
   The i18n satellite may emit its own bounded diagnostic; vv command outcome
   must stay tied to the business error, not to presentation fallback.
4. A developer wants locale labels to see a regional outage. Such a decision
   needs a bounded allow-list and privacy/cardinality review in i18n, not an
   ad-hoc service span tag.
5. A translation key itself encodes detailed domain state. Error code mapping
   must not reverse it into an unbounded telemetry dimension.
6. Error message source is called after the span ends in transport rendering.
   No late mutation of ended spans is attempted.

### Invariants and acceptance evidence

- locale and localized message values are absent from a multilingual secret
  corpus in traces, metrics and events;
- public error code has one telemetry projection independent of language;
- missing-catalogue tests show no command semantic change due to observability;
- i18n integration guide explicitly links [[D-048]] and keeps x/text outside
  the root module.

### First implementation slice

Nothing in the OTel satellite beyond its existing error-code projection. Add
cross-document contract tests only once an i18n satellite exists.

---

## O-18 — metrics that answer alerts, not every question

**Decision.** Metrics are low-cardinality aggregates. The initial instrument
set is intentionally small: command duration/histogram, command total,
repository duration where needed, and selected bounded lifecycle counters.
Everything else starts as trace evidence or an application-owned metric.

### Top-level declarative DX

```go
telemetry, err := vvotel.New(vvotel.Config{
    TracerProvider: provider,
    MeterProvider: meterProvider,
    ResourceName: "orders.order",
    Metrics: vvotel.DefaultMetrics(),
})
```

### Initial metric catalogue

| Instrument | Type | Required dimensions | Explicitly absent |
|---|---|---|---|
| `vv.command.duration` | histogram, seconds | resource, command, outcome | tenant, user, ID, route, filter |
| `vv.command.total` | counter | resource, command, outcome | locale, error text, policy reason |
| `vv.repository.duration` | histogram, seconds | resource, operation, outcome | database/table/query/row count |
| `vv.policy.total` | counter, opt-in | resource, policy name, result | subject/role/tenant |
| `vv.outbox.total` | future counter | event family, lifecycle result | event ID/payload/broker offset |
| `vv.storage.duration` | future histogram | operation, kind, outcome | bucket/key/endpoint |

### Happy use cases

1. An SLO uses `vv.command.duration` for `orders.order/create` successful
   operations, with result classes separated from latency rather than string
   matching a log message.
2. An error-rate alert groups by resource, command and outcome, producing a
   finite predictable series count across every instance.
3. A dashboard compares service command latency to repository latency and then
   opens a sampled trace for representative slow requests.
4. Policy denied rate is enabled for a small declared set of policies, helping
   detect a broken permission rollout without collecting identities.
5. A storage S3/filsystem deployment sees duration by kind at small cardinality;
   it does not create one metric stream per bucket or object.
6. An event projection tracks checkpoint/batch success at projection-family
   level; one noisy aggregate cannot create a new label set.

### Edge use cases

1. A developer puts an ID in a metric attribute using a generic API. The
   satellite exposes no such API and a registry test rejects it.
2. Resource names are customer-created. Metrics cannot use them; map to a
   configured resource family or disable that metric dimension.
3. A command returns 30 distinct error codes after a release. Existing labels
   must remain bounded; unknown/new codes collapse to `other_error` pending
   explicit registry acceptance.
4. Histogram buckets change. This is a metric schema migration with parallel
   recording/query update, not an invisible tuning edit.
5. The meter provider is no-op or its reader is overloaded. Commands continue;
   telemetry does not allocate per-request fallback counters.
6. A high-volume bulk operation's size is unbounded. Record logarithmic buckets
   or omit it; never label a counter with exact size.
7. A sampler drops all traces. Metrics still work under meter provider policy;
   developers must not use trace sampling to infer metric completeness.

### Cardinality budget

For each metric, reviewers must calculate an upper bound:

```
resource names × commands × outcomes × optional policy names × deployment mode
```

The default deployment must remain under a documented conservative budget before
considering host-level dimensions such as `service.instance.id`, which are
resource attributes rather than metric labels. Any proposal that cannot state a
finite bound is trace-only or refused.

### Invariants and acceptance evidence

- an instrument inventory test checks names, units, descriptions and label enum;
- a cardinality stress test supplies thousands of IDs/tenants/filters and sees
  no new metric attribute values;
- histogram bucket release changes have dashboard/alert migration evidence;
- no metric reports data only known after a span exporter/sampler decision;
- observability overhead benchmark covers all enabled initial instruments.

### First implementation slice

Create command duration and total only after trace operation/outcome mapping is
stable. Use OpenTelemetry metric API through caller-supplied meter provider;
avoid an independent Prometheus dependency or custom registry.

---

## O-19 — logs as correlation, not a third implementation

**Decision.** vv already treats logging as caller-owned (`port/log.go`). The
OTel satellite does not select a logging API or log exporter. It provides a
small optional helper to attach trace/span IDs to an application-owned `slog`
record, and documents where that helper belongs.

### Top-level declarative DX

```go
logger := vvotel.WithLogCorrelation(port.Logger(ctx), ctx)
logger.Info("outbox publication failed", "outcome", "retryable")
```

### Happy use cases

1. An application uses `slog`; its structured error log carries `trace_id` and
   `span_id`, allowing a click-through from application logs to an authorized
   trace backend.
2. A no-op/non-recording span context still has valid identifiers where host
   propagation created them; helper behaviour is documented and deterministic.
3. A worker logs a retry with its current attempt span correlation, separate
   from the original enqueue link.
4. An audit failure log gets correlation values, while audit contents remain in
   the audit store and not log fields by vv's choice.
5. A consumer with another logging library recreates the two standard IDs from
   span context itself; no vv logging adapter forces a dependency.

### Edge use cases

1. No valid span context exists. Helper returns a logger with no correlation
   fields; it does not invent a trace ID.
2. A logger already has `trace_id` from another system. The helper must define
   collision behaviour (normally retain existing app field or use namespaced
   keys) and test it.
3. A log record contains a raw error or command body. Correlation helper does
   not sanitize arbitrary caller fields; docs make the responsibility explicit.
4. A trace is sampled out but context is propagated. Linking to a missing trace
   is acceptable and must not cause a second logging decision.
5. A trace ID becomes a sensitive correlation token in a specific environment.
   The host may disable correlation helper; no command feature depends on it.
6. A request starts multiple spans concurrently. Each log uses the context
   supplied at that log call; there is no global “current span”.

### Invariants and acceptance evidence

- `vv/otel` imports no logging SDK beyond existing permitted stdlib seam;
- helper never changes log level, message, error object or arbitrary attributes;
- a concurrency fixture proves IDs remain attached to the right context;
- logs require no collector log-signal support to be useful;
- documentation tells operators that trace and log retention/access differ.

### First implementation slice

Provide only a `slog`-shaped helper if the root's current logging contract makes
it dependency-free. Do not implement OpenTelemetry log emission until the Go
log signal and a concrete user requirement are stable.

---

## O-20 — sampling, baggage and trust boundaries

**Decision.** The host owns sampler and propagator configuration. vv uses the
current context, never alters sampling flags, and treats baggage as untrusted
and unavailable by default. vv's own carrier integration propagates trace
context only unless a later security decision says otherwise.

### Top-level declarative DX

```go
// Host application decides sampler and propagator once.
otel.SetTextMapPropagator(propagation.TraceContext{})
// vv receives context normally; it does not inspect baggage.
```

### Happy use cases

1. Production uses parent-based ratio sampling. vv spans join selected traces
   without knowing sample probability.
2. Staging uses always-on sampling. The same decorators yield richer traces but
   no change in command results or emitted attribute vocabulary.
3. A public ingress removes baggage at a trust boundary while retaining valid
   trace parentage; vv command spans continue normally.
4. An internal outbox envelope persists trace-context headers only. A worker
   later links/parents its attempt according to documented rules.
5. A consumer elects tail sampling in its backend. vv does not rely on start
   sampling for metric semantics or audit completeness.

### Edge use cases

1. Baggage has a `tenant_id` key. vv never reads or copies it into attributes,
   metrics, logs, storage metadata or event payloads.
2. A malicious caller sends thousands of baggage entries. Upstream propagator
   limits/rejects them; vv cannot accidentally loop/allocate over baggage.
3. A carrier crosses a public webhook boundary. The integration guide requires
   explicit host policy on injection; default examples do not propagate secrets.
4. A remote parent requests sampled recording. Host sampler policy wins; vv
   cannot force a recording decision through a decorator option.
5. An operator needs 100% capture for a tenant incident. That cannot be solved
   by tenant attribute sampling; use an authorized host-level temporary policy
   with strict data governance, outside vv defaults.
6. A context is copied into an unbounded background queue. Context propagation
   expiry/retention must be set by the queue contract; vv doesn't preserve it
   forever just to keep trace linkage.

### Invariants and acceptance evidence

- no production vv code reads baggage APIs;
- no configuration setter changes process-global propagator/sampler state;
- carrier fixture verifies trace-only keys and size bounds;
- sampled, unsampled, remote-parent and no-parent tests retain equal command
  output and error identity;
- trust-boundary guide identifies ingress, webhook and durable queue decisions.

### First implementation slice

Document this before adding any asynchronous propagation. Add a test that fails
if a vv package imports baggage or global setter APIs; reviewers need an
executable guard against the most tempting convenience shortcut.

---

## O-21 — disabled-path overhead and failure containment

**Decision.** Telemetry is optional instrumentation, so the default no-op and
unsampled paths are product performance paths. Every new decorator needs an
allocation and latency budget, and every telemetry failure must be contained by
the OpenTelemetry SDK boundary rather than wrapped around business execution.

### Top-level declarative DX

```go
// One explicit dependency; no feature flags scattered through business code.
repo := vvotel.DecorateRepo(telemetry, baseRepo)
```

### Happy use cases

1. A service runs with the OpenTelemetry no-op provider. Decorated `Get` returns
   the same values/errors and stays within documented allocation budget.
2. A parent trace is unsampled. vv reads the non-recording context, creates no
   expensive dynamic attribute collection and immediately invokes the base seam.
3. A sampled request produces spans and metrics, while the business method is
   not retried, deferred, serialized or copied by the decorator.
4. A batch load operates under a span/event cap and increments a bounded metric
   rather than retaining all victims in memory for later export.
5. An exporter backend is unavailable. The SDK applies its processor policy;
   vv has no network path or error return to the user operation.
6. An application turns off `RecordPolicy` for a high-volume endpoint; command
   traces continue with core outcome attributes and no code branch in policy.

### Edge use cases

1. Attribute validation fails due to an application configuration regression.
   This must fail at construction or collapse to a safe fixed value, never fail
   a user write midway through a request.
2. A custom provider panics from `Start` or `End`. OpenTelemetry provider
   conformance is assumed; vv must not broadly recover arbitrary provider
   panics because that would hide host bugs and alter panic semantics.
3. A collector's backpressure increases SDK work. A performance test observes
   it, while operational mitigation remains batching/sampling/exporter tuning
   owned by the host.
4. A massive error chain is returned. Classification uses bounded known-error
   inspection and does not recursively stringify/unroll without a limit.
5. A context contains an unusually deep span chain. vv starts one child and
   does not walk ancestry or create links to every ancestor.
6. A command returns before an async exporter finishes. This is correct; vv
   must not add an accidental flush or shutdown call in a request path.

### Performance contract

Measure at least these situations on supported Go versions:

| Case | Maximum accepted regression versus undecorated seam | Notes |
|---|---|---|
| no-op provider, simple get | explicit benchmark baseline | allocation-sensitive |
| unsampled remote parent | explicit benchmark baseline | confirms no eager attributes |
| sampled success | explicit benchmark baseline | tracks normal instrumentation cost |
| sampled classified error | explicit benchmark baseline | proves no error text capture |
| list with complex options | explicit benchmark baseline | classifier cost bounded |
| bulk operation | explicit benchmark baseline | span/event count capped |

The exact numbers are release-specific and must be recorded when the API is
implemented; choosing arbitrary targets in a roadmap would be false precision.
The release gate is a non-negotiated threshold agreed from measured baseline,
not “telemetry seems fast enough”.

### Invariants and acceptance evidence

- no telemetry package starts a goroutine or network client;
- benchmark source is committed and runs in CI's performance lane;
- operation output, error identity, panic/cancellation semantics match base seam;
- allocation profiles show no command/model/filter serialization;
- fuzz/load tests cap span/event creation independently of input cardinality.

### First implementation slice

Add no-op, unsampled and sampled benchmarks with O-05 before adding optional
policy or async integration. Make failure-containment rules part of code review
checklist, not a one-time benchmark result.

---

## O-22 — test harness and exporter-free conformance suite

**Decision.** The satellite needs a deterministic in-memory test harness that
asserts trace topology, attributes, metric points and privacy. Tests must not
need a collector, wall-clock sleeps, global provider resets or real cloud
accounts.

### Top-level declarative DX

```go
recorder := oteltest.NewRecorder(t)
telemetry := recorder.Telemetry("orders.order")

_, err := vvotel.DecorateService(telemetry, service).Create(ctx, command)
recorder.RequireSpan("vv.service.create orders.order").
    WithOutcome("ok").
    WithoutAny("secret", "tenant", "email")
```

The snippet describes desired test ergonomics, not necessarily an exported
production package. It should live in test support unless consumers demonstrably
need the exact same assertions.

### Happy use cases

1. A unit test starts a known parent span, calls a decorated service and asserts
   one correctly named child with a bounded outcome.
2. A fake repository returns a conflict; the harness asserts service/repository
   parentage and confirms original error identity with `errors.Is`.
3. A policy fake refuses access; recorder finds one policy event and no subject
   or predicate fields.
4. A metric reader captures command duration/total after a deterministic call
   without relying on an external Prometheus scrape.
5. An asynchronous carrier fixture serializes/extracts trace context and tests
   a linked consumer topology with fixed IDs.
6. A golden JSON export is scrubbed for timing/IDs and compared for vocabulary
   compatibility across refactors.

### Edge use cases

1. Tests run in parallel. Each recorder/provider is private; no test touches a
   global provider or depends on test ordering.
2. An operation intentionally has no parent. Harness asserts a new root/no
   parent according to provider semantics without guessing a global trace ID.
3. Sampling is disabled. Test verifies business semantics and metric behaviour
   while accepting no recorded span data.
4. A hostile corpus uses every forbidden value in command, filter, error, URL,
   tenant and object key. A recursive exporter-payload scan catches regression.
5. End times vary. Assertions compare topology/names/status/attribute sets, not
   real elapsed durations unless a fake clock/instrument is explicitly used.
6. A provider returns non-recording spans. Harness doesn't use private SDK types
   to demand recorded events that a real sampler may legitimately omit.
7. A panic test needs exact propagation. It asserts Go panic behaviour first;
   telemetry assertion is optional and never masks it with `recover`.

### Required fixture families

| Fixture | Contract it proves |
|---|---|
| command outcome matrix | operation/name/error code projection |
| nested repo | span ownership and no duplication |
| policy allow/deny/error | bounded policy event/privacy |
| hostile data corpus | forbidden attribute non-emission |
| context matrix | local/remote/none/cancelled/deadline parentage |
| metric cardinality corpus | closed dimensions and unknown collapse |
| async carrier corpus | trace-only propagation, retry/replay links |
| future satellite contract fakes | storage/event/tenant/audit bridge boundaries |

### Invariants and acceptance evidence

- all tests are deterministic without an OTLP collector;
- no tests need internet, system clock sleep or process-global resets;
- fixtures assert both presence of required data and absence of prohibited data;
- exported test support avoids leaking unstable internals as consumer API;
- property/fuzz corpus runs under race detector for decorator context handling.

### First implementation slice

Implement the private in-memory trace recorder and privacy scanner before public
decorators. A telemetry feature that cannot be locally asserted is not ready to
be observable in production.

---

## O-23 — telemetry schema releases and dashboard compatibility

**Decision.** Span names, attribute keys, metric names, enum values, histogram
buckets and event names are a versioned operational schema. Version them as
carefully as a public JSON/gRPC contract, with overlap periods and migration
evidence for every incompatible change.

### Top-level declarative DX

```text
telemetry-schema: vv-otel/v1
instrumentation scope: example.com/billing@application-version
```

### Happy use cases

1. A new bounded outcome value is introduced. Dashboards first render an
   `other` group, then a release updates queries/alerts before the value becomes
   emitted as a first-class metric dimension.
2. A metric is renamed. The release records old and new metrics in parallel for
   an agreed window, migrates alerts, then deprecates the old series visibly.
3. A span attribute becomes clearer but changes meaning. New key is added; old
   key remains until consumers have a migration path, rather than being reused.
4. Histogram buckets are revised after measurement. Recording and dashboard
   query changes ship together with a compatibility note.
5. A resource family is split. Documentation states historical aggregation and
   dashboard owners choose transitional grouping.
6. A future event revision metric is added. It is trace-only experimental first
   until its finite value set and upgrade behaviour are demonstrated.

### Edge use cases

1. A user runs mixed vv versions during a rolling upgrade. Dashboards must
   tolerate both schemas and not treat missing new attributes as zero.
2. An exporter transforms attribute names. vv release tests validate emitted
   API vocabulary, while collector transformation is documented as host config.
3. A deprecated value is still emitted by an old satellite. New code accepts it
   in readers/dashboards but does not create additional series indefinitely.
4. A production emergency needs temporary debug detail. It must use an
   application-owned, time-bounded configuration and cannot change stable vv
   schema silently.
5. A sampled trace has an experimental attribute but a metric does not. Docs
   clearly distinguish signal stability; users may not build SLO alerts on the
   trace-only field.
6. An enum removal would make old data unqueryable. The migration guide maps it
   to successor or `legacy`, rather than erasing documentation.

### Release checklist

- update registry and generated semantic-schema artifact;
- classify change as additive, deprecated, renamed, redefined or removed;
- calculate metric cardinality before/after;
- update golden traces, dashboards, alerts and example queries;
- test mixed old/new satellite behaviour where relevant;
- publish an upgrade note and an end-of-support date for deprecated names;
- ensure no compatibility shim broadens privacy/data collection.

### Invariants and acceptance evidence

- no string literal key/name is emitted outside registry-owned code;
- semantic-schema diff is reviewed with every release;
- dashboard fixtures work against old, new and mixed golden exports;
- breaking telemetry schema change triggers major/minor policy stated by project;
- release notes name privacy changes as prominently as feature changes.

### First implementation slice

Check a registry snapshot into the telemetry satellite and add a CI diff gate.
Do this even before dashboard packages exist: operational users need a stable
source of truth from day one.

---

## O-24 — cross-module synergy map and delivery gates

**Decision.** Telemetry does not “integrate everything” by importing every
future module. It supplies stable correlation vocabulary; each satellite owns
its domain contract and opts into the vocabulary through narrow bridges.

### Top-level declarative DX

```text
HTTP span
  -> vv.service.create orders.order
    -> vv.repository.save orders.order
      -> db.client operation (upstream)
    -> vv.outbox.enqueued (event)
      -> vv.audit.record (audit correlation)
```

### Happy use cases

1. A user creates an order, producing a command span, logical repository span,
   driver span, durable outbox event and audit revision correlation without any
   one module owning the others' exporter/SDK.
2. A worker publishes an event, later storage writes a document and the audit
   record can be joined through chosen trace correlation; each action retains
   correct asynchronous topology.
3. A database-per-tenant deployment behaves as the same service/resource metric
   family as one-db mode while isolation code, not telemetry, chooses routing.
4. A localized validation failure has stable code/outcome across languages and
   never exports translated customer content.
5. Event upcasting/release rollout can be correlated with host `service.version`
   and bounded event revision information without event payload leakage.

### Edge use cases

1. One future satellite is not installed. The remaining satellites retain their
   contracts; missing spans never change data flow or authorization.
2. A satellite would need both OTel and an SDK (S3, NATS, Temporal). It owns a
   separate dependency decision and must not be merged into `vv/otel` merely
   for a prettier trace tree.
3. A telemetry collector is compromised or broadly accessible. Safe-by-default
   vocabulary means it cannot yield tenant/actor/object/event secrets supplied
   by vv.
4. A consumer chooses different providers for two runtimes. Explicit injection
   keeps their spans/metrics isolated and makes ownership clear.
5. An audit retention/legal hold outlives traces. Broken trace correlation is
   acceptable; no durable audit requirement is delegated to sampled telemetry.
6. A future feature asks telemetry to repair a missing domain invariant. The
   request is refused: traces show what happened, contracts prevent it.

### Integration ownership matrix

| Domain | Owns business/durability semantics | Owns backend dependency | OTel role |
|---|---|---|---|
| root vv | existing CRUD/error/policy contracts | none | no import |
| `vv/otel` | semantic registry/decorators | OpenTelemetry only | common vocabulary |
| storage satellite | object interface/stream correctness | fs or chosen S3 SDK | logical storage spans |
| event satellite | PostgreSQL event contract/versioning | PostgreSQL driver adapter | event causal topology |
| tenancy satellite | resolution/routing/isolation | datasource/pool choice | mode/outcome only |
| audit satellite | revision/retention/access | audit store choice | optional correlation |
| i18n satellite | locale/catalogue/plurals | x/text choice | stable code only |

### Invariants and acceptance evidence

- dependency graph verifies each satellite still isolates one decision;
- cross-satellite examples import only modules their application deliberately
  selected;
- end-to-end fixture proves trace correlation across a synthetic command/outbox
  worker/audit path while scanning all exports for forbidden data;
- removing every telemetry decorator preserves all functional integration tests;
- each new bridge is versioned and has no reverse import into root vv.

### First implementation slice

Land O-01 through O-08 as the first real release. Treat O-09 onward as gates
on the corresponding future domain satellites, not reasons to add speculative
interfaces to the root module.

---

## Conformance scenario catalogue

The following catalogue is intentionally more detailed than a list of unit test
names. Every completed implementation card adds executable cases matching these
scenarios. A case has four assertions: functional output, trace topology,
allowed vocabulary and forbidden-data scan. “DX” is a compile-time/boot-time
experience assertion, not a prose claim.

### C-01 — bootstrap and dependency isolation

#### C-01.1 — explicit provider is required

**DX.** `vvotel.New` accepts a caller-owned provider through its declared
configuration type.

**Setup.** Construct a valid in-memory provider and a valid meter provider.

**Action.** Build telemetry with a valid instrumentation and resource name.

**Happy assertion.** Construction succeeds without touching global OTel state.

**Trace assertion.** A later service call records under the supplied provider.

**Privacy assertion.** No environment/config provider value appears in signals.

**Edge setup.** Replace tracer provider with nil.

**Edge assertion.** Construction returns a typed configuration refusal.

**Control assertion.** No span/exporter/global provider is created on refusal.

#### C-01.2 — blank instrumentation name is refused

**DX.** The compiler permits config construction but bootstrap validation names
the invalid field clearly.

**Setup.** Use a valid provider with `InstrumentationName: ""`.

**Action.** Call telemetry construction.

**Happy assertion.** None; this is a configuration-negative case.

**Edge assertion.** The error is deterministic and contains no unrelated SDK
or exporter diagnostics.

**Control assertion.** A valid second config succeeds in the same process,
proving refusal did not mutate global state.

#### C-01.3 — instances remain isolated in parallel

**DX.** Test helper creates telemetry from a recorder in one expression.

**Setup.** Create recorders A and B and build distinct resources.

**Action.** Invoke decorated commands concurrently in both instances.

**Happy assertion.** Recorder A contains only A spans; recorder B only B spans.

**Edge assertion.** Run under `-race` and repeat enough times to expose globals.

**Control assertion.** Functional results match undecorated service results.

#### C-01.4 — no-op provider is a production-safe path

**DX.** Caller can pass a no-op implementation without feature-flag branches.

**Setup.** Create the same decorated service with no-op and recorder providers.

**Action.** Run the full command outcome matrix through both.

**Happy assertion.** All returned models and errors are equivalent.

**Trace assertion.** No-op records no exported payload; recorder sees expected.

**Edge assertion.** A context has a remote non-recording span context.

**Control assertion.** No-op path allocates within benchmark release budget.

### C-02 — naming and registry

#### C-02.1 — resource name comes from declaration, never reflection

**DX.** Decorator requires a validated logical resource declaration.

**Setup.** Use a Go model/table with deliberately hostile internal type names.

**Action.** Call `Create` through a decorated service.

**Happy assertion.** Span name and `vv.resource.name` equal configured value.

**Privacy assertion.** Type/table names do not appear in export payload.

**Edge assertion.** Invalid configured name is refused at bootstrap.

**Control assertion.** Valid rename follows semantic schema migration fixture.

#### C-02.2 — unknown query combination is safe

**DX.** New typed options compile without requiring telemetry string formatting.

**Setup.** Build a future/unknown option combination in a test adapter.

**Action.** Invoke a repository list operation.

**Happy assertion.** Functional call reaches base repository unchanged.

**Trace assertion.** Shape is `other` or omitted as registry specifies.

**Privacy assertion.** No option serialization or filter text appears.

**Control assertion.** Known combinations retain their documented shape values.

#### C-02.3 — raw attribute escape hatch does not exist

**DX.** Public package offers operation/outcome APIs, not arbitrary vv maps.

**Setup.** Compile a consumer misuse example attempting arbitrary vv attribute.

**Action.** Build it as an external-package negative compilation fixture.

**Happy assertion.** No public method permits unreviewed vv attributes.

**Edge assertion.** Application-created enclosing spans remain possible through
upstream OTel APIs and clearly are application-owned.

**Control assertion.** Registry-owned attributes compile and export normally.

### C-03 — service command outcome matrix

#### C-03.1 — successful create

**DX.** One `DecorateService` call preserves the original `port.Service` type
shape.

**Setup.** A fake service accepts a small valid create command and returns a
fixed model.

**Action.** Invoke `Create` with a known active parent context.

**Happy assertion.** Returned model is the same object/value as base service.

**Trace assertion.** Exactly one `vv.service.create orders.order` child span
ends once with `vv.outcome=ok` and an unset success status.

**Privacy assertion.** The create command and returned model are absent.

**Edge setup.** Use an empty-but-valid command whose fields would serialize as
zero values.

**Edge assertion.** Vocabulary remains identical; no field-presence labels form.

**Control assertion.** Base and decorated calls are observationally equivalent.

#### C-03.2 — invalid command

**DX.** No caller must annotate an error for standard `errs` classification.

**Setup.** Fake service returns the project’s typed invalid-request error with a
message containing a deliberately injected token.

**Action.** Invoke `Create` under a recorded parent.

**Happy assertion.** Caller receives the identical typed error.

**Trace assertion.** Service span ends error with `outcome=invalid` and the
allowed public code only.

**Privacy assertion.** Injected token and `error.Error()` text are absent from
attributes and events.

**Edge setup.** Wrap the error twice with different unsafe messages.

**Edge assertion.** Classification is stable; wrappers do not leak.

**Control assertion.** `errors.Is`/`errors.As` behave as without decoration.

#### C-03.3 — policy refusal

**DX.** Policy event collection is enabled with declared policy names at
construction, not strings from request paths.

**Setup.** Configured policy `orders.owner` returns the existing refusal error.

**Action.** Call `Update` with a principal-bearing context created by test code.

**Happy assertion.** Caller sees ordinary generic refusal.

**Trace assertion.** Command outcome is `refused`; one policy event has name,
result and no subject fields.

**Privacy assertion.** Principal ID, e-mail, roles and policy explanation are
absent even if fake policy includes them internally.

**Edge setup.** Execute a 10,000-victim bulk candidate refusal.

**Edge assertion.** Event count stays at documented cap, normally one summary.

**Control assertion.** Policy's own allow/deny count is not changed by telemetry.

#### C-03.4 — absence is not a driver failure

**DX.** The decorator's classifier understands the existing public not-found
surface without transport imports.

**Setup.** Service returns its usual not-found error from `Get`.

**Action.** Invoke `Get` with a hostile object ID that resembles an e-mail.

**Happy assertion.** Caller receives not-found as before.

**Trace assertion.** Span records `vv.outcome=not_found` and bounded error code.

**Privacy assertion.** Object ID does not occur anywhere in exported payload.

**Edge setup.** Underlying repository is instrumented and returns a driver-like
error that service deliberately maps to not-found.

**Edge assertion.** Service span follows public mapping; driver child remains
its own upstream instrumentation concern.

**Control assertion.** No synthetic HTTP 404 or SQLSTATE attribute is emitted.

#### C-03.5 — deadline and cancellation retain semantics

**DX.** Callers still pass normal `context.Context`; no telemetry cancellation
API is required.

**Setup.** Fake service returns `context.DeadlineExceeded` and separately
`context.Canceled` after observing its context.

**Action.** Invoke corresponding commands with controlled contexts.

**Happy assertion.** Exact cancellation/deadline error passes through.

**Trace assertion.** Outcome is respectively `deadline` or `cancelled`.

**Privacy assertion.** Context values and deadline timestamp do not export.

**Edge setup.** Context has a remote sampled parent and a hostile baggage value.

**Edge assertion.** Parentage works; baggage remains uninspected.

**Control assertion.** Decorator does not cancel/extend/replace caller context.

#### C-03.6 — panic remains application behaviour

**DX.** No special panic option is needed for the basic decorator.

**Setup.** Fake service panics with a sentinel value.

**Action.** Invoke it under a test that recovers outside the decorated call.

**Happy assertion.** Same sentinel reaches external recover point.

**Trace assertion.** Basic release specifies either normal deferred end with no
panic data or no assertion; it must not add raw stack/panic attributes.

**Privacy assertion.** Panic string/stack is absent by default.

**Edge setup.** Application’s own recovery middleware records a safe log.

**Edge assertion.** Middleware chooses its own trace event policy explicitly.

**Control assertion.** Recovery ordering is not changed by an internal recover.

### C-04 — repository ownership and query shape

#### C-04.1 — one logical repository span, optional SQL children

**DX.** Repository decoration accepts a normal port repository and requires no
database driver-specific interface.

**Setup.** Use a fake repository that invokes a fake driver observer zero, one
or three times.

**Action.** Call `GetAll` through service and repository decorators.

**Happy assertion.** One service span has one repository child.

**Trace assertion.** Optional driver spans remain children of repository span;
vv does not duplicate them or name them as vv SQL spans.

**Privacy assertion.** Fake SQL, binds and table name are absent from vv spans.

**Edge setup.** Driver observer is registered twice deliberately.

**Edge assertion.** vv leaves duplicates visible rather than guessing removal.

**Control assertion.** Query execution count/result remain unchanged.

#### C-04.2 — paged shape never reports cursor or size

**DX.** Shape comes from typed options automatically.

**Setup.** Create a page option with a cursor containing a known secret and a
large page size.

**Action.** Invoke `GetAll`.

**Happy assertion.** Repository span reports `vv.query.shape=paged`.

**Trace assertion.** Span has only registry-approved query attributes.

**Privacy assertion.** Cursor, page size and sort field do not occur.

**Edge setup.** Combine page with search and preload options.

**Edge assertion.** Classifier follows documented precedence/`other` rule.

**Control assertion.** Typed options reach base repository unmodified.

#### C-04.3 — aggregate remains bounded

**DX.** Aggregate kind requires an allow-listed form.

**Setup.** Request `count`, then an unknown custom aggregation extension.

**Action.** Invoke repository aggregate operations.

**Happy assertion.** Count records documented aggregate kind or shape.

**Trace assertion.** Unknown extension is `other`/omitted, not formatted text.

**Privacy assertion.** Group keys, field selections and result value stay out.

**Edge setup.** Result is an extremely large number.

**Edge assertion.** No value-derived attribute/metric label appears.

**Control assertion.** Return type/value/overflow semantics are unchanged.

#### C-04.4 — transaction parentage does not certify correctness

**DX.** Decorated `Tx` keeps existing function and error signature.

**Setup.** Fake repository starts a transaction and invokes nested `Save`.

**Action.** Execute successful and rollback cases.

**Happy assertion.** Trace shows logical transaction/service/repository work as
specified, while fake driver observes same calls.

**Trace assertion.** Commit/rollback outcome is bounded; datasource name absent.

**Privacy assertion.** Transaction handle, connection string and model absent.

**Edge setup.** Bind query to a wrong datasource and make base reject it.

**Edge assertion.** Telemetry records actual error outcome and makes no success
claim based merely on a nested span.

**Control assertion.** Existing transaction guard executes before/unchanged.

### C-05 — privacy and policy adversarial corpus

#### C-05.1 — one corpus covers every forbidden source

**DX.** Test helper accepts an arbitrary command/filter/error/context corpus and
scans serialized trace and metric output.

**Setup.** Populate unique sentinel values for tenant, user, token, cookie,
filter, SQL literal, object key, event payload, message text and locale.

**Action.** Exercise success, invalid, denied, conflict and internal-error flows.

**Happy assertion.** Required bounded vocabulary is still recorded.

**Trace assertion.** No serialized export contains any sentinel substring.

**Metric assertion.** No metric attribute contains a sentinel or dynamic count.

**Edge setup.** Place a sentinel in nested error wrapping and map keys.

**Edge assertion.** Recursive scanner still finds no leak.

**Control assertion.** A test-only expected safe span name remains detectable.

#### C-05.2 — policy event cap holds under bulk work

**DX.** One config value/documented default communicates event cap clearly.

**Setup.** Fake policy is called for 20,000 synthetic candidate victims.

**Action.** Execute a bulk command through enabled policy observability.

**Happy assertion.** Base policy executes its normal semantics.

**Trace assertion.** Span has at most configured number of policy events.

**Privacy assertion.** No victim representation, key or count-as-label occurs.

**Edge setup.** Every other victim produces a unique denial explanation.

**Edge assertion.** Explanations never enter span/event payload.

**Control assertion.** Memory/span count stays within measured cap.

#### C-05.3 — trace ID exposure changes no public refusal

**DX.** Host may choose response trace ID separately from vv service decorator.

**Setup.** Run same denied request with and without host response correlation.

**Action.** Compare status, public body and headers excluding chosen trace ID.

**Happy assertion.** Public refusal is functionally identical.

**Trace assertion.** Denied span still has safe policy event/outcome.

**Privacy assertion.** Policy reason/subject remains absent despite correlation.

**Edge setup.** Use an attacker-selected remote traceparent.

**Edge assertion.** No internal reason becomes derivable from propagated values.

**Control assertion.** Disabling telemetry preserves response exactly.

### C-06 — metrics and cardinality

#### C-06.1 — cardinality is independent of tenant population

**DX.** Default metrics need no tenant configuration to be safe.

**Setup.** Generate 10,000 distinct tenant IDs, request IDs and object IDs.

**Action.** Run the same successful `Get` and failed `Create` for each.

**Happy assertion.** Command counter series stay within resource×command×outcome.

**Metric assertion.** No tenant/request/object value becomes an attribute.

**Trace assertion.** Sampled traces likewise exclude identities.

**Edge setup.** Use database-per-tenant resolver mode.

**Edge assertion.** At most fixed `tenant.mode=database` series is added.

**Control assertion.** Functional resolver routing is not touched.

#### C-06.2 — new error code defaults safely

**DX.** Registry addition is an explicit reviewed code change.

**Setup.** Return a future/unknown public-code-like error from fake service.

**Action.** Invoke a command with metrics enabled.

**Happy assertion.** Operation returns original error.

**Metric assertion.** Outcome maps to `error`/`other_error` according to table.

**Trace assertion.** No arbitrary code/error string becomes an attribute.

**Edge setup.** Emit 1,000 unique unknown fake codes.

**Edge assertion.** Series count does not increase beyond bounded fallback.

**Control assertion.** Accepted known codes retain their exact projections.

#### C-06.3 — histogram changes require a fixture migration

**DX.** Bucket definition is visible in one registry/snapshot location.

**Setup.** Capture a baseline metric golden fixture.

**Action.** Propose a bucket change in a test branch/fixture.

**Happy assertion.** CI semantic diff identifies changed histogram schema.

**Metric assertion.** Existing dashboard fixture is updated or fails loudly.

**Edge setup.** Mix old and new telemetry producers.

**Edge assertion.** Query fixture tolerates both bucket schemas during overlap.

**Control assertion.** No unrelated metric name/dimension changes slip through.

### C-07 — asynchronous hand-off

#### C-07.1 — outbox commit versus broker acceptance

**DX.** Future event envelope propagation is a typed, size-bounded operation.

**Setup.** Fake transactional outbox and broker expose configurable crash points.

**Action.** Execute append/enqueue/publish through each crash point.

**Happy assertion.** Committed enqueue has a durable causal context fixture.

**Trace assertion.** Publisher success means broker acceptance only, not consume.

**Privacy assertion.** Event ID, payload, aggregate ID and headers absent.

**Edge setup.** Crash after broker acceptance before sent mark.

**Edge assertion.** Retry attempts link appropriately and allow duplication.

**Control assertion.** Transaction and retry semantics derive from event module.

#### C-07.2 — replay uses a link, not an impossible long parent

**DX.** Replay API names manual replay explicitly in future job/event config.

**Setup.** Create an old persisted carrier context and schedule replay days later.

**Action.** Process it with a fake worker clock.

**Happy assertion.** Handler receives normal execution context.

**Trace assertion.** Fresh processing span has a causal link to origin, as spec.

**Privacy assertion.** Carrier bytes and message identity never export.

**Edge setup.** Stored carrier is malformed/too large.

**Edge assertion.** Fresh root/validity class works without panic or value leak.

**Control assertion.** Replay executes according to domain deduplication rules.

#### C-07.3 — job cancellation is not a delivery guarantee

**DX.** Worker decorator passes caller/runtime context unchanged.

**Setup.** Fake handler ignores cancellation after starting work.

**Action.** Cancel task context during execution.

**Happy assertion.** Runtime reports its actual handler result.

**Trace assertion.** Span reflects cancellation/result without claiming stop.

**Privacy assertion.** Job argument and lease token are absent.

**Edge setup.** Runtime retries same job after lease expiration.

**Edge assertion.** Two attempts exist with bounded number/link structure.

**Control assertion.** No vv retry or cancellation call occurs.

### C-08 — future-domain bridge fixtures

#### C-08.1 — storage fs and S3 share logical vocabulary

**DX.** Storage decorator configuration selects a configured store family, not
an object path or endpoint.

**Setup.** Implement equivalent fake filesystem and fake S3 stores.

**Action.** Put/get/delete an object with hostile keys in both.

**Happy assertion.** Logical vv storage operation/outcome attributes match.

**Trace assertion.** Backend SDK/HTTP detail is optional and outside vv fields.

**Privacy assertion.** Key, path, bucket, endpoint and signed URL absent.

**Edge setup.** S3 fake performs multipart retry and fs fake rejects traversal.

**Edge assertion.** Results map boundedly without changing common vocabulary.

**Control assertion.** Stream/resource ownership stays defined by storage API.

#### C-08.2 — event conflict has no stream identity

**DX.** Event store configuration names a stream family at bootstrap.

**Setup.** Fake PostgreSQL event store rejects an append on expected version.

**Action.** Append with a sentinel aggregate/stream ID and revision.

**Happy assertion.** Caller gets normal optimistic-concurrency failure.

**Trace assertion.** `vv.eventstore.append` has `outcome=conflict`.

**Privacy assertion.** Aggregate ID, expected/actual version and payload absent.

**Edge setup.** Event upcaster fails on historic revision.

**Edge assertion.** Bounded upcast failure/revision family is all that exports.

**Control assertion.** PostgreSQL transaction/retry behaviour remains event-owned.

#### C-08.3 — tenant mode is bounded, routing stays opaque

**DX.** Tenancy config supplies an enum mode, never a tenant-ID extractor.

**Setup.** Use row and database-per-tenant fake resolvers with unique IDs.

**Action.** Resolve and execute identical commands through both.

**Happy assertion.** Both calls return base resolver results.

**Trace assertion.** Only `vv.tenant.mode=row|database` differs.

**Privacy assertion.** Tenant ID, database/schema and connection string absent.

**Edge setup.** Resolver deliberately misroutes and returns a typed error.

**Edge assertion.** Telemetry records outcome, never certifies isolation.

**Control assertion.** Security routing test is valid with telemetry disabled.

#### C-08.4 — audit correlation survives absent sampling

**DX.** Audit bridge accepts optional current span context and document its
missing-correlation representation.

**Setup.** Run audited mutation with sampled, unsampled and no span contexts.

**Action.** Persist fake audit revision in each case.

**Happy assertion.** Every audit record has full domain-required content.

**Trace assertion.** Sampled run may correlate; unsampled run remains correct.

**Privacy assertion.** Audit subject/actor/diff does not enter trace payload.

**Edge setup.** Audit writer runs asynchronously after parent span has ended.

**Edge assertion.** New span/link follows explicit async contract if used.

**Control assertion.** Audit atomicity/failure policy is identical without OTel.

### C-09 — schema release and interoperability

#### C-09.1 — additive attribute is visible to compatibility tooling

**DX.** Registry update requires a declared stability class and release note.

**Setup.** Add a hypothetical bounded trace-only attribute in a semantic-schema
fixture while retaining existing operation/outcome keys.

**Action.** Run schema snapshot comparison and dashboard fixture validation.

**Happy assertion.** Diff reports an additive experimental change explicitly.

**Trace assertion.** New field appears only on its documented span operation.

**Metric assertion.** No metric dimension changes without separate approval.

**Edge setup.** Run fixture against an older collector/dashboard schema reader.

**Edge assertion.** It ignores unknown trace field without breaking old query.

**Control assertion.** Existing key/value golden export is otherwise unchanged.

#### C-09.2 — renamed metric has an overlap plan

**DX.** A release change must name old metric, new metric and end date.

**Setup.** Prepare old-only, overlap and new-only metric fixture snapshots.

**Action.** Evaluate documented alert/dashboard query fixtures against each.

**Happy assertion.** Overlap fixture yields one logical alert result, not double
counting old and new streams.

**Metric assertion.** Both names preserve finite exact label vocabulary.

**Trace assertion.** Trace schema does not accidentally change with metric name.

**Edge setup.** A rolling upgrade has old/new producers simultaneously.

**Edge assertion.** Missing new metric is treated as absent schema, not zero.

**Control assertion.** Deprecation removal fails fixture until queries migrate.

#### C-09.3 — resource rename is not a silent dashboard break

**DX.** Resource declaration validation supplies a migration metadata hook or
documented manual process, never implicit reflection aliasing.

**Setup.** Create golden traces for `orders.order` and proposed `commerce.order`.

**Action.** Run resource rename migration test fixture.

**Happy assertion.** Release documentation maps historical and new grouping.

**Trace assertion.** Each trace uses exactly declared name for its version.

**Metric assertion.** Cardinality calculation includes temporary dual series.

**Edge setup.** Consumer pins an older vv/otel version during rollout.

**Edge assertion.** Cross-version dashboards remain explicit and functional.

**Control assertion.** No span contains database/type name as fallback alias.

#### C-09.4 — upstream semantic convention adoption is deliberate

**DX.** Registry proposal identifies vv key, upstream convention and migration.

**Setup.** Simulate adoption of a newly stable upstream semantic key.

**Action.** Compare registry, documentation and golden exporter payload.

**Happy assertion.** Upstream key is added only where its semantics precisely fit.

**Trace assertion.** Existing vv key is not repurposed to a different meaning.

**Privacy assertion.** Standardization does not authorize a sensitive value.

**Edge setup.** Upstream recommended key can hold a dynamic URL/database value.

**Edge assertion.** vv still refuses it under its own data-governance policy.

**Control assertion.** No direct upstream SDK change is forced on root vv.

### C-10 — operational failure drill

#### C-10.1 — exporter outage is functionally invisible

**DX.** Host can configure a failing test exporter through normal provider API.

**Setup.** Provider records spans to an exporter that rejects/flakes by design.

**Action.** Run success, conflict and cancellation commands under load.

**Happy assertion.** All business outputs equal baseline outputs.

**Trace assertion.** SDK may report internal exporter diagnostics to host policy;
vv emits no user-operation error because export failed.

**Metric assertion.** Meter path remains independent as configured by host.

**Edge setup.** Exporter blocks within its own configured processor limit.

**Edge assertion.** Benchmark/regression gate catches added request-path block.

**Control assertion.** vv never calls exporter, flush or shutdown directly.

#### C-10.2 — collector compromise drill validates safe payload

**DX.** Privacy scanner can render an export-like payload for security review.

**Setup.** Populate an end-to-end command with every known sensitive sentinel.

**Action.** Export sampled trace/metrics/log-correlation fields to fixture file.

**Happy assertion.** Operator-relevant operation/outcome/resource data remains.

**Privacy assertion.** Sentinel scan is empty across all vv-created data.

**Edge setup.** Include object key, event revision failure, tenant resolver error
and translated message in the same causal path.

**Edge assertion.** Future bridge vocabulary also passes the scanner.

**Control assertion.** Application-owned upstream HTTP/driver attributes are
listed separately, preventing false claim that vv config protects them.

#### C-10.3 — sampling outage does not erase audit or correctness

**DX.** Application can switch sampler to drop-all without editing vv wiring.

**Setup.** Run a cross-module synthetic mutation with drop-all sampler.

**Action.** Execute service → repository → outbox → audit fake flow.

**Happy assertion.** Model, event and audit records are complete per their specs.

**Trace assertion.** No recorded vv trace is required for assertions to pass.

**Metric assertion.** Metrics follow independent meter provider configuration.

**Edge setup.** Switch sampler during a rolling deployment.

**Edge assertion.** Mixed sampled/unsampled producers leave no business gap.

**Control assertion.** Removing every decorator yields same durable records.

#### C-10.4 — degraded telemetry remains bounded under storm

**DX.** One configuration documents per-operation span/event/batch limits.

**Setup.** Generate a burst of huge bulk commands, retries and invalid input.

**Action.** Run with sampled provider and constrained exporter test double.

**Happy assertion.** Base services preserve their existing backpressure/error rules.

**Trace assertion.** Span/event count per command remains under declared cap.

**Metric assertion.** Series count remains finite despite unique hostile values.

**Edge setup.** Include thousands of retries for a poison async message.

**Edge assertion.** Attempt details bucket/cap rather than create endless fields.

**Control assertion.** Heap/allocation profile stays within performance release gate.

---

## Definition of done for the first OTel release

The first release is complete only when all of the following are true:

1. O-01 through O-08 are implemented with their executable conformance cases.
2. `vv/otel` is a satellite whose dependency manifest contains only the one
   OpenTelemetry consumer decision required by [[D-051]].
3. Root `vv` has no OpenTelemetry imports, configuration, globals or indirect
   behavioural dependency.
4. Every emitted span name, event, attribute, metric, unit and enum is in the
   checked semantic registry and classified stable/experimental/deprecated.
5. The privacy corpus contains unique sentinels for all currently known risky
   sources and proves none appear in vv-created signals.
6. Command/repository outcomes, values, error identity, cancellation and panic
   semantics are equivalent with telemetry off, no-op, unsampled and sampled.
7. All metric dimensions have a calculated finite cardinality budget, not an
   assumption based on a current small production deployment.
8. No code sets OpenTelemetry globals, launches exporter lifecycle work or
   imports a concrete transport, broker, driver, cloud or log SDK.
9. Tests run deterministically without collector, network, cloud account or
   global provider reset; race and fuzz suites cover context/forbidden data.
10. Benchmarks capture no-op, unsampled, sampled and hostile bulk paths, with
    a release-approved regression threshold based on measured baselines.
11. The integration guide tells consumers how to combine upstream HTTP/gRPC/SQL
    instrumentation without duplicating spans or weakening their own privacy.
12. O-09 through O-24 remain documented gates for later satellites, not APIs
    smuggled into the root or first release prematurely.

## Explicit deferrals

This roadmap deliberately does **not** choose an OTLP endpoint, collector,
exporter, cloud observability vendor, Prometheus registry, HTTP router, gRPC
stack, SQL driver, queue/broker, job engine, S3 SDK or logging framework. It
also does not promise OTel Logs integration, automatic instrumentation, tail
sampling policy, raw request/SQL capture, per-tenant diagnostics, audit
retention, or event delivery guarantees. Each would either violate the privacy
model or make an independent dependency/product choice that deserves its own
contract and satellite under [[D-048]] and [[D-051]].

---

## Implementation review worksheet

Use this worksheet for every OTel pull request. It makes the previous cards
operationally reviewable and prevents a small convenience addition from quietly
changing privacy, compatibility or dependency policy.

### R-01 — ownership

**Question.** Which layer truly owns this span, event or metric?

**Expected answer.** Name one: upstream transport, vv service, vv repository,
upstream driver, or a future satellite domain operation.

**Reject when.** The answer is “all layers need it” or relies on duplicated SQL/
HTTP spans to make a trace look complete.

**Evidence.** Before/after trace topology fixture shows exactly one new owned
operation and no changed behaviour in adjacent owner spans.

### R-02 — dependency decision

**Question.** Does the package import a dependency a consumer might reasonably
want to choose independently?

**Expected answer.** `vv/otel` imports only the approved OpenTelemetry decision.

**Reject when.** The feature requires a router, broker, database driver, cloud
SDK, exporter, collector, logger or metrics backend import.

**Evidence.** `go list -deps` and module manifest match [[D-051]].

### R-03 — vocabulary

**Question.** Is every added string in the semantic registry with a stability
and bounded-value declaration?

**Expected answer.** Span/event/attribute/instrument name and enum values are
registry constants with generated/snapshot evidence.

**Reject when.** A decorator concatenates an input value, method name, type
name, table name, URL, filter, event type or configuration free text.

**Evidence.** Registry diff, golden export and cardinality calculation are in
the review.

### R-04 — privacy

**Question.** Could an external collector learn a user, tenant, record, object,
event, token or business payload from this change?

**Expected answer.** No; use a bounded class, a configured family or omit data.

**Reject when.** “It is hashed”, “only traces”, “sampling is low”, “it is only
an internal collector” or “operators need it” is the sole mitigation.

**Evidence.** Hostile sentinel fixture includes the new data path and passes.

### R-05 — correctness independence

**Question.** What happens when provider is no-op, sampler drops, exporter
fails, collector stalls, context is absent, or telemetry configuration is off?

**Expected answer.** Same model/value/error/transaction/policy semantics.

**Reject when.** A command now waits for export, changes retry/cancellation,
wraps a public error differently, recovers panic, or creates a missing global.

**Evidence.** Equivalence tests run against undecorated/no-op/unsampled/sampled
fixtures.

### R-06 — lifecycle and concurrency

**Question.** Who starts, ends and propagates context for this operation?

**Expected answer.** One documented owner; async transfer has carrier limits and
parent-versus-link rationale.

**Reject when.** Code launches unowned goroutines, retains a span through queue
delay, serializes arbitrary baggage, or closes an app-owned provider.

**Evidence.** Race fixture and async crash/replay topology tests pass.

### R-07 — metrics

**Question.** Can reviewers calculate the maximum series count before ship?

**Expected answer.** Yes, as multiplication of finite declared enums.

**Reject when.** Attribute derives from identity, resource instance, input,
database/schema, URL, error text, exact size/count or open-ended product name.

**Evidence.** Cardinality storm test produces no extra label values.

### R-08 — compatibility

**Question.** Is this additive, deprecating, renaming, redefining or removing
an operational schema element?

**Expected answer.** A release note and dashboard/alert migration describe it.

**Reject when.** Existing name changes meaning in place, a histogram moves
buckets silently, or a rollout cannot tolerate mixed producer versions.

**Evidence.** Old/new/mixed golden fixtures and semantic-schema diff pass.

### R-09 — performance

**Question.** Does the changed path collect/format data only when needed and in
bounded quantity?

**Expected answer.** No-op/unsampled fast paths and bulk caps have measurements.

**Reject when.** It serializes command/model/errors, scans unbounded options,
emits per-row/per-event spans, or calls exporter lifecycle from request path.

**Evidence.** Benchmarks/profiles are compared against approved baseline.

### R-10 — cross-module responsibility

**Question.** Does an integration bridge preserve the domain satellite's
authority over durability, isolation, retention or delivery semantics?

**Expected answer.** Telemetry observes bounded result and causal relation only.

**Reject when.** Tracing determines retry, tenant routing, storage consistency,
event delivery, audit completeness, locale rendering or authorization result.

**Evidence.** Domain contract tests pass with all telemetry removed.

## Milestone sequence

| Milestone | Scope | Must not include | Exit evidence |
|---|---|---|---|
| M1 | O-01–O-04 bootstrap/naming/context | transport or driver adapter | isolated provider + privacy tests |
| M2 | O-05–O-08 service/repo/error | policy internals or raw queries | command outcome matrix + benchmarks |
| M3 | O-09/O-18/O-19 policy/metrics/log correlation | global log/export setup | cardinality + policy privacy suite |
| M4 | O-20–O-23 governance/release | async persistence adapter | schema and performance gates |
| M5 | O-10–O-17 bridges as domains land | reverse imports into root | per-satellite contract fixtures |
| M6 | O-24 end-to-end synthetic example | dependency bundle | cross-module removal/equivalence test |

M1 and M2 are the first release. M3 may follow only when policy outcomes and
metric cardinality are stable. M4 is mandatory before claiming an operational
compatibility guarantee. M5 advances strictly with the separate storage, i18n,
event-sourcing, tenancy and audit roadmaps. M6 is a demonstration fixture, not
a reason to turn the satellites into a platform distribution.

## Operator hand-off checklist

Before a service enables the satellite in production, its owner confirms:

1. the service owns and shuts down its tracer/meter providers;
2. exporter endpoint, credentials, TLS and retention are configured outside vv;
3. standard HTTP/gRPC and SQL instrumentation have each been privacy-reviewed;
4. duplicated server/client/driver instrumentation has been eliminated;
5. resource attributes identify deployment without tenant/customer data;
6. sampling is selected as an operational policy, not to enforce privacy alone;
7. baggage is disabled or bounded at public trust boundaries;
8. the team has tested no-op, sampled and exporter-unavailable configurations;
9. dashboards use stable registry fields and tolerate a rolling upgrade;
10. alert labels stay within calculated cardinality budget;
11. access to traces is not assumed equal to access to audit records or logs;
12. incident procedures use trace IDs as correlation aids, never authorization;
13. a support request for raw IDs/payloads is routed to the audit/domain system;
14. the system has an explicit policy for future outbox/webhook header injection;
15. the team knows that a trace ending successfully proves neither event delivery
    nor audit persistence beyond what the corresponding domain contract says;
16. performance benchmark thresholds have been captured for this vv version;
17. semantic-schema release notes are retained alongside dashboard configuration;
18. removal of the satellite has been rehearsed as a functionally neutral change.

These confirmations intentionally ask more of the host application than a
one-line auto-instrumentation setup. That is the cost of preserving vv's
existing promises about caller ownership, explicit dependencies and safe public
contracts while still producing telemetry that production operators can trust.

## Final scope boundary

If an observability requirement cannot be expressed with the declared operation,
resource family, bounded outcome and explicit causal relationship, it does not
belong in the first `vv/otel` release. The proposing feature must either:

1. add an application-owned enclosing span under its own data policy;
2. design a narrow domain satellite contract with its own dependency decision;
3. add a durable audit/support capability with appropriate authorization; or
4. remain deliberately unobservable until its semantics and privacy cost are
   understood.

This boundary is the mechanism that lets the later storage, i18n, PostgreSQL
event-sourcing, tenancy and audit work interoperate without turning telemetry
into a privileged data exhaust or the root module into an integration bundle.

The same boundary is reviewed again at every satellite release.
