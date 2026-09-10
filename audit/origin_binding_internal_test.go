package audit

import (
	"errors"
	"testing"
)

func TestStorePageCannotBeReplayedAcrossEquivalentQueries(t *testing.T) {
	_, stored, _, _ := exactStoreFixture(t)
	revision := stored.View().Revision
	view := StoreQueryView{
		Log: revision.Header.Log, Catalogs: []CatalogRef{revision.Header.Catalog},
		Resources: []Resource{revision.Items[0].Resource}, Actions: []Action{revision.Items[0].Action},
		Classifications: []Classification{Public}, Direction: OldestFirst, Limit: 1, MaxBytes: MaxPageBytes,
	}
	query := newStoreQuery(view)
	page, err := NewStoredPage(query, StoredPageData{
		Revisions: []StoredRevision{stored}, Position: stored.View().Position,
		Progress: SearchProgressView{Pages: 1, Revisions: 1, Bytes: stored.EncodedBytes()},
	})
	if err != nil {
		t.Fatal(err)
	}
	replayed := newStoreQuery(query.View())
	if err := validateStoredPage(replayed, page); !errors.Is(err, ErrIntegrity) {
		t.Fatalf("cross-request stored page replay = %v", err)
	}
}

func TestAccessDecisionCannotBeReplayedAcrossEquivalentRequests(t *testing.T) {
	request := newAccessRequest(AccessRequestView{Intent: HistoryDisclosureAccess})
	decision, err := DenyAccess(request, "history.denied")
	if err != nil {
		t.Fatal(err)
	}
	replayed := newAccessRequest(request.View())
	if decision.value.origin == replayed.value.origin {
		t.Fatal("cross-request access decision retained authority")
	}
}
