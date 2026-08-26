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

// compile turns a document into the raw option list, so a test can count what
// the request asked for as well as read it.
func compile(t *testing.T, doc string, cfg *query.Config) []crud.Option {
	t.Helper()
	var req query.Request
	if err := json.Unmarshal([]byte(doc), &req); err != nil {
		t.Fatalf("decode %s: %v", doc, err)
	}
	opts, err := req.Compile(Articles.Meta(), cfg)
	if err != nil {
		t.Fatalf("compile %s: %v", doc, err)
	}
	return opts
}

// resolve applies the options the way a repository does.
func resolve(t *testing.T, doc string, cfg *query.Config) *crud.Options {
	t.Helper()
	return crud.Build(compile(t, doc, cfg)...)
}

// clause renders one predicate on its own, so each Where a request produced can
// be read back in the words the database would see.
func clause(t *testing.T, p crud.Predicate) string {
	t.Helper()
	b := crud.NewSQL(crud.Postgres{}, Articles.Meta()).Predicate(p)
	if err := b.Err(); err != nil {
		t.Fatalf("render: %v", err)
	}
	return crudtest.Normalize(b.String())
}

// Every knob in the document has to land on the matching field of Options —
// and nothing else may move. This is the whole contract of Compile in one test.
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

	wantFilters := []string{`"views" >= $1`, `"title" LIKE $1`, `"body" LIKE $1`}
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
	// Nothing in the wire DSL may ask for a lock or drop the repository's
	// stable sort: those are decisions for the server, not the client.
	if o.ForUpdate || o.NoSort {
		t.Fatalf("a request set forUpdate %v / noSort %v; neither is reachable from the wire",
			o.ForUpdate, o.NoSort)
	}
}

// A knob the client did not touch produces no option at all. An option carrying
// a zero would look like a request and overwrite the repository's own default.
func TestAbsentKnobsProduceNoOptions(t *testing.T) {
	for _, doc := range []string{
		`{}`,
		`{"filter":{}}`,
		`{"filter":null}`,
		`{"page":0,"limit":0,"offset":0}`,
		`{"sort":[],"select":[],"preload":[],"terms":[]}`,
		`{"search":"   "}`,
		`{"unpaged":false,"skipTotal":false,"distinct":false}`,
	} {
		if opts := compile(t, doc, nil); len(opts) != 0 {
			t.Fatalf("%s produced %d options, want none", doc, len(opts))
		}
	}

	var absent *query.Request
	opts, err := absent.Compile(Articles.Meta(), nil)
	if err != nil || opts != nil {
		t.Fatalf("a nil request compiled to %v, %v; want nothing at all", opts, err)
	}
}

// The observable consequence of the rule above: an empty document leaves the
// repository's default page size in place instead of asking for LIMIT 0.
func TestAnEmptyRequestKeepsTheRepositoryPageSize(t *testing.T) {
	sql, _ := run(t, `{}`, nil)
	if !strings.Contains(sql, "LIMIT 20") {
		t.Fatalf("sql = %s\nwant the repository default LIMIT 20", sql)
	}
}

// The three filter sources are separate predicates, ANDed in a fixed order, so
// a search can never widen what the filter narrowed.
func TestFilterTermsAndSearchAreAndedNotMerged(t *testing.T) {
	sql, _ := run(t, `{
		"filter": {"views": {"gte": 10}},
		"terms": [{"path": "authorId", "values": ["7"]}],
		"search": "go", "searchFields": ["title", "body"]
	}`, nil)
	want := `("views" >= $1 AND "author_id" = $2 AND ("title" LIKE $3 OR "body" LIKE $4))`
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

// An allow-list entry may name a field, a whole subtree or everything, and it
// is matched against the canonical path however the client spelled it.
func TestAllowListMatching(t *testing.T) {
	for _, tc := range []struct {
		name string
		cfg  *query.Config
		doc  string
		ok   bool
	}{
		{"empty list allows anything", &query.Config{}, `{"filter":{"views":1}}`, true},
		{"exact entry", &query.Config{Filterable: []string{"Views"}}, `{"filter":{"views":1}}`, true},
		{"entry is matched case-insensitively",
			&query.Config{Filterable: []string{"views"}}, `{"filter":{"Views":1}}`, true},
		{"the client's spelling does not matter",
			&query.Config{Filterable: []string{"PublishedAt"}}, `{"filter":{"published_at":null}}`, true},
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
			var req query.Request
			if err := json.Unmarshal([]byte(tc.doc), &req); err != nil {
				t.Fatal(err)
			}
			_, err := req.Compile(Articles.Meta(), tc.cfg)
			if tc.ok && err != nil {
				t.Fatalf("%s was refused: %v", tc.doc, err)
			}
			if !tc.ok && err == nil {
				t.Fatalf("%s was allowed", tc.doc)
			}
		})
	}
}

