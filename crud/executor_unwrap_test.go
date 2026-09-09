package crud_test

import (
	"context"
	"errors"
	"testing"

	"github.com/frostgrove/vv/crud"
)

type executorMarker interface {
	ExecutorMarker() string
}

type markedExecutor struct{}

func (markedExecutor) Exec(context.Context, string, ...any) (crud.Result, error) {
	return crud.Result{}, nil
}

func (markedExecutor) Query(context.Context, string, ...any) (crud.Rows, error) {
	return nil, nil
}

func (markedExecutor) ExecutorMarker() string { return "target" }

type executorWrapper struct{ inner crud.Executor }

func (w executorWrapper) Exec(ctx context.Context, query string, args ...any) (crud.Result, error) {
	return w.inner.Exec(ctx, query, args...)
}

func (w executorWrapper) Query(ctx context.Context, query string, args ...any) (crud.Rows, error) {
	return w.inner.Query(ctx, query, args...)
}

func (w executorWrapper) UnwrapExecutor() crud.Executor { return w.inner }

type sourceOnlyWrapper struct{ inner crud.Source }

func (w sourceOnlyWrapper) Exec(ctx context.Context, query string, args ...any) (crud.Result, error) {
	return w.inner.Exec(ctx, query, args...)
}

func (w sourceOnlyWrapper) Query(ctx context.Context, query string, args ...any) (crud.Rows, error) {
	return w.inner.Query(ctx, query, args...)
}

func (w sourceOnlyWrapper) Dialect() crud.Dialect { return w.inner.Dialect() }

func (w sourceOnlyWrapper) UnwrapSource() crud.Source { return w.inner }

type admissionSource struct {
	ds any
}

func (admissionSource) Exec(context.Context, string, ...any) (crud.Result, error) {
	return crud.Result{}, nil
}

func (admissionSource) Query(context.Context, string, ...any) (crud.Rows, error) {
	return nil, nil
}

func (admissionSource) Dialect() crud.Dialect { return crud.Postgres{} }

func (this admissionSource) DataSource() any { return this.ds }

type admissionSourceLink struct {
	admissionSource
	inner crud.Source
}

func (this *admissionSourceLink) UnwrapSource() crud.Source { return this.inner }

type admissionExecutorLink struct {
	admissionSource
	inner crud.Executor
}

func (this *admissionExecutorLink) UnwrapExecutor() crud.Executor { return this.inner }

type admissionDualLink struct {
	admissionSource
	source   crud.Source
	executor crud.Executor
}

func (this *admissionDualLink) UnwrapSource() crud.Source     { return this.source }
func (this *admissionDualLink) UnwrapExecutor() crud.Executor { return this.executor }

type admissionInterfacePayloadSource struct {
	admissionSource
	payload any
}

type markedTx struct{ markedExecutor }

func (markedTx) Commit(context.Context) error   { return nil }
func (markedTx) Rollback(context.Context) error { return nil }

func TestExecutorDiscoveryUsesOnlyTheExecutorNavigationSeam(t *testing.T) {
	target := markedExecutor{}
	wrapper := executorWrapper{inner: target}

	found, ok := crud.ExecutorAs[executorMarker](wrapper)
	if !ok || found.ExecutorMarker() != "target" {
		t.Fatalf("ExecutorAs = %#v, %v", found, ok)
	}

	source := srcOn(dbA, "source")
	if _, ok := crud.ExecutorAs[executorMarker](sourceOnlyWrapper{inner: source}); ok {
		t.Fatal("executor discovery crossed SourceUnwrapper")
	}
}

func TestTransactionDiscoveryCrossesOnlyExecutorWrappers(t *testing.T) {
	tx := markedTx{}
	if !crud.IsTransaction(executorWrapper{inner: tx}) {
		t.Fatal("an executor wrapper hid its transaction")
	}

	sourceTx := transactionalSource{fakeTx: &fakeTx{}}
	if crud.IsTransaction(sourceOnlyWrapper{inner: sourceTx}) {
		t.Fatal("transaction discovery crossed SourceUnwrapper")
	}
}

func TestSessionAdmissionStillFindsATransactionThroughSourceNavigation(t *testing.T) {
	sourceTx := transactionalSource{fakeTx: &fakeTx{}}
	_, err := crud.NewSession(sourceOnlyWrapper{inner: sourceTx}, markedExecutor{})
	var scoped *crud.ExecutorScopeError
	if !errors.As(err, &scoped) || scoped.Reason != crud.ExecutorScopeTransactionSource {
		t.Fatalf("NewSession returned %v, want transaction_source", err)
	}
}

