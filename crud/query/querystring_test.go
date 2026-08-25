package query_test

import (
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/shardit-io/vv/crud/query"
)

// Every operator spelling has to mean the same thing on both front doors. A
// drift here is silent: the JSON client filters, the query-string client gets
// something else, and nothing errors.
func TestEveryOperatorAliasMeansTheSameOnBothDoors(t *testing.T) {
	for _, tc := range []struct {
		alias string
		doc   string // the operand as JSON
		text  string // the same operand as query-string text
		want  string // the clause both doors must build
		args  []any
	}{
		{"eq", `"go"`, "go", `"title" = $1`, []any{"go"}},
		{"=", `"go"`, "go", `"title" = $1`, []any{"go"}},
		{"equals", `"go"`, "go", `"title" = $1`, []any{"go"}},
		{"is", `"go"`, "go", `"title" = $1`, []any{"go"}},

		{"ne", `"go"`, "go", `"title" <> $1`, []any{"go"}},
		{"neq", `"go"`, "go", `"title" <> $1`, []any{"go"}},
		{"not", `"go"`, "go", `"title" <> $1`, []any{"go"}},
		{"!=", `"go"`, "go", `"title" <> $1`, []any{"go"}},
		{"<>", `"go"`, "go", `"title" <> $1`, []any{"go"}},

		{"gt", `"go"`, "go", `"title" > $1`, []any{"go"}},
		{">", `"go"`, "go", `"title" > $1`, []any{"go"}},
		{"gte", `"go"`, "go", `"title" >= $1`, []any{"go"}},
		{">=", `"go"`, "go", `"title" >= $1`, []any{"go"}},
		{"ge", `"go"`, "go", `"title" >= $1`, []any{"go"}},
		{"lt", `"go"`, "go", `"title" < $1`, []any{"go"}},
		{"<", `"go"`, "go", `"title" < $1`, []any{"go"}},
		{"lte", `"go"`, "go", `"title" <= $1`, []any{"go"}},
		{"<=", `"go"`, "go", `"title" <= $1`, []any{"go"}},
		{"le", `"go"`, "go", `"title" <= $1`, []any{"go"}},

		// The LIKE family takes the pattern as written; only the convenience
		// operators wrap it in wildcards.
		{"like", `"go%"`, "go%", `"title" LIKE $1`, []any{"go%"}},
		{"notlike", `"go%"`, "go%", `"title" NOT LIKE $1`, []any{"go%"}},
		{"notLike", `"go%"`, "go%", `"title" NOT LIKE $1`, []any{"go%"}},
		{"ilike", `"go%"`, "go%", `LOWER("title") LIKE LOWER($1)`, []any{"go%"}},
		{"likeignorecase", `"go%"`, "go%", `LOWER("title") LIKE LOWER($1)`, []any{"go%"}},
		{"contains", `"go"`, "go", `"title" LIKE $1`, []any{"%go%"}},
		{"search", `"go"`, "go", `"title" LIKE $1`, []any{"%go%"}},
		{"startswith", `"go"`, "go", `"title" LIKE $1`, []any{"go%"}},
		{"prefix", `"go"`, "go", `"title" LIKE $1`, []any{"go%"}},
		{"endswith", `"go"`, "go", `"title" LIKE $1`, []any{"%go"}},
		{"suffix", `"go"`, "go", `"title" LIKE $1`, []any{"%go"}},

		{"in", `["go","rust"]`, "go,rust", `"title" IN ($1, $2)`, []any{"go", "rust"}},
		{"nin", `["go","rust"]`, "go,rust", `"title" NOT IN ($1, $2)`, []any{"go", "rust"}},
		{"notin", `["go","rust"]`, "go,rust", `"title" NOT IN ($1, $2)`, []any{"go", "rust"}},
		{"between", `["a","m"]`, "a,m", `"title" BETWEEN $1 AND $2`, []any{"a", "m"}},

		{"isnull", `true`, "true", `"title" IS NULL`, nil},
		{"null", `true`, "true", `"title" IS NULL`, nil},
		{"isnotnull", `true`, "true", `"title" IS NOT NULL`, nil},
		{"notnull", `true`, "true", `"title" IS NOT NULL`, nil},
		// A unary operator flips when it is asked to.
		{"isnull", `false`, "false", `"title" IS NOT NULL`, nil},
		{"notnull", `false`, "false", `"title" IS NULL`, nil},

		// Case and the Mongo-style `$` prefix fold away.
		{"GTE", `"go"`, "go", `"title" >= $1`, []any{"go"}},
		{"Contains", `"go"`, "go", `"title" LIKE $1`, []any{"%go%"}},
		{"NotIn", `["go"]`, "go", `"title" NOT IN ($1)`, []any{"go"}},
		{"IsNull", `true`, "true", `"title" IS NULL`, nil},
		{"$eq", `"go"`, "go", `"title" = $1`, []any{"go"}},
		{"$gte", `"go"`, "go", `"title" >= $1`, []any{"go"}},
		{"$in", `["go"]`, "go", `"title" IN ($1)`, []any{"go"}},
		{"$isNull", `true`, "true", `"title" IS NULL`, nil},
	} {
		t.Run(tc.alias+"/"+tc.text, func(t *testing.T) {
			docSQL, docArgs := run(t, `{"filter":{"title":{"`+tc.alias+`":`+tc.doc+`}}}`, nil)
			textReq, err := query.ParseQuery(url.Values{"f": {"title:" + tc.alias + ":" + tc.text}})
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			textSQL, textArgs := runReq(t, textReq, nil)

			if got := where(docSQL); got != tc.want {
				t.Fatalf("the JSON door built %s, want %s", got, tc.want)
			}
			if got := where(textSQL); got != tc.want {
				t.Fatalf("the query-string door built %s, want %s", got, tc.want)
			}
			if !reflect.DeepEqual(docArgs, tc.args) {
				t.Fatalf("the JSON door bound %#v, want %#v", docArgs, tc.args)
			}
			if !reflect.DeepEqual(textArgs, tc.args) {
				t.Fatalf("the query-string door bound %#v, want %#v", textArgs, tc.args)
			}
		})
	}
}

