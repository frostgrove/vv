package query_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/shardit-io/vv/crud/crudtest"
	"github.com/shardit-io/vv/crud/query"
)

// tryDoc compiles a JSON document and hands back the statement the repository
// would run, or the error the compiler raised. Unlike run() it never fails the
// test itself: these tests are about which of the two happens.
func tryDoc(t *testing.T, doc string, cfg *query.Config) (string, []any, error) {
	t.Helper()
	var req query.Request
	if err := json.Unmarshal([]byte(doc), &req); err != nil {
		return "", nil, err
	}
	return tryReq(t, &req, cfg)
}

func tryReq(t *testing.T, req *query.Request, cfg *query.Config) (string, []any, error) {
	t.Helper()
	opts, err := req.Compile(Articles.Meta(), cfg)
	if err != nil {
		return "", nil, err
	}
	rec := crudtest.Postgres().Push(crudtest.Rows(), crudtest.Rows([]any{int64(0)}))
	if _, err := Articles.Bind(rec).Get(context.Background(), opts...); err != nil {
		return "", nil, err
	}
	st := rec.Statements()[0]
	return crudtest.Normalize(st.SQL), st.Args, nil
}

// tryQuery is the same for the query-string door, including ParseQuery's own
// rejections.
func tryQuery(t *testing.T, qs string, cfg *query.Config) (string, []any, error) {
	t.Helper()
	v, err := url.ParseQuery(qs)
	if err != nil {
		t.Fatalf("the test's own query string %q is malformed: %v", qs, err)
	}
	req, err := query.ParseQuery(v)
	if err != nil {
		return "", nil, err
	}
	return tryReq(t, req, cfg)
}

// A document that is not a filter object is a rejection naming what arrived,
// not an empty WHERE clause that hands back the whole table.
func TestMalformedFilterDocumentsAreRefused(t *testing.T) {
	for _, tc := range []struct{ name, doc, want string }{
		{"an array", `{"filter":[{"title":"a"}]}`, `expected an object, got [{"title":"a"}]`},
		{"a bare string", `{"filter":"title"}`, `expected an object, got "title"`},
		{"a number", `{"filter":42}`, `expected an object, got 42`},
		{"a boolean", `{"filter":true}`, `expected an object, got true`},
		{"an or that is not a list", `{"filter":{"or":"title"}}`, `expected an array of filter objects, got "title"`},
		{"an array inside an or", `{"filter":{"or":[["title"]]}}`, `expected an object, got ["title"]`},
		{"an empty field name", `{"filter":{"":1}}`, "empty field path"},
		{"an operand that is an object", `{"filter":{"title":{"eq":{"nested":1}}}}`, "Title expects string"},
		{"a list operator given a scalar", `{"filter":{"views":{"in":5}}}`, "Views expects an array"},
		{"a scalar operator given a list", `{"filter":{"views":{"gte":[1]}}}`, "Views expects int"},
		{"between given three values", `{"filter":{"views":{"between":[1,2,3]}}}`, "exactly two values, got 3"},
		{"between given one", `{"filter":{"views":{"between":[1]}}}`, "exactly two values, got 1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sql, _, err := tryDoc(t, tc.doc, nil)
			if err == nil {
				t.Fatalf("%s was accepted and compiled to %s", tc.doc, sql)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %q, want it to say %q", err, tc.want)
			}
		})
	}
}

