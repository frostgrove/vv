package audit

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/frostgrove/vv/errs"
	"github.com/frostgrove/vv/port"
)

func TestPrimitivesMapEveryAuditSentinelToOneStableFaultClass(t *testing.T) {
	rows := []struct {
		sentinel error
		kind     errs.Kind
		code     errs.Code
	}{
		{ErrDeclaration, errs.KindValidation, errs.CodeCheck},
		{ErrInvalid, errs.KindValidation, errs.CodeCheck},
		{ErrTooLarge, errs.KindTooLarge, errs.CodeTooLarge},
		{ErrDenied, errs.KindForbidden, errs.CodeForbidden},
		{ErrNotFound, errs.KindNotFound, errs.CodeNotFound},
		{ErrConflict, errs.KindConflict, errs.CodeConflict},
		{ErrUnsupported, errs.KindMethodNotAllowed, errs.CodeMethodNotAllowed},
		{ErrWrongCatalog, errs.KindConflict, errs.CodeConflict},
		{ErrWrongStore, errs.KindConflict, errs.CodeConflict},
		{ErrWrongAuthority, errs.KindConflict, errs.CodeConflict},
		{ErrTransaction, errs.KindConflict, errs.CodeConflict},
		{ErrAdmission, errs.KindConflict, errs.CodeConflict},
		{ErrGroupClosed, errs.KindConflict, errs.CodeConflict},
		{ErrGroupPoisoned, errs.KindConflict, errs.CodeConflict},
		{ErrNotWritten, errs.KindRetryable, errs.CodeUnavailable},
		{ErrUnconfirmed, errs.KindRetryable, errs.CodeUnavailable},
		{ErrCommitUnconfirmed, errs.KindRetryable, errs.CodeUnavailable},
		{ErrRollbackUnconfirmed, errs.KindRetryable, errs.CodeUnavailable},
		{ErrBackend, errs.KindRetryable, errs.CodeUnavailable},
		{ErrClosed, errs.KindRetryable, errs.CodeUnavailable},
		{ErrRefused, errs.KindBadRequest, errs.CodeBadQuery},
		{ErrBadPosition, errs.KindBadRequest, errs.CodeBadQuery},
		{ErrStaleCatalog, errs.KindConflict, errs.CodeConflict},
		{ErrCursor, errs.KindBadRequest, errs.CodeBadQuery},
		{ErrFence, errs.KindBadRequest, errs.CodeBadQuery},
		{ErrExpired, errs.KindBadRequest, errs.CodeBadQuery},
		{ErrUnknownCatalog, errs.KindInternal, errs.CodeInternal},
		{ErrIntegrity, errs.KindInternal, errs.CodeInternal},
		{ErrMissingKey, errs.KindInternal, errs.CodeInternal},
		{ErrMalformedEvidence, errs.KindInternal, errs.CodeInternal},
		{ErrTemporalAmbiguity, errs.KindInternal, errs.CodeInternal},
	}
	seen := make(map[string]struct{}, len(rows))
	for rowIndex, row := range rows {
		if row.sentinel == nil {
			t.Fatalf("sentinel %d is nil", rowIndex)
		}
		if _, duplicate := seen[row.sentinel.Error()]; duplicate {
			t.Fatalf("sentinel %d shares the rendering %q", rowIndex, row.sentinel.Error())
		}
		seen[row.sentinel.Error()] = struct{}{}
		fault, ok := errs.AsFault(row.sentinel)
		if !ok || fault.Kind != row.kind || fault.Code != row.code {
			t.Fatalf("%v carries %#v, want kind %s and code %s", row.sentinel, fault, row.kind, row.code)
		}
		if got := port.KindOf(row.sentinel); got != row.kind {
			t.Fatalf("port projects %v as %s, want %s", row.sentinel, got, row.kind)
		}
		fault.Code = "mutated"
		fresh, _ := errs.AsFault(row.sentinel)
		if fresh.Code != row.code {
			t.Fatalf("a caller mutated shared fault state for %v", row.sentinel)
		}
		for otherIndex, other := range rows {
			if otherIndex != rowIndex && errors.Is(row.sentinel, other.sentinel) {
				t.Fatalf("%v crosses into sentinel %v", row.sentinel, other.sentinel)
			}
		}
	}
}

