package crudhttp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/shardit-io/vv/crud"
	"github.com/shardit-io/vv/errs"
	"github.com/shardit-io/vv/query"
)

// `ROADMAP-errors.md` §2's status table, arm by arm and written out. Deriving
// the expectation from StatusFor would agree with it whatever it said.
func TestEveryKindGetsTheStatusTheTableGivesIt(t *testing.T) {
	want := []struct {
		kind   errs.Kind
		status int
	}{
		{errs.KindValidation, http.StatusUnprocessableEntity},
		{errs.KindConflict, http.StatusConflict},
		{errs.KindNotFound, http.StatusNotFound},
		{errs.KindForbidden, http.StatusForbidden},
		{errs.KindUnauthorized, http.StatusUnauthorized},
		{errs.KindBadRequest, http.StatusBadRequest},
		{errs.KindRetryable, http.StatusServiceUnavailable},
		{errs.KindInternal, http.StatusInternalServerError},
	}

	seen := map[int]errs.Kind{}
	for _, tc := range want {
		got := StatusFor(tc.kind)
		if got != tc.status {
			t.Fatalf("%v answered %d, want %d", tc.kind, got, tc.status)
		}
		// Every arm is a different status, so a table that answered one status
		// to everything fails here rather than passing eight rows.
		if other, dup := seen[got]; dup {
			t.Fatalf("%v and %v both answer %d; the table has collapsed", other, tc.kind, got)
		}
		seen[got] = tc.kind
	}

	// The control: a kind from outside the table cannot be handed a 4xx it
	// does not support. errs.Kind's numeric values are a declaration order and
	// not API, so one read out of a stale table has to land on 500.
	if got := StatusFor(errs.Kind(200)); got != http.StatusInternalServerError {
		t.Fatalf("a kind outside the table answered %d, want 500", got)
	}
}

// The precedence table, and the two rows `ROADMAP-errors.md` §15 names as the
// control: a fault mixing Forbidden and Conflict answers 403, and one mixing
// Internal with anything answers 500 with an empty body.
func TestThePrecedenceTableResolvesAMixedFault(t *testing.T) {
	t.Run("forbidden beats conflict", func(t *testing.T) {
		f := errs.Forbidden().Code(errs.CodeForbidden).
			Field("Email").Code(errs.CodeUnique).Fault()
		if got := Status(f); got != http.StatusForbidden {
			t.Fatalf("a fault mixing forbidden and conflict answered %d, want 403", got)
		}
	})

	t.Run("internal beats everything, and says nothing", func(t *testing.T) {
		for _, other := range []errs.Code{errs.CodeUnique, errs.CodeRequired, errs.CodeNotFound, errs.CodeDeadlock} {
			f := errs.Conflict().Code(other).
				Field("Email").Code(other).
				General().Code(errs.CodeInternal).
				Wrapping(crud.ErrConflict).Fault()

			status, _, body := NewRenderer().Render(context.Background(), f)
			if status != http.StatusInternalServerError {
				t.Fatalf("a fault mixing internal with %s answered %d, want 500", other, status)
			}
			raw, err := json.Marshal(body)
			if err != nil {
				t.Fatalf("marshalling the 500 body: %v", err)
			}
			if string(raw) != `{"type":"error","errors":{"general":[{"error_code":"internal"}]}}` {
				t.Fatalf("the 500 mixed with %s answered %s, want nothing but the status", other, raw)
			}
		}
	})

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
			// Each alone answers its own status, or "the higher one wins" is
			// being measured against a table that answers one thing always.
			for _, one := range []struct {
				kind errs.Kind
				code errs.Code
			}{high, low} {
				f := errs.New(one.kind).Code(one.code).General().Code(one.code).Fault()
				if got := Status(f); got != StatusFor(one.kind) {
					t.Fatalf("%v alone answered %d, want %d", one.kind, got, StatusFor(one.kind))
				}
			}
			first := errs.New(high.kind).Code(high.code).
				General().Code(high.code).General().Code(low.code).Fault()
			second := errs.New(low.kind).Code(low.code).
				General().Code(low.code).General().Code(high.code).Fault()
			for _, f := range []*errs.Fault{first, second} {
				if got := Status(f); got != StatusFor(high.kind) {
					t.Fatalf("%v mixed with %v answered %d, want %v's %d",
						high.kind, low.kind, got, high.kind, StatusFor(high.kind))
				}
			}
		})
	}

	// A code the vocabulary never heard of contributes no kind. A service that
	// declared "too_young" and forgot to wire it must not have its own 422
	// turned into a 500 by the omission — KindInternal is the zero value, so
	// reading an unknown code as a kind is exactly that mistake.
	novel := errs.Validation().Code("too_young").Field("Age").Code("too_young").Fault()
	if got := Status(novel); got != http.StatusUnprocessableEntity {
		t.Fatalf("a fault carrying an undeclared code answered %d, want its own kind's 422", got)
	}
}

