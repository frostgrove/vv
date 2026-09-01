# Durable audit log roadmap — 2026-09-01

**Status:** current proposal; not an implementation commitment. A named consumer,
policy owner and accepted activation gate are required before package work begins.

**Supersedes:** package topology, wrapper examples and delivery gates in the
[2026-08-26 audit snapshot](2026-08-26-1558-audit-log-roadmap.md). That snapshot
remains research input for revision semantics, privacy, retention and operations.

**Architecture:** this revision follows the
[optional extension architecture](2026-09-01-extension-architecture-roadmap.md).
It defines one independently selectable audit extension with a PostgreSQL initial
profile, several ordinary base middleware factories and no packages for audit's
intersections with tenancy, event sourcing, storage backends or OpenTelemetry.

## Current baseline

The repository contains no audit package or module. Proposed names in the old
roadmap are not implemented APIs.

| Area | Current state | Consequence |
|---|---|---|
| Audit | No audit policy, writer, reader or persistence module exists | Every API name below is provisional until A0 |
| Root dependency graph | The root module has no third-party requirement | Audit and PostgreSQL dependencies remain optional in one extension module |
| Repository composition | `crud.Middleware`, `crud.Chain`, `crud.Base.Next`, `Core.Tx` and typed optional effects exist; `ExistsUnscopedOf` currently walks to an inner executable effect | First slice uses the base chain only after unscoped existence becomes exact-outer/explicitly forwarded or fail closed |
| Service composition | `port.Service` and restore discovery exist; service middleware/chain is planned but not implemented | No public audit service factory precedes acceptance of that base chain and an honest atomicity profile |
| Storage composition | `storage.Store`, `Backend` and `Capabilities` exist; Store middleware/chain is planned but not implemented | A future audit Store factory waits for that chain and imports no storage backend satellite |
| Tenancy | Existing policy/query scopes exist; no tenancy extension/runtime exists | Audit uses its own bounded scope reference or a root-neutral type, never `tenancy.Ref` |
| OpenTelemetry | No OTel module exists; the [current OTel roadmap](2026-08-31-opentelemetry-roadmap.md) does not implement audit-specific signals | No `auditotel` package or OTel production dependency |
| Event sourcing | No event-source module exists | Audit remains independently usable; any event commit mapping is application-owned |

Modified or untracked production files elsewhere in the tree are concurrent work,
not evidence that this proposal has shipped.

## Decision in one page

1. The first audit choice is one optional module, working import path
   `github.com/frostgrove/vv/audit`.
2. Its first persistence profile is PostgreSQL because the accepted initial claim
   is durable audit evidence committed with a PostgreSQL mutation. Policy,
   decorators, writer/reader and that initial profile live in the same module.
3. `auditpg` is therefore a profile/schema identifier, not a second required
   module. A future backend module exists only after a consumer proves a genuinely
   independent backend choice and a separate ADR accepts the owner seam.
4. The audit module may provide typed factories for several stable base seams.
   Each factory returns that base package's ordinary middleware/decorator type;
   audit does not own a competing chain, registry or service locator.
5. Application code selects and orders security, tenancy, audit, event sourcing
   and telemetry. The first middleware listed by a base chain is outermost and
   nil middleware is skipped.
6. The audit module imports dependency-light root seams and the reviewed
   PostgreSQL ecosystem only. It never imports tenancy, eventpg, `vvotel`,
   `storageminio`, JWT, an ORM, router, broker or container extension.
7. There is no `auditotel`, `auditevent`, `audittenancy`, `auditstorage`,
   `auditorm` or combination package. Cross-feature scenarios live in application
   wiring and unpublished conformance fixtures.
8. Transactional audit is claimed only when mutation and revision/item/value rows
   share one proven caller-owned PostgreSQL transaction authority. A wrapper that
   cannot prove that refuses transactional mode before I/O.
9. Audit capture follows verified policy and records only declared committed
   semantics. Denied/failed attempt evidence is a separately configured capability
   with its own availability, privacy and retention policy.
10. Repository/storage executable effects are exact-outer: an unknown wrapper is
    never skipped to find a hidden batch, restore, scoped or transaction verb.
11. Telemetry, event streams, logs and database triggers are not substitutes for
    the audit record. Sampling or exporter failure cannot reduce audit completeness.
