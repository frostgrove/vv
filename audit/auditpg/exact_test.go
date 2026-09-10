package auditpg

import (
	"bytes"
	"context"
	"math"
	"slices"
	"testing"

	"github.com/frostgrove/vv/audit"
	"github.com/frostgrove/vv/crud/adapter/crudsql"
)

func TestExactQueryValidationKeepsStoreAndDisclosureBounds(t *testing.T) {
	fixture := newSearchFixture(t)
	view, candidate := exactPGFixture(t, fixture)
	if err := validateExactPGQuery(view, fixture.ready, fixture.limits); err != nil {
		t.Fatalf("valid exact query = %v", err)
	}
	if !exactPGCovered(view, view.Targets[0], view.Targets[0].Revision, candidate.stored.View().Revision) {
		t.Fatal("valid exact revision did not satisfy its ceilings")
	}
	tests := []struct {
		name   string
		mutate func(*audit.ExactQueryView, *audit.LimitSpec)
	}{
		{name: "wrong log", mutate: func(view *audit.ExactQueryView, _ *audit.LimitSpec) { view.Log = audit.LogID{99} }},
		{name: "duplicate resource", mutate: func(view *audit.ExactQueryView, _ *audit.LimitSpec) {
			view.Resources = append(view.Resources, view.Resources[0])
		}},
		{name: "invalid classification", mutate: func(view *audit.ExactQueryView, _ *audit.LimitSpec) { view.Classifications = []audit.Classification{0} }},
		{name: "duplicate target", mutate: func(view *audit.ExactQueryView, _ *audit.LimitSpec) {
			view.Targets = append(view.Targets, view.Targets[0])
		}},
		{name: "target limit", mutate: func(view *audit.ExactQueryView, limits *audit.LimitSpec) {
			missing := view.Targets[0]
			missing.Revision.Revision[0] ^= 0xff
			view.Targets = append(view.Targets, missing)
			limits.ExactTargets = 1
		}},
		{name: "foreign target catalog", mutate: func(view *audit.ExactQueryView, _ *audit.LimitSpec) {
			view.Targets[0].Revision.Catalog.Digest[0] ^= 0xff
		}},
		{name: "postgres generation overflow", mutate: func(view *audit.ExactQueryView, _ *audit.LimitSpec) {
			view.Catalogs[0].Generation = audit.CatalogGeneration(math.MaxInt64) + 1
			view.Targets[0].Revision.Catalog = view.Catalogs[0]
		}},
		{name: "protected scope", mutate: func(view *audit.ExactQueryView, _ *audit.LimitSpec) { view.Coordinates[0].Mode = audit.AsProtected }},
		{name: "malformed scope", mutate: func(view *audit.ExactQueryView, _ *audit.LimitSpec) {
			view.Coordinates[0].Alternatives[0].Plaintext = []byte("tenant-only")
		}},
		{name: "byte limit", mutate: func(view *audit.ExactQueryView, limits *audit.LimitSpec) { view.MaxBytes = limits.ExactBytes + 1 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mutated := cloneExactPGView(view)
			limits := fixture.limits
			test.mutate(&mutated, &limits)
			if err := validateExactPGQuery(mutated, fixture.ready, limits); err == nil || err.Error() != "audit: store reported refused" {
				t.Fatalf("validation error = %v", err)
			}
		})
	}
}

func TestExactEntriesPreserveTargetOrderAndRefuseTruncation(t *testing.T) {
	fixture := newSearchFixture(t)
	view, candidate := exactPGFixture(t, fixture)
	foundItem := audit.ExactTargetView{Kind: audit.ExactItemTarget, Item: audit.ItemRef{Revision: view.Targets[0].Revision, Ordinal: 0}}
	missing := view.Targets[0]
	missing.Revision.Revision[0] ^= 0xff
	view.Targets = []audit.ExactTargetView{foundItem, missing, view.Targets[0]}
	candidates := map[audit.RevisionID]searchCandidate{candidate.observed.RevisionID: candidate}
	entries, err := exactPGEntries(view, candidates)
	if err != nil {
		t.Fatal(err)
	}
	wantStates := []audit.ExactEntryState{audit.ExactFound, audit.ExactMissing, audit.ExactFound}
	gotStates := make([]audit.ExactEntryState, len(entries))
	for index, entry := range entries {
		gotStates[index] = entry.State
		if entry.Target != view.Targets[index] {
			t.Fatalf("entry %d target = %+v, want %+v", index, entry.Target, view.Targets[index])
		}
	}
	if !slices.Equal(gotStates, wantStates) {
		t.Fatalf("entry states = %v, want %v", gotStates, wantStates)
	}
	view.MaxBytes = candidate.stored.EncodedBytes() - 1
	if _, err := exactPGEntries(view, candidates); err == nil || err.Error() != "audit: store reported refused" {
		t.Fatalf("undersized exact result = %v", err)
	}

	uncovered := cloneExactPGView(view)
	uncovered.MaxBytes = fixture.limits.ExactBytes
	uncovered.Resources = []audit.Resource{"other.resource"}
	entries, err = exactPGEntries(uncovered, candidates)
	if err != nil || entries[0].State != audit.ExactMissing || entries[2].State != audit.ExactMissing {
		t.Fatalf("resource ceiling mismatch = (%+v, %v)", entries, err)
	}
	wrongScope := cloneExactPGView(view)
	wrongScope.MaxBytes = fixture.limits.ExactBytes
	wrongScope.Coordinates[0].Alternatives[0].Plaintext = []byte("tenant\x00south")
	entries, err = exactPGEntries(wrongScope, candidates)
	if err != nil || entries[0].State != audit.ExactMissing || entries[2].State != audit.ExactMissing {
		t.Fatalf("scope mismatch = (%+v, %v)", entries, err)
	}
}

