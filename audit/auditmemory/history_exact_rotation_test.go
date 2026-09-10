package auditmemory_test

import (
	"bytes"
	"context"
	"errors"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/frostgrove/vv/audit"
	"github.com/frostgrove/vv/audit/auditmemory"
	"github.com/frostgrove/vv/crud"
)

type exactRotationEvent struct {
	Target string
	At     time.Time
}

type exactRotationEntity struct {
	ID   int64  `db:"id,pk"`
	Name string `db:"name"`
}

func TestExactHistoryRetainsOldScopeAsCurrentAuthorityBoundary(t *testing.T) {
	scoped := audit.ContextFacts(
		audit.GeneratedOperationFact(audit.Public, audit.AsPlaintext),
		audit.ScopeFact(audit.ContextRequired, audit.Provenances(audit.Verified), audit.Public, audit.AsPlaintext),
	)
	unscoped := audit.ContextFacts(audit.GeneratedOperationFact(audit.Public, audit.AsPlaintext))
	oldEvent := exactRotationDeclaration("rotation.resource", "rotation.action", scoped, "11")
	currentResource := exactRotationResource(t, "rotation.resource", "12")
	currentAction := exactRotationDeclaration("rotation.other", "rotation.action", unscoped, "13")
	runtime := exactRotationRuntime(t, oldEvent, []audit.Declaration{currentResource, currentAction})
	scope, err := audit.NewContextValue(audit.ScopedReference{Scope: "tenant", Reference: "north"}, audit.Verified)
	if err != nil {
		t.Fatal(err)
	}
	resolver, err := audit.StaticContext(audit.Context{Scope: scope})
	if err != nil {
		t.Fatal(err)
	}
	receipt := recordExactRotation(t, runtime.oldCatalogs, runtime.store, runtime.semantic, runtime.identities, resolver, oldEvent, "scope")
	runtime.activate(t)
	currentRecorder := newExactRotationRecorder(t, runtime.currentCatalogs, runtime.store, runtime.semantic, runtime.identities, resolver)
	var authorityCalls atomic.Int64
	history := newExactRotationHistory(t, currentRecorder, runtime.store, &authorityCalls)
	_, err = history.Revision(context.Background(), audit.RevisionRef{Catalog: runtime.oldCatalog.Ref(), Revision: receipt.RevisionID()}, audit.ExactAccessQuery{
		Resources: []audit.Resource{"rotation.other", "rotation.resource"}, Classifications: []audit.Classification{audit.Public},
		Query: audit.Query{Purpose: "history.read", Role: "auditor", Actions: []audit.Action{audit.Action(audit.EntityCreated), "rotation.action"}, Fields: audit.NoFields(), Context: audit.NoContext()},
	})
	if !errors.Is(err, audit.ErrInvalid) {
		t.Fatalf("retained scoped exact read without scope = %v", err)
	}
	if authorityCalls.Load() != 0 {
		t.Fatal("retained scoped exact read reached authority without a scope")
	}
}

func TestExactHistoryRejectsRetiredCrossPairAfterCatalogRotation(t *testing.T) {
	unscoped := audit.ContextFacts(audit.GeneratedOperationFact(audit.Public, audit.AsPlaintext))
	oldEvent := exactRotationDeclaration("rotation.one", "rotation.two", unscoped, "21")
	currentOne := exactRotationResource(t, "rotation.one", "22")
	currentTwo := exactRotationDeclaration("rotation.two", "rotation.two", unscoped, "23")
	runtime := exactRotationRuntime(t, oldEvent, []audit.Declaration{currentOne, currentTwo})
	resolver, err := audit.StaticContext(audit.Context{})
	if err != nil {
		t.Fatal(err)
	}
	receipt := recordExactRotation(t, runtime.oldCatalogs, runtime.store, runtime.semantic, runtime.identities, resolver, oldEvent, "cross")
	runtime.activate(t)
	currentRecorder := newExactRotationRecorder(t, runtime.currentCatalogs, runtime.store, runtime.semantic, runtime.identities, resolver)
	var authorityCalls atomic.Int64
	history := newExactRotationHistory(t, currentRecorder, runtime.store, &authorityCalls)
	_, err = history.Revision(context.Background(), audit.RevisionRef{Catalog: runtime.oldCatalog.Ref(), Revision: receipt.RevisionID()}, audit.ExactAccessQuery{
		Resources: []audit.Resource{"rotation.one", "rotation.two"}, Classifications: []audit.Classification{audit.Public},
		Query: audit.Query{Purpose: "history.read", Role: "auditor", Actions: []audit.Action{audit.Action(audit.EntityCreated), "rotation.two"}, Fields: audit.NoFields(), Context: audit.NoContext()},
	})
	if !errors.Is(err, audit.ErrRefused) {
		t.Fatalf("retired resource/action cross-pair = %v", err)
	}
	if authorityCalls.Load() != 1 {
		t.Fatalf("retired cross-pair authority calls = %d", authorityCalls.Load())
	}
}

