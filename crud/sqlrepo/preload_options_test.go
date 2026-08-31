package sqlrepo_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/crudtest"
	"github.com/frostgrove/vv/crud/sqlrepo"
)

type preloadOptionParent struct {
	ID       int64                `db:"id,pk"`
	Children []preloadOptionChild `rel:"has_many,fk=ParentID"`
}

type preloadOptionChild struct {
	ID       int64              `db:"id,pk"`
	ParentID int64              `db:"parent_id"`
	Toys     []preloadOptionToy `rel:"has_many,fk=ChildID"`
}

type preloadOptionToy struct {
	ID      int64 `db:"id,pk"`
	ChildID int64 `db:"child_id"`
}

var preloadOptionParents = sqlrepo.Define[preloadOptionParent, int64, struct{}]("preload_option_parents")

func TestRepositoryValidatesPreloadOptionsEvenWhenTheRootIsEmpty(t *testing.T) {
	rec := crudtest.Postgres().Push(crudtest.Rows())
	_, err := preloadOptionParents.Bind(rec).GetAll(context.Background(),
		crud.PreloadWhere("Children", crud.Select("ID")))
	var schemaErr *crud.SchemaError
	if !errors.As(err, &schemaErr) || !strings.Contains(schemaErr.Reason, "projection") {
		t.Fatalf("GetAll error = %T %v, want preload projection SchemaError", err, err)
	}
	if len(rec.Statements()) != 1 {
		t.Fatalf("empty root executed %d statements, want only its root read: %v", len(rec.Statements()), rec.SQL())
	}
}

func TestRepositoryGlobalPreloadRowsCapsEveryNestedHop(t *testing.T) {
	rec := crudtest.Postgres().Push(
		crudtest.Rows([]any{int64(1)}),
		crudtest.Rows([]any{int64(10), int64(1)}),
		crudtest.Rows(
			[]any{int64(100), int64(10)},
			[]any{int64(101), int64(10)},
		),
	)
	_, err := preloadOptionParents.Bind(rec).GetAll(context.Background(),
		crud.PreloadCap("Children.Toys", 3),
		crud.PreloadRows(1))
	if err == nil || !strings.Contains(err.Error(), "preload exceeds") {
		t.Fatalf("GetAll error = %v, want second-hop global cap refusal", err)
	}
	statements := rec.Statements()
	if len(statements) != 3 {
		t.Fatalf("GetAll statements = %d, want root and two preload hops: %v", len(statements), rec.SQL())
	}
	for i := 1; i < len(statements); i++ {
		if !strings.Contains(statements[i].SQL, "LIMIT 2") {
			t.Fatalf("preload hop %d SQL = %s, want stricter global cap plus one", i, statements[i].SQL)
		}
	}
}

func TestRepositoryRefusesNegativeGlobalPreloadRows(t *testing.T) {
	rec := crudtest.Postgres().Push(crudtest.Rows())
	_, err := preloadOptionParents.Bind(rec).GetAll(context.Background(),
		crud.Preload("Children"), crud.PreloadRows(-1))
	if err == nil || !strings.Contains(err.Error(), "cannot be negative") {
		t.Fatalf("GetAll error = %v, want negative global cap refusal", err)
	}
	if len(rec.Statements()) != 1 {
		t.Fatalf("negative global cap executed child SQL: %v", rec.SQL())
	}
}
