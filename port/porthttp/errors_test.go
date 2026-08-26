package porthttp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/query"
	"github.com/frostgrove/vv/errs"
	"github.com/frostgrove/vv/port"
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

// The seam, from the HTTP side. Status is this package's table over port's
// answer, and the two halves have to agree arm for arm: the classification
// moved to port with the vocabulary it belongs to, and a status did not.
//
// It asserts the concrete status as well as the composition. Comparing Status
// to StatusFor(port.KindOf(...)) alone would agree with itself whatever both
// said, and a table that answered 500 to everything would pass.
func TestStatusIsTheKindTableOverThePortsAnswer(t *testing.T) {
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
		{"a classified value violation", errs.Validation().Code(errs.CodeTooLong).Fault(), http.StatusUnprocessableEntity},
		{"a deadlock", errs.Retryable().Code(errs.CodeDeadlock).Fault(), http.StatusServiceUnavailable},
		{"an error nobody recognises", fmt.Errorf("boom"), http.StatusInternalServerError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := Status(tc.err); got != tc.want {
				t.Fatalf("Status(%v) = %d, want %d", tc.err, got, tc.want)
			}
			if tc.err == nil {
				return
			}
			if got, want := Status(tc.err), StatusFor(port.KindOf(tc.err)); got != want {
				t.Fatalf("Status answered %d and the kind table over port's answer said %d — the seam has drifted", got, want)
			}
		})
	}
}

// The composition where it matters most: port decides that internal beats every
// other kind, and this package renders that decision as a body with nothing in
// it. A mixed fault is the case that would leak — the violations are there, and
// the 500 must still say nothing ([[D-015]]).
func TestAnInternalKindRendersNothingWhateverElseTheFaultCarries(t *testing.T) {
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

	// The control: the same fault without the internal violation renders the
	// violations it carries, so the silence above is the internal kind's doing
	// and not this renderer's habit.
	f := errs.Conflict().Code(errs.CodeUnique).Field("Email").Code(errs.CodeUnique).Fault()
	status, _, body := NewRenderer().Render(context.Background(), f)
	if status != http.StatusConflict {
		t.Fatalf("a conflict answered %d, want 409", status)
	}
	raw, _ := json.Marshal(body)
	if !strings.Contains(string(raw), "unique") {
		t.Fatalf("a 409 answered %s, want the violation it carries", raw)
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

// Every kind answers the status it promises to, and the table is total.
//
// The gRPC binding has carried this control since it was written and this side
// did not, which is how errs could gain a kind and reach a client as 500 over
// HTTP while gRPC refused to build. The two tables are the same obligation
// ([[FL-013]]); they now fail the same way.
func TestEveryKindHasAStatusAndTheTableIsTotal(t *testing.T) {
	want := map[errs.Kind]int{
		errs.KindInternal:     http.StatusInternalServerError,
		errs.KindNotFound:     http.StatusNotFound,
		errs.KindUnauthorized: http.StatusUnauthorized,
		errs.KindForbidden:    http.StatusForbidden,
		errs.KindRetryable:    http.StatusServiceUnavailable,
		errs.KindConflict:     http.StatusConflict,
		errs.KindValidation:   http.StatusUnprocessableEntity,
		errs.KindBadRequest:   http.StatusBadRequest,
		errs.KindTooLarge:     http.StatusRequestEntityTooLarge,
	}
	for k, status := range want {
		if got := StatusFor(k); got != status {
			t.Fatalf("kind %s answered %d, want %d", k, got, status)
		}
		// KindForStatus is the same table read backwards, and the two have to
		// agree. Internal is the one row that is not injective: every status
		// outside the table means internal, so the round trip is only asked of
		// the rows that name a status of their own.
		if k != errs.KindInternal {
			if got := KindForStatus(status); got != k {
				t.Fatalf("status %d read back as %s, want %s", status, got, k)
			}
		}
	}

	// The control on the table's size. Kind.String is total — a kind outside
	// its own table renders as "internal" — so a kind nobody listed here is
	// spotted by being the one that is not KindInternal and still says so.
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

// A body that will not decode is refused in the client's own vocabulary, never
// in Go's.
//
// encoding/json's UnmarshalTypeError reads "cannot unmarshal string into Go
// struct field WidgetUpdate.price of type crud.Opt[int64]" — the Go type of the
// consumer's DTO and the Go type of the field, package path included. It was
// passed straight through as the rendered message, on every transport, because
// BadRequestAs renders what it is given. [[D-044]] forbids exactly that, and the
// corpus render test could not see it: it renders faults the *database* produced,
// and this one is produced by the decoder.
func TestAMalformedBodyIsRefusedWithoutNamingGoTypes(t *testing.T) {
	type inner struct {
		Price int64 `json:"price"`
	}
	var v inner
	err := DecodeJSON(strings.NewReader(`{"price":"free"}`), &v)
	if err == nil {
		t.Fatal("a string in an int64 field decoded cleanly")
	}

	_, _, body := NewRenderer().Render(context.Background(), err)
	raw, merr := json.Marshal(body)
	if merr != nil {
		t.Fatal(merr)
	}
	rendered := string(raw)

	for _, leak := range []string{"inner", "int64", "Go struct field", "unmarshal", "porthttp"} {
		if strings.Contains(rendered, leak) {
			t.Fatalf("the rendered refusal names %q, which is this process's internals:\n%s", leak, rendered)
		}
	}

	// The control, and it is the point: the refusal must still be useful. The
	// client is told which key of its own document was wrong and what belonged
	// there — both facts about what it sent.
	if !strings.Contains(rendered, "price") {
		t.Fatalf("the refusal does not name the key the client got wrong:\n%s", rendered)
	}
	if !strings.Contains(rendered, "whole number") {
		t.Fatalf("the refusal does not say what belonged there:\n%s", rendered)
	}
}

// The control on the rule's narrowness: an error that is *not* encoding/json's
// structural one still reaches the client whole.
//
// The first version of the sanitising branch swallowed everything it did not
// recognise, which turned query.Request's "no such option: filtr" — a refusal
// [[D-013]] requires to name the key — into "the request body could not be
// read". One disclosure bug traded for a refusal that says nothing.
func TestAnAuditedRefusalStillReachesTheClientWhole(t *testing.T) {
	var req query.Request
	err := DecodeJSON(strings.NewReader(`{"filtr":{"name":"x"}}`), &req)
	if err == nil {
		t.Fatal("an option the document does not define was accepted")
	}
	_, _, body := NewRenderer().Render(context.Background(), err)
	raw, _ := json.Marshal(body)
	if !strings.Contains(string(raw), "filtr") {
		t.Fatalf("the refusal no longer names the key the client sent:\n%s", raw)
	}
}

// A syntactically broken body keeps the one fact that is the client's own: where
// in its bytes the parser stopped.
func TestABodyThatIsNotJSONSaysWhereItStopped(t *testing.T) {
	var v struct {
		Price int64 `json:"price"`
	}
	err := DecodeJSON(strings.NewReader(`{"price":`), &v)
	if err == nil {
		t.Fatal("a truncated document decoded cleanly")
	}
	_, _, body := NewRenderer().Render(context.Background(), err)
	raw, _ := json.Marshal(body)
	if !strings.Contains(string(raw), "valid JSON") {
		t.Fatalf("a truncated body was not reported as invalid JSON:\n%s", raw)
	}
}
