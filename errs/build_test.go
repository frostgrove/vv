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
	// The spelling ROADMAP-errors.md §5 puts in front of a consumer. It has to
	// work as written, and the ambiguity it creates — whose Code? — has to have
	// one answer rather than an inferred one.
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

	// The other half, and each is the other's control: without this one the
	// test passes for a builder that routes every Code to a violation, and
	// without the one above, for a builder that routes every Code to the fault.
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
		{"Internal", errs.Internal(), errs.KindInternal},
	} {
		if got := tc.b.Fault().Kind; got != tc.want {
			t.Fatalf("%s() built a %v", tc.name, got)
		}
	}

	// The table is total, and stays total. A constructor added without a row
	// here is one whose kind nothing pins — TooLarge arrived that way and was
	// caught by a reader rather than by this test, which is the job this loop
	// exists to do. Kind.String is total, so a kind outside its own table
	// renders as "internal" and is how a new one is spotted.
	declared := 0
	for k := errs.Kind(0); k < errs.Kind(64); k++ {
		if k != errs.KindInternal && k.String() == "internal" {
			continue
		}
		declared++
	}
	if declared != 9 {
		t.Fatalf("errs declares %d kinds and this table has 9 rows — a constructor for the new one is owed", declared)
	}

	// The control: nine entry points that all built the same kind would pass
	// any single assertion above. Internal is zero, so it is the one an
	// unwritten field would look like.
	if errs.Validation().Fault().Kind == errs.Internal().Fault().Kind {
		t.Fatalf("Validation and Internal built the same kind")
	}
}

func TestAPerViolationStepWithNoViolationOpenOpensAGeneralOne(t *testing.T) {
	// A misordered chain must not drop what it was given. It produces a fault
	// with a visibly odd violation instead, which is the failure a reader can
	// see.
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
	// Origin, Source, Approximate and Partial are the four steps no other test
	// reads back. Stub all four to `return b` and the whole root module stays
	// green — which is the definition of a step nothing pins.
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

	// The control. Every assertion above compares against a non-zero value, so
	// each one already fails for a step that writes nothing — but only if the
	// zero value is what an unwritten field holds. This says it is, and it is
	// what the four steps would produce if they were stubbed.
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

		// The other half: the key is absent when nothing was capped, so a
		// marshal that always emitted it would not pass the assertion above.
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
	// The same rule Params is held to. The doc comment on Builder names all
	// four; without this only Params is pinned, and the other three could drop
	// what they were given.
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
	// The same rule as Code, and it needs the same two halves. Route every
	// Message to the fault and the whole root module stays green: the violation
	// loses its text, and Fault.Message — developer-facing, never rendered — is
	// overwritten with words meant for a client.
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

	// The control, and each half is the other's: without this one the test
	// passes for a builder that routes every Message to a violation, which is
	// how the fault's developer-facing text would reach the wire.
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

// The copy Fault() promises is a deep one. A shallow copy leaves two faults
// from one builder sharing one Path array, and leaves a caller's scratch slice
// live inside a fault it already handed off — which is what a phase-3 resolver
// rewriting a hop in place would then propagate ([[D-043]]).
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

	// The control on the clone helper: an absent column list must come back
	// absent. Cloning with an unconditional make turns "not known" into "no
	// columns", and every assertion above would still pass.
	t.Run("an absent column list stays nil", func(t *testing.T) {
		f := errs.Conflict().Field("slug").Code(errs.CodeUnique).Fault()

		if f.Detail.Columns != nil || f.Detail.RefColumns != nil || f.Violations[0].Source.Columns != nil {
			t.Fatalf("a fault built with no column lists reports empty ones: %+v", f.Detail)
		}
	})
}

func TestWrappingSkipsANilErrorAndKeepsTheRest(t *testing.T) {
	// A classifier that has a sentinel and no driver error passes both, and a
	// nil in Unwrap() []error is a nil every errors.Is walk has to survive.
	f := errs.Conflict().Code(errs.CodeUnique).Wrapping(errSentinel, nil).Fault()

	for i, err := range f.Unwrap() {
		if err == nil {
			t.Fatalf("Unwrap() element %d is nil", i)
		}
	}
	// The control: a Wrapping that dropped everything would pass the loop
	// above without wrapping anything at all.
	if !errors.Is(f, errSentinel) {
		t.Fatalf("the nil argument took the sentinel with it")
	}
}
