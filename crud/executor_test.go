package crud_test

import (
	"context"
	"errors"
	"testing"

	"github.com/shardit-io/qq/crud"
)

// fakeExec is an executor that only has to be distinguishable from another one.
type fakeExec struct {
	name string
	ds   any
}

func (fakeExec) Exec(context.Context, string, ...any) (crud.Result, error) {
	return crud.Result{}, nil
}
func (fakeExec) Query(context.Context, string, ...any) (crud.Rows, error) { return nil, nil }
func (f fakeExec) Dialect() crud.Dialect                                  { return crud.Postgres{} }

// named is a source that can say which database it speaks to.
type named struct {
	fakeExec
}

func (n named) DataSource() any { return n.ds }

// beginnerSource hands out a transaction so InTx has something to open.
type beginnerSource struct {
	named
	tx *fakeTx
}

func (b beginnerSource) Begin(context.Context) (crud.Tx, error) { return b.tx, nil }

type fakeTx struct {
	fakeExec
	committed  bool
	rolledBack bool
}

func (t *fakeTx) Commit(context.Context) error   { t.committed = true; return nil }
func (t *fakeTx) Rollback(context.Context) error { t.rolledBack = true; return nil }

var (
	dbA = new(int) // two handles that are only ever compared by identity
	dbB = new(int)
)

func srcOn(ds any, name string) named { return named{fakeExec{name: name, ds: ds}} }

// An unscoped executor is what an ent or gorm transaction pushes in, and it has
// to keep reaching every repository — that is the interop seam.
func TestAnUnscopedExecutorReachesEverySource(t *testing.T) {
	ctx := crud.WithExecutor(context.Background(), fakeExec{name: "foreign"})

	for _, src := range []any{srcOn(dbA, "a"), srcOn(dbB, "b"), fakeExec{name: "anonymous"}} {
		e, ok := crud.ExecutorFor(ctx, src)
		if !ok {
			t.Fatalf("%v found no executor", src)
		}
		if e.(fakeExec).name != "foreign" {
			t.Fatalf("%v ran on %q", src, e.(fakeExec).name)
		}
	}
}

// The whole point of the scoped form: a repository on another database must not
// be dragged into a transaction that has nothing to do with it.
func TestAScopedExecutorReachesOnlyItsOwnDatabase(t *testing.T) {
	ctx := crud.WithExecutorFor(context.Background(), dbA, fakeExec{name: "tx-of-a"})

	e, ok := crud.ExecutorFor(ctx, srcOn(dbA, "a"))
	if !ok || e.(fakeExec).name != "tx-of-a" {
		t.Fatalf("the owning source did not join its own transaction: %v %v", e, ok)
	}
	if _, ok := crud.ExecutorFor(ctx, srcOn(dbB, "b")); ok {
		t.Fatal("a source on another database joined the transaction — this is the bug")
	}
	if _, ok := crud.ExecutorFor(ctx, fakeExec{name: "anonymous"}); ok {
		t.Fatal("a source that cannot name its database matched a scoped binding")
	}
}

// Naming the raw handle and naming a source over it are the same statement.
func TestTheHandleAndASourceOverItNameTheSameDatabase(t *testing.T) {
	byHandle := crud.WithExecutorFor(context.Background(), dbA, fakeExec{name: "tx"})
	bySource := crud.WithExecutorFor(context.Background(), srcOn(dbA, "a"), fakeExec{name: "tx"})

	for _, ctx := range []context.Context{byHandle, bySource} {
		if _, ok := crud.ExecutorFor(ctx, srcOn(dbA, "a")); !ok {
			t.Fatal("the source did not recognise its own database")
		}
	}
}

// Bindings stack rather than replace, so a scoped one cannot hide an unscoped
// one from a repository it was never meant to affect.
func TestAScopedBindingDoesNotHideTheUnscopedOneUnderIt(t *testing.T) {
	ctx := crud.WithExecutor(context.Background(), fakeExec{name: "outer"})
	ctx = crud.WithExecutorFor(ctx, dbA, fakeExec{name: "inner-a"})

	if e, _ := crud.ExecutorFor(ctx, srcOn(dbA, "a")); e.(fakeExec).name != "inner-a" {
		t.Fatalf("the more specific binding lost: %q", e.(fakeExec).name)
	}
	if e, _ := crud.ExecutorFor(ctx, srcOn(dbB, "b")); e.(fakeExec).name != "outer" {
		t.Fatalf("b should have fallen back to the outer executor, got %q", e.(fakeExec).name)
	}
}

