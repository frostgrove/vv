# PostgreSQL event sourcing roadmap — 2026-09-01

**Status:** current proposal; not an implementation commitment. Activation still
requires the gates in this document and the live product roadmap.

**Supersedes:** package topology, integration examples and delivery gates in the
[2026-08-26 snapshot](2026-08-26-1558-postgres-event-sourcing-roadmap.md). That
snapshot remains research input for PostgreSQL failure cases, event evolution and
operational rehearsal.

**Architecture:** this revision follows the
[optional extension architecture](2026-09-01-extension-architecture-roadmap.md).
In particular, it creates one independently selectable PostgreSQL event-source
extension and no packages for its intersections with tenancy, audit, brokers,
storage, i18n or OpenTelemetry.

## Current baseline

The current tree is the starting point, not the proposed API in the historical
roadmap:

| Area | Current state | Consequence |
|---|---|---|
| Event sourcing | No `event`, `eventpg` or event-store package/module exists | Every name below is provisional until E0 accepts it |
| Root dependency graph | The root module has no third-party requirement | PostgreSQL dependencies must live in the one optional event extension |
| CRUD composition | `crud.Middleware`, `crud.Chain`, `crud.Base.Next` and typed optional effects exist; `ExistsUnscopedOf` currently walks to an inner executable effect | Reuse the chain for read models only after any used effect is exact-outer/explicitly forwarded or fail closed; do not create an event-specific CRUD chain |
| Service composition | `port.Service` and restore discovery exist; a service middleware/chain is planned but not implemented | No public event service decorator precedes acceptance of the base-owned chain |
| Storage composition | `storage.Store`, `storage.Backend` and `Capabilities` exist; a Store middleware/chain is planned but not implemented | Event code may use root storage vocabulary only after an actual use case; it never imports a storage satellite |
| Event operation seam | No second implementation or accepted dependency-neutral event contract exists | Do not add a root `event` contract, generic `EventStore` or event middleware chain in the first slice |
| OpenTelemetry | No OTel module exists; the [current OTel roadmap](2026-08-31-opentelemetry-roadmap.md) defers event-specific work | First event release has no `eventotel` package and no OTel dependency |

Modified or untracked production work elsewhere in the repository is concurrent
work, not evidence that any API in this proposal exists.

## Decision in one page

1. The first event-source choice is one optional module, working name
   `github.com/frostgrove/vv/eventpg`, for PostgreSQL event sourcing.
2. That module owns the aggregate/event vocabulary, PostgreSQL append/load
   implementation, schema/version registry and any transaction-local projection,
   snapshot or outbox implementation accepted by a milestone.
3. It does not promise database portability. An in-memory model may support pure
   tests but does not certify concurrency, commit, crash or recovery semantics.
4. The first public surface is a direct typed `eventpg` API. A dependency-neutral
   root event seam is added only when D-048's second-implementation rule is met
   and a separate ADR accepts its method and capability contract.
5. One event module may later return ordinary middleware or factories for several
   accepted base seams. It does not create one module per seam.
6. `eventpg` imports dependency-light root packages and its selected PostgreSQL
   ecosystem only. It never imports tenancy, audit, `vvotel`, a broker adapter,
   `storageminio`, a router/container binding or another optional extension.
7. The application composition root establishes tenant/policy context, orders
   base middleware, supplies a datasource/transaction and maps event results to
   audit or integration contracts. No pairwise package performs that wiring.
8. A transaction-local outbox record may belong to `eventpg`. Broker delivery is
   a separately selected application or broker-extension concern connected by a
   narrow sender contract; no `eventkafka`, `eventnats` or broker × OTel package
   is created.
9. Event and audit history remain independent truths. They may share a caller-owned
   PostgreSQL transaction only when the application proves the same transaction
   authority; otherwise neither module claims atomicity.
10. Generic service and driver telemetry may surround event work. Event-specific
    signals remain deferred until a neutral seam exists; `eventotel` is forbidden.
11. Constructors start no worker, publisher, projector or global provider. The
    host owns lifecycle, cancellation, shutdown and credentials.
12. Every optional executable effect is exact-outer or explicitly preserved and
    fail-closed. No wrapper tunnels through an unknown layer.

## Module and dependency boundary

