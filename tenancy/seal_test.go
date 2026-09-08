package tenancy

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"testing"
)

var durableTestKey = []byte("a-durable-key-of-at-least-32-bytes!!")

func sealingAuthority(t *testing.T, key []byte, raw string, generation uint64) (*Authority, Scope) {
	t.Helper()
	reference, err := ParseReference(raw)
	if err != nil {
		t.Fatal(err)
	}
	epoch, err := NewEpoch(generation)
	if err != nil {
		t.Fatal(err)
	}
	authority, err := New(Spec{
		Resolver:   Fixed{Reference: reference, Lifecycle: Active, Epoch: epoch},
		DurableKey: key,
	})
	if err != nil {
		t.Fatal(err)
	}
	scope, err := authority.Verify(context.Background(), ClassDurable)
	if err != nil {
		t.Fatal(err)
	}
	return authority, scope
}

func sealerOf(t *testing.T, authority *Authority) Sealer {
	t.Helper()
	sealer, err := authority.Sealer()
	if err != nil {
		t.Fatalf("the authority this test is built on cannot seal: %v", err)
	}
	return sealer
}

// A deployment that configured no key has no way to tell a record it wrote from
// one somebody else wrote into the same table, so the seam refuses to exist
// rather than reading whatever is there.
func TestADeploymentWithNoDurableKeySealsNothing(t *testing.T) {
	for name, key := range map[string][]byte{
		"no key at all":   nil,
		"a key too short": []byte("31-bytes-is-one-short-of-enough"),
	} {
		t.Run(name, func(t *testing.T) {
			reference, err := ParseReference("acme")
			if err != nil {
				t.Fatal(err)
			}
			epoch, err := NewEpoch(1)
			if err != nil {
				t.Fatal(err)
			}
			spec := Spec{
				Resolver:   Fixed{Reference: reference, Lifecycle: Active, Epoch: epoch},
				DurableKey: key,
			}
			authority, err := New(spec)
			if err != nil {
				if len(key) == 0 {
					t.Fatalf("a deployment that does no durable work was refused an authority: %v", err)
				}
				return
			}
			if _, err := authority.Sealer(); !errors.Is(err, ErrUntrusted) {
				t.Fatalf("err = %v, want ErrUntrusted — a deployment with %d key bytes sealed a record", err, len(key))
			}
		})
	}
	var missing *Authority
	if _, err := missing.Sealer(); !errors.Is(err, ErrNoScope) {
		t.Fatalf("err = %v, want ErrNoScope — a seam wired without an authority sealed a record", err)
	}
}

// Sealing is what turns a scope into a record another process will trust, so a
// scope this authority did not mint is refused here rather than carried into a
// worker under this deployment's key.
func TestOnlyAScopeThisAuthorityMintedIsSealed(t *testing.T) {
	mine, _ := sealingAuthority(t, durableTestKey, "acme", 1)
	_, theirs := sealingAuthority(t, durableTestKey, "acme", 1)

	if _, err := sealerOf(t, mine).Seal(context.Background(), theirs); !errors.Is(err, ErrUntrusted) {
		t.Fatalf("err = %v, want ErrUntrusted — a scope from another authority was sealed under this key", err)
	}
	if _, err := sealerOf(t, mine).Seal(context.Background(), Scope{}); !errors.Is(err, ErrNoScope) {
		t.Fatalf("err = %v, want ErrNoScope — a zero scope was sealed", err)
	}
}

