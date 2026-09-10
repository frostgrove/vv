package auditflow_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/frostgrove/vv/audit"
	"github.com/frostgrove/vv/audit/auditcrud"
	"github.com/frostgrove/vv/auth"
	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/adapter/crudsql"
	"github.com/frostgrove/vv/crud/decorators/faults"
	"github.com/frostgrove/vv/crud/decorators/security"
	"github.com/frostgrove/vv/crud/sqlrepo"
	"github.com/frostgrove/vv/port"
	"github.com/frostgrove/vv/tenancy"
	"github.com/frostgrove/vv/tenancy/tenancyrow"
	_ "modernc.org/sqlite"
)

const (
	auditAlphaToken  = "alpha-bearer-secret"
	auditAlphaTenant = "tenant-alpha"
	auditOtherTenant = "tenant-from-untrusted-header"
)

type auditAlphaInvoice struct {
	ID       int64  `db:"id,pk,auto"`
	TenantID string `db:"tenant_id"`
	Name     string `db:"name"`
}

type auditAlphaInvoiceUpdate struct {
	Name *string `db:"name"`
}

type principalTenantResolver struct {
	resolution tenancy.Resolution
}

type auditObservationLog struct {
	mu     sync.Mutex
	values []audit.AuditObservation
}

type auditAlphaStack struct {
	database     *sql.DB
	guard        *auth.Guard
	authority    *tenancy.Authority
	service      *port.DefaultService[auditAlphaInvoice, int64, auditAlphaInvoiceUpdate]
	observations *auditObservationLog
}

type storedAuditSummary struct {
	action           string
	actorKind        int
	actorProvenance  int
	actorMode        int
	actorPlaintext   int
	actorToken       int
	scopeProvenance  int
	scopeMode        int
	scopePlaintext   int
	scopeToken       int
	subjectMode      int
	subjectPlaintext int
	subjectToken     int
	fieldsRedacted   int
}

var auditAlphaInvoices = sqlrepo.Define[auditAlphaInvoice, int64, auditAlphaInvoiceUpdate]("auditflow_invoices")

