# AUDIT — USE CASES, INVARIANTS, AND DECLARATIVE DX

This document is the product contract for Frostgrove audit. The Go fragments
describe the intended shape and are not yet accepted API. The implementation is
allowed to change names, but not to weaken an observable guarantee without a
new decision.

The audit subsystem has four distinct products:

1. a security or business event records a decision or occurrence;
2. an entity revision records declared changes to one logical resource;
3. a reconstructable projection rebuilds only the fields whose policy explicitly
   promises reversible history;
4. an attempt lifecycle proves a durable start, bounded checkpoints, and one
   honest terminal or uncertain outcome for a declared operation.

They may share one revision envelope and one `operation_id`. They are not aliases.
An entity diff does not prove a business decision, and a business event does not
prove the complete state of a row. A correlation or one terminal event does not
prove that protected work started only after durable evidence or that exactly one
terminal outcome closed it.

## 1. Actors and goals

### AU-001 — Record a business or security event explicitly

An application author declares an event kind and records a typed occurrence such
as an approval, permission change, break-glass grant, export, or authentication
decision. The declaration chooses exactly one sealed target arm: a typed target
or explicit target absence. It never fabricates a reference for an occurrence
that has no logical target. The event carries a stable action, outcome, actor
chain, scope, reason code, source, time, correlation, causation, trace reference,
and the declared target presence according to policy. A present target declared
in a searchable storage mode is queryable through
the same protected, authorized, bounded history contract as an entity subject;
knowing the generated operation identity is not a prerequisite for investigation.
A protected-only or redacted target remains valid evidence but has no target-history
door and never falls back to a broad scan. A generic context
map, request dump, raw error, SQL string, or bearer credential has no spelling.

### AU-002 — Record a committed entity revision automatically

An application author binds a CRUD repository once through the sealed
`auditcrud.Secured` composite. A supported create/save, update, delete, or restore
records exactly the declared safe before/after evidence in the same transaction
as the mutation.
`DefaultService.Replace` is covered through the composite's Save path; the technical evidence
still says whether the entity was created or changed rather than guessing the
caller verb. A failed, denied, rolled-back, or no-op mutation does not claim a
committed change. A hard delete terminally closes that scoped subject chain;
recreating the same database key cannot silently start a second audit identity.

### AU-003 — Group related evidence without inventing atomicity

An application author may give several events and revisions one operation and
revision identity. They form one atomic revision only when every write shares the
same proven transaction authority. Grouping values from different transactions
is correlation, never an atomicity claim.

### AU-004 — Investigate history safely

An authorized investigator asks for bounded history by one permitted scope and
entity subject or resource declaration, declared entity index, event type/target/index,
operation type or exact operation, attempt type/target/exact operation, actor hop, or declared
context coordinate; exact revision/item inspection and an explicit short-lived
cross-scope grant are separate doors. Purpose, role, complete scoped-reference
identity, field/context projection, signed-observed or explicitly unsigned-
recorded time range, direction, changed fields, event codes, and query class are
authorized independently. The public API exposes origin-sealed typed equality
selectors, including bounded canonical conjunctions, not a generic property or
analytics language. A broad attempt investigation returns transition evidence,
never an incompletely inferred attempt status; exact operation status is a
distinct all-or-nothing door.
Every page cursor is encrypted and authenticated, freezes the original request,
requester, original and prior-effective grant ceilings, cumulative progress,
expiry, store, and admitted catalog lineage, and is
reauthorized on every continuation. Current authority may narrow or revoke that
grant but can never reintroduce anything removed on an earlier page. No page leaves the kernel
until its access evidence is independently committed outside any caller
transaction.

### AU-005 — Reconstruct declared state

An application author may declare selected fields reconstructable. The reader can
rebuild that declared projection and its authenticated present, soft-deleted, or
hard-deleted entity lifecycle state at a revision or causally closed integrity-bound
observed-time prefix; unsigned store-recorded time is never an authenticated boundary.
The request verifies the store-asserted subject head's signed revision and leaf
through genesis before it
trusts that prefix. Redacted and one-way
evidence report their own explicit `Redacted` or `Tokenized` knowledge; missing
keys, authenticated destruction, unknown codecs, and fields not yet captured by
their record-era catalog likewise remain explicit
unknown states. None becomes a guessed zero value. Merely
crossing a retention cutoff changes purge eligibility, not readable knowledge. A revert is a new authorized
mutation and a new audit revision, never a history rewrite. Retained catalog
generations authenticate old records and provide explicit upcast paths; the
current catalog alone decides present read authority.
Two ordered authenticated boundaries may be compared under one combined budget
and one access decision. Their lifecycle states participate in the comparison.
A field or lifecycle endpoint whose truth is hidden or unavailable reports an
indeterminate difference rather than guessed equality. An exact bounded neighbor
query reports predecessor and successor independently as present, proven chain
boundary, or unavailable; retention, authorization, or budget loss never becomes
false absence.

### AU-006 — Prove what was and was not captured

The policy owner reviews a deterministic named/versioned catalog lineage containing resources, actions,
fields, manifest-public non-sensitive code inventories, executable-policy versions/golden fingerprints,
codec wire fingerprints, classifications, target presence, typed equality indexes,
projections, retention, failure
consequences, context facts, hold-matter privacy policy, and owners. Each generation binds its predecessor
and is retained for verification. Adding a model field changes nothing. Adding
audit evidence or changing sampled executable meaning changes the manifest
fingerprint and requires an explicit declaration/version.

### AU-007 — Survive failures and retries truthfully

The caller can distinguish malformed declarations, bad wiring, refused input,
authorization denial, conflict, a certainly failed append, an unconfirmed append,
an unreadable historical value, and a backend outage. The framework never
automatically retries a mutation or an unconfirmed audit append. Caller-invoked
standalone `Retry` may submit only the exact frozen audit append; a
transaction-bound token remains lookup-only. An idempotency key can reconcile the
same logical revision even when randomized protection produces new ciphertext,
but cannot make two different revisions one event. Exact stored envelopes have
their own integrity commitment, signed when the catalog requires it, and cannot
be moved between fields or revisions.

### AU-008 — Plan retention and operate holds, corrections, and verification

An operator can inventory evidence by retention class, produce a bounded and
fenced hold-aware purge plan, place or release one identified legal-hold
membership without disturbing overlapping matters, append a
correction or dispute, and verify integrity. Release 1 does not execute purge,
destroy keys, or deliver exports: those need a separately accepted CAS/fence or
outbox contract. Destructive work starts nowhere implicitly and never bypasses
hold or manifest policy. Each hold transition signs its stable private
HoldIdentity plus authenticated expected and resulting membership/count/epoch/
canonical-identity-set state and the store applies it with one
exact CAS; projection indexes are never trusted without that transition proof.

### AU-009 — Integrate without an extension mesh

Authentication, tenancy, jobs, event sourcing, storage, telemetry, and application
modules map into audit-owned bounded values at the composition root. Production
packages do not import each other pairwise. OTel is an optional diagnostic mirror,
not the authoritative ledger. It may receive only bounded non-identifying work
counters such as scanned/verified revisions, pages, store calls, gaps, and budget
stops; sampling/export failure never removes required audit evidence.

### AU-010 — Test a store and an application policy without PostgreSQL

The root module ships a complete concurrent in-memory store and a public store
conformance suite. Applications can check declaration uniqueness, codec round
trips and wire fixtures, named subject/event semantic goldens, explicit target
absence, subject injectivity over their own samples, redaction canaries,
actor/scope mapping, entity-index aliases, lifecycle/neighbor honesty, attempt
facet separation, reconstruction, and reader authorization without a database.

### AU-011 — Prove a durable operation attempt from start through settlement

An application author declares a typed attempt lifecycle for one sealed operation
and chooses a typed target or explicit target absence at declaration time.
Its advertised Required `Run` gate independently commits start before it invokes
the protected job, authentication, HTTP, or application callback. Bounded
checkpoints, an explicit nonterminal OutcomeUnknown state, and one truthful
Succeeded, Failed, Cancelled, or explicitly adjudicated Abandoned terminal append
immutable signed transitions under one attempt-head CAS. A
transactional terminal may commit atomically with the business/entity revision;
an uncertain settlement is reconciled rather than relabelled failure. Restart
resumes only an exact verified open chain. BestEffort remains visibly
non-authoritative and gives no handle when its start is absent or unconfirmed.
Attempt results expose a resulting state only when committed or replayed evidence
authenticates one; a failed or unconfirmed append never guesses it.

## 2. Declarative DX target

The shortest intended production composition is shaped like this. Its explicit
deployment step has already installed and activated the exact `catalogs` lineage
through a deployment-only concrete handle; the `store` below is a different concrete
runtime handle whose dynamic method set has no `CatalogAdmin`. Runtime `audit.New`
verifies the declared state and never provisions it implicitly. `orderSubjectSemantics` and
`orderApprovedSemantics` are committed SemanticGolden values generated from
named application fixtures by audittest; typed samples never live in production
declarations:

```go
var OrderStatus = audit.Reconstruct[Order](
	"Status",
	"status",
	audit.Text(),
	audit.Internal,
)

var OrderAmount = audit.Value[Order](
	"AmountMinor",
	"amount_minor",
	audit.Int64(),
	audit.Internal,
)

var OrderStatusIndex = audit.EntityTokenIndex[Order, string](
	"Status",
	"status_lookup",
	audit.Text(),
	audit.Internal,
)

var OrderAmountIndex = audit.EntityTokenIndex[Order, int64](
	"AmountMinor",
	"amount_lookup",
	audit.Int64(),
	audit.Internal,
)

var OrderAuditContext = audit.ContextFacts(
	audit.ActorChain(audit.ContextRequired, audit.Provenances(audit.ServerDerived, audit.Verified), audit.Personal, audit.AsToken),
	audit.ScopeFact(audit.ContextRequired, audit.Provenances(audit.ServerDerived, audit.Verified), audit.Personal, audit.AsToken),
	audit.OperationFact(audit.ContextRequired, audit.Provenances(audit.ServerDerived), audit.Internal, audit.AsPlaintext),
	audit.CorrelationFact(audit.ContextOptional, audit.Provenances(audit.ServerDerived, audit.Forwarded), audit.Internal, audit.AsPlaintext),
)

var Orders = audit.Define(audit.Policy[Order, OrderID]{
	Model:     OrderRepository.Meta(),
	Semantics: audit.Semantics(1, orderSubjectSemantics),
	Descriptor: audit.Descriptor{
		Resource:    "commerce.order",
		Owner:       "commerce",
		Purpose:     "accountability",
		Retention:   "business.7y",
		Consequence: audit.Required,
		Context:     OrderAuditContext,
	},
	Subject: audit.TokenizedSubject(func(id OrderID) string { return id.String() }, audit.Internal),
	Actions: audit.Actions(
		audit.EntityCreated,
		audit.EntityChanged,
		audit.EntitySoftDeleted,
		audit.EntityRestored,
	),
	Fields: audit.Fields(
		OrderStatus,
		OrderAmount,
		OrderStatusIndex.Field(),
		OrderAmountIndex.Field(),
		audit.Optional[Order]("ApprovedAt", "approved_at", audit.Time(), audit.Internal),
		audit.Redacted[Order]("CustomerEmail", "customer_email", audit.Text(), audit.Personal),
	),
})

var ApprovalRuleIndex = audit.EventPlaintextIndex(
	"approval_rule",
	func(v OrderApproval) string { return v.Rule },
	audit.Text(),
	audit.Internal,
)

var OrderApproved = audit.Declare(audit.EventPolicy[OrderApproval]{
	Semantics: audit.Semantics(1, orderApprovedSemantics),
	Descriptor: audit.Descriptor{
		Resource:    "commerce.order",
		Action:      "commerce.order.approved",
		Owner:       "commerce",
		Purpose:     "accountability",
		Retention:   "business.7y",
		Consequence: audit.Required,
		Context:     OrderAuditContext,
	},
		Target: audit.EventTarget(approvalTarget, audit.Internal, audit.AsToken),
	Outcome: audit.EventOutcome(
		audit.Outcomes("approved", "rejected"),
		approvalOutcome,
	),
	Reason: audit.EventReason(
		audit.Reasons("policy.approved", "policy.rejected", "manual.override"),
		approvalReason,
	),
	OccurredAt: approvalOccurredAt,
	Fields: audit.EventFields(
		ApprovalRuleIndex.Field(),
		audit.EventProtected("reason_detail", func(v OrderApproval) string { return v.ReasonDetail }, audit.Text(), audit.Personal),
	),
})

var ApproveOrder = audit.DeclareOperation(audit.OperationPolicy{
	Name:        "commerce.order.approve",
	Semantics:   audit.Semantics(1),
	Retention:   "business.7y",
	Consequence: audit.Required,
	Context:     OrderAuditContext,
	Members: audit.OperationMembers(
		Orders.Action(audit.EntityChanged),
		OrderApproved,
	),
})

func composeAudit(ctx context.Context) error {
	catalog, err := audit.Compile(audit.CatalogSpec{
		ID:         "commerce.audit",
		Owner:      "commerce",
		Generation: 1,
		Retention: audit.RetentionRules(
			audit.KeepFor("business.7y", audit.CalendarPeriod{Years: 7}),
			audit.KeepFor("security.7y", audit.CalendarPeriod{Years: 7}),
		),
		Semantics:  semanticDigester.Description(),
		Identities: identityKeys.ActiveDescription(),
		Protection: protector.Description(),
		Tokens:     tokenizer.ActiveDescription(),
		Integrity:  audit.RequireSignature(signer.Description()),
		Control: audit.ControlPolicy{
			Resource:    "audit.control",
			Semantics:   audit.Semantics(1),
			Purpose:     "security.audit_control",
			Retention:   "security.7y",
			Consequence: audit.Required,
			Context:     OrderAuditContext,
			Matter:      audit.HoldMatter(audit.ReferenceText(), audit.Secret, audit.AsProtected),
			Actions: audit.ControlActions(
				audit.HistoryRead,
				audit.HistoryDenied,
				audit.ControlDenied,
				audit.CorrectionAppended,
				audit.DisputeAppended,
				audit.IntegrityVerified,
				audit.InventoryRead,
				audit.HoldPlaced,
				audit.HoldReleased,
				audit.PurgePlanned,
				audit.AttemptContinuationAuthorized,
				audit.AttemptAccessDenied,
			),
			Reasons: audit.ControlReasons(
				audit.ReasonsFor(audit.HistoryDenied, audit.Reasons("access.denied", "access.revoked")),
				audit.ReasonsFor(audit.ControlDenied, audit.Reasons("control.denied", "control.revoked")),
				audit.ReasonsFor(audit.CorrectionAppended, audit.Reasons("assertion.inaccurate")),
				audit.ReasonsFor(audit.DisputeAppended, audit.Reasons("assertion.disputed")),
				audit.ReasonsFor(audit.AttemptAccessDenied, audit.Reasons("attempt.access_denied", "attempt.access_revoked")),
			),
		},
	}, Orders, OrderApproved, ApproveOrder, ApproveOrderAttempt)
	if err != nil {
		return err
	}
	catalogs, err := audit.Lineage(catalog)
	if err != nil {
		return err
	}
	ledger, err := audit.New(audit.Config{
		Catalogs:   catalogs,
		Writer:     store,
		Context:    applicationAuditContext,
		Privacy:    productionPrivacyAdmission,
		Semantics:  semanticDigester,
		Identities: identityKeys,
		Signer:     signer,
		Protector:  protector,
		Tokenizer:  tokenizer,
	})
	if err != nil {
		return err
	}
	history, err := audit.NewHistory(audit.HistoryConfig{
		Recorder:       ledger,
		Log:            store,
		Exact:          store,
		Attempts:       store,
		Access:         applicationAuditAccess,
		Denials:        applicationAuditDenialLimiter,
		Cursors:        cursorKeys,
		CursorLifetime: 15 * time.Minute,
		Revealer:       productionRevealer,
		Verifier:       productionVerifier,
	})
	if err != nil {
		return err
	}
	attempts, err := audit.NewAttempts(audit.AttemptsConfig{
		Recorder:          ledger,
		State:             store,
		Types:             store,
		History:           history,
		SettlementTimeout: 5 * time.Second,
	})
	if err != nil {
		return err
	}
	_ = attempts

	orders := OrderRepository.Bind(
		source,
		auditcrud.Secured(ledger, Orders, orderAccess),
		faults.Enrich(),
	)
	_ = orders

	draft, err := OrderApproved.New(approval)
	if err != nil {
		return err
	}
	result, err := ledger.Record(ctx, draft)
	if err != nil {
		return err
	}
	_ = result
	return nil
}
```

