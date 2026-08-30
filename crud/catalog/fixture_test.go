package catalog

import (
	"context"
	"sync"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/crudtest"
)

// The canned schemas the unit tests load. They are PostgreSQL-shaped because
// readPostgres is the shortest of the four — three statements, no version probe
// — and what these tests are about is keying, ordering, refusal and the negative
// cache, none of which is per-engine. The engines' own answers are the
// integration suite's business, because only a server can give them.

// pgPass is how many statements one PostgreSQL introspection pass sends. A test
// that counts passes counts statements and divides, so a loader that grew a
// fourth statement fails loudly here rather than quietly miscounting.
const pgPass = 3

// pgSchema is one canned schema: the three result sets readPostgres asks for, in
// the order it asks for them.
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

// push queues n passes of the same schema.
func (this pgSchema) push(r *crudtest.Recorder, n int) *crudtest.Recorder {
	for range n {
		r.Push(this.results()...)
	}
	return r
}

// The row shapes, spelled once. Each mirrors one SELECT list in postgres.go, so
// a statement that gains a column breaks these rather than mis-scanning.
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

// oneTable is the smallest schema that is still worth looking at: one table, one
// column, one primary key.
func oneTable() pgSchema {
	return pgSchema{
		columns:     [][]any{pgColumnRow("rows", "id", 1)},
		constraints: [][]any{pgConstraintRow("rows", "rows_pkey", "p", 1, "id")},
	}
}

// recorder answers PostgreSQL statements and carries n passes of s.
func recorder(s pgSchema, n int) *crudtest.Recorder {
	return s.push(crudtest.Postgres(), n)
}

// identified is a Source that names a handle, the way crudsql.Executor does.
type identified struct {
	*crudtest.Recorder
	handle any
}

func (this identified) DataSource() any { return this.handle }

// gated holds its first statement until every declarer has reached one, so the
// interleaving the concurrent declaration test is about happens rather than
// being hoped for.
type gated struct {
	identified
	arrive *sync.WaitGroup
	once   *sync.Once
}

func (this gated) Query(ctx context.Context, q string, args ...any) (crud.Rows, error) {
	this.once.Do(func() { this.arrive.Done(); this.arrive.Wait() })
	return this.identified.Query(ctx, q, args...)
}

// anonymous deliberately forwards only Source. Recorder identifies itself so
// its transactions can be scoped safely; tests for third-party sources that do
// not implement Identified need an explicit capability-erasing wrapper.
type anonymous struct{ recorder *crudtest.Recorder }

func (this anonymous) Exec(ctx context.Context, q string, args ...any) (crud.Result, error) {
	return this.recorder.Exec(ctx, q, args...)
}

func (this anonymous) Query(ctx context.Context, q string, args ...any) (crud.Rows, error) {
	return this.recorder.Query(ctx, q, args...)
}

func (this anonymous) Dialect() crud.Dialect { return this.recorder.Dialect() }

// awkward is a Source that cannot name a database and cannot be compared
// either. reflect.Type calls its outer shape comparable — a pointer and an
// interface — but reflect.Value.Comparable sees the slice inside that interface
// and lets SameDataSource refuse it before == can panic.
type awkward struct {
	anonymous
	payload any
}

// namedDialect is a dialect that answers to a name no back-end serves.
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
