package auditpg

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/frostgrove/vv/audit"
	"github.com/frostgrove/vv/crud/adapter/crudsql"
)

type searchOccurrence struct {
	Target string
	Value  string
	At     time.Time
}

type searchRow struct {
	revision   []byte
	catalog    string
	generation int64
	operation  string
	wire       []byte
	recordedAt sql.NullTime
	position   int64
	err        error
}

func (r searchRow) Scan(destinations ...any) error {
	if r.err != nil {
		return r.err
	}
	if len(destinations) != 7 {
		return errors.New("search row destination mismatch")
	}
	*destinations[0].(*[]byte) = bytes.Clone(r.revision)
	*destinations[1].(*string) = r.catalog
	*destinations[2].(*int64) = r.generation
	*destinations[3].(*string) = r.operation
	*destinations[4].(*[]byte) = bytes.Clone(r.wire)
	*destinations[5].(*sql.NullTime) = r.recordedAt
	*destinations[6].(*int64) = r.position
	return nil
}

type searchFixture struct {
	row    searchRow
	view   audit.RevisionWireView
	query  audit.StoreQueryView
	ready  readiness
	limits audit.LimitSpec
}

func TestSearchQueryValidationRejectsUnsupportedAndExpandingShapes(t *testing.T) {
	fixture := newSearchFixture(t)
	if err := validateSearchQuery(fixture.query, fixture.ready, fixture.limits); err != nil {
		t.Fatalf("valid query = %v", err)
	}
	predicate, arguments := searchPredicate(fixture.query)
	if predicate != "((catalog_id=$1 AND catalog_generation=$2))" || len(arguments) != 2 || arguments[0] != string(fixture.view.Header.Catalog.ID) || arguments[1] != int64(fixture.view.Header.Catalog.Generation) {
		t.Fatalf("catalog predicate = %q, %#v", predicate, arguments)
	}

	tests := []struct {
		name   string
		mutate func(*audit.StoreQueryView)
	}{
		{name: "wrong log", mutate: func(view *audit.StoreQueryView) { view.Log = audit.LogID{99} }},
		{name: "internal classification", mutate: func(view *audit.StoreQueryView) { view.Classifications = []audit.Classification{audit.Internal} }},
		{name: "duplicate action", mutate: func(view *audit.StoreQueryView) { view.Actions = append(view.Actions, view.Actions[0]) }},
		{name: "snapshot", mutate: func(view *audit.StoreQueryView) { view.Snapshot = []byte("forged") }},
		{name: "unsupported coordinate", mutate: func(view *audit.StoreQueryView) { view.Coordinates[1].Mode = audit.AsProtected }},
		{name: "class mismatch", mutate: func(view *audit.StoreQueryView) { view.Class = audit.ResourceHistoryQuery }},
		{name: "page overflow", mutate: func(view *audit.StoreQueryView) { view.Limit = fixture.limits.PageRevisions + 1 }},
	}
	for _, test := range tests {
		view := fixture.query
		view.Catalogs = append([]audit.CatalogRef(nil), view.Catalogs...)
		view.Resources = append([]audit.Resource(nil), view.Resources...)
		view.Actions = append([]audit.Action(nil), view.Actions...)
		view.Classifications = append([]audit.Classification(nil), view.Classifications...)
		view.Coordinates = cloneSearchCoordinates(view.Coordinates)
		test.mutate(&view)
		if err := validateSearchQuery(view, fixture.ready, fixture.limits); err == nil || err.Error() != "audit: store reported refused" {
			t.Fatalf("%s error = %v", test.name, err)
		}
	}
}

