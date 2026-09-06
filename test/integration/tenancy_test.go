//go:build integration

package integration

import (
	"context"
	"errors"
	"strconv"
	"testing"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/adapter/crudsql"
	"github.com/frostgrove/vv/tenancy"
	"github.com/frostgrove/vv/tenancy/tenancyrow"
)

func tenantAuthority(t *testing.T, raw string, state tenancy.Lifecycle, epoch uint64) *tenancy.Authority {
	t.Helper()
	reference, err := tenancy.ParseReference(raw)
	if err != nil {
		t.Fatalf("cannot parse the tenant reference: %v", err)
	}
	generation, err := tenancy.NewEpoch(epoch)
	if err != nil {
		t.Fatalf("cannot build the epoch: %v", err)
	}
	authority, err := tenancy.New(tenancy.Spec{
		Resolver: tenancy.Fixed{Reference: reference, Lifecycle: state, Epoch: generation},
	})
	if err != nil {
		t.Fatalf("cannot build the authority: %v", err)
	}
	return authority
}

func numericTenant(reference tenancy.Reference) (any, error) {
	value, err := strconv.ParseInt(reference.Value(), 10, 64)
	if err != nil {
		return nil, tenancy.ErrMalformed
	}
	return value, nil
}

func ownedUsers(t *testing.T, source crud.Source, authority *tenancy.Authority) *crud.Repo[User, int64, UserUpdate] {
	t.Helper()
	return Users.Bind(source, tenancyrow.Repository[User, int64](authority,
		tenancyrow.Column[User]("TenantID", tenancyrow.Derive, numericTenant)))
}

func TestATenantOwnedRepositoryIsolatesTwoTenantsOnOneTable(t *testing.T) {
	target := Target{Name: "postgres", DB: "postgres", Source: crudsql.Postgres(pgDB)}
	target.reset(t)

	first := tenantAuthority(t, "1", tenancy.Active, 1)
	second := tenantAuthority(t, "2", tenancy.Active, 1)

	oneCtx, err := first.Bind(context.Background(), tenancy.ClassWrite)
	if err != nil {
		t.Fatal(err)
	}
	twoCtx, err := second.Bind(context.Background(), tenancy.ClassWrite)
	if err != nil {
		t.Fatal(err)
	}

	mine := ownedUsers(t, target.Source, first)
	theirs := ownedUsers(t, target.Source, second)

	stored, err := theirs.Save(twoCtx, &User{Email: "b@x.io", Name: "Bea", Active: true})
	if err != nil {
		t.Fatalf("the second tenant could not create its own row: %v", err)
	}
	if stored.TenantID != 2 {
		t.Fatalf("ownership was not derived from the scope: tenant_id = %d", stored.TenantID)
	}
	if _, err := mine.Save(oneCtx, &User{Email: "a@x.io", Name: "Ann", Active: true}); err != nil {
		t.Fatal(err)
	}

	foreign := stored.ID

	t.Run("naming a foreign row by id is not found", func(t *testing.T) {
		if _, err := mine.GetByID(oneCtx, foreign); !errors.Is(err, crud.ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
	})

	t.Run("a foreign row is not counted and does not exist", func(t *testing.T) {
		n, err := mine.Count(oneCtx)
		if err != nil {
			t.Fatal(err)
		}
		if n != 1 {
			t.Fatalf("count = %d, want the one row this tenant owns", n)
		}
		found, err := mine.Exists(oneCtx, crud.Where(crud.Eq("ID", foreign)))
		if err != nil {
			t.Fatal(err)
		}
		if found {
			t.Fatal("existence of another tenant's row was confirmed")
		}
	})

	t.Run("a foreign row cannot be updated or deleted by guessing its id", func(t *testing.T) {
		name := "renamed"
		if _, err := mine.Update(oneCtx, foreign, UserUpdate{Name: &name}); !errors.Is(err, crud.ErrNotFound) {
			t.Fatalf("update err = %v, want ErrNotFound", err)
		}
		n, err := mine.Delete(oneCtx, foreign)
		if err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Fatalf("delete removed %d foreign rows", n)
		}
		still, err := theirs.GetByID(twoCtx, foreign)
		if err != nil {
			t.Fatalf("the owner lost its row: %v", err)
		}
		if still.Name != "Bea" {
			t.Fatalf("the owner's row was rewritten: %+v", still)
		}
	})

	t.Run("a create into another tenant is refused", func(t *testing.T) {
		if _, err := mine.Save(oneCtx, &User{TenantID: 2, Email: "c@x.io", Name: "Cid"}); !errors.Is(err, crud.ErrForbidden) {
			t.Fatalf("err = %v, want a refusal", err)
		}
	})

	t.Run("and the tenant still sees every row it owns", func(t *testing.T) {
		rows, err := mine.GetAll(oneCtx)
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) != 1 || rows[0].Email != "a@x.io" {
			t.Fatalf("the owner was handed %d rows: %+v", len(rows), rows)
		}
	})
}

func TestALifecycleThatDoesNotAdmitWorkReachesNoStatement(t *testing.T) {
	target := Target{Name: "postgres", DB: "postgres", Source: crudsql.Postgres(pgDB)}
	target.reset(t)

	suspended := tenantAuthority(t, "1", tenancy.Suspended, 1)
	users := ownedUsers(t, target.Source, suspended)

	if _, err := suspended.Bind(context.Background(), tenancy.ClassRead); !errors.Is(err, tenancy.ErrInactive) {
		t.Fatalf("err = %v, want ErrInactive", err)
	}
	if _, err := users.GetAll(context.Background()); !errors.Is(err, tenancy.ErrNoScope) {
		t.Fatalf("err = %v, want ErrNoScope", err)
	}

	active := tenantAuthority(t, "1", tenancy.Active, 1)
	ctx, err := active.Bind(context.Background(), tenancy.ClassWrite)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ownedUsers(t, target.Source, active).Save(ctx, &User{Email: "a@x.io", Name: "Ann"}); err != nil {
		t.Fatalf("the control refuses too, so the assertions above prove nothing: %v", err)
	}
}
