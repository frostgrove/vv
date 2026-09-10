package audit

import (
	"encoding/binary"
	"errors"
	"testing"
	"time"
)

func TestExactResultPreservesOrderCardinalityAndCopiesEvidence(t *testing.T) {
	query, stored, revisionTarget, itemTarget := exactStoreFixture(t)
	result, err := NewExactResult(query, ExactResultData{Entries: []ExactEntryData{
		{Target: revisionTarget, State: ExactFound, Revision: stored},
		{Target: itemTarget, State: ExactFound, Revision: stored},
	}})
	if err != nil {
		t.Fatal(err)
	}
	entries := result.Entries()
	entries[0].Target = itemTarget
	wire := entries[1].Revision.View()
	wire.Revision.Items[0].Action = "mutated.action"
	again := result.Entries()
	if again[0].Target != revisionTarget || again[1].Revision.View().Revision.Items[0].Action != "history.event.recorded" {
		t.Fatal("exact result aliases caller memory")
	}
	for name, data := range map[string]ExactResultData{
		"reordered": {Entries: []ExactEntryData{
			{Target: itemTarget, State: ExactFound, Revision: stored},
			{Target: revisionTarget, State: ExactFound, Revision: stored},
		}},
		"short": {Entries: []ExactEntryData{{Target: revisionTarget, State: ExactFound, Revision: stored}}},
		"missing with evidence": {Entries: []ExactEntryData{
			{Target: revisionTarget, State: ExactMissing, Revision: stored},
			{Target: itemTarget, State: ExactFound, Revision: stored},
		}},
		"found without evidence": {Entries: []ExactEntryData{
			{Target: revisionTarget, State: ExactFound},
			{Target: itemTarget, State: ExactFound, Revision: stored},
		}},
	} {
		if _, err := NewExactResult(query, data); !errors.Is(err, ErrIntegrity) {
			t.Fatalf("%s exact result = %v", name, err)
		}
	}
	replayedQuery, err := newExactQuery(query.View())
	if err != nil {
		t.Fatal(err)
	}
	if err := validateExactResult(replayedQuery, result); !errors.Is(err, ErrIntegrity) {
		t.Fatalf("cross-request exact result replay = %v", err)
	}
}

func TestExactResultRejectsForeignReferenceLogCatalogOrdinalAndBytes(t *testing.T) {
	query, stored, revisionTarget, itemTarget := exactStoreFixture(t)
	base := query.View()
	wrongTarget := revisionTarget
	wrongTarget.Revision.Revision[0]++
	if _, err := NewExactResult(query, ExactResultData{Entries: []ExactEntryData{
		{Target: wrongTarget, State: ExactFound, Revision: stored},
		{Target: base.Targets[1], State: ExactFound, Revision: stored},
	}}); !errors.Is(err, ErrIntegrity) {
		t.Fatalf("foreign result target = %v", err)
	}
	_, signer, _, _, original := signedHistoryEvidenceFixture(t)
	for name, mutate := range map[string]func(*RevisionWireView){
		"log":     func(view *RevisionWireView) { view.Header.Log[0]++ },
		"catalog": func(view *RevisionWireView) { view.Header.Catalog.Generation++ },
	} {
		foreign := resignedExactRevision(t, signer, original, mutate)
		foreignStored := exactStoredFixture(t, foreign)
		entries := []ExactEntryData{
			{Target: revisionTarget, State: ExactFound, Revision: foreignStored},
			{Target: base.Targets[1], State: ExactFound, Revision: stored},
		}
		if _, err := NewExactResult(query, ExactResultData{Entries: entries}); !errors.Is(err, ErrIntegrity) {
			t.Fatalf("foreign result %s = %v", name, err)
		}
	}
	ordinalTarget := ExactTargetView{Kind: ExactItemTarget, Item: ItemRef{Revision: revisionTarget.Revision, Ordinal: 1}}
	ordinalView := base
	ordinalView.Targets = []ExactTargetView{ordinalTarget}
	ordinalQuery, err := newExactQuery(ordinalView)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewExactResult(ordinalQuery, ExactResultData{Entries: []ExactEntryData{{Target: ordinalTarget, State: ExactFound, Revision: stored}}}); !errors.Is(err, ErrIntegrity) {
		t.Fatalf("out-of-range result ordinal = %v", err)
	}
	byteView := base
	byteView.Targets = []ExactTargetView{revisionTarget}
	byteView.MaxBytes = stored.EncodedBytes() - 1
	byteQuery, err := newExactQuery(byteView)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewExactResult(byteQuery, ExactResultData{Entries: []ExactEntryData{{Target: revisionTarget, State: ExactFound, Revision: stored}}}); !errors.Is(err, ErrIntegrity) {
		t.Fatalf("over-byte exact result = %v", err)
	}
	for name, mutate := range map[string]func(*ExactQueryView){
		"revision resource ceiling": func(view *ExactQueryView) {
			view.Targets = []ExactTargetView{revisionTarget}
			view.Resources = []Resource{"foreign.resource"}
		},
		"item action ceiling": func(view *ExactQueryView) {
			view.Targets = []ExactTargetView{itemTarget}
			view.Actions = []Action{"foreign.action"}
		},
		"scope ceiling": func(view *ExactQueryView) {
			view.Targets = []ExactTargetView{revisionTarget}
			view.Coordinates = []QueryCoordinateView{exactPlaintextCoordinate(QueryScope, []byte("tenant\x00south"))}
		},
	} {
		ceiling := base
		mutate(&ceiling)
		ceilingQuery, err := newExactQuery(ceiling)
		if err != nil {
			t.Fatalf("construct %s query: %v", name, err)
		}
		target := ceiling.Targets[0]
		if _, err := NewExactResult(ceilingQuery, ExactResultData{Entries: []ExactEntryData{{Target: target, State: ExactFound, Revision: stored}}}); !errors.Is(err, ErrIntegrity) {
			t.Fatalf("%s result = %v", name, err)
		}
	}
}

