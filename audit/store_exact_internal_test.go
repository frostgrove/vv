package audit

import (
	"encoding/binary"
	"errors"
	"slices"
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

func TestExactAuthorityCarriesRetainedScopeAndRejectsRetiredResourceActionPairs(t *testing.T) {
	current := []DeclarationDescription{
		{Kind: EventDeclaration, Resource: "resource.one", Action: "action.one", TargetPresent: true, Target: SubjectDescription{Classification: Public, Mode: AsPlaintext}},
		{Kind: EventDeclaration, Resource: "resource.two", Action: "action.two", TargetPresent: true, Target: SubjectDescription{Classification: Public, Mode: AsPlaintext}},
	}
	ceiling, err := currentExactCeiling(current,
		[]Resource{"resource.one", "resource.two"},
		[]Action{"action.one", "action.two"},
		[]Classification{Public},
	)
	if err != nil {
		t.Fatal(err)
	}
	retained := cloneDeclarationDescription(current[0])
	retained.Action = "action.two"
	retained.Context = ContextPolicyDescription{Facts: []ContextFactDescription{{
		Kind: ScopeContext, Presence: ContextRequired, Allowed: []Provenance{Verified}, Classification: Public, Mode: AsPlaintext,
	}}}
	mergeRetainedExactScope(&ceiling, []DeclarationDescription{retained},
		[]Resource{"resource.one", "resource.two"},
		[]Action{"action.one", "action.two"},
	)
	if !ceiling.scopeDeclared || ceiling.scopeMode != AsPlaintext {
		t.Fatalf("retained scope boundary = (%v, %v)", ceiling.scopeDeclared, ceiling.scopeMode)
	}

	revision := RevisionWireView{Items: []ItemWireView{{Ordinal: 0, Kind: EventItem, Resource: "resource.one", Action: "action.two"}}}
	target := ExactTargetView{Kind: ExactRevisionTarget}
	grant := AccessGrantSpec{
		Resources: []Resource{"resource.one", "resource.two"},
		Actions:   []Action{"action.one", "action.two"},
	}
	if err := validateCurrentExactEvidence(current, target, grant, revision); !errors.Is(err, ErrRefused) {
		t.Fatalf("retired cross-pair evidence = %v", err)
	}
	revision.Items[0].Action = "action.one"
	if err := validateCurrentExactEvidence(current, target, grant, revision); err != nil {
		t.Fatalf("current resource/action evidence = %v", err)
	}
}

func TestExactAuthorityCannotBorrowFieldsOrContextAcrossCurrentPairs(t *testing.T) {
	operation := ContextFactDescription{Kind: OperationContext, Presence: ContextRequired, Allowed: []Provenance{ServerDerived}, Classification: Public, Mode: AsPlaintext}
	correlation := ContextFactDescription{Kind: CorrelationContext, Presence: ContextOptional, Allowed: []Provenance{Verified}, Classification: Public, Mode: AsPlaintext}
	name := FieldDescription{Name: "name", Codec: Text().Description(), Classification: Public, Mode: AsPlaintext}
	current := []DeclarationDescription{
		{Kind: EventDeclaration, Resource: "resource.one", Action: "action.shared", TargetPresent: true, Target: SubjectDescription{Classification: Public, Mode: AsPlaintext}, Context: ContextPolicyDescription{Facts: []ContextFactDescription{operation}}},
		{Kind: EventDeclaration, Resource: "resource.two", Action: "action.shared", TargetPresent: true, Target: SubjectDescription{Classification: Public, Mode: AsPlaintext}, Fields: []FieldDescription{name}, Context: ContextPolicyDescription{Facts: []ContextFactDescription{operation, correlation}}},
	}
	ceiling, err := currentExactCeiling(current,
		[]Resource{"resource.one", "resource.two"},
		[]Action{"action.shared"},
		[]Classification{Public},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(ceiling.fields) != 0 || !slices.Equal(ceiling.context, []ContextFactKind{OperationContext}) {
		t.Fatalf("cross-pair projection ceiling = fields:%v context:%v", ceiling.fields, ceiling.context)
	}

	target := ExactTargetView{Kind: ExactRevisionTarget}
	item := ItemWireView{Ordinal: 0, Kind: EventItem, Resource: "resource.one", Action: "action.shared"}
	fieldGrant := AccessGrantSpec{
		Resources: []Resource{"resource.one", "resource.two"}, Actions: []Action{"action.shared"},
		Fields: []FieldName{"name"}, Classifications: []Classification{Public},
	}
	revision := RevisionWireView{Items: []ItemWireView{item}}
	revision.Items[0].Values = []StoredValueView{{
		Field: "name", Codec: Text().Description(), Classification: Public, Mode: AsPlaintext,
		State: ValuePresent, Plaintext: []byte("retained"),
	}}
	if err := validateCurrentExactEvidence(current, target, fieldGrant, revision); !errors.Is(err, ErrRefused) {
		t.Fatalf("borrowed current field = %v", err)
	}

	contextGrant := AccessGrantSpec{
		Resources: []Resource{"resource.one", "resource.two"}, Actions: []Action{"action.shared"},
		Context: []ContextFactKind{CorrelationContext}, Classifications: []Classification{Public},
	}
	revision.Items[0].Values = nil
	revision.Context = []StoredContextFactView{{
		Kind: CorrelationContext, Provenance: Verified, Classification: Public, Mode: AsPlaintext,
		Plaintext: []byte("retained"),
	}}
	if err := validateCurrentExactEvidence(current, target, contextGrant, revision); !errors.Is(err, ErrRefused) {
		t.Fatalf("borrowed current context = %v", err)
	}
}

func TestExactAuthorityRejectsProvenanceRemovedByCurrentPair(t *testing.T) {
	current := []DeclarationDescription{{
		Kind: EventDeclaration, Resource: "resource.one", Action: "action.one",
		TargetPresent: true, Target: SubjectDescription{Classification: Public, Mode: AsPlaintext},
		Context: ContextPolicyDescription{Facts: []ContextFactDescription{
			{Kind: ActorChainContext, Presence: ContextOptional, Allowed: []Provenance{ServerDerived}, Classification: Public, Mode: AsPlaintext},
			{Kind: OperationContext, Presence: ContextOptional, Allowed: []Provenance{ServerDerived}, Classification: Public, Mode: AsPlaintext},
		}},
	}}
	item := ItemWireView{Ordinal: 0, Kind: EventItem, Resource: "resource.one", Action: "action.one"}
	grant := AccessGrantSpec{
		Resources: []Resource{"resource.one"}, Actions: []Action{"action.one"},
		Context: []ContextFactKind{ActorChainContext, OperationContext}, Classifications: []Classification{Public},
	}
	targets := []ExactTargetView{
		{Kind: ExactRevisionTarget},
		{Kind: ExactItemTarget, Item: ItemRef{Ordinal: 0}},
	}
	for _, target := range targets {
		revision := RevisionWireView{
			Items: []ItemWireView{item},
			Context: []StoredContextFactView{{
				Kind: OperationContext, Provenance: Verified, Classification: Public, Mode: AsPlaintext,
			}},
		}
		if err := validateCurrentExactEvidence(current, target, grant, revision); !errors.Is(err, ErrRefused) {
			t.Fatalf("removed context provenance for target %v = %v", target.Kind, err)
		}
		revision.Context = nil
		revision.Actors = []StoredActorView{{
			Ordinal: 0, Kind: HumanActor, Provenance: Verified, Classification: Public, Mode: AsPlaintext,
		}}
		if err := validateCurrentExactEvidence(current, target, grant, revision); !errors.Is(err, ErrRefused) {
			t.Fatalf("removed actor provenance for target %v = %v", target.Kind, err)
		}
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
