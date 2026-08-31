package query_test

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/query"
)

// render is the whole comparison this file rests on: a predicate as the
// database would see it, arguments included. Two predicates that render the
// same statement with the same binds are the same question, whatever shape the
// document that produced them had.
func render(t *testing.T, p crud.Predicate) (string, []any) {
	t.Helper()
	sql, args, err := crud.NewSQL(crud.Postgres{}, Articles.Meta()).Predicate(p).Done()
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	return sql, args
}

// filterOf compiles a filter document on its own and folds it to one predicate,
// the way a repository does.
func filterOf(t *testing.T, doc string) crud.Predicate {
	t.Helper()
	var request query.Request
	if err := json.Unmarshal([]byte(`{"filter":`+doc+`}`), &request); err != nil {
		t.Fatalf("decode filter %s: %v", doc, err)
	}
	options, err := request.Compile(Articles.Meta(), nil)
	if err != nil {
		t.Fatalf("compile filter %s: %v", doc, err)
	}
	return crud.Build(options...).Predicate()
}

// A filter that goes out to another service and comes back has to ask the same
// question. Not to look the same — shorthand spellings may be canonicalised —
// but to render the same statement against the same binds, because that is the
// only thing a caller can observe.
//
// This is the test that makes crud.MarshalPredicate worth having: without it,
// a remote Get with a filter is a remote Get with no filter, answering 200 with
// every row in the table.
func TestEveryFilterDocumentSurvivesARoundTripThroughAPredicate(t *testing.T) {
	docs := map[string]string{
		"shorthand value":           `{"title": "go"}`,
		"shorthand null":            `{"body": null}`,
		"shorthand list":            `{"views": [1, 2, 3]}`,
		"eq":                        `{"title": {"eq": "go"}}`,
		"ne":                        `{"title": {"ne": "go"}}`,
		"gt":                        `{"views": {"gt": 10}}`,
		"gte":                       `{"views": {"gte": 10}}`,
		"lt":                        `{"views": {"lt": 10}}`,
		"lte":                       `{"views": {"lte": 10}}`,
		"like":                      `{"title": {"like": "go%"}}`,
		"notlike":                   `{"title": {"notlike": "go%"}}`,
		"ilike":                     `{"title": {"ilike": "go%"}}`,
		"contains":                  `{"title": {"contains": "go"}}`,
		"startswith":                `{"title": {"startswith": "go"}}`,
		"endswith":                  `{"title": {"endswith": "go"}}`,
		"contains escapes":          `{"title": {"contains": "100%_off"}}`,
		"case-insensitive contains": `{"title": {"icontains": "100%_off"}}`,
		"case-insensitive prefix":   `{"title": {"istartswith": "go"}}`,
		"case-insensitive suffix":   `{"title": {"iendswith": "go"}}`,
		"in":                        `{"views": {"in": [1, 2, 3]}}`,
		"nin":                       `{"views": {"nin": [1, 2, 3]}}`,
		"between":                   `{"views": {"between": [1, 10]}}`,
		"isnull":                    `{"publishedAt": {"isnull": true}}`,
		"isnotnull":                 `{"publishedAt": {"isnotnull": true}}`,
		"timestamp":                 `{"createdAt": {"gte": "2026-01-02T03:04:05Z"}}`,
		"and":                       `{"and": [{"views": {"gte": 10}}, {"title": "go"}]}`,
		"or":                        `{"or": [{"views": {"gte": 10}}, {"title": "go"}]}`,
		"not":                       `{"not": {"title": "go"}}`,
		"two on one field":          `{"views": {"gte": 1, "lte": 10}}`,
		"nested":                    `{"and": [{"or": [{"views": {"lt": 1}}, {"title": "go"}]}, {"body": "x"}]}`,
		"through relation":          `{"author.name": {"contains": "an"}}`,
		"deep relation":             `{"comments.author.name": "ann"}`,
	}

	for name, doc := range docs {
		t.Run(name, func(t *testing.T) {
			first := filterOf(t, doc)
			wantSQL, wantArgs := render(t, first)

			// Without this the test would pass for a marshaller that produced
			// an empty document and a compiler that read it as no filter: both
			// sides would render "" and agree.
			if wantSQL == "" {
				t.Fatalf("%s rendered no SQL, so this case proves nothing", doc)
			}

			out, err := crud.MarshalPredicate(first)
			if err != nil {
				t.Fatalf("%s has no filter document: %v", doc, err)
			}

			second := filterOf(t, string(out))
			gotSQL, gotArgs := render(t, second)

			if gotSQL != wantSQL {
				t.Fatalf("%s came back asking something else\n  sent  %s\n  wire  %s\n  back  %s",
					doc, wantSQL, out, gotSQL)
			}
			if !reflect.DeepEqual(gotArgs, wantArgs) {
				t.Fatalf("%s came back with different binds\n  sent %#v\n  back %#v\n  wire %s",
					doc, wantArgs, gotArgs, out)
			}
		})
	}
}

