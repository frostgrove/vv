package query_test

import (
	"encoding/json"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/crudtest"
	"github.com/frostgrove/vv/crud/query"
)

func compile(t *testing.T, doc string, config *query.Config) []crud.Option {
	t.Helper()
	var request query.Request
	if err := json.Unmarshal([]byte(doc), &request); err != nil {
		t.Fatalf("decode %s: %v", doc, err)
	}
	options, err := request.Compile(Articles.Meta(), config)
	if err != nil {
		t.Fatalf("compile %s: %v", doc, err)
	}
	return options
}

func resolve(t *testing.T, doc string, config *query.Config) *crud.Options {
	t.Helper()
	return crud.Build(compile(t, doc, config)...)
}

func clause(t *testing.T, p crud.Predicate) string {
	t.Helper()
	b := crud.NewSQL(crud.Postgres{}, Articles.Meta()).Predicate(p)
	if err := b.Err(); err != nil {
		t.Fatalf("render: %v", err)
	}
	return crudtest.Normalize(b.String())
}

func TestCompileMapsEveryKnobOntoOptions(t *testing.T) {
	o := resolve(t, `{
		"page": 3, "limit": 20, "offset": 40,
		"sort": ["-views", "title"],
		"select": ["title", "views"],
		"preload": ["author", "comments.author"],
		"filter": {"views": {"gte": 10}},
		"terms": [{"path": "title", "op": "contains", "values": ["go"]}],
		"search": "rust", "searchFields": ["body"],
		"unpaged": true, "skipTotal": true, "distinct": true
	}`, exports)

	wantFilters := []string{`"views" >= $1`, `"title" LIKE $1 ESCAPE '\'`, `LOWER("body") LIKE LOWER($1) ESCAPE '\'`}
	if len(o.Filter) != len(wantFilters) {
		t.Fatalf("%d predicates, want %d (filter, terms, search)", len(o.Filter), len(wantFilters))
	}
	for i, want := range wantFilters {
		if got := clause(t, o.Filter[i]); got != want {
			t.Fatalf("predicate %d = %s, want %s", i, got, want)
		}
	}

	wantSort := []crud.Order{{Field: "Views", Desc: true}, {Field: "Title"}}
	if len(o.Sort) != len(wantSort) {
		t.Fatalf("sort = %+v, want %+v", o.Sort, wantSort)
	}
	for i, want := range wantSort {
		if o.Sort[i] != want {
			t.Fatalf("sort %d = %+v, want %+v", i, o.Sort[i], want)
		}
	}

	if got := strings.Join(o.Fields, ","); got != "Title,Views" {
		t.Fatalf("projection = %v, want the canonical [Title Views]", o.Fields)
	}
	if len(o.Preloads) != 2 || o.Preloads[0].Path != "Author" || o.Preloads[1].Path != "Comments.Author" {
		t.Fatalf("preloads = %+v, want the canonical Author and Comments.Author", o.Preloads)
	}
	if o.Page != 3 || o.Limit != 20 || o.Offset != 40 {
		t.Fatalf("paging = page %d, limit %d, offset %d; want 3/20/40", o.Page, o.Limit, o.Offset)
	}
	if !o.Unpaged || !o.NoTotal || !o.Distinct {
		t.Fatalf("flags = unpaged %v, noTotal %v, distinct %v; want all true", o.Unpaged, o.NoTotal, o.Distinct)
	}

	if o.ForUpdate || o.NoSort {
		t.Fatalf("a request set forUpdate %v / noSort %v; neither is reachable from the wire",
			o.ForUpdate, o.NoSort)
	}
}

func TestAbsentKnobsProduceTheEndpointPageCap(t *testing.T) {
	for _, doc := range []string{
		`{}`,
		`{"page":0,"limit":0,"offset":0}`,
		`{"sort":[],"select":[],"preload":[],"terms":[]}`,
		`{"search":"   "}`,
		`{"unpaged":false,"skipTotal":false,"distinct":false}`,
	} {
		if o := crud.Build(compile(t, doc, nil)...); o.Limit != 100 {
			t.Fatalf("%s produced limit %d, want the endpoint default 100", doc, o.Limit)
		}
	}

	var absent *query.Request
	options, err := absent.Compile(Articles.Meta(), nil)
	if err != nil || options != nil {
		t.Fatalf("a nil request compiled to %v, %v; want nothing at all", options, err)
	}
}

func TestAnEmptyRequestUsesTheEndpointPageCap(t *testing.T) {
	sql, _ := run(t, `{}`, nil)
	if !strings.Contains(sql, "LIMIT 100") {
		t.Fatalf("sql = %s\nwant the endpoint default LIMIT 100", sql)
	}
}

