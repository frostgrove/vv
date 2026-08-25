//go:build integration

package integration

import (
	"context"
	"database/sql"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/shardit-io/vv/crud"
	"github.com/shardit-io/vv/crud/adapter/crudpgx"
	"github.com/shardit-io/vv/crud/adapter/crudsql"
	"github.com/shardit-io/vv/test/sqlcgen"
	"github.com/shardit-io/vv/test/sqlcmysql"
	"github.com/shardit-io/vv/test/sqlcpgx"
)

// sqlc generates hand-written queries; vv does the boring CRUD. The point
// of these tests is that they can share one transaction, so a handler can mix
// both without a second connection or a second commit.

func TestSqlcDatabaseSQLPostgres(t *testing.T) {
	ctx := context.Background()
	truncate(t, pgDB)
	repo := Users.Bind(crudsql.Postgres(pgDB))

	tx, err := pgDB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()

	// One *sql.Tx, handed to both.
	q := sqlcgen.New(tx)
	txCtx := crud.WithExecutor(ctx, crudsql.From(tx))

	created, err := q.CreateUser(ctx, sqlcgen.CreateUserParams{
		TenantID: 1, Email: "sqlc@x.io", Name: "BySqlc",
		Age: sql.NullInt32{Int32: 40, Valid: true}, Active: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	// vv reads what sqlc wrote.
	got, err := repo.GetByID(txCtx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "BySqlc" {
		t.Fatalf("github.com/shardit-io/vv read back %+v", got)
	}
	if age, ok := got.Age.Get(); !ok || age != 40 {
		t.Fatalf("age = %v", got.Age)
	}

	// And sqlc reads what vv wrote.
	u := User{TenantID: 1, Email: "vv@x.io", Name: "ByVV", Active: true}
	if err := repo.Save(txCtx, &u); err != nil {
		t.Fatal(err)
	}
	back, err := q.GetUser(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if back.Name != "ByVV" || back.Age.Valid {
		t.Fatalf("sqlc read back %+v", back)
	}
	n, err := q.CountUsersByTenant(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("sqlc counted %d rows", n)
	}

	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if n, err := repo.Count(ctx); err != nil || n != 2 {
		t.Fatalf("after commit count = %d err = %v", n, err)
	}
}

func TestSqlcPgx(t *testing.T) {
	ctx := context.Background()
	truncate(t, pgDB)
	repo := Users.Bind(crudpgx.Open(pgPool))

	tx, err := pgPool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)

	// pgx.Tx satisfies sqlc's DBTX and vv's Queryer at the same time, so
	// the exact same object goes into both.
	q := sqlcpgx.New(tx)
	txCtx := crud.WithExecutor(ctx, crudpgx.From(tx))

	created, err := q.CreateUser(ctx, sqlcpgx.CreateUserParams{
		TenantID: 2, Email: "sqlcpgx@x.io", Name: "BySqlc",
		Age: pgtype.Int4{Int32: 33, Valid: true}, Active: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := repo.GetByID(txCtx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "BySqlc" {
		t.Fatalf("github.com/shardit-io/vv read back %+v", got)
	}

	u := User{TenantID: 2, Email: "vv-pgx@x.io", Name: "ByVV", Age: crud.Set(7)}
	if err := repo.Save(txCtx, &u); err != nil {
		t.Fatal(err)
	}
	back, err := q.GetUser(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if back.Name != "ByVV" || !back.Age.Valid || back.Age.Int32 != 7 {
		t.Fatalf("sqlc read back %+v", back)
	}
	if n, err := q.CountUsersByTenant(ctx, 2); err != nil || n != 2 {
		t.Fatalf("sqlc counted %d err = %v", n, err)
	}

	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestSqlcMySQL(t *testing.T) {
	ctx := context.Background()
	truncate(t, myDB)
	repo := Users.Bind(crudsql.MySQL(myDB))

	tx, err := myDB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()

	q := sqlcmysql.New(tx)
	txCtx := crud.WithExecutor(ctx, crudsql.From(tx))

	res, err := q.CreateUser(ctx, sqlcmysql.CreateUserParams{
		TenantID: 3, Email: "sqlcmy@x.io", Name: "BySqlc",
		Age: sql.NullInt32{Int32: 50, Valid: true}, Active: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	got, err := repo.GetByID(txCtx, id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "BySqlc" {
		t.Fatalf("github.com/shardit-io/vv read back %+v", got)
	}

	u := User{TenantID: 3, Email: "vv-my@x.io", Name: "ByVV", Active: true}
	if err := repo.Save(txCtx, &u); err != nil {
		t.Fatal(err)
	}
	back, err := q.GetUser(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if back.Name != "ByVV" {
		t.Fatalf("sqlc read back %+v", back)
	}
	if n, err := q.CountUsersByTenant(ctx, 3); err != nil || n != 2 {
		t.Fatalf("sqlc counted %d err = %v", n, err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}
