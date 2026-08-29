//go:build integration

package integration

import (
	"context"
	"testing"

	"github.com/jmoiron/sqlx"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/adapter/crudsql"
)

// sqlx handles are *sql.DB and *sql.Tx underneath, so crudsql takes them as is.
func TestSqlx(t *testing.T) {
	for _, tc := range []struct {
		name     string
		database *sqlx.DB
		d        crud.Dialect
	}{
		{"sqlx+postgres", sqlx.NewDb(pgDB, "pgx"), crud.Postgres{}},
		{"sqlx+mysql", sqlx.NewDb(myDB, "mysql"), crud.MySQL{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			RunSuite(t, Target{
				Name:   tc.name,
				DB:     tc.name,
				Source: crudsql.Open(tc.database.DB, tc.d),
			})
		})
	}
}

// One transaction, two APIs: sqlx's StructScan and vv's repository.
func TestSqlxSharedTransaction(t *testing.T) {
	ctx := context.Background()
	truncate(t, pgDB)
	database := sqlx.NewDb(pgDB, "pgx")
	repository := Users.Bind(crudsql.Open(database.DB, crud.Postgres{}))

	tx, err := database.BeginTxx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()

	// crudsql.From accepts the sqlx transaction directly.
	txCtx := crud.WithExecutor(ctx, crudsql.From(tx))
	u := User{TenantID: 1, Email: "sqlx@x.io", Name: "Sqlx", Age: crud.Set(44)}
	if stored, err := repository.Save(txCtx, &u); err != nil {
		t.Fatal(err)
	} else {
		u = stored
	}

	var row struct {
		ID   int64  `db:"id"`
		Name string `db:"name"`
		Age  *int   `db:"age"`
	}
	if err := tx.GetContext(ctx, &row, "SELECT id, name, age FROM users WHERE id = $1", u.ID); err != nil {
		t.Fatal(err)
	}
	if row.Name != "Sqlx" || row.Age == nil || *row.Age != 44 {
		t.Fatalf("sqlx read back %+v", row)
	}

	// A row written by sqlx is visible to the repository in the same transaction.
	if _, err := tx.ExecContext(ctx,
		"INSERT INTO users (tenant_id, email, name, active) VALUES ($1, $2, $3, true)",
		1, "by-sqlx@x.io", "BySqlx"); err != nil {
		t.Fatal(err)
	}
	n, err := repository.Count(txCtx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("count inside the transaction = %d, want 2", n)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}
