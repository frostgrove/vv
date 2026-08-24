package query_test

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/shardit-io/vv/crud"
	"github.com/shardit-io/vv/crud/crudtest"
	"github.com/shardit-io/vv/query"
	"github.com/shardit-io/vv/repo/basic"
)

type Author struct {
	ID   int64  `db:"id,pk,auto"`
	Name string `db:"name"`
}

type Tag struct {
	ID   int64  `db:"id,pk,auto"`
	Slug string `db:"slug"`
}

type Comment struct {
	ID        int64   `db:"id,pk,auto"`
	ArticleID int64   `db:"article_id"`
	AuthorID  int64   `db:"author_id"`
	Body      string  `db:"body"`
	Approved  bool    `db:"approved"`
	Author    *Author `rel:"belongs_to"`
}

type Article struct {
	ID          int64               `db:"id,pk,auto"`
	AuthorID    int64               `db:"author_id"`
	Title       string              `db:"title"`
	Body        string              `db:"body"`
	Views       int                 `db:"views"`
	PublishedAt crud.Opt[time.Time] `db:"published_at"`
	CreatedAt   time.Time           `db:"created_at"`

	Author   *Author   `rel:"belongs_to"`
	Comments []Comment `rel:"has_many"`
	Tags     []Tag     `rel:"many_to_many,join=article_tags"`
}

type ArticleUpdate struct {
	Title *string
	Views *int
}

var (
	Articles = basic.Define[Article, int64, ArticleUpdate]("articles")
	_        = basic.Define[Author, int64, struct{}]("authors")
	_        = basic.Define[Comment, int64, struct{}]("comments")
	_        = basic.Define[Tag, int64, struct{}]("tags")
)

const cols = `"id", "author_id", "title", "body", "views", "published_at", "created_at"`

// run compiles a JSON query document and returns the SQL the repository built.
func run(t *testing.T, doc string, cfg *query.Config) (string, []any) {
	t.Helper()
	var req query.Request
	if err := json.Unmarshal([]byte(doc), &req); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return runReq(t, &req, cfg)
}

func runReq(t *testing.T, req *query.Request, cfg *query.Config) (string, []any) {
	t.Helper()
	rec := crudtest.Postgres().Push(crudtest.Rows(), crudtest.Rows([]any{int64(0)}))
	opts, err := req.Compile(Articles.Meta(), cfg)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if _, err := Articles.Bind(rec).Get(context.Background(), opts...); err != nil {
		t.Fatalf("query: %v", err)
	}
	st := rec.Statements()[0]
	return crudtest.Normalize(st.SQL), st.Args
}

func mustFail(t *testing.T, doc string, cfg *query.Config, want string) {
	t.Helper()
	var req query.Request
	if err := json.Unmarshal([]byte(doc), &req); err != nil {
		t.Fatalf("decode: %v", err)
	}
	_, err := req.Compile(Articles.Meta(), cfg)
	if err == nil {
		t.Fatalf("expected a rejection mentioning %q", want)
	}
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("error = %q, want it to mention %q", err, want)
	}
}

func where(sql string) string {
	_, clause, _ := strings.Cut(sql, " WHERE ")
	for _, tail := range []string{" ORDER BY ", " LIMIT ", " OFFSET "} {
		clause, _, _ = strings.Cut(clause, tail)
	}
	return clause
}

