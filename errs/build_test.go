package errs_test

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/frostgrove/vv/errs"
)

func TestAViolationCodeIsNotTheFaultsCode(t *testing.T) {
	t.Run("a code after a field belongs to the violation", func(t *testing.T) {
		f := errs.Validation().
			Field("Age").Code("too_young").Params(errs.P{"min": 18}).
			Fault()

		if len(f.Violations) != 1 {
			t.Fatalf("built %d violations, want 1", len(f.Violations))
		}
		v := f.Violations[0]
		if v.Code != "too_young" {
			t.Fatalf("the violation's code is %q", v.Code)
		}
		if !reflect.DeepEqual(v.Path, errs.Path{errs.Named("Age")}) {
			t.Fatalf("the violation's path is %v", v.Path)
		}
		if v.Params["min"] != 18 {
			t.Fatalf("the violation's params are %v", v.Params)
		}
		if f.Code != "" {
			t.Fatalf("the fault took the violation's code as its own: %q", f.Code)
		}
	})

	t.Run("a code before any field belongs to the fault", func(t *testing.T) {
		f := errs.NotFound().Op("GetByID").Entity("User").Code(errs.CodeNotFound).Fault()

		if f.Code != errs.CodeNotFound {
			t.Fatalf("the fault's code is %q", f.Code)
		}
		if len(f.Violations) != 0 {
			t.Fatalf("a fault with no field named built %d violations", len(f.Violations))
		}
	})

	t.Run("a second Params call merges", func(t *testing.T) {
		f := errs.Validation().
			Field("Age").Code("too_young").Params(errs.P{"min": 18}).Params(errs.P{"got": 15}).
			Fault()

		v := f.Violations[0]
		if v.Params["min"] != 18 || v.Params["got"] != 15 {
			t.Fatalf("the second call replaced rather than merged: %v", v.Params)
		}
	})

	t.Run("each fault is its own copy", func(t *testing.T) {
		b := errs.Validation().Field("Age").Code("too_young").Params(errs.P{"min": 18})
		first := b.Fault()

		first.Violations[0].Code = "tampered"
		first.Violations[0].Params["min"] = 0
		second := b.Fault()

		if second.Violations[0].Code != "too_young" || second.Violations[0].Params["min"] != 18 {
			t.Fatalf("mutating a returned fault changed the builder: %v", second.Violations[0])
		}
	})
}

func TestEveryEntryPointCarriesItsKind(t *testing.T) {
	for _, tc := range []struct {
		name string
		b    *errs.Builder
		want errs.Kind
	}{
		{"Validation", errs.Validation(), errs.KindValidation},
		{"Conflict", errs.Conflict(), errs.KindConflict},
		{"NotFound", errs.NotFound(), errs.KindNotFound},
		{"Forbidden", errs.Forbidden(), errs.KindForbidden},
		{"Unauthorized", errs.Unauthorized(), errs.KindUnauthorized},
		{"BadRequest", errs.BadRequest(), errs.KindBadRequest},
		{"Retryable", errs.Retryable(), errs.KindRetryable},
		{"TooLarge", errs.TooLarge(), errs.KindTooLarge},
		{"MethodNotAllowed", errs.MethodNotAllowed(), errs.KindMethodNotAllowed},
		{"Internal", errs.Internal(), errs.KindInternal},
	} {
		if got := tc.b.Fault().Kind; got != tc.want {
			t.Fatalf("%s() built a %v", tc.name, got)
		}
	}

	declared := 0
	for k := errs.Kind(0); k < errs.Kind(64); k++ {
		if k != errs.KindInternal && k.String() == "internal" {
			continue
		}
		declared++
	}
	if declared != 10 {
		t.Fatalf("errs declares %d kinds and this table has 10 rows — a constructor for the new one is owed", declared)
	}

	if errs.Validation().Fault().Kind == errs.Internal().Fault().Kind {
		t.Fatalf("Validation and Internal built the same kind")
	}
}

