package crud_test

import (
	"reflect"
	"testing"

	"github.com/shardit-io/rx/crud"
)

// Robot is deliberately awkward: a narrow integer key (drivers report int64),
// a pointer column and an Opt column.
type Robot struct {
	ID     int32            `db:"id,pk,auto"`
	Name   string           `db:"name"`
	Weight *float64         `db:"weight"`
	Owner  crud.Opt[string] `db:"owner"`
}

func robotSchema(t *testing.T) *crud.Schema {
	t.Helper()
	s, err := crud.SchemaOf[Robot]()
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// Pointers is what every scan goes through: writing into the destinations must
// land in the model itself, in the order the fields were asked for.
func TestPointersScanIntoTheModel(t *testing.T) {
	s := robotSchema(t)
	var r Robot

	dest, err := s.Pointers(&r, s.Fields)
	if err != nil {
		t.Fatal(err)
	}
	if len(dest) != len(s.Fields) {
		t.Fatalf("got %d destinations for %d fields", len(dest), len(s.Fields))
	}

	*(dest[0].(*int32)) = 7
	*(dest[1].(*string)) = "bender"
	w := 12.5
	*(dest[2].(**float64)) = &w
	if err := dest[3].(*crud.Opt[string]).Scan("ann"); err != nil {
		t.Fatal(err)
	}

	if r.ID != 7 || r.Name != "bender" || r.Weight == nil || *r.Weight != 12.5 {
		t.Fatalf("scanning through the destinations did not reach the model: %+v", r)
	}
	if v, ok := r.Owner.Get(); !ok || v != "ann" {
		t.Fatalf("owner = %v, want the scanned value", r.Owner)
	}
}

// A subset of fields — a projection — yields exactly those destinations.
func TestPointersHonourAProjection(t *testing.T) {
	s := robotSchema(t)
	var r Robot

	dest, err := s.Pointers(&r, []*crud.Field{s.Field("Name")})
	if err != nil {
		t.Fatal(err)
	}
	if len(dest) != 1 {
		t.Fatalf("got %d destinations, want just the projected one", len(dest))
	}
	*(dest[0].(*string)) = "flexo"
	if r.Name != "flexo" || r.ID != 0 {
		t.Fatalf("a one-field projection wrote elsewhere: %+v", r)
	}
}

// Values are the bind arguments of an INSERT: the field's own value, wrappers
// intact, so the driver (or Opt.Value) decides how to encode NULL.
func TestValuesAreTheBindArguments(t *testing.T) {
	s := robotSchema(t)
	w := 3.5
	r := Robot{ID: 7, Name: "bender", Weight: &w, Owner: crud.Null[string]()}

	vals, err := s.Values(&r, s.Insert)
	if err != nil {
		t.Fatal(err)
	}
	if len(vals) != len(s.Insert) {
		t.Fatalf("got %d values for %d insert columns", len(vals), len(s.Insert))
	}
	if vals[0] != int32(7) || vals[1] != "bender" {
		t.Fatalf("values = %#v, want the model's own field values", vals)
	}
	if p, ok := vals[2].(*float64); !ok || p != &w {
		t.Fatalf("a pointer column must be passed through as a pointer, got %#v", vals[2])
	}
	if o, ok := vals[3].(crud.Opt[string]); !ok || !o.IsNull() {
		t.Fatalf("an Opt column must be passed through as an Opt, got %#v", vals[3])
	}
}

func TestIDAndHasID(t *testing.T) {
	s := robotSchema(t)

	var fresh Robot
	if has, err := s.HasID(&fresh); err != nil || has {
		t.Fatalf("HasID on an unsaved model = (%v, %v), want false — that is what routes Save to INSERT", has, err)
	}
	id, err := s.ID(&fresh)
	if err != nil || id != int32(0) {
		t.Fatalf("ID = (%#v, %v), want the zero key", id, err)
	}

	saved := Robot{ID: 42}
	if has, err := s.HasID(&saved); err != nil || !has {
		t.Fatalf("HasID on a keyed model = (%v, %v), want true", has, err)
	}
	if id, err := s.ID(&saved); err != nil || id != int32(42) {
		t.Fatalf("ID = (%#v, %v), want int32(42) — the key's own type", id, err)
	}
}

// A driver hands back LastInsertId as an int64 whatever the column is, so
// SetID has to narrow it.
func TestSetIDConvertsBetweenIntegerWidths(t *testing.T) {
	s := robotSchema(t)
	var r Robot

	if err := s.SetID(&r, int64(9)); err != nil {
		t.Fatal(err)
	}
	if r.ID != 9 {
		t.Fatalf("id = %d, want an int64 LastInsertId narrowed into the int32 key", r.ID)
	}
	if err := s.SetID(&r, int32(11)); err != nil {
		t.Fatal(err)
	}
	if r.ID != 11 {
		t.Fatalf("id = %d, want 11", r.ID)
	}
	if err := s.SetID(&r, nil); err != nil {
		t.Fatal(err)
	}
	if r.ID != 0 {
		t.Fatalf("id = %d, want a nil key to clear the field", r.ID)
	}
	if err := s.SetID(&r, "not-a-number"); err == nil {
		t.Fatal("assigning a string to an int32 key must be refused, not silently dropped")
	}
}

// Every accessor takes a *M of this very model; anything else is a programming
// error worth naming rather than a nil dereference.
func TestAccessorsRejectTheWrongModel(t *testing.T) {
	s := robotSchema(t)
	other := struct{ ID int32 }{}

	for _, tc := range []struct {
		name  string
		model any
	}{
		{"a value instead of a pointer", Robot{}},
		{"a nil pointer", (*Robot)(nil)},
		{"a pointer to another type", &other},
		{"nil", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := s.Pointers(tc.model, s.Fields); err == nil {
				t.Error("Pointers accepted it")
			}
			if _, err := s.Values(tc.model, s.Fields); err == nil {
				t.Error("Values accepted it")
			}
			if _, err := s.ID(tc.model); err == nil {
				t.Error("ID accepted it")
			}
			if _, err := s.HasID(tc.model); err == nil {
				t.Error("HasID accepted it")
			}
			if err := s.SetID(tc.model, int64(1)); err == nil {
				t.Error("SetID accepted it")
			}
		})
	}
}

