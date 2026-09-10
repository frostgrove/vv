package crud_test

import (
	"context"
	"errors"
	"testing"

	"github.com/frostgrove/vv/crud"
)

func requireSourceBoundScopeReason(t *testing.T, ctx context.Context, source any, reason crud.ExecutorScopeReason) {
	t.Helper()
	executor, found, err := crud.SourceBoundExecutorFor(ctx, source)
	if executor != nil || found {
		t.Fatalf("SourceBoundExecutorFor returned executor=%v, found=%v on a rejected scope", executor, found)
	}
	if !errors.Is(err, crud.ErrExecutorScope) {
		t.Fatalf("SourceBoundExecutorFor returned %v, want ErrExecutorScope", err)
	}
	var scoped *crud.ExecutorScopeError
	if !errors.As(err, &scoped) || scoped.Reason != reason {
		t.Fatalf("scope error = %#v, want reason %q", scoped, reason)
	}
}

func TestSourceBoundExecutorForReturnsOnlyAnExactDeclaredAssociation(t *testing.T) {
	source := srcOn(dbA, "declaration")
	bound := fakeExec{name: "tx-of-a"}
	ctx := crud.BindExecutor(context.Background(), source, bound)

	executor, found, err := crud.SourceBoundExecutorFor(ctx, srcOn(dbA, "repository"))
	if err != nil || !found || executor != bound {
		t.Fatalf("exact source binding = (%v, %v, %v), want the declared executor", executor, found, err)
	}
	executor, found, err = crud.SourceBoundExecutorFor(ctx, srcOn(dbB, "other"))
	if err != nil || found || executor != nil {
		t.Fatalf("unrelated safe binding = (%v, %v, %v), want no binding", executor, found, err)
	}
	executor, found, err = crud.SourceBoundExecutorFor(context.Background(), source)
	if err != nil || found || executor != nil {
		t.Fatalf("empty context = (%v, %v, %v), want no binding", executor, found, err)
	}
}

func TestSourceBoundExecutorForRejectsUnsafeAndInvalidBindingProvenance(t *testing.T) {
	source := srcOn(dbA, "repository")
	requireSourceBoundScopeReason(t,
		crud.WithUnsafeExecutor(context.Background(), fakeExec{name: "unsafe"}),
		source,
		crud.ExecutorScopeMissingSource,
	)
	requireSourceBoundScopeReason(t,
		crud.WithExecutor(context.Background(), fakeExec{name: "strict"}),
		source,
		crud.ExecutorScopeMismatch,
	)
	requireSourceBoundScopeReason(t,
		(crud.Session{}).Bind(context.Background()),
		source,
		crud.ExecutorScopeInvalidSession,
	)
}

func TestSourceBoundExecutorForValidatesTheWholeBindingChain(t *testing.T) {
	source := srcOn(dbA, "repository")
	bound := fakeExec{name: "exact"}

	ctx := crud.WithUnsafeExecutor(context.Background(), fakeExec{name: "fallback"})
	ctx = crud.BindExecutor(ctx, source, bound)
	executor, found, err := crud.SourceBoundExecutorFor(ctx, source)
	if err != nil || !found || executor != bound {
		t.Fatalf("exact binding over unsafe fallback = (%v, %v, %v), want the exact binding", executor, found, err)
	}

	poisoned := (crud.Session{}).Bind(context.Background())
	poisoned = crud.BindExecutor(poisoned, source, bound)
	requireSourceBoundScopeReason(t, poisoned, source, crud.ExecutorScopeInvalidSession)

	strict := crud.WithExecutor(context.Background(), fakeExec{name: "strict outer"})
	strict = crud.BindExecutor(strict, source, bound)
	requireSourceBoundScopeReason(t, strict, source, crud.ExecutorScopeMismatch)
}

func TestSourceBoundExecutorForDoesNotChangeUnsafeExecutorResolution(t *testing.T) {
	source := srcOn(dbA, "repository")
	unsafe := fakeExec{name: "explicit unsafe"}
	ctx := crud.WithUnsafeExecutor(context.Background(), unsafe)

	executor, found := crud.ExecutorFor(ctx, source)
	if !found || executor != unsafe {
		t.Fatalf("ExecutorFor no longer accepts the explicit unsafe fallback: (%v, %v)", executor, found)
	}

	safe := crud.BindExecutor(context.Background(), srcOn(dbB, "other"), fakeExec{name: "other tx"})
	if executor, found := crud.ExecutorFor(safe, source); found || executor != nil {
		t.Fatalf("ExecutorFor changed unrelated safe binding behavior: (%v, %v)", executor, found)
	}
}