// A deployment that admits no durable work for a lifecycle must not be handed a
// record for it: the record outlives the lifecycle, and the restorer's own check
// passes the moment the tenant is active again at the same generation.
func TestOnlyAScopeTheDurableClassAdmitsIsSealed(t *testing.T) {
	reference, err := ParseReference("acme")
	if err != nil {
		t.Fatal(err)
	}
	epoch, err := NewEpoch(1)
	if err != nil {
		t.Fatal(err)
	}
	admission := Admit(ClassRead, Active, Suspended).
		Merge(Admit(ClassWrite, Active)).
		Merge(Admit(ClassDurable, Active))

	for state, want := range map[Lifecycle]error{Suspended: ErrInactive, Active: nil} {
		t.Run(state.String(), func(t *testing.T) {
			authority, err := New(Spec{
				Resolver:   Fixed{Reference: reference, Lifecycle: state, Epoch: epoch},
				Admission:  admission,
				DurableKey: durableTestKey,
			})
			if err != nil {
				t.Fatal(err)
			}
			scope, err := authority.Verify(context.Background(), ClassRead)
			if err != nil {
				t.Fatalf("the read this deployment allows was refused: %v", err)
			}
			if _, err := sealerOf(t, authority).Seal(context.Background(), scope, []byte("billing")); !errors.Is(err, want) {
				t.Fatalf("err = %v, want %v — sealing does not ask what the durable class admits", err, want)
			}
		})
	}
}

// The MAC is over the claim, not beside it. A record whose tenant or generation
// was edited under a MAC this deployment really produced is the one shape that
// says what the MAC covers, and it is what somebody with write access to the
// queue table would try after the obvious forgeries fail.
func TestTheClaimARecordCarriesIsWhatItsMACCovers(t *testing.T) {
	authority, scope := sealingAuthority(t, durableTestKey, "acme", 1)
	sealer := sealerOf(t, authority)

	honest, err := sealer.Seal(context.Background(), scope, []byte("billing"))
	if err != nil {
		t.Fatal(err)
	}
	if reference, generation, err := sealer.Unseal(honest, []byte("billing")); err != nil ||
		reference.Value() != "acme" || generation.Value() != 1 {
		t.Fatalf("the honest record reads back as %v at %d (%v), so the edits below prove nothing", reference, generation.Value(), err)
	}

	renamed := append([]byte(nil), honest[:sealHeaderBytes]...)
	renamed = append(renamed, "globex"...)

	restored := append([]byte(nil), honest...)
	binary.BigEndian.PutUint64(restored[:sealGenerationBytes], 2)

	for name, edited := range map[string][]byte{
		"the tenant rewritten under the same MAC":     renamed,
		"the generation rewritten under the same MAC": restored,
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := sealer.Unseal(edited, []byte("billing")); !errors.Is(err, ErrUntrusted) {
				t.Fatalf("err = %v, want ErrUntrusted — the MAC does not cover this part of the claim", err)
			}
		})
	}
}

// Origin is the deployment fence, and a DurableKey is copied wiring: an
// environment cloned from another's configuration draws a new salt and keeps the
// key, so the key alone cannot be what tells the two apart.
func TestARecordSealedInAnotherDeploymentIsNotRead(t *testing.T) {
	reference, err := ParseReference("acme")
	if err != nil {
		t.Fatal(err)
	}
	epoch, err := NewEpoch(1)
	if err != nil {
		t.Fatal(err)
	}
	resolver := Fixed{Reference: reference, Lifecycle: Active, Epoch: epoch}
	deployment := func(origin string) *Authority {
		t.Helper()
		authority, err := New(Spec{Resolver: resolver, Origin: origin, DurableKey: durableTestKey})
		if err != nil {
			t.Fatal(err)
		}
		return authority
	}
	staging, production := deployment("invoices.staging"), deployment("invoices.production")

	scope, err := staging.Verify(context.Background(), ClassDurable)
	if err != nil {
		t.Fatal(err)
	}
	token, err := sealerOf(t, staging).Seal(context.Background(), scope, []byte("billing"))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := sealerOf(t, production).Unseal(token, []byte("billing")); !errors.Is(err, ErrUntrusted) {
		t.Fatalf("err = %v, want ErrUntrusted — a record written in staging was executed in production", err)
	}
	if _, _, err := sealerOf(t, staging).Unseal(token, []byte("billing")); err != nil {
		t.Fatalf("the control refuses too, so the assertion above proves nothing: %v", err)
	}
}