`auditcrud.Secured` is the sole release-alpha audit CRUD entry. It constructs one
closed `security Gate -> private audit receiver -> faults/store` graph: the
returned terminal exposes neither `Next` nor an unsecured audit receiver, and
application middleware cannot be inserted inside the composite. Factory and bind
validation reject invalid recorder, resource policy, security policy, metadata,
catalog, source, Writer, or required inner effects before callbacks or I/O. Gate
authorizes and inspects before the mutation transaction; the private receiver
carries and revalidates that copied fence only after exact source resolution
(either no binding for an owned root or a source-bound executor) and framework
root-transaction proof. The atomic claim covers the mutation and its evidence,
not arbitrary policy-callback I/O.

`EventPolicy.Target` and the target arm inside `AttemptStart` are nonzero sealed
choices. Targeted declarations use their typed target constructors; targetless
declarations spell `audit.NoEventTarget[T]()` or `audit.NoAttemptTarget[T]()`.
There is no zero-value default and no convention that maps absence to an empty
reference, resource name, operation identity, or sentinel value. The choice is
manifest meaning and therefore participates in policy/replay fingerprints and
every applicable semantic, authorization, cursor, result, and integrity input.

Deployment is a separate executable workflow. The complete genesis-to-active plan
is safely rerunnable: every exact install/activation replay is idempotent, and each
step below opens and closes deployment authority so a restart can occur at any
boundary. It then reads back the bounded immutable mutation log and verifies the
complete exact records after a final reopen. The
serving graph receives neither this concrete deployment handle nor authority to mint
its change references. The application-supplied `prepare` closure calls
`audit.PrepareCatalogActivation` with a separately opened read-only runtime
StoreInfo/AttemptLog/AttemptTypeState/ExactLog, the exact current lineage, and
Verifier; genesis
uses the deployment origin with an empty gate. It performs no catalog mutation:

```go
type auditCatalogStep struct {
	Manifest audit.Manifest
	Install  audit.CatalogChangeRef
	Activate audit.CatalogChangeRef
}

type auditDeploymentOpener func(context.Context) (audit.DeploymentStore, error)
type auditActivationPreparer func(context.Context, audit.CatalogRef, audit.Manifest) (audit.CatalogActivationProof, error)

func withAuditDeployment(
	ctx context.Context,
	open auditDeploymentOpener,
	run func(audit.DeploymentStore) error,
) (err error) {
	admin, err := open(ctx)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := admin.Close(); err == nil {
			err = closeErr
		}
	}()
	return run(admin)
}

func applyAuditCatalogs(
	ctx context.Context,
	open auditDeploymentOpener,
	prepare auditActivationPreparer,
	steps []auditCatalogStep,
) (manifests []audit.Manifest, mutations []audit.CatalogMutationView, err error) {
	manifests = make([]audit.Manifest, 0, len(steps))
	mutations = make([]audit.CatalogMutationView, 0, len(steps)*2)
	var expected audit.CatalogRef
	for _, step := range steps {
		if err := withAuditDeployment(ctx, open, func(admin audit.DeploymentStore) error {
			return admin.InstallCatalog(ctx, step.Manifest, step.Install)
		}); err != nil {
			return nil, nil, err
		}
		next := step.Manifest.Ref()
		proof, err := prepare(ctx, expected, step.Manifest)
		if err != nil {
			return nil, nil, err
		}
		if err := withAuditDeployment(ctx, open, func(admin audit.DeploymentStore) error {
			return admin.ActivateCatalog(ctx, expected, next, step.Activate, proof)
		}); err != nil {
			return nil, nil, err
		}
		manifests = append(manifests, step.Manifest)
		mutations = append(mutations,
			audit.CatalogInstalled(next, step.Install),
			audit.CatalogActivated(expected, next, step.Activate, proof),
		)
		expected = next
	}
	return manifests, mutations, nil
}

func verifyAuditCatalogs(
	ctx context.Context,
	open auditDeploymentOpener,
	manifests []audit.Manifest,
	expected []audit.CatalogMutationView,
) (err error) {
	admin, err := open(ctx)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := admin.Close(); err == nil {
			err = closeErr
		}
	}()
	if err := admin.VerifyCatalogs(ctx, manifests); err != nil {
		return err
	}
	mutations, err := admin.CatalogMutations(ctx)
	if err != nil {
		return err
	}
	return audit.VerifyCatalogMutations(admin, expected, mutations)
}

func deployAuditCatalog(
	ctx context.Context,
	open auditDeploymentOpener,
	prepare auditActivationPreparer,
	steps []auditCatalogStep,
) error {
	manifests, mutations, err := applyAuditCatalogs(ctx, open, prepare, steps)
	if err != nil {
		return err
	}
	return verifyAuditCatalogs(ctx, open, manifests, mutations)
}
```

The first middleware is outermost. Security decides before capture. The audit
adapter opens or joins exactly one transaction through the repository source,
executes the mutation once, captures the returned persisted row when that is the
honest source of after-state, and appends once. Telemetry observes at the
application composition root; there is no invented audit×OTel repository layer.

Grouping is explicit and transaction-neutral:

```go
func approveOrder(ctx context.Context, ledger *audit.Recorder, orders crud.Core[Order, OrderID]) (pending audit.ReconcileKey, err error) {
	err = orders.Tx(ctx, func(tx context.Context) error {
		groupResult, err := ledger.Within(tx, audit.GroupSpec{
			Operation: ApproveOrder,
		}, func(group context.Context) error {
			if _, err := orders.Update(group, id, patch); err != nil {
				return err
			}
			draft, err := OrderApproved.New(approval)
			if err != nil {
				return err
			}
			return ledger.Stage(group, draft)
		})
		if key, ok := groupResult.ReconcileKey(); ok {
			pending = key
		}
		return err
	})
	return pending, err
}
```

`Within` resolves a catalog-declared operation and freezes the audit writer's
exact transaction authority before invoking its callback. The callback stages
bounded immutable items; after it returns successfully, `Within` sorts them by
canonical bytes and appends once before returning to the outer transaction
callback. It does not open a transaction and therefore cannot turn two
databases into one atomic unit. A group without a caller-owned authority may hold
manual audit items, but audited CRUD refuses before mutation I/O in that group.
Audited CRUD also refuses an unsafe, foreign, or changed binding; the legal path
must prove the same source-bound authority frozen by `Within`. Its receipt is
pending while the caller-owned transaction is open; final commit classification
belongs to that transaction's owner. An exact same-recorder nested group joins
without its own receipt; an undeclared operation or conflicting group refuses
before its callback. Concurrent staging is bounded and race-safe, while canonical
order makes scheduling irrelevant. Any failed required mutation capture poisons
the active group. `Within` therefore returns the capture failure even when its
callback accidentally suppresses the adapter error, and the transaction owner
must roll back the whole unit.

Protected reads are capability-oriented rather than CRUD-shaped:

```go
func investigate(ctx context.Context, ledger *audit.Recorder) error {
	history, err := audit.NewHistory(audit.HistoryConfig{
		Recorder: ledger,
		Log:      store,
		Exact:    store,
		Attempts: store,
		Access:   applicationAuditAccess,
		Denials:  applicationAuditDenialLimiter,
		Cursors:  cursorKeys,
		CursorLifetime: 15 * time.Minute,
		Revealer: productionRevealer,
		Verifier: productionVerifier,
	})
	if err != nil {
		return err
	}
	page, err := Orders.History(history).Subject(ctx, orderID, audit.Query{
		Purpose:   "support.investigation",
		Role:      "support.investigator",
		Scope:     audit.CurrentScope(),
		Fields:    audit.OnlyFields("status", "amount_minor"),
		Context:   audit.OnlyContext(audit.ActorChainContext, audit.CorrelationContext),
		Time:      audit.AllObservedTime(),
		Direction: audit.NewestFirst,
		Changed:   audit.AnyChanged("status"),
		Limit:     100,
	})
	if err != nil {
		return err
	}
	_ = page

	targetPage, err := OrderApproved.History(history).Target(ctx, audit.Reference(orderID.String()), audit.Query{
		Purpose:   "support.investigation",
		Role:      "support.investigator",
		Scope:     audit.CurrentScope(),
		Fields:    audit.OnlyFields("approval_rule"),
		Context:   audit.NoContext(),
		Time:      audit.AllOccurredTime(),
		Direction: audit.NewestFirst,
		Outcomes:  []audit.Outcome{"approved"},
		Limit:     100,
	})
	if err != nil {
		return err
	}
	_ = targetPage

	rulePage, err := audit.ByEventIndex(
		ctx,
		OrderApproved.History(history),
		ApprovalRuleIndex,
		"risk.v3",
		audit.Query{
			Purpose: "support.investigation",
			Role:    "support.investigator",
			Scope:   audit.CurrentScope(),
			Fields:  audit.OnlyFields("approval_rule"),
			Limit:   100,
		},
	)
	if err != nil {
		return err
	}
	_ = rulePage

	statusSelector, err := audit.TryAfterAnyOf(OrderStatusIndex, "approved", "fulfilled")
	if err != nil {
		return err
	}
	amountSelector, err := audit.TryEitherAnyOf(OrderAmountIndex, int64(10_000), int64(25_000))
	if err != nil {
		return err
	}
	where, err := audit.TryAllOf(statusSelector, amountSelector)
	if err != nil {
		return err
	}
	indexedPage, err := Orders.History(history).Revisions(
		ctx,
		audit.Query{
			Purpose: "support.investigation",
			Role:    "support.investigator",
			Scope:   audit.CurrentScope(),
			Fields:  audit.OnlyFields("status", "amount_minor"),
			Where:   where,
			Limit:   100,
		},
	)
	if err != nil {
		return err
	}
	_ = indexedPage

	projection, err := Orders.History(history).State(
		ctx,
		orderID,
		audit.AtRevisionAfter(revisionRef),
		audit.StateRequest{
			Query: audit.Query{
				Purpose: "support.investigation",
				Role:    "support.investigator",
				Scope:   audit.CurrentScope(),
				Fields:  audit.OnlyFields("status"),
			},
			Budget: audit.ReconstructionBudget{
				MaxRevisions: 10_000,
				MaxPages:     100,
				MaxBytes:     64 << 20,
			},
		},
	)
	if err != nil {
		return err
	}
	projectedStatus := audit.Projected(projection, OrderStatus)
	if projectedStatus.Knowledge() == audit.FieldKnown {
		status, ok := projectedStatus.Get()
		_, _ = status, ok
	}
	entityState, stateKnown := projection.State()
	_, _ = entityState, stateKnown

	comparison, err := Orders.History(history).Compare(
		ctx,
		orderID,
		audit.AtRevisionAfter(previousRevisionRef),
		audit.AtRevisionAfter(revisionRef),
		audit.StateRequest{
			Query: audit.Query{
				Purpose: "support.investigation",
				Role:    "support.investigator",
				Scope:   audit.CurrentScope(),
				Fields:  audit.OnlyFields("status"),
			},
			Budget: audit.ReconstructionBudget{
				MaxRevisions: 10_000,
				MaxPages:     100,
				MaxBytes:     64 << 20,
			},
		},
	)
	if err != nil {
		return err
	}
	changed, known := audit.Compared(comparison, OrderStatus).Changed()
	beforeState, beforeStateKnown := comparison.Before().State()
	afterState, afterStateKnown := comparison.After().State()
	_, _, _, _, _, _ =
		changed, known, beforeState, beforeStateKnown, afterState, afterStateKnown

	neighbors, err := Orders.History(history).Neighbors(
		ctx,
		orderID,
		revisionRef,
		audit.NeighborRequest{
			Query: audit.Query{
				Purpose: "support.investigation",
				Role:    "support.investigator",
				Scope:   audit.CurrentScope(),
				Fields:  audit.NoFields(),
			},
			Budget: audit.ReconstructionBudget{
				MaxRevisions: 10_000,
				MaxPages:     100,
				MaxBytes:     64 << 20,
			},
		},
	)
	if err != nil {
		return err
	}
	_, _ = neighbors.Previous(), neighbors.Next()

	control, err := audit.NewControl(audit.ControlConfig{
		Recorder:       ledger,
		Exact:          store,
		Attempts:       store,
		AttemptTypes:   store,
		Mutations:      store,
		Lifecycle:      store,
		Authority:      applicationAuditControl,
		Denials:        applicationAuditDenialLimiter,
		Revealer:       productionRevealer,
		Verifier:       productionVerifier,
		Fences:         fenceKeys,
		CursorLifetime: 15 * time.Minute,
		Clock:          productionRetentionClock,
	})
	if err != nil {
		return err
	}
	holdSubject, err := Orders.SelectSubject(orderID, audit.EntityChanged)
	if err != nil {
		return err
	}
	hold, err := control.PlaceHold(ctx, audit.PlaceHoldCommand{
		Hold:           holdID,
		Matter:         matterReference,
		Revision:       revisionRef,
		Access: audit.ExactControlAccess{
			Scope:           audit.CurrentScope(),
			Resources:       []audit.Resource{"commerce.order"},
			Actions:         []audit.Action{audit.Action(audit.EntityChanged)},
			Coordinates:     []audit.EvidenceSelector{holdSubject},
			Classifications: []audit.Classification{audit.Internal, audit.Personal},
		},
		Purpose:        "legal.discovery",
		Role:           "legal.hold_operator",
		IdempotencyKey: requestID,
	})
	if err != nil {
		return err
	}
	_ = hold
	return nil
}
```

The three `Entity...Index` constructors seal an exact declared model field, codec,
classification, and plaintext, token, or indexed-protected equality mode.
`BeforeEquals`, `AfterEquals`, and `EitherEquals` are the entire side algebra. The
`Try...AnyOf` forms canonicalize a bounded nonempty set of typed values for one
index; `TryAllOf` canonicalizes a bounded nonempty conjunction of origin-sealed
selectors compatible with the bound typed history door. One entity or event item,
not different sibling items in its containing revision, must satisfy every
item-level conjunct. After authorization, History expands retained token/index
aliases internally and then rechecks the authenticated
index values after store lookup. No runtime field name, metadata path, token, SQL
fragment, or backend predicate enters this surface.

Durable attempt capture is declared independently from an ordinary terminal
event and remains explicit at the composition root:

```go
type ApprovalStart struct {
	Order      OrderID
	Invocation audit.Reference
}

type ApprovalCheckpoint struct {
	Code audit.AttemptCheckpointCode
}

type ApprovalFinish struct {
	Receipt audit.Reference
}

var ApproveOrderAttempt = audit.DeclareAttempt(
	audit.AttemptPolicy[ApprovalStart, ApprovalCheckpoint, ApprovalFinish]{
		Operation: ApproveOrder,
		Semantics: audit.Semantics(1,
			audit.PolicyGolden("approval-attempt-v1", approvalAttemptSemantics),
		),
		Descriptor: audit.AttemptDescriptor{
			Resource:    "commerce.order.attempt",
			Owner:       "commerce",
			Purpose:     "accountability",
			Retention:   "business.7y",
			Consequence: audit.Required,
			Context:     OrderAuditContext,
		},
		MaxOpen:        15 * time.Minute,
		MaxCheckpoints: 32,
		MaxStateBytes:  512 << 10,
		Continuity: audit.AttemptOwnedBy(
			audit.AttemptEffectiveActorOwner,
		),
		Start: audit.AttemptStart(
			audit.AttemptTarget(
				func(v ApprovalStart) audit.Reference { return audit.Reference(v.Order.String()) },
				audit.Internal,
				audit.AsToken,
			),
			audit.AttemptFields(
				audit.AttemptValue(
					"invocation",
					func(v ApprovalStart) audit.Reference { return v.Invocation },
					audit.ReferenceText(),
					audit.Internal,
				),
			),
		),
		Checkpoints: audit.AttemptCheckpoints(
			audit.CheckpointCodes("validated", "mutation_staged"),
			func(v ApprovalCheckpoint) audit.AttemptCheckpointCode { return v.Code },
		),
		Finish: audit.AttemptFinish(
			audit.AttemptReasons(
				audit.AttemptReasonsFor(audit.AttemptFailedTransition, audit.Reasons("storage.rolled_back")),
				audit.AttemptReasonsFor(audit.AttemptCancelledTransition, audit.Reasons("caller.cancelled")),
				audit.AttemptReasonsFor(audit.AttemptOutcomeUnknownTransition, audit.Reasons("settlement.unconfirmed")),
			),
			audit.AttemptFinishFields(
				audit.AttemptFieldsFor(
					audit.AttemptSucceededTransition,
					audit.AttemptProtected(
						"receipt",
						func(v ApprovalFinish) audit.Reference { return v.Receipt },
						audit.ReferenceText(),
						audit.Internal,
					),
				),
			),
		),
	},
)

func approveWithAttempt(
	ctx context.Context,
	attempts *audit.Attempts,
	orderID OrderID,
	invocation audit.Reference,
	requestID audit.IdempotencyKey,
) error {
	execution, err := ApproveOrderAttempt.Run(
		ctx,
		attempts,
		ApprovalStart{Order: orderID, Invocation: invocation},
		audit.AttemptRunSpec{IdempotencyKey: requestID},
		func(
			runCtx context.Context,
			progress *audit.AttemptProgress[ApprovalCheckpoint],
		) (audit.AttemptCompletion[ApprovalFinish], error) {
			_ = progress
			receipt, appErr := executeOrderApproval(runCtx, orderID)
			if appErr != nil {
				return audit.AttemptFailed[ApprovalFinish]("storage.rolled_back", ApprovalFinish{}), appErr
			}
			return audit.AttemptSucceeded(ApprovalFinish{Receipt: receipt}), nil
		},
	)
	if err != nil {
		return err
	}
	return execution.ApplicationError()
}
```

`ApproveOrderAttempt.History(history).Transitions(ctx, query)` is the bounded
AttemptType-wide investigative door, and `.Target(ctx, target, query)` is available
only because this declaration has an equality-searchable target. Both return
verified transition items in an immutable paged cohort and make no complete-state
claim. `.Operation(ctx, operationID, query)` is a different all-or-nothing query:
it obtains the per-attempt projection from `HistoryConfig.Attempts`, exact-inspects
the complete bounded chain, and returns one `AttemptStatus` or no status. Resume,
ResolveUnknown, and Abandon additionally require an `AttemptAccessSpec` with an
explicit Purpose and Role; both are frozen into authorization and success/denial
evidence.

The application authority receives a copied, policy-filtered logical view of the
same frozen requester context that audit later protects for evidence. Its allow
or deny constructor is origin-bound to that exact request; a decision created for
another requester is rejected. The authority and every downstream collaborator
receive a value-free context that preserves Deadline, Done, Err, and normalized
Cause only, so a
second context lookup cannot authorize one principal while evidence names
another. Audit validates and seals the grant; callers cannot manufacture one by
constructing a public struct. Denial evidence is attempted only after the
application-owned limiter admits the bounded attempt; nil or refusal still
returns the original denial and performs no append.

## 3. Observable happy cases

### Declarations and capture

- **AH-001:** A valid resource declaration seals on first use and is safe for concurrent use.
- **AH-002:** `Define` panics on an invalid declaration; `TryDefine` returns the same error.
- **AH-003:** A valid event declaration with versioned semantic goldens and closed
   outcome/reason sets behaves the same way through `Declare` and `TryDeclare`.
- **AH-004:** A named catalog manifest built from declarations, policy fingerprints,
   code inventories, and codec fingerprints is stable across process and map order;
   a retained lineage rejects gaps, forks, parent mismatch, and generation reuse.
- **AH-005:** Built-in `Text`, `Bool`, `Int64`, `Uint64`, `DecimalText`, `Bytes`,
   `ReferenceText`, `UUIDReference`, `Duration`, and UTC `Time` codecs are canonical and have the
   exact public signatures frozen by the implementation contract.
- **AH-006:** A custom CodecEngine wrapped by DefineCodec can retain readers and
   accepted/rejected fixtures for older versions and upcast into the current typed
   value across a restart and catalog-generation change. The engine is an explicit
   trusted collaborator for termination, complexity, determinism, concurrency, and
   unsampled behavior; the kernel bounds its returned bytes before any later crypto
   or store work.
- **AH-007:** A create records absent-to-value states for declared fields.
- **AH-008:** An existing-head update records only fields whose canonical states
    changed. A first audited update of a pre-existing row additionally records a
    marked full after-state for every reconstructable field while keeping Changes
    limited to the fields that actually changed.
- **AH-009:** A delete records value-to-absent states before the row becomes unreachable.
- **AH-010:** A restore records absent-to-value states from the restored committed row.
- **AH-011:** A redacted field records only whether its state changed.
- **AH-012:** A protected field reaches the store only as a bounded randomized envelope with
    algorithm/key identity and kernel-authenticated coordinates. Stable keyed
    logical commitment and exact stored-envelope commitment are distinct.
- **AH-013:** A tokenized field reaches the store only as a keyed one-way token.
- **AH-014:** A reconstructable field round-trips through every retained accepted
    codec fixture and rejects every declared malformed fixture.

### Context and identity

- **AH-015:** One actor may be a human, workload, service, system, or external principal.
- **AH-016:** Delegation records initiator, effective actor, and on-behalf-of hops in order.
- **AH-017:** Every identity-bearing context field carries its provenance: server-derived,
    verified, forwarded, or client-supplied.
- **AH-018:** Policy names the exact accepted provenance set per action and refuses every
    other label; no implicit strength order exists.
- **AH-019:** Service, deployment, scope, operation, correlation, causation, and trace
    references are bounded and independently optional according to policy.
- **AH-020:** An outcome or reason is a stable action-scoped code from the exact
    manifest declaration. Optional narrative, when enabled, is a separately
    classified protected field rather than free metadata.
- **AH-021:** Every retained actor, scope, service, deployment, client, operation,
    correlation, causation, trace, and source fact is individually declared with
    requiredness, allowed provenance set, classification, and storage mode.
- **AH-022:** Undeclared optional context facts are discarded before semantic commitment or
    protection; a required missing fact or disallowed provenance refuses before
    Writer I/O. Provenance labels are never ordered by enum value.

### Writes and transactions

- **AH-023:** A standalone manual event is one atomic store append.
- **AH-024:** An audited CRUD mutation and its evidence commit together on one proven source.
    A transaction-bound Writer returns one execution object that privately captures
    the exact frozen executor and exposes only the bound audit operations; it needs
    no authority registry or cleanup callback to find the caller transaction.
- **AH-025:** A caller-owned transaction can group several items under one revision.
- **AH-026:** A rollback removes both entity writes and audit writes.
- **AH-027:** The same idempotency key and stable keyed logical digest returns the original
    stored receipt even when a fresh randomized envelope differs, provided the
    call is standalone or the original is staged in the same exact authority. A
    legal-hold replay returns its original signed disposition and projection even
    after later transitions changed current state; state-derived CAS intent is not
    keyed logical identity.
- **AH-028:** The same idempotency key with different semantics, or the same revision ID
    with different full content, is a conflict.
- **AH-029:** Store failure classification determines whether the caller knows the append
   was not written or must reconcile an unknown outcome.
- **AH-030:** A certainly-not-written or unconfirmed result carries an opaque exact retry
   token; retry executes only the frozen append and reruns no application code.
   A token without a caller idempotency key replays by the same revision identity
   and full integrity content. A token tied to a caller transaction is
   reconciliation-only: it can
   look up visibility but can never retry an audit append separately from the
   business mutation.
- **AH-031:** Required evidence failure fails the unit of work; diagnostic observer failure
    never does.
- **AH-032:** A declared best-effort event is visibly marked non-authoritative and cannot be
    used for a policy that requires durable evidence.
- **AH-033:** Concurrent staging produces the same canonical item order for the same item
   multiset regardless of goroutine scheduling.
- **AH-034:** Consecutive entity revisions bind a per-subject predecessor digest. The store
   validates and advances each head in the same transaction; replay advances it
   zero additional times. Genesis derives one stable pseudorandom chain identity
    from the resource, exact evidence-scope commitment, and active keyed subject
    commitment; concurrent creators
   therefore race on the same identity, and retained aliases preserve it across
   key rotation.
- **AH-035:** Known entity subjects are reserved before grouped mutation I/O. Any failure
    after a required mutation starts permanently poisons the outer group; a
    swallowed adapter error cannot permit sealing or append.
- **AH-036:** Audited CRUD in a caller-owned transaction requires this Recorder's active
    group. Its pending GroupResult exposes a ReconcileKey before the transaction
    owner commits; before commit Lookup sees no staged row, and after an uncertain
    commit the caller can check committed visibility without retrying either write.

### Reads and reconstruction

- **AH-037:** A subject, declared searchable event target, or operation query returns revisions in the
    normalized requested time-axis/direction order with the canonical revision tie-breaker. The typed event history shell binds the declared
    resource, action, target policy, and protected lookup aliases automatically.
- **AH-038:** A cursor resumes its frozen origin cohort without omission or duplication even while
    normal or backdated revisions commit concurrently. Every
    continuation reauthorizes the same frozen requester and query, intersects
    current authority with both the encrypted original and prior-effective grant
    ceilings, freezes the new effective intersection, honors
    revocation, and never outlives the minimum of its configured hard-bounded
    lifetime, original grant expiry, narrowed current grant expiry, and prior
    continuation expiry.
- **AH-039:** Authorization narrows values before they are handed to the caller. A decision
    is valid only for the exact origin request and requester-context commitment
    from which its constructor was called.
- **AH-040:** Reader projections expose public/internal/personal fields only as the grant permits.
- **AH-041:** A reconstructable projection is correct at create, after updates, before delete,
   after restore, at the authenticated current head, and at a historical boundary.
   A field introduced after the chain's last full anchor is explicitly Unobserved
   until its first authenticated assignment; an unrelated update cannot invent it.
- **AH-042:** A correction links to the original item and appends a new assertion. Its
    authorization request binds a value-free digest plus the complete proposed
    resource, action, target identity, field set, modes, and classifications; the
    sealed decision cannot authorize a substituted draft. After authenticated
    lookup, a protected original target is decoded or revealed under its
    record-era description and must equal the proposed logical target. An event
    correction uses the same stable resource/action declaration across retained
    generations. A current redacted proposal refuses before authority or store
    I/O; an exact original whose record-era target is redacted refuses after its
    authenticated lookup with the same non-disclosing mismatch result. A redacted
    value is never guessed or inherited from the original.
- **AH-043:** Audit-history access and lifecycle operations create their own bounded evidence.
    Separate exhaustive typed request/grant/result digest trios bind history and
    control outcomes, including reconstruction boundaries, verification statuses,
    retention AsOf, continuation and fence presence. Successful pages and reports
    are withheld until that evidence is independently committed; caller
    transactions and groups are not joined.
- **AH-044:** Historical verification uses the exact retained manifest while current access
   policy decides what may be returned. Missing catalog history is explicit and
   never becomes partial data. The keyed SemanticDigest is a write-time equality
   commitment and is not recomputed by History; historical semantic keys are not
   reader requirements. Exact-envelope verification and a required record-era
   signature authenticate stored content.

### Lifecycle and integrity

- **AH-045:** Every revision has distinct keyed logical, exact stored-envelope, and integrity
    digests over their declared inputs. The integrity digest binds the first two.
- **AH-046:** A configured signer adds an algorithm and key identifier and verification can
   report valid, invalid, missing-key, and unsupported-algorithm separately.
- **AH-047:** Database privileges and triggers make ordinary application roles insert/read
   only according to their role, with UPDATE/TRUNCATE denied.
- **AH-048:** Repeating a purge plan against the same exact inventory fence returns the same
   eligible revision set and fence token. Every candidate carries the canonical
   complete resource set of its whole revision.
- **AH-049:** Every legal-hold membership has a stable HoldID and matter. A revision remains
    ineligible while any membership is active; releasing one never clears another.
    Active and retained HoldID commitments resolve to one private stable
    HoldIdentity, so key rotation cannot change the authenticated set.
- **AH-050:** Inventory fences are private authenticated bounded tokens tied to durable log,
    durable BackingID, catalog lineage, query, cohort, eligibility facts, complete
    resource sets, and active-hold epochs. Process-local Backing identity is never
    serialized.
- **AH-051:** Inventory and verification distinguish incomplete evidence, missing keys,
    unsupported algorithms, invalid seals, and backend failure.
- **AH-052:** Retention is a catalog-declared record-era rule anchored to integrity-bound
    observed capture time. Control reads its trusted clock exactly once, binds that
    normalized AsOf into authorization, selection, query, and fence, and accepts only
    a byte-exact store echo. Calendar anniversaries, leap days, month-end clamping, the inclusive
    cutoff instant, Forever, and hold precedence are deterministic across restart.
- **AH-053:** Exact hold, correction, dispute, and explicit verification targets are read
    through a least-privilege exact seam already narrowed by the sealed grant; a
    store can implement it outside package audit without a broad scan.
- **AH-054:** Each hold command is authorized for the exact revision, keyed HoldID
    commitment, command kind, and keyed matter commitment or explicit release absence.
    It derives one signed candidate from the complete authenticated prior transition
    chain. A dedicated persisted hold-transition wire arm binds command, HoldID,
    stable private HoldIdentity, revision, matter presence/representation, and
    complete expected/result state. The active-set digest is the fixed-domain
    digest of the canonical unique active HoldIdentity set for that exact log and
    revision.
    The store serializes membership state with append and CASes every expected
    projection component, so the returned disposition and epoch projection never
    come from a racy pre-read or an unsigned guess.
- **AH-055:** Reconciliation lookup uses a value-free committed snapshot and returns the
    original persisted header after restart. It cannot see a staged row through
    the caller's transaction context.
- **AH-056:** The hidden Gate evaluates each Save or Update payload and caller option
    exactly once, then gives only its private receiver the copied narrowing fence.
    The receiver revalidates the exact ID, scope, relation, snapshot, and victim
    coordinates in the proven root transaction before the business effect, so no
    public wrapper can replace or replay them between security and audit.
- **AH-057:** The advertised root composition compiles from an external package using every
    built-in codec, custom CodecEngine/DefineCodec contract, semantic/code-set
    helper, HMAC helper, and AES-256-GCM protection/history-cursor/control-token
    keyring without an implementation-specific constructor.
- **AH-058:** A public memory-store constructor, transaction fixture, conformance factory,
    report, and application-policy proxy can exercise a complete setup without a
    database; the same Recorder-facing code can use the PostgreSQL constructor and
    explicit schema lifecycle without changing audit policy.
- **AH-059:** Aborting, panicking, or failing before Append leaves no transaction capability
    registered in a Writer. Only the active Recorder frame retains the store-owned
    execution object; dropping that frame drops the sole capability reference.