```text
application composition root
  |
  +-- constructs eventpg directly
  +-- applies accepted port/crud/storage middleware
  +-- supplies verified context, datasource and transaction authority
  +-- projects bounded results to audit/broker/application code
  |
  +--> root dependency-light seams
  +--> eventpg module --------> PostgreSQL ecosystem
  +--> tenancy module --------> root seams
  +--> audit module ----------> root seams
  +--> vvotel module ---------> root seams + OTel API
  +--> broker extension ------> its neutral sender/worker seam

eventpg --------X--------> tenancy / audit / vvotel / broker / storage satellites
other extension -X-------> eventpg
root ------------X-------> eventpg
```

The one-module decision includes files for aggregate, codec, PostgreSQL store,
projection and outbox concerns when they are accepted. A subpackage or nested
module is not created merely because another file adapts a base seam. A separate
adapter module is justified only by a new independently selected third-party
ecosystem, and it targets an owning neutral contract rather than importing
`eventpg` to form a pair.

The following package names are explicitly forbidden:

```text
tenancyevent   auditevent   eventaudit   eventotel   eventstorage
eventkafka     eventnats    eventpgotel  eventpgaudit
```

An unpublished conformance fixture may import several extensions to prove their
application composition. No production module acquires that privilege.

## Scope and vocabulary

The initial feature is a trustworthy append-only aggregate history, not a generic
event bus or alternate CRUD mode.

| Term | Meaning |
|---|---|
| aggregate | one domain consistency boundary that decides events from state and a command |
| stream | ordered immutable events for one aggregate identity and type |
| stream version | optimistic concurrency/order number, separate from payload revision |
| event type | stable declared machine identifier such as `account.credited` |
| event revision | version of one event type's payload meaning |
| global position | PostgreSQL store order used by bounded consumers/checkpoints |
| upcaster | deterministic old-revision to current-reader transformation |
| projection | idempotent consumer with explicit checkpoint semantics |
| snapshot | disposable replay acceleration, never history authority |
| outbox record | durable post-commit delivery intent, not broker acknowledgement |

## Non-negotiable event invariants

- Stored event envelopes and payloads are append-only. Corrections are new facts,
  not updates to retained history.
- Every aggregate append names the exact observed stream version. A blind append
  is not a convenience overload.
- Stream advancement, event rows and any accepted transaction-local outbox or
  synchronous projection work commit or roll back in one PostgreSQL transaction.
- Rehydration and upcasting perform no I/O, telemetry, clock, randomness, broker
  send or application side effect.
- Event type and revision are declared wire identifiers, not Go type names,
  translated labels, tenant-derived strings or reflection output.
- Unknown, malformed or unsupported revisions fail before returning a partially
  rehydrated aggregate.
- A conflict requires a fresh load and a new domain decision. No framework layer
  retries a stale proposed event list.
- Snapshots are disposable and versioned independently. Full replay remains the
  authority for every retained supported history.
- Projection and publisher delivery are at least once. Consumers are idempotent;
  low duplicate rates do not become an exactly-once claim.
- Aggregate IDs, event IDs, payloads, expected versions, tenant identities and
  checkpoints do not become default log, span or metric fields.
- Event history is not exposed through an ordinary CRUD list/filter endpoint.
- No constructor starts a goroutine or mutates a global registry.

## Provisional direct API shape

The following snippets communicate semantics only. They are not accepted APIs,
and their names may change at E0.

```go
// Illustrative only.
events, err := eventpg.New(eventpg.Config{
    DataSource: source,
    Registry:   registry,
})
if err != nil {
    return err
}

account, err := events.Load(ctx, eventpg.Stream{
    Family: "accounts.account",
    ID:     accountID,
})
if err != nil {
    return err
}

proposed, err := account.Decide(command)
if err != nil {
    return err
}

commit, err := events.Append(ctx, eventpg.AppendRequest{
    Stream:          account.Stream(),
    ExpectedVersion: account.Version(),
    Events:          proposed,
})
if err != nil {
    return err
}
return useCommit(commit)
```

The store is implementation-aware without exposing a driver handle in domain
values. The final constructor must state datasource ownership, transaction
binding, supported PostgreSQL/driver versions and close semantics.

## Base-seam composition

`eventpg` does not own a framework-wide chain. When an accepted base chain is the
right binding point, an event factory returns that base's ordinary middleware
type. Application code owns order:

