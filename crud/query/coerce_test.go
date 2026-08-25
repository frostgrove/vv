package query_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/shardit-io/vv/crud"
	"github.com/shardit-io/vv/crud/crudtest"
	"github.com/shardit-io/vv/crud/query"
	"github.com/shardit-io/vv/crud/sqlrepo"
)

// code is a column type that parses itself. Coercion has to let it, or a uuid,
// an enum or a money column would lose its own rules the moment its value
// arrives as text.
type code string

func (c *code) UnmarshalText(b []byte) error {
	s := strings.TrimSpace(string(b))
	if s == "" {
		return errors.New("empty code")
	}
	*c = code(strings.ToUpper(s))
	return nil
}

// Sample carries one column of every scalar kind the library maps, so the JSON
// door and the query-string door can be pointed at the same column and their
// answers compared.
type Sample struct {
	ID     int64            `db:"id,pk,auto"`
	Name   string           `db:"name"`
	Active bool             `db:"active"`
	Small  int8             `db:"small"`
	Count  int              `db:"count"`
	Big    int64            `db:"big"`
	Tiny   uint8            `db:"tiny"`
	Total  uint64           `db:"total"`
	Ratio  float32          `db:"ratio"`
	Score  float64          `db:"score"`
	Blob   []byte           `db:"blob"`
	At     time.Time        `db:"at"`
	Code   code             `db:"code"`
	Nick   crud.Opt[string] `db:"nick"`
	Age    *int             `db:"age"`
}

var Samples = sqlrepo.Define[Sample, int64, struct{}]("samples")

// runSample compiles a request against the sample model and returns the WHERE
// clause and the arguments the database would receive.
func runSample(t *testing.T, req *query.Request) (string, []any) {
	t.Helper()
	opts, err := req.Compile(Samples.Meta(), nil)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	rec := crudtest.Postgres().Push(crudtest.Rows())
	if _, err := Samples.Bind(rec).GetAll(context.Background(), opts...); err != nil {
		t.Fatalf("query: %v", err)
	}
	st := rec.Last()
	return where(crudtest.Normalize(st.SQL)), st.Args
}

func jsonRequest(t *testing.T, doc string) *query.Request {
	t.Helper()
	var req query.Request
	if err := json.Unmarshal([]byte(doc), &req); err != nil {
		t.Fatalf("decode %s: %v", doc, err)
	}
	return &req
}

func termRequest(t *testing.T, terms ...string) *query.Request {
	t.Helper()
	req, err := query.ParseQuery(url.Values{"f": terms})
	if err != nil {
		t.Fatalf("parse %v: %v", terms, err)
	}
	return req
}

// same compares two bound arguments the way the database would see them.
func same(a, b any) bool {
	switch av := a.(type) {
	case time.Time:
		bv, ok := b.(time.Time)
		return ok && av.Equal(bv)
	case []byte:
		bv, ok := b.([]byte)
		return ok && string(av) == string(bv)
	}
	return a == b
}