// The shapes that carry no constraint compile to no WHERE clause at all. The
// alternative — an empty AND or an empty operator object rendering as a
// constant — would either match everything or nothing depending on which, and
// the client could not tell which it got.
func TestDegenerateFilterShapesAddNoClause(t *testing.T) {
	for _, tc := range []struct{ name, doc string }{
		{"an empty document", `{"filter":{}}`},
		{"an operator object with no operators", `{"filter":{"title":{}}}`},
		{"an empty and", `{"filter":{"and":[]}}`},
		{"an empty or", `{"filter":{"or":[]}}`},
		{"an empty not", `{"filter":{"not":{}}}`},
		{"a null branch inside an or", `{"filter":{"or":[null]}}`},
		{"a null filter", `{"filter":null}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sql, args, err := tryDoc(t, tc.doc, nil)
			if err != nil {
				t.Fatalf("%s was refused: %v", tc.doc, err)
			}
			if strings.Contains(sql, "WHERE") {
				t.Fatalf("%s built %s, want no WHERE clause", tc.doc, sql)
			}
			if len(args) != 0 {
				t.Fatalf("%s bound %#v", tc.doc, args)
			}
		})
	}
}

// An empty value list is the one degenerate shape with a real answer: IN ()
// is a syntax error in every database, so it degrades to a constant that
// matches nothing — and NOT IN () to one that matches everything.
func TestAnEmptyValueListBecomesAConstant(t *testing.T) {
	for _, tc := range []struct{ name, doc, want string }{
		{"in", `{"filter":{"views":{"in":[]}}}`, "1 = 0"},
		{"the array shorthand", `{"filter":{"views":[]}}`, "1 = 0"},
		{"notIn", `{"filter":{"views":{"nin":[]}}}`, "1 = 1"},
		{"a term with no values", `{"terms":[{"path":"views","op":"in"}]}`, "1 = 0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sql, args, err := tryDoc(t, tc.doc, nil)
			if err != nil {
				t.Fatalf("%s was refused: %v", tc.doc, err)
			}
			if got := where(sql); got != tc.want {
				t.Fatalf("where = %s, want %s", got, tc.want)
			}
			if strings.Contains(sql, "IN ()") {
				t.Fatalf("sql = %s, which no database will parse", sql)
			}
			if len(args) != 0 {
				t.Fatalf("a constant bound %#v", args)
			}
		})
	}
}

// null is only an operand for a scalar comparison, where it means IS NULL.
// Handed to any other operator it used to pass straight through
// json.Unmarshal as that operator's zero value, so a filter the client asked
// for stopped narrowing anything.
func TestNullOperandsAreRefusedWhereTheyHaveNoMeaning(t *testing.T) {
	for _, tc := range []struct{ name, doc string }{
		{"contains", `{"filter":{"title":{"contains":null}}}`},
		{"like", `{"filter":{"title":{"like":null}}}`},
		{"startsWith", `{"filter":{"title":{"startswith":null}}}`},
		{"isNull", `{"filter":{"publishedAt":{"isNull":null}}}`},
		{"isNotNull", `{"filter":{"publishedAt":{"isNotNull":null}}}`},
		{"in", `{"filter":{"title":{"in":null}}}`},
		{"notIn", `{"filter":{"title":{"nin":null}}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sql, args, err := tryDoc(t, tc.doc, nil)
			if err == nil {
				t.Fatalf("a null operand compiled to %s %v, want a rejection", where(sql), args)
			}
			if !strings.Contains(err.Error(), "null") {
				t.Fatalf("error = %q, want it to name the null operand", err)
			}
		})
	}

	// The scalar door keeps its meaning: both spellings of "no value" are the
	// same IS NULL, so a client cannot pick the wrong one by accident.
	for _, doc := range []string{
		`{"filter":{"publishedAt":null}}`,
		`{"filter":{"publishedAt":{"eq":null}}}`,
	} {
		sql, args, err := tryDoc(t, doc, nil)
		if err != nil {
			t.Fatalf("%s was refused: %v", doc, err)
		}
		if got := where(sql); got != `"published_at" IS NULL` || len(args) != 0 {
			t.Fatalf("%s built %s %v, want \"published_at\" IS NULL with no arguments", doc, got, args)
		}
	}
	sql, _, err := tryDoc(t, `{"filter":{"publishedAt":{"ne":null}}}`, nil)
	if err != nil || where(sql) != `"published_at" IS NOT NULL` {
		t.Fatalf("ne null built %s (%v), want \"published_at\" IS NOT NULL", where(sql), err)
	}
}