- **AH-060:** Two scoped references with the same value and different scope namespaces remain
    different through authorization, grants, tokens, cursors, fences, and
    revocation. When history or lifecycle is declared, its requester scope is
    required and every present active or retained evidence scope is stored in an
    equality-searchable mode; an absent optional scope never becomes a wildcard.
- **AH-061:** A lifecycle operation over a mixed-resource revision succeeds only when the
    sealed grant contains every canonical resource, item action, and classification, contains
    every present exact ScopedReference, and covers every subject or searchable-
    target coordinate through an exact logical selector or an explicit same-
    declaration `all` selector. An empty selector set matches only a summary that
    itself has zero such coordinates and is never a wildcard. Reports and
    private fences preserve one authenticated canonical summary.
- **AH-062:** Rebuilding a store over a new process handle but the same persisted BackingID,
    LogID, catalog set, and retained token key resumes a cursor or fence correctly.
- **AH-063:** A returned denied history/control error exposes one safe denial-evidence state:
    not configured, suppressed, limiter failed, not written, unconfirmed, or
    committed, without exposing the request, limiter error, receipt, retry token,
    or stored identifiers. A limiter panic returns no denial error or state: cleanup
    runs, no append or grant occurs, and the identical panic is re-raised.
- **AH-064:** A faults probe may use one framework-owned savepoint wholly inside the already
    admitted root mutation guard. It releases or rolls back before capture and
    staging, while the outer root remains the sole atomic settlement authority.
- **AH-065:** Manual-event outcomes and reasons and application-authored control reasons
    come only from immutable action-scoped declared code sets in the record-era
    manifest. Narrative is a separately classified protected field, and
    verification/internal outcomes use closed package types.
- **AH-066:** Every executable resource/event policy has a nonzero semantic version and
    committed named logical-output fingerprints; every codec has constructor-run
    accepted/rejected wire fixtures and a library-computed fingerprint. Application
    golden checks exercise typed samples without retaining them in production
    declarations.
- **AH-067:** Time reconstruction first verifies a store-asserted subject head's
    signed revision and leaf through genesis, then selects only a causally closed
    predecessor prefix whose cumulative maximum integrity-bound ObservedAt is at or
    before the request. A descendant timestamp at or before the request behind an
    excluded later predecessor returns temporal ambiguity and no projection.
    Monotone tampering with unsigned RecordedAt or backend positions and omission of
    a signed suffix relative to the asserted head cannot change the answer. A missing
    head can never prove authenticated absence and returns temporal ambiguity with no
    projection.
- **AH-068:** A hold-enabled control policy declares the exact matter codec,
    classification, and protected mode. Every transition binds authenticated prior
    and resulting membership/count/epoch/set/head state plus the stable private
    HoldIdentity, and one full-state CAS yields the returned projection.
- **AH-069:** Catalog installation and activation are a separate deployment
    workflow over a concrete deployment handle. Each mutation carries an independently
    authorized deployment-ledger reference; a bounded immutable mutation query proves
    the persisted install/activation kind, catalog, expected parent, and exact reference
    after reopen. A mutation that would make the complete log exceed its count or byte
    ceiling refuses atomically before persistence, so successful state is always
    queryable. Ordered direct-child activation completes before verification. The
    separate concrete runtime handle does not implement CatalogAdmin, including through
    its dynamic method set.
- **AH-070:** The sole `auditcrud.Secured` terminal supports no-ID Save, assigned-ID
    Save, Update, Delete, and Restore through its hidden Gate and private audited
    receiver. No-ID Save records an atomic create; assigned-ID Save truthfully
    records create, change, or first-touch baseline from the fenced previous state.
    The terminal advertises no `Next`, unsecured audit entry, write-only mutation,
    bulk mutation, or public scoped effect.
- **AH-071:** Access and control request/grant/result plus hold- and denial-request
    digests are typed, keyed, domain-separated commitments to exhaustive frozen
    input views. Requests include the keyed commitment to the complete admitted
    requester context, target kind/presence and every present target coordinate,
    purpose, role, scope, query class, action/field/context projections, time axis,
    bounds, direction, change/code predicates, the complete logical equality-
    selector conjunction, and limits, reconstruction or ordered
    comparison boundaries/work budgets, action-scoped control reason, hold request,
    continuation presence and exact typed cursor commitment, and the origin request/
    grant commitments. Five disjoint access-result arms bind page, reconstruction,
    comparison, exact revision, or exact item identity, including authenticated
    envelope/integrity contributors and the explicitly unsigned returned RecordedAt.
    Results also bind reconstruction head/leaf, difference status, control statuses,
    retention AsOf, fences, and the complete returned hold projection. Results and
    denials likewise enumerate every member. Low-entropy subjects and targets do not
    become public dictionary oracles. The injected SemanticDigester is an explicit
    trusted keyed-PRF, stable-output, termination, and complexity collaborator.
    Control selection and the exact encrypted inventory-fence envelope/decoded
    claim have their own acyclic keyed domains rather than unexplained hashes.
    Store-query and result commitments separately bind the exact bounded internal
    storage-alias expansion derived only after authorization.
- **AH-072:** A value-free downstream context preserves deadline, Done, and Err but
    normalizes context.Cause to the public cancellation sentinel. After the sole
    resolver returns, cancellation wins over its error and discards its diagnostic
    cause before CauseOf can retain it. A caller-supplied secret cause object never
    reaches any later audit-injected collaborator or public diagnostic door;
    ContextResolver is the one explicit injected trusted consumer of the original
    context and can observe it. Application callbacks deliberately retain their
    caller context and transaction bindings and remain application trust boundaries.
- **AH-073:** Outcome and reason codes are manifest-public protocol labels, returned
    only with an authorized containing item. Grammar and record-era membership are
    kernel-enforced; semantic non-sensitivity and narrative-free wording are explicit
    declaration-author and deployment-review obligations exercised by application
    canaries. Sensitive narrative or identifiers require separately classified fields.
- **AH-074:** One tracked normalized trace registry is the source of every AU-to-
    requirement, requirement-to-obligation, mutant, section, package, and reserved-test
    edge. Every obligation has one independent activation section; requirement
    tests and implementation sections are never falsely multiplied. The section-aware executable parses `go test -json` and requires a non-skipped
    passing event for every active reserved test as well as its structural test.
    Exact semantic records cover every AU/AH/AE/AI/AT/AM/PN body. Deleting or
    renaming the executable, registry, semantic body, test, or either edge is a
    failing checkpoint; no duplicated reverse table can drift.
- **AH-075:** Entity continuity commits exact scope presence and both
    ScopedReference components before subject aliases are derived. Reservations,
    head lookup, genesis, rotation, grouping, and reconstruction keep equal local
    IDs in different scope namespaces on different chains.
- **AH-076:** Resource-wide entity history, event-type history, a typed declared event index, initiating/effective/
    any actor-hop history, each declared context-coordinate history, and exact
    operation or operation-type history, plus revision/item inspection are
    separate sealed query classes. They reuse one
    authorization, retained-catalog authenticity, privacy, and committed access-
    evidence protocol without exposing a generic property language or store token.
    Exact inspection supplies nonempty requested resource/action/classification
    ceilings before authority and lookup; unknown row contents cannot mint scope.
- **AH-077:** A query has deterministic least-privilege zero defaults, explicit
    None/All/Only field and context projections, exact open/closed time windows,
    stable forward/reverse order, typed any/all/not changed-field predicates over
    authenticated field names, and record-era
    event outcome/reason matching. Occurred time is event-item-only; recorded time
    is explicitly unsigned operational metadata.
- **AH-078:** The first page freezes a bounded immutable store-side cohort. Every
    continuation reuses its exact snapshot, sort, expiry, position, and cumulative
    page/revision/byte progress, so concurrent normal or backdated appends never
    appear mid-walk and grant ceilings cannot reset per page.
- **AH-079:** One typed comparison resolves two ordered boundaries on one verified
    entity chain under one combined budget and one authorization/evidence unit.
    Known values and absence compare exactly; hidden, destroyed, undecodable,
    incomplete, or unprojected endpoints yield an indeterminate status.
- **AH-080:** A hard-delete entity revision terminally closes the stable scoped-
    subject chain. A later database row with the same active or retained identity
    cannot become another genesis; soft delete remains restorable and nonterminal.
- **AH-081:** When audit is enabled over a row with no prior entity head, its first
    admitted changed mutation creates a signed full-state genesis anchor whose
    action remains entity.changed, whose observed time is only that mutation's
    capture time, and whose complete reconstructable after-state is protected by
    the current catalog. It never fabricates creation, prior history, or unchanged
    field changes.

- **AH-082:** A valid typed AttemptPolicy freezes its sealed OperationType, start/
    checkpoint/finish schemas, code inventories, lifetime, checkpoint/state
    ceilings, continuity owner facts, consequence, context, and semantic goldens
    into the catalog manifest. Its context requires one non-generated stable
    OperationFact so restart resolves the same OperationID without a free override.
- **AH-083:** A Required Begin returns an active typed run only after one immutable
    Started transition is independently Committed with Inserted disposition. The
    higher-level Required Run gate invokes its protected callback only after that
    settlement. A Replayed start returns its original receipt, no handle, and zero
    callback invocations; bypassing the gate is explicit outside-policy
    application composition.
- **AH-084:** Each declared checkpoint appends one signed transition, advances the
    authenticated sequence/head and bounded checkpoint count exactly once, and
    leaves the attempt Open.
- **AH-085:** A truthful standalone Succeeded, Failed, Cancelled, or OutcomeUnknown
    transition closes or marks an open attempt exactly once under CAS and returns
    its signed projection after commit.
- **AH-086:** A terminal staged through an attempt-bound group and its admitted
    business/entity mutation share one exact transaction authority, append once,
    and commit or roll back together.
- **AH-087:** Retrying an identical begin or transition replays its complete
    original envelope/projection while that evidence is retained; the same key or
    revision with different phase semantics conflicts and advances no head. A
    compatible catalog generation with the same CatalogID,
    AttemptPolicyFingerprint, and derived
    AttemptReplayFingerprint preserves that replay even though its active
    CatalogRef, lineage, deployment, or ephemeral request context differs; the
    original record-era catalog remains authenticated.
- **AH-088:** Known external settlement uncertainty may append OutcomeUnknown.
    Potentially committed transactional terminal evidence instead returns its
    ReconcileKey and cannot be replaced by Failed or another standalone terminal.
    A later exact authorized resolution may move Uncertain to one truthful
    Succeeded, Failed, or Cancelled terminal under the same CAS.
- **AH-089:** After restart, exact OperationID, AttemptType, scope, durable store,
    and current authorization resume a run only after the complete signed
    start-to-head transition chain is exact-inspected and recomputed.
- **AH-090:** Once the declared deadline is reached, an explicitly authorized
    Abandon command may close an Open or Uncertain attempt; a concurrent genuine
    terminal and abandonment race through one CAS so exactly one wins.
- **AH-091:** Exact operation history exposes authorized Started, checkpoints, and
    terminal/uncertain transitions under record-era verification, including a
    derived duration only when signed start and terminal times make it knowable.
- **AH-092:** Every EventPolicy and AttemptPolicy seals exactly one declaration-level
    target arm: a typed target with its codec/classification/storage description, or
    explicit target absence. That presence bit participates in the manifest,
    semantic and replay fingerprints, stored item, per-chain projection, integrity inputs, access
    request/grant/result evidence, and idempotent replay. A targetless event or
    attempt records and reads normally by its other typed doors without inventing
    an empty, resource-derived, operation-derived, or sentinel Reference. A present
    attempt target is always equality-searchable; a present non-searchable event
    target remains recordable but contributes no target-history capability.
- **AH-093:** A declared typed EntityIndex seals one exact entity schema field,
    stable index name, codec, classification, and equality-searchable plaintext,
    tokenized, or indexed-protected storage mode. Capture authenticates its optional
    canonical Before and After values on every applicable entity item. Typed `BeforeEquals`,
    `AfterEquals`, and `EitherEquals` queries apply to that single item; an absent
    side does not equal any value. History derives active and retained storage
    aliases without exposing them to the authority or caller and post-validates the
    authenticated values before release.
- **AH-094:** `AnyOf` accepts one canonical duplicate-free value set of 1 through
    `MaxSelectorAlternatives` values for one origin-sealed equality selector.
    `AllOf` accepts 1 through `MaxQuerySelectors` as one bounded canonical
    duplicate-free set of origin-sealed actor, context, event-index, entity-index,
    or attempt-target selectors compatible with the bound typed history door. A
    returned item's item-level predicates cannot be distributed across siblings in
    a grouped revision. The normalized selector
    identities, sides, logical values, count/byte work ceilings, and conjunction
    participate unchanged or narrower in authorization. Only after allow does
    History derive bounded storage aliases; those remain internal and bind the
    StoreQuery, snapshot/cursor, post-validation, and committed access evidence.
- **AH-095:** Every successful entity projection exposes one authenticated
    `EntityLifecycleState`: EntityPresent, EntitySoftDeletedState, or terminal
    EntityHardDeletedState. Create, first-touch baseline, and ordinary change
    establish or preserve EntityPresent; soft-delete, restore, and hard-delete
    advance only through their declared legal
    transitions. Ordered comparison returns both endpoint lifecycle states and an
    Equal, Changed, or Indeterminate lifecycle difference under the same boundary,
    chain, budget, authorization, and evidence unit as field comparison.
- **AH-096:** An exact typed entity-neighbor query reports predecessor and successor
    independently as NeighborPresent with an authenticated adjacent revision,
    NeighborChainBoundary only when genesis or the verified current head proves no
    neighbor, or NeighborUnavailable when retained evidence, authorization,
    integrity, or the combined count/byte budget
    cannot prove either answer. Predecessor verification follows the signed direct
    link; successor verification closes the path from the asserted authenticated
    head to the requested revision. The pair has one sealed query class and one
    committed access-evidence result.
- **AH-097:** An AttemptType-wide query and, only for an equality-searchable present
    target, a typed attempt-target query return bounded immutable pages of verified
    transition items across matching chains. They expose no `AttemptStatus`, final
    state, or complete-chain assertion. Exact `AttemptHistory.Operation` remains a
    separate door that reads one per-attempt projection and returns a status only
    after every transition through its authenticated head fits and verifies under
    one bounded request.
- **AH-098:** Per-chain `AttemptLog` and global `AttemptTypeState` are distinct
    least-privilege store facets with distinct typed queries and results. Attempt
    orchestration receives both from the same Recorder origin; catalog activation
    reads only the authenticated global Unsettled counter certificate, while
    resume/status/retention inspect per-chain state and exact evidence. An
    `AttemptResult` exposes `ResultingState` only as `(AttemptState, true)` for an
    authenticated Committed settlement with Inserted or Replayed disposition;
    every not-written,
    unconfirmed, canceled, or pre-append refusal returns `(zero, false)`.
- **AH-099:** A History built for a catalog set containing an AttemptType requires
    an explicit same-origin `AttemptLog` dependency before it can expose attempt
    status or investigation. Resume, ResolveUnknown, and Abandon access requests
    carry nonzero Purpose and Role in their strict attempt branch; the sealed grant,
    keyed request/result commitments, and AttemptContinuationAuthorized or
    AttemptAccessDenied evidence bind that exact intent. Ordinary history branches
    keep the attempt branch zero.

## 4. Edge and hostile cases

- **AE-001:** Semantic names outside the lowercase ASCII dot-segment grammar, including
   empty, overlong, duplicate, or confusable Unicode spellings, are refused.
   Identity references are bounded, valid UTF-8, NUL/control-free opaque bytes;
   the kernel never normalizes or interprets them.
- **AE-002:** A resource with no owner, purpose, retention class, subject mapper, action, or
   field policy is refused.
- **AE-003:** A field name that does not resolve to the model or resolves ambiguously is refused.
- **AE-004:** An exact CRUD model field tagged secret cannot be declared for reversible or
   plaintext capture, even under another evidence name. Entity extraction uses
   the resolved metadata field, never a caller getter.