12. Constructors start no exporter, retention worker or background writer. The
    host owns lifecycle, credentials, scheduling and shutdown.

## Why one module and one initial profile

The first consumer decision is “use Frostgrove's durable PostgreSQL audit
capability,” not two independently useful choices called audit policy and audit
PostgreSQL. Splitting the unimplemented feature into `audit` plus `auditpg` would
give the initial consumer two modules while providing no alternate persistence
implementation. Keeping one module also allows the same audit choice to return
ordinary repository, service and storage middleware without multiplying packages
by seam.

The module may organize implementation by files or internal packages:

```text
audit/                         one published module
  policy.go                    declared resources/actions/fields
  repository.go               crud.Middleware factory
  service.go                  future port middleware factory
  storage.go                  future storage middleware factory
  postgres.go                 initial persistence profile
  history.go                  protected reader
  observer.go                 optional audit-local typed hook
```

This is a shape, not an accepted file manifest. A later backend module is justified
only if a consumer can select it independently, it implements an accepted audit
owner seam, it isolates one third-party ecosystem and it imports no other optional
extension. If the initial audit module directly owns PostgreSQL dependencies, that
later decision first requires an explicit compatibility refactor to a
dependency-neutral owner seam; the new backend must not inherit the PostgreSQL
graph accidentally. It is not permission for `auditotel`, `auditevent` or
`auditstorage`.

## Dependency direction

```text
application composition root
  |
  +-- constructs audit runtime and PostgreSQL profile
  +-- applies ordinary crud/port/storage middleware
  +-- maps verified actor/scope and optional event references
  +-- owns transaction, ordering and lifecycle
  |
  +--> root dependency-light seams
  +--> audit module ----------> reviewed PostgreSQL ecosystem
  +--> tenancy module --------> root seams
  +--> eventpg module --------> root seams + PostgreSQL ecosystem
  +--> vvotel module ---------> root seams + OTel API

audit ------------X--------> tenancy / eventpg / vvotel / storage backends
independent extension -X---> audit
root -------------X--------> audit

future audit backend adapter ----> accepted audit owner seam + one ecosystem
```

An unpublished application/conformance module may import all selected extensions.
Production packages do not.

## Product intent and vocabulary

Audit answers who performed a declared operation, under which bounded trusted
context, what approved safe evidence changed, whether it committed, and who may
inspect that history.

| Term | Meaning |
|---|---|
| revision | grouping record for one committed auditable unit of work |
| item | one declared resource/action/subject inside a revision |
| subject | protected logical resource identity, never a telemetry label |
| actor | verified user/service identity mapped through audit policy |
| action | stable machine identifier such as `orders.status_transition` |
| value/diff | declared canonical before/after field evidence or redaction state |
| attempt evidence | separately configured record of a denied/failed security action |
| history reader | purpose/role/scope protected query capability |
| retention profile | policy for hold, archive, erasure and destruction conflicts |
| correlation reference | optional protected link, never audit authority by itself |

## Non-negotiable audit invariants

- Every audited resource, action and captured field has a declared purpose and
  owner. “Audit everything” is not a policy.
- New model fields never enter audit evidence implicitly. Generic model JSON,
  reflection dumps and arbitrary context maps are forbidden.
- Transactional mode means mutation and revision/item/value rows commit or roll
  back in one proven PostgreSQL transaction.
- A denied action does not create a successful revision. Attempt evidence has a
  separate type, policy, storage-failure consequence and rate limit.
- Actor, scope, reason and source come from trusted typed application inputs, not
  raw JWT claims, headers, baggage or request bodies.
- Field values use declared canonical versioned codecs and explicit redaction.
  Secrets, credentials, large blobs and unbounded collections are absent by
  default.
- Audit history is not reachable through an ordinary CRUD preload/filter endpoint.
  Reader role, purpose, tenant/scope and field projection are independently
  authorized.
- Audit retention, legal hold, correction, archive, backup and cryptographic
  erasure conflicts are explicit. No broad generic delete helper resolves them.
- Event history may be linked but is not copied and is not audit evidence by
  implication.
- Traces and logs are diagnostic and lossy. They never carry before/after values
  and never determine whether audit succeeded.
- No async writer/exporter/retention worker starts implicitly.

## Data shape to freeze at A0

