package crud_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/shardit-io/go-rx-crud/crud"
)

// mustRender renders a predicate on its own, with no surrounding statement, so
// what the test asserts is exactly what the node produces.
func mustRender(t *testing.T, d crud.Dialect, m *crud.Meta, p crud.Predicate) (string, []any) {
	t.Helper()
	sql, args, err := crud.NewSQL(d, m).Predicate(p).Done()
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	return sql, args
}

func checkRender(t *testing.T, d crud.Dialect, m *crud.Meta, p crud.Predicate, wantSQL string, wantArgs []any) {
	t.Helper()
	sql, args := mustRender(t, d, m, p)
	if sql != wantSQL {
		t.Errorf("sql  = %s\nwant = %s", sql, wantSQL)
	}
	if !reflect.DeepEqual(args, wantArgs) {
		t.Errorf("args = %#v, want %#v", args, wantArgs)
	}
}

func TestPredicateConstructors(t *testing.T) {
	m := articleMeta(t)

	for _, tc := range []struct {
		name string
		p    crud.Predicate
		sql  string
		args []any
	}{
		{"Eq", crud.Eq("Title", "Go"), `"title" = $1`, []any{"Go"}},
		{"Eq(nil) is IS NULL, not = NULL", crud.Eq("Title", nil), `"title" IS NULL`, nil},
		{"Eq of a nil pointer is IS NULL too", crud.Eq("Title", (*string)(nil)), `"title" IS NULL`, nil},
		{"Ne", crud.Ne("Title", "Go"), `"title" <> $1`, []any{"Go"}},
		{"Ne(nil) is IS NOT NULL", crud.Ne("Title", nil), `"title" IS NOT NULL`, nil},
		{"Gt", crud.Gt("Views", 10), `"views" > $1`, []any{10}},
		{"Gte", crud.Gte("Views", 10), `"views" >= $1`, []any{10}},
		{"Lt", crud.Lt("Views", 10), `"views" < $1`, []any{10}},
		{"Lte", crud.Lte("Views", 10), `"views" <= $1`, []any{10}},
		{"IsNull", crud.IsNull("Title"), `"title" IS NULL`, nil},
		{"IsNotNull", crud.IsNotNull("Title"), `"title" IS NOT NULL`, nil},

		{"Like takes the pattern verbatim", crud.Like("Title", "Go%"), `"title" LIKE $1`, []any{"Go%"}},
		{"NotLike", crud.NotLike("Title", "Go%"), `"title" NOT LIKE $1`, []any{"Go%"}},
		{"LikeIgnoreCase folds both sides", crud.LikeIgnoreCase("Title", "Go%"),
			`LOWER("title") LIKE LOWER($1)`, []any{"Go%"}},

		// Contains and friends build the pattern, so the wildcards a user typed
		// have to be escaped or they widen the search.
		{"Contains", crud.Contains("Title", "go"), `"title" LIKE $1`, []any{"%go%"}},
		{"Contains escapes wildcards", crud.Contains("Title", "50%_off"), `"title" LIKE $1`, []any{`%50\%\_off%`}},
		{"StartsWith", crud.StartsWith("Title", "go"), `"title" LIKE $1`, []any{"go%"}},
		{"EndsWith", crud.EndsWith("Title", "go"), `"title" LIKE $1`, []any{"%go"}},

		{"Between", crud.Between("Views", 1, 10), `"views" BETWEEN $1 AND $2`, []any{1, 10}},
		{"In", crud.In("Views", 1, 2), `"views" IN ($1, $2)`, []any{1, 2}},
		{"NotIn", crud.NotIn("Views", 1, 2), `"views" NOT IN ($1, $2)`, []any{1, 2}},
		{"InAny takes a typed slice", crud.InAny("Views", []int{1, 2}), `"views" IN ($1, $2)`, []any{1, 2}},
		{"NotInAny", crud.NotInAny("Views", []int{1}), `"views" NOT IN ($1)`, []any{1}},
		// IN () is a syntax error everywhere, and the honest answer to "in
		// nothing" is false — never "match everything".
		{"In of nothing matches nothing", crud.In("Views"), `1 = 0`, nil},
		{"NotIn of nothing matches everything", crud.NotIn("Views"), `1 = 1`, nil},

		{"EqField compares two columns", crud.EqField("AuthorID", "ID"), `"author_id" = "id"`, nil},

		{"And", crud.And(crud.Eq("Title", "Go"), crud.Gt("Views", 10)),
			`("title" = $1 AND "views" > $2)`, []any{"Go", 10}},
		{"Or", crud.Or(crud.Eq("Title", "Go"), crud.Gt("Views", 10)),
			`("title" = $1 OR "views" > $2)`, []any{"Go", 10}},
		{"Not", crud.Not(crud.Eq("Title", "Go")), `NOT ("title" = $1)`, []any{"Go"}},
		{"True", crud.True(), `1 = 1`, nil},
		{"False", crud.False(), `1 = 0`, nil},

		{"Raw rewrites ? into the dialect's marker", crud.Raw(`"views" > ?`, 10), `"views" > $1`, []any{10}},
		{"Raw escapes a literal question mark as ??", crud.Raw(`"title" <> ?? AND "views" > ?`, 10),
			`"title" <> ? AND "views" > $1`, []any{10}},
		{"Raw without markers is passed straight through", crud.Raw(`"views" IS NOT NULL`), `"views" IS NOT NULL`, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			checkRender(t, crud.Postgres{}, m, tc.p, tc.sql, tc.args)
		})
	}
}

