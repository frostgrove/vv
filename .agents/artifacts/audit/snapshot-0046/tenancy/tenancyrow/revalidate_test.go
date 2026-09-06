package tenancyrow_test

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/crudtest"
	"github.com/frostgrove/vv/tenancy"
	"github.com/frostgrove/vv/tenancy/tenancyrow"
)

type counting struct {
	current *tenancy.Resolution
	lookups atomic.Int64
}

func (this *counting) Resolve(context.Context) (tenancy.Resolution, error) { return *this.current, nil }

func (this *counting) Lookup(_ context.Context, reference tenancy.Reference) (tenancy.Resolution, error) {
	this.lookups.Add(1)
	if reference != this.current.Reference {
		return tenancy.Resolution{}, tenancy.ErrUnmapped
	}
	return *this.current, nil
}

func revalidating(t *testing.T, on bool) (*tenancy.Authority, *tenancy.Resolution, *counting) {
	t.Helper()
	epoch, err := tenancy.NewEpoch(1)
	if err != nil {
		t.Fatal(err)
	}
	current := &tenancy.Resolution{Reference: reference(t, secretReference), Lifecycle: tenancy.Active, Epoch: epoch}
	resolver := &counting{current: current}
	authority, err := tenancy.New(tenancy.Spec{Resolver: resolver, Revalidate: on})
	if err != nil {
		t.Fatal(err)
	}
	return authority, current, resolver
}

func verbs(t *testing.T, invoices *crud.Repo[Invoice, int64, InvoiceUpdate], ctx context.Context) map[string]func() error {
	t.Helper()
	number := "INV-rewritten"
	return map[string]func() error{
		"GetAll":    func() error { _, err := invoices.GetAll(ctx); return err },
		"GetByID":   func() error { _, err := invoices.GetByID(ctx, 1); return err },
		"Count":     func() error { _, err := invoices.Count(ctx); return err },
		"Save":      func() error { _, err := invoices.Save(ctx, &Invoice{Number: "INV-1"}); return err },
		"Update":    func() error { _, err := invoices.Update(ctx, 1, InvoiceUpdate{Number: &number}); return err },
		"UpdateAll": func() error { _, err := invoices.UpdateAll(ctx, InvoiceUpdate{Number: &number}); return err },
		"Delete":    func() error { _, err := invoices.Delete(ctx, 1); return err },
		"DeleteAll": func() error { _, err := invoices.DeleteAll(ctx); return err },
	}
}

func TestRevalidationStopsTheNextVerbAfterTheControlPlaneMoves(t *testing.T) {
	for name, move := range map[string]struct {
		apply func(*tenancy.Resolution, *testing.T)
		want  error
	}{
		"the generation moved": {
			apply: func(current *tenancy.Resolution, t *testing.T) {
				epoch, err := tenancy.NewEpoch(2)
				if err != nil {
					t.Fatal(err)
				}
				current.Epoch = epoch
			},
			want: tenancy.ErrStale,
		},
		"the tenant was deleted": {
			apply: func(current *tenancy.Resolution, t *testing.T) { current.Lifecycle = tenancy.Deleted },
			want:  tenancy.ErrInactive,
		},
	} {
		t.Run(name, func(t *testing.T) {
			recorder := crudtest.Postgres()
			authority, current, resolver := revalidating(t, true)
			invoices := Invoices.Bind(recorder, tenancyrow.Repository[Invoice, int64](authority,
				tenancyrow.Column[Invoice]("TenantID", tenancyrow.Derive, nil)))

			ctx, err := authority.Bind(context.Background(), tenancy.ClassWrite)
			if err != nil {
				t.Fatal(err)
			}
			recorder.Push(crudtest.Rows(), crudtest.Rows())
			if _, err := invoices.GetAll(ctx); err != nil {
				t.Fatalf("a bound tenant was refused before anything moved: %v", err)
			}
			before := resolver.lookups.Load()
			if before == 0 {
				t.Fatal("the resolver was never re-asked, so revalidation is not happening at all")
			}

			move.apply(current, t)

			for verb, call := range verbs(t, invoices, ctx) {
				recorder.Reset()
				recorder.Push(crudtest.Rows(), crudtest.Rows(), crudtest.Rows())
				if err := call(); !errors.Is(err, move.want) {
					t.Fatalf("%s answered %v, want %v", verb, err, move.want)
				}
				if len(recorder.Statements()) != 0 {
					t.Fatalf("%s reached the database after the control plane moved: %v", verb, recorder.SQL())
				}
			}
			if resolver.lookups.Load() <= before {
				t.Fatal("the resolver was not asked again, so the refusals came from the carried scope rather than from the control plane")
			}
		})
	}
}

func TestWithoutRevalidationTheCarriedScopeHoldsUntilTheNextBoundary(t *testing.T) {
	recorder := crudtest.Postgres()
	authority, current, resolver := revalidating(t, false)
	invoices := Invoices.Bind(recorder, tenancyrow.Repository[Invoice, int64](authority,
		tenancyrow.Column[Invoice]("TenantID", tenancyrow.Derive, nil)))

	ctx, err := authority.Bind(context.Background(), tenancy.ClassWrite)
	if err != nil {
		t.Fatal(err)
	}
	current.Lifecycle = tenancy.Deleted

	recorder.Push(crudtest.Rows())
	if _, err := invoices.GetAll(ctx); err != nil {
		t.Fatalf("the default pinned the scope and should still have run: %v", err)
	}
	if !strings.Contains(where(recorder), "tenant_id") {
		t.Fatalf("the statement lost its narrowing: %s", recorder.Last().SQL)
	}
	if resolver.lookups.Load() != 0 {
		t.Fatal("the default asked the control plane anyway, so the two settings are the same setting")
	}

	if _, err := authority.Bind(context.Background(), tenancy.ClassWrite); !errors.Is(err, tenancy.ErrInactive) {
		t.Fatalf("the next boundary answered %v, want ErrInactive — pinned means until the next Bind, not for ever", err)
	}
}
