package access

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"runtime"
	"strconv"
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
	stored, err := parseStoredHash(encoded)
	if err != nil {
		return false, err
	}
	got := argon2.IDKey([]byte(password), stored.salt, stored.time, stored.memory, stored.threads, uint32(len(stored.digest)))
	return subtle.ConstantTimeCompare(got, stored.digest) == 1, nil
}

type storedHash struct {
	memory  uint32
	time    uint32
	threads uint8
	salt    []byte
	digest  []byte
}

const (
	argonMinMemory  = 8
	argonMaxMemory  = 1 << 20
	argonMinTime    = 1
	argonMaxTime    = 16
	argonMinThreads = 1
	argonMinSaltLen = 8
	argonMaxSaltLen = 64
	argonMinKeyLen  = 16
	argonMaxKeyLen  = 64
)

func parseStoredHash(encoded string) (storedHash, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return storedHash{}, ErrSecretFormat
	}
	version, err := bounded(parts[2], "v=", argon2.Version, argon2.Version)
	if err != nil || version != argon2.Version {
		return storedHash{}, ErrSecretFormat
	}

	cost := strings.Split(parts[3], ",")
	if len(cost) != 3 {
		return storedHash{}, ErrSecretFormat
	}
	memory, memoryErr := bounded(cost[0], "m=", argonMinMemory, argonMaxMemory)
	time, timeErr := bounded(cost[1], "t=", argonMinTime, argonMaxTime)
	threads, threadsErr := bounded(cost[2], "p=", argonMinThreads, 255)
	if memoryErr != nil || timeErr != nil || threadsErr != nil {
		return storedHash{}, ErrSecretFormat
	}

	salt, err := decoded(parts[4], argonMinSaltLen, argonMaxSaltLen)
	if err != nil {
		return storedHash{}, err
	}
	digest, err := decoded(parts[5], argonMinKeyLen, argonMaxKeyLen)
	if err != nil {
		return storedHash{}, err
	}
	return storedHash{memory: memory, time: time, threads: uint8(threads), salt: salt, digest: digest}, nil
}

func bounded(field, prefix string, low, high uint64) (uint32, error) {
	digits, found := strings.CutPrefix(field, prefix)
	if !found || digits == "" || (len(digits) > 1 && digits[0] == '0') {
		return 0, ErrSecretFormat
	}
	value, err := strconv.ParseUint(digits, 10, 32)
	if err != nil || value < low || value > high {
		return 0, ErrSecretFormat
	}
	return uint32(value), nil
}

func decoded(field string, low, high int) ([]byte, error) {
	if len(field) > base64.RawStdEncoding.EncodedLen(high) {
		return nil, ErrSecretFormat
	}
	out, err := base64.RawStdEncoding.DecodeString(field)
	if err != nil || len(out) < low || len(out) > high {
		return nil, ErrSecretFormat
	}
	return out, nil
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
