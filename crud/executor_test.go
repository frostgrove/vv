package crud_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/frostgrove/vv/crud"
)

type fakeExec struct {
	name string
	ds   any
}

func (fakeExec) Exec(context.Context, string, ...any) (crud.Result, error) {
	return crud.Result{}, nil
}
func (fakeExec) Query(context.Context, string, ...any) (crud.Rows, error) { return nil, nil }
func (this fakeExec) Dialect() crud.Dialect                               { return crud.Postgres{} }

type named struct {
	fakeExec
}

func (this named) DataSource() any { return this.ds }

type beginnerSource struct {
	named
	tx *fakeTx
}

func (this beginnerSource) Begin(context.Context) (crud.Tx, error) { return this.tx, nil }

type fakeTx struct {
	fakeExec
	committed        bool
	rolledBack       bool
	rollbackErr      error
	rollbackCtxErr   error
	rollbackDeadline time.Time
	rollbackBounded  bool
	rollbackValueKey any
	rollbackValue    any
}

type countedExecutor struct{ calls int }

func (this *countedExecutor) Exec(context.Context, string, ...any) (crud.Result, error) {
	this.calls++
	return crud.Result{}, nil
}

func (this *countedExecutor) Query(context.Context, string, ...any) (crud.Rows, error) {
	this.calls++
	return nil, nil
}

type countedBeginnerSource struct {
	named
	begins int
	calls  int
	tx     *fakeTx
}

func (this *countedBeginnerSource) Exec(context.Context, string, ...any) (crud.Result, error) {
	this.calls++
	return crud.Result{}, nil
}

func (this *countedBeginnerSource) Query(context.Context, string, ...any) (crud.Rows, error) {
	this.calls++
	return nil, nil
}

func (this *countedBeginnerSource) Begin(context.Context) (crud.Tx, error) {
	this.begins++
	return this.tx, nil
}

type transactionalSource struct{ *fakeTx }

func (this transactionalSource) DataSource() any { return this.fakeTx }

func (this *fakeTx) Commit(context.Context) error { this.committed = true; return nil }
func (this *fakeTx) Rollback(ctx context.Context) error {
	this.rolledBack = true
	this.rollbackCtxErr = ctx.Err()
	this.rollbackDeadline, this.rollbackBounded = ctx.Deadline()
	if this.rollbackValueKey != nil {
		this.rollbackValue = ctx.Value(this.rollbackValueKey)
	}
	return this.rollbackErr
}

var (
	dbA = new(int)
	dbB = new(int)
)

func srcOn(ds any, name string) named { return named{fakeExec{name: name, ds: ds}} }

func assertScopeFailure(t *testing.T, ctx context.Context, source any, reason crud.ExecutorScopeReason) {
	t.Helper()
	e, ok := crud.ExecutorFor(ctx, source)
	if !ok {
		t.Fatal("the invalid binding was silently ignored")
	}
	_, err := e.Exec(ctx, "must not reach a datasource")
	if !errors.Is(err, crud.ErrExecutorScope) {
		t.Fatalf("executor returned %v, want ErrExecutorScope", err)
	}
	var scoped *crud.ExecutorScopeError
	if !errors.As(err, &scoped) || scoped.Reason != reason {
		t.Fatalf("scope error = %#v, want reason %q", scoped, reason)
	}
}

func TestWithExecutorRefusesAnInferredScopeMismatch(t *testing.T) {
	ctx := crud.WithExecutor(context.Background(), fakeExec{name: "foreign"})
	assertScopeFailure(t, ctx, srcOn(dbA, "a"), crud.ExecutorScopeMismatch)
}

func TestWithUnsafeExecutorReachesEverySource(t *testing.T) {
	ctx := crud.WithUnsafeExecutor(context.Background(), fakeExec{name: "foreign"})

	for _, source := range []any{srcOn(dbA, "a"), srcOn(dbB, "b"), fakeExec{name: "anonymous"}} {
		e, ok := crud.ExecutorFor(ctx, source)
		if !ok || e.(fakeExec).name != "foreign" {
			t.Fatalf("%v did not adopt the explicitly unsafe executor: %v %v", source, e, ok)
		}
	}
}

