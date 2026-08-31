package crud_test

import (
	"context"
	"errors"
	"testing"

	"github.com/frostgrove/vv/crud"
)

type tracing struct {
	inner crud.Source
	seen  *int
}

func (this tracing) Exec(ctx context.Context, q string, args ...any) (crud.Result, error) {
	*this.seen++
	return this.inner.Exec(ctx, q, args...)
}

func (this tracing) Query(ctx context.Context, q string, args ...any) (crud.Rows, error) {
	*this.seen++
	return this.inner.Query(ctx, q, args...)
}

func (this tracing) Dialect() crud.Dialect     { return this.inner.Dialect() }
func (this tracing) UnwrapSource() crud.Source { return this.inner }

type blind struct{ inner crud.Source }

func (this blind) Exec(ctx context.Context, q string, args ...any) (crud.Result, error) {
	return this.inner.Exec(ctx, q, args...)
}

func (this blind) Query(ctx context.Context, q string, args ...any) (crud.Rows, error) {
	return this.inner.Query(ctx, q, args...)
}

func (this blind) Dialect() crud.Dialect { return this.inner.Dialect() }

func TestAWrappedSourceKeepsWhatItWrapsWhenItSaysWhatItWraps(t *testing.T) {
	replica := srcOn(dbB, "replica")
	primary := beginnerSource{named: srcOn(dbA, "primary"), tx: &fakeTx{}}
	rw := crud.ReadWrite(primary, replica)

	var seen int
	wrapped := tracing{inner: rw, seen: &seen}

	if _, ok := crud.BeginnerOf(wrapped); !ok {
		t.Fatal("the wrapper lost Beginner: every transaction through it would be ErrNoTxSupport")
	}
	got, ok := crud.ReadSourceOf(wrapped)
	if !ok {
		t.Fatal("the wrapper lost ReadSourcer: every read would silently go to the primary")
	}
	if crud.KeyOf(got) != dbB {
		t.Fatalf("the read source is not the replica the ReadWrite was built with")
	}
	if key := crud.KeyOf(wrapped); key != dbA {
		t.Fatalf("the wrapper answers %v as its database, want the primary's handle", key)
	}
	if _, ok := crud.UnsafeBulkInserterOf(wrapped); ok {
		t.Fatal("UnwrapSource exposed a side-effect capability underneath tracing")
	}
	if _, ok := crud.UnsafeBulkInserterOf(rw); !ok {
		t.Fatal("ReadWrite did not explicitly publish its primary bulk route")
	}

	deaf := blind{inner: rw}
	if _, ok := crud.BeginnerOf(deaf); ok {
		t.Fatal("a wrapper that says nothing was still read through — the helpers are guessing")
	}
	if _, ok := crud.ReadSourceOf(deaf); ok {
		t.Fatal("a wrapper that says nothing was still read through — the helpers are guessing")
	}
	if crud.KeyOf(deaf) == dbA {
		t.Fatal("a wrapper that says nothing was still read through — the helpers are guessing")
	}

	if _, err := wrapped.Exec(context.Background(), "SELECT 1"); err != nil {
		t.Fatalf("the wrapper did not forward Exec: %v", err)
	}
	if seen != 1 {
		t.Fatalf("the wrapper saw %d statements, want 1", seen)
	}
}

func TestInTxReachesTheBeginnerThroughAWrapper(t *testing.T) {
	tx := &fakeTx{}
	primary := beginnerSource{named: srcOn(dbA, "primary"), tx: tx}

	var seen int
	err := crud.InTx(context.Background(), tracing{inner: primary, seen: &seen},
		func(ctx context.Context) error { return nil })
	if err != nil {
		t.Fatalf("InTx through a wrapper: %v", err)
	}
	if !tx.committed {
		t.Fatal("the transaction was never committed, so it was never really opened")
	}

	if err := crud.InTx(context.Background(), blind{inner: primary},
		func(ctx context.Context) error { return nil }); err == nil {
		t.Fatal("a wrapper that says nothing about what it wraps opened a transaction anyway")
	}
}

func TestAWrappedPrimaryIsStillTheDatabaseItNames(t *testing.T) {
	var seen int
	primary := beginnerSource{named: srcOn(dbA, "primary"), tx: &fakeTx{}}
	replica := srcOn(dbB, "replica")

	rw := crud.ReadWrite(tracing{inner: primary, seen: &seen}, replica)

	if key := crud.KeyOf(rw); key != dbA {
		t.Fatalf("a ReadWrite over an instrumented primary answers %v as its database, want the primary's handle", key)
	}

	id, ok := rw.(crud.Identified)
	if !ok {
		t.Fatal("a ReadWrite no longer says which database it is")
	}
	if got := id.DataSource(); got != dbA {
		t.Fatalf("ReadWrite.DataSource answers %v for an instrumented primary, want the primary's handle", got)
	}

	ctx := crud.WithExecutorFor(context.Background(), dbA, fakeExec{name: "foreign"})
	if _, ok := crud.ExecutorFor(ctx, rw); !ok {
		t.Fatal("a scoped executor no longer reaches a repository bound through an instrumented pair")
	}
	session := crud.BindExecutor(context.Background(), rw, fakeExec{name: "session"})
	if e, ok := crud.ExecutorFor(session, primary); !ok || e.(fakeExec).name != "session" {
		t.Fatalf("a session declared through the wrapped ReadWrite resolved to %v, found=%v", e, ok)
	}
	if _, ok := crud.ExecutorFor(session, replica); ok {
		t.Fatal("a session declared for the wrapped primary captured its replica")
	}

	other := crud.WithExecutorFor(context.Background(), dbB, fakeExec{name: "elsewhere"})
	if _, ok := crud.ExecutorFor(other, rw); ok {
		t.Fatal("a binding scoped to another database matched — the walk is answering for anything")
	}
}

func TestATransactionOnAWrappedSourceIsScopedToItsDatabase(t *testing.T) {
	var seen int
	primary := beginnerSource{named: srcOn(dbA, "primary"), tx: &fakeTx{}}
	elsewhere := srcOn(dbB, "elsewhere")

	var reached bool
	err := crud.InTx(context.Background(), tracing{inner: primary, seen: &seen},
		func(ctx context.Context) error {
			if _, ok := crud.ExecutorFor(ctx, elsewhere); ok {
				t.Error("a repository on another database adopted this transaction")
			}

			if _, ok := crud.ExecutorFor(ctx, primary); !ok {
				t.Error("the source the transaction was opened on did not get it")
			}
			reached = true
			return nil
		})
	if err != nil {
		t.Fatalf("InTx through a wrapper: %v", err)
	}
	if !reached {
		t.Fatal("the closure never ran")
	}
}

func TestASessionRecognisesATransactionHiddenBySourceWrappers(t *testing.T) {
	tx := &fakeTx{fakeExec: fakeExec{name: "transaction"}}
	txSource := transactionalSource{fakeTx: tx}
	var seen int
	wrapped := tracing{inner: txSource, seen: &seen}

	for name, source := range map[string]crud.Source{
		"instrumented": wrapped,
		"read-write":   crud.ReadWrite(wrapped, srcOn(dbB, "replica")),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := crud.NewSession(source, tx)
			var scoped *crud.ExecutorScopeError
			if !errors.As(err, &scoped) || scoped.Reason != crud.ExecutorScopeTransactionSource {
				t.Fatalf("NewSession returned %v, want transaction_source", err)
			}
		})
	}
}
