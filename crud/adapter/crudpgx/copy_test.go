package crudpgx

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/errs"
)

type copyHandle struct {
	table  pgx.Identifier
	called int
	err    error
}

func (*copyHandle) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, nil
}

func (*copyHandle) Query(context.Context, string, ...any) (pgx.Rows, error) { return nil, nil }

func (this *copyHandle) CopyFrom(_ context.Context, table pgx.Identifier, _ []string, _ pgx.CopyFromSource) (int64, error) {
	this.called++
	this.table = slices.Clone(table)
	if this.err != nil {
		return 0, this.err
	}
	return 2, nil
}

type queryOnlyHandle struct{}

func (*queryOnlyHandle) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, nil
}

func (*queryOnlyHandle) Query(context.Context, string, ...any) (pgx.Rows, error) { return nil, nil }

func TestUnsafeCopyFromTableHandsPgxSeparateExactIdentifierComponents(t *testing.T) {
	handle := new(copyHandle)
	executor := From(handle)
	ref := crud.TableRef{Schema: "tenant.42", Name: "product.events"}

	n, err := executor.UnsafeCopyFromTable(context.Background(), ref, []string{"id"}, [][]any{{1}, {2}})
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 || handle.called != 1 {
		t.Fatalf("count/calls = %d/%d", n, handle.called)
	}
	if !slices.Equal(handle.table, []string{"tenant.42", "product.events"}) {
		t.Fatalf("pgx identifier = %#v", handle.table)
	}
}

func TestUnsafeStringCopyFromRefusesADotBeforeCallingPgx(t *testing.T) {
	handle := new(copyHandle)
	executor := From(handle)

	if _, err := executor.UnsafeCopyFrom(context.Background(), "tenant_42.products", nil, nil); err == nil ||
		!strings.Contains(err.Error(), "separate components") {
		t.Fatalf("dotted COPY error = %v", err)
	}
	if handle.called != 0 {
		t.Fatal("a dotted string reached pgx")
	}
	if _, err := executor.UnsafeCopyFromTable(context.Background(), crud.TableRef{}, nil, nil); err == nil {
		t.Fatal("an invalid structured reference reached pgx")
	}
	if handle.called != 0 {
		t.Fatal("an invalid TableRef reached pgx")
	}
}

func TestUnsafeCopyEmptyRowsIsANoopButStillValidatesTheTable(t *testing.T) {
	handle := new(copyHandle)
	executor := From(handle)

	n, err := executor.UnsafeCopyFromTable(context.Background(), crud.TableRef{Name: "events"}, []string{"id"}, nil)
	if err != nil || n != 0 {
		t.Fatalf("empty COPY = %d, %v", n, err)
	}
	if handle.called != 0 {
		t.Fatalf("empty COPY called pgx %d times", handle.called)
	}
	if _, err := executor.UnsafeCopyFromTable(context.Background(), crud.TableRef{}, nil, nil); err == nil {
		t.Fatal("invalid table was hidden by the empty-row no-op")
	}
}

func TestUnsafeCopyNamesMissingBulkSupportRatherThanTransactions(t *testing.T) {
	executor := From(new(queryOnlyHandle))
	_, err := executor.UnsafeCopyFromTable(context.Background(), crud.TableRef{Name: "events"}, []string{"id"}, [][]any{{1}})
	if !errors.Is(err, crud.ErrNoBulkInsertSupport) {
		t.Fatalf("err = %v, want ErrNoBulkInsertSupport", err)
	}
	if errors.Is(err, crud.ErrNoTxSupport) {
		t.Fatalf("COPY refusal still describes transaction support: %v", err)
	}
}

func TestUnsafeCopyClassifiesDriverConflicts(t *testing.T) {
	driverErr := &pgconn.PgError{Code: "23505", ConstraintName: "events_key", TableName: "events", ColumnName: "code"}
	handle := &copyHandle{err: driverErr}
	executor := From(handle)

	_, err := executor.UnsafeCopyFromTable(context.Background(), crud.TableRef{Name: "events"}, []string{"code"}, [][]any{{"duplicate"}})
	if !errors.Is(err, crud.ErrConflict) {
		t.Fatalf("err = %v, want classified conflict", err)
	}
	var retained *pgconn.PgError
	if !errors.As(err, &retained) || retained != driverErr {
		t.Fatalf("classified error did not retain pgx cause: %#v", retained)
	}
	if fault, ok := errs.AsFault(err); !ok || fault.Code != errs.CodeUnique {
		t.Fatalf("COPY fault = %#v, found=%v", fault, ok)
	}
}

func TestExactUnsafeCopyAndContextResolvedBulkAreDifferentOnPurpose(t *testing.T) {
	pool := new(copyHandle)
	tx := new(copyHandle)
	source := From(pool)
	ctx := source.BindExecutor(context.Background(), tx)
	ref := crud.TableRef{Name: "events"}

	if _, err := source.UnsafeCopyFromTable(ctx, ref, []string{"id"}, [][]any{{1}}); err != nil {
		t.Fatal(err)
	}
	if _, err := crud.UnsafeBulkInsertFor(ctx, source, ref, []string{"id"}, [][]any{{2}}); err != nil {
		t.Fatal(err)
	}
	if pool.called != 1 || tx.called != 1 {
		t.Fatalf("pool/tx COPY calls = %d/%d, want exact unsafe 1 and resolved helper 1", pool.called, tx.called)
	}
}
