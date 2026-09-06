package tenancy

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
)

const (
	sealGenerationBytes = 8
	sealMACBytes        = 32
	sealHeaderBytes     = sealGenerationBytes + sealMACBytes
)

// A durable record carries a tenant reference into a process that did not write
// it — a queue row read back by a worker, an outbox entry, a schedule. The table
// it sits in is writable by whatever else reaches that database, so the reference
// is a claim rather than an authority, and what makes it this deployment's claim
// is a key configured for the deployment rather than the per-authority salt: the
// salt is drawn per process, and the process that verifies is not the process
// that sealed. A seam with no key refuses to be constructed at all.
type Sealer struct{ authority *Authority }

func (this *Authority) Sealer() (Sealer, error) {
	if this == nil {
		return Sealer{}, ErrNoScope
	}
	if len(this.durableKey) < MinDurableKeyBytes {
		return Sealer{}, ErrUntrusted
	}
	return Sealer{authority: this}, nil
}

// The binding fields tie the token to the record that carries it — for a job, the
// queue and the definition — so a record lifted out of one and replayed into
// another fails the comparison rather than the handler. Each field is
// length-prefixed, because a token whose fields can be slid past one another
// authenticates a different record than it travelled with.
//
// The scope is accepted for the durable class rather than merely checked for this
// authority's mint: sealing is what turns a scope into a record another process
// will trust, `From(ctx)` hands any carried scope to anyone, and a deployment
// that admits no durable work for a lifecycle must not get a durable record for
// it — one that would execute the moment the tenant becomes active again, with
// no generation change to stop it.
func (this Sealer) Seal(scope Scope, binding ...[]byte) ([]byte, error) {
	if this.authority == nil {
		return nil, ErrUntrusted
	}
	sealed, err := this.authority.accept(scope, ClassDurable)
	if err != nil {
		return nil, err
	}
	token := make([]byte, sealHeaderBytes, sealHeaderBytes+len(sealed.reference.Value()))
	binary.BigEndian.PutUint64(token[:sealGenerationBytes], sealed.epoch.Value())
	token = append(token, sealed.reference.Value()...)
	mac := this.mac(binding, sealed.epoch, sealed.reference)
	copy(token[sealGenerationBytes:sealHeaderBytes], mac[:])
	return token, nil
}

// What comes back is a reference and a generation, never a scope: the record is
// disbelieved even once it verifies, and the control plane is what turns it back
// into work — a tenant suspended, deleted or restored since the record was
// written refuses there.
func (this Sealer) Unseal(token []byte, binding ...[]byte) (Reference, Epoch, error) {
	if this.authority == nil {
		return Reference{}, 0, ErrUntrusted
	}
	if len(token) <= sealHeaderBytes {
		return Reference{}, 0, ErrUntrusted
	}
	generation, err := NewEpoch(binary.BigEndian.Uint64(token[:sealGenerationBytes]))
	if err != nil {
		return Reference{}, 0, ErrUntrusted
	}
	reference, err := ParseReference(string(token[sealHeaderBytes:]))
	if err != nil {
		return Reference{}, 0, ErrUntrusted
	}
	want := this.mac(binding, generation, reference)
	if !hmac.Equal(token[sealGenerationBytes:sealHeaderBytes], want[:]) {
		return Reference{}, 0, ErrUntrusted
	}
	return reference, generation, nil
}

// The origin is inside the seal for the same reason it is inside a scope's
// binding: a `DurableKey` is configured wiring, and wiring is what survives a
// deployment cloned from another's. Two environments that share the key still
// refuse each other's records.
func (this Sealer) mac(binding [][]byte, epoch Epoch, reference Reference) [32]byte {
	mac := hmac.New(sha256.New, this.authority.durableKey)
	writeField(mac, this.authority.origin)
	writeCount(mac, len(binding))
	for _, field := range binding {
		writeBytes(mac, field)
	}
	var generation [8]byte
	binary.BigEndian.PutUint64(generation[:], epoch.Value())
	_, _ = mac.Write(generation[:])
	writeField(mac, reference.Value())
	var out [32]byte
	copy(out[:], mac.Sum(nil))
	return out
}
