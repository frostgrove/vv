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

func (s pgSchema) results() []crudtest.Result {
	return []crudtest.Result{
		crudtest.Rows(s.columns...),
		crudtest.Rows(s.constraints...),
		crudtest.Rows(s.indexes...),
	}
}

// push queues n passes of the same schema.
func (s pgSchema) push(r *crudtest.Recorder, n int) *crudtest.Recorder {
	for range n {
		r.Push(s.results()...)
	}
	return r
}

// The row shapes, spelled once. Each mirrors one SELECT list in postgres.go, so
// a statement that gains a column breaks these rather than mis-scanning.
func pgColumnRow(table, name string, pos int) []any {
	return []any{"public", table, name, pos, "text", false, false, false, "", 0}
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
// The recorder itself deliberately does not — see
// TestTheRecorderKeysAsItselfSoTheProbeHasAUnitTestSeam.
type identified struct {
	*crudtest.Recorder
	handle any
}

func (s identified) DataSource() any { return s.handle }

// gated holds its first statement until every declarer has reached one, so the
// interleaving the concurrent declaration test is about happens rather than
// being hoped for.
type gated struct {
	identified
	arrive *sync.WaitGroup
	once   *sync.Once
}

func (g gated) Query(ctx context.Context, q string, args ...any) (crud.Rows, error) {
	g.once.Do(func() { g.arrive.Done(); g.arrive.Wait() })
	return g.identified.Query(ctx, q, args...)
}

// awkward is a Source that cannot name a database and cannot be compared
// either. Its type is comparable as far as reflect is concerned — a pointer and
// an interface — and == on it panics once that interface holds a slice.
type awkward struct {
	*crudtest.Recorder
	payload any
}

// namedDialect is a dialect that answers to a name no back-end serves.
type namedDialect struct {
	crud.Postgres
	name string
}

func (d namedDialect) Name() string { return d.name }

var (
	_ crud.Source     = awkward{}
	_ crud.Source     = identified{}
	_ crud.Source     = gated{}
	_ crud.Identified = identified{}
	_ crud.Dialect    = namedDialect{}
)
