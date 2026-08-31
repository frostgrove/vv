package access

import (
	"errors"
	"strings"
	"testing"
)

func testHasher() *Argon2Hasher {
	return &Argon2Hasher{Time: 1, Memory: 8 * 1024, Threads: 1, KeyLen: 16}
}

func TestAPasswordVerifiesAgainstItsOwnHashAndNothingElse(t *testing.T) {
	h := testHasher()
	encoded, err := h.Hash("correct-horse-battery")
	if err != nil {
		t.Fatal(err)
	}

	if strings.Contains(encoded, "correct-horse-battery") {
		t.Fatal("the stored value contains the password")
	}

	ok, err := h.Verify("correct-horse-battery", encoded)
	if err != nil || !ok {
		t.Fatalf("the right password did not verify: ok=%v err=%v", ok, err)
	}
	ok, err = h.Verify("correct-horse-batterz", encoded)
	if err != nil || ok {
		t.Fatalf("a wrong password verified: ok=%v err=%v", ok, err)
	}
}

func TestTwoHashesOfOnePasswordDiffer(t *testing.T) {
	h := testHasher()
	a, _ := h.Hash("same-password-twice")
	b, _ := h.Hash("same-password-twice")
	if a == b {
		t.Fatal("the salt is not random; equal digests name every account that shares a password")
	}
}

func TestAHashVerifiesAgainstAHasherWithDifferentParameters(t *testing.T) {
	written := &Argon2Hasher{Time: 1, Memory: 8 * 1024, Threads: 1, KeyLen: 16}
	encoded, err := written.Hash("a-password-from-last-year")
	if err != nil {
		t.Fatal(err)
	}

	raised := &Argon2Hasher{Time: 3, Memory: 32 * 1024, Threads: 2, KeyLen: 32}
	ok, err := raised.Verify("a-password-from-last-year", encoded)
	if err != nil || !ok {
		t.Fatalf("raising the cost locked out an existing account: ok=%v err=%v", ok, err)
	}
}

func TestAnUnreadableHashIsAFaultAndNotAMismatch(t *testing.T) {
	h := testHasher()
	for _, encoded := range []string{
		"",
		"not-a-hash",
		"$argon2i$v=19$m=65536,t=3,p=4$c2FsdA$aGFzaA",
		"$argon2id$v=19$m=65536,t=3,p=4$c2FsdA",
		"$argon2id$v=1$m=65536,t=3,p=4$c2FsdA$aGFzaA",
		"$argon2id$v=19$nonsense$c2FsdA$aGFzaA",
		"$argon2id$v=19$m=65536,t=3,p=4$!!!$aGFzaA",
	} {
		ok, err := h.Verify("anything", encoded)
		if ok {
			t.Errorf("%q verified", encoded)
		}
		if !errors.Is(err, ErrSecretFormat) {
			t.Errorf("Verify(%q) err = %v, want ErrSecretFormat — a mismatch and an unreadable column must not answer alike", encoded, err)
		}
	}
}

func TestTheDummyHashIsARealHashThatNothingVerifiesAgainst(t *testing.T) {
	dummy := DummyHash()
	if !strings.HasPrefix(dummy, "$argon2id$") {
		t.Fatalf("DummyHash() = %q, which is not an argon2id hash and costs nothing to check", dummy)
	}
	ok, err := NewHasher().Verify("access: no credential", dummy)
	if err != nil {
		t.Fatalf("the dummy hash is not readable: %v", err)
	}
	if !ok {
		t.Fatal("the dummy hash does not verify against its own input, so it is not a real hash")
	}
	if DummyHash() != dummy {
		t.Fatal("DummyHash is recomputed per call; the point of the sync.Once is that it is not")
	}
}

func TestASessionTokenIsUnguessableAndStoredOnlyAsADigest(t *testing.T) {
	a, err := NewToken()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := NewToken()
	if a == b {
		t.Fatal("two tokens came out equal")
	}
	if len(a) < 40 {
		t.Fatalf("a token is %d characters; %d bytes of entropy should not encode that short", len(a), TokenBytes)
	}

	digest := HashToken(a)
	if strings.Contains(digest, a) || digest == a {
		t.Fatal("the digest carries the token")
	}
	if HashToken(a) != digest {
		t.Fatal("hashing is not stable, so a lookup by digest would never match")
	}
	if HashToken(b) == digest {
		t.Fatal("two tokens hash alike")
	}
	if len(digest) != 64 {
		t.Fatalf("digest is %d characters, want 64 hex characters of SHA-256", len(digest))
	}
}
