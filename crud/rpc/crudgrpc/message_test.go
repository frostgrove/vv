package crudgrpc

import (
	"errors"
	"strings"
	"testing"

	"google.golang.org/protobuf/types/known/structpb"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/errs"
	"github.com/frostgrove/vv/remote"
)

type queryNumberModel struct {
	ID    int     `db:"id,pk"`
	Score float64 `db:"score"`
}

type preloadQueryChild struct {
	ID                   int64 `db:"id,pk"`
	PreloadQueryParentID int64 `db:"parent_id"`
	Price                int64 `db:"price"`
}

type preloadQueryParent struct {
	ID       int64               `db:"id,pk"`
	Children []preloadQueryChild `rel:"has_many"`
}

var queryNumberMeta = func() *crud.Meta {
	m, err := crud.NewMeta[queryNumberModel]("query_number_models")
	if err != nil {
		panic(err)
	}
	return m
}()

var preloadQueryMeta = func() *crud.Meta {
	m, err := crud.NewMeta[preloadQueryParent]("preload_query_parents")
	if err != nil {
		panic(err)
	}
	return m
}()

func TestQueryOfRefusesAnUnsafeIntegralNumber(t *testing.T) {
	unsafe := doc(t, `{"filter":{"price":9007199254740993}}`)
	if _, err := queryOf(unsafe, widgetMeta); err == nil {
		t.Fatal("an integral number that Struct rounded was accepted")
	}

	// Struct preserves the decimal string, and the whole query pipeline — not
	// merely queryOf — accepts it as the same integer a query string carries.
	req, err := queryOf(doc(t, `{"filter":{"price":"9007199254740993"}}`), widgetMeta)
	if err != nil {
		t.Fatalf("the exact string spelling was refused at the gRPC door: %v", err)
	}
	if _, err := req.Compile(widgetMeta, nil); err != nil {
		t.Fatalf("the exact string spelling did not compile: %v", err)
	}

	// A huge float does not pretend to be an exact integer. It is representable
	// as the float64 Struct transports and must reach a float model field.
	req, err = queryOf(doc(t, `{"filter":{"score":1e100}}`), queryNumberMeta)
	if err != nil {
		t.Fatalf("a high-magnitude float was refused at the gRPC door: %v", err)
	}
	if _, err := req.Compile(queryNumberMeta, nil); err != nil {
		t.Fatalf("a high-magnitude float did not compile: %v", err)
	}
}

func TestGRPCStructNeverRoundsIDsOrPreloadFilterIntegers(t *testing.T) {
	for _, tc := range []struct {
		name     string
		raw      string
		value    int64
		numberOK bool
	}{
		{"positive safe edge", "9007199254740991", 9007199254740991, true},
		{"negative safe edge", "-9007199254740991", -9007199254740991, true},
		{"positive refused boundary", "9007199254740992", 9007199254740992, false},
		{"negative refused boundary", "-9007199254740992", -9007199254740992, false},
		{"positive beyond boundary", "9007199254740993", 9007199254740993, false},
		{"negative beyond boundary", "-9007199254740993", -9007199254740993, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			id, err := idOf[int64](doc(t, `{"id":`+tc.raw+`}`), "id")
			ids, idsErr := idsOf[int64](doc(t, `{"ids":[`+tc.raw+`]}`), "ids")
			if tc.numberOK {
				if err != nil || id != tc.value || idsErr != nil || len(ids) != 1 || ids[0] != tc.value {
					t.Fatalf("numeric ID/IDs = %d %v / %v %v, want %d", id, err, ids, idsErr, tc.value)
				}
			} else if err == nil || idsErr == nil {
				t.Fatalf("unsafe numeric ID/IDs = %v / %v, want both refused", err, idsErr)
			}

			id, err = idOf[int64](doc(t, `{"id":"`+tc.raw+`"}`), "id")
			ids, idsErr = idsOf[int64](doc(t, `{"ids":["`+tc.raw+`"]}`), "ids")
			if err != nil || id != tc.value || idsErr != nil || len(ids) != 1 || ids[0] != tc.value {
				t.Fatalf("string ID/IDs = %d %v / %v %v, want %d", id, err, ids, idsErr, tc.value)
			}
		})
	}

	unsafePreload := doc(t, `{"preload":{"path":"children","filter":{"price":9007199254740993}}}`)
	if _, err := queryOf(unsafePreload, preloadQueryMeta); err == nil {
		t.Fatal("a preload filter's rounded integral number was accepted")
	}
	unsafePreloadRows := doc(t, `{"preload":{"path":"children","maxRows":9007199254740993}}`)
	if _, err := queryOf(unsafePreloadRows, preloadQueryMeta); err == nil {
		t.Fatal("a preload row cap's rounded integral number was accepted")
	}
	if _, err := queryOf(doc(t, `{"preload":{"path":"children","filter":{"price":"9007199254740993"}}}`), preloadQueryMeta); err != nil {
		t.Fatalf("an exact preload-filter string was refused: %v", err)
	}
}