// An operator nobody defined is a rejection on both doors, never an ignored
// clause that returns the whole table.
func TestUnknownOperatorIsRefusedOnBothDoors(t *testing.T) {
	mustFail(t, `{"filter":{"title":{"approximately":"go"}}}`, nil, `unknown operator "approximately"`)

	req, err := query.ParseQuery(url.Values{"f": {"title:approximately:go"}})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := req.Compile(Articles.Meta(), nil); err == nil ||
		!strings.Contains(err.Error(), `unknown operator "approximately"`) {
		t.Fatalf("err = %v, want an unknown-operator rejection", err)
	}
}

// A term with no operator at all is an equality, and a term whose operator is
// unary needs no value.
func TestTermDefaults(t *testing.T) {
	sql, args := runReq(t, &query.Request{
		Terms: []query.Term{{Path: "title", Values: query.Strings{"go"}}},
	}, nil)
	if got := where(sql); got != `"title" = $1` || args[0] != "go" {
		t.Fatalf("where = %s args = %v, want an equality", got, args)
	}

	sql, args = runReq(t, &query.Request{
		Terms: []query.Term{{Path: "publishedAt", Op: "isNull"}},
	}, nil)
	if got := where(sql); got != `"published_at" IS NULL` || len(args) != 0 {
		t.Fatalf("where = %s args = %v, want IS NULL with no arguments", got, args)
	}
}

// A binary operator with nothing to compare against is a rejection: an empty
// value would otherwise become an equality against the empty string, which
// quietly matches nothing.
func TestTermWithoutAValueIsRefused(t *testing.T) {
	for _, term := range []query.Term{
		{Path: "title", Op: "eq"},
		{Path: "title", Op: "contains"},
	} {
		req := &query.Request{Terms: []query.Term{term}}
		if _, err := req.Compile(Articles.Meta(), nil); err == nil {
			t.Fatalf("%s with no value was accepted", term.Op)
		} else if !strings.Contains(err.Error(), "needs a value") {
			t.Fatalf("err = %v, want it to say the operator needs a value", err)
		}
	}
}

