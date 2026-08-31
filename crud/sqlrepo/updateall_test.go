package sqlrepo_test

import (
	"context"
	"strings"
	"testing"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/crudtest"
)

func TestUpdateAllIsOneStatementForTheWholeFilter(t *testing.T) {
	rec := crudtest.Postgres().ExecResult(crud.Result{RowsAffected: 3})

	n, err := Users.Bind(rec).UpdateAll(context.Background(),
		UserUpdate{Name: ptr("Renamed")}, crud.Where(crud.Eq("TenantID", int64(7))))
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Fatalf("n = %d, want the 3 rows the database reported", n)
	}
	if got := len(rec.Statements()); got != 1 {
		t.Fatalf("%d statements for one filtered update: %v", got, rec.SQL())
	}
	wantSQL(t, rec.Last().SQL, `UPDATE "users" SET "name" = $1 WHERE "tenant_id" = $2`)
	if rec.Last().Args[0] != "Renamed" || rec.Last().Args[1] != int64(7) {
		t.Fatalf("args = %v", rec.Last().Args)
	}
}

func TestUpdateAllWritesEveryDefinedFieldAndNothingElse(t *testing.T) {
	ctx := context.Background()

	t.Run("an undefined field never reaches the statement", func(t *testing.T) {
		rec := crudtest.Postgres().ExecResult(crud.Result{RowsAffected: 1})
		if _, err := Users.Bind(rec).UpdateAll(ctx, UserUpdate{Name: ptr("N")}); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(rec.Last().SQL, `"age"`) {
			t.Fatalf("an undefined DTO field was written: %s", rec.Last().SQL)
		}
	})

	t.Run("a null Opt writes NULL", func(t *testing.T) {
		rec := crudtest.Postgres().ExecResult(crud.Result{RowsAffected: 1})
		if _, err := Users.Bind(rec).UpdateAll(ctx, UserUpdate{Age: crud.Null[int]()}); err != nil {
			t.Fatal(err)
		}
		wantSQL(t, rec.Last().SQL, `UPDATE "users" SET "age" = $1`)
		if rec.Last().Args[0] != nil {
			t.Fatalf("age was bound as %#v, want NULL", rec.Last().Args[0])
		}
	})

	t.Run("a DTO that defines nothing writes nothing", func(t *testing.T) {
		rec := crudtest.Postgres().ExecResult(crud.Result{RowsAffected: 99})
		n, err := Users.Bind(rec).UpdateAll(ctx, UserUpdate{})
		if err != nil {
			t.Fatal(err)
		}
		if n != 0 || len(rec.Statements()) != 0 {
			t.Fatalf("n = %d after %d statements: an empty DTO must not reach the database", n, len(rec.Statements()))
		}
	})
}

func TestUpdateAllCarriesTheRepositoryScope(t *testing.T) {
	rec := crudtest.Postgres().ExecResult(crud.Result{RowsAffected: 1})

	if _, err := scopedUsers.Bind(rec).UpdateAll(context.Background(),
		UserUpdate{Name: ptr("N")}, crud.Where(crud.Eq("Email", "a@b.c"))); err != nil {
		t.Fatal(err)
	}
	wantSQL(t, rec.Last().SQL,
		`UPDATE "users" SET "name" = $1 WHERE ("tenant_id" = $2 AND "email" = $3)`)
	if rec.Last().Args[1] != int64(1) {
		t.Fatalf("the scope's tenant was not bound: %v", rec.Last().Args)
	}
}
