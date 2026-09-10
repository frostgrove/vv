package auditmemory_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/frostgrove/vv/audit"
	"github.com/frostgrove/vv/audit/auditmemory"
)

func TestCatalogDeploymentRecordsExactImmutableMutationHistory(t *testing.T) {
	ctx := context.Background()
	first, second, _, _, _ := memoryCatalogLineage(t, "invoice.sent")
	conflicting, _, _, _, _ := memoryCatalogLineage(t, "invoice.canceled")
	log, err := auditmemory.NewLog(auditmemory.LogSpec{})
	if err != nil {
		t.Fatal(err)
	}
	deployment, err := auditmemory.NewDeployment(log)
	if err != nil {
		t.Fatal(err)
	}
	if err := deployment.InstallCatalog(ctx, first.Manifest(), audit.CatalogChangeRef{}); !storeOutcome(err, audit.Refused) {
		t.Fatalf("zero install change = %v", err)
	}
	installFirst := memoryChange(t, "install-first")
	installSecond := memoryChange(t, "install-second")
	activateFirst := memoryChange(t, "activate-first")
	activateSecond := memoryChange(t, "activate-second")
	if err := deployment.InstallCatalog(ctx, first.Manifest(), installFirst); err != nil {
		t.Fatal(err)
	}
	if err := deployment.InstallCatalog(ctx, first.Manifest(), installFirst); err != nil {
		t.Fatalf("exact install replay: %v", err)
	}
	if err := deployment.InstallCatalog(ctx, first.Manifest(), installSecond); !storeOutcome(err, audit.Conflict) {
		t.Fatalf("install change conflict = %v", err)
	}
	if err := deployment.InstallCatalog(ctx, conflicting.Manifest(), installFirst); !storeOutcome(err, audit.Conflict) {
		t.Fatalf("same generation conflict = %v", err)
	}
	if err := deployment.InstallCatalog(ctx, second.Manifest(), installSecond); err != nil {
		t.Fatal(err)
	}
	firstProof, err := audit.NoAttemptCatalogActivation(audit.CatalogRef{}, first.Ref())
	if err != nil {
		t.Fatal(err)
	}
	secondProof, err := audit.NoAttemptCatalogActivation(first.Ref(), second.Ref())
	if err != nil {
		t.Fatal(err)
	}
	if err := deployment.ActivateCatalog(ctx, audit.CatalogRef{}, first.Ref(), audit.CatalogChangeRef{}, firstProof); !storeOutcome(err, audit.Refused) {
		t.Fatalf("zero activation change = %v", err)
	}
	if err := deployment.ActivateCatalog(ctx, audit.CatalogRef{}, first.Ref(), activateFirst, firstProof); err != nil {
		t.Fatal(err)
	}
	if err := deployment.ActivateCatalog(ctx, audit.CatalogRef{}, first.Ref(), activateFirst, firstProof); err != nil {
		t.Fatalf("exact activation replay: %v", err)
	}
	if err := deployment.ActivateCatalog(ctx, audit.CatalogRef{}, first.Ref(), activateSecond, firstProof); !storeOutcome(err, audit.Conflict) {
		t.Fatalf("activation change conflict = %v", err)
	}
	if err := deployment.ActivateCatalog(ctx, first.Ref(), second.Ref(), activateSecond, secondProof); err != nil {
		t.Fatal(err)
	}
	if err := deployment.ActivateCatalog(ctx, audit.CatalogRef{}, first.Ref(), activateFirst, firstProof); err != nil {
		t.Fatalf("completed ancestor activation replay: %v", err)
	}
	want := []audit.CatalogMutationView{
		audit.CatalogInstalled(first.Ref(), installFirst),
		audit.CatalogInstalled(second.Ref(), installSecond),
		audit.CatalogActivated(audit.CatalogRef{}, first.Ref(), activateFirst, firstProof),
		audit.CatalogActivated(first.Ref(), second.Ref(), activateSecond, secondProof),
	}
	mutations, err := deployment.CatalogMutations(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := audit.VerifyCatalogMutations(deployment, want, mutations); err != nil {
		t.Fatalf("verify deployment mutations: %v", err)
	}
	read := mutations.Mutations()
	read[0] = audit.CatalogMutationView{}
	again, err := deployment.CatalogMutations(ctx)
	if err != nil || again.Mutations()[0].Catalog != first.Ref() {
		t.Fatalf("mutation read aliases store state: %v", err)
	}
	lineage, err := audit.Lineage(second, audit.Retain(first))
	if err != nil {
		t.Fatal(err)
	}
	if err := deployment.VerifyCatalogs(ctx, lineage.Manifests()); err != nil {
		t.Fatalf("verify catalogs: %v", err)
	}
	store, err := auditmemory.New(auditmemory.Spec{Log: log})
	if err != nil {
		t.Fatal(err)
	}
	storeMutations, err := store.CatalogMutations(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := audit.VerifyCatalogMutations(store, want, storeMutations); err != nil {
		t.Fatalf("verify runtime mutation readback: %v", err)
	}
}

func TestSearchRefusesCandidateCohortsBeyondConfiguredBounds(t *testing.T) {
	tests := []struct {
		name      string
		revisions uint32
		bytes     uint64
		records   int
	}{
		{name: "revisions", revisions: 2, bytes: 4 << 20, records: 3},
		{name: "bytes", revisions: 10, bytes: 1, records: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			first, _, event, operation, runtime := memoryCatalogLineage(t, "invoice.sent")
			catalogs, err := audit.Lineage(first)
			if err != nil {
				t.Fatal(err)
			}
			limits := memoryLimitSpec(test.revisions, test.bytes)
			log, err := auditmemory.NewLog(auditmemory.LogSpec{Limits: limits})
			if err != nil {
				t.Fatal(err)
			}
			deployment, err := auditmemory.NewDeployment(log)
			if err != nil {
				t.Fatal(err)
			}
			if err := deployment.InstallAndActivate(ctx, catalogs, memoryChange(t, "bounded-search")); err != nil {
				t.Fatal(err)
			}
			store, err := auditmemory.New(auditmemory.Spec{Log: log})
			if err != nil {
				t.Fatal(err)
			}
			operationFact, err := audit.NewContextValue(audit.OperationID{7}, audit.ServerDerived)
			if err != nil {
				t.Fatal(err)
			}
			resolver, err := audit.StaticContext(audit.Context{Operation: operationFact})
			if err != nil {
				t.Fatal(err)
			}
			recorder, err := audit.New(audit.Config{
				Catalogs: catalogs, Writer: store, Context: resolver,
				Semantics: runtime.semantic, Identities: runtime.identities,
			})
			if err != nil {
				t.Fatal(err)
			}
			for index := range test.records {
				draft, draftErr := event.New(memoryHistoryEvent{
					Target: "invoice:a", Value: fmt.Sprintf("value-%d", index), At: time.Unix(int64(index+1), 0).UTC(),
				})
				if draftErr != nil {
					t.Fatal(draftErr)
				}
				if _, recordErr := recorder.Record(ctx, draft, audit.InOperation(operation)); recordErr != nil {
					t.Fatal(recordErr)
				}
			}
			history, err := audit.NewHistory(audit.HistoryConfig{
				Profile: audit.PublicOnePageDevelopmentAlpha, Recorder: recorder, Log: store,
				Access: audit.AccessAuthorityFunc(func(_ context.Context, request audit.AccessRequest) (audit.AccessDecision, error) {
					return allowMemoryRequested(request)
				}),
			})
			if err != nil {
				t.Fatal(err)
			}
			query := audit.Query{
				Purpose: "history.read", Role: "auditor", Fields: audit.AllFields(), Context: audit.AllContext(),
				Direction: audit.OldestFirst, Limit: 10,
			}
			if _, err := event.History(history).Events(ctx, query); !errors.Is(err, audit.ErrRefused) {
				t.Fatalf("oversized cohort = %v", err)
			}
		})
	}
}

type memoryCatalogRuntime struct {
	semantic   audit.SemanticDigester
	identities audit.IdentityKeyring
}

type memoryHistoryEvent struct {
	Target string
	Value  string
	At     time.Time
}

func memoryCatalogLineage(t *testing.T, action audit.Action) (*audit.Catalog, *audit.Catalog, *audit.EventType[memoryHistoryEvent], *audit.OperationType, memoryCatalogRuntime) {
	t.Helper()
	policy := audit.ContextFacts(
		audit.OperationFact(audit.ContextRequired, audit.Provenances(audit.ServerDerived), audit.Public, audit.AsPlaintext),
	)
	event := audit.Declare(audit.EventPolicy[memoryHistoryEvent]{
		Semantics: audit.Semantics(1, audit.PolicyGolden(audit.FixtureName(action), strings.Repeat("08", 32))),
		Descriptor: audit.Descriptor{
			Resource: "billing.invoice", Action: action, Owner: "history.team", Purpose: "history.read",
			Retention: "business.forever", Consequence: audit.Required, Context: policy,
		},
		Target: audit.EventTarget(func(value memoryHistoryEvent) audit.Reference { return audit.Reference(value.Target) }, audit.Public, audit.AsPlaintext),
		Outcome: audit.EventOutcome(audit.Outcomes("accepted"), func(memoryHistoryEvent) audit.Outcome {
			return "accepted"
		}),
		OccurredAt: audit.EventOccurredAt(func(value memoryHistoryEvent) time.Time { return value.At }),
		Fields: audit.EventFields(
			audit.EventValue("value", func(value memoryHistoryEvent) string { return value.Value }, audit.Text(), audit.Public),
		),
	})
	operation := audit.DeclareOperation(audit.OperationPolicy{
		Name: "billing.invoice.process", Semantics: audit.Semantics(1), Retention: "business.forever",
		Consequence: audit.Required, Context: policy, Members: audit.OperationMembers(event),
	})
	semantic, err := audit.HMACSemanticDigester("history-semantic", bytes.Repeat([]byte{1}, 32))
	if err != nil {
		t.Fatal(err)
	}
	identities, err := audit.HMACIdentityKeyring(audit.HMACIdentityKey{KeyID: "history-identity", Key: bytes.Repeat([]byte{2}, 32), Active: true})
	if err != nil {
		t.Fatal(err)
	}
	spec := audit.CatalogSpec{
		ID: "history.audit", Owner: "history.team", Generation: 1,
		Retention: audit.RetentionRules(audit.KeepForever("business.forever")),
		Semantics: semantic.Description(), Identities: identities.ActiveDescription(), Integrity: audit.IntegrityOnly(),
	}
	first, err := audit.Compile(spec, event, operation)
	if err != nil {
		t.Fatal(err)
	}
	spec.Generation = 2
	spec.Previous = first.Ref()
	second, err := audit.Compile(spec, operation, event)
	if err != nil {
		t.Fatal(err)
	}
	return first, second, event, operation, memoryCatalogRuntime{semantic: semantic, identities: identities}
}

func allowMemoryRequested(request audit.AccessRequest) (audit.AccessDecision, error) {
	view := request.View()
	grant := audit.AccessGrantSpec{
		Roles: []audit.Reference{view.Query.Role}, Catalogs: view.Catalogs,
		Resources: view.Target.Resources, Actions: view.Query.Actions,
		Fields: view.Query.Fields.Fields, Context: view.Query.Context.Facts,
		Classifications: []audit.Classification{audit.Public}, Direction: view.Query.Direction,
		ExpiresAt:    time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC),
		MaxRevisions: view.Query.Limit, MaxPages: 1, MaxBytes: 2 << 20,
	}
	return audit.AllowAccess(request, grant)
}