// Each list guards its own verb, and the refusal names the canonical path so a
// client can see what it asked for.
func TestEachAllowListGuardsItsOwnVerb(t *testing.T) {
	cfg := &query.Config{
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
		t.Run(tc.name, func(t *testing.T) { mustFail(t, tc.doc, cfg, tc.want) })
	}
	// …and a request that stays inside every list is not merely accepted: each
	// clause it asked for is in the statement.
	sql, args := run(t, `{"filter":{"title":"a"},"sort":["title"],"select":["title"],
		"preload":["author"],"search":"go","searchFields":["title"]}`, cfg)
	// author_id is in the projection because the preload the list allowed needs
	// it to find its parents.
	want := `SELECT "id", "title", "author_id" FROM "articles" WHERE ("title" = $1 AND "title" LIKE $2) ` +
		`ORDER BY "title" ASC, "id" ASC LIMIT 20`
	if sql != want {
		t.Fatalf("sql  = %s\nwant = %s", sql, want)
	}
	if len(args) != 2 || args[0] != "a" || args[1] != "%go%" {
		t.Fatalf("args = %#v, want the filter's value then the search term", args)
	}
}

// With no explicit field list a search falls back to the configured defaults,
// and only to them.
func TestSearchUsesDefaultSearchFields(t *testing.T) {
	cfg := &query.Config{DefaultSearchFields: []string{"Title"}}
	sql, args := run(t, `{"search":"go"}`, cfg)
	if got := where(sql); got != `"title" LIKE $1` {
		t.Fatalf("where = %s, want only the default field", got)
	}
	if args[0] != "%go%" {
		t.Fatalf("args = %v", args)
	}
	// An explicit list wins over the defaults.
	sql, _ = run(t, `{"search":"go","searchFields":["body"]}`, cfg)
	if got := where(sql); got != `"body" LIKE $1` {
		t.Fatalf("where = %s, want the requested field", got)
	}
	// A default field still has to be searchable.
	mustFail(t, `{"search":"go"}`,
		&query.Config{DefaultSearchFields: []string{"Body"}, Searchable: []string{"Title"}},
		"Body is not searchable")
}

// A non-text default search field joins the OR only when the term fits it, so
// searching for "42" can match an id column without a type error, and searching
// for a word does not.
func TestSearchJoinsNonTextColumnsOnlyWhenTheTermFits(t *testing.T) {
	cfg := &query.Config{DefaultSearchFields: []string{"Title", "Views"}}
	sql, args := run(t, `{"search":"42"}`, cfg)
	if got := where(sql); got != `("title" LIKE $1 OR "views" = $2)` {
		t.Fatalf("where = %s, want the numeric column to join in", got)
	}
	if _, ok := args[1].(int); !ok {
		t.Fatalf("views bound as %T, want int", args[1])
	}
	sql, _ = run(t, `{"search":"go"}`, cfg)
	if got := where(sql); got != `"title" LIKE $1` {
		t.Fatalf("where = %s, want the numeric column left out", got)
	}
}

// When nothing is searchable the search disappears instead of becoming an empty
// OR — which renders as a constant and would quietly return no rows.
func TestSearchWithNothingToSearchProducesNoPredicate(t *testing.T) {
	opts := compile(t, `{"search":"go"}`, &query.Config{Searchable: []string{"Views"}})
	if len(opts) != 0 {
		t.Fatalf("%d options, want none", len(opts))
	}
	sql, _ := run(t, `{"search":"go"}`, &query.Config{Searchable: []string{"Views"}})
	if strings.Contains(sql, "WHERE") {
		t.Fatalf("sql = %s, want no WHERE clause at all", sql)
	}
}

