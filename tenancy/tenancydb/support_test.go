package tenancydb_test

import (
	"context"
	"fmt"
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

type switchable struct{ current *tenancy.Resolution }

func (this switchable) Resolve(context.Context) (tenancy.Resolution, error) {
	return *this.current, nil
}

func (this switchable) Lookup(_ context.Context, reference tenancy.Reference) (tenancy.Resolution, error) {
	if reference != this.current.Reference {
		return tenancy.Resolution{}, tenancy.ErrUnmapped
	}
	return *this.current, nil
}

func bound(t *testing.T, authority *tenancy.Authority) context.Context {
	t.Helper()
	ctx, err := authority.Bind(context.Background(), tenancy.ClassWrite)
	if err != nil {
		t.Fatalf("cannot bind the scope the test is built on: %v", err)
	}
	return ctx
}

type directory struct {
	byReference map[tenancy.Reference]tenancy.Resolution
}

func (this directory) Resolve(context.Context) (tenancy.Resolution, error) {
	return tenancy.Resolution{}, tenancy.ErrNoScope
}

func (this directory) Lookup(_ context.Context, ref tenancy.Reference) (tenancy.Resolution, error) {
	resolution, ok := this.byReference[ref]
	if !ok {
		return tenancy.Resolution{}, tenancy.ErrUnmapped
	}
	return resolution, nil
}

func manyTenantAuthority(t *testing.T, count int) (*tenancy.Authority, []string) {
	t.Helper()
	epoch, err := tenancy.NewEpoch(1)
	if err != nil {
		t.Fatal(err)
	}
	byReference := make(map[tenancy.Reference]tenancy.Resolution, count)
	raws := make([]string, 0, count)
	for i := range count {
		raw := fmt.Sprintf("tenant-%04d", i)
		ref := reference(t, raw)
		byReference[ref] = tenancy.Resolution{Reference: ref, Lifecycle: tenancy.Active, Epoch: epoch}
		raws = append(raws, raw)
	}
	authority, err := tenancy.New(tenancy.Spec{Resolver: directory{byReference: byReference}})
	if err != nil {
		t.Fatal(err)
	}
	return authority, raws
}