func TestFlatFilters(t *testing.T) {
	for _, tc := range []struct {
		name string
		doc  string
		want string
		args []any
	}{
		{"shorthand equality", `{"filter":{"title":"Go"}}`, `"title" = $1`, []any{"Go"}},
		{"explicit operators", `{"filter":{"views":{"gte":100,"lt":1000}}}`,
			`("views" >= $1 AND "views" < $2)`, []any{100, 1000}},
		{"array shorthand is IN", `{"filter":{"title":["a","b"]}}`,
			`"title" IN ($1, $2)`, []any{"a", "b"}},
		{"null shorthand", `{"filter":{"publishedAt":null}}`, `"published_at" IS NULL`, nil},
		{"isNull operator", `{"filter":{"publishedAt":{"isNull":true}}}`, `"published_at" IS NULL`, nil},
		{"isNull false flips", `{"filter":{"publishedAt":{"isNull":false}}}`, `"published_at" IS NOT NULL`, nil},
		{"contains", `{"filter":{"title":{"contains":"go"}}}`, `"title" LIKE $1`, []any{"%go%"}},
		{"between", `{"filter":{"views":{"between":[1,10]}}}`, `"views" BETWEEN $1 AND $2`, []any{1, 10}},
		{"snake case path", `{"filter":{"published_at":{"isNotNull":true}}}`, `"published_at" IS NOT NULL`, nil},
		{"column name path", `{"filter":{"author_id":7}}`, `"author_id" = $1`, []any{int64(7)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sql, args := run(t, tc.doc, nil)
			if got := where(sql); got != tc.want {
				t.Fatalf("where = %s, want %s", got, tc.want)
			}
			if tc.args != nil {
				if len(args) != len(tc.args) {
					t.Fatalf("args = %#v, want %#v", args, tc.args)
				}
				for i := range args {
					if args[i] != tc.args[i] {
						t.Fatalf("arg %d = %#v (%T), want %#v (%T)", i, args[i], args[i], tc.args[i], tc.args[i])
					}
				}
			}
		})
	}
}

// Values are decoded into the column's Go type, not left as float64 — that is
// what keeps an int column comparing against an int.
func TestValuesAreTypedByColumn(t *testing.T) {
	_, args := run(t, `{"filter":{"views":{"gte":100}}}`, nil)
	if _, ok := args[0].(int); !ok {
		t.Fatalf("views bound as %T, want int", args[0])
	}
	_, args = run(t, `{"filter":{"authorId":7}}`, nil)
	if _, ok := args[0].(int64); !ok {
		t.Fatalf("authorId bound as %T, want int64", args[0])
	}
	_, args = run(t, `{"filter":{"createdAt":{"gte":"2026-01-02T03:04:05Z"}}}`, nil)
	got, ok := args[0].(time.Time)
	if !ok {
		t.Fatalf("createdAt bound as %T, want time.Time", args[0])
	}
	if !got.Equal(time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)) {
		t.Fatalf("createdAt = %v", got)
	}
	// A date-only string is accepted too.
	_, args = run(t, `{"filter":{"createdAt":{"gte":"2026-01-02"}}}`, nil)
	if _, ok := args[0].(time.Time); !ok {
		t.Fatalf("date-only bound as %T", args[0])
	}
}

func TestLogicalComposition(t *testing.T) {
	sql, _ := run(t, `{"filter":{
		"or": [
			{"title": "a"},
			{"and": [{"views": {"gt": 10}}, {"publishedAt": {"isNotNull": true}}]}
		],
		"not": {"title": "spam"}
	}}`, nil)
	want := `(NOT ("title" = $1) AND ("title" = $2 OR ("views" > $3 AND "published_at" IS NOT NULL)))`
	if got := where(sql); got != want {
		t.Fatalf("where = %s\nwant  = %s", got, want)
	}
}

// Nested paths become correlated EXISTS subqueries rather than joins, so a
// to-many filter cannot multiply the result set or corrupt the count.
func TestNestedFilters(t *testing.T) {
	for _, tc := range []struct {
		name string
		doc  string
		want string
	}{
		{"belongs to", `{"filter":{"author.name":"Ann"}}`,
			`EXISTS (SELECT 1 FROM "authors" AS rx1 WHERE rx1."id" = "articles"."author_id" AND rx1."name" = $1)`},
		{"has many", `{"filter":{"comments.body":{"contains":"nice"}}}`,
			`EXISTS (SELECT 1 FROM "comments" AS rx1 WHERE rx1."article_id" = "articles"."id" AND rx1."body" LIKE $1)`},
		{"many to many", `{"filter":{"tags.slug":{"in":["go","rust"]}}}`,
			`EXISTS (SELECT 1 FROM "tags" AS rx1 JOIN "article_tags" AS rx2 ON rx2."tag_id" = rx1."id" ` +
				`WHERE rx2."article_id" = "articles"."id" AND rx1."slug" IN ($1, $2))`},
		{"two hops", `{"filter":{"comments.author.name":"Bob"}}`,
			`EXISTS (SELECT 1 FROM "comments" AS rx1 WHERE rx1."article_id" = "articles"."id" AND ` +
				`EXISTS (SELECT 1 FROM "authors" AS rx2 WHERE rx2."id" = rx1."author_id" AND rx2."name" = $1))`},
		{"negated to-many means none match", `{"filter":{"not":{"comments.approved":false}}}`,
			`NOT (EXISTS (SELECT 1 FROM "comments" AS rx1 WHERE rx1."article_id" = "articles"."id" AND rx1."approved" = $1))`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sql, _ := run(t, tc.doc, nil)
			if got := where(sql); got != tc.want {
				t.Fatalf("where = %s\nwant  = %s", got, tc.want)
			}
		})
	}
}

