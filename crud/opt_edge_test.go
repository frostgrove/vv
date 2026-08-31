package crud_test

import (
	"database/sql/driver"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/frostgrove/vv/crud"
)

func TestOptUnmarshalsAbsentNullAndValueDifferently(t *testing.T) {
	type patch struct {
		Bio crud.Opt[string] `json:"bio,omitzero"`
	}

	for _, tc := range []struct {
		name string
		body string
		want string
	}{
		{"an absent key never reaches UnmarshalJSON", `{}`, "<undefined>"},
		{"a key that is present elsewhere leaves this one undefined", `{"other":1}`, "<undefined>"},
		{"an explicit null is a null", `{"bio":null}`, "<null>"},
		{"a value is a value", `{"bio":"hi"}`, "hi"},
		{"the empty string is a value, not an absence", `{"bio":""}`, ""},
		{`the four letters "null" quoted are a string`, `{"bio":"null"}`, "null"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var p patch
			if err := json.Unmarshal([]byte(tc.body), &p); err != nil {
				t.Fatal(err)
			}
			if got := p.Bio.String(); got != tc.want {
				t.Fatalf("%s decoded to %s, want %s", tc.body, got, tc.want)
			}
		})
	}
}

func TestOptUnmarshalOverwritesWhateverWasThere(t *testing.T) {
	o := crud.Set("old")
	if err := json.Unmarshal([]byte(`null`), &o); err != nil {
		t.Fatal(err)
	}
	if !o.IsNull() {
		t.Fatalf("decoding null over a set Opt left %v", o)
	}
	if err := json.Unmarshal([]byte(`"new"`), &o); err != nil {
		t.Fatal(err)
	}
	if v, _ := o.Get(); v != "new" {
		t.Fatalf("decoding a value over a null Opt left %v", o)
	}
}

func TestOptUnmarshalOfTheWrongTypeIsAnError(t *testing.T) {
	o := crud.Set(7)
	if err := json.Unmarshal([]byte(`"seven"`), &o); err == nil {
		t.Fatal(`Opt[int] accepted the string "seven"`)
	}
	if v, ok := o.Get(); !ok || v != 7 {
		t.Fatalf("a rejected payload changed the Opt: %v", o)
	}
}

func TestOnlyOmitzeroDropsAnUndefinedFieldOnMarshal(t *testing.T) {
	type tagged struct {
		Bio crud.Opt[string] `json:"bio,omitzero"`
	}
	type untagged struct {
		Bio crud.Opt[string] `json:"bio"`
	}

	if out, err := json.Marshal(tagged{}); err != nil || string(out) != `{}` {
		t.Fatalf("marshal = %s (%v), want an undefined field to vanish", out, err)
	}
	if out, err := json.Marshal(tagged{Bio: crud.Null[string]()}); err != nil || string(out) != `{"bio":null}` {
		t.Fatalf("marshal = %s (%v), want an explicit null on the wire", out, err)
	}
	if out, err := json.Marshal(untagged{}); err != nil || string(out) != `{"bio":null}` {
		t.Fatalf("marshal = %s (%v), want undefined to fall back to null without omitzero", out, err)
	}
}

func TestOptOfAPointer(t *testing.T) {
	set := crud.Set(ptr(5))
	if v, ok := set.Get(); !ok || *v != 5 {
		t.Fatalf("Get = (%v, %v), want the pointer back", v, ok)
	}
	if got, err := set.Value(); err != nil || got != int64(5) {
		t.Fatalf("Value = (%#v, %v), want the pointer dereferenced to int64(5)", got, err)
	}

	nilInside := crud.Set((*int)(nil))
	if !nilInside.IsSet() {
		t.Fatal("a nil pointer inside Set is still set: the field was provided")
	}
	if got, err := nilInside.Value(); err != nil || got != nil {
		t.Fatalf("Value = (%#v, %v), want NULL for a set nil pointer", got, err)
	}
	if out, err := json.Marshal(nilInside); err != nil || string(out) != "null" {
		t.Fatalf("marshal = %s (%v), want null", out, err)
	}

	var back crud.Opt[*int]
	if err := json.Unmarshal([]byte("null"), &back); err != nil {
		t.Fatal(err)
	}
	if !back.IsNull() {
		t.Fatalf("null decoded into Opt[*int] as %v, want a null", back)
	}
}

