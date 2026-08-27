package query_test

import (
	"encoding/json"
	"net/url"
	"strings"
	"testing"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/query"
)

// The wire DSL is the one surface in this library that parses bytes somebody
// else chose. These targets state what must hold for *any* input, which is a
// different question from the hostile-input suite: that one asks whether the
// payloads a person thought of are refused, and this asks whether anything at
// all gets past the properties the rest of the design rests on.
//
// Three invariants, and every other guarantee is built on them:
//
//  1. **It never panics.** This code runs before any authorisation does, so a
//     panic here is a 500 on a path that has not yet decided who is asking.
//  2. **It compiles or it refuses, never both.** [[D-013]]: a rejection produces
//     no options at all, so a transport cannot log the error and run "the good
//     half" — which would be a query answering with more rows than were asked
//     for.
//  3. **No text the caller wrote reaches the statement.** [[D-003]] and
//     [[D-014]]: names resolve to the model's own quoted columns and values are
//     always bound. This is what makes the DSL safe to expose, and the only one
//     of the three whose failure is silent — a well-formed 200 with the wrong
//     rows in it.
//
// Run them with:
//
//	go test ./crud/query/ -run Fuzz -fuzz FuzzCompileJSON -fuzztime 2m
//
// The seed corpus alone runs as an ordinary test under `go test`, so `make unit`
// exercises every seed below on every run without anybody opting in.

// FuzzCompileJSON drives the JSON document door.
func FuzzCompileJSON(f *testing.F) {
	// Seeded from the shapes the hand-written suite already covers, so the
	// fuzzer starts inside the grammar rather than spending its budget
	// discovering that a document begins with a brace.
	for _, seed := range []string{
		`{}`,
		`{"filter":{"title":"go"}}`,
		`{"filter":{"views":{"gte":10}}}`,
		`{"filter":{"and":[{"views":{"gt":1}},{"title":{"contains":"x"}}]}}`,
		`{"filter":{"not":{"or":[{"id":1},{"id":2}]}}}`,
		`{"filter":{"comments.body":{"like":"x"}}}`,
		`{"filter":{"author.name":{"startsWith":"An"}}}`,
		`{"filter":{"views":{"in":[1,2,3]}}}`,
		`{"filter":{"views":{"between":[1,9]}}}`,
		`{"filter":{"publishedAt":{"isNull":true}}}`,
		`{"sort":["-views","title"],"select":["id","title"]}`,
		`{"preload":["author","comments.author"]}`,
		`{"search":"go","searchFields":["title","body"]}`,
		`{"terms":[{"path":"views","op":"gte","values":["10"]}]}`,
		`{"page":2,"limit":5,"offset":0}`,
		`{"after":"eyJmIjpbIklEIl0sInYiOlsxXX0","sort":["id"]}`,
		`{"unpaged":true}`,
		`{"distinct":true,"skipTotal":true}`,
		// The shapes an attacker reaches for first. Each is already refused or
		// already bound; they are here so a change that stopped refusing one is
		// caught by the seed run rather than by a fuzz campaign nobody ran.
		`{"filter":{"title":{"eq":"' OR 1=1 --"}}}`,
		`{"filter":{"title":{"eq":"\"; DROP TABLE articles; --"}}}`,
		`{"filter":{"title; DROP TABLE articles":"x"}}`,
		`{"sort":["title; DROP TABLE articles"]}`,
		`{"select":["*"]}`,
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, doc string) {
		var req query.Request
		if err := json.Unmarshal([]byte(doc), &req); err != nil {
			// Not a document at all. The transport answers 400 and Compile is
			// never reached, so there is nothing here to say.
			return
		}

		// Invariants 1 and 2. A panic fails the test by itself; the assertion is
		// that a refusal comes back empty-handed.
		opts, err := req.Compile(Articles.Meta(), nil)
		if err != nil {
			if len(opts) != 0 {
				t.Fatalf("a refusal came back with %d options, so a transport could log the error and run the good half:\n%s",
					len(opts), doc)
			}
			return
		}

		// Invariant 3. Rendering is where a predicate that smuggled text would
		// show it — and a filter that compiled and will not render is the one
		// shape that should be impossible, because Compile validated every name
		// against the model before it built anything.
		o := crud.Build(opts...)
		sql, _, rerr := crud.NewSQL(crud.Postgres{}, Articles.Meta()).Predicate(o.Predicate()).Done()
		if rerr != nil {
			t.Fatalf("a compiled filter would not render (%v):\n%s", rerr, doc)
		}
		assertNothingTheCallerWroteIsInTheSQL(t, doc, sql)
	})
}

