package crud_test

import (
	"errors"
	"testing"

	"github.com/shardit-io/qq/crud"
)

// done asserts the whole statement at once: text, binds and the resolution
// error, because a builder that renders the right SQL with the wrong argument
// order is still broken.
func done(t *testing.T, b *crud.SQL, wantSQL string, wantArgs ...any) {
	t.Helper()
	sql, args, err := b.Done()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if sql != wantSQL {
		t.Fatalf("sql  = %s\nwant = %s", sql, wantSQL)
	}
	if len(args) != len(wantArgs) {
		t.Fatalf("args = %#v, want %#v", args, wantArgs)
	}
	for i := range args {
		if args[i] != wantArgs[i] {
			t.Fatalf("arg %d = %#v, want %#v", i, args[i], wantArgs[i])
		}
	}
}

// One statement using every piece of the builder, in the order a repository
// assembles it.
func TestSQLBuilderAssemblesAWholeStatement(t *testing.T) {
	m := articleMeta(t)
	b := crud.NewSQL(crud.Postgres{}, m)

	b.Raw("SELECT ").Columns(m.Fields).
		Raw(" FROM ").Table().
		Where(crud.Eq("Title", "Go")).
		Raw(" AND ").Column("Views").Raw(" > ").Bind(10).
		OrderBy([]crud.Order{crud.Desc("Views"), crud.Asc("ID")}).
		LimitOffset(20, 40)

	done(t, b,
		`SELECT "id", "author_id", "title", "views" FROM "articles" WHERE "title" = $1 AND "views" > $2 `+
			`ORDER BY "views" DESC, "id" ASC LIMIT 20 OFFSET 40`,
		"Go", 10)
}

func TestSQLBuilderPieces(t *testing.T) {
	m := articleMeta(t)

	for _, tc := range []struct {
		name string
		fn   func(*crud.SQL)
		sql  string
		args []any
	}{
		{"Raw is copied through untouched", func(b *crud.SQL) { b.Raw("SELECT count(*) FROM x") },
			"SELECT count(*) FROM x", nil},
		{"Ident quotes", func(b *crud.SQL) { b.Ident("weird col") }, `"weird col"`, nil},
		{"Table is the bound table", func(b *crud.SQL) { b.Table() }, `"articles"`, nil},
		{"Column resolves a Go field name", func(b *crud.SQL) { b.Column("AuthorID") }, `"author_id"`, nil},
		{"Column also takes a column name", func(b *crud.SQL) { b.Column("author_id") }, `"author_id"`, nil},
		{"Column is forgiving about spelling", func(b *crud.SQL) { b.Column("authorId") }, `"author_id"`, nil},
		{"Columns is comma separated", func(b *crud.SQL) { b.Columns(m.Fields[:2]) }, `"id", "author_id"`, nil},
		{"Columns of nothing renders nothing", func(b *crud.SQL) { b.Columns(nil) }, "", nil},
		{"Bind numbers per statement", func(b *crud.SQL) { b.Bind("a").Raw(" ").Bind("b") }, "$1 $2", []any{"a", "b"}},
		{"Binds spreads a whole list", func(b *crud.SQL) { b.Binds([]any{1, 2, 3}) }, "$1, $2, $3", []any{1, 2, 3}},
		{"Binds of nothing renders nothing", func(b *crud.SQL) { b.Binds(nil) }, "", nil},
		{"Where prefixes the keyword", func(b *crud.SQL) { b.Where(crud.Eq("Views", 1)) }, ` WHERE "views" = $1`, []any{1}},
		{"Where of nil is a no-op", func(b *crud.SQL) { b.Raw("x").Where(nil) }, "x", nil},
		{"Predicate omits the keyword", func(b *crud.SQL) { b.Predicate(crud.Eq("Views", 1)) }, `"views" = $1`, []any{1}},
		{"Predicate of nil is a no-op", func(b *crud.SQL) { b.Raw("x").Predicate(nil) }, "x", nil},
		{"OrderBy of nothing renders nothing", func(b *crud.SQL) { b.OrderBy(nil) }, "", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := crud.NewSQL(crud.Postgres{}, m)
			tc.fn(b)
			done(t, b, tc.sql, tc.args...)
		})
	}
}