// ElemValue is what makes a bare int64 out of a request context comparable
// with an Opt[int64] column.
func TestElemValueUnwraps(t *testing.T) {
	n := 5
	for _, tc := range []struct {
		name string
		in   any
		want any
	}{
		{"a set Opt yields the bare value", crud.Set(int64(3)), int64(3)},
		{"a null Opt yields nil", crud.Null[int64](), nil},
		{"an undefined Opt yields nil", crud.Undefined[int64](), nil},
		{"a pointer yields the pointee", &n, 5},
		{"a nil pointer yields nil", (*int)(nil), nil},
		{"a plain value is itself", "x", "x"},
		{"nil is nil", nil, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := crud.ElemValue(tc.in); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("ElemValue(%#v) = %#v, want %#v", tc.in, got, tc.want)
			}
		})
	}
}

// CheckID runs once at Define time so that a repository declared with the wrong
// ID type fails at start-up instead of on the first request.
func TestCheckIDAcceptsTheKeyItsWrappersAndNothingElse(t *testing.T) {
	type optKey struct {
		ID   crud.Opt[int64] `db:"id,pk"`
		Name string          `db:"name"`
	}
	type ptrKey struct {
		ID   *string `db:"id,pk"`
		Name string  `db:"name"`
	}

	plain := robotSchema(t)
	opt, err := crud.SchemaOf[optKey]()
	if err != nil {
		t.Fatal(err)
	}
	ptr, err := crud.SchemaOf[ptrKey]()
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name string
		s    *crud.Schema
		id   reflect.Type
		ok   bool
	}{
		{"exact match", plain, reflect.TypeFor[int32](), true},
		{"a wider integer is still the wrong key", plain, reflect.TypeFor[int64](), false},
		{"the element of an Opt key", opt, reflect.TypeFor[int64](), true},
		{"the element of a pointer key", ptr, reflect.TypeFor[string](), true},
		{"an unrelated type", ptr, reflect.TypeFor[int64](), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.s.CheckID(tc.id)
			if tc.ok && err != nil {
				t.Fatalf("CheckID(%s) on a %s key = %v, want it accepted", tc.id, tc.s.PK.Type, err)
			}
			if !tc.ok && err == nil {
				t.Fatalf("CheckID(%s) on a %s key was accepted; the mismatch has to fail at Define time", tc.id, tc.s.PK.Type)
			}
		})
	}
}