func memoryLimitSpec(searchRevisions uint32, searchBytes uint64) audit.LimitSpec {
	return audit.LimitSpec{
		RevisionBytes: 1 << 20, AppendRequestBytes: 2 << 20,
		PageRevisions: 100, PageBytes: 2 << 20, ExactTargets: 100, ExactBytes: 2 << 20,
		PositionBytes: 128, InventoryCandidates: 100, InventoryCohorts: 100,
		InventoryBytes: 2 << 20, SnapshotBytes: 1024,
		SearchCohortRevisions: searchRevisions, SearchCohortBytes: searchBytes,
		AttemptTransitions: 32, AttemptStateBytes: 1 << 20, AttemptOpenLifetime: 24 * time.Hour,
	}
}

func memoryChange(t *testing.T, name string) audit.CatalogChangeRef {
	t.Helper()
	change, err := audit.NewCatalogChangeRef("memory.deploy", audit.DeploymentChange(name))
	if err != nil {
		t.Fatal(err)
	}
	return change
}

func storeOutcome(err error, outcome audit.StoreOutcome) bool {
	return err != nil && err.Error() == "audit: store reported "+outcome.String()
}

var _ audit.CatalogAdmin = (*auditmemory.Deployment)(nil)
var _ audit.CatalogMutationLogReader = (*auditmemory.Store)(nil)