func TestASealedRecordIsReadBackAsWhatWasSealed(t *testing.T) {
	authority, scope := sealingAuthority(t, durableTestKey, "acme", 7)
	sealer := sealerOf(t, authority)

	token, err := sealer.Seal(context.Background(), scope, []byte("billing"), []byte("send-invoice"))
	if err != nil {
		t.Fatal(err)
	}
	reference, generation, err := sealer.Unseal(token, []byte("billing"), []byte("send-invoice"))
	if err != nil {
		t.Fatalf("the record this authority sealed does not verify: %v", err)
	}
	if reference != scope.Reference() || generation != scope.Epoch() {
		t.Fatalf("the record read back as %v at generation %d", reference, generation.Value())
	}
}

// The binding fields are what tie a record to the place it was written for. Each
// is length-prefixed and the count is sealed with them, so neither a field
// spelled across a boundary nor an extra empty one reads as the honest record.
func TestBindingFieldsCannotBeSlidPastOneAnother(t *testing.T) {
	authority, scope := sealingAuthority(t, durableTestKey, "acme", 1)
	sealer := sealerOf(t, authority)

	honest, err := sealer.Seal(context.Background(), scope, []byte("billing"), []byte("send-invoice"))
	if err != nil {
		t.Fatal(err)
	}
	for name, binding := range map[string][][]byte{
		"the boundary moved":      {[]byte("billingsend"), []byte("-invoice")},
		"the fields concatenated": {[]byte("billingsend-invoice")},
		"an empty field appended": {[]byte("billing"), []byte("send-invoice"), {}},
		"the fields swapped":      {[]byte("send-invoice"), []byte("billing")},
		"nothing bound at all":    nil,
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := sealer.Unseal(honest, binding...); !errors.Is(err, ErrUntrusted) {
				t.Fatalf("err = %v, want ErrUntrusted — this record verified somewhere it was never written for", err)
			}
		})
	}
}

func TestARecordAnotherDeploymentSealedIsNotRead(t *testing.T) {
	mine, _ := sealingAuthority(t, durableTestKey, "acme", 1)
	theirs, theirScope := sealingAuthority(t, []byte("a-different-durable-key-of-32-bytes!"), "acme", 1)

	token, err := sealerOf(t, theirs).Seal(context.Background(), theirScope, []byte("billing"))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := sealerOf(t, mine).Unseal(token, []byte("billing")); !errors.Is(err, ErrUntrusted) {
		t.Fatalf("err = %v, want ErrUntrusted — a record sealed under another deployment's key was read", err)
	}
	if _, _, err := sealerOf(t, theirs).Unseal(token, []byte("billing")); err != nil {
		t.Fatalf("the control refuses too, so the assertion above proves nothing: %v", err)
	}
}

func TestARecordTooShortToCarryAClaimIsRefused(t *testing.T) {
	authority, scope := sealingAuthority(t, durableTestKey, "acme", 1)
	sealer := sealerOf(t, authority)
	honest, err := sealer.Seal(context.Background(), scope)
	if err != nil {
		t.Fatal(err)
	}
	for name, token := range map[string][]byte{
		"nothing at all":         nil,
		"a header and no tenant": honest[:len(honest)-len("acme")],
		"a truncated header":     honest[:8],
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := sealer.Unseal(token); !errors.Is(err, ErrUntrusted) {
				t.Fatalf("err = %v, want ErrUntrusted", err)
			}
		})
	}
}

// Two seams derive an identifier from this digest — an object namespace and a
// cache partition — and a restore that changed one without the other would fence
// the bucket and serve the previous generation's cache.
func TestTheDigestSeparatesTenantsAndGenerations(t *testing.T) {
	_, acme := sealingAuthority(t, durableTestKey, "acme", 1)
	_, globex := sealingAuthority(t, durableTestKey, "globex", 1)
	_, restored := sealingAuthority(t, durableTestKey, "acme", 2)

	first, second, third := acme.Digest(), globex.Digest(), restored.Digest()
	if bytes.Equal(first[:], second[:]) {
		t.Fatal("two tenants digest the same, so one addresses the other's objects and cached values")
	}
	if bytes.Equal(first[:], third[:]) {
		t.Fatal("a restored generation digests as the one it replaced")
	}
	if again := acme.Digest(); !bytes.Equal(first[:], again[:]) {
		t.Fatal("the same scope digests differently twice, so nothing addressed by it can be found again")
	}
}

