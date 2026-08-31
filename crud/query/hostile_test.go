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

func quoted(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		panic(err)
	}
	return string(b)
}

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

func TestAPreloadsOwnFilterBindsItsValue(t *testing.T) {
	const payload = `x'; DROP TABLE comments --`
	rec := crudtest.Postgres().Push(
		crudtest.Rows(articleRow(1, 10, "first")),
		crudtest.Rows(),
	)
	var request query.Request
	doc := `{"preload":[{"path":"comments","filter":{"body":` + quoted(payload) + `}}]}`
	if err := json.Unmarshal([]byte(doc), &request); err != nil {
		t.Fatalf("decode: %v", err)
	}
	options, err := request.Compile(Articles.Meta(), nil)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if _, err := Articles.Bind(rec).GetAll(context.Background(), options...); err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(rec.Statements()) != 2 {
		t.Fatalf("%d statements, want the page and its preload:\n%s", len(rec.SQL()), strings.Join(rec.SQL(), "\n"))
	}
	st := rec.Statements()[1]
	want := `SELECT "id", "article_id", "author_id", "body", "approved" FROM "comments" ` +
		`WHERE "article_id" IN ($1) AND "body" = $2 LIMIT 1001`
	if got := crudtest.Normalize(st.SQL); got != want {
		t.Fatalf("preload = %s\nwant %s", got, want)
	}
	if len(st.Args) != 2 || st.Args[1] != payload {
		t.Fatalf("preload bound %#v, want the payload as its second argument", st.Args)
	}
}

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
			if !strings.Contains(sql, "LIKE ") {
				t.Fatalf("sql = %s, want a bound pattern", sql)
			}
			if len(args) != 1 || args[0] != tc.want {
				t.Fatalf("pattern = %#v, want %q", args, tc.want)
			}
		})
	}
}

func TestADeniedColumnStaysDeniedHoweverItIsSpelled(t *testing.T) {
	config := &query.Config{
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
			sql, _, err := tryDoc(t, doc, config)
			if err == nil {
				t.Fatalf("%s slipped past the allow-list as %q and built\n  %s", doc, spelling, sql)
			}
		}
	}

	for _, doc := range []string{
		`{"filter":{"comments.body":"x"}}`,
		`{"sort":["author.name"]}`,
		`{"preload":["comments"]}`,
		`{"search":"go","searchFields":["author.name"]}`,
	} {
		if sql, _, err := tryDoc(t, doc, config); err == nil {
			t.Fatalf("%s walked to an unlisted relation and built\n  %s", doc, sql)
		}
	}
}

func TestAPreloadableRelationIsNotAFilterableOne(t *testing.T) {
	config := &query.Config{Filterable: []string{"Title"}, Preloadable: []string{"Comments"}}
	sql, _, err := tryDoc(t, `{"preload":["comments"]}`, config)
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
		if _, _, err := tryDoc(t, doc, config); err == nil {
			t.Fatalf("%s filtered a relation on a column no list named", doc)
		} else if !strings.Contains(err.Error(), "not filterable") {
			t.Fatalf("error = %q, want it to say the column is not filterable", err)
		}
	}
}

func TestSearchCannotReachOutsideItsList(t *testing.T) {
	config := &query.Config{Searchable: []string{"Title"}}
	sql, args, err := tryDoc(t, `{"search":"go"}`, config)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if got := where(sql); got != `LOWER("title") LIKE LOWER($1) ESCAPE '\'` || len(args) != 1 || args[0] != "%go%" {
		t.Fatalf("an unqualified search built %s %#v, want only the searchable column", got, args)
	}
	for _, doc := range []string{
		`{"search":"go","searchFields":["body"]}`,
		`{"search":"go","searchFields":["title","body"]}`,
		`{"search":"go","searchFields":["author.name"]}`,
	} {
		if _, _, err := tryDoc(t, doc, config); err == nil {
			t.Fatalf("%s searched a column outside the list", doc)
		}
	}
}

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

	two := `{"preload":[{"path":"comments","filter":{"approved":true}},{"path":"tags","filter":{"slug":"go"}}]}`
	if _, _, err := tryDoc(t, two, &query.Config{MaxConditions: 2}); err != nil {
		t.Fatalf("two preloads with one comparison each, at a budget of 2: %v", err)
	}
	if _, _, err := tryDoc(t, two, &query.Config{MaxConditions: 1}); err == nil {
		t.Fatal("two preloads spent two conditions against a budget of one")
	}
}

func TestADocumentTooDeepToParseIsRejectedNotFatal(t *testing.T) {
	for _, depth := range []int{100, 5_000, 50_000} {
		raw := strings.Repeat(`{"not":`, depth) + `{"title":"a"}` + strings.Repeat(`}`, depth)

		var request query.Request
		docErr := json.Unmarshal([]byte(`{"filter":`+raw+`}`), &request)

		_, _, rawErr := tryReq(t, &query.Request{Filter: query.RawFilter(raw)}, nil)
		if rawErr == nil {
			t.Fatalf("a filter nested %d deep compiled", depth)
		}
		if docErr == nil {
			if _, err := request.Compile(Articles.Meta(), nil); err == nil {
				t.Fatalf("a request nested %d deep compiled", depth)
			}
		}
	}
}

func TestOneClauseCannotEscapeAnother(t *testing.T) {
	sql, args, err := tryDoc(t, `{
		"filter": {"or": [{"title": "a"}, {"body": "b"}]},
		"terms": [{"path": "views", "op": "gte", "values": ["10"]}],
		"search": "go", "searchFields": ["title", "body"]
	}`, nil)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	want := `(("title" = $1 OR "body" = $2) AND "views" >= $3 AND (LOWER("title") LIKE LOWER($4) ESCAPE '\' OR LOWER("body") LIKE LOWER($5) ESCAPE '\'))`
	if got := where(sql); got != want {
		t.Fatalf("where = %s\nwant  = %s", got, want)
	}
	if len(args) != 5 {
		t.Fatalf("args = %#v", args)
	}

	sql, _, err = tryDoc(t, `{"filter":{"not":{"or":[{"title":"a"},{"body":"b"}]},"views":1}}`, nil)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	want = `(NOT (("title" = $1 OR "body" = $2)) AND "views" = $3)`
	if got := where(sql); got != want {
		t.Fatalf("where = %s\nwant  = %s", got, want)
	}
}

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
		var request query.Request
		if err := json.Unmarshal([]byte(doc), &request); err != nil {
			t.Fatalf("decode: %v", err)
		}
		options, err := request.Compile(Articles.Meta(), nil)
		if err == nil {
			t.Fatalf("%s compiled", doc)
		}
		if options != nil {
			t.Fatalf("%s was rejected but still handed back %d options", doc, len(options))
		}
	}
}
