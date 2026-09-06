package tenancyrow_test

import (
	"testing"

	"github.com/frostgrove/vv/tenancy"
)

const secretReference = "acme-7f3c"

func reference(t *testing.T, raw string) tenancy.Reference {
	t.Helper()
	value, err := tenancy.ParseReference(raw)
	if err != nil {
		t.Fatalf("cannot parse the reference the test is built on: %v", err)
	}
	return value
}

func authorityFor(t *testing.T, raw string, state tenancy.Lifecycle, epoch uint64, admission tenancy.Admission) *tenancy.Authority {
	t.Helper()
	generation, err := tenancy.NewEpoch(epoch)
	if err != nil {
		t.Fatalf("cannot build the epoch the test is built on: %v", err)
	}
	authority, err := tenancy.New(tenancy.Spec{
		Resolver:  tenancy.Fixed{Reference: reference(t, raw), Lifecycle: state, Epoch: generation},
		Admission: admission,
	})
	if err != nil {
		t.Fatalf("cannot build the authority: %v", err)
	}
	return authority
}

func activeAuthority(t *testing.T, raw string) *tenancy.Authority {
	t.Helper()
	return authorityFor(t, raw, tenancy.Active, 1, tenancy.Admission{})
}