var durableBinding = [][]byte{[]byte("billing"), []byte("send-invoice")}

func rotatingAuthority(t *testing.T, current []byte, retired ...[]byte) (*Authority, Scope) {
	t.Helper()
	reference, err := ParseReference("acme")
	if err != nil {
		t.Fatal(err)
	}
	epoch, err := NewEpoch(1)
	if err != nil {
		t.Fatal(err)
	}
	authority, err := New(Spec{
		Resolver:           Fixed{Reference: reference, Lifecycle: Active, Epoch: epoch},
		DurableKey:         current,
		RetiredDurableKeys: retired,
	})
	if err != nil {
		t.Fatal(err)
	}
	scope, err := authority.Verify(context.Background(), ClassDurable)
	if err != nil {
		t.Fatal(err)
	}
	return authority, scope
}

// A durable key cannot be rotated by restarting with a new one: the queue already
// holds records sealed with the old key, and a process that verifies only the
// current key refuses every one of them. Rotation is therefore two deploys, and
// this is the middle state — the new key seals, the old one still verifies.
func TestARetiredDurableKeyStillReadsTheRecordsItSealed(t *testing.T) {
	before := []byte("the-key-this-deployment-started-with!")
	after := []byte("the-key-this-deployment-rotated-to!!!")

	old, oldScope := rotatingAuthority(t, before)
	written, err := sealerOf(t, old).Seal(context.Background(), oldScope, durableBinding...)
	if err != nil {
		t.Fatal(err)
	}

	// The middle deploy: sealing with the new key, still verifying the old.
	rotating, rotatingScope := rotatingAuthority(t, after, before)
	if _, _, err := sealerOf(t, rotating).Unseal(written, durableBinding...); err != nil {
		t.Fatalf("a record sealed before the rotation was refused during it: %v", err)
	}

	// And what it seals now is readable by a process that has finished rotating,
	// which is what makes the second deploy safe.
	fresh, err := sealerOf(t, rotating).Seal(context.Background(), rotatingScope, durableBinding...)
	if err != nil {
		t.Fatal(err)
	}
	finished, _ := rotatingAuthority(t, after)
	if _, _, err := sealerOf(t, finished).Unseal(fresh, durableBinding...); err != nil {
		t.Fatalf("a record sealed during the rotation was refused after it: %v", err)
	}

	// The control, and the reason this is not just "accept anything": once the old
	// key is gone, its records are gone too, which is what the drain is for.
	if _, _, err := sealerOf(t, finished).Unseal(written, durableBinding...); !errors.Is(err, ErrUntrusted) {
		t.Fatalf("err = %v, want ErrUntrusted — a retired key kept verifying after it was removed", err)
	}
}

func TestARetiredKeyWithNothingToSealWithIsRefused(t *testing.T) {
	reference, err := ParseReference("acme")
	if err != nil {
		t.Fatal(err)
	}
	epoch, err := NewEpoch(1)
	if err != nil {
		t.Fatal(err)
	}
	resolver := Fixed{Reference: reference, Lifecycle: Active, Epoch: epoch}
	if _, err := New(Spec{
		Resolver:           resolver,
		RetiredDurableKeys: [][]byte{[]byte("a-retired-key-that-is-long-enough!!!")},
	}); err == nil {
		t.Fatal("an authority that can verify durable records but never produce one was constructed")
	}
	if _, err := New(Spec{
		Resolver:           resolver,
		DurableKey:         []byte("a-current-key-that-is-long-enough!!!"),
		RetiredDurableKeys: [][]byte{[]byte("short")},
	}); err == nil {
		t.Fatal("a retired key too short to be a key was accepted")
	}
}