| Record | Required bounded fields | Prohibited defaults |
|---|---|---|
| revision | ID, commit/order time policy, actor kind/reference, approved scope reference, source/reason code, schema version | raw claims, tokens, headers, request body, free metadata |
| item | revision ID, resource, subject reference, action, result, field-schema version | whole model JSON, SQL, stack trace |
| value | item ID, declared field path, codec version, before/after/redaction state | secrets, blobs, unbounded relations/collections |
| access/export | actor, purpose, bounded query class, result and time | copied audit contents in ordinary logs/telemetry |
| lifecycle | retention class, hold/archive/correction state and provenance | unaudited broad destructive command |

Subject, actor and scope references may be necessary in protected audit storage.
That does not authorize their appearance in metrics, traces, URLs or ordinary logs.

## Provisional construction and repository composition

The snippets below describe the intended type relationships only. None is an
accepted API.

```go
// Illustrative only: one extension module, PostgreSQL initial profile.
auditor, err := audit.New(audit.Config{
    Persistence: audit.PostgreSQL{
        DataSource: auditSource,
    },
    Manifest: manifest,
})
if err != nil {
    return err
}

core := crud.Chain(
    baseCore,
    security.Gate(orderPolicy),
    audit.Repository[Order, OrderID](auditor, audit.Policy[Order]{
        Resource: "orders.order",
        Actions:  audit.Create | audit.Update | audit.Delete | audit.Restore,
        Subject:  audit.PrimaryKey[Order](),
        Fields:   audit.Fields("status", "amount", "currency"),
    }),
    faults.Enrich[Order, OrderID](),
)
orders := crud.Wrap[Order, OrderID, OrderUpdate](core)
```

`crud.Chain` owns composition. The first middleware listed is outermost. In this
shape the security layer authorizes before invoking audit, so a refusal does not
become successful committed evidence. The final order with tenancy, faults and
diagnostics is frozen by A0 tests; the audit package never constructs those
unrelated layers.

The first accepted slice targets one explicit repository mutation under a proven
transaction. A service decorator alone cannot manufacture atomicity around an
opaque service that owns an undiscoverable transaction.

## Future service and storage factories

One audit module may later return middleware for other accepted base seams:

```go
// Illustrative only. These base chains and factories are not implemented.
service := port.ChainService(
    baseService,
    tenancy.Service[Order, OrderID, OrderUpdate](tenantPolicy),
    audit.Service[Order, OrderID, OrderUpdate](auditor),
    vvotel.Service[Order, OrderID, OrderUpdate](telemetry),
)

store := storage.Chain(
    baseStore,
    audit.Store(auditor, objectPolicy),
    vvotel.Store(telemetry),
)
```

The service factory is accepted only when it can state whether it records an
application action, an attempt, or transactionally committed data evidence. If
the service transaction is opaque, it may not advertise transactional audit.

The storage factory records a declared logical object action/reference, never a
pre-signed URL, credential, body or backend-specific value. It imports root
`storage` only and does not import `storageminio` or another backend module.

## Wrapper and capability obligations

An audit wrapper is a policy boundary, so compiling through embedding is not
enough. Every accepted factory carries a D-030/D-061-style inventory.

### Repository obligations

- All current `crud.Core` methods have an explicit unaudited-forward,
  audited-forward or fail-closed decision.
- `Meta`, `Next` and source identity are forwarded only according to their named
  navigation/identity contracts.
- Executable optional effects—including batch insert, restore, scoped restore,
  tombstone load, scoped save/delete and unsafe bulk operations—are asserted on
  the exact outer value. Lookup never walks through an opaque wrapper.
- An effect that audit can preserve is explicitly authorized, captured and
  forwarded exactly once. Otherwise it refuses before mutation I/O.
- `RestoreSupport` or another descriptive support query remains consistent with
  the exact executable method exposed by the wrapper.
- `crud.Option`, batch options, callbacks and predicates execute exactly once;
  audit does not resolve/replay them to infer victims or before state.
- Bulk evidence names the exact successfully mutated set or an explicitly approved
  bounded summary. It never fabricates per-row completeness from a count alone.
- Both orders with security, faults and an opaque unrelated middleware retain the
  same capabilities or the documented fail-closed result.

### Service obligations

- Every `port.Service` method, `Meta` and `Paths` is forwarded exactly once.
- Optional restore uses the base's honest dynamic-type/provider discovery. The
  audit wrapper neither erases restore nor always advertises it and later reports
  unsupported.
