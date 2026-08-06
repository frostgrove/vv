package crud_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"rx-crud/crud"
	"rx-crud/crud/crudtest"
	"rx-crud/repo/basic"
)

// crud.Base and crud.Decorate are what a third-party decorator is written
// against. If either stops working every user-written middleware stops working
// with it, and nothing inside the library would notice — so they are exercised
// here through the same seam a stranger would use.

// countingRepo embeds Base and overrides one method, which is exactly the shape
// the Base documentation promises.
type countingRepo struct {
	crud.Base[Article, int64]
	deletes *int
}

func (c countingRepo) Delete(ctx context.Context, ids ...int64) (int64, error) {
	*c.deletes++
	return c.Base.Delete(ctx, ids...)
}

func counting(deletes *int) crud.Middleware[Article, int64] {
	return func(next crud.Core[Article, int64]) crud.Core[Article, int64] {
		return countingRepo{Base: crud.Base[Article, int64]{Core: next}, deletes: deletes}
	}
}

// refusing denies everything, so which layer answered is visible in the result.
func refusing() crud.Middleware[Article, int64] {
	return func(next crud.Core[Article, int64]) crud.Core[Article, int64] {
		return refusingRepo{crud.Base[Article, int64]{Core: next}}
	}
}

type refusingRepo struct{ crud.Base[Article, int64] }

func (r refusingRepo) Delete(context.Context, ...int64) (int64, error) {
	return 0, crud.ErrForbidden
}

var decArticles = basic.Define[Article, int64, struct{}]("dec_articles")

// An overridden method runs instead of the wrapped one; every method that was
// not overridden still reaches the repository, which is the entire point of
// embedding Base rather than implementing eleven methods.
func TestBasePassesEverythingThroughAndLetsOneOverrideWin(t *testing.T) {
	ctx := context.Background()
	var deletes int
	rec := crudtest.Postgres().
		ExecResult(crud.Result{RowsAffected: 1}).
		Push(crudtest.Rows([]any{int64(1), int64(7), "t", 0}))

	repo := decArticles.Bind(rec, counting(&deletes))

	if _, err := repo.Delete(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if deletes != 1 {
		t.Fatalf("the override ran %d times, want once", deletes)
	}
	if got := rec.Last().SQL; !strings.HasPrefix(got, "DELETE") {
		t.Fatalf("the override swallowed the statement instead of forwarding it: %v", rec.SQL())
	}

	// A method the decorator says nothing about still reaches the repository.
	if _, err := repo.GetByID(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if got := rec.Last().SQL; !strings.HasPrefix(got, "SELECT") {
		t.Fatalf("a method the decorator does not override did not reach SQL: %v", rec.SQL())
	}
}

// Decorate adds a layer to an already-typed repository, and the first argument
// ends up outermost — so it is the one that gets to refuse.
func TestDecorateStacksWithTheFirstMiddlewareOutermost(t *testing.T) {
	ctx := context.Background()
	var deletes int
	rec := crudtest.Postgres().ExecResult(crud.Result{RowsAffected: 1})

	repo := crud.Decorate(decArticles.Bind(rec), refusing(), counting(&deletes))

	if _, err := repo.Delete(ctx, 1); !errors.Is(err, crud.ErrForbidden) {
		t.Fatalf("err = %v, want the outermost layer's refusal", err)
	}
	if deletes != 0 {
		t.Fatalf("the inner layer ran %d times; the outer one refused before it", deletes)
	}
	if n := len(rec.Statements()); n != 0 {
		t.Fatalf("a refused delete still reached the database: %v", rec.SQL())
	}

	// Reversed, the counter is outermost and the refusal comes from beneath it.
	rec = crudtest.Postgres().ExecResult(crud.Result{RowsAffected: 1})
	repo = crud.Decorate(decArticles.Bind(rec), counting(&deletes), refusing())
	if _, err := repo.Delete(ctx, 1); !errors.Is(err, crud.ErrForbidden) {
		t.Fatalf("err = %v, want ErrForbidden", err)
	}
	if deletes != 1 {
		t.Fatalf("the outermost layer ran %d times, want once", deletes)
	}
}

// Unwrap hands back the Core underneath, which is how a caller adds another
// layer to a repository somebody else built.
func TestUnwrapReturnsTheDecoratedCore(t *testing.T) {
	rec := crudtest.Postgres()
	repo := decArticles.Bind(rec, refusing())

	if _, ok := repo.Unwrap().(refusingRepo); !ok {
		t.Fatalf("Unwrap returned %T, want the outermost decorator", repo.Unwrap())
	}
}

// A relation resolves its target's table through the registry, so a model whose
// repository lives elsewhere — or is never declared at all — can still be
// reached by pointing the registry at the right table.
func TestRegisterTableTypeRedirectsARelationsTarget(t *testing.T) {
	type Ledger struct {
		ID   int64  `db:"id,pk,auto"`
		Name string `db:"name"`
	}
	type Entry struct {
		ID       int64   `db:"id,pk,auto"`
		LedgerID int64   `db:"ledger_id"`
		Ledger   *Ledger `rel:"belongs_to,fk=LedgerID"`
	}

	crud.RegisterTableType(reflect.TypeOf(Ledger{}), "accounting_ledgers")

	rec := crudtest.Postgres().Push(crudtest.Rows([]any{int64(9), "main"}))
	runPreloads(t, rec, metaOf[Entry](t, "entries"),
		[]Entry{{ID: 1, LedgerID: 9}}, specs("Ledger")...)

	if got := rec.Last().SQL; !strings.Contains(got, `FROM "accounting_ledgers"`) {
		t.Fatalf("the preload read %s, not the registered table", got)
	}
}