// Every rejection is a *query.Error carrying the path that was wrong, because
// that is what a transport turns into a 400 body. An error that is merely a
// string forces the caller to choose between leaking internals and saying
// nothing useful.
func TestEveryRejectionNamesThePathThatWasWrong(t *testing.T) {
	for _, tc := range []struct{ name, doc, path string }{
		{"a filter field", `{"filter":{"nope":1}}`, "filter.nope"},
		{"a nested filter field", `{"filter":{"author.nope":1}}`, "filter.author.nope"},
		{"an operator", `{"filter":{"title":{"wat":1}}}`, "filter.title.wat"},
		{"an operand", `{"filter":{"views":{"gte":"abc"}}}`, "filter.views.gte"},
		{"a branch of an or", `{"filter":{"or":[{"title":"a"},{"nope":1}]}}`, "filter.or[1].nope"},
		{"a sort field", `{"sort":["nope"]}`, "sort"},
		{"a projection", `{"select":["nope"]}`, "select"},
		{"a preload", `{"preload":["nope"]}`, "preload"},
		{"a search field", `{"search":"go","searchFields":["nope"]}`, "searchFields"},
		{"a preload's own filter", `{"preload":[{"path":"comments","filter":{"nope":1}}]}`,
			"preload.Comments.filter.nope"},
		{"a flat term", `{"terms":[{"path":"nope","values":["1"]}]}`, "filter"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := tryDoc(t, tc.doc, nil)
			var qerr *query.Error
			if !errors.As(err, &qerr) {
				t.Fatalf("error = %#v, want a *query.Error", err)
			}
			if qerr.Path != tc.path {
				t.Fatalf("path = %q, want %q", qerr.Path, tc.path)
			}
			if qerr.Reason == "" {
				t.Fatalf("%s carries no reason", tc.doc)
			}
			if !strings.HasPrefix(qerr.Error(), "query: "+tc.path+": ") {
				t.Fatalf("message = %q, want it to start with the path", qerr)
			}
		})
	}
}

// A column reference is resolved through the schema, so the spelling a client
// happens to use never survives into the statement.
func TestClientSpellingNeverReachesTheStatement(t *testing.T) {
	for _, spelling := range []string{"authorId", "author_id", "AuthorID", "AUTHOR_ID", "author-id", "author id"} {
		doc := `{"filter":{"` + spelling + `":7},"sort":["` + spelling + `"],"select":["` + spelling + `"]}`
		sql, args, err := tryDoc(t, doc, nil)
		if err != nil {
			t.Fatalf("%q was refused: %v", spelling, err)
		}
		want := `SELECT "id", "author_id" FROM "articles" WHERE "author_id" = $1 ` +
			`ORDER BY "author_id" ASC, "id" ASC LIMIT 20`
		if sql != want {
			t.Fatalf("%q built\n  %s\nwant\n  %s", spelling, sql, want)
		}
		if len(args) != 1 || args[0] != int64(7) {
			t.Fatalf("%q bound %#v, want the int64 the column holds", spelling, args)
		}
	}
}

// Coercion failures are rejections naming the column and the type it wanted.
// A silently zeroed value would match the wrong rows; a panic would take the
// process down on a request anyone can send.
func TestUncoercibleValuesAreRejectedNotZeroed(t *testing.T) {
	mentions := func(err error, what string) bool {
		return strings.Contains(strings.ToLower(err.Error()), strings.ToLower(what))
	}
	for _, tc := range []struct{ name, doc, term, field, typ string }{
		{"a word into an int", `{"filter":{"count":"twelve"}}`, "count:eq:twelve", "count", "int"},
		{"a fraction into an int", `{"filter":{"count":1.5}}`, "count:eq:1.5", "count", "int"},
		{"an int64 into an int8", `{"filter":{"small":9223372036854775807}}`,
			"small:eq:9223372036854775807", "small", "int8"},
		{"a negative into a uint", `{"filter":{"tiny":-1}}`, "tiny:eq:-1", "tiny", "uint8"},
		{"past the top of a uint64", `{"filter":{"total":18446744073709551616}}`,
			"total:eq:18446744073709551616", "total", "uint64"},
		{"a word into a bool", `{"filter":{"active":"maybe"}}`, "active:eq:maybe", "active", "bool"},
		{"a word into a time", `{"filter":{"at":"yesterday"}}`, "at:eq:yesterday", "at", "time.Time"},
		{"a unix epoch into a time", `{"filter":{"at":1735689600}}`, "at:eq:1735689600", "at", "time.Time"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for door, req := range map[string]*query.Request{
				"json":         jsonRequest(t, tc.doc),
				"query string": termRequest(t, tc.term),
			} {
				opts, err := req.Compile(Samples.Meta(), nil)
				if err == nil {
					t.Fatalf("the %s door accepted %s and compiled %d options", door, tc.name, len(opts))
				}
				var qerr *query.Error
				if !errors.As(err, &qerr) {
					t.Fatalf("the %s door returned %#v, want a *query.Error", door, err)
				}
				if !mentions(qerr, tc.field) || !mentions(qerr, tc.typ) {
					t.Fatalf("the %s door said %q, want it to name the column %q and the type %q",
						door, qerr, tc.field, tc.typ)
				}
			}
		})
	}
}

