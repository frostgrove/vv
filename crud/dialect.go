package crud

import (
	"strconv"
	"strings"
)

type Dialect interface {
	Name() string

	Placeholder(n int) string

	Quote(ident string) string

	Upsert(pk string, cols []string) string

	SupportsReturning() bool

	LockClause() string
}

type BindBudget interface {
	MaxBindValues() int
}

type DefaultValuesInserter interface {
	DefaultValuesClause() string
}

func DefaultValuesClause(d Dialect) string {
	if inserter, ok := d.(DefaultValuesInserter); ok {
		if clause := inserter.DefaultValuesClause(); clause != "" {
			if clause[0] != ' ' {
				return " " + clause
			}
			return clause
		}
	}
	return " DEFAULT VALUES"
}

const PortableBindLimit = 999

func BindLimit(d Dialect) int {
	if budget, ok := d.(BindBudget); ok {
		if limit := budget.MaxBindValues(); limit > 0 {
			return limit
		}
	}
	return PortableBindLimit
}

type OffsetLimiter interface {
	LimitAll() string
}

type LikeEscaper interface {
	LikeEscapeClause() string
}

type UpsertScope interface {
	UpsertSwallowsPrimaryKeyOnly() bool
}

type StatementRollback interface {
	RollsBackStatementOnly() bool
}

type Postgres struct{}

func (Postgres) Name() string             { return "postgres" }
func (Postgres) Placeholder(n int) string { return "$" + strconv.Itoa(n) }
func (Postgres) Quote(ident string) string {
	return `"` + strings.ReplaceAll(ident, `"`, `""`) + `"`
}
func (Postgres) SupportsReturning() bool { return true }

func (Postgres) MaxBindValues() int { return 65_535 }

func (Postgres) UpsertSwallowsPrimaryKeyOnly() bool { return true }
func (Postgres) LockClause() string                 { return " FOR UPDATE" }
func (Postgres) LikeEscapeClause() string           { return ` ESCAPE '\'` }

func (this Postgres) Upsert(pk string, cols []string) string {
	var b strings.Builder
	b.WriteString(" ON CONFLICT (")
	b.WriteString(this.Quote(pk))
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
		b.WriteString(this.Quote(c))
		b.WriteString(" = EXCLUDED.")
		b.WriteString(this.Quote(c))
	}
	return b.String()
}

type MySQL struct {
	RowAlias bool
}

func (MySQL) Name() string                { return "mysql" }
func (MySQL) Placeholder(int) string      { return "?" }
func (MySQL) SupportsReturning() bool     { return false }
func (MySQL) LockClause() string          { return " FOR UPDATE" }
func (MySQL) LikeEscapeClause() string    { return ` ESCAPE X'5C'` }
func (MySQL) MaxBindValues() int          { return 65_535 }
func (MySQL) DefaultValuesClause() string { return " () VALUES ()" }
func (MySQL) Quote(ident string) string {
	return "`" + strings.ReplaceAll(ident, "`", "``") + "`"
}

func (MySQL) LimitAll() string { return " LIMIT 18446744073709551615" }

func (MySQL) RollsBackStatementOnly() bool { return true }

func (this MySQL) Upsert(pk string, cols []string) string {
	var b strings.Builder
	if this.RowAlias {
		b.WriteString(" AS new")
	}
	b.WriteString(" ON DUPLICATE KEY UPDATE ")
	if len(cols) == 0 {
		b.WriteString(this.Quote(pk))
		b.WriteString(" = ")
		b.WriteString(this.Quote(pk))
		return b.String()
	}
	for i, c := range cols {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(this.Quote(c))
		b.WriteString(" = ")
		if this.RowAlias {
			b.WriteString("new.")
			b.WriteString(this.Quote(c))
		} else {
			b.WriteString("VALUES(")
			b.WriteString(this.Quote(c))
			b.WriteString(")")
		}
	}
	return b.String()
}

type SQLite struct{}

func (SQLite) Name() string            { return "sqlite" }
func (SQLite) Placeholder(int) string  { return "?" }
func (SQLite) SupportsReturning() bool { return true }

func (SQLite) MaxBindValues() int { return PortableBindLimit }

func (SQLite) LockClause() string       { return "" }
func (SQLite) LikeEscapeClause() string { return ` ESCAPE '\'` }

func (SQLite) LimitAll() string { return " LIMIT -1" }

func (SQLite) UpsertSwallowsPrimaryKeyOnly() bool { return true }

func (SQLite) RollsBackStatementOnly() bool { return true }
func (this SQLite) Quote(ident string) string {
	return `"` + strings.ReplaceAll(ident, `"`, `""`) + `"`
}
func (this SQLite) Upsert(pk string, cols []string) string { return Postgres{}.Upsert(pk, cols) }

var (
	_ Dialect               = Postgres{}
	_ Dialect               = MySQL{}
	_ Dialect               = SQLite{}
	_ BindBudget            = Postgres{}
	_ BindBudget            = MySQL{}
	_ BindBudget            = SQLite{}
	_ DefaultValuesInserter = MySQL{}
	_ UpsertScope           = Postgres{}
	_ UpsertScope           = SQLite{}
	_ StatementRollback     = MySQL{}
	_ StatementRollback     = SQLite{}
)
