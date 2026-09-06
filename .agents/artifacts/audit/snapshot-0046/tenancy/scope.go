package tenancy

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"log/slog"
)

type Resolution struct {
	Reference Reference
	Lifecycle Lifecycle
	Epoch     Epoch
}

func (this Resolution) valid() bool {
	return this.Reference.valid() && this.Lifecycle.Valid() && this.Epoch.valid()
}

func (this Resolution) String() string { return "[tenant resolution]" }

func (this Resolution) Format(state fmt.State, _ rune) {
	_, _ = fmt.Fprint(state, this.String())
}

type Scope struct {
	reference Reference
	lifecycle Lifecycle
	epoch     Epoch
	binding   [32]byte
}

func (this Scope) Reference() Reference { return this.reference }
func (this Scope) Lifecycle() Lifecycle { return this.lifecycle }
func (this Scope) Epoch() Epoch         { return this.epoch }
func (this Scope) IsZero() bool         { return this.binding == [32]byte{} }
func (this Scope) String() string       { return "[tenancy scope]" }

func (this Scope) Format(state fmt.State, _ rune) {
	_, _ = fmt.Fprint(state, this.String())
}

func (this Scope) LogValue() slog.Value { return slog.StringValue(this.String()) }

func (Scope) MarshalJSON() ([]byte, error) {
	return nil, fmt.Errorf("tenancy: a scope cannot be serialized: %w", ErrUntrusted)
}

// The binding is what makes a scope unforgeable rather than merely unexported.
// A zero value, a value copied field by field into an identically shaped type in
// another package, and a value minted by a different authority — another
// deployment, another environment, a test double standing next to production
// wiring — all fail this comparison, because the salt is generated per authority
// and never leaves it. Comparing in constant time keeps the mint from becoming
// an oracle for its own salt.
func (this Scope) boundTo(salt []byte, origin string) bool {
	if this.IsZero() || !this.reference.valid() || !this.lifecycle.Valid() || !this.epoch.valid() {
		return false
	}
	want := bind(salt, origin, Resolution{Reference: this.reference, Lifecycle: this.lifecycle, Epoch: this.epoch})
	return hmac.Equal(this.binding[:], want[:])
}

func bind(salt []byte, origin string, resolution Resolution) [32]byte {
	mac := hmac.New(sha256.New, salt)
	writeField(mac, origin)
	writeField(mac, resolution.Reference.Value())
	var scratch [9]byte
	scratch[0] = byte(resolution.Lifecycle)
	binary.BigEndian.PutUint64(scratch[1:], resolution.Epoch.Value())
	_, _ = mac.Write(scratch[:])
	var digest [32]byte
	copy(digest[:], mac.Sum(nil))
	return digest
}

// A tenant restored to a new generation must address neither the objects nor the
// cached values the previous one left, and the object namespace and the cache
// partition are built in two packages that must not disagree about that: a
// restore that fences the bucket and serves the old cache is the shape this one
// function exists to make impossible. It identifies a tenant and a generation and
// authorises nothing, which is why it may be read outside the mint.
func (this Scope) Digest() [32]byte {
	digest := sha256.New()
	writeField(digest, this.reference.Value())
	var generation [8]byte
	binary.BigEndian.PutUint64(generation[:], this.epoch.Value())
	_, _ = digest.Write(generation[:])
	var out [32]byte
	copy(out[:], digest.Sum(nil))
	return out
}

type writer interface{ Write([]byte) (int, error) }

func writeField(mac writer, value string) { writeBytes(mac, []byte(value)) }

func writeCount(mac writer, count int) {
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(count))
	_, _ = mac.Write(length[:])
}

func writeBytes(mac writer, value []byte) {
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(value)))
	_, _ = mac.Write(length[:])
	_, _ = mac.Write(value)
}
