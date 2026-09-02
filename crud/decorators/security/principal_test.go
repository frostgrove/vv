package security_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/frostgrove/vv/auth"
	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/crudtest"
	"github.com/frostgrove/vv/crud/decorators/security"
	"github.com/frostgrove/vv/crud/sqlrepo"
)

var editor = auth.Claims{
	Sub:         "u-1",
	Roles:       []auth.Role{"editor"},
	Permissions: []auth.Permission{"doc:read", "doc:write"},
	Attrs:       map[string]any{"tenant": int64(7)},
}

func as(p auth.Principal) context.Context {
	return auth.WithPrincipal(context.Background(), p)
}

func bound(rec *crudtest.Recorder, p security.Policy[Doc, int64]) *crud.Repo[Doc, int64, DocUpdate] {
	return Docs.Bind(rec, security.Gate(p))
}

func TestRequirePermissionRefusesTheCallerThatLacksOne(t *testing.T) {
	policy := security.RequirePermission[Doc, int64]("doc:read", "doc:admin")

	t.Run("a caller holding only one of two is refused", func(t *testing.T) {
		rec := crudtest.Postgres()
		_, err := bound(rec, policy).GetAll(as(editor))
		if !errors.Is(err, crud.ErrForbidden) {
			t.Fatalf("the gate answered %v, want a denial", err)
		}
		if len(rec.Statements()) != 0 {
			t.Fatal("a refused read still went to the database")
		}
	})

	t.Run("control: a caller holding both is let through", func(t *testing.T) {
		admin := editor
		admin.Permissions = append([]auth.Permission{"doc:admin"}, editor.Permissions...)
		rec := crudtest.Postgres().Push(crudtest.Rows())
		if _, err := bound(rec, policy).GetAll(as(admin)); err != nil {
			t.Fatalf("a caller holding every named permission was refused: %v", err)
		}
		if len(rec.Statements()) != 1 {
			t.Fatal("the read did not reach the database")
		}
	})

	t.Run("naming no permission refuses nothing", func(t *testing.T) {
		rec := crudtest.Postgres().Push(crudtest.Rows())
		if _, err := bound(rec, security.RequirePermission[Doc, int64]()).GetAll(as(editor)); err != nil {
			t.Fatalf("an empty permission list locked the table: %v", err)
		}
	})
}

func TestRequireAnyPermissionTakesTheOtherQuantifier(t *testing.T) {
	policy := security.RequireAnyPermission[Doc, int64]("doc:read", "doc:admin")

	t.Run("one of them is enough", func(t *testing.T) {
		rec := crudtest.Postgres().Push(crudtest.Rows())
		if _, err := bound(rec, policy).GetAll(as(editor)); err != nil {
			t.Fatalf("a caller holding one of the two was refused: %v", err)
		}
	})

	t.Run("none of them is a refusal", func(t *testing.T) {
		stranger := auth.Claims{Sub: "u-2", Permissions: []auth.Permission{"other:read"}}
		if _, err := bound(crudtest.Postgres(), policy).GetAll(as(stranger)); !errors.Is(err, crud.ErrForbidden) {
			t.Fatalf("a caller holding neither was let through: %v", err)
		}
	})

	t.Run("naming none refuses everything", func(t *testing.T) {
		if _, err := bound(crudtest.Postgres(), security.RequireAnyPermission[Doc, int64]()).GetAll(as(editor)); err == nil {
			t.Fatal("RequireAnyPermission of nothing was satisfiable, so an empty rule is a licence")
		}
	})
}

func TestRequireRoleAsksAboutMembership(t *testing.T) {
	policy := security.RequireRole[Doc, int64]("admin", "editor")

	t.Run("a member is let through", func(t *testing.T) {
		rec := crudtest.Postgres().Push(crudtest.Rows())
		if _, err := bound(rec, policy).GetAll(as(editor)); err != nil {
			t.Fatalf("a member of a named role was refused: %v", err)
		}
	})

	t.Run("a non-member is refused", func(t *testing.T) {
		guest := auth.Claims{Sub: "u-3", Roles: []auth.Role{"guest"}}
		if _, err := bound(crudtest.Postgres(), policy).GetAll(as(guest)); !errors.Is(err, crud.ErrForbidden) {
			t.Fatal("a caller in none of the named roles was let through")
		}
	})
}