func TestExactHistoryRejectsRemovedProvenanceAfterCatalogRotation(t *testing.T) {
	oldPolicy := audit.ContextFacts(audit.OperationFact(
		audit.ContextRequired,
		audit.Provenances(audit.ServerDerived, audit.Verified),
		audit.Public,
		audit.AsPlaintext,
	))
	currentPolicy := audit.ContextFacts(audit.OperationFact(
		audit.ContextRequired,
		audit.Provenances(audit.ServerDerived),
		audit.Public,
		audit.AsPlaintext,
	))
	oldEvent := exactRotationDeclarationVersion("rotation.provenance", "rotation.provenance.recorded", oldPolicy, 1, "31")
	currentEvent := exactRotationDeclarationVersion("rotation.provenance", "rotation.provenance.recorded", currentPolicy, 2, "32")
	runtime := exactRotationRuntime(t, oldEvent, []audit.Declaration{currentEvent})
	operation, err := audit.NewContextValue(audit.OperationID{9}, audit.Verified)
	if err != nil {
		t.Fatal(err)
	}
	resolver, err := audit.StaticContext(audit.Context{Operation: operation})
	if err != nil {
		t.Fatal(err)
	}
	receipt := recordExactRotation(t, runtime.oldCatalogs, runtime.store, runtime.semantic, runtime.identities, resolver, oldEvent, "provenance")
	runtime.activate(t)
	currentRecorder := newExactRotationRecorder(t, runtime.currentCatalogs, runtime.store, runtime.semantic, runtime.identities, resolver)
	var authorityCalls atomic.Int64
	history := newExactRotationHistory(t, currentRecorder, runtime.store, &authorityCalls)
	reference := audit.RevisionRef{Catalog: runtime.oldCatalog.Ref(), Revision: receipt.RevisionID()}
	input := audit.ExactAccessQuery{
		Resources: []audit.Resource{"rotation.provenance"}, Classifications: []audit.Classification{audit.Public},
		Query: audit.Query{
			Purpose: "history.read", Role: "auditor", Actions: []audit.Action{"rotation.provenance.recorded"},
			Fields: audit.NoFields(), Context: audit.OnlyContext(audit.OperationContext),
		},
	}
	if _, err := history.Revision(context.Background(), reference, input); !errors.Is(err, audit.ErrRefused) {
		t.Fatalf("revision with removed provenance = %v", err)
	}
	if _, err := history.Item(context.Background(), audit.ItemRef{Revision: reference, Ordinal: 0}, input); !errors.Is(err, audit.ErrRefused) {
		t.Fatalf("item with removed provenance = %v", err)
	}
	if authorityCalls.Load() != 2 {
		t.Fatalf("removed provenance authority calls = %d", authorityCalls.Load())
	}
}

type exactRotationState struct {
	store           *auditmemory.Store
	deployment      *auditmemory.Deployment
	oldCatalog      *audit.Catalog
	currentCatalog  *audit.Catalog
	oldCatalogs     *audit.CatalogSet
	currentCatalogs *audit.CatalogSet
	semantic        audit.SemanticDigester
	identities      audit.IdentityKeyring
	change          audit.CatalogChangeRef
}

