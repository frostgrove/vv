package audit

import (
	"errors"
	"testing"
	"time"
)

func TestMalformedAttemptArmBreaksRevisionIntegrity(t *testing.T) {
	view := validAttemptRevisionFixture()
	if _, err := newRevision(view); err != nil {
		t.Fatalf("valid attempt revision: %v", err)
	}
	for name, mutate := range map[string]func(*RevisionWireView){
		"foreign entity arm": func(view *RevisionWireView) {
			view.Items[0].EntityState = EntityFullState
		},
		"malformed target": func(view *RevisionWireView) {
			view.Items[0].Target = StoredValueView{State: ValuePresent, Classification: Public, Mode: AsPlaintext, Plaintext: []byte("target")}
			view.Items[0].Attempt.Result.TargetPresent = true
			view.Items[0].Attempt.Result.Target = AttemptTargetCommitment{1}
		},
		"zero transition bytes": func(view *RevisionWireView) {
			view.Items[0].Attempt.Result.TransitionBytes = 0
		},
		"foreign hold arm": func(view *RevisionWireView) {
			view.Items[0].Hold.Command = HoldPlaceCommand
		},
		"attempt arm on event": func(view *RevisionWireView) {
			view.Items[0].Kind = EventItem
		},
	} {
		candidate := cloneRevisionView(view)
		mutate(&candidate)
		if _, err := newRevision(candidate); !errors.Is(err, ErrMalformedEvidence) {
			t.Fatalf("%s error = %v", name, err)
		}
	}
}

func TestAttemptArmParticipatesInLeafEnvelopeAndIntegrity(t *testing.T) {
	view := validAttemptRevisionFixture()
	position, err := NewStorePosition([]byte{1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewStoredRevision(StoredRevisionData{Revision: view, RecordedAt: view.Header.ObservedAt, Position: position}); err != nil {
		t.Fatalf("valid stored attempt revision: %v", err)
	}
	for name, mutate := range map[string]func(*RevisionWireView){
		"transition": func(view *RevisionWireView) {
			view.Items[0].Attempt.Sequence++
		},
		"projection": func(view *RevisionWireView) {
			view.Items[0].Attempt.Result.TransitionBytes++
		},
		"type projection": func(view *RevisionWireView) {
			view.Items[0].Attempt.TypeResult.Unsettled++
		},
	} {
		candidate := cloneRevisionView(view)
		mutate(&candidate)
		if _, err := NewStoredRevision(StoredRevisionData{Revision: candidate, RecordedAt: view.Header.ObservedAt, Position: position}); err == nil {
			t.Fatalf("%s mutation retained integrity", name)
		}
	}
}

func validAttemptRevisionFixture() RevisionWireView {
	catalog := CatalogRef{ID: "attempt.catalog", Generation: 1, Digest: CatalogDigest{1}}
	revisionID := RevisionID{2}
	operationID := OperationID{3}
	policy := AttemptPolicyFingerprint{4}
	replay := AttemptReplayFingerprint{5}
	chain := AttemptChainID{6}
	owner := AttemptOwnerCommitment{7}
	ref := ItemRef{Revision: RevisionRef{Catalog: catalog, Revision: revisionID}}
	expires := time.Date(2050, 1, 1, 1, 0, 0, 0, time.UTC)
	next := AttemptProjectionNextView{
		Present: true, Chain: chain, Policy: policy, Replay: replay,
		Operation: "attempt.operation", OperationID: operationID, Owner: owner,
		State: AttemptOpenState, Sequence: 1, TransitionBytes: 1,
		Start: ref, Head: ref, ExpiresAt: expires,
	}
	typeExpected := AttemptTypeProjectionStateView{Catalog: catalog.ID, Operation: next.Operation, Policy: policy, Replay: replay}
	item := ItemWireView{
		Kind: AttemptItem, Resource: "attempt.resource", Action: AttemptStartedAction,
		Attempt: AttemptTransitionWireView{
			Chain: chain, Policy: policy, Replay: replay, Operation: next.Operation, OperationID: operationID,
			Start: ref, Sequence: 1, Kind: AttemptStartedTransition, ExpiresAt: expires,
			Result: next, TypeExpected: typeExpected,
			TypeResult: AttemptTypeProjectionNextView{Catalog: catalog.ID, Operation: next.Operation, Policy: policy, Replay: replay, Unsettled: 1, Head: ref},
		},
	}
	header := RevisionHeaderView{
		Format: revisionFormatV1, Log: LogID{8}, Catalog: catalog, CatalogSet: CatalogSetDigest{9},
		Operation: next.Operation, OperationID: operationID, RevisionID: revisionID,
		ObservedAt: time.Date(2050, 1, 1, 0, 0, 0, 0, time.UTC), Retention: "attempt.retention", Consequence: BestEffort,
		Semantic: SemanticDigest{10},
	}
	item.Leaf = leafDigestOf(header.Log, header.Catalog, header.RevisionID, item)
	header.Authorization = authorizationSummary([]ItemWireView{item}, nil, nil)
	view := RevisionWireView{Header: header, Items: []ItemWireView{item}}
	view.Header.Envelope = envelopeDigestOf(view)
	view.Header.Integrity = integrityDigestOf(view.Header)
	return view
}
