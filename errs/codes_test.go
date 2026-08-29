package errs_test

import (
	"context"
	"errors"
	"testing"

	"github.com/frostgrove/vv/errs"
)

// everyStandardCode is the vocabulary this package declares. It is written out
// rather than derived, so a code added to the constant block and forgotten in
// StandardCodes fails here instead of answering "unknown" at run time.
var everyStandardCode = []errs.Code{
	errs.CodeUnique, errs.CodeNotUnique, errs.CodeForeignKey, errs.CodeRestrict,
	errs.CodeRequired, errs.CodeCheck, errs.CodeExclusion, errs.CodeTooLong,
	errs.CodeOutOfRange, errs.CodeInvalidFormat, errs.CodeInvalidEnum, errs.CodeStaleVersion,
	errs.CodeMalformedBody, errs.CodeInvalidID, errs.CodeUnknownField, errs.CodeBadQuery,
	errs.CodeNotFound, errs.CodeForbidden, errs.CodeUnauthenticated,
	errs.CodeDeadlock, errs.CodeSerializationFailure, errs.CodeLockTimeout,
	errs.CodeTransactionAborted, errs.CodeUnavailable, errs.CodeInternal,
}

func TestRedeclaringACodeWithADifferentKindIsRefused(t *testing.T) {
	c := errs.NewCodes()
	if err := c.Add("too_long", errs.KindValidation, "this value is too long"); err != nil {
		t.Fatalf("the first declaration was refused: %v", err)
	}
	if k, ok := c.KindOf("too_long"); !ok || k != errs.KindValidation {
		t.Fatalf("after declaring too_long as validation, KindOf said (%v, %v)", k, ok)
	}

	// The control: a second declaration that agrees is not an error. Refusing
	// every redeclaration is the process-wide panic this design rejected,
	// wearing a return value.
	if err := c.Add("too_long", errs.KindValidation, "a different sentence"); err != nil {
		t.Fatalf("a redeclaration with the same kind was refused: %v", err)
	}
	if message, _ := c.MessageFor("too_long"); message != "this value is too long" {
		t.Fatalf("the second declaration overwrote the message: %q", message)
	}

	err := c.Add("too_long", errs.KindConflict, "this value is already taken")
	if err == nil {
		t.Fatalf("declaring too_long a second time with a different kind was accepted — two libraries would then decide each other's status by link order")
	}
	if !errors.Is(err, errs.ErrCodeRedeclared) {
		t.Fatalf("the refusal is not reachable with errors.Is(ErrCodeRedeclared): %v", err)
	}

	// The control that matters most: a caller told its declaration was refused
	// must not then find the table changed anyway.
	if k, _ := c.KindOf("too_long"); k != errs.KindValidation {
		t.Fatalf("the refused declaration changed the kind to %v", k)
	}

	// And the whole point of a wired value rather than a global: two of them do
	// not see each other.
	other := errs.NewCodes()
	if err := other.Add("too_long", errs.KindConflict, "this value is already taken"); err != nil {
		t.Fatalf("a second, independent Codes could not declare too_long its own way: %v", err)
	}
	if k, _ := other.KindOf("too_long"); k != errs.KindConflict {
		t.Fatalf("the second Codes answered %v, so the two share state", k)
	}
	if k, _ := c.KindOf("too_long"); k != errs.KindValidation {
		t.Fatalf("declaring too_long elsewhere changed this table to %v", k)
	}
}

func TestTheRetryableCodesAreTheirOwnKind(t *testing.T) {
	c := errs.StandardCodes()
	for _, code := range []errs.Code{
		errs.CodeDeadlock, errs.CodeSerializationFailure, errs.CodeLockTimeout,
		errs.CodeTransactionAborted, errs.CodeUnavailable,
	} {
		k, ok := c.KindOf(code)
		if !ok {
			t.Fatalf("%s is not declared", code)
		}
		if k != errs.KindRetryable {
			t.Fatalf("%s is %v — a lock timeout answered as a client error tells the caller to change something that is not wrong", code, k)
		}
	}

	// The control: without these four the assertions above pass for a table
	// that answers retryable to everything, and both of D-040's forbids — never
	// a conflict, never a fall-through to 500 — would look proven.
	for _, tc := range []struct {
		code errs.Code
		want errs.Kind
	}{
		{errs.CodeUnique, errs.KindConflict},
		{errs.CodeInternal, errs.KindInternal},
		{errs.CodeRequired, errs.KindValidation},
		{errs.CodeCheck, errs.KindValidation},
	} {
		if k, _ := c.KindOf(tc.code); k != tc.want {
			t.Fatalf("%s is %v, want %v", tc.code, k, tc.want)
		}
	}
}

func TestTheInternalCodeHasNoDefaultMessage(t *testing.T) {
	c := errs.StandardCodes()
	if message, ok := c.MessageFor(errs.CodeInternal); ok || message != "" {
		t.Fatalf(`the internal code answered (%q, %v) — a 500 says nothing, and that has to hold by construction`, message, ok)
	}

	// The control: an empty Codes would pass the assertion above.
	for _, code := range everyStandardCode {
		if code == errs.CodeInternal {
			continue
		}
		if _, ok := c.KindOf(code); !ok {
			t.Fatalf("%s is not in the standard vocabulary", code)
		}
		if message, ok := c.MessageFor(code); !ok || message == "" {
			t.Fatalf("%s has no default message, so nothing distinguishes it from the internal code", code)
		}
	}
}