func TestSessionAdmissionAlternatesBothNavigationSeams(t *testing.T) {
	txSource := transactionalSource{fakeTx: &fakeTx{}}

	cases := map[string]crud.Source{
		"source then executor": &admissionSourceLink{
			admissionSource: admissionSource{ds: dbA},
			inner: &admissionExecutorLink{
				admissionSource: admissionSource{ds: dbA},
				inner:           txSource,
			},
		},
		"executor then source": &admissionExecutorLink{
			admissionSource: admissionSource{ds: dbA},
			inner: &admissionSourceLink{
				admissionSource: admissionSource{ds: dbA},
				inner:           txSource,
			},
		},
	}
	for name, source := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := crud.NewSession(source, markedExecutor{})
			var scoped *crud.ExecutorScopeError
			if !errors.As(err, &scoped) || scoped.Reason != crud.ExecutorScopeTransactionSource {
				t.Fatalf("NewSession returned %v, want transaction_source", err)
			}
			called := false
			err = crud.InNewTx(context.Background(), source, func(context.Context) error {
				called = true
				return nil
			})
			if !errors.As(err, &scoped) || scoped.Reason != crud.ExecutorScopeTransactionSource {
				t.Fatalf("InNewTx returned %v, want transaction_source", err)
			}
			if called {
				t.Fatal("InNewTx invoked the callback through a mixed transaction source")
			}
			if crud.IsTransaction(source) {
				t.Fatal("ordinary transaction discovery crossed a mixed source path")
			}
		})
	}
}

func TestSessionAdmissionFailsClosedOnIncompleteNavigation(t *testing.T) {
	cycleSource := &admissionSourceLink{admissionSource: admissionSource{ds: dbA}}
	cycleExecutor := &admissionExecutorLink{admissionSource: admissionSource{ds: dbA}}
	cycleSource.inner = cycleExecutor
	cycleExecutor.inner = cycleSource

	var nilSource *admissionSourceLink
	var nilExecutor *admissionExecutorLink
	nonComparable := admissionInterfacePayloadSource{
		admissionSource: admissionSource{ds: dbA},
		payload:         []int{1},
	}
	cases := map[string]crud.Source{
		"mixed cycle": cycleSource,
		"divergent navigation": &admissionDualLink{
			admissionSource: admissionSource{ds: dbA},
			source:          admissionSource{ds: dbA},
			executor:        admissionSource{ds: dbB},
		},
		"typed nil source target": &admissionSourceLink{
			admissionSource: admissionSource{ds: dbA},
			inner:           nilSource,
		},
		"typed nil executor target": &admissionExecutorLink{
			admissionSource: admissionSource{ds: dbA},
			inner:           nilExecutor,
		},
		"unprovable convergence": &admissionDualLink{
			admissionSource: admissionSource{ds: dbA},
			source:          nonComparable,
			executor:        nonComparable,
		},
	}

	deep := crud.Source(admissionSource{ds: dbA})
	for i := 0; i < 65; i++ {
		if i%2 == 0 {
			deep = &admissionSourceLink{admissionSource: admissionSource{ds: dbA}, inner: deep}
			continue
		}
		deep = &admissionExecutorLink{admissionSource: admissionSource{ds: dbA}, inner: deep}
	}
	cases["depth budget"] = deep

	for name, source := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := crud.NewSession(source, markedExecutor{})
			var scoped *crud.ExecutorScopeError
			if !errors.As(err, &scoped) || scoped.Reason != crud.ExecutorScopeTransactionSource {
				t.Fatalf("NewSession returned %v, want transaction_source", err)
			}
		})
	}
}

func TestSessionAdmissionAcceptsConvergentNavigation(t *testing.T) {
	inner := &admissionSourceLink{
		admissionSource: admissionSource{ds: dbA},
		inner:           admissionSource{ds: dbA},
	}
	source := &admissionDualLink{
		admissionSource: admissionSource{ds: dbA},
		source:          inner,
		executor:        inner,
	}
	if _, err := crud.NewSession(source, markedExecutor{}); err != nil {
		t.Fatalf("NewSession rejected convergent navigation: %v", err)
	}
	clearNonComparablePath := &admissionSourceLink{
		admissionSource: admissionSource{ds: dbA},
		inner: admissionInterfacePayloadSource{
			admissionSource: admissionSource{ds: dbA},
			payload:         []int{1},
		},
	}
	if _, err := crud.NewSession(clearNonComparablePath, markedExecutor{}); err != nil {
		t.Fatalf("NewSession rejected a clear non-comparable path: %v", err)
	}
}

type countedReadSourcer struct {
	inner crud.Source
	calls int
}

func (r *countedReadSourcer) Exec(ctx context.Context, query string, args ...any) (crud.Result, error) {
	return r.inner.Exec(ctx, query, args...)
}

func (r *countedReadSourcer) Query(ctx context.Context, query string, args ...any) (crud.Rows, error) {
	return r.inner.Query(ctx, query, args...)
}

func (r *countedReadSourcer) Dialect() crud.Dialect { return r.inner.Dialect() }

func (r *countedReadSourcer) ReadSource() crud.Source {
	r.calls++
	return r.inner
}

func TestReadSourcerDiscoveryDoesNotInvokeTheCapability(t *testing.T) {
	reader := &countedReadSourcer{inner: srcOn(dbB, "replica")}
	found, ok := crud.ReadSourcerOf(reader)
	if !ok || found != reader {
		t.Fatalf("ReadSourcerOf = %#v, %v", found, ok)
	}
	if reader.calls != 0 {
		t.Fatalf("discovery invoked ReadSource %d times", reader.calls)
	}
	if got, ok := crud.ReadSourceOf(reader); !ok || got != reader.inner {
		t.Fatalf("ReadSourceOf = %#v, %v", got, ok)
	}
	if reader.calls != 1 {
		t.Fatalf("ReadSourceOf invoked ReadSource %d times", reader.calls)
	}
}