func TestASessionReachesOnlyItsOwnDatabase(t *testing.T) {
	source := srcOn(dbA, "declaration")
	ctx := crud.BindExecutor(context.Background(), source, fakeExec{name: "tx-of-a"})

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

func TestASessionCanBeCheckedOnceAndReused(t *testing.T) {
	source := srcOn(dbA, "declaration")
	session, err := crud.NewSession(source, fakeExec{name: "tx"})
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		ctx := session.Bind(context.Background())
		if e, ok := crud.ExecutorFor(ctx, srcOn(dbA, "repository")); !ok || e.(fakeExec).name != "tx" {
			t.Fatalf("reused session resolved to %v, found=%v", e, ok)
		}
	}
}

func TestANaturalTransactionIdentityIsRefusedInsteadOfMissingSilently(t *testing.T) {
	tx := &fakeTx{fakeExec: fakeExec{name: "foreign"}}
	ctx := crud.WithExecutorFor(context.Background(), tx, tx)

	assertScopeFailure(t, ctx, srcOn(dbA, "repository"), crud.ExecutorScopeMismatch)
	called := false
	err := crud.InTx(ctx, beginnerSource{named: srcOn(dbA, "repository"), tx: &fakeTx{}}, func(context.Context) error {
		called = true
		return nil
	})
	if !errors.Is(err, crud.ErrExecutorScope) {
		t.Fatalf("InTx returned %v, want ErrExecutorScope", err)
	}
	if called {
		t.Fatal("InTx ran the callback after the transaction binding mismatched")
	}
}

func TestAStrictInnerMismatchCannotFallThroughToAnOuterSession(t *testing.T) {
	source := srcOn(dbA, "repository")
	ctx := crud.BindExecutor(context.Background(), source, fakeExec{name: "outer"})
	ctx = crud.WithExecutor(ctx, &fakeTx{fakeExec: fakeExec{name: "unscoped-inner"}})

	assertScopeFailure(t, ctx, source, crud.ExecutorScopeMismatch)
}

func TestAnOlderFailureCannotBeHiddenByANewerMatchingSession(t *testing.T) {
	source := srcOn(dbA, "repository")
	bound := &countedExecutor{}
	ctx := (crud.Session{}).Bind(context.Background())
	ctx = crud.BindExecutor(ctx, source, bound)

	assertScopeFailure(t, ctx, source, crud.ExecutorScopeInvalidSession)
	if bound.calls != 0 {
		t.Fatalf("the newer executor was called %d times through a poisoned context", bound.calls)
	}
	if executor, ok := crud.ExecutorFrom(ctx); !ok {
		t.Fatal("ExecutorFrom lost the declaration failure")
	} else if _, err := executor.Exec(ctx, "must remain poisoned"); !errors.Is(err, crud.ErrExecutorScope) {
		t.Fatalf("ExecutorFrom returned a healthy executor over an older failure: %v", err)
	}
}

func TestAnOlderStrictMismatchCannotBeHiddenByANewerMatchingSession(t *testing.T) {
	source := srcOn(dbA, "repository")
	bound := &countedExecutor{}
	ctx := crud.WithExecutor(context.Background(), fakeExec{name: "ambiguous outer"})
	ctx = crud.BindExecutor(ctx, source, bound)

	assertScopeFailure(t, ctx, source, crud.ExecutorScopeMismatch)
	if bound.calls != 0 {
		t.Fatalf("the newer executor was called %d times over an older strict mismatch", bound.calls)
	}
}

func TestTransactionHelpersCannotHideAnOlderBindingFailure(t *testing.T) {
	for _, tc := range []struct {
		name string
		run  func(context.Context, crud.Executor, func(context.Context) error) error
	}{
		{"InTx", crud.InTx},
		{"InAtomic", crud.InAtomic},
		{"InNewTx", crud.InNewTx},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, outer := range []struct {
				name string
				ctx  func() context.Context
			}{
				{"failed declaration", func() context.Context {
					return (crud.Session{}).Bind(context.Background())
				}},
				{"strict mismatch", func() context.Context {
					return crud.WithExecutor(context.Background(), fakeExec{name: "ambiguous outer"})
				}},
			} {
				t.Run(outer.name, func(t *testing.T) {
					source := &countedBeginnerSource{
						named: srcOn(dbA, "repository"),
						tx:    &fakeTx{fakeExec: fakeExec{name: "must-not-open"}},
					}
					bound := &countedExecutor{}
					ctx := crud.BindExecutor(outer.ctx(), source, bound)
					called := false

					err := tc.run(ctx, source, func(context.Context) error {
						called = true
						return nil
					})
					if !errors.Is(err, crud.ErrExecutorScope) {
						t.Fatalf("%s returned %v, want ErrExecutorScope", tc.name, err)
					}
					if called || source.begins != 0 || source.calls != 0 || bound.calls != 0 {
						t.Fatalf("%s hid the failure: callback=%v begins=%d source calls=%d bound calls=%d",
							tc.name, called, source.begins, source.calls, bound.calls)
					}
				})
			}
		})
	}
}