// A preload carries its own options, compiled against the related model.
func TestPreloadOptionsTravelWithTheRelation(t *testing.T) {
	o := resolve(t, `{"preload":[{"path":"comments","filter":{"approved":true},"sort":["-body"]}]}`, nil)
	if len(o.Preloads) != 1 || o.Preloads[0].Path != "Comments" {
		t.Fatalf("preloads = %+v", o.Preloads)
	}
	sub := crud.Build(o.Preloads[0].Opts...)
	if len(sub.Filter) != 1 || len(sub.Sort) != 1 {
		t.Fatalf("preload options = %d filters, %d sorts; want 1 and 1", len(sub.Filter), len(sub.Sort))
	}
	if sub.Sort[0] != (crud.Order{Field: "Body", Desc: true}) {
		t.Fatalf("preload sort = %+v", sub.Sort[0])
	}
	// A plain path carries nothing, so the relation is loaded whole.
	o = resolve(t, `{"preload":["comments"]}`, nil)
	if len(o.Preloads[0].Opts) != 0 {
		t.Fatalf("an unqualified preload picked up %d options", len(o.Preloads[0].Opts))
	}
}

// Keys are visited in sorted order, not document order: the same request must
// always produce the same statement, whatever the client's JSON encoder did.
func TestKeyOrderDoesNotFollowTheDocument(t *testing.T) {
	forwards, _ := run(t, `{"filter":{"authorId":1,"title":"a","views":{"gte":2,"lt":9}}}`, nil)
	backwards, _ := run(t, `{"filter":{"views":{"lt":9,"gte":2},"title":"a","authorId":1}}`, nil)
	if forwards != backwards {
		t.Fatalf("key order changed the statement:\n%s\n%s", forwards, backwards)
	}
	// The nested AND is flattened away, which is why the operator object's own
	// two conditions still read left to right in sorted order.
	want := `("author_id" = $1 AND "title" = $2 AND "views" >= $3 AND "views" < $4)`
	if got := where(forwards); got != want {
		t.Fatalf("where = %s\nwant  = %s", got, want)
	}
}

// The depth limit bounds a single path as well as the nesting of and/or.
func TestMaxDepthBoundsPathLength(t *testing.T) {
	cfg := &query.Config{MaxDepth: 2}
	sql, _ := run(t, `{"filter":{"comments.body":"x"}}`, cfg)
	want := `EXISTS (SELECT 1 FROM "comments" AS rx1 WHERE rx1."article_id" = "articles"."id" AND rx1."body" = $1)`
	if got := where(sql); got != want {
		t.Fatalf("a two-segment path at MaxDepth 2 compiled to\n  %s\nwant\n  %s", got, want)
	}
	mustFail(t, `{"filter":{"comments.author.name":"x"}}`, cfg, "deeper than the allowed 2 segments")
	mustFail(t, `{"sort":["comments.author.name"]}`, cfg, "deeper than the allowed 2 segments")
}

