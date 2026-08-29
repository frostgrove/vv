//go:build integration

package integration

import (
	"context"
	"errors"
	"testing"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/adapter/crudpgx"
)

func TestPgx(t *testing.T) {
	RunSuite(t, Target{Name: "pgx", DB: "postgres", Source: crudpgx.Open(pgPool)})
}

// pgx owns the transaction; vv joins it through the context. One physical
// transaction, two APIs.
func TestPgxSharedTransaction(t *testing.T) {
	ctx := context.Background()
	truncate(t, pgDB)
	repository := Users.Bind(crudpgx.Open(pgPool))

	tx, err := pgPool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	txCtx := crud.WithExecutor(ctx, crudpgx.From(tx))

	u := User{TenantID: 1, Email: "pgx-tx@x.io", Name: "Joined"}
	if err := repository.Save(txCtx, &u); err != nil {
		t.Fatal(err)
	}
	// A raw pgx statement in the same transaction sees the row vv wrote.
	var name string
	if err := tx.QueryRow(ctx, "SELECT name FROM users WHERE id = $1", u.ID).Scan(&name); err != nil {
		t.Fatal(err)
	}
	if name != "Joined" {
		t.Fatalf("pgx read back %q", name)
	}
	// And vv sees what pgx writes.
	if _, err := tx.Exec(ctx, "UPDATE users SET name = 'ByPgx' WHERE id = $1", u.ID); err != nil {
		t.Fatal(err)
	}
	got, err := repository.GetByID(txCtx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "ByPgx" {
		t.Fatalf("github.com/frostgrove/vv read back %q", got.Name)
	}
	// Outside the transaction the row does not exist yet.
	if _, err := repository.GetByID(ctx, u.ID); !errors.Is(err, crud.ErrNotFound) {
		t.Fatalf("err = %v: the write leaked out of the transaction", err)
	}

	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if n, err := repository.Count(ctx); err != nil || n != 0 {
		t.Fatalf("count = %d err = %v: the rollback did not take", n, err)
	}
}

// pgx gives nested Begin savepoint semantics, so an inner failure can be undone
// without losing the outer transaction.
func TestPgxNestedSavepoint(t *testing.T) {
	ctx := context.Background()
	truncate(t, pgDB)
	source := crudpgx.Open(pgPool)
	repository := Users.Bind(source)

	err := crud.InTx(ctx, source, func(ctx context.Context) error {
		outer := User{TenantID: 1, Email: "outer@x.io", Name: "outer"}
		if err := repository.Save(ctx, &outer); err != nil {
			return err
		}
		inner, _ := crud.ExecutorFrom(ctx)
		sp, err := inner.(crud.Beginner).Begin(ctx)
		if err != nil {
			return err
		}
		spCtx := crud.WithExecutor(ctx, sp)
		doomed := User{TenantID: 1, Email: "doomed@x.io", Name: "doomed"}
		if err := repository.Save(spCtx, &doomed); err != nil {
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
	if len(all) != 1 || all[0].Email != "outer@x.io" {
		t.Fatalf("rows = %+v, want just the outer one", all)
	}
}

// The COPY fast path is picked up automatically when the executor offers it.
func TestPgxBulkCopy(t *testing.T) {
	ctx := context.Background()
	truncate(t, pgDB)
	source := crudpgx.Open(pgPool)

	bulk, ok := any(source).(crud.BulkInserter)
	if !ok {
		t.Fatal("the pgx adapter should implement crud.BulkInserter")
	}
	rows := [][]any{
		{int64(1), "copy-1@x.io", "c1", 20, true},
		{int64(1), "copy-2@x.io", "c2", 21, true},
	}
	n, err := bulk.CopyFrom(ctx, "users", []string{"tenant_id", "email", "name", "age", "active"}, rows)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("copied %d rows", n)
	}
	if got, err := Users.Bind(source).Count(ctx); err != nil || got != 2 {
		t.Fatalf("count = %d err = %v", got, err)
	}
}