// A timestamp keeps the zone it arrived with, and one without a zone is read as
// UTC rather than as the server's local time — which would move the boundary of
// every range filter with the deployment.
func TestTimestampZonesSurviveCoercion(t *testing.T) {
	for _, tc := range []struct {
		name, text string
		want       time.Time
	}{
		{"UTC", "2026-01-02T03:04:05Z", time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)},
		{"an eastern offset", "2026-01-02T03:04:05+02:00", time.Date(2026, 1, 2, 1, 4, 5, 0, time.UTC)},
		{"a western offset", "2026-01-02T03:04:05-05:00", time.Date(2026, 1, 2, 8, 4, 5, 0, time.UTC)},
		{"no zone at all", "2026-01-02T03:04:05", time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)},
		{"a date on its own", "2026-01-02", time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for door, req := range map[string]*query.Request{
				"json":         jsonRequest(t, `{"filter":{"at":{"gte":"`+tc.text+`"}}}`),
				"query string": termRequest(t, "at:gte:"+tc.text),
			} {
				opts, err := req.Compile(Samples.Meta(), nil)
				if err != nil {
					t.Fatalf("the %s door refused %q: %v", door, tc.text, err)
				}
				rec := crudtest.Postgres().Push(crudtest.Rows())
				if _, err := Samples.Bind(rec).GetAll(context.Background(), opts...); err != nil {
					t.Fatalf("query: %v", err)
				}
				got, ok := rec.Last().Args[0].(time.Time)
				if !ok {
					t.Fatalf("the %s door bound %#v, want a time.Time", door, rec.Last().Args[0])
				}
				if !got.Equal(tc.want) {
					t.Fatalf("the %s door read %q as %s, want the instant %s",
						door, tc.text, got.Format(time.RFC3339Nano), tc.want.Format(time.RFC3339Nano))
				}
			}
		})
	}
}