func TestExactQueryRejectsDuplicateAndOverBoundTargets(t *testing.T) {
	query, _, revisionTarget, _ := exactStoreFixture(t)
	duplicate := query.View()
	duplicate.Targets = []ExactTargetView{revisionTarget, revisionTarget}
	if _, err := newExactQuery(duplicate); !errors.Is(err, ErrInvalid) {
		t.Fatalf("duplicate exact targets = %v", err)
	}
	overBound := query.View()
	overBound.Targets = make([]ExactTargetView, MaxExactTargets+1)
	for index := range overBound.Targets {
		reference := revisionTarget.Revision
		binary.BigEndian.PutUint32(reference.Revision[:4], uint32(index+1))
		overBound.Targets[index] = ExactTargetView{Kind: ExactRevisionTarget, Revision: reference}
	}
	if _, err := newExactQuery(overBound); !errors.Is(err, ErrInvalid) {
		t.Fatalf("over-bound exact targets = %v", err)
	}
}

func exactStoreFixture(t *testing.T) (ExactQuery, StoredRevision, ExactTargetView, ExactTargetView) {
	t.Helper()
	catalogs, _, _, _, revision := signedHistoryEvidenceFixture(t)
	stored := exactStoredFixture(t, revision)
	reference := RevisionRef{Catalog: revision.Header.Catalog, Revision: revision.Header.RevisionID}
	revisionTarget := ExactTargetView{Kind: ExactRevisionTarget, Revision: reference}
	itemTarget := ExactTargetView{Kind: ExactItemTarget, Item: ItemRef{Revision: reference, Ordinal: 0}}
	query, err := newExactQuery(ExactQueryView{
		Log: revision.Header.Log, Catalogs: catalogRefs(catalogs), Targets: []ExactTargetView{revisionTarget, itemTarget},
		Resources: []Resource{"history.event"}, Actions: []Action{"history.event.recorded"},
		Classifications: []Classification{Public}, MaxBytes: MaxPageBytes,
	})
	if err != nil {
		t.Fatal(err)
	}
	return query, stored, revisionTarget, itemTarget
}

func exactStoredFixture(t *testing.T, revision RevisionWireView) StoredRevision {
	t.Helper()
	position, err := NewStorePosition([]byte{7})
	if err != nil {
		t.Fatal(err)
	}
	stored, err := NewStoredRevision(StoredRevisionData{
		Revision: revision, RecordedAt: time.Unix(2, 0).UTC(), Position: position,
	})
	if err != nil {
		t.Fatal(err)
	}
	return stored
}

func resignedExactRevision(t *testing.T, signer Signer, original RevisionWireView, mutate func(*RevisionWireView)) RevisionWireView {
	t.Helper()
	result := cloneRevisionView(original)
	mutate(&result)
	for index := range result.Items {
		result.Items[index].Leaf = leafDigestOf(result.Header.Log, result.Header.Catalog, result.Header.RevisionID, result.Items[index])
	}
	result.Header.Authorization = authorizationSummary(result.Items, result.Actors, result.Context)
	result.Header.Envelope = envelopeDigestOf(result)
	result.Header.Integrity = integrityDigestOf(result.Header)
	var err error
	result.Header.Seal, err = signer.Sign(t.Context(), result.Header.Integrity)
	if err != nil {
		t.Fatal(err)
	}
	return result
}
