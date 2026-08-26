package query_test

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/frostgrove/vv/crud/crudtest"
	"github.com/frostgrove/vv/crud/query"
)

// payloads are the strings an attacker puts where a field name goes: statement
// terminators, comment starters, quotes of both flavours, path traversal,
// wildcards and control characters.
var payloads = []string{
	`id; DROP TABLE users`,
	`id"; DROP TABLE users --`,
	"id`; DROP TABLE users",
	`id' OR '1'='1`,
	`1) OR (1=1`,
	`id/*`,
	`id UNION SELECT 1`,
	`id;--`,
	`..`,
	`../../etc/passwd`,
	`*`,
	`%`,
	"id\x00",
	"id\nviews",
	`articles"."id`,
}

// quoted renders a payload as a JSON string, escapes and all, so the document
// stays well-formed however hostile the payload is.
func quoted(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		panic(err)
	}
	return string(b)
}

// Anywhere the wire DSL takes the *name* of something — a column, a relation, a
// sort key, an operator — the name is resolved against the schema. A payload
// that resolves to nothing is a rejection, so there is no path from a request
// to an identifier the model never declared.
func TestPayloadsInEveryNamePositionAreRefused(t *testing.T) {
	for _, position := range []struct {
		name  string
		build func(payload string) string
	}{
		{"a filter key", func(p string) string { return `{"filter":{` + quoted(p) + `:1}}` }},
		{"a nested filter key", func(p string) string { return `{"filter":{` + quoted("author."+p) + `:1}}` }},
		{"a relation segment", func(p string) string { return `{"filter":{` + quoted(p+".name") + `:1}}` }},
		{"an operator", func(p string) string { return `{"filter":{"title":{` + quoted(p) + `:"x"}}}` }},
		{"a sort key", func(p string) string { return `{"sort":[` + quoted(p) + `]}` }},
		{"a sort's nulls placement", func(p string) string {
			return `{"sort":[{"field":"title","nulls":` + quoted(p) + `}]}`
		}},
		{"a projection", func(p string) string { return `{"select":[` + quoted(p) + `]}` }},
		{"a preload", func(p string) string { return `{"preload":[` + quoted(p) + `]}` }},
		{"a search field", func(p string) string {
			return `{"search":"go","searchFields":[` + quoted(p) + `]}`
		}},
		{"a term path", func(p string) string {
			return `{"terms":[{"path":` + quoted(p) + `,"values":["1"]}]}`
		}},
		{"a preload's own filter key", func(p string) string {
			return `{"preload":[{"path":"comments","filter":{` + quoted(p) + `:1}}]}`
		}},
		{"a preload's own sort key", func(p string) string {
			return `{"preload":[{"path":"comments","sort":[` + quoted(p) + `]}]}`
		}},
	} {
		t.Run(position.name, func(t *testing.T) {
			for _, payload := range payloads {
				sql, args, err := tryDoc(t, position.build(payload), nil)
				if err == nil {
					t.Fatalf("%s accepted %q and built\n  %s\n  %#v", position.name, payload, sql, args)
				}
			}
		})
	}
}

// The schema resolves names ignoring separators, so `id--` is a spelling of
// `ID` rather than a rejection. It is still the canonical column that reaches
// the statement, which is why a payload that survives the lookup cannot carry
// its punctuation in with it.
func TestAPayloadThatFoldsOntoAColumnRendersThatColumn(t *testing.T) {
	for _, spelling := range []string{"id--", "id-", "id __ ", "i-d"} {
		doc := `{"filter":{` + quoted(spelling) + `:1},"sort":[` + quoted(spelling) + `]}`
		sql, args, err := tryDoc(t, doc, nil)
		if err != nil {
			t.Fatalf("%q was refused: %v", spelling, err)
		}
		if got := where(sql); got != `"id" = $1` {
			t.Fatalf("%q built %s, want \"id\" = $1", spelling, got)
		}
		if !strings.Contains(sql, `ORDER BY "id" ASC`) {
			t.Fatalf("%q built %s", spelling, sql)
		}
		if strings.Contains(sql, "--") || strings.Contains(sql, spelling) {
			t.Fatalf("%q reached the statement:\n%s", spelling, sql)
		}
		if len(args) != 1 || args[0] != int64(1) {
			t.Fatalf("%q bound %#v", spelling, args)
		}
	}
}

