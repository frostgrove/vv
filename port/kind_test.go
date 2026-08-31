package port

import (
	"errors"
	"fmt"
	"testing"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/query"
	"github.com/frostgrove/vv/errs"
)

func TestThePrecedenceTableResolvesAMixedFault(t *testing.T) {
	order := []struct {
		kind errs.Kind
		code errs.Code
	}{
		{errs.KindInternal, errs.CodeInternal},
		{errs.KindNotFound, errs.CodeNotFound},
		{errs.KindUnauthorized, errs.CodeUnauthenticated},
		{errs.KindForbidden, errs.CodeForbidden},
		{errs.KindRetryable, errs.CodeDeadlock},
		{errs.KindConflict, errs.CodeUnique},
		{errs.KindValidation, errs.CodeTooLong},
		{errs.KindBadRequest, errs.CodeBadQuery},
	}
	for i := 0; i+1 < len(order); i++ {
		high, low := order[i], order[i+1]
		t.Run(fmt.Sprintf("%v beats %v", high.kind, low.kind), func(t *testing.T) {
			for _, one := range []struct {
				kind errs.Kind
				code errs.Code
			}{high, low} {
				f := errs.New(one.kind).Code(one.code).General().Code(one.code).Fault()
				if got := KindOf(f); got != one.kind {
					t.Fatalf("%v alone resolved to %v", one.kind, got)
				}
			}
			first := errs.New(high.kind).Code(high.code).
				General().Code(high.code).General().Code(low.code).Fault()
			second := errs.New(low.kind).Code(low.code).
				General().Code(low.code).General().Code(high.code).Fault()
			for _, f := range []*errs.Fault{first, second} {
				if got := KindOf(f); got != high.kind {
					t.Fatalf("%v mixed with %v resolved to %v, want %v",
						high.kind, low.kind, got, high.kind)
				}
			}
		})
	}

	novel := errs.Validation().Code("too_young").Field("Age").Code("too_young").Fault()
	if got := KindOf(novel); got != errs.KindValidation {
		t.Fatalf("a fault carrying an undeclared code resolved to %v, want its own kind", got)
	}
}

func TestEverySentinelGetsItsKindWithoutAFault(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want errs.Kind
	}{
		{"a missing row", crud.ErrNotFound, errs.KindNotFound},
		{"a denial", crud.ErrForbidden, errs.KindForbidden},
		{"a collision", crud.ErrConflict, errs.KindConflict},
		{"a stale write", crud.ErrStaleVersion, errs.KindConflict},
		{"a save with no key", crud.ErrMissingID, errs.KindBadRequest},
		{"an invalid repository operation", crud.ErrBadRequest, errs.KindBadRequest},
		{"a rejected query document", &query.Error{Path: "filter", Reason: "unknown field"}, errs.KindBadRequest},
		{"a field the model lacks", &crud.UnknownFieldError{Model: "Widget", Field: "nope"}, errs.KindBadRequest},
		{"a declaration that does not hold together", &crud.SchemaError{Model: "Widget", Reason: "no primary key"}, errs.KindBadRequest},
		{"a binding's own refusal", BadRequestf("nope"), errs.KindBadRequest},
		{"an error nobody recognises", errors.New("boom"), errs.KindInternal},
		{"no error at all", nil, errs.KindInternal},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := KindOf(tc.err); got != tc.want {
				t.Fatalf("KindOf(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}

	both := fmt.Errorf("%w: %w", crud.ErrNotFound, crud.ErrForbidden)
	if got := KindOf(both); got != errs.KindNotFound {
		t.Fatalf("an error wrapping both sentinels resolved to %v, want NotFound", got)
	}
}

func TestTheDefaultVocabularyIsNeverHandedOut(t *testing.T) {
	f := errs.Validation().Code("too_young").Field("Age").Code("too_young").Fault()

	mine := errs.StandardCodes()
	if got, want := KindOfWith(f, mine), KindOf(f); got != want {
		t.Fatalf("an undeclared code resolved to %v through a caller's vocabulary and %v through the default", got, want)
	}

	if err := mine.Add("too_young", errs.KindForbidden, "too young"); err != nil {
		t.Fatalf("declaring a code on a caller's own vocabulary: %v", err)
	}
	if got := KindOfWith(f, mine); got != errs.KindForbidden {
		t.Fatalf("the caller's own declaration resolved to %v, want the kind it declared", got)
	}
	if got := KindOf(f); got != errs.KindValidation {
		t.Fatalf("a caller's declaration reached the default vocabulary: the same fault now resolves to %v", got)
	}

	if got, want := KindOfWith(crud.ErrNotFound, nil), KindOf(crud.ErrNotFound); got != want {
		t.Fatalf("a nil vocabulary resolved to %v, want the default's %v", got, want)
	}
}

func TestADefaultMessageComesFromTheDeclaredVocabulary(t *testing.T) {
	if m, ok := DefaultMessage(errs.CodeUnique); !ok || m == "" {
		t.Fatalf("a declared code answered %q, %v — the message ladder has nothing to fall back to", m, ok)
	}

	if m, ok := DefaultMessage("too_young"); ok {
		t.Fatalf("an undeclared code answered %q, want a decline", m)
	}
}

func TestEveryKindRendersACodeAndTheTableIsTotal(t *testing.T) {
	want := map[errs.Kind]errs.Code{
		errs.KindInternal:     errs.CodeInternal,
		errs.KindNotFound:     errs.CodeNotFound,
		errs.KindUnauthorized: errs.CodeUnauthenticated,
		errs.KindForbidden:    errs.CodeForbidden,
		errs.KindRetryable:    errs.CodeUnavailable,
		errs.KindConflict:     errs.CodeConflict,
		errs.KindValidation:   errs.CodeCheck,
		errs.KindBadRequest:   errs.CodeBadQuery,
		errs.KindTooLarge:     errs.CodeTooLarge,

		errs.KindMethodNotAllowed: errs.CodeMethodNotAllowed,
	}
	for k, code := range want {
		if got := CodeForKind(k); got != code {
			t.Fatalf("kind %s renders %q, want %q", k, got, code)
		}

		if k != errs.KindInternal {
			if got, ok := errs.StandardCodes().KindOf(code); !ok || got != k {
				t.Fatalf("code %q is declared with kind %s, but is what kind %s renders", code, got, k)
			}
		}
	}

	declared := 0
	for k := errs.Kind(0); k < errs.Kind(64); k++ {
		if k != errs.KindInternal && k.String() == "internal" {
			continue
		}
		declared++
		if _, listed := want[k]; !listed {
			t.Fatalf("errs declares the kind %s and this table has no row for it", k)
		}
	}
	if declared != len(want) {
		t.Fatalf("errs declares %d kinds and the table has %d rows", declared, len(want))
	}
}