// The dangerous half. Every one of these means "no rows" or "SQL", and the
// filter document can say neither. Dropping one silently is the failure that
// matters: an And that loses a term answers with more rows than the caller
// asked for, over a 200, and nothing in the response says so.
func TestAPredicateTheWireCannotCarryIsRefusedByName(t *testing.T) {
	cases := map[string]struct {
		p    crud.Predicate
		node string
	}{
		"raw SQL":              {crud.Raw(`"views" > ?`, 1), "crud.Raw"},
		"column vs column":     {crud.EqField("Title", "Body"), "crud.EqField"},
		"false":                {crud.False(), "crud.False"},
		"empty in":             {crud.In("Views"), "crud.In"},
		"empty or":             {crud.Or(), "crud.Or"},
		"not of true":          {crud.Not(crud.True()), "crud.Not"},
		"not of nothing":       {crud.Not(nil), "crud.Not"},
		"raw inside an and":    {crud.And(crud.Eq("Title", "go"), crud.Raw("1 = 1")), "crud.Raw"},
		"raw after true in or": {crud.Or(crud.True(), crud.Raw("1 = 1")), "crud.Raw"},
		"false inside an or":   {crud.Or(crud.Eq("Title", "go"), crud.False()), "crud.False"},
	}

	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			out, err := crud.MarshalPredicate(c.p)
			if err == nil {
				t.Fatalf("it was written as %s instead of being refused", out)
			}
			var pe *crud.PredicateError
			if !errors.As(err, &pe) {
				t.Fatalf("refused with %T, which a caller cannot branch on: %v", err, err)
			}
			if pe.Node != c.node {
				t.Fatalf("blamed %s; the call site wrote %s", pe.Node, c.node)
			}
			if !strings.Contains(err.Error(), c.node) {
				t.Fatalf("the message does not name %s: %s", c.node, err)
			}
		})
	}
}

// The identity elements are not refused, because a scope default of crud.True()
// is documented as a thing to write and it narrows nothing. What it must not do
// is survive into an Or, where "every row" swallows the rest.
func TestAnUnconditionalPredicateNarrowsNothingAndSwallowsAnOr(t *testing.T) {
	both := func(t *testing.T, p crud.Predicate) string {
		t.Helper()
		out, err := crud.MarshalPredicate(p)
		if err != nil {
			t.Fatalf("refused: %v", err)
		}
		return string(out)
	}

	if got := both(t, crud.True()); got != "{}" {
		t.Fatalf("True() went out as %s; an empty document is how no narrowing is spelled", got)
	}
	if got := both(t, crud.And()); got != "{}" {
		t.Fatalf("And() went out as %s; And of nothing is every row", got)
	}
	if got := both(t, crud.NotIn("Views")); got != "{}" {
		t.Fatalf("NotIn of nothing went out as %s; it is the true identity", got)
	}
	// The term survives and the identity does not: an And keeps the narrowing.
	if got := both(t, crud.And(crud.True(), crud.Eq("Title", "go"))); got != `{"Title":{"eq":"go"}}` {
		t.Fatalf("And(True, eq) went out as %s", got)
	}
	if got := both(t, crud.And(crud.NotIn("Views"), crud.Eq("Title", "go"))); got != `{"Title":{"eq":"go"}}` {
		t.Fatalf("And(empty NotIn, eq) went out as %s", got)
	}
	// The Or does not: any-true is true, and sending only the other term would
	// answer with fewer rows than the caller asked for.
	if got := both(t, crud.Or(crud.True(), crud.Eq("Title", "go"))); got != "{}" {
		t.Fatalf("Or(True, eq) went out as %s; Or with an unconditional term is every row", got)
	}
	if got := both(t, crud.Or(crud.NotIn("Views"), crud.Eq("Title", "go"))); got != "{}" {
		t.Fatalf("Or(empty NotIn, eq) went out as %s; the true term must swallow the Or", got)
	}
}

