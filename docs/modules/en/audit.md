# audit — declared, protected evidence

```go
import (
    "github.com/frostgrove/vv/audit"
    "github.com/frostgrove/vv/audit/auditcrud"
    "github.com/frostgrove/vv/audit/auditmemory"
)
```

**Module:** `audit`, `auditmemory`, `audittest`, and `auditcrud` are in the root
module and add no third-party dependency. `audit/auditpg` is a separate nested
module because it owns a PostgreSQL driver decision.

`audit` records allowlisted business facts and entity revisions. A declaration
fixes the meaning, retention, consequence, context, field codecs, privacy class,
and storage mode before a value reaches a writer. The writer receives canonical
evidence, never the caller's context values or an arbitrary object graph.

The current supported boundary is an **application-development alpha**. Manual
capture, grouping, idempotency/reconciliation, the memory store, transactional
CRUD, PostgreSQL persistence, and public one-page history are usable. Protected
history, resumable cursors, attempts, reconstruction, corrections, holds, and
purge planning are not shipped yet; their vocabulary being public is not a
capability claim.

---

## What you get

| | |
|---|---|
| `Declare` | A typed event with a closed resource/action/outcome/reason and allowlisted fields |
| `Define` | A typed entity policy over existing `crud.Meta`, including subject identity and reconstructable fields |
| `DeclareOperation` | A named operation and the exact event/entity actions it may contain |
| `Compile` · `Lineage` | An immutable catalog and its append-only retained lineage |
| `New` | A recorder assembled from one catalog set, writer, trusted context resolver, semantic digester, and identity keyring |
| `Recorder.Capture` | One standalone event with an immediate `Committed` receipt |
| `Recorder.Record` | Explicit operation and idempotency options, including a retry/reconciliation result |
| `Recorder.Within` · `Stage` | One operation revision containing several declared facts; joins only an exact source-bound root transaction |
| `auditmemory` | Concurrent in-process writer/log plus explicit deployment activation and immutable catalog-mutation readback |
| `auditcrud.Secured` | A sealed security-first CRUD terminal for create, assigned/unassigned save, update, hard/soft delete, and restore |
| `NewHistory` | Independently authorized, bounded public history for resource, subject, event type/target, and operation type/instance |
| `audittest.BasicHistory` | Reusable history conformance for a store implementation |
| `auditpg` | Exact schema readiness, catalog activation/readback, durable append/reconciliation, transaction joining, and basic history |

## Declare first

```go
type StatusPublished struct {
    Slug string
    At     time.Time
}

var AuditContext = audit.ContextFacts(
    audit.ScopeFact(
        audit.ContextRequired,
        audit.Provenances(audit.Verified),
        audit.Public,
        audit.AsPlaintext,
    ),
    audit.GeneratedOperationFact(audit.Public, audit.AsPlaintext),
)

var StatusPublishedEvent = audit.Declare(audit.EventPolicy[StatusPublished]{
    Semantics: audit.Semantics(1,
        audit.PolicyGolden("status.published.v1", statusPublishedPolicyFingerprint),
    ),
    Descriptor: audit.Descriptor{
        Resource:    "platform.status",
        Action:      "status.published",
        Owner:       "platform.team",
        Purpose:     "business.audit",
        Retention:   "business.forever",
        Consequence: audit.Required,
        Context:     AuditContext,
    },
    Target: audit.EventTarget(
        func(value StatusPublished) audit.Reference { return audit.Reference(value.Slug) },
        audit.Public,
        audit.AsPlaintext,
    ),
    Outcome: audit.EventOutcome(
        audit.Outcomes("published"),
        func(StatusPublished) audit.Outcome { return "published" },
    ),
    OccurredAt: audit.EventOccurredAt(func(value StatusPublished) time.Time { return value.At }),
})

var PublishStatus = audit.DeclareOperation(audit.OperationPolicy{
    Name:        "platform.status.publish",
    Semantics:   audit.Semantics(1),
    Retention:   "business.forever",
    Consequence: audit.Required,
    Context:     AuditContext,
    Members:     audit.OperationMembers(StatusPublishedEvent),
})
```