// FuzzCompileQueryString drives the other door.
//
// It parses a URL query rather than a document, and it has its own coercion,
// its own aliases and its own flat-term grammar — so a property proved for the
// JSON door says nothing about this one. The two are meant to express the same
// things ([[UC-002]] guarantee 1), which is exactly why both need this.
func FuzzCompileQueryString(f *testing.F) {
	for _, seed := range []string{
		"",
		"f=views:gte:10",
		"f=title:contains:go&sort=-views",
		"f=comments.body:like:x&preload=comments",
		"f=views:in:1,2,3",
		"f=publishedAt:isNull:true",
		"search=go&searchFields=title,body",
		"page=2&limit=5",
		"unpaged=1",
		"all=true",
		"after=eyJmIjpbIklEIl0sInYiOlsxXX0&sort=id",
		"filter=%7B%22title%22:%22x%22%7D",
		"select=id,title&preload=author",
		"f=title:eq:' OR 1=1 --",
		"sort=title;DROP+TABLE+articles",
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, raw string) {
		v, err := url.ParseQuery(raw)
		if err != nil {
			return
		}
		req, err := query.ParseQuery(v)
		if err != nil {
			return
		}
		opts, err := req.Compile(Articles.Meta(), nil)
		if err != nil {
			if len(opts) != 0 {
				t.Fatalf("a refusal came back with %d options:\n%s", len(opts), raw)
			}
			return
		}
		o := crud.Build(opts...)
		sql, _, rerr := crud.NewSQL(crud.Postgres{}, Articles.Meta()).Predicate(o.Predicate()).Done()
		if rerr != nil {
			t.Fatalf("a compiled filter would not render (%v):\n%s", rerr, raw)
		}
		assertNothingTheCallerWroteIsInTheSQL(t, raw, sql)
	})
}

// assertNothingTheCallerWroteIsInTheSQL is invariant 3, stated as a check.
//
// It looks for the marks that would mean a value or a name reached the statement
// as text rather than as a bind or a column the model resolved. The one fixed
// literal is the backslash in a convenience LIKE's `ESCAPE '\'` clause; remove
// that grammar before checking the caller-controlled part of the statement.
//
// The double-quote case is the subtle one. Quotes are legitimate — that is how
// identifiers are quoted — so what matters is what sits *between* them. No
// column of any model is named with a space or a parenthesis, so anything like
// that inside quotes is text that arrived from outside.
func assertNothingTheCallerWroteIsInTheSQL(t *testing.T, input, sql string) {
	t.Helper()
	checked := strings.ReplaceAll(sql, ` ESCAPE '\'`, "")
	for _, mark := range []string{"'", ";", "--", "/*", "*/"} {
		if strings.Contains(checked, mark) {
			t.Fatalf("the rendered statement carries %q, so something the caller wrote reached it as text:\ninput: %s\nsql:   %s",
				mark, input, sql)
		}
	}
	parts := strings.Split(checked, `"`)
	for i := 1; i < len(parts); i += 2 {
		if strings.ContainsAny(parts[i], " ()\t\n\r") {
			t.Fatalf("a quoted identifier contains %q, which is not the name of any column:\ninput: %s\nsql:   %s",
				parts[i], input, sql)
		}
	}
}
