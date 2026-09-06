package tenancyrow_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/crudtest"
	"github.com/frostgrove/vv/crud/decorators/faults"
	"github.com/frostgrove/vv/tenancy"
	"github.com/frostgrove/vv/tenancy/tenancyrow"
)

// GAP-T5: the row seam must resolve its narrowing for reads and its inspection for
// the class of the action, or the three admission lists collapse into one.
func TestTheRepositorySeamAsksForTheClassOfTheVerb(t *testing.T) {
	recorder := crudtest.Postgres()
	epoch, err := tenancy.NewEpoch(1)
	if err != nil {
		t.Fatal(err)
	}
	authority, err := tenancy.New(tenancy.Spec{
		Resolver:  tenancy.Fixed{Reference: reference(t, secretReference), Lifecycle: tenancy.Suspended, Epoch: epoch},
		Admission: tenancy.Admit(tenancy.ClassRead, tenancy.Active, tenancy.Suspended).Merge(tenancy.Admit(tenancy.ClassWrite, tenancy.Active)),
	})
	if err != nil {
		t.Fatal(err)
	}
	invoices := Invoices.Bind(recorder, tenancyrow.Repository[Invoice, int64](authority,
		tenancyrow.Column[Invoice]("TenantID", tenancyrow.Derive, nil)))

	ctx, err := authority.Bind(context.Background(), tenancy.ClassRead)
	if err != nil {
		t.Fatalf("a suspended tenant this deployment keeps readable was refused: %v", err)
	}

	recorder.Push(crudtest.Rows([]any{int64(1), secretReference, "INV-1", int64(100)}))
	if _, err := invoices.GetAll(ctx); err != nil {
		t.Fatalf("the read this deployment allows was refused: %v", err)
	}

	recorder.Reset()
	recorder.Push(crudtest.Rows([]any{int64(1), secretReference, "INV-1", int64(100)}))
	if _, err := invoices.Save(ctx, &Invoice{Number: "INV-2"}); !errors.Is(err, tenancy.ErrInactive) {
		t.Fatalf("err = %v, want ErrInactive — a suspended tenant wrote through a seam that only admits its reads", err)
	}
	for _, statement := range recorder.SQL() {
		if strings.HasPrefix(statement, "INSERT") || strings.HasPrefix(statement, "UPDATE") {
			t.Fatalf("the write reached the database: %s", statement)
		}
	}
}

// INV-31 / UC-7: order may change what a neighbour observes; it never changes
// whether the narrowing happens. `faults.Enrich` is a real unrelated decorator
// that forwards every optional effect, so it is the honest neighbour to compose
// with rather than a fake written to agree.
func TestNarrowingHoldsWhicheverOrderTheExtensionsAreListedIn(t *testing.T) {
	ctx := func(authority *tenancy.Authority) context.Context { return bound(t, authority) }

	for name, order := range map[string]func(*tenancy.Authority) []crud.Middleware[Invoice, int64]{
		"tenancy outermost": func(a *tenancy.Authority) []crud.Middleware[Invoice, int64] {
			return []crud.Middleware[Invoice, int64]{
				tenancyrow.Repository[Invoice, int64](a, tenancyrow.Column[Invoice]("TenantID", tenancyrow.Derive, nil)),
				faults.Enrich[Invoice, int64](),
			}
		},
		"tenancy innermost": func(a *tenancy.Authority) []crud.Middleware[Invoice, int64] {
			return []crud.Middleware[Invoice, int64]{
				faults.Enrich[Invoice, int64](),
				tenancyrow.Repository[Invoice, int64](a, tenancyrow.Column[Invoice]("TenantID", tenancyrow.Derive, nil)),
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			recorder := crudtest.Postgres()
			authority := activeAuthority(t, secretReference)
			invoices := Invoices.Bind(recorder, order(authority)...)

			recorder.Push(crudtest.Rows([]any{int64(1), secretReference, "INV-1", int64(100)}))
			if _, err := invoices.GetAll(ctx(authority)); err != nil {
				t.Fatal(err)
			}
			clause := clauseOf(recorder.Last().SQL)
			if !strings.Contains(clause, `"tenant_id" = $`) {
				t.Fatalf("the read is not narrowed in this order: %q", clause)
			}
			if !slices.Contains(recorder.Last().Args, any(secretReference)) {
				t.Fatalf("the read narrows to %v, not to the scope's tenant", recorder.Last().Args)
			}

			recorder.Reset()
			recorder.Push(crudtest.Rows())
			if _, err := invoices.GetAll(context.Background()); !errors.Is(err, tenancy.ErrNoScope) {
				t.Fatalf("an unscoped read in this order answered %v", err)
			}
			if len(recorder.Statements()) != 0 {
				t.Fatalf("an unscoped read reached the database in this order: %v", recorder.SQL())
			}
		})
	}
}