func TestQueryOfExplainsUnsafePagingControlsCannotUseStrings(t *testing.T) {
	for _, tc := range []string{
		`{"page":9007199254740993}`,
		`{"limit":9007199254740993}`,
		`{"offset":9007199254740993}`,
		`{"preload":{"path":"children","maxRows":9007199254740993}}`,
	} {
		_, err := queryOf(doc(t, tc), preloadQueryMeta)
		if err == nil {
			t.Fatalf("%s was accepted", tc)
		}
		var fault *errs.Fault
		if !errors.As(err, &fault) || !strings.Contains(fault.Message, "must be exact JSON integers") {
			t.Fatalf("%s error = %v, want a control-range explanation", tc, err)
		}
	}
}

func TestRequestForRefusesAnEmptyKeyBeforeItCanInvoke(t *testing.T) {
	for _, method := range []remote.Method{
		remote.MethodGet,
		remote.MethodUpdate,
		remote.MethodReplace,
		remote.MethodDelete,
	} {
		t.Run(string(method), func(t *testing.T) {
			if _, err := requestFor(remote.Call{Method: method}); err == nil || !strings.Contains(err.Error(), "non-empty id") {
				t.Fatalf("empty %s = %v, want a local key refusal", method, err)
			}
		})
	}
}

func TestRequestForRefusesAnEmptyKeyedMutationBodyBeforeItCanInvoke(t *testing.T) {
	for _, tc := range []struct {
		method remote.Method
		body   string
	}{
		{remote.MethodUpdate, ""},
		{remote.MethodReplace, ""},
		{remote.MethodUpdate, "null"},
		{remote.MethodReplace, "null"},
	} {
		t.Run(string(tc.method)+tc.body, func(t *testing.T) {
			_, err := requestFor(remote.Call{Method: tc.method, ID: "42", Body: []byte(tc.body)})
			if err == nil || !strings.Contains(err.Error(), "non-null body") {
				t.Fatalf("empty %s body = %v, want a local refusal", tc.method, err)
			}
		})
	}
}

func TestRequestForRefusesANonObjectKeyedMutationBodyBeforeItCanInvoke(t *testing.T) {
	for _, tc := range []struct {
		method remote.Method
		body   string
	}{
		{remote.MethodUpdate, "[]"},
		{remote.MethodReplace, "false"},
		{remote.MethodUpdate, `"text"`},
		{remote.MethodReplace, `{"broken":`},
	} {
		t.Run(string(tc.method)+tc.body, func(t *testing.T) {
			_, err := requestFor(remote.Call{Method: tc.method, ID: "42", Body: []byte(tc.body)})
			if err == nil || !strings.Contains(err.Error(), "JSON object") {
				t.Fatalf("invalid %s body = %v, want a local object refusal", tc.method, err)
			}
		})
	}
}

func TestADirectBulkDeleteWithNoIDsUsesTheEmptySetSpelling(t *testing.T) {
	ids, err := exactBulkIDs(nil)
	if err != nil || string(ids) != "null" {
		t.Fatalf("zero IDs = %q, %v, want null", ids, err)
	}
	request, err := requestFor(remote.Call{Method: remote.MethodBulkDelete})
	if err != nil {
		t.Fatal(err)
	}
	got, err := idsOf[int64](request, "ids")
	if err != nil || len(got) != 0 {
		t.Fatalf("zero IDs decoded as %v, %v, want an empty set", got, err)
	}
}

func TestCountsRemainExactInStructResponses(t *testing.T) {
	for _, tc := range []struct {
		name string
		doc  *structpb.Struct
		key  string
		want string
	}{
		{"count", countDoc(9007199254740993), "count", "9007199254740993"},
		{"negative count", countDoc(-9007199254740993), "count", "-9007199254740993"},
		{"deleted", deletedDoc(9007199254740993), "deleted", "9007199254740993"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.doc.GetFields()[tc.key].GetStringValue(); got != tc.want {
				t.Fatalf("%s = %q, want exact string %q", tc.key, got, tc.want)
			}
		})
	}
	if got := countDoc(5).GetFields()["count"].GetNumberValue(); got != 5 {
		t.Fatalf("small count = %v, want JSON number 5", got)
	}
}
