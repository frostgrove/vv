package tenancy

import (
	"context"
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
// queue, the definition, the invocation and a digest of the payload — so a record
// lifted out of one and replayed into another fails the comparison rather than
// the handler. Each field is length-prefixed, because a token whose fields can be
// slid past one another authenticates a different record than it travelled with.
//
// A seam that binds only what is constant across the records of one queue has a
// token that authenticates all of them, which is a bearer credential for the
// tenant rather than a claim about a row: at least one field must distinguish one
// record from the next. See [[D-119]].
//
// The scope is accepted for the durable class rather than merely checked for this
// authority's mint: sealing is what turns a scope into a record another process
// will trust, `From(ctx)` hands any carried scope to anyone, and a deployment
// that admits no durable work for a lifecycle must not get a durable record for
// it — one that would execute the moment the tenant becomes active again, with
// no generation change to stop it.
//
// The context is here for the grant. Sealing under a cohort grant that permits
// only reads would otherwise write a durable record for every tenant in the
// cohort — work that runs later, outside the grant's own deadline, with a class
// the grant never named. Every other verb asks; this one has to ask too.
func (this Sealer) Seal(ctx context.Context, scope Scope, binding ...[]byte) ([]byte, error) {
	if this.authority == nil {
		return nil, ErrUntrusted
	}
	if ctx == nil {
		return nil, ErrUntrusted
	}
	sealed, err := this.authority.accept(scope, ClassDurable)
	if err != nil {
		return nil, err
	}
	if err := this.authority.permittedByGrant(ctx, ClassDurable); err != nil {
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
	if !this.verified(token[sealGenerationBytes:sealHeaderBytes], binding, generation, reference) {
		return Reference{}, 0, ErrUntrusted
	}
	return reference, generation, nil
}

// The sealing key first, then every retired one. A rotation leaves records in the
// queue that were sealed with the previous key, and a deployment that could only
// verify the current one would have to drain the backlog to zero before it could
// change a key — which is the same as not being able to change it. Every
// candidate is compared, and each comparison is constant time.
func (this Sealer) verified(mac []byte, binding [][]byte, epoch Epoch, reference Reference) bool {
	ok := false
	for _, key := range this.authority.sealingKeys() {
		want := macWith(key, this.authority.origin, binding, epoch, reference)
		if hmac.Equal(mac, want[:]) {
			ok = true
		}
	}
	return ok
}

// The origin is inside the seal for the same reason it is inside a scope's
// binding: a `DurableKey` is configured wiring, and wiring is what survives a
// deployment cloned from another's. Two environments that share the key still
// refuse each other's records.
func (this Sealer) mac(binding [][]byte, epoch Epoch, reference Reference) [32]byte {
	return macWith(this.authority.durableKey, this.authority.origin, binding, epoch, reference)
}

func macWith(key []byte, origin string, binding [][]byte, epoch Epoch, reference Reference) [32]byte {
	mac := hmac.New(sha256.New, key)
	writeField(mac, origin)
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
