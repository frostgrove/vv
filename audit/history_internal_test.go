package audit

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

type historyIntegrityOccurrence struct{ At time.Time }

func historyIntegrityEvent() *EventType[historyIntegrityOccurrence] {
	return Declare(EventPolicy[historyIntegrityOccurrence]{
		Semantics: Semantics(1, PolicyGolden("history.fixture", strings.Repeat("01", 32))),
		Descriptor: Descriptor{
			Resource: "history.event", Action: "history.event.recorded", Owner: "history.team", Purpose: "history.read",
			Retention: "test.long", Consequence: Required,
		},
		Target: NoEventTarget[historyIntegrityOccurrence](),
		Outcome: EventOutcome(Outcomes("accepted"), func(historyIntegrityOccurrence) Outcome {
			return "accepted"
		}),
		OccurredAt: EventOccurredAt(func(value historyIntegrityOccurrence) time.Time { return value.At }),
	})
}

func TestHistoryVerifierInventoryCoversEverySignedRetainedManifest(t *testing.T) {
	event := historyIntegrityEvent()
	semantic, err := HMACSemanticDigester("history-semantic", bytes.Repeat([]byte{1}, 32))
	if err != nil {
		t.Fatal(err)
	}
	identities, err := HMACIdentityKeyring(HMACIdentityKey{KeyID: "history-identity", Key: bytes.Repeat([]byte{2}, 32), Active: true})
	if err != nil {
		t.Fatal(err)
	}
	retainedSigner, err := HMACSigner(HMACSigningKey{KeyID: "history-retained", Key: bytes.Repeat([]byte{3}, 32)})
	if err != nil {
		t.Fatal(err)
	}
	activeSigner, err := HMACSigner(HMACSigningKey{KeyID: "history-active", Key: bytes.Repeat([]byte{4}, 32)})
	if err != nil {
		t.Fatal(err)
	}
	genesis, err := Compile(CatalogSpec{
		ID: "history.audit", Owner: "history.team", Generation: 1,
		Retention: RetentionRules(KeepForever("test.long")), Semantics: semantic.Description(),
		Identities: identities.ActiveDescription(), Integrity: RequireSignature(retainedSigner.Description()),
	}, event)
	if err != nil {
		t.Fatal(err)
	}
	active, err := Compile(CatalogSpec{
		ID: "history.audit", Owner: "history.team", Generation: 2, Previous: genesis.Ref(),
		Retention: RetentionRules(KeepForever("test.long")), Semantics: semantic.Description(),
		Identities: identities.ActiveDescription(), Integrity: RequireSignature(activeSigner.Description()),
	}, event)
	if err != nil {
		t.Fatal(err)
	}
	catalogs, err := Lineage(active, Retain(genesis))
	if err != nil {
		t.Fatal(err)
	}
	activeOnly, _ := HMACVerifier(HMACVerificationKey{KeyID: "history-active", Key: bytes.Repeat([]byte{4}, 32)})
	if err := validateHistoryVerifier(catalogs, activeOnly); !errors.Is(err, ErrDeclaration) {
		t.Fatalf("verifier missing retained key = %v", err)
	}
	complete, _ := HMACVerifier(
		HMACVerificationKey{KeyID: "history-retained", Key: bytes.Repeat([]byte{3}, 32)},
		HMACVerificationKey{KeyID: "history-active", Key: bytes.Repeat([]byte{4}, 32)},
	)
	if err := validateHistoryVerifier(catalogs, complete); err != nil {
		t.Fatalf("complete retained verifier inventory = %v", err)
	}
}

func TestHistoryRejectsRecomputedUnkeyedEvidenceWithoutAValidRecordEraSeal(t *testing.T) {
	catalogs, signer, verifier, query, original := signedHistoryEvidenceFixture(t)
	valid := historyEvidencePage(t, query, original)
	if err := verifyHistoryEvidence(context.Background(), catalogs, verifier, valid); err != nil {
		t.Fatalf("valid signed evidence = %v", err)
	}

	mutated := cloneRevisionView(original)
	mutated.Items[0].Outcome = "substituted"
	mutated.Items[0].Leaf = leafDigestOf(mutated.Header.Log, mutated.Header.Catalog, mutated.Header.RevisionID, mutated.Items[0])
	mutated.Header.Authorization = authorizationSummary(mutated.Items, mutated.Actors, mutated.Context)
	mutated.Header.Envelope = envelopeDigestOf(mutated)
	mutated.Header.Integrity = integrityDigestOf(mutated.Header)
	if err := verifyHistoryEvidence(context.Background(), catalogs, verifier, historyEvidencePage(t, query, mutated)); !errors.Is(err, ErrIntegrity) {
		t.Fatalf("recomputed unkeyed evidence with stale seal = %v", err)
	}

	missing := cloneRevisionView(original)
	missing.Header.Seal = Seal{}
	if err := verifyHistoryEvidence(context.Background(), catalogs, verifier, historyEvidencePage(t, query, missing)); !errors.Is(err, ErrIntegrity) {
		t.Fatalf("signed evidence without a seal = %v", err)
	}

	bad := cloneRevisionView(original)
	bad.Header.Seal, _ = NewSeal(
		original.Header.Seal.Algorithm(), original.Header.Seal.Profile(), original.Header.Seal.KeyID(), bytes.Repeat([]byte{0xff}, 32),
	)
	if err := verifyHistoryEvidence(context.Background(), catalogs, verifier, historyEvidencePage(t, query, bad)); !errors.Is(err, ErrIntegrity) {
		t.Fatalf("signed evidence with a substituted seal = %v", err)
	}

	resigned := cloneRevisionView(mutated)
	var err error
	resigned.Header.Seal, err = signer.Sign(context.Background(), resigned.Header.Integrity)
	if err != nil {
		t.Fatal(err)
	}
	if err := verifyHistoryEvidence(context.Background(), catalogs, verifier, historyEvidencePage(t, query, resigned)); err != nil {
		t.Fatalf("validly resigned evidence = %v", err)
	}
}

