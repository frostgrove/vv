package crudsql_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/adapter/crudsql"
)

type wrappedSQLExecutor struct{ inner crud.Executor }

func (w wrappedSQLExecutor) Exec(ctx context.Context, query string, args ...any) (crud.Result, error) {
	return w.inner.Exec(ctx, query, args...)
}

func (w wrappedSQLExecutor) Query(ctx context.Context, query string, args ...any) (crud.Rows, error) {
	return w.inner.Query(ctx, query, args...)
}

func (w wrappedSQLExecutor) UnwrapExecutor() crud.Executor { return w.inner }

func TestTransactionExtractionCrossesDeclaredExecutorWrappers(t *testing.T) {
	native := new(sql.Tx)
	wrapper := wrappedSQLExecutor{inner: crudsql.From(native)}

	got, ok := crudsql.Transaction(wrapper)
	if !ok || got != native {
		t.Fatalf("Transaction = %p, %v; want %p", got, ok, native)
	}
}