- A service-level action is not called transactionally audited unless its exact
  transaction-bound recorder is part of the accepted seam.

### Storage obligations

- Every `storage.Store` method has an explicit audit/forward decision and
  `Capabilities` is forwarded exactly.
- `io.Reader` inputs and an `Open` result are never consumed, duplicated or
  buffered by the audit wrapper merely to inspect content.
- An `Open` audit item describes the method-return access decision. It does not
  claim complete download/reader consumption unless a separately accepted stream
  lifecycle seam can prove that event.
- Temporary URLs, object keys and backend metadata stay out of telemetry and
  ordinary logs; policy stores only a governed logical reference when required.

Method inventory and capability-matrix tests fail whenever a base seam grows
without an audit decision.

## Transaction and atomicity contract

The initial PostgreSQL profile supports exactly these truthful modes:

| Mode | Required authority | Honest result |
|---|---|---|
| Transactional mutation audit | Mutation and audit writer share the same exact PostgreSQL transaction/source | Data and revision/item/value commit or roll back together |
| Standalone audited mutation | Wrapper owns one documented transaction around one supported mutation | One revision per committed operation |
| Multi-resource revision | Caller-owned UoW binds all supported mutations and the audit writer | One revision groups only work in that transaction |
| Asynchronous/attempt/export evidence | Explicit outbox/worker policy | At-least-once and never labelled mutation-atomic |
| Unsupported/mismatched source | No proven common transaction | Refuse before audit or mutation I/O in strict transactional mode |

The implementation must prove exact datasource identity and transaction binding;
matching a connection string or tracing parent is not proof. A wrapper does not
open a second hidden transaction when one is already bound.

Audit SQL failure in transactional mode returns an error and rolls back the
mutation. A disconnect around commit follows a documented unknown-outcome and
idempotency/reconciliation path; callers do not blindly repeat a possibly
committed action. A failed/rolled-back transaction leaves no revision claiming
success.

Before/after capture is read and serialized under the same authorized transaction
and exact victim predicate. If a repository/ORM/raw SQL path cannot expose safe
pre-state and final committed semantics, that action is unsupported or uses an
approved action-only evidence policy. Audit never serializes a stale in-memory
object as a guessed database diff.

## Tenancy and scope boundary

Audit APIs do not import a tenancy extension type. A normal revision uses one
bounded audit-owned scope reference or an accepted root-neutral scope value:

```go
// Illustrative application projection; audit imports no tenancy module.
scope := audit.ScopeRef(tenantScope.OpaqueAuditReference())
page, err := history.Search(ctx, scope, query)
```

The application maps verified tenant state into audit vocabulary. Shared-table
and database-per-tenant profiles separately prove missing scope, wrong route,
pool/session fencing, RLS/predicate defense and lifecycle readiness. No scope
failure falls back to a global/default audit store.

A cross-tenant investigation requires a separate short-lived purpose-bound grant
with a bounded cohort, and its issuance/use is itself protected evidence. It does
not add `audittenancy` or broaden the ordinary history reader.

## Event-source boundary

Event and audit records remain independent. Application code may map a bounded
event result into an audit-owned reference inside a proven shared UoW:

```go
// Illustrative only. The application owns toAuditRef and imports both modules.
err := unitOfWork.Within(ctx, func(txCtx context.Context) error {
    commit, err := events.Append(txCtx, request)
    if err != nil {
        return err
    }
    return auditor.RecordDomainCommit(txCtx, toAuditRef(commit))
})
```

Neither module imports the other. The mapping carries no event payload or raw
stream metadata. A reader follows the reference only after satisfying both audit
and event-history authorization. Different databases mean explicit asynchronous
delivery or refusal of the atomic claim, never a hidden coordinator.

## Storage evidence boundary

Audit may retain a digest and an opaque root `storage` reference for one approved
evidence kind. Application wiring promotes the object and then records the
reference according to the transaction/lifecycle policy. Audit imports no MinIO,
S3 or filesystem adapter and stores no signed URL or credential.

Cross-backend conformance belongs in an unpublished application fixture. Object
retention, versioning, legal hold and audit retention must agree before the audit
item claims durable evidence.

## Telemetry boundary

