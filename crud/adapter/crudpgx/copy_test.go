package crudpgx

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/frostgrove/vv/crud"
)

type copyHandle struct {
	table  pgx.Identifier
	called int
}

func (*copyHandle) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, nil
}

func (*copyHandle) Query(context.Context, string, ...any) (pgx.Rows, error) { return nil, nil }

func (this *copyHandle) CopyFrom(_ context.Context, table pgx.Identifier, _ []string, _ pgx.CopyFromSource) (int64, error) {
	this.called++
	this.table = slices.Clone(table)
	return 2, nil
}

func TestCopyFromTableHandsPgxSeparateExactIdentifierComponents(t *testing.T) {
	handle := new(copyHandle)
	executor := From(handle)
	ref := crud.TableRef{Schema: "tenant.42", Name: "product.events"}

	n, err := executor.CopyFromTable(context.Background(), ref, []string{"id"}, [][]any{{1}, {2}})
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

func TestStringCopyFromRefusesADotBeforeCallingPgx(t *testing.T) {
	handle := new(copyHandle)
	executor := From(handle)

	if _, err := executor.CopyFrom(context.Background(), "tenant_42.products", nil, nil); err == nil ||
		!strings.Contains(err.Error(), "separate components") {
		t.Fatalf("dotted COPY error = %v", err)
	}
	if handle.called != 0 {
		t.Fatal("a dotted string reached pgx")
	}
	if _, err := executor.CopyFromTable(context.Background(), crud.TableRef{}, nil, nil); err == nil {
		t.Fatal("an invalid structured reference reached pgx")
	}
	if handle.called != 0 {
		t.Fatal("an invalid TableRef reached pgx")
	}
}
