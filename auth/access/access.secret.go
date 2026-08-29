package access

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"sync"

	"golang.org/x/crypto/argon2"
)

// Everything in this file is about a secret nobody may read back: a password on
// its way into a column, and a session token on its way into one. Both are
// stored as digests, and the two use different functions on purpose.
//
// A password is low-entropy and guessable, so it gets argon2id — deliberately
// slow and memory-hard, so that a stolen table is not a list of passwords. A
// session token is 256 bits this process chose, so there is nothing to guess
// and SHA-256 is enough; running argon2 on every authenticated request would
// cost 64 MiB and several milliseconds to defend against an attack that cannot
// happen.

// ErrSecretFormat reports a stored hash this process cannot read. It is a
// deployment fault, not a wrong password, and the two must not answer alike:
// treating an unreadable hash as a mismatch locks every account out quietly.
var ErrSecretFormat = errors.New("access: unreadable password hash")

// The argon2id parameters. RFC 9106's second recommended option — 64 MiB and
// three passes — which is the one that fits an API process that may serve
// several sign-ins at once.
const (
	argonTime    uint32 = 3
	argonMemory  uint32 = 64 * 1024
	argonKeyLen  uint32 = 32
	argonSaltLen        = 16
)

// A Hasher turns a password into something safe to store, and checks one.
type Hasher interface {
	Hash(password string) (string, error)
	Verify(password, encoded string) (bool, error)
}

// Argon2Hasher is the one implementation. It is a struct rather than a set of
// functions so a test can hand a cheap one in — argon2 at these parameters
// makes a suite that signs in fifty times take a minute.
type Argon2Hasher struct {
	Time    uint32
	Memory  uint32
	Threads uint8
	KeyLen  uint32
}

// NewHasher builds the production hasher.
func NewHasher() *Argon2Hasher {
	threads := uint8(runtime.NumCPU())
	if threads > 4 {
		threads = 4
	}
	if threads == 0 {
		threads = 1
	}
	return &Argon2Hasher{Time: argonTime, Memory: argonMemory, Threads: threads, KeyLen: argonKeyLen}
}

// Hash encodes in the PHC string format, so the parameters travel with the
// digest. A hash that did not carry them could never be verified again after
// the cost was raised, and raising the cost is the one maintenance this has.
func (this *Argon2Hasher) Hash(password string) (string, error) {
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("access: reading entropy for a password salt: %w", err)
	}
	sum := argon2.IDKey([]byte(password), salt, this.Time, this.Memory, this.Threads, this.KeyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, this.Memory, this.Time, this.Threads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(sum),
	), nil
}

// Verify recomputes with the *stored* parameters rather than the current ones,
// which is what lets the cost be raised without invalidating every account.
func (this *Argon2Hasher) Verify(password, encoded string) (bool, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false, ErrSecretFormat
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return false, ErrSecretFormat
	}
	var memory, time uint32
	var threads uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &time, &threads); err != nil {
		return false, ErrSecretFormat
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false, ErrSecretFormat
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false, ErrSecretFormat
	}
	got := argon2.IDKey([]byte(password), salt, time, memory, threads, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

// DummyHash is what a sign-in verifies against when no credential was found, so
// that a request for an unknown identifier costs the same as one for a known
// identifier with the wrong password. Without it the response time answers
// "does this address have an account here", which is the one thing the uniform
// error message exists to withhold.
//
// It is a real argon2id hash of a value nothing can present, computed once on
// first use rather than at package initialisation — argon2 at these parameters
// is 60ms, and a package that spends it whether or not anybody signs in makes
// every test binary that links this package slower for nothing.
func DummyHash() string {
	dummyOnce.Do(func() {
		h, err := NewHasher().Hash("access: no credential")
		if err != nil {
			panic(err)
		}
		dummy = h
	})
	return dummy
}

var (
	dummyOnce sync.Once
	dummy     string
)

// TokenBytes is how much entropy a session token carries. 256 bits, so the
// token is not something anybody enumerates and the digest below needs no salt.
const TokenBytes = 32

// NewToken mints a session token. The caller gets it once, this process stores
// only [HashToken] of it, and there is no path back.
func NewToken() (string, error) {
	buf := make([]byte, TokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("access: reading entropy for a session token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// HashToken is what the sessions table stores and what a lookup compares
// against. Hex rather than base64 so the column is plain to read in a query.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return fmt.Sprintf("%x", sum)
}