func TestTheZeroSessionFailsClosed(t *testing.T) {
	ctx := (crud.Session{}).Bind(context.Background())
	assertScopeFailure(t, ctx, srcOn(dbA, "repository"), crud.ExecutorScopeInvalidSession)
}

func TestASessionRejectsTypedNilDeclarations(t *testing.T) {
	var source *named
	var executor *fakeTx
	missingHandle := named{fakeExec: fakeExec{name: "wrapped-nil", ds: (*int)(nil)}}

	for name, err := range map[string]error{
		"source": func() error {
			_, err := crud.NewSession(source, fakeExec{name: "executor"})
			return err
		}(),
		"executor": func() error {
			_, err := crud.NewSession(srcOn(dbA, "source"), executor)
			return err
		}(),
		"executor handle": func() error {
			_, err := crud.NewSession(srcOn(dbA, "source"), missingHandle)
			return err
		}(),
	} {
		t.Run(name, func(t *testing.T) {
			if !errors.Is(err, crud.ErrExecutorScope) {
				t.Fatalf("NewSession returned %v, want ErrExecutorScope", err)
			}
		})
	}
}

func TestASessionRefusesATransactionUsedAsTheCanonicalSource(t *testing.T) {
	tx := &fakeTx{fakeExec: fakeExec{name: "foreign"}}
	source := transactionalSource{fakeTx: tx}
	if _, err := crud.NewSession(source, tx); err == nil {
		t.Fatal("NewSession accepted a transaction as the canonical source")
	} else {
		var scoped *crud.ExecutorScopeError
		if !errors.As(err, &scoped) || scoped.Reason != crud.ExecutorScopeTransactionSource {
			t.Fatalf("NewSession returned %v, want transaction_source", err)
		}
	}

	ctx := crud.BindExecutor(context.Background(), source, tx)
	assertScopeFailure(t, ctx, srcOn(dbA, "pool-repository"), crud.ExecutorScopeTransactionSource)
	called := false
	err := crud.InTx(context.Background(), source, func(context.Context) error {
		called = true
		return nil
	})
	if !errors.Is(err, crud.ErrExecutorScope) || called {
		t.Fatalf("InTx returned %v and called=%v, want a refusal before the callback", err, called)
	}
}

func TestTheHandleAndASourceOverItNameTheSameDatabase(t *testing.T) {
	byHandle := crud.WithExecutorFor(context.Background(), dbA, fakeExec{name: "tx"})
	bySource := crud.WithExecutorFor(context.Background(), srcOn(dbA, "a"), fakeExec{name: "tx"})

	for _, ctx := range []context.Context{byHandle, bySource} {
		if _, ok := crud.ExecutorFor(ctx, srcOn(dbA, "a")); !ok {
			t.Fatal("the source did not recognise its own database")
		}
	}
}

func TestASessionDoesNotHideTheUnsafeExecutorUnderIt(t *testing.T) {
	ctx := crud.WithUnsafeExecutor(context.Background(), fakeExec{name: "outer"})
	ctx = crud.BindExecutor(ctx, srcOn(dbA, "declaration"), fakeExec{name: "inner-a"})

	if e, _ := crud.ExecutorFor(ctx, srcOn(dbA, "a")); e.(fakeExec).name != "inner-a" {
		t.Fatalf("the more specific binding lost: %q", e.(fakeExec).name)
	}
	if e, _ := crud.ExecutorFor(ctx, srcOn(dbB, "b")); e.(fakeExec).name != "outer" {
		t.Fatalf("b should have fallen back to the outer executor, got %q", e.(fakeExec).name)
	}
}

func TestExecutorFromSeesAnyBinding(t *testing.T) {
	ctx := crud.WithExecutorFor(context.Background(), dbA, fakeExec{name: "tx"})
	if _, ok := crud.ExecutorFrom(ctx); !ok {
		t.Fatal("ExecutorFrom missed a scoped binding")
	}
	if _, ok := crud.ExecutorFrom(context.Background()); ok {
		t.Fatal("ExecutorFrom invented an executor")
	}
}