// The flat form's edges: what a query string can carry that a JSON body cannot
// express, and what it does with the shapes in between.
func TestQueryStringTermEdges(t *testing.T) {
	for _, tc := range []struct {
		name  string
		qs    string
		where string
		args  []any
		fail  string
	}{
		{name: "a repeated parameter ANDs its terms", qs: "f=views:gte:1&f=views:lt:9",
			where: `("views" >= $1 AND "views" < $2)`, args: []any{1, 9}},
		{name: "the same field twice keeps both terms", qs: "f=title:contains:go&f=title:contains:rust",
			where: `("title" LIKE $1 AND "title" LIKE $2)`, args: []any{"%go%", "%rust%"}},
		{name: "one colon means equality", qs: "f=title:go",
			where: `"title" = $1`, args: []any{"go"}},
		{name: "an empty operator falls back to equality", qs: "f=title::go",
			where: `"title" = $1`, args: []any{"go"}},
		{name: "a value keeps every colon after the second", qs: "f=createdAt:gte:2026-01-02T03:04:05Z",
			where: `"created_at" >= $1`, args: []any{time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)}},
		{name: "an encoded colon is part of the value", qs: "f=title:eq:a%3Ab",
			where: `"title" = $1`, args: []any{"a:b"}},
		{name: "an encoded payload arrives whole", qs: "f=title:eq:%25go%25%20%2B%20rust",
			where: `"title" = $1`, args: []any{"%go% + rust"}},
		{name: "a pipe separates two terms in one parameter", qs: "f=views:gte:1|title:eq:go",
			where: `("views" >= $1 AND "title" = $2)`, args: []any{1, "go"}},

		{name: "no colon at all", qs: "f=title", fail: `"title" is not field:op:value`},
		{name: "an empty field name", qs: "f=:eq:go", fail: "empty field path"},
		{name: "an empty value", qs: "f=title:eq:", fail: "eq needs a value"},
		{name: "an empty value for a text operator", qs: "f=title:contains:", fail: "contains needs a value"},
		{name: "a list operator given one value", qs: "f=views:between:1", fail: "exactly two values, got 1"},
		{name: "a value the column cannot hold", qs: "f=views:eq:9223372036854775808",
			fail: `"9223372036854775808" is not a valid int`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sql, args, err := tryQuery(t, tc.qs, nil)
			if tc.fail != "" {
				if err == nil {
					t.Fatalf("%s was accepted and compiled to %s", tc.qs, where(sql))
				}
				if !strings.Contains(err.Error(), tc.fail) {
					t.Fatalf("error = %q, want it to say %q", err, tc.fail)
				}
				return
			}
			if err != nil {
				t.Fatalf("%s was refused: %v", tc.qs, err)
			}
			if got := where(sql); got != tc.where {
				t.Fatalf("where = %s, want %s", got, tc.where)
			}
			if len(args) != len(tc.args) {
				t.Fatalf("args = %#v, want %#v", args, tc.args)
			}
			for i := range args {
				if !same(args[i], tc.args[i]) {
					t.Fatalf("arg %d = %#v (%T), want %#v (%T)",
						i, args[i], args[i], tc.args[i], tc.args[i])
				}
			}
		})
	}
}

// The `filter` parameter holds a JSON document, and a malformed one is a
// rejection. Dropping it would answer a narrowed request with the whole table,
// which is the one failure the client cannot see from the response.
func TestAMalformedFilterParameterIsRefusedNotDropped(t *testing.T) {
	for _, tc := range []struct{ name, qs string }{
		{"truncated json", "filter=%7B%22title%22"},
		{"not json at all", "filter=title:eq:go"},
		{"an array", "filter=%5B%7B%22title%22:%22a%22%7D%5D"},
		{"a bare string", "filter=%22title%22"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sql, _, err := tryQuery(t, tc.qs, nil)
			if err == nil {
				t.Fatalf("%s was dropped and answered with %s", tc.qs, sql)
			}
			if !strings.Contains(err.Error(), "expected an object") {
				t.Fatalf("error = %q, want it to say the filter was not an object", err)
			}
		})
	}

	// An absent or explicitly null filter is not malformed: it is no filter.
	for _, qs := range []string{"", "filter=", "filter=%20", "filter=null"} {
		sql, _, err := tryQuery(t, qs, nil)
		if err != nil {
			t.Fatalf("%q was refused: %v", qs, err)
		}
		if strings.Contains(sql, "WHERE") {
			t.Fatalf("%q built %s, want no WHERE clause", qs, sql)
		}
	}

	// And a well-formed one still compiles, so the rule above did not simply
	// close the door.
	sql, args, err := tryQuery(t, "filter=%7B%22views%22:%7B%22gte%22:10%7D%7D", nil)
	if err != nil {
		t.Fatalf("a well-formed filter parameter was refused: %v", err)
	}
	if got := where(sql); got != `"views" >= $1` || len(args) != 1 || args[0] != 10 {
		t.Fatalf("where = %s args = %#v, want \"views\" >= $1 [10]", got, args)
	}
}