```go
// Illustrative only. port.ChainService and these factories are not implemented.
// applicationService calls eventpg's direct API where its existing CRUD-shaped
// command contract honestly represents the application operation.
service := port.ChainService(
    applicationService,
    tenancy.Service[Order, OrderID, OrderUpdate](tenantPolicy),
    audit.Service[Order, OrderID, OrderUpdate](auditRuntime),
    vvotel.Service[Order, OrderID, OrderUpdate](telemetry),
)
```

The first listed middleware is outermost and nil middleware is skipped. The
example does not prescribe universal ordering: E0 records the required partial
order for each operation. At minimum, verified tenant and authorization policy
must run before event SQL; telemetry cannot change results; audit may claim
atomic committed evidence only through the transaction rule below.

The following are the only initial base-seam candidates:

| Base seam | Event use | Gate |
|---|---|---|
| `port.Service` | It cannot add a named aggregate command to the current fixed CRUD-shaped method set; use it only when one existing verb honestly represents the concrete application operation | Otherwise keep the direct eventpg API and defer a separate dependency-neutral command seam until real multi-implementation/composition evidence exists |
| `crud.Core` | build a read model using existing CRUD middleware | Reuse `crud.Chain`; aggregate writes never masquerade as CRUD updates |
| `storage.Store` | persist a governed logical object reference when event payload policy requires it | Wait for accepted Store middleware; import root storage only, never a backend satellite |
| jobs/worker hooks | run a projector or publisher under host-owned lifecycle | Use the accepted jobs seam only; do not invent `jobsevent` |

No root event contract is part of this list. If a second production event-store
implementation or two concrete independent decorators later require one, its ADR
must define typed middleware, ordering, nil behavior and optional capabilities
before any public `event.Chain` exists.

## Capability and wrapper obligations

Any event adapter or future event-specific chain must satisfy all of these before
release:

1. Every base method has an explicit forward, observe or refuse decision.
2. Navigation, identity and description may use only a named bounded unwrap.
3. Append, snapshot writes, projection checkpoints, outbox claims/marks and other
   executable effects are asserted on the exact outer value. An unknown wrapper
   cannot be skipped to find an inner implementation.
4. A wrapper that preserves an effect performs its policy/observation and forwards
   it exactly once, or fails closed before I/O.
5. Method-set presence that itself promises support uses honest dynamic types or
   `(capability, bool)` discovery. It never advertises support and later discovers
   absence after beginning work.
6. A wrapper does not replay options, event batches, payload readers or callbacks
   to inspect them.
7. Error identity and cancellation are preserved. A wrapper does not convert a
   context cancellation, conflict or unknown commit into another class.
8. If an event service adapter wraps `port.Service`, it preserves optional restore
   according to the base provider/dynamic-type contract; it does not manufacture
   or erase restore.
9. If it wraps `storage.Store`, it forwards `Capabilities` exactly and preserves
   `Open` stream ownership; it does not read or buffer a body for observation.
10. D-030/D-061-style method inventories and capability matrices fail when the
    wrapped seam grows without a decision.

Conformance runs the event layer with two unrelated opaque middleware in both
orders, plus nil middleware. The test proves that ordering changes only the
documented policy/observation envelope and never makes a hidden effect reappear.

## Transaction and atomicity rules

The initial PostgreSQL profile owns one exact append transaction:

```text
begin on the caller-selected PostgreSQL authority
  verify stream and expected version
  append immutable event rows
  advance stream version/order
  optionally write an accepted local projection/outbox row
commit or roll back all of the above
```

The adapter must not open a second unscoped transaction when the caller has bound
one. A datasource or transaction mismatch fails before event/audit writes. A
connection loss around commit reports the documented unknown outcome; callers do
not blindly append again.

Audit composition is application-owned:

```go
// Illustrative only. The application imports both modules and owns this mapping.
err := unitOfWork.Within(ctx, func(txCtx context.Context) error {
    commit, err := events.Append(txCtx, request)
    if err != nil {
        return err
    }
    return auditor.RecordDomainCommit(txCtx, toAuditRef(commit))
})
```

`toAuditRef` belongs to the application. `eventpg` does not import audit, audit
does not import `eventpg`, and neither receives the other's payload. The audit
module may say “atomic with append” only when both calls prove the same PostgreSQL
transaction authority. Otherwise the application chooses an explicitly named
asynchronous link/outbox policy or refuses the configuration; there is no hidden
cross-database transaction coordinator.

