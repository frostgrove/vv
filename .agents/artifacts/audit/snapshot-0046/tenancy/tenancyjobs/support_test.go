package tenancyjobs

import (
	"context"
	"testing"

	"github.com/frostgrove/vv/tenancy"
)

var durableTestKey = []byte("a-durable-key-of-at-least-32-bytes!!")

func fixedAuthority(t *testing.T, raw string, state tenancy.Lifecycle, generation uint64) (*tenancy.Authority, tenancy.Scope) {
	t.Helper()
	reference, err := tenancy.ParseReference(raw)
	if err != nil {
		t.Fatal(err)
	}
	epoch, err := tenancy.NewEpoch(generation)
	if err != nil {
		t.Fatal(err)
	}
	authority, err := tenancy.New(tenancy.Spec{
		Resolver:   tenancy.Fixed{Reference: reference, Lifecycle: state, Epoch: epoch},
		DurableKey: durableTestKey,
	})
	if err != nil {
		t.Fatal(err)
	}
	scope, err := authority.Verify(context.Background(), tenancy.ClassDurable)
	if err != nil && state == tenancy.Active {
		t.Fatalf("the authority refused an active tenant: %v", err)
	}
	return authority, scope
}

func authorityOnly(t *testing.T, raw string, state tenancy.Lifecycle, generation uint64) *tenancy.Authority {
	t.Helper()
	authority, _ := fixedAuthority(t, raw, state, generation)
	return authority
}

type knownTenants struct {
	byReference map[tenancy.Reference]tenancy.Resolution
}

func (this knownTenants) Resolve(context.Context) (tenancy.Resolution, error) {
	return tenancy.Resolution{}, tenancy.ErrNoScope
}

func (this knownTenants) Lookup(_ context.Context, reference tenancy.Reference) (tenancy.Resolution, error) {
	resolution, ok := this.byReference[reference]
	if !ok {
		return tenancy.Resolution{}, tenancy.ErrUnmapped
	}
	return resolution, nil
}