// The same tree renders against whichever dialect the statement is being built
// for; nothing above the writer knows about placeholders or quoting.
func TestPredicatesFollowTheDialect(t *testing.T) {
	m := articleMeta(t)
	p := crud.And(crud.Eq("Title", "Go"), crud.In("Views", 1, 2), crud.Contains("Title", "x"))

	checkRender(t, crud.Postgres{}, m, p,
		`("title" = $1 AND "views" IN ($2, $3) AND "title" LIKE $4)`, []any{"Go", 1, 2, "%x%"})
	checkRender(t, crud.MySQL{}, m, p,
		"(`title` = ? AND `views` IN (?, ?) AND `title` LIKE ?)", []any{"Go", 1, 2, "%x%"})
	checkRender(t, crud.SQLite{}, m, p,
		`("title" = ? AND "views" IN (?, ?) AND "title" LIKE ?)`, []any{"Go", 1, 2, "%x%"})
}

// A chain of .And() calls is one flat clause, not a staircase of parentheses.
// AND and OR are associative, so this only ever affects readability — but the
// SQL is what a human debugs.
func TestLogicFlattening(t *testing.T) {
	m := articleMeta(t)
	a, b, c := crud.Eq("Title", "a"), crud.Eq("Title", "b"), crud.Eq("Title", "c")

	for _, tc := range []struct {
		name string
		p    crud.Predicate
		sql  string
	}{
		{"a nested AND is inlined", crud.And(a, crud.And(b, c)),
			`("title" = $1 AND "title" = $2 AND "title" = $3)`},
		{"however deep the nesting goes", crud.And(crud.And(crud.And(a, b), c), a),
			`("title" = $1 AND "title" = $2 AND "title" = $3 AND "title" = $4)`},
		{"an OR inside an AND keeps its own parentheses", crud.And(a, crud.Or(b, c)),
			`("title" = $1 AND ("title" = $2 OR "title" = $3))`},
		{"a single operand needs no parentheses at all", crud.And(a), `"title" = $1`},
		{"nil operands are dropped", crud.And(nil, a, nil), `"title" = $1`},
		{"AND of nothing is true", crud.And(), `1 = 1`},
		{"OR of nothing is false", crud.Or(), `1 = 0`},
		{"an empty nested AND disappears rather than rendering ()", crud.And(crud.And()), `1 = 1`},
		{"NOT of nothing matches nothing", crud.Not(nil), `1 = 0`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sql, _ := mustRender(t, crud.Postgres{}, m, tc.p)
			if sql != tc.sql {
				t.Fatalf("sql  = %s\nwant = %s", sql, tc.sql)
			}
		})
	}
}