The audit module imports no OpenTelemetry package and no `auditotel` package
exists. Applications may use generic `vvotel` service/storage middleware around
the same base operations. Audit-specific lifecycle signals are allowed only as:

- a consumer-local adapter to an audit-owned typed event/observer hook;
- a later neutral base hook justified independently; or
- an explicit deferral.

The hook contains audit-domain enums and bounded outcomes, not OTel types. It is
panic-isolated and cannot change transaction results. The audit module never
accepts a global provider/exporter and does not start an exporter.

Trace correlation is optional, sampling-independent and protected. No actor,
subject, scope, before/after value, reason, filter, SQL or raw error appears in a
span/metric/log. Exporter failure never rolls back an otherwise valid audit
transaction solely because diagnostics are unavailable.

## History, privacy and lifecycle gate

The first reader is one-subject, one-scope and purpose-bound. It uses bounded
filters, stable cursors bound to authorization context and role-specific field
projection. Ordinary application resource access does not imply audit access.

Every field codec has old/current/future fixtures. Unknown versions fail closed
without exposing raw values. Corrections append a linked authorized record; they
do not rewrite historical truth. Retention, hold, archive, restored backup,
redaction and key-destruction paths have explicit precedence and read behavior.

Exports require their own recipient/purpose/schema/idempotency contract. If an
outbox is used, it is transaction-local audit intent with at-least-once delivery;
the external transport remains an application/independent adapter choice and no
audit × broker package is created.

## Delivery plan

### A0 — activate, freeze boundary and policy

1. Name one audited resource/action, purpose, owner, actor class, scope topology,
   fields/redactions, reader roles, retention class and failure consequence.
2. Accept one-module ADR for `audit`, its PostgreSQL initial profile, dependency
   allow-list and explicit forbidden package names.
3. Freeze the first exact repository mutation and transaction authority; do not
   accept a generic service wrapper as atomicity evidence.
4. Inventory `crud`, `port` and `storage` methods/capabilities and identify only
   the base adapters justified by current consumers.
5. Resolve the executable `crud.ExistsUnscopedOf` walk before publishing the
   audit repository middleware; opaque neighbours cannot expose inner effects.
6. Freeze first-listed-outermost order, nil handling, policy-before-capture,
   error identity, panic behavior and exact-effect rules.
7. Freeze revision/item/value schema, canonical codecs, privacy classes and
   manifest compilation.

Exit evidence:

- one module graph is `audit -> root + reviewed PostgreSQL ecosystem` only;
- root and every other optional extension have no reverse/mutual import;
- no public API is accepted by an illustrative snippet;
- a complete method/capability matrix exists before the decorator API;
- transaction mismatch and unsupported mutation paths fail before I/O;
- a canary policy proves unlisted fields never enter stored evidence.

### A1 — one transactional repository mutation

1. Create the one audit module and PostgreSQL schema/profile.
2. Implement one explicit repository mutation through `crud.Middleware` and the
   existing base `crud.Chain`.
3. Persist one revision, item and bounded canonical value set in the same proven
   transaction as the mutation.
4. Implement commit, rollback, audit-write-fault, concurrent-victim, cancellation
   and unknown-commit tests against live PostgreSQL.
5. Add D-030/D-061 method inventory, opaque-neighbour and both-order capability
   conformance.

Exit evidence:

- committed mutation has exactly the declared evidence;
- rollback/denial/no-op has no false success revision;
- an audit storage fault cannot leave a claimed transactional mutation committed;
- raw SQL, unsupported bulk and unlisted actions fail closed or remain explicitly
  outside the policy manifest;
- no OTel, event, tenancy, storage-backend or ORM module enters production imports.

### A2 — protected history reader

1. Add one-subject, one-scope, purpose-bound reader and role projections.
2. Add cursor binding, denial, hostile query and field-level privacy tests.
3. Add oldest/current/unknown codec fixtures and append-only correction semantics.
4. Add access evidence without exposing audit contents to ordinary diagnostics.

Exit evidence includes direct SQL least-privilege tests, negative cross-scope
queries, bounded page/cursor behavior and privacy canary scans.

### A3 — additional base adapters and operation coverage

1. Add service or storage middleware only after its base chain is accepted and a
   concrete audit use can state honest atomicity.
2. Expand repository verbs only with exact victim/before-after semantics.
3. Test service restore, CRUD optional effects and storage capabilities in both
   orders with two unrelated opaque middleware.