func TestParseTerm(t *testing.T) {
	for _, tc := range []struct {
		name   string
		in     string
		path   string
		op     string
		values []string
	}{
		{"the usual triple", "views:gte:100", "views", "gte", []string{"100"}},
		{"two segments mean equality", "title:go", "title", "eq", []string{"go"}},
		{"a comma separates values", "status:in:draft,live", "status", "in", []string{"draft", "live"}},
		// Only the first two colons are structural, so a timestamp survives.
		{"a timestamp keeps its colons", "createdAt:gte:2026-01-02T03:04:05Z",
			"createdAt", "gte", []string{"2026-01-02T03:04:05Z"}},
		{"a value may be a whole range", "createdAt:between:2026-01-02T00:00:00Z,2026-01-03T00:00:00Z",
			"createdAt", "between", []string{"2026-01-02T00:00:00Z", "2026-01-03T00:00:00Z"}},
		{"a nested path is left alone", "comments.author.name:eq:Ann",
			"comments.author.name", "eq", []string{"Ann"}},
		{"surrounding space is trimmed", " views : gte : 100 ", "views", "gte", []string{"100"}},
		{"an empty operator falls back later", "views::100", "views", "", []string{"100"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := query.ParseTerm(tc.in)
			if err != nil {
				t.Fatalf("ParseTerm(%q): %v", tc.in, err)
			}
			if got.Path != tc.path || got.Op != tc.op {
				t.Fatalf("ParseTerm(%q) = path %q op %q, want %q and %q", tc.in, got.Path, got.Op, tc.path, tc.op)
			}
			if !reflect.DeepEqual([]string(got.Values), tc.values) {
				t.Fatalf("ParseTerm(%q) values = %#v, want %#v", tc.in, got.Values, tc.values)
			}
		})
	}

	if _, err := query.ParseTerm("title"); err == nil {
		t.Fatal("a bare field name is not a term and should be refused")
	}
}

// The whole query string, read in one go.
func TestParseQueryReadsEveryParameter(t *testing.T) {
	v, err := url.ParseQuery("page=2&limit=25&offset=5" +
		"&sort=-views,title&sort=%2BcreatedAt" +
		// distinct is in here too, and a DISTINCT projection has to select
		// whatever it sorts by — both engines refuse it otherwise.
		"&select=id,title,views,createdAt&preload=author&preload=comments.author" +
		"&search=go&searchFields=title,body" +
		"&f=views:gte:100|title:contains:go&f=publishedAt:isNull:true" +
		"&filter=%7B%22body%22:%22x%22%7D" +
		"&unpaged=true&skipTotal=1&distinct=true")
	if err != nil {
		t.Fatal(err)
	}
	req, err := query.ParseQuery(v)
	if err != nil {
		t.Fatal(err)
	}

	if req.Page != 2 || req.Limit != 25 || req.Offset != 5 {
		t.Fatalf("paging = %d/%d/%d, want 2/25/5", req.Page, req.Limit, req.Offset)
	}
	wantSort := query.Sorts{{Field: "views", Desc: true}, {Field: "title"}, {Field: "createdAt"}}
	if !reflect.DeepEqual(req.Sort, wantSort) {
		t.Fatalf("sort = %+v, want %+v", req.Sort, wantSort)
	}
	if !reflect.DeepEqual([]string(req.Select), []string{"id", "title", "views", "createdAt"}) {
		t.Fatalf("select = %v", req.Select)
	}
	if len(req.Preload) != 2 || req.Preload[0].Path != "author" || req.Preload[1].Path != "comments.author" {
		t.Fatalf("preload = %+v", req.Preload)
	}
	if req.Search != "go" || !reflect.DeepEqual([]string(req.SearchFields), []string{"title", "body"}) {
		t.Fatalf("search = %q %v", req.Search, req.SearchFields)
	}
	// Three terms: the pipe separates two of them inside one parameter.
	wantTerms := []query.Term{
		{Path: "views", Op: "gte", Values: query.Strings{"100"}},
		{Path: "title", Op: "contains", Values: query.Strings{"go"}},
		{Path: "publishedAt", Op: "isNull", Values: query.Strings{"true"}},
	}
	if !reflect.DeepEqual(req.Terms, wantTerms) {
		t.Fatalf("terms = %+v, want %+v", req.Terms, wantTerms)
	}
	if req.Filter.IsZero() {
		t.Fatal("the filter document was dropped")
	}
	if !req.Unpaged || !req.SkipTotal || !req.Distinct {
		t.Fatalf("flags = %v/%v/%v, want all true", req.Unpaged, req.SkipTotal, req.Distinct)
	}

	// And it all compiles into one statement: the JSON document and the flat
	// terms are ANDed, not one instead of the other.
	sql, _ := runReq(t, req, nil)
	want := `("body" = $1 AND "views" >= $2 AND "title" LIKE $3 AND "published_at" IS NULL ` +
		`AND ("title" LIKE $4 OR "body" LIKE $5))`
	if got := where(sql); got != want {
		t.Fatalf("where = %s\nwant  = %s", got, want)
	}
}

