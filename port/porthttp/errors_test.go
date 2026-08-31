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

		if other, dup := seen[got]; dup {
			t.Fatalf("%v and %v both answer %d; the table has collapsed", other, tc.kind, got)
		}
		seen[got] = tc.kind
	}

	if got := StatusFor(errs.Kind(200)); got != http.StatusInternalServerError {
		t.Fatalf("a kind outside the table answered %d, want 500", got)
	}
}

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

func TestAFaultsKindDecidesAndTheSentinelIsTheFallback(t *testing.T) {
	tooLong := errs.Validation().Code(errs.CodeTooLong).
		Field("Name").Code(errs.CodeTooLong).Fault()
	if got := Status(tooLong); got != http.StatusUnprocessableEntity {
		t.Fatalf("a classified value violation answered %d, want 422", got)
	}

	deadlock := errs.Retryable().Code(errs.CodeDeadlock).Fault()
	if got := Status(deadlock); got != http.StatusServiceUnavailable {
		t.Fatalf("a deadlock answered %d, want 503", got)
	}

	unset := errs.New(errs.Kind(0)).Field("age").Code("too_young").Fault()
	if got := Status(unset); got != http.StatusInternalServerError {
		t.Fatalf("a fault with no kind set answered %d, want 500", got)
	}

	if got := Status(fmt.Errorf("loading: %w", crud.ErrNotFound)); got != http.StatusNotFound {
		t.Fatalf("a bare wrapped ErrNotFound answered %d, want 404", got)
	}
	missing := errs.NotFound().Code(errs.CodeNotFound).Wrapping(crud.ErrNotFound).Fault()
	if got := Status(missing); got != http.StatusNotFound {
		t.Fatalf("a fault wrapping crud.ErrNotFound answered %d, not 404", got)
	}
}

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

		errs.KindMethodNotAllowed: http.StatusMethodNotAllowed,
	}
	for k, status := range want {
		if got := StatusFor(k); got != status {
			t.Fatalf("kind %s answered %d, want %d", k, got, status)
		}

		if k != errs.KindInternal {
			if got := KindForStatus(status); got != k {
				t.Fatalf("status %d read back as %s, want %s", status, got, k)
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

	if !strings.Contains(rendered, "price") {
		t.Fatalf("the refusal does not name the key the client got wrong:\n%s", rendered)
	}
	if !strings.Contains(rendered, "whole number") {
		t.Fatalf("the refusal does not say what belonged there:\n%s", rendered)
	}
}

func TestAnAuditedRefusalStillReachesTheClientWhole(t *testing.T) {
	var request query.Request
	err := DecodeJSON(strings.NewReader(`{"filtr":{"name":"x"}}`), &request)
	if err == nil {
		t.Fatal("an option the document does not define was accepted")
	}
	_, _, body := NewRenderer().Render(context.Background(), err)
	raw, _ := json.Marshal(body)
	if !strings.Contains(string(raw), "filtr") {
		t.Fatalf("the refusal no longer names the key the client sent:\n%s", raw)
	}
}

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