// Sorting through a to-one relation is a scalar subquery; through a to-many it
// has no single value and is refused.
func TestNestedSort(t *testing.T) {
	sql, _ := run(t, `{"sort":["-author.name","title"],"limit":10}`, nil)
	want := `ORDER BY (SELECT rx1."name" FROM "authors" AS rx1 WHERE rx1."id" = "articles"."author_id" LIMIT 1) DESC, ` +
		`"title" ASC, "id" ASC`
	if !strings.Contains(sql, want) {
		t.Fatalf("sql = %s\nwant it to contain %s", sql, want)
	}

	var req query.Request
	_ = json.Unmarshal([]byte(`{"sort":["comments.body"],"limit":5}`), &req)
	opts, err := req.Compile(Articles.Meta(), nil)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	rec := crudtest.Postgres().Push(crudtest.Rows())
	if _, err := Articles.Bind(rec).Get(context.Background(), opts...); err == nil {
		t.Fatal("sorting through a has_many should be refused")
	} else if !strings.Contains(err.Error(), "has_many") {
		t.Fatalf("err = %v", err)
	}
}

func TestSearchIsParenthesised(t *testing.T) {
	// The classic trap: an OR of search terms must not escape the surrounding
	// AND. The AST parenthesises it, so it cannot.
	sql, args := run(t, `{"filter":{"views":{"gt":5}},"search":"go","searchFields":["title","body"]}`, nil)
	want := `("views" > $1 AND ("title" LIKE $2 OR "body" LIKE $3))`
	if got := where(sql); got != want {
		t.Fatalf("where = %s\nwant  = %s", got, want)
	}
	if args[1] != "%go%" || args[2] != "%go%" {
		t.Fatalf("args = %v", args)
	}

	// With no field list, every text column joins the OR.
	sql, _ = run(t, `{"search":"go"}`, nil)
	if got := where(sql); got != `("title" LIKE $1 OR "body" LIKE $2)` {
		t.Fatalf("where = %s", got)
	}
}

func TestPagingAndProjection(t *testing.T) {
	sql, _ := run(t, `{"page":3,"limit":5,"select":["title","views"]}`, nil)
	if !strings.HasPrefix(sql, `SELECT "id", "title", "views" FROM "articles"`) {
		t.Fatalf("sql = %s", sql)
	}
	if !strings.Contains(sql, "LIMIT 5 OFFSET 10") {
		t.Fatalf("sql = %s", sql)
	}
}

func TestQueryStringForm(t *testing.T) {
	v, err := url.ParseQuery(
		"page=2&limit=5&sort=-views,title&select=title&search=go&searchFields=title" +
			"&f=views:gte:100&f=tags.slug:in:go,rust&f=publishedAt:isNull:true")
	if err != nil {
		t.Fatal(err)
	}
	req, err := query.ParseQuery(v)
	if err != nil {
		t.Fatal(err)
	}
	sql, args := runReq(t, req, nil)

	want := `("views" >= $1 AND EXISTS (SELECT 1 FROM "tags" AS rx1 JOIN "article_tags" AS rx2 ` +
		`ON rx2."tag_id" = rx1."id" WHERE rx2."article_id" = "articles"."id" AND rx1."slug" IN ($2, $3)) ` +
		`AND "published_at" IS NULL AND "title" LIKE $4)`
	if got := where(sql); got != want {
		t.Fatalf("where = %s\nwant  = %s", got, want)
	}
	// The text value became an int, because the column is one.
	if _, ok := args[0].(int); !ok {
		t.Fatalf("views bound as %T, want int", args[0])
	}
	if !strings.Contains(sql, "LIMIT 5 OFFSET 5") {
		t.Fatalf("sql = %s", sql)
	}
}