func TestJSONTermValuesKeepCommasAcrossARoundTrip(t *testing.T) {
	const doc = `{"terms":[{"path":"title","op":"eq","values":["Smith, John"]}]}`
	var first query.Request
	if err := json.Unmarshal([]byte(doc), &first); err != nil {
		t.Fatal(err)
	}
	if got := []string(first.Terms[0].Values); !reflect.DeepEqual(got, []string{"Smith, John"}) {
		t.Fatalf("decoded values = %#v, want one untouched string", got)
	}
	b, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	var second query.Request
	if err := json.Unmarshal(b, &second); err != nil {
		t.Fatal(err)
	}
	if got := []string(second.Terms[0].Values); !reflect.DeepEqual(got, []string{"Smith, John"}) {
		t.Fatalf("round-tripped values = %#v, want one untouched string", got)
	}
}

func TestJSONTermNullSurvivesARoundTrip(t *testing.T) {
	var first query.Request
	if err := json.Unmarshal([]byte(`{"terms":[{"path":"title","op":"eq","values":[null]}]}`), &first); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"values":[null]`) {
		t.Fatalf("a null term was rewritten as another value: %s", b)
	}
	var second query.Request
	if err := json.Unmarshal(b, &second); err != nil {
		t.Fatal(err)
	}
	firstOpts, err := first.Compile(Articles.Meta(), nil)
	if err != nil {
		t.Fatal(err)
	}
	secondOpts, err := second.Compile(Articles.Meta(), nil)
	if err != nil {
		t.Fatal(err)
	}
	firstSQL, firstArgs := render(t, crud.Build(firstOpts...).Predicate())
	secondSQL, secondArgs := render(t, crud.Build(secondOpts...).Predicate())
	if firstSQL != secondSQL || !reflect.DeepEqual(firstArgs, secondArgs) {
		t.Fatalf("round trip changed the null predicate: %s %#v -> %s %#v", firstSQL, firstArgs, secondSQL, secondArgs)
	}
}

// Two conditions on one field are the case a document built out of a map gets
// wrong: {"views":{...},"views":{...}} is a repeated JSON key, and a decoder
// keeps the last one. Half the caller's filter would go missing with nothing to
// see, so every node writes a single-key object and And writes an array.
func TestTwoConditionsOnOneFieldBothSurvive(t *testing.T) {
	p := crud.And(crud.Gte("Views", 1), crud.Lte("Views", 10))
	wantSQL, wantArgs := render(t, p)

	out, err := crud.MarshalPredicate(p)
	if err != nil {
		t.Fatalf("refused: %v", err)
	}
	if strings.Count(string(out), `"Views"`) != 2 {
		t.Fatalf("both conditions have to reach the wire; got %s", out)
	}

	gotSQL, gotArgs := render(t, filterOf(t, string(out)))
	if gotSQL != wantSQL || !reflect.DeepEqual(gotArgs, wantArgs) {
		t.Fatalf("one of the two was lost\n  sent %s %#v\n  back %s %#v\n  wire %s",
			wantSQL, wantArgs, gotSQL, gotArgs, out)
	}
}
