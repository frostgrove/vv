# Product and delivery roadmap — 2026-08-26 15:22 +05

This is a proposed delivery order, not a replacement for the live inventory in
[Roadmap.md](Roadmap.md). The inventory says what is still unbuilt; this document
says what should be done first, why, what the finished behaviour is, and what is
deliberately deferred. It is based on the gaps in
[ai/usecases](../ai/usecases/Index.md), the module readiness sweeps, and the
existing decisions. A new public behaviour still needs a use case, flows and a
decision where the shape is contentious.

## Product direction

vv is already unusually strong where generic CRUD libraries are usually vague:
it owns neither the caller's connection nor transaction, keeps the root module
dependency-free, and makes an error response a public contract rather than a
driver accident. The next work should make those promises dependable under
hostile input, tenant boundaries and production operations before extending the
number of transports or database engines.

The ordering is therefore:

1. remove ways to mutate or disclose more than the policy checked;
2. make the safe path short for the common multi-tenant application;
3. make consumer tests, diagnostics and generated artefacts trustworthy;
4. make the first release repeatable;
5. only then add contract and platform breadth.

## Delivery map

| Horizon | Outcome | Depends on | Exit signal |
|---|---|---|---|
| 0 — trust blockers | A scoped request cannot inspect one set of rows and mutate or reveal another | decisions where noted | the adversarial integration cases are green on every supported engine |
| 1 — safe defaults | The common tenant, soft-delete and public-read shapes need no hand-written policy | Horizon 0 | a new consumer can follow one documented example for each shape |
| 2 — consumer confidence | Tests fail loudly; operators can diagnose refusals; generated API artefacts are usable | Horizons 0–1 | examples and external-package tests use only public seams |
| 3 — release | Every published module is checked in CI and the tag procedure is rehearsed | Horizon 2 | a clean checkout can produce a candidate release unattended |
| 4 — expansion | Add capability only where the contract and live test matrix can carry it | Horizon 3 | each feature has a demand signal, an owner, and a compatibility story |

No horizon is a calendar promise. A smaller, independently safe item can ship
earlier; an item that needs a product decision must not quietly choose a
different contract just to keep the schedule moving.

---

## Horizon 0 — close trust blockers

### 0.1 Make inspected victims and written victims identical

**Problem.** A gated `UpdateAll` or `DeleteAll` can receive a caller-provided
limit. The gate inspects the limited set, while SQL changes every row matching
the filter. This breaks the core assumption of a per-row policy. It is recorded
as UC-004 gap 4 and is the open question in [[D-026]].

**Recommended contract.** Paging is not a meaningful operation on a mass write.
At the repository boundary, reject `Page`, `Limit`, `Offset`, cursor and
`Unpaged` options on `UpdateAll` and `DeleteAll`; the only selection is the
filter. This fail-closed rule removes the mismatch for every present and future
decorator. If bounded bulk work is needed, introduce a separate, explicitly
named API whose victim query and write share one deterministic key set.

**Done when:**

- the decision names the accepted mass-write selection model;
- every pagination spelling is refused before any statement is executed;
- positive and control tests prove that an `Inspect` sees every changed row;
- the behaviour is covered by recorder tests and live PostgreSQL, MySQL,
  MariaDB and SQLite cases;
- HTTP and gRPC document that these verbs are intentionally not exposed until
  their selection semantics are equally safe.

### 0.2 Make resurrection an explicit soft-delete operation

**Problem.** `Save` is an upsert, so a supplied key may overwrite a tombstoned
row. Under a gate, the existence probe can see that row as absent and classify
the operation as a create. An ordinary create endpoint can thereby revive data
without the application choosing that capability (UC-016 gap 17).

**Recommended contract.** A soft-deleted row stays unavailable to `Save` and
all normal reads. Restoration is an opt-in, separately authorised operation
with an explicit name (`Restore` or a policy-provided restore action); it clears
only the configured tombstone fields and respects tenant scope.

**Done when:**

- a save using a tombstoned key cannot change the row or recreate it;
- a restore is impossible without the application's explicit opt-in;
- restore is narrowed, inspected and audited like any other write;
- tests cover existing/non-existing/tombstoned keys, with and without a gate,
  and the control case proves a normal active upsert still works.

### 0.3 Remove silent transaction and datasource capture failures

**Problem.** A scoped executor keyed with a transaction instead of its physical
datasource may not match any repository and silently run work outside that
transaction. Omitting the datasource can capture too broadly (UC-012 gap 18).

**Recommended contract.** The safe public construction should carry the
datasource identity from the adapter, not ask the application to repeat an
untyped `any`. A scoped request that cannot be matched must return a
distinguishable error before it executes; broad capture must require an
intentionally named opt-in.

**Done when:**

- the API makes the safe source-scoped form the shortest form to write;
- wrong-source and missing-source examples fail loudly and execute zero SQL;
- nested transactions, two databases and both `database/sql` and pgx are live
  tested;
- the ent, gorm, sqlx and sqlc examples demonstrate the correct binding shape.

### 0.4 Make the two untrusted query doors semantically identical

**Problem.** JSON and query-string compilation disagree on null range terms,
comma-separated scalar values, byte slices and malformed `isNull`. Several
bounds also fail to charge nested work, search and select entries (UC-002 gaps
19–20).

**Recommended contract.** Both decoders produce the same closed intermediate
document and use one validator. Every list, path segment, condition, sort,
select and nested-preload predicate consumes a visible budget; invalid input is
refused, never repaired into a different predicate.

**Done when:**

- a table-driven corpus runs each accepted and rejected case through both
  doors and compares the resulting options or fault;
- fuzzing asserts that compilation never panics, never emits partial options on
  a refusal and never puts caller text into SQL;
- each resource can set conservative defaults and documented maximums;
- tests include a deep preload, a deep nested filter and a large search-field
  list so every former budget hole is charged.

### 0.5 Finish the error-code evidence, not a guessed classifier

**Problem.** `CodeExclusion` is public but unreachable because no PostgreSQL
exclusion violation has been captured. Unknown MySQL class-23 errors are also
intentionally left unclassified rather than guessed.

**Plan.** Add an `EXCLUDE` fixture to the live PostgreSQL corpus, capture the
real driver payload and assert its public projection end to end. Keep the
no-guess policy for unknown engine errors; add a regression test that makes the
absence deliberate. This closes Roadmap item 6 without weakening [[D-046]].

---

## Horizon 1 — make safe application patterns the easy patterns

### 1.1 Ship relationship-based tenant scope as a first-class helper

**Problem.** The common schema is `Comment → Article → Tenant`, not a tenant
column on every table. The current scalar scope helper cannot safely give that
shape its create-time check and immutable foreign key; users fall back to a
hand-written policy that narrows reads but may leave creates unguarded.

**Plan.** Design a relation-scope helper around an explicit ownership relation.
It must establish all three pieces together: SQL narrowing through the relation,
write-time verification that the referenced parent belongs to the principal,
and freezing of the relation key after creation. Do not accept an arbitrary
dotted predicate and call it a policy; the declaration must resolve its relation
path at startup and fail on a typo.

**Done when:** a standalone child resource, nested preload, relation filter,
sort, count, update and delete all retain the tenant condition; cross-tenant
parent assignment is refused; and an example covers one-level and self-relation
cases.

### 1.2 Compose public reads with authenticated writes

**Problem.** `auth.Optional()` correctly admits an anonymous request, but
principal-driven policy helpers require a principal. Combining an anonymous
scope with those helpers often fails closed before the policy can select the
public branch.

**Plan.** Add a small, explicit policy combinator such as `WhenPrincipal` or
`AnonymousOr`. It chooses one complete policy branch based on the presence of a
principal; it is not a nullable principal and does not make invalid credentials
anonymous. The anonymous branch must be a real SQL narrowing, not a post-filter.

**Done when:** one documented policy expresses “published rows for everyone;
owners may write their own”, bad credentials still receive 401, and both
branches compose with relation scope and permission checks.

### 1.3 Complete the relation security matrix

Before new policy surface, prove the paths already promised:

- test a principal-derived relation narrowing inside a nested `ORDER BY`;
- validate preload allow-lists hop by hop, so authorising `comments.author`
  cannot accidentally authorise every intermediate relation for another use;
- keep the page `COUNT`, `Exists`, aggregate subqueries, preloads and nested
  filters in the same policy matrix;
- decide aggregate semantics under `InspectReads`: either an aggregate has a
  dedicated authorisation rule or the refusal explains exactly why it occurred.

The result is one generated test matrix over verb × query-shape × policy-shape,
with a control test for every security-positive case.

### 1.4 Make bulk security scale predictably

`SaveAll` under a scope may make one or two existence checks per input row, and
gated cleanup reads every victim fully before one delete. That is safe but turns
a ten-thousand-row sync into N+1 queries and can turn a nightly cleanup into an
out-of-memory process.

**Plan.** Batch ownership/existence checks by key, and inspect filtered victims
in bounded key-ordered batches. The final write must still be protected against
the race between inspection and mutation, for example by a transaction plus a
matching key predicate or appropriate locks. Publish the statement and memory
cost in the module reference rather than hiding it behind “bulk”.

**Done when:** a batch of N inputs has bounded query count, a large cleanup has
bounded memory, no inspected key can escape the final write set, and the
concurrent-writer suite proves the chosen race semantics.

### 1.5 Make policy refusals operable without leaking them

The public 403 must remain generic, but support needs to know which policy or
field denied a request. Add an opt-in observer or structured event seam at the
transport-neutral layer. It receives action, resource, request correlation data
from context and the internal reason; it never serialises that reason to the
client and never writes through a process-global logger.

This should integrate with the existing caller-owned logger contract, not add a
metrics or logging dependency to the root module.

---

## Horizon 2 — earn consumer confidence

### 2.1 Publish strict test doubles at both seams

`crudtest` is valuable for SQL intent, but an unqueued read and an empty read
look alike, queue leftovers pass silently, and handler-level doubles remain
internal.

**Plan.** Keep the current permissive recorder for concise tests, then add an
opt-in strict mode that fails on an unplanned query and can assert the queue was
drained. Publish a small `porttest`-style package, or equivalent public helpers,
for driving a service/handler through its interface with no database.

**Done when:** a consumer can test “the handler refused before execution” and
“this statement was exactly issued” using only exported packages; docs explain
the query queue order for every verb and dialect; and the strict mode has a
control test that fails when an expected query is deleted.

### 2.2 Add a production-ready reference application

The existing stack examples are excellent focused demonstrations. Add one
short, runnable reference app that connects the pieces people need on day one:

- config loaded once and validated before serving;
- database opening, context lifetime and graceful shutdown;
- authentication and a tenant/ownership policy;
- migrations owned by the application, with a clear recommendation rather than
  a new migration framework in vv;
- structured logging/tracing supplied by the caller;
- an HTTP resource, gRPC resource or both, plus a curlable failure example;
- a test that proves tenant isolation and a transaction rollback.

It should be intentionally boring and use `net/http` or one chosen external
binding. Its job is to make the correct architecture copyable, not to showcase
every supported stack.

### 2.3 Generate a machine-readable API contract

The library can expose a resource without handlers, but a frontend or another
service still needs routes, body shapes, error envelopes and field names in a
tool-consumable form.

**Plan.** Add optional CLI output for an OpenAPI document or a smaller vv route
manifest. Start with resource paths, model/update schema, standard faults and
the query endpoint's documented grammar. Make generation an artefact, not a
runtime reflection service, and keep satellite framework imports out of the
generated root-module code.

**Done when:** a generated contract covers net/http, Gin, Fiber and gRPC's
transport-specific differences where applicable; a drift test proves its model
schema derives from compiled types; and one example uses it to build a typed
client or validate a request.

### 2.4 Make schema mismatch visible before production traffic

`Define` validates Go mapping eagerly, while the real table, indexes and
constraints live in the database. Provide an opt-in startup/schema validation
command or API that compares the declared model with catalog information and
reports actionable differences: missing column, incompatible nullability,
missing unique/foreign-key constraint needed by fault probing, or unmapped
generated field.

It must be advisory by default: vv must not own migrations or prevent a
deliberately partial rollout. The reference app should show the check in CI or
startup health validation, not as magic on every request.

### 2.5 Establish observability vocabulary

Design an optional, dependency-free hook interface before emitting metrics.
Useful events include command start/end, total SQL statement count, classified
error code, probe use, transaction join/begin/rollback and policy refusal.
Publish an example adapter for OpenTelemetry or `slog` in a satellite/example,
not in the root contract. Context cancellation and sensitive values must be
redacted by default.

---

## Horizon 3 — make the first tag repeatable

### 3.1 Add CI that runs the checks already worth having

The repository has `make unit`, integration tests, structural checks, generated
API baseline and vulnerability scanning, but the dependency-diff gate is blocked
on CI. Add a pull-request workflow with a clean, reproducible toolchain and
Docker services.

Required jobs:

- unit tests under `-race` for every workspace module and examples;
- integration suite under `-race`, run twice to expose flakes;
- `make check`, `make vet`, formatting check and generated-file diff check;
- public API baseline regeneration, uploaded as a readable diff rather than an
  automatic failure for intentional additions;
- dependency snapshots for every public package, where a change requires an
  explicit reviewed update;
- `make vuln` in release/nightly context, with the workspace-mode safeguard.

Cache only Go downloads and build output; never cache generated source or a
database data directory in a way that changes what the suite proves.

### 3.2 Rehearse release before publishing it

Create a non-publishing release-candidate procedure that starts from a clean
checkout and verifies all published modules resolve at the same version. It
should build a tiny consumer module outside the workspace for each meaningful
combination: root only, each HTTP binding, gRPC, pgx, JWT and `errs` once it is
split after the first tag.

The procedure must check tags, module paths, README install commands, generated
headers and that `go get` works without `replace`. This is the guard against a
workspace hiding a module-resolution bug.

### 3.3 Freeze and communicate compatibility policy

Before `v0.1.0`, decide:

- whether the organisation/module name is final;
- what lockstep means for satellite modules and how retractions will be used;
- what changes to JSON, gRPC and public error codes count as breaking;
- whether a deprecation window exists before `v1`;
- which Go versions and database versions form the support matrix.

The exported-surface baseline catches Go API changes, but wire compatibility
needs its own golden corpus. Add one for error envelopes and generated contract
artefacts before they become client dependencies.

### 3.4 Finish first-tag documentation hygiene

Close the mechanical citations to retired roadmaps, keep the recently reorganised
use-case index navigable, and add a single “choosing a stack” page. A newcomer
should be able to answer in minutes:

- Which two packages are necessary for a minimal app?
- Which optional module matches their driver/framework/auth scheme?
- Which capabilities are guaranteed versus explicitly out of scope?
- Which production checks and limits must they set before exposing query?

This is documentation work, but it is release work: a dangerous default hidden
in a deep module page is still a dangerous default.

---

## Horizon 4 — expansion after the contract is stable

### 4.1 Add engines only with a real compatibility matrix

Prioritise CockroachDB if actual users need PostgreSQL-like distributed SQL;
then SQL Server or Oracle only with a sponsor/user who can provide a live test
server and failure corpus. Each engine must bring a dialect, catalog reader,
fault corpus, probe behaviour, transactional test matrix and a documented list
of unsupported guarantees. “It parses the SQL” is not support.

### 4.2 Add field-level projection/redaction as a port concern

Row-level scope does not prevent a public model from including PII. Explore an
optional response projection/redaction seam after the mapper, applied uniformly
to list, get-by-id, relation preload and remote responses. It should not mutate
the stored model and must compose predictably with explicit `Select` and with
schema/OpenAPI output. Start with a use case and a threat model; do not expose a
half-working `omitempty` convention as access control.

### 4.3 Consider idempotent create and write requests

Payment/webhook/worker consumers eventually need retry-safe creates. A possible
future `IdempotencyStore` interface belongs at the port/application boundary:
the caller owns storage, expiry and key authentication; vv owns only request
fingerprint and replay semantics. This is valuable, but it should not be added
until the response contract and transaction boundary are frozen.

### 4.4 Improve remote-resource resilience without hidden retries

The remote repository can gain caller-configured deadlines, bounded retries for
explicitly idempotent reads, circuit-breaker hooks and tracing propagation. Do
not make the framework retry arbitrary writes or serialisation failures behind
the caller's back; that question remains governed by [[D-040]].

### 4.5 Defer extra transports and framework wrappers

