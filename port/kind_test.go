package port

import (
	"errors"
	"fmt"
	"testing"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/query"
	"github.com/frostgrove/vv/errs"
)

// The precedence table, arm by arm. It moved here with the vocabulary it is
// part of: a status is HTTP's, but which of two kinds decides an answer is not.
func TestThePrecedenceTableResolvesAMixedFault(t *testing.T) {
	// Every adjacent pair of the order, in both build orders. An implementation
	// keyed on slice position rather than on precedence passes one and fails
	// the other, which is the whole point of building it twice.
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
			// Each alone answers its own kind, or "the higher one wins" is
			// being measured against a table that answers one thing always.
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

	// A code the vocabulary never heard of contributes no kind. A service that
	// declared "too_young" and forgot to wire it must not have its own
	// validation kind turned into an internal one by the omission —
	// KindInternal is the zero value, so reading an unknown code as a kind is
	// exactly that mistake.
	novel := errs.Validation().Code("too_young").Field("Age").Code("too_young").Fault()
	if got := KindOf(novel); got != errs.KindValidation {
		t.Fatalf("a fault carrying an undeclared code resolved to %v, want its own kind", got)
	}
}

// The sentinel half holds on its own: nothing has to produce a fault to be
// classified, which is what keeps a service layer that wraps crud.ErrForbidden
// working with no registration step ([[D-015]]).
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

	// A hidden row is never confirmed to exist by the classification: an error
	// wrapping both sentinels is NotFound and not Forbidden ([[D-008]]).
	both := fmt.Errorf("%w: %w", crud.ErrNotFound, crud.ErrForbidden)
	if got := KindOf(both); got != errs.KindNotFound {
		t.Fatalf("an error wrapping both sentinels resolved to %v, want NotFound", got)
	}
}

// The default vocabulary is behaviour and never a value. A caller that wants
// its own codes builds its own *errs.Codes, and nothing it does to that value
// can be seen from here — which is what stops a package-level registry two
// libraries would fight over.
func TestTheDefaultVocabularyIsNeverHandedOut(t *testing.T) {
	f := errs.Validation().Code("too_young").Field("Age").Code("too_young").Fault()

	// The control: before the caller declares anything, its own vocabulary
	// answers exactly what the default one does. Without this the assertion
	// below passes for a vocabulary that was simply broken.
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

	// And a nil vocabulary is the default one rather than an empty one, so a
	// renderer that was never given codes classifies like everything else.
	if got, want := KindOfWith(crud.ErrNotFound, nil), KindOf(crud.ErrNotFound); got != want {
		t.Fatalf("a nil vocabulary resolved to %v, want the default's %v", got, want)
	}
}

// A default message is the rung below a catalogue and above the code itself.
func TestADefaultMessageComesFromTheDeclaredVocabulary(t *testing.T) {
	if m, ok := DefaultMessage(errs.CodeUnique); !ok || m == "" {
		t.Fatalf("a declared code answered %q, %v — the message ladder has nothing to fall back to", m, ok)
	}
	// The control: an undeclared code declines rather than inventing a
	// sentence, which is what sends the ladder down to the code itself.
	if m, ok := DefaultMessage("too_young"); ok {
		t.Fatalf("an undeclared code answered %q, want a decline", m)
	}
}

// Every kind renders a code of its own, and the table is total.
//
// This is the third kind-keyed table in the library and the last one to get this
// control. The status table and the gRPC code table both had one, and both
// refused to compile when errs gained KindTooLarge; this one has a `default`
// arm, so it said `internal` instead — a 413 whose body told the client the
// server had broken. A fault with no code of its own is exactly the case
// CodeForKind exists for, and 413 is the kind most likely to arrive without one,
// because nothing about the body was parsed.
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
	}
	for k, code := range want {
		if got := CodeForKind(k); got != code {
			t.Fatalf("kind %s renders %q, want %q", k, got, code)
		}
		// And the code it renders has to be declared with that same kind, or a
		// client reading error_code and a client reading the status would
		// disagree about what happened.
		if k != errs.KindInternal {
			if got, ok := errs.StandardCodes().KindOf(code); !ok || got != k {
				t.Fatalf("code %q is declared with kind %s, but is what kind %s renders", code, got, k)
			}
		}
	}

	// The control on the table's size, the same one the other two carry.
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