`PolicyGolden` is a versioned semantic fixture, not a generated schema hash to
ignore. Compute and review it with `ComputeEventFixtureFingerprint` or
`ComputeSubjectFixtureFingerprint`, then commit the approved value. Changing a
field, codec, privacy mode, outcome, or extractor without deliberately changing
the fixture is refused.

Use `NoEventTarget` for an intentionally targetless fact. An empty target is not
the spelling of absence. `EventValue`, `EventTokenized`, `EventProtected`, and
`EventRedacted` are the only value doors; arbitrary maps, errors, request bodies,
credentials, SQL, and localized text have no audit representation.

## Assemble and capture

Deployment and runtime are separate. Constructors allocate and validate; the
application performs activation explicitly.

```go
catalog, err := audit.Compile(audit.CatalogSpec{
    ID:         "platform.audit",
    Owner:      "platform.team",
    Generation: 1,
    Retention:  audit.RetentionRules(audit.KeepForever("business.forever")),
    Semantics:  semantic.Description(),
    Identities: identities.ActiveDescription(),
    Tokens:     tokens.ActiveDescription(),
    Integrity:  audit.IntegrityOnly(),
}, StatusPublishedEvent, PublishStatus)
if err != nil {
    return err
}

catalogs, err := audit.Lineage(catalog)
if err != nil {
    return err
}
log, err := auditmemory.NewLog(auditmemory.LogSpec{})
if err != nil {
    return err
}
deployment, err := auditmemory.NewDeployment(log)
if err != nil {
    return err
}
change, err := audit.NewCatalogChangeRef("deployments", "release-2026-09")
if err != nil {
    return err
}
if err := deployment.InstallAndActivate(ctx, catalogs, change); err != nil {
    return err
}
store, err := auditmemory.New(auditmemory.Spec{Log: log})
if err != nil {
    return err
}
recorder, err := audit.New(audit.Config{
    Catalogs:   catalogs,
    Writer:     store,
    Context:    applicationAuditContext,
    Semantics:  semantic,
    Identities: identities,
    Tokenizer:  tokens,
})
```

```go
draft, err := StatusPublishedEvent.New(StatusPublished{Slug: "api-v2", At: now})
if err != nil {
    return err
}
result, err := recorder.Record(ctx, draft,
    audit.InOperation(PublishStatus),
    audit.WithIdempotencyKey("publish/api-v2/v1"),
)
if err != nil {
    if key, ok := audit.ReconcileKeyOf(err); ok {
        return reconcileLater(key)
    }
    return err
}
receipt, ok := result.Receipt()
if !ok || receipt.Settlement() != audit.Committed {
    return errUnexpectedSettlement
}
```

Do not retry the business action when the append outcome is unknown. Preserve
the reconciliation key and call `Recorder.Lookup`. `Recorder.Retry` is only for
a standalone write proved certainly not written; transactional uncertainty is
reconciled, never replayed.

## Transactional CRUD

`auditcrud.Secured` is the terminal mutation boundary. It validates the model,
required effects, catalog membership, exact datasource, and ability to open a
root transaction while the application is assembled.

```go
base := invoices.Bind(source)
faulted := faults.Enrich[Invoice, int64]()(base.Core)
secured := auditcrud.Secured(recorder, InvoiceAudit, invoicePolicy)(faulted)
repo := crud.Wrap[Invoice, int64, InvoiceUpdate](secured)
```

The order is security → audit supervision → faults/store. A supported mutation
and its evidence commit or roll back together. Caller transactions without the
recorder's active group, savepoint laundering, wrong sources, write-only calls,
bulk updates/deletes, and effects whose exact persisted result cannot be proved
return `audit.ErrUnsupported` or a classified transaction error before mutation
I/O. Raw SQL and alternate repository handles remain explicit bypasses.