Do **not** build Chi, Echo, GraphQL or queue adapters merely to fill a matrix.
`net/http` already works naturally with router ecosystems. GraphQL needs a
deliberate selection, authorization and error contract; a queue consumer has no
request path or status code at all. Admit either only with a concrete consumer,
a use case and a transport-specific test matrix.

---

## Suggested first three implementation slices

These slices minimise the time a consumer can be exposed to a known dangerous
shape while keeping each review understandable.

1. **Mass-write parity.** Settle [[D-026]], reject paging on filtered writes,
   add the adversarial gate tests and update the module/use-case references.
2. **Soft-delete integrity.** Refuse implicit resurrection, design explicit
   restore as a policy-controlled action, then exercise it across all engines.
3. **Query parity and limits.** Unify the two decoders' validation path, add the
   dual-door corpus and close all budget holes before a public query endpoint is
   advertised broadly.

After those, choose either tenant relation scope (if the first users are B2B
applications) or CI/release rehearsal (if the first tag is imminent). Both are
more valuable than a fifth transport.

## Definition of done for every roadmap item

An item is complete only when all applicable evidence exists:

- its decision and use case say what changed and what remains out of scope;
- a flow maps the changed path; module docs and both language variants describe
  the consumer-facing surface;
- a unit test proves the SQL/contract shape and has a non-vacuous control case;
- integration tests cover every engine and adapter the promise names;
- transport parity is checked across the HTTP triplet and gRPC where relevant;
- fuzz/property tests cover untrusted grammars or generated inputs;
- examples use the public API, and a fresh consumer module can resolve it;
- API, wire and dependency diffs were read by a person rather than blindly
  accepted;
- the item is removed from the live inventory instead of being marked “done”.

---

# Framework capability catalogue

This catalogue makes the roadmap concrete. It covers framework capabilities
that Go's standard library intentionally does not supply and that applications
otherwise re-implement inconsistently: durable side effects, asynchronous work,
resilience boundaries, cache correctness, operational lifecycle, and policy
adjacent decorators. CRUD, typed errors and authentication prove that vv's value
is not “a wrapper over `net/http`”; it is a small set of sharp contracts that
remove an entire class of application mistakes.

The catalogue is deliberately wider than the next release. A row is a design
candidate, not a promise to ship it. Its purpose is to make the future choices
comparable before an application-specific helper accidentally becomes a public
framework subsystem.

## What other frameworks teach, and what vv should take from them

This is not a plan to copy Spring, Dapr, MassTransit or Temporal. Each earned
its abstractions by solving a different deployment model. The transferable
lessons are narrower:

| Source | Useful lesson | vv translation |
|---|---|---|
| [Spring Modulith event publications](https://docs.spring.io/spring-modulith/reference/events.html) | A durable publication needs explicit pending, processing, completed and failed states, resubmission and retention | event publication is a stored contract, not a `go func` after `COMMIT` |
| [MassTransit transactional outbox](https://masstransit.io/documentation/patterns/transactional-outbox) | Outbox and inbox solve different halves; a consumer needs deduplication and ordered delivery after its database work commits | ship the two separately, make their composition easy, never call this broker-level “exactly once” |
| [Dapr pub/sub](https://docs.dapr.io/developing-applications/building-blocks/pubsub/pubsub-overview/) | At-least-once is the honest portable delivery promise; dead letters stop infinite redelivery | every worker contract names acknowledgement, attempt and dead-letter semantics |
| [Dapr jobs](https://docs.dapr.io/developing-applications/building-blocks/jobs/jobs-overview/) | Durable scheduling favours no-early execution and horizontal safety over millisecond precision | a distributed scheduler must name misfire and duplicate policy instead of pretending cron is exact |
| [Temporal Go workflows](https://pkg.go.dev/go.temporal.io/sdk/workflow) | Long-running coordination needs durable state, signals and explicit activities; it is a different product from a task queue | provide a bridge to durable workflow engines before considering an in-house saga runtime |
| [Resilience4j decorators](https://resilience4j.readme.io/docs/getting-started) | Retry, rate limit, bulkhead, circuit breaker and timeout compose as small decorators | one operation-shaped policy can wrap remote calls, workers and services without annotations or global state |
| [Spring cache abstraction](https://docs.spring.io/spring-framework/reference/integration/cache.html) | Caching needs a storage SPI and explicit eviction, not a hidden map | cache only declared stable reads; invalidation is part of the same API |
| [OpenTelemetry Go](https://opentelemetry.io/docs/languages/go/) | traces and metrics are stable ecosystem standards; logs are not a reason to hard-wire an exporter | emit neutral events in the root; ship OTel adapters as satellites |
| [OpenFeature](https://openfeature.dev/docs/reference/intro/) | flags need providers, evaluation context, hooks and provider lifecycle | integrate the standard rather than inventing a flag control plane |
| [OPA decision logs](https://www.openpolicyagent.org/docs/management-decision-logs) | authorisation observability must carry a decision id and redact sensitive policy input | add an audit/decision seam, never send an internal denial explanation to the client |
| [NATS JetStream consumers](https://docs.nats.io/nats-concepts/jetstream/consumers) | a durable consumer has acknowledgement deadlines and redelivery; this is why handler idempotency is required | adapter packages expose these semantics rather than flattening them away |

## Non-negotiable shape of any new subsystem

Every candidate below follows these rules unless a future decision explicitly
supersedes one:

1. The root `github.com/frostgrove/vv` module stays dependency-free.
2. A contract package may import only the standard library and existing contract
   packages, following the closed manifest discipline.
3. A concrete driver, broker, OpenTelemetry, OpenFeature, cron parser or cloud
   SDK lives in a separately versioned satellite module.
4. The application owns connections, clients, credentials, goroutine lifetime,
   deployment topology and retention policy. vv may validate and coordinate; it
   must not secretly start a global worker.
5. Context carries request-scoped facts such as principal, logger, trace,
   locale, idempotency key and transaction capture. It does not carry a mutable
   service locator.
6. Every durable operation names its delivery guarantee exactly: at-most-once,
   at-least-once, deduplicated effect, or ordered-per-key. “Exactly once” is not
   a synonym for “we wrote an outbox table”.
7. Any decorator must forward every optional capability its wrapped value
   exposes through `Next`, `SourceOf`, `BeginnerOf`, `ReadSourceOf` or the
   equivalent walker. [[D-061]] already makes this a correctness rule.
8. Public faults use `errs`; a broker sentence, cache key, policy input,
   credential, SQL text or webhook secret is never an accidental wire detail.
9. Time is injected in tests. A wall clock, random jitter and goroutine timing
   are not assertions.
10. A capability ships only with its unhappy-path story, a retention story and a
    shutdown story.

## Package topology proposed for discussion

The following names are sketches, not an API commitment. They show boundaries,
not directory decisions.

```
events/                       contract: Envelope, Publisher, Handler, registry
events/outbox/                SQL-backed durable publication core
events/outbox/outboxsql/      database/sql implementation
events/outbox/outboxpgx/      pgx implementation (satellite)
events/broker/natsjs/         NATS JetStream publisher/consumer (satellite)
events/broker/kafka/          Kafka publisher/consumer (satellite, only on demand)
jobs/                         contract: Job, Schedule, Worker, Lease, Clock
jobs/sqljobs/                 durable SQL queue and scheduler
workflow/                     portable workflow-facing contracts and Temporal bridge
resilience/                   operation decorators: retry, timeout, breaker, bulkhead
ratelimit/                    local and store-backed quota contracts
cache/                        cache store and cache decorators
idempotency/                  idempotency key store and port service middleware
audit/                        append-only audit event and sink contracts
policy/                       field projection/redaction and external authorizer bridge
flags/                        OpenFeature-neutral feature flag bridge
runtime/                      lifecycle, health and diagnostics contracts
runtime/otel/                 OpenTelemetry instrumentation (satellite)
port/porttest/                service/command fake and assertion helpers
```

The packages should not all be created in one release. A directory is cheap;
the compatibility promise it makes is not. The order below is the order in
which a consumer can safely build on the previous capability.

## Shared vocabulary used by every card

| Word | Meaning in this roadmap |
|---|---|
| **contract** | dependency-free interface and value types that application code may implement |
| **satellite** | optional Go module that isolates one dependency choice |
| **declarative DX** | the shortest honest application wiring; examples are proposed API shapes, not compiling code yet |
| **happy use case** | normal use that must remain concise and unsurprising |
| **edge use case** | failure, race, replay, shutdown or abuse shape that determines whether the feature is trustworthy |
| **control case** | a test that deliberately removes the guard and proves the positive test is not green by coincidence |
| **effect** | an externally visible action: database write, broker publish, webhook call, email or mutation of a remote service |
| **owner** | application code that chooses a policy, resource name, key, retention period or credentials |

---

## A. Reliable side effects and asynchronous work

### F-01 — Typed domain-event kernel

**Priority:** A0 — prerequisite for outbox, audit, webhooks and cache
invalidation.

**What it adds.** A small in-process event vocabulary that is typed, explicit
about metadata and compatible with persistence. It replaces ad-hoc callbacks
and `go func` side effects after a write.

**Why stdlib is insufficient.** Channels move values in one process but do not
give an event identity, schema name, causation chain, durable representation,
handler registry or transaction boundary.

**Natural vv seam.** `port.Service` is the best publisher boundary for a
business command; `crud.Core` is useful only for data-level events that must see
every repository write. Do not make a repository event the only event model:
“order submitted” is a command-level fact, not merely `Save(Order)`.

**Top-level declarative DX — illustrative.**

```go
orders := port.Decorate(
    port.NewService(orderRepo),
    events.Emit[Order, OrderID](bus,
        events.Created("order.created", func(o Order) any { return OrderCreated{ID: o.ID} }),
        events.Updated("order.status-changed", statusChanged),
    ),
)

bus.Handle("order.created", inventory.Reserve)
bus.Handle("order.created", email.SendReceipt)
```

**Happy use cases.**

- An order command commits, then exposes `order.created` to two local handlers.
- A handler receives a stable event name, UUID, occurrence time and correlation
  id without depending on an HTTP request type.
- A service emits a semantic event although it updates several repositories.
- A handler is registered at startup and a duplicate name/handler declaration
  fails before serving traffic.
- An event emitted by an HTTP command and by a gRPC command has the same
  envelope.

**Edge use cases.**

- A handler panics: the source command must not appear successful merely because
  an after-commit callback ran unsafely.
- A handler requests an event version it does not understand.
- A publisher is called with a nil payload, empty type or unregistered schema.
- A context is cancelled after commit but before an asynchronous handler runs.
- An event crosses a queue and must not carry request locale or a bearer token.
- A process starts twice and registers the same handler twice.

**Required invariants.**

- An envelope has immutable `ID`, `Type`, `Version`, `OccurredAt`, `Subject`,
  `CorrelationID`, `CausationID` and opaque payload bytes/value.
- Event type is a stable public contract, not a Go reflect type name.
- Local dispatch has an explicit mode: synchronous-before-return,
  after-transaction, or durable-later; the default is never guessed.
- A handler sees one immutable envelope and cannot alter what another handler
  sees.
- The kernel does not choose JSON, protobuf, a broker or a goroutine policy.

**Minimum first slice.**

1. `events.Envelope`, `Publisher`, `Handler`, `Registry` and a deterministic
   in-memory test dispatcher.
2. Explicit synchronous dispatch only; no persistence claim yet.
3. Metadata propagation from context with a fixed, documented allow-list.
4. A `port` service decorator, not a global event bus singleton.

**Do not include yet.** Event sourcing, broker routing expressions, automatic
payload reflection, wildcard subscriptions and dynamic plugin loading.

**Exit evidence.** A test service emits two semantic events over HTTP and gRPC;
handler order and failure mode are asserted; an external-package test proves no
event leaks an `Authorization` header or locale.

### F-02 — Transactional outbox decorator

**Priority:** A0 — the first durable asynchronous primitive.

**What it adds.** Atomic persistence of a business state change and an event
publication intent in the same database transaction. A separate deliverer moves
the intent to a broker or webhook only after commit.

**Why it matters.** A direct broker publish before commit announces data that
may roll back. A publish after commit can be lost on crash. This is the exact
split that the transactional outbox exists to close; MassTransit distinguishes
its durable transactional outbox from its in-memory buffer, and Spring Modulith
tracks outstanding publication state rather than treating a listener call as a
delivery receipt.

**Natural vv seam.** A `port.Service` decorator enters the repository-owned or
caller-owned transaction and writes both domain state and outbox rows through
the same `crud.Executor`. It must join the executor already captured in context
instead of opening a second transaction.

**Top-level declarative DX — illustrative.**

```go
orders := port.Decorate(
    port.NewService(orderRepo),
    outbox.Emit(sqlOutbox,
        outbox.OnCreate("order.created", orderCreated),
        outbox.OnUpdate("order.status-changed", statusChanged),
    ),
)

runner := outbox.NewRunner(sqlOutbox, natsPublisher,
    outbox.Concurrency(8),
    outbox.Lease(30*time.Second),
    outbox.Retry(backoff.Exponential(time.Second, time.Minute)),
)
app.OnShutdown(runner.Stop)
```

**Happy use cases.**

- `CreateOrder` stores the order and one `order.created` record atomically.
- A runner on any replica claims pending rows and publishes them after commit.
- A broker outage leaves rows pending; restarting a process resumes delivery.
- One service publishes to a topic while another uses the same outbox for
  outgoing webhooks.
- Completed publications are retained for audit for 30 days, then purged.

**Edge use cases.**

- The database commit succeeds and the process dies before broker publish.
- Broker publish succeeds but marking the outbox row complete fails; delivery
  repeats, so the receiver must be idempotent.
- Two runners claim the same row after a lease expires.
- One aggregate emits events A then B; a consumer requires their per-subject
  order even while unrelated subjects process concurrently.
- A poison event exceeds retry attempts or cannot be decoded after a deployment.
- A tenant must never receive another tenant's event due to a routing bug.
- The caller runs a transaction supplied by ent, gorm, pgx or `database/sql`.

**Required invariants.**

- A state change and its outbox rows commit or roll back together.
- Publication is at-least-once; the API never calls it exactly-once delivery.
- Each row has an immutable event id, type, version, payload, subject key,
  attempt count, next-attempt time, state, lease holder and lease deadline.
- Claiming is atomic and recoverable after a dead runner.
- Ordering, if offered, is only per declared subject/partition key; global order
  is not promised.
- Retention and dead-letter policies are required configuration, not a forgotten
  cron job.

**Minimum first slice.**

1. PostgreSQL `database/sql` outbox schema and repository, including
   `FOR UPDATE SKIP LOCKED` or an equivalent tested claim strategy.
2. One `Publisher` contract and a fake publisher for tests.
3. Manual `RunOnce(ctx)` rather than an auto-started goroutine.
4. Pending, processing, published and failed terminal states, plus a CLI/admin
   listing command with payload redaction.

**Do not include yet.** Cross-database atomicity, arbitrary order guarantees,
Kafka transactions, exactly-once marketing, automatic schema migration or an
in-memory outbox advertised as durable.

**Exit evidence.** Kill the process at each point between state write, commit,
broker acknowledgement and completion mark; after restart, assert no committed
intent is lost, duplicate delivery is observable, and a failure cannot expose
the raw payload through `errs`.

### F-03 — Consumer inbox and effect deduplication

**Priority:** A0 — required before vv calls worker handling “safe”.

**What it adds.** A durable record of received event/message ids and processing
state. It turns at-least-once delivery into at-most-once *successful local
effect* inside one consumer transaction where the store supports it.

**Why stdlib is insufficient.** A message acknowledgement tells the broker
nothing about whether the business mutation committed. A `sync.Map` cannot
survive restart, scale across replicas or coordinate with the consumer's
database transaction.

**Natural vv seam.** Worker middleware wraps one handler call. It claims the
inbox entry, runs the handler and the handler's outbox effect in one executor,
then records completion before acknowledging transport delivery.

**Top-level declarative DX — illustrative.**

```go
payments := worker.Handle[PaymentCaptured](
    "payment.captured.v1",
    billing.ApplyCapture,
    worker.WithInbox(sqlInbox, worker.DeduplicateFor(14*24*time.Hour)),
    worker.WithOutbox(sqlOutbox),
)
```

**Happy use cases.**

- A broker redelivers the same `PaymentCaptured` id; billing changes once.
- A consumer writes a receipt and emits `receipt.created` through its own
  outbox in the same transaction.
- A duplicate arriving after successful completion is acknowledged cheaply.
- Two replicas receive the same id simultaneously and one waits or exits as a
  duplicate according to explicit policy.
- Operators can inspect completion attempts for a message id.

**Edge use cases.**

- A worker crashes after database commit but before broker ack.
- A claim lease expires while the first handler still runs.
- A duplicate has the same id but a different type, version or payload hash.
- A provider redelivers after the deduplication window expires.
- A handler calls a remote payment API that has no idempotency key.
- One tenant intentionally reuses an id generated by another tenant.
- The inbox table grows unbounded because completed rows never expire.

**Required invariants.**

- Deduplication identity includes consumer name and message id, not only id.
- A conflicting replay (same id, different immutable envelope) fails loudly and
  is quarantined; it is never treated as a harmless duplicate.
- Completion is recorded only after all local effects commit.
- A transport ack happens only after completion or an explicit dead-letter
  disposition.
- The module publishes a retention requirement and storage estimate.

**Minimum first slice.**

1. SQL inbox table, claim/complete API and `worker.Handler` wrapper.
2. Duplicate, in-progress and failed are separate observable outcomes.
3. The handler receives a stable `Delivery` containing attempt, event id and
   trace/correlation metadata.
4. A fake clock and deterministic store test every lease transition.

**Do not include yet.** Global deduplication across services, inbox reuse as a
message archive, payload mutation on replay or implicit remote-call
idempotency.

**Exit evidence.** A live test delivers the same message concurrently and
after a simulated crash; exactly one local row and one outbox intent exist, while
the broker-facing attempt count remains observable.

### F-04 — Queue worker contract

**Priority:** A0 — a broker adapter is unusable without an application-facing
worker model.

**What it adds.** Transport-neutral worker registration, delivery metadata,
acknowledgement disposition, concurrency limits and shutdown coordination. It is
the asynchronous sibling of `port.Service`.

**Why stdlib is insufficient.** `context`, channels and `errgroup` can run
goroutines, but they do not define how a failed message is retried, acknowledged,
dead-lettered, correlated or reported across NATS, Kafka, RabbitMQ and SQL.

**Natural vv seam.** The contract should be a generic handler:

```go
type Handler[E any] interface {
    Handle(context.Context, Delivery[E]) error
}
```

The broker adapter owns decoding and acks. The handler owns business intent. A
`port.Service` adapter lets a message invoke the same service commands as HTTP
and gRPC rather than duplicating rules.

**Top-level declarative DX — illustrative.**

```go
app.Workers(
    worker.On[OrderCreated]("order.created.v1", fulfillment.Reserve,
        worker.Name("fulfillment.reserve"),
        worker.Concurrency(16),
        worker.Retry(worker.Exponential(100*time.Millisecond, time.Minute)),
        worker.DeadLetter("order.created.failed"),
    ),
)
```

**Happy use cases.**

- An event handler scales to sixteen independent deliveries.
- A handler starts with trace, correlation, causation and principal-derived
  service identity in context.
- A transient database deadlock retries with bounded backoff.
- A permanent validation failure moves to dead letter with an `errs` public
  code and an operator-only cause.
- Worker shutdown stops new claims and waits for active handlers up to a
  configured deadline.

**Edge use cases.**

- A handler exceeds its deadline but keeps a goroutine blocked on I/O.
- The broker redelivers while the old process is still draining.
- A decoder sees a newer event version.
- A handler returns `context.Canceled` because application shutdown began.
- A `panic` occurs after a side effect.
- Concurrency is one for a partitioned key but twenty globally.
- The dead-letter publisher is down too.

**Required invariants.**

- Worker name is globally unique within a process and stable across releases.
- Delivery attempt, first-seen time, source and id are visible to the handler.
- An error classifier decides retry/dead-letter/ack; `err.Error()` does not.
- No handler is auto-retried after its context deadline without a fresh,
  explicitly classified delivery attempt.
- Shutdown never acknowledges an uncompleted handler merely to exit quickly.

**Minimum first slice.**

1. Contract and deterministic in-memory worker harness.
2. Explicit `Disposition` enum: Ack, RetryAfter, DeadLetter, Reject.
3. Context-aware concurrency semaphore and shutdown state machine.
4. No broker; let outbox tests use the in-memory harness first.

**Do not include yet.** Dynamic consumer discovery, autoscaling, work stealing,
arbitrary reflection decoding, exactly-once claims or a built-in dashboard.

**Exit evidence.** Tests cover graceful stop, forced timeout, panic, retryable
error, permanent fault and duplicate delivery; no test passes if `Ack` is sent
before a successful handler return.

### F-05 — Broker adapters, beginning with NATS JetStream

**Priority:** A1 — only after outbox, inbox and worker contracts exist.

**What it adds.** Satellite adapters translating `events.Publisher` and
`worker.Handler` to one broker's real delivery model. NATS JetStream is a good
first candidate because its durable consumers, explicit acknowledgements and
pull-based horizontal scaling map cleanly to the contracts.

**Why not a universal broker abstraction first.** Kafka partitions, RabbitMQ
queues, SQS visibility timeouts and JetStream ack windows have different
semantics. A lowest-common-denominator interface would hide the exact property
the application needs to tune.

**Natural vv seam.** `events/broker/natsjs` implements the core contracts and
adds adapter-specific options in its own module. The generic worker remains
free of NATS imports.

**Top-level declarative DX — illustrative.**

```go
js := natsjs.Connect(nc, natsjs.Stream("APP_EVENTS"))

app.Workers(
    natsjs.Subscribe(js, orderCreated,
        natsjs.Durable("fulfillment-reserve-v1"),
        natsjs.Pull(32),
        natsjs.AckWait(45*time.Second),
        natsjs.MaxDeliver(12),
    ),
)
```

**Happy use cases.**

- The outbox publisher writes an event to a declared subject after commit.
- Pull consumers process batches across replicas with explicit acknowledgements.
- A new deployment resumes a durable consumer at its stored position.
- A failed message redelivers until max attempts then emits a dead-letter
  record with source metadata.
- Per-subject ordering is documented where JetStream provides it.

**Edge use cases.**

- An ack arrives after `AckWait` and another worker has started processing.
- A consumer name changes during deployment and silently starts from a new
  position.
- A subject wildcard consumes an event not authorised for that service.
- Broker payload headers exceed limits or contain spoofed correlation ids.
- A max-delivery advisory is emitted but a dead-letter operation fails.
- The stream retains a payload after the application's privacy retention has
  expired.

**Required invariants.**

- Adapter options expose ack policy, ack deadline, max delivery, durable name,
  consumer group and subject; none are hidden defaults.
- The adapter verifies event type/version before calling business code.
- Broker metadata is attached as untrusted provenance, not accepted as auth.
- `Nack`, timeout and transport disconnect have distinguishable metrics/events.
- The docs state which NATS promise is relied on and which is still application
  responsibility.

**Minimum first slice.**

1. NATS JetStream publisher and pull consumer in one satellite.
2. Docker integration tests with restart, duplicate and ack-timeout cases.
3. An explicit mapping between outbox partition key and NATS subject/key.
4. A test-only fake that exercises the generic worker contract, not NATS mocks.

**Do not include yet.** Kafka, RabbitMQ, SQS or an adapter factory. A second
broker is evidence that the core contract was correctly chosen; it is not a
launch requirement.

**Exit evidence.** A two-replica integration test demonstrates durable resume,
redelivery after missed ack, inbox deduplication and dead letter after the final
attempt, with no side effect duplicated.

### F-06 — Durable delayed jobs and scheduler

**Priority:** A1 — needed for retries, retention, reminders, reports and
outbox housekeeping.

**What it adds.** A schedule record plus a worker delivery when a job becomes
due. It supports one-shot delay, interval and cron-like recurrence, but names
misfire, overlap and time-zone semantics instead of inheriting local process
behaviour.

**Why stdlib is insufficient.** `time.AfterFunc` and `time.Ticker` vanish on
restart, run once per replica, cannot answer who owns a late invocation and
have no durable trace of a scheduled business action.

**Natural vv seam.** `jobs.Schedule` persists command/event intent; a jobs
runner turns it into the same `worker.Delivery` contract. Outbox can schedule
publication retry without importing a cron library.

**Top-level declarative DX — illustrative.**

```go
reminders := sqljobs.New(db,
    jobs.Timezone("UTC"),
    jobs.Lease(30*time.Second),
)

jobs.At(ctx, reminders, time.Now().Add(24*time.Hour),
    jobs.Job("trial.expire", TrialExpired{TrialID: id}),
)
jobs.Cron(reminders, "billing.close-day", "0 5 * * *", billing.CloseDay,
    jobs.Zone("Asia/Almaty"),
    jobs.NoOverlap(),
)
```

**Happy use cases.**

- A trial-expiry event survives restart and fires no earlier than due time.
- Every replica installs the same recurring job but one lease owner runs it.
- A nightly settlement begins at the configured local business time.
- A failed job retry records the original schedule and next due time.
- Outbox completion rows are purged through a named maintenance schedule.

**Edge use cases.**

- DST skips or repeats a local clock time.
- A process is down for two hours and returns after several cron ticks.
- A job takes longer than its interval and overlap would double-settle money.
- A user updates or deletes a job while another replica has claimed it.
- Clock skew makes a lease appear expired on another node.
- A retry due time falls after a retention deadline.
- A cron parser accepts a grammar different from the operator expects.

**Required invariants.**

- Scheduler guarantees no early execution, not punctual execution.
- Misfire policy is explicit: skip, coalesce, catch up once or catch up all.
- All stored times are UTC; time zone is a scheduling interpretation attached to
  the recurrence definition.
- Job idempotency key and tenant/subject partition are part of the record.
- Recurring jobs require an explicit overlap policy.

**Minimum first slice.**

1. SQL one-shot `At` jobs and a manual `RunDue(ctx)` runner.
2. Lease claim, cancellation, retry and fake clock.
3. No cron parser until first users demonstrate a real recurrence need.
4. An adapter that republishes a durable event rather than invoking arbitrary
   functions out of a database row.

**Do not include yet.** Exact-on-time guarantees, global time sync, a browser
schedule editor, ad-hoc shell commands or arbitrary user-supplied Go function
names in durable records.

**Exit evidence.** A fake-clock test covers restart, lease expiration,
cancellation and a late runner; a live two-replica test proves one claimed job
does not run twice without entering the documented duplicate/retry path.

### F-07 — Workflow and saga bridge, not an in-house durable workflow engine

**Priority:** A2 — important, but only after jobs/outbox/inbox are real.

**What it adds.** A small adapter for starting, signalling and observing an
external durable workflow (first Temporal) from vv commands and events. It
provides familiar error/path/context integration without claiming to replace
Temporal's history, replay and activity model.

**Why this boundary.** A task queue coordinates one independent handler. An
order fulfilment or onboarding workflow waits days, reacts to signals, performs
compensation and must recover state after workers die. Temporal exists exactly
for this distinction; rebuilding it inside a CRUD library would be a decade of
hidden operational work.

**Natural vv seam.** A service decorator starts a workflow through the outbox
after its command commits. Worker adapters turn workflow activities into `port`
services or typed application functions.

**Top-level declarative DX — illustrative.**

```go
orders = port.Decorate(orders,
    workflow.StartAfterCommit(temporalClient,
        workflow.On("order.created", "fulfillment.v1",
            workflow.ID(func(e OrderCreated) string { return "fulfillment/" + e.ID.String() }),
        ),
    ),
)

workflow.Signal(ctx, temporalClient, "fulfillment/"+id.String(), "payment.captured", payment)
```

**Happy use cases.**

- An order creates one durable fulfilment workflow after its event commits.
- A payment capture signals a running workflow rather than polling a table.
- An activity invokes a vv service command and gets its typed `errs.Fault`.
- The workflow's trace/correlation id joins the initiating request trace.
- An operator can see workflow id in the order's audit trail.

**Edge use cases.**

- The same outbox event is delivered twice and starts two workflows.
- A deployment changes workflow code in a replay-incompatible way.
- A human approval arrives before the workflow has reached its wait state.
- A cancellation occurs while an irreversible activity is in flight.
- A compensation fails after a downstream service permanently removed data.
- A workflow runs longer than retention of request/user PII permits.

**Required invariants.**

- Workflow id is deterministically derived or the create is inbox-deduplicated.
- Starting a workflow is at-least-once; the adapter relies on the engine's
  workflow-id policy for one running execution.
- Business code never assumes its in-memory local variables survive restart.
- Activity calls have timeout, retry and idempotency policy independent of the
  workflow orchestration retry.
- vv does not expose Temporal types from generic contract packages.

**Minimum first slice.**

1. `workflow.Starter` and `workflow.Signaler` interfaces in a dependency-free
   package.
2. Temporal satellite translating ids, context metadata and faults.
3. One end-to-end sample: order → outbox → workflow → activity → service.
4. Docs that say when a job is sufficient and when a workflow is justified.

**Do not include yet.** A custom workflow DSL, a state-machine runtime, sagas
stored in arbitrary vv tables, distributed transaction illusion or workflow
visualisation.

**Exit evidence.** A test restarts the worker between two activities, redelivers
the starting event, signals during a wait and verifies exactly one business
effect for each idempotent activity.

### F-08 — Outgoing webhook delivery

**Priority:** A1 — a high-value use of outbox that prevents every application
from reimplementing signing, retries and delivery logs.

**What it adds.** A durable subscriber registry and delivery worker for
customer-owned HTTPS endpoints. It is event externalization with a first-class
security, retry and observability contract.

**Why stdlib is insufficient.** `http.Client` sends a POST. It does not choose
which committed event to deliver, sign a canonical body, prevent SSRF, retain a
delivery attempt, rotate secrets, back off, dead-letter, redact payloads or let
customers replay an event safely.

**Natural vv seam.** The outbox stores an event once; webhook subscriptions fan
out by subscription id. Delivery is a worker with inbox-like attempt state, not
a direct `AfterSave` HTTP call.

**Top-level declarative DX — illustrative.**

```go
hooks := webhooks.New(sqlStore,
    webhooks.AllowHosts("hooks.example.com", "*.partner.example"),
    webhooks.Sign(webhooks.HMACSHA256),
    webhooks.Retry(backoff.Exponential(time.Second, 24*time.Hour)),
)

hooks.Subscribe("order.created", customerEndpoint,
    webhooks.Secret(secretRef),
    webhooks.Tenant(tenantID),
)
app.Workers(hooks.Deliver(outboxEvents))
```

**Happy use cases.**

- A customer receives a signed `order.created` after commit.
- Two subscriptions for one tenant get independently retried deliveries.
- A receiver returns 2xx and the delivery records response status/timing without
  retaining response secrets.
- An operator replays one delivery with the original envelope id.
- A subscription rotates its secret while old in-flight deliveries use the
  declared version.

**Edge use cases.**

- The endpoint is `127.0.0.1`, a link-local address or changes DNS after
  validation (SSRF).
- A 301 redirects to an unapproved host.
- The customer returns 429 with `Retry-After`.
- A 410 means unsubscribe, while a 400 may be payload incompatibility.
- A payload contains PII that should not be delivered to every tenant admin.
- A receiver sees the same delivery id multiple times.
- An endpoint holds a TCP connection forever.

**Required invariants.**

- The signed bytes, timestamp, event id, type and secret version are immutable
  per delivery attempt/replay contract.
- URL validation occurs at subscription time and again at connection time;
  redirects are off by default.
- Delivery uses a narrow egress policy, bounded body and per-host concurrency.
- Non-2xx classification is configurable but defaults conservative.
- A customer-visible delivery log never stores raw signing secret or full
  sensitive body by default.

**Minimum first slice.**

1. One HTTPS-only subscription per event type/tenant and a SQL delivery table.
2. HMAC SHA-256 timestamped signature, short response body cap, no redirects.
3. Outbox-backed worker with explicit retry/dead-letter and manual replay.
4. A local test server plus SSRF, timeout and duplicate-id controls.

**Do not include yet.** Arbitrary custom code callbacks, inbound webhook
verification, XML payloads, OAuth token refresh, marketplace UI or a generic
HTTP proxy.

**Exit evidence.** Integration tests prove no request reaches loopback/private
addresses, duplicate delivery ids are stable, retry respects `Retry-After`,
secret rotation is observable and a committed event remains recoverable across
process crash.

---

## B. Declarative resilience, fairness and replay safety

### F-09 — Idempotency keys for externally retried commands

**Priority:** A0 — the public-command complement to inbox deduplication.

**What it adds.** A service decorator that turns a caller-supplied idempotency
key into one durable command outcome. It is for API creates and actions that
clients, load balancers and payment providers may retry after a timeout.

**Why stdlib is insufficient.** A request can be retried after the server has
committed but before the response arrives. `context.Context` cannot persist the
first response, bind a key to an authenticated scope, compare request intent or
coordinate competing replicas.

**Natural vv seam.** `port.Service` decorator, before model mapping makes a
semantic command fingerprint hard to compare. The transport extracts the key;
the service owns replay policy uniformly across HTTP and gRPC.

**Top-level declarative DX — illustrative.**

```go
orders = port.Decorate(orders,
    idempotency.Enforce(sqlKeys,
        idempotency.Header("Idempotency-Key"),
        idempotency.For(port.Create, port.Replace),
        idempotency.TTL(24*time.Hour),
        idempotency.Scope(idempotency.PrincipalAndRoute),
    ),
)
```

**Happy use cases.**

- A mobile client retries `POST /orders` after network loss and receives the
  original 201 representation.
- A payment provider retries the same command id and the order/event exist once.
- The same principal may reuse a key on a different route without collision.
- A completed result is replayed without re-running authorization or mutation.
- A key table is pruned after its declared retention window.

**Edge use cases.**

- The same key carries a different request body or target id.
- Two replicas receive the same key concurrently.
- The first request is still running when the retry arrives.
- The original outcome was a 4xx validation fault, a 409 conflict or a 5xx.
- A malicious client floods unique keys to grow storage.
- Anonymous traffic shares one key value across unrelated users.
- The response body is too large or contains a short-lived signed URL.

**Required invariants.**

- Identity scope, operation, resource and canonical request fingerprint are
  stored with the key.
- Same key plus different fingerprint is a conflict, never a replay.
- A request in progress has an explicit wait/retry response policy.
- Only outcomes declared replayable are persisted; infrastructure failures are
  not converted to successful-looking cached responses.
- Response retention, maximum body and key entropy validation are configurable.

**Minimum first slice.**

1. SQL store supporting claim, complete, fail and replay transitions.
2. Create and replace commands over HTTP and gRPC.
3. Principal-and-route scope, 24-hour TTL and capped rendered response storage.
4. An outbox-aware transaction so command state, key result and event intent
   commit together.

**Do not include yet.** Cross-service idempotency federation, idempotency of
GET, body hashing of streaming uploads or global Redis as a mandatory store.

**Exit evidence.** Concurrent same-key requests create one order; changed-body
reuse returns a stable conflict; a crashed response path replays the committed
result without emitting another outbox event.

### F-10 — Retry policy as a typed operation decorator

**Priority:** A1 — needed by outbox, workers, webhooks and remote resources.

**What it adds.** A reusable retry engine with a typed classifier, bounded
attempts, jitter, retry budget and operation metadata. It does not decide that
every `error` is transient.

**Why stdlib is insufficient.** Go provides loops and timers, not a standard
definition of backoff, cancellation, jitter, budget, attempt event, retryable
error class or protection against retry storms.

**Natural vv seam.** A small `resilience.Do` contract wraps remote calls and
worker delivery. Repository transaction retries remain a separate explicit
decision because [[D-040]] forbids silently retrying caller-visible work.

**Top-level declarative DX — illustrative.**

```go
send := resilience.Retry[webhooks.Delivery](
    resilience.Exponential(200*time.Millisecond, 30*time.Second),
    resilience.MaxAttempts(8),
    resilience.FullJitter(),
    resilience.When(errs.IsRetryable),
    resilience.Budget("webhook-delivery", 0.10),
)
err := send(ctx, delivery, hooks.Post)
```

**Happy use cases.**

- A DNS reset retries an idempotent webhook with jitter.
- A SQL serialization error inside a worker retries the whole delivery attempt.
- A 429 respects a bounded server-provided retry delay.
- Metrics show attempt number, delay, final outcome and reason class.
- A retry budget prevents a brief downstream outage from multiplying traffic.

**Edge use cases.**

- Context deadline expires during sleep.
- A permanent `errs.KindValidation` error is retried by an overly broad matcher.
- A service returns 503 forever and every worker becomes a retry loop.
- `Retry-After` is years in the future or malformed.
- Jitter uses process-global randomness and makes tests flaky.
- A retry wraps a non-idempotent payment call with no idempotency key.
- A caller retries inside an already retrying broker delivery.

**Required invariants.**

- Retry classification is a predicate over error/outcome, never string matching.
- Each attempt receives a child context whose deadline cannot outlive the parent.
- Maximum elapsed time, maximum attempts and retry budget are all bounded.
- Delay calculation is injected/testable and recorded as an event.
- The decorator exposes whether it performed zero, one or N attempts.

**Minimum first slice.**

1. Generic synchronous operation wrapper with fixed/exponential backoff.
2. Context-aware sleep, fake clock/randomness and typed attempt record.
3. No automatic repository/HTTP middleware installation.
4. Explicit integration in outbox and webhook worker only.

**Do not include yet.** Retrying arbitrary `POST`, reflection-driven annotations,
unbounded retries, custom scripting or global retry configuration.

**Exit evidence.** A deterministic test advances a fake clock through every
delay, proves cancellation interrupts wait, verifies permanent faults run once,
and shows nested retry budget exhaustion fails fast.

### F-11 — Deadline and time-budget propagation

**Priority:** A1 — a prerequisite for useful retries, queues and remote calls.

**What it adds.** A named operation budget that divides a parent request's
deadline among database, remote, broker and rendering phases without extending
the caller's deadline.

**Why stdlib is insufficient.** `context.WithTimeout` is powerful but every
layer chooses an arbitrary duration. In a composite request, three sequential
30-second defaults can ignore a 5-second client deadline and leave work running
after the client gave up.

**Natural vv seam.** Context-only helper plus `port.Service`/worker decorator.
The root package can stay standard-library only; no tracer or HTTP dependency is
needed to track an operation's remaining budget.

**Top-level declarative DX — illustrative.**

```go
svc := port.Decorate(orders,
    resilience.Budget(
        resilience.Total(2*time.Second),
        resilience.Phase("database", 900*time.Millisecond),
        resilience.Phase("outbox", 300*time.Millisecond),
        resilience.Reserve("render", 100*time.Millisecond),
    ),
)
```

**Happy use cases.**

- An HTTP request with a 2-second budget leaves time to render a useful fault.
- A webhook attempt gets a 10-second worker budget independent of the original
  client request that produced the event yesterday.
- A database call respects a caller's shorter existing deadline.
- Metrics show which phase exhausted the budget.
- A queue handler uses one consistent deadline for decode, handler and ack.

**Edge use cases.**

- Parent context has no deadline.
- Clock jumps or test time advances backwards.
- A phase gets zero/negative time because previous work consumed the budget.
- A child goroutine accidentally receives `context.Background()`.
- The database driver ignores cancellation until a slow query completes.
- A retry sleeps beyond remaining time.
- A deadline fault leaks internal phase names to an external client.

**Required invariants.**

- A child deadline is never later than the parent deadline.
- An absent parent deadline does not create a hidden universal default unless the
  operation explicitly configures one.
- Phase allocation is advisory, observable and cannot reanimate a cancelled
  context.
- Deadline exhaustion maps to a typed retryable/timeout classification locally;
  wire rendering follows port policy.
- Budget metadata excludes secrets and high-cardinality user values.

**Minimum first slice.**

1. `resilience.WithBudget`, `Remaining` and `Phase` helpers.
2. Service/worker decorators that record duration/phase on a neutral observer.
3. Tests for parent-shorter, cancellation, no-deadline and exhausted-phase
   paths.
4. Documentation showing ownership: transport sets total; application overrides
   worker/outbox budgets.

**Do not include yet.** A global request timeout, automatic SQL query killing or
a promise that every Go dependency honours cancellation.

**Exit evidence.** An integration test starts with a short inbound deadline,
forces a slow first phase and verifies no second remote call starts once the
reserved response time would be consumed.

### F-12 — Circuit breaker for remote dependencies

**Priority:** A2 — after timeout and retry are stable.

**What it adds.** A local state machine that stops sending work to an unhealthy
remote dependency after a configured failure window, permits limited probes and
emits a typed unavailable fault.

**Why stdlib is insufficient.** An HTTP timeout loop tells each request the
same downstream is failing only after it has waited. It does not retain a
rolling result window, open a circuit, schedule probes, cap them or expose
state for readiness/metrics.

**Natural vv seam.** `remote` transport decorator and webhook sender decorator;
not `crud.Core`, because databases and remote APIs have materially different
failure/transaction contracts.

**Top-level declarative DX — illustrative.**

```go
catalogHTTP := remotehttp.Decorate(catalogHTTP,
    resilience.Breaker("catalog-api",
        resilience.Window(50),
        resilience.OpenWhen(resilience.FailureRate(0.5)),
        resilience.OpenFor(20*time.Second),
        resilience.HalfOpen(3),
    ),
)
```

**Happy use cases.**

- A failing pricing API opens its circuit after the declared error threshold.
- Later requests fail fast with a typed unavailable outcome.
- After cooldown, three bounded probe calls determine whether to close again.
- Separate named dependencies have independent breaker state.
- Dashboard metrics show closed/open/half-open transition and rejection count.

**Edge use cases.**

- Authentication 401s or validation 400s count as dependency failure by mistake.
- One hot tenant causes a global circuit for every tenant.
- All half-open probes time out and flood the recovered service.
- Process restart erases state during a widespread outage.
- A circuit opens after cancellations caused by the caller, not the dependency.
- A fallback returns stale data without marking it stale.
- Unbounded breaker names become metric-cardinality abuse.

**Required invariants.**

- Breaker classification is explicit and defaults to transport/timeout/retryable
  failures, not all errors.
- State is process-local by default; a distributed breaker is a separate design.
- Half-open probes are bounded and never bypass caller cancellation.
- Rejection is distinguishable from an attempted downstream failure.
- Fallback is an application-provided operation and carries freshness metadata.

**Minimum first slice.**

1. In-memory deterministic breaker with fake clock and bounded ring/window.
2. Generic operation decorator; remote HTTP example only.
3. Observer events for transitions and rejects.
4. No persistence or cross-pod co-ordination.

**Do not include yet.** Automatic fallback values, shared Redis state, global
breaker registry, database query breaking or per-user dynamic breaker names.

**Exit evidence.** A fake dependency proves open state stops calls, half-open
permits only configured probes, permanent caller validation never opens the
circuit and a context-cancelled caller is not counted as downstream failure.

### F-13 — Bulkheads and bounded concurrency

**Priority:** A1 — protects the process before remote resilience can matter.

**What it adds.** Named, context-aware semaphores/queues that bound concurrent
operations per dependency, worker, tenant class or expensive repository path.

**Why stdlib is insufficient.** `x/sync/semaphore` is useful but does not give
named operational policy, queue/reject choice, metrics, per-operation isolation,
fairness or a declarative way to compose it with retries and time budgets.

**Natural vv seam.** Generic operation decorator, worker concurrency control and
remote transport decorator. It should not hide inside a database adapter where
the caller's pool already owns connection concurrency.

**Top-level declarative DX — illustrative.**

```go
sendInvoice := resilience.Bulkhead(
    "invoice-provider",
    resilience.MaxConcurrent(12),
    resilience.Queue(24),
    resilience.RejectAfter(150*time.Millisecond),
)(invoices.Send)
```

**Happy use cases.**

- A slow email provider uses at most twelve concurrent calls.
- A worker reserves separate capacity for settlement and low-priority reports.
- A queued operation begins when another completes before its own deadline.
- Metrics distinguish active, queued, rejected and completed operations.
- An HTTP caller gets an explicit overload response rather than waiting until
  the server runs out of goroutines.

**Edge use cases.**

- A queued caller cancels and its permit remains reserved.
- A retry attempt consumes another slot while its first attempt still runs.
- One tenant fills the entire queue and starves all others.
- A handler spawns a goroutine that continues after releasing capacity.
- Shutdown blocks forever on a queue with no drain deadline.
- A max of zero accidentally means unlimited.
- Metrics label a bulkhead by raw URL or user id.

**Required invariants.**

- Capacity is non-zero and declared at startup.
- Permit release is guaranteed through defer/state-machine discipline.
- Queue waiting respects context cancellation and never outlives a deadline.
- The caller chooses reject versus bounded queue; neither is an invisible
  default for expensive operations.
- Retry composition consumes capacity per actual attempt and documents order.

**Minimum first slice.**

1. Local semaphore with reject-now and bounded-wait modes.
2. Generic `Do` wrapper plus worker/remote examples.
3. Fake-clock/cancellation tests and observer counters.
4. No tenant fairness algorithm until a concrete quota product exists.

**Do not include yet.** Dynamic autoscaling, global distributed semaphores,
unbounded queues, implicit goroutine pools or database pool replacement.

**Exit evidence.** A stress test holds all permits, cancels one queued request,
then releases capacity and verifies exactly one live waiter proceeds without a
leak or a post-cancellation remote call.

### F-14 — Rate limits and resource quotas

**Priority:** A1 — a public CRUD/query API needs intentional fairness controls.

**What it adds.** Declarative limits for request rate, expensive query units,
bulk mutation cardinality, worker throughput and tenant plan quotas. Local
limits are free of dependencies; distributed enforcement is an optional store
choice.

**Why stdlib is insufficient.** A token bucket implementation alone does not
define identity, trusted proxy boundaries, cost calculation, response headers,
retry time, shared storage correctness, query budget interaction or a tenant
plan model.

**Natural vv seam.** `port.Service` decorator calculates operation cost after
the request is known but before database execution; HTTP/gRPC bindings map an
exceeded limit to their transports. Worker limits use the same classifier but
never send HTTP headers.

**Top-level declarative DX — illustrative.**

```go
api := port.Decorate(orders,
    ratelimit.Enforce(limits,
        ratelimit.By(ratelimit.PrincipalOrIP),
        ratelimit.Rule("read", ratelimit.PerMinute(600)),
        ratelimit.Rule("write", ratelimit.PerMinute(60)),
        ratelimit.Rule("query-units", ratelimit.PerMinute(2_000), ratelimit.QueryCost()),
    ),
)
```

**Happy use cases.**

- An authenticated principal receives 600 cheap reads per minute.
- A deep query consumes more units than a get-by-id before it reaches SQL.
- A paid tenant gets a higher declared plan limit without changing handlers.
- A response includes retry timing appropriate to HTTP; gRPC receives resource
  exhausted metadata.
- A worker limits webhook traffic per destination host.

**Edge use cases.**

- Client IP comes from an untrusted forwarded header.
- Anonymous NAT users share one address and unfairly limit each other.
- A distributed store outage decides whether the API fails open or closed.
- Clocks differ among replicas around bucket reset.
- A caller intentionally creates millions of identities/keys.
- Query cost is calculated after expensive compilation or database work.
- An idempotency replay consumes the mutation quota twice.

**Required invariants.**

- Identity source and proxy trust boundary are explicitly configured.
- Every rule has a bounded key cardinality strategy.
- Local and distributed backends expose their consistency/failure mode.
- Limit rejection runs before the protected expensive operation.
- A replayed idempotent success does not re-charge mutation work by default.

**Minimum first slice.**

1. In-process fixed/token bucket store and principal-or-IP identity helper.
2. Port service decorator for read/write and query cost units.
3. HTTP/gRPC transport projections using `errs.CodeRateLimited` or new agreed
   public code only after an error-contract decision.
4. Documented tests for trusted proxy and anonymous scope.

**Do not include yet.** A central billing system, automatic plan lookup,
multi-region exact quotas or a Redis dependency in root.

**Exit evidence.** Tests prove a spoofed `X-Forwarded-For` cannot pick another
bucket, a rejected deep query executes zero SQL, a replayed idempotent response
does not consume twice, and store outage follows the declared policy.

### F-15 — Read-through cache decorator

**Priority:** A2 — only once invalidation and privacy rules are designed.

**What it adds.** A cache abstraction and a repository/service decorator for
explicitly cacheable reads. It supports TTL, cache key derivation, negative
cache policy and stampede protection without pretending all CRUD reads are safe
to cache.

**Why stdlib is insufficient.** A `map` plus mutex does not define expiry,
single-flight load, serialization, tenant/principal isolation, invalidation,
distributed store choice or metrics. Spring's cache lesson is valuable here:
cache population and eviction must be visible application intent.

**Natural vv seam.** Prefer `port.Service` for rendered/authorised response
shapes and `crud.Core` only for internal public immutable lookup tables. A cache
below security risks serving one principal's scoped result to another.

**Top-level declarative DX — illustrative.**

```go
catalog := port.Decorate(catalog,
    cache.Reads(memoryCache,
        cache.GetByID("product", cache.TTL(5*time.Minute)),
        cache.List("public-catalog", cache.TTL(30*time.Second), cache.Key(publicCatalogKey)),
        cache.SingleFlight(),
    ),
)
```

**Happy use cases.**

- A public product by id is served from cache for five minutes.
- A hundred concurrent cache misses coalesce into one repository read.
- A cache entry includes resource schema/version and tenant/public partition.
- Metrics report hit, miss, load duration and eviction cause.
- A response declares its freshness and is invalidated after a product change.

**Edge use cases.**

- A cache key omits principal or field-projection policy.
- A not-found result is cached while a resource is created moments later.
- TTL expires during a long loader and two loaders race.
- Cache serialization cannot represent `crud.Opt` or an evolving model.
- Cache backend is down; read must still work or fail according to policy.
- A list cache key includes a raw unbounded query document.
- An event invalidates before the database transaction commits.

**Required invariants.**

- A cache declaration names exactly which command/read is safe and its key
  dimensions; no blanket `CacheAll`.
- Security scope, projection, locale and representation version are included or
  the read is refused as cacheable.
- Loader result is stored only after it completes successfully.
- Negative caching and stale-while-revalidate are separate explicit policies.
- Cache failure mode and maximum value size are declared.

**Minimum first slice.**

1. Dependency-free `cache.Store`, TTL entry and in-memory implementation.
2. `GetByID` decorator for explicitly public/no-principal resources only.
3. Single-flight per key, fake clock and cache event observer.
4. No distributed cache backend until a consumer chooses one.

**Do not include yet.** Caching arbitrary scoped lists, caching writes, automatic
model reflection keys, cache-as-primary-store or hidden stale responses.

**Exit evidence.** A security control demonstrates that a missing key dimension
refuses declaration; concurrent misses issue one read; invalidation after a
committed write prevents stale get-by-id; backend outage follows configured mode.

### F-16 — Event-driven cache invalidation and HTTP conditional reads

**Priority:** A2 — one coherent freshness story, not two unrelated caches.

**What it adds.** Invalidation handlers driven by committed events plus optional
ETag/Last-Modified conditional handling for public HTTP responses. The first
keeps server cache coherent; the second prevents sending unchanged bytes.

**Why stdlib is insufficient.** `net/http` provides header types but does not
derive resource version, guarantee invalidation after commit, fan out an event,
associate a cache tag with a model change or define whether a stale response is
acceptable.

**Natural vv seam.** Outbox event handler invalidates cache tags after commit;
`porthttp` projects an explicit representation validator. gRPC gets version
metadata, not fake HTTP semantics.

**Top-level declarative DX — illustrative.**

```go
products = cache.InvalidateOn(products, eventBus,
    cache.On("product.changed", cache.Tags("product", productID)),
)

crudnet.New(products,
    crudnet.Conditional(cache.ETagFromVersion("Version")),
)
```

**Happy use cases.**

- Updating a product commits, emits an event and invalidates product cache tags.
- A public `GET` with matching `If-None-Match` receives 304 without a body.
- A client stores the ETag and performs an optimistic `If-Match` update.
- Related list tags are invalidated when a product becomes unavailable.
- CDN/reverse proxy semantics use the same documented validator.

**Edge use cases.**

- An outbox event delivers twice; invalidation must be idempotent.
- Invalidation is delayed, so cache may serve a bounded stale window.
- ETag is a hash of a representation containing principal-specific fields.
- A weak ETag is used as an update precondition incorrectly.
- Soft delete and restore must invalidate both get and list tags.
- A cache tag value contains untrusted arbitrary query text.
- A conditional request's 304 response accidentally includes a stale body.

**Required invariants.**

- Invalidation follows commit through outbox, not pre-commit callbacks.
- Cache tag namespace, maximum fan-out and retention are bounded.
- Strong update preconditions derive from a persisted version, not a random
  response hash.
- Conditional HTTP is opt-in and applies only to a stable representation.
- Delivery duplicate means repeated invalidation, never a divergent cache state.

**Minimum first slice.**

1. Tag-based invalidation of in-memory cache from local committed events.
2. One get-by-id ETag projection using an existing optimistic lock/version field.
3. HTTP tests for 200/304/412 and gRPC metadata tests where a version exists.
4. Documentation of bounded stale period under asynchronous invalidation.

**Do not include yet.** Surrogate-key CDN integration, transparent distributed
cache invalidation, arbitrary list ETags or freshness claims across regions.

**Exit evidence.** A write that rolls back leaves cache valid; a committed write
invalidates once or many times safely; stale `If-Match` update fails; a scoped
response cannot declare conditional caching without a safe representation key.

---

## C. Policy, integrity and data-governance decorators

### F-17 — Declarative input validation that speaks `errs`

**Priority:** A1 — a useful companion to fault enrichment, not a second source
of truth for database constraints.

**What it adds.** A dependency-free rule contract for structural validation of
commands/models/DTOs before repository work, plus optional adapters for popular
validators. It turns failures into the same multi-violation `errs.Fault` shape
that database probing already uses.

**Why stdlib is insufficient.** Go has no built-in declarative validation,
cross-field rule mechanism, locale-aware violation mapping or standard path
translation from an input DTO to a public error envelope.

**Natural vv seam.** `port.Service` decorator validates a mapped command after
server-controlled fields are cleared and before repository effects. `crud.Core`
must not quietly validate every `Save`: domain/API validation and persistence
constraint truth are distinct layers.

**Top-level declarative DX — illustrative.**

```go
orders = port.Decorate(orders,
    validate.Commands[Order, OrderUpdate](
        validate.Create(orderRules),
        validate.Update(orderPatchRules),
        validate.Replace(orderRules),
    ),
)

var orderRules = validate.Struct[Order](
    validate.Required("CustomerID"),
    validate.Min("Amount", money.OneCent),
    validate.When("Currency", isISO4217),
    validate.Cross("StartsAt", "EndsAt", startsBeforeEnds),
)
```

**Happy use cases.**

- A create reports required, range and cross-field failures in one response.
- A PATCH validates only fields that are present, preserving absent/null intent.
- A rule attaches an `errs.Code` and parameter map without hard-coding English.
- A generated DTO path maps back to the wire name through the existing resolver
  chain.
- An application wraps an existing validator through one adapter rather than
  translating its errors in every handler.

**Edge use cases.**

- Validation changes a value instead of merely rejecting it.
- A plain DTO field is always “present” and accidentally validates an absent
  PATCH value.
- A cross-field rule needs database state and becomes an N+1 query.
- A rule panics or blocks on a remote service.
- A validation failure duplicates a database unique violation.
- An error path names a Go field because a mapper resolver is missing.
- A custom rule exposes the rejected password or token in parameters.

**Required invariants.**

- Rules are pure and side-effect free; I/O belongs in an explicit service
  authorization/command rule, not structural validation.
- PATCH rules receive presence information, not a zero-value lie.
- Multiple rule failures aggregate deterministically and preserve path order.
- Rule codes are registered in `errs` or rejected at startup.
- Validation never claims it eliminated database constraint checking.

**Minimum first slice.**

1. Function-based `validate.Rule[T]` and struct-field/path helpers.
2. Port decorator for create/update/replace with aggregation into `errs`.
3. Presence-aware helpers for pointers and `crud.Opt`.
4. Existing validator bridge with a control case for resolver path translation.

**Do not include yet.** A tag language with reflection magic, remote validators,
automatic SQL constraint generation, schema migrations or silently trimming/
normalising user input.

**Exit evidence.** A three-failure create emits one stable fault; a partial
update validates only defined fields; a bridge fault reaches JSON and gRPC with
the correct wire path; no validation rule runs after the repository call.

### F-18 — Composable command rules and policy decisions

**Priority:** A2 — makes business rules repeatable without turning `port.Service`
into a second CRUD decorator stack.

**What it adds.** An explicit command middleware contract for “may this command
run?” and “what context-derived facts must be attached?” It covers rules that
need business state but are not generic row scope: order state transitions,
approval thresholds, feature prerequisites and immutable workflow states.

**Why stdlib is insufficient.** Hand-written service methods solve this locally
but drift across HTTP/gRPC/queue paths, frequently forget a bulk/background
path, and have no common error/path/observability composition.

**Natural vv seam.** `port.Service` decorator, with a narrow typed rule per
command. It must not reuse `security.Policy` blindly: security narrows data and
authorizes actors, while a domain transition may need a loaded aggregate and an
explicit action vocabulary.

**Top-level declarative DX — illustrative.**

```go
orders = port.Decorate(orders,
    rules.Enforce[Order, OrderID, OrderUpdate](
        rules.Create(requiresOpenCart),
        rules.Update(onlyDraftOrders, noDecreaseAfterPayment),
        rules.Delete(notShipped),
    ),
)
```

**Happy use cases.**

- An order can transition from draft to submitted exactly once.
- A create checks the authenticated account is active before saving.
- A queue worker invokes the same command rule as an HTTP handler.
- Multiple rules contribute separate violations to one command fault.
- A rule records a decision id for audit without adding a response detail.

**Edge use cases.**

- Two rules load the same aggregate and produce duplicate SQL.
- A rule's read scope differs from the subsequent write scope.
- A rule evaluates after the mutation because middleware ordering is ambiguous.
- An administrative override has no visible/audited reason.
- A rule is registered for update but forgotten for replace.
- A bulk command bypasses per-row rules.
- A retry executes an external side effect from a rule twice.

**Required invariants.**

- Rule order is declared and observable; fail-fast versus aggregate mode is
  explicit per command.
- Rules receive a read-only view or explicit loaded state; they do not mutate
  models behind the service's back.
- A rule that requires data uses the same transaction/scope as the write it
  guards.
- A denied action maps to the existing error/privacy contract.
- Every rule has a name suitable for logs/metrics/audit, never a function
  pointer string.

**Minimum first slice.**

1. Generic before-command middleware and named `Decision` outcome.
2. One update-state example using the repository/service seam.
3. Tests for ordering, same transaction capture and queue reuse.
4. No automatic state machine generator.

**Do not include yet.** A rule scripting language, automatic aggregate loading,
rules that mutate DTOs, cross-service distributed locking or remote policy calls
inside an open database transaction.

**Exit evidence.** The same forbidden state transition is rejected identically
over HTTP, gRPC and a worker adapter; a control deletes the rule and proves the
mutation would otherwise occur.

### F-19 — External authorization and policy-engine bridge

**Priority:** B1 — integrate, do not replace current typed local policies.

**What it adds.** A satellite bridge that lets an application ask an external
authorizer (initially OPA-compatible or an application callback) for a command
or resource decision, preserve a decision id and emit a redacted audit event.

**Why it matters.** Local `Role`, `Permission` and `security.Policy` cover many
services cleanly. Organisations with central policy management need distributed
policy bundles, versioning and decision logs. OPA demonstrates that the control
plane and policy evaluation engine are their own product; vv should integrate
without importing a policy language into its core.

**Natural vv seam.** `port.Service` and `crud` security decorator adapters. The
bridge turns `Principal`, action, resource metadata and selected attributes into
a policy input, then maps allow/deny/indeterminate into vv's typed outcomes.

**Top-level declarative DX — illustrative.**

```go
orders = port.Decorate(orders,
    policy.Authorize(opaClient,
        policy.Decision("/vv/orders/allow"),
        policy.Input(policy.Principal, policy.Command, policy.ResourceMeta),
        policy.FailClosed(),
        policy.Audit(decisionAudit),
    ),
)
```

**Happy use cases.**

- A central policy allows an account manager to update assigned orders.
- A denial has a stable decision id in the audit log and generic 403 on wire.
- Bundle revision is recorded with the decision for later investigation.
- Local scope policy still filters rows before an external action decision.
- A background worker supplies its service principal deliberately.

**Edge use cases.**

- Policy engine is down: read endpoint needs fail-open/fail-closed decision.
- Input includes password, token, raw PII or unbounded request body.
- A stale cached decision outlives role revocation.
- The external decision says allow but SQL scope still finds no row.
- A policy response is malformed or names a nonexistent action.
- One OPA call per row becomes an N+1 list authorization attack.
- An operator sees policy reason and accidentally returns it to client.

**Required invariants.**

- Fail-open/fail-closed/stale policy is configured per operation, never global
  magic.
- Input is a whitelist with size caps and redaction, not arbitrary context dump.
- Decision id, policy revision and outcome are audit metadata, not client body.
- A local security scope remains an independent hard boundary.
- Bulk/list decisions must be vectorised or prohibited; no hidden per-row RPC.

**Minimum first slice.**

1. Dependency-free `policy.Authorizer` interface and decision record.
2. HTTP/OPA satellite with explicit request timeout and bounded input encoder.
3. Port command adapter only; no row-by-row read filtering.
4. Recorder tests for redaction and network fault policy.

**Do not include yet.** Rego embedding in root, policy bundle distribution,
automatic cache, policy-editor UI, arbitrary JSON context or a claim that
external authorization replaces database scope.

**Exit evidence.** A fake authorizer proves allow/deny/indeterminate projections;
redaction tests show credentials absent; outage uses declared fail mode; audit
contains a decision id and revision while the wire response does not.

### F-20 — Field-level projection and redaction policy

**Priority:** A1 for customer-facing APIs; row scope alone does not protect PII.

**What it adds.** A transport-neutral representation policy that selects,
masks or removes fields after an authorised service result and before rendering.
It applies uniformly to list, get, preloads, remote responses and generated
contracts.

**Why stdlib is insufficient.** JSON tags are static. `omitempty` is not access
control. Hand-written response DTOs drift across transports and a query `Select`
can become a data-disclosure bypass if security is not part of projection.

**Natural vv seam.** A `port.Presenter`/mapper-facing decorator after service
authorization. Keep it above `crud.Core`: the stored model may legitimately
include a field that this particular client may not receive.

**Top-level declarative DX — illustrative.**

```go
customers = port.Decorate(customers,
    policy.Project[Customer](
        policy.ForAnonymous(policy.Only("ID", "Name", "AvatarURL")),
        policy.ForPermission("customers:pii", policy.Allow("Email", "Phone")),
        policy.Mask("Email", policy.PartialEmail()),
    ),
)
```

**Happy use cases.**

- Anonymous catalog users receive name/avatar but never email.
- Support staff with a permission see a masked email, not a full address.
- A relation preload is projected by the target model's own policy.
- The generated OpenAPI document describes public representation separately from
  internal model fields.
- An audit event records that redaction applied without storing raw values.

**Edge use cases.**

- A user requests `?select=email` and bypasses default projection.
- A nested `Author.Email` preload is not covered by root policy.
- A cache key omits permission/policy revision and serves full PII publicly.
- An aggregate or sort leaks a hidden field's value indirectly.
- A mapper error returns the original model in a fallback path.
- A field is added to a model and defaults visible by accident.
- Masking a value changes business semantics for an internal service consumer.

**Required invariants.**

- Default on a new field is deny until a declaration makes it visible.
- Client `Select` can only narrow an already allowed projection.
- Projection walks relations with explicit policy ownership and depth cap.
- Cache/ETag/OpenAPI keys include representation policy identity.
- Redaction is a representation transform; persistence and authorization logic
  always see the original typed value.

**Minimum first slice.**

1. Explicit field allow-list/deny-list for a root response model.
2. `Select` intersection and a reflection/codegen declaration-time field check.
3. HTTP/gRPC output tests plus cache safety declaration check.
4. No dynamic expression language or automatic PII classifier.

**Do not include yet.** Writing redacted models back, hiding fields from SQL
security rules, opaque reflection masking or a promise that aggregate inference
is automatically safe.

**Exit evidence.** A control request explicitly asks for a denied field and gets
no value; nested relation case is covered; cache/ETag declaration rejects a
projection without a policy dimension; added model field fails a coverage test.

### F-21 — Append-only audit trail

**Priority:** A1 — replace scattered `audit.Log` callbacks with an honest,
durable, queryable security/product record.

**What it adds.** A stable audit event schema plus an append-only sink/outbox
path for user-visible and operator-visible actions: actor, action, target,
outcome, decision id, request correlation and minimal safe change summary.

**Why stdlib is insufficient.** `slog` is operational telemetry, can be sampled
or dropped, and has no retention/query/immutability guarantees. An ad-hoc audit
table often omits failed attempts, before/after policy, actor identity or
transaction linkage.

**Natural vv seam.** `port.Service` decorator observes commands; security and
policy emit decision metadata; outbox durably commits audit intent with the
business mutation. A separate sink may write SQL table, object archive or SIEM.

**Top-level declarative DX — illustrative.**

```go
orders = port.Decorate(orders,
    audit.Commands(auditOutbox,
        audit.Actor(auth.Subject),
        audit.Target(audit.ModelID),
        audit.Change(audit.DiffAllowed("Status", "ShippingAddress")),
        audit.Include(audit.Allowed, audit.Denied),
    ),
)
```

**Happy use cases.**

- A manager changes order status and audit records actor, old/new status and
  correlation id after the mutation commits.
- A denied delete has an audit record without exposing reason to client.
- An auditor filters events by tenant, target or actor using a bounded API.
- A webhook delivery uses audit ids to connect external effect to original order.
- Retention archives old records without mutating their signed/hashed payload.

**Edge use cases.**

- Audit write fails while business transaction succeeds.
- A sensitive field diff contains card data, password hash or encryption key.
- An actor for a background job is absent or a human impersonates another user.
- Multiple updates inside one command create contradictory audit records.
- An administrator attempts to update/delete an audit record via CRUD endpoint.
- Tenant A queries an audit event about tenant B.
- Clock skew corrupts chronological ordering.

**Required invariants.**

- Audit intent commits atomically with the state it claims occurred, or the
  command fails according to explicit compliance policy.
- Record has immutable id, UTC occurrence time, actor kind/id, action, target,
  tenant scope, outcome, correlation/causation and schema version.
- Field allow-list governs diffs; raw model serialization is forbidden.
- Audit storage is append-only at framework API; retention/archive is a distinct
  administrative operation.
- Failed/denied actions are distinguishable from successful mutation.

**Minimum first slice.**

1. Dependency-free audit envelope and `Sink` contract.
2. Outbox-backed event sink with a sample SQL projector.
3. Explicit allow-list diff helper and service principal support.
4. No audit reader CRUD binding until row scope/project policy is integrated.

**Do not include yet.** Blockchain claims, tamper-proof storage guarantee without
storage support, recording every field automatically, browser UI or legal
retention defaults for every jurisdiction.

**Exit evidence.** Rollback produces no success audit record; committed mutation
and audit intent share a correlation id; denied attempt is present; sensitive
field control proves it never enters SQL/outbox/log payload.

### F-22 — Change history and temporal resource versions

**Priority:** B2 — useful for high-value records, not every table by default.

**What it adds.** An optional history decorator/projector that records logical
versions of selected resources and answers “what did the application show at a
given time/version?” separately from operational audit events.

**Why stdlib is insufficient.** Optimistic locking detects a conflict but does
not retain old representation, reconstruct change history, apply tenant scope,
diff versions or define restore semantics.

**Natural vv seam.** A repository/service decorator writes version records via
the same transaction/outbox. Read APIs for history should be separate from the
ordinary CRUD surface so defaults remain lean and security is explicit.

**Top-level declarative DX — illustrative.**

```go
orders = crud.Decorate(orderRepo,
    history.Record[Order](history.SQL(sqlHistory),
        history.Fields("Status", "ShippingAddress", "Version"),
        history.OnSave(),
        history.OnDelete(history.Tombstone()),
    ),
)
```

**Happy use cases.**

- Support compares order version 9 against version 8 with redacted fields.
- A dispute view renders the state at the time of a payment event.
- Soft delete records a tombstone transition without resurrection ambiguity.
- An audit event links to immutable history version id.
- A restore is an explicit new version, not mutation of the old record.

**Edge use cases.**

- A large JSON/blob field makes every version storage-prohibitive.
- A retention/erasure request conflicts with immutable history.
- Two concurrent writers create a gap or duplicate version number.
- A schema rename makes old serialized snapshots undecodable.
- A history read leaks PII removed from current projection policy.
- An automated worker makes thousands of no-op updates.
- Restoring old data reintroduces a state now invalid under current policy.

**Required invariants.**

- Version sequence/source is explicit and monotonic per aggregate.
- Snapshot, patch and event-sourced representations are separate choices;
  version one chooses one and documents reconstruction cost.
- History read applies current authorization/projection plus any legal retention
  policy; archival is not a security bypass.
- No-op updates do not create phantom versions unless explicitly requested.
- Restore is a new command subject to today's rules, not a raw database rewind.

**Minimum first slice.**

1. Append-only snapshot rows for one model, keyed by model id/version.
2. Existing optimistic version column integration and transaction test.
3. Read-only history service contract with no generic HTTP binding initially.
4. Storage-size and retention documentation.

**Do not include yet.** Event sourcing framework, automatic all-table history,
legal hold product, generic undo button or time-travel SQL across relations.

**Exit evidence.** Concurrent update test shows one consistent version sequence;
history respects tenant/field projection; a no-op update produces no record; a
restore command is denied when current policy would deny a fresh write.

### F-23 — Data retention, erasure and lifecycle jobs

**Priority:** A2 for regulated or multi-tenant products; a necessary companion
to outbox, audit, inbox, cache and history retention.

**What it adds.** Declarative lifecycle policy for selected records and durable
artifacts: retention deadline, legal hold extension, purge/anonymise action,
dependencies and a scheduled/observable executor.

**Why stdlib is insufficient.** A periodic SQL script does not discover every
copy in outbox, cache, inbox, history, webhook delivery, logs and object store;
it does not record why deletion was deferred or coordinate with foreign keys.

**Natural vv seam.** Metadata/lifecycle registry plus jobs worker. It should be
an application-declared policy, never a library default that deletes business
data.

**Top-level declarative DX — illustrative.**

```go
lifecycle.Register(Customer{},
    lifecycle.After("DeletedAt", 30*24*time.Hour),
    lifecycle.Anonymise("Email", "Phone", "Address"),
    lifecycle.PurgeRelated("sessions", "webhook_deliveries"),
    lifecycle.Hold("legal_hold"),
)
app.Workers(lifecycle.Run(sqlLifecycle, jobsRunner))
```

**Happy use cases.**

- Soft-deleted customer PII is anonymised after 30 days.
- A legal hold blocks purge and records the deferred reason.
- Outbox/webhook payload retention is shorter than order business retention.
- Cache invalidation occurs before an erased record can be served.
- A lifecycle job emits audit evidence of which policy/version acted.

**Edge use cases.**

- A foreign key blocks deletion halfway through a multi-table purge.
- An outbox event sent before erasure remains in broker retention.
- A restore arrives after anonymisation but before physical deletion.
- A tenant has a different retention rule or jurisdiction.
- Clock/calendar arithmetic across DST/month boundaries is misconstrued.
- A legal hold is removed while a purge job holds an old lease.
- A lifecycle worker retries a destructive step without idempotency.

**Required invariants.**

- Lifecycle action is idempotent, resumable and auditable.
- Destructive policies declare their target, scope, dependency order and hold
  predicate at startup.
- Erasure distinguishes application database deletion from broker/archive
  retention the application cannot retract.
- Purge never broadens tenant scope or bypasses authorization in an admin API.
- Retention uses UTC timestamp semantics; business calendar policies are named.

**Minimum first slice.**

1. Advisory scanner reporting candidates/dependencies without deleting.
2. One explicit anonymisation action for one model under transaction.
3. Jobs/outbox audit integration and dry-run/read-only mode.
4. No generic cascade across arbitrary schemas.

**Do not include yet.** A claim of GDPR compliance, automatic cross-SaaS erasure,
encryption key destruction, legal advice or default retention for user data.

**Exit evidence.** A live fixture holds a legal record, expires another and
forces one retryable purge failure; only the eligible record is anonymised,
cache invalidates, audit records outcome and repeated run makes no new change.

### F-24 — Feature flags through an OpenFeature bridge

**Priority:** B1 — valuable for safe rollout, but not a home-grown flag
management product.

**What it adds.** A tiny vv-facing feature gate/decision helper integrated with
principal/tenant context, test overrides, audit/telemetry hooks and the
OpenFeature provider ecosystem.

**Why stdlib is insufficient.** Environment booleans cannot target a tenant,
roll out gradually, observe evaluation, provide provider lifecycle or test a
change under both branches. OpenFeature already standardises provider, context,
hooks and lifecycle boundaries.

**Natural vv seam.** `port.Service`/worker decorator or explicit gate in an
application rule. Flags must not hide database schema compatibility or security
scope decisions; they gate a prepared safe capability.

**Top-level declarative DX — illustrative.**

```go
orders = port.Decorate(orders,
    flags.Gate(openFeatureClient,
        flags.Command(port.Create, "orders.new-checkout"),
        flags.Context(flags.Principal, flags.Tenant, flags.ServiceVersion),
        flags.WhenOff(flags.Deny(errs.CodeFeatureDisabled)),
    ),
)
```

**Happy use cases.**

- A new checkout path enables for one internal tenant before broad release.
- Tests force flag on/off without a real provider.
- Evaluation adds correlation/tenant context and emits bounded telemetry.
- A worker uses the same rollout rule as HTTP commands.
- Provider shutdown follows application lifecycle cleanly.

**Edge use cases.**

- Flag provider is unavailable mid-request.
- A default value makes a dangerous feature enabled by accident.
- Targeting context includes a raw email or token.
- A flag changes between an API read and a write inside one command.
- A database migration is removed while old flag-off code still executes.
- A flag key is deleted but callers silently use their default forever.
- Per-user flag keys become telemetry cardinality explosion.

**Required invariants.**

- Every flag declaration has owner, default, expiry/removal date and safe
  failure policy.
- Security/tenant scope cannot be turned off by a general product flag.
- One command evaluation is stable for its duration and recorded with provider
  revision when available.
- Context is whitelisted and redacted.
- Test provider overrides are scoped to test/context, never process-global
  mutable state.

**Minimum first slice.**

1. OpenFeature satellite adapter and dependency-free `flags.Evaluator` facade.
2. Command gate decorator with explicit fail/default policy.
3. In-memory deterministic test provider and expiry lint in codegen/docs.
4. No vendor SDK in root and no vv-hosted flag service.

**Do not include yet.** A UI, percentage algorithm, custom remote polling,
flags for authorization bypass, permanent unowned flags or automatic cleanup of
code branches.

**Exit evidence.** A test pins on/off/provider-error outcomes; request context
does not emit raw PII; an expired flag declaration fails CI; a provider
transition/shutdown does not race concurrent service commands.

---

## D. Runtime and operational contracts

### F-25 — Health, readiness and dependency diagnostics

**Priority:** A1 — a framework that owns optional workers must state whether it
is safe to receive traffic.

**What it adds.** Transport-neutral liveness, readiness and diagnostic checks
with bounded execution, dependency classification and an optional HTTP/gRPC
projection. It distinguishes “the process is alive” from “it can safely fulfil
its advertised contract”.

**Why stdlib is insufficient.** An application can write `/healthz`, but there
is no standard aggregation model, no timeout/failure classification, no safe
redaction policy and no uniform reflection of outbox backlog, consumer lease or
database readiness across all vv bindings.

**Natural vv seam.** `runtime.Checker` contract and a small binding adapter.
Checks are registered by application ownership: pool ping, outbox runner,
feature provider or broker client. vv does not probe every dependency secretly.

**Top-level declarative DX — illustrative.**

```go
health := runtime.NewHealth(
    runtime.Live(runtime.Process()),
    runtime.Ready(sqlDB.PingContext),
    runtime.Ready(outboxRunner.Ready),
    runtime.Degraded(natsClient.Status),
    runtime.Timeout(500*time.Millisecond),
)

crudnet.MountHealth(mux, health)
grpchealth.Register(server, health)
```

**Happy use cases.**

- Kubernetes liveness answers that the process event loop is alive.
- Readiness stays false until database and required outbox runner are usable.
- Optional analytics broker failure reports degraded but does not remove API
  traffic if product policy permits it.
- An operator endpoint returns stable check names/status/duration with secrets
  redacted.
- During graceful shutdown readiness becomes false before listener close.

**Edge use cases.**

- A health check blocks on a down remote service and causes probe pile-up.
- A database ping succeeds while migrations/schema are incompatible.
- One optional worker is deliberately disabled in a deployment role.
- Detailed errors reveal hostnames, credentials or tenant data publicly.
- A single process has multiple services with different readiness requirements.
- A transient degraded state flaps deployment traffic every second.
- A check panics or starts a goroutine that outlives shutdown.

**Required invariants.**

- Liveness never depends on remote network calls.
- Every readiness check has a deadline and named required/degraded policy.
- Public projection has only status; detailed diagnostics require an internal
  authenticated surface.
- Check execution is bounded/concurrent only where dependencies are independent.
- Disabled components are reported as disabled, not healthy by absence.

**Minimum first slice.**

1. Dependency-free `Check`/`Report`/`Status` types and fake clock tests.
2. `Live`, `Ready`, `Degraded` aggregation with per-check deadline.
3. HTTP/gRPC projection packages, no mandatory route mounting.
4. Adapters for SQL ping and manually supplied runner readiness.

**Do not include yet.** Auto-discovery of every client, a dashboard, Kubernetes
dependency, continuous background probing or detailed public error text.

**Exit evidence.** Tests show liveness remains green under database outage,
readiness becomes false before shutdown, optional dependency is degraded, timeout
does not leak goroutines and public body has no raw error string.

### F-26 — Application lifecycle and supervised runners

**Priority:** A1 — required once outbox/jobs/workers have lifecycle.

**What it adds.** A small supervisor that starts explicitly registered runners,
orders their shutdown, propagates cancellation, bounds drain time and reports
unexpected termination. It is not a service locator or dependency injection
container.

**Why stdlib is insufficient.** `signal.NotifyContext` and `errgroup` are
building blocks, but applications repeat subtle ordering: stop readiness, stop
intake, stop claims, drain handlers, flush telemetry, close connections. One
missed ordering causes accepted work to vanish or a process never exits.

**Natural vv seam.** `runtime.Runner` interface with `Start`, `Ready`, `Stop`.
All background systems opt in; no package launches its own forever goroutine in
an initializer.

**Top-level declarative DX — illustrative.**

```go
app := runtime.New(
    runtime.Listen(httpServer),
    runtime.Run(outboxRunner),
    runtime.Run(workerPool),
    runtime.Run(jobRunner),
    runtime.Health(health),
    runtime.DrainFor(20*time.Second),
)
return app.Run(signalContext)
```

**Happy use cases.**

- Process starts runners, waits for readiness and then accepts traffic.
- SIGTERM first removes readiness, then stops new queue claims, drains active
  commands and flushes telemetry before DB close.
- A runner failing unexpectedly surfaces a named error and triggers declared
  whole-process/restart policy.
- A web-only deployment omits workers without importing their adapters.
- Tests run one runner under a fake signal/context and assert lifecycle events.

**Edge use cases.**

- Outbox runner never drains because broker is down.
- HTTP server receives a request after worker/database has been stopped.
- Two runners depend on each other but registration order is accidental.
- One runner calls `os.Exit` or panics in a goroutine.
- A second SIGTERM should force exit after a documented deadline.
- Context cancellation occurs before start finishes.
- A background runner starts twice in an integration test.

**Required invariants.**

- Start order and stop order are declared, reversible and cycle-checked.
- Stop is idempotent, context-bounded and safe after failed start.
- Supervisor owns no global signal handler unless application calls `Run`.
- Readiness changes precede intake stop and resource close follows drain.
- Runner errors carry their stable registered name.

**Minimum first slice.**

1. `Runner` contract and deterministic supervisor state machine.
2. Manual `Start(ctx)`/`Stop(ctx)`; signal helper optional.
3. Outbox and worker examples wired through it.
4. No dependency injection, configuration container or auto-restart loop.

**Do not include yet.** Process manager, Kubernetes operator, fork/exec,
background daemon mode, service discovery or global `init` registration.

**Exit evidence.** A test records startup/shutdown order, fails a runner before
ready, forces a drain timeout and proves database close happens only after all
successful runners have stopped or been reported timed out.

### F-27 — Neutral operation observer and metrics vocabulary

**Priority:** A1 — define event names before coupling to a metrics vendor.

**What it adds.** A dependency-free observer interface and stable low-cardinality
event model for service command, repository statement, outbox publication,
worker delivery, cache, quota and resilience operation outcomes.

**Why stdlib is insufficient.** `log/slog` gives logs but not standard trace
spans, metric names, duration outcomes, cardinality policy, lifecycle events or
cross-module correlation. Each satellite otherwise invents incompatible labels.

**Natural vv seam.** Context-carried `runtime.Observer`, parallel to the caller
owned logger. Wrappers/decorators emit structured events; no root package
requires an exporter.

**Top-level declarative DX — illustrative.**

```go
ctx = runtime.WithObserver(ctx,
    runtime.Multi(observe, runtime.SampleErrors()),
)

orders = port.Decorate(orders,
    runtime.ObserveCommands("orders"),
)
outboxRunner = runtime.ObserveRunner(outboxRunner, "outbox.orders")
```

**Happy use cases.**

- A command event contains resource, action, outcome code, duration and trace
  correlation without model payload.
- An outbox publisher records pending/claimed/published/failed counts.
- A breaker transition and rate-limit reject are observable with bounded names.
- An application installs a test observer and asserts one event sequence.
- A no-observer path allocates minimally and has deterministic behaviour.

**Edge use cases.**

- An observer blocks or panics and breaks the business request.
- Labels include raw path, email, query filter or event id as metric cardinality.
- Telemetry emission recursively traces its own exporter HTTP call.
- Sampling hides the only event that a compliance audit requires.
- A context carries two observers with incompatible shutdown requirements.
- An event emits a raw SQL statement or webhook body.
- A fast path allocates per field even when no observer is present.

**Required invariants.**

- Observer failure is isolated and logged through caller's logger without
  recursion; it cannot alter command outcome.
- Event schema uses bounded enumerations for operation/outcome/component.
- Sensitive/variable values are forbidden at type/API level where possible.
- Audit is not sampling-dependent telemetry; the two interfaces stay separate.
- Events carry monotonic duration and optional trace/correlation id only.

**Minimum first slice.**

1. `Observer`, `Event`, `Start/End` outcome model and no-op implementation.
2. Command/outbox/worker events only, with an in-memory test recorder.
3. Cardinality/sensitivity guide in docs and a linter/test of allowed keys.
4. No metrics registry/exporter dependency.

**Do not include yet.** Prometheus format in root, logs-as-metrics, arbitrary
map labels, distributed tracing implementation or automatic request-body capture.

**Exit evidence.** A benchmark proves no-op observer overhead is bounded; a
test observer sees a command/outbox flow; invalid label keys are refused; a
panic observer cannot make the command fail or leak content.

### F-28 — OpenTelemetry instrumentation satellite

**Priority:** A2 — build after neutral observer vocabulary settles.

**What it adds.** An optional `runtime/otel` adapter converting vv operation
events into spans, metrics and selected logs. It supplements, rather than
duplicates, standard `net/http`, gRPC and database instrumentation.

**Why it matters.** OpenTelemetry Go has stable traces and metrics, and its
instrumentation libraries cover transport edges; vv is the layer that knows
business command, error kind, outbox delivery and security decision. Exporters
and SDK ownership remain application choices.

**Natural vv seam.** Implements `runtime.Observer`; binds trace context from
existing context. The satellite imports OTel, the root does not.

**Top-level declarative DX — illustrative.**

```go
shutdownOTel := otelx.Install(ctx, otelx.Config{ServiceName: "orders"})
defer shutdownOTel(context.Background())

ctx = runtime.WithObserver(ctx,
    otelx.Observer(otelx.Tracer("github.com/frostgrove/vv")),
)
```

**Happy use cases.**

- A request span contains a nested `vv.command.create` span and outbox child
  delivery trace linked through the event envelope.
- Metrics count command durations by resource/action/outcome class.
- A worker continues trace/correlation from broker headers after validation.
- An operator can connect a 409 fault class to cache miss/retry/breaker events.
- Application chooses OTLP/Prometheus/Jaeger exporter outside vv.

**Edge use cases.**

- Untrusted broker trace headers create huge/invalid trace context.
- A high-cardinality model id becomes a span attribute/metric label.
- Sampling drops a trace but metrics must still count outcome.
- Exporter outage causes queue worker backpressure.
- Log signal remains beta and changes API.
- Trace propagation carries tenant/user PII.
- OTel SDK shuts down before outbox runner flushes final spans.

**Required invariants.**

- Attributes follow a documented allow-list and never contain payload, SQL,
  headers, ids with unbounded cardinality or raw errors.
- Adapter validates/extracts remote trace context defensively.
- Instrumentation is best-effort and cannot block a command beyond bounded SDK
  configuration owned by the application.
- Metrics and traces can be enabled independently.
- OTel lifecycle is owned/injected by application, not singleton-initialised.

**Minimum first slice.**

1. Observer adapter for command, worker and outbox events.
2. Semantic-convention document and integration test with in-memory exporter.
3. HTTP/gRPC trace context uses established instrumentation rather than custom
   propagation when available.
4. No logs exporter requirement.

**Do not include yet.** Vendored OTel SDK, automatic exporter setup in a library,
profiling, tail-sampling service or claim of standard semantic conventions before
they are written/tested.

**Exit evidence.** In-memory exporter test asserts parent/child/linked spans;
attribute control catches PII/id; exporter shutdown cannot prevent graceful
worker drain; root module dependency graph remains unchanged.

### F-29 — Runtime configuration snapshots and secret references

**Priority:** B1 — `vvcfg` handles startup configuration; long-running systems
need a carefully bounded answer to rotations and dynamic policy.

**What it adds.** Immutable, validated configuration snapshots and an optional
secret-reference resolver contract for components that can safely reload:
webhook signing secrets, feature provider endpoint, rate-plan table or broker
credentials with client-specific reconnect policy.

**Why stdlib is insufficient.** Environment variables/files can be read again,
but do not validate an atomic multi-field update, version a snapshot, notify
components, bound reload frequency or distinguish a secret reference from the
secret's accidental appearance in logs.

**Natural vv seam.** Extend `utils/vvcfg` as application plumbing, not root
framework core. Each subsystem explicitly opts into a snapshot interface; a
database DSN or SQL schema does not hot-reload by accident.

**Top-level declarative DX — illustrative.**

```go
cfg, watch := vvcfg.Watch[Config]("config.yaml",
    vvcfg.Validate(),
    vvcfg.Debounce(time.Second),
)

hooks.ReloadFrom(watch, webhooks.Secrets(secretResolver))
limits.ReloadFrom(watch, ratelimit.Rules(configuredPlans))
```

**Happy use cases.**

- Webhook secret rotation adds a new key version without restarting delivery.
- Feature/rate limit policy reloads atomically after validation.
- A runner reports current config revision in diagnostics/audit.
- Invalid config leaves last known-good snapshot active and logs a safe error.
- Tests inject a snapshot source without touching files/environment.

**Edge use cases.**

- Half-written config file triggers a transient parse error.
- Reload changes endpoint and credential inconsistently.
- A secret resolver returns plaintext that reaches logs/errors.
- A database pool setting changes while calls are active.
- Rapid file changes cause reload storm.
- One component accepts a snapshot another rejects.
- Config rollback reuses an old/revoked signing secret.

**Required invariants.**

- A published snapshot is complete, validated and versioned; readers see old or
  new, never a mixture.
- Reload is opt-in per component and has explicit accept/reject/rollback report.
- Secret values implement redaction-safe formatting and are never default
  serialisable.
- Last-known-good and fail-closed behaviour are declared per setting.
- Watcher lifecycle is supervised and stoppable.

**Minimum first slice.**

1. Immutable generic snapshot source and test implementation.
2. File watcher/debounce only in optional `vvcfg` module.
3. One safe consumer: webhook secret versions.
4. No dynamic database reconfiguration or arbitrary config command endpoint.

**Do not include yet.** A remote configuration SaaS, generic secret vault,
automatic environment polling, global mutable config or runtime schema changes.

**Exit evidence.** A test alternates valid/invalid snapshots, proves consumers
observe atomic revisions, verifies secret redaction and shows watcher stops
during application shutdown.

### F-30 — Schema compatibility and migration guard

**Priority:** A1 — vv already reflects models and catalogs database metadata;
the missing value is an actionable deployment check.

**What it adds.** An opt-in validator/CLI that compares the model's declared
columns, nullability, key/version fields and the constraints required by fault
probe/security semantics against the live catalog before traffic takes a bad
deployment.

**Why stdlib is insufficient.** Go compilation validates types, not a live
PostgreSQL/MySQL/MariaDB/SQLite schema. Migration tools know migrations but not
vv's DTO/soft-delete/version/fault-probe contract.

**Natural vv seam.** `crud/catalog` and `sqlrepo` metadata produce a read-only
report. `runtime.Ready` can consume report policy, but vv never owns migration
execution.

**Top-level declarative DX — illustrative.**

```go
report := schema.Check(ctx, db,
    schema.Repository(Orders),
    schema.Require(schema.Columns, schema.Nullability, schema.PrimaryKey),
    schema.Warn(schema.ExtraColumns),
    schema.RequireProbeConstraints(),
)
if err := report.RequireClean(); err != nil { return err }
```

**Happy use cases.**

- CI detects a missing unique constraint required to explain a form failure.
- Startup readiness refuses a model column absent from the migrated schema.
- A staged migration marks an upcoming nullable column warning before it becomes
required in the application release.
- An application exports a machine-readable report in deployment logs/artifact.
- Four supported engines report equivalent high-level discrepancy codes.

**Edge use cases.**

- A zero-downtime expand/contract migration intentionally has temporary extra
  column/nullability states.
- A database account lacks catalog permissions.
- A replica schema lags primary.
- Constraint names differ across environments.
- An unsupported engine reports incomplete metadata.
- A destructive “fix” action is mistakenly assumed from validation.
- Catalog cache hides a migration run after application start.

**Required invariants.**

- Checker is read-only and never applies DDL.
- Finding severity is declared: error, warning, ignored-with-reason.
- A compatibility profile supports staged deployment rather than one rigid
  equality rule.
- Output uses stable codes and redacts credential/database-specific details.
- Cache reload/connection target is explicit.

**Minimum first slice.**

1. PostgreSQL model-to-table report for columns, PK, nullability and version.
2. CLI usable in CI and `runtime.Ready` adapter.
3. Fixture for intentional expand/contract warning/control.
4. No migration generator or auto-fix.

**Do not include yet.** Taking ownership of Flyway/Atlas/goose, guessing DDL,
requiring every schema feature at startup or blocking a deliberate canary
deployment without an override record.

**Exit evidence.** Integration fixtures prove missing/mismatched/extra/staged
findings; report remains stable across catalog cache reload; readiness policy
blocks only error severity and no statement mutates schema.

---

## E. Contracts, evolution and developer confidence

### F-31 — Generated OpenAPI and resource manifest

**Priority:** A2 — consumers should not reverse-engineer a generated CRUD API.

**What it adds.** Optional CLI output of a machine-readable resource contract:
paths/commands, model and update shape, query grammar/limits, fault envelope,
auth requirements, idempotency/ETag rules and event/webhook types. OpenAPI is
the HTTP projection; a smaller vv manifest retains gRPC/worker facts.

**Why stdlib is insufficient.** `net/http` gives no route/schema description.
Hand-maintained OpenAPI drifts from tags, generated DTOs, field projections,
error codes and framework binding conventions exactly when a consumer needs it
for frontend client generation or contract tests.

**Natural vv seam.** `cmd/vv` reads compiled/generated model metadata and
explicit binding/service declarations. It emits an artifact; the runtime does
not become a reflection-based documentation server. `port` owns transport-neutral
operation facts; `porthttp` owns HTTP projection.

**Top-level declarative DX — illustrative.**

```go
//go:generate vv contract -resource Order -http /orders -grpc OrderService \
//    -out api/openapi.yaml -manifest api/vv-manifest.json

orders := crudnet.New(orderService,
    crudnet.Rules(port.Rules{Query: orderQuery}),
    crudnet.Conditional(cache.ETagFromVersion("Version")),
)
```

**Happy use cases.**

- A frontend obtains create/update schemas and standard fault response shapes.
- A generated client knows the query endpoint supports only an allow-listed
filter/sort/preload set and stated limits.
- A webhook subscriber receives event type/version/payload documentation.
- A contract test compares mounted routes with generated OpenAPI paths.
- Public field projection is represented separately from the persistence model.

**Edge use cases.**

- A route mounts a hand-written service whose command semantics are not default.
- Gin/Fiber/net/http differ in a transport-specific binding detail.
- gRPC `Struct` representation loses field precision compared with a Go type.
- A security rule makes fields/principal-dependent responses dynamic.
- Generated document claims an error status that a renderer cannot produce.
- A breaking event/schema change reuses the old name/version.
- Description text accidentally includes internal constraint/table names.

**Required invariants.**

- Generated artifact derives from compiled model/service metadata, not merely the
  generator's source parse, mirroring the existing codegen drift discipline.
- Unknown/hand-written semantics are explicitly marked extension/undocumented,
  never invented.
- HTTP and gRPC differences have separate projection tests.
- Query costs/limits and body caps are part of the contract, not prose footnote.
- Contract generation has no satellite framework dependency unless caller asks
  for that output module.

**Minimum first slice.**

1. vv resource manifest for one default service and its model/update DTO.
2. OpenAPI 3.1 document for `net/http` CRUD paths, fault envelope and query
   endpoint.
3. Generated-file drift test plus mounted-route comparison.
4. One frontend/client example consuming the artifact.

**Do not include yet.** Runtime Swagger UI, guessing business-rule schemas,
automatic SDK generator, complete JSON-Schema expression of every query AST or
pretending public projection is static when it is policy-dependent.

**Exit evidence.** Generated contract changes when a compiled writable model
field changes; a tampered artifact is caught; every mounted default route has a
matching operation; a denied hidden field is absent from public schema.

### F-32 — Wire/API evolution and compatibility gates

**Priority:** A1 before first public tag; Go exported surface alone is not the
whole consumer contract.

**What it adds.** Versioned golden contracts for HTTP envelopes, gRPC details,
event envelopes, webhook signatures and generated manifests, plus explicit
deprecation/version negotiation policy.

**Why stdlib is insufficient.** Semver does not understand JSON field removal,
default changes, query grammar meaning, event payload version, an HTTP status
change or a gRPC detail field. The existing `make api` baseline is necessary but
cannot see any of them.

**Natural vv seam.** `docs/api` gains separate wire baselines; `errs`, `porthttp`,
`crudgrpc`, `events` and contract CLI own their projections. Tests run from an
external consumer module after tagging/candidate release.

**Top-level declarative DX — illustrative.**

```go
// api/compat.yaml
wire:
  http: v1
  grpc: v1
events:
  order.created: v1
deprecations:
  - field: Order.legacy_status
    remove_after: v2
```

```bash
make api
make contract
make compat
```

**Happy use cases.**

- Adding an optional response field is classified as compatible.
- Removing a JSON error code or changing status fails the compatibility gate.
- An event producer emits v1 and v2 deliberately during migration.
- A consumer example builds against every satellite at the candidate version.
- Release notes enumerate contract changes from the reviewed diff.

**Edge use cases.**

- A field's JSON name changes while Go name remains the same.
- `omitempty`/null/absent semantics change under a PATCH update.
- Query `limit=0` or an error default alters meaning without API signature move.
- An old client sends an idempotency key/header unknown to new server.
- An event payload uses a reused type name with incompatible bytes.
- Two satellite versions are selected by MVS in a dangerous mismatch.
- A generated contract baseline updates automatically and hides breaking change.

**Required invariants.**

- Compatibility policy names HTTP, gRPC, query, errors, events and generated
  artifacts separately.
- Baseline generation produces human-readable diff and requires human decision.
- Deprecation carries replacement, first-deprecated version and removal floor.
- Event type plus schema version is immutable once externally published.
- Root/satellite lockstep/retract policy is documented before tag.

**Minimum first slice.**

1. Golden HTTP/gRPC error envelopes and query acceptance/rejection corpus.
2. Event envelope fixture comparer once F-01 exists.
3. CI job that reports, not blindly accepts, meaningful contract diffs.
4. External consumer compilation matrix in release rehearsal.

**Do not include yet.** Automatic version negotiation protocol, multiple API
versions forever, opaque “compatibility score”, breaking-change suppression
labels or contract compatibility guessed from Go types alone.

**Exit evidence.** Controls intentionally remove a JSON field, alter a status,
change an event version and shift null semantics; all fail `make compat`, while
a documented optional addition produces an explainable reviewed diff.

### F-33 — Public `porttest` service and transport test seam

**Priority:** A1 — applications need to test their business service and routes
without binding a real database or copying vv internal fakes.

**What it adds.** A public package of command-service doubles, scripted
responses, captured calls, deterministic fault helpers and assertions that a
transport mapped input/path/status correctly and did not execute prohibited
work.

**Why stdlib is insufficient.** Go interfaces make fakes possible but provide
no ergonomic generic recorder, command matching, typed response queue, context
capture or control for “no call happened”. Existing repository recorder is
statement-oriented, not handler/service-oriented.

**Natural vv seam.** `port.Service[M, ID, U]` is already the narrow transport
contract. `porttest` implements it; bindings remain tested through real HTTP/
gRPC but application can inject a scripted service.

**Top-level declarative DX — illustrative.**

```go
svc := porttest.New[Order, int64, OrderUpdate](t,
    porttest.OnCreate(func(cmd port.CreateCommand[Order]) (Order, error) {
        return Order{ID: 7, Status: "draft"}, nil
    }),
)

crudnet.New(svc).Mount(mux, "/orders")
// assert HTTP 201, then svc.AssertCalled(porttest.CreateOnce())
```

**Happy use cases.**

- A handler test asserts body mapped to model and server id was cleared.
- A service error becomes correct HTTP/gRPC wire fault through actual binding.
- A query document compiles to the expected `ListCommand` options.
- An unauthenticated/invalid body path asserts the service saw zero calls.
- A test scripts list/get/create/update results without SQL queue bookkeeping.

**Edge use cases.**

- A scripted fake accepts any command and lets a changed route pass vacuously.
- A response queue has an unused expected call at test end.
- Generic `ID`/DTO types are mismatched by a runtime type assertion.
- Context principal/logger/locale is lost before reaching service.
- Concurrent requests make call order non-deterministic.
- A fake cannot implement `Meta`/`Paths`, so binding cannot mount.
- A test checks only status and misses a forbidden repository/service action.

**Required invariants.**

- Strict mode fails on unexpected call and unconsumed expectation through
  `t.Cleanup`.
- Captured command is immutable/deep-copied where pointer/slice mutation would
  make post-call assertions lie.
- Fake provides deliberate `Meta`/`Paths` construction, not zero reflection.
- Calls carry context facts needed for application assertions without exposing
  context's entire value bag.
- Concurrency/order assertion is opt-in and names its synchronization policy.

**Minimum first slice.**

1. Scripted create/get/list service fake and `AssertNoCalls`.
2. Strict expectation mode and command comparator helpers.
3. net/http and gRPC example tests owned by external-package fixtures.
4. No mocking framework dependency or generated mocks.

**Do not include yet.** Simulating SQL, broker, cache or full application
container; a fake that silently auto-generates models; process-global test
registry; reflection-only matchers.

**Exit evidence.** A control deletes a handler's service call and the test fails;
another sends invalid input and proves zero calls; parallel tests do not leak
expectations/context across instances.

### F-34 — Deterministic clock, randomness and failure-injection kit

**Priority:** A1 — every durable/retry/lease/scheduler capability otherwise
creates flaky tests and untestable edge semantics.

**What it adds.** Minimal dependency-free interfaces/test helpers for clock,
timer/sleep, randomness/jitter, id generation and controlled fault points. It
is infrastructure for framework tests and consumers implementing workers.

**Why stdlib is insufficient.** `time.Now`, `time.Sleep`, `math/rand` and real
goroutine scheduling create tests that either sleep in production time or fail
only under CI timing. Build tags and globals make parallel tests race.

**Natural vv seam.** Individual components receive interfaces/options; no
global “test mode”. Existing `NowFunc` patterns can be migrated deliberately
without silently changing public behaviour.

**Top-level declarative DX — illustrative.**

```go
clock := runtimetest.NewClock(t, start)
runner := outbox.NewRunner(store, publisher,
    outbox.Clock(clock),
    outbox.Random(runtimetest.Sequence(0.0, 0.5, 1.0)),
)

clock.Advance(30 * time.Second)
runner.RunOnce(ctx)
```

**Happy use cases.**

- A retry test advances through exponential backoff instantly.
- A scheduler test runs a week of due jobs without sleeping.
- A lease-expiry race is reproduced at one exact timestamp.
- Event ids/random jitter are deterministic in golden contract tests.
- A fault injector kills runner between claim/publish/complete transitions.

**Edge use cases.**

- A fake clock moves while a goroutine is waiting and misses wakeup.
- Two components accidentally use different clocks in one test.
- Deterministic random sequence runs out and silently repeats.
- Production code accepts a nil clock and falls back unexpectedly.
- A failure injection point survives into production configuration.
- Real database transaction timing still races even with fake time.
- Test parallelism mutates a package global clock.

**Required invariants.**

- Time/randomness dependencies are explicit constructor options with safe
  production default and test-only fake packages.
- Fake clock wakeups are deterministic and context cancellation aware.
- Failure injection is compiled/test-scoped or capability-injected, never a
  magic environment variable on a production path.
- No package-level mutable clock is introduced.
- Tests distinguish deterministic model check from real-driver concurrency test.

**Minimum first slice.**

1. `Clock`/`Timer` interface, manual fake clock and sequence random source.
2. Apply to outbox claims, retry and jobs only.
3. Failure-injecting publisher/store decorators for tests.
4. Guide for consumer worker tests.

**Do not include yet.** A fake Go scheduler, replacing `context` deadlines,
monkey patching time globally, deterministically simulating a database engine or
shipping fault injection endpoints.

**Exit evidence.** Retry/job/outbox suites contain no real sleep; fake-clock
advance wakes exactly expected waiters; race-enabled live integration still runs
separately; fault controls prove every crash window in F-02/F-03.

### F-35 — Declarative test fixtures and data factories

**Priority:** B2 — helps consumer adoption once strict seams exist, but should
not become an ORM fixture framework.

**What it adds.** Typed builders for model/update/event/command fixtures,
relationship-aware defaults, deterministic ids/timestamps and a small scenario
DSL for readable happy/edge tests across service, repository and worker layers.

**Why stdlib is insufficient.** Table tests are excellent but every application
repeats pointer/`Opt` helpers, valid baseline models, relationship wiring,
event envelopes and mutation copies. Hand-written fixtures commonly hide an
invalid default that makes every test pass for the wrong reason.

**Natural vv seam.** `crudtest`/`porttest` companion module. Builders are
application-owned generic values, not an introspective database seeder.

**Top-level declarative DX — illustrative.**

```go
order := fixture.Build(Order{Status: "draft"},
    fixture.Set("CustomerID", customer.ID),
    fixture.Set("Amount", money.New(1999, "KZT")),
)

given := scenario.New(t,
    scenario.Principal(manager),
    scenario.Event(OrderCreated{ID: order.ID}),
)
given.When(worker.Handle).Then(scenario.Outbox("invoice.requested"))
```

**Happy use cases.**

- A valid baseline order is explicit and changing a required field breaks
  fixture compilation/test setup.
- A PATCH fixture distinguishes absent, null and set values clearly.
- A scenario combines principal, clock, service fake and one event envelope.
- Fixtures use deterministic ids/times suitable for golden assertions.
- Relationship setup is readable without a hidden global database.

**Edge use cases.**

- Fixture defaults accidentally include an authority/tenant the test should set.
- Builder shares slices/maps between cases and one mutation leaks to another.
- Reflection setter silently accepts misspelled field name.
- A fixture's “valid” state violates a new database constraint.
- Factories create real database rows and tests become order-dependent.
- Random data makes an error payload/golden test flaky.
- A scenario hides critical setup behind too much DSL.

**Required invariants.**

- Builder defaults are minimal, named and clone mutable values.
- Field names resolve/validate at setup; typed constructor helpers are preferred.
- Database persistence is a separately explicit test step.
- Scenarios optimise readability only; they expose resulting calls/events for
  direct assertion.
- No generated production code depends on test fixture packages.

**Minimum first slice.**

1. Generic clone/set helpers for structs and `crud.Opt`/pointer examples.
2. Event/command fixture builders plus deterministic id/clock integration.
3. Documentation of when a simple literal is clearer than a builder.
4. No database seeding or factory global registry.

**Do not include yet.** A Faker dependency in root, auto-persisting fixture,
random default data, an assertion DSL that hides errors or a test-only ORM.

**Exit evidence.** A mutation-isolation control catches shared slice/map; an
unknown field is rejected; PATCH null/absent fixture tests match real service
semantics; external consumer test uses only public fixture package.

---

## Cross-cutting delivery order for the capability catalogue

The cards are intentionally not a flat backlog. Several become dangerous when
implemented before their foundations. This is the recommended sequence after the
trust blockers at the top of this roadmap:

| Slice | Build together | Why these belong together | Explicitly postpone |
|---|---|---|---|
| S1 — durable intent | F-01 event kernel, F-02 outbox, F-34 deterministic time/failure kit | Every later async effect needs an immutable envelope, atomic intent and crash tests | broker adapter, cron, workflow, webhooks |
| S2 — safe consumption | F-03 inbox, F-04 worker contract, F-10 retry, F-11 budgets, F-13 bulkhead | At-least-once delivery is only usable when duplicate, timeout and process capacity semantics are explicit | “exactly once”, distributed worker coordination |
| S3 — first real integration | F-05 NATS JetStream, F-06 one-shot jobs, F-08 webhooks, F-25 health, F-26 lifecycle | One broker and one durable timer prove the contracts against process restart and deployment shutdown | Kafka/RabbitMQ/SQS, cron, workflow runtime |
| S4 — public command safety | F-09 idempotency, F-14 quota, F-17 validation, F-20 field projection, F-21 audit | These make a generated public API safe and explainable rather than merely convenient | generic rule DSL, audit UI, global plan service |
| S5 — operations | F-27 observer, F-28 OTel, F-29 controlled reload, F-30 schema guard | Observability/lifecycle have real components to observe and protect | mandatory exporters, control plane |
| S6 — consumer contract | F-31 contract generation, F-32 compatibility, F-33 porttest, F-35 fixtures | Users can depend on and verify the framework without internal fakes | automatic SDKs, a new transport |
| S7 — advanced policy | F-18 command rules, F-19 external auth, F-22 history, F-23 lifecycle/erasure, F-24 flags | These require the earlier audit/outbox/lifecycle boundaries to be trustworthy | a central policy/flag/SaaS product |
| S8 — only on demand | F-07 workflow bridge, cache F-15/F-16, second broker, more engines | These are valuable but should answer a real consumer/deployment need | home-grown workflow engine, generic broker facade |

## The decorator matrix

vv must keep decorator placement obvious. A capability that lives at the wrong
layer produces a feature that works in one transport and leaks through another.

| Capability | `crud.Core` | `port.Service` | worker | binding | reason |
|---|---:|---:|---:|---:|---|
| soft delete/history | primary | optional command semantics | no | no | it observes persistence and must join transaction |
| tenant/row security | primary | action context | inherited | no | filters every query/write path |
| validation | no | primary | command adapter | decode only | validates command intent after sanitize |
| command business rule | no | primary | same handler rule | no | one rule across HTTP/gRPC/queue |
| outbox/event emission | selected data events | primary semantic events | consumer effects | no | command knows business fact; repository joins executor |
| idempotency | no | primary | inbox analogue | extracts key | command outcome belongs above persistence |
| cache | carefully public lookup | primary representation cache | no | conditional HTTP projection | scope/projection makes below-service cache risky |
| quota/rate limit | no | primary | worker variant | identity/header projection | protects work before it reaches SQL |
| audit | optional persistence helper | primary | worker action | no | business action/outcome belongs at service |
| field redaction | no | presenter/service output | worker payload mapping | renderer | stored model is not public representation |
| retry/breaker/bulkhead | no | selected remote service | primary | no | protects external operations, not arbitrary SQL mutation |
| tracing/observer | selected statements | primary command | primary delivery | incoming span | each layer adds only facts it owns |

## Rules for adding a new decorator

Before a package is created, answer these review questions in its use case and
decision:

1. Which exact seam sees every operation it must protect?
2. Does it observe or alter data? If it alters, can a caller opt out by using a
   lower-level public API?
3. Does it need one transaction with its wrapped operation? If yes, how does it
   join a caller-owned `database/sql`, pgx, ent or gorm transaction?
4. Which context facts are trusted and which must be decoded/validated first?
5. What is the behaviour on process crash between every external effect?
6. What is the retry/idempotency unit and its storage retention?
7. Does it add a Go module dependency? If so, is that one consumer choice and
   therefore one satellite module under [[D-051]]?
8. Can a wrapper preserve optional executor/source capabilities according to
   [[D-061]]?
9. How is failure represented on HTTP, gRPC, queue and local Go call?
10. Which sensitive/high-cardinality fields must never enter logs, traces,
    errors, cache key, audit, event or metrics?
11. Which fake clock/store/transport control makes the unhappy path deterministic?
12. What test would pass if the decorator were deleted, and what control makes
    that vacuity impossible?

## Deliberate non-goals

The following sound framework-like but should not enter vv without a radically
different product decision:

- **A dependency-injection container.** Go constructors and interfaces already
  solve this; global registration would fight vv's explicit dependency ownership.
- **An ORM or migration engine.** vv interoperates with existing ones and must
  not take over model ownership/database lifecycle.
- **A generic message-broker facade that promises all brokers are alike.** Start
  from correct contracts and adapters with exposed semantics.
- **A home-grown workflow/saga engine.** Bridge to Temporal or an equivalent
  durable engine; do not imitate event history/replay superficially.
- **A hosted flag, policy, metrics or secret-control plane.** Provide adapters
  and contracts; do not turn a Go library into SaaS infrastructure.
- **A universal cache.** Cache correctness depends on scope, representation,
  invalidation and retention; unspecified cache is a disclosure bug.
- **Automatic retry of commands/transactions.** A retryable database error is
  not permission to replay a caller-visible external effect.
- **Annotations/reflection that silently change every method.** vv should stay
  explicit at wiring sites, type checked and inspectable in code review.
- **A new HTTP framework adapter before `net/http` has a concrete limitation.**
  Chi/Echo/gorilla can mount normal handlers today; an adapter is not a feature.
- **“Exactly once” as a product claim.** vv can deduplicate one consumer effect
  and persist an outbox intent; broker/remote side effects remain at-least-once.

## Completion gate for a capability card

A card must not graduate from design to public package merely because a happy
path demo works. It needs all of the following:

- a numbered use case with public guarantees and explicit out-of-scope section;
- a decision for semantics that would be expensive to change after release;
- flows documenting source locations and failure transitions;
- root/satellite dependency review and a standalone consumer module build;
- deterministic unit tests for every state transition and a control for each
  security/retry/idempotency positive case;
- live integration test against each support matrix component it names;
- restart/crash/lease/timeout/shutdown test where persistent or concurrent;
- public error/metric/audit/redaction contract reviewed for sensitive data;
- retention/migration/upgrade/deprecation story;
- one runnable production-shaped example and one minimal test example;
- `make api`, wire compatibility baseline and dependency snapshot reviewed;
- removal from the live open-work inventory only after all evidence exists.

## First twelve issues worth opening after the existing trust blockers

This shortlist intentionally contains implementable discovery/design issues, not
twelve feature implementations. It keeps the next work concrete without
pretending all cards are committed.

1. Write an ADR/use case defining vv domain event envelope ownership, metadata,
   event type/version and local dispatch modes (F-01).
2. Prototype PostgreSQL outbox claim schema/state machine and fault-injection
   test matrix; do not attach it to CRUD yet (F-02, F-34).
3. Design inbox identity/retention and prove concurrent duplicate claim with a
   real transaction (F-03).
4. Specify worker `Delivery`, acknowledgement dispositions and shutdown state;
   build in-memory harness first (F-04).
5. Decide mass-write paging semantics and soft-delete restoration from Horizon 0
   before any event/audit decorator observes an unsafe write.
6. Add `porttest` strict fake because every subsequent port decorator needs an
   external-package test seam (F-33).
7. Define neutral observer event schema/cardinality/redaction review, before
   adding OpenTelemetry (F-27).
8. Build public-command idempotency design around scope/fingerprint/outcome
   replay; test it with the eventual outbox transaction (F-09).
9. Establish time/clock abstraction in new packages only and replace no global
   time state opportunistically (F-34).
10. Design validation rule/`errs` aggregation without duplicating database
    constraints or erasing PATCH presence (F-17).
11. Specify field projection threat model and `Select`/cache/OpenAPI interaction
    before exposing PII control as a feature (F-20).
12. Write the release compatibility policy plus initial HTTP/gRPC wire golden
    corpus before the first tag makes accidental defaults permanent (F-32).