// A value, unlike a name, is the client's to choose — so it never becomes SQL
// text. It is bound, whatever it says.
func TestPayloadsInValuePositionsAreBoundNotWritten(t *testing.T) {
	for _, payload := range payloads {
		t.Run(quoted(payload), func(t *testing.T) {
			for _, tc := range []struct {
				name string
				doc  string
				want string
			}{
				{"equality", `{"filter":{"title":` + quoted(payload) + `}}`, `"title" = $1`},
				{"a list", `{"filter":{"title":{"in":[` + quoted(payload) + `]}}}`, `"title" IN ($1)`},
				{"a raw pattern", `{"filter":{"title":{"like":` + quoted(payload) + `}}}`, `"title" LIKE $1`},
				{"a flat term", `{"terms":[{"path":"title","values":[` + quoted(payload) + `]}]}`, `"title" = $1`},
			} {
				sql, args, err := tryDoc(t, tc.doc, nil)
				if err != nil {
					t.Fatalf("%s was refused: %v", tc.name, err)
				}
				if got := where(sql); got != tc.want {
					t.Fatalf("%s built %s, want %s", tc.name, got, tc.want)
				}
				if len(args) != 1 || args[0] != payload {
					t.Fatalf("%s bound %#v, want the payload itself", tc.name, args)
				}
				if strings.Contains(sql, payload) {
					t.Fatalf("%s wrote the payload into the statement:\n%s", tc.name, sql)
				}
			}
		})
	}
}

// A preload's filter reaches the database in a statement of its own, so the
// rule has to hold there too — and that statement is the one no caller sees.
func TestAPreloadsOwnFilterBindsItsValue(t *testing.T) {
	const payload = `x'; DROP TABLE comments --`
	rec := crudtest.Postgres().Push(
		crudtest.Rows(articleRow(1, 10, "first")),
		crudtest.Rows(),
	)
	var req query.Request
	doc := `{"preload":[{"path":"comments","filter":{"body":` + quoted(payload) + `}}]}`
	if err := json.Unmarshal([]byte(doc), &req); err != nil {
		t.Fatalf("decode: %v", err)
	}
	opts, err := req.Compile(Articles.Meta(), nil)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if _, err := Articles.Bind(rec).GetAll(context.Background(), opts...); err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(rec.Statements()) != 2 {
		t.Fatalf("%d statements, want the page and its preload:\n%s", len(rec.SQL()), strings.Join(rec.SQL(), "\n"))
	}
	st := rec.Statements()[1]
	want := `SELECT "id", "article_id", "author_id", "body", "approved" FROM "comments" ` +
		`WHERE "article_id" IN ($1) AND "body" = $2`
	if got := crudtest.Normalize(st.SQL); got != want {
		t.Fatalf("preload = %s\nwant %s", got, want)
	}
	if len(st.Args) != 2 || st.Args[1] != payload {
		t.Fatalf("preload bound %#v, want the payload as its second argument", st.Args)
	}
}

// The convenience patterns escape the wildcards a client sends, so a prefix
// search cannot be turned into a full scan — or into a match on rows the
// pattern was never meant to reach. `like` is the one operator documented to
// take the pattern as written.
func TestWildcardsInAPatternAreEscaped(t *testing.T) {
	for _, tc := range []struct{ name, doc, want string }{
		{"contains", `{"filter":{"title":{"contains":"100%"}}}`, `%100\%%`},
		{"startsWith", `{"filter":{"title":{"startswith":"_x"}}}`, `\_x%`},
		{"endsWith", `{"filter":{"title":{"endswith":"%"}}}`, `%\%`},
		{"a backslash of its own", `{"filter":{"title":{"contains":"a\\b"}}}`, `%a\\b%`},
		{"search", `{"search":"%","searchFields":["title"]}`, `%\%%`},
		{"like takes the pattern as written", `{"filter":{"title":{"like":"%go%"}}}`, `%go%`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sql, args, err := tryDoc(t, tc.doc, nil)
			if err != nil {
				t.Fatalf("compile: %v", err)
			}
			if !strings.Contains(sql, "LIKE $1") {
				t.Fatalf("sql = %s, want a bound pattern", sql)
			}
			if len(args) != 1 || args[0] != tc.want {
				t.Fatalf("pattern = %#v, want %q", args, tc.want)
			}
		})
	}
}