func signedHistoryEvidenceFixture(t *testing.T) (*CatalogSet, Signer, Verifier, StoreQuery, RevisionWireView) {
	t.Helper()
	event := historyIntegrityEvent()
	semantic, err := HMACSemanticDigester("history-semantic", bytes.Repeat([]byte{1}, 32))
	if err != nil {
		t.Fatalf("construct semantic digester: %v", err)
	}
	identities, err := HMACIdentityKeyring(HMACIdentityKey{KeyID: "history-identity", Key: bytes.Repeat([]byte{2}, 32), Active: true})
	if err != nil {
		t.Fatalf("construct identity keyring: %v", err)
	}
	signer, err := HMACSigner(HMACSigningKey{KeyID: "history-signature", Key: bytes.Repeat([]byte{3}, 32)})
	if err != nil {
		t.Fatalf("construct signer: %v", err)
	}
	verifier, err := HMACVerifier(HMACVerificationKey{KeyID: "history-signature", Key: bytes.Repeat([]byte{3}, 32)})
	if err != nil {
		t.Fatalf("construct verifier: %v", err)
	}
	catalog, err := Compile(CatalogSpec{
		ID: "history.audit", Owner: "history.team", Generation: 1,
		Retention: RetentionRules(KeepForever("test.long")), Semantics: semantic.Description(),
		Identities: identities.ActiveDescription(), Integrity: RequireSignature(signer.Description()),
	}, event)
	if err != nil {
		t.Fatalf("compile signed history catalog: %v", err)
	}
	catalogs, err := Lineage(catalog)
	if err != nil {
		t.Fatalf("construct signed history lineage: %v", err)
	}
	item := ItemWireView{
		Kind: EventItem, Resource: "history.event", Action: "history.event.recorded", Outcome: "accepted",
	}
	header := RevisionHeaderView{
		Format: revisionFormatV1, Log: LogID{1}, Catalog: catalog.Ref(), CatalogSet: catalogs.Digest(),
		Operation: "history.event.recorded", OperationID: OperationID{2}, RevisionID: RevisionID{3},
		ObservedAt: time.Unix(1, 0).UTC(), Retention: "test.long", Consequence: Required, Semantic: SemanticDigest{4},
	}
	item.Leaf = leafDigestOf(header.Log, header.Catalog, header.RevisionID, item)
	header.Authorization = authorizationSummary([]ItemWireView{item}, nil, nil)
	revision := RevisionWireView{Header: header, Items: []ItemWireView{item}}
	revision.Header.Envelope = envelopeDigestOf(revision)
	revision.Header.Integrity = integrityDigestOf(revision.Header)
	revision.Header.Seal, err = signer.Sign(context.Background(), revision.Header.Integrity)
	if err != nil {
		t.Fatal(err)
	}
	query := newStoreQuery(StoreQueryView{
		Log: header.Log, Catalogs: []CatalogRef{header.Catalog}, Class: ResourceHistoryQuery,
		Resources: []Resource{item.Resource}, Actions: []Action{item.Action}, Classifications: []Classification{Public},
		Fields: FieldProjectionView{Kind: ProjectionNone}, Context: ContextProjectionView{Kind: ProjectionNone},
		Direction: OldestFirst, Limit: 1, MaxBytes: MaxPageBytes,
	})
	return catalogs, signer, verifier, query, revision
}

func historyEvidencePage(t *testing.T, query StoreQuery, revision RevisionWireView) StoredPage {
	t.Helper()
	position, err := NewStorePosition([]byte{1})
	if err != nil {
		t.Fatal(err)
	}
	stored, err := NewStoredRevision(StoredRevisionData{
		Revision: revision, RecordedAt: time.Unix(2, 0).UTC(), Position: position,
	})
	if err != nil {
		t.Fatal(err)
	}
	page, err := NewStoredPage(query, StoredPageData{
		Revisions: []StoredRevision{stored}, Position: position,
		Progress: SearchProgressView{Pages: 1, Revisions: 1, Bytes: stored.EncodedBytes()},
	})
	if err != nil {
		t.Fatal(err)
	}
	return page
}
