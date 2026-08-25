package errs_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/shardit-io/vv/errs"
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

		// The control. A position and a name that happens to be a digit must
		// differ in the array and in the dotted form, or the implementation is
		// stringifying every step and the index rules above are untested. The
		// pointer is the one rendering where they coincide, and that is RFC
		// 6901 rather than a gap: a reference token is a string, so an index
		// and the member named "0" address the same node. Making Pointer tell
		// them apart would be the defect.
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

	// The control: the escaping belongs to the pointer alone. Applying it to
	// the other two would corrupt the one rendering a client actually reads.
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

	// The control, and the documented limit: the dotted form is a log
	// rendering, not a parseable one. A later "fix" that escaped separators in
	// String would change every log line in the tree, and without this case the
	// suite would stay green while it did.
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
	// A path built by hand and one parsed from the same text are the same
	// value, which is what lets the bridge and the message ladder agree.
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
	// A position is an index into a list, and there is no minus-first element.
	// Parsed as one it would be an index step the renderings then print as
	// [-1], which no framework resolves.
	// Compared step by step, not by rendering: Named("a[-1]") and the pair
	// Named("a"), Indexed(-1) both render as a[-1], so a message printing the
	// two paths would show the same text twice and say nothing.
	got := errs.ParsePath("a[-1].b")
	want := errs.Path{errs.Named("a[-1]"), errs.Named("b")}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("a[-1].b parsed to %d steps %+v, want the two %+v — a negative number is not a position, and there is no minus-first element of a list", len(got), []errs.Step(got), []errs.Step(want))
	}

	// The control: a non-negative number in the same position is an index, so
	// the row above is about the sign and not about a parser that never makes
	// an index at all.
	got = errs.ParsePath("a[1].b")
	want = errs.Path{errs.Named("a"), errs.Indexed(1), errs.Named("b")}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("a[1].b parsed to %+v, want the three %+v — nothing here makes an index step, so the row above proves nothing", []errs.Step(got), []errs.Step(want))
	}
}