func TestTheZeroKindIsInternalAndSoIsAnUnknownOne(t *testing.T) {
	if errs.Kind(0) != errs.KindInternal {
		t.Fatalf("the zero kind is not internal, so a kind nobody set claims a status it cannot support")
	}
	if got := errs.Kind(0).String(); got != "internal" {
		t.Fatalf("the zero kind renders as %q", got)
	}
	if got := errs.Kind(200).String(); got != "internal" {
		t.Fatalf("an unrecognised kind renders as %q — a kind that lost its meaning must not read as a 4xx", got)
	}

	// The control: a String that returned "internal" for everything would pass
	// both assertions above and make the type useless.
	seen := map[string]errs.Kind{}
	for _, k := range []errs.Kind{
		errs.KindInternal, errs.KindNotFound, errs.KindUnauthorized, errs.KindForbidden,
		errs.KindRetryable, errs.KindConflict, errs.KindValidation, errs.KindBadRequest,
	} {
		name := k.String()
		if name == "" {
			t.Fatalf("kind %d has no name", k)
		}
		if other, ok := seen[name]; ok {
			t.Fatalf("kinds %d and %d both render as %q", other, k, name)
		}
		seen[name] = k
		if k != errs.KindInternal && k == 0 {
			t.Fatalf("%q is zero as well as internal", name)
		}
	}
}

// A nil *Codes is a wiring that exists: NewMessages documents it, and a
// component handed no vocabulary must read as "no code is known" rather than
// dereferencing at the first error — the moment least able to absorb a panic.
func TestANilCodesReadsAsEmptyInsteadOfPanicking(t *testing.T) {
	var none *errs.Codes
	if k, ok := none.KindOf(errs.CodeUnique); ok || k != errs.KindInternal {
		t.Fatalf("a nil Codes answered (%v, %v) for a code it cannot know", k, ok)
	}
	if message, ok := none.MessageFor(errs.CodeUnique); ok || message != "" {
		t.Fatalf("a nil Codes produced the message %q", message)
	}

	v := errs.Violation{Path: errs.Path{errs.Named("email")}, Code: errs.CodeUnique}

	// The control: with a vocabulary wired, an empty catalogue falls through to
	// MessageFor. Without this assertion the nil case below would hold just as
	// well for a Message that never consults the codes at all, and the nil
	// receiver would go unexercised.
	wired := errs.NewMessages(errs.StandardCodes())
	if got, ok := wired.Message(context.Background(), v, "en"); !ok || got != "this value is already taken" {
		t.Fatalf("an unresolved message did not reach the code's default: (%q, %v)", got, ok)
	}

	if got, ok := errs.NewMessages(nil).Message(context.Background(), v, "en"); ok || got != "" {
		t.Fatalf("a catalogue wired with no codes answered (%q, %v) instead of falling through", got, ok)
	}
}

// Only the kind reaches the status table — ROADMAP-errors.md §2 maps the kind
// and never the code — so a row declared with the wrong one is the wrong HTTP
// status for every consumer from phase 4 on. It is also a one-token edit
// nothing else in the tree notices: eight of these rows could be changed at
// once with the whole root module green.
func TestEveryStandardCodeHasTheKindTheStatusTableGivesIt(t *testing.T) {
	// Written out rather than read back from StandardCodes, for the same reason
	// everyStandardCode is: an expectation derived from the thing it checks
	// agrees with it whatever it says.
	want := []struct {
		code errs.Code
		kind errs.Kind
	}{
		{errs.CodeUnique, errs.KindConflict},
		{errs.CodeNotUnique, errs.KindConflict},
		{errs.CodeForeignKey, errs.KindConflict},
		{errs.CodeRestrict, errs.KindConflict},
		{errs.CodeStaleVersion, errs.KindConflict},
		{errs.CodeExclusion, errs.KindConflict},

		{errs.CodeRequired, errs.KindValidation},
		{errs.CodeCheck, errs.KindValidation},
		{errs.CodeTooLong, errs.KindValidation},
		{errs.CodeOutOfRange, errs.KindValidation},
		{errs.CodeInvalidFormat, errs.KindValidation},
		{errs.CodeInvalidEnum, errs.KindValidation},

		{errs.CodeMalformedBody, errs.KindBadRequest},
		{errs.CodeInvalidID, errs.KindBadRequest},
		{errs.CodeUnknownField, errs.KindBadRequest},
		{errs.CodeBadQuery, errs.KindBadRequest},

		{errs.CodeNotFound, errs.KindNotFound},
		{errs.CodeForbidden, errs.KindForbidden},
		{errs.CodeUnauthenticated, errs.KindUnauthorized},

		{errs.CodeDeadlock, errs.KindRetryable},
		{errs.CodeSerializationFailure, errs.KindRetryable},
		{errs.CodeLockTimeout, errs.KindRetryable},
		{errs.CodeTransactionAborted, errs.KindRetryable},
		{errs.CodeUnavailable, errs.KindRetryable},

		{errs.CodeInternal, errs.KindInternal},
	}

	c := errs.StandardCodes()
	pinned := map[errs.Code]bool{}
	for _, tc := range want {
		pinned[tc.code] = true
		k, ok := c.KindOf(tc.code)
		if !ok {
			t.Fatalf("%s is not in the standard vocabulary", tc.code)
		}
		if k != tc.kind {
			t.Fatalf("%s is %v, want %v — the kind is the whole input to the status table, so this row answers the wrong status everywhere", tc.code, k, tc.kind)
		}
	}

	// The control: a row missing from the expectation above is a row nothing
	// pins, and the loop passes without ever looking at it.
	for _, code := range everyStandardCode {
		if !pinned[code] {
			t.Fatalf("%s is declared but no row above says which kind it must have", code)
		}
	}
}
