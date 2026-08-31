package catalog

import (
	"context"
	"sync"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/crudtest"
)

const pgPass = 3

type pgSchema struct {
	columns     [][]any
	constraints [][]any
	indexes     [][]any
}

func (this pgSchema) results() []crudtest.Result {
	return []crudtest.Result{
		crudtest.Rows(this.columns...),
		crudtest.Rows(this.constraints...),
		crudtest.Rows(this.indexes...),
	}
}

func (this pgSchema) push(r *crudtest.Recorder, n int) *crudtest.Recorder {
	for range n {
		r.Push(this.results()...)
	}
	return r
}

func pgColumnRow(table, name string, pos int) []any {
	return pgColumnRowInSchema("public", table, name, pos, true)
}

func pgColumnRowInSchema(schema, table, name string, pos int, bare bool) []any {
	return []any{schema, table, name, pos, "text", false, false, false, "", 0, bare}
}

func pgConstraintRow(table, name, contype string, ord int, col string) []any {
	return []any{"public", table, name, contype, false, ord, col, "", "", "", "", "",
		contype + " on " + table + "." + col}
}

func pgIndexRow(table, name, col string, partial bool, predicate string) []any {
	return []any{"public", table, name, col, "", partial, predicate,
		"CREATE UNIQUE INDEX " + name + " ON " + table}
}

func oneTable() pgSchema {
	return pgSchema{
		columns:     [][]any{pgColumnRow("rows", "id", 1)},
		constraints: [][]any{pgConstraintRow("rows", "rows_pkey", "p", 1, "id")},
	}
}

func recorder(s pgSchema, n int) *crudtest.Recorder {
	return s.push(crudtest.Postgres(), n)
}

type identified struct {
	*crudtest.Recorder
	handle any
}

func (this identified) DataSource() any { return this.handle }

type gated struct {
	identified
	arrive *sync.WaitGroup
	once   *sync.Once
}

func (this gated) Query(ctx context.Context, q string, args ...any) (crud.Rows, error) {
	this.once.Do(func() { this.arrive.Done(); this.arrive.Wait() })
	return this.identified.Query(ctx, q, args...)
}

type anonymous struct{ recorder *crudtest.Recorder }

func (this anonymous) Exec(ctx context.Context, q string, args ...any) (crud.Result, error) {
	return this.recorder.Exec(ctx, q, args...)
}

func (this anonymous) Query(ctx context.Context, q string, args ...any) (crud.Rows, error) {
	return this.recorder.Query(ctx, q, args...)
}

func (this anonymous) Dialect() crud.Dialect { return this.recorder.Dialect() }

type awkward struct {
	anonymous
	payload any
}

type namedDialect struct {
	crud.Postgres
	name string
}

func (this namedDialect) Name() string { return this.name }

var (
	_ crud.Source     = anonymous{}
	_ crud.Source     = awkward{}
	_ crud.Source     = identified{}
	_ crud.Source     = gated{}
	_ crud.Identified = identified{}
	_ crud.Dialect    = namedDialect{}
)
