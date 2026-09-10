package crud_test

import (
	"context"
	"strconv"
	"testing"

	"github.com/frostgrove/vv/crud"
)

type countedExecutorLink struct {
	inner crud.Executor
	calls *int
}

func (link *countedExecutorLink) Exec(ctx context.Context, query string, args ...any) (crud.Result, error) {
	return link.inner.Exec(ctx, query, args...)
}

func (link *countedExecutorLink) Query(ctx context.Context, query string, args ...any) (crud.Rows, error) {
	return link.inner.Query(ctx, query, args...)
}

func (link *countedExecutorLink) UnwrapExecutor() crud.Executor {
	*link.calls++
	return link.inner
}

type boundaryBeginnerSource struct{}

func (boundaryBeginnerSource) Exec(context.Context, string, ...any) (crud.Result, error) {
	return crud.Result{}, nil
}

func (boundaryBeginnerSource) Query(context.Context, string, ...any) (crud.Rows, error) {
	return nil, nil
}

func (boundaryBeginnerSource) Dialect() crud.Dialect { return crud.Postgres{} }

func (boundaryBeginnerSource) Begin(context.Context) (crud.Tx, error) { return nil, nil }

type countedSourceLink struct {
	inner crud.Source
	calls *int
}

func (link *countedSourceLink) Exec(ctx context.Context, query string, args ...any) (crud.Result, error) {
	return link.inner.Exec(ctx, query, args...)
}

func (link *countedSourceLink) Query(ctx context.Context, query string, args ...any) (crud.Rows, error) {
	return link.inner.Query(ctx, query, args...)
}

func (link *countedSourceLink) Dialect() crud.Dialect { return link.inner.Dialect() }

func (link *countedSourceLink) UnwrapSource() crud.Source {
	*link.calls++
	return link.inner
}

func executorChain(values int, calls *int) crud.Executor {
	var current crud.Executor = markedExecutor{}
	for range values - 1 {
		current = &countedExecutorLink{inner: current, calls: calls}
	}
	return current
}

func sourceChain(values int, calls *int) crud.Source {
	var current crud.Source = boundaryBeginnerSource{}
	for range values - 1 {
		current = &countedSourceLink{inner: current, calls: calls}
	}
	return current
}

func TestExecutorWalkInspectsThe64thValueWithoutCallingItsUnwrapper(t *testing.T) {
	for _, values := range []int{63, 64, 65} {
		t.Run(strconv.Itoa(values), func(t *testing.T) {
			calls := 0
			found, ok := crud.ExecutorAs[executorMarker](executorChain(values, &calls))
			wantOK := values <= 64
			if ok != wantOK || ok && found.ExecutorMarker() != "target" {
				t.Fatalf("ExecutorAs values=%d = %#v, %v", values, found, ok)
			}
			wantCalls := values - 1
			if wantCalls > 63 {
				wantCalls = 63
			}
			if calls != wantCalls {
				t.Fatalf("unwrap calls values=%d = %d, want %d", values, calls, wantCalls)
			}
		})
	}
}

func TestSourceWalkUsesTheSameExact64ValueBudget(t *testing.T) {
	for _, values := range []int{63, 64, 65} {
		t.Run(strconv.Itoa(values), func(t *testing.T) {
			calls := 0
			_, ok := crud.BeginnerOf(sourceChain(values, &calls))
			if ok != (values <= 64) {
				t.Fatalf("BeginnerOf values=%d found=%v", values, ok)
			}
			wantCalls := values - 1
			if wantCalls > 63 {
				wantCalls = 63
			}
			if calls != wantCalls {
				t.Fatalf("unwrap calls values=%d = %d, want %d", values, calls, wantCalls)
			}
		})
	}
}

func TestNavigationCyclesAndTypedNilCapabilitiesFailClosed(t *testing.T) {
	executorCalls := 0
	executorCycle := &countedExecutorLink{calls: &executorCalls}
	executorCycle.inner = executorCycle
	if _, ok := crud.ExecutorAs[executorMarker](executorCycle); ok || executorCalls != 63 {
		t.Fatalf("executor cycle = found:%v calls:%d", ok, executorCalls)
	}

	sourceCalls := 0
	sourceCycle := &countedSourceLink{calls: &sourceCalls}
	sourceCycle.inner = sourceCycle
	if _, ok := crud.BeginnerOf(sourceCycle); ok || sourceCalls != 63 {
		t.Fatalf("source cycle = found:%v calls:%d", ok, sourceCalls)
	}

	var nilBeginner *typedNilBeginnerSource
	if _, ok := crud.BeginnerOf(nilBeginner); ok {
		t.Fatal("typed-nil Beginner was admitted")
	}
	reader := typedNilReadSourcer{}
	if _, ok := crud.ReadSourceOf(reader); ok {
		t.Fatal("typed-nil ReadSource result was admitted")
	}
}

type typedNilBeginnerSource struct{ boundaryBeginnerSource }

type typedNilReadSourcer struct{ boundaryBeginnerSource }

func (typedNilReadSourcer) ReadSource() crud.Source {
	return (*typedNilBeginnerSource)(nil)
}