func TestAnUncomparableDataSourceDoesNotPanic(t *testing.T) {
	weird := []int{1, 2, 3}
	ctx := crud.WithExecutorFor(context.Background(), weird, fakeExec{name: "tx"})

	assertScopeFailure(t, ctx, srcOn(weird, "same-slice"), crud.ExecutorScopeInvalidSource)
	assertScopeFailure(t, ctx, srcOn(dbA, "a"), crud.ExecutorScopeInvalidSource)
}

func TestADataSourceWithAnUncomparableInterfaceValueDoesNotPanic(t *testing.T) {
	type identity struct{ Value any }
	left := identity{Value: []int{1}}
	right := identity{Value: []int{1}}

	if crud.SameDataSource(left, right) {
		t.Fatal("two identities containing slices were treated as the same database")
	}

	ctx := crud.WithExecutorFor(context.Background(), left, fakeExec{name: "tx"})
	assertScopeFailure(t, ctx, srcOn(right, "right"), crud.ExecutorScopeInvalidSource)
}

func TestInTxScopesTheTransactionItOpens(t *testing.T) {
	tx := &fakeTx{fakeExec: fakeExec{name: "tx-of-a"}}
	source := beginnerSource{named: srcOn(dbA, "a"), tx: tx}

	err := crud.InTx(context.Background(), source, func(ctx context.Context) error {
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

func TestInTxRefusesAnUnidentifiedSourceBeforeBegin(t *testing.T) {
	tx := &fakeTx{fakeExec: fakeExec{name: "anonymous-tx"}}
	source := struct {
		fakeExec
		beginner
	}{fakeExec{name: "anonymous"}, beginner{tx}}

	called := false
	err := crud.InTx(context.Background(), source, func(context.Context) error {
		called = true
		return nil
	})
	if !errors.Is(err, crud.ErrExecutorScope) {
		t.Fatalf("InTx returned %v, want ErrExecutorScope", err)
	}
	if called {
		t.Fatal("InTx ran fn for an unidentified source")
	}
	if tx.committed || tx.rolledBack {
		t.Fatal("InTx began a transaction before validating source identity")
	}
}

func TestInNewTxDoesNotHideAnInvalidAmbientBinding(t *testing.T) {
	tx := &fakeTx{fakeExec: fakeExec{name: "must-not-open"}}
	source := beginnerSource{named: srcOn(dbA, "a"), tx: tx}
	ctx := crud.WithExecutor(context.Background(), &fakeTx{fakeExec: fakeExec{name: "ambiguous"}})

	called := false
	err := crud.InNewTx(ctx, source, func(context.Context) error {
		called = true
		return nil
	})
	if !errors.Is(err, crud.ErrExecutorScope) {
		t.Fatalf("InNewTx returned %v, want ErrExecutorScope", err)
	}
	if called || tx.committed || tx.rolledBack {
		t.Fatalf("InNewTx hid the invalid binding: called=%v committed=%v rolledBack=%v", called, tx.committed, tx.rolledBack)
	}
}

type beginner struct{ tx crud.Tx }

func (this beginner) Begin(context.Context) (crud.Tx, error) { return this.tx, nil }

func TestInTxJoinsRatherThanNests(t *testing.T) {
	tx := &fakeTx{fakeExec: fakeExec{name: "outer"}}
	source := beginnerSource{named: srcOn(dbA, "a"), tx: &fakeTx{fakeExec: fakeExec{name: "should-not-open"}}}

	ctx := crud.WithExecutorFor(context.Background(), dbA, tx)
	if err := crud.InTx(ctx, source, func(context.Context) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if source.tx.committed || source.tx.rolledBack {
		t.Fatal("InTx opened a competing transaction instead of joining")
	}
}

func TestInTxDoesNotJoinAnotherDatabasesTransaction(t *testing.T) {
	own := &fakeTx{fakeExec: fakeExec{name: "own"}}
	source := beginnerSource{named: srcOn(dbA, "a"), tx: own}

	ctx := crud.WithExecutorFor(context.Background(), dbB, &fakeTx{fakeExec: fakeExec{name: "b"}})
	if err := crud.InTx(ctx, source, func(context.Context) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if !own.committed {
		t.Fatal("InTx joined another database's transaction instead of opening its own")
	}
}

func TestInTxWithoutABeginnerIsRefused(t *testing.T) {
	err := crud.InTx(context.Background(), fakeExec{name: "no-tx"}, func(context.Context) error {
		t.Error("fn ran without a transaction")
		return nil
	})
	if !errors.Is(err, crud.ErrNoTxSupport) {
		t.Fatalf("err = %v, want ErrNoTxSupport", err)
	}
}

func TestRollbackOutlivesTheCanceledRequestButRemainsBounded(t *testing.T) {
	tx := &fakeTx{fakeExec: fakeExec{name: "tx"}}
	source := beginnerSource{named: srcOn(dbA, "a"), tx: tx}
	request, cancel := context.WithCancel(context.Background())
	want := errors.New("operation failed")

	err := crud.InTx(request, source, func(context.Context) error {
		cancel()
		return want
	})
	if !errors.Is(err, want) {
		t.Fatalf("InTx returned %v, want the operation failure", err)
	}
	if !tx.rolledBack {
		t.Fatal("the failed transaction was not rolled back")
	}
	if err := tx.rollbackCtxErr; err != nil {
		t.Fatalf("rollback inherited the canceled request: %v", err)
	}
	remaining := time.Until(tx.rollbackDeadline)
	if !tx.rollbackBounded || remaining <= 0 || remaining > 5*time.Second {
		t.Fatalf("rollback context has deadline %v, bounded=%v; want a live deadline within five seconds", tx.rollbackDeadline, tx.rollbackBounded)
	}
}

func TestRollbackKeepsTheOperationAndCleanupErrorsInspectable(t *testing.T) {
	operationErr := errors.New("operation failed")
	rollbackErr := errors.New("rollback failed")
	tx := &fakeTx{fakeExec: fakeExec{name: "tx"}, rollbackErr: rollbackErr}
	source := beginnerSource{named: srcOn(dbA, "a"), tx: tx}

	err := crud.InTx(context.Background(), source, func(context.Context) error {
		return operationErr
	})
	if !errors.Is(err, operationErr) {
		t.Fatalf("InTx returned %v; errors.Is(operationErr) = false", err)
	}
	if !errors.Is(err, rollbackErr) {
		t.Fatalf("InTx returned %v; errors.Is(rollbackErr) = false", err)
	}
}

func TestPanicRollsBackWithADetachedBoundedContextAndRepanics(t *testing.T) {
	want := errors.New("panic from transaction")
	tx := &fakeTx{fakeExec: fakeExec{name: "tx"}}
	source := beginnerSource{named: srcOn(dbA, "a"), tx: tx}
	request, cancel := context.WithCancel(context.Background())

	defer func() {
		if got := recover(); got != want {
			t.Fatalf("recovered %v, want the original panic %v", got, want)
		}
		if !tx.rolledBack {
			t.Fatal("the panicking transaction was not rolled back")
		}
		if err := tx.rollbackCtxErr; err != nil {
			t.Fatalf("panic rollback inherited the canceled request: %v", err)
		}
		remaining := time.Until(tx.rollbackDeadline)
		if !tx.rollbackBounded || remaining <= 0 || remaining > 5*time.Second {
			t.Fatalf("panic rollback context has deadline %v, bounded=%v; want a live deadline within five seconds", tx.rollbackDeadline, tx.rollbackBounded)
		}
	}()

	_ = crud.InTx(request, source, func(context.Context) error {
		cancel()
		panic(want)
	})
	t.Fatal("InTx swallowed the callback panic")
}

func TestRollbackPreservesRequestContextValues(t *testing.T) {
	type contextKey struct{}
	key := contextKey{}
	want := new(int)
	tx := &fakeTx{fakeExec: fakeExec{name: "tx"}, rollbackValueKey: key}
	source := beginnerSource{named: srcOn(dbA, "a"), tx: tx}
	request, cancel := context.WithCancel(context.WithValue(context.Background(), key, want))

	err := crud.InTx(request, source, func(context.Context) error {
		cancel()
		return errors.New("operation failed")
	})
	if err == nil {
		t.Fatal("InTx discarded the operation failure")
	}
	if tx.rollbackValue != want {
		t.Fatalf("rollback context value = %v, want %v", tx.rollbackValue, want)
	}
}

func TestATransactionVVOpenedIsMarkedOwnedAndAForeignOneIsNot(t *testing.T) {
	tx := &fakeTx{fakeExec: fakeExec{name: "tx-of-a"}}
	source := beginnerSource{named: srcOn(dbA, "a"), tx: tx}

	err := crud.InTx(context.Background(), source, func(ctx context.Context) error {
		if _, found, owned := crud.OwnedExecutorFor(ctx, source); !found || !owned {
			t.Errorf("a transaction vv opened reports found=%v owned=%v", found, owned)
		}

		return crud.InTx(ctx, source, func(inner context.Context) error {
			if _, _, owned := crud.OwnedExecutorFor(inner, source); !owned {
				t.Error("joining our own transaction gave up ownership of it")
			}
			return nil
		})
	})
	if err != nil {
		t.Fatal(err)
	}

	foreign := crud.BindExecutor(context.Background(), source, tx)
	if _, found, owned := crud.OwnedExecutorFor(foreign, source); !found || owned {
		t.Fatalf("a foreign transaction reports found=%v owned=%v", found, owned)
	}
	scoped := crud.WithExecutorFor(context.Background(), dbA, tx)
	if _, found, owned := crud.OwnedExecutorFor(scoped, source); !found || owned {
		t.Fatalf("a foreign transaction named by handle reports found=%v owned=%v", found, owned)
	}

	elsewhere := crud.WithExecutorFor(context.Background(), dbB, tx)
	if _, found, _ := crud.OwnedExecutorFor(elsewhere, source); found {
		t.Fatal("a transaction on another database was reported as this repository's")
	}
	if _, ok := crud.ExecutorFrom(elsewhere); !ok {
		t.Fatal("ExecutorFrom stopped seeing the binding, so the trap above is not the one described")
	}
}

func TestASavepointClaimCountsPerTransactionAndNotPerRepository(t *testing.T) {
	one := beginnerSource{named: srcOn(dbA, "one"), tx: &fakeTx{fakeExec: fakeExec{name: "tx"}}}
	two := srcOn(dbA, "two")

	err := crud.InTx(context.Background(), one, func(ctx context.Context) error {
		if n, ok := crud.ClaimSavepoint(ctx, one); !ok || n != 1 {
			t.Fatalf("the first claim reported %d, ok=%v", n, ok)
		}
		if n, ok := crud.ClaimSavepoint(ctx, two); !ok || n != 2 {
			t.Fatalf("a second repository over the same transaction reported %d, ok=%v: "+
				"the budget is per transaction, not per repository", n, ok)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	other := beginnerSource{named: srcOn(dbA, "one"), tx: &fakeTx{fakeExec: fakeExec{name: "tx2"}}}
	err = crud.InTx(context.Background(), other, func(ctx context.Context) error {
		if n, ok := crud.ClaimSavepoint(ctx, other); !ok || n != 1 {
			t.Fatalf("a fresh transaction started its budget at %d", n)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestNoSavepointIsClaimedInAForeignTransactionOrOutsideOne(t *testing.T) {
	source := srcOn(dbA, "a")
	if _, ok := crud.ClaimSavepoint(context.Background(), source); ok {
		t.Error("a savepoint was claimed with no transaction in sight")
	}
	foreign := crud.BindExecutor(context.Background(), source, fakeExec{name: "ent"})
	if _, ok := crud.ClaimSavepoint(foreign, source); ok {
		t.Error("a savepoint was claimed inside a transaction vv does not own")
	}

	own := beginnerSource{named: source, tx: &fakeTx{fakeExec: fakeExec{name: "tx"}}}
	if err := crud.InTx(context.Background(), own, func(ctx context.Context) error {
		if _, ok := crud.ClaimSavepoint(ctx, own); !ok {
			t.Error("our own transaction refused a savepoint, so the refusals above say nothing")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestARejectedScopeCannotBeConsumedAsASavepointMiss(t *testing.T) {
	source := srcOn(dbA, "a")
	ctx := crud.WithExecutor(context.Background(), &fakeTx{fakeExec: fakeExec{name: "ambiguous"}})

	if _, ok := crud.ClaimSavepoint(ctx, source); ok {
		t.Fatal("ClaimSavepoint accepted an executor whose scope was rejected")
	}

	assertScopeFailure(t, ctx, source, crud.ExecutorScopeMismatch)
}
