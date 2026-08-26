package errs_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/frostgrove/vv/errs"
)

// fieldError is what a validation library hands over, written out here so this
// package's tests import nothing. The live assertion that the real type still
// has these four methods is in test/bridge, the one module allowed to import a
// validator.
type fieldError struct {
	namespace string
	tag       string
	param     string
	value     any
}

func (f fieldError) Namespace() string { return f.namespace }
func (f fieldError) Tag() string       { return f.tag }
func (f fieldError) Param() string     { return f.param }
func (f fieldError) Value() any        { return f.value }

func TestTheMeasuredValidatorNamespacesBecomePaths(t *testing.T) {
	// The three rows measured against v10.30.1 with the roadmap's own input DTO.
	vs := []fieldError{
		{namespace: "In.smth", tag: "required"},
		{namespace: "In.user.email", tag: "email", value: "nope"},
		{namespace: "In.user.age", tag: "gte", param: "18", value: 15},
	}
	got := errs.FromFieldViolations("In", vs...)

	if len(got) != len(vs) {
		t.Fatalf("converted %d of %d violations", len(got), len(vs))
	}
	for i, want := range []errs.Violation{
		{Path: errs.Path{errs.Named("smth")}, Code: "required"},
		{Path: errs.Path{errs.Named("user"), errs.Named("email")}, Code: "email", Params: errs.P{"value": any("nope")}},
		{Path: errs.Path{errs.Named("user"), errs.Named("age")}, Code: "gte", Params: errs.P{"param": "18", "value": any(15)}},
	} {
		if !reflect.DeepEqual(got[i].Path, want.Path) {
			t.Fatalf("%q became %v, want %v", vs[i].namespace, got[i].Path, want.Path)
		}
		if got[i].Code != want.Code {
			t.Fatalf("the tag %q became code %q", vs[i].tag, got[i].Code)
		}
		if !reflect.DeepEqual(got[i].Params, want.Params) {
			t.Fatalf("%q carried params %v, want %v", vs[i].namespace, got[i].Params, want.Params)
		}
		if got[i].Origin != errs.OriginInput {
			t.Fatalf("%q is %v — everything a validator reports is the caller's own payload", vs[i].namespace, got[i].Origin)
		}
		if got[i].Approximate {
			t.Fatalf("%q was marked approximate; the library reported the path itself", vs[i].namespace)
		}
	}
}

func TestARootThatDoesNotMatchKeepsEverySegment(t *testing.T) {
	// The control for the strip above. An unconditional first-segment drop
	// turns a mistyped root into a silently wrong path — user.email becoming
	// email — where match-or-keep leaves one visibly extra step.
	got := errs.FromFieldViolations("In", fieldError{namespace: "Other.user.email", tag: "email"})

	want := errs.Path{errs.Named("Other"), errs.Named("user"), errs.Named("email")}
	if !reflect.DeepEqual(got[0].Path, want) {
		t.Fatalf("a namespace that does not start with the root became %v, want %v", got[0].Path, want)
	}

	// And an empty root strips nothing at all.
	got = errs.FromFieldViolations("", fieldError{namespace: "In.user.email", tag: "email"})
	want = errs.Path{errs.Named("In"), errs.Named("user"), errs.Named("email")}
	if !reflect.DeepEqual(got[0].Path, want) {
		t.Fatalf("with no root the path became %v, want %v", got[0].Path, want)
	}
}

func TestAnIndexedNamespaceBecomesAnIndexStep(t *testing.T) {
	got := errs.FromFieldViolations("In", fieldError{namespace: "In.Items[3].Email", tag: "email"})

	want := errs.Path{errs.Named("Items"), errs.Indexed(3), errs.Named("Email")}
	if !reflect.DeepEqual(got[0].Path, want) {
		t.Fatalf("In.Items[3].Email became %v, want %v", got[0].Path, want)
	}
	b, err := json.Marshal(got[0].Path)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != `["Items",3,"Email"]` {
		t.Fatalf("it renders as %s, and a client reading the position needs a number", b)
	}

	// The control: without it the test passes for a parser that treats any
	// trailing digits as a position, and — since none of the three measured
	// namespaces contains an index at all — for one that ignores brackets.
	got = errs.FromFieldViolations("In", fieldError{namespace: "In.Items3.Email", tag: "email"})
	want = errs.Path{errs.Named("Items3"), errs.Named("Email")}
	if !reflect.DeepEqual(got[0].Path, want) {
		t.Fatalf("In.Items3.Email became %v, want %v", got[0].Path, want)
	}
	b, err = json.Marshal(got[0].Path)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != `["Items3","Email"]` {
		t.Fatalf("it renders as %s, want the name as a string", b)
	}
}

func TestNoFieldViolationsConvertToNoSlice(t *testing.T) {
	// A caller ranges the result, and an empty non-nil slice reads as "the
	// validator ran and found nothing" exactly as nil does — but a caller who
	// branches on == nil to mean "nothing to report" gets the wrong answer.
	if got := errs.FromFieldViolations[fieldError]("In"); got != nil {
		t.Fatalf("no violations converted to %v, want nil", got)
	}

	// The control: one violation still produces one, so the row above is not
	// passing for a function that returns nil whatever it is given.
	if got := errs.FromFieldViolations("In", fieldError{namespace: "In.smth", tag: "required"}); len(got) != 1 {
		t.Fatalf("one violation converted to %d", len(got))
	}
}
