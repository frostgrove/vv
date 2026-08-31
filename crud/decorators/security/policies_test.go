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

type Wide struct {
	ID       uint64 `db:"id,pk,auto"`
	TenantID uint   `db:"tenant_id"`
	Name     string `db:"name"`
}

type WideUpdate struct{ Name *string }

var Wides = sqlrepo.Define[Wide, uint64, WideUpdate]("wides")

func TestAClaimOfADifferentWidthThanTheColumnStillWorks(t *testing.T) {
	policy := security.ScopeField[Wide, uint64]("TenantID", func(context.Context) (any, error) {
		return int64(7), nil
	})
	rec := crudtest.Postgres().ExecResult(crud.Result{RowsAffected: 1})
	repository := Wides.Bind(rec, security.Gate(policy))

	if _, err := repository.GetAll(context.Background()); err != nil {
		t.Fatalf("a read under a width-mismatched claim failed: %v", err)
	}
	if !strings.Contains(rec.Last().SQL, `"tenant_id" = $`) {
		t.Fatalf("the read was not narrowed:\n%s", rec.Last().SQL)
	}

	row := Wide{TenantID: 7, Name: "mine"}
	_, err := repository.Save(context.Background(), &row)
	if errors.Is(err, security.ErrForbidden) {
		t.Fatalf("a create carrying the caller's own tenant was denied: %v", err)
	}

	other := Wide{TenantID: 9, Name: "theirs"}
	if _, err := repository.Save(context.Background(), &other); !errors.Is(err, security.ErrForbidden) {
		t.Fatalf("a create for another tenant was allowed: %v", err)
	}
}

func TestAnUncomparableClaimTypeFailsClosed(t *testing.T) {
	policy := security.ScopeField[Wide, uint64]("TenantID", func(context.Context) (any, error) {
		return struct{ X int }{1}, nil
	})
	rec := crudtest.Postgres()
	_, err := Wides.Bind(rec, security.Gate(policy)).GetAll(context.Background())
	if !errors.Is(err, security.ErrForbidden) {
		t.Fatalf("err = %v, want ErrForbidden", err)
	}
	if len(rec.Statements()) != 0 {
		t.Fatalf("an incompatible claim reached the database: %v", rec.SQL())
	}
}

type RelationValue struct {
	ID       int64               `db:"id,pk,auto"`
	TenantID int64               `db:"tenant_id"`
	Notes    []RelationValueNote `rel:"has_many,fk=RelationValueID"`
}

type RelationValueNote struct {
	ID              int64 `db:"id,pk,auto"`
	RelationValueID int64 `db:"relation_value_id"`
	TenantID        uint  `db:"tenant_id"`
}

func TestRelationScopeReconcilesItsValueToTheFarSideColumn(t *testing.T) {
	p := security.ScopeRelationField[RelationValue, int64]("Notes", "TenantID", func(context.Context) (any, error) {
		return int64(7), nil
	})
	rs, err := p.RelationScopes(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if rs == nil || rs.Empty() {
		t.Fatal("relation scope is missing")
	}
}

func TestRelationScopeRejectsANilValueBeforeItReachesTheDriver(t *testing.T) {
	p := security.ScopeRelationField[RelationValue, int64]("Notes", "TenantID", func(context.Context) (any, error) {
		return nil, nil
	})
	_, err := p.RelationScopes(context.Background())
	if !errors.Is(err, security.ErrForbidden) {
		t.Fatalf("err = %v, want ErrForbidden", err)
	}
}

func TestRelationScopeRejectsUnsafeNumericToStringConversion(t *testing.T) {
	type TextNote struct {
		ID              int64  `db:"id,pk,auto"`
		RelationValueID int64  `db:"relation_value_id"`
		TenantID        string `db:"tenant_id"`
	}
	type TextRoot struct {
		ID    int64      `db:"id,pk,auto"`
		Notes []TextNote `rel:"has_many,fk=RelationValueID"`
	}
	p := security.ScopeRelationField[TextRoot, int64]("Notes", "TenantID", func(context.Context) (any, error) {
		return int64(42), nil
	})
	_, err := p.RelationScopes(context.Background())
	if !errors.Is(err, security.ErrForbidden) {
		t.Fatalf("err = %v, want ErrForbidden instead of a string converted from an integer", err)
	}
}

func TestScopeFieldRefusesALossyFloatTenantConversion(t *testing.T) {
	type FloatTenant struct {
		ID       int64   `db:"id,pk"`
		TenantID float32 `db:"tenant_id"`
	}

	p := security.ScopeField[FloatTenant, int64]("TenantID", func(context.Context) (any, error) {
		return float64(16_777_217), nil
	})
	_, err := p.Scope(context.Background())
	if !errors.Is(err, security.ErrForbidden) {
		t.Fatalf("err = %v, want forbidden rather than a rounded tenant id", err)
	}
}
