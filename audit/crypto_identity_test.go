package audit

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"testing"
)

func TestHMACSemanticDigesterCopiesItsKeyAndIsDomainStable(t *testing.T) {
	key := bytes.Repeat([]byte{7}, 32)
	digester, err := HMACSemanticDigester("semantic-key", key)
	if err != nil {
		t.Fatal(err)
	}
	first, err := digester.Digest(context.Background(), []byte("value"))
	if err != nil {
		t.Fatal(err)
	}
	key[0] = 9
	second, err := digester.Digest(context.Background(), []byte("value"))
	if err != nil || first != second || first == (SemanticDigest{}) {
		t.Fatalf("stable digest = (%x, %x, %v)", first, second, err)
	}
	other, _ := digester.Digest(context.Background(), []byte("other"))
	if other == first {
		t.Fatal("different semantic inputs produced one digest")
	}
}

func TestHMACIdentityKeyringReturnsEveryAliasWithTheDeclaredActiveKey(t *testing.T) {
	oldKey := bytes.Repeat([]byte{1}, 32)
	activeKey := bytes.Repeat([]byte{2}, 32)
	keyring, err := HMACIdentityKeyring(
		HMACIdentityKey{KeyID: "a-retained", Key: oldKey},
		HMACIdentityKey{KeyID: "z-active", Key: activeKey, Active: true},
	)
	if err != nil {
		t.Fatal(err)
	}
	required := []IdentityCommitmentDescription{keyring.ActiveDescription()}
	for _, description := range keyring.Descriptions() {
		if description != keyring.ActiveDescription() {
			required = append(required, description)
		}
	}
	request, err := newIdentityCommitmentRequest(CommitEntitySubject, []byte("subject:42"), []byte("tenant:7"), required)
	if err != nil {
		t.Fatal(err)
	}
	plaintext := request.Plaintext()
	plaintext[0] = 'x'
	if string(request.Plaintext()) != "subject:42" {
		t.Fatal("request plaintext accessor aliases internal state")
	}
	set, err := keyring.CommitIdentities(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if set.Domain() != CommitEntitySubject || set.Active().Description() != keyring.ActiveDescription() || len(set.Aliases()) != 2 {
		t.Fatalf("commitment set = domain %v active %+v aliases %d", set.Domain(), set.Active().Description(), len(set.Aliases()))
	}
	aliases := set.Aliases()
	aliases[0] = IdentityCommitment{}
	if len(set.Aliases()) != 2 || set.Aliases()[0] == (IdentityCommitment{}) {
		t.Fatal("commitment aliases accessor exposes its slice")
	}
	oldKey[0] = 9
	activeKey[0] = 9
	again, err := keyring.CommitIdentities(context.Background(), request)
	if err != nil || !slices.Equal(set.Active().Bytes(), again.Active().Bytes()) {
		t.Fatal("identity keyring retained caller key storage")
	}
}

func TestIdentityCommitmentConstructionRejectsForgeryShapes(t *testing.T) {
	if _, err := HMACIdentityKeyring(HMACIdentityKey{KeyID: "only", Key: bytes.Repeat([]byte{1}, 31), Active: true}); !errors.Is(err, ErrDeclaration) {
		t.Fatalf("short key = %v", err)
	}
	if _, err := HMACIdentityKeyring(
		HMACIdentityKey{KeyID: "one", Key: bytes.Repeat([]byte{1}, 32), Active: true},
		HMACIdentityKey{KeyID: "two", Key: bytes.Repeat([]byte{2}, 32), Active: true},
	); !errors.Is(err, ErrDeclaration) {
		t.Fatalf("multiple active keys = %v", err)
	}
	if _, err := NewIdentityCommitment(IdentityCommitmentDescription{
		Algorithm: HMACSHA256Algorithm,
		Profile:   HMACIdentityProfileV1,
		KeyID:     "key",
	}, make([]byte, 31)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("short commitment = %v", err)
	}
	if _, err := NewIdentityCommitmentSet(IdentityCommitmentRequest{}, nil); !errors.Is(err, ErrInvalid) {
		t.Fatalf("zero request = %v", err)
	}
}
