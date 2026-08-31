package errs_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/frostgrove/vv/errs"
)

type fieldError struct {
	namespace string
	tag       string
	param     string
	value     any
}

func (this fieldError) Namespace() string { return this.namespace }
func (this fieldError) Tag() string       { return this.tag }
func (this fieldError) Param() string     { return this.param }
func (this fieldError) Value() any        { return this.value }

func TestTheMeasuredValidatorNamespacesBecomePaths(t *testing.T) {
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
	got := errs.FromFieldViolations("In", fieldError{namespace: "Other.user.email", tag: "email"})

	want := errs.Path{errs.Named("Other"), errs.Named("user"), errs.Named("email")}
	if !reflect.DeepEqual(got[0].Path, want) {
		t.Fatalf("a namespace that does not start with the root became %v, want %v", got[0].Path, want)
	}

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
	if got := errs.FromFieldViolations[fieldError]("In"); got != nil {
		t.Fatalf("no violations converted to %v, want nil", got)
	}

	if got := errs.FromFieldViolations("In", fieldError{namespace: "In.smth", tag: "required"}); len(got) != 1 {
		t.Fatalf("one violation converted to %d", len(got))
	}
}