func TestPrimitivesOwnCausesWithoutExposingThemToStandardTraversal(t *testing.T) {
	cause := errors.New("postgres://root:secret@private/ledger row 42")
	err := auditError(ErrBackend, cause)
	if !errors.Is(err, ErrBackend) || errors.Is(err, ErrInvalid) {
		t.Fatalf("owned backend error has the wrong sentinel partition: %v", err)
	}
	if errors.Is(err, cause) {
		t.Fatal("standard traversal reached a protected cause")
	}
	if _, ok := err.(interface{ Unwrap() error }); ok {
		t.Fatal("an owned error publishes an unwrap door")
	}
	fault, ok := errs.AsFault(err)
	if !ok || fault.Kind != errs.KindRetryable || fault.Code != errs.CodeUnavailable {
		t.Fatalf("owned backend error carries %#v", fault)
	}
	if got := CauseOf(err); got != cause {
		t.Fatalf("CauseOf returned %v, want the original diagnostic cause", got)
	}
	if strings.Contains(err.Error(), "secret") || strings.Contains(fmt.Sprintf("%+v", err), "row 42") {
		t.Fatalf("owned error rendered protected cause as %q", err)
	}
}

func TestPrimitivesCopySafeViolationDetails(t *testing.T) {
	err := auditErrorAt(ErrInvalid, "catalog.owner")
	fault, ok := errs.AsFault(err)
	if !ok || len(fault.Violations) != 1 || fault.Violations[0].Path.String() != "catalog.owner" {
		t.Fatalf("field error carries %#v", fault)
	}
	fault.Violations[0].Path[0].Name = "changed"
	fresh, _ := errs.AsFault(err)
	if got := fresh.Violations[0].Path.String(); got != "catalog.owner" {
		t.Fatalf("fault state was shared with a caller: %q", got)
	}
	over := auditTooLarge("catalogs", MaxCatalogs)
	fault, _ = errs.AsFault(over)
	if len(fault.Violations) != 1 || fault.Violations[0].Params["max"] != MaxCatalogs {
		t.Fatalf("bound error carries %#v", fault)
	}
}

func TestPrimitivesStoreFailuresAreOpaqueAndNormalized(t *testing.T) {
	cause := errors.New("sqlstate 23505 secret table")
	for _, one := range []struct {
		input StoreOutcome
		want  StoreOutcome
	}{
		{Conflict, Conflict},
		{Refused, Refused},
		{StoreOutcome(255), Unclassified},
	} {
		err := Failure(one.input, cause)
		if got, ok := storeOutcomeOf(fmt.Errorf("store decorator: %w", err)); !ok || got != one.want {
			t.Fatalf("store outcome is %s/%v, want %s/true", got, ok, one.want)
		}
		if errors.Is(err, cause) {
			t.Fatal("store failure unwraps to its cause")
		}
		var fault *errs.Fault
		if errors.As(err, &fault) {
			t.Fatal("store failure advertises a transport fault before the kernel maps it")
		}
		if _, ok := err.(interface{ Unwrap() error }); ok {
			t.Fatal("store failure publishes Unwrap")
		}
		if strings.Contains(err.Error(), "23505") || CauseOf(err) != cause {
			t.Fatalf("store failure rendering/cause door disagrees: %q / %v", err, CauseOf(err))
		}
	}
}

func TestPrimitivesCryptoFailuresAreOpaqueAndNormalized(t *testing.T) {
	cause := errors.New("key customer-42 failed")
	for _, one := range []struct {
		input CryptoOutcome
		want  CryptoOutcome
	}{
		{CryptoMissingKey, CryptoMissingKey},
		{CryptoRefused, CryptoRefused},
		{CryptoOutcome(255), CryptoUnclassified},
	} {
		err := CryptoFailure(one.input, cause)
		wrapped := errors.Join(errors.New("another branch"), fmt.Errorf("crypto decorator: %w", err))
		if got, ok := CryptoOutcomeOf(wrapped); !ok || got != one.want {
			t.Fatalf("crypto outcome is %s/%v, want %s/true", got, ok, one.want)
		}
		if errors.Is(err, cause) || strings.Contains(err.Error(), "customer-42") {
			t.Fatal("crypto failure exposed its cause")
		}
		if CauseOf(err) != cause {
			t.Fatal("crypto failure lost its diagnostic cause")
		}
	}
	if _, ok := CryptoOutcomeOf(errors.New("foreign")); ok {
		t.Fatal("a foreign error was classified as a crypto failure")
	}
}

