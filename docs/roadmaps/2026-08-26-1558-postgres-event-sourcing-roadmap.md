# PostgreSQL event sourcing roadmap — 2026-08-26 15:58 +05

This roadmap proposes event sourcing as a deliberate vv satellite, using
**PostgreSQL only** for the event store. It is not a generic abstraction over
Kafka, MongoDB, EventStoreDB, files or arbitrary SQL drivers, and it does not
turn every existing CRUD resource into an aggregate. The goal is a trustworthy
append-only domain history with optimistic concurrency, projections, snapshots,
outbox integration, replay, event schema evolution and releases that remain
safe years after a first deploy.

## Reference implementations to study first

The primary hands-on reference is
[eugene-khyst/postgresql-event-sourcing](https://github.com/eugene-khyst/postgresql-event-sourcing):
a 1.4k-star Java/Spring Boot reference implementation that explicitly presents
itself as a PostgreSQL event-store template. It has the parts we need to study
without first absorbing a giant framework: append-only aggregate streams,
optimistic concurrency, snapshots, synchronous projections, asynchronous
integration events, transactional outbox variants, PostgreSQL notifications and
end-to-end tests. Its README also makes the important at-least-once and eventual
consistency limits explicit. [Repository documentation](https://github.com/eugene-khyst/postgresql-event-sourcing)

Use it as the **main practical template** for DDL shape, append transaction,
projection/outbox boundaries, test matrix and operational failure cases. Do not
port its Spring/JPA API or assume all of its choices fit Go/vv; translate its
invariants into explicit Go contracts and live PostgreSQL tests.

Use Axon as a **secondary semantic reference**, specifically for the hard
event-revision/upcaster model rather than as the implementation template:

- [Axon Framework on GitHub](https://github.com/AxonFramework/AxonFramework)
- [Axon event versioning and upcasters](https://docs.axoniq.io/axon-framework-reference/main/events/event-versioning/)
- [Axon event-store infrastructure / PostgreSQL engine](https://docs.axoniq.io/axon-framework-reference/development/events/infrastructure/)

Neither repository should be copied wholesale. The Go/PostgreSQL design must
remain native to vv's explicit caller-owned context, transaction and satellite
decisions. These references exist to avoid known traps: changing historic JSON
in place, treating a revision as only a deployment number, using upcasters as
business handlers, losing original event identity, replaying side effects, or
hand-waving a mixed-version rollout.

## Architectural decision

The event store is a dedicated satellite, e.g. `vv/eventpg` or
`vv/eventsourcingpg`. It has one deliberate dependency decision: PostgreSQL
event sourcing. It does not claim generic database portability. A Postgres
driver/adapter choice still needs a module boundary consistent with [[D-051]];
the event contract itself must not depend on an HTTP server, broker, workflow
engine, OTel exporter or serializer magic.

| Module/candidate | Owns | Does not own |
|---|---|---|
| root vv | existing CRUD/errors/policy/context seams | event source imports/contracts |
| event-source core satellite | aggregate/event/version/projection contracts | a second database engine |
| PostgreSQL adapter | append/load/checkpoint SQL/transaction semantics | broker delivery or application logging |
| outbox bridge | atomic event/outbox intent integration | exactly-once broker result |
| OTel bridge | bounded append/replay causal evidence | raw event/aggregate identity/payload |
| audit bridge | durable domain/audit action relation | audit as a substitute event history |

## Why PostgreSQL only

PostgreSQL gives the first design one coherent concurrency, transaction, index,
JSON/binary payload, migration and operational model. Hiding this behind a
generic `EventStore` interface would either leak PostgreSQL semantics anyway or
water them down at the exact points an event store must be precise: stream
version checks, append transaction, advisory/row locking strategy, projection
checkpoint, isolation level, serialization failure, index maintenance and
partition/archive lifecycle.

The public source contract can be implementation-aware without importing SQL:
it names the semantics it requires. PostgreSQL is the only supported durable
implementation and test target. An in-memory fake is test support only; it may
not be used to certify concurrent append, crash recovery or transaction results.

## Product intent

An aggregate decision loads a bounded historic stream, applies pure events,
decides new immutable events, and appends them with an expected version in one
PostgreSQL transaction. Durable side effects follow through a transactional
outbox/projection contract, not directly during replay.

```go
account, err := repository.Load(ctx, accountID)
if err != nil { return err }

events, err := account.Deposit(command)
if err != nil { return err }

err = repository.Append(ctx, accountID, account.Version(), events)
```

Names are illustrative. The contract must make the following impossible or
highly visible:

- appending events without a declared aggregate/stream type and expected version;
- silently overwriting/reordering/altering historic events;
- treating a failed optimistic append as an automatic business retry;
- publishing to broker before database commit;
- executing external side effects when replaying/loading old events;
- translating/localizing event types/payload fields;
- emitting aggregate IDs/payloads into metrics/traces by convenience;
- forgetting an upcast path when schema changes during a rolling release.

## Vocabulary

| Term | Meaning |
|---|---|
| aggregate | domain consistency boundary that decides events from state/command |
| stream | ordered immutable events for one aggregate identity/type |
| stream version | monotonically increasing append sequence, not event schema revision |
| event type | stable machine-facing domain name, e.g. `account.credited` |
| event revision | payload/schema version for one event type |
| event ID | immutable opaque event identity; no user/metric/trace label |
| global position | store-order checkpoint position, separate from stream version |
| upcaster | deterministic transformation old event revision -> current event form(s) |
| projector | idempotent consumer building a read model/checkpoint |
| snapshot | optional derived aggregate acceleration artifact, not history authority |
| outbox | durable post-commit publication work record |
| replay | reprocessing historic events under a replay-safe policy |
| release epoch | application deployment/schema compatibility phase |

## Non-negotiable invariants

1. **Append only.** Stored event envelope/payload is never updated in place to
   “migrate” history. Corrections are new events or explicitly governed archive.
2. **Expected version required.** Every aggregate append names exact observed
   stream version; blind append is not a public convenience path.
3. **One Postgres transaction.** Event rows, stream/version mutation, outbox
   intent and optionally projection-local work commit/roll back together only
   where their contract genuinely shares one database transaction.
4. **Pure rehydration.** Applying historic events mutates aggregate state only;
   it performs no I/O, clock, UUID, telemetry side effect, email or broker call.
5. **Event type/revision are stable machine identifiers.** They are not Go type
   names, localized strings, table names, routing keys built from tenant data or
   reflection output.
6. **Payload evolution is explicit.** Every revision change has an upcaster or
   a declared immutable old reader path with test fixtures.
7. **Upcasters are deterministic.** They do not query mutable external state,
   call network, inspect current time, depend on locale or execute domain effect.
8. **Projection delivery is at least once.** Projectors must be idempotent and
   checkpoint atomically/explicitly; telemetry does not promise exactly once.
9. **Tenant/security policy is outside event identity.** A tenant partition may
   be explicit in store routing/authorization, but cannot be smuggled into
   arbitrary stream names/telemetry labels.
10. **Historical data access is governed.** Event payload may contain sensitive
    data; replay/read tooling has authorization, retention/redaction/erasure
    policy and is not exposed through generic CRUD/trace endpoints.
11. **Snapshots are disposable.** Rebuild from event history remains possible
    for supported history; snapshot schema has its own version/checksum policy.
12. **No hidden background publisher.** Worker/outbox lifecycle is host/satellite
    owned explicitly, never auto-started by append constructor.

## PostgreSQL storage shape to decide in ADR

The exact DDL needs load/concurrency testing, but must explicitly choose:

| Concern | Required decision |
|---|---|
| streams | separate stream table or derived max sequence; aggregate type/ID storage |
| event rows | event ID, stream FK/key, stream version, global position, type, revision, payload, metadata, recorded time |
| ordering | stream sequence unique constraint; global position monotonic source |
| append | `INSERT` guarded by expected version with exact transaction/isolation strategy |
| concurrency | unique index/row lock/serializable handling and retry responsibility |
| payload | JSONB or byte payload codec, canonical encoding and size bounds |
| metadata | closed system metadata plus bounded declared domain correlation fields |
| outbox | same transaction table/foreign relation and publish lifecycle |
| projections | checkpoint table, idempotency key/transaction plan |
| snapshots | separate table/version/threshold/invalidating policy |
| indexes | stream load, global replay, type/revision, tenant partition only when justified |
| partitioning | trigger/threshold, retention/archive query and migration strategy |
| RLS/tenancy | one-db isolation rules vs database-per-tenant routing owner |

No DDL can be accepted merely because it inserts events in a happy test. It must
have append conflict, deadlock/serialization, crash/outbox, projected replay and
historic upcast evidence under PostgreSQL versions explicitly supported.

## Event envelope proposed shape

```text
event_id                opaque immutable UUID/ULID chosen by contract
aggregate_type          declared bounded logical type
aggregate_id            opaque domain identity, protected/high-cardinality
stream_version           positive contiguous integer per stream
global_position          monotonic PostgreSQL store order/checkpoint
event_type               declared stable machine name
event_revision           declared schema revision, e.g. "1", "2"
payload                  immutable codec bytes/JSONB
metadata                 bounded closed metadata; no arbitrary headers
recorded_at              database/explicit clock policy timestamp
causation/correlation    optional governed opaque references, not automatic logs
```

Aggregate ID, event ID, payload and free metadata are generally absent from OTel
and metrics. Audit/event administration can retain identity subject to its own
access model. Causation/correlation must be defined before using it: a random
trace ID is not a durable business correlation model.

## Versioning has several independent axes

Do not call all of these “version”:

| Axis | Purpose | Change mechanism |
|---|---|---|
| stream version | optimistic concurrency/order in one aggregate | append next event only |
| event revision | payload schema meaning | upcaster/old reader, never in-place edit |
| aggregate code version | handler/decision implementation | rollout compatibility/replay tests |
| snapshot version | derived state encoding | discard/rebuild/migrate snapshot |
| projection version | read-model schema/logic | new projection/rebuild/checkpoint policy |
| API contract version | external command/query event exposure | explicit API evolution |
| release epoch | deployed binary/database compatibility | expand/migrate/contract release plan |

Every release proposal must state which axis changes. A change to aggregate code
is not automatically an event revision; a projection rewrite is not a reason to
rewrite stored event payload; a snapshot cannot be used to erase history.

## Release strategy overview

Use expand → dual-compatible readers/upcasters → backfill/replay where needed →
deploy writers → observe → retire only after all historic/replay/rollback gates.
Never deploy a new writer that emits revision N+1 until all active readers,
projectors, recovery tools and rollback binary behavior are known. Event history
outlives a rolling upgrade, so “all pods updated eventually” is not sufficient.

## Cross-roadmap synergy

| Domain | Event-source contribution | Boundary |
|---|---|---|
| outbox/workers | commit event/outbox intent then publish at least once | no broker success in append tx |
| OTel | logical append/load/upcast/projection spans and causal links | no payload/IDs/SQL text |
| storage | domain object reference/lifecycle event coordination | no remote S3 transaction claim |
| i18n | stable machine event/error IDs; render only at UI edge | no translated event payload authority |
| tenancy | route/authorize stream store topology | no tenant ID trace label/free stream format |
| audit | durable actor/action/revision correlation | audit remains distinct from full event history |
| CRUD | selected read models can use vv CRUD safely | aggregate write is not generic update |

---

## E-01 — aggregate boundary and pure event application

**Decision.** Each aggregate is a named domain consistency boundary with a
finite event registration set. `Apply(event)` is deterministic state evolution;
`Decide(command)` evaluates loaded state and emits proposed immutable events.

### Top-level declarative DX

```go
type Account struct { /* state and version */ }

func (a *Account) Apply(event Event) { /* no I/O */ }
func (a *Account) Deposit(cmd Deposit) ([]Event, error) { /* decide */ }
```

### Happy use cases

1. Loading recorded `account.opened` then `account.credited` produces same state
   every time without database/broker/clock work in `Apply`.
2. A deposit command validates state and emits one immutable credited event; it
   does not mutate repository/global state before Append succeeds.
3. Tests rehydrate aggregate from fixture events then assert resulting event list
   and state independently from PostgreSQL adapter.
4. A new event handler is registered explicitly for aggregate type; unknown
   event type/revision is a controlled load/upcast error, not reflection panic.
5. Domain code uses stable event type constants, not Go package/type names or
   localized phrases, permitting safe refactor/translation changes.

### Edge use cases

1. `Apply` calls current time/UUID/randomness. Test purity harness detects side
   effect/non-determinism; event must carry the chosen value from Decide instead.
2. Command retry after append conflict re-invokes Decide on freshly rehydrated
   state; it must not reuse unsafe event sequence or duplicate external effect.
3. Historic event is unknown because deployment omitted upcaster/handler. Load
   fails visibly before decision; it never skips event and creates corrupt state.
4. Aggregate stream has millions events. Snapshot/load policy bounds work; code
   must not assume all aggregates are small because unit fixture is small.
5. Event contains a reference to deleted/redacted data. Apply handles declared
   event semantics without fetching mutable current record during replay.
6. Developer puts a side-effecting projector call in Apply. Architecture/test
   rule rejects it; projectors run after durable append/checkpoint boundaries.

### Invariants and acceptance evidence

- pure rehydration test runs same fixture repeatedly with fixed output/state;
- static/review rule forbids I/O dependencies in Apply package boundary;
- unknown/malformed event/revision fixture fails before partial aggregate result;
- code rename/refactor fixture preserves stable event type/revision wire values;
- command result/events are identical with OTel/audit/outbox decorators removed.

### First implementation slice

Build a small example aggregate and fixture codec before SQL. Do not create a
reflection event registry or annotation-like magic; explicit registration makes
historic type/revision ownership reviewable.

---

## E-02 — PostgreSQL append and optimistic concurrency

**Decision.** Append accepts an aggregate identity/type, exact expected stream
version and non-empty declared events. In one PostgreSQL transaction it verifies
version, inserts contiguous envelopes, advances stream state and creates needed
outbox records. Conflict is a normal domain concurrency outcome, not an adapter
retry hidden from the command.

### Top-level declarative DX

```go
err := events.Append(ctx, eventpg.AppendRequest{
    Stream: eventpg.Stream{Type: "account", ID: accountID},
    ExpectedVersion: account.Version(),
    Events: proposed,
})
```

### Happy use cases

1. Empty stream expected version 0 receives opened event at version 1 and a
   monotonic global position assigned by PostgreSQL design.
2. Loaded stream version 4 appends two events at contiguous versions 5 and 6,
   with one transaction commit and all event/outbox rows durable together.
3. Another writer has advanced stream. Append returns typed conflict with no
   partial event/outbox rows and caller chooses reload/re-decide/retry policy.
4. PostgreSQL serialization/deadlock error is mapped distinctly from expected
   version conflict; transaction/retry responsibility is documented/tested.
5. An aggregate command emits no events because state already satisfies intent.
   Repository uses explicit no-op result, not append empty batch ambiguously.
6. Event metadata uses declared bounded causation/correlation fields and payload
   codec; adapter never inserts arbitrary `map[string]any` headers by default.

### Edge use cases

1. Two transactions both observe version 4. Unique sequence/locking design lets
   only one commit; other sees conflict/serialization, never duplicated version.
2. Insert fails after first row but before second/outbox. Transaction rolls back
   every partial state; no publisher can see half aggregate transition.
3. Connection drops after commit acknowledgement is uncertain. Adapter returns
   bounded uncertain/transient outcome or documented PG error; caller reconciles
   by stream/event idempotency design, not blind append retry.
4. Writer passes wrong aggregate type for existing ID. Store enforces stream type
   identity/constraint; it cannot merge unrelated histories by ID coincidence.
5. Payload exceeds size/codec validation limit. Request fails before opening SQL
   transaction where possible and never stores truncated event.
6. Postgres isolation setting changes in deployment. Integration test/health
   check detects incompatible configuration; public semantics do not quietly drift.

### Invariants and acceptance evidence

- live PostgreSQL concurrent append suite proves contiguous unique stream version;
- rollback/crash injection leaves no partial event, stream or outbox rows;
- expected conflict and PG serialization/deadlock have separate type/test paths;
- append never starts broker/network publication/goroutine;
- SQL adapter reports no raw statement/bind/event payload in public/OTel output.

### First implementation slice

Write ADR/DDL and live conflict suite before general repository API. Start with
one stream table/event table and one transaction code path; do not optimize via
partitioning, snapshots or batch global replay until correctness baseline holds.

---

## E-03 — event envelope, codec and immutable payload limits

**Decision.** Every stored event has a validated envelope and one declared codec
contract. Event type/revision/metadata are finite/typed; payload bytes are
immutable and size-bounded before PostgreSQL transaction. Codec changes are
event-revision decisions, not an incidental Go JSON library upgrade.

### Top-level declarative DX

```go
event := events.New("account.credited", "1", AccountCredited{
    Amount: amount,
    OccurredAt: now,
})
```

### Happy use cases

1. A declared `account.credited` revision `1` encodes a validated domain payload
   deterministically and stores its bytes with envelope type/revision/ID.
2. Aggregate code receives decoded current event form through an explicit
   registry; SQL adapter never reflects arbitrary Go types into event names.
3. Payload max bytes, metadata key/count/size and event batch count are validated
   before PostgreSQL transaction and before an OTel/audit/error formatter sees it.
4. Event envelope includes recorded time chosen by DB/explicit clock policy and
   event ID supplied/generated under one idempotency/reconciliation design.
5. A codec migration adds revision `2` and upcaster reader while keeping revision
   `1` bytes untouched and readable through test fixtures.
6. An integration-event mapper creates a separately declared external envelope;
   it does not publish raw domain event payload just because both are JSON.

### Edge use cases

1. Payload has an unknown/extra field from a newer writer. Decoder/upcaster uses
   explicit forward/backward compatibility rule; it never silently drops meaning.
2. Payload exceeds declared limit or has recursion/invalid encoding. Append
   refuses before SQL and reports bounded invalid/codec condition without bytes.
3. Metadata includes a JWT, tenant name, HTTP headers or trace baggage. Closed
   metadata schema rejects it; correlation fields require a separate ADR.
4. Encoder is non-deterministic due map iteration/current time. Contract tests
   catch different bytes/semantic output for equal input before storage use.
5. Event type/revision comes from caller string. Registry validates it against
   code-owned manifest; no dynamic topic/metric/cardinality channel exists.
6. An old codec dependency upgrade changes number/date serialization. Golden
   payload fixture/manifest diff makes it a deliberate event revision decision.

### Invariants and acceptance evidence

- codec corpus covers round-trip, malformed, size, unknown-field and determinism;
- raw payload/event/aggregate IDs never reach default telemetry/metric/log values;
- envelope metadata has a finite manifest and schema version/release policy;
- events are immutable Go values/bytes after validation and cannot be mutated by
  caller/async publisher after append request starts;
- only explicit event registry maps type/revision to decoder/upcaster/handler.

### First implementation slice

Pick a single canonical JSONB-or-bytes codec and document its number/time/null
rules. Build stored fixture corpus before DDL; do not expose arbitrary serializer
interfaces or Go type-name reflection as first public API.

---

## E-04 — stream load, snapshot and rehydration bounds

**Decision.** Loading an aggregate reads a declared stream in contiguous version
order, optionally starts from a validated snapshot, upcasts/decode events, then
applies them purely. Snapshot accelerates current-state loading; it never makes
historic events disposable or hides a broken upcaster.

### Top-level declarative DX

```go
account, err := repository.Load(ctx, eventpg.Stream{Type: "account", ID: id})
```

### Happy use cases

1. Empty/new stream returns documented absence/new-aggregate result without
   confusing it with SQL/network failure.
2. A stream with versions 1–6 loads ordered events, applies all and reports
   aggregate version 6 suitable for expected-version append.
3. Snapshot at version 100 decodes validated aggregate state, then stream loader
   applies events 101–120 in order to produce same state as full replay fixture.
4. Snapshot threshold/policy is configured per aggregate type and benchmarked;
   it does not change event source of truth or external event ordering.
5. A historical stream can be loaded to a requested bounded version for authorized
   diagnostics/test only, with event history access policy separate from CRUD.
6. Load output has no automatic projection/broker/email/cache side effects; pure
   Apply makes repeated tests/replay deterministic.

### Edge use cases

1. Stream has missing/duplicate/out-of-order sequence due corruption/bug. Loader
   refuses visibly rather than skipping/reordering and returning plausible state.
2. Snapshot version is ahead of stream, invalid codec, wrong aggregate type or
   checksum/schema mismatch. Loader discards/fails according to explicit policy
   and records bounded snapshot failure, never blindly trusts derived state.
3. Stream has millions events and no snapshot. Load limit/operational policy
   prevents request path resource exhaustion; caller cannot hide it with tracing.
4. Context cancels during load/upcast. No partially hydrated aggregate is returned
   as usable state; callers see context/typed load failure.
5. One historic event revision lacks upcaster. Load fails before Decide/Append;
   it does not turn unknown fact into no-op or use current code default values.
6. Snapshot creation succeeds but append transaction rolls back. Snapshot follows
   same transaction/lifecycle or is discarded/rebuilt; it cannot point to absent
   events and become observed source of truth.

### Invariants and acceptance evidence

- full replay and snapshot+tail fixture state/version equality is exhaustive;
- load sequence validator detects gap/duplicate/type/version corruption;
- snapshot has its own type/version/codec/checksum/lifecycle manifest;
- resource limits/cancellation tests prove no partial aggregate result escapes;
- no external effect spy is invoked by Apply/load/replay fixture.

### First implementation slice

Start without snapshots for one short-lived aggregate, but write the snapshot
contract now. Add snapshot only after a measured aggregate stream needs it and
full replay/concurrency/upcast fixtures are already reliable.

---

## E-05 — event revision, upcaster chains and historic readability

**Decision.** Event revision is a stable schema identifier per event type. A
reader maps historic revisions through deterministic, pure upcaster chain into
the current in-memory form. An upcaster may transform one event into zero, one
or more semantically declared events only where the evolution decision documents
ordering/causation; it never rewrites the stored row.

### Top-level declarative DX

```go
registry.RegisterUpcaster("account.credited", "1", "2", creditV1ToV2)
```

### Happy use cases

1. Revision `1` payment field is renamed/expanded into revision `2` using a pure
   function whose fixture inputs/outputs live with event schema source.
2. A stream containing v1 and v2 events loads into one current aggregate handler
   form, while database still retains exact historic v1 bytes/revision.
3. Upcaster chain registers declared source/target revisions and fails bootstrap
   if a supported historic revision has no path to current reader form.
4. A release deploys readers/upcasters first, then writers emitting v2 only after
   current and rollback/support tooling can still process v1/v2 history.
5. Projection rebuild uses same upcaster chain as aggregate load or an explicitly
   versioned projection-specific reader, avoiding divergent historic meaning.
6. OTel records at most bounded upcast operation/revision-family outcome; it
   never serializes event payload/ID or creates metrics per aggregate.

### Edge use cases

1. Upcaster depends on database/current profile/locale/time/network. Test purity
   harness rejects it; such enrichment belongs to a new event/projection workflow.
2. Upcaster discards event. Decision documents why historical fact is subsumed and
   test proves resulting aggregate/projection semantics; no silent filter exists.
3. Upcaster splits event into multiple events. Ordering/derived IDs/causation are
   deterministic and bounded; it never manufactures fresh current timestamps.
4. Chain loops, has two ambiguous paths or skips an intermediate incompatibility.
   Registry bootstrap rejects graph before loading production stream.
5. Upcast fails only for malformed rare historic payload. Load/projection records
   safe failure/cursor state, preserves original bytes and has remediation runbook.
6. New writer emits v2 before old rollback binary installs v2 reader. Release
   gate blocks this, rather than discovering incompatibility under rollback.

### Invariants and acceptance evidence

- upcaster graph/path compiler validates all retained source revisions at startup;
- every upcaster has canonical input/output/invalid golden fixtures;
- deterministic test runs twice with no I/O/clock/randomness and compares output;
- stored event rows are immutable and schema migration contains no payload update;
- release notes name event type, revisions, reader/writer order and removal gate.

### First implementation slice

Build one v1→v2 fixture and an explicit registry/path validator before offering
reflection/automatic JSON migration. Study Axon's upcaster documentation for
failure modes, but keep Go function/registry API narrow and test-first.

---

## E-06 — projection model, checkpoints and idempotency

**Decision.** Projections are named read-model consumers of the immutable event
log. A projection persists its output and checkpoint/idempotency state with a
defined transaction strategy. Delivery is at least once; duplicate/out-of-order
handling is an explicit projection contract, never a broker slogan.

### Top-level declarative DX

```go
runner := eventpg.NewProjector(eventpg.ProjectorConfig{
    Name: "accounts.balance_view",
    Handler: projectBalance,
})
```

### Happy use cases

1. A balance projection reads global event positions in bounded batches, applies
   v1/v2 upcast form and writes read model plus checkpoint in one Postgres tx.
2. Crash before checkpoint commit repeats an event; handler/idempotency key makes
   second application safe and final read model correct.
3. A projection can rebuild into a new table/name/version from position zero,
   then atomically switch query routing after validation.
4. Projection error records a bounded failure/checkpoint state and pauses/retries
   according to runner policy; it never silently advances past unknown event.
5. Synchronous same-transaction projection is permitted only when it uses same
   PostgreSQL transaction and its consistency/cost semantics are declared.
6. Query side may use vv CRUD/policy helpers after projection data model exists;
   aggregate command side remains event append, not generic CRUD update.

### Edge use cases

1. Two worker instances claim same projection batch. Claim/checkpoint locking
   strategy ensures safe serialization or idempotent duplicate handling.
2. Global position has transaction visibility/order nuance. Postgres query/tx
   design is tested under concurrent commits; no simplistic `max(id)` assumption.
3. Handler calls external API/email. It must use an outbox/side-effect contract;
   projection checkpoint alone cannot give exactly-once external action.
4. Rebuild sees schema change/new code while old projection serves traffic. New
   projection version/table/routing lifecycle avoids half-rebuilt user reads.
5. One poison event always fails upcast/handler. Runner exposes stop/quarantine/
   authorized repair policy and preserves checkpoint/evidence, not infinite spin.
6. Batch size is huge. Runner bounds memory/transaction duration and cancellation;
   no one trace span covers an entire multi-day replay.

### Invariants and acceptance evidence

- live Postgres crash/duplicate/concurrent-claim fixtures prove final projection;
- checkpoint update and read-model writes have documented atomic relationship;
- rebuild fixture compares old/new expected model and controlled cutover behavior;
- projector never logs payload/aggregate IDs through default OTel/metrics;
- external side effect test proves outbox/idempotency policy is separately required.

### First implementation slice

Implement one local PostgreSQL projection with a single checkpoint table and
duplicate crash fixture. Defer multi-database projection, generic subscription
framework and horizontal scaling until global ordering/claim semantics are proven.

---

## E-07 — transactional outbox and integration-event boundary

**Decision.** Append may create an outbox record in the same PostgreSQL
transaction. A separate explicit publisher claims/publishes/marks records with
at-least-once behavior. Domain events are internal historical facts; integration
events are separately versioned contracts for external consumers.

### Top-level declarative DX

```go
err := repository.Append(ctx, request.WithIntegration("accounts.balance_changed"))
```

### Happy use cases

1. Aggregate append inserts domain events, stream advance and integration outbox
record in one transaction; rollback leaves none visible to publisher.
2. Publisher claims a bounded batch after commit, maps domain state/event to a
   declared integration event and sends it through host-selected broker adapter.
3. Broker accepts message; publisher marks record sent according to its own tx
   design, while consumer still treats delivery as at least once.
4. Integration event has independent type/revision/payload/privacy compatibility
   policy; it does not expose all internal historic fields by default.
5. OTel uses causal context/link between append, publish and consume attempts;
   event ID/payload/broker endpoint remain out of default attributes.
6. Audit records business/publisher action according to separate policy; it is
   complete even when trace sampling drops a publisher attempt.

### Edge use cases

1. Broker succeeds then process crashes before sent mark. Retry duplicates send;
   idempotent consumer/producer message identity/contract handles it explicitly.
2. Publisher crashes after claim before send. Lease/claim timeout/retry design
   avoids permanent stuck record and does not double claim unsafely.
3. Domain event v2 changes but integration contract remains v1. Mapper/upcaster
   policy keeps external consumers compatible rather than publishing raw v2.
4. Broker/order is unavailable. Append still commits domain/outbox transaction;
   service response must not pretend external side effect already happened.
5. One event produces many integration messages. Batch/metrics/span policy bounds
   fan-out and delivery attempts; no unbounded per-event trace exhaust.
6. Publisher uses tenant/locale/headers from raw old request. Envelope contains
   only explicitly approved durable carrier/context; no arbitrary baggage replay.

### Invariants and acceptance evidence

- live crash-window suite covers before commit, after commit, claim, send, mark;
- outbox row foreign/correlation state prevents publication of rolled-back event;
- integration schema has independent manifest/revision/privacy release tests;
- publisher owns no hidden startup/goroutine unless explicitly configured;
- delivery semantics documentation says at least once and names duplicate handling.

### First implementation slice

Use the eugene-khyst reference repository's transactional outbox sections as a
study checklist, then build one bounded polling publisher and crash fixture. Do
not bind a broker SDK into core PostgreSQL event-store module.

---

## E-08 — release choreography for readers, writers and history

**Decision.** Every event-source deployment has a compatibility table: active
reader/upcaster versions, writers, projections, snapshots, integrations, admin
tools and rollback binary. Release uses expand → compatible readers → compatible
writers → observe/rebuild → retire, never an in-place historic rewrite.

### Top-level declarative DX

```text
release ES-2026-08
  read: revisions 1,2
  write: revision 1 (phase A), revision 2 (phase B)
  rollback read: 1,2
  snapshot: v3 accepts events 1,2
```

### Happy use cases

1. Phase A deploys v2 upcasters/readers while writers still emit v1; all old/new
   pods, projectors and repair CLI load v1 streams successfully.
2. Phase B enables v2 writer only after compatibility evidence confirms every
   active/rollback reader can process v2 or rollout forbids rollback past gate.
3. Projection v2 rebuilds in parallel from immutable history, validates output,
   then query routing switches under a named rollout transaction/process.
4. Snapshot v3 is introduced as optional acceleration; old snapshots read/rebuild
   and old code can ignore/new code handles snapshot transition explicitly.
5. Release notes link event schema manifest, upcaster graph, DDL migration, data
   backfill/replay result, dashboards/alerts and on-call rollback procedure.
6. After retention/support window, a revision reader/upcaster is retired only
   after scans/proofs show no retained stream/snapshot/backup replay needs it.

### Edge use cases

1. Blue/green deployment has v1 writer and v2 writer simultaneously. Both must
   obey declared revision policy or feature gate prevents unsafe mixed emission.
2. Rollback after v2 event was committed. Old binary must read v2 or rollback is
   prohibited/uses forward-fix route; no wishful code rollback claim.
3. A long-running projector/replay starts before upgrade and ends after. It pins
   compatible reader/container version or stops/restarts on declared boundary.
4. Database DDL expands/changes index/checkpoint/snapshot schema. Migration is
   backward-compatible until every runtime/tool cohort reaches new version.
5. Emergency hotfix needs payload correction. Append compensating/corrective
   event or upcaster; never SQL UPDATE historic payload in production.
6. Archive/backup includes older revisions. Retirement review includes restore
   drills; production table scan alone cannot prove historic reader removal safe.

### Invariants and acceptance evidence

- each event schema release has reader/writer/rollback compatibility matrix;
- mixed-version/live Postgres and replay fixtures run prior to writer enablement;
- DDL migrations are expand/contract documented with no historic payload rewrite;
- every rollback plan declares what happens after new revision append;
- release audit records stable schema/version/action, not event payloads.

### First implementation slice

Create one v1→v2 release rehearsal with two binaries/fixture registries and an
actual PostgreSQL database. A prose migration diagram without a mixed-reader
test is not enough to ship a public event revision.

---

## E-09 — snapshot lifecycle, versioning and rebuild safety

**Decision.** A snapshot is a cache/derived acceleration for a named aggregate
stream at a precise stream version. It has its own snapshot schema revision,
codec/integrity metadata and invalidation/rebuild policy. It never changes the
meaning/retention of events, serves as an audit record or becomes a generic
serialized Go aggregate dump.

### Top-level declarative DX

```go
snapshots := eventpg.Snapshots(eventpg.SnapshotPolicy{
    Aggregate: "account",
    Every: 100,
    Revision: "3",
})
```

### Happy use cases

1. Account stream version 200 has a validated snapshot at 200; loader decodes it
   then applies only 201+ events, yielding same state/version as full replay.
2. Snapshot revision v2 reader/upcaster supports historic snapshot or discards it
   and replays stream under declared migration/rebuild policy.
3. Snapshot write occurs in same append transaction only when it can point to
   committed contiguous event version; otherwise asynchronous cache build has
   explicit stale/retry state and never claims atomic event truth.
4. A snapshot includes aggregate type, stream identity, stream version, snapshot
   revision, codec/checksum and creation metadata, all access-controlled/not OTel.
5. Corrupt/stale snapshot is detected, discarded/quarantined and full replay
   control succeeds; operators can measure bounded snapshot failure class.
6. Snapshot compaction/cleanup retains a configured safe set while history/event
   rows stay append-only and restore/replay readers remain supported.

### Edge use cases

1. Snapshot written at version N but transaction rolls back events N. Constraints
   and transaction ordering prevent snapshot from becoming visible as valid.
2. Snapshot payload contains a new private field. Snapshot schema/review/redaction
   is independent from event codec; generic struct serialization is rejected.
3. Aggregate code changed behavior without event schema change. Snapshot rebuild
   policy/release tests expose semantic result change; it cannot be silently cached.
4. A snapshot v3 reader is unavailable in rollback binary. Rollback either handles
   v3/discards it safely or is blocked after v3 snapshot activation.
5. Rebuild loads millions events and takes too long. Worker/maintenance process
   has bounded resource/progress/cancellation policy, never request-path surprise.
6. A user asks “restore deleted data” from snapshot. Domain retention/audit/event
   policy decides; snapshot is not a customer-facing version history API.

### Invariants and acceptance evidence

- full replay equals every supported snapshot+tail path for fixture streams;
- snapshot rows have separate schema/checksum/aggregate/version constraints;
- corrupt/future/stale/wrong-stream snapshot corpus is explicit and safe;
- snapshot bytes/aggregate IDs absent from default telemetry/errors/log fields;
- snapshot introduction/removal has reader/writer/rollback release matrix.

### First implementation slice

Keep snapshots out of first append release unless measured load demands them.
Ship their data model and tests only after event revision/upcaster/replay rules are
stable; an early snapshot is usually a way to conceal an unproven event history.

---

## E-10 — PostgreSQL transactions, locks and isolation evidence

**Decision.** PostgreSQL is not an opaque persistence detail. The event adapter
documents exact transaction isolation, uniqueness/locking statements, retryable
database faults and statement ordering. It must demonstrate its optimistic
concurrency guarantee under live PostgreSQL, not only in-memory fake tests.

### Top-level declarative DX

```go
store := eventpg.New(eventpg.Config{
    DataSource: source,
    Isolation: eventpg.SerializableOrDocumentedDefault,
})
```

### Happy use cases

1. Append at expected version uses stream row/version guard and unique event
   sequence constraint in one transaction; concurrent writer gets typed conflict.
2. Transactionally updated synchronous projection/outbox rows commit/roll back
   with event rows on the same correctly scoped datasource/transaction.
3. Postgres driver returns serialization/deadlock error; adapter maps it into a
   distinct retryable database class rather than expected stream conflict.
4. Repository caller explicitly decides command retry: reload aggregate, re-run
   Decide and append with fresh version, retaining idempotency/side-effect safety.
5. Connection pooling uses caller-owned datasource; adapter binds exact physical
   source/transaction according to existing vv scoping safeguards.
6. Migration creates constraints/indexes before writers rely on them, with live
   test that violates each constraint intentionally and observes bounded error.

### Edge use cases

1. Two new streams with same aggregate ID/type race. Unique stream identity lets
   one open/append and other gets conflict without duplicate event history.
2. One transaction appends to two aggregates. Lock ordering/transaction policy is
   documented/tested to avoid deadlock surprises or hides a multi-aggregate saga.
3. Slow transaction blocks projection/publisher visibility. Operational tests
   measure behavior; app has timeout/transaction duration policy, not assumption.
4. Database connection drops after commit. Adapter's uncertainty/idempotency story
   prevents blind re-append of possibly durable events.
5. A query uses wrong datasource/tx scope. Existing guard fails loudly before
   event/outbox writes escape transaction; trace parentage never certifies safety.
6. DBA changes isolation/constraint/index. Health/migration checks detect drift;
   adapter doesn't silently weaken sequence/transaction semantics.

### Invariants and acceptance evidence

- live PostgreSQL concurrent append/deadlock/serialization/crash test suite exists;
- DDL constraints are treated as part of public storage correctness document;
- test recorder asserts events/outbox/projection share expected tx/datasource;
- no broad automatic retry reuses stale proposed event list after conflict;
- adapter dependency graph has PostgreSQL choice only, no broker/http/OTel import.

### First implementation slice

Write DDL plus two-session concurrency test before exposing append to a consumer.
Document exact supported Postgres versions/isolation and rerun live suite on every
driver/Postgres upgrade; a mock transaction cannot prove this card.

---

## E-11 — aggregate command retries and idempotency boundaries

**Decision.** An optimistic conflict means the decision was based on old state;
retry requires rehydrating and re-deciding. The event store never blindly
re-appends stale proposed events. API-level idempotency is a separate durable
command/result contract, not stream version or event ID accidentally reused.

### Top-level declarative DX

```go
result, err := commands.ExecuteIdempotent(ctx, requestID, func(ctx context.Context) error {
    return decideLoadAppend(ctx)
})
```

### Happy use cases

1. Two deposits race; loser reloads new account, revalidates rule and emits a
   new correct event list or reports domain conflict according to current state.
2. HTTP client repeats a create command with same idempotency key; command layer
   returns stored/derived original response rather than adding duplicate stream.
3. Event IDs are immutable technical/domain identities but not alone assumed to
   deduplicate arbitrary command retries unless the contract explicitly links them.
4. A command retry after serialization error follows bounded policy with context
   deadline and preserves final visible business error/outcome semantics.
5. Outbox/notification side effects occur only from committed event/outbox, so
   a failed conflict retry does not send an external action.
6. Audit records declared command attempt/result semantics separately from event
   history and remains accurate if idempotency returns prior success.

### Edge use cases

1. Command carries current time/random price. Retry must pin/recompute under
   explicit domain policy; it cannot silently create a semantically different event.
2. First append committed but network response was lost. Idempotency record/query
   reconciles result; blind retry must not duplicate business fact.
3. A retry calls external service before append. Design moves it to prevalidated
   command/outbox/saga policy; event-store retry cannot make it exactly once.
4. Idempotency key is unbounded/user-provided. Validate length/charset/retention
   and never expose key in telemetry/metrics/default error logs.
5. Reusing key for a different command/body occurs. Durable idempotency contract
   rejects conflict and does not return prior result for semantically different input.
6. Multi-aggregate command cannot fit one consistency boundary. It uses explicit
   saga/process manager or transaction design, not endless optimistic retries.

### Invariants and acceptance evidence

- conflict test proves stale event slice is never appended after another writer;
- idempotency same-key/same-body, same-key/different-body, lost-response fixtures;
- retry has context attempt/time budget and external effect spy remains untouched;
- command/idempotency raw keys/bodies absent from OTel/metric/default logs;
- event store API has no `RetryAppend` convenience that hides re-decision.

### First implementation slice

Keep idempotency outside event-store core until command boundary/use cases exist.
Document a reference composition and use it in e2e fixture; do not overload event
ID/expected version with every API retry responsibility.

---

## E-12 — tenant scope, one database and database-per-tenant event stores

**Decision.** Tenant scope is verified/routed before event repository access.
One-database deployments make tenant partition/authorization part of stream/data
model and query constraints; database-per-tenant deployments resolve caller-owned
Postgres datasource per verified tenant. Neither exposes tenant/database identity
as a free event type, stream prefix, metric label or raw header field.

### Top-level declarative DX

```go
events := tenancyevent.Decorate(eventStore, tenantResolver)
account, err := events.Load(ctx, stream)
```

### Happy use cases

1. Shared database event table includes approved tenant partition/constraint and
   every stream/global replay/projection query has tenant policy where required.
2. Database-per-tenant resolver maps verified scope to a bounded datasource/client
   and event store uses same append/load semantics without domain code branching.
3. Tenant-specific aggregate ID collision cannot merge histories because tenant
   partition/stream uniqueness is explicit under selected topology.
4. Outbox/projector jobs persist/re-establish verified tenant scope before reading
   tenant event rows or selecting per-tenant database.
5. Audit can retain tenant/actor/event subject under protected audit policy; OTel
   emits only bounded topology/result, never tenant ID/database/schema name.
6. Cross-tenant admin/rebuild uses named authorized cohort process, batching and
   audit—not a nil tenant scope or arbitrary global stream query.

### Edge use cases

1. Missing tenant scope reaches tenant event store. It fails closed with zero SQL
   /datasource fallback rather than treating it as global/shared tenant.
2. Shared table global position/order query accidentally omits tenant policy.
   Test matrix catches cross-tenant projection/replay leakage/duplicate checkpoint.
3. Database resolver returns wrong datasource for tenant. Isolation test catches
   it; telemetry/correct parent span cannot be considered proof of correct routing.
4. Tenant is suspended/deleted while retry/replay job starts. Worker checks current
   lifecycle policy and records bounded outcome, not stale request scope blindly.
5. Event payload embeds tenant identity redundantly. Schema rejects unless a
   concrete domain/audit requirement and privacy/revision policy justifies it.
6. Fleet database migration has mixed event schema/upcaster versions. Control
   plane cohort matrix blocks a writer that some tenant DB/tool cannot safely read.

### Invariants and acceptance evidence

- shared/db-per-tenant conformance fixtures produce same aggregate behavior;
- missing/wrong/cross-tenant scope tests prove zero data leakage/misrouting;
- datasource/pool lifecycle is tenancy-owner responsibility, not event-core global;
- tenant IDs/db names/schema strings absent from normal event telemetry/metrics;
- cross-tenant maintenance/rebuild has explicit authorization/audit/batch policy.

### First implementation slice

Implement non-tenant single-store semantics first, then compose only after the
multitenancy roadmap's verified scope/resolver contract is stable. Do not bake a
tenant string into `Stream.ID` and call that a security model.

---

## E-13 — audit relationship and history access policy

**Decision.** Event history and audit log have complementary purposes. Event
stream holds immutable domain facts and supports rehydration; audit records
authorized actor/action/request/revision evidence under purpose/retention/read
policy. A domain append may create an audit item/revision, but neither storage
is automatically a complete substitute for the other.

### Top-level declarative DX

```go
audited := auditevent.Decorate(events, audit.Policy{Resource: "account"})
err := audited.Append(ctx, request)
```

### Happy use cases

1. An authorized command appends account event and writes audit revision/action
   in same PostgreSQL transaction where policy requires committed evidence.
2. Event stream retains domain `account.credited` fact; audit captures verified
   actor/action/source/purpose/tenant subject under restricted audit reader access.
3. An audit viewer can correlate a protected audit subject to event history only
   when its own permission/purpose allows it; ordinary CRUD/event query cannot.
4. Audit stores a bounded event/stream reference or revision correlation by policy,
   not full payload/model snapshot/trace output automatically.
5. Failed authorization can create an explicitly designed security-attempt audit
   item without fabricating a successful domain event/append revision.
6. Audit retention/erasure/hold lifecycle has its own policy; deleting a snapshot
   or event archival does not silently delete/alter required audit evidence.

### Edge use cases

1. Event contains sensitive historic data; audit cannot simply copy payload into
   its more broadly/less broadly accessible table without redaction/purpose ADR.
2. Audit write fails in transactional policy. Append rolls back; no committed
   event claims action without required evidence. Async audit is separately named.
3. Event is appended by system/job with no human actor. Audit records declared
   service actor/source class, not a fake user or raw worker identity.
4. An authorized audit query requests all tenant event data. Cross-tenant cohort/
   pagination/export rules apply; no generic event table endpoint is opened.
5. Trace is unsampled/expired. Audit revision remains complete; trace correlation
   field may be empty and never drives event append correctness.
6. Audit serializer changes field representation. Audit schema version/migration
   is independent from event revision/upcaster changes and never rewrites history.

### Invariants and acceptance evidence

- audit/event transaction matrix covers commit, rollback, missing actor, async job;
- audit reader authorization fixture proves event history isn't accidentally broad;
- event payload/aggregate ID remains absent from default audit capture unless policy;
- OTel removal/sampling never changes audit/event atomicity;
- docs identify event retention, audit retention and legal-hold owners separately.

### First implementation slice

First implement event append without audit, but write cross-satellite transaction
fixture once audit policy is ready. Avoid adding audit columns/ad-hoc actor maps
to core event DDL as a shortcut around the audit roadmap.

---

## E-14 — OpenTelemetry topology and privacy for event operations

**Decision.** Event OTel bridge observes logical append/load/upcast/project/outbox
operations with bounded type family/revision/outcome/batch bucket. Postgres driver
owns SQL spans; event bridge never emits event payload, aggregate/stream/event ID,
checkpoint, tenant, database or raw SQL/provider error text.

### Top-level declarative DX

```go
observed := eventotel.Decorate(events, eventotel.Config{
    Telemetry: telemetry,
    StreamFamily: "accounts.account",
})
```

### Happy use cases

1. Service command span has child `vv.eventstore.append`; optional driver SQL
   spans below answer database timing without duplicate vv SQL instrumentation.
2. Optimistic version conflict reports bounded `conflict` outcome, distinguishing
   contention from unavailable/corrupt codec without expected-version value leak.
3. Projection batch creates a bounded batch/checkpoint span with causal links to
   origin where available; a multi-day replay is many bounded spans, not one.
4. Upcaster execution records a bounded revision transition/outcome on trace-only
   field after registry review; metrics remain finite or omit revision dimension.
5. Outbox publish/consume uses explicit carrier/link policy from OTel roadmap;
   success means broker acceptance/handler result as documented, not exactly once.
6. No-op/sampled/exporter-failure provider leaves append/load/result/transactions
   exactly equivalent to undecorated store.

### Edge use cases

1. Stream type dynamic per customer. Bridge maps to configured family/unknown,
   never string-concatenates an unbounded span or metric name.
2. Poison payload/upcast error includes raw bytes/aggregate ID. Span records safe
   error class/revision family only; authorized repair tooling owns details.
3. One append emits thousands events. Event/span count is capped/summarized and
   cannot retain each event for export after transaction.
4. Trace context in outbox row is corrupt/attacker-influenced. Carrier parser
   bounds/validates it; starts safe root/link outcome without raw header values.
5. Tenant database selection fails. Trace has topology mode/outcome but neither
   database/client/tenant identity, preserving security/cardinality invariant.
6. A developer adds event metadata to attributes “for debugging.” Registry/privacy
   check rejects arbitrary escape hatch; app uses protected audit/support workflow.

### Invariants and acceptance evidence

- event core has no OTel imports and bridge owns selected OTel dependency only;
- golden trace corpus scans all envelope/payload/ID/tenant/checkpoint sentinels;
- append/load/projection/outbox topology fixtures assert one logical owner span;
- metrics cardinality calculation uses finite stream family/outcome/operation only;
- bridge has no global provider/exporter/propagator/lifecycle ownership.

### First implementation slice

Implement append/load logical trace fixture after event contract lives, reusing
OTel O-14 registry. Do not instrument SQL or broker directly in event satellite;
use upstream driver/broker instrumentation configured by the host.

---

## E-15 — event security, access and sensitive-history policy

**Decision.** Event streams can carry years of sensitive facts. Append, load,
replay, historical-version query, projection rebuild, export and repair are
separate authorized operations. Encryption/redaction/erasure/retention cannot be
solved by a generic payload map or a wish to mutate immutable history later.

### Top-level declarative DX

```go
history, err := eventAccess.LoadForPurpose(ctx, stream, purpose)
```

### Happy use cases

1. Ordinary command handler loads one aggregate under domain/tenant policy and
   does not expose stream rows via public list/export endpoint.
2. Authorized support/replay tool uses named purpose/capability, bounded range/
   pagination and protected audit evidence to inspect historic stream data.
3. Payload schema avoids secrets/credentials/large blobs by reference/redaction
   policy; object bytes live in storage lifecycle, not event JSON by default.
4. A privacy deletion request results in domain compensating/redaction/key-policy
   workflow documented per event type, preserving append-only audit/history truth.
5. Encryption-at-rest/key management is PostgreSQL/infrastructure/domain design;
   event core labels no generic `Encrypted bool` promise without full semantics.
6. Read model can omit sensitive historic fields while event store access remains
   constrained; projection data retention is separately governed/versioned.

### Edge use cases

1. A developer adds raw password/token/API response into event metadata. Codec
   schema/review rejects it; error logs/OTel cannot become remediation channel.
2. Legal hold conflicts with user deletion. Retention policy records state and
   governs key/object access; SQL DELETE/update historic event is still forbidden.
3. Audit/support export is too broad/long. Purpose/tenant/range/result limits
   and batch/cancellation rules prevent unrestricted event data extraction.
4. Upcaster needs a deleted personal field. It must handle declared historic
   representation deterministically; cannot query current profile to fill it.
5. Snapshot/read model retains materialized sensitive value after event policy
   changes. Snapshot/projection lifecycle handles deletion/rebuild independently.
6. Event encryption key unavailable. Load fails safely/controlled; it must not
   produce a partial aggregate or silently substitute current external data.

### Invariants and acceptance evidence

- payload schema lint/test rejects designated secret/unsafe fields and size forms;
- history access/admin/replay fixtures enforce named purpose/tenant/actor policy;
- all privacy/retention workflows are append-only/domain-governed, not payload SQL;
- audit/OTel export scanner contains sensitive event/data/tenant sentinels;
- docs distinguish encryption, redaction, crypto-shredding, retention and erasure.

### First implementation slice

Start with one explicit “what must never be an event payload” guideline and a
protected test/support access seam. Defer general event encryption/redaction API
until product/legal/key-management requirements specify recoverability truthfully.

---

## E-16 — partitioning, archiving and retained-history operations

**Decision.** PostgreSQL partitioning/archiving is an operational lifecycle
optimization chosen after measured stream/global replay behavior. It must retain
the query/order/reader/upcaster/backup/audit guarantees needed by supported
history. A partition drop/archive is never a casual replacement for event delete.

### Top-level declarative DX

```text
archive policy: events before epoch E move to retained immutable archive;
reader policy: repair/replay tools can resolve archive through authorized path.
```

### Happy use cases

1. Measured high-volume event table is partitioned by declared safe dimension/time
   while per-stream load and global projection order queries retain correct indexes.
2. Archive process copies/verifies immutable event records/checksums then marks/
   moves according to retention policy, with restore/replay reader documented.
3. Projection rebuild/repair knows which partitions/archive ranges it needs and
   reports bounded progress/absence rather than silently skipping old history.
4. Database backup/restore drill includes archived partitions and required old
   upcasters/snapshots/tools, not only hot event table.
5. Retention policy aligns domain/audit/legal/tenant decisions and records action
   through controlled operational/audit workflow.
6. Schema/index partition migration is expand/tested under live Postgres load,
   preserving append conflict/stream ordering behavior throughout rollout.

### Edge use cases

1. Partition pruning/index change makes one stream load slow/wrong. Benchmark and
   query plan/regression suite catch it before operational cutover.
2. Archive omits an event revision needed to rehydrate a snapshot tail. Restore
   drill fails safely; archive cannot be declared complete without reader proof.
3. A tenant/db-per-tenant fleet archives different cohorts. Control plane tracks
   topology/version/lifecycle without leaking tenant IDs into generic metrics.
4. Legal hold blocks archive/purge. Workflow records retention state and does not
   execute broad partition drop just because a time threshold passed.
5. Archive restore produces duplicates/global position collision. Import/read
   protocol is immutable/read-only or has explicit identity/ordering constraints.
6. “Delete old events for performance” is proposed. Decision requires schema,
   domain, backup, audit/legal and rollback evidence; default answer is refuse.

### Invariants and acceptance evidence

- partition/archive feature has live query/append/replay/restore performance/correctness suite;
- every retained revision/upcaster/snapshot dependency appears in archive retirement audit;
- destructive operations resolve exact tables/partitions/cohorts and are batch/recoverable;
- generic event core API has no `DeleteStream`/`PurgeBefore` convenience method;
- archive identity/payload remains protected outside telemetry/ordinary CRUD.

### First implementation slice

Defer partition/archive until Postgres metrics prove need. First write retention/
restore scenario corpus and table/query-plan benchmark; an early partition scheme
often hard-codes assumptions about tenants, stream age and replay access wrongly.

---

## E-17 — deterministic test harness and reference study protocol

**Decision.** Event-source correctness needs pure aggregate fixtures, live
PostgreSQL integration, crash/fault injection, mixed-version release tests and
reference-repository study notes. Test harness never substitutes an in-memory
store for concurrent PostgreSQL/outbox/projection semantics.

### Top-level declarative DX

```go
eventcontract.Run(t, eventcontract.Subject{
    NewPostgresStore: newTestStore,
    Registry: registry,
})
```

### Happy use cases

1. Pure aggregate test rehydrates fixtures, decides commands and compares proposed
   events/state without a database, clock/network/broker/locale side effect.
2. Live Postgres test creates isolated schema/database, applies migrations and
   runs append/load/conflict/rollback/upcast/snapshot/projection/outbox fixtures.
3. Fault harness injects transaction failure, connection loss, publisher crash,
   duplicate delivery and checkpoint failure at named windows.
4. Release harness loads historic v1 fixtures with v2 registry, runs old/new
   writer/reader combinations and tests rollback decision boundaries.
5. Reference study checklist maps `eugene-khyst/postgresql-event-sourcing`
   features—optimistic control, snapshots, projections, outbox/notify—to a vv
   contract/test; Axon upcaster concepts map to registry/path fixtures.
6. Golden payload/trace/DDL/manifest snapshots make schema behavior reviewable
   without embedding real user/tenant data in tests or documentation.

### Edge use cases

1. Test database schema left from prior run changes global position/order result.
   Harness uses isolated setup/cleanup with validated non-broad destructive scope.
2. Test fake permits append race impossible in Postgres. Report marks it unit-only;
   release claim requires live two-session integration evidence.
3. Time/random event value makes snapshot/upcaster fixture flaky. Tests inject
   deterministic clock/ID at Decide boundary, not inside Apply/upcaster.
4. Crash after commit cannot be simulated by normal return error. Harness controls
   connection/process fault/reconciliation and asserts durable state post restart.
5. Reference repository design differs (Spring/JPA). Study note records semantic
   lesson, chosen Go/Postgres alternative and reason—not a copied API artifact.
6. E2E uses broker/OTel/cloud unavailable in CI. Core suite remains deterministic;
   separate target integration reports unverified cells honestly.

### Invariants and acceptance evidence

- test matrix has pure/live/fault/release/target layers and labels their strength;
- every crash/retry window has expected event/stream/outbox/projection state table;
- tests scan exports for payload/ID/tenant/raw error accidental leakage;
- reference study links source section and issue/ADR decisions to implementation;
- no test depends on process global provider, host locale or shared database state.

### First implementation slice

Create aggregate fixture format, live Postgres test helper and concurrent append
case before package API proliferation. Clone/run the eugene-khyst reference in a
separate research environment if useful; do not vendor or couple it to vv code.

---

## PostgreSQL event-store FMEA and verification matrix

The following checks are deliberately granular. Each is a candidate table-driven,
two-session, crash/restart, migration or release test—not a claim that a generic
event-store interface has hidden the condition away.

### Stream identity and append validation

- Reject empty aggregate type before opening PostgreSQL transaction.
- Reject aggregate type outside the explicit event/aggregate registry.
- Reject empty aggregate ID before SQL/OTel/audit work begins.
- Reject an aggregate ID over its declared byte/encoding limit.
- Reject an expected version below the documented new-stream value.
- Reject an expected version that overflows implementation integer bounds.
- Reject empty event batch unless API has an explicit no-op append result.
- Reject event batch above configured event-count limit before SQL.
- Reject payload above per-event byte limit before SQL.
- Reject aggregate transition payload total above batch byte limit before SQL.
- Reject event type not registered for the aggregate type.
- Reject event revision with no registered writer/reader schema.
- Reject unsupported metadata key before encoder/transaction begins.
- Reject metadata value/count/total size over its declared limit.
- Reject caller-derived free-form stream name/topic/partition value.
- Reject raw tenant header as a stream routing argument.
- Reject a stream type mismatch for an existing aggregate identity.
- Reject a new stream with a non-new expected-version value.
- Reject a nonempty existing stream append with new-stream expected version.
- Reject a proposed stream sequence that would create a gap.
- Reject caller-specified global position as an append input.
- Reject caller-specified recorded timestamp when contract owns database time.
- Reject untrusted raw trace/baggage map as event metadata.
- Reject external broker envelope as a domain event payload shortcut.
- Reject localized event type or message text as machine event type.
- Reject Go reflection type name as an implicit event type registration.
- Reject mutable event object after validation/append request construction.
- Reject implicit auto-generation of missing required payload domain field.
- Reject inconsistent event type/revision pair in one batch.
- Reject a batch whose first event does not follow aggregate decision contract.

### PostgreSQL transaction and conflict behavior

- Prove one append transaction owns stream/version/event/outbox inserts.
- Prove successful append commits every event row in contiguous stream order.
- Prove failed second event insert rolls back first event row.
- Prove failed outbox insert rolls back event/stream advance in transactional mode.
- Prove failed synchronous projection write rolls back append where policy claims it.
- Prove wrong datasource binding executes zero event/outbox SQL.
- Prove nested caller transaction is reused rather than silently opening another.
- Prove expected-version conflict leaves no event/outbox/projection partial rows.
- Prove two concurrent writers cannot both commit the same next stream version.
- Prove two concurrent new-stream writers cannot both create stream identity.
- Prove stream version unique constraint catches a defective application guard.
- Prove transaction isolation/lock strategy handles race under live PostgreSQL.
- Prove serialization failure is not reclassified as ordinary expected conflict.
- Prove deadlock failure is distinguishable from a domain rule rejection.
- Prove connection acquire failure occurs before partial append work.
- Prove transaction begin failure emits no outbox/publisher work.
- Prove transaction commit failure/uncertainty has documented reconciliation path.
- Prove connection loss after commit is not blindly retried with stale events.
- Prove context cancellation before transaction creates no rows.
- Prove cancellation during statement rolls back/returns no usable append success.
- Prove timeout does not start hidden background retry/publish work.
- Prove long-running transaction behavior is measured and operationally documented.
- Prove index/constraint migration has no writer window without concurrency guard.
- Prove database setting drift is detected before safety semantics degrade.
- Prove a DB error raw SQL/bind/payload is absent from public projection.
- Prove app retry rehydrates/re-decides after conflict, not reuses stale events.
- Prove idempotency layer, if configured, shares/coordinates correct transaction.
- Prove repeated same command key has a durable documented result.
- Prove same command key with different canonical request is explicit conflict.
- Prove no `RetryAppend` API masks a missing re-decision step.

### Stream load and rehydration

- Prove empty stream has a distinct result from connection or codec failure.
- Prove load returns events in ascending contiguous stream version order.
- Prove aggregate reported version equals last successfully applied event version.
- Prove sequence gap fails before a partial aggregate is returned.
- Prove duplicate sequence fails before a partial aggregate is returned.
- Prove wrong aggregate type/ID row cannot be joined into a stream.
- Prove unknown type/revision fails before Decide can operate on state.
- Prove malformed payload fails with safe codec condition and preserved row evidence.
- Prove full replay executes no broker/email/HTTP/storage side effect.
- Prove Apply executes no clock/UUID/random provider call.
- Prove load honors caller context cancellation without returning partial state.
- Prove load has bounded stream/event/byte resource policy for huge streams.
- Prove aggregate Apply error names type/revision/version safely without payload dump.
- Prove event metadata isn't treated as code/config/locale/current context input.
- Prove load-to-version authorized API enforces bounds/purpose/tenant policy.
- Prove generic ordinary CRUD endpoint cannot enumerate raw streams.
- Prove historical reader support includes retained revisions after deployment.
- Prove a decoder library upgrade compares canonical historic fixture output.
- Prove map/order/number/time codec behavior stays deterministic across processes.
- Prove stored original event bytes/revision are not changed by load/upcast.
- Prove load under no-op telemetry yields identical aggregate/result/error.
- Prove load under sampled telemetry yields identical aggregate/result/error.
- Prove malformed trace carrier does not corrupt event/aggregate load context.
- Prove tenant scope is checked before shared-table stream query.
- Prove per-tenant datasource is selected before database query in DB-per-tenant.
- Prove missing tenant scope executes zero load SQL in tenant-scoped store.
- Prove cross-tenant guessed aggregate ID returns no unauthorized stream data.
- Prove admin historical-load path is named/authorized/audited separately.
- Prove projection uses same/reviewed reader semantics rather than raw SQL decode.
- Prove test fixture covers historic event row stored by prior supported binary.

### Snapshot validation and lifecycle

- Prove snapshot aggregate type equals requested stream aggregate type.
- Prove snapshot identity equals requested stream identity under protected comparison.
- Prove snapshot version is not greater than committed stream last version.
- Prove snapshot revision is known/readable or safely discarded/rebuilt.
- Prove snapshot checksum/codec validation precedes state application.
- Prove corrupt snapshot doesn't return partial aggregate state.
- Prove corrupt snapshot fallback full replay follows declared safety policy.
- Prove snapshot+tail state equals full replay for every fixture stream.
- Prove snapshot at event N applies only N+1 onward in correct order.
- Prove snapshot at stream end with no tail yields exact final state/version.
- Prove snapshot write rollback leaves no usable row when append rolls back.
- Prove snapshot write never alters/deletes event rows.
- Prove snapshot payload doesn't default to generic aggregate JSON dump.
- Prove snapshot private/redacted fields follow its own schema policy.
- Prove snapshot build threshold is per aggregate/documented not hidden magic.
- Prove snapshot failure doesn't mark a successful append as failed unless policy says.
- Prove asynchronous snapshot build has stale/retry/claim lifecycle when offered.
- Prove snapshot cleanup keeps a safe rebuild/rollback set.
- Prove rollback binary can read or safely discard newly introduced snapshot version.
- Prove archive/restore fixtures include required snapshot rows/versions.
- Prove snapshot table/index migration preserves append/load availability.
- Prove snapshot compaction is not advertised as event/audit retention deletion.
- Prove snapshot IDs/bytes/stream identity never enter normal telemetry fields.
- Prove snapshot performance test measures full replay versus snapshot+tail benefit.
- Prove a new aggregate can operate without snapshot support configured.
- Prove long aggregate recovery remains bounded/cancellable under snapshot outage.
- Prove snapshot mismatch produces bounded diagnostic/audit operational evidence.
- Prove no normal user endpoint restores arbitrary snapshot as a version API.
- Prove snapshot schema changes have old/new/mixed reader fixture matrix.
- Prove snapshot implementation imports no broker/transport/OTel core dependency.

### Upcaster graph and event revision evolution

- Prove every retained event type/revision has one declared current reader path.
- Prove graph rejects missing source-to-target transition.
- Prove graph rejects cycles before serving a load/replay request.
- Prove graph rejects ambiguous multiple paths with different semantic output.
- Prove graph rejects revision target not registered as a valid reader schema.
- Prove v1→v2 canonical input transforms to expected canonical v2 fixture.
- Prove malformed v1 payload reports safe upcast error without original-byte log.
- Prove upcaster function has no database/network/clock/locale/random dependency.
- Prove upcaster runs deterministically twice for equal historical input.
- Prove upcaster preserves/explicitly maps original event identity/recorded order.
- Prove a split upcast emits deterministic child order/identity/causation mapping.
- Prove a drop upcast has a written semantic justification and state/projection test.
- Prove an upcaster cannot call aggregate Decide or external side-effect handler.
- Prove projection rebuild/upcaster behavior matches declared aggregate reader policy.
- Prove integration mapper does not accidentally see an incompatible raw revision.
- Prove current writer revision isn't enabled before readers/upcasters are deployed.
- Prove old rollback reader path covers every writer revision allowed at rollback time.
- Prove a hotfix never updates stored historical JSON/payload columns in place.
- Prove an event correction uses compensating fact/new event policy instead.
- Prove revision schema change is distinct from stream version change in docs/tests.
- Prove aggregate code refactor without wire change does not invent event revision.
- Prove snapshot revision upgrade is distinct from event revision upgrade.
- Prove projection version upgrade is distinct from event revision upgrade.
- Prove API version upgrade is distinct from domain event revision upgrade.
- Prove archive reader/removal decision includes each old upcaster revision path.
- Prove upcaster error stops/records projection checkpoint safely.
- Prove upcaster execution has bounded per-event output/count/size limits.
- Prove upcast monitoring uses finite revision/type family vocabulary only.
- Prove a library codec upgrade triggers golden upcast/replay fixture review.
- Prove release note gives operator reader/writer/rollback order and removal gate.

### Projection, checkpoint and replay verification

- Prove projector name is a configured stable finite logical identifier.
- Prove duplicate projector name/configuration conflict fails at bootstrap.
- Prove projection checkpoint schema/version is explicit and migration-tested.
- Prove first run from position zero applies bounded ordered batches.
- Prove projection checkpoint only advances after corresponding model writes commit.
- Prove crash before checkpoint commit repeats event without corrupt final model.
- Prove crash after checkpoint commit does not reapply event as a normal path.
- Prove duplicate delivery is idempotent under declared model/key strategy.
- Prove concurrent worker claim cannot both advance same projection checkpoint wrongly.
- Prove claim/lease expiry/recovery is bounded and leaves evidence.
- Prove poison event failure does not silently advance checkpoint past event.
- Prove retry backoff/attempt limit is explicit and context-cancellable.
- Prove manual skip/quarantine requires named authorization/audit/purpose action.
- Prove quarantine retains safe protected event reference, not payload in logs.
- Prove projector handler receives current upcast event form deterministically.
- Prove projector handler cannot accidentally use raw unupcast payload path.
- Prove projection code has no locale/current-time side effects during replay.
- Prove projection external call goes via a separate durable outbox/side-effect plan.
- Prove projection transaction uses same expected PostgreSQL datasource/scope.
- Prove projection new read-model table can build alongside old live table.
- Prove rebuild never mutates live projection table before cutover policy allows.
- Prove rebuild cutover validates row/semantic invariants, not only row count.
- Prove read routing switch has rollback behavior to old projection version.
- Prove projection schema change has old/new tool/runtime compatibility fixture.
- Prove projection range/batch size is bounded for memory and transaction duration.
- Prove cancellation leaves checkpoint/model relationship consistent.
- Prove global order query uses documented visibility/order semantics under commit races.
- Prove projection metrics use projection family/outcome/bucket, never checkpoint ID.
- Prove projection traces omit aggregate/event IDs and payload data.
- Prove full replay output matches expected model for historic v1/v2 fixture mix.
- Prove replay can be paused/resumed from durable checkpoint after process restart.
- Prove replay for a tenant scope cannot read another tenant's event position/data.
- Prove cross-tenant rebuild uses explicit cohort/list/authorization policy.
- Prove a new projector can start from zero without interfering with existing one.
- Prove projection deletion/rebuild respects retention/legal/audit policy.
- Prove projection owns no hidden background worker unless host config starts it.
- Prove projectors stopped on deployment use safe claim/checkpoint handoff.
- Prove a long transaction effect on projection lag is visible/operationally bounded.
- Prove projection test fake cannot certify live Postgres global order behavior.
- Prove every projection release has owner, schema version and recovery runbook.

### Outbox and integration delivery verification

- Prove outbox row cannot be created without committed source event/transaction.
- Prove rolled-back append produces no eligible publisher row.
- Prove publisher claim is atomic/bounded and identifies only eligible records.
- Prove publisher crash before send leaves record retryable after claim policy.
- Prove publisher crash after send before mark allows documented duplicate delivery.
- Prove publisher mark failure does not declare exactly-once delivery.
- Prove broker outage does not roll back already committed domain event/outbox row.
- Prove broker timeout/lost response has uncertain/deduplication handling.
- Prove consumer duplicate fixture yields one correct projection/effect outcome.
- Prove consumer out-of-order fixture follows integration event contract explicitly.
- Prove integration message identity is stable/bounded and protected from metric labels.
- Prove integration schema revision is independent from domain event revision.
- Prove integration mapper validates destination event schema before publish.
- Prove integration mapper omits internal payload/sensitive fields by default.
- Prove mapping a domain v1/v2 event produces compatible external contract fixtures.
- Prove no direct broker call occurs in aggregate Apply/Decide/append transaction.
- Prove publisher carrier contains only approved bounded trace/context fields.
- Prove publisher does not copy arbitrary locale/tenant/header/baggage data.
- Prove corrupt carrier starts safe new context/link and does not fail event correctness.
- Prove publisher trace success means broker acceptance, not consumer completion.
- Prove consumer trace/link obeys replay/retry parent-versus-link policy.
- Prove publisher metrics have finite event family/lifecycle/attempt dimensions.
- Prove broker endpoint/topic/tenant/payload/event ID are absent from vv signals.
- Prove publisher backpressure/batch limits avoid unbounded memory/event span growth.
- Prove manual redrive is a named/audited operation with duplicate expectation.
- Prove dead-letter state is durable and has an authorized repair/replay path.
- Prove publisher shutdown is host-owned and does not lose claimed state silently.
- Prove message encryption/signing/auth is broker/application concern, not core event API.
- Prove target broker adapter dependency is isolated from PostgreSQL event core.
- Prove outbox cleanup/retention does not delete needed audit/event history blindly.
- Prove integration test has real/fake layers and labels delivery guarantee accurately.
- Prove outbox table migration preserves old publisher compatibility during rollout.
- Prove multiple publisher instances don't publish unclaimed/foreign rows incorrectly.
- Prove tenant database publisher resolves correct datasource before claim query.
- Prove cross-tenant publisher maintenance cannot operate with absent tenant scope.
- Prove payload redaction/contract changes have consumer compatibility release plan.
- Prove eugene-khyst outbox pattern lessons map to an explicit vv choice/test.
- Prove no automatic NATS/Kafka/Temporal dependency enters the event-store core.
- Prove integration event audit is complete even if OTel sampling/export fails.
- Prove operational dashboard does not infer exactly-once from low duplicate rate.

### Release, rollback and operational verification

- Prove every deploy names active reader revisions for each event type.
- Prove every deploy names active writer revision for each event type.
- Prove every deploy names old rollback binary reader capabilities.
- Prove every deploy names upcaster graph version/hash and snapshot reader revisions.
- Prove every deploy names projection schema/version/rebuild/cutover state.
- Prove every deploy names integration schema mapper/consumer compatibility state.
- Prove every deploy names PostgreSQL DDL migration expand/contract stage.
- Prove reader/upcaster deployment precedes any writer revision feature activation.
- Prove writer revision feature gate can be disabled without historic payload rewrite.
- Prove blue/green pods with v1/v2 readers load mixed streams correctly.
- Prove blue/green writers cannot emit incompatible revisions simultaneously.
- Prove rollback after v2 write is rehearsed and explicitly permitted/blocked.
- Prove new snapshot revision doesn't make rollback reader return corrupted state.
- Prove projection rebuild version serves no partial user query during cutover.
- Prove archive/backup restore includes historic upcaster/readers before retirement.
- Prove event schema deprecation has support window/owner/removal acceptance criteria.
- Prove no historic payload UPDATE/DELETE DDL exists in ordinary release migration.
- Prove compensating event approach is selected/tested for factual correction.
- Prove code-only aggregate behavior change has replay regression review.
- Prove codec/JSON/driver/Postgres upgrades rerun historic fixture corpus.
- Prove schema/index/isolation changes rerun concurrent append/projection tests.
- Prove rollback plan handles outbox publisher/integration schema queue state.
- Prove rollback plan handles tenant database cohort versions where supported.
- Prove release audit captures version/action/operator but no payload contents.
- Prove release dashboard can distinguish code/version/schema outcome safely.
- Prove on-call runbook has append conflict, upcast failure and projection lag paths.
- Prove incident remediation doesn't ask operators to edit historic payload in SQL.
- Prove emergency repair tool has purpose/audit/transaction/backup guardrails.
- Prove retention/archive job is paused/reviewed during incompatible schema change.
- Prove first event-source release has a full restore/replay rehearsal, not only CI.
- Prove feature flags changing writer revision are audited and safely scoped.
- Prove a failed migration cannot leave a database cohort with a writer it can't read.
- Prove migration status is bounded/topology evidence, not per-tenant metric label.
- Prove release documentation names reference implementation lessons and deviations.
- Prove compatibility matrix is regenerated/approved rather than hand-waved prose.
- Prove all operational background components have startup/shutdown owner/config.
- Prove benchmarks establish append/load/project/upcast performance baselines.
- Prove capacity plan includes event growth, indexes, snapshots, outbox and archive.
- Prove no “supported Postgres” claim survives without versioned live test report.
- Prove final release conclusion lists explicit deferrals as clearly as delivered API.

### Tenant, audit, telemetry and history-access verification

- Prove verified tenant scope is established before shared-store stream query.
- Prove verified tenant scope is established before DB-per-tenant datasource query.
- Prove absent tenant scope has zero SQL and no fallback to default datasource.
- Prove wrong resolver mapping does not return another tenant's aggregate state.
- Prove shared table stream uniqueness includes tenant partition where design requires.
- Prove global projection/checkpoint tenant query doesn't leak/skip tenant rows.
- Prove tenant event metadata is not automatically duplicated in every payload.
- Prove tenant ID/database/schema/connection string are absent from OTel/metrics.
- Prove cross-tenant replay is explicit admin capability with bounded cohort/action.
- Prove tenant suspension/deletion affects worker/replay policy under current control plane.
- Prove audit append revision contains declared actor/source/action/result fields only.
- Prove audit does not default-copy event payload or aggregate snapshot.
- Prove audit failure atomicity matches published append policy.
- Prove attempt audit isn't falsely represented as committed domain event.
- Prove audit history reader is separately authorized from aggregate application load.
- Prove trace/span correlation optionality doesn't affect audit event correctness.
- Prove event store OTel has one logical append/load/project/outbox owner span.
- Prove upstream Postgres driver spans remain separate and no SQL duplication occurs.
- Prove OTel status/outcome maps expected conflict, invalid codec, cancel, database error.
- Prove raw event type dynamic values collapse to configured stream/type family.
- Prove aggregate/event/stream IDs and payloads never appear in span/event fields.
- Prove expected/actual stream version numbers never appear as metric labels.
- Prove projection checkpoints/global positions never appear as trace or metric IDs.
- Prove event revision metrics are bounded/trace-only until finite registry review.
- Prove trace carrier persistence in outbox has byte/key/baggage bounds.
- Prove OTel no-op/sampled/exporter failure changes no PostgreSQL transaction result.
- Prove audit/OTel integrations are directional satellites with no root reverse import.
- Prove history/admin query purpose is recorded/audited without leaking request filter.
- Prove support export range/page caps prevent unbounded event data extraction.
- Prove sensitive payload schema/redaction/crypto/retention decisions have owners.
- Prove a deletion request cannot use generic event delete as a shortcut.
- Prove snapshot/projection/storage copies are included in privacy retention analysis.
- Prove storage object lifecycle events carry logical references, not backend credentials/URLs.
- Prove i18n never changes event type/revision/payload/upcaster/replay semantics.
- Prove background locale/tenant context is explicit where notification outputs need it.
- Prove external observability access is not considered authorization to history data.
- Prove resource/log policies do not stringify raw errors/payload for “debugging.”
- Prove operation cardinality budget remains finite under many aggregates/tenants/events.
- Prove all cross-satellite integration tests still pass when OTel/audit decorators vanish.
- Prove each protected-history operation has an owner, purpose, retention and runbook.

### Crash-window injection table

| Window | Durable state expected | Retry/recovery owner | Forbidden conclusion |
|---|---|---|---|
| before transaction begin | no stream/event/outbox mutation | caller command path | append succeeded |
| after transaction begin | no committed mutation | PostgreSQL rollback | event is visible |
| after stream row guard | no committed mutation | PostgreSQL rollback | version advanced |
| after first event insert | no committed mutation | PostgreSQL rollback | partial batch persists |
| after final event insert | no committed mutation | PostgreSQL rollback | outbox may publish |
| after synchronous projection write | no committed mutation | PostgreSQL rollback | read model is current |
| after outbox insert | no committed mutation | PostgreSQL rollback | publisher may claim row |
| before commit | no committed mutation | PostgreSQL rollback | caller has success |
| during commit | unknown until connection/result evidence | reconciliation/idempotency | retry stale batch blindly |
| after commit before response | committed or uncertain | command idempotency/query | no event exists |
| after response before caller persists result | event committed | command/result recovery | new command is safe duplicate |
| before snapshot write in tx | event path determines snapshot absence | loader/full replay | snapshot must exist |
| after snapshot insert before tx commit | no committed snapshot/event | rollback | snapshot is valid |
| after commit before snapshot async mark | event committed, cache state uncertain | snapshot worker | stream invalid |
| before projection claim | no new projection work | runner | checkpoint advanced |
| after projection claim before read | claim may expire | claim recovery | event processed |
| after event read before model write | no model/checkpoint commit | runner retry | projection complete |
| after model write before checkpoint in tx | rollback/no partial final relation | DB transaction | checkpoint can advance |
| after model/checkpoint commit before ack | duplicate possible | idempotent projector | exactly once |
| before outbox publisher claim | committed row eligible | publisher | broker has message |
| after publisher claim before send | claimed/lease state | lease retry | message accepted |
| after send before sent mark | broker may accept | duplicate-safe publisher/consumer | no duplicate possible |
| after sent mark before cleanup | sent record durable | retention job | external consumer done |
| before consumer handler | message exists | consumer | projection/effect done |
| after consumer effect before dedup/checkpoint | duplicate possible | idempotency layer | exactly once |
| before upcaster invocation | historic row immutable | reader | current form available |
| during upcaster failure | stream untouched | repair/release owner | event may be skipped |
| after upcast before Apply | no aggregate side effect | load retry | state partially valid |
| during Apply panic/error | no returned aggregate result | caller/loader | later events can apply |
| before archive copy | hot history remains | archive worker | archive authoritative |
| after archive copy before verification | duplicate copies possible | archive verifier | hot row can drop |
| after verification before cutover | both retained per policy | archive workflow | old reader blocked |
| after archive mark before delete/drop | policy state durable | archive recovery | physical purge complete |
| during restore | target state partial/isolated | restore runbook | production reader safe |
| after restore before validation | imported data not yet trusted | restore verifier | history ready |
| during tenant resolver selection | no event SQL on failure | tenancy control plane | fallback tenant allowed |
| after datasource selected before append | selected scope only | scoped transaction | selection audited success |
| during audit revision write in tx | no committed domain/audit if fail | transaction rollback | event action audited |
| after audit commit before trace export | audit complete | audit store | trace required |
| during OTel exporter failure | append/load independent | host exporter | operation must fail |
| during release reader deployment | old writer history readable | release manager | new writer can emit |
| during writer enablement | mixed reader compatibility required | feature gate | rollback automatically safe |
| after new revision append | old reader policy decides | release manager | revert binary blindly |
| during projection rebuild | old projection serves until cutover | rebuild runner | new model complete |
| during cutover | routing switch atomic/controlled | release manager | queries see partial mix |
| during rollback | reader/snapshot/upcaster matrix applies | runbook | historic payload changed |

### Append/load implementation review checklist

- Is aggregate type a configured stable logical name?
- Is event type a configured stable machine contract name?
- Is every event revision declared in a registry/manifest?
- Is aggregate ID treated as protected/high-cardinality identity?
- Is stream version separate from event revision in API and docs?
- Is global position separate from stream sequence in API and docs?
- Is every append given an exact expected stream version?
- Is empty append behavior named and intentional?
- Is batch event order explicit and preserved through SQL/codec/outbox?
- Does the caller own command retry/re-decision after expected conflict?
- Does the adapter distinguish expected conflict from serialization/deadlock failure?
- Are Postgres transaction isolation and uniqueness/lock rules written down?
- Are event/event-outbox/projection transaction boundaries visible in code/tests?
- Can wrong datasource scope fail before any SQL executes?
- Are payload and metadata byte/count bounds checked before DB work?
- Are payload codec null/number/time/map-order semantics canonicalized/tested?
- Are raw event envelopes immutable after validation?
- Does stream load validate contiguous sequence before returning state?
- Does aggregate Apply remain free of I/O/time/random/external side effects?
- Does unknown historic revision fail safely before a command can decide?
- Does snapshot remain clearly a derived cache rather than historical authority?
- Does snapshot+tail reproduce full replay state/version in every fixture?
- Does no core method make source event payload mutation/deletion convenient?
- Does an integration event mapper deliberately separate external contract?
- Does every publisher outcome avoid an exactly-once claim?
- Does every projector have duplicate/checkpoint/rebuild semantics?
- Does every history/replay/export operation have a policy/purpose/tenant guard?
- Does all telemetry omit payload/IDs/tenant/SQL/raw database errors?
- Does audit correlation remain optional and protected from trace sampling?
- Does i18n remain outside event envelope/type/upcaster/replay semantics?

### Event-revision release review checklist

- Is change actually an event revision rather than only aggregate code change?
- Is a new event type more accurate than mutating meaning of an old type?
- Is v1 payload retained byte-for-byte with no update migration?
- Is v1→v2 path pure, deterministic and golden-tested?
- Is every source revision path unambiguous and complete?
- Are upcaster drops/splits documented with state/projection semantic fixtures?
- Are old/new reader/upcaster binaries tested against mixed historic streams?
- Are writers held at old revision until readers/upcasters deploy everywhere needed?
- Is feature-gated writer enablement/disablement documented and audited?
- Can intended rollback read every revision that might already have been written?
- Are snapshot versions/readers included in the rollback matrix?
- Are projections/integration mappers/rebuild tools included in the rollback matrix?
- Are retained backups/archives/repair tools included in old-revision removal review?
- Is a compensating event selected for factual correction rather than payload edit?
- Are DDL/index/codec/Postgres/driver upgrades replay-tested with historic fixtures?
- Does release documentation name compatibility window and removal exit evidence?
- Is there an actual two-version PostgreSQL rehearsal rather than only unit tests?
- Is migration status/cohort state visible without tenant identifier metric leakage?
- Is on-call repair procedure explicit about no direct historic row rewrites?
- Are all deferred event source features still clearly non-goals in release notes?

### Postgres schema and migration review checklist

- Does stream identity have type/ID and required tenant partition constraints?
- Does event row enforce stream version uniqueness and required envelope fields?
- Does global position/order source have documented concurrency/visibility behavior?
- Are event type/revision/payload/metadata size/encoding constraints represented?
- Are append/load/global projection query indexes measured with representative load?
- Are outbox/checkpoint/snapshot/audit FK/index lifecycle choices explicit?
- Does DDL avoid historical payload UPDATE/DELETE in ordinary migration path?
- Are additive columns/indexes deployed before new writer/reader assumptions?
- Is destructive contract/index removal delayed until all runtime/tool cohorts move?
- Is Postgres version/extension/isolation configuration support explicitly tested?
- Is migration transaction/lock duration/cancellation behavior operationally rehearsed?
- Is schema drift detection/health evidence available without raw connection leakage?
- Are db-per-tenant migration cohorts/tracking independent from generic store core?
- Are shared-DB RLS/tenant query constraints tested under pooled transactions if used?
- Is backup/restore/archival plan part of schema retirement, not an afterthought?
- Are test cleanup targets explicit and non-broad before any drop/truncate action?
- Are data-volume/partition plans deferred until benchmark/query-plan evidence exists?
- Are error mappings based on observed driver payloads, not guessed string parsing?
- Are raw SQL/binds/rows never copied to public error/trace/audit generic fields?
- Does an external DDL reviewer own production operation beyond code package tests?

## Operational incident and repair protocol

### Optimistic-concurrency conflict spike

1. Confirm the event-store outcome is expected-version conflict, not serialization/deadlock.
2. Identify aggregate type/resource family using bounded operational dimensions.
3. Do not add aggregate IDs, expected versions or payloads to generic metrics.
4. Confirm application retry path reloads and re-decides rather than re-appending stale events.
5. Confirm idempotency layer behavior for duplicated client command requests.
6. Check whether a release introduced a new high-contention aggregate boundary.
7. Check command batching/multi-aggregate transaction lock-order behavior.
8. Inspect authorized audit/support data only through its protected purpose path.
9. Measure PostgreSQL transaction duration and conflict rate with existing safe signals.
10. Reproduce using synthetic aggregate fixture, never real customer event payload copy.
11. Add/adjust domain command or aggregate boundary after semantic review.
12. Do not “fix” conflict spike by disabling expected-version validation.

### Upcaster/unknown historic revision failure

1. Stop affected projection/load operation at the known safe checkpoint.
2. Record bounded event type/revision family and active reader/upcaster release version.
3. Preserve original event row/bytes; do not update payload in PostgreSQL manually.
4. Identify whether failure is missing path, malformed historic data or incorrect mapper.
5. Reproduce against sanitized canonical historical fixture in isolated test database.
6. Verify all reader/upcaster paths and target revision registry graph.
7. Decide pure corrective upcaster, new event semantics or protected repair workflow.
8. Add canonical success/failure fixture before deploying any code change.
9. Deploy compatible reader/upcaster before enabling any related writer change.
10. Rebuild affected projection into separate version/table if historic output changed.
11. Rehearse rollback/old reader behavior with the failed revision already present.
12. Record outcome in release/audit documentation without leaking event content.

### Projection lag, poison event or duplicate processing

1. Identify projector family and checkpoint state through bounded operations data.
2. Verify whether lag is expected long transaction, capacity, claim contention or handler error.
3. Do not advance checkpoint manually past an event without named repair authorization.
4. Do not inspect payload in logs; use protected history/support access if necessary.
5. Confirm handler/upcaster version matches current release compatibility matrix.
6. Confirm duplicate handling/idempotency behavior before restarting a worker fleet.
7. Use bounded batch/cancellation controls; avoid one massive emergency replay trace.
8. For poison data, quarantine/reference according to declared repair policy.
9. Rebuild read model in a new projection version if logic/schema needs change.
10. Validate rebuilt output semantically, not only through total row count.
11. Cut over routing only under documented rollback-capable release step.
12. Retain audit evidence for manual skip/replay/repair action and purpose.

### Outbox publisher uncertainty or duplicate integration event

1. Determine whether source PostgreSQL transaction/event/outbox row committed.
2. Treat broker acceptance after a lost response as uncertain rather than absent.
3. Do not delete/reinsert source domain event to “retry publication.”
4. Inspect claim/lease/send/mark state through bounded operational tooling.
5. Redrive under declared duplicate-safe integration message identity policy.
6. Confirm consumer is idempotent and handles duplicate/out-of-order message cases.
7. Verify no external side effect was performed directly inside aggregate append.
8. Check mapping revision compatibility between domain event and integration event.
9. Do not add topic, payload, tenant or event ID as metric label while debugging.
10. Restart/release publisher only with a recorded claim/recovery state plan.
11. Reconcile any manual intervention through audit/runbook action.
12. Update fault-window regression test if observed state was not in the matrix.

### Database migration or rollback emergency

1. Freeze new writer revision feature gates before changing reader/schema assumptions.
2. Capture active binary/reader/upcaster/snapshot/projection/DDL version matrix.
3. Verify whether rollback binary can read every revision already committed.
4. If not, use forward fix or explicitly block rollback rather than deploy blind.
5. Verify PostgreSQL migration state/constraints/indexes on every relevant cohort.
6. Do not drop/rewrite historic event payload or essential compatibility column.
7. Stop/restart projections/publishers according to their checkpoint/claim contract.
8. Rehearse/execute isolated restore or synthetic replay before production destructive repair.
9. Keep archive/retention jobs paused when their assumptions no longer hold.
10. Activate only compatible reader/upcaster code before any new writer behavior.
11. Record migration/rollback actor/action/version in protected audit/release system.
12. Add a mixed-version regression fixture for every unexpected deployment condition.

### Tenant routing/isolation incident

1. Fail closed when verified scope or datasource mapping is unavailable.
2. Do not fall back to shared/default database because a tenant pool is unhealthy.
3. Use authorized control-plane diagnostics to inspect resolver mapping, not trace labels.
4. Verify cross-tenant query/predicate/datasource tests before restoring traffic.
5. Treat any possible misroute as a security incident with audit/retention process.
6. Pause affected tenant workers/projectors/publishers using explicit scope controls.
7. Preserve event history; do not move rows between tenants without migration protocol.
8. Check tenant lifecycle/suspension/migration cohort state before retrying jobs.
9. Verify backups/restore plan before correcting any stored routing/control-plane entry.
10. Remove tenant/database identity from temporary generic logs/metrics after incident.
11. Re-run shared and db-per-tenant conformance suites after resolver remediation.
12. Update threat model/runbook if a raw carrier reached store routing unexpectedly.

## First delivery backlog

### ED-01 — decisions and fixtures before package API

- Write an ADR naming the PostgreSQL-only support boundary and why generic stores are refused.
- Write an ADR choosing event envelope codec, canonical JSON/bytes and size limits.
- Write an ADR defining stream identity/version/global-position and DDL transaction strategy.
- Write an ADR defining revisions/upcaster graph/removal/release compatibility policy.
- Capture sanitized v1 event fixtures for one aggregate and expected rehydrated state.
- Capture malformed/gap/duplicate/unknown revision fixture corpus.
- Capture one canonical optimistic conflict two-session PostgreSQL integration test.
- Capture one append rollback test covering event/stream/outbox transaction state.
- Capture one pure Apply/Decide no-I/O fixture with deterministic clock/ID injection.
- Capture reference-study notes mapping eugene-khyst and Axon lessons to decisions.

### ED-02 — minimal append/load PostgreSQL implementation

- Create isolated Postgres migration for stream and event rows plus required constraints.
- Implement validated envelope/event registry and canonical codec writer/reader.
- Implement append with exact expected-version guard and live conflict classification.
- Implement ordered load with sequence integrity check and pure aggregate rehydration.
- Implement typed safe error projection without raw SQL/payload diagnostics.
- Implement caller-owned context/datasource/transaction binding integration.
- Benchmark small aggregate append/load and capture no-op operational baselines.
- Add golden DDL/payload/aggregate state fixtures into CI.
- Avoid snapshot, broker, generic subscriptions and dynamic serializer scope initially.
- Gate release on live PostgreSQL conflict/rollback/load corpus green.

### ED-03 — revision and projection correctness

- Implement explicit v1→v2 upcaster registry/path validation for one event family.
- Implement upcast golden/error/determinism fixtures and mixed stream rehydration.
- Implement one checkpointed local PostgreSQL projection with duplicate-crash test.
- Implement a projection rebuild into new version/table with validation/cutover fixture.
- Add snapshot model only when a measured stream needs it and full replay is trusted.
- Add snapshot+tail/full replay equality/corrupt snapshot corpus before enabling snapshot writes.
- Add metrics/OTel fixtures only through optional bridge after semantic registry review.
- Add protected history/replay purpose interface/reference test, no broad event endpoint.
- Add mixed v1/v2 binary release rehearsal with reader-before-writer gate.
- Gate on no payload/identity leakage across trace/metric/error/audit test scanners.

### ED-04 — outbox and production operations

- Implement outbox row in append transaction and bounded explicit publisher worker.
- Implement claim/crash/send/mark duplicate delivery test matrix.
- Define integration event schema separate from domain event revision path.
- Add consumer/projector idempotency/reference tests and documented at-least-once semantics.
- Add release compatibility manifest for reader/writer/upcaster/snapshot/projection/outbox.
- Add on-call runbooks for conflict/upcast/projection/publisher/migration/tenant incidents.
- Add archive/partition benchmarks and design only when data growth requires them.
- Add audit/tenancy/storage/i18n bridges only after their satellite contracts land.
- Run PostgreSQL restore/replay rehearsal before public beta support statement.
- Publish explicit non-goals: no other DB engine, no raw event CRUD, no hidden workers.

## Explicit anti-pattern catalogue

The following are refusal cases, not merely preferences. Each item has caused
real-world event histories to become non-replayable, non-auditable or impossible
to roll back safely. A proposed shortcut needs a new decision and equivalent
evidence before it can replace a refusal.

### History mutation anti-patterns

- `UPDATE events SET payload = ...` to correct a previous factual event.
- `DELETE FROM events` to satisfy ordinary cleanup or reduce table size.
- Reusing event type `account.updated` with incompatible new business meaning.
- Changing an old event revision label without an actual reader/upcaster path.
- Adding a default field in current aggregate code and pretending old fact had it.
- Using an SQL migration to rename/drop payload JSON fields in historic rows.
- Re-serializing every event with a new JSON library because output looks similar.
- Replacing a historic event ID with a new generated value during a migration.
- Modifying recorded timestamp to “fix” ordering after the fact.
- Changing global position/order values while exporting/restoring history.
- Removing an upcaster because hot database rows no longer show old revision.
- Removing old revision reader without testing backup/archive/restore corpus.
- Treating a snapshot as proof that preceding events can be discarded.
- Serving snapshot bytes as a user-visible audit/version endpoint.
- Writing correction directly to projection and forgetting source event history.

### Concurrency and transaction anti-patterns

- Providing an append overload with no expected stream version.
- Checking stream version in one query then inserting events in another transaction.
- Treating an expected-version conflict as an automatic retry of stale events.
- Retrying a command without rehydrating aggregate and re-running its decision.
- Retrying an uncertain post-commit append blindly because response timed out.
- Using `max(stream_version)+1` without unique constraints/transaction strategy.
- Assuming an in-memory lock proves multi-process/PostgreSQL concurrency behavior.
- Opening a new database transaction in an event decorator despite caller tx scope.
- Binding event store to wrong datasource and falling back to any available pool.
- Storing cross-aggregate consistency rule in a single aggregate by hidden global query.
- Holding huge/replay transaction open while performing remote I/O or waiting broker.
- Treating PostgreSQL serialization failure as a user-visible optimistic conflict.
- Swallowing a deadlock/error and returning successful command result because event list exists.
- Inserting events/outbox separately and trusting a process-local sequence to reconcile.
- Letting a request context cancellation trigger unbounded background append retry.

### Aggregate and handler anti-patterns

- Calling HTTP, broker, email, storage or database from aggregate `Apply`.
- Reading current time, random values or environment inside `Apply`.
- Generating UUIDs in replay/upcaster rather than carrying deterministic value.
- Calling a projector/notification from `Decide` before append commits.
- Keeping mutable global aggregate state keyed by stream ID.
- Reflecting arbitrary Go type name into event type/revision registry.
- Letting unknown historic event become a no-op to keep service available.
- Applying only events a current handler recognizes and dropping rest silently.
- Returning partially rehydrated aggregate after decode/upcast failure.
- Making aggregate decision mutate a SQL/read model before event append succeeds.
- Modeling every CRUD row as an aggregate without a consistency/history demand.
- Using generic map payload in domain handler without schema/type validation.
- Treating application error text/localized message as a stable event fact.
- Writing whole aggregate/current state as every event “for convenience.”
- Hiding business compensation in an upcaster instead of a new domain event.

### Projection and outbox anti-patterns

- Advancing checkpoint before projection model writes commit.
- Marking event processed only in memory/cache.
- Skipping a poison event without an authorized/audited repair decision.
- Assuming broker partition ordering equals aggregate/global PostgreSQL order automatically.
- Calling external API directly from projection handler with checkpoint transaction.
- Marking outbox row sent before broker acceptance/reliable handoff evidence.
- Treating broker acceptance as consumer completion or exactly-once end-to-end.
- Deleting outbox rows before duplicate/retention/reconciliation policy allows it.
- Publishing raw domain event payload as integration event across bounded contexts.
- Using event type/revision as broker topic name derived from tenant/customer input.
- Rebuilding live projection table in place while users query it.
- Switching projection routing before semantic validation of rebuilt model.
- Keeping one unbounded trace/span from initial append through days of replay.
- Emitting checkpoint/global position/event ID as high-cardinality metric labels.
- Running hidden publisher/projector goroutine from event store constructor.

### Release and versioning anti-patterns

- Enabling a new writer revision before every active reader/upcaster is compatible.
- Rolling back binary after new revision append without a reader compatibility check.
- Declaring release complete when pods update but repair tools/projectors remain old.
- Bundling DDL contraction with code that still requires old columns/indexes.
- Changing snapshot schema without old/new snapshot reader/discard test.
- Treating code version, event revision and stream sequence as one version number.
- Deleting v1 fixtures after v2 rollout because production has few old rows.
- Testing only a fresh database rather than historical mixed revision fixture.
- Treating an emergency data correction as authorization for historic SQL mutation.
- Reusing a migration name/version for a changed DDL operation.
- Removing event reader support without restore/archive/backup rehearsal.
- Adding a new event type to a library without release manifest/documented owner.
- Making a serializer dependency upgrade invisible to event compatibility review.
- Assuming one tenant database cohort upgrade proves all database-per-tenant fleet safe.
- Publishing “Postgres event sourcing supported” without live target/version report.

### Security, tenancy and observability anti-patterns

- Taking tenant ID from a request header as direct event-store routing input.
- Encoding tenant name/ID in a dynamic stream type or metric label.
- Falling back to a default database when tenant resolver cannot select one.
- Exposing raw stream/event tables through generic list/filter REST endpoint.
- Logging event payload/raw SQL/raw driver errors to debug an upcaster failure.
- Including aggregate ID, event ID, expected version or checkpoint in default OTel fields.
- Copying trace baggage/header map into event metadata/outbox persistence.
- Assuming a sampled trace is a durable audit record of command/event action.
- Assuming an event stream alone contains verified actor/authorization evidence.
- Copying entire event payload into audit row without a redaction/purpose policy.
- Localizing event type/revision/integration contract/aggregate keys.
- Storing pre-signed storage URL/credentials as a historic event convenience field.
- Treating stored event payload encryption as a generic boolean without key/replay plan.
- Allowing arbitrary history export without purpose, range, tenant and audit guard.
- Using a deletion request to purge event history without legal/retention design.

## Evidence package for a public beta

Before calling the PostgreSQL event satellite beta-ready, attach a reviewable
evidence package that contains every artifact below. A green unit test suite is
only one row of the package.

### Design evidence

- PostgreSQL-only ADR with explicit rejected generic database abstraction.
- Stream/event/global-position/outbox/snapshot/checkpoint DDL and index rationale.
- Codec canonicalization, payload limits and envelope metadata manifest.
- Aggregate boundary/purity and command/retry/idempotency composition documentation.
- Event revision/upcaster graph specification and historic fixture inventory.
- Projection/checkpoint/duplicate/rebuild/cutover design with transaction boundaries.
- Integration event/outbox delivery and at-least-once/consumer idempotency declaration.
- Tenancy one-db/db-per-tenant routing/constraint/resolver integration boundary.
- Audit action/revision/history-access/redaction/retention boundary.
- OTel semantic registry/privacy/cardinality/link topology mapping.
- Security/payload/retention/archival/restore policy and named owners.
- Release choreography, rollback gates and explicit initial non-goals.

### Automated evidence

- Pure aggregate Apply/Decide fixture suite with no I/O/time/random side-effect spy.
- Codec determinism, malformed, limits and event registry validation corpus.
- Live PostgreSQL append/load/sequence/conflict/rollback/deadlock/serialization suite.
- Snapshot full replay equality/corrupt/mixed-revision/rollback suite if snapshots ship.
- Upcaster graph/path/determinism/golden/malformed/mixed historic stream suite.
- Projection duplicate/crash/checkpoint/rebuild/cutover/local Postgres suite.
- Outbox claim/crash/send/mark/duplicate consumer/fault-window suite if outbox ships.
- Tenant shared/db-per-tenant missing/wrong/cross-scope isolated integration suite.
- Audit transactional/no-sampling/privacy purpose/access suite if audit bridge ships.
- OTel no-op/sampled/privacy/cardinality/topology exporter-free suite if bridge ships.
- Mixed reader/writer/upcaster/snapshot/projection release rehearsal against Postgres.
- Restore/archive/replay drill or an explicit documented pre-beta deferral.

### Operational evidence

- Supported PostgreSQL/driver version matrix and dated live integration report.
- Append/load/project/upcast/outbox benchmark baselines at representative volume.
- Capacity/index/query-plan/transaction-duration measurements and alert ownership.
- Backup/restore and historical replay rehearsal result with safe test data.
- Incident runbooks for conflict, upcast, projection, outbox, migration and tenant routing.
- On-call owner and maintenance worker startup/shutdown/credential lifecycle documentation.
- Dashboard/alert queries using only approved bounded semantic fields.
- Security review confirming no payload/identity/raw query leakage in normal telemetry/logs.
- Release/rollback operator checklist with a feature-gated writer revision control.
- Test fixture process documenting use of eugene-khyst/Axon references and deviations.

## Worked release rehearsal — `account.credited` v1 to v2

This is a generic rehearsal pattern, not a domain schema to copy. It makes the
separate version axes visible and gives a minimum test sequence for any payload
evolution that requires a new event revision.

### Initial state

```text
event type: account.credited
event revision: 1
stream versions: arbitrary per account
aggregate reader: v1
writer: v1
snapshot revision: 1
projection: accounts.balance_view/v1
integration contract: accounts.balance_changed/v1
```

Revision 2 adds a required explicit currency representation that cannot safely
be inferred from every historic event without a declared deterministic rule. The
release must not call this merely “adding a field”; old bytes will outlive every
rolling deployment and backup.

### R0 — classify and decide

1. Confirm the changed payload meaning is an event revision, not only code refactor.
2. Name `account.credited` source revision `1` and target revision `2` explicitly.
3. Write deterministic v1→v2 upcaster rule and all exceptional/malformed cases.
4. Decide whether existing historic v1 fields supply currency or a compensating fact is needed.
5. Decide whether integration event remains v1 or needs independent external version.
6. Decide whether projection v1 can consume v2/current upcast form or requires v2 rebuild.
7. Decide snapshot v1 compatibility/discard/rebuild behavior under v2 reader.
8. Decide rollback point after which old v1-only binary is prohibited.
9. Update event manifest, owner, security/payload and retention documentation.
10. Add initial release matrix with all currently deployed readers/writers/tools.

### R1 — fixtures and pure reader path

1. Add canonical v1 valid payload fixture with expected v2 transformed form.
2. Add v1 malformed/missing/invalid currency field fixture with safe failure outcome.
3. Add v2 valid writer payload fixture and decoder round-trip test.
4. Add mixed stream fixture containing v1/v2 events at contiguous versions.
5. Add full replay expected aggregate state/version for mixed stream.
6. Add projection expected output for mixed stream after upcast.
7. Add snapshot v1 + v1/v2 tail expected aggregate state fixture.
8. Run upcaster twice in pure environment and compare canonical output exactly.
9. Assert upcaster does not access time, DB, locale, HTTP, random or storage spy.
10. Assert original stored v1 fixture bytes/revision remain unchanged after reader run.

### R2 — deploy compatible readers first

1. Deploy code that registers v1 decoder/upcaster path to v2 current handler.
2. Keep writer feature gate emitting v1 while validating reader deployment.
3. Deploy same compatible reader into services, projectors, repair CLI and workers.
4. Deploy snapshot reader that reads/discards v1 snapshot safely and writes no v2 yet.
5. Verify old archived/backup fixture restores and loads through deployed reader.
6. Verify live Postgres can load historic v1 streams with current reader.
7. Verify OTel/audit/error output remains payload/identity safe under v1 load.
8. Verify no external integration mapper starts publishing changed schema accidentally.
9. Verify all release cohort health checks report compatible reader version safely.
10. Do not enable v2 writer merely because a subset of request pods updated.

### R3 — mixed-reader and rollback rehearsal

1. Start one v1-only reader fixture and one v1+v2 reader fixture against same DB.
2. Append only v1 events and prove both rehydrate/project correctly.
3. Confirm rollback binary/tool manifest explicitly declares its maximum readable revision.
4. Confirm a v1-only binary cannot be selected as rollback once v2 writing begins.
5. Test deployment rollback before writer gate: it should load all existing v1 history.
6. Test in-progress projection/replay jobs pin/upgrade compatible reader version safely.
7. Test tenant cohort with a lagging reader does not receive v2 writer feature inadvertently.
8. Test snapshot v1 cleanup/write remains compatible in mixed reader phase.
9. Test telemetry/dashboards distinguish reader release version without payload labels.
10. Record rehearsal outcome and unresolved tool/consumer compatibility gaps.

### R4 — enable v2 writer deliberately

1. Enable v2 writer feature only in an approved cohort after all reader gates pass.
2. Append a v2 event through live PostgreSQL expected-version transaction fixture.
3. Load same stream through v2 reader and validate aggregate state/version.
4. Project same v2 event through current projector and validate output/checkpoint.
5. Map the domain event to unchanged or independently versioned integration event.
6. Validate outbox crash/duplicate behavior has no payload/version leakage.
7. Validate snapshot policy does not create unreadable state for deployed recovery tools.
8. Validate an old v1-only reader fails visibly in controlled test—not silently wrong.
9. Mark binary rollback boundary in deployment/runbook after first v2 commit.
10. Audit writer-gate activation/schema release under protected release policy.

### R5 — projection and snapshot evolution

1. If projection schema/meaning changes, create `accounts.balance_view/v2` separately.
2. Rebuild v2 projection from zero/mixed history using same declared upcaster path.
3. Compare v1/v2 projection outputs according to documented changed business semantics.
4. Validate v2 table indexes/query policy and tenant scope before routing cutover.
5. Switch query routing atomically/explicitly only after validation and rollback plan.
6. Keep v1 projection/read route until cutover support/rollback window ends.
7. Introduce snapshot v2/v3 only with snapshot+tail/full replay equality fixtures.
8. Test old snapshots in v2 reader: read/upcast/discard/rebuild rule is documented.
9. Do not delete v1 events or readers just because v2 projection/snapshot exists.
10. Record rebuild/cutover version and operator evidence in release artifact.

### R6 — retirement and archival gate

1. Scan active event store for retained v1 rows under authorized operational process.
2. Include archives, backups, disaster restore and offline repair fixtures in review.
3. Include long-lived tenant cohorts and old projection/checkpoint/snapshot artifacts.
4. Include integration consumers/replay tooling that may need v1 interpretation.
5. Confirm support/rollback window has ended and no allowed binary is v1-only.
6. Confirm v1 reader removal will cause a clear controlled failure if unexpected data returns.
7. Keep original v1 rows immutable even after upcaster/reader code retirement decision.
8. Remove only code/path per documented release, never historic event payload data.
9. Update manifest/release notes/archive reader documentation and on-call runbook.
10. Preserve canonical v1 fixture in regression suite after runtime path removal.

## Event evolution decision table

| Proposed change | Correct first question | Usual safe action | Never do |
|---|---|---|---|
| rename Go field only | wire bytes changed? | code refactor if no | change event type casually |
| add optional payload field | old reader behavior? | additive revision or tolerant codec proof | assume JSON default is semantic |
| add required field | can old fact derive it purely? | v2 + upcaster/compensating rule | update old rows |
| split one fact into two | historic ordering/meaning? | explicit upcaster split with fixture | generate new clock/IDs |
| merge facts | does aggregate state retain meaning? | explicit current reader/projection plan | silently discard fact |
| correct wrong business fact | what actually happened? | compensating/correction event | mutate payload |
| change external integration payload | domain vs external contract? | independent integration version mapper | publish raw domain bytes |
| change snapshot serialization | event history unchanged? | snapshot revision/rebuild | call it event migration |
| change read model | rebuild/cutover required? | new projection version | update live table blindly |
| change tenant topology | routing/readers compatible? | cohort migration and resolver tests | embed tenant in event name |
| change codec library | canonical bytes still same? | golden fixture/revision review | assume patch upgrade safe |
| add encryption/redaction | historic recovery policy? | dedicated ADR/workflow | boolean option/SQL rewrite |
| archive old data | readers/restores need it? | archive/restore proof | partition drop by age |
| remove upcaster | all retained sources gone? | evidence + fixture preservation | scan hot table only |
| roll back code | any new event committed? | reader matrix/forward fix | deploy old binary blindly |

## Initial non-goals and refusal boundary

- EventStoreDB, Kafka, MongoDB, filesystem or another RDBMS event-store adapter.
- A generic database-neutral event-store interface that erases Postgres transaction semantics.
- Direct event payload CRUD, search/filter/list endpoints for ordinary clients.
- Automatic aggregate discovery/reflection/annotation-driven event registration.
- Automatic serializer evolution, in-place migration or “best effort” unknown event skip.
- Blind command/event append retry after optimistic conflict or uncertain commit.
- Exactly-once broker delivery or external side effect guarantee.
- A generic workflow/saga/Temporal/NATS engine embedded in event-store core.
- A global background publisher/projector started by `New`.
- A universal snapshot/partition/archive/retention implementation without measured need.
- Arbitrary per-event metadata/header/baggage/tenant/locale propagation map.
- Event payload/audit/trace copy as an automatic support/debugging convenience.
- Generic field-level GDPR/crypto deletion implementation with false immutability claims.
- Localized event names or use of event source as an i18n/CMS/audit substitute.
- Database-per-tenant resolver/identity provider/credential manager inside event core.

## Completion definition for PostgreSQL event-source roadmap

This roadmap is implementation-ready only when its first-release scope can prove:

1. PostgreSQL is the sole durable backend and is named in module/DDL/test support.
2. Append/load event semantics, expected version, sequence/global order and error taxonomy are decided.
3. One real aggregate, historic fixture corpus and live two-session Postgres conflict suite exist.
4. Upcaster/revision graph, mixed reader/writer release rehearsal and rollback boundary exist.
5. Projection/checkpoint/outbox behavior is either tested first scope or explicitly deferred.
6. Snapshots, audit, tenancy, storage, i18n and OTel bridges each have directional contracts/deferrals.
7. Payload/identity/privacy/retention and history-access policies are explicit and testable.
8. Reference implementation study is captured as choices/tests, not a Java/Spring API clone.
9. DDL/migration/restore/archive/release/on-call evidence is assigned and versioned.
10. Every item in this roadmap that affects a historic fact has a no-in-place-mutation invariant.

## Daily engineering review workbook

Use this workbook for every package, migration, handler and operational change.
It deliberately repeats critical decisions in short form so a review can stop a
bad change before a long implementation makes the historic data contract costly
to unwind.

### Before adding an event

- Name the aggregate consistency boundary and why a current-state update is insufficient.
- Name the stable past-tense event fact, not a command/UI label.
- Name event type owner, domain audience and sensitive-field policy.
- Name revision `1` payload schema and canonical encoding rules.
- Name aggregate Apply rule and prove it is pure/deterministic.
- Name command Decide rule and expected state/version preconditions.
- Name whether zero/multiple events are possible and their order semantics.
- Name stream identity/type and tenant partition/routing policy if applicable.
- Name payload size/metadata limits and what data must never be stored.
- Name snapshot need as measured optimization or explicitly defer it.
- Name projection/read-model consumer or explicitly defer query requirements.
- Name external integration mapper/outbox need or explicitly defer it.
- Name audit action/evidence need or explicitly defer it.
- Name OTel logical operation/outcome vocabulary without payload identity.
- Name test fixtures for valid, invalid, conflict and historic replay behavior.

### Before changing an event

- Compare old/new wire bytes and semantic meaning, not only Go struct fields.
- Identify every existing revision in hot store, backup, archive and test fixture.
- Decide additive tolerance, upcaster path, new type or compensating event.
- Write golden source/target/malformed fixtures before changing a writer.
- Confirm upcaster is pure and needs no current database/profile/service lookup.
- Confirm aggregate and projection current handlers agree on transformed meaning.
- Confirm integration mapper external contract change is analyzed separately.
- Confirm snapshot reader/rebuild behavior for old/new event form.
- Confirm all active reader/projector/repair/rollback binaries support it.
- Confirm writer feature flag remains disabled until compatible reader deployment.
- Confirm tenant cohort migration and database topology status if relevant.
- Confirm telemetry/audit vocabulary stays bounded and does not expose new fields.
- Confirm event retention/privacy/legal owner has reviewed sensitive semantic change.
- Confirm release note explains rollback boundary after first new event commit.
- Confirm no migration proposes SQL update/delete of historic event payload.

### Before adding a projection

- Name projection logical purpose, owner and query audience.
- Name source event types/revisions/current upcaster policy.
- Name initial checkpoint/global-order position and tenant scope behavior.
- Name exact idempotency key or duplicate-safe model update mechanism.
- Name transaction relationship between read model and checkpoint.
- Name failure/retry/poison/quarantine/manual-skip policy.
- Name batch size, claim/lease and cancellation/backpressure behavior.
- Name whether external side effects are forbidden or routed through outbox.
- Name rebuild target table/version and validation/cutover/rollback procedure.
- Name read access policy and how vv CRUD/security helpers apply to model.
- Name metrics/traces using finite projection/outcome/batch vocabulary only.
- Name audit/purpose policy for manual replay/rebuild/admin repair actions.
- Name schema migration expand/contract order with old runtime compatibility.
- Name integration test for crash before/after checkpoint commit.
- Name a fixture that proves full replay yields the expected projection model.

### Before enabling an outbox publisher

- Name outbox row schema and transaction relation to source event append.
- Name publisher startup/shutdown owner and explicit host configuration.
- Name claim/lease/order/batch/backpressure and expiry/recovery semantics.
- Name broker adapter/module dependency boundary and credential/client owner.
- Name integration event type/revision/schema/consumer compatibility owner.
- Name message identity and producer/consumer duplicate/idempotency contract.
- Name send/ack/mark crash windows and expected at-least-once behavior.
- Name dead-letter/manual-redrive/retention/audit policy.
- Name carrier propagation trust/byte/baggage limit and link/parent topology.
- Name sensitive payload omission/redaction and external consumer authorization rule.
- Name observability fields/metrics and prohibited topic/endpoint/event ID labels.
- Name integration test for broker acceptance then process crash before sent mark.
- Name integration test for consumer duplicate/out-of-order message handling.
- Name rollback behavior when mapper/writer/schema version changes.
- Name explicit non-goal if exactly-once or ordering cannot be proven.

### Before a PostgreSQL migration

- Name schema version, target Postgres versions and deployment owner.
- Name every writer/reader/projector/publisher/snapshot/tool affected.
- Add columns/indexes/constraints before code begins depending on them.
- Measure lock/transaction duration and plan cancellation/retry/maintenance window.
- Prove constraint behavior with live Postgres negative tests.
- Prove existing event rows/payloads remain unmodified and readable.
- Prove expand phase works with old binaries and contract phase is delayed.
- Prove database-per-tenant cohort sequencing/control-plane status if used.
- Prove shared-db RLS/tenant query session settings remain correct under pooling if used.
- Prove backup/restore/replay fixture before destructive/index/partition transition.
- Prove cleanup/drop target exactness and recoverability before destructive operation.
- Prove query plans/indexes for stream load/global projection remain acceptable.
- Prove migration error mapping/logging does not expose SQL or event payload data.
- Prove rollback/forward-fix choice after partial migration is documented.
- Prove schema manifest/release compatibility matrix receives the new migration state.

### Before a production release

- Freeze/update event reader/writer/upcaster compatibility matrix.
- Freeze/update snapshot/projection/outbox/integration compatibility matrix.
- Freeze/update Postgres DDL/driver/version/tenant cohort compatibility matrix.
- Run pure aggregate, codec and upcaster golden fixture suite.
- Run live Postgres append/load/conflict/rollback/sequence suite.
- Run projection/outbox crash/duplicate suite for each enabled component.
- Run OTel/audit/tenancy/privacy bridge equivalence suite for each enabled bridge.
- Run old/new/mixed reader/writer binary rehearsal against historic fixture database.
- Run rollback scenario after a new revision event commit where writer changes.
- Run restore/replay/archive reader scenario or record explicit pre-beta deferral.
- Capture performance baselines/capacity/lag/transaction duration evidence.
- Verify dashboards/alerts/runbooks use approved bounded vocabulary.
- Verify release/audit owner and emergency repair contact/purpose path.
- Verify all deferred features/non-goals remain visible to consumer documentation.
- Do not deploy if any required reader/tool/cohort compatibility evidence is absent.

## Sign-off roles and questions

| Role | Must answer before release |
|---|---|
| domain owner | Is this event a true immutable fact and is aggregate boundary correct? |
| PostgreSQL owner | Do DDL/isolation/index/backup/restore prove append/load safety? |
| event maintainer | Does registry/upcaster/release matrix preserve every retained revision? |
| projection owner | Are duplicates/checkpoints/rebuild/cutover/queries correct and recoverable? |
| integration owner | Is outbox at-least-once and consumer schema/idempotency contract explicit? |
| tenancy owner | Are shared/db-per-tenant scope/routing/cohort cases fail-closed? |
| audit/privacy owner | Are actor/history/payload/retention/purpose boundaries lawful and protected? |
| observability owner | Are traces/metrics useful, bounded and free of event/tenant/payload identity? |
| release manager | Is writer enablement/rollback boundary rehearsed with every active tool? |
| on-call owner | Can incident responders repair without historical SQL mutation or data leakage? |

## Fast refusal questions

Stop implementation and return to a decision when any answer is “yes”:

- Does this need another durable backend beside PostgreSQL?
- Does this require an append without expected version?
- Does this mutate, delete or normalize historic payload in place?
- Does this upcaster need current external data, time, locale or I/O?
- Does this retry stale proposed events after a conflict/uncertain commit?
- Does this publish/broker-call during append transaction or aggregate Apply?
- Does this call duplicate delivery exactly once without a demonstrated idempotency boundary?
- Does this use a snapshot/projection as a reason to throw away event history?
- Does this put a tenant, user, event ID, payload or raw SQL into telemetry metric labels?
- Does this use a translated message/type or storage URL as an event identity?
- Does this make a generic raw event history endpoint available to ordinary clients?
- Does this assume a database-per-tenant resolver can fall back to default DB?
- Does this add a background worker/global provider implicitly at package creation?
- Does this introduce a serializer/DDL/reader change without mixed-version replay tests?
- Does this call a reference Java framework API the Go satellite does not need?

If yes, the change is not an ordinary implementation task. It needs a new
contract/ADR, a bounded satellite boundary, a security/retention review and the
corresponding live PostgreSQL/release evidence before it is safe to continue.

## Compact end-to-end scenario register

### ES-01 — first aggregate open

**DX.** `Append` takes stream type/ID and expected version zero explicitly.

**Action.** Decide/open one aggregate and append its first event in PostgreSQL.

**Happy.** One stream/event/version/outbox state commits atomically.

**Edge.** Concurrent first open yields one success and one typed conflict.

**Evidence.** Live two-session query confirms no duplicate stream/version row.

### ES-02 — ordinary aggregate change

**DX.** Load returns current state/version to command handler only.

**Action.** Load v3 stream, decide two events, append expected v3.

**Happy.** Versions 4/5 commit contiguous and rehydrate expected state.

**Edge.** Second writer advances stream first; stale events never append.

**Evidence.** Full replay/transaction rollback fixture compares database rows.

### ES-03 — malformed historical payload

**DX.** Registry owns decoder path, no caller raw JSON parser.

**Action.** Load a stream containing deliberately invalid historic payload bytes.

**Happy.** Valid control stream loads normally.

**Edge.** Invalid stream fails safe before partial aggregate/side effect.

**Evidence.** Error/trace scanner finds no payload/SQL in exported output.

### ES-04 — historic v1 to v2 upcast

**DX.** Register v1→v2 pure function in explicit graph.

**Action.** Load mixed v1/v2 stream with current aggregate handler.

**Happy.** State/version equal approved current fixture.

**Edge.** Missing/ambiguous graph path blocks load at bootstrap/runtime safely.

**Evidence.** Stored v1 row bytes/revision remain byte-identical after run.

### ES-05 — upcaster malformed input

**DX.** Upcaster returns typed safe failure, not log-and-skip API.

**Action.** Feed one malformed v1 event into aggregate/projector fixture.

**Happy.** Valid v1 control transforms deterministically.

**Edge.** Failure leaves source history/checkpoint state at known safe boundary.

**Evidence.** Repair runbook/fixture requires no historic row update.

### ES-06 — snapshot tail

**DX.** Snapshot policy declares version/codec/rebuild behavior.

**Action.** Load valid snapshot v100 plus events 101–105.

**Happy.** Result equals full replay v1–105 state/version.

**Edge.** Corrupt/wrong snapshot discards/fails per policy without partial state.

**Evidence.** Snapshot+tail/full replay equality suite runs in CI.

### ES-07 — commit uncertainty

**DX.** Command/idempotency layer owns post-commit response-loss reconciliation.

**Action.** Drop client connection after database commit outcome window.

**Happy.** Normal response control returns event result once.

**Edge.** Uncertain case is not blindly re-appended with stale expected version.

**Evidence.** Query/idempotency fixture verifies exactly documented durable state.

### ES-08 — serialization/deadlock distinction

**DX.** Error type distinguishes Postgres retryable failure from stream conflict.

**Action.** Inject serialization/deadlock condition in live transaction fixture.

**Happy.** Expected-version conflict control remains domain concurrency outcome.

**Edge.** DB failure does not return a misleading stale-state business conflict.

**Evidence.** Caller retry test rehydrates/re-decides under context budget.

### ES-09 — synchronous projection

**DX.** Projection declares same-transaction behavior explicitly.

**Action.** Append event and update local read model in one transaction.

**Happy.** Commit exposes both event/model; rollback exposes neither.

**Edge.** Projection insert failure rolls back append where that guarantee is claimed.

**Evidence.** Live Postgres commit/rollback visibility matrix passes.

### ES-10 — asynchronous projection duplicate

**DX.** Projector has named idempotency/checkpoint strategy.

**Action.** Crash after model update before checkpoint/ack and restart worker.

**Happy.** Final read model equals once-applied expected state.

**Edge.** Duplicate causes no external action or model corruption.

**Evidence.** Crash/restart fixture records exact checkpoint/model transitions.

### ES-11 — projection poison event

**DX.** Runner exposes pause/quarantine/repair policy, not silent skip.

**Action.** Make handler/upcaster fail permanently on one position.

**Happy.** Valid preceding events/checkpoint state remain correct.

**Edge.** Failure cannot advance past poison event without authorized action.

**Evidence.** Manual repair audit/purpose fixture and runbook are exercised.

### ES-12 — projection rebuild cutover

**DX.** New projection version has a distinct table/name and routing switch.

**Action.** Rebuild v2 from zero while v1 serves reads.

**Happy.** Validated v2 switches only after full expected output proof.

**Edge.** Failed rebuild leaves v1 user query path intact.

**Evidence.** Cutover/rollback integration fixture compares routing/model outputs.

### ES-13 — transactional outbox commit

**DX.** Append request explicitly names integration mapping/outbox intent.

**Action.** Commit append, stream advance and outbox row together.

**Happy.** Publisher sees row only after commit.

**Edge.** Event/outbox/projection insert failure rolls all state back.

**Evidence.** Live transaction query confirms no rolled-back publish candidate.

### ES-14 — publisher duplicate after send

**DX.** Publisher/consumer contract states at-least-once and message identity.

**Action.** Crash after broker acceptance before sent marker update.

**Happy.** Normal send/mark control progresses to sent lifecycle state.

**Edge.** Restart may duplicate; consumer remains idempotent.

**Evidence.** Fault test sees two delivery attempts and one correct consumer result.

### ES-15 — integration schema isolation

**DX.** Mapper has independently versioned external envelope registry.

**Action.** Change internal domain event v1 to v2 without external contract change.

**Happy.** External v1 consumer fixture continues to decode expected message.

**Edge.** Mapper rejects accidental raw internal field/payload leakage.

**Evidence.** Schema/privacy golden fixtures cover both source revisions.

### ES-16 — tenant shared database

**DX.** Verified tenant scope decorates store before stream operation.

**Action.** Append/load same aggregate ID under tenants A and B in one DB.

**Happy.** Each sees only its own independent stream/history.

**Edge.** Missing/crafted scope executes zero/unscoped SQL.

**Evidence.** Cross-tenant guessed-ID/projection/replay query matrix passes.

### ES-17 — tenant database per tenant

**DX.** Resolver selects caller-owned datasource from verified scope.

**Action.** Append/load equal stream ID under two tenant DB fixture stores.

**Happy.** Both aggregate semantics/conformance results match shared topology.

**Edge.** Resolver failure never falls back to another/default database.

**Evidence.** Wrong-mapping/pool lifecycle integration test remains fail closed.

### ES-18 — event append audit revision

**DX.** Audit bridge declares actor/action/resource/subject capture policy.

**Action.** Append an authorized event inside transactional audit mode.

**Happy.** Event and protected audit revision commit together.

**Edge.** Audit failure prevents committed action where policy claims atomicity.

**Evidence.** Unsampled/no-OTel fixture proves audit remains complete.

### ES-19 — event OTel privacy

**DX.** OTel bridge maps stream to configured family and bounded outcome.

**Action.** Append/load payload/ID/tenant sentinel fixtures under sampled provider.

**Happy.** Logical spans show append/load/conflict timing/outcome.

**Edge.** No sentinel/event ID/version/checkpoint becomes signal attribute.

**Evidence.** Golden export/cardinality/no-op equivalence test passes.

### ES-20 — release rollback gate

**DX.** Release manifest names reader/writer/upcaster/snapshot/projector versions.

**Action.** Commit first v2 event then attempt old binary rollback fixture.

**Happy.** Pre-v2 rollback control remains permitted/readable.

**Edge.** Post-v2 rollback is blocked or uses declared compatible reader/forward fix.

**Evidence.** Live mixed-binary Postgres rehearsal is attached to release.

### ES-21 — no-op append result

**DX.** Command result names no-event/no-op state explicitly.

**Action.** Decide command against state where requested fact already holds.

**Happy.** No append/outbox row is created and result has documented no-op state.

**Edge.** Caller cannot send empty batch through ordinary Append accidentally.

**Evidence.** Audit/telemetry semantics distinguish no-op from failed append.

### ES-22 — aggregate type collision

**DX.** Stream identity includes validated aggregate type plus opaque ID.

**Action.** Attempt load/append account and order with identical raw ID.

**Happy.** Separate type controls remain independently readable.

**Edge.** Wrong type against existing stream fails, never merges histories.

**Evidence.** DB constraint and load registry tests prove isolation.

### ES-23 — payload size rejection

**DX.** Envelope builder exposes documented per-event/batch size limits.

**Action.** Append below-limit and over-limit payload fixtures.

**Happy.** Below-limit event commits/reloads normally.

**Edge.** Over-limit request fails before transaction and no bytes leak in error.

**Evidence.** Memory/trace/privacy corpus confirms bounded behavior.

### ES-24 — metadata secret rejection

**DX.** Envelope metadata schema is closed and typed.

**Action.** Add authorization/token/header/baggage sentinel to metadata.

**Happy.** Approved safe correlation metadata control validates.

**Edge.** Secret/free-map form is refused before encode/store/outbox.

**Evidence.** Event/audit/OTel export scan finds no sentinel.

### ES-25 — archive restore replay

**DX.** Archive reader/restore operation has named authorized purpose/runbook.

**Action.** Restore sanitized historic partition containing v1 events.

**Happy.** Current reader/upcaster rehydrates expected state/projection.

**Edge.** Missing old reader/upcaster blocks retirement/restoration safely.

**Evidence.** Restore rehearsal artifact records source/version/checksum/result.

### ES-26 — archive retention legal hold

**DX.** Archive job receives explicit retention/hold policy, not age-only delete.

**Action.** Attempt archive/purge on a held historic range fixture.

**Happy.** Eligible control range follows verified immutable archive workflow.

**Edge.** Held range remains and records bounded policy refusal.

**Evidence.** Audit/runbook confirms no generic partition drop escaped policy.

### ES-27 — command idempotency mismatch

**DX.** Idempotency interface stores canonical request identity/result policy.

**Action.** Repeat key with same request then different request fixture.

**Happy.** Same request returns one documented prior/current result.

**Edge.** Different request with same key is conflict, never prior event replay.

**Evidence.** No raw request/key becomes telemetry/audit generic payload.

### ES-28 — context cancellation during replay

**DX.** Load/project/rebuild APIs accept ordinary caller context.

**Action.** Cancel while a bounded historic batch is decoding/upcasting.

**Happy.** Short control batch completes normally.

**Edge.** Canceled operation returns no partial aggregate/checkpoint as completed.

**Evidence.** Retry/resume uses documented durable checkpoint/load policy.

### ES-29 — source driver error no guessing

**DX.** Error mapper receives observed Postgres driver error types/codes only.

**Action.** Inject unknown SQLSTATE/raw driver text fixture.

**Happy.** Known expected conflict/serialization controls classify correctly.

**Edge.** Unknown error stays bounded internal/unavailable, not guessed domain type.

**Evidence.** Raw SQL/message sentinel is absent from public/OTel outputs.

### ES-30 — support history access range limit

**DX.** Protected reader requires purpose and maximum stream/range/page limits.

**Action.** Authorized tool requests small and enormous historic ranges.

**Happy.** Small approved control retrieves protected data under audit policy.

**Edge.** Large/unscoped request fails/batches without broad data extract.

**Evidence.** Access/audit/tenant bound fixture proves zero ordinary endpoint leak.

## Final Postgres-only boundary reminders

- PostgreSQL transaction semantics are a product feature, not an adapter detail.
- Expected version is a mandatory precondition, not a tuning option.
- A committed event is immutable; correction is a new fact, not a row edit.
- Stream version, event revision, snapshot revision and projection version are distinct.
- Upcasters make readers compatible; they never make historic bytes current by mutation.
- A snapshot speeds replay; it never displaces append-only history.
- A projector can be duplicated; its checkpoint/model contract must survive that.
- An outbox gives atomic intent and at-least-once publication, never magical exactly once.
- A broker is an optional external dependency, never a Postgres event store replacement.
- A tenant scope must be verified before event routing/query, never parsed from a raw carrier.
- An audit revision has actor/purpose evidence; it is not the aggregate source of truth.
- A trace gives transient diagnostics; it is not an audit/event retention system.
- A locale renders a UI message; it never changes event type, schema or replay state.
- Storage holds bytes through its own lifecycle; event payload holds logical domain facts/references.
- Axon is a source of upcaster/revision lessons; it is not vv's Java API blueprint.
- eugene-khyst is the main practical Postgres template; its choices still require Go/Postgres evidence.
- A release is safe only when old/new readers, writers, tools, snapshots and projections agree.
- A rollback is safe only before/after explicitly rehearsed writer boundaries.
- Archive/restore are history operations with reader/retention proof, not table maintenance shortcuts.
- Every automatic convenience that hides one of these facts is a candidate refusal.

## Final implementation order

1. Establish DDL/codec/aggregate fixture/expected-version live PostgreSQL baseline.
2. Establish reader/upcaster graph and v1→v2 mixed release rehearsal.
3. Establish checkpointed projection with duplicate/crash/rebuild fixture.
4. Establish outbox only with explicit at-least-once publisher/consumer evidence.
5. Establish audit/tenancy/OTel/storage/i18n bridges as separate directional satellites.
6. Establish operations: backups, restore/replay, benchmarks, incident/runbook/release matrix.
7. Add snapshots, partitions, archive, extra aggregate/product breadth only with measured demand.

No stage can be skipped by adding a framework abstraction or a second storage
engine. The quality gate is evidence that a historic fact remains readable,
ordered, authorized, replayable and release-compatible after the next failure.

## Reference-study checklist — what to take and what to leave

### From `eugene-khyst/postgresql-event-sourcing`

- Study the separated PostgreSQL core versus application-domain project layout.
- Study its event/aggregate table relation and version-check append transaction.
- Study its explicit optimistic-concurrency explanation and live competing writer behavior.
- Study its snapshot configuration and load-from-snapshot-plus-tail explanation.
- Study its distinction between write aggregate stream and query projections.
- Study its synchronous projection option in the same database transaction.
- Study its transactional outbox rationale for asynchronous integration events.
- Study its alternative publisher/notification/polling operational tradeoffs.
- Study its acknowledgement that async handling is at least once, not exactly once.
- Study its warning about long transactions and subscription/projection behavior.
- Study its end-to-end test/docker/database migration organization.
- Study its “adapt application module, keep event core separate” intent.

### Do not copy blindly from that Java/Spring reference

- Do not import Spring/JPA annotations or lifecycle assumptions into Go/vv.
- Do not accept its table/lock/query shape without current Postgres load testing.
- Do not copy a domain model or HTTP/API transport as a framework contract.
- Do not assume its outbox/broker choice establishes vv's broker dependency.
- Do not assume its snapshot threshold is appropriate for vv aggregates.
- Do not assume its error mapping/audit/tenant/privacy model matches vv decisions.
- Do not expose its raw SQL/entity abstractions as vv public API.
- Do not treat a demo's E2E coverage as substitute for vv fault/release tests.
- Do not turn its project modules into root-module dependencies.
- Do not substitute repository admiration for a specific Go/Postgres ADR.
- Do not publish a compatibility claim until vv target version matrix passes.
- Do not omit new failure cases merely because the reference did not model them.

### From Axon versioning/upcaster guidance

- Take the principle that persisted events outlive current code/schema.
- Take the principle that upcasters transform reader view rather than stored history.
- Take the need for an ordered/validated upcaster chain across revisions.
- Take the possibility that an evolution may map one source event to zero or many.
- Take the discipline of keeping historic reader compatibility explicit.
- Take the release need to coordinate readers, writers and event schema evolution.
- Take the distinction between event revision and other application versions.
- Take the need to test historic serialized forms, not only new events.

### Do not copy Axon wholesale

- Do not pull in Axon's aggregate runtime, annotations, command bus or Java types.
- Do not make Go event registration reflection/annotation magic by imitation.
- Do not turn Axon convenience APIs into implicit global vv behavior.
- Do not assume an Axon storage engine abstraction erases PostgreSQL decision details.
- Do not adopt every Axon extension/saga/query feature before vv has its own demand.
- Do not make upcasting a way to run current business logic or external services.
- Do not replace vv root contracts with a foreign framework vocabulary.
- Do not skip live PostgreSQL proof because a mature Java framework supports a concept.

## Research deliverables before code review

1. A one-page comparison records the reference table/transaction strategy and vv alternative.
2. A DDL sketch identifies every unique/index/FK/transaction boundary it relies on.
3. A failure table identifies conflict, connection-loss, crash and retry outcomes.
4. A version table separates stream, event, snapshot, projection, API and release axes.
5. A v1→v2 fixture demonstrates the actual upcaster and writer rollout decision.
6. A projection/outbox fixture demonstrates at-least-once duplicate behavior concretely.
7. A privacy table lists fields prohibited from envelope, audit and OTel outputs.
8. A tenancy table shows shared and DB-per-tenant routing before store invocation.
9. A rollback rehearsal proves or forbids old binary deployment after new event write.
10. A restore/replay rehearsal proves historic reader intent extends beyond hot rows.
11. A dependency graph proves root vv has no PostgreSQL/event/OTel/broker coupling.
12. A roadmap/index/ADR link lets future maintainers find every irreversible choice.

## Final source links

- [PostgreSQL event sourcing reference implementation](https://github.com/eugene-khyst/postgresql-event-sourcing)
- [Axon Framework GitHub repository](https://github.com/AxonFramework/AxonFramework)
- [Axon event versioning and upcasters](https://docs.axoniq.io/axon-framework-reference/main/events/event-versioning/)
- [Axon PostgreSQL event-store infrastructure](https://docs.axoniq.io/axon-framework-reference/development/events/infrastructure/)
- [Transactional outbox pattern reference](https://masstransit.io/documentation/patterns/transactional-outbox)
- [OpenTelemetry event-store integration boundary](2026-08-26-1558-opentelemetry-roadmap.md)
- [Multitenancy topology boundary](2026-08-26-1558-multitenancy-roadmap.md)
- [Audit revision boundary](2026-08-26-1558-audit-log-roadmap.md)
- [Storage lifecycle boundary](2026-08-26-1558-storage-roadmap.md)
- [Language-neutral event boundary](2026-08-26-1558-i18n-roadmap.md)

## Last-mile release checklist

### Aggregate correctness

- Rehydrate every canonical historic fixture.
- Rehydrate every mixed revision fixture.
- Rehydrate every snapshot-plus-tail fixture.
- Reject every corrupt/gap/duplicate fixture.
- Decide every command fixture deterministically.
- Confirm every conflict re-decides from current state.
- Confirm no handler executes external side effects.
- Confirm no event type is localized/dynamic/reflection-derived.
- Confirm payload/metadata limits run before SQL.
- Confirm historic rows remain untouched by test and migration paths.

### PostgreSQL correctness

- Run real concurrent append sessions.
- Run rollback after every transaction sub-step.
- Run serialization/deadlock classification fixture.
- Run connection-loss/uncertain commit fixture.
- Run wrong datasource/transaction-scope fixture.
- Run constraint/index negative cases.
- Run load order/query-plan regression fixture.
- Run Postgres/driver version support report.
- Run backup restore then full replay fixture.
- Run destructive test cleanup scope validation.

### Asynchronous correctness

- Run projection duplicate crash fixture.
- Run projection poison/quarantine fixture.
- Run projection rebuild/cutover/rollback fixture.
- Run outbox commit/claim/send/mark crash fixture.
- Run consumer duplicate/out-of-order fixture.
- Run manual redrive/audit purpose fixture.
- Run worker cancellation/backpressure fixture.
- Run trace carrier corruption/bounds fixture.
- Run no-broker/no-exporter deterministic core suite.
- Run explicit startup/shutdown ownership fixture.

### Release correctness

- Run readers-before-writers deployment fixture.
- Run old/new mixed reader fixture.
- Run feature-gated new writer fixture.
- Run rollback before first new revision fixture.
- Run rollback after first new revision fixture.
- Run projection/snapshot/integration compatibility fixture.
- Run archive/restore old-reader retention fixture.
- Run tenant cohort/schema compatibility fixture.
- Run schema expand/contract migration fixture.
- Publish/review final compatibility matrix and non-goals.

### Privacy and composition

- Run payload/ID/tenant/raw-error sentinel export scanner.
- Run trace no-op/sampled/exporter failure equivalence tests.
- Run audit sampled/unsampled transactional evidence tests.
- Run shared/DB-per-tenant cross-scope refusal tests.
- Run protected history access/purpose/range-limit tests.
- Run storage reference/event lifecycle no-URL/no-key tests.
- Run i18n replay/upcaster/identity isolation tests.
- Run metric cardinality stress tests.
- Run generic CRUD/event-history separation tests.
- Run root dependency graph/one-satellite-decision checks.

## Final rule

If a developer cannot explain a change using the six separate version axes,
show a pure historic fixture, and name its reader/writer/rollback consequences,
the change is not ready to enter an append-only event history.

## Stable terminology card

- **Aggregate ID** identifies one domain consistency boundary; it is not telemetry label data.
- **Stream version** protects append order for that aggregate; it is not event schema revision.
- **Event revision** describes payload meaning; it is not database migration number.
- **Global position** drives ordered consumption/checkpoints; it is not a user-visible cursor.
- **Snapshot revision** describes derived cache encoding; it is not historic fact version.
- **Projection version** describes read-model implementation; it is not aggregate state version.
- **Integration revision** describes external contract; it is not raw domain event revision.
- **Release version** describes deployed compatibility cohort; it is not a fact in a stream.
- **Expected version** is supplied by a fresh aggregate load; it is not an optional optimization.
- **Conflict** means stale decision; it is not a permission failure or database outage.
- **Serialization failure** is a PostgreSQL retryable condition; it is not domain conflict by default.
- **Upcaster** is a pure reader transform; it is not a migration job or business handler.
- **Projection** is an at-least-once read model consumer; it is not an exactly-once promise.
- **Outbox** is durable publish intent; it is not broker delivery confirmation.
- **Audit revision** proves declared actor/action evidence; it is not full event-sourced state.
- **Tenant scope** routes/authorizes before storage; it is not a stream-name suffix.
- **Trace correlation** is diagnostic linkage; it is not a durable business causation model.
- **Archive** is retained history lifecycle; it is not permission to delete facts casually.
- **Restore** is an authorized operational procedure; it is not a generic query method.
- **Compensating event** records a later correction; it is not a mutation of earlier truth.

## Minimal adoption warning

Event sourcing raises the cost of an incorrect shortcut because data must remain
readable after the code, deployment and team that wrote it are gone. Start with
one aggregate whose audit/history/consistency value justifies that cost; prove
the Postgres append/replay/release path end to end; then expand by evidence.

## Final implementation constraints

The first event satellite chooses PostgreSQL on purpose.

It owns no alternate backend promise.

It starts no hidden broker or projection worker.

It sets no global OTel provider or locale.

It owns no JWT/header/tenant authentication parser.

It exposes no raw events table as generic CRUD.

It accepts no writer without expected version.

It stores no arbitrary metadata bag.

It publishes no raw domain payload as integration contract by default.

It retries no stale append after a conflict.

It treats no trace as an audit record.

It treats no snapshot as a reason to forget facts.

It treats no upcaster as a historic SQL update.

It treats no successful broker call as exactly-once completion.

It retires no historic reader before restore/replay evidence.

It enters beta only with live PostgreSQL and mixed-release proof.