## Tenant and authorization composition

Tenant scope is established and authorized before event repository access. The
event module accepts only neutral context/datasource inputs; it does not name a
tenancy extension type.

```go
// Illustrative application wiring, not eventpg API approval.
source, err := tenantSources.Resolve(ctx)
if err != nil {
    return err
}
events, err := eventpg.New(eventpg.Config{DataSource: source, Registry: registry})
```

Shared-table and database-per-tenant profiles require separate live tests for
missing scope, wrong route, pooling/session state and cross-tenant projection or
replay. A resolver failure executes zero event SQL and never falls back to a
default tenant/database. No `tenancyevent` package is permitted.

## Outbox and broker boundary

An accepted outbox stores durable delivery intent in the append transaction. It
does not store broker success. A host-owned publisher claims bounded rows, maps a
domain fact to a separately versioned integration contract and invokes a narrow
sender selected by the application.

The sender interface, if needed, is owned by the publishing/base subsystem and
contains no `eventpg` type. A broker extension implements that neutral contract;
it does not import `eventpg`. Until such a base seam has a second use, the adapter
remains application-local.

Required evidence covers commit, claim, send, acknowledgement, mark, crash and
redelivery windows. Publisher start/stop, credentials, backpressure, dead-letter
handling and manual redrive belong to the host/broker choice. Exactly-once delivery
is not claimed.

## Telemetry boundary

The first event release imports no OpenTelemetry package. Applications may use:

- the ordinary `vvotel` service decorator around a command;
- application-selected PostgreSQL driver instrumentation below `eventpg`;
- application-selected broker instrumentation around a sender.

No event-specific append/load/upcast/project/outbox span is promised. Such signals
require a later neutral typed event operation seam and an amendment to the current
OTel roadmap. Sampling/exporter availability never changes append, audit or
publisher correctness, and telemetry never carries payloads or identities.

## Delivery plan

### E0 — activate and freeze architecture

1. Name one aggregate and the consumer requirement that justifies event sourcing.
2. Accept an ADR for the one `eventpg` module, final import path, PostgreSQL/driver
   decision and exact dependency allow-list.
3. Record that the initial API is direct and that no root event contract/chain is
   justified yet under D-048.
4. Inventory any accepted base adapters and freeze first-listed-outermost order,
   nil behavior, error identity and capability rules.
5. Record the forbidden module/package list and source/import checks.
6. Freeze event envelope, stream identity/version, codec limits, error taxonomy,
   transaction ownership and supported PostgreSQL versions.

Exit evidence:

- no public API is added by prose alone;
- the module graph is `eventpg -> root + reviewed PostgreSQL ecosystem` only;
- root and every other extension have no reverse import;
- direct API and every proposed base adapter have a method/capability inventory;
- an unpublished composition fixture can select eventpg beside two fake extensions
  without a pairwise package.

### E1 — one aggregate, append and replay

1. Create one eventpg module and one aggregate/event registry.
2. Implement canonical bounded envelope encoding and exact revision readers.
3. Implement live PostgreSQL schema, expected-version append and ordered load.
4. Prove pure rehydration with historic, malformed, missing and unknown-revision
   fixtures.
5. Prove concurrent append, rollback, cancellation and commit-uncertainty behavior
   with live PostgreSQL sessions.
6. Publish safe errors without SQL, payload or identity leakage.

Exit evidence:

- one aggregate survives full replay and a v1-to-v2 reader/upcaster fixture;
- two concurrent writers produce one winner and one typed conflict;
- rollback leaves no event or stream-version fragment;
- no snapshot, outbox, worker, broker or cross-extension code is required.

### E2 — projection and evolution

1. Add one idempotent checkpointed local projection only from a concrete read need.
2. Add mixed old/new reader and writer release rehearsals.
3. Add a snapshot only after measured replay cost and full-replay equivalence proof.
4. Define archive/restore and retained-reader policy before beta.

Exit evidence includes duplicate/crash/checkpoint tests, rebuild/cutover rollback,
snapshot corruption fallback if enabled, and a restore/replay rehearsal.

### E3 — accepted base composition