- **AE-005:** A personal field cannot use plaintext storage unless deployment admission names
   a stable reviewer-visible reason; metadata-secret entity fields and explicitly
   Secret event fields are never retained reversibly.
- **AE-006:** No generic door accepts a whole-model JSON value, metadata map, request body,
   raw error, header bag, SQL, stack, signed URL, or opaque object bytes. Manual
   event extractors are trusted policy code: credential semantics cannot be
   inferred from an ordinary string, so application conformance must exercise
   token/password canaries and protection policy.
- **AE-007:** A CodecEngine that is nondeterministic, aliases mutable scratch memory,
   drops or changes an accepted/rejected fixture, panics during a constructor-run
   fixture, self-reports identity, or cannot read an old version is caught by
   DefineCodec or conformance checks. An unsampled runtime panic is a trusted-engine
   violation and is re-raised unchanged.
- **AE-008:** Mutating a model or payload after capture cannot change the pending record.
- **AE-009:** A subject mapper collision is found by the application's sample proxy; one
   declaration cannot make injectivity universally decidable.
- **AE-010:** Missing trusted context never falls back to anonymous, global scope, a previous
    request, or process state. Fixed system facts still come from an explicitly
    configured validated StaticContext; only OperationID has a declared
    kernel-generated mode.
- **AE-011:** Raw JWT claims, headers, baggage, and client fields cannot directly mint a
    verified actor or scope.
- **AE-012:** A forwarded identity is never silently upgraded to verified.
    Provenance labels are an explicit allowed set, not an ordered trust scale;
    client-supplied never passes merely because its enum value is larger.
- **AE-013:** A denied action does not create successful entity evidence. Optional built-in
    history/control denial evidence has its own catalog action, retention, and
    consequence and is attempted only after an application-owned DenialLimiter
    admits the origin-bound, value-free attempt.
- **AE-014:** The framework never claims to bound an attempt flood by itself. With no
    DenialLimiter, limiter refusal, or limiter failure, denial remains
    non-disclosing and no evidence allocation or append occurs. Its safe public
    state distinguishes not configured, suppressed, and a returned limiter failure
    without exposing the cause. A limiter panic performs no append and is re-panicked
    unchanged rather than converted into a denial state.
- **AE-015:** Cancellation before any statement remains cancellation. A lost response after
    append may be unconfirmed and must not be relabeled cancellation.
- **AE-016:** A panic from the wrapped repository rolls back and is re-panicked unchanged.
- **AE-017:** A panic from an observer is isolated and counted as diagnostic failure.
- **AE-018:** A panic from a store is not recovered into a guessed outcome.
- **AE-019:** A mismatched datasource, non-transaction executor, hidden transaction, or
    incomparable source identity refuses before mutation I/O.
    Caller-visible database/sql savepoints, nested transaction executors, and raw
    transaction wrappers with unstated scope provenance are explicitly refused in
    release 1. The only exception is a framework-owned faults savepoint created and
    finalized wholly inside an already admitted root mutation guard; it never
    carries audit staging or settlement. Out-of-band raw SQL savepoints are not
    detectable and are outside the atomic claim; presenting their parent context
    is a caller contract violation, never something the framework claims to prove.
- **AE-020:** A decorator between audit and the repository that hides an executable effect
    is not walked through.
- **AE-021:** Options, predicates, callbacks, readers, and payload getters execute
    exactly once. Update appends the Gate's scope, relation, and snapshot constraints
    after the materialized caller options, so a hostile assigning option cannot
    erase security narrowing. Gate authorizes Restore before an empty-ID shortcut,
    and `port.DefaultService.RestoreMany` forwards even an empty set to the sealed
    repository boundary; neither entry can bypass policy, transaction, or evidence
    work.
- **AE-022:** No-ID Save, assigned-ID Save, Update, Delete, and Restore reaching the
    private receiver through its hidden Gate preserve the copied security fence
    and exact victim set; audit never reloads a row outside that closed path to
    learn more.
- **AE-023:** `SaveOnly`, `SaveAll`, `UpdateAll`, `DeleteAll`, and unsupported
    upsert-like or scoped effects are refused by the sealed terminal before a Gate
    callback, pre-read, root transaction, mutation, or append. Assigned-ID `Save`
    remains supported only through the terminal's hidden scoped fence and must
    prove create versus truthful change or first-touch baseline.
- **AE-024:** No-op update records no entity revision and does not advance a reconstructable state.
- **AE-025:** Bulk, raw SQL, ORM hooks, update-column shortcuts, M2M changes, database cascades,
    triggers, and CDC each have an explicit coverage verdict. Unsupported paths fail
    closed when policy claims they are covered.
- **AE-026:** A count alone never becomes fabricated per-row evidence.
- **AE-027:** An auto-generated primary key is captured from the committed returned row, not
    from the input model before insertion.
- **AE-028:** A soft delete and hard delete are distinct declared actions.
- **AE-029:** A query with an unauthorized scope, subject, field, role, purpose, time range,
    action, target, or actor returns no partial data. Replacing only the namespace
    component of a ScopedReference is a different unauthorized scope.
- **AE-030:** A cursor from another store, requester, query, purpose, grant ceiling, catalog
    lineage, or expired key is refused rather than treated as the beginning. A
    current decision cannot widen its encrypted original or prior-effective
    ceiling, reset cumulative progress, extend its hard expiry, ignore revocation,
    or reuse it for another principal. A narrow-then-rewiden sequence remains
    narrowed. Zero, negative,
    over-hard, overflowing, or already-expired cursor lifetimes refuse; a shorter
    grant expiry wins.
- **AE-031:** Query limits, time ranges, subjects, actions, result bytes, cursor bytes,
    inventory candidates, fence bytes, and catalog generations have hard ceilings.
- **AE-032:** A malicious store returning foreign-scope, foreign-subject, duplicate, misordered,
    oversized, malformed, or digest-invalid rows is rejected by the kernel.
- **AE-033:** Unknown codec versions or record-era codec-fingerprint mismatches expose
    neither raw bytes nor a zero value.
- **AE-034:** Missing decryption, signing, cursor, and fence keys are distinct from corrupt
    evidence. A historical semantic-digest key is deliberately not required:
    readers do not recompute the write-time idempotency commitment.
- **AE-035:** An authenticated future destruction assertion reports a distinct Destroyed
    state; merely crossing the age cutoff does not hide retained or held evidence.
- **AE-036:** Hold placement/release, correction, dispute, integrity verification, inventory,
    and purge planning are authorized and audited themselves. Two active HoldIDs
    on one revision remain independent, and a released membership cannot be
    resurrected. Correction authorization binds the proposed assertion; changing
    its action, target, resource, fields, modes, classifications, or digest after
    the decision refuses before append.
- **AE-037:** Retention never rewrites a surviving revision into a different semantic record.
- **AE-038:** Restored backups re-run schema, catalog-lineage, durable-log identity, hold,
    key, and integrity checks before reads.
- **AE-039:** Trace/log/metric attributes contain no actor, scope, subject, reason narrative,
    before/after value, filter, cursor, SQL, or raw error.
- **AE-040:** Telemetry disabled, sampled out, overflowing, timing out, or crashing changes no
    authoritative audit result.
- **AE-041:** Different databases never claim one atomic revision; asynchronous links state
   at-least-once and unknown-outcome semantics.
- **AE-042:** Constructors, declarations, schema descriptors, and module descriptors start no
   goroutine, migration, exporter, purge, relay, or ticker.
- **AE-043:** A reconstruction request has explicit revision, page, and byte budgets. If
    they are exhausted before a field is proven, that field is `BudgetExceeded`; it is never
    returned as known from a partial scan.
- **AE-044:** Generic Record, Stage, and Capture accept only the manual-event Draft
    type. Resource capture returns a distinct opaque EntityDraft with no public
    append or standalone head door; entity evidence runs only inside a
    recorder-supervised one-use mutation guard, while
    access/control/lifecycle evidence is admitted only through its authorized
    owner service and committed-evidence barrier.
- **AE-045:** One revision cannot contain two entity items for the same resource and
    subject. Known subjects are reserved before mutation; a generated-subject
    collision or any other post-mutation capture failure poisons the outer group.
- **AE-046:** Randomized ciphertext for identical logical evidence can differ without
    changing keyed idempotent equality. Moving or changing any envelope byte,
    algorithm, key, field, item, or revision coordinate breaks integrity.
- **AE-047:** A keyed retry returns the original stored envelope and receipt; an exact retry
    invokes no context resolver, semantic digester, protector, signer, or clock.
- **AE-048:** Catalog activation refuses gaps, forks, parent mismatch, stale writers,
    generation reuse, incompatible wire meaning, and undeclared privacy weakening.
- **AE-049:** Historical reads never use an old policy as present authorization. Removed
    fields are unavailable unless the current catalog explicitly admits a stable
    historical identity and upcast. A reconstructable field introduced after a
    baseline remains Unobserved across unrelated deltas until a full anchor or its
    first actual assignment; removal plus HistoricalReconstruct preserves that
    state instead of fabricating absence.
- **AE-050:** A page, inventory, verification report, or purge plan is never returned with
    rollback-capable access evidence. Not-written, unconfirmed, or malicious
    caller-transaction settlement discards the prepared result.
- **AE-051:** A fence bit flip, wrong key, durable log, backing, lineage, query, cohort,
    hold epoch, expiry, or oversized encoding refuses before lifecycle I/O. The
    serialized identity is durable BackingID, never a pointer-derived local Backing.
- **AE-052:** Equal or regressing signed observed times retain predecessor order.
    For `10 -> 30 -> 20` at `25`, the descendant cannot cross its excluded predecessor:
    reconstruction returns the declared temporal-ambiguity outcome and no
    projection, never future-observed state. Unsigned store times never select the
    entity chain.
- **AE-053:** A committed duplicate found while a different live caller transaction is
    expected refuses and poisons that unit. It never lets a second business
    mutation borrow the first transaction's audit evidence.
- **AE-054:** Retention rule reuse with another meaning, zero/negative or overflowing
    periods, caller- or store-selected future AsOf time, changed result echo, and
    cutoff races refuse. The one kernel-clock instant and one store snapshot identifier
    remain distinct and produce no partial eligibility.
- **AE-055:** An access or control decision constructed for request A is rejected when
    returned for request B, even if its visible constraints are byte-identical.
    Resolver and authority each run once; stores and private evidence receive no
    caller context values.
- **AE-056:** Every equality-filtered tokenized scope, subject, client, or target requires a
    query token for every admitted retained generation/profile. One missing key
    rejects the whole query before Log I/O; protected-only and redacted identities
    are not searchable, while indexed-protected mode uses a token index plus a
    separately protected display value.
- **AE-057:** `auditcrud.Secured` rejects zero or typed-nil factory arguments and
    invalid metadata, catalog, source, Writer, or inner capabilities before
    callbacks or I/O. Its terminal exposes no `Next`, Gate, private receiver, or
    unsecured audit entry; `faults -> Secured` fails at bind time, while the sole
    supported order remains `Secured -> faults -> repository`.
- **AE-058:** Entity, idempotency, requester, and hold-matter alias sets are copied,
    domain-separated, description-complete, and atomically resolve to at most one
    stable object. Split aliases, duplicate descriptions, and concurrent first-use
    races fail closed rather than create parallel histories or hold identities.
- **AE-059:** A denied read never causes automatic evidence amplification. With no
    application limiter admission it returns the original non-disclosing denial
    and performs zero append calls; an admitted neighboring request may append
    bounded denial evidence but still cannot become permission.
- **AE-060:** An exact control store that omits, duplicates, reorders, substitutes, or
    over-sizes a requested revision/item—or returns a row outside the sealed
    resource, class, scope, subject, or catalog constraint—releases no target or
    report.
- **AE-061:** A hold store that accepts a stale or mismatched full-state CAS,
    substitutes expected/result membership, count, epoch, active set, or transition
    head, appends on conflict/not-found, advances a no-op/replay, skips an atomic
    keyed recheck, or returns a candidate other than the authenticated original
    keyed replay or exact inserted/revision replay is a contract failure and
    releases no success.
- **AE-062:** Lookup answers with caller-transaction visibility, a nonzero authority,
    staged data, a missing Found header, or a present AbsentNow header are rejected
    without a receipt.
- **AE-063:** A zero, copied, replayed, cross-recorder, wrong-kind, or substituted
    private bridge carrier, guard, specification, or batch refuses before
    transaction or model I/O. Application code has no public constructor, token,
    binder, or nameable receiver path for those values; deliberately binding a
    repository without `Secured` remains an explicit outside-policy application
    choice rather than falsely audited work.
- **AE-064:** A RetryToken has no serialization or parsing door and never survives as an
    unauthenticated execution capability. Restart recovery uses ReconcileKey and
    a rebuilt idempotent logical operation.
- **AE-065:** After the sole context-resolver call, a context-value canary at any store,
    authority, limiter, codec, crypto, retry, or evidence seam finds no caller
    value or caller-provided cancellation cause; only Deadline, Done, and normalized
    Err/Cause state remains.
- **AE-066:** Integrity invalidity, missing key, missing seal, unsupported profile,
    malformed evidence, incomplete evidence, unknown catalog, and verifier backend
    failure never collapse into one result or leak raw stored bytes.
- **AE-067:** A Writer that would need an authority-to-executor map, retains an executor or
    execution outside the object returned to the active frame, exposes its executor,
    swaps authority, or returns a nil/foreign execution fails conformance. Callback
    abort and pre-append failure leave no store-side registry or cleanup obligation.
- **AE-068:** `{scope: tenant, reference: acme}` never authorizes, resumes, or revokes
    `{scope: region, reference: acme}`; every intersection compares both bounded
    components byte-exactly. A protected-only or redacted ScopeFact in any
    generation needed by history or lifecycle refuses at catalog/lineage
    construction rather than becoming an unverifiable summary coordinate.
- **AE-069:** A mixed-resource revision cannot be inventoried, held, verified, or included in
    a purge plan under a grant that covers only one item. Omitting, duplicating, or
    relabeling any resource, item action, classification, scope, subject, or searchable target in
    a store candidate rejects the whole result. A grant for the same resource and
    action but another event target, or an implicit empty-list wildcard, also fails.
- **AE-070:** A correction decision for one immutable proposal cannot be replayed after
    substituting its event declaration, action, target, field set, mode,
    classification, protected commitment, or logical draft digest. A current
    redacted-target proposal refuses before authority and lookup; a redacted
    record-era original refuses after authenticated exact lookup without a
    distinct existence signal or append.
- **AE-071:** Restarting with a new pool pointer and the same durable BackingID succeeds;
    reusing cursor/fence bytes with another BackingID fails before store I/O, and
    neither errors nor tokens contain a rendered process identity.
- **AE-072:** Missing, short, duplicate, inactive, or description-mismatched HMAC keys refuse
    at construction. Built-in codecs and helpers copy keys and bytes and expose no
    secret through formatting or errors.
- **AE-073:** A conformance factory that claims a capability without its required hook,
    constructs no usable store, returns no verdict, or certifies no section fails
    rather than reporting green; unsupported remains NotCertified.
- **AE-074:** An event target whose storage policy is not equality-searchable cannot promise
    target history. Cross-event, cross-action, cross-resource, raw-versus-token,
    and retained-token target substitutions return no page.
- **AE-075:** A caller-supplied or escaped savepoint remains refused even when faults is in
    the chain. Removing, widening, or failing to finalize the single internal
    faults savepoint causes the root mutation/audit unit to fail and roll back.
- **AE-076:** Empty, malformed, duplicate, undeclared, or cross-action outcome/reason
    codes refuse before protection or Writer I/O. A historical row is checked against
    its authenticated record-era code set, never today's set. A syntactically valid
    sensitive-looking code demonstrates the honest trusted-author boundary and fails
    an application policy canary rather than an impossible semantic kernel inference.