func TestPerActionRefusesAVerbNobodyDeclared(t *testing.T) {
	policy := security.PerAction[Doc, int64](map[security.Action]auth.Permission{
		security.Read:   "doc:read",
		security.Update: "doc:write",
	})

	t.Run("a declared verb the caller holds is allowed", func(t *testing.T) {
		rec := crudtest.Postgres().Push(crudtest.Rows())
		if _, err := bound(rec, policy).GetAll(as(editor)); err != nil {
			t.Fatalf("a declared verb was refused: %v", err)
		}
	})

	t.Run("an undeclared verb is refused even for a caller with every permission", func(t *testing.T) {
		rec := crudtest.Postgres()
		_, err := bound(rec, policy).Delete(as(editor), 1)
		if !errors.Is(err, crud.ErrForbidden) {
			t.Fatalf("Delete was not declared and was allowed anyway: %v", err)
		}
		if len(rec.Statements()) != 0 {
			t.Fatal("an undeclared verb still reached the database")
		}
	})

	t.Run("the map is copied at declaration", func(t *testing.T) {
		m := map[security.Action]auth.Permission{security.Read: "doc:read"}
		p := security.PerAction[Doc, int64](m)
		m[security.Delete] = "doc:read"
		if _, err := bound(crudtest.Postgres(), p).Delete(as(editor), 1); err == nil {
			t.Fatal("writing to the caller's map after binding changed what is enforced")
		}
	})
}

func TestScopeAttrNarrowsInSQLAndFreezesTheColumn(t *testing.T) {
	policy := security.ScopeAttr[Doc, int64]("TenantID", "tenant")

	t.Run("the claim reaches the WHERE clause", func(t *testing.T) {
		rec := crudtest.Postgres().Push(crudtest.Rows())
		if _, err := bound(rec, policy).GetAll(as(editor)); err != nil {
			t.Fatal(err)
		}
		if got := lastWhere(rec); got != `"tenant_id" = $1` {
			t.Fatalf("where = %s, want the tenant claim", got)
		}
		if rec.Last().Args[0] != int64(7) {
			t.Fatalf("args = %v, want the claim's value", rec.Last().Args)
		}
	})

	t.Run("a create into another tenant is refused", func(t *testing.T) {
		rec := crudtest.Postgres()
		d := Doc{TenantID: 9, Title: "x"}
		if _, err := bound(rec, policy).Save(as(editor), &d); !errors.Is(err, crud.ErrForbidden) {
			t.Fatalf("a create into another tenant answered %v, want a denial", err)
		}
		if len(rec.Statements()) != 0 {
			t.Fatal("the refused insert still reached the database")
		}
	})

	t.Run("control: a create into the caller's own tenant is allowed", func(t *testing.T) {
		rec := crudtest.Postgres().Push(crudtest.Rows(docRow(1, 7, "x")))
		d := Doc{TenantID: 7, Title: "x"}
		if _, err := bound(rec, policy).Save(as(editor), &d); err != nil {
			t.Fatalf("a create into the caller's own tenant was refused: %v", err)
		}
	})

	t.Run("an update naming the scope column is refused before any SQL", func(t *testing.T) {
		rec := crudtest.Postgres()
		nine := int64(9)
		_, err := bound(rec, policy).Update(as(editor), 1, DocUpdate{TenantID: &nine})
		if !errors.Is(err, crud.ErrForbidden) {
			t.Fatalf("an update naming the frozen column answered %v", err)
		}
		if len(rec.Statements()) != 0 {
			t.Fatal("the refused update still reached the database")
		}
	})
}