// A fault is additive, and this is where that claim is made against a real crud
// sentinel: Status is the mapping every binding shares, and a classified
// duplicate key reaches 409 whether or not anything downstream knows what a
// fault is ([[D-038]]).
//
// The mechanism's own tests live in errs, against a sentinel declared there.
// This one has to be here — errs is a package of the root module until the
// first tag, and go mod tidy counts test imports, so a crud import inside its
// test package would become errs requiring the root that requires errs
// ([[D-036]]).
func TestAFaultKeepsItsSentinelReachableThroughStatus(t *testing.T) {
	f := errs.Conflict().Op("Save").Entity("User").Code(errs.CodeUnique).
		Field("email").Code(errs.CodeUnique).
		Wrapping(crud.ErrConflict).
		Fault()

	if got := Status(f); got != http.StatusConflict {
		t.Fatalf("a fault wrapping crud.ErrConflict answered %d, not 409 — every binding that had not learned about faults would answer 500 for a duplicate key", got)
	}
	if got := Status(fmt.Errorf("saving user: %w", f)); got != http.StatusConflict {
		t.Fatalf("the same fault, wrapped once more by a service layer, answered %d", got)
	}
}

// The phase-4 replacement for TestAFaultWrappingNoSentinelIsStillAnInternalError,
// whose claim stopped being true when the kind became the input to the table.
//
// It was the control that a fault must not become a status by being a fault.
// That is still the rule for an error with no classification: what changed is
// that a *classified* one now carries its own answer, which is the whole of
// [[D-049]]. The negative it protected moves onto the zero value — a fault
// whose kind was never set is KindInternal and still 500.
func TestAFaultsKindDecidesAndTheSentinelIsTheFallback(t *testing.T) {
	// A class-22 fault wraps no sentinel at all: the integrity gate refuses
	// class 22 ([[D-015]]) and the classifier still produces a code. Before
	// phase 4 this was an opaque 500.
	tooLong := errs.Validation().Code(errs.CodeTooLong).
		Field("Name").Code(errs.CodeTooLong).Fault()
	if got := Status(tooLong); got != http.StatusUnprocessableEntity {
		t.Fatalf("a classified value violation answered %d, want 422", got)
	}

	// A retryable one, the same: no sentinel, and 503 rather than 500.
	deadlock := errs.Retryable().Code(errs.CodeDeadlock).Fault()
	if got := Status(deadlock); got != http.StatusServiceUnavailable {
		t.Fatalf("a deadlock answered %d, want 503", got)
	}

	// The control the old test carried, kept: KindInternal is the zero value,
	// so a fault somebody forgot to give a kind says 500 rather than claiming a
	// 4xx it cannot support.
	unset := errs.New(errs.Kind(0)).Field("age").Code("too_young").Fault()
	if got := Status(unset); got != http.StatusInternalServerError {
		t.Fatalf("a fault with no kind set answered %d, want 500", got)
	}

	// And the other half: a plain wrapped sentinel, with no fault anywhere,
	// still maps. Nothing has to produce a fault to be classified.
	if got := Status(fmt.Errorf("loading: %w", crud.ErrNotFound)); got != http.StatusNotFound {
		t.Fatalf("a bare wrapped ErrNotFound answered %d, want 404", got)
	}
	missing := errs.NotFound().Code(errs.CodeNotFound).Wrapping(crud.ErrNotFound).Fault()
	if got := Status(missing); got != http.StatusNotFound {
		t.Fatalf("a fault wrapping crud.ErrNotFound answered %d, not 404", got)
	}
}

// Status is exported for handlers that render their own bodies, so the sentinel
// half has to hold on its own — including the branches no route reaches with a
// real repository behind it.
func TestStatusMapsEverySentinelWithoutAFault(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want int
	}{
		{"no error at all", nil, http.StatusOK},
		{"a missing row", crud.ErrNotFound, http.StatusNotFound},
		{"a denial", crud.ErrForbidden, http.StatusForbidden},
		{"a collision", crud.ErrConflict, http.StatusConflict},
		{"a stale write", crud.ErrStaleVersion, http.StatusConflict},
		{"a save with no key", crud.ErrMissingID, http.StatusBadRequest},
		{"a rejected query document", &query.Error{Path: "filter", Reason: "unknown field"}, http.StatusBadRequest},
		{"a field the model lacks", &crud.UnknownFieldError{Model: "Widget", Field: "nope"}, http.StatusBadRequest},
		{"a declaration that does not hold together", &crud.SchemaError{Model: "Widget", Reason: "no primary key"}, http.StatusBadRequest},
		{"a binding's own refusal", BadRequestf("nope"), http.StatusBadRequest},
		{"an error nobody recognises", fmt.Errorf("boom"), http.StatusInternalServerError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := Status(tc.err); got != tc.want {
				t.Fatalf("Status(%v) = %d, want %d", tc.err, got, tc.want)
			}
		})
	}
}
