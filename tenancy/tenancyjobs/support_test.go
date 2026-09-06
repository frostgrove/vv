package tenancyjobs

import (
	"context"
	"testing"

	"github.com/frostgrove/vv/tenancy"
)

var durableTestKey = []byte("a-durable-key-of-at-least-32-bytes!!")

func fixedAuthority(t *testing.T, raw string, state tenancy.Lifecycle, generation uint64) (*tenancy.Authority, tenancy.Scope) {
	t.Helper()
	authority := authorityOnly(t, raw, state, generation)
	scope, err := authority.Verify(context.Background(), tenancy.ClassDurable)
	if err != nil {
		t.Fatalf("the authority refused a %s tenant: %v", state, err)
	}
	return authority, scope
}

func authorityOnly(t *testing.T, raw string, state tenancy.Lifecycle, generation uint64) *tenancy.Authority {
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

func admittedAuthority(t *testing.T, raw string, state tenancy.Lifecycle, generation uint64, admission tenancy.Admission) *tenancy.Authority {
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
		Admission:  admission,
		DurableKey: durableTestKey,
	})
	if err != nil {
		t.Fatal(err)
	}
	return authority
}
