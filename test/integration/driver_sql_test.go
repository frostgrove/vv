//go:build integration

package integration

import (
	"context"
	"errors"
	"testing"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/adapter/crudsql"
)

func TestDatabaseSQLPostgres(t *testing.T) {
	RunSuite(t, Target{Name: "database/sql+postgres", DB: "postgres", Source: crudsql.Postgres(pgDB)})
}

func TestDatabaseSQLMySQL(t *testing.T) {
	RunSuite(t, Target{Name: "database/sql+mysql", DB: "mysql", Source: crudsql.MySQL(myDB)})
}

func TestDatabaseSQLMariaDB(t *testing.T) {
	RunSuite(t, Target{Name: "database/sql+mariadb", DB: "mysql", Source: crudsql.MySQL(mariaDB)})
}

func TestDatabaseSQLMySQLRowAlias(t *testing.T) {
	RunSuite(t, Target{
		Name:   "database/sql+mysql(row alias)",
		DB:     "mysql",
		Source: crudsql.Open(myDB, crud.MySQL{RowAlias: true}),
	})
}

func TestDatabaseSQLSharedTransaction(t *testing.T) {
	ctx := context.Background()
	truncate(t, pgDB)
	source := crudsql.Postgres(pgDB)
	repository := Users.Bind(source)

	tx, err := pgDB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()

	txCtx := source.BindExecutor(ctx, tx)
	u := User{TenantID: 1, Email: "sql-tx@x.io", Name: "Joined"}
	if stored, err := repository.Save(txCtx, &u); err != nil {
		t.Fatal(err)
	} else {
		u = stored
	}

	var name string
	if err := tx.QueryRowContext(ctx, "SELECT name FROM users WHERE id = $1", u.ID).Scan(&name); err != nil {
		t.Fatal(err)
	}
	if name != "Joined" {
		t.Fatalf("raw read back %q", name)
	}
	if _, err := repository.GetByID(ctx, u.ID); !errors.Is(err, crud.ErrNotFound) {
		t.Fatalf("err = %v: the write leaked out of the transaction", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.GetByID(ctx, u.ID); err != nil {
		t.Fatalf("after commit: %v", err)
	}
}

func TestDatabaseSQLSavepoint(t *testing.T) {
	ctx := context.Background()
	truncate(t, pgDB)
	source := crudsql.Postgres(pgDB)
	repository := Users.Bind(source)

	err := crud.InTx(ctx, source, func(ctx context.Context) error {
		keep := User{TenantID: 1, Email: "keep@x.io", Name: "keep"}
		if _, err := repository.Save(ctx, &keep); err != nil {
			return err
		}
		ex, _ := crud.ExecutorFrom(ctx)
		sp, err := ex.(crud.Beginner).Begin(ctx)
		if err != nil {
			return err
		}
		drop := User{TenantID: 1, Email: "drop@x.io", Name: "drop"}
		if _, err := repository.Save(crud.BindExecutor(ctx, source, sp), &drop); err != nil {
			return err
		}
		return sp.Rollback(ctx)
	})
	if err != nil {
		t.Fatal(err)
	}
	all, err := repository.GetAll(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 || all[0].Email != "keep@x.io" {
		t.Fatalf("rows = %+v, want only the kept one", all)
	}
}