4. Keep custom domain commands application-local until a second real use justifies
   a neutral owning seam.

No milestone adds an audit-owned chain or pairwise package.

### A4 — lifecycle and independent composition

1. Add retention inventory, hold-aware dry run and restored-backup rehearsal before
   destructive lifecycle automation.
2. Add event commit references, storage evidence, telemetry hooks, cross-tenant
   investigation or external export one at a time through application wiring.
3. For each scenario, prove no mutual production import and no broadened data/read
   authority.
4. Add an independent backend module only after a separate consumer/backend ADR,
   never for symmetry.

## Verification matrix

| Area | Required proof |
|---|---|
| One extension | Exactly one audit module represents the initial durable PostgreSQL audit choice |
| Root optionality | Root and non-audit consumers have no audit or PostgreSQL audit graph |
| Import direction | Audit imports root seams and its reviewed PostgreSQL ecosystem only |
| No combinations | No audit × OTel/event/tenancy/storage/backend/ORM/broker package exists |
| Base composition | Every factory returns ordinary base middleware; first listed is outermost and nil is skipped |
| Method totality | Every base verb has an audit/forward/refuse decision and additions fail tests |
| Capability safety | Exact effects never tunnel; service restore and storage capabilities remain honest |
| Options/payloads | Options, callbacks, readers and payloads execute/pass exactly once without inspection replay |
| Transaction | Mutation and evidence share one exact source/transaction or strict mode refuses before I/O |
| Failure | Denial, rollback, audit SQL fault, cancellation and unknown commit have distinct truthful outcomes |
| Policy/privacy | Manifest is allow-list based; canaries remain absent from storage, reader, export and diagnostics |
| Reader | Scope, purpose, role, cursor and field projection fail closed |
| Event | Application-owned bounded mapping; neither module imports the other |
| Tenancy | Audit-owned/root-neutral scope value; no tenancy type in public audit API |
| Storage | Root logical reference only; no backend import or signed URL/credential persistence |
| Telemetry | No OTel import or auditotel; exporter/no-op/sampling changes no audit result |
| Lifecycle | Hold/archive/correction/restore/destruction behavior is rehearsed and authorized |
| Modules | `GOWORK=off` fixtures cover root-only, audit-only and multi-extension application composition |

## Initial non-goals

- Automatic auditing of every table, model, field, ORM flush or raw SQL statement.
- Generic serialization of models, claims, context or errors.
- A separate `auditpg` module before an independent backend decision exists.
- Pairwise/combination packages or extension-to-extension imports.
- Audit as an event store, trace/log substitute or analytics warehouse.
- Broad audit CRUD/search/export or unaudited break-glass access.
- Cross-database transactional claims or a generic transaction coordinator.
- Automatic async writers, exporters, retention jobs or background goroutines.
- Universal tamper-proof, legal-compliance or exactly-once claims.
- Arbitrary attachments, URLs, credentials or unbounded relation diffs.

## Definition of done

The first durable audit release is complete only when:

1. one accepted ADR names the audit module, PostgreSQL initial profile, dependency
   boundary, forbidden combinations and exact transaction authority;
2. exactly one audit extension module exists and no audit pairwise package exists;
3. one named repository mutation commits or rolls back its revision/item/value
   evidence atomically in live PostgreSQL tests;
4. the policy manifest, canonical codec corpus, redaction canaries, actor/scope
   mapping and failure consequence are reviewed;
5. every shipped base middleware passes first-listed-outermost, nil, method
   inventory, opaque-neighbour and exact-capability conformance;
6. unsupported raw/bulk/custom/ORM paths are refused or visibly excluded rather
   than covered by a generic `audited=true` claim;
7. one protected history reader passes purpose, scope, role, field, cursor,
   least-privilege and hostile-query tests;
8. event, tenancy, storage and telemetry scenarios are application composition
   fixtures with no production extension-to-extension import;
9. isolated `GOWORK=off` graphs prove root-only, audit-only and composed consumers;
10. retention/hold/archive/correction/restored-backup behavior and incident runbooks
    are reviewed before lifecycle automation;
11. release documentation treats `auditpg-v1` as a profile/schema version, not a
    second required module, and labels provisional versus implemented APIs;
12. `git diff --check` and applicable documentation/module checks pass without
    production-code changes from this roadmap revision.
