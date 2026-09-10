package auditpg

import (
	"bytes"
	"reflect"
	"testing"
	"time"

	"github.com/frostgrove/vv/audit"
)

func TestRevisionEvidenceRoundTripsOpaqueCryptographicValues(t *testing.T) {
	token, err := audit.NewToken("hmac-sha256", "frostgrove.audit.token.v1", "token-1", bytes.Repeat([]byte{3}, 32))
	if err != nil {
		t.Fatal(err)
	}
	protected, err := audit.NewProtectedValue("aes-gcm", "frostgrove.audit.protection.v1", "protect-1", bytes.Repeat([]byte{4}, 12), []byte("ciphertext-and-tag"))
	if err != nil {
		t.Fatal(err)
	}
	seal, err := audit.NewSeal("hmac-sha256", "frostgrove.audit.signature.v1", "sign-1", bytes.Repeat([]byte{5}, 32))
	if err != nil {
		t.Fatal(err)
	}
	description := audit.IdentityCommitmentDescription{Algorithm: "hmac-sha256", Profile: "frostgrove.audit.identity.v1", KeyID: "identity-1"}
	commitment, err := audit.NewIdentityCommitment(description, bytes.Repeat([]byte{6}, 32))
	if err != nil {
		t.Fatal(err)
	}
	commitments, err := audit.NewStoredIdentityCommitmentSet(audit.StoredIdentityCommitmentSetData{
		Domain: audit.CommitEntitySubject, Active: description, Commitments: []audit.IdentityCommitment{commitment},
	})
	if err != nil {
		t.Fatal(err)
	}
	value := audit.StoredValueView{Field: "value", Classification: audit.Secret, Mode: audit.AsIndexedProtected, State: audit.ValuePresent, Token: token, Protected: protected}
	view := audit.RevisionWireView{
		Header: audit.RevisionHeaderView{
			Format: 1, ObservedAt: time.Unix(1_700_000_000, 123).UTC(), Seal: seal,
			Authorization: audit.RevisionAuthorizationSummaryView{Coordinates: []audit.QueryCoordinateView{{
				Kind: audit.QuerySubject, Alternatives: []audit.QueryCoordinateAlternativeView{{Tokens: []audit.Token{token}, Commitments: commitments}},
			}}},
		},
		Actors:  []audit.StoredActorView{{Reference: value}},
		Context: []audit.StoredContextFactView{{Token: token, Protected: protected}},
		Items: []audit.ItemWireView{{
			Subject: value, Target: value, Values: []audit.StoredValueView{value},
			Changes: []audit.StoredChangeView{{Before: value, After: value}}, Hold: audit.HoldTransitionWireView{Matter: value},
		}},
	}
	wire, err := encodeEvidence(view)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeEvidence(wire)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(view, decoded) {
		t.Fatal("revision evidence did not preserve the complete wire view")
	}
	wire[0] = '!'
	if _, err := decodeEvidence(wire); err == nil {
		t.Fatal("malformed evidence was accepted")
	}
}

func TestCatalogSetDigestIsOrderedAndBounded(t *testing.T) {
	first, err := catalogSetDigest([][]byte{[]byte("generation-1"), []byte("generation-2")})
	if err != nil || first == (audit.CatalogSetDigest{}) {
		t.Fatalf("digest = (%x, %v)", first, err)
	}
	second, _ := catalogSetDigest([][]byte{[]byte("generation-2"), []byte("generation-1")})
	if first == second {
		t.Fatal("catalog generation order is absent from the digest")
	}
	if _, err := catalogSetDigest(nil); err == nil {
		t.Fatal("empty catalog set was accepted")
	}
}
