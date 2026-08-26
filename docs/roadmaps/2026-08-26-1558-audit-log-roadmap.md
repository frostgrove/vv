# Audit log roadmap — Hibernate/Envers-style revisions — 2026-08-26 15:58 +05

This roadmap proposes a durable audit-log satellite for vv applications. Its
model is inspired by Hibernate Envers: one revision identity/time plus auditable
entity/action history and optional custom revision metadata. It must be adapted
to vv's explicit context, policy, transaction and multi-tenant boundaries—not
copied as ORM magic or treated as a generic event-sourcing replacement.

## Reference model

Hibernate Envers demonstrates the valuable core idea: audit rows are grouped by
a revision entity containing revision number/timestamp, and applications can
add controlled metadata to that revision entity. [Hibernate Envers revision log](https://docs.hibernate.org/envers/3.6/reference/en-US/html/revisionlog.html)

Study it for revision grouping, history query ergonomics and custom revision
metadata. Do not import Hibernate semantics wholesale: vv must support multiple
repository adapters and must make capture/atomicity/redaction/policy visible,
rather than hidden in entity annotations and ORM flush hooks.

## Architectural decision

Audit is a separate satellite. It owns durable evidence of selected domain/data
actions, its retention/query authorization and revision format. It does not own
the root error contract, PostgreSQL event store, authentication provider, global
logger, OpenTelemetry exporter or every database write in process.

| Module | Owns | Must not claim |
|---|---|---|
| root vv | existing CRUD/policy/error/context seams | durable audit storage |
| `vv/audit` | audit action/revision/value policy contract | an ORM or identity provider |
| `vv/auditpg` | chosen PostgreSQL audit tables/transaction adapter | all database engines |
| `vv/auditotel` | optional safe trace correlation | audit completeness from spans |
| event PG satellite | append-only domain-event history | actor/request-level audit by itself |
| tenancy satellite | tenant scope/routing | audit authorisation bypass |

The primary initial backend should be PostgreSQL if it must participate in the
same transaction as current CRUD/audit records. A write-to-log pipeline or SaaS
audit exporter cannot claim the same atomic evidence; it would be an explicit
asynchronous/at-least-once integration, not an equivalent implementation.

## Product intent

A resource opts into an audit policy that declares actions, subject identity,
revision grouping, fields/relations allowed to capture and redaction. At a
transaction boundary the audit decorator writes a revision and item records with
the domain mutation according to documented atomicity. An authorized auditor can
query history; ordinary resource reads cannot discover it.

```go
orders := audit.DecorateRepo(ordersRepo, audit.Policy[Order]{
    Resource: "orders.order",
    Actions: audit.Create | audit.Update | audit.Delete | audit.Restore,
    Subject: audit.PrimaryKey[Order](),
    Fields: audit.Allow("status", "amount", "currency"),
    Redact: audit.Deny("payment_token", "internal_note"),
})
```

The final API is illustrative. It must make these rules real:

- audit is opt-in per declared resource/action, no magical capture of every
  struct/field/SQL statement;
- capture happens after policy validation and only for the committed mutation
  semantics; failed writes have a separately named security/attempt audit policy;
- one transaction/revision can group multiple mutations with actor/request/safe
  context provided by caller; revision metadata is bounded/declared;
- before/after value capture follows explicit field serializers/redaction and
  never blindly serializes model/command/error/principal claims;
- audit query/read is separately authorized and tenant-scoped; it is not a
  preload or ordinary repository endpoint;
- audit data retains enough subject/action/time/revision evidence for purpose,
  but does not become a second ungoverned PII/data lake;
- OTel trace/span correlation is optional and sampling-independent; event source
  events remain domain history with different intent/retention/read semantics.

## Vocabulary

| Term | Meaning |
|---|---|
| revision | durable grouping record for one committed auditable unit of work |
| audit item | action on one declared resource subject within a revision |
| subject | auditable resource logical identity; access-controlled/high cardinality |
| actor | verified principal/service identity captured per audit policy |
| action | stable machine enum: create/update/delete/restore/etc., not UI copy |
| before/after | declared canonical field snapshots/diff, never generic model dump |
| redaction | intentional irreversible omission/masking/tokenization policy |
| retention | legal/business lifecycle for audit evidence |
| audit query | separately authorized historical read/search/export operation |
| attempt audit | optional evidence of denied/failed security action, distinct from commit |
| trace correlation | optional trace/span reference, never required for audit completeness |

## Non-negotiable invariants

1. **Audit has a declared purpose.** Each resource/action/field capture policy
   names business/regulatory/operator reason and an owning team.
2. **No generic serialization.** `json.Marshal(model)` is not an audit strategy:
   it captures future secret fields accidentally and destabilizes history schema.
3. **Committed mutation evidence is atomic where promised.** If policy says
   transactional audit, mutation and audit revision commit/rollback together in
   one PostgreSQL transaction; otherwise API calls it async/best effort clearly.
4. **Capture is post-policy, pre/post mutation by declared semantics.** Denied
   action does not create a fake successful revision; security attempt evidence
   is separate action/type with its own privacy/retention policy.
5. **Revision metadata is closed.** Actor, tenant scope, request/correlation,
   reason code and source are declared/bounded; arbitrary context/claims map is
   forbidden.
6. **Audit read is protected.** No ordinary CRUD filter/preload/export exposes
   audit rows; a named audit-query service enforces tenant/role/purpose scope.
7. **Audit values are canonical and versioned.** Field serializers encode stable
   schema; changes have reader/migration strategy and never rewrite old rows.
8. **Redaction is designed, not logging.** Secrets/tokens/passwords/credentials,
   raw PII and large blobs are omitted or transformed before persistence; audit
   does not assume encrypted disk excuses indiscriminate capture.
9. **Retention/deletion conflict is explicit.** Soft delete, tenant deletion,
   GDPR/legal hold, key destruction and event-history retention cannot be solved
   by a generic `DELETE FROM audit` helper.
10. **No telemetry substitution.** Dropped/expired trace and logs never reduce
    audit completeness; trace contains no audit before/after snapshots.
11. **No event-source substitution.** Domain events may help reconstruct business
    history but do not automatically contain actor/authorization/field evidence.
12. **No background writes by surprise.** Async audit/export worker exists only
    in explicitly configured satellite with delivery/idempotency contract.

## Audit data shape to decide in ADR

| Record | Required fields | Prohibited default fields |
|---|---|---|
| revision | ID, committed time, actor ref/class, tenant scope ref where authorized, source/action context, schema version | raw claims, auth token, request body, raw IP/headers unless approved |
| item | revision ID, resource name, subject reference, action enum, result/commit state, field-schema version | arbitrary model JSON, SQL, full error stack |
| value/diff | item ID, declared field path, canonical safe before/after/redaction state | secrets, unbounded collections/blobs, implicit relation graph |
| access/export | actor/purpose/filter/result/when | audit contents in telemetry/default logs |
| retention state | policy/hold/archive/tombstone class | broad destructive command without approval |

The data model needs a deliberate choice about subject/actor identity privacy. It
will normally be necessary for audit read, but must be stored in a protected
table/encrypted/tokenized representation according to application requirements,
not copied into metrics/traces or non-auditor logs.

## Revision grouping model

One application transaction may mutate multiple auditable resources. It produces
one revision and several items if and only if all changes share the transaction
and actor/context policy. A standalone repository operation without an explicit
transaction can either create a revision per committed operation or be refused
for transactional audit mode; it must not fake a multi-resource atomic revision.

```text
transaction begin
  policy allows order update
  mutate order
  mutate inventory reservation
  write audit revision R (actor/source/safe context)
  write audit item R/order/update + declared field diff
  write audit item R/reservation/update + declared field diff
transaction commit
```

The actual order of audit SQL relative to data SQL is an implementation detail
only after failure semantics are proven. Audit entries must not claim committed
success if transaction rolls back. If database trigger/ORM mechanisms are used
as defence in depth, their rows must reconcile with the same policy rather than
creating a second unstructured audit channel.

## Cross-roadmap synergy

| Domain | Relationship |
|---|---|
| OTel | optional trace/span correlation on revision; no snapshots/actor/subject in trace |
| event source | event append can be audited as action; audit not event store replacement |
| tenancy | audit revision/item/query subject to tenant scope and admin cross-tenant policy |
| storage | audit stores authorized logical object subject/action, not pre-signed URL/blob |
| i18n | action/reason/message IDs are stable; translated text rendered at viewer edge |
| policy | audit captures declared decision/result; policy explanation/principal data remain governed |
| outbox | async external audit/export uses durable at-least-once design, not mutation tx fiction |

---

## A-01 — declared resource/action policy

**Decision.** A resource opts into audit using a compile/configuration-time
policy that names stable resource/action/subject/field serializer/redaction
rules. Unannotated fields and resources are not captured by default.

### Top-level declarative DX

```go
policy := audit.Policy[Order]{
    Resource: "orders.order",
    Subject: audit.PrimaryKey[Order](),
    Actions: audit.Create | audit.Update | audit.Delete,
    Fields: audit.Fields("status", "amount", "currency"),
}
```

### Happy use cases

1. `orders.order` declares create/update/delete and three safe business fields;
   generated validation rejects typo/duplicate resource and field rules at boot.
2. Update from `pending` to `paid` records stable action/resource/subject plus
   canonical declared before/after diff inside one committed revision.
3. Create has an explicit before-absent representation; delete has explicit
   after-absent/tombstone representation rather than serializing a whole model.
4. A policy serializes amount/currency with fixed decimal/currency format,
   making historic audit reader stable across Go struct/tag refactors.
5. One declared sensitive field is redacted/tokenized/omitted by serializer and
   cannot be re-enabled by a caller option on a normal request.
6. Resources that do not need audit stay undecorated and incur no reflection/
   serialisation/audit-write cost.

### Edge use cases

1. Developer adds `payment_token` to model after initial release. It is absent
   until policy change/review explicitly permits a safe representation.
2. Field is a relation/large collection/blob. First policy refuses generic diff;
   domain may audit a bounded summary/action or separate subject resource.
3. Resource name comes from generic type/table name. API requires configured
   logical stable name; code/database refactor cannot rewrite audit taxonomy.
4. Serializer fails on unexpected value. Transactional mode aborts mutation or
   follows explicitly decided audit-failure policy; it cannot silently omit item.
5. An update has no actual declared-field change. Policy chooses explicit no-op
   capture/skip semantics, tested so revision count is not accidental.
6. Bulk operation affects thousands subjects. Policy has bounded batch/revision
   strategy and does not allocate a giant JSON diff or claim individual detail
   it cannot safely persist.
7. A caller asks to record a free-text reason. It must use bounded reason code or
   separately approved protected field; arbitrary request string is not default.

### Invariants and acceptance evidence

- policy manifest lists every audit resource/action/field/redaction/owner/purpose;
- model evolution test proves new fields do not enter historic audit implicitly;
- serializer determinism tests cover nil/zero/decimal/time/enum/relation values;
- hostile value corpus proves secrets/large data stay absent from audit payload;
- no resource/action/field identifier is localized or user-composed.

### First implementation slice

Implement one resource and a hand-written typed serializer/diff before generic
reflection helpers. The first accepted policy format should prefer explicitness
over breadth; only repeated proven patterns deserve generation.

---

## A-02 — transactional revision and failure semantics

**Decision.** For the first PostgreSQL audit adapter, an auditable mutation and
its revision/items share the caller's correctly scoped PostgreSQL transaction.
If no transaction is available, policy either opens/owns an explicit unit or
refuses transactional audit; it never emits an asynchronous audit row labelled
as committed evidence.

### Top-level declarative DX

```go
err := repository.Tx(ctx, func(ctx context.Context) error {
    return auditedOrders.Update(ctx, order)
})
```

### Happy use cases

1. Transaction updates order and writes revision/item; commit makes both visible
   together to authorized reader.
2. Mutation violates constraint/policy and transaction rolls back; no successful
   audit item/revision is visible, while optional attempt-audit follows own flow.
3. Two audited resources change in one caller transaction; one revision groups
   both items with same verified actor/scope/source metadata.
4. Audit SQL fails due storage/schema error. Transactional policy returns error
   and rollback leaves neither mutation nor partial audit claim committed.
5. Nested repository calls use same physical datasource/transaction binding;
   audit decorator proves it does not accidentally open a second unscoped DB tx.
6. A simple single update runs with explicit per-operation transaction policy
   and results in exactly one revision/item after commit.

### Edge use cases

1. Transaction commits but connection response is lost. Caller gets uncertain
   DB result; audit/admin reconciliation uses revision/domain constraints, not
   a fabricated success/absence answer.
2. Audit item insert succeeds then mutation fails. Transaction rollback removes
   item; no orphan revision is queryable as completed audit.
3. Audit revision insert fails after mutation SQL in transaction. Rollback error
   handling retains primary cause safely and never logs field snapshot by default.
4. Wrong datasource/transaction scope is supplied. Existing vv scoped-executor
   guard fails loudly before audit/mutation escape into different transactions.
5. A deferred async job wants audit. It must establish tenant/actor/service
   scope and use its own durable/outbox mode; it cannot inherit expired request tx.
6. Database trigger also records row change. Duplicate/unstructured trigger audit
   is disabled/reconciled by design; two conflicting audit truths are refused.
7. Mass write cannot inspect/freeze precise victim list. It is refused or uses
   separate bounded action model before claiming per-subject audit evidence.

### Invariants and acceptance evidence

- live PostgreSQL rollback/crash/error matrix proves atomic visibility claims;
- revision/item uniqueness/foreign key constraints prevent detached successful
  item without committed revision;
- transaction/datasource mismatch tests execute zero unintended audit/data SQL;
- audit failure policy is per resource/action documented and never best-effort by
  accident;
- removing OTel/log bridge does not change revision/mutation atomicity.

### First implementation slice

Build one live PostgreSQL audited update test with transaction propagation and
rollback first. Do not expose an audit query, before/after general diff or async
export until the central “what exactly did commit?” guarantee is real.

---

## A-03 — actor, source, reason and bounded revision metadata

**Decision.** An audit revision records only declared, verified metadata needed
to explain an action: actor reference/class, source channel, tenant scope under
audit access policy, request/reason/correlation code where approved. It never
serializes arbitrary context values, JWT claims, headers, IP/user-agent, raw
command/body or free-form error/trace data by default.

### Top-level declarative DX

```go
ctx = audit.WithActor(ctx, verifiedActor)
ctx = audit.WithReason(ctx, audit.ReasonCode("customer_request"))
```

### Happy use cases

1. Auth boundary supplies verified actor reference/class; audited update revision
   records it in protected audit representation and regular request path need not
   parse identity/token in audit core.
2. API/service source is one bounded enum (`http`, `grpc`, `job`, `admin`,
   `migration`), allowing audit reader to understand provenance without raw route.
3. A declared business reason code accompanies an approved sensitive change and
   is validated against finite policy enum rather than arbitrary text input.
4. Tenant scope reference is written only in audit storage under tenant/audit
   policy; ordinary logs/OTel bridge still receives only bounded topology/outcome.
5. System/background action has service actor/source class and explicit job/work
   reference policy, not a fake end-user or inherited expired request principal.
6. Trace/span correlation optionally stores a validated opaque value in revision
   metadata; it remains empty/valid when no span is sampled/recorded.

### Edge use cases

1. Actor missing for action that requires it. Transactional audit policy refuses
   mutation or records explicit system/anonymous class only where decision allows.
2. Raw JWT claims include e-mail/roles/token. Audit metadata mapper extracts only
   approved actor ref/class; it does not copy claims map for “future usefulness.”
3. Reason arrives as arbitrary 100-KB user text. Policy maps to finite reason code
   or a separately protected/redacted field with limit; no generic free text.
4. Request source wants full URL/IP/user agent. These are privacy/retention fields
   requiring an ADR; source enum alone is default and avoids accidental collection.
5. Tenant scope is absent/misrouted. Audit writes no cross-tenant revision and
   tenancy guard fails data action before audit resolver selects a tenant partition.
6. Trace ID is malformed/attacker-controlled carrier. Correlation extractor limits/
   validates/omits it and never changes audit/mutation correctness or authorization.

### Invariants and acceptance evidence

- revision metadata schema is finite/versioned with field purpose/retention/owner;
- hostile context/claim/header/reason corpus is absent from stored audit values;
- actor/source/reason/tenant/trace policy tests cover human/service/admin/background;
- audit query access controls actor/tenant data separately from ordinary resource reads;
- no audit package imports JWT/router/OTel global/identity-provider implementation.

### First implementation slice

Start with actor class/reference, source enum and optional reason code. Defer raw
request metadata and custom revision entity extensions until each field has a
purpose, privacy/retention rule, serializer and protected-reader test.

---

## A-04 — typed field serializers, canonical values and redaction

**Decision.** Each audit field uses a declared serializer/redaction policy that
emits a stable canonical representation or explicit redaction state. Audit does
not inspect arbitrary struct/maps through reflection, and it does not rely on
database encryption/logging to make copying secrets/huge relations acceptable.

### Top-level declarative DX

```go
Fields: audit.Fields(
    audit.Decimal("amount"),
    audit.Enum("status"),
    audit.Redacted("payment_token"),
)
```

### Happy use cases

1. Decimal amount serializer emits canonical precision/currency representation;
   historical audit reader remains stable through Go type/library formatting change.
2. Enum serializer stores stable machine state value and audit viewer later maps it
   to localized display text through i18n boundary, not localized stored audit copy.
3. Create/update/delete/restore have explicit before/after absent/present/redacted
   representation, allowing viewer to understand action without whole model dump.
4. A sensitive payment token field is omitted/redacted with reason state rather
   than copied encrypted/plain/hash into general audit record by default.
5. Relation change is represented as declared safe subject/reference/action summary
   where needed, not recursive object graph/foreign model serialization.
6. Serializer manifest versions field path/type/format and tests old/new readers
   or migration representation without rewriting historic audit rows.

### Edge use cases

1. Decimal/time/enum serializer receives invalid/nil/overflow/unexpected value.
   It fails safe/returns declared redaction/error; mutation policy handles it explicitly.
2. New model field is added. It appears in no audit item until policy/serializer
   review adds it; generic `json.Marshal` cannot silently capture the new secret.
3. Collection is huge/order-unstable. Policy records bounded count/summary or
   separate audited subject; it never writes millions diff entries in one revision.
4. Field value is personal data but support wants it. Use a protected purpose/
   tokenization/redaction design, not a “debug audit everything” option.
5. Hash appears attractive for secret value. Stable hash can be correlation/PPI
   leak; any hashing needs keyed/rotating/approved design outside default serializer.
6. Serializer code changes formatting but keeps field name. Version manifest/golden
   reader fixtures treat it as audit schema evolution, not an invisible refactor.

### Invariants and acceptance evidence

- every auditable field has declared type/serializer/redaction/purpose/owner;
- deterministic serializer golden corpus covers nil/zero/decimal/time/enum/text values;
- new model field regression test proves it is absent by default from audit rows;
- secret/large/relation/recursive hostile corpus is blocked/redacted/bounded;
- audit reader can distinguish absent, null, redacted and changed values clearly.

### First implementation slice

Hand-write serializers for one resource/status/amount case. Do not build a generic
reflection diff engine until repeated approved field patterns and historic reader
compatibility demonstrate a narrow safe abstraction.

---

## A-05 — audit query, history reader and export authorization

**Decision.** Audit query is a separate protected application service with its
own action/purpose/tenant/resource/subject/time/pagination/export policy. It is
not exposed through ordinary repository filters/preloads nor inferred from an
admin role alone. Large history access is bounded, audited and cancellation aware.

### Top-level declarative DX

```go
page, err := auditHistory.Search(ctx, audit.Query{
    Purpose: audit.Purpose("support_case"),
    Resource: "orders.order",
    Subject: subject,
    Limit: 100,
})
```

### Happy use cases

1. Authorized support user with declared purpose queries one order's audit
   revisions, receives declared safe field diffs/actor/action/time and pagination.
2. Tenant administrator sees only audit records in verified tenant scope; a
   platform auditor cross-tenant query uses named stronger capability/cohort/audit.
3. Audit viewer renders stable action/field/enum IDs in requested locale at UI edge;
   stored audit values remain language-neutral canonical machine evidence.
4. Export job uses explicit purpose/range/format/retention policy and writes its
   own audit action/result; it does not dump arbitrary table via `GetAll`.
5. Query returns redaction markers and schema version so viewer can truthfully show
   what was intentionally not retained rather than treating absent as unchanged.
6. Pagination/cursor is opaque/bounded and cannot be tampered to access another
   tenant/resource/subject; backend filters are policy composed before query.

### Edge use cases

1. User asks broad no-subject/all-time audit search. Policy requires explicit
   privilege/cohort/time range/batch and may refuse normal support endpoint.
2. Cursor from tenant A/resource X is supplied to tenant B/resource Y. Reader
   refuses invalid scope before returning history or revealing cursor internals.
3. Audit values have old serializer schema. Reader uses versioned decoder/view
   policy or returns controlled unsupported record, never guesses current formatting.
4. Export contains sensitive values/actor identity. Export destination encryption,
   access, retention and delivery are explicit domain/security decisions.
5. Query runs too long. Limit/range/cancellation/backpressure prevents broad table
   scans; no trace/metric labels reveal subject/cursor/tenant identifiers.
6. Auditor tries to use audit history to reconstruct raw deleted event/storage
   bytes. Contract exposes only declared audit representation and states limitation.

### Invariants and acceptance evidence

- audit query is a distinct interface/service, absent from normal resource CRUD surface;
- tenant/purpose/resource/subject/time/page policy matrix has adversarial controls;
- pagination/cursor fuzz corpus avoids value leak/unbounded scans/cross-scope access;
- audit reader schema versions/redaction states have golden fixtures;
- export access/actions are durably audited and default telemetry remains identity-safe.

### First implementation slice

Ship one `HistoryBySubject` protected query with bounded limit, purpose and tenant
scope after transactional write is proven. Defer flexible search/export DSL until
audit access/threat/retention requirements specify safe semantics.

---

## A-06 — bulk writes, no-op changes and mass-action evidence

**Decision.** Per-subject audit requires a precise, bounded victim set and stable
before/after representation. Existing vv bulk write safety rules apply: inspection
and mutation must identify same rows. If an operation cannot guarantee that, it
is refused or uses a separately named summary/batch audit contract.

### Top-level declarative DX

```go
result, err := audited.BulkTransition(ctx, audit.BulkRequest{
    Action: audit.Action("orders.expire"),
    Selection: selection,
})
```

### Happy use cases

1. A bounded key-ordered batch selects exact tenant-scoped victims, captures
   declared fields, applies update and writes one revision with declared item set.
2. A mass action too large for per-item audit uses explicit batch summary: action,
   authorized selection definition/count bucket/result/revision—not fake full diffs.
3. No-op update with unchanged declared fields follows policy: no audit item or
   explicit no-change action, consistently tested and visible to authorized viewer.
4. Bulk failure mid-transaction rolls back mutation/audit together in transactional
   mode; batched multi-transaction process records exact completed/retry states.
5. Tenant/admin scope, reason/purpose and selection method are captured as bounded
   policy metadata, while raw untrusted filter/IDs remain out of generic telemetry.
6. Restore/soft-delete bulk actions have distinct stable action enum and before/
   after tombstone representation rather than abusing generic update label.

### Edge use cases

1. Caller passes pagination/limit that would inspect subset but update all filter
   matches. Operation refuses or uses separate deterministic victim-key contract.
2. Victim changes between inspection/audit capture and final write. Transaction/
   locks/final key predicate prevent audit of a different row than mutation.
3. Batch has 1M rows. Policy uses bound/chunk/resume/progress/audit summary, never
   loads all models/diffs/spans into memory or uses one unbounded revision payload.
4. Some victims policy-denied. Batch contract defines atomic all-or-nothing versus
   per-victim result; no audit claims all success after partial authorization failure.
5. Selection raw filter contains sensitive search/tenant data. Protected audit may
   store a declared selection reference; generic error/trace never copies filter text.
6. Operator retries batch after unknown transaction result. Durable batch/idempotency
   state/reconciliation avoids duplicate/omitted audit/mutation claims.

### Invariants and acceptance evidence

- per-item audit bulk suite proves inspected victims equal written victims under race;
- summary-batch audit schema explicitly differs from individual subject diff schema;
- memory/query/span/event limits are benchmarked for large action fixture;
- no-op/partial/failed/unknown/retry outcomes have precise audit revision semantics;
- raw filters/IDs/victim values remain protected/absent from default observability.

### First implementation slice

Do not audit generic `UpdateAll/DeleteAll` until their victim-selection issue is
resolved. Start with normal single-resource update; later add named bounded bulk
workflow with a dedicated decision and control tests.

---

## A-07 — retention, legal hold, deletion and cryptographic erasure

**Decision.** Audit retention is a declared policy per audit class, not a table
TTL and not an accidental consequence of the retention policy of the primary
resource. The policy names the evidence purpose, minimum and maximum retention,
legal-hold behavior, archive tier, deletion authority and how redacted
identifiers remain usable for historical proof.

### Top-level declarative DX

```go
audit.Policy[Customer]{
    Resource: "crm.customer",
    Retention: audit.Retention{
        Class:       "regulated-customer-change-v1",
        MinAge:      7 * 365 * 24 * time.Hour,
        ArchiveAfter: 90 * 24 * time.Hour,
        Purge:       audit.RequireTwoPersonApproval,
        LegalHold:   audit.HoldStopsPurge,
    },
}
```

The application author chooses an approved named policy, rather than passing a
duration to each write. The operator can inspect the effective policy and its
owner before a lifecycle job moves or destroys any evidence.

### Happy use cases

1. A payment-status change produces a revision in the regulated class. The
   retention worker archives a sealed historical partition after the declared
   hot-read window, keeping the index and authorized-history lookup intact.
2. A customer has a legal hold. The lifecycle worker records that the candidate
   partition was skipped and leaves every covered revision intact.
3. A business policy legitimately expires low-risk configuration-change history.
   The two-person purge workflow records proposal, approval, execution, count,
   evidence range and verification result in a separate retention audit class.
4. An account erasure request removes or cryptographically erases personal
   values according to law while preserving a non-identifying fact that an
   authorized action occurred when that residual evidence is allowed.
5. An auditor searches a revision after it moved to cold storage. The history
   service returns the same logical revision identity and a clearly indicated
   retrieval state; it does not silently fabricate a missing diff.
6. A backup restore brings back a partition later than the primary purge run.
   The reconciliation process uses recorded purge/tombstone evidence to decide
   whether it must be re-purged before the restored database serves requests.

### Edge use cases

1. Product asks for "keep all audits forever" while privacy policy mandates
   erasure. The conflict is escalated to an explicit legal/product decision;
   no engineer solves it by silently deleting fields or retaining raw PII.
2. A revision spans a transaction that touches records with different retention
   classes. The model either forbids the grouping or records item-level lifecycle
   rules with a proof that purging one item cannot corrupt remaining evidence.
3. A legal hold arrives while a purge batch is running. The purge transaction
   re-checks hold state under its lock/transaction and reports uncertain races;
   it never relies on a stale pre-scan alone.
4. The archive provider is unavailable. The worker retains hot evidence and
   alarms; it may not delete primary rows before archival success is verified.
5. Encryption key destruction is requested but replicas, exports, backups or
   object-store versions may preserve plaintext. The runbook names all copies,
   key hierarchy, verification evidence and a truthful residual-risk state.
6. An attacker with ordinary admin access requests retention change. The control
   plane requires a distinct retention authority and records immutable policy
   history; resource-admin privilege alone is insufficient.
7. A clock correction makes rows appear older than their true commit ordering.
   Lifecycle selection uses trusted commit timestamps/monotonic boundaries and
   tolerates clock skew rather than relying on request-provided time.
8. The policy is shortened after old data is written. The system creates a
   governed migration/evaluation record; it does not retroactively claim the
   old policy never existed.

### Invariants and acceptance evidence

- every audit policy maps to an approved retention class with owner and reason;
- a lifecycle candidate cannot be deleted if a matching active legal hold exists;
- archive-before-delete and archive-readback checks are exercised on real storage;
- purge execution has a stable range/selection identifier, approver evidence and
  post-condition count/checksum proof rather than a broad `DELETE` console log;
- subject erasure behavior distinguishes redaction, tombstone, key destruction,
  backup expiry and legal hold instead of using one overloaded `deleted` flag;
- retention jobs neither copy before/after values into telemetry nor make them
  visible in scheduler logs or dead-letter payloads;
- test fixtures cover a restored backup, replayed lifecycle job and a hold/purge
  race, with the expected authoritative outcome written down.

### First implementation slice

Ship the retention-class field, read-only lifecycle inventory and hold-aware
candidate query before automatic purge. The first destructive runner should be
dry-run only, emit protected results, require explicit approval and be rehearsed
against a disposable restored backup.

---

## A-08 — event-sourced commands, CRUD revisions and causal boundaries

**Decision.** Audit revisions and PostgreSQL event streams remain separate
records with explicit optional links. A domain event tells a business fact in an
aggregate's history; an audit revision records authorized actor/action/declared
data evidence about a committed operation. Neither is generated by blindly
copying the other.

### Top-level declarative DX

```go
result, err := orders.Handle(ctx, cmd)
if err != nil { return err }

// Within the same approved unit of work, adapters may record links, not payload copies.
audit.LinkDomainCommit(tx, audit.DomainCommitRef{
    Store: "orders-pg-v1", Commit: result.CommitID, Streams: result.StreamRefs,
})
```

The service author uses a typed, bounded commit reference. They never pass an
event payload, raw stream content or arbitrary event headers into audit metadata.

### Happy use cases

1. A command appends `OrderConfirmed` and updates a synchronous read model. The
   audit revision records `orders.confirm`, the actor, reason code and a link to
   the committed event-store transaction/aggregate reference authorized for audit.
2. A pure CRUD profile change has no domain event. It still creates an audit
   revision because audit coverage is based on declared action policy, not ES use.
3. An event-sourced aggregate emits three events for one command. The audit
   revision contains one action item plus bounded commit/stream references, not
   three copied JSON event payloads.
4. An asynchronous projection handles a committed event. Its operational update
   can create a distinct system-actor audit revision when that projection's
   resource policy calls for it; it does not impersonate the original user.
5. An operator replays a projection. The replay revision has a stable system
   action/reason and run identifier, making it distinguishable from a new command.
6. A support viewer opens an audit row and, only when separately allowed, follows
   a link to an event timeline. Each system enforces its own read authorization.

### Edge use cases

1. The event append commits but audit is configured to another database. The
   configuration may not claim atomic audit; it either uses an explicit outbox
   delivery state or rejects the transactional-audit mode at boot.
2. An event is backfilled/imported from another system. Its historical actor is
   not silently attributed to the current importer; source/import actor and
   original actor reference follow a declared provenance policy.
3. A command is rejected after aggregate replay/policy evaluation. It creates no
   successful audit revision; optional security attempt evidence is distinct.
4. An event upcaster changes old payload interpretation. It never rewrites the
   historical audit diff merely to match a new event view; presentation links
   state their version and readers render both records under their own schemas.
5. A user action triggers an eventual external integration. A later delivery
   retry must not create a duplicate user-action audit revision; it records its
   own delivery/system operation only if declared.
6. A domain event contains PII permitted in protected event history but forbidden
   from audit. The link remains a reference; payload extraction/redaction must
   happen only in authorized purpose-specific reader code.
7. A fraud investigation requires causal trace across event, audit and outbox.
   Correlation is query-time, authorization-checked joining—not a broad duplicated
   correlation blob stored on every row.

### Invariants and acceptance evidence

- audit item schema contains bounded event-store references rather than payload;
- command tests prove one business command produces the specified revision count;
- rejection, replay, import and projection cases have non-ambiguous actor/action;
- an audit reader cannot use a commit reference to bypass event-stream tenancy or
  event-history authorization;
- event revision/upcaster tests prove original audit values stay immutable;
- outbox retry tests prove duplicated delivery cannot duplicate a committed action;
- diagrams distinguish command transaction, event append, audit revision,
  projection transaction and outbox publication boundaries.

### First implementation slice

Keep the first ES/audit integration to a nullable typed `event_commit_ref` on a
revision and one cross-system acceptance test. Do not build generic event-payload
mirroring, a global chronology service or cross-store transaction coordinator.

---

## A-09 — audit query, viewer language, proof and export lifecycle

**Decision.** Historical viewing is a dedicated capability with a limited query
grammar, purpose-aware authorization, field-level visibility and export controls.
The query API returns stable action/reason/message identifiers; UI/API rendering
uses i18n catalogues at the reader edge, never translated prose persisted as the
authoritative audit fact.

### Top-level declarative DX

```go
page, err := history.Search(ctx, audit.Query{
    Scope:     audit.OneTenant(scope),
    Resource:  "orders.order",
    Subject:   audit.SubjectRef(orderID),
    Actions:   []audit.Action{audit.Update, audit.Delete},
    From:      start,
    To:        end,
    Purpose:   audit.PurposeSupportInvestigation,
    Page:      audit.Cursor(cursor),
})
```

The API is deliberately not `repo.List("audit_items", filters...)`. Query
options are typed, bounded and validated before database SQL or export work.

### Happy use cases

1. A support agent with purpose approval sees the history for one authorized
   customer, including localized action labels and only fields allowed to that
   role. Stable keys remain available for investigation/export verification.
2. A compliance officer asks for changes to a resource over a bounded time range.
   The service uses cursor pagination and a protected result limit, not an
   unbounded OFFSET scan or whole-table download.
3. A requester generates an export. The system creates an export job with scope,
   purpose, columns, policy version, requester and expiration; resulting object
   access is time-bound and subject to audit itself.
4. A viewer uses a locale lacking a new audit-action translation. The presentation
   layer falls back deterministically while authoritative action/reason enum is
   unchanged and searchable.
5. A revision contains a redacted field. Viewer shows a truthful `redacted` state
   and policy explanation key, rather than an empty value that looks unchanged.
6. An auditor needs integrity evidence. The reader returns stored schema version,
   stable revision identifier and any declared seal/checkpoint state without
   pretending cryptographic integrity is present if it is not implemented.

### Edge use cases

1. Search filter requests multiple tenants but caller only has one-tenant scope.
   Query fails closed; it does not quietly drop foreign tenants and return a
   misleading apparently complete report.
2. The result would expose an actor whose identity is restricted to another role.
   Field-level viewer policy replaces it with an approved pseudonym/withheld marker.
3. A cursor from one query/purpose/tenant is supplied to another. Cursor carries
   protected query binding/version and is rejected rather than becoming a confused
   pagination capability.
4. Export job fails after writing partial object. Storage stages and deletes/quarantines
   incomplete output; no final signed URL is issued and audit says `failed`.
5. A report asks for free-text full-text search over audit diffs. It is refused
   until a separate privacy/indexing design exists; default is structured fields
   and authorized subject/action/time searches only.
6. Viewer locale includes attacker-controlled BCP-47 string or translation key.
   The i18n resolver validates locale/catalogue and does not use it for SQL,
   object key, template evaluation or audit identity.
7. A legal hold prevents export deletion but export TTL expires. The system treats
   export artifact retention separately and alerts for manual governance rather
   than silently retaining a broad copy forever.
8. A CSV cell begins with formula syntax. Export encodes it safely for the chosen
   format; consumer-generated download must not turn audited values into formulas.

### Invariants and acceptance evidence

- every history search/export has named reader, scope, purpose and decision result;
- SQL/explain tests prove scope predicates and cursor bindings are preserved;
- UI/API contract carries stable action/reason/policy keys plus localized display
  separately; no translated phrase is used for filter/authorization behavior;
- redacted/omitted/unavailable/expired fields have distinct output states;
- export staging, download authorization, TTL, access log and deletion behavior
  are integration-tested with the storage satellite;
- audit access itself is recorded without placing the viewed diffs/subjects in logs;
- reporting throughput and time-range limits have a documented operator response.

### First implementation slice

Implement a single-subject, single-tenant cursor history endpoint with an
explicit `PurposeSupportInvestigation` capability. It returns safe revision/item
metadata and a small declared diff set. Postpone report builder, full-text and
multi-tenant export until their authorization and privacy design is proven.

---

## A-10 — storage references, attachments and evidentiary blobs

**Decision.** Audit does not store arbitrary attachments or raw document bytes in
revision JSON. When an audit purpose needs evidence related to an object, it
stores a governed logical storage reference, declared digest/version/retention
class and access policy. Object storage remains the authority for blob lifecycle.

### Top-level declarative DX

```go
audit.Attach(item, audit.EvidenceRef{
    Kind:       "kyc.decision-pdf",
    Object:     storage.ObjectRef("evidence/opaque-key"),
    Digest:     digest,
    Version:    objectVersion,
    Visibility: audit.EvidenceRestricted,
})
```

The API accepts a pre-approved evidence type and typed object reference. It
never accepts a filesystem path, S3 URL, pre-signed URL, MIME guess or raw bytes.

### Happy use cases

1. A signed decision PDF is staged, scanned and promoted by the storage satellite.
   The committed audit item records its logical identity/digest/version after
   promotion, making later authorized verification possible.
2. An evidence object is superseded. Audit preserves the historical object version
   reference under retention policy instead of pointing silently at newest bytes.
3. A viewer is allowed to see audit metadata but not content. They receive a
   `restricted evidence` marker; download requires an additional storage policy.
4. A filesystem development adapter and an S3-compatible production adapter pass
   the same evidence-reference conformance tests without exposing their paths.
5. A tenant storage key is scheduled for deletion. Retention workflow checks
   linked audit evidence and legal holds before allowing irreversible removal.

### Edge use cases

1. Caller supplies `https://bucket/...` or a pre-signed URL. API refuses it:
   URL credentials/query strings are transport artifacts, not durable evidence.
2. Object promotion succeeds but audit transaction rolls back. Reconciler finds
   an unlinked staged/promoted object under a bounded quarantine policy; it does
   not create a fabricated audit row later without provenance.
3. Audit commits but object finalization fails. Transactional claim is not made
   unless a jointly proven storage transaction exists; item records pending/failed
   evidence state and workflow handles recovery.
4. Object digest algorithm changes. Reference includes algorithm/version; reader
   never compares unlike digests or silently recomputes historical claims.
5. An attachment has malware or disallowed personal data. Scanner/classifier
   decision is a separate bounded audit fact; content is quarantined and not
   embedded in trace, error or history-diff JSON.
6. Object-store versioning/lifecycle deletes a noncurrent version linked by audit.
   Storage policy must identify this retention conflict before lifecycle rules run.

### Invariants and acceptance evidence

- audit DB schema has only typed references/digests/declared metadata, never blob;
- evidence reference test passes against secure fs and at least one S3-compatible
  target, including version/promote/failure behavior;
- unauthorized history readers cannot turn a logical reference into a download;
- lifecycle reconciliation detects both orphan object and dangling audit reference;
- pre-signed URL, absolute path, bucket, credentials and object metadata sensitive
  fields are absent from default audit/telemetry/log records;
- backup/restore rehearsal preserves or truthfully flags evidence-reference reachability.

### First implementation slice

Start with a digest plus opaque `storage.ObjectRef` on one approved evidence kind.
Require object promotion before the audit relation is committed. Defer arbitrary
attachments, inline previews and cross-bucket copies.

---

## A-11 — tenancy, cross-tenant investigation and service actors

**Decision.** Audit storage and query obey tenant topology rather than becoming a
tenant-neutral super-database. A normal revision is bound to exactly one verified
tenant scope. A cross-tenant investigation is an explicitly privileged workflow
with a bounded cohort, purpose and separate audit of the investigator's access.

### Top-level declarative DX

```go
grant, err := investigations.Authorize(ctx, audit.InvestigationRequest{
    Purpose: "fraud-case-2026-18",
    Tenants: []tenancy.Ref{tenantA, tenantB},
    Expires: time.Now().Add(2 * time.Hour),
})
if err != nil { return err }

page, err := history.SearchCrossTenant(ctx, grant, query)
```

An ordinary `history.Search` has exactly one tenant scope. The exceptional API
requires a separate capability whose issuance and use are themselves auditable.

### Happy use cases

1. In shared DB mode, revision/item tables include protected tenant identity and
   every normal query is predicate/RLS scoped together with the resource history.
2. In database-per-tenant mode, audit writes share that tenant transaction or
   honestly use a configured local audit backend; no control-plane database
   receives a false atomic copy.
3. A fraud investigator obtains a two-hour, two-tenant purpose grant. Search
   fans out through an authorized coordinator with per-tenant result limits and
   a normalized, clearly partial/failed result representation.
4. A billing batch runs as a service actor. Audit records a verified workload
   identity/class and bounded batch run reference, not a fabricated human user.
5. A tenant is suspended. Ordinary service data and audit queries fail closed,
   while an explicitly authorized incident response procedure can retrieve the
   evidence allowed by suspension policy.
6. Tenant onboarding verifies migrations and audit-policy readiness before traffic
   is enabled, preventing data writes with silently missing revisions.

### Edge use cases

1. A shared DB query has correct resource predicate but omits audit-item tenant
   predicate. Database-side RLS/composite constraints and query-matrix tests stop
   this class of cross-tenant history leak.
2. A per-tenant resolver points at the wrong database after mapping cache staleness.
   datasource identity validation/epoch checks, audit row scope and request abort
   ensure a misroute cannot be mistaken for an authorized revision.
3. A cross-tenant export resolves 1,000 databases. Coordinator enforces named
   cohort/max concurrency/time budget and returns an explicit incomplete status;
   it does not open unbounded pools or hide unavailable tenants.
4. Tenant is renamed. Stable internal ref remains audit key; presentation name is
   resolved at viewer edge and never retroactively changes scope evidence.
5. A support user can impersonate tenant user for app action. Audit records both
   effective actor and authorized impersonator chain under policy, not only one.
6. Tenant deletion requests conflict with audit retention or legal hold. Lifecycle
   reports the governing disposition rather than dropping audit database blindly.

### Invariants and acceptance evidence

- ordinary revision/item/history APIs require exactly one verified tenant scope;
- row and database topology suites run the same core audit behavior assertions;
- no tenant/database identity becomes a metric label, trace attribute or URL path;
- cross-tenant capability is short-lived, purpose-bound, cohort-bounded and audited;
- service/impersonation actors use typed lineage that cannot be supplied by callers;
- migration/readiness probe proves audit schema/policy/version before tenant activation;
- negative tests attempt cross-tenant ID guessing, resolver cache staleness and
  missing scope in jobs/exports and prove fail-closed outcomes.

### First implementation slice

Require single-tenant scope on every revision/item and history query. Add a
topology adapter contract with shared transaction semantics. Leave cross-tenant
search disabled until the dedicated grant, fan-out and compliance review exist.

---

## A-12 — OpenTelemetry, logs and operational diagnostics

**Decision.** Telemetry explains audit pipeline health and causality without
becoming audit content. The OTel satellite emits bounded operation/result/schema
signals and optional correlation reference; it never places actor IDs, subjects,
before/after values, reasons, tenant IDs, raw errors, SQL or export filters into
span names, attributes, metric labels, baggage or logs.

### Top-level declarative DX

```go
func (s *Service) UpdateOrder(ctx context.Context, cmd UpdateOrder) error {
    ctx, span := tracer.Start(ctx, "orders.update")
    defer span.End()

    return auditedOrders.Update(ctx, cmd)
}
```

The service instruments its business operation once. The audit bridge may link
the committed revision to the trace internally, but it owns neither provider
bootstrap nor a global exporter and does not create per-diff spans.

### Happy use cases

1. Operator sees `vv.audit.write` latency/error rate partitioned by action class,
   topology and result class, then uses protected audit search to inspect evidence.
2. A trace has a safe `audit.revision_linked=true` event after commit. An
   authorized operator uses a protected correlation lookup, not a raw revision ID
   exposed in a public trace UI.
3. Audit exporter/retention worker records queue age, batch outcome and retry
   class as bounded metrics; on-call learns it is stuck without receiving payloads.
4. Sampling drops a trace. The audit revision is still complete because its write
   path does not depend on exporter availability or sampled context.
5. A database error is classified as conflict/unavailable/constraint; customer
   error mapping and audit attempt policy remain distinct from log stack details.

### Edge use cases

1. Engineer adds `audit.subject` span attribute to debug production. Static
   schema tests/linter reject it; protected audit reader is the prescribed route.
2. OTel exporter blocks or fails. Write request follows telemetry failure policy
   and does not roll back an otherwise valid transactional audit revision solely
   because observability transport is unavailable.
3. A retry emits many failure logs. Rate/aggregation policy bounds logs/metrics;
   durable audit reflects final declared result and optional separate attempt
   records, not one arbitrary row per logger call.
4. Baggage from untrusted inbound request carries tenant/actor/reason. Audit
   ignores it as authority; trusted app context/policy supplies bounded metadata.
5. Correlation lookup is invoked by a viewer without audit permission. It denies
   rather than allowing trace access to become an audit-read backdoor.
6. A high-cardinality action name is assembled from resource ID. Contracts require
   stable resource/action enum, rejecting dynamic span/metric dimensions.

### Invariants and acceptance evidence

- OTel bridge has no dependency from core audit policy/storage to OTel SDK;
- signal schema registry allows only bounded audit operation/result/topology fields;
- test collector asserts absent actor/subject/tenant/value/filter/URL/SQL payload;
- forced exporter failure/sampling-off test proves audit transaction semantics hold;
- dashboards alert on write failure, backlog, retention lag and reader/export
  denial classes without enabling arbitrary data inspection;
- trace-to-audit correlation requires both systems' authorization gates.

### First implementation slice

Expose only write duration, outcome and revision-commit boolean through the
optional OTel adapter, with a privacy regression test. Do not add trace IDs to
the public audit model until correlation authorization and retention rules exist.

---

## A-13 — repository interception, transaction ownership and ORM compatibility

**Decision.** vv audit wraps named repository/service mutation seams and joins a
caller-owned transaction abstraction. It may provide adapters for SQL, ORM or
generated repositories, but it never claims that an ORM dirty-check hook observes
every meaningful business operation correctly. Repository integration must state
which write shapes are covered and reject unsupported ones in strict mode.

### Top-level declarative DX

```go
tx, err := unitOfWork.Begin(ctx)
if err != nil { return err }
defer tx.RollbackUnlessCommitted()

orders := audit.Decorate(tx.Orders(), orderAuditPolicy)
if err := orders.ChangeStatus(ctx, id, next); err != nil { return err }
return tx.Commit(ctx)
```

The caller can see the transaction boundary and the audited repository surface.
An integration never asks the application to enable hidden global listeners.

### Happy use cases

1. A generated SQL repository implements `Load`, `Insert`, `Update`, `Delete`
   and returns declared before/after state. Audit decorator captures only policy
   fields and writes revision/items through the same transaction handle.
2. A service composes two audited repositories inside one unit of work. One
   revision groups the specified items after the commit succeeds.
3. An ORM adapter supplies an explicit snapshot before mutation and a declared
   changed-field list. Audit serializer sees canonical values, not lazy proxies.
4. A repository has a custom domain operation (`Approve`, `Cancel`). It uses a
   stable audit action enum instead of misleading generic `update` inference.
5. Strict configuration causes an unsupported raw SQL mutation path to fail at
   startup/test wiring rather than creating an un-audited production bypass.
6. A read-only transaction calls history reader. No revision is allocated simply
   because a model was loaded, preloaded, normalized or cache-refreshed.

### Edge use cases

1. ORM flush writes an entity after code has lost the original before state. The
   adapter either captures a declared pre-image at the correct boundary or refuses
   diff audit for that resource; it must not serialize post-flush guesswork.
2. A database trigger mutates an audited derived column. Policy decides whether
   database-derived value is included; tests read final committed value and avoid
   a diff that contradicts database state.
3. A raw `Exec` touches an opted-in table. Strict guard/inventory detects it;
   support for a named audited SQL command is added deliberately, not inferred
   from SQL text at runtime.
4. A repository opens its own hidden transaction while outer operation owns one.
   Adapter refuses nested atomicity ambiguity or uses explicitly supported savepoint
   semantics with documented revision grouping.
5. A retrying ORM performs update twice after transient failure. Only the winning
   transaction creates a committed revision; failed tentative captures stay absent.
6. A cache writes asynchronously after mutation. Cache refresh is not a business
   mutation and does not produce audit noise unless separately declared.
7. Caller forgets to commit. The disposable transaction rolls back data/revision;
   test proves no history row represents a success.

### Invariants and acceptance evidence

- coverage inventory lists every supported repository/mutation shape and its
  before/after acquisition method;
- integration tests use real PostgreSQL transaction rollback/commit paths;
- nested transaction/savepoint behavior has one specified revision outcome;
- raw SQL/ORM bulk/bypass attempts are rejected or explicitly reported by strict mode;
- audit decorator introduces no cyclic dependency on application repository code;
- concurrency/retry tests prove only committed state has a corresponding revision;
- generated documentation tells teams exactly how to make a new command auditable.

### First implementation slice

Support one explicit SQL repository contract and caller-owned unit of work. Add
coverage inventory tooling before any ORM listener. Hibernate/Envers stays an
inspiration for revision semantics, not permission to hide capture behavior.

---

## A-14 — field paths, relations, collection semantics and state evolution

**Decision.** Audit policies describe a bounded schema of scalar fields and
named relation actions. They do not recursively diff arbitrary object graphs.
Relation membership, ordering and large collections have resource-specific
events/items or carefully bounded summary semantics.

### Top-level declarative DX

```go
audit.Policy[Order]{
    Resource: "orders.order",
    Fields: audit.Fields(
        audit.Scalar("status", audit.Enum()),
        audit.Scalar("shipping_address.country", audit.CountryCode()),
        audit.Relation("line_item", audit.MemberChangesOnly()),
    ),
}
```

`shipping_address.country` is a stable policy field path, not reflection into a
future model graph. `line_item` records named add/remove/change relations under
its own subject rules rather than embedding all child models into parent diff.

### Happy use cases

1. A scalar enum changes from `draft` to `confirmed`; diff stores stable enum
   tokens and viewer localizes their labels at render time.
2. A value object changes country but not full postal address. Policy records the
   allowed country path while restricted address details remain redacted.
3. One line item is added. Audit produces a bounded relation item with child
   subject reference/action rather than an array-before/array-after dump.
4. A child record is changed by its own service. Its resource policy writes a
   child revision item and the parent history can show an authorized relation link.
5. Field rename creates new policy schema version and reader maps old `state`
   evidence to stable display meaning without rewriting historical rows.
6. A relation order has business significance. Policy explicitly records move/
   position change with a bounded representation and ordering invariant.

### Edge use cases

1. A list has ten thousand members. Generic diff is refused; operation uses
   named bulk/member evidence or a separately governed summary.
2. A map key is user-supplied/free text. It cannot become dynamic audit field path
   or metric dimension; serializer applies an approved bounded value representation.
3. Field changes `nil` to empty string. Canonical serializer preserves the three
   states: absent/not captured, explicit null, and empty value.
4. A computed field changes due to clock or external lookup. It is excluded unless
   policy explicitly defines why it is evidence and how deterministic values arise.
5. A relation is reassigned across tenants. Normal path rejects it; named migration
   action records source/destination under cross-tenant authority.
6. JSON column adds unexpected keys. Policy captures only declared keys/version;
   generic JSON diff cannot silently start retaining sensitive additions.
7. Parent delete cascades children in database. Policy names whether children get
   individual items, a validated cascade summary or are non-audited; silent loss
   or misleading parent-only claim is not allowed.

### Invariants and acceptance evidence

- audit field paths are statically/configuration validated against allowed schema;
- unlisted scalar/JSON/relation fields cannot appear in persisted values;
- relation tests cover add/remove/update/reorder/cascade/large collection bounds;
- null/absent/redacted/unknown have distinct stable codec tags;
- schema-version reader tests render old and new field policies together;
- collection benchmarks show bounded SQL, memory, revision size and trace output;
- migration review names child/parent ownership and cascade semantics.

### First implementation slice

Implement declared scalar fields only, with explicit `null`/redacted state and
schema version. Introduce relation membership later for one small child resource;
defer generic deep-diff, map traversal and arbitrary JSON patch capture.

---

## A-15 — integrity, write authorization and tamper-evidence claims

**Decision.** PostgreSQL access, least-privilege roles, append-only constraints
and protected operations are the initial integrity boundary. Hash chaining,
signatures, WORM/archive locks or external ledgers are optional additions only
when a concrete threat/regulatory requirement and key/verification runbook exist.
No roadmap language may imply cryptographic non-repudiation merely because rows
are append-only by convention.

### Top-level declarative DX

```text
application_writer: INSERT revision/item/value; no UPDATE/DELETE
history_reader:    SELECT through security-definer query/view only
retention_worker:  execute approved lifecycle procedure only
schema_owner:      migration role, break-glass controlled
```

Privileges are provisioned separately from ordinary application configuration
and are verified in deployment tests against the actual database roles.

### Happy use cases

1. Application writer inserts committed revisions but cannot update/delete them.
   A correction is represented by a later declared correction action/revision.
2. History reader sees only authorized tenant/resource/field views and cannot
   query base value tables directly with arbitrary SQL credentials.
3. A retention worker invokes an approved stored procedure with approval token/
   job identity; it cannot delete arbitrary revision IDs outside candidate policy.
4. Migration creates indexes/partitions and grants while schema-owner credentials
   are absent from runtime pods/services.
5. An integrity checkpoint job seals a declared sequence/range using an optional
   implementation and stores verification metadata. Reader accurately shows the
   coverage and algorithm rather than claiming all rows are signed.
6. Break-glass access is time-bound, separately authorized and creates protected
   access evidence that an independent reviewer can inspect.

### Edge use cases

1. DBA directly edits a revision in an emergency. System cannot make impossible
   guarantees; procedure logs break-glass, runs consistency checks, records repair
   revision/provenance and reports affected trust boundary honestly.
2. Hash chain has partition boundary/reordering/retry issue. Design identifies
   canonical ordering, included fields, key rotation and verification gaps before
   enabling a claim of tamper evidence.
3. A malicious app role tries `UPDATE audit_item`. Database permission test must
   fail even if the application code normally never executes that statement.
4. A service account used for history read leaks. Field views/purpose checks and
   credential rotation limit blast radius; audit values are not assumed harmless.
5. Schema migration wants to alter old codec values in place. It is rejected;
   add reader compatibility/new columns or a governed corrective record instead.
6. Partition maintenance detaches a range. Archive/checkpoint/hold checks happen
   first; an automated DDL job cannot bypass retention evidence accidentally.
7. A transaction records audit before data and then fails. Database rollback must
   remove both; an independently committed integrity seal must not include it.

### Invariants and acceptance evidence

- runtime database roles are least privilege and permission-tested in CI/staging;
- audit append/update/delete grants are asserted per role, including direct SQL;
- correction/revocation procedure is documented and tested without physical rewrite;
- optional integrity features name exact covered fields/range/algorithm/key owner;
- schema owner, backup, archive and break-glass access are in threat model/runbook;
- integrity verifier detects controlled mutation, missing row and wrong order when
  those are in its promised coverage; gaps are visible as `unverified`;
- audit query API never depends on broad schema-owner connection for convenience.

### First implementation slice

Create append-only grants/constraints and a tested correction action. Publish a
truthful integrity statement: "protected append-only application roles", not
"tamper proof". Evaluate seals only with a real compliance threat model.

---

## A-16 — schema migration, codec versioning and compatible release flow

**Decision.** Audit rows are long-lived evidence. A new field, serializer or
viewer rule follows expand → dual-read/optional-dual-write → backfill only when
safe → cutover → retire, with a rollback story that never makes old evidence
unreadable. Migration/release status is a declared compatibility contract.

### Top-level declarative DX

```text
audit schema: revision v3 + item v4 + amount codec v2
writer release N: reads v1..v4; writes item v4 / amount v2
viewer release N: renders v1..v4
rollback target: reads v1..v3; deployment guard blocks incompatible v4 writer
```

Release notes state the actual minimum reader/writer versions and migration phase,
not only an opaque database migration number.

### Happy use cases

1. Amount codec moves from decimal string to `{currency, minor_units}`. New reader
   understands both; writer emits v2 after all deployed readers are compatible.
2. A new optional field is added. Old rows render `not captured under v3`, which
   differs from a captured null and from redaction.
3. Index is created concurrently/online according to PostgreSQL plan. Deployment
   observes progress and retains service rollback capability while build runs.
4. A projection/audit integration introduces commit reference. It first accepts
   null, then writes reference, later enforces it only after coverage proof.
5. Tenant database cohorts migrate gradually. App resolver routes only a release
   compatible with a tenant schema/version, and surfaced cohort lag is operable.
6. A failed backfill is restarted idempotently with range checkpoints; it does
   not alter original revision timestamps/action identities or create duplicate rows.

### Edge use cases

1. Rollback code cannot decode a new writer's codec. Deploy guard prevents that
   version combination; emergency path uses forward fix/read-only mode, not
   destructive down-migration of history.
2. Backfill accidentally uses current redaction policy to rewrite historical rows.
   It is forbidden unless legal governance explicitly calls for a redaction
   transformation with provenance; codec migration alone preserves original state.
3. New enum action is unknown to older viewer. It renders safe `unknown action
   (stable key)` with no authorization change; never treats unknown as allow.
4. A nullable column becomes non-null while archived partitions lag. Readiness
   check includes hot/cold/tenant cohort state before constraint is asserted.
5. Migration lock blocks high-volume audit writes. Plan has lock timeout, online
   alternative, maintenance window and abort criteria; no silent dropped audits.
6. A tenant database failed migration after app rollout. Resolver fences it from
   incompatible writes and emits operational state without tenant ID telemetry.
7. Old export contains legacy codec. Export manifest identifies schema versions
   and reader tools retain compatibility for declared retention horizon.

### Invariants and acceptance evidence

- every persisted record/value has version and tested reader compatibility range;
- migration test fixture includes oldest retained, current and future/unknown data;
- rollback drill proves old application is blocked or safely read-only before
  incompatible audit writer exists;
- release checklist includes archive partitions and db-per-tenant cohort evidence;
- backfill is resumable/idempotent/bounded and produces protected operations record;
- no schema migration deletes/reinterprets audit evidence without an approved
  retention/redaction/correction decision;
- deployment dashboard exposes version distribution, migration phase and failures.

### First implementation slice

Put `schema_version` and `value_codec_version` in first tables/codecs. Add a
golden-reader corpus to CI. Require an expand/cutover/rollback paragraph in every
audit schema PR before introducing online migrations.

---

## A-17 — attempt evidence, authorization decisions and incident response

**Decision.** Committed action revisions are not security-attempt logs. Where the
product needs evidence of denied, suspicious or failed operations, it defines a
separate attempt-audit policy with purpose, privacy fields, rate limits, retention
and failure isolation. It never changes a failed request into a fake revision.

### Top-level declarative DX

```go
attempts.Record(ctx, audit.Attempt{
    Kind:   audit.AttemptExportDenied,
    Actor:  verifiedActor,
    Scope:  verifiedScope,
    Reason: audit.ReasonPolicyDenied,
})
```

The call carries a bounded attempt kind/reason and trusted context. It excludes
raw request body, credential, query filter and full policy-expression trace.

### Happy use cases

1. A user without audit export right requests export. Service denies, creates a
   rate-limited attempt record and returns ordinary safe error; no success revision.
2. An internal service retries an invalid stale command. Metrics count conflict;
   attempt policy may sample/bound repeated evidence by run identity rather than
   writing unbounded rows.
3. Incident responder obtains an access grant and views attempt history under a
   different purpose/role from normal business audit history.
4. A policy decision includes stable allow/deny reason code. Audit reader shows
   localized explanation later, but stores no implementation details/ACL graph.
5. A credential compromise investigation correlates service actor/class, source
   category and time window without storing bearer tokens or raw headers.
6. Attempt storage outage is handled according to declared security posture: fail
   closed for critical operation or degrade with alert for low-risk operation,
   never silently pretending evidence exists.

### Edge use cases

1. Attacker sends unique payload each time to explode attempt-store cardinality.
   Schema has fixed kinds/reasons, quota/dedupe and no raw payload fields.
2. Authorization evaluator itself crashes. Result is `indeterminate`, not `deny`
   or `allow` guessed after the fact; operational alert/error class are distinct.
3. Login/service claims are later found invalid. Attempt provenance preserves
   verifier result/source category without retroactively asserting a trusted actor.
4. A denied update mutates nothing, but audit decorator captured before state.
   It discards it; attempt record has no protected value snapshots by default.
5. Incident rule demands IP/User-Agent. This is an explicit approved collection
   policy with minimization/retention/access controls, not generic metadata map.
6. A denial has different semantics per tenant plan. Reason enum remains stable;
   policy/version ref allows authorized explanation without inserting tenant into
   shared telemetry dimensions.

### Invariants and acceptance evidence

- success revision and failed/denied attempt schemas/types/retention differ;
- attempt policy declares exact availability consequence for its own storage fault;
- adversarial traffic test proves bounded writes/size/cardinality and safe logs;
- attempt viewer authorization is separate from normal resource/audit history;
- policy reason is stable, localized only at render, and contains no raw rule text;
- denied/malformed/indeterminate cases are exercised with zero successful items.

### First implementation slice

Do not ship attempt evidence automatically. Start only if a named threat/control
requires it, with one `export_denied` kind, fixed metadata and load-shedding test.

---

## A-18 — conformance suite, failure injection and adoption gate

**Decision.** Audit correctness is verified as a reusable conformance suite for
each supported repository/transaction/topology adapter. Unit tests of a diff
function are insufficient: the suite must exercise real PostgreSQL commit,
rollback, race, privilege and reader paths.

### Top-level declarative DX

```text
audit conformance --adapter sqlpg --topology row
audit conformance --adapter sqlpg --topology database
audit conformance --adapter orm-x --strict-coverage
```

An adapter is not advertised as audit-compatible until it publishes results for
the required profile and its documented intentional exclusions.

### Happy use cases

1. Create/update/delete/restore each produce declared revision/item/value rows
   after commit and no rows after deliberate rollback.
2. Two repository writes in one transaction yield one revision with two ordered
   items according to specified grouping; standalone writes yield separate revisions.
3. History query returns only the verified tenant/resource/field set for each role.
4. Value codecs round-trip golden corpus for nullable, redacted, enum, money and
   historical-version examples.
5. Storage evidence reference test verifies promote, version lookup and denied
   download across filesystem and S3-compatible adapter.
6. Telemetry test collector proves expected bounded spans/metrics and absence of
   disallowed subject/value/tenant attributes.

### Edge use cases

1. Kill process before commit, after data statement, after audit statement and
   after commit acknowledgement; inspect durable state and retry outcome.
2. Force deadlock/serialization failure/unique conflict. Verify retry behavior
   does not create duplicate committed revisions or lose winning evidence.
3. Revoke app role's audit insert/update/select grant independently; verify startup
   readiness and runtime errors follow honest policy.
4. Change tenant resolver mapping during a transaction or background job; verify
   fencing/misroute detection and no foreign revision appears.
5. Inject archive/export failure and partial object. Verify no signed final URL,
   retry idempotency and protected failure visibility.
6. Load a future codec/action version and corrupt test row. Reader reports bounded
   unavailable/unknown state without panicking, expanding payload or authorizing
   a default interpretation.
7. Run a bulk operation over adversarial selection, massive collection and
   no-op changes; verify memory/query/revision size and exact outcome semantics.

### Invariants and acceptance evidence

- suite runs against a real PostgreSQL version/configuration documented by adapter;
- every test identifies topological profile, transaction boundary and expected
  committed revision/item/value/access state;
- fault matrix includes rollback, retry, crash, privilege denial, migration lag,
  archive fault, RLS/tenant leak and telemetry privacy regression;
- performance tests define ceilings for mutation latency, row count, diff bytes,
  history page, export job and lifecycle batch with representative fixtures;
- golden fixtures include sensitive fields and assert omission/redaction rather
  than relying on test data that contains no secrets;
- release gate requires current and previous reader/writer compatibility suite;
- adapter README names explicit non-goals such as unsupported raw SQL or bulk paths.

### First implementation slice

Build the smallest PostgreSQL transaction suite before public decorator API. Make
commit/rollback/no-leak/redaction tests mandatory for every later integration.

---

## A-19 — PostgreSQL physical design, partitioning and query plans

**Decision.** The first PostgreSQL implementation uses explicit normalized
revision, item and declared-value tables with immutable identifiers, commit-time
ordering and indexes designed from authorized reader queries. JSON may be used
inside a versioned canonical value only when its schema/size/query role is
declared; it is not a universal escape hatch for generic model dumps.

### Top-level declarative DX

```text
audit_revision(id, committed_at, actor_ref, actor_kind, tenant_ref,
               source, reason_code, schema_version, ...)
audit_item(id, revision_id, tenant_ref, resource, subject_ref, action,
           result, field_schema_version, ...)
audit_value(item_id, field_key, before_state, after_state, codec_version, ...)
```

The actual names/types are a schema ADR, but the adapter documents primary keys,
foreign keys, uniqueness, partition key, ordering semantics, indexes and all
reader queries before performance claims are made.

### Happy use cases

1. A single-subject history query uses `(tenant_ref, resource, subject_ref,
   committed_at, item_id)` index and cursor predicate; operator can inspect the
   query plan without a table scan.
2. An actor/time investigation uses a separately approved index/reader view and
   strict time/purpose limit rather than making every raw actor field a global
   searchable index.
3. Monthly partitions contain revision/item/value records under a documented
   co-location rule. Retention can detach an eligible partition only after holds,
   archive/readback and relation integrity checks.
4. A high-write workload batches value inserts within the mutation transaction
   and remains under declared per-operation item/value/byte limits.
5. Cursor ordering uses committed ordering plus tie-breaker; two records with
   same timestamp appear deterministically and no page repeats/skips on normal
   new writes.
6. Read replica is allowed for historical search only if lag/result-staleness is
   explicit; current authorization/policy state required for a decision remains
   validated against the authoritative source.

### Edge use cases

1. Partition key based only on application clock differs from database commit
   time around month boundary. Design specifies the database-derived/time-zone
   source and tests the boundary instead of relying on host local time.
2. A foreign key from item to revision crosses partitions. Detach/archival plan
   demonstrates the allowed PostgreSQL mechanism and does not discover invalid
   global constraints during a production retention run.
3. Long tenant subject reference causes index bloat. Identity codec has bounded
   representation/length or protected surrogate mapping; it never truncates into
   collision without detection.
4. A user's query filters arbitrary before/after values. Default schema denies
   it; adding an index/search capability requires data classification and privacy
   review because it creates a new disclosure surface.
5. An audit transaction holds locks while export page scans. Reader queries use
   appropriate transaction/isolation/timeout policy so report traffic cannot
   accidentally become a write availability attack.
6. Autovacuum/partition maintenance lags due to append rate. Runbook defines
   monitoring, tuning/partition size and safe intervention without deleting data.
7. A cursor's sort key is redacted/expired in a newer reader. Cursor uses internal
   stable ordering keys; viewer visibility does not change page correctness.
8. Database restore changes sequence cache/order gaps. Revision identifier is not
   presented as proof of contiguous business history; ranges/checkpoints state
   their scope and recovery assumptions.

### Physical-model decisions to record before migration

| Question | Required decision/evidence |
|---|---|
| revision identity | UUID/ULID/sequence choice, generation authority, collision/recovery behavior |
| ordering | commit order, request order or logical order; tie breaker and reader cursor contract |
| time | database/application source, precision/time-zone, skew behavior and retention use |
| tenant scope | protected key representation, RLS/composite FK/index plan and topology difference |
| subject reference | opaque identity codec/limit, access path and no raw dynamic indexing policy |
| values | typed columns/codec envelope, maximum bytes, compression/encryption decision and reader version |
| partitioning | key/granularity/co-location, creation automation, detach/archive/restore rehearsal |
| indexes | each index maps to an authorized query; write amplification/retention cost accepted |
| access | base-table grants, views/functions/RLS roles and break-glass verification |
| replica | allowed query classes, lag signaling, privilege/schema synchronization |

### Invariants and acceptance evidence

- schema has primary/foreign/unique/check constraints for revision/item/value linkage;
- required history queries have captured `EXPLAIN` plans and fixture cardinalities;
- pagination test runs inserts between pages and proves no missing/duplicate items
  under documented isolation semantics;
- partition creation, attach/detach, archived read and backup restore are rehearsed;
- table/index growth and autovacuum/maintenance indicators have dashboards/limits;
- every queryable field has purpose/role/privacy approval, and unapproved value
  predicates are impossible through the public history grammar;
- DB schema comments/migrations identify version/retention/owner rather than only
  application code knowing those rules.

### First implementation slice

Use unpartitioned tables only for an intentionally bounded initial pilot, but
write schema so IDs, ordering and access views survive future partitioning. Add
one subject-history index and capture its plan under production-like rows before
advertising a general audit search feature.

---

## A-20 — privacy classification, redaction reviews and reader roles

**Decision.** Audit policy reviews classify every captured field and metadata
element separately from the source model. The audit database is a sensitive
system of record: encryption, restricted roles and careful viewer projections
support minimization; they do not justify capturing all data by default.

### Top-level declarative DX

```go
audit.Field("email", audit.Omit())
audit.Field("amount", audit.Capture(audit.Money()))
audit.Field("tax_id", audit.Hash(audit.HMACKeyRef("audit-tax-id-v1")))
audit.Field("risk_decision", audit.Capture(audit.Enum()))
```

The code names a reviewed classification action. There is no convenience default
that captures a newly added model field as JSON because it "might be useful".

### Happy use cases

1. Payment token/password/reset secret is omitted and a golden fixture confirms
   it cannot appear in row, error, trace, test snapshot or export.
2. Tax identifier comparison is a legitimate audit need. Policy stores a keyed
   protected representation with key reference/rotation plan, while viewer policy
   does not expose raw identifier.
3. Monetary amount/currency is needed for approval evidence. Typed codec stores
   canonical values and a role can view it only within approved purpose.
4. Risk decision is shown as stable enum and localized explanation key, while
   proprietary score/feature vector remains outside the audit diff.
5. Support role can see action/time/reason but not values; compliance role sees
   additional declared fields; investigator role requires case purpose/grant.
6. A field's classification changes. New writes follow new policy; historic data
   follows retention/redaction migration decision with an explicit provenance row.

### Edge use cases

1. Team classifies a free-text note as safe, then product begins putting medical
   or credential data into it. Generic free text remains omitted/restricted until
   a new field-specific policy/security review exists.
2. Hashing a low-entropy value permits guessing. Policy treats keyed hash/token
   as potentially sensitive and does not advertise it as anonymization.
3. Masked value `****1234` still identifies sensitive account context. Masking
   choice is reviewed per field/reader/export rather than assumed universally safe.
4. Field serializer throws while processing an untrusted value. Transaction fails
   or follows declared safe error policy; it never falls back to serializing raw
   model/reflection value to preserve a record.
5. Redaction rule is introduced after a breach. Emergency transformation has
   legal approval, range/accounting, backup/archive disposition and an auditable
   method; normal code deployment cannot clandestinely erase historic evidence.
6. A user asks for data access. Audit export may reveal data about other actors;
   DSAR/subject-access workflow has its own authorization/redaction rules.
7. Test fixtures with real production values are copied to compatibility corpus.
   Test policy requires synthetic/sanitized data; golden tests prove shape, not
   expose customer contents in source control/CI logs.

### Reader-role matrix

| Role/purpose | May see | Must not get by default |
|---|---|---|
| resource user | no audit history unless product explicitly grants it | actor identity, internal reason, protected values |
| support | action/time/safe subject label under case scope | secret/financial/restricted diff and broad search |
| compliance | declared regulated values under purpose | arbitrary raw values/other tenant data |
| investigator | bounded cross-resource/cross-tenant evidence with grant | permanent bulk export or unbounded filter |
| operator | pipeline health and protected link | audit contents in logs/traces/dashboards |
| retention worker | lifecycle metadata/ranges | value plaintext or general history query |
| break-glass reviewer | controlled exceptional access evidence | automatic indefinite superuser access |

### Invariants and acceptance evidence

- policy manifest lists classification, purpose, viewer roles, retention and owner
  for every field/metadata/attachment type;
- authorization tests run every reader role against every declared field state;
- privacy regression scan uses canary secret/PII values across SQL, exports, logs,
  traces, error text, metrics and test artifacts;
- serializer failure/property tests prove no generic fallback/partial raw snapshot;
- hash/token/mask use records algorithm/key/rotation/disclosure limitations;
- data-access/erasure/redaction/hold conflicts have a reviewed operational path;
- viewer schemas do not expose fields merely because database row contains them.

### First implementation slice

Adopt deny-by-default fields, two viewer roles and canary-value tests. Avoid
hashing/masking until an actual query/evidence requirement justifies them and
their privacy properties are understood.

---

## A-21 — failure-mode analysis and operational response

**Decision.** Each audit pipeline failure is classified by whether the primary
mutation may commit, whether evidence is durable, who is alerted, what is safe
to retry and how operator truthfully communicates an evidence gap. Availability
policy must be decided per action/risk class, not discovered in catch blocks.

### Failure-mode matrix

| Failure | Permitted primary outcome | Required evidence/response |
|---|---|---|
| audit insert constraint/codec fault in same tx | rollback mutation | safe error class, alert, no success revision |
| PostgreSQL disconnect before commit | unknown outcome; reconcile | idempotency/commit probe, no fabricated success |
| PostgreSQL disconnect after commit ack lost | possibly committed | idempotency key/revision lookup, retry-safe response |
| audit DB separate/outbox unavailable | only declared async mode may commit | durable pending state, backlog alarm, no atomicity claim |
| history read RLS denial | deny read | protected access outcome, no SQL/value leak |
| export object staging failure | fail export | quarantine/cleanup/retry, no final capability |
| retention archive failure | retain primary | alert/backoff, no destructive deletion |
| legal hold lookup unavailable | fail closed for purge | retained candidate/incident, no broad bypass |
| codec unknown to viewer | return unavailable typed state | compatibility alert, no guessed rendering |
| telemetry exporter failure | audit semantics unchanged | bounded diagnostic metric/log policy |
| tenant resolver uncertain | no tenant mutation/history query | fail closed, operational resolution path |
| clock/ordering anomaly | do not make false chronology claim | cursor/checkpoint/incident procedure |

### Happy use cases

1. Same-transaction audit insert fails due to policy codec bug. Whole mutation
   rolls back, caller receives safe retryable/nonretryable class and on-call sees
   an aggregate failure signal with no sensitive payload.
2. Network drops after client sends commit. Service uses command idempotency/revision
   lookup to tell caller the actual outcome rather than retrying a potentially
   duplicate business action.
3. A low-risk analytics audit export is async and backlog grows. Dashboard alarms,
   worker catches up from durable outbox and final records are marked delivered.
4. Archive object verification fails. Lifecycle marks range blocked and continues
   no destructive step; operator can retry after storage fault is corrected.
5. Reader sees unknown future schema. It returns a stable `unavailable_version`
   state and captures non-sensitive version count for release owner.
6. An alert identifies a missing expected revision ratio. Incident playbook freezes
   affected write path where policy requires, scopes impact and reconciles from
   immutable transaction/outbox evidence without pretending full reconstruction.

### Edge use cases

1. Error handler calls audit again to record failed audit, causing recursive fault.
   Error/attempt pathway has bounded recursion guard and alternate safe signal.
2. A retry moves to another process/region. Idempotency/revision correlation
   survives process memory and writer identity does not create a second action.
3. Database failover returns a stale replica read for reconciliation. Procedure
   requires a correct consistency source and reports `unknown` until confirmed.
4. Retention job resumes after code change. Durable job version/policy/range
   checkpoints allow safe compatibility decision, not blind continuation.
5. Export worker receives duplicate job message. Stable job/idempotency/object
   staging rules prevent multiple downloadable artifacts/audit claims.
6. Alert volume is huge during outage. Aggregation/rate limits protect operators
   while protected forensic evidence remains queryable after recovery.
7. Partial tenant cohort outage causes reports with only some databases. Reader
   output has explicit per-tenant completeness state and cannot be exported as a
   full compliance attestation without acknowledgement.

### Invariants and acceptance evidence

- failure taxonomy is mapped to error contracts, retry policy and caller-visible outcome;
- chaos/fault tests reproduce every row in matrix against the deployed topology;
- no error path logs raw diff/actor/subject/tenant/filter/connection secret;
- unknown commit state is first-class and cannot be translated automatically to success;
- operator runbooks include detect, contain, reconcile, notify, recover and postmortem;
- SLOs distinguish mutation availability, audit durability, history freshness,
  export latency and retention progress; one green metric cannot hide a gap;
- all availability exceptions are signed off by risk owner and visible in policy manifest.

### First implementation slice

Write the same-transaction insert failure and unknown-after-commit scenarios first.
Make their outcomes visible in service API tests and a one-page operator runbook
before implementing optional async exports or retention automation.

---

## A-22 — policy manifest, review workflow and configuration compilation

**Decision.** Auditable-resource policies compile into a reviewable manifest that
is versioned with the application. Startup validates manifest/schema/adapter
compatibility; deployment exposes only safe policy version/coverage counts. A
runtime administrator cannot expand captured fields or retention by free-form
configuration without the same review/governance path as code.

### Top-level declarative DX

```text
audit manifest check ./audit-policies
audit manifest diff release-N-1 release-N
audit adapter verify --policy-manifest=build/audit-manifest.json
```

The commands show changed resource/action/field/retention/viewer contracts in
human-readable form. They do not print sensitive fixture values or database data.

### Happy use cases

1. A new `orders.cancel` action adds a policy owner, purpose, reason enum,
   allowed fields, retention class and test profile. Manifest diff makes each
   decision visible to reviewer before merge.
2. A field is removed from future capture. Compiler confirms reader compatibility
   and requires an explicit historic-data disposition rather than treating it as
   an ordinary refactor.
3. A team tries to mark a field `Capture` without serializer/classification. Build
   fails with a targeted policy error before application starts.
4. Production startup compares compiled manifest version with installed audit
   schema/adapter capabilities and fails readiness when incompatible.
5. Security reviewer reads one generated inventory listing capture purpose/owner
   and test status across resources without searching arbitrary source code.
6. A satellite upgrade introduces a deprecated codec. Manifest identifies every
   policy still using it and blocks removal until migration proof exists.

### Edge use cases

1. A package uses reflection/plugin registration that compiler cannot see. Strict
   mode refuses unregistered audit policy or produces a deploy-blocking coverage gap.
2. Different services publish same resource name with different field schema.
   Namespace/ownership registry detects collision; they cannot write ambiguous
   shared history merely because names are strings.
3. Generated manifest contains a raw field name considered sensitive. Manifest
   itself is classified/reviewed and excludes values; names may still be protected
   artifact access depending on threat model.
4. Runtime feature flag changes capture behavior. Flag is a versioned approved
   policy input with rollout/audit record, not a hidden per-request toggle.
5. Emergency break-glass policy needs a temporary extra action. It has expiry,
   approver, deployment record and automatic expiry verification, not a permanent
   unreviewed widening.
6. Build version is rolled back while DB contains newer manifest rows. Compatibility
   check blocks unsafe writer or runs read-only until forward-compatible release.

### Invariants and acceptance evidence

- all policy fields/actions/codecs/retention/viewers have stable identifiers and owners;
- manifest diff is reviewed in schema/privacy/security release checklists;
- compiler rejects default capture, unknown enum, unbounded serializer and omitted
  retention/purpose/role declarations;
- production readiness validates manifest/schema/adapter/version but emits no values;
- policy coverage inventory is compared against supported repository write inventory;
- emergency overrides have scope/expiry/approval and post-expiry verification test.

### First implementation slice

Generate a JSON/Markdown manifest from static policies and enforce resource/action/
field serializer completeness in CI. Defer a policy administration UI until the
same manifest, authorization and release controls exist.

---

## A-23 — corrections, reversals, disputes and historical truth

**Decision.** An audit row is never updated to make history prettier. Business
corrections, data repair, reversal, invalidation and disputed evidence are new
declared actions/revisions that link to prior evidence and state who authorized
the change. Reader presentation can show a corrected view while preserving the
original fact and scope of its correction.

### Top-level declarative DX

```go
err := corrections.Reverse(ctx, audit.Reversal{
    Target: audit.RevisionRef(revisionID),
    Action: audit.Action("orders.cancel_correction"),
    Reason: audit.ReasonOperatorRepair,
})
```

The API requires a target, stable action and approved reason. It cannot issue a
generic `UPDATE audit_item SET after = ...` operation.

### Happy use cases

1. Operator entered wrong order status and performs an authorized correction.
   New revision records repair actor/reason, corrected domain action and link to
   original revision; reader shows both in chronological relationship.
2. A payment is reversed. Domain semantics append reversal event/transaction;
   audit records a separate `reverse` action rather than overwriting payment diff.
3. A value was captured contrary to newly discovered privacy rule. Governed
   redaction transformation records authority/range/reason and viewer tells
   auditor original value was removed under policy—not that it was never captured.
4. Customer disputes an action. Case system may link dispute reference to a
   protected investigation annotation without changing the original audit actor
   or result into a claimed falsehood.
5. Database repair fixes a projection/state mismatch. System actor and repair run
   reference distinguish it from end-user command while links support traceability.
6. A correction itself is later superseded. Reader follows explicit chain with
   cycle/length protection and clear current/legacy interpretation.

### Edge use cases

1. Correction points to a revision caller cannot read. Authorization distinguishes
   permission to repair from broad historical disclosure; error leaks no details.
2. A correction uses same idempotency key after timeout. Durable action identity
   prevents two reversal revisions and reports actual committed status.
3. Operator tries to correct across tenant boundary. Normal action refuses; named
   migration/investigation authority, both tenants and legal purpose are required.
4. Original evidence is archived or redacted. Link resolves to truthful archived/
   redacted/unavailable state; it is not silently removed from correction chain.
5. Repair wants to alter audit metadata such as actor/source. It writes a dispute/
   provenance note under strict policy; original actor evidence remains immutable.
6. Circular correction references are produced by bug/import. Constraint/validator
   blocks or marks corrupt state for repair without unbounded reader recursion.

### Invariants and acceptance evidence

- application role has no update/delete route for historical evidence values;
- every correction/reversal names target/reason/actor/authority and its own action;
- reader tests show original plus correction order and privacy/archival states;
- idempotency/concurrency tests prove one corrective result per requested action;
- governed redaction is visibly different from normal data correction;
- repair/import workflows have dedicated service actor/provenance and cannot masquerade
  as original human request;
- consistency job detects dangling/cyclic/mismatched correction references.

### First implementation slice

Provide no generic correction API. Implement one domain-level reversal action and
its audit linkage only after core append-only constraints/revision reader tests
are passing. Document direct DBA repair as break-glass, never as a normal feature.

---

## A-24 — revision grouping, ordering and time semantics

**Decision.** Revision ID is a grouping identifier, not a universal timeline.
The roadmap records exactly what `committed_at`, item ordering and transaction
grouping mean. Cross-database, async and imported actions retain their own local
ordering/provenance; viewer must not imply a global serial history it cannot prove.

### Top-level declarative DX

```text
revision: one committed PostgreSQL transaction in one audit database
item order: declared service order, tie-broken by immutable item sequence
time: database commit-time approximation with documented precision
cross-store link: causal reference only; no total order claim
```

The documentation sits beside schema/reader code and is used by export/report
templates so user-facing language does not accidentally overstate certainty.

### Happy use cases

1. Two order changes occur in one transaction and appear under one revision with
   stable item sequence. Viewer says "part of one committed operation."
2. Two concurrent transactions commit. History cursor orders by chosen commit/order
   key and tie-breaker; it does not infer which user started first from timestamps.
3. A request carries client timestamp. Audit may show it as separately classified
   user assertion if approved, but canonical audit time remains trusted database/
   service commit information.
4. An async worker processes event later. Its revision links cause/source and
   shows own execution time, avoiding an apparent same-transaction action.
5. An import loads historical evidence. Original source time and import commit time
   are distinct versioned fields with provenance; reporting chooses explicitly.
6. Daylight-saving/time-zone presentation changes. Stored canonical time stays
   instant-based; localized viewer conversion does not mutate/order records.

### Edge use cases

1. Transaction assigns revision ID then rolls back. IDs may have gaps; readers/
   integrity reports never interpret gap as missing audit event without evidence.
2. Database commit timestamp extension is unavailable/inexact. Adapter records
   its fallback semantics and refuses claims needing stronger ordering guarantee.
3. System clock jumps backwards. Application time may be displayed with marker,
   but primary cursor uses database/order sequence unaffected by host clock drift.
4. Multi-row statement execution order is database-dependent. Policy does not
   expose artificial per-item chronology unless deterministic order is designed.
5. Nested savepoint rolls back inner mutation. Parent revision item list excludes
   it; savepoint behavior is tested instead of assuming outer transaction success.
6. A user requests an audit report sorted by `actor`/localized action text. Query
   grammar permits it only with deterministic safe secondary sort/tie breaker and
   proper index/cost guard; locale does not change authoritative chronology.

### Invariants and acceptance evidence

- schema/reader/export docs declare one exact ordering/time model;
- transaction/concurrency/savepoint/import/async tests prove stated grouping;
- cursors use stable opaque ordering tuple and reject cross-query replay;
- display time/localization is clearly presentation, not a data mutation/order key;
- reports with partial/cross-store data label uncertainty/completeness correctly;
- no component treats sequence gaps, trace start or event IDs as automatic global order.

### First implementation slice

Define one Postgres transaction scope, opaque cursor tuple and timestamp source
in the first adapter ADR. Add concurrency and rollback-gap fixtures before complex
cross-system correlation or compliance chronological export.

---

## A-25 — external audit export, delivery contracts and downstream consumers

**Decision.** If audit data must reach SIEM, warehouse, regulator or another
system, it flows through a dedicated export policy and durable outbox/delivery
protocol. Delivery is at-least-once unless an end-to-end proof says otherwise;
consumer payload is minimized, versioned and authorized independently from local
history read. It never turns every mutation into an unbounded webhook.

### Top-level declarative DX

```go
audit.ExportPolicy{
    Name:       "regulated-audit-feed-v1",
    Consumer:   "compliance-archive",
    Fields:     audit.ExportFields("action", "time", "subject_token", "amount"),
    Delivery:   audit.TransactionalOutbox,
    Retention:  "audit-export-regulated-v1",
}
```

The configured field list is smaller than local audit schema by default and is
reviewed with the consumer contract/version/retention/incident contact.

### Happy use cases

1. Committed local revision writes an outbox record in same transaction. Relay
   delivers versioned minimal envelope; consumer deduplicates by export record ID.
2. Consumer is down. Backlog grows under bounded retention/alert; local audit
   remains complete and relay retries without re-running source business command.
3. New export field is introduced through schema version/consumer compatibility
   rehearsal; old consumer ignores/handles it according to explicit contract.
4. A regulator requires daily file. Export job stages encrypted output, verifies
   count/checkpoint, publishes through authorized channel and records delivery/
   receipt evidence without putting contents in logs.
5. Consumer asks for resend of range. Operator uses protected replay capability
   with range/purpose/approver, and receiver tolerates duplicates/reordering.
6. SIEM gets operational attempt events but not protected before/after diffs;
   incident investigator follows local protected process for additional details.

### Edge use cases

1. Relay publishes then crashes before acknowledgement. It duplicates delivery;
   idempotent consumer must not interpret duplicate as two business actions.
2. Consumer schema rejects new field. Outbox marks retry/quarantine with safe
   classification; local writer does not silently discard or block unrelated
   transactional audit unless policy declares a hard dependency.
3. Destination credential/link leaks. Revocation/rotation/replay policy handles
   it; export data is not made safe merely because local tables were protected.
4. Cross-tenant delivery target receives data. Export policy must name tenant
   boundaries/residency and prohibit a generic global feed by accidental fan-out.
5. Consumer requests a raw audit blob for convenience. Contract refuses until
   field classification, minimization, legal basis and recipient controls exist.
6. Retention deletes local row before consumer got it. Outbox retention/ack/replay
   horizon is designed separately; alert/hold stops destructive loss of mandated feed.
7. Message ordering differs from revision ordering under partitions. Consumer gets
   declared sequence/cursor semantics and must not infer stronger causal ordering.

### Invariants and acceptance evidence

- outbox row and local revision commit atomically in declared transactional mode;
- export envelope carries version/idempotency/minimal fields/provenance, no raw diff;
- consumer contract documents dedupe, retry, ordering, retention, authorization,
  endpoint/certificate/credential rotation and incident contacts;
- integration tests inject publish/ack/schema/credential/partial-file faults;
- replay requires protected scope/range/purpose and is itself audit-recorded;
- exports are absent from default OTel/logs apart from bounded delivery health;
- removal/change of export field is migration-reviewed independently of local reader.

### First implementation slice

Do not export audit externally at first. When required, implement one local
transactional outbox envelope and an idempotent test consumer before network/
SIEM integrations. Treat any new recipient as a privacy/security architecture change.

---

## Audit conformance catalogue

The following catalogue is the minimum evidence pack before claiming a resource
is covered by transactional, privacy-aware audit. Each scenario records adapter,
topology, policy schema and PostgreSQL version alongside its result. A green unit
test of one serializer does not satisfy a scenario that names a transaction or
reader boundary.

### AC-01 through AC-10 — ordinary committed operations

**AC-01: create.** Insert one declared resource in a tenant scope.

Assert one committed revision, one create item and only allowed after values.

Assert no omitted secret/default/future field is present in table, error or trace.

**AC-02: scalar update.** Change one declared scalar from value A to B.

Assert canonical before/after codec, actor/source/reason and expected action enum.

Assert viewer role sees exactly its allowed projection and localized label is derived.

**AC-03: no-op update.** Submit a valid update that does not change declared state.

Assert configured no-op outcome: no item or explicit no-change item, consistently.

Assert caller cannot infer undisclosed original fields from no-op response/history.

**AC-04: delete.** Perform declared soft/hard domain delete under normal policy.

Assert action/tombstone representation follows resource policy, not generic model dump.

Assert retention/reader behavior is distinguished from ordinary data disappearance.

**AC-05: restore.** Restore a previously deleted resource where policy permits it.

Assert restore action links resource identity without mutating original delete audit.

Assert tenant/actor/authorization follow current restore authority rather than old actor.

**AC-06: custom command.** Execute `Approve` domain operation with several fields.

Assert stable `approve` action rather than inferred `update` and declared diff only.

Assert event-source link, when configured, is bounded reference rather than payload.

**AC-07: relation member add.** Add one small approved child relation.

Assert parent/child policy's declared relation item shape and no full collection dump.

Assert child belongs to same verified tenant or operation is rejected.

**AC-08: relation member remove.** Remove relation without deleting child entity.

Assert membership action differs from child delete and history reader communicates it.

Assert ordering/identity codec is deterministic under retry.

**AC-09: database-derived field.** Update that changes an approved DB-derived value.

Assert final committed value is captured only when policy says to include it.

Assert adapter does not capture stale ORM object before trigger/default execution.

**AC-10: read only.** Load/preload/cache-refresh a resource without mutation.

Assert no revision/item/value row is created and no audit write metric claims change.

Assert authorized history read itself follows access-audit policy independently.

### AC-11 through AC-20 — transaction and retry correctness

**AC-11: explicit rollback.** Mutate two audited resources then roll back unit of work.

Assert neither data mutation nor revision/item/value is durable.

Assert no outbox/export success record or false telemetry commit event remains.

**AC-12: grouped commit.** Mutate two declared resources in one transaction.

Assert exactly one revision and exact item count/order per grouping contract.

Assert both items share only metadata permitted to share under policy.

**AC-13: independent commits.** Run two repository calls without a shared unit of work.

Assert each gets its own revision or strict policy rejects standalone writes.

Assert reader does not group them merely because actor/request/time match.

**AC-14: failed codec.** Serializer rejects an invalid value in same transaction.

Assert mutation rolls back under transactional policy and caller gets safe error class.

Assert raw model/value is absent from error, log, trace and attempt record.

**AC-15: deadlock/serialization retry.** Induce PostgreSQL retryable write failure.

Assert only winning commit has one revision and retries do not duplicate item/diff.

Assert conflict diagnostic is bounded and does not expose competing subject values.

**AC-16: client timeout after commit.** Drop response after database commit.

Assert idempotency/reconciliation finds existing command/revision without second action.

Assert history contains one truthful committed result irrespective of client retry.

**AC-17: process crash before commit.** Kill worker after prepared writes but before commit.

Assert database recovery leaves no success evidence and retry's outcome is clean.

Assert any allocated identifier gap is not reported as missing business history.

**AC-18: process crash after commit.** Kill worker after commit before response/metric.

Assert durable data/revision exist and telemetry absence does not question audit truth.

Assert retry uses stable key and returns/reconciles result without duplicating audit.

**AC-19: savepoint rollback.** Roll back inner savepoint, commit outer transaction.

Assert only surviving mutation has audit item; grouping/sequence contains no ghost item.

Assert adapter documents unsupported savepoints by refusal, if it cannot prove this.

**AC-20: transaction owner misuse.** Repository opens hidden transaction inside outer one.

Assert strict adapter rejects or demonstrates exact documented nesting semantics.

Assert no partial audit/data commit violates caller's advertised atomic boundary.

### AC-21 through AC-30 — scope, reader and privacy controls

**AC-21: missing tenant.** Call scoped mutation with no verified tenant context.

Assert zero tenant SQL/audit mutation and a fail-closed safe error.

Assert no default/global audit revision appears under a nil tenant identity.

**AC-22: cross-tenant guessed subject.** Search history using a foreign subject ID.

Assert no result versus forbidden result follows stated non-enumeration policy.

Assert query plan includes tenant scope and RLS/composite constraints where enabled.

**AC-23: database-per-tenant misroute.** Inject stale resolver mapping to wrong database.

Assert binding/epoch/database identity check aborts before foreign evidence is written.

Assert diagnostic exposes only bounded resolver failure class, not database identifier.

**AC-24: field role projection.** Open same revision as support and compliance roles.

Assert each field is visible/redacted/withheld exactly per viewer policy.

Assert cursor/result count does not reveal values in prohibited fields.

**AC-25: forbidden export.** Request export without the required purpose/capability.

Assert no job/object/presigned URL is created and optional attempt result is safe.

Assert source subject/filter/data never appears in default logs or telemetry.

**AC-26: cursor confusion.** Reuse a cursor with different tenant/purpose/resource.

Assert reader rejects it and does not run an unintended broad query.

Assert error does not reveal original query scope or matched count.

**AC-27: secret canary.** Mutate a model containing known fake secret/PII canary.

Assert canary absent from persisted audit, exports, errors, traces, metrics and logs.

Assert allowed transformed representation exists only if policy expressly declares it.

**AC-28: serializer panic.** Make approved serializer panic/fail on hostile input.

Assert failure follows transaction policy and no reflection/JSON fallback retains raw input.

Assert incident signal is bounded/versioned and operationally actionable.

**AC-29: locale fallback.** Render history with unsupported/attacker locale tag.

Assert stable action/reason remain correct, safe fallback renders and no SQL/template injection.

Assert persisted audit data is unchanged by render locale.

**AC-30: actor restriction.** Viewer may see action but not actor identity.

Assert protected actor is withheld/pseudonymized according to policy and no side channel
through CSV, sort, counts, correlation or error gives it away.

### AC-31 through AC-40 — lifecycle, exports and compatibility

**AC-31: archive then read.** Move eligible protected range to archive.

Assert archive read returns same logical IDs/schema with truthful retrieval status.

Assert hot rows are not deleted before integrity/readback verification succeeds.

**AC-32: legal hold race.** Place hold while retention candidate is being processed.

Assert final destructive action rechecks hold and retains evidence on uncertainty.

Assert job/access evidence records blocked state without values.

**AC-33: governed purge.** Execute approved dry-run then purge small eligible range.

Assert two-person/purpose/range/count/checksum controls and postcondition evidence.

Assert restored-backup reconciliation detects/reapplies required deletion disposition.

**AC-34: object evidence.** Link audited item to staged/promoted object version.

Assert typed logical reference/digest/version, denied download for unauthorized reader.

Assert failed promotion produces truthful pending/failure state, never dead URL.

**AC-35: external outbox.** Commit audit revision and export outbox in one transaction.

Assert relay duplicate delivery is tolerated and local audit is not duplicated.

Assert consumer receives minimized versioned envelope, not full local diff.

**AC-36: consumer outage.** Hold destination unavailable beyond retry window.

Assert backlog/alarm/replay procedure, and no loss of local evidence/outbox silently occurs.

Assert availability consequence matches documented export class.

**AC-37: codec expand.** Deploy reader accepting new amount/value codec first.

Assert old/new golden values render safely and writer cutover only after compatible state.

Assert rollback guard prevents old incompatible writer/reader combination.

**AC-38: unknown future value.** Insert fixture with future codec/action enum.

Assert reader returns `unknown/unavailable` typed state, no panic/default authorization.

Assert metric counts version only, not payload/tenant/subject.

**AC-39: redaction migration.** Apply approved redaction transformation to historical range.

Assert provenance/authority/range and reader mark distinguish transformed versus absent.

Assert archives/backups/object versions follow the independently approved disposition.

**AC-40: partition restore.** Restore archived/old partition to isolated verification DB.

Assert schema/codecs/roles/reader work and retention/hold state reconciles before serving.

Assert operator cannot accidentally attach old partition into live service without checks.

### AC-41 through AC-50 — topology, integrations and claims

**AC-41: row tenant RLS.** Attempt direct SQL/select/update with wrong tenant setting.

Assert database defense denies/filters as designed alongside application predicate tests.

Assert RLS configuration does not become excuse to remove application scope evidence.

**AC-42: database tenant lifecycle.** Provision a tenant database with audit schema.

Assert readiness/migration/policy version verifies before activation; deprovision honors hold.

Assert control plane does not leak connection/database name in audit/telemetry.

**AC-43: asynchronous job.** Deliver a tenant work item to background worker.

Assert worker reconstructs verified scope/service actor deliberately before audit write.

Assert raw queue payload cannot select tenant/database without authorization.

**AC-44: event-source command.** Append a PostgreSQL event-store command plus audit link.

Assert audit/event payloads stay separate, commit link is bounded and read gates hold.

Assert replay/import/projection actions use distinct system actor/action semantics.

**AC-45: OTel exporter fault.** Fail telemetry exporter and force unsampled trace.

Assert committed audit behavior is unchanged and forbidden attributes/canary absent.

Assert audit-to-trace lookup still requires separate protected authorization.

**AC-46: bulk exact selection.** Update a bounded selected victim set under race.

Assert audited subject set equals actually mutated set and partial/failure semantics exact.

Assert raw filters/IDs are not emitted as generic observability attributes.

**AC-47: large bulk summary.** Run approved massive action summary workflow.

Assert bounded memory/rows/diff bytes and explicit summary—not fake individual evidence.

Assert selection/run/retry/count/status are sufficient for its declared purpose.

**AC-48: break glass.** Use controlled elevated role to view/repair protected history.

Assert expiry/approver/purpose/access evidence and post-use review work.

Assert routine service operation cannot obtain the same base-table capabilities.

**AC-49: integrity claim.** If optional seal/checkpoint is enabled, mutate fixture range.

Assert verifier detects promised tampering/missing/order cases and reports coverage gaps.

Assert UI/docs do not claim global tamper proof outside proven sealed range.

**AC-50: audit coverage review.** Compare service mutation inventory to policy manifest.

Assert every opted-in write shape is covered or explicit exemption is approved/visible.

Assert new raw/bulk/ORM pathway fails release gate until its conformance profile exists.

---

## Delivery sequence and stop conditions

### Phase 0 — decide purpose before tables

Name the first audited resource, action, actor type, tenant topology, fields,
retention class, viewer roles and availability consequence. Stop if business team
cannot say why each field/action is retained or who is authorized to see it.

### Phase 1 — transactional narrow slice

Implement Postgres revision/item/value schema, least-privilege roles, one explicit
repository command and commit/rollback/retry conformance tests. Stop if any path
can commit a claimed success while audit insert failed or if secret canary leaks.

### Phase 2 — protected historical viewer

Add one-subject/single-tenant/purpose-bound cursor reader with role projections,
i18n rendering at edge and access evidence. Stop if history becomes another CRUD
table, has raw free-text filtering or can be reached through OTel correlation.

### Phase 3 — lifecycle and topology

Add manifest validation, row/db-per-tenant adapter profile, retention inventory,
hold-aware dry run and archive rehearsal. Stop if tenant/data lifecycle conflict
is unresolved or partition/archive procedure has not been restored/tested.

### Phase 4 — advanced integrations

Only after prior gates: event-store commit refs, storage evidence references,
external export outbox, cross-tenant investigation grants, optional integrity seals
and governed corrections. Stop each one if it broadens data capture/read access
without independently tested purpose/authorization/retention contract.

## Final review worksheet

Before calling an audit integration production-ready, answer all of these with
links to executable tests/ADRs/runbooks, not confidence alone:

1. Which exact mutation commands are covered, and which are deliberately excluded?
2. What does one revision mean, who owns the transaction and how are retries handled?
3. Which fields are captured, transformed or omitted, and why is each necessary?
4. Can a newly added model field accidentally appear in audit history or exports?
5. Which database roles can append, read, alter, purge or break glass—and are grants tested?
6. How does row and database-per-tenant topology establish and verify scope?
7. What user/service/impersonation actor evidence is trustworthy, bounded and private?
8. How do authorized history search, cursor, export and cross-tenant workflows fail closed?
9. What is the honest outcome for codec fault, DB failure, unknown commit and exporter outage?
10. How does event sourcing link without becoming copied payload or false atomicity?
11. What reaches OTel/logs/metrics, and which collector test proves sensitive absence?
12. What is archive/purge/legal-hold/backup/key-destruction behavior and who approves it?
13. How will old readers/writers/codecs/partitions/tenant cohorts coexist during release?
14. What exact claims about append-only integrity or tamper evidence are technically proven?
15. Which scenario IDs in the conformance catalogue cover this resource and adapter?

## Definition of done

The audit satellite is ready for a resource only when its declared policy manifest,
transaction adapter profile, PostgreSQL roles/schema, golden codec corpus, privacy
canary tests, tenant query matrix, authorized reader, operational dashboards,
failure runbook, retention inventory and release compatibility evidence are all
present and reviewed. Anything less may be a useful log or prototype, but it is
not yet an Envers-like durable audit capability with trustworthy semantics.

---

## Operator handoff, dashboards and incident rehearsal

The audit subsystem needs named owners for application policy, database schema,
privacy/retention, history-reader authorization, tenancy control plane, storage
evidence and external export. A generic "platform owns it" handoff is inadequate
because a healthy writer metric can coexist with an unreadable archive, an unsafe
viewer or a missing tenant cohort.

### Required dashboards

| Dashboard | Bounded signals | Operator question |
|---|---|---|
| write path | attempts, committed/rolled-back/unknown outcomes, latency, DB class | Are declared writes still producing evidence atomically? |
| reader path | authorized/denied/error/page latency/version-unavailable counts | Can approved readers access history without a privacy leak? |
| delivery | outbox age, retry/quarantine, consumer acknowledgement class | Is external feed behind and can it be replayed safely? |
| lifecycle | candidates, holds, archive verified/blocked, purge dry-run/executed | Is evidence retained or disposed according to policy? |
| topology | mode, resolver success/failure, schema/policy cohort state | Are all active tenants audit-ready without IDs in telemetry? |
| compatibility | schema/codec/action-version distribution, unknown reader states | Can deployed writers/readers coexist and roll back safely? |
| capacity | row/index/partition growth, autovacuum, query plan latency | Will history/retention operations remain within safety budgets? |
| access | viewer/export/break-glass outcomes by bounded role/result | Is sensitive audit access being governed and reviewed? |

### On-call rules

1. Do not use application logs or distributed traces to retrieve audit diff values.
   Start with bounded health signals, then enter the protected history/operations
   workflow using an authorized incident purpose.
2. Treat a transaction-audit writer failure as a mutation availability/security
   event until policy proves otherwise. Do not advise teams to disable audit
   decorators or grant broad database privileges simply to restore traffic.
3. Treat `unknown commit` as genuinely unknown. Reconcile using idempotency key,
   durable transaction/revision state and authoritative database source; avoid
   re-running a business command on the assumption that timeout meant rollback.
4. For archive or purge fault, stop destructive progression and preserve primary
   evidence. Capture candidate range/policy/job version, then recover/rehearse
   readback before retrying.
5. For a suspected tenant leak, contain the affected reader/resolver path, rotate
   access as required, preserve protected access evidence and investigate both
   application predicates and database RLS/control-plane mapping.
6. For a privacy leak, stop unsafe viewers/exports, identify all replicas/object
   versions/backups/CI artifacts potentially involved and use legal/privacy-led
   remediation rather than silently redacting only the live table.
7. For a compatibility fault, fence incompatible writers/readers/tenant cohorts.
   Prefer a forward compatibility fix or read-only mode to a destructive schema
   rollback which would make retained evidence unreadable.
8. For a break-glass request, verify purpose, approver, expiry and minimum access;
   record use and schedule independent post-incident review before credentials lapse.

### Incident rehearsal set

| Drill | Expected outcome | Evidence to retain |
|---|---|---|
| audit insert codec failure | data and audit rollback; safe caller error | test trace class, no canary data, failed transaction proof |
| client disconnect after commit | one durable command/revision after reconciliation | idempotency lookup and final caller outcome |
| role revoke at runtime | readiness/error detects missing permission; no silent bypass | database grant test and remediation record |
| RLS/resolver misroute attempt | no foreign row returned/written | negative scope test, mapping/version diagnosis |
| archive destination outage | primary retained, candidate blocked, alert fires | archive verification failure and retry plan |
| corrupted/unknown codec row | viewer typed unavailable state, no panic/leak | version count and compatibility issue |
| export duplicate delivery | consumer dedupes, source revision unchanged | delivery IDs and replay result |
| legal hold during purge | destructive action aborts or preserves candidate | hold/range/job ordering proof |
| fake secret in mutation | absent from every audit/telemetry/export artifact | canary scan results |
| previous release rollback | deployment guard/read-only path protects readers | version matrix and rollback decision |

## Common false shortcuts to reject in review

1. "We have request logs, so we have an audit log." Logs are sampled/rotated,
   unstructured, broadly visible and do not prove transactional outcome.
2. "Events are audit." Event streams document business state transitions; they
   do not automatically capture authorization, actor provenance, field policy or
   protected historical-view semantics.
3. "Envers annotation covers it." ORM hooks do not cover raw SQL, bulk/cascade,
   cross-repository transaction, privacy classification or topology by magic.
4. "We will serialize the whole model for now." This is a one-way privacy/schema
   expansion and will capture future secrets before reviewers notice.
5. "The database is encrypted." Encryption at rest does not authorize every
   application role, export, replica, backup or telemetry processor to receive data.
6. "The trace ID is enough." OTel sampling/export retention and viewer permission
   differ from durable audit and must not decide its completeness.
7. "We can purge later." Retention/hold/archive/backups determine table/index and
   reference design today; retrofitting them after ungoverned collection is costly.
8. "A global admin can query all tenants." That is an unbounded cross-tenant
   capability; it needs purpose/cohort/time/authorization/evidence, not a nil scope.
9. "A `DELETE FROM audit` repair is harmless." A correction/remediation must retain
   provenance, authorization and a truthful account of evidence alteration.
10. "Sequence numbers prove no gaps." Rolled-back, restored or concurrent database
    sequences can have gaps; integrity claims require explicitly implemented proofs.
11. "Exports are just reports." An export is a new sensitive data store/delivery
    surface with artifact access, lifetime, recipient and duplication semantics.
12. "Audit writes can be best effort." That may be valid for a named low-risk
    policy, but it cannot be called transactional or compliance evidence by accident.

## Ownership and sign-off record

For each production audit policy, record the following named approvals in the
change/release system. This is a checklist record, not a permission to delegate
accountability into a generated document.

| Concern | Required sign-off evidence |
|---|---|
| business purpose | resource owner names decision/use case and action semantics |
| privacy | field/metadata classification, reader/export roles and retention disposition |
| security | actor/source trust boundary, DB roles, cross-tenant and break-glass model |
| data | PostgreSQL schema/index/partition/backup restore and transaction proof |
| application | repository coverage inventory, policy manifest and compatibility tests |
| operations | dashboards, alerts, on-call response, lifecycle/archive rehearsal |
| localization | stable message keys/action/reason rendering and fallback test |
| telemetry | approved bounded schema and collector privacy regression test |
| integrations | storage/event/outbox/export causal boundary and at-least-once test |
| release | expand/cutover/rollback plan, tenant cohort/version evidence |

### Final operator questions

1. Can you show a committed revision for a known safe test command, and prove the
   same command after forced rollback produced no revision?
2. Can a support user, compliance reviewer and unauthorized user see exactly the
   expected fields of that history without raw database access?
3. Can you demonstrate that a fake secret never reaches audit, export, trace, log
   or metric—even when serialization/export/retry errors are injected?
4. Can you restore an archived/old partition and still render its historical codec
   under the correct tenant/reader permission without reactivating it prematurely?
5. Can you explain the exact response if a client times out after commit and prove
   retry cannot create duplicate audit evidence or domain mutation?
6. Can you state the difference between normal one-tenant history and the
   exceptional cross-tenant investigation capability, including expiry/purpose?
7. Can you name which downstream export consumer may receive which fields and
   demonstrate duplicate/outage/replay behavior without losing local evidence?
8. Can you prove runtime application credentials cannot alter/delete historical
   revisions, and show the documented correction/break-glass procedure instead?
9. Can you deploy a newer codec then roll back application safely—or does the
   deployment guard stop an unsafe version combination before it writes history?
10. Can the owners state which audit guarantees are transactional/proven, which
    are at-least-once/eventual, and which have intentionally not been implemented?

---

## Review cards for high-risk design changes

Use a review card whenever a change expands an audit action, serializer, field,
viewer, topology, export, retention rule or database privilege. The card makes
the diff reviewable without assuming every reviewer has the whole roadmap in
working memory.

### RC-01 — a new auditable resource

State the stable resource key and business purpose.

State which concrete commands/mutations create each action and why raw/bulk paths
cannot bypass the declared repository/service seam.

State tenant/topology, actor/source/reason, retention class, reader roles and
whether the action can link to an event-store transaction or object evidence.

Attach create/update/delete/rollback/tenant/privacy canary conformance results.

### RC-02 — a new captured field

State whether this exact field is necessary for audit evidence rather than just
convenient for debugging or future analytics.

State canonical codec/version/size/null/redaction behavior and historical reader
behavior if the source model later renames, removes or reclassifies the field.

State all roles, exports, archives/backups and retention/erasure implications.

Attach a canary test showing it appears only in the authorized reader projection.

### RC-03 — a new sensitive transformation

State whether the transformation is omit, redact, mask, token or keyed digest,
including what privacy/security property it does and does not provide.

State key management, rotation, input entropy, collision/lookup behavior and
whether the result is visible/searchable/exportable to each role.

State migration/backup/archive behavior for old raw values, with legal authority.

Attach adversarial-input and no-fallback serializer tests.

### RC-04 — a new audit reader filter

State the investigator purpose, role and scoped query grammar this filter enables.

State database index/cost/timeout and whether it creates a new inference surface.

State cursor binding, cross-tenant behavior and every field/identity it can expose.

Attach explain plan plus denied-role/foreign-tenant/cursor-reuse tests.

### RC-05 — a new export recipient

State recipient legal/operational purpose, fields/minimization, residency and
recipient's retention/access/security contact.

State delivery semantics, idempotency/order/replay, credential rotation and how
outage/schema rejection will be contained without silent evidence loss.

State artifact storage/download/TTL/hold and audit-of-export-access requirements.

Attach fault injection for publish-before-ack, consumer rejection and duplicate replay.

### RC-06 — a new tenant topology/route

State row/database topology, trusted scope origin, resolver/RLS/credential boundary
and how a wrong mapping is detected before any query or audit write.

State schema/policy/migration/readiness cohort handling and backup/deletion/hold
responsibility for that tenant.

State permitted exceptional cross-tenant flow rather than relying on empty scope.

Attach query matrix and misroute/missing-scope/background-job tests.

### RC-07 — a new database migration

State expand/read/write/cutover/rollback/retire phases for every audit schema/value.

State longest retained historical reader/export/archive version and tenant cohort
compatibility, including lock/partition/replica impact.

State what data never gets rewritten and which governed redaction/correction path
would be required if values must change.

Attach old/current/future corpus, migration timing/plan and rollback guard evidence.

### RC-08 — an availability-policy change

State whether primary mutation rolls back, commits pending, or fails closed when
audit/outbox/archive/attempt storage is unavailable.

State caller result, idempotency/reconciliation, alert/SLO and risk owner acceptance.

State which dashboard/runbook line lets an operator distinguish known failure from
unknown commit state without reading private payloads.

Attach database disconnect, after-commit timeout and retry test results.

### RC-09 — a new bulk operation

State how victim selection is authorized, bounded and made identical to final
mutation subject set under races.

State individual versus summary audit form, no-op/partial/failure/retry semantics
and upper bounds for query/memory/diff/object export work.

State how raw filters/IDs are protected and what authorized reviewer can reconstruct.

Attach large/adversarial/race/unknown-outcome test and operational stop condition.

### RC-10 — an integrity/tamper-evidence statement

State threat actor, database/backup/archive trust assumptions and exact claim.

State algorithm/canonical bytes/ordering/partition/key custody/rotation/verifier
and which mutations, gaps or restorations it detects or does not detect.

State user-facing/report wording so a checkpoint is not marketed as global proof.

Attach controlled tamper/missing/wrong-order verification and coverage-gap output.

### RC-11 — an OTel or log schema addition

State bounded attribute/event/metric name and operational question it answers.

State explicit denial of actor/tenant/subject/value/filter/URL/SQL/error payload
and why correlation cannot become an audit-read bypass.

State exporter/sampling failure behavior and data retention/access characteristics.

Attach collector canary/absence test and high-cardinality stress test.

### RC-12 — a retention/legal-hold rule change

State evidence class/min-max/archive/purge authority/legal basis and which records
or referenced objects/backups/key versions are covered.

State hold race/failure/default behavior, two-person approval and restore/replay
reconciliation; spell out unresolved deletion-versus-preservation conflicts.

State reader/export implications once evidence is archived/redacted/destroyed.

Attach dry-run/readback/hold-race/restored-backup rehearsal results.

## Minimal artefacts that travel with a release

1. The audited-resource policy manifest and human-readable diff from prior release.
2. PostgreSQL migration plus schema version/rollback/partition/privilege notes.
3. Golden corpus covering oldest supported, current and unknown future codecs/actions.
4. Commit/rollback/retry/crash conformance report for each enabled topology adapter.
5. Privacy canary report covering database, history API, export, telemetry, logs and CI.
6. Query plans and capacity envelope for every newly authorized history filter/index.
7. Tenant cohort readiness report where db-per-tenant is active.
8. Retention/hold/archive/restore impact report when lifecycle schema/policy changed.
9. External consumer compatibility/fault/replay report for every export contract change.
10. Dashboard/alert/runbook links with named on-call and business/privacy/security owners.
11. Explicit deferrals list: unsupported write shapes, fields, viewers, topologies and claims.
12. Final release decision with any accepted risk and expiry/review date for exceptions.

## Boundary summary

Audit answers: who did which declared operation, under what bounded trusted context,
what approved safe evidence changed, whether it committed, and who may inspect it.

It does not answer every question about request logging, distributed tracing,
event replay, analytics, legal discovery, object retention, tenant administration
or database forensics by itself. Those integrations are valuable precisely when
their causal, authorization, delivery, privacy and lifecycle boundaries remain
explicit in this roadmap and in the shipped adapters.

---

## Worked first-resource walk-through

This walk-through is deliberately narrow: an order status transition in a
single-tenant PostgreSQL transaction. It gives an implementation team a shared
way to prove the pieces fit before attempting generic entity auditing.

### Input contract

1. HTTP/RPC host authenticates a principal and resolves an authorized tenant scope.
2. Host validates a command with opaque `orderID`, desired stable status enum and
   a bounded reason code; it does not pass raw JWT/header/filter into audit.
3. Service starts a caller-owned PostgreSQL unit of work and loads order under
   tenant scope, policy and optimistic/version rule.
4. Domain rule decides whether transition is legal. A refusal returns normal
   application error; optional attempt evidence is considered separately.
5. Service calls named repository method `TransitionStatus`, not raw generic SQL.

### Declared policy

```text
resource:       orders.order
action:         orders.status_transition
subject:        opaque order identity codec
fields:         status, status_reason_code
omitted:        payment tokens, full shipping address, internal notes
actor:          verified user or typed service actor
tenant:         exactly one verified scope
retention:      orders-audit-v1
viewer:         support sees action/time; compliance sees declared fields
transaction:    mutation + revision/item/value in one PostgreSQL transaction
```

The policy deliberately avoids "all order fields" and makes the business meaning
of the action clearer than an ORM-observed generic update.

### Commit path

1. Repository captures canonical prior `status` and optional prior reason from
   authorized row within the same transaction.
2. Repository applies update guarded by tenant/expected state/version conditions.
3. If no row wins, service classifies not-found/conflict/policy outcome and writes
   no successful audit item.
4. Decorator creates/reuses revision for the unit of work with actor kind/ref,
   tenant ref, trusted source, reason code and schema version.
5. Decorator writes one item with `orders.status_transition`, opaque subject,
   result `committed-pending`, field schema version and bounded action metadata.
6. Decorator writes two or fewer typed values: old/new enum, old/new reason code.
7. Unit of work commits mutation/revision/item/value atomically, then returns
   committed result; a revision may optionally receive a protected trace link.
8. Reader cursor later orders it by specified commit/order tuple and localizes
   status/action label without treating display language as historical state.

### Failure handling

1. If serialization validation fails, the transaction rolls back and caller gets
   safe failure; fake fallback JSON of entire order is forbidden.
2. If DB disconnect happens before known commit, service follows unknown-outcome
   reconciliation through command/revision identity before accepting retry.
3. If telemetry exporter fails after commit, audit stays durable and metric/log
   show only bounded diagnostics—no order ID/status/reason values.
4. If audit reader lacks compliance role, it sees action/time or a denial under
   its exact policy; it cannot fetch base tables or infer values from cursor.
5. If retention later archives the range, viewer shows archived/retrieval state;
   it does not silently change original action or erase the linkage explanation.

### Extension checkpoints

Before adding each one, run its focused conformance profile:

- event sourcing: link a commit reference only, prove no copied event payload;
- object evidence: reference a promoted version/digest only, prove no dead URL;
- db-per-tenant: demonstrate mapping fence/readiness and no database-name telemetry;
- external export: prove one transactional outbox, duplicate-tolerant consumer;
- bulk transition: prove exact victim semantics or choose a bounded summary policy;
- correction: append a new authorized action linking original revision, never rewrite;
- cross-tenant case: issue time/purpose/cohort grant and audit investigation access;
- redaction change: obtain governance decision and rehearse archive/backup impact.

If any checkpoint requires serializing unbounded models, using a global context,
granting schema-owner access or relying on traces as evidence, stop and redesign
that extension. The narrow path is intentionally the reusable foundation.

---

## Compact decision register for the first PostgreSQL adapter

The following decisions must be recorded as accepted, deferred or rejected before
the first adapter becomes a dependency other teams rely on. An unanswered row is
a design gap, not a default implementation choice.

| ID | Decision to make | Proof before acceptance |
|---|---|---|
| AD-01 | exact PostgreSQL transaction/connection ownership | commit/rollback/crash integration test |
| AD-02 | revision ID and item ordering tuple | concurrent cursor/retry/restore fixture |
| AD-03 | role/grant/RLS model | direct SQL least-privilege negative test |
| AD-04 | tenant reference and db-per-tenant binding | missing/misroute/cohort readiness test |
| AD-05 | actor/source/reason trusted codecs | hostile claims/baggage/impersonation test |
| AD-06 | initial resource/action/field policy | manifest/privacy owner approval/canary test |
| AD-07 | scalar codec/null/redaction/version envelope | oldest/current/future golden corpus |
| AD-08 | relation and bulk-write exclusions | strict coverage inventory/build gate |
| AD-09 | history query grammar/cursor/viewer roles | scope/explain/denial/cursor-binding suite |
| AD-10 | retention/hold/archive/backups disposition | dry-run/hold-race/readback/restore rehearsal |
| AD-11 | correction/redaction transformation behavior | append-only/provenance/cycle test |
| AD-12 | telemetry correlation/data schema | sampled/exporter-fault/privacy collector test |
| AD-13 | event-store reference boundary | no-payload/authorization/replay test |
| AD-14 | storage evidence reference boundary | promote/digest/version/denied-download test |
| AD-15 | external export need and delivery semantics | outbox duplicate/outage/replay consumer test |
| AD-16 | integrity claim or intentional absence | threat model/verifier coverage statement |
| AD-17 | online migration/rollback/version horizon | expand/cutover/rollback/cohort drill |
| AD-18 | capacity/partition/maintenance plan | query plan/growth/autovacuum/load evidence |
| AD-19 | availability result for audit subsystem faults | client/idempotency/unknown outcome/risk sign-off |
| AD-20 | break-glass and incident access control | expiry/approval/access-review rehearsal |

### Implementation order checklist

1. Decide AD-01 through AD-07 before creating a public audit decorator API.
2. Build AC-01 through AC-20 against real PostgreSQL and reject hidden-write paths.
3. Decide AD-09 through AD-12 before granting any history-reader production role.
4. Build AC-21 through AC-40 before enabling retention/archive/export capabilities.
5. Decide AD-13 through AD-20 only when that integration is actually in scope.
6. Build AC-41 through AC-50 before advertising cross-domain or high-assurance claims.
7. Re-run the applicable catalogue on every schema/codec/policy/topology release.
8. Keep known unsupported paths visible in the manifest/index; do not disguise them
   with a generic `audited=true` product claim.

This register keeps the Hibernate/Envers inspiration focused on its useful
revision/history model while requiring vv's explicit policy, tenancy, storage,
event-source, i18n and observability boundaries to be proven in the actual stack.

---

## First production rollout record

The first production enablement should be deliberately boring: one low-volume,
well-understood resource, one PostgreSQL topology, a narrow scalar policy and one
authorized single-subject history view. The record below is completed before the
feature flag exposes it to normal traffic.

### Preconditions

- the resource owner has named the legal/business/operator purpose for every action;
- privacy review approved captured fields, omitted fields, role projections and retention;
- security review verified actor/scope trust, PostgreSQL grants and absence of raw bypass;
- application tests passed AC-01 to AC-30 plus the resource-specific custom command;
- database migration/backup restore/query plan/capacity results are linked to release;
- on-call dashboard, alert and runbook have an owner who performed one fault drill;
- history endpoint has no broad list/full-text/export/cross-tenant query escape hatch;
- telemetry collector test proved the resource's safe canary never leaks to signals;
- deployment guard knows the current/previous reader/writer/codec compatibility range;
- unsupported operations are inventory-visible and fail closed/are explicitly non-audited.

### Canary rollout

1. Enable policy for internal synthetic tenant only; execute create/update/rollback.
2. Verify database rows through restricted audit reader and run canary-value scan.
3. Force a codec failure and a database rollback; verify no success evidence survives.
4. Verify dashboard has bounded committed/failed latency signals without subject values.
5. Enable one real tenant cohort; monitor write errors, reader authorization and size limits.
6. Rehearse idempotent retry after controlled response timeout before broader traffic.
7. Freeze expansion if any unknown codec, missing revision, foreign scope or sensitive leak occurs.
8. Record actual release/manifest/schema/adapter versions and named approval owners.

### Post-rollout review

Within the agreed review window, compare mutation inventory with revisions, inspect
reader denials and query plans, test restored data sample, review any break-glass
or attempt events, and decide whether the narrow slice is truly sufficient. Only
then add another action, field, topology or integration under the relevant review
card. Growth in audit coverage is a sequence of evidence-backed decisions, not an
automatic side effect of adding a decorator to more repositories.

## Release sign-off sample

```text
Audit resource: orders.order / orders.status_transition
Manifest: audit-policy-v1     Adapter: auditpg-v1     Schema: audit schema v1
Topology: shared PostgreSQL with verified tenant predicate and tested RLS
Writer guarantee: mutation and revision/item/value commit or roll back together
Reader guarantee: one tenant + support purpose; status values need compliance role
Retention: orders-audit-v1; archive dry run complete; no purge enabled in release
Telemetry: bounded write-result metrics only; fake-secret collector test passed
Known exclusions: generic bulk update, raw SQL, cross-tenant search, external feed
Required evidence: AC-01..30 report, rollback drill, grant test, query plan, runbook
Approvals: resource / privacy / security / data / operations, with release links
```

This small explicit record is preferable to a blanket phrase such as "auditing is
enabled." It gives the next engineer, operator and reviewer a precise starting
point, and makes it obvious which extension requires a new roadmap decision.

## Final boundary test

If a proposed feature needs all model fields, raw transport metadata, a global
tenant, unbounded query, generic cross-database transaction, trace payload or a
schema-owner runtime credential, it is outside this initial audit contract.

Return to its actual concern—analytics, debugging, event store, support search,
identity, migration or incident forensics—and design the integration explicitly.
That discipline is what allows an Envers-like revision history to be useful and
trusted instead of becoming a second, opaque copy of production data.

## Compact readiness matrix

| Capability | Required before enabling | Explicitly not implied |
|---|---|---|
| revision write | transaction, policy, codec, grants, rollback test | all tables/SQL audited |
| historical read | one-scope purpose/role/cursor tests | broad admin reporting |
| tenant route | verified scope and topology profile | global cross-tenant access |
| sensitive value | classification/serializer/canary test | safe analytics/search value |
| archive/purge | hold, readback, restore and approvals | legal erasure complete |
| trace link | bounded bridge/privacy collector test | trace is audit evidence |
| event link | typed commit ref/read gates | copied event payload or 2PC |
| evidence object | promote/digest/version lifecycle test | arbitrary attachment upload |
| external feed | outbox/recipient/dedupe fault test | exactly-once delivery |
| integrity seal | threat model/verifier coverage | universal tamper-proof claim |

For each enabled cell, link actual proof. For every disabled cell, retain the
intentional deferral in the policy/index so product and operators do not infer it
from similarly named capabilities in another satellite.

## Closing operating principle

The persistent record must be smaller, more deliberate and more protected than
the live object graph/request/trace it accompanies. A useful audit item is a
stable, authorized statement about a committed action—not a debugging snapshot.

When requirements genuinely demand more detail, add it through a named field,
viewer, retention and conformance decision. That keeps the system inspectable as
resources, tenants, codecs, releases and integrations grow.

Before a policy is widened, repeat the following short question set:

1. Is this evidence necessary for a declared decision, control or investigation?
2. Can an authorized reader see exactly this data and no neighboring secrets?
3. Does it commit/roll back with the claimed domain action?
4. Can it be rendered after future schema/i18n release without rewriting history?
5. Can it be retained, archived, held, corrected or erased truthfully?
6. Can tests prove it does not leak into telemetry, logs, exports or foreign tenants?
7. Is its operational failure behavior and owner acceptable to the business risk?

If any answer is unknown, keep the field/action/integration deferred until the
corresponding policy, adapter and operational evidence are available.

The first audit adapter should therefore optimize for a small proof surface.

Its policy must be readable by product, privacy, security and operations.

Its transactional guarantee must be reproducible in an integration test.

Its reader must be less powerful than a direct database connection.

Its evolution must preserve old evidence without silently expanding collection.

Its diagnostics must prove health while protecting the evidence they describe.

Those constraints are the durable contract this roadmap asks the framework to keep.