- **AE-077:** A codec engine cannot self-report its fingerprint or omit an accepted/rejected
    fixture. Reusing a policy or codec version after golden drift is a lineage
    conflict. Function addresses, source paths, closure state, reflection, and
    binary hashes never enter identity; an unsampled behavior change is explicitly
    the trusted engine author's obligation to version and fixture, not a false
    runtime guarantee inferred from finite construction calls.
- **AE-078:** A hostile Log may monotonically alter every unsigned RecordedAt and
    backend position or omit a valid signed suffix while preserving returned bytes.
    AtObservedTime verifies the store-asserted head's signed revision and leaf through genesis
    and still resolves the same causally closed boundary or returns bounded unknown/
    temporal ambiguity; a missing head/history is likewise ambiguity, never a
    successful empty projection. It never accepts the forged time axis or truncated prefix.
- **AE-079:** Hold actions with a missing matter policy, plaintext/token-only/redacted
    matter, an incompatible codec, or a changed record-era matter fingerprint
    refuse before authority or store I/O. Protected matter never appears in a
    manifest, summary, error, or result as raw bytes.
- **AE-080:** False AlreadyActive/AlreadyReleased, false release, modified
    count/epoch/set/head, stale CAS, omitted transition, cross-target/HoldID/command
    or stable-HoldIdentity substitution, zero/duplicate identity in the set,
    rotation-dependent set digest, missing typed hold arm, generic-value
    substitution, or matter/request change after authorization releases no HoldResult. A legal authenticated
    neighbor returns the exact signed projection and a full RevisionRef for each transition.
- **AE-081:** A context canceled with a distinct secret-bearing cause exposes only
    context.Canceled or context.DeadlineExceeded at every downstream seam. Direct,
    wrapped, or joined resolver returns of that cause are discarded after resolution;
    the raw cause object/text is absent from CauseOf, values, errors, observations,
    and evidence.
- **AE-082:** The same resource and local subject under absent, tenant, and region
    scope identities cannot collide in a reservation, genesis, alias, head, group,
    rotation, or reconstruction. A store returning another scope's predecessor is
    rejected before mutation commit.
- **AE-083:** Calling no-ID Save on the advertised terminal can reach the private
    receiver only through its hidden Gate and remains one atomic create after
    authorization. The terminal exposes no `Next` or unsecured sibling entry;
    `SaveOnly` and bulk alternatives refuse before Gate callbacks or I/O.
- **AE-084:** A trace checkpoint with a missing or renamed executable/registry/test,
    zero matching or skipped structural/active behavioral test, unmapped AU or
    requirement, ill-typed/one-way edge, missing/duplicate obligation activation,
    fabricated AT×section pair, wrong section/package, or pending test
    treated as active fails. Go's successful `-run` with no matching test cannot
    satisfy it. Missing or changed AU/AH/AE/AI/AT/AM/PN semantic text fails its
    digest/anchor check. Semantic correctness of a structurally valid edge
    assignment remains a reviewer obligation because no duplicated reverse map
    exists.
- **AE-085:** A custom CodecEngine, SemanticDigester, IdentityKeyring, Tokenizer,
    Protector, Revealer, Signer, Verifier, CursorKeys, or FenceKeys implementation
    that changes only after construction, retains inputs, violates its cryptographic
    property, or consumes unbounded work demonstrates the documented trusted-
    collaborator boundary; finite fixtures are never reported as proof of arbitrary
    future behavior, PRF/AEAD/unforgeability, nonce uniqueness, or termination. A
    change exhibited during a fixture/application golden run still fails, and every
    observable returned byte is bounded and copied before downstream work.
- **AE-086:** An unkeyed or wrong-domain access, result, continuation, or denial digest
    and mutation of any one exhaustive input member, including role, query class,
    requested reconstruction/comparison boundary/budget, control reason/limit/hold request,
    incoming or emitted continuation token/key/expiry, normalized control
    selection, inventory-fence envelope/decoded claim, returned hold projection,
    or origin request/grant commitment, fails a low-entropy
    dictionary/cross-domain canary; the keyed legal neighbor stays stable under its
    exact catalog profile.
- **AE-087:** Catalog install or activation with a zero, malformed, missing, discarded,
    or falsely reported external deployment-ledger reference refuses. A stale expected
    parent, a mutation that would make complete readback exceed its hard bound, skipped
    direct-child activation, mutation-log mismatch after reopen, or a
    runtime concrete value whose dynamic type exposes CatalogAdmin also fails its
    deployment fixture.
- **AE-088:** A zero or malformed non-default projection, mixed time axis/bounds,
    reversed or overflowing window, invalid direction, duplicate/unknown changed
    field or event code, class-incompatible predicate, or grant that rewrites a
    predicate refuses before authority or Log I/O; safe zero defaults normalize
    identically in request, grant, store query, cursor, and evidence. A resource-
    wide or operation-type query cannot omit scope/limit, inject a foreign
    resource/action/member, or spell all resources/scopes.
- **AE-089:** Actor, event-index, client, service, deployment, operation,
    correlation, causation, trace, source, target, subject, and scope equality
    searches require every exact active/retained token profile for their declared
    mode. A missing profile, foreign typed index, protected-only/redacted
    coordinate, raw token, or attempted broad-scan fallback releases nothing.
- **AE-090:** A missing, expired, recreated, mutable, foreign, oversized, or
    reordered search snapshot, changed sort key, progress regression/jump, or
    cumulative grant overflow rejects the whole page. Histories beyond the
    declared cohort count/byte ceilings fail at origin instead of degrading to an
    unstable cursor.
- **AE-091:** Comparison with reversed, forked, foreign, or ambiguous boundaries,
    or an independently widened second walk returns neither endpoint nor evidence.
    Shared-budget exhaustion cannot reset at the second walk: it returns only the
    bounded BudgetExceeded endpoint knowledge and Indeterminate differences after
    its own evidence commits. Redacted-versus-absent and every other unknowable
    pair never become a false equal/different boolean.
- **AE-092:** Recreating a hard-deleted local ID under the same absent or exact
    scope, racing recreation with deletion, or resolving a retained alias of that
    terminal identity rolls back the business mutation and advances no audit head.
    Exact replay of the accepted terminal revision remains idempotent.
- **AE-093:** Exact revision/item access with a foreign catalog/log/ordinal,
    partial containing revision, omitted authorization-summary member, changed
    post-preparation RecordedAt, or mismatched envelope/integrity digest releases
    no object and commits no successful result claim; the originally prepared
    RecordedAt remains explicitly unsigned.
- **AE-094:** A delta-shaped changed revision cannot establish a missing entity
    head. If the adapter cannot capture every current reconstructable after-state,
    if its row/head lock or genesis CAS races, or if any baseline field fails
    encoding/protection, the business mutation rolls back and no partial anchor
    appears. Retry/crash recovery is idempotent; concurrent first touches serialize
    to one full baseline followed by ordinary deltas. `AtObservedTime` before that
    baseline returns `TemporalAmbiguity` with no projection or result evidence,
    never fabricated absence; `Compare` applies the same boundary rule. Exact
    vectors cover baseline minus one nanosecond, baseline itself, and equal-time
    descendants in predecessor order.

- **AE-095:** Required Run whose Begin is NotWritten, Unconfirmed, canceled,
    denied, or invalid invokes its protected callback zero times and returns no
    run handle. Lower-level Begin likewise returns no handle.
    Lookup.AbsentNow after an uncertain begin is observation, not permission to
    execute or start a second attempt.
- **AE-096:** A BestEffort begin failure remains visible. The caller may explicitly
    proceed outside authoritative lifecycle coverage, but receives no handle and
    the framework never reports a complete attempt.
- **AE-097:** Reusing an OperationID or idempotency key with changed start-target
    presence or value,
    values, stable owner, scope, policy/replay fingerprint, operation, semantic
    provider description, or consequence conflicts before protected work and
    advances no attempt head. A changed
    trace/correlation/causation/client/source fact does not rerun work or defeat
    start replay; the stored first-call context remains immutable evidence.
- **AE-098:** Checkpoint or finish before Started, checkpoint after OutcomeUnknown
    or any terminal, an undeclared checkpoint/reason, a regressing/duplicate
    sequence, and a second distinct terminal all refuse without append. A distinct
    authorized ResolveUnknown is the only non-abandon transition from Uncertain
    and may append exactly one truthful Succeeded, Failed, or Cancelled terminal.
- **AE-099:** Concurrent genuine terminal transitions serialize through one exact
    expected-state CAS: one signed terminal wins and every incompatible loser is a
    conflict; exact idempotent replay of the winner is inert.
- **AE-100:** Checkpoint count/bytes have policy, hard, and store ceilings. Exhausting
    checkpoint capacity cannot consume separately reserved capacity for the
    largest direct terminal or for OutcomeUnknown followed by its largest legal
    resolving terminal, and cannot make the terminal proof unbounded.
- **AE-101:** Caller cancellation, deadline, returned error, or panic never by
    itself asserts Failed or Cancelled. A panic is re-raised identically and leaves
    the committed attempt Open unless explicit later evidence closes it.
- **AE-102:** Known rollback may produce Failed and known acknowledged success may
    produce Succeeded. Unknown commit produces neither; a potentially committed
    transactional terminal remains reconcilable and cannot race a standalone
    replacement.
- **AE-103:** A copied, forged, stale, or foreign run handle, another Recorder,
    process-local Backing, durable BackingID/LogID, scope, OperationType,
    AttemptType, catalog, or requester cannot checkpoint, finish, resume, or
    abandon the attempt.
- **AE-104:** A hostile attempt projection with an omitted/reordered transition,
    wrong state/sequence/start/head/leaf/expiry/checkpoint count, or a valid-looking
    sibling chain is rejected after exact chain recomputation and releases no
    handle or state.
- **AE-105:** Passing ExpiresAt never mutates state implicitly. Abandon requires an
    exact authorized attempt, one trusted normalized clock sample at or after the
    signed deadline, a verified Open/Uncertain chain, and one winning CAS against
    concurrent completion.
- **AE-106:** Signed transition time may regress without changing chain sequence.
    In that case derived duration is explicitly unknown rather than negative or
    reordered; process-monotonic duration remains diagnostic Observer data only.
- **AE-107:** Catalog activation cannot remove or incompatibly change an
    AttemptPolicy while its exact authenticated Unsettled count, Open plus
    Uncertain, is nonzero. A malformed/head-mismatched projection cannot block or
    permit activation; a stale prepared proof loses the atomic catalog/counter
    CAS. A store that consistently replays an old valid counter and its matching
    signed head remains an explicit rollback threat requiring an external
    freshness anchor.
- **AE-108:** Retention inventory never selects only part of an attempt chain.
    Open/Uncertain chains are ineligible; a closed cohort uses the maximum
    record-era retention cutoff across every member regardless of sequence or
    regressing signed time, and a hold on any member blocks every transition and
    every co-revision that cannot be split. PurgePlan excludes the current type-
    counter head until atomic supersession or an authenticated activation-reset
    anchor and declares the future executor's operation/start-key non-reuse
    coordination prerequisites. It makes no claim that deletion ran or that a
    tombstone already survived it.
- **AE-109:** A composition root for authentication or another attacker-driven
    operation must place its application-owned admission/rate limiter before
    Begin. Suppressed floods therefore never enter the audit lifecycle; limiter
    failure grants nothing. This is an explicit integration prerequisite, not a
    claim that the audit kernel can identify abusive callers.
- **AE-110:** Calling a target-history door on an explicitly targetless EventType
    or AttemptType, constructing `AttemptTargetIs` for a targetless AttemptType, or
    calling a target door on a present target whose record mode is not equality-
    searchable, returns the same non-disclosing closed-query refusal before
    ContextResolver, authority, keyring, Log, ExactLog, AttemptLog, observer, or
    denial-evidence I/O. Substituting present for absent target semantics, or
    replaying a targetless record with a fabricated empty/sentinel target, conflicts
    and advances no head.
- **AE-111:** An EntityIndex over an unknown, foreign, redacted, secret-forbidden,
    protected-only, noncanonical, or mismatched entity field/codec, a duplicate
    stable index name, or a non-equality storage mode fails declaration compilation.
    A store row with a missing/extra index, changed Before/After presence, side,
    codec, classification, mode, logical commitment, retained token alias, or
    containing item is rejected before release. Create has no Before match, hard
    delete has no After match, and `EitherEquals` never turns absence into equality.
- **AE-112:** Empty, duplicate, above-`MaxSelectorAlternatives`, over-byte,
    malformed, or unencodable `AnyOf` values and empty, duplicate-coordinate, incompatible-origin,
    above-`MaxQuerySelectors`, or over-byte `AllOf` selectors refuse before
    authority or store I/O. Input reorder yields the same canonical query and
    cursor. Two sibling items that separately
    satisfy halves of an `AllOf` conjunction do not match, and backend OR/AND or
    alias-expansion mistakes are caught by authenticated post-validation.
- **AE-113:** A projection or comparison whose lifecycle state disagrees with its
    verified action chain, skips SoftDeleted before Restore, advances after
    HardDeleted, derives EntityPresent from an ineligible pre-baseline boundary, or
    changes
    only the lifecycle result/evidence commitment is rejected. Missing, destroyed,
    unauthorized, or budget-incomplete endpoint proof yields Indeterminate rather
    than EntityPresent, Equal, or Changed by guess.
- **AE-114:** A forged predecessor pointer, sibling-chain neighbor, omitted middle
    revision, rolled-back asserted head, successor chosen by timestamp, or store
    `not found` response cannot prove a neighbor or NeighborChainBoundary. A missing
    retained
    predecessor, incomplete head-to-target successor walk, narrowed grant, or
    cumulative budget stop returns NeighborUnavailable for the affected side and
    discloses no
    inaccessible reference; it never falls through to a different subject or a
    reset second-side budget.
- **AE-115:** AttemptType-wide and attempt-target pages reject a transition from a
    foreign type/target/chain, an omitted selector alias, a mutable snapshot, or a
    target presence/mode mismatch. No page, cursor, projection row, or partial exact
    inspection may be converted into a final AttemptState. If exact operation
    history cannot authenticate every transition through its asserted head or fit
    all disclosed matches, it returns no AttemptStatus, duration, evidence, or
    continuation cursor.
- **AE-116:** Supplying one object as both `AttemptLog` and `AttemptTypeState` does
    not merge their authority: each request must arrive through its own interface
    method and typed query. A process/durable origin, catalog-set, policy/replay
    fingerprint, anchor, head, or counter mismatch fails construction or the exact
    operation. A NotWritten or Unconfirmed AttemptResult with a nonzero or present
    ResultingState, or a committed transition without one, is invalid and releases
    no handle.
- **AE-117:** A zero, changed, or substituted Purpose/Role in Resume,
    ResolveUnknown, or Abandon, a disclosure grant replayed as attempt authority,
    or an attempt grant replayed for another intent refuses before chain state is
    disclosed or mutated. NewHistory with active attempt declarations and nil or
    foreign AttemptLog fails construction; an ordinary non-attempt history request
    carrying a nonzero attempt branch is malformed.

## 5. System invariants

### Policy and privacy

- **AI-001 Allowlist:** a field, equality index, relation, event attribute, context fact, and export
  column is absent unless explicitly declared.
- **AI-002 Earliest protection:** redaction, tokenization, and encryption happen
  before the store or observer receives evidence.
- **AI-003 Secret denial:** exact metadata-secret entity fields and explicitly
  Secret manual-event fields cannot be retained reversibly. Arbitrary manual
  extractors are trusted policy and must pass application credential canaries.
- **AI-004 Stable meaning:** every wire identifier, declared code, executable-policy
  version, and codec version has one reviewed semantic meaning for the lifetime of
  stored evidence, backed by golden fingerprints rather than function identity.
  Grammar and manifest membership are mechanically enforced; semantic privacy of a
  syntactically valid public code is an explicit declaration-author and reviewer
  obligation.
