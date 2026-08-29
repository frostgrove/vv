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

// With the sql/execquery feature, *ent.Client and *ent.Tx both expose
// ExecContext/QueryContext — which is exactly crudsql.Queryer. So vv can
// run straight on ent's driver, transactions included.
var (
	_ crudsql.Queryer = (*ent.Client)(nil)
	_ crudsql.Queryer = (*ent.Tx)(nil)
)

// entSource is the whole ent adapter: wrap the client, name the dialect, and
// hand ent's own transaction back as a crud.Tx.
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

// entTx deliberately has no Begin: ent owns the transaction, and the *sql.Tx
// that a SAVEPOINT would have to be issued on is behind it. So an ent-backed
// source is not a crud.Beginner at the second level, a nested Begin answers
// crud.ErrNoTxSupport, and docs/ent.md §8 says so next to the twenty lines it
// tells a reader to write.
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

// The full conformance suite, executed entirely through ent's driver and ent's
// transactions.
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

// ent owns the transaction and the entity API; vv joins through the
// context. One physical transaction, both libraries writing into it.
func TestEntSharedTransaction(t *testing.T) {
	ctx := context.Background()
	truncate(t, pgDB)
	client := entClient(pgDB, dialect.Postgres)
	repository := Users.Bind(crudsql.Postgres(pgDB))

	tx, err := client.Tx(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()

	txCtx := crud.WithExecutor(ctx, crudsql.From(tx))

	// ent writes an entity.
	byEnt, err := tx.User.Create().
		SetTenantID(1).SetEmail("ent@x.io").SetName("ByEnt").SetAge(28).SetActive(true).
		Save(ctx)
	if err != nil {
		t.Fatal(err)
	}

	// vv sees it inside the transaction.
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

	// vv writes, ent reads.
	u := User{TenantID: 1, Email: "rx@x.io", Name: "ByVV", Active: true}
	if _, err := repository.Save(txCtx, &u); err != nil {
		t.Fatal(err)
	}
	back, err := tx.User.Query().Where(entuser.IDEQ(u.ID)).Only(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if back.Name != "ByVV" || back.Age != nil {
		t.Fatalf("ent read back %+v", back)
	}

	// vv's partial update is visible to ent.
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

// An ent rollback has to undo vv's writes as well.
func TestEntRollback(t *testing.T) {
	ctx := context.Background()
	truncate(t, pgDB)
	client := entClient(pgDB, dialect.Postgres)
	repository := Users.Bind(crudsql.Postgres(pgDB))

	tx, err := client.Tx(ctx)
	if err != nil {
		t.Fatal(err)
	}
	u := User{TenantID: 1, Email: "gone@x.io", Name: "Gone"}
	if _, err := repository.Save(crud.WithExecutor(ctx, crudsql.From(tx)), &u); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if n, err := repository.Count(ctx); err != nil || n != 0 {
		t.Fatalf("count = %d err = %v: the rollback missed vv's write", n, err)
	}
}

// The limitation, stated as a test rather than only as a sentence in the guide:
// inside an ent transaction there is no savepoint to open, because ent owns the
// *sql.Tx a SAVEPOINT would have to be issued on. What still works is the shape
// the library actually encourages — a nested Tx joins the one already running —
// so the cost is bounded and worth writing down next to the twenty lines.
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
		// Which is what crud.InTx reports if somebody asks for a transaction of
		// its own rather than joining.
		if err := crud.InTx(context.Background(), ex, func(context.Context) error { return nil }); !errors.Is(err, crud.ErrNoTxSupport) {
			t.Errorf("err = %v, want ErrNoTxSupport", err)
		}
		// Joining, on the other hand, works: this is the second write in the
		// same physical transaction.
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