type Point struct {
	X int `json:"x"`
	Y int `json:"y"`
}

func TestOptOfAStructAndOfASlice(t *testing.T) {
	var p crud.Opt[Point]
	if err := json.Unmarshal([]byte(`{"x":1,"y":2}`), &p); err != nil {
		t.Fatal(err)
	}
	if v, _ := p.Get(); v != (Point{1, 2}) {
		t.Fatalf("decoded %v, want {1 2}", v)
	}
	if out, err := json.Marshal(p); err != nil || string(out) != `{"x":1,"y":2}` {
		t.Fatalf("marshal = %s (%v)", out, err)
	}
	if got, err := p.Value(); err != nil || !reflect.DeepEqual(got, Point{1, 2}) {
		t.Fatalf("Value = (%#v, %v), want the struct handed to the driver as it is", got, err)
	}

	var s crud.Opt[[]string]
	if err := json.Unmarshal([]byte(`[]`), &s); err != nil {
		t.Fatal(err)
	}
	if v, ok := s.Get(); !ok || v == nil || len(v) != 0 {
		t.Fatalf("[] decoded to %#v, want an empty but present slice", v)
	}
	if err := json.Unmarshal([]byte(`null`), &s); err != nil {
		t.Fatal(err)
	}
	if !s.IsNull() {
		t.Fatalf("null decoded to %v, want a null rather than an empty slice", s)
	}
	if got, err := crud.Set([]string{"a"}).Value(); err != nil || !reflect.DeepEqual(got, []string{"a"}) {
		t.Fatalf("Value = (%#v, %v), want the slice passed through untouched", got, err)
	}
}

type Status string

func TestOptScanConvertsWhatDatabaseSQLConverts(t *testing.T) {
	t.Run("int64 into Opt[int]", func(t *testing.T) {
		var o crud.Opt[int]
		mustScan(t, &o, int64(42))
		if v, _ := o.Get(); v != 42 {
			t.Fatalf("scanned %v, want 42", o)
		}
	})
	t.Run("a numeric string into Opt[int]", func(t *testing.T) {
		var o crud.Opt[int]
		mustScan(t, &o, "42")
		if v, _ := o.Get(); v != 42 {
			t.Fatalf("scanned %v, want 42", o)
		}
	})
	t.Run("int64 into Opt[bool]", func(t *testing.T) {
		var o crud.Opt[bool]
		mustScan(t, &o, int64(1))
		if v, _ := o.Get(); !v {
			t.Fatalf("scanned %v, want true", o)
		}
	})
	t.Run("bytes into a named string type", func(t *testing.T) {
		var o crud.Opt[Status]
		mustScan(t, &o, []byte("live"))
		if v, _ := o.Get(); v != "live" {
			t.Fatalf("scanned %v, want live", o)
		}
	})
	t.Run("a time into Opt[time.Time]", func(t *testing.T) {
		when := time.Date(2024, 3, 1, 12, 0, 0, 0, time.UTC)
		var o crud.Opt[time.Time]
		mustScan(t, &o, when)
		if v, _ := o.Get(); !v.Equal(when) {
			t.Fatalf("scanned %v, want %v", o, when)
		}
	})
	t.Run("NULL into Opt[time.Time]", func(t *testing.T) {
		o := crud.Set(time.Now())
		mustScan(t, &o, nil)
		if !o.IsNull() {
			t.Fatalf("NULL scanned as %v, want a null", o)
		}
		if _, ok := o.Get(); ok {
			t.Fatal("a null Opt must not report a value")
		}
	})
}

func TestOptScanOfAnImpossibleValueFailsAndChangesNothing(t *testing.T) {
	o := crud.Set(7)
	err := o.Scan(1.5)
	if err == nil {
		t.Fatal("Opt[int] accepted the float 1.5")
	}
	if v, ok := o.Get(); !ok || v != 7 {
		t.Fatalf("a failed scan changed the value to %v (err %v)", o, err)
	}
}

func TestOptScanCopiesTheDriversBytes(t *testing.T) {
	source := []byte("first")
	var o crud.Opt[[]byte]
	mustScan(t, &o, source)
	copy(source, "SECON")

	if v, _ := o.Get(); string(v) != "first" {
		t.Fatalf("the scanned value followed the driver's buffer: %q, want %q", v, "first")
	}
}