// LIMIT and OFFSET are the one place the builder has to know a dialect quirk:
// neither MySQL nor SQLite will take an OFFSET without a LIMIT in front of it,
// and they spell "no limit" differently. `?unpaged=true&offset=5` is one wire
// request away, and SQLite used to answer it with `near "5": syntax error`.
func TestLimitOffset(t *testing.T) {
	for _, tc := range []struct {
		name           string
		limit          int
		offset         int
		pg, my, sqlite string
	}{
		{"neither", 0, 0, "", "", ""},
		{"limit only", 20, 0, " LIMIT 20", " LIMIT 20", " LIMIT 20"},
		{"limit and offset", 20, 40, " LIMIT 20 OFFSET 40", " LIMIT 20 OFFSET 40", " LIMIT 20 OFFSET 40"},
		{"offset only", 0, 40, " OFFSET 40",
			" LIMIT 18446744073709551615 OFFSET 40", " LIMIT -1 OFFSET 40"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pg := crud.NewSQL(crud.Postgres{}, articleMeta(t)).LimitOffset(tc.limit, tc.offset)
			done(t, pg, tc.pg)
			my := crud.NewSQL(crud.MySQL{}, articleMeta(t)).LimitOffset(tc.limit, tc.offset)
			done(t, my, tc.my)
			lite := crud.NewSQL(crud.SQLite{}, articleMeta(t)).LimitOffset(tc.limit, tc.offset)
			done(t, lite, tc.sqlite)
		})
	}
}

// The whole statement follows the dialect: markers and quoting both.
func TestBuilderFollowsTheDialect(t *testing.T) {
	m := articleMeta(t)
	b := crud.NewSQL(crud.MySQL{}, m).
		Raw("SELECT ").Columns(m.Fields[:2]).Raw(" FROM ").Table().
		Where(crud.And(crud.Eq("Title", "Go"), crud.Gt("Views", 10)))

	done(t, b, "SELECT `id`, `author_id` FROM `articles` WHERE (`title` = ? AND `views` > ?)", "Go", 10)
}

// An alias qualifies every column the statement renders, which is what lets a
// batched preload put two tables in one FROM.
func TestAliasQualifiesColumns(t *testing.T) {
	m := articleMeta(t)
	b := crud.NewSQL(crud.Postgres{}, m).Alias("a").
		Raw("SELECT ").Column("Title").Raw(" FROM ").Table().Raw(" AS a").
		Where(crud.Eq("Views", 1)).
		OrderBy([]crud.Order{crud.Asc("Title")})

	done(t, b, `SELECT a."title" FROM "articles" AS a WHERE a."views" = $1 ORDER BY a."title" ASC`, 1)
}

// A field that does not exist poisons the statement instead of producing SQL
// that a database would have to reject.
func TestUnknownFieldIsReportedNotRendered(t *testing.T) {
	b := crud.NewSQL(crud.Postgres{}, articleMeta(t)).
		Raw("SELECT ").Column("Nope").Raw(" FROM ").Table()

	_, _, err := b.Done()
	var unknown *crud.UnknownFieldError
	if !errors.As(err, &unknown) {
		t.Fatalf("err = %v, want an UnknownFieldError", err)
	}
	if unknown.Field != "Nope" || unknown.Model != "Article" {
		t.Fatalf("error names %s.%s, want Article.Nope", unknown.Model, unknown.Field)
	}
	if b.Err() == nil {
		t.Error("Err must keep reporting the failure after Done")
	}
}

// The first failure is the one reported: a later error must not overwrite the
// explanation of what actually went wrong.
func TestOnlyTheFirstFailureIsKept(t *testing.T) {
	b := crud.NewSQL(crud.Postgres{}, articleMeta(t)).Column("First").Raw(", ").Column("Second")

	var unknown *crud.UnknownFieldError
	if !errors.As(b.Err(), &unknown) || unknown.Field != "First" {
		t.Fatalf("err = %v, want the first unknown field", b.Err())
	}
}

func TestBuilderExposesItsDialect(t *testing.T) {
	if got := crud.NewSQL(crud.MySQL{}, articleMeta(t)).Dialect().Name(); got != "mysql" {
		t.Fatalf("Dialect().Name() = %q, want mysql", got)
	}
}

// A repository may read the statement as it stands without finishing it, and
// what it reads is what Done would hand back.
func TestStringAndArgsReadTheStatementWithoutFinishingIt(t *testing.T) {
	b := crud.NewSQL(crud.Postgres{}, articleMeta(t)).Raw("SELECT 1 WHERE ").Predicate(crud.Eq("Views", 3))

	if want := `SELECT 1 WHERE "views" = $1`; b.String() != want {
		t.Fatalf("String() = %q\nwant       %q", b.String(), want)
	}
	if args := b.Args(); len(args) != 1 || args[0] != 3 {
		t.Fatalf("Args() = %#v, want [3]", args)
	}

	sql, args, err := b.Done()
	if err != nil {
		t.Fatal(err)
	}
	if sql != b.String() || len(args) != 1 || args[0] != b.Args()[0] {
		t.Fatalf("Done = (%q, %#v) but the half-built statement read (%q, %#v)",
			sql, args, b.String(), b.Args())
	}
}