// An allow-list is matched on the canonical path, so none of the spellings that
// reach the same column reach it around the list.
func TestADeniedColumnStaysDeniedHoweverItIsSpelled(t *testing.T) {
	cfg := &query.Config{
		Filterable:  []string{"Title"},
		Sortable:    []string{"Title"},
		Selectable:  []string{"Title"},
		Searchable:  []string{"Title"},
		Preloadable: []string{"Author"},
	}
	for _, spelling := range []string{"publishedAt", "published_at", "PublishedAt", "PUBLISHED_AT", "published-at", " publishedAt "} {
		for _, doc := range []string{
			`{"filter":{` + quoted(spelling) + `:null}}`,
			`{"filter":{"or":[{` + quoted(spelling) + `:null}]}}`,
			`{"filter":{"not":{` + quoted(spelling) + `:null}}}`,
			`{"terms":[{"path":` + quoted(spelling) + `,"op":"isNull"}]}`,
			`{"sort":[` + quoted(spelling) + `]}`,
			`{"select":[` + quoted(spelling) + `]}`,
			`{"search":"go","searchFields":[` + quoted(spelling) + `]}`,
		} {
			sql, _, err := tryDoc(t, doc, cfg)
			if err == nil {
				t.Fatalf("%s slipped past the allow-list as %q and built\n  %s", doc, spelling, sql)
			}
		}
	}
	// A relation the list does not name is closed the same way, in every verb
	// that can walk one.
	for _, doc := range []string{
		`{"filter":{"comments.body":"x"}}`,
		`{"sort":["author.name"]}`,
		`{"preload":["comments"]}`,
		`{"search":"go","searchFields":["author.name"]}`,
	} {
		if sql, _, err := tryDoc(t, doc, cfg); err == nil {
			t.Fatalf("%s walked to an unlisted relation and built\n  %s", doc, sql)
		}
	}
}

// A preload's own filter is compiled against the related model, and the
// allow-list is applied there too: a relation being preloadable does not make
// its columns filterable.
func TestAPreloadableRelationIsNotAFilterableOne(t *testing.T) {
	cfg := &query.Config{Filterable: []string{"Title"}, Preloadable: []string{"Comments"}}
	sql, _, err := tryDoc(t, `{"preload":["comments"]}`, cfg)
	if err != nil {
		t.Fatalf("the preload the list allows was refused: %v", err)
	}
	if !strings.Contains(sql, `FROM "articles"`) {
		t.Fatalf("sql = %s", sql)
	}
	for _, doc := range []string{
		`{"preload":[{"path":"comments","filter":{"approved":true}}]}`,
		`{"preload":[{"path":"comments","filter":{"or":[{"approved":true}]}}]}`,
		`{"preload":[{"path":"comments","filter":{"author.name":"Ann"}}]}`,
	} {
		if _, _, err := tryDoc(t, doc, cfg); err == nil {
			t.Fatalf("%s filtered a relation on a column no list named", doc)
		} else if !strings.Contains(err.Error(), "not filterable") {
			t.Fatalf("error = %q, want it to say the column is not filterable", err)
		}
	}
}