func TestScanningNeverProducesUndefined(t *testing.T) {
	for _, source := range []any{nil, int64(1), "x"} {
		var o crud.Opt[string]
		if err := o.Scan(source); err != nil {
			continue
		}
		if !o.IsDefined() {
			t.Fatalf("scanning %#v left the Opt undefined", source)
		}
	}
}

func TestOptValueForEveryState(t *testing.T) {
	when := time.Date(2024, 3, 1, 12, 0, 0, 0, time.UTC)

	for _, tc := range []struct {
		name string
		o    driver.Valuer
		want any
	}{
		{"a set int is widened to int64 the way database/sql wants", crud.Set(7), int64(7)},
		{"a set time goes through as it is", crud.Set(when), when},
		{"a set []byte goes through as it is", crud.Set([]byte("x")), []byte("x")},
		{"a set uint8 widens too", crud.Set(uint8(3)), int64(3)},

		{"null is NULL", crud.Null[int](), nil},
		{"undefined is NULL as well", crud.Undefined[int](), nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.o.Value()
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("Value = %#v, want %#v", got, tc.want)
			}
		})
	}
}

type Money struct{ Cents int64 }

func (this Money) Value() (driver.Value, error) { return this.Cents, nil }

func TestOptAsksItsElementTypeForItsDriverValue(t *testing.T) {
	got, err := crud.Set(Money{Cents: 250}).Value()
	if err != nil {
		t.Fatal(err)
	}
	if got != int64(250) {
		t.Fatalf("Value = %#v, want the element type's own driver.Value", got)
	}
}

func TestOptOfANilPointerToAValuerIsNullNotAPanic(t *testing.T) {
	got, err := crud.Set((*Money)(nil)).Value()
	if err != nil {
		t.Fatalf("Value = %v, want NULL", err)
	}
	if got != nil {
		t.Fatalf("Value = %#v, want NULL", got)
	}
}

func TestFromPtrOfAZeroValueIsAValueNotAnAbsence(t *testing.T) {
	zero := crud.FromPtr(ptr(0))
	if !zero.IsSet() {
		t.Fatalf("FromPtr(&0) = %v, want a set zero", zero)
	}
	if got, err := zero.Value(); err != nil || got != int64(0) {
		t.Fatalf("Value = (%#v, %v), want 0 written, not NULL", got, err)
	}

	none := crud.FromPtr[int](nil)
	if !none.IsNull() || none.IsDefined() != true {
		t.Fatalf("FromPtr(nil) = %v, want an explicit null", none)
	}
	if got, err := none.Value(); err != nil || got != nil {
		t.Fatalf("Value = (%#v, %v), want NULL", got, err)
	}
}

func TestOptsCompareByStateThenValue(t *testing.T) {
	for _, tc := range []struct {
		name  string
		a, b  crud.Opt[int]
		equal bool
	}{
		{"two sets of the same value", crud.Set(1), crud.Set(1), true},
		{"two sets of different values", crud.Set(1), crud.Set(2), false},
		{"two nulls", crud.Null[int](), crud.Null[int](), true},
		{"two undefineds", crud.Undefined[int](), crud.Opt[int]{}, true},
		{"null is not undefined", crud.Null[int](), crud.Undefined[int](), false},
		{"a set zero is not undefined", crud.Set(0), crud.Undefined[int](), false},
		{"a set zero is not null", crud.Set(0), crud.Null[int](), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if (tc.a == tc.b) != tc.equal {
				t.Fatalf("%v == %v reported %v, want %v", tc.a, tc.b, tc.a == tc.b, tc.equal)
			}
		})
	}
}

func TestMustGetPanicsOnlyWhenThereIsNoValue(t *testing.T) {
	if got := crud.Set(3).MustGet(); got != 3 {
		t.Fatalf("MustGet = %v, want 3", got)
	}
	for _, o := range []crud.Opt[int]{crud.Null[int](), crud.Undefined[int]()} {
		func() {
			defer func() {
				if recover() == nil {
					t.Fatalf("MustGet on %v returned instead of panicking", o)
				}
			}()
			_ = o.MustGet()
		}()
	}
}

func mustScan(t *testing.T, o interface{ Scan(any) error }, source any) {
	t.Helper()
	if err := o.Scan(source); err != nil {
		t.Fatalf("Scan(%#v): %v", source, err)
	}
}