// A relation hop is a correlated EXISTS, never a join: a join against a
// to-many relation multiplies the driving rows, which would corrupt both LIMIT
// and COUNT. EXISTS is a semi-join — one row in, one row out.
func TestRelationHopsRenderAsCorrelatedExists(t *testing.T) {
	articles := articleMeta(t)

	for _, tc := range []struct {
		name string
		m    *crud.Meta
		p    crud.Predicate
		sql  string
		args []any
	}{
		{"belongs_to correlates on the local foreign key", articles, crud.Eq("Author.Name", "Ann"),
			`EXISTS (SELECT 1 FROM "authors" AS rx1 WHERE rx1."id" = "articles"."author_id" AND rx1."name" = $1)`,
			[]any{"Ann"}},

		{"has_many correlates on the remote foreign key", articles, crud.Eq("Comments.Approved", true),
			`EXISTS (SELECT 1 FROM "comments" AS rx1 WHERE rx1."article_id" = "articles"."id" AND rx1."approved" = $1)`,
			[]any{true}},

		{"many_to_many joins the join table inside the subquery", articles, crud.In("Tags.Slug", "go", "rust"),
			`EXISTS (SELECT 1 FROM "tags" AS rx1 JOIN "article_tags" AS rx2 ON rx2."tag_id" = rx1."id" ` +
				`WHERE rx2."article_id" = "articles"."id" AND rx1."slug" IN ($1, $2))`,
			[]any{"go", "rust"}},

		{"two hops nest one EXISTS inside the other", articles, crud.Contains("Comments.Author.Name", "an"),
			`EXISTS (SELECT 1 FROM "comments" AS rx1 WHERE rx1."article_id" = "articles"."id" ` +
				`AND EXISTS (SELECT 1 FROM "authors" AS rx2 WHERE rx2."id" = rx1."author_id" AND rx2."name" LIKE $1))`,
			[]any{"%an%"}},

		{"a hop carries any predicate, not just equality", articles, crud.IsNull("Author.City"),
			`EXISTS (SELECT 1 FROM "authors" AS rx1 WHERE rx1."id" = "articles"."author_id" AND rx1."city" IS NULL)`,
			nil},

		{"NOT EXISTS is how 'has no matching child' is expressed", articles, crud.Not(crud.Eq("Comments.Approved", true)),
			`NOT (EXISTS (SELECT 1 FROM "comments" AS rx1 WHERE rx1."article_id" = "articles"."id" ` +
				`AND rx1."approved" = $1))`,
			[]any{true}},

		{"a relation on a natural key correlates on that key", metaOf[Warehouse](t, "warehouses"),
			crud.Gt("Crates.Weight", 10),
			`EXISTS (SELECT 1 FROM "crates" AS rx1 WHERE rx1."warehouse_code" = "warehouses"."code" ` +
				`AND rx1."weight" > $1)`,
			[]any{10}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			checkRender(t, crud.Postgres{}, tc.m, tc.p, tc.sql, tc.args)
		})
	}
}

// Each hop gets its own alias within a statement, so two subqueries over
// different relations cannot shadow one another's correlation.
func TestEveryHopGetsItsOwnAlias(t *testing.T) {
	checkRender(t, crud.Postgres{}, articleMeta(t),
		crud.And(crud.Eq("Author.Name", "Ann"), crud.Eq("Comments.Approved", true)),
		`(EXISTS (SELECT 1 FROM "authors" AS rx1 WHERE rx1."id" = "articles"."author_id" AND rx1."name" = $1)`+
			` AND EXISTS (SELECT 1 FROM "comments" AS rx2 WHERE rx2."article_id" = "articles"."id" AND rx2."approved" = $2))`,
		[]any{"Ann", true})
}