// The condition budget covers every shape a comparison can take, so it cannot
// be side-stepped by spelling comparisons as shorthand equalities or by
// splitting them across the two filter forms.
func TestConditionBudgetCountsEveryComparison(t *testing.T) {
	for _, tc := range []struct{ name, doc string }{
		{"operator objects", `{"filter":{"views":{"gte":1,"lt":9}}}`},
		{"shorthand equalities", `{"filter":{"title":"a","body":"b"}}`},
		{"shorthand lists", `{"filter":{"title":["a"],"body":["b"]}}`},
		{"across the branches of an or", `{"filter":{"or":[{"title":"a"},{"body":"b"}]}}`},
		{"flat terms", `{"terms":[{"path":"title","values":["a"]},{"path":"body","values":["b"]}]}`},
		{"one of each form", `{"filter":{"title":"a"},"terms":[{"path":"body","values":["b"]}]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mustFail(t, tc.doc, &query.Config{MaxConditions: 1}, "at most 1 conditions")
			// The budget is a bound, not a filter: at 2 the same document
			// compiles whole, both of its comparisons bound.
			_, args := run(t, tc.doc, &query.Config{MaxConditions: 2})
			if len(args) != 2 {
				t.Fatalf("at a budget of 2 the document bound %#v, want its two comparisons", args)
			}
		})
	}
}

// A Request is a wire document in both directions: an API gateway that stores a
// saved search, or a client that builds one up and posts it, needs the document
// it wrote back to mean what it meant. Filter holds raw bytes precisely so it
// can survive that round trip, and nothing had ever sent one back out.
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

	// The proof is not that the bytes match — key order and whitespace are free
	// — but that both documents ask the database for the same thing.
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

func compileReq(t *testing.T, req *query.Request) []crud.Option {
	t.Helper()
	opts, err := req.Compile(Articles.Meta(), nil)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	return opts
}

// A request cannot turn pagination off unless the endpoint says it may.
//
// Every other bound in query.Config bounds what a request may *name*. This one
// bounds how much comes back, and it is the only knob a client can set that has
// no ceiling of its own: crud.Options.Resolved clamps unpaged down to the
// repository's MaxLimit, and MaxLimit is unset by default. With both defaults,
// `?unpaged=true` on a public list route is a full table scan and a full table
// in memory, chosen by whoever sent the request.
//
// It reads like security.Policy's AllowUnscopedDeleteAll, and for the same
// reason: the dangerous direction is the one that has to be named.
func TestUnpagedIsRefusedUnlessTheEndpointServesIt(t *testing.T) {
	var req query.Request
	if err := json.Unmarshal([]byte(`{"unpaged":true}`), &req); err != nil {
		t.Fatal(err)
	}

	for _, cfg := range []*query.Config{nil, {}, {Filterable: []string{"*"}}} {
		if _, err := req.Compile(Articles.Meta(), cfg); err == nil {
			t.Fatalf("unpaged was accepted on an endpoint that never declared it: %+v", cfg)
		}
	}

	// The control. Everything above would hold just as well if unpaged were
	// refused unconditionally, so an endpoint that declares it has to get it —
	// and get it as far as the resolved options, not merely past validation.
	o := resolve(t, `{"unpaged":true}`, &query.Config{AllowUnpaged: true})
	if !o.Unpaged {
		t.Fatal("an endpoint that declared AllowUnpaged did not get unpaged results")
	}

	// And the refusal is a client mistake with a path, not a 500: the client
	// asked for something this endpoint does not serve, and can be told which
	// part of its request was refused.
	_, err := req.Compile(Articles.Meta(), nil)
	var qe *query.Error
	if !errors.As(err, &qe) || qe.Path != "unpaged" {
		t.Fatalf("the refusal is not a query.Error naming the parameter: %#v", err)
	}

	// And it names the spelling the client sent. The query string accepts `all`
	// as an alias, and blaming that request on `unpaged` points at a key that
	// appears nowhere in it — a path the client cannot act on, which is the one
	// thing the path is for.
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

// A list a client chose the length of is bounded, and so is a sort.
//
// Both are the same hole in different clothes: a bound the condition budget
// cannot see. `{"id": {"in": [...]}}` is charged as exactly one condition
// however long the array is, and every element becomes a bound parameter —
// PostgreSQL refuses a statement past 65535 of them, so without a cap here the
// honest 400 arrived from the driver, as a 500, after the statement was built.
// A sort has no budget of its own at all, and a term that hops a relation
// renders as a correlated scalar subquery, so a long list is not linear.
func TestAClientChosenListAndSortAreBounded(t *testing.T) {
	t.Run("in", func(t *testing.T) {
		big := make([]string, 40)
		for i := range big {
			big[i] = strconv.Itoa(i)
		}
		doc := `{"filter":{"views":{"in":[` + strings.Join(big, ",") + `]}}}`

		cfg := &query.Config{MaxInValues: 8}
		var req query.Request
		if err := json.Unmarshal([]byte(doc), &req); err != nil {
			t.Fatal(err)
		}
		if _, err := req.Compile(Articles.Meta(), cfg); err == nil {
			t.Fatal("a 40-value list past a cap of 8 compiled")
		}

		// The control, and the point: the same document is one condition, so
		// MaxConditions never sees its length. Without MaxInValues this passes.
		if _, err := req.Compile(Articles.Meta(), &query.Config{MaxConditions: 1}); err != nil {
			t.Fatalf("a 40-value list is charged as more than one condition: %v", err)
		}

		// And a list under the cap still works.
		compile(t, `{"filter":{"views":{"in":[1,2,3]}}}`, cfg)
	})

	t.Run("the shorthand spelling is bounded too", func(t *testing.T) {
		big := make([]string, 40)
		for i := range big {
			big[i] = strconv.Itoa(i)
		}
		doc := `{"filter":{"views":[` + strings.Join(big, ",") + `]}}`
		var req query.Request
		if err := json.Unmarshal([]byte(doc), &req); err != nil {
			t.Fatal(err)
		}
		if _, err := req.Compile(Articles.Meta(), &query.Config{MaxInValues: 8}); err == nil {
			t.Fatal("the array shorthand is not bounded, only the explicit `in` operator is")
		}
	})

	t.Run("the flat-term spelling is bounded too", func(t *testing.T) {
		// The third spelling, and the one D-060's cap did not reach. It is
		// reachable from POST /query through Term.Values, not only from a query
		// string, so a stock config still produced one bind parameter per element
		// with no ceiling.
		big := make([]string, 40)
		for i := range big {
			big[i] = strconv.Itoa(i)
		}
		doc := `{"terms":[{"path":"views","op":"in","values":["` + strings.Join(big, `","`) + `"]}]}`
		var req query.Request
		if err := json.Unmarshal([]byte(doc), &req); err != nil {
			t.Fatal(err)
		}
		if _, err := req.Compile(Articles.Meta(), &query.Config{MaxInValues: 8}); err == nil {
			t.Fatal("a 40-value flat term compiled past a cap of 8")
		}
		// The control: the same term charges as one condition, so MaxConditions
		// never sees its length — which is why it needs a cap of its own.
		if _, err := req.Compile(Articles.Meta(), &query.Config{MaxConditions: 1}); err != nil {
			t.Fatalf("a 40-value flat term is charged as more than one condition: %v", err)
		}
	})

	t.Run("the search field list is bounded", func(t *testing.T) {
		// The fourth spelling: each entry is its own LIKE with its own bind.
		fields := make([]string, 40)
		for i := range fields {
			fields[i] = "title"
		}
		var req query.Request
		doc := `{"search":"go","searchFields":["` + strings.Join(fields, `","`) + `"]}`
		if err := json.Unmarshal([]byte(doc), &req); err != nil {
			t.Fatal(err)
		}
		if _, err := req.Compile(Articles.Meta(), &query.Config{MaxSort: 4}); err == nil {
			t.Fatal("40 search fields compiled past a cap of 4")
		}
	})

	t.Run("a repeated search field is searched once", func(t *testing.T) {
		o := resolve(t, `{"search":"go","searchFields":["title","title","body"]}`, nil)
		sql, _, err := crud.NewSQL(crud.Postgres{}, Articles.Meta()).Predicate(o.Predicate()).Done()
		if err != nil {
			t.Fatal(err)
		}
		if n := strings.Count(sql, `"title" LIKE`); n != 1 {
			t.Fatalf("the same column was searched %d times; the second LIKE matches the same rows and costs the same scan:\n%s", n, sql)
		}
	})

	t.Run("sort", func(t *testing.T) {
		var req query.Request
		if err := json.Unmarshal([]byte(`{"sort":["title","-views","createdAt"]}`), &req); err != nil {
			t.Fatal(err)
		}
		if _, err := req.Compile(Articles.Meta(), &query.Config{MaxSort: 2}); err == nil {
			t.Fatal("three sort terms past a cap of two compiled")
		}
		// The control: at the cap it is fine.
		if _, err := req.Compile(Articles.Meta(), &query.Config{MaxSort: 3}); err != nil {
			t.Fatalf("three sort terms at a cap of three were refused: %v", err)
		}
	})

	t.Run("a repeated sort path is rendered once", func(t *testing.T) {
		o := resolve(t, `{"sort":["title","title","-title"]}`, nil)
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
}

// A cursor cannot reach a column the endpoint declined to expose to filtering.
//
// A cursor is a filter. Its payload is the sort tuple, and the repository turns
// it into an inequality over exactly those columns ([[D-028]]) — so a column that
// is Sortable and not Filterable became comparable with `>` and `<` by forging a
// two-element token, while the same comparison written as a filter was refused
// by name. That is a binary search over a column the deployment kept back:
// enough to read a salary to the nearest unit in twenty requests.
//
// The token is opaque and unsigned, and that is fine — it carries a position,
// not authority. What it must not do is reach further than the document could.
func TestACursorCannotCompareAColumnTheEndpointHidesFromFiltering(t *testing.T) {
	cfg := &query.Config{
		Filterable: []string{"ID", "Title"},
		Sortable:   []string{"ID", "Title", "Views"},
	}

	// The control first, and it is what makes this a hole rather than a
	// preference: the same comparison written as a filter is refused by name.
	var asFilter query.Request
	if err := json.Unmarshal([]byte(`{"filter":{"views":{"gt":100}}}`), &asFilter); err != nil {
		t.Fatal(err)
	}
	if _, err := asFilter.Compile(Articles.Meta(), cfg); err == nil {
		t.Fatal("the control failed: Views is filterable after all, so this test proves nothing")
	}

	// The same reach, through a cursor.
	var viaCursor query.Request
	if err := json.Unmarshal([]byte(`{"sort":["views","id"],"after":"whatever"}`), &viaCursor); err != nil {
		t.Fatal(err)
	}
	if _, err := viaCursor.Compile(Articles.Meta(), cfg); err == nil {
		t.Fatal("a cursor reached a column the endpoint hides from filtering")
	}

	// And the ordinary case still works: sorting by a filterable column and
	// paging through it is what cursors are for.
	var fine query.Request
	if err := json.Unmarshal([]byte(`{"sort":["title","id"],"after":"whatever"}`), &fine); err != nil {
		t.Fatal(err)
	}
	if _, err := fine.Compile(Articles.Meta(), cfg); err != nil {
		t.Fatalf("a cursor over filterable columns was refused: %v", err)
	}

	// And sorting by the hidden column *without* a cursor is still allowed — it
	// orders rows the caller may already see, which is what Sortable means.
	var sortOnly query.Request
	if err := json.Unmarshal([]byte(`{"sort":["views"]}`), &sortOnly); err != nil {
		t.Fatal(err)
	}
	if _, err := sortOnly.Compile(Articles.Meta(), cfg); err != nil {
		t.Fatalf("sorting by a sortable column was refused when no cursor was involved: %v", err)
	}
}

// An allow-list entry that names nothing fails where it was written.
//
// The lists are plain strings and `allowed` is pure string matching, so a
// misspelled entry is inert: it never matches, the field it was meant to expose
// stays closed, and every request asking for that field is refused as the
// *client's* mistake. `Filterable: {"CreatedAt"}` on a model whose field is
// `Created` is a filter nobody can use and an error that blames the caller,
// forever, with nothing anywhere saying otherwise.
func TestAnAllowListEntryThatNamesNothingIsRefusedAtDeclaration(t *testing.T) {
	m := Articles.Meta()

	for _, tc := range []struct {
		name string
		cfg  *query.Config
	}{
		{"filterable", &query.Config{Filterable: []string{"Nope"}}},
		{"sortable", &query.Config{Sortable: []string{"Nope"}}},
		{"selectable", &query.Config{Selectable: []string{"Nope"}}},
		{"searchable", &query.Config{Searchable: []string{"Nope"}}},
		{"defaultSearchFields", &query.Config{DefaultSearchFields: []string{"Nope"}}},
		{"preloadable", &query.Config{Preloadable: []string{"Nope"}}},
		{"preloadable naming a column", &query.Config{Preloadable: []string{"Title"}}},
		{"a subtree whose prefix resolves to nothing", &query.Config{Filterable: []string{"Nope.*"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.cfg.Check(m); err == nil {
				t.Fatal("the entry was accepted, so it exposes nothing and every request naming that field is blamed on the client")
			}
		})
	}

	// The controls. Everything above would hold for a Check that refused any
	// config at all, so the legal shapes have to pass: real fields, a real
	// relation, a subtree, the bare wildcard, and the empty list that means
	// "anything the model maps".
	for _, tc := range []struct {
		name string
		cfg  *query.Config
	}{
		{"real fields", &query.Config{Filterable: []string{"Title", "Views"}}},
		{"a path across a relation", &query.Config{Filterable: []string{"Author.Name"}}},
		{"a relation subtree", &query.Config{Filterable: []string{"Comments.*"}}},
		{"a real relation to preload", &query.Config{Preloadable: []string{"Comments", "Comments.Author"}}},
		{"the wildcard", &query.Config{Filterable: []string{"*"}}},
		{"the empty config", &query.Config{}},
		{"nil", nil},
	} {
		t.Run("control: "+tc.name, func(t *testing.T) {
			if err := tc.cfg.Check(m); err != nil {
				t.Fatalf("a legal config was refused: %v", err)
			}
		})
	}
}