func TestAPerViolationStepWithNoViolationOpenOpensAGeneralOne(t *testing.T) {
	f := errs.Validation().Params(errs.P{"min": 18}).Fault()

	if len(f.Violations) != 1 {
		t.Fatalf("Params before any field built %d violations, want 1", len(f.Violations))
	}
	if f.Violations[0].Params["min"] != 18 {
		t.Fatalf("the parameters were dropped: %v", f.Violations[0].Params)
	}
	if len(f.Violations[0].Path) != 0 {
		t.Fatalf("the violation was given a path nobody asked for: %v", f.Violations[0].Path)
	}
}

func TestTheStepsThatWriteNothingElseReadsStillWrite(t *testing.T) {
	t.Run("the per-violation steps land on the open violation", func(t *testing.T) {
		f := errs.Conflict().
			Field("email").Code(errs.CodeUnique).
			Origin(errs.OriginState).
			Source(errs.Source{Table: "cp_parent", Schema: "public", Columns: []string{"slug"}, Constraint: "cp_parent_slug_key"}).
			Approximate(true).
			Fault()

		v := f.Violations[0]
		if v.Origin != errs.OriginState {
			t.Fatalf("Origin(OriginState) left the violation at %v", v.Origin)
		}
		if v.Source.Constraint != "cp_parent_slug_key" || v.Source.Table != "cp_parent" ||
			v.Source.Schema != "public" || !reflect.DeepEqual(v.Source.Columns, []string{"slug"}) {
			t.Fatalf("Source did not reach the violation: %+v", v.Source)
		}
		if !v.Approximate {
			t.Fatalf("Approximate(true) left the violation resolved")
		}
	})

	t.Run("without the steps the same violation is the zero one", func(t *testing.T) {
		f := errs.Conflict().Field("email").Code(errs.CodeUnique).Fault()

		v := f.Violations[0]
		if v.Origin != errs.OriginInput || !reflect.DeepEqual(v.Source, errs.Source{}) || v.Approximate {
			t.Fatalf("an unwritten violation is not zero, so the test above proves nothing: %+v", v)
		}
		if f.Partial {
			t.Fatalf("an unwritten fault is already partial, so the test below proves nothing")
		}
	})

	t.Run("Partial reaches the fault and the wire", func(t *testing.T) {
		f := errs.Validation().Field("email").Code(errs.CodeUnique).Partial(true).Fault()

		if !f.Partial {
			t.Fatalf("Partial(true) left the fault complete")
		}
		b, err := json.Marshal(f)
		if err != nil {
			t.Fatalf("marshalling the capped fault failed: %v", err)
		}
		if !strings.Contains(string(b), `"partial":true`) {
			t.Fatalf("a capped answer did not say so on the wire: %s", b)
		}

		full, err := json.Marshal(errs.Validation().Field("email").Code(errs.CodeUnique).Fault())
		if err != nil {
			t.Fatalf("marshalling the complete fault failed: %v", err)
		}
		if strings.Contains(string(full), "partial") {
			t.Fatalf("a complete answer called itself partial: %s", full)
		}
	})
}

func TestOriginSourceAndApproximateAlsoOpenAGeneralViolation(t *testing.T) {
	for _, tc := range []struct {
		name string
		b    *errs.Builder
		ok   func(errs.Violation) bool
	}{
		{"Origin", errs.Conflict().Origin(errs.OriginState), func(v errs.Violation) bool { return v.Origin == errs.OriginState }},
		{"Source", errs.Conflict().Source(errs.Source{Constraint: "cp_parent_slug_key"}), func(v errs.Violation) bool { return v.Source.Constraint == "cp_parent_slug_key" }},
		{"Approximate", errs.Conflict().Approximate(true), func(v errs.Violation) bool { return v.Approximate }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := tc.b.Fault()

			if len(f.Violations) != 1 {
				t.Fatalf("%s before any field built %d violations, want 1", tc.name, len(f.Violations))
			}
			if len(f.Violations[0].Path) != 0 {
				t.Fatalf("the violation was given a path nobody asked for: %v", f.Violations[0].Path)
			}
			if !tc.ok(f.Violations[0]) {
				t.Fatalf("%s opened a violation and then dropped what it was given: %+v", tc.name, f.Violations[0])
			}
		})
	}
}