func TestSearchCandidateRejectsHostilePersistedEvidence(t *testing.T) {
	fixture := newSearchFixture(t)
	candidate, err := scanSearchCandidate(fixture.row, fixture.limits)
	if err != nil {
		t.Fatal(err)
	}
	if candidate.observed.RevisionID != fixture.view.Header.RevisionID || candidate.rawBytes != uint64(len(fixture.row.wire)) {
		t.Fatalf("candidate = %+v", candidate)
	}
	original := candidate.stored.View().Revision.Items[0].Target.Plaintext
	fixture.row.wire[0] ^= 0xff
	if !bytes.Equal(candidate.stored.View().Revision.Items[0].Target.Plaintext, original) {
		t.Fatal("stored candidate aliases database wire memory")
	}

	tests := []struct {
		name   string
		mutate func(*searchRow)
	}{
		{name: "invalid json", mutate: func(row *searchRow) { row.wire = []byte("{") }},
		{name: "metadata mismatch", mutate: func(row *searchRow) { row.revision[0] ^= 0xff }},
		{name: "missing recorded time", mutate: func(row *searchRow) { row.recordedAt = sql.NullTime{} }},
		{name: "invalid position", mutate: func(row *searchRow) { row.position = 0 }},
	}
	for _, test := range tests {
		row := fixture.row
		row.revision = bytes.Clone(fixture.row.revision)
		row.wire = bytes.Clone(fixture.row.wire)
		test.mutate(&row)
		if _, err := scanSearchCandidate(row, fixture.limits); err == nil || err.Error() != "audit: store reported corrupt" {
			t.Fatalf("%s error = %v", test.name, err)
		}
	}
}

func TestSearchMatcherNarrowsTheWholeRevisionAndExactCoordinates(t *testing.T) {
	fixture := newSearchFixture(t)
	if !matchesSearchQuery(fixture.query, fixture.view) {
		t.Fatal("valid exact public revision did not match")
	}
	wrongTarget := fixture.query
	wrongTarget.Coordinates = cloneSearchCoordinates(wrongTarget.Coordinates)
	wrongTarget.Coordinates[1].Alternatives[0].Plaintext = []byte("invoice:other")
	if matchesSearchQuery(wrongTarget, fixture.view) {
		t.Fatal("wrong exact target matched")
	}
	expanding := fixture.view
	expanding.Header.Authorization.Resources = append(expanding.Header.Authorization.Resources, "private.resource")
	if matchesSearchQuery(fixture.query, expanding) {
		t.Fatal("revision wider than the authorized resource ceiling matched")
	}
	private := fixture.view
	private.Items = append([]audit.ItemWireView(nil), fixture.view.Items...)
	private.Items[0].Target.Classification = audit.Internal
	if matchesSearchQuery(fixture.query, private) {
		t.Fatal("non-public exact target matched")
	}
}

