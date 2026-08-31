package errs_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/frostgrove/vv/errs"
)

func TestAPathRendersThreeWaysFromOneValue(t *testing.T) {
	for _, tc := range []struct {
		name    string
		p       errs.Path
		array   string
		dotted  string
		pointer string
	}{
		{"a name, a position and a name", errs.Path{errs.Named("items"), errs.Indexed(3), errs.Named("email")},
			`["items",3,"email"]`, "items[3].email", "/items/3/email"},
		{"a leading position takes no dot", errs.Path{errs.Indexed(0), errs.Named("email")},
			`[0,"email"]`, "[0].email", "/0/email"},
		{"one name", errs.Path{errs.Named("email")}, `["email"]`, "email", "/email"},
		{"nothing at all", errs.Path{}, `[]`, "", ""},
		{"nothing at all, spelled nil", nil, `[]`, "", ""},

		{"a position zero", errs.Path{errs.Named("a"), errs.Indexed(0)}, `["a",0]`, "a[0]", "/a/0"},
		{"a name that reads like one", errs.Path{errs.Named("a"), errs.Named("0")}, `["a","0"]`, "a.0", "/a/0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, err := json.Marshal(tc.p)
			if err != nil {
				t.Fatalf("marshalling the path failed: %v", err)
			}
			if string(b) != tc.array {
				t.Fatalf("the envelope's field is %s, want %s", b, tc.array)
			}
			if got := tc.p.String(); got != tc.dotted {
				t.Fatalf("the log rendering is %q, want %q", got, tc.dotted)
			}
			if got := tc.p.Pointer(); got != tc.pointer {
				t.Fatalf("the RFC 6901 pointer is %q, want %q", got, tc.pointer)
			}
		})
	}
}

func TestAPointerEscapesWhatRFC6901Requires(t *testing.T) {
	p := errs.Path{errs.Named("a/b"), errs.Named("m~n")}

	if got, want := p.Pointer(), "/a~1b/m~0n"; got != want {
		t.Fatalf("the pointer is %q, want %q — an unescaped slash addresses a different node and nothing downstream would notice", got, want)
	}

	if got, want := p.String(), "a/b.m~n"; got != want {
		t.Fatalf("the log rendering is %q, want %q", got, want)
	}
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(b), `["a/b","m~n"]`; got != want {
		t.Fatalf("the envelope's field is %s, want %s", got, want)
	}
}

func TestParsePathRoundTripsTheDottedForm(t *testing.T) {
	for _, p := range []errs.Path{
		{errs.Named("email")},
		{errs.Named("user"), errs.Named("email")},
		{errs.Named("items"), errs.Indexed(3), errs.Named("email")},
		{errs.Indexed(0), errs.Named("email")},
		{errs.Named("items"), errs.Indexed(0)},
		{errs.Named("user"), errs.Named("userName")},
		{errs.Named("Items3"), errs.Named("Email")},
		{errs.Named("a"), errs.Indexed(11), errs.Indexed(2), errs.Named("b")},
	} {
		t.Run(p.String(), func(t *testing.T) {
			if got := errs.ParsePath(p.String()); !reflect.DeepEqual(got, p) {
				t.Fatalf("%q parsed back to %v, want %v", p.String(), got, p)
			}
		})
	}

	t.Run("a name holding a separator does not survive", func(t *testing.T) {
		p := errs.Path{errs.Named("a.b")}
		got := errs.ParsePath(p.String())
		if reflect.DeepEqual(got, p) {
			t.Fatalf("%q round-tripped, so String is escaping something it is documented not to escape", p.String())
		}
		if want := (errs.Path{errs.Named("a"), errs.Named("b")}); !reflect.DeepEqual(got, want) {
			t.Fatalf("it parsed to %v, want %v", got, want)
		}
	})

	t.Run("an empty string is an empty path", func(t *testing.T) {
		if got := errs.ParsePath(""); len(got) != 0 {
			t.Fatalf("an empty string parsed to %v", got)
		}
	})

	t.Run("a bracket holding something other than a position stays in the name", func(t *testing.T) {
		got := errs.ParsePath("a[x].b")
		want := errs.Path{errs.Named("a[x]"), errs.Named("b")}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("a[x].b parsed to %v, want %v", got, want)
		}
	})
}

func TestAParsedPositionIsANumberOnTheWire(t *testing.T) {
	from := errs.ParsePath("Items[3].Email")
	want := errs.Path{errs.Named("Items"), errs.Indexed(3), errs.Named("Email")}
	if !reflect.DeepEqual(from, want) {
		t.Fatalf("Items[3].Email parsed to %v, want %v", from, want)
	}
	b, err := json.Marshal(from)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "3,") {
		t.Fatalf("the position rendered as %s, and a client reading field[1] needs a number", b)
	}
}

func TestABracketedNegativeNumberStaysPartOfTheName(t *testing.T) {
	got := errs.ParsePath("a[-1].b")
	want := errs.Path{errs.Named("a[-1]"), errs.Named("b")}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("a[-1].b parsed to %d steps %+v, want the two %+v — a negative number is not a position, and there is no minus-first element of a list", len(got), []errs.Step(got), []errs.Step(want))
	}

	got = errs.ParsePath("a[1].b")
	want = errs.Path{errs.Named("a"), errs.Indexed(1), errs.Named("b")}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("a[1].b parsed to %+v, want the three %+v — nothing here makes an index step, so the row above proves nothing", []errs.Step(got), []errs.Step(want))
	}
}

func TestAPathSurvivesTheWireExactly(t *testing.T) {
	paths := map[string]errs.Path{
		"empty":       nil,
		"one name":    {errs.Named("email")},
		"nested":      {errs.Named("items"), errs.Indexed(3), errs.Named("email")},
		"first index": {errs.Indexed(0)},
		"odd member":  {errs.Named("a.b[0]"), errs.Named(`say "hi"`)},
		"deep":        {errs.Indexed(2), errs.Indexed(0), errs.Named("x")},
	}

	for name, want := range paths {
		t.Run(name, func(t *testing.T) {
			raw, err := json.Marshal(want)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			var got errs.Path
			if err := json.Unmarshal(raw, &got); err != nil {
				t.Fatalf("unmarshal %s: %v", raw, err)
			}
			if got.String() != want.String() || len(got) != len(want) {
				t.Fatalf("%s came back as %s (%d steps, was %d)", raw, got.String(), len(got), len(want))
			}
			for i := range want {
				if got[i] != want[i] {
					t.Fatalf("step %d came back as %+v, was %+v", i, got[i], want[i])
				}
			}
		})
	}

	var p errs.Path
	if err := json.Unmarshal([]byte(`["a", true]`), &p); err == nil {
		t.Fatal("a step that is neither a name nor an index was accepted, so the cases above prove nothing")
	}
}