func TestExactAdapterRejectsUnoriginatedQueriesAndHidesContextValues(t *testing.T) {
	database, state := newStubDB(t)
	defer database.Close()
	state.active = audit.CatalogRef{ID: "exact.audit", Generation: 1, Digest: audit.CatalogDigest{1}}
	state.set = audit.CatalogSetDigest{1}
	store, err := New(Spec{DB: database, Source: crudsql.Postgres(database)})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	if store.Capabilities().View().ExactInspection != audit.SupportSupported {
		t.Fatal("PostgreSQL store did not advertise exact inspection")
	}
	if _, err := store.Inspect(context.Background(), audit.ExactQuery{}); err == nil || err.Error() != "audit: store reported refused" {
		t.Fatalf("unoriginated empty query = %v", err)
	}
	if _, err := store.Inspect(nil, audit.ExactQuery{}); err == nil || err.Error() != "audit: store reported refused" {
		t.Fatalf("nil exact context = %v", err)
	}
	type secretKey struct{}
	parent := context.WithValue(context.Background(), secretKey{}, "secret")
	wrapped := exactReadContext{Context: parent}
	if wrapped.Value(secretKey{}) != nil {
		t.Fatal("exact store context exposed caller values")
	}
	canceled, cancel := context.WithCancel(parent)
	cancel()
	if err := (exactReadContext{Context: canceled}).Err(); err != context.Canceled {
		t.Fatalf("exact context cancellation = %v", err)
	}
}

func TestExactPredicatesDeduplicateRevisionLookups(t *testing.T) {
	fixture := newSearchFixture(t)
	view, _ := exactPGFixture(t, fixture)
	revision := view.Targets[0].Revision
	view.Targets = append(view.Targets, audit.ExactTargetView{Kind: audit.ExactItemTarget, Item: audit.ItemRef{Revision: revision, Ordinal: 0}})
	ids := exactPGRevisionIDs(view.Targets)
	if len(ids) != 1 || ids[0] != revision.Revision {
		t.Fatalf("deduplicated revision IDs = %v", ids)
	}
	predicate, arguments := exactRevisionPredicate(ids)
	if predicate != "revision_id IN ($1)" || len(arguments) != 1 || !bytes.Equal(arguments[0].([]byte), revision.Revision[:]) {
		t.Fatalf("revision predicate = %q %#v", predicate, arguments)
	}
	catalogPredicate, catalogArguments := exactCatalogPredicate(view.Catalogs)
	if catalogPredicate != "((catalog_id=$1 AND generation=$2 AND digest=$3))" || len(catalogArguments) != 3 {
		t.Fatalf("catalog predicate = %q %#v", catalogPredicate, catalogArguments)
	}
}

func exactPGFixture(t *testing.T, fixture searchFixture) (audit.ExactQueryView, searchCandidate) {
	t.Helper()
	candidate, err := scanSearchCandidate(fixture.row, fixture.limits)
	if err != nil {
		t.Fatal(err)
	}
	reference := audit.RevisionRef{Catalog: fixture.view.Header.Catalog, Revision: fixture.view.Header.RevisionID}
	return audit.ExactQueryView{
		Log: fixture.view.Header.Log, Catalogs: []audit.CatalogRef{fixture.view.Header.Catalog},
		Targets:   []audit.ExactTargetView{{Kind: audit.ExactRevisionTarget, Revision: reference}},
		Resources: []audit.Resource{fixture.view.Items[0].Resource}, Actions: []audit.Action{fixture.view.Items[0].Action},
		Classifications: []audit.Classification{audit.Public}, Coordinates: cloneSearchCoordinates(fixture.query.Coordinates[:1]),
		MaxBytes: fixture.limits.ExactBytes,
	}, candidate
}

func cloneExactPGView(view audit.ExactQueryView) audit.ExactQueryView {
	view.Catalogs = slices.Clone(view.Catalogs)
	view.Targets = slices.Clone(view.Targets)
	view.Resources = slices.Clone(view.Resources)
	view.Actions = slices.Clone(view.Actions)
	view.Classifications = slices.Clone(view.Classifications)
	view.Coordinates = cloneSearchCoordinates(view.Coordinates)
	return view
}