func TestAMissingClaimIsADenialAndNotAZeroValue(t *testing.T) {
	policy := security.ScopeAttr[Doc, int64]("TenantID", "tenant")
	noTenant := auth.Claims{Sub: "u-9", Permissions: editor.Permissions}

	rec := crudtest.Postgres()
	_, err := bound(rec, policy).GetAll(as(noTenant))
	if !errors.Is(err, crud.ErrForbidden) {
		t.Fatalf("a principal with no tenant claim answered %v, want a denial", err)
	}
	if len(rec.Statements()) != 0 {
		t.Fatal("a missing claim compiled to a query — read as a zero value, it would be WHERE tenant_id = 0")
	}
}

type Ticket struct {
	ID    int64  `db:"id,pk,auto"`
	Owner string `db:"owner"`
	Body  string `db:"body"`
}

type TicketUpdate struct{ Body *string }

var Tickets = sqlrepo.Define[Ticket, int64, TicketUpdate]("tickets")

func TestScopeSubjectNarrowsToTheCallersOwnRows(t *testing.T) {
	rec := crudtest.Postgres().Push(crudtest.Rows())
	repository := Tickets.Bind(rec, security.Gate(security.ScopeSubject[Ticket, int64]("Owner")))
	if _, err := repository.GetAll(as(editor)); err != nil {
		t.Fatal(err)
	}
	if got := lastWhere(rec); got != `"owner" = $1` {
		t.Fatalf("where = %s, want the subject", got)
	}
	if rec.Last().Args[0] != "u-1" {
		t.Fatalf("args = %v, want the principal's subject", rec.Last().Args)
	}
}

func TestEveryPrincipalPolicyFailsClosedWithoutOne(t *testing.T) {
	for _, tc := range []struct {
		name   string
		policy security.Policy[Doc, int64]
	}{
		{"RequirePermission", security.RequirePermission[Doc, int64]("doc:read")},
		{"RequireAnyPermission", security.RequireAnyPermission[Doc, int64]("doc:read")},
		{"RequireRole", security.RequireRole[Doc, int64]("editor")},
		{"PerAction", security.PerAction[Doc, int64](map[security.Action]auth.Permission{security.Read: "doc:read"})},
		{"ScopeAttr", security.ScopeAttr[Doc, int64]("TenantID", "tenant")},
		{"ScopeSubject", security.ScopeSubject[Doc, int64]("TenantID")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := crudtest.Postgres()
			_, err := bound(rec, tc.policy).GetAll(context.Background())
			if !errors.Is(err, auth.ErrUnauthenticated) {
				t.Fatalf("an unauthenticated read answered %v, want auth.ErrUnauthenticated", err)
			}
			if len(rec.Statements()) != 0 {
				t.Fatal("an unauthenticated read reached the database")
			}
		})
	}
}

func TestTheDeclaredRequirementIsTheOneTheGateEnforces(t *testing.T) {
	policy := security.PerAction[Doc, int64](map[security.Action]auth.Permission{
		security.Read:   "doc:read",
		security.Delete: "doc:purge",
	})

	t.Run("the declaration a route would carry is the permission the gate asks for", func(t *testing.T) {
		need, declared := policy.RequiredFor(security.Delete)
		if !declared {
			t.Fatal("the policy denies an undeclared delete at request time and declares nothing for it, so a route declaration cannot be derived")
		}
		if len(need) != 1 || need[0] != "doc:purge" {
			t.Fatalf("the policy declares %v for delete, and a route declaration built from it would guard the delete route with that", need)
		}
	})

	t.Run("a caller without the declared permission is refused", func(t *testing.T) {
		rec := crudtest.Postgres()
		if _, err := bound(rec, policy).Delete(as(editor), 1); !errors.Is(err, crud.ErrForbidden) {
			t.Fatalf("a caller holding no doc:purge deleted anyway: %v", err)
		}
		if len(rec.Statements()) != 0 {
			t.Fatal("a refused delete still reached the database")
		}
	})

	t.Run("control: a caller holding the declared permission is let through", func(t *testing.T) {
		purger := editor
		purger.Permissions = append([]auth.Permission{"doc:purge"}, editor.Permissions...)
		rec := crudtest.Postgres()
		if _, err := bound(rec, policy).Delete(as(purger), 1); err != nil {
			t.Fatalf("the caller holding exactly what the policy declares was refused: %v", err)
		}
	})

	t.Run("an action nobody declared carries no declaration either", func(t *testing.T) {
		if _, declared := policy.RequiredFor(security.Create); declared {
			t.Fatal("the gate refuses a create nobody declared, and the declaration says a permission guards it")
		}
	})
}