func newSearchFixture(t *testing.T) searchFixture {
	t.Helper()
	ctx := context.Background()
	db, database := newStubDB(t)
	t.Cleanup(func() { _ = db.Close() })
	semantic, err := audit.HMACSemanticDigester("search-semantic", bytes.Repeat([]byte{31}, 32))
	if err != nil {
		t.Fatal(err)
	}
	identities, err := audit.HMACIdentityKeyring(audit.HMACIdentityKey{KeyID: "search-identity", Key: bytes.Repeat([]byte{32}, 32), Active: true})
	if err != nil {
		t.Fatal(err)
	}
	contextPolicy := audit.ContextFacts(
		audit.ScopeFact(audit.ContextRequired, audit.Provenances(audit.Verified), audit.Public, audit.AsPlaintext),
		audit.OperationFact(audit.ContextRequired, audit.Provenances(audit.Verified), audit.Public, audit.AsPlaintext),
	)
	event := audit.Declare(audit.EventPolicy[searchOccurrence]{
		Semantics: audit.Semantics(1, audit.PolicyGolden("auditpg.search.fixture", strings.Repeat("0e", 32))),
		Descriptor: audit.Descriptor{
			Resource: "billing.invoice", Action: "invoice.seen", Owner: "auditpg.team", Purpose: "history.read",
			Retention: "business.forever", Consequence: audit.Required, Context: contextPolicy,
		},
		Target:     audit.EventTarget(func(value searchOccurrence) audit.Reference { return audit.Reference(value.Target) }, audit.Public, audit.AsPlaintext),
		Outcome:    audit.EventOutcome(audit.Outcomes("accepted"), func(searchOccurrence) audit.Outcome { return "accepted" }),
		OccurredAt: audit.EventOccurredAt(func(value searchOccurrence) time.Time { return value.At }),
		Fields: audit.EventFields(
			audit.EventValue("value", func(value searchOccurrence) string { return value.Value }, audit.Text(), audit.Public),
		),
	})
	catalog, err := audit.Compile(audit.CatalogSpec{
		ID: "search.audit", Owner: "auditpg.team", Generation: 1,
		Retention: audit.RetentionRules(audit.KeepForever("business.forever")),
		Semantics: semantic.Description(), Identities: identities.ActiveDescription(), Integrity: audit.IntegrityOnly(),
	}, event)
	if err != nil {
		t.Fatal(err)
	}
	catalogs, err := audit.Lineage(catalog)
	if err != nil {
		t.Fatal(err)
	}
	database.active, database.set = catalog.Ref(), catalogs.Digest()
	store, err := New(Spec{DB: db, Source: crudsql.Postgres(db)})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Check(ctx); err != nil {
		t.Fatal(err)
	}
	scope, err := audit.NewContextValue(audit.ScopedReference{Scope: "tenant", Reference: "north"}, audit.Verified)
	if err != nil {
		t.Fatal(err)
	}
	operation, err := audit.NewContextValue(audit.OperationID{17}, audit.Verified)
	if err != nil {
		t.Fatal(err)
	}
	resolver, err := audit.StaticContext(audit.Context{Scope: scope, Operation: operation})
	if err != nil {
		t.Fatal(err)
	}
	recorder, err := audit.New(audit.Config{Catalogs: catalogs, Writer: store, Context: resolver, Semantics: semantic, Identities: identities})
	if err != nil {
		t.Fatal(err)
	}
	draft, err := event.New(searchOccurrence{Target: "invoice:a", Value: "value-a", At: time.Unix(1_900_000_000, 0).UTC()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := recorder.Record(ctx, draft); err != nil {
		t.Fatal(err)
	}
	database.mu.Lock()
	var record stubRevision
	for _, candidate := range database.revisions {
		record = candidate
	}
	database.mu.Unlock()
	view, err := decodeEvidence(record.wire)
	if err != nil {
		t.Fatal(err)
	}
	limits := store.Limits().View()
	return searchFixture{
		row: searchRow{
			revision: bytes.Clone(view.Header.RevisionID[:]), catalog: string(view.Header.Catalog.ID),
			generation: int64(view.Header.Catalog.Generation), operation: string(view.Header.Operation),
			wire: bytes.Clone(record.wire), recordedAt: sql.NullTime{Time: record.recordedAt, Valid: true}, position: record.position,
		},
		view: view,
		query: audit.StoreQueryView{
			Log: view.Header.Log, Catalogs: []audit.CatalogRef{view.Header.Catalog}, Class: audit.EventTargetHistoryQuery,
			Resources: []audit.Resource{view.Items[0].Resource}, Actions: []audit.Action{view.Items[0].Action},
			Classifications: []audit.Classification{audit.Public},
			Coordinates: []audit.QueryCoordinateView{
				{Kind: audit.QueryScope, Match: audit.QueryCoordinateExact, Mode: audit.AsPlaintext, Alternatives: []audit.QueryCoordinateAlternativeView{{Plaintext: []byte("tenant\x00north")}}},
				{Kind: audit.QueryTarget, Match: audit.QueryCoordinateExact, Mode: audit.AsPlaintext, Alternatives: []audit.QueryCoordinateAlternativeView{{Plaintext: []byte("invoice:a")}}},
			},
			Fields:    audit.FieldProjectionView{Kind: audit.ProjectionAll, Fields: []audit.FieldName{"value"}},
			Context:   audit.ContextProjectionView{Kind: audit.ProjectionAll, Facts: []audit.ContextFactKind{audit.ScopeContext, audit.OperationContext}},
			Direction: audit.OldestFirst, Limit: 10, MaxBytes: 1 << 20,
		},
		ready: *loadReadiness(&store.value.state), limits: limits,
	}
}

func cloneSearchCoordinates(input []audit.QueryCoordinateView) []audit.QueryCoordinateView {
	result := make([]audit.QueryCoordinateView, len(input))
	for index, coordinate := range input {
		result[index] = coordinate
		result[index].Alternatives = make([]audit.QueryCoordinateAlternativeView, len(coordinate.Alternatives))
		for alternativeIndex, alternative := range coordinate.Alternatives {
			result[index].Alternatives[alternativeIndex] = alternative
			result[index].Alternatives[alternativeIndex].Plaintext = bytes.Clone(alternative.Plaintext)
			result[index].Alternatives[alternativeIndex].Tokens = append([]audit.Token(nil), alternative.Tokens...)
		}
	}
	return result
}