// Each parameter answers to the spellings different clients use.
func TestParseQueryAliases(t *testing.T) {
	for _, tc := range []struct {
		name  string
		query string
		check func(*query.Request) bool
	}{
		{"limit", "limit=7", func(r *query.Request) bool { return r.Limit == 7 }},
		{"perPage", "perPage=7", func(r *query.Request) bool { return r.Limit == 7 }},
		{"per_page", "per_page=7", func(r *query.Request) bool { return r.Limit == 7 }},
		{"per-page", "per-page=7", func(r *query.Request) bool { return r.Limit == 7 }},
		{"pageSize", "pageSize=7", func(r *query.Request) bool { return r.Limit == 7 }},

		{"sorts", "sorts=title", func(r *query.Request) bool { return len(r.Sort) == 1 }},
		{"orderBy", "orderBy=title", func(r *query.Request) bool { return len(r.Sort) == 1 }},
		{"order_by", "order_by=title", func(r *query.Request) bool { return len(r.Sort) == 1 }},

		{"fields", "fields=title", func(r *query.Request) bool { return len(r.Select) == 1 }},

		{"preloads", "preloads=author", func(r *query.Request) bool { return len(r.Preload) == 1 }},
		{"with", "with=author", func(r *query.Request) bool { return len(r.Preload) == 1 }},
		{"include", "include=author", func(r *query.Request) bool { return len(r.Preload) == 1 }},

		{"q", "q=go", func(r *query.Request) bool { return r.Search == "go" }},
		{"search_fields", "search_fields=title", func(r *query.Request) bool { return len(r.SearchFields) == 1 }},
		{"search-fields", "search-fields=title", func(r *query.Request) bool { return len(r.SearchFields) == 1 }},

		{"all", "all=true", func(r *query.Request) bool { return r.Unpaged }},
		{"skip_total", "skip_total=true", func(r *query.Request) bool { return r.SkipTotal }},
		{"noTotal", "noTotal=true", func(r *query.Request) bool { return r.SkipTotal }},
		{"filters", "filters=views:gte:1", func(r *query.Request) bool { return len(r.Terms) == 1 }},

		// A flag is only set when it says so.
		{"unpaged=false", "unpaged=false", func(r *query.Request) bool { return !r.Unpaged }},
		{"distinct=0", "distinct=0", func(r *query.Request) bool { return !r.Distinct }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v, err := url.ParseQuery(tc.query)
			if err != nil {
				t.Fatal(err)
			}
			req, err := query.ParseQuery(v)
			if err != nil {
				t.Fatalf("ParseQuery(%s): %v", tc.query, err)
			}
			if !tc.check(req) {
				t.Fatalf("%s did not reach the request: %+v", tc.query, req)
			}
		})
	}
}

// A number that is not one is a rejection naming the parameter, not a zero that
// silently means "the default".
func TestParseQueryRejectsNonNumbers(t *testing.T) {
	for _, tc := range []struct{ param, want string }{
		{"page=one", "page"},
		{"limit=lots", "limit"},
		{"offset=-", "offset"},
	} {
		v, err := url.ParseQuery(tc.param)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := query.ParseQuery(v); err == nil {
			t.Fatalf("%s was accepted", tc.param)
		} else if !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("err = %v, want it to name %q", err, tc.want)
		}
	}
}

// An empty query string is an empty request, not a request for page zero.
func TestParseQueryOfNothing(t *testing.T) {
	req, err := query.ParseQuery(url.Values{})
	if err != nil {
		t.Fatal(err)
	}
	if req == nil {
		t.Fatal("ParseQuery returned no request")
	}
	opts, err := req.Compile(Articles.Meta(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(opts) != 0 {
		t.Fatalf("an empty query string produced %d options", len(opts))
	}
}

// Empty and blank entries drop out rather than becoming empty field names that
// no schema can resolve.
func TestParseQuerySkipsBlankEntries(t *testing.T) {
	v, err := url.ParseQuery("sort=,title,&select=%20&preload=&f=%20|views:gte:1")
	if err != nil {
		t.Fatal(err)
	}
	req, err := query.ParseQuery(v)
	if err != nil {
		t.Fatal(err)
	}
	if len(req.Sort) != 1 || req.Sort[0].Field != "title" {
		t.Fatalf("sort = %+v, want just title", req.Sort)
	}
	if len(req.Select) != 0 || len(req.Preload) != 0 {
		t.Fatalf("select = %v, preload = %+v; want both empty", req.Select, req.Preload)
	}
	if len(req.Terms) != 1 || req.Terms[0].Path != "views" {
		t.Fatalf("terms = %+v", req.Terms)
	}
}
