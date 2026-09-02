package access

import (
	"encoding/base64"
	"errors"
	"fmt"
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

func TestAStoredHashIsRefusedUnlessEveryParameterIsInBounds(t *testing.T) {
	h := testHasher()
	salt := base64.RawStdEncoding.EncodeToString(make([]byte, argonSaltLen))
	digest := base64.RawStdEncoding.EncodeToString(make([]byte, argonKeyLen))
	sound := fmt.Sprintf("$argon2id$v=19$m=8192,t=1,p=1$%s$%s", salt, digest)
	if _, err := h.Verify("anything", sound); err != nil {
		t.Fatalf("the control hash was refused, so the refusals below prove nothing: %v", err)
	}

	for name, encoded := range map[string]string{
		"no digest at all":       fmt.Sprintf("$argon2id$v=19$m=8192,t=1,p=1$%s$", salt),
		"a digest of four bytes": fmt.Sprintf("$argon2id$v=19$m=8192,t=1,p=1$%s$aGFzaA", salt),
		"a salt of four bytes":   fmt.Sprintf("$argon2id$v=19$m=8192,t=1,p=1$c2FsdA$%s", digest),
		"a memory cost of zero":  fmt.Sprintf("$argon2id$v=19$m=0,t=1,p=1$%s$%s", salt, digest),
		"no rounds":              fmt.Sprintf("$argon2id$v=19$m=8192,t=0,p=1$%s$%s", salt, digest),
		"no threads":             fmt.Sprintf("$argon2id$v=19$m=8192,t=1,p=0$%s$%s", salt, digest),
		"a terabyte of memory":   fmt.Sprintf("$argon2id$v=19$m=1073741824,t=1,p=1$%s$%s", salt, digest),
		"four billion rounds":    fmt.Sprintf("$argon2id$v=19$m=8192,t=4000000000,p=1$%s$%s", salt, digest),
		"a cost that is text":    fmt.Sprintf("$argon2id$v=19$m=lots,t=1,p=1$%s$%s", salt, digest),
		"a fourth cost":          fmt.Sprintf("$argon2id$v=19$m=8192,t=1,p=1,x=9$%s$%s", salt, digest),
		"no leading empty field": fmt.Sprintf("argon2id$v=19$m=8192,t=1,p=1$%s$%s$", salt, digest),
	} {
		t.Run(name, func(t *testing.T) {
			ok, err := h.Verify("anything", encoded)
			if ok {
				t.Fatalf("%q verified", encoded)
			}
			if !errors.Is(err, ErrSecretFormat) {
				t.Fatalf("Verify(%q) err = %v, want ErrSecretFormat", encoded, err)
			}
		})
	}
}

func FuzzAStoredHashIsEitherRefusedOrWithinItsBounds(f *testing.F) {
	f.Add("$argon2id$v=19$m=8192,t=1,p=1$AAAAAAAAAAAAAAAAAAAAAA$AAAAAAAAAAAAAAAAAAAAAA")
	f.Add("$argon2id$v=19$m=65536,t=3,p=4$c2FsdA$")
	f.Add("$argon2id$v=19$m=65536,t=3,p=4$c2FsdA$aGFzaA")
	f.Add("$argon2id$v=99999999999999999999$m=8,t=1,p=1$AAAAAAAAAAA$AAAAAAAAAAA")
	f.Add("")

	f.Fuzz(func(t *testing.T, encoded string) {
		stored, err := parseStoredHash(encoded)
		if err != nil {
			if !errors.Is(err, ErrSecretFormat) {
				t.Fatalf("parsing %q answered %v, which no caller distinguishes from a mismatch", encoded, err)
			}
			return
		}
		switch {
		case stored.memory < argonMinMemory || stored.memory > argonMaxMemory:
			t.Fatalf("%q asks for %d KiB of memory", encoded, stored.memory)
		case stored.time < argonMinTime || stored.time > argonMaxTime:
			t.Fatalf("%q asks for %d rounds", encoded, stored.time)
		case stored.threads < argonMinThreads:
			t.Fatalf("%q asks for %d threads", encoded, stored.threads)
		case len(stored.salt) < argonMinSaltLen || len(stored.salt) > argonMaxSaltLen:
			t.Fatalf("%q carries a salt of %d bytes", encoded, len(stored.salt))
		case len(stored.digest) < argonMinKeyLen || len(stored.digest) > argonMaxKeyLen:
			t.Fatalf("%q carries a digest of %d bytes", encoded, len(stored.digest))
		}
	})
}