## Public one-page history alpha

History has a separate authority. Ordinary CRUD access never grants audit read
access.

```go
historyAuthority := audit.AccessAuthorityFunc(func(
    _ context.Context,
    request audit.AccessRequest,
) (audit.AccessDecision, error) {
    view := request.View()
    var scopes []audit.ScopedReference
    if view.Query.Scope.Kind == audit.ScopeExact {
        scopes = []audit.ScopedReference{view.Query.Scope.Reference}
    }
    return audit.AllowAccess(request, audit.AccessGrantSpec{
        Roles:           []audit.Reference{view.Query.Role},
        Scopes:          scopes,
        Catalogs:        view.Catalogs,
        Resources:       view.Target.Resources,
        Actions:         view.Query.Actions,
        Fields:          view.Query.Fields.Fields,
        Context:         view.Query.Context.Facts,
        Classifications: []audit.Classification{audit.Public},
        Direction:       view.Query.Direction,
        ExpiresAt:       authorityClock.Now().Add(time.Minute),
        MaxRevisions:    view.Query.Limit,
        MaxPages:        1,
        MaxBytes:        1 << 20,
    })
})

history, err := audit.NewHistory(audit.HistoryConfig{
    Profile:  audit.PublicOnePageDevelopmentAlpha,
    Recorder: recorder,
    Log:      store,
    Access:   historyAuthority,
})
if err != nil {
    return err
}

page, err := StatusPublishedEvent.History(history).Events(ctx, audit.Query{
    Purpose:   "incident.review",
    Role:      "auditor",
    Scope:     audit.CurrentScope(),
    Fields:    audit.NoFields(),
    Context:   audit.NoContext(),
    Direction: audit.NewestFirst,
    Limit:     100,
})
```

The authority receives an origin-bound `AccessRequest` and must answer with
`AllowAccess(request, grant)` or `DenyAccess(request, reason)`. A grant can only
narrow the normalized request. The example mirrors the requested ceiling only
to show the complete shape; a real authority derives stricter roles, scopes,
projections, expiry, and budgets from application policy. This alpha releases only `Public` evidence and
one bounded page. `HasMore` reports truncation, while `Cursor` deliberately
returns `ErrUnsupported`; protected values are never released without the
future independently committed access-evidence path.

When a retained catalog requires signatures, pass a `Verifier` containing the
exact verification-key inventory for every retained signed generation.
`NewHistory` refuses a missing, extra, or mismatched inventory, and every result
is verified against its record-era catalog before projection.

The zero `Fields` or `Context` value means **none**, never all. Ask explicitly
with `AllFields`/`AllContext` or narrow with `OnlyFields`/`OnlyContext`.

## Integration boundary

- Authentication and tenancy are mapped by an application-owned
  `ContextResolver`; only authenticated principals and authority-minted scopes
  become declared facts.
- Event sourcing records bounded commit coordinates, never event payloads, and
  does not claim the two stores committed atomically.
- Jobs keep logical effect identity separate from delivery-attempt identity.
- Storage events may name a governed namespace/key, never body, metadata,
  credentials, or signed URLs.
- OTel observers receive bounded non-identifying outcomes and work counts; they
  are not the evidence ledger.
- i18n renders safe errors after classification; locale and rendered text never
  enter evidence.
- Module profiles opt into runtime handles. Schema migration, catalog activation,
  workers, and destructive work remain application-owned lifecycle actions.

Executable examples live in `test/auditflow`; store behavior is exercised by
`audit/audittest` and the live PostgreSQL integration profile.

## Current hard boundary

Do not call the current alpha a production-complete audit product. It does not
yet provide access-evidence-backed protected disclosure, resumable immutable
cursors, durable attempts, reconstruction/comparison, correction/dispute,
legal-hold projection, or purge plans. Capability values report these as
unsupported. The implementation roadmap is
[2026-09-01-audit-log-roadmap.md](../../roadmaps/2026-09-01-audit-log-roadmap.md).