func TestRequiringIsTheExplicitFormPerActionWraps(t *testing.T) {
	explicit := security.Requiring[Doc, int64](map[security.Action][]auth.Permission{
		security.Read: {"doc:read", "doc:admin"},
	})

	t.Run("every named permission is required, not one of them", func(t *testing.T) {
		rec := crudtest.Postgres()
		if _, err := bound(rec, explicit).GetAll(as(editor)); !errors.Is(err, crud.ErrForbidden) {
			t.Fatalf("a caller holding doc:read but not doc:admin read anyway: %v", err)
		}
	})

	t.Run("control: a caller holding both reads", func(t *testing.T) {
		admin := editor
		admin.Permissions = append([]auth.Permission{"doc:admin"}, editor.Permissions...)
		rec := crudtest.Postgres().Push(crudtest.Rows())
		if _, err := bound(rec, explicit).GetAll(as(admin)); err != nil {
			t.Fatalf("a caller holding both named permissions was refused: %v", err)
		}
	})

	t.Run("the wrapper declares what the explicit form declares", func(t *testing.T) {
		wrapped := security.PerAction[Doc, int64](map[security.Action]auth.Permission{security.Read: "doc:read"})
		one := security.Requiring[Doc, int64](map[security.Action][]auth.Permission{security.Read: {"doc:read"}})
		got, declared := wrapped.RequiredFor(security.Read)
		want, _ := one.RequiredFor(security.Read)
		if !declared || !slices.Equal(got, want) {
			t.Fatalf("PerAction declares %v where the explicit constructor declares %v", got, want)
		}
	})
}

func TestADeclarationThatNamesNoActionRefusesEveryOne(t *testing.T) {
	empty := security.Requiring[Doc, int64](nil)

	rec := crudtest.Postgres()
	if _, err := bound(rec, empty).GetAll(as(editor)); !errors.Is(err, crud.ErrForbidden) {
		t.Fatalf("a policy that declares no action let a read through: %v", err)
	}
	if len(rec.Statements()) != 0 {
		t.Fatal("a refused read still reached the database")
	}
}

func TestCombiningDeclarationsKeepsOnlyWhatEveryDeclarationAllows(t *testing.T) {
	combined := security.Combine(
		security.PerAction[Doc, int64](map[security.Action]auth.Permission{
			security.Read:   "doc:read",
			security.Delete: "doc:purge",
		}),
		security.PerAction[Doc, int64](map[security.Action]auth.Permission{
			security.Read:    "doc:admin",
			security.Restore: "doc:restore",
		}),
	)

	t.Run("an action both declare needs both permissions", func(t *testing.T) {
		need, declared := combined.RequiredFor(security.Read)
		if !declared || !slices.Equal(need, []auth.Permission{"doc:read", "doc:admin"}) {
			t.Fatalf("the combined policy declares %v for read, want both halves", need)
		}
		rec := crudtest.Postgres()
		if _, err := bound(rec, combined).GetAll(as(editor)); !errors.Is(err, crud.ErrForbidden) {
			t.Fatalf("a caller holding only the first half read anyway: %v", err)
		}
	})

	t.Run("an action only one of them declares is declared by neither", func(t *testing.T) {
		for _, action := range []security.Action{security.Delete, security.Restore} {
			if _, declared := combined.RequiredFor(action); declared {
				t.Fatalf("the combined policy declares %s, and the gate refuses it: the declaration would guard a route the repository never serves", action)
			}
		}
		purger := editor
		purger.Permissions = append([]auth.Permission{"doc:purge"}, editor.Permissions...)
		rec := crudtest.Postgres()
		if _, err := bound(rec, combined).Delete(as(purger), 1); !errors.Is(err, crud.ErrForbidden) {
			t.Fatalf("the half that declares nothing for delete stopped refusing it: %v", err)
		}
	})
}