1. Wait for the relevant base middleware/chain to be accepted and implemented.
2. Add only eventpg factories justified by a concrete existing base seam; do not
   invent `eventpg.Service` to smuggle named aggregate commands through the fixed
   CRUD-shaped `port.Service`, and do not add an event-owned composition registry.
3. Test first-listed-outermost order, nil, opaque neighbours, full method inventory
   and optional capabilities in both relevant orders.
4. Prove tenant/policy executes before event SQL and telemetry is result-neutral.

If no real base consumer exists, this milestone remains deferred and direct API
usage is the supported shape.

### E4 — optional outbox and external composition

1. Add a transaction-local outbox only for a named integration-event consumer.
2. Keep the first broker sender application-local unless a neutral base seam and
   independent broker extension are already justified.
3. Add application composition fixtures for tenancy, audit and telemetry without
   production mutual imports.
4. Rehearse publisher crash/redelivery, audit atomicity modes and exporter failure.

No step creates a bridge or combination package.

## Verification matrix

| Area | Required proof |
|---|---|
| One extension | Exactly one eventpg module represents PostgreSQL event sourcing |
| Root optionality | Root and non-event consumers have no eventpg or PostgreSQL event graph |
| Import direction | Eventpg imports root seams and its PostgreSQL choice only; source checks reject optional-extension imports |
| No combinations | No event × tenancy/audit/OTel/storage/broker package or nested module exists |
| Direct seam | First release does not publish a generic root event contract without D-048 evidence |
| Base composition | Each accepted adapter is an ordinary base middleware and composes with two unrelated layers in declared order |
| Capabilities | Exact effects never tunnel; method-set capabilities remain honest through opaque neighbours |
| Append | Expected version, immutable rows and stream order are proven with live concurrent PostgreSQL |
| Atomicity | Stream/event/local outbox work shares one proven transaction or the configuration refuses the claim |
| Audit | Application mapping carries a bounded reference only; neither production module imports the other |
| Tenancy | Verified scope/routing precedes event SQL; missing or wrong scope fails with zero leakage |
| Broker | Outbox proves at-least-once crash windows; broker adapter is independent/application-owned |
| Telemetry | Eventpg has no OTel imports; no-op/exporter failure changes no result; sensitive values remain absent |
| Evolution | Retained revisions have one reader path; upcasters are deterministic; mixed-release rehearsal passes |
| Lifecycle | Constructors start nothing; host owns publishers/projectors, cancellation and shutdown |
| Modules | `GOWORK=off` fixtures cover root-only, eventpg-only and multi-extension application composition |

## Initial non-goals

- A generic database-neutral event store.
- EventStoreDB, Kafka, NATS, MongoDB or filesystem backends.
- A root event contract before a second implementation justifies it.
- Pairwise or combination packages of any kind.
- Automatic aggregate/reflection registration or arbitrary metadata maps.
- Raw event-history CRUD/search for ordinary clients.
- Exactly-once broker delivery, hidden retry or hidden background workers.
- Treating audit, traces, logs, snapshots or projections as the event source of truth.
- Payload/identity export for debugging convenience.
- A generic saga/workflow engine.

## Definition of done

The first PostgreSQL event-source release is complete only when:

1. one accepted ADR names the exact eventpg module, PostgreSQL choice, direct API
   boundary and forbidden imports;
2. exactly one eventpg module exists and no event combination package exists;
3. one aggregate passes canonical codec, append/load, conflict, rollback, full
   replay and v1-to-v2 evolution tests against supported PostgreSQL;
4. expected-version, commit-uncertainty and no-blind-retry behavior are documented
   and executable;
5. every accepted base adapter uses a base-owned typed chain and passes ordering,
   nil, method inventory and exact-capability conformance;
6. audit, tenancy, broker and telemetry scenarios are proven only in application
   composition fixtures with no production extension-to-extension import;
7. any transaction-local projection/outbox or snapshot has its own explicit
   activation gate and failure matrix;
8. isolated `GOWORK=off` graphs prove root-only, event-only and composed consumers;
9. privacy scanners find no payload, aggregate/event/tenant identity, SQL, raw
   errors, expected versions or checkpoints in default diagnostics;
10. restore/replay, mixed-release rollback, capacity and incident runbooks are
    reviewed before beta;
11. documentation labels provisional and implemented APIs accurately;
12. `git diff --check` and the applicable documentation/module checks pass without
    production-code changes from this roadmap revision.
