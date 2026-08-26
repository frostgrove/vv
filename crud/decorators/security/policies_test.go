package security_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/crudtest"
	"github.com/frostgrove/vv/crud/decorators/security"
	"github.com/frostgrove/vv/crud/sqlrepo"
)

// Wide is what these tests need that the package's other fixtures do not give:
// a tenant column that is not int64, so a JSON claim — which decodes as int64 or
// float64 — does not match it by exact type.
type Wide struct {
	ID       uint64 `db:"id,pk,auto"`
	TenantID uint   `db:"tenant_id"`
	Name     string `db:"name"`
}

type WideUpdate struct{ Name *string }

var Wides = sqlrepo.Define[Wide, uint64, WideUpdate]("wides")

// A tenant claim of a different integer width than the column still scopes and
// still authorises.
//
// The two halves of ScopeField consumed the extractor's `any` differently and
// only one of them coerced. Scope binds it as a parameter, so the engine widens
// it and reads worked; Inspect compares through crud.EqualValues, which is exact
// reflect.Type identity — so an int64 claim against a uint column read perfectly
// and denied **every create**, at request time, on a policy that looks right.
// The shipped gorm guide's own `ScopeAttr[Member, uint]("TenantID", "tenant")`
// against an int64 JWT claim is the reproduction.
func TestAClaimOfADifferentWidthThanTheColumnStillWorks(t *testing.T) {
	// The column is uint; the extractor answers int64, as a JSON claim does.
	policy := security.ScopeField[Wide, uint64]("TenantID", func(context.Context) (any, error) {
		return int64(7), nil
	})
	rec := crudtest.Postgres().ExecResult(crud.Result{RowsAffected: 1})
	repo := Wides.Bind(rec, security.Gate(policy))

	// The read narrows, and binds the value the column can hold.
	if _, err := repo.GetAll(context.Background()); err != nil {
		t.Fatalf("a read under a width-mismatched claim failed: %v", err)
	}
	if !strings.Contains(rec.Last().SQL, `"tenant_id" = $`) {
		t.Fatalf("the read was not narrowed:\n%s", rec.Last().SQL)
	}

	// And the write is authorised rather than denied. This is the half that was
	// broken: every create refused with "row belongs to a different TenantID".
	row := Wide{TenantID: 7, Name: "mine"}
	err := repo.Save(context.Background(), &row)
	if errors.Is(err, security.ErrForbidden) {
		t.Fatalf("a create carrying the caller's own tenant was denied: %v", err)
	}

	// The control: a row of somebody else's tenant is still refused, so the
	// conversion did not turn the check off.
	other := Wide{TenantID: 9, Name: "theirs"}
	if err := repo.Save(context.Background(), &other); !errors.Is(err, security.ErrForbidden) {
		t.Fatalf("a create for another tenant was allowed: %v", err)
	}
}

// An extractor whose type cannot be compared with the column at all fails where
// it is written, not on the first write.
func TestAnUncomparableClaimTypePanicsRatherThanDenyingEverything(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("a claim type that cannot be compared with the column was accepted; every write would be denied at request time")
		}
	}()
	policy := security.ScopeField[Wide, uint64]("TenantID", func(context.Context) (any, error) {
		return struct{ X int }{1}, nil
	})
	// The panic is at first use, which is where the extractor is first called.
	rec := crudtest.Postgres()
	_, _ = Wides.Bind(rec, security.Gate(policy)).GetAll(context.Background())
}
