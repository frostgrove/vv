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

// OffsetLimiter is implemented by dialects whose grammar has no OFFSET without
// a LIMIT in front of it. MySQL and SQLite both need one and spell "no limit"
// differently; PostgreSQL needs nothing, so it does not implement this and a
// dialect written outside this package keeps compiling.
type OffsetLimiter interface {
	// LimitAll renders the LIMIT clause — leading space included — that has to
	// precede an OFFSET when the caller asked for no limit.
	LimitAll() string
}

// LikeEscaper supplies the SQL fragment that selects the backslash escape
// character for literal-safe LIKE helpers. It is optional so a dialect outside
// this package remains source-compatible; the writer falls back to the
// standard `ESCAPE '\'` form. MySQL implements it because a quoted backslash
// is not stable under NO_BACKSLASH_ESCAPES.
type LikeEscaper interface {
	LikeEscapeClause() string
}

// UpsertScope is the optional interface a Dialect implements to say how much
// its own Upsert clause swallows. A conflict the upsert absorbs is never
// reported, so nothing may claim it as a violation.
//
// Only a dialect whose clause names a target implements it: PostgreSQL and
// SQLite emit ON CONFLICT (pk) DO UPDATE, which swallows the primary key and
// nothing else. MySQL deliberately does not — ON DUPLICATE KEY UPDATE swallows
// every unique key — and neither does a dialect written outside this package.
//
// "Swallows every unique key" is therefore the default, and that direction is
// the safe one: a reader that guessed the other way would claim a conflict the
// server absorbed.
type UpsertScope interface {
	UpsertSwallowsPrimaryKeyOnly() bool
}

// StatementRollback is the optional interface a Dialect implements to say that
// a refused statement rolls back only itself and leaves the transaction usable.
//
// MySQL and SQLite do. PostgreSQL does not — a constraint error aborts the
// whole transaction with SQLSTATE 25P02 and nothing runs until ROLLBACK or
// ROLLBACK TO SAVEPOINT — so it does not implement this, and neither does a
// dialect written elsewhere. The default is therefore "the transaction is
// poisoned", which is the direction that only ever costs a second statement
// nobody runs.
type StatementRollback interface {
	RollsBackStatementOnly() bool
}

// Postgres targets PostgreSQL (and CockroachDB).
type Postgres struct{}

func (Postgres) Name() string             { return "postgres" }
func (Postgres) Placeholder(n int) string { return "$" + strconv.Itoa(n) }
func (Postgres) Quote(ident string) string {
	return `"` + strings.ReplaceAll(ident, `"`, `""`) + `"`
}
func (Postgres) SupportsReturning() bool { return true }

// UpsertSwallowsPrimaryKeyOnly reports what ON CONFLICT (pk) DO UPDATE covers:
// the named target and nothing else. A second unique key still refuses.
func (Postgres) UpsertSwallowsPrimaryKeyOnly() bool { return true }
func (Postgres) LockClause() string                 { return " FOR UPDATE" }
func (Postgres) LikeEscapeClause() string           { return ` ESCAPE '\'` }

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

func (MySQL) Name() string             { return "mysql" }
func (MySQL) Placeholder(int) string   { return "?" }
func (MySQL) SupportsReturning() bool  { return false }
func (MySQL) LockClause() string       { return " FOR UPDATE" }
func (MySQL) LikeEscapeClause() string { return ` ESCAPE X'5C'` }
func (MySQL) Quote(ident string) string {
	return "`" + strings.ReplaceAll(ident, "`", "``") + "`"
}

// LimitAll is MySQL's documented spelling of "everything from here on": the
// largest unsigned 64-bit integer, because the grammar demands a row count.
func (MySQL) LimitAll() string { return " LIMIT 18446744073709551615" }

// RollsBackStatementOnly reports InnoDB's behaviour on a refused statement: it
// is undone on its own and the transaction stays usable.
func (MySQL) RollsBackStatementOnly() bool { return true }

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

// LockClause is empty because SQLite has no row locks: a write transaction
// locks the database. crud.ForUpdate() therefore renders nothing here, and the
// serialisation a caller wanted comes from the transaction instead.
func (SQLite) LockClause() string       { return "" }
func (SQLite) LikeEscapeClause() string { return ` ESCAPE '\'` }

// LimitAll is SQLite's spelling: a negative row count means "no limit".
func (SQLite) LimitAll() string { return " LIMIT -1" }

// UpsertSwallowsPrimaryKeyOnly is Postgres's answer, because Upsert renders
// Postgres's clause.
func (SQLite) UpsertSwallowsPrimaryKeyOnly() bool { return true }

// RollsBackStatementOnly reports SQLite's ON CONFLICT ABORT default: the
// statement is undone and the transaction carries on.
func (SQLite) RollsBackStatementOnly() bool { return true }
func (d SQLite) Quote(ident string) string {
	return `"` + strings.ReplaceAll(ident, `"`, `""`) + `"`
}
func (d SQLite) Upsert(pk string, cols []string) string { return Postgres{}.Upsert(pk, cols) }

var (
	_ Dialect           = Postgres{}
	_ Dialect           = MySQL{}
	_ Dialect           = SQLite{}
	_ UpsertScope       = Postgres{}
	_ UpsertScope       = SQLite{}
	_ StatementRollback = MySQL{}
	_ StatementRollback = SQLite{}
)