// A value must mean the same thing whichever door it came through. A filter
// that binds an int64 as JSON and a string as a query string is a bug nobody
// notices until the planner stops using the index.
func TestBothDoorsBindTheSameValue(t *testing.T) {
	for _, tc := range []struct {
		field string
		doc   string // the value as a JSON scalar
		text  string // the same value as query-string text
		want  any
	}{
		{"name", `"hi"`, "hi", "hi"},
		{"active", `true`, "true", true},
		{"small", `7`, "7", int8(7)},
		{"count", `42`, "42", 42},
		// Beyond 2^53: a value that would silently change if it went through a
		// float64 on its way to the database.
		{"big", `9007199254740993`, "9007199254740993", int64(9007199254740993)},
		{"tiny", `255`, "255", uint8(255)},
		{"total", `18446744073709551615`, "18446744073709551615", uint64(18446744073709551615)},
		{"ratio", `1.5`, "1.5", float32(1.5)},
		{"score", `1.25`, "1.25", 1.25},
		{"at", `"2026-01-02T03:04:05Z"`, "2026-01-02T03:04:05Z",
			time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)},
		// The column type's own parser runs on both doors, so "go" arrives as
		// the canonical "GO" either way.
		{"code", `"go"`, "go", code("GO")},
		// crud.Opt and *T columns compare against their element type.
		{"nick", `"ann"`, "ann", "ann"},
		{"age", `31`, "31", 31},
	} {
		t.Run(tc.field, func(t *testing.T) {
			docSQL, docArgs := runSample(t, jsonRequest(t, `{"filter":{"`+tc.field+`":`+tc.doc+`}}`))
			textSQL, textArgs := runSample(t, termRequest(t, tc.field+":eq:"+tc.text))
			if docSQL != textSQL {
				t.Fatalf("the two doors built different SQL:\njson  %s\nquery %s", docSQL, textSQL)
			}
			if len(docArgs) != 1 || len(textArgs) != 1 {
				t.Fatalf("args: json %#v, query %#v", docArgs, textArgs)
			}
			if !same(docArgs[0], tc.want) {
				t.Fatalf("the JSON door bound %#v (%T), want %#v (%T)",
					docArgs[0], docArgs[0], tc.want, tc.want)
			}
			if !same(textArgs[0], tc.want) {
				t.Fatalf("the query-string door bound %#v (%T), want %#v (%T)",
					textArgs[0], textArgs[0], tc.want, tc.want)
			}
		})
	}
}

// Null is the one value that changes the operator rather than the argument, and
// it has to do so on both doors.
func TestNullMeansIsNullOnBothDoors(t *testing.T) {
	docSQL, docArgs := runSample(t, jsonRequest(t, `{"filter":{"nick":null}}`))
	textSQL, textArgs := runSample(t, termRequest(t, "nick:eq:null"))
	if docSQL != `"nick" IS NULL` || textSQL != `"nick" IS NULL` {
		t.Fatalf("json %q, query %q, want both to be \"nick\" IS NULL", docSQL, textSQL)
	}
	if len(docArgs) != 0 || len(textArgs) != 0 {
		t.Fatalf("IS NULL bound arguments: json %#v, query %#v", docArgs, textArgs)
	}
}

// A list keeps its element type, and a null inside one stays null.
func TestListsCoerceEveryElement(t *testing.T) {
	_, args := runSample(t, jsonRequest(t, `{"filter":{"count":{"in":[1,2,3]}}}`))
	for i, arg := range args {
		if _, ok := arg.(int); !ok {
			t.Fatalf("element %d bound as %T, want int", i, arg)
		}
	}
	sql, args := runSample(t, jsonRequest(t, `{"filter":{"nick":{"in":["a",null]}}}`))
	if sql != `"nick" IN ($1, $2)` {
		t.Fatalf("where = %s", sql)
	}
	if len(args) != 2 || args[0] != "a" || args[1] != nil {
		t.Fatalf("args = %#v, want [\"a\" <nil>]", args)
	}
	// The query-string form spells null with the bare word.
	_, args = runSample(t, termRequest(t, "nick:in:a,null"))
	if len(args) != 2 || args[0] != "a" || args[1] != nil {
		t.Fatalf("args = %#v, want [\"a\" <nil>]", args)
	}
}

// Both doors accept the timestamp spellings clients actually send. The
// query-string door used to accept only RFC 3339, because time.Time's own
// UnmarshalText got there first.
func TestEveryTimestampLayoutIsAccepted(t *testing.T) {
	for _, tc := range []struct {
		text string
		want time.Time
	}{
		{"2026-01-02T03:04:05.123456789Z", time.Date(2026, 1, 2, 3, 4, 5, 123456789, time.UTC)},
		{"2026-01-02T03:04:05Z", time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)},
		{"2026-01-02T03:04:05", time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)},
		{"2026-01-02 03:04:05", time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)},
		{"2026-01-02", time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)},
	} {
		t.Run(tc.text, func(t *testing.T) {
			_, docArgs := runSample(t, jsonRequest(t, `{"filter":{"at":{"gte":"`+tc.text+`"}}}`))
			_, textArgs := runSample(t, termRequest(t, "at:gte:"+tc.text))
			for door, args := range map[string][]any{"json": docArgs, "query string": textArgs} {
				got, ok := args[0].(time.Time)
				if !ok {
					t.Fatalf("the %s door bound %#v (%T), want a time.Time", door, args[0], args[0])
				}
				if !got.Equal(tc.want) {
					t.Fatalf("the %s door read %q as %v, want %v", door, tc.text, got, tc.want)
				}
			}
		})
	}
}

