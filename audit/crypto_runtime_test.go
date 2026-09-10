package audit

import (
	"bytes"
	"context"
	"testing"
)

func TestAESGCMRevealRejectsMalformedEnvelopeWithoutPanicking(t *testing.T) {
	keys, err := AESGCMProtection(AESGCMKey{KeyID: "active", Key: bytes.Repeat([]byte{7}, 32), Active: true})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name       string
		nonce      []byte
		ciphertext []byte
	}{
		{name: "wrong nonce size", nonce: []byte{1}, ciphertext: make([]byte, 16)},
		{name: "short ciphertext", nonce: make([]byte, 12), ciphertext: []byte{1}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			envelope, err := NewProtectedValue(AES256GCMAlgorithm, AESGCMProtectionProfileV1, "active", test.nonce, test.ciphertext)
			if err != nil {
				t.Fatal(err)
			}
			request, err := newRevealRequest(envelope, []byte("field-aad"))
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if recovered := recover(); recovered != nil {
					t.Fatalf("Reveal panicked for malformed envelope: %v", recovered)
				}
			}()
			if _, err := keys.Reveal(context.Background(), request); err == nil {
				t.Fatal("Reveal accepted a malformed envelope")
			} else if outcome, ok := CryptoOutcomeOf(err); !ok || outcome != CryptoMalformed {
				t.Fatalf("Reveal outcome = %v, %v", outcome, err)
			}
		})
	}
}

func TestAESGCMProtectionRoundTripsExactPlaintextAndAAD(t *testing.T) {
	keys, err := AESGCMProtection(AESGCMKey{KeyID: "active", Key: bytes.Repeat([]byte{9}, 32), Active: true})
	if err != nil {
		t.Fatal(err)
	}
	request, err := newProtectionRequest([]byte("private-value"), []byte("field-aad"))
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := keys.Protect(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	reveal, err := newRevealRequest(envelope, []byte("field-aad"))
	if err != nil {
		t.Fatal(err)
	}
	plaintext, err := keys.Reveal(context.Background(), reveal)
	if err != nil || !bytes.Equal(plaintext, []byte("private-value")) {
		t.Fatalf("round trip = %q, %v", plaintext, err)
	}
	wrong, err := newRevealRequest(envelope, []byte("other-aad"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := keys.Reveal(context.Background(), wrong); err == nil {
		t.Fatal("Reveal accepted different AAD")
	}
}
