package query_test

import (
	"encoding/json"
	"net/url"
	"strings"
	"testing"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/query"
)

func FuzzCompileJSON(f *testing.F) {
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

		`{"filter":{"title":{"eq":"' OR 1=1 --"}}}`,
		`{"filter":{"title":{"eq":"\"; DROP TABLE articles; --"}}}`,
		`{"filter":{"title; DROP TABLE articles":"x"}}`,
		`{"sort":["title; DROP TABLE articles"]}`,
		`{"select":["*"]}`,
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, doc string) {
		var request query.Request
		if err := json.Unmarshal([]byte(doc), &request); err != nil {
			return
		}

		options, err := request.Compile(Articles.Meta(), nil)
		if err != nil {
			if len(options) != 0 {
				t.Fatalf("a refusal came back with %d options, so a transport could log the error and run the good half:\n%s",
					len(options), doc)
			}
			return
		}

		o := crud.Build(options...)
		sql, _, rerr := crud.NewSQL(crud.Postgres{}, Articles.Meta()).Predicate(o.Predicate()).Done()
		if rerr != nil {
			t.Fatalf("a compiled filter would not render (%v):\n%s", rerr, doc)
		}
		assertNothingTheCallerWroteIsInTheSQL(t, doc, sql)
	})
}

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
		request, err := query.ParseQuery(v)
		if err != nil {
			return
		}
		options, err := request.Compile(Articles.Meta(), nil)
		if err != nil {
			if len(options) != 0 {
				t.Fatalf("a refusal came back with %d options:\n%s", len(options), raw)
			}
			return
		}
		o := crud.Build(options...)
		sql, _, rerr := crud.NewSQL(crud.Postgres{}, Articles.Meta()).Predicate(o.Predicate()).Done()
		if rerr != nil {
			t.Fatalf("a compiled filter would not render (%v):\n%s", rerr, raw)
		}
		assertNothingTheCallerWroteIsInTheSQL(t, raw, sql)
	})
}

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