func TestPagingAlwaysUsesTheEndpointLimit(t *testing.T) {
	var request query.Request
	if err := json.Unmarshal([]byte(`{"page":2}`), &request); err != nil {
		t.Fatal(err)
	}
	if o, err := request.Compile(Articles.Meta(), &query.Config{MaxLimit: 1, MaxOffset: 10_000}); err != nil {
		t.Fatal(err)
	} else if got := crud.Build(o...); got.Limit != 1 || got.Page != 2 {
		t.Fatalf("page options = %+v, want page 2 capped to one row", got)
	}

	if err := json.Unmarshal([]byte(`{"after":"opaque","limit":50,"sort":["id"]}`), &request); err != nil {
		t.Fatal(err)
	}
	o, err := request.Compile(Articles.Meta(), &query.Config{MaxLimit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if got := crud.Build(o...); got.Limit != 10 || got.After != "opaque" {
		t.Fatalf("cursor options = %+v, want its client limit clamped to 10", got)
	}
}

func TestBothCursorDirectionsAreRefusedOnEveryRequestShape(t *testing.T) {
	var request query.Request
	if err := json.Unmarshal([]byte(`{"after":"newer","before":"older"}`), &request); err != nil {
		t.Fatal(err)
	}
	if _, err := request.Compile(Articles.Meta(), nil); err == nil || !strings.Contains(err.Error(), "cannot be combined") {
		t.Fatalf("err = %v, want an after/before conflict", err)
	}

	if err := json.Unmarshal([]byte(`{"after":"","before":"older"}`), &request); err != nil {
		t.Fatal(err)
	}
	if _, err := request.Compile(Articles.Meta(), nil); err == nil || !strings.Contains(err.Error(), "after") {
		t.Fatalf("err = %v, want an explicit empty after rejected like ?after=", err)
	}
}

func TestPageDepthCannotOverflowPastTheEndpointBudget(t *testing.T) {
	var request query.Request
	if err := json.Unmarshal([]byte(`{"page":9223372036854775807}`), &request); err != nil {
		t.Fatal(err)
	}
	if _, err := request.Compile(Articles.Meta(), &query.Config{MaxLimit: 100, MaxOffset: 10_000}); err == nil || !strings.Contains(err.Error(), "page") {
		t.Fatalf("err = %v, want a page-depth refusal", err)
	}
}

func TestSelectHasABudgetAndCanonicalDeduplication(t *testing.T) {
	var request query.Request
	if err := json.Unmarshal([]byte(`{"select":["title","Title","title"]}`), &request); err != nil {
		t.Fatal(err)
	}
	if _, err := request.Compile(Articles.Meta(), &query.Config{MaxSelect: 2}); err == nil {
		t.Fatal("a repeated projection exceeded no input budget")
	}
	o, err := request.Compile(Articles.Meta(), &query.Config{MaxSelect: 3})
	if err != nil {
		t.Fatal(err)
	}
	if fields := crud.Build(o...).Fields; len(fields) != 1 || fields[0] != "Title" {
		t.Fatalf("projection = %v, want one canonical Title", fields)
	}
}

func TestFilterTermsAndSearchAreAndedNotMerged(t *testing.T) {
	sql, _ := run(t, `{
		"filter": {"views": {"gte": 10}},
		"terms": [{"path": "authorId", "values": ["7"]}],
		"search": "go", "searchFields": ["title", "body"]
	}`, nil)
	want := `("views" >= $1 AND "author_id" = $2 AND (LOWER("title") LIKE LOWER($3) ESCAPE '\' OR LOWER("body") LIKE LOWER($4) ESCAPE '\'))`
	if got := where(sql); got != want {
		t.Fatalf("where = %s\nwant  = %s", got, want)
	}
}

func TestSortNullsPlacement(t *testing.T) {
	for _, tc := range []struct {
		name string
		doc  string
		want crud.Order
		sql  string
	}{
		{"unspecified", `{"sort":[{"field":"publishedAt"}]}`,
			crud.Order{Field: "PublishedAt"}, `"published_at" ASC`},
		{"first", `{"sort":[{"field":"publishedAt","nulls":"first"}]}`,
			crud.Order{Field: "PublishedAt", NullsSet: true}, `"published_at" ASC NULLS FIRST`},
		{"last", `{"sort":[{"field":"publishedAt","nulls":"LAST","desc":true}]}`,
			crud.Order{Field: "PublishedAt", Desc: true, NullsLast: true, NullsSet: true},
			`"published_at" DESC NULLS LAST`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := resolve(t, tc.doc, nil)
			if len(o.Sort) != 1 || o.Sort[0] != tc.want {
				t.Fatalf("sort = %+v, want %+v", o.Sort, tc.want)
			}
			sql, _ := run(t, tc.doc+"", nil)
			if !strings.Contains(sql, tc.sql) {
				t.Fatalf("sql = %s\nwant it to contain %s", sql, tc.sql)
			}
		})
	}
	mustFail(t, `{"sort":[{"field":"title","nulls":"middle"}]}`, nil, "nulls must be first or last")
}

func TestAllowListMatching(t *testing.T) {
	for _, tc := range []struct {
		name   string
		config *query.Config
		doc    string
		ok     bool
	}{
		{"empty list allows anything", &query.Config{}, `{"filter":{"views":1}}`, true},
		{"exact entry", &query.Config{Filterable: []string{"Views"}}, `{"filter":{"views":1}}`, true},
		{"entry is matched case-insensitively",
			&query.Config{Filterable: []string{"views"}}, `{"filter":{"Views":1}}`, true},
		{"the client's spelling does not matter",
			&query.Config{Filterable: []string{"PublishedAt"}}, `{"filter":{"published_at":null}}`, true},
		{"the declaration spelling does not matter",
			&query.Config{Filterable: []string{"published_at"}}, `{"filter":{"publishedAt":null}}`, true},
		{"star allows anything", &query.Config{Filterable: []string{"*"}}, `{"filter":{"views":1}}`, true},
		{"a field outside the list is refused",
			&query.Config{Filterable: []string{"Title"}}, `{"filter":{"views":1}}`, false},
		{"a subtree entry allows a nested path",
			&query.Config{Filterable: []string{"Comments.*"}}, `{"filter":{"comments.body":"x"}}`, true},
		{"a subtree entry does not allow a sibling",
			&query.Config{Filterable: []string{"Comments.*"}}, `{"filter":{"author.name":"x"}}`, false},
		{"a subtree entry does not allow the root's own columns",
			&query.Config{Filterable: []string{"Comments.*"}}, `{"filter":{"title":"x"}}`, false},
		{"a subtree entry allows the relation itself",
			&query.Config{Preloadable: []string{"Comments.*"}}, `{"preload":["comments"]}`, true},
		{"a subtree entry allows a deeper preload",
			&query.Config{Preloadable: []string{"Comments.*"}}, `{"preload":["comments.author"]}`, true},
		{"a prefix that is not a path segment does not match",
			&query.Config{Filterable: []string{"Com.*"}}, `{"filter":{"comments.body":"x"}}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var request query.Request
			if err := json.Unmarshal([]byte(tc.doc), &request); err != nil {
				t.Fatal(err)
			}
			_, err := request.Compile(Articles.Meta(), tc.config)
			if tc.ok && err != nil {
				t.Fatalf("%s was refused: %v", tc.doc, err)
			}
			if !tc.ok && err == nil {
				t.Fatalf("%s was allowed", tc.doc)
			}
		})
	}
}

func TestEachAllowListGuardsItsOwnVerb(t *testing.T) {
	config := &query.Config{
		Filterable:  []string{"Title"},
		Sortable:    []string{"Title"},
		Selectable:  []string{"Title"},
		Preloadable: []string{"Author"},
		Searchable:  []string{"Title"},
	}
	for _, tc := range []struct{ name, doc, want string }{
		{"filter", `{"filter":{"views":1}}`, "Views is not filterable"},
		{"sort", `{"sort":["views"]}`, "Views is not sortable"},
		{"select", `{"select":["views"]}`, "Views cannot be selected"},
		{"preload", `{"preload":["comments"]}`, "Comments cannot be preloaded"},
		{"search", `{"search":"go","searchFields":["body"]}`, "Body is not searchable"},
	} {
		t.Run(tc.name, func(t *testing.T) { mustFail(t, tc.doc, config, tc.want) })
	}

	sql, args := run(t, `{"filter":{"title":"a"},"sort":["title"],"select":["title"],
		"preload":["author"],"search":"go","searchFields":["title"]}`, config)

	want := `SELECT "id", "title", "author_id" FROM "articles" WHERE ("title" = $1 AND LOWER("title") LIKE LOWER($2) ESCAPE '\') ` +
		`ORDER BY "title" ASC, "id" ASC LIMIT 100`
	if sql != want {
		t.Fatalf("sql  = %s\nwant = %s", sql, want)
	}
	if len(args) != 2 || args[0] != "a" || args[1] != "%go%" {
		t.Fatalf("args = %#v, want the filter's value then the search term", args)
	}
}

func TestSearchUsesDefaultSearchFields(t *testing.T) {
	config := &query.Config{DefaultSearchFields: []string{"Title"}}
	sql, args := run(t, `{"search":"go"}`, config)
	if got := where(sql); got != `LOWER("title") LIKE LOWER($1) ESCAPE '\'` {
		t.Fatalf("where = %s, want only the default field", got)
	}
	if args[0] != "%go%" {
		t.Fatalf("args = %v", args)
	}

	sql, _ = run(t, `{"search":"go","searchFields":["body"]}`, config)
	if got := where(sql); got != `LOWER("body") LIKE LOWER($1) ESCAPE '\'` {
		t.Fatalf("where = %s, want the requested field", got)
	}

	mustFail(t, `{"search":"go"}`,
		&query.Config{DefaultSearchFields: []string{"Body"}, Searchable: []string{"Title"}},
		"Body is not searchable")
}

func TestSearchJoinsNonTextColumnsOnlyWhenTheTermFits(t *testing.T) {
	config := &query.Config{DefaultSearchFields: []string{"Title", "Views"}}
	sql, args := run(t, `{"search":"42"}`, config)
	if got := where(sql); got != `(LOWER("title") LIKE LOWER($1) ESCAPE '\' OR "views" = $2)` {
		t.Fatalf("where = %s, want the numeric column to join in", got)
	}
	if _, ok := args[1].(int); !ok {
		t.Fatalf("views bound as %T, want int", args[1])
	}
	sql, _ = run(t, `{"search":"go"}`, config)
	if got := where(sql); got != `LOWER("title") LIKE LOWER($1) ESCAPE '\'` {
		t.Fatalf("where = %s, want the numeric column left out", got)
	}
}

func TestSearchWithNothingToSearchIsRefused(t *testing.T) {
	var request query.Request
	if err := json.Unmarshal([]byte(`{"search":"go"}`), &request); err != nil {
		t.Fatal(err)
	}
	if _, err := request.Compile(Articles.Meta(), &query.Config{Searchable: []string{"Views"}}); err == nil {
		t.Fatal("a search with no usable fields compiled into an unfiltered read")
	}
}

func TestPreloadOptionsTravelWithTheRelation(t *testing.T) {
	o := resolve(t, `{"preload":[{"path":"comments","filter":{"approved":true},"sort":["-body"]}]}`, nil)
	if len(o.Preloads) != 1 || o.Preloads[0].Path != "Comments" {
		t.Fatalf("preloads = %+v", o.Preloads)
	}
	sub := crud.Build(o.Preloads[0].Opts...)
	if len(sub.Filter) != 1 || len(sub.Sort) != 1 || o.Preloads[0].MaxRows != 1000 {
		t.Fatalf("preload options = %d filters, %d sorts; want 1 and 1", len(sub.Filter), len(sub.Sort))
	}
	if sub.Sort[0] != (crud.Order{Field: "Body", Desc: true}) {
		t.Fatalf("preload sort = %+v", sub.Sort[0])
	}

	o = resolve(t, `{"preload":["comments"]}`, nil)
	if o.Preloads[0].MaxRows != 1000 || len(o.Preloads[0].Opts) != 0 {
		t.Fatalf("an unqualified preload = %+v, want a direct row cap and no fake narrowing", o.Preloads[0])
	}
}

func TestPreloadCanTightenButNotWidenTheEndpointRowCap(t *testing.T) {
	for _, tc := range []struct {
		name   string
		doc    string
		config *query.Config
		want   int
	}{
		{
			name:   "client tightens",
			doc:    `{"preload":[{"path":"comments","maxRows":1}]}`,
			config: &query.Config{MaxPreloadRows: 5},
			want:   1,
		},
		{
			name:   "endpoint remains the ceiling",
			doc:    `{"preload":[{"path":"comments","maxRows":6}]}`,
			config: &query.Config{MaxPreloadRows: 5},
			want:   5,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := resolve(t, tc.doc, tc.config)
			if len(o.Preloads) != 1 || o.Preloads[0].MaxRows != tc.want {
				t.Fatalf("preload caps = %+v, want %d", o.Preloads, tc.want)
			}
			if sub := crud.Build(o.Preloads[0].Opts...); sub.PreloadRows != 0 {
				t.Fatalf("preload cap was redundantly encoded as a nested option: %+v", sub)
			}
		})
	}

	var request query.Request
	if err := json.Unmarshal([]byte(`{"preload":[{"path":"comments","maxRows":-1}]}`), &request); err != nil {
		t.Fatal(err)
	}
	if _, err := request.Compile(Articles.Meta(), nil); err == nil || !strings.Contains(err.Error(), "maxRows") {
		t.Fatalf("negative preload cap = %v, want a maxRows refusal", err)
	}
}

func TestKeyOrderDoesNotFollowTheDocument(t *testing.T) {
	forwards, _ := run(t, `{"filter":{"authorId":1,"title":"a","views":{"gte":2,"lt":9}}}`, nil)
	backwards, _ := run(t, `{"filter":{"views":{"lt":9,"gte":2},"title":"a","authorId":1}}`, nil)
	if forwards != backwards {
		t.Fatalf("key order changed the statement:\n%s\n%s", forwards, backwards)
	}

	want := `("author_id" = $1 AND "title" = $2 AND "views" >= $3 AND "views" < $4)`
	if got := where(forwards); got != want {
		t.Fatalf("where = %s\nwant  = %s", got, want)
	}
}

func TestMaxDepthBoundsPathLength(t *testing.T) {
	config := &query.Config{MaxDepth: 2}
	sql, _ := run(t, `{"filter":{"comments.body":"x"}}`, config)
	want := `EXISTS (SELECT 1 FROM "comments" AS rx1 WHERE rx1."article_id" = "articles"."id" AND rx1."body" = $1)`
	if got := where(sql); got != want {
		t.Fatalf("a two-segment path at MaxDepth 2 compiled to\n  %s\nwant\n  %s", got, want)
	}
	mustFail(t, `{"filter":{"comments.author.name":"x"}}`, config, "deeper than the allowed 2 segments")
	mustFail(t, `{"sort":["comments.author.name"]}`, config, "deeper than the allowed 2 segments")
}

func TestConditionBudgetCountsEveryComparison(t *testing.T) {
	for _, tc := range []struct{ name, doc string }{
		{"operator objects", `{"filter":{"views":{"gte":1,"lt":9}}}`},
		{"shorthand equalities", `{"filter":{"title":"a","body":"b"}}`},
		{"shorthand lists", `{"filter":{"title":["a"],"body":["b"]}}`},
		{"across the branches of an or", `{"filter":{"or":[{"title":"a"},{"body":"b"}]}}`},
		{"flat terms", `{"terms":[{"path":"title","values":["a"]},{"path":"body","values":["b"]}]}`},
		{"one of each form", `{"filter":{"title":"a"},"terms":[{"path":"body","values":["b"]}]}`},
		{"a filter plus search", `{"filter":{"title":"a"},"search":"go","searchFields":["body"]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mustFail(t, tc.doc, &query.Config{MaxConditions: 1}, "at most 1 conditions")

			_, args := run(t, tc.doc, &query.Config{MaxConditions: 2})
			if len(args) != 2 {
				t.Fatalf("at a budget of 2 the document bound %#v, want its two comparisons", args)
			}
		})
	}
}

func TestARequestSurvivesBeingWrittenBackOutAsJSON(t *testing.T) {
	const doc = `{
		"page": 3, "limit": 20, "offset": 40,
		"sort": ["-views", "title"],
		"select": ["title", "views"],
		"preload": ["author", "comments.author"],
		"filter": {"views": {"gte": 10}, "author.name": "Ann"},
		"terms": [{"path": "title", "op": "contains", "values": ["go"]}],
		"search": "rust", "searchFields": ["body"],
		"skipTotal": true, "distinct": true
	}`

	var first query.Request
	if err := json.Unmarshal([]byte(doc), &first); err != nil {
		t.Fatal(err)
	}
	out, err := json.Marshal(first)
	if err != nil {
		t.Fatalf("a Request could not be written back out: %v", err)
	}
	var second query.Request
	if err := json.Unmarshal(out, &second); err != nil {
		t.Fatalf("the document vv wrote is not one it can read: %v\n%s", err, out)
	}

	before, after := crud.Build(compileReq(t, &first)...), crud.Build(compileReq(t, &second)...)
	if before.Page != after.Page || before.Limit != after.Limit || before.Offset != after.Offset ||
		before.Unpaged != after.Unpaged || before.NoTotal != after.NoTotal || before.Distinct != after.Distinct {
		t.Fatalf("paging and flags changed across the round trip:\n%+v\n%+v", before, after)
	}
	if len(before.Filter) != len(after.Filter) {
		t.Fatalf("%d predicates became %d: the filter document did not survive being re-encoded\n%s",
			len(before.Filter), len(after.Filter), out)
	}
	for i := range before.Filter {
		if clause(t, before.Filter[i]) != clause(t, after.Filter[i]) {
			t.Fatalf("predicate %d changed:\n  %s\n  %s", i, clause(t, before.Filter[i]), clause(t, after.Filter[i]))
		}
	}
	if len(before.Sort) != len(after.Sort) || before.Sort[0] != after.Sort[0] {
		t.Fatalf("sort changed: %+v -> %+v", before.Sort, after.Sort)
	}
	if len(before.Preloads) != len(after.Preloads) || before.Preloads[1].Path != after.Preloads[1].Path {
		t.Fatalf("preloads changed: %+v -> %+v", before.Preloads, after.Preloads)
	}
	if strings.Join(before.Fields, ",") != strings.Join(after.Fields, ",") {
		t.Fatalf("projection changed: %v -> %v", before.Fields, after.Fields)
	}
}

func compileReq(t *testing.T, request *query.Request) []crud.Option {
	t.Helper()
	options, err := request.Compile(Articles.Meta(), exports)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	return options
}

func TestUnpagedIsRefusedUnlessTheEndpointServesIt(t *testing.T) {
	var request query.Request
	if err := json.Unmarshal([]byte(`{"unpaged":true}`), &request); err != nil {
		t.Fatal(err)
	}

	for _, config := range []*query.Config{nil, {}, {Filterable: []string{"*"}}} {
		if _, err := request.Compile(Articles.Meta(), config); err == nil {
			t.Fatalf("unpaged was accepted on an endpoint that never declared it: %+v", config)
		}
	}

	o := resolve(t, `{"unpaged":true}`, &query.Config{AllowUnpaged: true})
	if !o.Unpaged {
		t.Fatal("an endpoint that declared AllowUnpaged did not get unpaged results")
	}

	_, err := request.Compile(Articles.Meta(), nil)
	var qe *query.Error
	if !errors.As(err, &qe) || qe.Path != "unpaged" {
		t.Fatalf("the refusal is not a query.Error naming the parameter: %#v", err)
	}

	for _, spelling := range []string{"unpaged", "all"} {
		v, verr := url.ParseQuery(spelling + "=1")
		if verr != nil {
			t.Fatal(verr)
		}
		qr, perr := query.ParseQuery(v)
		if perr != nil {
			t.Fatal(perr)
		}
		if !qr.Unpaged {
			t.Fatalf("?%s=1 did not set the unpaged flag at all", spelling)
		}
		_, cerr := qr.Compile(Articles.Meta(), nil)
		var e *query.Error
		if !errors.As(cerr, &e) {
			t.Fatalf("?%s=1 was not refused: %v", spelling, cerr)
		}
		if e.Path != spelling {
			t.Fatalf("?%s=1 was refused at path %q, naming a parameter the client did not send", spelling, e.Path)
		}
	}
}

func TestAClientChosenListAndSortAreBounded(t *testing.T) {
	t.Run("in", func(t *testing.T) {
		big := make([]string, 40)
		for i := range big {
			big[i] = strconv.Itoa(i)
		}
		doc := `{"filter":{"views":{"in":[` + strings.Join(big, ",") + `]}}}`

		config := &query.Config{MaxInValues: 8}
		var request query.Request
		if err := json.Unmarshal([]byte(doc), &request); err != nil {
			t.Fatal(err)
		}
		if _, err := request.Compile(Articles.Meta(), config); err == nil {
			t.Fatal("a 40-value list past a cap of 8 compiled")
		}

		if _, err := request.Compile(Articles.Meta(), &query.Config{MaxConditions: 1}); err != nil {
			t.Fatalf("a 40-value list is charged as more than one condition: %v", err)
		}

		compile(t, `{"filter":{"views":{"in":[1,2,3]}}}`, config)
	})

	t.Run("the shorthand spelling is bounded too", func(t *testing.T) {
		big := make([]string, 40)
		for i := range big {
			big[i] = strconv.Itoa(i)
		}
		doc := `{"filter":{"views":[` + strings.Join(big, ",") + `]}}`
		var request query.Request
		if err := json.Unmarshal([]byte(doc), &request); err != nil {
			t.Fatal(err)
		}
		if _, err := request.Compile(Articles.Meta(), &query.Config{MaxInValues: 8}); err == nil {
			t.Fatal("the array shorthand is not bounded, only the explicit `in` operator is")
		}
	})

	t.Run("the flat-term spelling is bounded too", func(t *testing.T) {
		big := make([]string, 40)
		for i := range big {
			big[i] = strconv.Itoa(i)
		}
		doc := `{"terms":[{"path":"views","op":"in","values":["` + strings.Join(big, `","`) + `"]}]}`
		var request query.Request
		if err := json.Unmarshal([]byte(doc), &request); err != nil {
			t.Fatal(err)
		}
		if _, err := request.Compile(Articles.Meta(), &query.Config{MaxInValues: 8}); err == nil {
			t.Fatal("a 40-value flat term compiled past a cap of 8")
		}

		if _, err := request.Compile(Articles.Meta(), &query.Config{MaxConditions: 1}); err != nil {
			t.Fatalf("a 40-value flat term is charged as more than one condition: %v", err)
		}
	})

	t.Run("the search field list is bounded", func(t *testing.T) {
		fields := make([]string, 40)
		for i := range fields {
			fields[i] = "title"
		}
		var request query.Request
		doc := `{"search":"go","searchFields":["` + strings.Join(fields, `","`) + `"]}`
		if err := json.Unmarshal([]byte(doc), &request); err != nil {
			t.Fatal(err)
		}
		if _, err := request.Compile(Articles.Meta(), &query.Config{MaxSort: 4}); err == nil {
			t.Fatal("40 search fields compiled past a cap of 4")
		}
	})

	t.Run("a repeated search field is searched once", func(t *testing.T) {
		o := resolve(t, `{"search":"go","searchFields":["title","title","body"]}`, nil)
		sql, _, err := crud.NewSQL(crud.Postgres{}, Articles.Meta()).Predicate(o.Predicate()).Done()
		if err != nil {
			t.Fatal(err)
		}
		if n := strings.Count(sql, `LOWER("title") LIKE`); n != 1 {
			t.Fatalf("the same column was searched %d times; the second LIKE matches the same rows and costs the same scan:\n%s", n, sql)
		}
	})

	t.Run("sort", func(t *testing.T) {
		var request query.Request
		if err := json.Unmarshal([]byte(`{"sort":["title","-views","createdAt"]}`), &request); err != nil {
			t.Fatal(err)
		}
		if _, err := request.Compile(Articles.Meta(), &query.Config{MaxSort: 2}); err == nil {
			t.Fatal("three sort terms past a cap of two compiled")
		}

		if _, err := request.Compile(Articles.Meta(), &query.Config{MaxSort: 3}); err != nil {
			t.Fatalf("three sort terms at a cap of three were refused: %v", err)
		}
	})

	t.Run("an identical repeated sort path is rendered once", func(t *testing.T) {
		o := resolve(t, `{"sort":["title","title"]}`, nil)
		n := 0
		for _, ord := range o.Sort {
			if strings.EqualFold(ord.Field, "Title") {
				n++
			}
		}
		if n != 1 {
			t.Fatalf("the same column was sorted %d times; the second term decides nothing and still costs", n)
		}
	})

	t.Run("conflicting repeated sort paths are refused", func(t *testing.T) {
		mustFail(t, `{"sort":["title","-title"]}`, nil, "conflicting order")
	})
}

func TestTheDefaultVolumeBudgetsAreActive(t *testing.T) {
	t.Run("limit is capped", func(t *testing.T) {
		o := resolve(t, `{"limit":1000000}`, nil)
		if o.Limit != 100 {
			t.Fatalf("limit = %d, want the default cap of 100", o.Limit)
		}
	})

	t.Run("offset and page are bounded", func(t *testing.T) {
		for _, doc := range []string{
			`{"offset":10001}`,
			`{"page":102}`,
			`{"page":-1}`,
			`{"limit":-1}`,
		} {
			var request query.Request
			if err := json.Unmarshal([]byte(doc), &request); err != nil {
				t.Fatal(err)
			}
			if _, err := request.Compile(Articles.Meta(), nil); err == nil {
				t.Fatalf("%s escaped the page/offset validation", doc)
			}
		}
	})

	t.Run("distinct needs an endpoint opt-in", func(t *testing.T) {
		var request query.Request
		if err := json.Unmarshal([]byte(`{"distinct":true}`), &request); err != nil {
			t.Fatal(err)
		}
		if _, err := request.Compile(Articles.Meta(), nil); err == nil {
			t.Fatal("distinct was accepted by a stock endpoint")
		}
		if o, err := request.Compile(Articles.Meta(), &query.Config{AllowDistinct: true}); err != nil || !crud.Build(o...).Distinct {
			t.Fatalf("an endpoint that enabled distinct did not receive it: %v", err)
		}
	})

	t.Run("all bind values share one budget", func(t *testing.T) {
		var request query.Request
		if err := json.Unmarshal([]byte(`{"filter":{"views":{"in":[1,2,3]},"authorId":{"in":[4,5,6]}}}`), &request); err != nil {
			t.Fatal(err)
		}
		if _, err := request.Compile(Articles.Meta(), &query.Config{MaxBindValues: 5}); err == nil {
			t.Fatal("two individually valid lists exceeded the document bind budget")
		}
	})
}

func TestACursorCannotCompareAColumnTheEndpointHidesFromFiltering(t *testing.T) {
	config := &query.Config{
		Filterable: []string{"ID", "Title"},
		Sortable:   []string{"ID", "Title", "Views"},
	}

	var asFilter query.Request
	if err := json.Unmarshal([]byte(`{"filter":{"views":{"gt":100}}}`), &asFilter); err != nil {
		t.Fatal(err)
	}
	if _, err := asFilter.Compile(Articles.Meta(), config); err == nil {
		t.Fatal("the control failed: Views is filterable after all, so this test proves nothing")
	}

	var viaCursor query.Request
	if err := json.Unmarshal([]byte(`{"sort":["views","id"],"after":"whatever"}`), &viaCursor); err != nil {
		t.Fatal(err)
	}
	if _, err := viaCursor.Compile(Articles.Meta(), config); err == nil {
		t.Fatal("a cursor reached a column the endpoint hides from filtering")
	}

	var hiddenTiebreaker query.Request
	if err := json.Unmarshal([]byte(`{"sort":["title"],"after":"whatever"}`), &hiddenTiebreaker); err != nil {
		t.Fatal(err)
	}
	noID := &query.Config{Filterable: []string{"Title"}, Sortable: []string{"Title"}}
	if _, err := hiddenTiebreaker.Compile(Articles.Meta(), noID); err == nil || !strings.Contains(err.Error(), "ID") {
		t.Fatalf("err = %v, want the hidden ID tiebreaker refused", err)
	}

	var implicitSort query.Request
	if err := json.Unmarshal([]byte(`{"after":"whatever"}`), &implicitSort); err != nil {
		t.Fatal(err)
	}
	if _, err := implicitSort.Compile(Articles.Meta(), config); err == nil || !strings.Contains(err.Error(), "explicit sort") {
		t.Fatalf("err = %v, want a cursor without reviewed sort refused", err)
	}

	var fine query.Request
	if err := json.Unmarshal([]byte(`{"sort":["title","id"],"after":"whatever"}`), &fine); err != nil {
		t.Fatal(err)
	}
	if _, err := fine.Compile(Articles.Meta(), config); err != nil {
		t.Fatalf("a cursor over filterable columns was refused: %v", err)
	}

	var sortOnly query.Request
	if err := json.Unmarshal([]byte(`{"sort":["views"]}`), &sortOnly); err != nil {
		t.Fatal(err)
	}
	if _, err := sortOnly.Compile(Articles.Meta(), config); err != nil {
		t.Fatalf("sorting by a sortable column was refused when no cursor was involved: %v", err)
	}
}

func TestCursorPredicateConsumesTheSameBudgetsAsItsExpansion(t *testing.T) {
	var request query.Request
	if err := json.Unmarshal([]byte(`{"sort":["title","id"],"after":"opaque"}`), &request); err != nil {
		t.Fatal(err)
	}

	config := &query.Config{Filterable: []string{"Title", "ID"}, Sortable: []string{"Title", "ID"}}
	tooFewConditions := *config
	tooFewConditions.MaxConditions = 2
	if _, err := request.Compile(Articles.Meta(), &tooFewConditions); err == nil || !strings.Contains(err.Error(), "conditions") {
		t.Fatalf("err = %v, want cursor condition budget refusal", err)
	}
	tooFewBinds := *config
	tooFewBinds.MaxBindValues = 2
	if _, err := request.Compile(Articles.Meta(), &tooFewBinds); err == nil || !strings.Contains(err.Error(), "bound values") {
		t.Fatalf("err = %v, want cursor bind budget refusal", err)
	}
	withinBudget := *config
	withinBudget.MaxConditions, withinBudget.MaxBindValues = 3, 3
	if _, err := request.Compile(Articles.Meta(), &withinBudget); err != nil {
		t.Fatalf("cursor exactly at its expansion budget was refused: %v", err)
	}
}

func TestCursorCannotPromiseAPositionInARelationSort(t *testing.T) {
	var request query.Request
	if err := json.Unmarshal([]byte(`{"sort":["author.name"],"after":"opaque"}`), &request); err != nil {
		t.Fatal(err)
	}
	config := &query.Config{
		Filterable: []string{"Author.Name", "ID"},
		Sortable:   []string{"Author.Name"},
	}
	if _, err := request.Compile(Articles.Meta(), config); err == nil || !strings.Contains(err.Error(), "relation") {
		t.Fatalf("err = %v, want relation-sort cursor refusal", err)
	}
}

func TestAnAllowListEntryThatNamesNothingIsRefusedAtDeclaration(t *testing.T) {
	m := Articles.Meta()

	for _, tc := range []struct {
		name   string
		config *query.Config
	}{
		{"filterable", &query.Config{Filterable: []string{"Nope"}}},
		{"sortable", &query.Config{Sortable: []string{"Nope"}}},
		{"selectable", &query.Config{Selectable: []string{"Nope"}}},
		{"searchable", &query.Config{Searchable: []string{"Nope"}}},
		{"defaultSearchFields", &query.Config{DefaultSearchFields: []string{"Nope"}}},
		{"preloadable", &query.Config{Preloadable: []string{"Nope"}}},
		{"preloadable naming a column", &query.Config{Preloadable: []string{"Title"}}},
		{"a field rule naming a relation", &query.Config{Filterable: []string{"Comments"}}},
		{"a default search field naming a relation", &query.Config{DefaultSearchFields: []string{"Comments"}}},
		{"a default search wildcard", &query.Config{DefaultSearchFields: []string{"*"}}},
		{"a default search subtree", &query.Config{DefaultSearchFields: []string{"Comments.*"}}},
		{"a default search field outside searchable", &query.Config{Searchable: []string{"Title"}, DefaultSearchFields: []string{"Body"}}},
		{"a deep preload without its intermediate hop", &query.Config{Preloadable: []string{"Comments.Author"}}},
		{"a subtree whose prefix resolves to nothing", &query.Config{Filterable: []string{"Nope.*"}}},
		{"an empty field declaration", &query.Config{Filterable: []string{""}}},
		{"an empty preload declaration", &query.Config{Preloadable: []string{""}}},
		{"a malformed field wildcard", &query.Config{Filterable: []string{"*.*"}}},
		{"a malformed preload wildcard", &query.Config{Preloadable: []string{"*.*"}}},
		{"a negative numeric declaration", &query.Config{MaxBindValues: -1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.config.Check(m); err == nil {
				t.Fatal("the entry was accepted, so it exposes nothing and every request naming that field is blamed on the client")
			}
		})
	}

	for _, tc := range []struct {
		name   string
		config *query.Config
	}{
		{"real fields", &query.Config{Filterable: []string{"Title", "Views"}}},
		{"a path across a relation", &query.Config{Filterable: []string{"Author.Name"}}},
		{"a relation subtree", &query.Config{Filterable: []string{"Comments.*"}}},
		{"a real relation to preload", &query.Config{Preloadable: []string{"Comments", "Comments.Author"}}},
		{"a searchable default", &query.Config{Searchable: []string{"Title"}, DefaultSearchFields: []string{"Title"}}},
		{"the wildcard", &query.Config{Filterable: []string{"*"}}},
		{"the empty config", &query.Config{}},
		{"nil", nil},
	} {
		t.Run("control: "+tc.name, func(t *testing.T) {
			if err := tc.config.Check(m); err != nil {
				t.Fatalf("a legal config was refused: %v", err)
			}
		})
	}
}
