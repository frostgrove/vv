# PostgreSQL event sourcing roadmap — 2026-09-01

**Current baseline, 2026-09-08 (`c938866`):** the PostgreSQL store
[`event/eventpg`](../modules/en/eventpg.md) landed in `6e1c846` ([[FL-037]]).
Earlier store-absence and aggregate-blocker statements below are historical;
E0–E4 are retained, not rewritten as a completion report. The
[nine additional mechanisms](#research-appendices-2026-09-08) remain unimplemented.

**Historical proposal status, before PostgreSQL implementation:** proposal,
not a delivery commitment, for **`eventpg`**. The
PostgreSQL store is not implemented and E0's first decision — the aggregate —
still gates it. Activation of everything below E1 still requires the gates in
this document and the live product roadmap.

**Superseded in part, 2026-09-07.** The root packages `event`,
`event/eventmemory` and `event/eventtest` are implemented and green: the
vocabulary, the store seam, a complete in-memory store and the conformance suite
a store runs against itself. E0 decisions 1 and 3 are superseded by [[D-121]]
below and the entries in the baseline table that assert no `event` package
exists are stale for the root half. Read [[FL-036]] and
[docs/modules/en/event.md](../modules/en/event.md) for what actually shipped.

**Revised 2026-09-06** against the tree at `1e67ad9` and the tenancy work beside
it in the working copy. Five of the assumptions this document was written on had
expired, and one of its examples named an API that will now never exist. The
[revision list](#what-this-revision-changes) is below, and the
[baseline](#current-baseline) is rewritten around it.

**Supersedes:** package topology, integration examples and delivery gates in the
[2026-08-26 snapshot](2026-08-26-1558-postgres-event-sourcing-roadmap.md). That
snapshot remains research input for PostgreSQL failure cases, event evolution and
operational rehearsal.

**Architecture:** this revision follows the
[optional extension architecture](2026-09-01-extension-architecture-roadmap.md).
In particular, it creates one independently selectable PostgreSQL event-source
extension and no packages for its intersections with tenancy, audit, brokers,
storage, i18n or OpenTelemetry.

## What this revision changes

1. **The two base chains it was waiting for have landed.**
   `port.ServiceMiddleware` with `port.ChainService`, and `storage.Middleware`
   with `storage.Chain`, are in the root module and green. E3 is therefore no
   longer blocked on a seam that does not exist; it is blocked on a factory being
   justified. The reason the first event API stays direct is now only the fixed
   CRUD-shaped method set — not a missing chain.
2. **The OpenTelemetry module exists.** `github.com/frostgrove/vv/otel`, package
   `vvotel`, currently ships service, storage and cache facade/backend observers
   ([[D-128]]). "No `eventotel`" is now a rule about a neighbour that is real;
   event-source telemetry remains an explicitly unimplemented appendix in this
   roadmap.
3. **Tenancy is delivered, and it is a package of the root module, not a
   module** ([[D-116]]). It exposes no service middleware, so the previous
   revision's `tenancy.Service(...)` example named an API that is not coming. The
   real seams are `tenancy.Authority`, `tenancydb.Directory`, `tenancyrow.Repository`
   and `tenancyrow.Policy`, and its published profile is shared row only.
4. **The transaction plumbing this document hand-waved is written.**
   `crudsql.TransactionFor`, `crud.IsTransaction`, `crud.SameDataSource` and
   `crud.KeyOf` exist, and `jobspg` is a working example of a PostgreSQL adapter
   that joins a caller's `*sql.Tx` and proves which transaction it joined.
   `eventpg` inherits that shape instead of inventing one.
5. **Durable intent inside a caller's transaction already exists**, as
   `jobs.Stager` with `jobs.TransactionContext`. E4 must justify an outbox
   against staging a job in the append transaction, rather than assume the outbox
   is the only answer.

Four smaller facts move gates from prose into commands: schema management is a
deployment profile choice ([[D-101]]), a background activity is a supervised
`runtime.Runner` ([[D-092]]), a probe publishes an answer while the composition
root owns importance ([[D-091]]), and `make integration` does not run a satellite
module's tagged suite — so a live gate must name its own command and fail rather
than skip.

## Current baseline

The current tree is the starting point, not the proposed API in the historical
roadmap:

| Area | Current state | Consequence |
|---|---|---|
| Event sourcing | **Superseded 2026-09-07.** `event`, `event/eventmemory` and `event/eventtest` exist in the root module ([[D-121]], [[FL-036]]); no `eventpg` module exists | The `eventpg` names below are still provisional until E0's aggregate decision is made. The vocabulary is not: it is frozen at the phase-1 baseline in `docs/api/surface.md` |
| Root dependency graph | The root module has no third-party requirement, and `make check-deps` lists `-test -tags=integration` too | PostgreSQL lives in the event module — and so does anything only its live fixtures import |
| Module boundary | A module boundary is a third-party dependency boundary, not an optionality boundary ([[D-116]]) | `eventpg` is a module because its fixtures require a driver, exactly as `jobs/jobspg` does — not because it is optional |
| CRUD composition | `crud.Middleware`, `crud.Chain`, `crud.Base.Next` and typed optional effects exist; `ExistsUnscopedOf` is exact-outer ([[D-115]]) | Reuse the chain for read models; do not create an event-specific CRUD chain |
| Service composition | `port.ServiceMiddleware` and `port.ChainService` exist: first listed is outermost, nil middleware is skipped, and a nil base or a middleware that returns nil collapses the chain to nil. Restore is discovered by `port.RestorableOf` | An event service decorator is now expressible. It is still not justified: the fixed method set cannot carry a named aggregate command |
| Storage composition | `storage.Middleware` and `storage.Chain` exist with the same order and nil rules; the root store forwards `Capabilities` from its backend | Event code may use root storage vocabulary after an actual use case; it never imports a storage satellite |
| Event operation seam | **Superseded 2026-09-07.** `event.Store` is the dependency-neutral contract and `eventmemory` is the second implementation the old row was waiting for; `eventtest` is what a third one is held to | `eventpg` implements `event.Store` rather than inventing a surface. There is still no event middleware chain and no generic `EventStore` beside it |
| OpenTelemetry | The `otel` module exists (`vvotel.Service`, `vvotel.Store`, `vvotel.Cache`, `vvotel.CacheMemory`); the [current OTel roadmap](2026-09-08-opentelemetry-maximal-roadmap.md) commits durable-jobs telemetry but leaves event-source signals in ES-10 here | First event release has no `eventotel` package and no OTel dependency; an application still gets command spans |
| Tenancy | Delivered as a root-module package with one adapter per seam ([[D-116]], [[D-117]]); a scope is minted and cannot be manufactured, and a unit of work is pinned to a tenant *and* a generation | Event tenancy is application composition over `tenancy.Authority` and `tenancydb.Directory`. There is no `tenancy.Service` to sit in a chain |
| Audit | No audit package exists | Any event/audit mapping stays application-owned, and neither module imports the other |
| Transaction authority | `crud.Source`, `crud.Beginner`, `crud.IsTransaction`, `crud.SameDataSource`, `crud.KeyOf`, `crudsql.Transaction` and `crudsql.TransactionFor` exist; `jobspg.Driver.Stager` binds a caller's `*sql.Tx` and mints a `jobs.TransactionContext` that says which transaction it is | `eventpg` joins a caller's transaction with these; it writes no new plumbing and opens no second connection |
| Durable intent in a caller transaction | `jobs.Stager` stages work inside the caller's transaction and carries a `jobs.DurabilityProfile` | An outbox is one option, not the premise. E4 compares it against staging a job |
| Background work | `runtime.Runner` and `runtime.Supervisor` own start, restart and drain ([[D-092]]) | A projector or publisher is contributed as a runner; nothing in `eventpg` starts a goroutine |
| Health | `health.Probe` and `health.Contribution` publish an answer; the composition root chooses importance and the public code ([[D-091]]) | A store readiness answer is a contribution, not a decision the module makes |
| Container binding | `app/module.Definition`, `module.Catalog` and `module.Doctor` describe a deployment; contributions are inferred from constructors ([[D-110]]), resource declarations are contributed as data ([[D-111]]), and a deployment role is declared rather than read off the graph ([[D-108]]) | An `eventpgfx` is a separate module on the `jobspgfx` pattern, and E1 does not need it |
| Schema | Migrating is a deployment profile choice ([[D-101]]); `jobspg` separates managed from verified start-up and publishes `MigrationStatements` beside a `MIGRATIONS.md` | The event schema follows that shape: statements a deployment may run, and a start-up that verifies rather than migrates by default |
| Live evidence | `make integration` runs `./test/...` only. `jobspg`'s tagged suite lives in its own module and skips when its DSN variable is unset | The event live gate names its own command, and an unset DSN fails it instead of passing quietly |
| Error taxonomy | `errs.CodeConflict`/`errs.KindConflict` and `crud.ErrConflict` already carry the conflict class to a transport | An append conflict is that class. It is not a new code and not a retry signal |

Modified or untracked production work elsewhere in the repository is concurrent
work, not evidence that any API in this proposal exists.

## Decision in one page

1. The first event-source choice is one optional module, working name
   `github.com/frostgrove/vv/eventpg`, for PostgreSQL event sourcing.
2. It is a module because its live fixtures require a PostgreSQL driver, and
   `make check-deps` reads test build configurations. Its production code may
   stay on `database/sql` and `crudsql`, as `jobspg`'s does; that does not make
   it a root package, because the driver its own tests import is a requirement of
   the published module ([[D-036]], [[D-116]]).
3. That module owns the aggregate/event vocabulary, PostgreSQL append/load
   implementation, schema/version registry and any transaction-local projection,
   snapshot or outbox implementation accepted by a milestone. One package,
   organised by files.
4. No root `event` package is created for the vocabulary. A base package is added
   when two independent consumers, or one consumer plus a concrete conformance
   obligation, justify it; a second event store is what would start that
   conversation, and an ADR would then decide it.
5. It does not promise database portability. An in-memory model may support pure
   tests but does not certify concurrency, commit, crash or recovery semantics.
6. The first public surface is a direct typed `eventpg` API. A dependency-neutral
   root event seam is added only when [[D-048]]'s second-implementation rule is
   met and a separate ADR accepts its method and capability contract.
7. One event module may later return ordinary middleware or factories for several
   accepted base seams. It does not create one module per seam.
8. `eventpg` imports dependency-light root packages and its selected PostgreSQL
   ecosystem only. It never imports `tenancy` or a tenancy adapter, audit,
   `vvotel`, a broker adapter, `storageminio`, a router/container binding or
   another optional extension.
9. The application composition root establishes tenant/policy context, orders
   base middleware, supplies a datasource/transaction and maps event results to
   audit or integration contracts. No pairwise package performs that wiring.
10. A transaction-local outbox record may belong to `eventpg`. Broker delivery is
    a separately selected application or broker-extension concern connected by a
    narrow sender contract; no `eventkafka`, `eventnats` or broker × OTel package
    is created.
11. Event and audit history remain independent truths. They may share a
    caller-owned PostgreSQL transaction only when the application proves the same
    transaction authority; otherwise neither module claims atomicity.
12. Generic service and driver telemetry may surround event work. Event-specific
    signals remain deferred until a neutral seam exists; `eventotel` is forbidden.
13. Constructors start no worker, publisher, projector or global provider. Work
    that runs is a `runtime.Runner` the host supervises ([[D-092]]), and what a
    deployment migrates is a profile choice ([[D-101]]).
14. Every optional executable effect is exact-outer or explicitly preserved and
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
  +--> root dependency-light seams (crud, port, storage, errs, health, runtime)
  +--> eventpg module --------> PostgreSQL ecosystem
  +--> tenancy package -------> root seams          (root module, D-116)
  +--> audit module ----------> root seams          (does not exist yet)
  +--> otel module -----------> root seams + OTel API
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

The proposed layout, provisional until E0 accepts it:

```text
eventpg/                    MODULE github.com/frostgrove/vv/eventpg
  go.mod                    one dependency decision: the driver its live fixtures require
  doc.go                    package eventpg
  stream.go                 stream family, identity and version — what a caller names
  event.go                  the envelope: declared type, revision, payload, order
  registry.go               declared types and revisions, and the reader for each
  codec.go                  canonical encoding and the bounded decoder
  upcast.go                 old revision to current reader, pure
  store.go                  construction, datasource ownership, close semantics
  append.go                 the expected-version append and its one transaction
  load.go                   ordered load and rehydration
  schema.go                 the statements a deployment may run, and what start-up verifies
  errors.go                 the refusal vocabulary, and what never travels back as text
  *_integration_test.go     //go:build integration — live PostgreSQL
```

A new module is not finished when it compiles: it needs its `go.work` line, the
`replace` lines `test/go.mod` and `_examples/go.mod` carry for the root, a
`docs/modules/en` and `docs/modules/ru` page with its index rows, the flow that
maps its files, and a regenerated `docs/api/surface.md`.

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
| append authority | the exact transaction or datasource an append is bound to, and what a second writer must prove to claim atomicity with it |
| commit uncertainty | a connection lost around commit: the outcome is unknown, and no layer guesses it |

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
  retries a stale proposed event list, which is [[D-040]] applied to append.
- Snapshots are disposable and versioned independently. Full replay remains the
  authority for every retained supported history.
- Projection and publisher delivery are at least once. Consumers are idempotent;
  low duplicate rates do not become an exactly-once claim.
- Aggregate IDs, event IDs, payloads, expected versions, tenant identities and
  checkpoints do not become default log, span or metric fields.
- Event history is not exposed through an ordinary CRUD list/filter endpoint.
- No constructor starts a goroutine or mutates a global registry. Anything that
  runs is a `runtime.Runner` the host supervises.
- No start-up migrates a schema it was not told to migrate. What a deployment
  runs is a profile the composition root selects ([[D-101]]).

## Provisional direct API shape

The following snippets communicate semantics only. They are not accepted APIs,
and their names may change at E0. The construction shape follows `jobspg`: a
store is opened over a datasource the caller owns, and a caller-owned transaction
is passed explicitly rather than discovered from a context nobody can see.

```go
// Illustrative only.
events, err := eventpg.New(eventpg.Spec{
    Source:   source,   // crud.Source the application opened and owns
    Registry: registry, // declared event types and their readers
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
    return err // a conflict here is errs.CodeConflict, and the caller decides again
}
return useCommit(commit)
```

A caller who is already inside a transaction binds to it explicitly rather than
hoping the store finds it — `events.Within(tx)` in the audit example below is
that shape, and it is the only one that can be checked before a statement runs.

The store is implementation-aware without exposing a driver handle in domain
values. The final constructor must state datasource ownership, transaction
binding, supported PostgreSQL/driver versions and close semantics.

## Base-seam composition

`eventpg` does not own a framework-wide chain. When an accepted base chain is the
right binding point, an event factory returns that base's ordinary middleware
type. Application code owns order, and the chains it would join now exist:

```go
// port.ChainService and vvotel.Service are implemented. There is no
// tenancy.Service and no audit package, so an event command reaches tenancy and
// authorization through the seams below rather than through this chain.
service := port.ChainService(
    applicationService,
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
| `port.Service` | The chain exists, and the method set is still the fixed CRUD-shaped one; use it only when one existing verb honestly represents the concrete application operation | Otherwise keep the direct eventpg API. Do not smuggle a named aggregate command through `Update`, and do not add a dependency-neutral command seam without real multi-implementation evidence |
| `crud.Core` | build a read model using existing CRUD middleware | Reuse `crud.Chain`; aggregate writes never masquerade as CRUD updates |
| `storage.Store` | persist a governed logical object reference when event payload policy requires it | The Store chain exists; import root storage only, never a backend satellite, and forward `Capabilities` exactly |
| jobs/worker hooks | run a projector or publisher under host-owned lifecycle | Use the accepted jobs seam and contribute a `runtime.Runner`; do not invent `jobsevent` |
| `health.Probe` | answer whether the store's schema and datasource are usable | Publish the answer; the composition root names its importance and public code ([[D-091]]) |

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
   cannot be skipped to find an inner implementation, which is the rule
   `crud.ExistsUnscopedOf` was corrected to obey ([[D-115]]).
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
   as `port.RestorableOf` discovers it; it does not manufacture or erase restore.
9. If it wraps `storage.Store`, it forwards `Capabilities` exactly and preserves
   `Open` stream ownership; it does not read or buffer a body for observation.
10. [[D-030]]/[[D-061]]-style method inventories and capability matrices fail when
    the wrapped seam grows without a decision.

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

The mechanisms are the ones the tree already has, and E1 uses them rather than
new plumbing:

- a caller who owns a transaction hands it over the way `jobspg` takes one —
  explicitly, so the store cannot be handed a source that silently is not the one
  the caller is inside. `crudsql.Transaction` and `crudsql.TransactionFor` are how
  a `*sql.Tx` is recovered from an executor or a context;
- `crud.IsTransaction` says whether an executor already is one, and
  `crud.SameDataSource` with `crud.KeyOf` is how a mismatch is detected before a
  statement rather than after it;
- the adapter must not open a second unscoped transaction when the caller has
  bound one. A datasource or transaction mismatch fails before event writes;
- a connection loss around commit reports the documented unknown outcome; callers
  do not blindly append again.

Proving *which* transaction a commit belongs to is a real value, not an
assertion in prose. `jobs.TransactionContext` is the shape: a backend identity, an
opaque binding that cannot be serialised or reconstructed by a caller, and a
durability profile. `eventpg` needs an equivalent of its own — it does not import
`jobs` to borrow one, and it does not answer the question with a datasource
pointer a decorator could copy.

Audit composition is application-owned:

```go
// Illustrative only. The application imports both modules, owns the transaction
// and owns this mapping. There is no framework unit-of-work type that spans them.
tx, err := beginner.Begin(ctx)
if err != nil {
    return err
}
defer rollback(tx)

commit, err := events.Within(tx).Append(ctx, request)
if err != nil {
    return err
}
if err := auditor.RecordDomainCommit(ctx, tx, toAuditRef(commit)); err != nil {
    return err
}
return tx.Commit()
```

`toAuditRef` belongs to the application. `eventpg` does not import audit, audit
does not import `eventpg`, and neither receives the other's payload. The audit
module may say "atomic with append" only when both calls prove the same
PostgreSQL transaction authority — the same binding, not the same-looking
handle. Otherwise the application chooses an explicitly named asynchronous
link/outbox policy or refuses the configuration; there is no hidden cross-database
transaction coordinator.

## Schema, migration and resources

The event schema is a deployment concern with an accepted shape already in the
tree, and E1 follows it rather than choosing again:

- the module publishes the statements a deployment may run, the way `jobspg`
  publishes `MigrationStatements` beside a `MIGRATIONS.md` that says what each
  one is for;
- a start-up that migrates and a start-up that verifies are two different profile
  choices, and the default is not "migrate" ([[D-101]]). A verified start-up
  refuses a schema that is not the one the code expects, and says which;
- schema identity is compared, not assumed. A store that finds an older or
  unknown schema fails closed before an append;
- the resources a deployment must have — the database identity the store writes
  to — are declared as data, so a composition root can refuse an undeclared one
  instead of discovering it at the first write ([[D-111]]);
- the schema version and the migration a deployment is missing belong in what the
  module reports about itself, so `module.Doctor` can eventually answer for the
  whole composition rather than for the wiring alone.

## Lifecycle, health and the container

- Nothing in `eventpg` starts a goroutine. A projector, publisher or retention
  sweep is a `runtime.Runner` contributed to the supervisor the application owns,
  which is what makes start, restart, drain and shutdown observable ([[D-092]]).
- A readiness answer is a `health.Contribution`. The module reports whether its
  datasource and schema are usable; the composition root decides whether that is
  critical and what a public probe says about it ([[D-091]]).
- A container binding, if one is ever wanted, is a separate module on the
  `jobspgfx` pattern: it declares its deployment role rather than inferring one
  from the graph ([[D-108]]), contributes resource declarations as data
  ([[D-111]]), and activates nothing through an empty `fx.Invoke`. E1 does not
  need it, and E1 does not build it.

## Tenant and authorization composition

Tenant scope is established and authorized before event repository access. The
event module accepts only neutral context/datasource inputs; it does not name a
tenancy type, and `tenancy` does not name an event one.

```go
// Illustrative application wiring, not eventpg API approval. tenancy.Authority
// and tenancydb.Directory are implemented; the eventpg call is not.
lease, err := directory.Borrow(ctx, tenancy.ClassWrite)
if err != nil {
    return err
}
defer lease.Release()

events, err := eventpg.New(eventpg.Spec{Source: lease.Source(), Registry: registry})
```

What the delivered tenancy design already decides, and an event deployment
inherits:

- a scope is minted by an authority and cannot be manufactured by an adapter
  ([[D-117]]), so "the tenant was verified" is a fact the event layer receives
  rather than a claim it re-derives;
- a unit of work is pinned to a tenant *and* its generation: a second bind for the
  same tenant at a restored generation is refused with `tenancy.ErrPinned`. An
  append that outlives its scope's generation is that refusal, not a write;
- the shared-row path narrows rows through `tenancyrow.Repository` and
  `tenancyrow.Policy`; a database-per-tenant deployment routes through
  `tenancydb.Directory`, whose selection happens once, before any statement, and
  is keyed on the generation as well as the tenant;
- a resolver failure executes zero event SQL and never falls back to a default
  tenant or database.

The published tenancy profile names **shared row only** — the two-database, pool,
migration and recovery rehearsals are open ([multitenancy roadmap](2026-09-01-multitenancy-roadmap.md)
M2 and M5). An event deployment therefore may not advertise database-per-tenant
event sourcing before those rehearsals exist, whatever `eventpg` itself proves.
Shared-table and database-per-tenant profiles require separate live tests for
missing scope, wrong route, pooling/session state and cross-tenant projection or
replay. No `tenancyevent` package is permitted.

## Outbox and broker boundary

The framework already stages durable intent inside a caller's transaction:
`jobs.Stager` takes a placement in the caller's `*sql.Tx` and carries the
durability profile that says what an acknowledgement means. E4 therefore opens
with a comparison, not an implementation:

| Option | What it costs | When it wins |
|---|---|---|
| Stage a job in the append transaction | The deployment already runs jobs, and the delivery, retry, redrive and admin surface exist | The integration event is work the same system performs |
| An eventpg-owned outbox table | A new claim/mark protocol, its own crash matrix and its own operator surface | Delivery is to a broker the job system does not speak, or ordering by global position is part of the contract |

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

The first event release imports no OpenTelemetry package. What an application can
already have around event work, without any event-specific signal:

- `vvotel.Service` around the command, when the command is a `port.Service` one;
- application-selected PostgreSQL driver instrumentation below `eventpg`;
- application-selected broker instrumentation around a sender;
- `vvotel.Store`, if event payload policy ever puts an object in `storage.Store`.

No event-specific append/load/upcast/project/outbox span is promised. The
[current OTel roadmap](2026-09-08-opentelemetry-maximal-roadmap.md) keeps those
signals in unimplemented ES-10 below; its separate durable-jobs messaging work
does not instrument the event subsystem. Shipping ES-10 requires its neutral
typed event seams and gates here. Sampling/exporter availability
never changes append, audit or publisher correctness, and telemetry never carries
payloads or identities.

## Delivery plan

### E0 — activate and freeze architecture

Nothing below E0 is built, and E0 is a set of decisions rather than code. The
blocking one is the first: this repository has no domain of its own, so the
aggregate comes from a real consumer.

Decisions E0 records, with what today's tree already implies:

| # | Decision | What the current tree suggests |
|---|---|---|
| 1 | The aggregate, and the consumer requirement that justifies event sourcing over CRUD plus audit | **Superseded in part by [[D-121]] (2026-09-07).** It still gates E1 — no schema is written without a named aggregate. It does **not** gate the vocabulary, which is a library contract rather than a live schema: the blocker was aimed at what a wrong aggregate costs in a migration, and phase 1 wrote none |
| 2 | Module path and layout | `github.com/frostgrove/vv/eventpg`, one package, files by concern — the module because of the driver its fixtures need ([[D-116]]) |
| 3 | Whether a root `event` vocabulary package is created | **Superseded by [[D-121]] (2026-09-07): yes, and it is written.** This row's own condition was met — `eventmemory` is the second store, shipped beside the seam rather than promised after it, and `eventtest` is what holds a third to the same contract |
| 4 | Driver decision and dependency allow-list | Production over `database/sql` plus `crudsql` if it costs nothing, driver in the fixtures — the `jobspg` shape |
| 5 | Direct API versus a base-seam adapter | Direct. Both chains now exist, and neither carries a named aggregate command |
| 6 | Transaction ownership and the binding that proves it | Caller-owned transaction passed explicitly; an eventpg-owned equivalent of `jobs.TransactionContext` |
| 7 | Schema management profile and start-up behaviour | Verify by default, migrate on an explicit profile ([[D-101]]) |
| 8 | Envelope, stream identity/version, codec limits, error taxonomy, supported PostgreSQL versions | Conflict maps to the existing `errs.CodeConflict`; the rest is new and frozen here |
| 9 | The forbidden module/package list and the source/import checks that hold it | The list above, held by a `scripts` graph test on the `tenancy_test.go` model |

Exit evidence:

- no public API is added by prose alone;
- the module graph is `eventpg -> root + reviewed PostgreSQL ecosystem` only;
- root and every other extension have no reverse import, checked rather than
  reviewed: `make check-deps`, `make check-tiers`, `make check-utils`, and a
  graph test that fails when `eventpg` reaches an optional extension or when a
  base subsystem reaches `eventpg`;
- direct API and every proposed base adapter have a method/capability inventory;
- an unpublished composition fixture can select `eventpg` beside two fake
  extensions without a pairwise package.

### E1 — one aggregate, append and replay

1. Create the module: `go.mod`, the `go.work` line, the `replace` lines in
   `test/go.mod` and `_examples/go.mod`.
2. Implement one aggregate/event registry and canonical bounded envelope
   encoding with exact revision readers.
3. Implement the live PostgreSQL schema, expected-version append and ordered
   load, joining a caller's transaction when there is one.
4. Prove pure rehydration with historic, malformed, missing and unknown-revision
   fixtures.
5. Prove concurrent append, rollback, cancellation and commit-uncertainty
   behaviour against live PostgreSQL sessions.
6. Publish safe errors without SQL, payload or identity leakage.
7. Write the module page in `docs/modules/en` and `docs/modules/ru` with their
   index rows, the flow that maps the files, and the use case it serves; run
   `make api`.

Exit evidence:

- one aggregate survives full replay and a v1-to-v2 reader/upcaster fixture;
- two concurrent writers produce one winner and one typed conflict;
- rollback leaves no event or stream-version fragment;
- the live suite ran and is named in the report with its command — an unset DSN
  is a failure of the gate, not a skip that reads like a pass;
- `make unit`, `make vet`, `make check` and `make tidy` are green with the new
  module in the workspace;
- no snapshot, outbox, worker, broker or cross-extension code is required.

### E2 — projection and evolution

1. Add one idempotent checkpointed local projection only from a concrete read need.
2. Add mixed old/new reader and writer release rehearsals.
3. Add a snapshot only after measured replay cost and full-replay equivalence proof.
4. Define archive/restore and retained-reader policy before beta.
5. Whatever runs continuously is a `runtime.Runner`; the projector does not own
   its own lifecycle.

Exit evidence includes duplicate/crash/checkpoint tests, rebuild/cutover rollback,
snapshot corruption fallback if enabled, and a restore/replay rehearsal.

### E3 — accepted base composition

1. Add only `eventpg` factories justified by a concrete base seam. The chains
   exist, so the question is no longer whether one can be written but whether a
   real consumer needs it.
2. Do not invent `eventpg.Service` to smuggle named aggregate commands through
   the fixed CRUD-shaped `port.Service`, and do not add an event-owned
   composition registry.
3. Test first-listed-outermost order, nil, opaque neighbours, full method
   inventory and optional capabilities in both relevant orders.
4. Prove tenant/policy executes before event SQL and telemetry is result-neutral.

If no real base consumer exists, this milestone remains deferred and direct API
usage is the supported shape.

### E4 — optional outbox and external composition

1. Answer the outbox-versus-staged-job comparison above for a named integration
   event consumer, and record why.
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
| Root optionality | Root and non-event consumers have no eventpg or PostgreSQL event graph; `make check-deps` stays green with the module in the workspace |
| Import direction | Eventpg imports root seams and its PostgreSQL choice only; a graph test rejects an optional-extension import in either direction |
| No combinations | No event × tenancy/audit/OTel/storage/broker package or nested module exists |
| Direct seam | First release does not publish a generic root event contract without [[D-048]] evidence |
| Base composition | Each accepted adapter is an ordinary base middleware and composes with two unrelated layers in declared order |
| Capabilities | Exact effects never tunnel; method-set capabilities remain honest through opaque neighbours |
| Append | Expected version, immutable rows and stream order are proven with live concurrent PostgreSQL |
| Atomicity | Stream/event/local outbox work shares one proven transaction authority, or the configuration refuses the claim |
| Audit | Application mapping carries a bounded reference only; neither production module imports the other |
| Tenancy | Verified scope/routing precedes event SQL; missing, wrong or out-of-generation scope fails with zero leakage; no profile claims a topology the tenancy roadmap has not rehearsed |
| Broker | Outbox or staged job proves at-least-once crash windows; broker adapter is independent/application-owned |
| Telemetry | Eventpg has no OTel imports; no-op/exporter failure changes no result; sensitive values remain absent |
| Evolution | Retained revisions have one reader path; upcasters are deterministic; mixed-release rehearsal passes |
| Lifecycle | Constructors start nothing; every continuous activity is a supervised runner; the host owns cancellation and shutdown |
| Schema | Start-up verifies by default, migrates only on an explicit profile, and refuses an unknown schema before an append |
| Health | The store publishes a readiness answer and names no importance of its own |
| Modules | `GOWORK=off` fixtures cover root-only, eventpg-only and multi-extension application composition, on the `scripts/otel-consumer.sh` model |
| Live evidence | The live suite's command is recorded and its absence fails; a skipped run is not evidence |
| Documentation | Module pages in both languages, index rows, a flow, a use case and a regenerated surface baseline |

## Initial non-goals

- A generic database-neutral event store.
- EventStoreDB, Kafka, NATS, MongoDB or filesystem backends.
- A root event contract before a second implementation justifies it. **Met, not
  abandoned:** the second implementation shipped in the same phase as the
  contract, which is what the clause asked for ([[D-121]]).
- Pairwise or combination packages of any kind.
- Automatic aggregate/reflection registration or arbitrary metadata maps.
- Raw event-history CRUD/search for ordinary clients.
- Exactly-once broker delivery, hidden retry or hidden background workers.
- Treating audit, traces, logs, snapshots or projections as the event source of truth.
- Payload/identity export for debugging convenience.
- A generic saga/workflow engine.

## Definition of done

The first PostgreSQL event-source release is complete only when:

1. one accepted ADR names the aggregate, the exact eventpg module, the PostgreSQL
   choice, the direct API boundary and the forbidden imports;
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
11. documentation labels provisional and implemented APIs accurately, and the
    module has its pages, index rows, flow and regenerated surface baseline;
12. `make check`, `make unit`, `make vet` and `git diff --check` pass, and the
    live suite ran rather than skipped.

<a id="research-appendices-2026-09-08"></a>

## Дополнительные приложения — 2026-09-08

**ES-01–ES-09 не выполнены.** Это дополнительные механики и уточнения E1/E2,
а не отчёт о реализации. База сверена с `dev/ai-improvements` на `c938866`,
включая реализованный [PostgreSQL store](../../event/eventpg/) из `6e1c846`.
Существующие E0–E4 и их gates здесь не пересматриваются. Реализация store
не закрывает ES-01–ES-09.

Владельцы — опциональная event-подсистема и выбранный store, не framework kernel.
Приложение передаёт обработчики, SQL transaction authority и read-model repository
своего ORM; никакого обязательного ORM, DI, broker или межмодульного registry.
Новые гарантии требуют отдельного контракта и тестов: нынешний [[UC-032]] ими
не расширяется. DX ниже — эскизы, не существующие API.

### Приложение ES-01 — durable subscriptions и атомарная SQL-проекция

**Статус: не выполнено.** Уточняет E2, не повторная реализация `Reader.Cursor`.

1. Механизм: [Axon tracking tokens](https://docs.axoniq.io/axon-framework-reference/4.12/events/event-processors/streaming/) — постоянная позиция отдельного подписчика, продолжение после рестарта. [Marten daemon](https://martendb.io/events/projections/async-daemon.html) сохраняет изменения проекции и её продвижение одной транзакцией.
2. Зачем: падение между изменением read model и checkpoint не должно терять событие или повторно применять уже закоммиченный SQL-эффект.
3. Адаптация: выбранный SQL-профиль фиксирует effect + checkpoint одной transaction authority. У текущего `eventpg` log читается вне write transaction, чтобы не принять собственные staged rows за подтверждённую историю; обработчик пишет effect/checkpoint в предоставленной SQL-транзакции. Курсор не проходит незавершённый append, `MAX(sequence)` не доказательство видимости. Чужая БД/HTTP требуют своей идемпотентности.
4. Уже есть: [reader и сохраняемый cursor](../../event/reader.go), [PostgreSQL settled watermark](../../event/eventpg/read.go) и [caller-owned SQL transaction binding](../../event/eventpg/executor.go). [Capabilities](../../event/eventpg/config.go): Transactions/Persistence/SharedBacking поддержаны, MonotoneVisibility — нет. Постоянного subscriber state и SQL-projector нет.
5. DX: имя подписчика + read-only log + обработчик batch в предоставленной SQL-транзакции; после crash запуск продолжает сохранённую позицию.

### Приложение ES-02 — параллельные подписчики с порядком внутри последовательности

**Статус: не выполнено.** Дополняет E2.

1. Механизм: [Axon segments и token claims](https://docs.axoniq.io/axon-framework-reference/4.12/events/event-processors/streaming/) распределяют работу между процессами, сохраняя порядок связанных событий.
2. Зачем: ускорить проекции несколькими воркерами, не получить `OrderPaid` раньше `OrderCreated` при перераспределении нагрузки.
3. Адаптация: application задаёт ключ последовательности по конфликтующей read model; aggregate ID подходит не для любой multi-stream проекции. SQL commit проверяет актуальное поколение claim вместе с effect/checkpoint. Изменение числа partitions требует согласованной передачи позиции, не замены `hash % N` на ходу. Внешний HTTP такой fence не защищает.
4. Уже есть: [упорядоченные stream/log reads](../../event/store.go), [host-owned runner](../../runtime/runner.go). Нет subscriber partitions, их ownership и handoff; порядок событий не равен порядку параллельных handlers.
5. DX: подписчик + `SequenceBy(key)` + число partitions; запуск нескольких instances не меняет прикладной handler.

### Приложение ES-03 — dead-letter queue сохраняет причинный порядок

**Статус: не выполнено.** Дополняет E2; не общий redrive jobs.

1. Механизм: [Axon sequenced DLQ](https://docs.axoniq.io/dead-letter-queue-guide/4.13/) паркует не только ошибочное событие, но и следующие события той же последовательности; остальные продолжают обрабатываться.
2. Зачем: один сломанный заказ не останавливает весь projector, но его последующие изменения не применяются к неверному состоянию.
3. Адаптация: parking и scan checkpoint атомарны; успешная applied-позиция считается отдельно. Очередь ограничена числом sequences/bytes; overflow останавливает затронутую partition, не пропускает событие. Retry обрабатывает sequence по порядку; skip — явная операторская операция с отметкой неполноты проекции.
4. Уже есть: [классы unreadable history](../../event/errors.go), bounded pages. Нет subscriber DLQ, состояния заблокированной sequence и ordered redrive.
5. DX: политика subscriber failure + `RetrySequence(reference)`; состояние показывает blocked/degraded, не ложное «догнал историю».

### Приложение ES-04 — перестроение проекции рядом с работающей версией

**Статус: не выполнено.** Конкретизирует rebuild/cutover из E2.

1. Механизм: [Marten/Wolverine versioned projections](https://jeremydmiller.com/2025/03/26/projections-consistency-models-and-zero-downtime-deployments-with-the-critter-stack/) строят новую версию в отдельных таблицах, пока старая продолжает обслуживать запросы.
2. Зачем: исправить расчёт или изменить read model без очистки работающей таблицы и выдачи полупостроенных данных.
3. Адаптация: generation имеет отдельные данные, checkpoints и claims; catch-up продолжается до согласованного барьера. Переключение read target атомарно для заявленного набора таблиц. Старый worker не пишет в новое поколение. Rollback допустим, пока старое поколение поддерживается актуальным либо снова догнало историю. Rebuild получает отдельный ресурсный бюджет.
4. Уже есть: [полный log walk](../../event/reader.go); E2 требует rebuild/cutover rollback. Нет generation storage, переключения read target и проверки готовности.
5. DX: `Rebuild(projection, newGeneration)` → проверка результата/барьера → `Activate(newGeneration)`; старое поколение удаляется отдельной операцией.

### Приложение ES-05 — ожидание конкретного изменения в read model

**Статус: не выполнено.** Дополняет E2.

1. Механизм: [Marten non-stale queries](https://martendb.io/events/projections/async-daemon.html#querying-for-non-stale-data) ждут, пока асинхронная проекция догонит зафиксированную границу истории.
2. Зачем: после успешной команды следующий read не должен неожиданно показывать старое состояние; тестам не нужны `sleep` и опрос бизнес-таблиц наугад.
3. Адаптация: ждать подтверждённую stream/version либо store-issued barrier конкретного projection generation, а не «пока lag станет нулём». Scan checkpoint после parking не доказывает применение события. Deadline возвращает timeout/degraded; stale-read разрешается явно. Успех даёт видимость до барьера, не глобальную linearizability и не свежесть чужой read replica.
4. Уже есть: [Commit.Stream/Last](../../event/token.go), но receipt внутри caller transaction ещё не доказывает её commit. Нет applied progress и ожидания проекции.
5. DX: после подтверждённого commit — `projection.Wait(ctx, committedVersion)`, затем чтение согласованного read target.

### Приложение ES-06 — replay не повторяет письма и платежи

**Статус: не выполнено.** Дополняет E2/E4.

1. Механизм: [Marten side effects](https://martendb.io/events/projections/side-effects) отделяет обновление проекции от эффектов и подавляет последние при rebuild; [Axon replay policy](https://docs.axoniq.io/axon-framework-reference/4.12/events/event-processors/streaming/#replay-api) исключает выбранные handlers из replay.
2. Зачем: восстановление read model не должно повторно выставлять счёт, отправлять webhook или создавать новую job за старое событие.
3. Адаптация: отдельные projection/effect handlers; rebuild не получает effect-dispatch capability. Начальный backfill тоже имеет явную effect policy. При переключении поколения durable граница владения live effects не допускает двух отправителей. Это не sandbox: произвольный HTTP внутри пользовательского projection callback запрещается его контрактом и проверяется тестом, не блокируется магией.
4. Уже есть: [ReadOnly без append escape](../../event/reader.go), pure rehydration; E4 выбирает staged jobs/outbox. Нет режима доставки и replay-aware effect dispatch.
5. DX: `Rebuild(projection)` запускает только пересчёт; live effects подключаются отдельным обработчиком с собственной delivery identity.

### Приложение ES-07 — выяснение результата неопределённого append

**Статус: не выполнено.** Дополняет E1; не скрытый retry `Store.Append`.

1. Механизм: [KurrentDB idempotent append](https://docs.kurrent.io/clients/python/v1.3/appending-events) распознаёт повтор того же append по прежним consistency checks и event IDs. Для Frost предлагаем отдельную постоянную квитанцию операции, а не изменение обычного append.
2. Зачем: после потери соединения на commit узнать, записана ли именно эта операция; одного нового stream version для этого недостаточно.
3. Адаптация: operation identity + fingerprint + диапазон событий записываются рядом с append в той же caller-owned SQL-транзакции. Повтор проверяет прежнюю квитанцию; другое содержимое с тем же ключом — конфликт. Отсутствующая квитанция не доказывает rollback, пока исходная транзакция не разрешилась. Retention квитанций ограничивает окно проверки; framework не переигрывает доменное решение.
4. Уже есть: [ErrUncertain](../../event/errors.go), [возвращаемый Commit](../../event/token.go) и [классификация SQL append outcomes](../../event/eventpg/classify.go) без скрытого retry; [Store.Append](../../event/store.go) намеренно не дедуплицирует. Нет durable operation identity и lookup результата. Квитанция — отдельный SQL-профиль, не произвольные metadata в event envelope.
5. DX: operation key перед командой; при unknown outcome — `receipts.Resolve(ctx, operationKey)`, не новый `Load → Decide → Append` вслепую.

### Приложение ES-08 — историческое состояние по версии

**Статус: не выполнено.** Отдельный read-only контракт; не изменение нынешнего `Repo.Load`.

1. Механизм: [Marten time travelling](https://martendb.io/events/projections/live-aggregates#time-travelling) восстанавливает состояние на выбранной версии stream.
2. Зачем: показать состояние заказа до спорного изменения, воспроизвести инцидент или сравнить расчёт до/после исправления projection code.
3. Адаптация: читать полный префикс до указанной версии, не произвольный фильтр событий. Результат не выдаёт append token. Первая граница — точная версия; timestamp не объявляется business-time и не заменяет порядок commit. Неподдерживаемая revision или отсутствующий префикс возвращают отказ, не частичное состояние.
4. Уже есть: [постраничное чтение stream](../../event/store.go), [retained revision chain](../../event/chain.go), [полный replay](../../event/repo.go). Нет top-level ограниченного replay; [[UC-032]] пока требует полный `Load` и должен отдельно описать новый read-only сценарий.
5. DX: `history.AtVersion(ctx, id, version)` → state + provenance; исправление истории остаётся новой доменной командой.

### Приложение ES-09 — совместимые snapshots с безопасным fallback

**Статус: не выполнено.** Уточняет уже запланированную, условную snapshot-механику E2.

1. Механизм: [Axon snapshot revision filters](https://docs.axoniq.io/axon-framework-reference/4.10/tuning/event-snapshots/) исключают snapshots несовместимой версии агрегата из загрузки.
2. Зачем: ускорить длинные streams, не получить старое состояние после изменения fold или кодека.
3. Адаптация: snapshot привязан к backing/stream/version и версии вычисления состояния, независимой от payload revision. Загружается только подтверждённая версия; затем проигрывается весь хвост. Несовместимость/повреждение snapshot ведёт к полному replay; unreadable event не скрывается fallback. Нужны measured benefit и проверка равенства snapshot+tail полному replay. Историю не удаляем.
4. Уже есть: [полный replay](../../event/repo.go) и revision readers; snapshots нет. E2 уже ставит performance/equivalence gate. Оптимизированный load требует отдельного принятого контракта, а не незаметного изменения гарантий [[UC-032]].
5. DX: необязательная snapshot policy у конкретного event store; обычные команды не зависят от наличия snapshot, оператор может его отбросить.

### Приложение ES-10 — OTel для store, полного replay и проекций

**Статус: не выполнено.** Принадлежит event roadmap, а не общей поставке
[OTel](2026-09-08-opentelemetry-maximal-roadmap.md).

1. Механизм: OTel Store middleware измеряет `ReadStream`/`ReadAll`/`Append` как I/O, typed repository decorator — полный `Load` с decode/upcast/fold, а projection observer — batch/checkpoint/lag после появления ES-01. Разделение следует реальным lifecycle: один store call не равен replay и продвижение scan cursor не равно применённой проекции.
2. Зачем: отличать медленную БД от дорогого folding/upcast, store conflict от pre-store refusal и отставание проекции от остановившегося projector.
3. Адаптация: root `event` остаётся OTel-free и получает только общий `Middleware`/`Chain` и безопасный `OutcomeOf(error)`, если они подтверждены независимым потребителем. Реализация живёт в единственном `vvotel`, без `eventsourceotel`. Span/metric attributes содержат только закрытые operation/outcome/error-type; stream/key/type/payload/version/checkpoint не экспортируются. Append внутри чужой transaction означает accepted/staged, не committed. Store wrapper сохраняет Capabilities/Limits/Backing/Transaction/Close и не пытается выдать page I/O за полный replay. Projection telemetry не реализуется до ES-01.
4. Уже есть: [Store](../../event/store.go), полный replay в [Repo.Load](../../event/repo.go), закрытый [Outcome](../../event/outcome.go) и private failure, но нет middleware, публичного classifier, repository decorator или projection observer. Текущий `vvotel.Store` относится только к object storage.
5. DX: `event.Chain(store, vvotel.EventStore(tel))` для I/O и `vvotel.EventRepo(tel, repo)` для полного load/append; после ES-01 projector получает обычный `vvotel.EventProjection(tel)` observer. Native OTel API остаётся escape hatch.
