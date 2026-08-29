// Package bridge holds the one assertion in the repository that imports a
// validation library.
//
// errs.FieldViolation is satisfied structurally, so neither package imports the
// other and the bridge costs no dependency ([[D-033]]). The cost of that is
// that a signature change in the library is not a compile error in errs — it is
// a failed type assertion at a consumer's call site. This is what turns it back
// into a compile error, and it lives in the test module because the library is
// a driver-and-framework dependency the library itself must never take
// ([[D-036]]).
package bridge

import (
	"reflect"
	"strings"
	"testing"

	"github.com/frostgrove/vv/errs"
	"github.com/go-playground/validator/v10"
)

// The signatures still match. This is the assertion the whole dependency-free
// bridge rests on, and it proves nothing about the values.
var _ errs.FieldViolation = (validator.FieldError)(nil)

// In is ROADMAP-errors.md §2's own input DTO.
type In struct {
	Smth string `json:"smth" validate:"required"`
	User struct {
		Email string `json:"email" validate:"email"`
		OrgID int    `json:"org_id"`
		Age   int    `json:"age" validate:"gte=18"`
	} `json:"user"`
}

func payload() In {
	var in In
	in.User.Email = "nope"
	in.User.OrgID = 42
	in.User.Age = 15
	return in
}

// jsonNames is the one thing a consumer must register. Without it every path is
// silently wrong — which is what the second test below asserts.
func jsonNames(fld reflect.StructField) string {
	name := strings.SplitN(fld.Tag.Get("json"), ",", 2)[0]
	if name == "-" {
		return ""
	}
	return name
}

func namespaces(t *testing.T, verrs validator.ValidationErrors) []string {
	t.Helper()
	out := make([]string, 0, len(verrs))
	for _, fe := range verrs {
		out = append(out, fe.Namespace())
	}
	return out
}

func validate(t *testing.T, v *validator.Validate) validator.ValidationErrors {
	t.Helper()
	err := v.Struct(payload())
	if err == nil {
		t.Fatalf("the payload passed validation, so there is nothing to convert")
	}
	verrs, ok := err.(validator.ValidationErrors)
	if !ok {
		t.Fatalf("the validator reported %T, not ValidationErrors", err)
	}
	if len(verrs) != 3 {
		t.Fatalf("the payload produced %d violations (%v), want 3", len(verrs), namespaces(t, verrs))
	}
	return verrs
}

func TestValidatorFieldErrorSatisfiesFieldViolation(t *testing.T) {
	v := validator.New()
	v.RegisterTagNameFunc(jsonNames)
	verrs := validate(t, v)

	if got, want := namespaces(t, verrs), []string{"In.smth", "In.user.email", "In.user.age"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("the validator reported %v, want %v — the paths below are read straight out of these", got, want)
	}

	// One line, no explicit type argument: T is inferred and ValidationErrors
	// passes straight through as the variadic.
	vs := errs.FromFieldViolations("In", verrs...)

	for i, want := range []errs.Path{
		{errs.Named("smth")},
		{errs.Named("user"), errs.Named("email")},
		{errs.Named("user"), errs.Named("age")},
	} {
		if !reflect.DeepEqual(vs[i].Path, want) {
			t.Fatalf("%q became %v, want %v", verrs[i].Namespace(), vs[i].Path, want)
		}
		if string(vs[i].Code) != verrs[i].Tag() {
			t.Fatalf("the tag %q became code %q", verrs[i].Tag(), vs[i].Code)
		}
		if vs[i].Origin != errs.OriginInput {
			t.Fatalf("%q is %v, and a validator only ever reads the payload", verrs[i].Namespace(), vs[i].Origin)
		}
	}
	if vs[2].Params["param"] != "18" || vs[2].Params["value"] != 15 {
		t.Fatalf("the gte violation carried params %v", vs[2].Params)
	}
}

// The control, in the shape of test/integration/gate_relscope_test.go's "not
// declared" subtest: it asserts the wrong paths are there without the
// registration. Without it the test above passes for a validator that reported
// json names by default, and the claim that registering the tag-name function
// is the one thing a consumer must do would be untested.
func TestWithoutTheTagNameFuncEveryPathIsGoFieldNames(t *testing.T) {
	verrs := validate(t, validator.New())

	if got, want := namespaces(t, verrs), []string{"In.Smth", "In.User.Email", "In.User.Age"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("the validator reported %v without RegisterTagNameFunc, want %v — the registration has stopped mattering, so the test above proves nothing", got, want)
	}

	vs := errs.FromFieldViolations("In", verrs...)
	want := errs.Path{errs.Named("User"), errs.Named("Email")}
	if !reflect.DeepEqual(vs[1].Path, want) {
		t.Fatalf("%q became %v, want the Go field names %v", verrs[1].Namespace(), vs[1].Path, want)
	}
}
