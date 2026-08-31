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

var ErrSecretFormat = errors.New("access: unreadable password hash")

const (
	argonTime    uint32 = 3
	argonMemory  uint32 = 64 * 1024
	argonKeyLen  uint32 = 32
	argonSaltLen        = 16
)

type Hasher interface {
	Hash(password string) (string, error)
	Verify(password, encoded string) (bool, error)
}

type Argon2Hasher struct {
	Time    uint32
	Memory  uint32
	Threads uint8
	KeyLen  uint32
}

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

const TokenBytes = 32

func NewToken() (string, error) {
	buf := make([]byte, TokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("access: reading entropy for a session token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return fmt.Sprintf("%x", sum)
}