func TestRelationHopFollowsTheDialect(t *testing.T) {
	checkRender(t, crud.MySQL{}, articleMeta(t), crud.Eq("Author.Name", "Ann"),
		"EXISTS (SELECT 1 FROM `authors` AS rx1 WHERE rx1.`id` = `articles`.`author_id` AND rx1.`name` = ?)",
		[]any{"Ann"})
}

// A path that stops on a relation names no column to compare, so it is refused
// rather than guessed at.
func TestPredicateOnARelationPathIsRefused(t *testing.T) {
	_, _, err := crud.NewSQL(crud.Postgres{}, articleMeta(t)).Predicate(crud.Eq("Comments", 1)).Done()
	var schemaErr *crud.SchemaError
	if !errors.As(err, &schemaErr) {
		t.Fatalf("err = %v, want a SchemaError about the path naming a relation", err)
	}
}

// ---------------------------------------------------------------------------
// sorting

func orderSQL(t *testing.T, d crud.Dialect, m *crud.Meta, orders ...crud.Order) string {
	t.Helper()
	sql, _, err := crud.NewSQL(d, m).OrderBy(orders).Done()
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	return sql
}

func TestOrderBy(t *testing.T) {
	m := articleMeta(t)

	for _, tc := range []struct {
		name   string
		orders []crud.Order
		want   string
	}{
		{"ascending is spelled out", []crud.Order{crud.Asc("Views")}, ` ORDER BY "views" ASC`},
		{"descending", []crud.Order{crud.Desc("Views")}, ` ORDER BY "views" DESC`},
		{"terms keep the order they were given",
			[]crud.Order{crud.Desc("Views"), crud.Asc("Title"), crud.Asc("ID")},
			` ORDER BY "views" DESC, "title" ASC, "id" ASC`},
		{"a forgiving field spelling still resolves", []crud.Order{crud.Asc("authorId")}, ` ORDER BY "author_id" ASC`},
		{"nulls last", []crud.Order{crud.Asc("Title").WithNullsLast()}, ` ORDER BY "title" ASC NULLS LAST`},
		{"nulls first", []crud.Order{crud.Desc("Title").WithNullsFirst()}, ` ORDER BY "title" DESC NULLS FIRST`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := orderSQL(t, crud.Postgres{}, m, tc.orders...); got != tc.want {
				t.Fatalf("sql  = %s\nwant = %s", got, tc.want)
			}
		})
	}
}

// MySQL has no NULLS LAST; asking for it is silently dropped rather than
// rendered into a syntax error.
func TestNullsOrderingIsPostgresOnly(t *testing.T) {
	m := articleMeta(t)
	if got := orderSQL(t, crud.MySQL{}, m, crud.Asc("Title").WithNullsLast()); got != " ORDER BY `title` ASC" {
		t.Fatalf("sql = %s, want the NULLS clause dropped on MySQL", got)
	}
}

// Sorting through a to-one relation is a scalar subquery — the only shape
// ORDER BY accepts.
func TestNestedSortIsAScalarSubquery(t *testing.T) {
	for _, tc := range []struct {
		name  string
		m     *crud.Meta
		order crud.Order
		want  string
	}{
		{"one hop", articleMeta(t), crud.Desc("Author.Name"),
			` ORDER BY (SELECT rx1."name" FROM "authors" AS rx1 WHERE rx1."id" = "articles"."author_id" LIMIT 1) DESC`},
		{"a forgiving path spelling", articleMeta(t), crud.Asc("author.name"),
			` ORDER BY (SELECT rx1."name" FROM "authors" AS rx1 WHERE rx1."id" = "articles"."author_id" LIMIT 1) ASC`},
		{"two hops nest a subquery in the projection", metaOf[Person](t, "persons"), crud.Asc("Manager.Manager.Name"),
			` ORDER BY (SELECT (SELECT rx2."name" FROM "persons" AS rx2 WHERE rx2."id" = rx1."manager_no" LIMIT 1)` +
				` FROM "persons" AS rx1 WHERE rx1."id" = "persons"."manager_no" LIMIT 1) ASC`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := orderSQL(t, crud.Postgres{}, tc.m, tc.order); got != tc.want {
				t.Fatalf("sql  = %s\nwant = %s", got, tc.want)
			}
		})
	}
}

