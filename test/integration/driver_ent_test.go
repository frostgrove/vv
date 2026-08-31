//go:build integration

package integration

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/adapter/crudsql"
	"github.com/frostgrove/vv/test/ent"
	entuser "github.com/frostgrove/vv/test/ent/user"
)

var (
	_ crudsql.Queryer = (*ent.Client)(nil)
	_ crudsql.Queryer = (*ent.Tx)(nil)
)

type entSource struct {
	crudsql.Executor
	client *ent.Client
	d      crud.Dialect
}

func (this entSource) Dialect() crud.Dialect { return this.d }

func (this entSource) Begin(ctx context.Context) (crud.Tx, error) {
	tx, err := this.client.Tx(ctx)
	if err != nil {
		return nil, err
	}
	return entTx{Executor: crudsql.From(tx), tx: tx}, nil
}

type entTx struct {
	crudsql.Executor
	tx *ent.Tx
}

func (this entTx) Commit(context.Context) error   { return this.tx.Commit() }
func (this entTx) Rollback(context.Context) error { return this.tx.Rollback() }

func newEntSource(client *ent.Client, d crud.Dialect) entSource {
	return entSource{Executor: crudsql.From(client), client: client, d: d}
}

func entClient(database *sql.DB, d string) *ent.Client {
	return ent.NewClient(ent.Driver(entsql.OpenDB(d, database)))
}

func TestEnt(t *testing.T) {
	for _, tc := range []struct {
		name    string
		client  *ent.Client
		dialect crud.Dialect
	}{
		{"ent+postgres", entClient(pgDB, dialect.Postgres), crud.Postgres{}},
		{"ent+mysql", entClient(myDB, dialect.MySQL), crud.MySQL{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			RunSuite(t, Target{Name: tc.name, DB: tc.name, Source: newEntSource(tc.client, tc.dialect)})
		})
	}
}

func TestEntSharedTransaction(t *testing.T) {
	ctx := context.Background()
	truncate(t, pgDB)
	client := entClient(pgDB, dialect.Postgres)
	source := crudsql.Postgres(pgDB)
	repository := Users.Bind(source)

	tx, err := client.Tx(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()

	txCtx := source.BindExecutor(ctx, tx)

	byEnt, err := tx.User.Create().
		SetTenantID(1).SetEmail("ent@x.io").SetName("ByEnt").SetAge(28).SetActive(true).
		Save(ctx)
	if err != nil {
		t.Fatal(err)
	}

	got, err := repository.GetByID(txCtx, byEnt.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "ByEnt" {
		t.Fatalf("github.com/frostgrove/vv read back %+v", got)
	}
	if age, ok := got.Age.Get(); !ok || age != 28 {
		t.Fatalf("age = %v", got.Age)
	}

	u := User{TenantID: 1, Email: "rx@x.io", Name: "ByVV", Active: true}
	if stored, err := repository.Save(txCtx, &u); err != nil {
		t.Fatal(err)
	} else {
		u = stored
	}
	back, err := tx.User.Query().Where(entuser.IDEQ(u.ID)).Only(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if back.Name != "ByVV" || back.Age != nil {
		t.Fatalf("ent read back %+v", back)
	}

	if _, err := repository.Update(txCtx, byEnt.ID, UserUpdate{Name: ptr("Renamed"), Age: crud.Null[int]()}); err != nil {
		t.Fatal(err)
	}
	renamed, err := tx.User.Get(ctx, byEnt.ID)
	if err != nil {
		t.Fatal(err)
	}
	if renamed.Name != "Renamed" || renamed.Age != nil {
		t.Fatalf("ent read back %+v", renamed)
	}

	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if n, err := repository.Count(ctx); err != nil || n != 2 {
		t.Fatalf("after commit count = %d err = %v", n, err)
	}
}

func TestEntRollback(t *testing.T) {
	ctx := context.Background()
	truncate(t, pgDB)
	client := entClient(pgDB, dialect.Postgres)
	source := crudsql.Postgres(pgDB)
	repository := Users.Bind(source)

	tx, err := client.Tx(ctx)
	if err != nil {
		t.Fatal(err)
	}
	u := User{TenantID: 1, Email: "gone@x.io", Name: "Gone"}
	if _, err := repository.Save(source.BindExecutor(ctx, tx), &u); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if n, err := repository.Count(ctx); err != nil || n != 0 {
		t.Fatalf("count = %d err = %v: the rollback missed vv's write", n, err)
	}
}

func TestAnEntTransactionJoinsButCannotOpenASavepoint(t *testing.T) {
	ctx := context.Background()
	truncate(t, pgDB)
	source := newEntSource(entClient(pgDB, dialect.Postgres), crud.Postgres{})
	repository := Users.Bind(source)

	err := crud.InTx(ctx, source, func(ctx context.Context) error {
		if _, err := repository.Save(ctx, &User{TenantID: 1, Email: "ent-outer@x.io", Name: "outer"}); err != nil {
			return err
		}
		ex, _ := crud.ExecutorFrom(ctx)
		if _, ok := ex.(crud.Beginner); ok {
			t.Error("an ent transaction now offers Begin; this test should be asserting savepoint semantics instead of their absence")
		}

		called := false
		err := crud.InTx(context.Background(), ex, func(context.Context) error {
			called = true
			return nil
		})
		var scoped *crud.ExecutorScopeError
		if !errors.As(err, &scoped) || scoped.Reason != crud.ExecutorScopeTransactionSource || called {
			t.Errorf("err = %v called=%v, want transaction_source before callback", err, called)
		}

		return repository.Tx(ctx, func(ctx context.Context) error {
			_, err := repository.Save(ctx, &User{TenantID: 1, Email: "ent-inner@x.io", Name: "inner"})

			return err
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	if n, err := repository.Count(ctx); err != nil || n != 2 {
		t.Fatalf("count = %d err = %v: both writes belong to the one transaction ent committed", n, err)
	}
}