// The search is confined to the columns the config names, whichever way the
// request tries to widen it: an explicit field list, a nested path, or a
// default list the config itself carries.
func TestSearchCannotReachOutsideItsList(t *testing.T) {
	cfg := &query.Config{Searchable: []string{"Title"}}
	sql, args, err := tryDoc(t, `{"search":"go"}`, cfg)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if got := where(sql); got != `"title" LIKE $1` || len(args) != 1 || args[0] != "%go%" {
		t.Fatalf("an unqualified search built %s %#v, want only the searchable column", got, args)
	}
	for _, doc := range []string{
		`{"search":"go","searchFields":["body"]}`,
		`{"search":"go","searchFields":["title","body"]}`,
		`{"search":"go","searchFields":["author.name"]}`,
	} {
		if _, _, err := tryDoc(t, doc, cfg); err == nil {
			t.Fatalf("%s searched a column outside the list", doc)
		}
	}
}

// The document budgets hold at their defaults, not only when a config sets
// them: an endpoint that passes nil is still bounded.
func TestTheDefaultBudgetsBoundAnUnconfiguredEndpoint(t *testing.T) {
	t.Run("conditions", func(t *testing.T) {
		var b strings.Builder
		b.WriteString(`{"filter":{"or":[`)
		for i := range 200 {
			if i > 0 {
				b.WriteString(",")
			}
			b.WriteString(`{"views":`)
			b.WriteString(strconv.Itoa(i))
			b.WriteString(`}`)
		}
		b.WriteString(`]}}`)
		_, _, err := tryDoc(t, b.String(), nil)
		if err == nil {
			t.Fatal("200 conditions were accepted with no config at all")
		}
		if !strings.Contains(err.Error(), "at most 64 conditions") {
			t.Fatalf("error = %q, want the default budget of 64", err)
		}
	})

	t.Run("nesting", func(t *testing.T) {
		doc := `{"filter":` + strings.Repeat(`{"not":`, 40) + `{"title":"a"}` + strings.Repeat(`}`, 40) + `}`
		_, _, err := tryDoc(t, doc, nil)
		if err == nil {
			t.Fatal("a filter nested 40 deep was accepted with no config at all")
		}
		if !strings.Contains(err.Error(), "deeper than the allowed 6 levels") {
			t.Fatalf("error = %q, want the default depth of 6", err)
		}
	})

	t.Run("path length", func(t *testing.T) {
		path := strings.Repeat("comments.author.", 3) + "name"
		_, _, err := tryDoc(t, `{"filter":{`+quoted(path)+`:"x"}}`, nil)
		if err == nil {
			t.Fatalf("the %d-segment path %s was accepted", strings.Count(path, ".")+1, path)
		}
		if !strings.Contains(err.Error(), "deeper than the allowed 6 segments") {
			t.Fatalf("error = %q, want the default depth of 6", err)
		}
	})

	t.Run("preloads", func(t *testing.T) {
		paths := make([]string, 20)
		for i := range paths {
			paths[i] = `"author"`
		}
		_, _, err := tryDoc(t, `{"preload":[`+strings.Join(paths, ",")+`]}`, nil)
		if err == nil {
			t.Fatal("20 preloads were accepted with no config at all")
		}
		if !strings.Contains(err.Error(), "at most 16 relations") {
			t.Fatalf("error = %q, want the default of 16", err)
		}
	})
}

// The condition budget belongs to the document, not to each model in it. A
// preload compiles against its own model, and if it also got its own allowance
// a request could buy sixteen more filters by naming sixteen relations.
func TestAPreloadSpendsTheDocumentsConditionBudget(t *testing.T) {
	doc := `{"filter":{"title":"a"},"preload":[{"path":"comments","filter":{"approved":true}}]}`
	if _, _, err := tryDoc(t, doc, &query.Config{MaxConditions: 2}); err != nil {
		t.Fatalf("one comparison at the root and one in a preload, at a budget of 2: %v", err)
	}
	_, _, err := tryDoc(t, doc, &query.Config{MaxConditions: 1})
	if err == nil {
		t.Fatal("a preload's filter was compiled on a budget of its own")
	}
	if !strings.Contains(err.Error(), "at most 1 conditions") {
		t.Fatalf("error = %q, want the budget refusal", err)
	}

	// The same across two preloads, where neither one exceeds the budget alone.
	two := `{"preload":[{"path":"comments","filter":{"approved":true}},{"path":"tags","filter":{"slug":"go"}}]}`
	if _, _, err := tryDoc(t, two, &query.Config{MaxConditions: 2}); err != nil {
		t.Fatalf("two preloads with one comparison each, at a budget of 2: %v", err)
	}
	if _, _, err := tryDoc(t, two, &query.Config{MaxConditions: 1}); err == nil {
		t.Fatal("two preloads spent two conditions against a budget of one")
	}
}