// "Order articles by their comments' bodies" has no single value to sort on.
// Picking one quietly would make the page order depend on the storage engine.
func TestSortThroughAToManyRelationIsRefused(t *testing.T) {
	for _, path := range []string{"Comments.Body", "Tags.Slug"} {
		_, _, err := crud.NewSQL(crud.Postgres{}, articleMeta(t)).OrderBy([]crud.Order{crud.Asc(path)}).Done()
		var schemaErr *crud.SchemaError
		if !errors.As(err, &schemaErr) {
			t.Fatalf("sorting by %s: err = %v, want a SchemaError", path, err)
		}
		if !strings.Contains(schemaErr.Reason, "cannot sort through") {
			t.Fatalf("sorting by %s: reason = %q, want it to explain the refusal", path, schemaErr.Reason)
		}
	}
}

func TestSortOnAnUnknownFieldIsReported(t *testing.T) {
	for _, path := range []string{"Nope", "Author.Nope", "Nope.Name"} {
		_, _, err := crud.NewSQL(crud.Postgres{}, articleMeta(t)).OrderBy([]crud.Order{crud.Asc(path)}).Done()
		var unknown *crud.UnknownFieldError
		if !errors.As(err, &unknown) {
			t.Fatalf("sorting by %s: err = %v, want an UnknownFieldError", path, err)
		}
	}
}

// A narrowing declared by path has to reach the hop it names, however deep.
//
// It did not. The path was extended after the recursion rather than before it,
// so the second hop looked itself up as "Manager" instead of "Manager.Manager" —
// a narrowing declared for the inner hop silently did not apply, and one
// declared for the outer hop applied twice. Model-declared narrowings still
// worked, which is what kept this out of sight: basic.Scope installs one of
// those, so a self-relation stayed covered while a path declaration did not.
func TestARelationScopeReachesTheHopItNames(t *testing.T) {
	m := metaOf[Person](t, "persons")

	for _, tc := range []struct {
		name   string
		scopes *crud.RelationScopes
		want   string // the narrowing, and which subquery it must land in
	}{
		{
			"the inner hop",
			(*crud.RelationScopes)(nil).AtPath("Manager.Manager", crud.Eq("Name", "root")),
			`rx2."name" = $1 LIMIT 1)`,
		},
		{
			"the outer hop",
			(*crud.RelationScopes)(nil).AtPath("Manager", crud.Eq("Name", "boss")),
			`rx1."name" = $1 LIMIT 1)`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sql, args, err := crud.NewSQL(crud.Postgres{}, m).
				RelationScopes(tc.scopes).
				OrderBy([]crud.Order{crud.Asc("Manager.Manager.Name")}).
				Done()
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(sql, tc.want) {
				t.Fatalf("the narrowing did not land on the hop that declared it:\n%s\nwant to contain: %s",
					sql, tc.want)
			}
			if len(args) != 1 {
				t.Fatalf("args = %v, want the narrowing's one bind", args)
			}
		})
	}
}

// The control: with nothing declared, neither subquery carries a narrowing — so
// the assertions above are measuring the declaration and not something the
// writer does anyway.
func TestATwoHopSortCarriesNoNarrowingWhenNoneIsDeclared(t *testing.T) {
	m := metaOf[Person](t, "persons")
	sql, args, err := crud.NewSQL(crud.Postgres{}, m).
		OrderBy([]crud.Order{crud.Asc("Manager.Manager.Name")}).
		Done()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(sql, "$1") || len(args) != 0 {
		t.Fatalf("an undeclared narrowing appeared:\n%s\nargs = %v", sql, args)
	}
}
