package crud_test

import (
	"database/sql/driver"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/shardit-io/rx/crud"
)

func TestOptStates(t *testing.T) {
	var undefined crud.Opt[int]
	set := crud.Set(7)
	null := crud.Null[int]()

	for _, tc := range []struct {
		name            string
		o               crud.Opt[int]
		defined, isNull bool
		isSet           bool
		value           int
		valueOK         bool
	}{
		{"undefined", undefined, false, false, false, 0, false},
		{"set", set, true, false, true, 7, true},
		{"null", null, true, true, false, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.o.IsDefined() != tc.defined || tc.o.IsNull() != tc.isNull || tc.o.IsSet() != tc.isSet {
				t.Fatalf("%v: defined=%v null=%v set=%v", tc.o, tc.o.IsDefined(), tc.o.IsNull(), tc.o.IsSet())
			}
			v, ok := tc.o.Get()
			if v != tc.value || ok != tc.valueOK {
				t.Fatalf("Get = (%v, %v)", v, ok)
			}
		})
	}

	if got := undefined.OrElse(3); got != 3 {
		t.Errorf("OrElse = %v", got)
	}
	if got := set.OrElse(3); got != 7 {
		t.Errorf("OrElse = %v", got)
	}
	if p := set.Ptr(); p == nil || *p != 7 {
		t.Errorf("Ptr = %v", p)
	}
	if null.Ptr() != nil {
		t.Error("Ptr on a null Opt should be nil")
	}
	if !undefined.IsZero() || set.IsZero() || null.IsZero() {
		t.Error("only an undefined Opt is the zero value")
	}
}

// The whole point of Opt: an absent JSON key and an explicit null are different
// intents, and a PATCH body has to keep them apart.
func TestOptJSONRoundTrip(t *testing.T) {
	type patch struct {
		Name string              `json:"name"`
		Age  crud.Opt[int]       `json:"age,omitzero"`
		Bio  crud.Opt[string]    `json:"bio,omitzero"`
		When crud.Opt[time.Time] `json:"when,omitzero"`
	}

	var p patch
	if err := json.Unmarshal([]byte(`{"name":"a","age":null}`), &p); err != nil {
		t.Fatal(err)
	}
	if !p.Age.IsNull() {
		t.Errorf("age = %v, want an explicit null", p.Age)
	}
	if p.Bio.IsDefined() {
		t.Errorf("bio = %v, want undefined", p.Bio)
	}

	out, err := json.Marshal(patch{Name: "a", Age: crud.Null[int](), Bio: crud.Set("hi")})
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != `{"name":"a","age":null,"bio":"hi"}` {
		t.Errorf("marshal = %s", out)
	}
}

func TestOptSQL(t *testing.T) {
	var o crud.Opt[int]
	if err := o.Scan(int64(42)); err != nil {
		t.Fatal(err)
	}
	if v, ok := o.Get(); !ok || v != 42 {
		t.Fatalf("scanned %v", o)
	}
	if err := o.Scan(nil); err != nil {
		t.Fatal(err)
	}
	if !o.IsNull() {
		t.Fatalf("scanning NULL gave %v", o)
	}

	// Scanning a []byte into a string Opt goes through database/sql's own
	// conversion rules.
	var s crud.Opt[string]
	if err := s.Scan([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	if v, _ := s.Get(); v != "hello" {
		t.Fatalf("scanned %v", s)
	}

	for _, tc := range []struct {
		o    driver.Valuer
		want any
	}{
		{crud.Set(5), int64(5)},
		{crud.Null[int](), nil},
		{crud.Undefined[int](), nil},
		{crud.Set("x"), "x"},
		{crud.Set(true), true},
	} {
		got, err := tc.o.Value()
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%v.Value() = %#v, want %#v", tc.o, got, tc.want)
		}
	}
}

func TestFromPtr(t *testing.T) {
	if o := crud.FromPtr[int](nil); !o.IsNull() {
		t.Errorf("nil pointer should mean null, got %v", o)
	}
	v := 3
	if o := crud.FromPtr(&v); !o.IsSet() {
		t.Errorf("non-nil pointer should be set, got %v", o)
	}
}