// The same request must produce byte-identical SQL every time. Go randomises
// map iteration, so a document with many keys is the case that catches a
// forgotten sort — and the arguments have to line up with the placeholders they
// were numbered for.
func TestTheSameDocumentAlwaysCompilesToTheSameStatement(t *testing.T) {
	doc := `{"filter":{
		"title":"a","body":"b","authorId":3,"views":{"gte":1,"lt":9,"ne":5},
		"createdAt":{"gte":"2026-01-02"},"publishedAt":{"isNull":true},
		"author.name":"Ann","comments.body":{"contains":"x"},"tags.slug":{"in":["go","rust"]},
		"or":[{"title":"c"},{"body":"d"},{"views":7}],
		"not":{"title":"spam"}
	},"sort":["-views","title"],"select":["title","views"],"preload":["author","comments"]}`

	sql, args, err := tryDoc(t, doc, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Sorted key order, not document order: author.name, authorId, body,
	// comments.body, createdAt, not, or, publishedAt, tags.slug, title, views.
	want := `SELECT "id", "title", "views", "author_id" FROM "articles" WHERE ` +
		`(EXISTS (SELECT 1 FROM "authors" AS rx1 WHERE rx1."id" = "articles"."author_id" AND rx1."name" = $1) ` +
		`AND "author_id" = $2 AND "body" = $3 ` +
		`AND EXISTS (SELECT 1 FROM "comments" AS rx2 WHERE rx2."article_id" = "articles"."id" AND rx2."body" LIKE $4) ` +
		`AND "created_at" >= $5 ` +
		`AND NOT ("title" = $6) ` +
		`AND ("title" = $7 OR "body" = $8 OR "views" = $9) ` +
		`AND "published_at" IS NULL ` +
		`AND EXISTS (SELECT 1 FROM "tags" AS rx3 JOIN "article_tags" AS rx4 ON rx4."tag_id" = rx3."id" ` +
		`WHERE rx4."article_id" = "articles"."id" AND rx3."slug" IN ($10, $11)) ` +
		`AND "title" = $12 AND "views" >= $13 AND "views" < $14 AND "views" <> $15) ` +
		`ORDER BY "views" DESC, "title" ASC, "id" ASC LIMIT 20`
	if sql != want {
		t.Fatalf("sql  = %s\nwant = %s", sql, want)
	}
	wantArgs := []any{"Ann", int64(3), "b", "%x%",
		time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC), "spam",
		"c", "d", 7, "go", "rust", "a", 1, 9, 5}
	if len(args) != len(wantArgs) {
		t.Fatalf("args = %#v, want %#v", args, wantArgs)
	}
	for i := range args {
		if !same(args[i], wantArgs[i]) {
			t.Fatalf("$%d = %#v (%T), want %#v (%T)", i+1, args[i], args[i], wantArgs[i], wantArgs[i])
		}
	}

	// Then the same document, and the same document with its keys written in a
	// different order, over and over.
	shuffled := `{"filter":{
		"not":{"title":"spam"},"tags.slug":{"in":["go","rust"]},"comments.body":{"contains":"x"},
		"or":[{"title":"c"},{"body":"d"},{"views":7}],"author.name":"Ann",
		"publishedAt":{"isNull":true},"createdAt":{"gte":"2026-01-02"},
		"views":{"ne":5,"lt":9,"gte":1},"authorId":3,"body":"b","title":"a"
	},"sort":["-views","title"],"select":["title","views"],"preload":["author","comments"]}`
	for i := range 50 {
		for _, d := range []string{doc, shuffled} {
			got, gotArgs, err := tryDoc(t, d, nil)
			if err != nil {
				t.Fatal(err)
			}
			if got != sql {
				t.Fatalf("run %d built a different statement:\n%s\n%s", i, sql, got)
			}
			if len(gotArgs) != len(args) {
				t.Fatalf("run %d bound %#v, want %#v", i, gotArgs, args)
			}
			for j := range args {
				if !same(gotArgs[j], args[j]) {
					t.Fatalf("run %d moved $%d: %#v, want %#v", i, j+1, gotArgs[j], args[j])
				}
			}
		}
	}
}