func exactRotationRuntime(t *testing.T, old audit.Declaration, current []audit.Declaration) *exactRotationState {
	t.Helper()
	semantic, err := audit.HMACSemanticDigester("rotation-semantic", bytes.Repeat([]byte{31}, 32))
	if err != nil {
		t.Fatalf("semantic digester: %#v", err)
	}
	identities, err := audit.HMACIdentityKeyring(audit.HMACIdentityKey{KeyID: "rotation-identity", Key: bytes.Repeat([]byte{32}, 32), Active: true})
	if err != nil {
		t.Fatalf("identity keyring: %#v", err)
	}
	spec := audit.CatalogSpec{
		ID: "rotation.exact", Owner: "rotation.team", Generation: 1,
		Retention: audit.RetentionRules(audit.KeepForever("business.forever")),
		Semantics: semantic.Description(), Identities: identities.ActiveDescription(), Integrity: audit.IntegrityOnly(),
	}
	oldCatalog, err := audit.Compile(spec, old)
	if err != nil {
		t.Fatalf("compile retained catalog: %#v", err)
	}
	spec.Generation = 2
	spec.Previous = oldCatalog.Ref()
	currentCatalog, err := audit.Compile(spec, current...)
	if err != nil {
		t.Fatalf("compile active catalog: %#v", err)
	}
	oldCatalogs, err := audit.Lineage(oldCatalog)
	if err != nil {
		t.Fatalf("retained lineage: %#v", err)
	}
	currentCatalogs, err := audit.Lineage(currentCatalog, audit.Retain(oldCatalog))
	if err != nil {
		t.Fatalf("active lineage: %#v", err)
	}
	log, err := auditmemory.NewLog(auditmemory.LogSpec{})
	if err != nil {
		t.Fatalf("memory log: %#v", err)
	}
	deployment, err := auditmemory.NewDeployment(log)
	if err != nil {
		t.Fatalf("memory deployment: %#v", err)
	}
	change, err := audit.NewCatalogChangeRef("rotation.deploy", "exact-authority")
	if err != nil {
		t.Fatalf("change ref: %#v", err)
	}
	if err := deployment.InstallAndActivate(context.Background(), oldCatalogs, change); err != nil {
		t.Fatalf("activate retained catalog: %#v", err)
	}
	store, err := auditmemory.New(auditmemory.Spec{Log: log})
	if err != nil {
		t.Fatalf("memory store: %#v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return &exactRotationState{
		store: store, deployment: deployment, oldCatalog: oldCatalog, currentCatalog: currentCatalog,
		oldCatalogs: oldCatalogs, currentCatalogs: currentCatalogs, semantic: semantic, identities: identities, change: change,
	}
}

func (s *exactRotationState) activate(t *testing.T) {
	t.Helper()
	if err := s.deployment.InstallCatalog(context.Background(), s.currentCatalog.Manifest(), s.change); err != nil {
		t.Fatal(err)
	}
	proof, err := audit.NoAttemptCatalogActivation(s.oldCatalog.Ref(), s.currentCatalog.Ref())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.deployment.ActivateCatalog(context.Background(), s.oldCatalog.Ref(), s.currentCatalog.Ref(), s.change, proof); err != nil {
		t.Fatal(err)
	}
}

func exactRotationDeclaration(resource audit.Resource, action audit.Action, policy audit.ContextPolicy, golden string) *audit.EventType[exactRotationEvent] {
	return exactRotationDeclarationVersion(resource, action, policy, 1, golden)
}

func exactRotationDeclarationVersion(resource audit.Resource, action audit.Action, policy audit.ContextPolicy, version audit.PolicyVersion, golden string) *audit.EventType[exactRotationEvent] {
	return audit.Declare(audit.EventPolicy[exactRotationEvent]{
		Semantics: audit.Semantics(version, audit.PolicyGolden(audit.FixtureName("rotation.fixture_"+golden), strings.Repeat(golden, 32))),
		Descriptor: audit.Descriptor{
			Resource: resource, Action: action, Owner: "rotation.team", Purpose: "history.read",
			Retention: "business.forever", Consequence: audit.Required, Context: policy,
		},
		Target:     audit.EventTarget(func(value exactRotationEvent) audit.Reference { return audit.Reference(value.Target) }, audit.Public, audit.AsPlaintext),
		Outcome:    audit.EventOutcome(audit.Outcomes("recorded"), func(exactRotationEvent) audit.Outcome { return "recorded" }),
		OccurredAt: audit.EventOccurredAt(func(value exactRotationEvent) time.Time { return value.At }),
	})
}

func exactRotationResource(t *testing.T, resource audit.Resource, golden string) *audit.ResourcePolicy[exactRotationEntity, int64] {
	t.Helper()
	meta, err := crud.NewMeta[exactRotationEntity]("exact_rotation_entities")
	if err != nil {
		t.Fatalf("rotation entity metadata: %#v", err)
	}
	declared, err := audit.TryDefine(audit.Policy[exactRotationEntity, int64]{
		Model: meta, Semantics: audit.Semantics(1, audit.PolicyGolden(audit.FixtureName("rotation.fixture_"+golden), strings.Repeat(golden, 32))),
		Descriptor: audit.Descriptor{
			Resource: resource, Owner: "rotation.team", Purpose: "history.read",
			Retention: "business.forever", Consequence: audit.Required,
			Context: audit.ContextFacts(audit.GeneratedOperationFact(audit.Public, audit.AsPlaintext)),
		},
		Subject: audit.PlaintextSubject(func(id int64) string { return strconv.FormatInt(id, 10) }, audit.Public),
		Actions: audit.Actions(audit.EntityCreated),
		Fields:  audit.Fields[exactRotationEntity](audit.Value[exactRotationEntity]("Name", "name", audit.Text(), audit.Public)),
	})
	if err != nil {
		t.Fatalf("rotation entity declaration: %#v", err)
	}
	return declared
}

func recordExactRotation(t *testing.T, catalogs *audit.CatalogSet, store *auditmemory.Store, semantic audit.SemanticDigester, identities audit.IdentityKeyring, resolver audit.ContextResolver, event *audit.EventType[exactRotationEvent], target string) audit.Receipt {
	t.Helper()
	recorder := newExactRotationRecorder(t, catalogs, store, semantic, identities, resolver)
	draft, err := event.New(exactRotationEvent{Target: target, At: time.Unix(1_900_000_000, 0).UTC()})
	if err != nil {
		t.Fatalf("rotation event draft: %#v", err)
	}
	result, err := recorder.Record(context.Background(), draft)
	if err != nil {
		t.Fatalf("record rotation event: %#v", err)
	}
	receipt, ok := result.Receipt()
	if !ok {
		t.Fatal("rotation exact record has no receipt")
	}
	return receipt
}

func newExactRotationRecorder(t *testing.T, catalogs *audit.CatalogSet, store *auditmemory.Store, semantic audit.SemanticDigester, identities audit.IdentityKeyring, resolver audit.ContextResolver) *audit.Recorder {
	t.Helper()
	recorder, err := audit.New(audit.Config{Catalogs: catalogs, Writer: store, Context: resolver, Semantics: semantic, Identities: identities})
	if err != nil {
		t.Fatal(err)
	}
	return recorder
}

func newExactRotationHistory(t *testing.T, recorder *audit.Recorder, store *auditmemory.Store, calls *atomic.Int64) *audit.History {
	t.Helper()
	history, err := audit.NewHistory(audit.HistoryConfig{
		Profile: audit.PublicOnePageDevelopmentAlpha, Recorder: recorder, Log: store, Exact: store,
		Access: audit.AccessAuthorityFunc(func(_ context.Context, request audit.AccessRequest) (audit.AccessDecision, error) {
			calls.Add(1)
			view := request.View()
			grant := audit.AccessGrantSpec{
				Roles: []audit.Reference{view.Query.Role}, Catalogs: view.Catalogs,
				Resources: view.Target.Resources, Actions: view.Query.Actions,
				Fields: view.Query.Fields.Fields, Context: view.Query.Context.Facts,
				Classifications: []audit.Classification{audit.Public}, Direction: view.Query.Direction,
				ExpiresAt: time.Now().Add(time.Minute), MaxRevisions: view.Query.Limit, MaxPages: 1, MaxBytes: 1 << 20,
			}
			if view.Query.Scope.Kind == audit.ScopeExact {
				grant.Scopes = []audit.ScopedReference{view.Query.Scope.Reference}
			}
			return audit.AllowAccess(request, grant)
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	return history
}