- **AI-005 Manifest lineage:** semantically relevant policy compiles to a
  deterministic generation whose digest binds its exact predecessor. Writes use
  only the active generation; retained generations authenticate history but never
  grant present access. Policy/code-set/codec fixture fingerprints are immutable
  record-era meaning. Every RetentionClass has one immutable record-era rule; the
  same identifier can never acquire another cutoff meaning.
- **AI-006 Boundedness:** all observable text, bytes, collections, batches, actor
    chains, selector alternatives/conjunctions, neighbor walks, pages, time ranges,
    catalog generations, cursors, inventory cohorts, fences, and retries are bounded
    before attributable work begins. Termination and
  complexity inside an opaque custom callback are an explicit trusted-collaborator
  obligation; every returned value is bounded before any later cryptographic,
  observer, or store work.

### Truth and atomicity

- **AI-007 Immutable truth:** committed revisions, items, and values are append-only;
  corrections and disputes are linked new assertions, while a business reversal
  is an ordinary newly authorized entity revision rather than an unreachable
  special wire arm.
- **AI-008 Exact claim:** `atomic` means one exact transaction authority over one
  backing. Correlation and grouping never imply it. An unsafe source-less executor
  is never acceptable proof. One store-owned execution object privately captures
  the exact executor and offers only transaction-bound audit calls; no hidden
  registry, ambient context lookup, or root-store retention participates in the
  claim.
- **AI-009 Committed semantics:** entity evidence describes the successfully mutated
  victim and returned persisted state, never caller intent masquerading as result.
- **AI-010 Outcome partition:** denial, not-written, conflict, unconfirmed, canceled,
  backend failure, and unreadable history remain distinguishable.
- **AI-011 Idempotency:** replay equality is a stable keyed logical commitment
  under catalog, operation, and idempotency identity. A separate exact envelope
  digest commits every stored byte and coordinate; the signed integrity digest
  binds both plus generated/observed immutable facts. Exact unkeyed retry uses the
  same revision identity and byte-identical stored integrity content. Disagreement
  is never deduplicated. A conditional hold request binds one authenticated
  expected state, one signed result state, and the complete candidate in its exact
  AppendIntentDigest, while keyed replay compares only the stable logical command
  SemanticDigest. Same-revision/different-intent has no exception; same-key/equal-
  semantic replay returns the original candidate even when current state would
  derive another intent.
- **AI-012 No hidden recovery:** the framework retries, reconciles, reverts, or
  compensates nothing implicitly. A retry token bound to a caller transaction
  cannot be replayed under any authority; it is visibility-reconciliation only.
  RetryToken is process-local and non-serializable; ReconcileKey plus an explicit
  rebuilt idempotent operation is the restart boundary.

### Composition

- **AI-013 Microkernel:** root packages and unrelated extensions never import audit.
- **AI-014 Cost topology:** dependency-light vocabulary, memory implementation,
  conformance, and seam adapters remain in the root module; a backend with a
  third-party dependency is a nested module.
- **AI-015 No combinations:** no audit×tenancy, audit×event, audit×OTel,
  audit×broker, audit×ORM, or audit×storage-backend production package exists.
- **AI-016 Owner seam:** adapters return or implement the ordinary seam they adapt;
  audit owns no competing repository/service/storage chain or service locator.
- **AI-017 Exact effects:** optional executable effects are asserted only where
  their owner exposes them and are never discovered through an opaque wrapper.
  `auditcrud.Secured` is one deliberately ordered authority firewall: its Gate
  alone reaches the private audited receiver, while the returned terminal omits
  `Next` and every unsupported optional mutation capability.
- **AI-018 No lifecycle by construction:** constructors perform validation and
  allocation only.

### Read authority and lifecycle

- **AI-019 Independent read authority:** ordinary access to a resource grants no
  audit-history access.
- **AI-020 Sealed grants:** only the configured access authority can mint a reader
  grant; public values are insufficient.
- **AI-021 Store defense:** authorization narrows the store query, and the kernel
  validates every returned row against the sealed grant. Exact control targets
  use a separate bounded target-set seam that cannot express a broad scan.
  Whole-revision lifecycle requires direct grant ceilings to contain every
  resource, record action, classification, and present exact ScopedReference. Each subject and
  searchable-target coordinate requires an exact or explicit declaration-bound
  wildcard EvidenceSelector; candidates preserve one complete canonical summary.
  Every present scope coordinate in such a summary has an equality-searchable
  record-era representation.
- **AI-022 Cursor binding:** a cursor is opaque and bound to durable log identity,
  durable BackingID, normalized query, complete ScopedReference, grant,
  catalog-set digest, and cursor-key generation. Process-local Backing is checked
  only while assembling store facets and is never serialized.
- **AI-023 Hold precedence:** a revision with any active HoldID membership is absent
  from every purge plan. An inventory fence freezes one bounded exact candidate
  cohort, each immutable eligibility digest, hold epoch and active-set digest,
  durable LogID/BackingID, lineage, query, and as-of time. Recording the
  inventory action itself or a late unrelated commit cannot invalidate it; any
  candidate or canonical stable-HoldIdentity-set change does.
- **AI-024 Audit the auditor:** reads, grants, corrections, verification, inventory,
  holds, and purge planning produce bounded evidence without recursive loops.
  Prepared sensitive results remain hidden until this evidence is independently
  committed outside every caller transaction.

### Integrity and diagnostics

- **AI-025 Canonical bytes:** logical equality and exact stored-envelope integrity
  use distinct versioned canonical representations. Protection AAD binds catalog,
  revision, item, field/context fact, codec, classification, and mode. Neither
  representation depends on database physical layout or map iteration order.
- **AI-026 Honest tamper claim:** a digest/signature is tamper-evident under its key
  and append-only controls; no universal tamper-proof claim is made.
- **AI-027 Observer isolation:** observer failure cannot change an audit transaction;
  observer data is bounded enums, timings, and non-identifying saturated work
  counters, never evidence values, selectors, identifiers, or authority.
- **AI-028 Authoritative ledger:** logs, traces, metrics, event streams, ORM hooks,
  and CDC are sources or mirrors only when policy says so; none becomes authoritative
  by implication.

### Ownership and concurrency

- **AI-029 Frozen input:** bytes and records retained after a call are copied before
  ownership crosses; returned pages and payloads belong fully to the caller.
- **AI-030 Concurrent safety:** declarations, catalog administration, recorder,
  reader/control services, every store seam, codecs, semantic and identity
  digesters, tokenizers, protectors, signers, reveal/verify/cursor/fence
  keyrings, clocks, ID sources, context resolvers, access/control authorities,
  denial limiters, and observers are either documented concurrently safe or
  serialized by the kernel, with race tests for both contracts.
- **AI-031 Single evaluation:** application callbacks and base options execute once.
- **AI-032 Dense item order:** item ordinal is dense canonical order within a revision
  and carries no timing claim; database/global positions need only be stable and
  strictly increasing after commit, with gaps allowed.
- **AI-033 Time honesty:** occurred, observed, recorded, and committed/settled time are
  different facts. No sequence allocation is labelled commit order.
- **AI-034 Entity continuity:** every entity item binds the exact prior
  per-scoped-subject leaf digest, and reconstruction follows that verified chain rather
  than guessing from timestamps, global positions, or item ordinals.
- **AI-035 Context authority:** every stored context fact is selected by one sealed
  action/operation policy with explicit allowed provenance. Provenance labels are
  incomparable; policy never orders their enum values or unions member policies.
  The resolver is the only reader of caller context values; all later Recorder
  collaborators receive explicit copied facts and deadline/cancellation only. If
  cancellation is observable after resolution, resolver output and diagnostic cause
  are discarded before any public error, evidence, rendering, or observation.
- **AI-036 Mutation supervision:** a required entity mutation executes only inside
  a one-use reserved recorder callback. Its first post-start failure or panic
  permanently poisons the outer group and cannot be cleared by nesting or error
  suppression.
- **AI-037 Historical authenticity:** each historical record is verified against
  its exact retained catalog generation and current policy is the sole present
  authorization overlay. Missing lineage returns no partial result. A field absent
  from a record-era manifest is Unobserved, not value-absent, until authenticated
  later evidence first establishes its state.
- **AI-038 Committed disclosure:** history and lifecycle data crosses the public
  boundary only after independently committed access/control evidence; an
  uncertain or rollback-capable settlement releases nothing.
- **AI-039 Hold set:** legal hold is immutable set membership keyed by HoldID, not a
  boolean. Effective transitions advance a per-revision epoch; replay does not and
  a released `(HoldID, RevisionRef)` membership cannot be resurrected. The store
  full-state CASes one kernel-signed candidate while serializing that membership
  and projection. A dedicated signed wire arm binds command, HoldID, stable private
  HoldIdentity, target, matter presence and representation, exact expected and
  result projections, and
  disposition; no generic field encoding, pre-read, or unsigned guess decides
  success.
- **AI-040 Honest transaction scope:** only root transaction provenance the adapter
  can actually preserve qualifies for atomic capture. Caller-visible nested/
  savepoint and unstated raw wrappers refuse; a faults-owned savepoint is allowed
  only wholly inside the supervised root mutation and is finalized before capture
  or staging. Invisible out-of-band savepoints are an explicit caller-contract
  exclusion, never a detected guarantee.
- **AI-041 Origin-bound authority:** access and control decisions bind the exact
  normalized request and keyed requester-context commitment. Resolver output is
  frozen once; authority receives it explicitly, while all later calls use a
  value-free deadline/cancellation context.
- **AI-042 Monotone continuation:** an encrypted authenticated cursor freezes its
  original request, requester, original and prior-effective grant ceilings,
  cumulative progress, expiry, log, backing, and catalog set. Every page
  reauthorizes and intersects with both ceilings, then freezes the next effective
  result; continuation can only stay equal or narrow. The declarative lifetime is positive and hard-bounded,
  while grant and prior-token expiry may only shorten it.
- **AI-043 Searchable protection:** equality search is available only for
  plaintext, tokenized, or indexed-protected coordinates. Every admitted retained
  token profile participates or the whole query refuses; random protection and
  redaction alone never trigger a broad fallback scan.
- **AI-044 Stable private identity:** raw idempotency keys, requester facts, and
  hold matter cross no persistence or diagnostic boundary; a subject is retained
  only in its explicitly declared plaintext, tokenized, or indexed-protected
  form. Domain-separated active/retained operational aliases resolve atomically
  to one stable identity across key rotation and concurrent genesis. Hold aliases
  reuse one private HoldIdentity, and its canonical active-set digest is independent
  of commitment generation and transition order.
