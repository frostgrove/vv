package crud_test

import (
	"context"
	"testing"

	"github.com/shardit-io/vv/crud"
)

// tracing is what an application writes to see the statements this library
// builds: it forwards Exec and Query, and says what it wraps.
//
// One method — UnwrapSource — is the whole obligation. Everything the wrapped
// source could do goes on working through it.
type tracing struct {
	inner crud.Source
	seen  *int
}

func (t tracing) Exec(ctx context.Context, q string, args ...any) (crud.Result, error) {
	*t.seen++
	return t.inner.Exec(ctx, q, args...)
}

func (t tracing) Query(ctx context.Context, q string, args ...any) (crud.Rows, error) {
	*t.seen++
	return t.inner.Query(ctx, q, args...)
}

func (t tracing) Dialect() crud.Dialect     { return t.inner.Dialect() }
func (t tracing) UnwrapSource() crud.Source { return t.inner }

// blind is the same wrapper with the one method left out — the shape an
// application writes by accident, and the reason the helpers exist.
type blind struct{ inner crud.Source }

func (b blind) Exec(ctx context.Context, q string, args ...any) (crud.Result, error) {
	return b.inner.Exec(ctx, q, args...)
}

func (b blind) Query(ctx context.Context, q string, args ...any) (crud.Rows, error) {
	return b.inner.Query(ctx, q, args...)
}

func (b blind) Dialect() crud.Dialect { return b.inner.Dialect() }

// A wrapper that says what it wraps keeps every behaviour the wrapped source
// had, and a wrapper that does not keeps none of them.
//
// The three losses are not equally visible and that is why this is one test.
// Losing Beginner turns every transaction into ErrNoTxSupport, which is loud.
// Losing Identified misses the catalog keyed on the handle, which refuses at
// start-up. Losing ReadSourcer sends every read to the primary and says nothing
// at all — the replica just sits idle, and nothing anywhere connects that to the
// day somebody added a metrics wrapper.
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

	// The control. Every assertion above would hold for helpers that reached
	// past any wrapper at all — including one that deliberately replaced the
	// datasource — so the wrapper that does not say what it wraps has to lose
	// all three. That loss is the bug this exists to make survivable, not a
	// behaviour to preserve.
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

	// And the wrapper really is in the path, or it would be proving nothing
	// about instrumentation.
	if _, err := wrapped.Exec(context.Background(), "SELECT 1"); err != nil {
		t.Fatalf("the wrapper did not forward Exec: %v", err)
	}
	if seen != 1 {
		t.Fatalf("the wrapper saw %d statements, want 1", seen)
	}
}

// A transaction opened on a wrapped source is opened on the source underneath,
// so InTx works through instrumentation.
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

	// The control: without the one method, the same call is refused.
	if err := crud.InTx(context.Background(), blind{inner: primary},
		func(ctx context.Context) error { return nil }); err == nil {
		t.Fatal("a wrapper that says nothing about what it wraps opened a transaction anyway")
	}
}

// A ReadWrite whose primary is wrapped still names the primary's database, and
// a transaction opened on a wrapped source is still scoped to it.
//
// These are the two places the walk was not applied when it was written, and
// they are the two where losing it is silent. `KeyOf` through a ReadWrite over an
// instrumented primary answered the pair itself, so a scoped `WithExecutorFor`
// stopped matching the repository it was meant for; and `InTx` resolved the
// Beginner through the walk while scoping the binding through an assertion, so
// the transaction opened and then bound *unscoped* — adoptable by every
// repository in the process, including ones bound to another database.
//
// The second is the worse one. Before the walk existed, the same wrapper was
// refused at InTx with ErrNoTxSupport: wrong, but loud. Half a walk turned a
// loud refusal into a silent cross-database write.
func TestAWrappedPrimaryIsStillTheDatabaseItNames(t *testing.T) {
	var seen int
	primary := beginnerSource{named: srcOn(dbA, "primary"), tx: &fakeTx{}}
	replica := srcOn(dbB, "replica")

	// The shape D-062 recommends: instrument the primary, then pair it.
	rw := crud.ReadWrite(tracing{inner: primary, seen: &seen}, replica)

	if key := crud.KeyOf(rw); key != dbA {
		t.Fatalf("a ReadWrite over an instrumented primary answers %v as its database, want the primary's handle", key)
	}

	// Directly, and not only through KeyOf. catalog keys on Identified itself
	// (crud/catalog/set.go), so a pair that answers nil here loses its catalog
	// even where KeyOf would have walked past it.
	id, ok := rw.(crud.Identified)
	if !ok {
		t.Fatal("a ReadWrite no longer says which database it is")
	}
	if got := id.DataSource(); got != dbA {
		t.Fatalf("ReadWrite.DataSource answers %v for an instrumented primary, want the primary's handle", got)
	}

	// And the scoped binding still finds it, which is what KeyOf is for.
	ctx := crud.WithExecutorFor(context.Background(), dbA, fakeExec{name: "foreign"})
	if _, ok := crud.ExecutorFor(ctx, rw); !ok {
		t.Fatal("a scoped executor no longer reaches a repository bound through an instrumented pair")
	}

	// The control: a binding scoped to the *other* database must still not match,
	// or the walk has become "yes to anything" and D-027's whole point is gone.
	other := crud.WithExecutorFor(context.Background(), dbB, fakeExec{name: "elsewhere"})
	if _, ok := crud.ExecutorFor(other, rw); ok {
		t.Fatal("a binding scoped to another database matched — the walk is answering for anything")
	}
}

// A transaction vv opens on a wrapped source is scoped to that source's
// database, not left unscoped for anyone to adopt.
func TestATransactionOnAWrappedSourceIsScopedToItsDatabase(t *testing.T) {
	var seen int
	primary := beginnerSource{named: srcOn(dbA, "primary"), tx: &fakeTx{}}
	elsewhere := srcOn(dbB, "elsewhere")

	var reached bool
	err := crud.InTx(context.Background(), tracing{inner: primary, seen: &seen},
		func(ctx context.Context) error {
			// The repository on the *other* database must not be handed this
			// transaction. Unscoped, it was — and the write reported success
			// from inside a transaction on a database it has nothing to do with.
			if _, ok := crud.ExecutorFor(ctx, elsewhere); ok {
				t.Error("a repository on another database adopted this transaction")
			}
			// And the one it really is for must be.
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
