package crud_test

import (
	"context"
	"errors"
	"testing"

	"github.com/frostgrove/vv/crud"
)

type routedExecutor struct {
	execs   int
	queries int
}

func (this *routedExecutor) Exec(context.Context, string, ...any) (crud.Result, error) {
	this.execs++
	return crud.Result{}, nil
}

func (this *routedExecutor) Query(context.Context, string, ...any) (crud.Rows, error) {
	this.queries++
	return nil, nil
}

type routedSource struct {
	routedExecutor
	id any
}

func (this *routedSource) Dialect() crud.Dialect { return crud.Postgres{} }
func (this *routedSource) DataSource() any       { return this.id }

func TestUnsafeRawHelpersResolveTheSourceBoundExecutor(t *testing.T) {
	source := &routedSource{id: new(int)}
	ambient := new(routedExecutor)
	ctx := crud.BindExecutor(context.Background(), source, ambient)

	if _, err := crud.UnsafeExecFor(ctx, source, "UPDATE events SET seen = true"); err != nil {
		t.Fatal(err)
	}
	if _, err := crud.UnsafeQueryFor(ctx, source, "SELECT id FROM events"); err != nil {
		t.Fatal(err)
	}
	if ambient.execs != 1 || ambient.queries != 1 {
		t.Fatalf("ambient exec/query = %d/%d, want 1/1", ambient.execs, ambient.queries)
	}
	if source.execs != 0 || source.queries != 0 {
		t.Fatalf("source exec/query = %d/%d; raw SQL escaped the session", source.execs, source.queries)
	}
}

func TestUnsafeRawHelpersDoNotCaptureAnotherDatasource(t *testing.T) {
	source := &routedSource{id: new(int)}
	other := &routedSource{id: new(int)}
	ambient := new(routedExecutor)
	ctx := crud.BindExecutor(context.Background(), other, ambient)

	if _, err := crud.UnsafeExecFor(ctx, source, "UPDATE events SET seen = true"); err != nil {
		t.Fatal(err)
	}
	if source.execs != 1 || ambient.execs != 0 {
		t.Fatalf("source/foreign execs = %d/%d", source.execs, ambient.execs)
	}
	if _, err := crud.UnsafeQueryFor(ctx, source, "SELECT id FROM events"); err != nil {
		t.Fatal(err)
	}
	if source.queries != 1 || ambient.queries != 0 {
		t.Fatalf("source/foreign queries = %d/%d", source.queries, ambient.queries)
	}
}

func TestUnsafeRawHelpersPreserveExecutorDeclarationFailures(t *testing.T) {
	source := &routedSource{id: new(int)}
	ctx := (crud.Session{}).Bind(context.Background())

	if _, err := crud.UnsafeExecFor(ctx, source, "must not run"); !errors.Is(err, crud.ErrExecutorScope) {
		t.Fatalf("err = %v, want ErrExecutorScope", err)
	}
	if _, err := crud.UnsafeQueryFor(ctx, source, "must not run"); !errors.Is(err, crud.ErrExecutorScope) {
		t.Fatalf("query err = %v, want ErrExecutorScope", err)
	}
	if source.execs != 0 {
		t.Fatalf("invalid session reached source %d times", source.execs)
	}
}

func TestUnsafeRawHelpersRefuseANilSource(t *testing.T) {
	var source *routedSource
	if _, err := crud.UnsafeExecFor(context.Background(), source, "must not run"); !errors.Is(err, crud.ErrExecutorScope) {
		t.Fatalf("exec err = %v, want ErrExecutorScope", err)
	}
	if _, err := crud.UnsafeQueryFor(context.Background(), source, "must not run"); !errors.Is(err, crud.ErrExecutorScope) {
		t.Fatalf("query err = %v, want ErrExecutorScope", err)
	}
}