func TestPrimitivesAttachExactlyOneDenialEvidenceState(t *testing.T) {
	for state := DenialEvidenceNotConfigured; state <= DenialEvidenceCommitted; state++ {
		err := auditDenial(state)
		if !errors.Is(err, ErrDenied) {
			t.Fatalf("denial state %s lost ErrDenied", state)
		}
		if got, ok := DenialEvidenceStateOf(fmt.Errorf("transport: %w", err)); !ok || got != state {
			t.Fatalf("denial evidence state is %s/%v, want %s/true", got, ok, state)
		}
		if CauseOf(err) != nil {
			t.Fatal("denial evidence carries a diagnostic cause")
		}
	}
	if _, ok := DenialEvidenceStateOf(ErrDenied); ok {
		t.Fatal("a bare denial sentinel pretended to carry evidence state")
	}
	if _, ok := DenialEvidenceStateOf(auditError(ErrInvalid, nil)); ok {
		t.Fatal("a foreign error class pretended to carry denial evidence")
	}
}

func TestGenericErrorHelpersCannotCreateAnUnclassifiedDenial(t *testing.T) {
	secret := errors.New("secret denial cause")
	for _, err := range []error{
		auditError(ErrDenied, secret),
		auditErrorAt(ErrDenied, "subject"),
	} {
		state, ok := DenialEvidenceStateOf(err)
		if !ok || state != DenialEvidenceNotConfigured {
			t.Fatalf("denial state = (%v, %v)", state, ok)
		}
		if CauseOf(err) != nil || strings.Contains(err.Error(), secret.Error()) {
			t.Fatal("generic denial retained its cause")
		}
	}
}

type primitiveCycle struct{ next error }

func (*primitiveCycle) Error() string { return "cyclic foreign error" }

func (cycle *primitiveCycle) Unwrap() error { return cycle.next }

type primitiveLyingAs struct{ next error }

func (primitiveLyingAs) Error() string { return "lying foreign error" }

func (primitiveLyingAs) As(any) bool { return true }

func (value primitiveLyingAs) Unwrap() error { return value.next }

type primitivePanickingAs struct{ next error }

func (primitivePanickingAs) Error() string { return "panicking foreign error" }

func (primitivePanickingAs) As(any) bool { panic("foreign matcher panic") }

func (value primitivePanickingAs) Unwrap() error { return value.next }

type primitiveAsOnly struct{ failure *cryptoFailure }

func (primitiveAsOnly) Error() string { return "as-only foreign error" }

func (value primitiveAsOnly) As(target any) bool {
	match, ok := target.(**cryptoFailure)
	if !ok {
		return false
	}
	*match = value.failure
	return true
}

func TestPrimitivesBoundHostileForeignErrorGraphs(t *testing.T) {
	cycle := &primitiveCycle{}
	cycle.next = cycle
	done := make(chan struct{})
	go func() {
		defer close(done)
		CryptoOutcomeOf(cycle)
		DenialEvidenceStateOf(cycle)
		CauseOf(cycle)
		errorMatches(cycle, ErrBackend)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("a cyclic error graph exhausted the caller instead of its 64-node budget")
	}

	failure := CryptoFailure(CryptoBackend, errors.New("private")).(*cryptoFailure)
	if got, ok := CryptoOutcomeOf(primitiveLyingAs{next: failure}); !ok || got != CryptoBackend {
		t.Fatalf("a lying As stopped a real wrapped classification: %s/%v", got, ok)
	}
	if got, ok := CryptoOutcomeOf(primitiveAsOnly{failure: failure}); !ok || got != CryptoBackend {
		t.Fatalf("an As-only classification is unreachable: %s/%v", got, ok)
	}
	if got, ok := CryptoOutcomeOf(primitiveAsOnly{}); ok || got != CryptoUnclassified {
		t.Fatalf("a typed-nil As result escaped: %s/%v", got, ok)
	}
	if got, ok := CryptoOutcomeOf(primitivePanickingAs{next: failure}); ok || got != CryptoUnclassified {
		t.Fatalf("a panicking As escaped or guessed a classification: %s/%v", got, ok)
	}
}
