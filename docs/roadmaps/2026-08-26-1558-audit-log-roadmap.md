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