// A timestamp keeps its colons: only the first two separators are structural.
func TestQueryStringTermKeepsColons(t *testing.T) {
	term, err := query.ParseTerm("createdAt:gte:2026-01-02T03:04:05Z")
	if err != nil {
		t.Fatal(err)
	}
	if term.Path != "createdAt" || term.Op != "gte" || term.Values[0] != "2026-01-02T03:04:05Z" {
		t.Fatalf("term = %+v", term)
	}
}

func TestRejections(t *testing.T) {
	for _, tc := range []struct{ name, doc, want string }{
		{"unknown field", `{"filter":{"nope":1}}`, "unknown field"},
		{"unknown nested field", `{"filter":{"author.nope":1}}`, "unknown field"},
		{"unknown relation", `{"filter":{"nope.name":1}}`, "unknown field"},
		{"unknown operator", `{"filter":{"title":{"wat":1}}}`, "unknown operator"},
		{"bad value type", `{"filter":{"views":"lots"}}`, "expects int"},
		{"unknown sort", `{"sort":["nope"]}`, "unknown field"},
		{"unknown preload", `{"preload":["nope"]}`, "unknown field"},
		{"preload of a column", `{"preload":["title"]}`, "not a relation"},
		{"select across a relation", `{"select":["author.name"]}`, "use preload"},
		{"between needs two", `{"filter":{"views":{"between":[1]}}}`, "exactly two"},
	} {
		t.Run(tc.name, func(t *testing.T) { mustFail(t, tc.doc, nil, tc.want) })
	}
}

// An unknown path is a rejection, never a silently dropped clause — the
// difference between a typo you notice and a filter that quietly does nothing.
func TestUnknownFieldNeverReachesTheDatabase(t *testing.T) {
	rec := crudtest.Postgres()
	var req query.Request
	_ = json.Unmarshal([]byte(`{"filter":{"nope":1}}`), &req)
	if _, err := req.Compile(Articles.Meta(), nil); err == nil {
		t.Fatal("expected a rejection")
	}
	if len(rec.Statements()) != 0 {
		t.Fatal("nothing should have run")
	}
}

func TestAllowLists(t *testing.T) {
	cfg := &query.Config{
		Filterable:  []string{"Title", "Comments.*"},
		Sortable:    []string{"Title"},
		Preloadable: []string{"Author"},
		MaxDepth:    3,
	}
	if _, _ = run(t, `{"filter":{"title":"a"}}`, cfg); true {
	}
	mustFail(t, `{"filter":{"views":1}}`, cfg, "not filterable")
	mustFail(t, `{"sort":["views"]}`, cfg, "not sortable")
	mustFail(t, `{"preload":["comments"]}`, cfg, "cannot be preloaded")

	// The subtree wildcard lets nested paths through.
	if sql, _ := run(t, `{"filter":{"comments.body":"x"}}`, cfg); !strings.Contains(sql, "comments") {
		t.Fatalf("sql = %s", sql)
	}
}

func TestLimitsAreEnforced(t *testing.T) {
	mustFail(t, `{"filter":{"or":[{"or":[{"or":[{"or":[{"or":[{"title":"a"}]}]}]}]}]}}`,
		&query.Config{MaxDepth: 3}, "nested deeper")
	mustFail(t, `{"filter":{"views":{"gt":1,"lt":2}}}`,
		&query.Config{MaxConditions: 1}, "at most 1 conditions")
	mustFail(t, `{"preload":["author","comments"]}`,
		&query.Config{MaxPreloads: 1}, "at most 1 relations")
}

// The document is order-insensitive but the SQL is not: keys are sorted so the
// same request always produces the same statement.
func TestDeterministicOutput(t *testing.T) {
	doc := `{"filter":{"views":{"gte":1},"title":"a","author.name":"b"}}`
	first, _ := run(t, doc, nil)
	for range 8 {
		if got, _ := run(t, doc, nil); got != first {
			t.Fatalf("SQL is not stable:\n%s\n%s", first, got)
		}
	}
}