- **AI-045 Sealed mutation boundary:** audited CRUD has one closed public
  composition, `auditcrud.Secured(recorder, resource, policy)`, whose terminal
  cannot navigate to its Gate or private receiver. Gate authorization precedes
  root-transaction creation; the receiver accepts only its copied fence, exact
  source resolution (unbound owned root or source-bound executor), and direct
  framework root transaction (or this Recorder's exact active `Within` frame),
  then revalidates state before one business effect.
  Unsafe fallback, foreign source/frame, raw transaction wrapper, savepoint,
  caller transaction without the frame, or private-bridge replay fails before
  mutation I/O. There is no public mutation-admission protocol or unsecured
  `auditcrud` entry.
- **AI-046 Bounded denial admission:** access denial never becomes permission and
  never persists evidence without an application-owned limiter admitting that
  bounded origin-bound attempt. Limiter refusal or failure preserves the denial
  and performs no append. A safe state accessor exposes only whether evidence was
  not configured, suppressed, limiter-failed, not-written, unconfirmed, or
  committed. A limiter panic has no returned state and follows the subsystem-wide
  identical-repanic rule.
- **AI-047 Executable traceability:** one tracked normalized registry maps every
    happy path, edge case, invariant, actor goal, test obligation, implementation
    section, package, reserved test symbol, and stable defect mutant, while one
    canonical manifest commits every AU/AH/AE/AI/AT/AM/PN semantic body. Reciprocal
  machine checks reject missing, duplicate, ranged, orphaned, ill-typed goal,
  wrong-section, wrong-package, duplicate/missing AT activation, fabricated
  Cartesian coverage, or checkpoint-unreachable identifiers. A section
  can claim coverage only after every active reserved behavioral test emits a
  non-skipped pass event from its declared package.
- **AI-048 Closed codes:** every persisted free-form application outcome or reason is a
  member of the exact action-scoped record-era manifest set; deployment waiver
  reasons and closed kernel statuses cannot cross into that namespace.
- **AI-049 Honest executable identity:** policy and codec fingerprints bind declarative
    versions and complete reviewed fixture transcripts, never unstable Go
    implementation identity. Samples remain test-owned; unsampled behavior is an
    explicit author-versioning responsibility.
- **AI-050 Authenticated time boundary:** public time reconstruction verifies a
    store-asserted subject head's signed revision and leaf through genesis, then uses only the
    causally closed signed ObservedAt prefix. A descendant at or before the requested
    instant behind an excluded later-than-request predecessor is temporal ambiguity,
    never a selectable state. RecordedAt, backend position, suffix omission relative
    to the asserted head, and a regressing descendant cannot change the result. A
    hostile store rolling back both history and its previously valid asserted head
    or erasing both head and history remains detectable only with an independently
    retained external anchor and is outside the store-present completeness claim.
- **AI-051 Authenticated hold state:** each hold transition signs exact prior and
    resulting membership/count/epoch/set/head state together with its dedicated
    typed command, HoldID, stable private HoldIdentity, target, matter
    representation, disposition, and exact
    authorization-request commitment. Writer success requires a full-state CAS, and
    lifecycle projections become usable only after the kernel recomputes them from
    authenticated transition references.
- **AI-052 Normalized cancellation:** audit-injected downstream value-free contexts preserve
    Deadline, Done, and Err while discarding Value and caller-controlled Cause.
    After ContextResolver returns, observable cancellation wins over its value or
    error; the resolver cause is discarded and public CauseOf/context.Cause can
    expose only the normalized cancellation sentinel or no diagnostic cause.
    Application callbacks retain the caller context by design and are outside this
    injected-collaborator claim.
- **AI-053 Scoped entity continuity:** entity identity, aliases, reservations,
    genesis, heads, and predecessors bind explicit scope absence or the complete
    ScopedReference pair before subject commitment. No chain crosses a scope
    namespace accidentally.
- **AI-054 Catalog administration boundary:** install and every direct-child
    activation are deployment-only mutations carrying a persisted external
    change-ledger reference. Their immutable bounded mutation records are queryable
    and fully verifiable after reopen; no successful mutation may make complete
    readback exceed its hard count or byte ceiling. Deployment and runtime are distinct concrete
    handle types; the runtime dynamic type exposes no CatalogAdmin. Administration
    never runs in construction or appears in the serving dependency graph, and the
    framework does not pretend to self-audit its bootstrap.
- **AI-055 Public code vocabulary:** persisted outcome/reason codes are
    manifest-public labels and reveal no value authority. The kernel enforces code
    grammar and exact record-era membership; semantic non-sensitivity and
    narrative-free meaning are reviewed declaration-author obligations. Anything
    sensitive or narrative is a separately classified declared field.
- **AI-056 Finite executable conformance:** construction fixtures and application
    goldens prove only the calls they execute. Arbitrary custom policy, codec,
    SemanticDigester, identity, tokenization, protection, reveal, signing,
    verification, cursor, and fence implementations remain explicit trusted
    collaborators with versioning, determinism, concurrency, termination,
    complexity, non-retention, and the required PRF/AEAD/unforgeability/nonce/key-
    continuity properties. Only audit-owned helpers make those cryptographic
    properties an inspectable framework guarantee; custom providers carry explicit
    deployment and application-canary obligations.
- **AI-057 Non-vacuous goal trace:** one normalized edge registry covers every actor
    goal and every requirement reciprocally without a duplicated reverse table.
    Structural validation and the active section's complete behavioral pass-event
    set must both run and agree before a checkpoint succeeds.
- **AI-058 Keyed operational commitments:** access and control request/grant/result,
    access/control continuation, hold-request, and denial-request digests use distinct keyed domains over
    exhaustive typed normalized inputs, including the complete admitted requester
    commitment, role, query class, reconstruction request boundary/budgets, control
    reason/limit/hold request, result boundary/head leaf/status/hold projection,
    retention AsOf, exact cursor protocol/key/expiry, and continuation-origin
    request/grant coordinates.
    A custom SemanticDigester is explicitly trusted to be a stable keyed PRF for
    these domains. No low-entropy private identity is exposed through an unkeyed
    digest.
- **AI-059 Closed investigation algebra:** public history composes only declared
    typed resource/subject, entity/event index, event, actor, context, operation
    type/instance, attempt type/target/instance, revision, and item selectors with
    finite bounded equality conjunctions plus change/code/time predicates. Neither an
    arbitrary metadata path nor an adapter query object crosses the kernel.
- **AI-060 Snapshot monotonicity:** every paged history is one immutable bounded
    cohort with a non-resetting cumulative page/revision/byte budget. History,
    inventory, and selection verification continuations can only advance
    within that cohort under the same or narrower current grant; concurrent and
    backdated commits cannot enter it.
- **AI-061 Honest comparison:** comparison shares one authenticated entity chain,
    ordered pair of boundaries, authorization, combined budget, and committed
    result. Equality is claimed only from two known canonical values or two known
    absences; every epistemically incomplete pair remains indeterminate.
- **AI-062 Terminal entity identity:** stable scoped-subject identity survives
    hard deletion as a terminal head. No active/retained alias can be rebound to a
    new incarnation, while exact retry advances nothing.
- **AI-063 Query closure:** normalized projection, time axis/bounds, direction,
    positive and excluded changed fields, event codes, selector identity, catalog set, grant ceilings,
    bounded selector conjunction and post-allow storage-alias expansion, snapshot,
    and progress remain identical or monotonically narrower through
    authorization, store query, post-validation, cursor, and access evidence.
- **AI-064 Honest entity baseline:** every nonterminal entity chain begins with a
    signed full-state anchor. entity.created is reserved for an actual insert; a
    legacy first-touch anchor is a full entity.changed state at its real
    ObservedAt, and no API infers state or nonexistence before that boundary.
- **AI-065 Durable attempt start:** the advertised Required Run gate begins
    protected work only after its exact Started transition is independently
    Committed with Inserted disposition and a bound active handle exists. Replayed
    starts invoke no callback and expose no handle. Direct application bypass is
    explicit outside policy, never an invisible kernel guarantee.
- **AI-066 Immutable attempt lifecycle:** Started, checkpoints, uncertainty, and
    terminal outcomes are append-only signed transitions; no update, replace,
    discard, or projection rewrite alters prior evidence.
- **AI-067 Single attempt terminal:** one attempt has at most one terminal state
    under exact expected-state CAS; exact replay advances nothing and every
    different terminal conflicts.
- **AI-068 Settlement honesty:** Succeeded, Failed, Cancelled, OutcomeUnknown, and
    Abandoned describe explicit known evidence. Context errors, deadline passage,
    panic, or AbsentNow never infer them.
- **AI-069 Attempt continuity:** AttemptType, OperationType/ID, target presence and
    every present target coordinate/alias, scope identity, start anchor, policy and
    replay fingerprints, sequence, expected/result
    projection, and prior leaf remain bound across every transition and resume.
- **AI-070 Attempt projection distrust:** mutable per-attempt head and per-policy
    Unsettled rows are coordination indexes. The bounded per-attempt chain is
    fully exact-inspected and recomputed before a handle or retention decision.
    A policy counter uses its current exact-verified signed head certificate plus
    inductive append-time CAS; it is never falsely described as a bounded full
    history scan.
- **AI-071 Bounded attempt lifecycle:** phase values, target presence/value, codes, lifetime,
    checkpoint count, transition proof, references, and bytes each obey policy,
    request, hard, and store ceilings; capacity for the largest direct terminal
    and for OutcomeUnknown plus its largest resolving terminal is always reserved.
- **AI-072 Attempt privacy:** request/response dumps, headers, credentials, raw
    errors/exceptions, stack traces, ambient context, and arbitrary payload maps
    have no lifecycle spelling; only declared typed protected values cross.
- **AI-073 Attempt retention cohort:** Open/Uncertain chains never enter a purge
    plan. Closed transitions are selected all-or-none using the maximum record-era
    cutoff of every member, and a hold on any member blocks the indivisible cohort
    and its co-revisions. PurgePlan keeps the current type-counter head outside the
    evidence cohort and declares the future executor's non-reuse coordination
    prerequisites; release 1 asserts neither deletion nor post-deletion survival.
- **AI-074 Attempt lifecycle has no implicit runtime:** NewAttempts validates and
    allocates only; deadline passage, reconciliation, and abandonment start no
    worker, timer, reaper, goroutine, scan, or callback registration.
- **AI-075 Attempt microkernel boundary:** auth, HTTP, jobs, OTel, and application
    modules translate their facts at the composition root. Audit imports none of
    them and owns no framework-specific interceptor or handler.
- **AI-076 Explicit target presence:** event and attempt targets are declaration-
    level sealed unions. Explicit absence has no encoded reference, aliases,
    classification, or target query door, but its presence bit remains authenticated
    semantic and evidence meaning. No kernel or adapter fabricates a target from
    resource, operation, subject, scope, or an empty/sentinel value.
- **AI-077 Closed entity index:** an EntityIndex is a typed manifest member over one
    declared schema field with one canonical codec/classification and plaintext,
    tokenized, or indexed-protected equality representation. Before/After presence
    and value are immutable
    signed item evidence; retained privacy aliases are derived only inside History.
    There is no public property name, generic comparison, range, ordering, regex,
    JSON-path, or backend-expression escape hatch.
- **AI-078 Canonical selector conjunction:** AnyOf is a bounded canonical disjunction
    of typed values for one origin-sealed equality selector; AllOf is a bounded
    canonical conjunction of mutually compatible origin-sealed selectors, with
    every item-level predicate satisfied by one item.
    Logical selectors remain closed and exact through authorization. Their bounded
    alias expansion occurs only after allow, remains internal, and is exact through
    storage, snapshot/cursor, post-validation, and evidence.
- **AI-079 Authenticated entity lifecycle:** projection lifecycle comes only from a
    complete verified legal entity-action prefix and is EntityPresent,
    EntitySoftDeletedState, or terminal EntityHardDeletedState. Comparison applies
    the same epistemic tri-state as field comparison and never infers a lifecycle
    state across an unknown boundary.
- **AI-080 Honest entity neighbors:** NeighborPresent and NeighborChainBoundary are
    positive authenticated claims over one stable scoped-subject chain. Any missing
    proof is NeighborUnavailable, not absence. Both sides share one origin-bound
    authorization, integrity closure, cumulative budget, and independently committed result.
- **AI-081 Attempt investigation is not status:** type-wide and searchable-target
    attempt queries disclose bounded verified transition items only. A final state,
    duration, or complete status exists only behind exact OperationID lookup and a
    verified complete start-to-head chain; no page aggregation may synthesize it.
- **AI-082 Separate attempt coordination facets:** AttemptLog owns bounded per-chain
    state lookup; AttemptTypeState owns only the authenticated global Unsettled
    counter certificate used by append coordination and catalog activation. They
    have disjoint request/result types even when one store implements both. Public
    ResultingState presence exactly follows an authenticated Committed settlement
    with Inserted or Replayed transition evidence.
- **AI-083 Attempt access closure:** History explicitly depends on same-origin
    AttemptLog before offering attempt doors. Resume, resolution, and abandonment
    authorization binds nonzero Purpose, Role, intent, type, operation, target
    presence, scope, owner, fingerprints, and budgets in a strict attempt-only
    branch; its success and denial evidence preserve the same coordinates.

## 6. Capability and bypass matrix

Every production adapter and store publishes a checked matrix with one of four
verdicts: `atomic`, `recorded_non_atomic`, `refused`, or `outside_policy`.

| Path | Minimum first-release verdict |
|---|---|
| Manual typed event, with a typed target or explicit target absence | atomic store append; same-UoW atomic only with shared authority |
| audited CRUD in an existing caller transaction without active `Within` | refused before mutation I/O; the CRUD method cannot expose pending reconciliation or enforce rollback after a swallowed audit error |
| audited CRUD in this Recorder's active `Within` | atomic under the exact frozen root authority; GroupResult carries pending reconciliation |
| caller-visible database/sql savepoint/nested transaction | refused in release 1 before audit or mutation I/O |
| faults-owned savepoint wholly inside an admitted root mutation guard | allowed only around the inner business statement; finalized before capture/staging and never owns audit settlement |
| wrapped raw `*sql.Tx` with unstated scope | refused; only framework-root provenance is accepted |
| out-of-band raw SQL savepoint hidden behind a root context | outside the atomic claim and a caller-contract violation; not detectable by type inspection |
| CRUD create | atomic through no-ID `Save` on the sealed `Secured` terminal |
| CRUD update | atomic exact diff through `Update` on the sealed terminal after Gate-fence revalidation |
| `DefaultService.Replace` | atomic through the sealed terminal's Save; recorded as created or changed |
| Direct optional `Creator`/`Replacer` | outside release 1; capability is not advertised |
| CRUD soft delete | atomic through the terminal's hidden Gate and exact scoped victim capture when tombstone metadata and the soft-delete action agree |
| CRUD hard delete | atomic through the terminal's hidden Gate and exact scoped victim capture when no tombstone exists and the hard-delete action is declared |
| CRUD restore | atomic through the terminal's hidden Gate and exact scoped tombstone/result capture |
| No-ID `Save` on `Secured` | atomic create from the returned persisted subject |
| Assigned-ID `Save` on `Secured` | atomic created, changed, or first-touch-baseline classification from fenced `Previous` |
| Repository deliberately bound without `Secured` | outside policy; no audit claim or evidence |
| Navigation to Gate/private receiver or an unsecured `auditcrud` adapter | impossible through the advertised terminal: no `Next`, public binder, token, or alternative entry |
| SaveOnly/SaveScopedOnly | refused before Gate callback, pre-read, transaction, mutation, or append; no write-only result can prove stored after-state |
| SaveAll/InsertBatch | refused before Gate callback or I/O until exact per-row outcomes are available |
| UpdateAll/DeleteAll | refused before Gate callback or I/O in release 1; a future exact bounded result seam needs its own accepted contract |
| Raw SQL | outside policy unless a declared trigger/CDC profile covers it |
| ORM normal lifecycle | outside policy unless it calls the audited seam |
| ORM/builder shortcut | outside policy and explicitly listed |
| M2M/relation changes | outside policy until a relation policy exists |
| Database cascade/trigger | outside policy unless database evidence profile declares it |
| Event-sourced decision | manual typed event linked in application composition |
| Typed entity-index lookup | bounded Before/After/Either equality with one-coordinate AnyOf and compatible origin-sealed AllOf |
| Generic property/path/backend predicate | refused; no kernel spelling |
| Required targetful or targetless job/auth/HTTP/application attempt | independently committed typed Started before handler/effect; one explicit immutable terminal or uncertainty transition |
| BestEffort attempt | visible non-authoritative failure; caller may proceed only explicitly and receives no handle if start is absent/unconfirmed |
| Same-store attempt terminal plus business mutation | one attempt-bound root group with entity and attempt-head CAS in the same transaction |
| Crash or overdue open attempt | remains Open until exact resume/reconciliation or explicitly authorized Abandoned; no constructor-started reaper |
| AttemptType/target investigation | immutable bounded transition page only; never a partial or inferred AttemptStatus |
| Exact attempt OperationID status | all-or-nothing complete verified chain through its authenticated head |
| Storage access/change | manual typed event; no content or URL capture |
| Audit read/lifecycle | dedicated access/lifecycle path; result released only after independently committed evidence |
| Denied history/control attempt | built-in catalog-declared evidence only after an application-owned limiter admission; otherwise denial with zero append |

## 7. Release tiers

Implementation checkpoints follow the delivery order requested for this project:
foundation/core first; CRUD plus Frostgrove composition integrations second;
advanced history, reconstruction, attempt, and control capabilities third; and a
final hostile-edge, concurrency, mutation, and full-suite hardening pass last. This
is sequencing only. Every production-core requirement below remains in the final
release gate. The CRUD-alpha checkpoint proves the four supported mutation happy
paths and their focused boundary failures; the broad combinatorial substitution
and property matrix remains mandatory in that final hardening pass.

### Production core

- declarations, append-only catalog lineage, canonical codecs, context allowlists,
  privacy modes, trusted provenance;
- manual targetful or explicitly targetless business/security events and entity
  revisions;
- declared targetful or explicitly targetless durable attempt lifecycle with committed Required start, bounded
  checkpoints, exact restart resume, honest uncertainty/abandonment, single
  terminal CAS, separate per-chain/global-counter store facets, and optional
  same-store atomic terminal grouping;
- exact outcome classification, keyed logical and stored-envelope commitments,
  AAD-bound protection, optional signing;
- memory store and conformance suite;
- PostgreSQL store with managed/verified schema and exact transaction binding;
- sealed `auditcrud.Secured` CRUD no-ID save/create, assigned save, update, delete,
  and restore composite with a full first-touch baseline for pre-existing rows,
  no navigation or unsecured entry, exact source/root-transaction proof, and
  fail-closed write-only and bulk methods;
- protected typed resource/subject/entity-index/event/index/actor/context/
  operation-type/operation-instance/attempt-type/attempt-target/exact history with
  immutable bounded snapshot cursors and closed filters;
- bounded canonical typed AnyOf/AllOf equality selectors with entity
  Before/After/Either semantics and internal retained privacy aliases;
- reconstruction and ordered comparison for explicitly reversible fields with
  authenticated entity lifecycle state, plus exact tri-state entity neighbors;
- independently committed access evidence, correction, integrity verification,
  overlapping identified holds, retention inventory and private authenticated
  fenced hold-aware purge planning;
- composition fixtures for auth, tenancy, event, jobs, storage, OTel, and app modules.

### Production follow-up

- bounded explicit legacy backfill with migration/source provenance, bulk victims,
  relationship/M2M policies, fleet-wide overdue-attempt discovery/reaper,
  renewable leases/ownership handoff, and reviewed framework-specific attempt
  adapters;
- bounded resource-wide point-in-time entity sets/reification with an explicit
  completeness and snapshot-watermark contract; release 1 resource-wide history
  does not imply this stronger claim;
- derived authenticated reconstruction checkpoints bound to catalog, chain,
  revision, leaf, and per-field knowledge, always verified against immutable
  predecessors rather than treated as replacement truth;
- fenced purge execution that atomically revalidates the plan, deletes each attempt
  cohort all-or-none, preserves the current type-counter certificate, materializes
  or retains authenticated non-reusable OperationID/start-key tombstones, and makes
  later reuse return an evidence-destroyed conflict; plus signed export outbox/
  recipient adapters;
- partition automation, WORM archive checkpoints, KMS/HSM adapters;
- database trigger/CDC ingestion profile for writers outside Frostgrove;
- cross-region causal graph and settlement watermarks;
- ergonomic code generation from a reviewed audit manifest.

### Explicit non-goals

- audit every field/table/request automatically;
- arbitrary metadata or generic model serialization;
- hidden distributed transactions or exactly-once delivery;
- transparent raw SQL/ORM-hook coverage;
- a generic analytics query language over evidence;
- automatic revert, purge, export, relay, migration, or background processing;
- universal legal compliance or tamper-proof claims.
