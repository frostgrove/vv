package crud

import (
	"strconv"
	"strings"
)

// Dialect is the only place SQL syntax differences live.
type Dialect interface {
	Name() string
	// Placeholder renders the n-th bind marker, 1-based ($1 / ?).
	Placeholder(n int) string
	// Quote renders an identifier.
	Quote(ident string) string
	// Upsert renders the conflict clause appended to an INSERT, including the
	// leading space. cols are the columns that should be overwritten on
	// conflict (never the primary key).
	Upsert(pk string, cols []string) string
	// SupportsReturning reports whether INSERT/UPDATE ... RETURNING works. When
	// it does, writes refresh the model in a single round trip.
	SupportsReturning() bool
	// LockClause renders a row lock for SELECT ... FOR UPDATE.
	LockClause() string
}

// Postgres targets PostgreSQL (and CockroachDB).
type Postgres struct{}

func (Postgres) Name() string             { return "postgres" }
func (Postgres) Placeholder(n int) string { return "$" + strconv.Itoa(n) }
func (Postgres) Quote(ident string) string {
	return `"` + strings.ReplaceAll(ident, `"`, `""`) + `"`
}
func (Postgres) SupportsReturning() bool { return true }
func (Postgres) LockClause() string      { return " FOR UPDATE" }

func (d Postgres) Upsert(pk string, cols []string) string {
	var b strings.Builder
	b.WriteString(" ON CONFLICT (")
	b.WriteString(d.Quote(pk))
	b.WriteString(") DO ")
	if len(cols) == 0 {
		b.WriteString("NOTHING")
		return b.String()
	}
	b.WriteString("UPDATE SET ")
	for i, c := range cols {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(d.Quote(c))
		b.WriteString(" = EXCLUDED.")
		b.WriteString(d.Quote(c))
	}
	return b.String()
}

// MySQL targets MySQL and MariaDB.
//
// RowAlias switches the upsert clause from the deprecated VALUES() function to
// the `AS new` row alias introduced in MySQL 8.0.19. Leave it off for MariaDB
// and MySQL 5.7.
type MySQL struct {
	RowAlias bool
}

func (MySQL) Name() string            { return "mysql" }
func (MySQL) Placeholder(int) string  { return "?" }
func (MySQL) SupportsReturning() bool { return false }
func (MySQL) LockClause() string      { return " FOR UPDATE" }
func (MySQL) Quote(ident string) string {
	return "`" + strings.ReplaceAll(ident, "`", "``") + "`"
}

func (d MySQL) Upsert(pk string, cols []string) string {
	var b strings.Builder
	if d.RowAlias {
		b.WriteString(" AS new")
	}
	b.WriteString(" ON DUPLICATE KEY UPDATE ")
	if len(cols) == 0 {
		// A no-op assignment keeps the statement valid and reports 0 rows.
		b.WriteString(d.Quote(pk))
		b.WriteString(" = ")
		b.WriteString(d.Quote(pk))
		return b.String()
	}
	for i, c := range cols {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(d.Quote(c))
		b.WriteString(" = ")
		if d.RowAlias {
			b.WriteString("new.")
			b.WriteString(d.Quote(c))
		} else {
			b.WriteString("VALUES(")
			b.WriteString(d.Quote(c))
			b.WriteString(")")
		}
	}
	return b.String()
}

// SQLite targets modern SQLite (3.35+, which has RETURNING).
type SQLite struct{}

func (SQLite) Name() string            { return "sqlite" }
func (SQLite) Placeholder(int) string  { return "?" }
func (SQLite) SupportsReturning() bool { return true }
func (SQLite) LockClause() string      { return "" }
func (d SQLite) Quote(ident string) string {
	return `"` + strings.ReplaceAll(ident, `"`, `""`) + `"`
}
func (d SQLite) Upsert(pk string, cols []string) string { return Postgres{}.Upsert(pk, cols) }

var (
	_ Dialect = Postgres{}
	_ Dialect = MySQL{}
	_ Dialect = SQLite{}
)
