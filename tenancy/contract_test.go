package tenancy_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/frostgrove/vv/tenancy"
)

// GAP-T8: the pin is on the whole binding, not on the tenant's name.
func TestTheUnitOfWorkPinCoversTheGenerationAsWellAsTheTenant(t *testing.T) {
	epoch, err := tenancy.NewEpoch(1)
	if err != nil {
		t.Fatal(err)
	}
	current := &tenancy.Resolution{Reference: reference(t, secretReference), Lifecycle: tenancy.Active, Epoch: epoch}
	authority, err := tenancy.New(tenancy.Spec{Resolver: switchable{current: current}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, err := authority.Bind(context.Background(), tenancy.ClassWrite)
	if err != nil {
		t.Fatal(err)
	}

	moved, err := tenancy.NewEpoch(2)
	if err != nil {
		t.Fatal(err)
	}
	current.Epoch = moved
	if _, err := authority.Bind(ctx, tenancy.ClassWrite); !errors.Is(err, tenancy.ErrPinned) {
		t.Fatalf("err = %v, want ErrPinned — the same tenant at a restored generation joined a unit of work opened for the previous one", err)
	}
}

// GAP-T6: Origin is the deployment fence, and the per-authority salt would hide a
// regression in it. Two authorities that share everything but the origin must not
// accept each other's scopes; two that share the origin still must not, because
// the salt is the other half.
func TestTheOriginFencesOneDeploymentFromAnother(t *testing.T) {
	epoch, err := tenancy.NewEpoch(1)
	if err != nil {
		t.Fatal(err)
	}
	resolution := tenancy.Fixed{Reference: reference(t, secretReference), Lifecycle: tenancy.Active, Epoch: epoch}

	staging, err := tenancy.New(tenancy.Spec{Resolver: resolution, Origin: "invoices.staging"})
	if err != nil {
		t.Fatal(err)
	}
	production, err := tenancy.New(tenancy.Spec{Resolver: resolution, Origin: "invoices.production"})
	if err != nil {
		t.Fatal(err)
	}
	fromStaging, err := staging.Verify(context.Background(), tenancy.ClassRead)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := production.With(context.Background(), fromStaging); !errors.Is(err, tenancy.ErrUntrusted) {
		t.Fatalf("err = %v, want ErrUntrusted — a scope that travelled from staging was honoured in production", err)
	}
	if _, err := staging.With(context.Background(), fromStaging); err != nil {
		t.Fatalf("the control refuses too, so the assertion above proves nothing: %v", err)
	}
}

// GAP-T7: the class bound is enforced when the cohort is entered and again at every
// verb underneath. Both guards, or a read grant writes.
func TestAReadGrantCannotWriteAtEitherGuard(t *testing.T) {
	clock := time.Now()
	authority, cohort := cohortAuthority(t, &clock, "acme", "globex")
	grant, err := authority.Accept(purpose(t, "monthly-billing"), cohort, clock.Add(time.Hour), tenancy.ClassRead)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := authority.Each(context.Background(), grant, tenancy.ClassWrite, func(context.Context) error {
		t.Fatal("a read grant entered its cohort to write")
		return nil
	}); !errors.Is(err, tenancy.ErrGrantRequired) {
		t.Fatalf("the entry guard answered %v, want ErrGrantRequired", err)
	}

	var inner error
	if _, err := authority.Each(context.Background(), grant, tenancy.ClassRead, func(ctx context.Context) error {
		if _, err := authority.Scope(ctx, tenancy.ClassRead); err != nil {
			t.Fatalf("the read the grant permits was refused inside it: %v", err)
		}
		_, inner = authority.Scope(ctx, tenancy.ClassWrite)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(inner, tenancy.ErrGrantRequired) {
		t.Fatalf("the verb guard answered %v, want ErrGrantRequired — a read grant wrote once it was inside its cohort", inner)
	}
}
