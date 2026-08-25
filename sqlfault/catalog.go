package sqlfault

import (
	"slices"

	"github.com/shardit-io/vv/catalog"
	"github.com/shardit-io/vv/errs"
)

// Columns answers which columns a constraint covers, in key order.
//
// One method, and it hands back names and nothing else. A third party can
// supply a schema without importing catalog, and cannot hand back a predicate, a
// definition or a DDL text — none of which has any business near a renderer
// ([[D-044]]).
//
// It takes no context and does no I/O. A signature that accepted one would be a
// lazy loader, and a lazy loader cannot fail at start-up ([[D-041]]).
type Columns interface {
	ConstraintColumns(table, constraint string) ([]string, bool)
}

// FromCatalog wires a loaded catalog as the lookup.
//
// The catalog is held on the classifier value the caller declared, never in a
// package-level variable: it is per physical handle, and one process talking to
// two databases has two of them ([[D-041]]).
func FromCatalog(cat catalog.Catalog) Columns { return catalogColumns{cat} }

type catalogColumns struct{ cat catalog.Catalog }

func (c catalogColumns) ConstraintColumns(table, constraint string) ([]string, bool) {
	if c.cat == nil {
		return nil, false
	}
	con, ok := c.cat.Constraint(table, constraint)
	if !ok || len(con.Columns) == 0 {
		return nil, false
	}
	// An expression key part is recorded as an empty name with the text in
	// catalog.Constraint.Expressions. The SPI hands back names, and "" is not one:
	// passed on, it claims a column that does not exist. Dropping only that part
	// is worse — it describes (tenant_id, lower(email)) as the key (tenant_id),
	// which is a key the engine does not enforce, and a resolver would then raise
	// a violation against a field nothing collided on. This adapter is the only
	// thing that knows the positional convention, so it is where "not a name"
	// becomes "not known".
	out := make([]string, len(con.Columns))
	copy(out, con.Columns)
	return out, true
}

// fill supplies the columns the driver did not name, and only those.
//
// Three engines name no column ever: mysql.MySQLError carries Number, SQLState
// and Message and nothing else, and SQLite's error carries nothing at all. Even
// PostgreSQL names one only for 23502 — a unique violation reports the
// constraint and the table and no column, which is exactly what this fills.
//
// A miss leaves the list nil rather than empty. "Not known" must not read as
// "no columns" (errs/build.go:cloneStrings holds the same rule for the builder).
// A list holding a name that is not a name is a miss too: [Columns] is a
// third-party interface, and one empty entry would claim a column no schema has.
// And what the driver said is never overwritten: the engine saw the statement
// and this did not.
func (c *Classifier) fill(s errs.Source) errs.Source {
	if c.cols == nil || len(s.Columns) > 0 || s.Table == "" || s.Constraint == "" {
		return s
	}
	if cols, ok := c.cols.ConstraintColumns(s.Table, s.Constraint); ok && len(cols) > 0 && !slices.Contains(cols, "") {
		s.Columns = cols
	}
	return s
}