func TestAViolationMessageIsNotTheFaultsMessage(t *testing.T) {
	t.Run("a message after a field belongs to the violation", func(t *testing.T) {
		f := errs.Conflict().
			Code(errs.CodeUnique).Message("constraint cp_parent_slug_key on cp_parent").
			Field("slug").Code(errs.CodeUnique).Message("this value is already taken").
			Fault()

		if len(f.Violations) != 1 {
			t.Fatalf("built %d violations, want 1", len(f.Violations))
		}
		if got := f.Violations[0].Message; got != "this value is already taken" {
			t.Fatalf("the violation's message is %q", got)
		}
		if f.Message != "constraint cp_parent_slug_key on cp_parent" {
			t.Fatalf("the violation's message overwrote the fault's: %q", f.Message)
		}
		b, err := json.Marshal(f)
		if err != nil {
			t.Fatalf("marshalling the fault failed: %v", err)
		}
		if !strings.Contains(string(b), `"message":"this value is already taken"`) {
			t.Fatalf("the violation's message never reached the wire: %s", b)
		}
	})

	t.Run("a message before any field belongs to the fault", func(t *testing.T) {
		f := errs.Conflict().Message("constraint cp_parent_slug_key on cp_parent").Fault()

		if f.Message != "constraint cp_parent_slug_key on cp_parent" {
			t.Fatalf("the fault's message is %q", f.Message)
		}
		if len(f.Violations) != 0 {
			t.Fatalf("a message with no field named built %d violations", len(f.Violations))
		}
	})
}

func TestAFaultDoesNotShareASliceWithTheBuilderOrTheCaller(t *testing.T) {
	t.Run("two faults from one builder share no path", func(t *testing.T) {
		b := errs.Validation().Field("Age").Code("too_young")
		first, second := b.Fault(), b.Fault()

		first.Violations[0].Path[0] = errs.Named("Name")
		if got := second.Violations[0].Path.String(); got != "Age" {
			t.Fatalf("writing into one fault's path moved another's to %q", got)
		}
	})

	t.Run("a caller's path does not stay live inside the fault", func(t *testing.T) {
		p := errs.Path{errs.Named("user"), errs.Named("email")}
		f := errs.Validation().At(p).Code(errs.CodeRequired).Fault()

		p[1] = errs.Named("HACKED")
		if got := f.Violations[0].Path.String(); got != "user.email" {
			t.Fatalf("the fault's path followed the caller's scratch value to %q", got)
		}
	})

	t.Run("a caller's column lists do not stay live either", func(t *testing.T) {
		cols := []string{"slug"}
		dcols, refs := []string{"a"}, []string{"b"}
		f := errs.Conflict().
			Detail(errs.Detail{Columns: dcols, RefColumns: refs}).
			General().Source(errs.Source{Columns: cols}).
			Fault()

		cols[0], dcols[0], refs[0] = "secret", "secret", "secret"
		if got := f.Violations[0].Source.Columns[0]; got != "slug" {
			t.Fatalf("the violation's source columns followed the caller's slice to %q", got)
		}
		if got := f.Detail.Columns[0]; got != "a" {
			t.Fatalf("the detail's columns followed the caller's slice to %q", got)
		}
		if got := f.Detail.RefColumns[0]; got != "b" {
			t.Fatalf("the detail's referenced columns followed the caller's slice to %q", got)
		}
	})

	t.Run("an absent column list stays nil", func(t *testing.T) {
		f := errs.Conflict().Field("slug").Code(errs.CodeUnique).Fault()

		if f.Detail.Columns != nil || f.Detail.RefColumns != nil || f.Violations[0].Source.Columns != nil {
			t.Fatalf("a fault built with no column lists reports empty ones: %+v", f.Detail)
		}
	})
}

func TestWrappingSkipsANilErrorAndKeepsTheRest(t *testing.T) {
	f := errs.Conflict().Code(errs.CodeUnique).Wrapping(errSentinel, nil).Fault()

	for i, err := range f.Unwrap() {
		if err == nil {
			t.Fatalf("Unwrap() element %d is nil", i)
		}
	}

	if !errors.Is(f, errSentinel) {
		t.Fatalf("the nil argument took the sentinel with it")
	}
}
