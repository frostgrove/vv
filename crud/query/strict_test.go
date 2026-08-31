package query_test

import (
	"encoding/json"
	"errors"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/frostgrove/vv/crud/query"
)

func TestAMisspelledDocumentKeyIsRefused(t *testing.T) {
	for _, body := range []string{
		`{"filtr":{"title":"x"}}`,
		`{"filter":{"title":"x"},"limits":10}`,
		`{"sortt":["-id"]}`,
		`{"preloads":["author"]}`,
	} {
		t.Run(body, func(t *testing.T) {
			var request query.Request
			err := json.Unmarshal([]byte(body), &request)
			if err == nil {
				t.Fatal("accepted — a typo in the key that carries the filter returns the whole table")
			}
			var qe *query.Error
			if !errors.As(err, &qe) {
				t.Fatalf("err = %T %v, want a *query.Error the transport can map to 400", err, err)
			}
			if qe.Path == "" {
				t.Fatalf("err = %v: the message does not name the offending key", err)
			}
		})
	}
}

func TestNullIsNotAnEmptyQueryOrAHiddenDefault(t *testing.T) {
	for _, body := range []string{
		`null`,
		`{"limit":null}`,
		`{"search":null}`,
		`{"filter":null}`,
		`{"after":null}`,
	} {
		t.Run(body, func(t *testing.T) {
			var request query.Request
			if err := json.Unmarshal([]byte(body), &request); err == nil {
				t.Fatalf("%s decoded as %+v instead of being refused", body, request)
			}
		})
	}
}

func TestDuplicateKeysAndNullListElementsAreRefused(t *testing.T) {
	for _, body := range []string{
		`{"limit":10,"limit":1000}`,
		`{"filter":{"views":{"gte":1,"gte":2}}}`,
		`{"sort":[null]}`,
		`{"preload":[null]}`,
		`{"select":[null]}`,
		`{"searchFields":[null]}`,
		`{"terms":[{"path":"title","op":null,"values":["x"]}]}`,
		`{"sort":{"field":"title","desc":null}}`,
		`{"preload":{"path":"comments","sort":null}}`,
	} {
		t.Run(body, func(t *testing.T) {
			var request query.Request
			if err := json.Unmarshal([]byte(body), &request); err == nil {
				t.Fatal("ambiguous or null list input decoded into a different query")
			}
		})
	}

	request := query.Request{Filter: query.RawFilter(`{"views":{"gte":1,"gte":2}}`)}
	if _, err := request.Compile(Articles.Meta(), nil); err == nil {
		t.Fatal("RawFilter accepted a duplicate operator key")
	}
}

func TestAMisspelledNestedObjectKeyIsRefused(t *testing.T) {
	for _, body := range []string{
		`{"sort":{"field":"title","descc":true}}`,
		`{"preload":{"path":"comments","filtter":{"approved":true}}}`,
		`{"terms":[{"path":"title","opp":"eq","values":["x"]}]}`,
	} {
		t.Run(body, func(t *testing.T) {
			var request query.Request
			err := json.Unmarshal([]byte(body), &request)
			if err == nil {
				t.Fatal("accepted a misspelled nested key that would widen or change the query")
			}
			var qe *query.Error
			if !errors.As(err, &qe) || qe.Path == "" {
				t.Fatalf("err = %T %v, want a query error naming the nested key", err, err)
			}
		})
	}
}

func TestEveryDocumentKeyStillParses(t *testing.T) {
	body := `{
		"page": 2, "limit": 20, "offset": 5,
		"sort": ["-createdAt", {"field":"title","nulls":"last"}],
		"select": "id,title",
		"preload": ["author", {"path":"comments","filter":{"approved":true}}],
		"filter": {"views":{"gte":100}},
		"terms": [],
		"search": "go", "searchFields": ["title"],
		"unpaged": false, "skipTotal": true, "distinct": true
	}`
	var request query.Request
	if err := json.Unmarshal([]byte(body), &request); err != nil {
		t.Fatalf("a document using every key was refused: %v", err)
	}
	if request.Page != 2 || request.Limit != 20 || request.Offset != 5 {
		t.Fatalf("paging = %+v", request)
	}
	if len(request.Sort) != 2 || len(request.Preload) != 2 || len(request.Select) != 2 {
		t.Fatalf("lists = sort %d preload %d select %d", len(request.Sort), len(request.Preload), len(request.Select))
	}
	if !request.SkipTotal || !request.Distinct || request.Unpaged {
		t.Fatalf("flags = %+v", request)
	}
	if request.Filter.IsZero() {
		t.Fatal("the filter was dropped")
	}
}

func TestTheOfferedKeyListMatchesTheStruct(t *testing.T) {
	rt := reflect.TypeOf(query.Request{})
	var fromStruct []string
	for i := range rt.NumField() {
		tag := rt.Field(i).Tag.Get("json")
		if name, _, _ := strings.Cut(tag, ","); name != "" && name != "-" {
			fromStruct = append(fromStruct, name)
		}
	}

	var request query.Request
	err := json.Unmarshal([]byte(`{"nope":1}`), &request)
	if err == nil {
		t.Fatal("an unknown key was accepted")
	}
	_, offer, ok := strings.Cut(err.Error(), "the document accepts ")
	if !ok {
		t.Fatalf("the message does not offer the key list: %v", err)
	}
	offered := strings.Split(offer, ", ")

	if !reflect.DeepEqual(offered, fromStruct) {
		t.Fatalf("the offered list has drifted from the struct:\n  offered: %v\n  struct:  %v",
			offered, fromStruct)
	}
}

func TestAMisspelledQueryParameterIsRefused(t *testing.T) {
	for _, raw := range []string{"filtr=x", "limi=10", "prelaod=author", "seach=go", "sortt=-id"} {
		t.Run(raw, func(t *testing.T) {
			v, _ := url.ParseQuery(raw)
			if _, err := query.ParseQuery(v); err == nil {
				t.Fatal("accepted — a misspelled parameter is silently ignored")
			}
		})
	}
}

func TestAnApplicationsOwnParametersArePassedThrough(t *testing.T) {
	for _, raw := range []string{
		"includeArchived=1",
		"tenant=7&format=csv",
		"a=1&b=2",
		"page=2&myFlag=on",
		"expand=profile",
	} {
		t.Run(raw, func(t *testing.T) {
			v, _ := url.ParseQuery(raw)
			if _, err := query.ParseQuery(v); err != nil {
				t.Fatalf("refused an application parameter: %v", err)
			}
		})
	}
}

func TestEveryAcceptedSpellingSurvivesTheCheck(t *testing.T) {
	for _, name := range []string{
		"page", "limit", "perPage", "per_page", "per-page", "pageSize", "offset",
		"unpaged", "all", "skipTotal", "skip_total", "noTotal", "distinct",
		"sort", "sorts", "orderBy", "order_by", "select", "fields",
		"preload", "preloads", "with", "include",
		"search", "q", "searchFields", "search_fields", "search-fields",
		"f", "filters", "filter",
	} {
		v := url.Values{}
		switch name {
		case "f", "filters":
			v.Set(name, "title:eq:x")
		case "filter":
			v.Set(name, `{"title":"x"}`)
		default:
			v.Set(name, "1")
		}
		if _, err := query.ParseQuery(v); err != nil {
			t.Errorf("%s: an accepted spelling was refused: %v", name, err)
		}
	}
}
