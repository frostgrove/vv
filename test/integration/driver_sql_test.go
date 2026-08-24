//go:build integration

package integration

import (
	"context"
	"errors"
	"testing"

	"github.com/shardit-io/rx/adapter/crudsql"
	"github.com/shardit-io/rx/crud"
)

// database/sql over pgx's stdlib driver.
func TestDatabaseSQLPostgres(t *testing.T) {
	RunSuite(t, Target{Name: "database/sql+postgres", DB: "postgres", Source: crudsql.Postgres(pgDB)})
}

// database/sql over go-sql-driver/mysql. The default upsert form uses VALUES(),
// which every MySQL and MariaDB understands.
func TestDatabaseSQLMySQL(t *testing.T) {
	RunSuite(t, Target{Name: "database/sql+mysql", DB: "mysql", Source: crudsql.MySQL(myDB)})
}

// The same suite with the modern `AS new` row alias (MySQL 8.0.19+).
func TestDatabaseSQLMySQLRowAlias(t *testing.T) {
	RunSuite(t, Target{
		Name:   "database/sql+mysql(row alias)",
		DB:     "mysql",
		Source: crudsql.Open(myDB, crud.MySQL{RowAlias: true}),
	})
}

// A transaction owned by the caller, shared with rx-crud through the context.
func TestDatabaseSQLSharedTransaction(t *testing.T) {
	ctx := context.Background()
	truncate(t, pgDB)
	repo := Users.Bind(crudsql.Postgres(pgDB))

	tx, err := pgDB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()

	txCtx := crud.WithExecutor(ctx, crudsql.From(tx))
	u := User{TenantID: 1, Email: "sql-tx@x.io", Name: "Joined"}
	if err := repo.Save(txCtx, &u); err != nil {
		t.Fatal(err)
	}

	var name string
	if err := tx.QueryRowContext(ctx, "SELECT name FROM users WHERE id = $1", u.ID).Scan(&name); err != nil {
		t.Fatal(err)
	}
	if name != "Joined" {
		t.Fatalf("raw read back %q", name)
	}
	if _, err := repo.GetByID(ctx, u.ID); !errors.Is(err, crud.ErrNotFound) {
		t.Fatalf("err = %v: the write leaked out of the transaction", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.GetByID(ctx, u.ID); err != nil {
		t.Fatalf("after commit: %v", err)
	}
}

// Inside a transaction, a nested Tx becomes a SAVEPOINT.
func TestDatabaseSQLSavepoint(t *testing.T) {
	ctx := context.Background()
	truncate(t, pgDB)
	src := crudsql.Postgres(pgDB)
	repo := Users.Bind(src)

	err := crud.InTx(ctx, src, func(ctx context.Context) error {
		keep := User{TenantID: 1, Email: "keep@x.io", Name: "keep"}
		if err := repo.Save(ctx, &keep); err != nil {
			return err
		}
		ex, _ := crud.ExecutorFrom(ctx)
		sp, err := ex.(crud.Beginner).Begin(ctx)
		if err != nil {
			return err
		}
		drop := User{TenantID: 1, Email: "drop@x.io", Name: "drop"}
		if err := repo.Save(crud.WithExecutor(ctx, sp), &drop); err != nil {
			return err
		}
		return sp.Rollback(ctx)
	})
	if err != nil {
		t.Fatal(err)
	}
	all, err := repo.GetAll(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 || all[0].Email != "keep@x.io" {
		t.Fatalf("rows = %+v, want only the kept one", all)
	}
}