// Coerce is exported for transports that have to turn a path parameter into an
// identifier, so every kind it claims to handle is pinned here directly.
func TestCoerceHandlesEveryScalarKind(t *testing.T) {
	for _, tc := range []struct {
		text string
		want any
	}{
		{"hello", "hello"},
		{"true", true},
		{"0", false},
		{"-8", int8(-8)},
		{"-32000", int16(-32000)},
		{"-2000000000", int32(-2000000000)},
		{"-9223372036854775808", int64(-9223372036854775808)},
		{"-1", -1},
		{"255", uint8(255)},
		{"65535", uint16(65535)},
		{"4294967295", uint32(4294967295)},
		{"18446744073709551615", uint64(18446744073709551615)},
		{"7", uint(7)},
		{"1.5", float32(1.5)},
		{"1e10", 1e10},
		// A []byte column takes the text as it stands: a query string has no
		// other encoding to speak of.
		{"hi", []byte("hi")},
		{"go", code("GO")},
		{"2026-01-02T03:04:05Z", time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)},
	} {
		t.Run(reflect.TypeOf(tc.want).String()+"/"+tc.text, func(t *testing.T) {
			got, err := query.Coerce(tc.text, reflect.TypeOf(tc.want))
			if err != nil {
				t.Fatalf("Coerce(%q, %T): %v", tc.text, tc.want, err)
			}
			if !same(got, tc.want) {
				t.Fatalf("Coerce(%q, %T) = %#v, want %#v", tc.text, tc.want, got, tc.want)
			}
		})
	}
}

// A value the column cannot hold is a rejection, not a wrapped or truncated
// number reaching the database.
func TestCoerceRefusesValuesTheColumnCannotHold(t *testing.T) {
	for _, tc := range []struct {
		name string
		text string
		typ  any
	}{
		{"int8 overflow", "300", int8(0)},
		{"uint8 overflow", "256", uint8(0)},
		{"negative uint", "-1", uint(0)},
		{"fraction into an int", "1.5", 0},
		{"word into a bool", "maybe", false},
		{"word into a float", "lots", 0.0},
		{"unparsable timestamp", "yesterday", time.Time{}},
		{"empty custom type", "", code("")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got, err := query.Coerce(tc.text, reflect.TypeOf(tc.typ)); err == nil {
				t.Fatalf("Coerce(%q, %T) = %#v, want a rejection", tc.text, tc.typ, got)
			}
		})
	}
}

// The same refusals arrive as a 400-shaped query error, naming the field and
// the type it expected, rather than as SQL.
func TestBadValuesAreRejectedByBothDoors(t *testing.T) {
	for _, tc := range []struct {
		name, doc, term, want string
	}{
		{"string into an int", `{"filter":{"count":"lots"}}`, "count:eq:lots", "count"},
		{"overflow", `{"filter":{"small":300}}`, "small:eq:300", "small"},
		{"word into a bool", `{"filter":{"active":"yes"}}`, "active:eq:yes", "active"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for door, req := range map[string]*query.Request{
				"json":         jsonRequest(t, tc.doc),
				"query string": termRequest(t, tc.term),
			} {
				_, err := req.Compile(Samples.Meta(), nil)
				if err == nil {
					t.Fatalf("the %s door accepted %s", door, tc.name)
				}
				if !strings.Contains(strings.ToLower(err.Error()), tc.want) {
					t.Fatalf("the %s door said %q, want it to name %q", door, err, tc.want)
				}
			}
		})
	}
}

// An int8 overflow is the loudest case: 300 into an int8 would wrap to 44 and
// quietly match the wrong rows.
func TestOverflowNeverWraps(t *testing.T) {
	req := jsonRequest(t, `{"filter":{"small":300}}`)
	if _, err := req.Compile(Samples.Meta(), nil); err == nil {
		t.Fatal("300 was accepted for an int8 column; it would have bound as 44")
	}
}