// A document nested past what encoding/json itself will parse has to come back
// as a rejection. The recursive descent through the filter tree is the one
// place a request could take the process down with it.
func TestADocumentTooDeepToParseIsRejectedNotFatal(t *testing.T) {
	for _, depth := range []int{100, 5_000, 50_000} {
		raw := strings.Repeat(`{"not":`, depth) + `{"title":"a"}` + strings.Repeat(`}`, depth)

		// The whole-document door: json.Unmarshal draws its own line.
		var req query.Request
		docErr := json.Unmarshal([]byte(`{"filter":`+raw+`}`), &req)

		// And the query-string door, where the document is kept raw until the
		// compiler walks it.
		_, _, rawErr := tryReq(t, &query.Request{Filter: query.RawFilter(raw)}, nil)
		if rawErr == nil {
			t.Fatalf("a filter nested %d deep compiled", depth)
		}
		if docErr == nil {
			if _, err := req.Compile(Articles.Meta(), nil); err == nil {
				t.Fatalf("a request nested %d deep compiled", depth)
			}
		}
	}
}

// Two clauses that mean different things must not be able to collide into one.
// The filter, the flat terms and the search each become their own predicate,
// and crud.Where ANDs them — so no amount of nesting in one can reach around
// another.
func TestOneClauseCannotEscapeAnother(t *testing.T) {
	sql, args, err := tryDoc(t, `{
		"filter": {"or": [{"title": "a"}, {"body": "b"}]},
		"terms": [{"path": "views", "op": "gte", "values": ["10"]}],
		"search": "go", "searchFields": ["title", "body"]
	}`, nil)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	want := `(("title" = $1 OR "body" = $2) AND "views" >= $3 AND ("title" LIKE $4 OR "body" LIKE $5))`
	if got := where(sql); got != want {
		t.Fatalf("where = %s\nwant  = %s", got, want)
	}
	if len(args) != 5 {
		t.Fatalf("args = %#v", args)
	}

	// The same for a negated group: NOT wraps the whole of what it was given,
	// so a nested or cannot leak a branch out of it.
	sql, _, err = tryDoc(t, `{"filter":{"not":{"or":[{"title":"a"},{"body":"b"}]},"views":1}}`, nil)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	want = `(NOT (("title" = $1 OR "body" = $2)) AND "views" = $3)`
	if got := where(sql); got != want {
		t.Fatalf("where = %s\nwant  = %s", got, want)
	}
}

// A rejected document compiles to nothing at all — not to the options it had
// managed to build before the bad clause. A transport that logs the error and
// carries on would otherwise run a request nobody wrote: the good half of a
// filter, without the half that narrowed it.
func TestARejectedDocumentCompilesToNoOptions(t *testing.T) {
	for _, doc := range []string{
		`{"filter":{"title":"a","nope":1}}`,
		`{"filter":{"title":"a"},"sort":["nope"]}`,
		`{"filter":{"title":"a"},"select":["nope"]}`,
		`{"filter":{"title":"a"},"preload":["nope"]}`,
		`{"filter":{"title":"a"},"terms":[{"path":"nope","values":["1"]}]}`,
		`{"filter":{"title":"a"},"search":"go","searchFields":["nope"]}`,
		`{"limit":5,"filter":{"views":"lots"}}`,
	} {
		var req query.Request
		if err := json.Unmarshal([]byte(doc), &req); err != nil {
			t.Fatalf("decode: %v", err)
		}
		opts, err := req.Compile(Articles.Meta(), nil)
		if err == nil {
			t.Fatalf("%s compiled", doc)
		}
		if opts != nil {
			t.Fatalf("%s was rejected but still handed back %d options", doc, len(opts))
		}
	}
}
