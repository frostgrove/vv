package audit

import (
	"bytes"
	"context"
	"testing"
)

func TestEntityIdentityNamespaceSurvivesCatalogGenerations(t *testing.T) {
	first := CatalogRef{ID: "application", Generation: 1, Digest: CatalogDigest{1}}
	second := CatalogRef{ID: "application", Generation: 2, Digest: CatalogDigest{2}}
	other := CatalogRef{ID: "other", Generation: 1, Digest: CatalogDigest{1}}
	description := IdentityCommitmentDescription{Algorithm: HMACSHA256Algorithm, Profile: HMACIdentityProfileV1, KeyID: "identity"}
	scope := EvidenceScopeCommitment{1}

	left, err := identitySubjectRequest(first, "users", scope, true, "user:42", []IdentityCommitmentDescription{description})
	if err != nil {
		t.Fatal(err)
	}
	right, err := identitySubjectRequest(second, "users", scope, true, "user:42", []IdentityCommitmentDescription{description})
	if err != nil {
		t.Fatal(err)
	}
	isolated, err := identitySubjectRequest(other, "users", scope, true, "user:42", []IdentityCommitmentDescription{description})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(left.AAD(), right.AAD()) {
		t.Fatal("entity identity namespace changed across catalog generations")
	}
	if bytes.Equal(left.AAD(), isolated.AAD()) {
		t.Fatal("different catalog families share an entity identity namespace")
	}
}

func TestScopeCommitmentSurvivesCatalogAndActiveKeyRotation(t *testing.T) {
	oldKey := HMACIdentityKey{KeyID: "identity-1", Key: bytes.Repeat([]byte{1}, 32)}
	newKey := HMACIdentityKey{KeyID: "identity-2", Key: bytes.Repeat([]byte{2}, 32)}
	oldActive, err := HMACIdentityKeyring(
		HMACIdentityKey{KeyID: oldKey.KeyID, Key: oldKey.Key, Active: true},
		newKey,
	)
	if err != nil {
		t.Fatal(err)
	}
	newActive, err := HMACIdentityKeyring(
		oldKey,
		HMACIdentityKey{KeyID: newKey.KeyID, Key: newKey.Key, Active: true},
	)
	if err != nil {
		t.Fatal(err)
	}
	scope, err := NewContextValue(ScopedReference{Scope: "tenant", Reference: "42"}, Verified)
	if err != nil {
		t.Fatal(err)
	}
	descriptions := []IdentityCommitmentDescription{
		{Algorithm: HMACSHA256Algorithm, Profile: HMACIdentityProfileV1, KeyID: oldKey.KeyID},
		{Algorithm: HMACSHA256Algorithm, Profile: HMACIdentityProfileV1, KeyID: newKey.KeyID},
	}
	first, present, err := scopeCommitment(context.Background(), oldActive, CatalogRef{ID: "application", Generation: 1, Digest: CatalogDigest{1}}, Context{Scope: scope}, descriptions)
	if err != nil || !present {
		t.Fatalf("first scope commitment = (%x, %v, %v)", first, present, err)
	}
	second, present, err := scopeCommitment(context.Background(), newActive, CatalogRef{ID: "application", Generation: 2, Digest: CatalogDigest{2}}, Context{Scope: scope}, descriptions)
	if err != nil || !present {
		t.Fatalf("second scope commitment = (%x, %v, %v)", second, present, err)
	}
	if first != second {
		t.Fatal("scope commitment changed across catalog and active-key rotation")
	}
}