func TestAuditAlphaAuthenticatedTenantCRUDCommitsProtectedEvidence(t *testing.T) {
	stack := newAuditAlphaStack(t)
	ctx := stack.authenticatedContext(t)

	created, err := stack.service.Create(ctx, port.CreateCommand[auditAlphaInvoice]{
		Model: auditAlphaInvoice{Name: "draft lease"},
	})
	if err != nil {
		fault := port.FaultOf(err)
		t.Fatalf("create through application stack: %v; detail=%+v violations=%+v", err, fault.Detail, fault.Violations)
	}
	if created.ID == 0 || created.TenantID != auditAlphaTenant || created.Name != "draft lease" {
		t.Fatalf("created invoice = %+v", created)
	}

	approved := "approved lease"
	updated, err := stack.service.Update(ctx, port.UpdateCommand[int64, auditAlphaInvoiceUpdate]{
		ID: created.ID, Patch: auditAlphaInvoiceUpdate{Name: &approved},
	})
	if err != nil {
		t.Fatalf("update through application stack: %v", err)
	}
	if updated.ID != created.ID || updated.TenantID != auditAlphaTenant || updated.Name != approved {
		t.Fatalf("updated invoice = %+v", updated)
	}

	deleted, err := stack.service.Delete(ctx, port.DeleteCommand[int64]{ID: created.ID})
	if err != nil {
		t.Fatalf("delete through application stack: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("deleted rows = %d, want 1", deleted)
	}
	if rows := tableCount(t, stack.database, "auditflow_invoices"); rows != 0 {
		t.Fatalf("business rows after delete = %d, want 0", rows)
	}

	summaries := storedAuditSummaries(t, stack.database)
	wantActions := []string{
		string(audit.EntityCreated),
		string(audit.EntityChanged),
		string(audit.EntityHardDeleted),
	}
	if got := summaryActions(summaries); !slices.Equal(got, wantActions) {
		t.Fatalf("stored audit actions = %v, want %v", got, wantActions)
	}
	for index, summary := range summaries {
		if summary.actorKind != int(audit.HumanActor) || summary.actorProvenance != int(audit.Verified) {
			t.Fatalf("revision %d actor identity = kind:%d provenance:%d", index, summary.actorKind, summary.actorProvenance)
		}
		if summary.actorMode != int(audit.AsToken) || summary.actorPlaintext != 0 || summary.actorToken != 32 {
			t.Fatalf("revision %d actor storage = mode:%d plaintext:%d token:%d", index, summary.actorMode, summary.actorPlaintext, summary.actorToken)
		}
		if summary.scopeProvenance != int(audit.Verified) || summary.scopeMode != int(audit.AsToken) || summary.scopePlaintext != 0 || summary.scopeToken != 32 {
			t.Fatalf("revision %d scope storage = provenance:%d mode:%d plaintext:%d token:%d", index, summary.scopeProvenance, summary.scopeMode, summary.scopePlaintext, summary.scopeToken)
		}
		if summary.subjectMode != int(audit.AsToken) || summary.subjectPlaintext != 0 || summary.subjectToken != 32 {
			t.Fatalf("revision %d subject storage = mode:%d plaintext:%d token:%d", index, summary.subjectMode, summary.subjectPlaintext, summary.subjectToken)
		}
		if summary.fieldsRedacted != 1 {
			t.Fatalf("revision %d retained an unredacted declared field", index)
		}
	}

	observations := stack.observations.snapshot()
	if len(observations) != 3 {
		t.Fatalf("audit observations = %d, want 3 terminal mutation observations", len(observations))
	}
	for index, observation := range observations {
		if observation.Kind != audit.ObservationGroup || observation.Phase != audit.ObservationStore || observation.Items != 1 ||
			observation.Disposition != audit.Inserted || observation.Settlement != audit.InCallerTransaction ||
			observation.Failure != audit.NoFailure || observation.Work.StoreCalls != 1 {
			t.Fatalf("observation %d = %+v", index, observation)
		}
	}
}

func TestAuditAlphaRequiresGuardAndAuthorityBeforeCRUD(t *testing.T) {
	stack := newAuditAlphaStack(t)
	if _, err := stack.guard.Authenticate(context.Background(), func(string) string { return "Bearer wrong" }); !errors.Is(err, auth.ErrUnauthenticated) {
		t.Fatalf("wrong bearer credential = %v, want auth.ErrUnauthenticated", err)
	}

	authenticated, err := stack.guard.Authenticate(context.Background(), func(name string) string {
		if name == auth.HeaderAuthorization {
			return "Bearer " + auditAlphaToken
		}
		return ""
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stack.service.Create(authenticated, port.CreateCommand[auditAlphaInvoice]{
		Model: auditAlphaInvoice{Name: "must not write"},
	}); !errors.Is(err, tenancy.ErrNoScope) {
		t.Fatalf("unbound tenant create = %v, want tenancy.ErrNoScope", err)
	}
	if rows := tableCount(t, stack.database, "auditflow_invoices"); rows != 0 {
		t.Fatalf("unauthorized business writes = %d", rows)
	}
	if revisions := tableCount(t, stack.database, "auditflow_revisions"); revisions != 0 {
		t.Fatalf("unauthorized audit writes = %d", revisions)
	}
}

func TestAuditAlphaConstraintFaultRollsBackBusinessAndEvidence(t *testing.T) {
	stack := newAuditAlphaStack(t)
	ctx := stack.authenticatedContext(t)

	command := port.CreateCommand[auditAlphaInvoice]{Model: auditAlphaInvoice{Name: "duplicate lease"}}
	if _, err := stack.service.Create(ctx, command); err != nil {
		t.Fatalf("seed invoice: %v", err)
	}
	if _, err := stack.service.Create(ctx, command); !errors.Is(err, crud.ErrConflict) {
		t.Fatalf("duplicate create = %v, want crud.ErrConflict", err)
	}
	if rows := tableCount(t, stack.database, "auditflow_invoices"); rows != 1 {
		t.Fatalf("business rows after duplicate = %d, want 1", rows)
	}
	if revisions := tableCount(t, stack.database, "auditflow_revisions"); revisions != 1 {
		t.Fatalf("audit revisions after duplicate = %d, want 1", revisions)
	}
	if observations := stack.observations.snapshot(); len(observations) != 1 {
		t.Fatalf("terminal audit observations after duplicate = %d, want 1", len(observations))
	}
}

func newAuditAlphaStack(t *testing.T) *auditAlphaStack {
	t.Helper()
	database, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	database.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = database.Close() })
	for _, statement := range []string{
		`CREATE TABLE auditflow_invoices (id INTEGER PRIMARY KEY AUTOINCREMENT, tenant_id TEXT NOT NULL, name TEXT NOT NULL, UNIQUE (tenant_id, name))`,
		`CREATE TABLE auditflow_revisions (
			sequence INTEGER PRIMARY KEY AUTOINCREMENT,
			revision_id BLOB NOT NULL UNIQUE,
			action TEXT NOT NULL,
			actor_kind INTEGER NOT NULL,
			actor_provenance INTEGER NOT NULL,
			actor_mode INTEGER NOT NULL,
			actor_plaintext BLOB,
			actor_token BLOB,
			scope_provenance INTEGER NOT NULL,
			scope_mode INTEGER NOT NULL,
			scope_plaintext BLOB,
			scope_token BLOB,
			subject_mode INTEGER NOT NULL,
			subject_plaintext BLOB,
			subject_token BLOB,
			fields_redacted INTEGER NOT NULL
		)`,
		`CREATE TABLE auditflow_entity_heads (
			resource TEXT NOT NULL,
			scoped INTEGER NOT NULL,
			scope BLOB NOT NULL,
			subject BLOB NOT NULL,
			chain BLOB NOT NULL,
			leaf BLOB NOT NULL,
			terminal INTEGER NOT NULL,
			PRIMARY KEY (resource, scoped, scope, subject)
		)`,
	} {
		if _, err := database.Exec(statement); err != nil {
			t.Fatalf("prepare audit alpha schema: %v", err)
		}
	}

	tenantReference, err := tenancy.ParseReference(auditAlphaTenant)
	if err != nil {
		t.Fatal(err)
	}
	epoch, err := tenancy.NewEpoch(7)
	if err != nil {
		t.Fatal(err)
	}
	authority, err := tenancy.New(tenancy.Spec{
		Resolver: principalTenantResolver{resolution: tenancy.Resolution{
			Reference: tenantReference, Lifecycle: tenancy.Active, Epoch: epoch,
		}},
		Origin: "auditflow-alpha",
	})
	if err != nil {
		t.Fatal(err)
	}

	contextPolicy := audit.ContextFacts(
		audit.ActorChain(audit.ContextRequired, audit.Provenances(audit.Verified), audit.Personal, audit.AsToken),
		audit.ScopeFact(audit.ContextRequired, audit.Provenances(audit.Verified), audit.Internal, audit.AsToken),
		audit.GeneratedOperationFact(audit.Internal, audit.AsPlaintext),
	)
	resource := audit.Define(audit.Policy[auditAlphaInvoice, int64]{
		Model:     auditAlphaInvoices.Meta(),
		Semantics: audit.Semantics(1, audit.PolicyGolden("auditflow.invoice.resource", strings.Repeat("11", 32))),
		Descriptor: audit.Descriptor{
			Resource: "auditflow.invoice", Owner: "auditflow.team", Purpose: "business.audit",
			Retention: "business.forever", Consequence: audit.Required, Context: contextPolicy,
		},
		Subject: audit.TokenizedSubject(func(id int64) string {
			return "invoice:" + strconv.FormatInt(id, 10)
		}, audit.Internal),
		Actions: audit.Actions(audit.EntityCreated, audit.EntityChanged, audit.EntityHardDeleted),
		Fields: audit.Fields[auditAlphaInvoice](
			audit.Redacted[auditAlphaInvoice, string]("Name", "name", audit.Text(), audit.Personal),
		),
	})
	operation := audit.DeclareOperation(audit.OperationPolicy{
		Name: "auditflow.invoice.mutation", Semantics: audit.Semantics(1),
		Retention: "business.forever", Consequence: audit.Required, Context: contextPolicy,
		Members: audit.OperationMembers(
			resource.Action(audit.EntityCreated),
			resource.Action(audit.EntityChanged),
			resource.Action(audit.EntityHardDeleted),
		),
	})
	semantic, err := audit.HMACSemanticDigester("auditflow-semantic", bytes.Repeat([]byte{1}, 32))
	if err != nil {
		t.Fatal(err)
	}
	identities, err := audit.HMACIdentityKeyring(audit.HMACIdentityKey{
		KeyID: "auditflow-identity", Key: bytes.Repeat([]byte{2}, 32), Active: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	tokenizer, err := audit.HMACTokenizer(audit.HMACTokenKey{
		KeyID: "auditflow-token", Key: bytes.Repeat([]byte{3}, 32), Active: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := audit.Compile(audit.CatalogSpec{
		ID: "auditflow.alpha", Owner: "auditflow.team", Generation: 1,
		Retention: audit.RetentionRules(audit.KeepForever("business.forever")),
		Semantics: semantic.Description(), Identities: identities.ActiveDescription(),
		Tokens: tokenizer.ActiveDescription(), Integrity: audit.IntegrityOnly(),
	}, resource, operation)
	if err != nil {
		t.Fatal(err)
	}
	catalogs, err := audit.Lineage(catalog)
	if err != nil {
		t.Fatal(err)
	}
	source := crudsql.SQLite(database)
	writer, err := newSQLiteAuditWriter(database, source, catalogs)
	if err != nil {
		t.Fatal(err)
	}
	observations := &auditObservationLog{}
	recorder, err := audit.New(audit.Config{
		Catalogs: catalogs, Writer: writer, Context: applicationAuditContext(authority),
		Semantics: semantic, Identities: identities, Tokenizer: tokenizer,
		Observer: audit.MustObservers(
			audit.ObserverFunc(func(audit.AuditObservation) { panic("application metrics exporter") }),
			audit.ObserverFunc(observations.observe),
		),
	})
	if err != nil {
		t.Fatal(err)
	}
	permission := auth.Permission("invoice.write")
	policy := security.Combine(
		tenancyrow.Policy[auditAlphaInvoice, int64](authority,
			tenancyrow.Column[auditAlphaInvoice]("TenantID", tenancyrow.Derive, nil)),
		security.RequirePermission[auditAlphaInvoice, int64](permission),
	)
	base := auditAlphaInvoices.Bind(source)
	faulted := faults.Enrich[auditAlphaInvoice, int64]()(base.Core)
	secured := auditcrud.Secured(recorder, resource, policy)(faulted)
	repository := crud.Wrap[auditAlphaInvoice, int64, auditAlphaInvoiceUpdate](secured)
	service := port.NewService(repository)
	guard := auth.NewGuard(auth.AuthenticatorFunc(func(_ context.Context, credential auth.Credential) (auth.Principal, error) {
		if !credential.Is(auth.SchemeBearer) || credential.Token != auditAlphaToken {
			return nil, auth.Unauthenticated("credential rejected")
		}
		return auth.Claims{
			Sub: "user:42", Permissions: []auth.Permission{permission},
			Attrs: map[string]any{"tenant": auditAlphaTenant},
		}, nil
	}))
	return &auditAlphaStack{
		database: database, guard: guard, authority: authority,
		service: service, observations: observations,
	}
}

func (stack *auditAlphaStack) authenticatedContext(t *testing.T) context.Context {
	t.Helper()
	ctx, err := stack.guard.Authenticate(context.Background(), func(name string) string {
		switch name {
		case auth.HeaderAuthorization:
			return "Bearer " + auditAlphaToken
		case "X-Tenant-ID":
			return auditOtherTenant
		default:
			return ""
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, err = stack.authority.Bind(ctx, tenancy.ClassWrite)
	if err != nil {
		t.Fatal(err)
	}
	return ctx
}

func (resolver principalTenantResolver) Resolve(ctx context.Context) (tenancy.Resolution, error) {
	principal, err := auth.Require(ctx)
	if err != nil {
		return tenancy.Resolution{}, tenancy.ErrNoScope
	}
	value, ok := principal.Attr("tenant")
	if !ok || value != resolver.resolution.Reference.Value() {
		return tenancy.Resolution{}, tenancy.ErrUntrusted
	}
	return resolver.resolution, nil
}

func (resolver principalTenantResolver) Lookup(_ context.Context, reference tenancy.Reference) (tenancy.Resolution, error) {
	if reference != resolver.resolution.Reference {
		return tenancy.Resolution{}, tenancy.ErrUnmapped
	}
	return resolver.resolution, nil
}

func applicationAuditContext(authority *tenancy.Authority) audit.ContextResolver {
	return audit.ContextResolverFunc(func(ctx context.Context) (audit.Context, error) {
		principal, err := auth.Require(ctx)
		if err != nil {
			return audit.Context{}, err
		}
		scope, err := authority.Scope(ctx, tenancy.ClassWrite)
		if err != nil {
			return audit.Context{}, err
		}
		scopeValue, err := audit.NewContextValue(audit.ScopedReference{
			Scope: "tenant", Reference: audit.Reference(fmt.Sprintf("%s@%d", scope.Reference().Value(), scope.Epoch().Value())),
		}, audit.Verified)
		if err != nil {
			return audit.Context{}, err
		}
		return audit.Context{
			Actors: []audit.Actor{{Kind: audit.HumanActor, Reference: audit.Reference(principal.Subject()), Provenance: audit.Verified}},
			Scope:  scopeValue,
		}, nil
	})
}

func (log *auditObservationLog) observe(observation audit.AuditObservation) {
	log.mu.Lock()
	log.values = append(log.values, observation)
	log.mu.Unlock()
}

func (log *auditObservationLog) snapshot() []audit.AuditObservation {
	log.mu.Lock()
	defer log.mu.Unlock()
	return slices.Clone(log.values)
}

func tableCount(t *testing.T, database *sql.DB, table string) int {
	t.Helper()
	var count int
	if err := database.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func storedAuditSummaries(t *testing.T, database *sql.DB) []storedAuditSummary {
	t.Helper()
	rows, err := database.Query(`SELECT action, actor_kind, actor_provenance, actor_mode,
		COALESCE(length(actor_plaintext), 0), COALESCE(length(actor_token), 0),
		scope_provenance, scope_mode, COALESCE(length(scope_plaintext), 0), COALESCE(length(scope_token), 0),
		subject_mode, COALESCE(length(subject_plaintext), 0), COALESCE(length(subject_token), 0), fields_redacted
		FROM auditflow_revisions ORDER BY sequence`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var summaries []storedAuditSummary
	for rows.Next() {
		var summary storedAuditSummary
		if err := rows.Scan(
			&summary.action, &summary.actorKind, &summary.actorProvenance, &summary.actorMode,
			&summary.actorPlaintext, &summary.actorToken, &summary.scopeProvenance, &summary.scopeMode,
			&summary.scopePlaintext, &summary.scopeToken, &summary.subjectMode, &summary.subjectPlaintext,
			&summary.subjectToken, &summary.fieldsRedacted,
		); err != nil {
			t.Fatal(err)
		}
		summaries = append(summaries, summary)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return summaries
}

func summaryActions(summaries []storedAuditSummary) []string {
	actions := make([]string, len(summaries))
	for index, summary := range summaries {
		actions[index] = summary.action
	}
	return actions
}