// ExecutorFrom answers a different question — "is there a transaction here at
// all" — and still sees a scoped binding.
func TestExecutorFromSeesAnyBinding(t *testing.T) {
	ctx := crud.WithExecutorFor(context.Background(), dbA, fakeExec{name: "tx"})
	if _, ok := crud.ExecutorFrom(ctx); !ok {
		t.Fatal("ExecutorFrom missed a scoped binding")
	}
	if _, ok := crud.ExecutorFrom(context.Background()); ok {
		t.Fatal("ExecutorFrom invented an executor")
	}
}

// A datasource handle is a pointer in practice, but the contract does not say
// so — an uncomparable one must not take the process down.
func TestAnUncomparableDataSourceDoesNotPanic(t *testing.T) {
	weird := []int{1, 2, 3} // slices panic on ==
	ctx := crud.WithExecutorFor(context.Background(), weird, fakeExec{name: "tx"})

	if _, ok := crud.ExecutorFor(ctx, srcOn(weird, "same-slice")); ok {
		t.Fatal("two uncomparable identities were treated as one database")
	}
	if _, ok := crud.ExecutorFor(ctx, srcOn(dbA, "a")); ok {
		t.Fatal("an uncomparable binding matched an unrelated source")
	}
}

// A transaction rx-crud opens itself is scoped to the source that opened it, so
// it reaches siblings on the same database and nothing else.
func TestInTxScopesTheTransactionItOpens(t *testing.T) {
	tx := &fakeTx{fakeExec: fakeExec{name: "tx-of-a"}}
	src := beginnerSource{named: srcOn(dbA, "a"), tx: tx}

	err := crud.InTx(context.Background(), src, func(ctx context.Context) error {
		if e, ok := crud.ExecutorFor(ctx, srcOn(dbA, "sibling")); !ok || e.(*fakeTx) != tx {
			t.Error("a sibling repository on the same database did not join")
		}
		if _, ok := crud.ExecutorFor(ctx, srcOn(dbB, "b")); ok {
			t.Error("a repository on another database joined")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !tx.committed {
		t.Fatal("the transaction was not committed")
	}
}

// A source that cannot name its database keeps the old behaviour exactly: the
// transaction it opens is unscoped and everyone joins it. Refusing to join is
// no safer than joining the wrong one — a write outside the transaction is just
// as silent, so third-party adapters are left as they were.
func TestInTxLeavesAnUnidentifiedSourceUnscoped(t *testing.T) {
	tx := &fakeTx{fakeExec: fakeExec{name: "anonymous-tx"}}
	src := struct {
		fakeExec
		beginner
	}{fakeExec{name: "anonymous"}, beginner{tx}}

	err := crud.InTx(context.Background(), src, func(ctx context.Context) error {
		if e, ok := crud.ExecutorFor(ctx, srcOn(dbB, "b")); !ok || e.(*fakeTx) != tx {
			t.Error("an unidentified source did not bind unscoped")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

type beginner struct{ tx crud.Tx }

func (b beginner) Begin(context.Context) (crud.Tx, error) { return b.tx, nil }

// Already inside a transaction of my own database means join it, not open a
// second one.
func TestInTxJoinsRatherThanNests(t *testing.T) {
	tx := &fakeTx{fakeExec: fakeExec{name: "outer"}}
	src := beginnerSource{named: srcOn(dbA, "a"), tx: &fakeTx{fakeExec: fakeExec{name: "should-not-open"}}}

	ctx := crud.WithExecutorFor(context.Background(), dbA, tx)
	if err := crud.InTx(ctx, src, func(context.Context) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if src.tx.committed || src.tx.rolledBack {
		t.Fatal("InTx opened a competing transaction instead of joining")
	}
}

// But a transaction on somebody else's database is not one to join.
func TestInTxDoesNotJoinAnotherDatabasesTransaction(t *testing.T) {
	own := &fakeTx{fakeExec: fakeExec{name: "own"}}
	src := beginnerSource{named: srcOn(dbA, "a"), tx: own}

	ctx := crud.WithExecutorFor(context.Background(), dbB, &fakeTx{fakeExec: fakeExec{name: "b"}})
	if err := crud.InTx(ctx, src, func(context.Context) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if !own.committed {
		t.Fatal("InTx joined another database's transaction instead of opening its own")
	}
}

// A source with no transactions at all still says so.
func TestInTxWithoutABeginnerIsRefused(t *testing.T) {
	err := crud.InTx(context.Background(), fakeExec{name: "no-tx"}, func(context.Context) error {
		t.Error("fn ran without a transaction")
		return nil
	})
	if !errors.Is(err, crud.ErrNoTxSupport) {
		t.Fatalf("err = %v, want ErrNoTxSupport", err)
	}
}
