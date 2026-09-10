package audit

import (
	"reflect"
	"unicode"
	"unicode/utf8"

	"github.com/frostgrove/vv/errs"
)

var (
	ErrDeclaration         error = newAuditSentinel("audit: declaration is invalid", errs.KindValidation, errs.CodeCheck)
	ErrInvalid             error = newAuditSentinel("audit: value is invalid", errs.KindValidation, errs.CodeCheck)
	ErrTooLarge            error = newAuditSentinel("audit: value exceeds a bound", errs.KindTooLarge, errs.CodeTooLarge)
	ErrDenied              error = newAuditSentinel("audit: access is denied", errs.KindForbidden, errs.CodeForbidden)
	ErrNotFound            error = newAuditSentinel("audit: evidence is not found", errs.KindNotFound, errs.CodeNotFound)
	ErrConflict            error = newAuditSentinel("audit: operation conflicts with current state", errs.KindConflict, errs.CodeConflict)
	ErrUnsupported         error = newAuditSentinel("audit: operation is unsupported", errs.KindMethodNotAllowed, errs.CodeMethodNotAllowed)
	ErrWrongCatalog        error = newAuditSentinel("audit: catalog does not match", errs.KindConflict, errs.CodeConflict)
	ErrWrongStore          error = newAuditSentinel("audit: store does not match", errs.KindConflict, errs.CodeConflict)
	ErrWrongAuthority      error = newAuditSentinel("audit: authority does not match", errs.KindConflict, errs.CodeConflict)
	ErrTransaction         error = newAuditSentinel("audit: transaction is invalid", errs.KindConflict, errs.CodeConflict)
	ErrAdmission           error = newAuditSentinel("audit: admission failed", errs.KindConflict, errs.CodeConflict)
	ErrGroupClosed         error = newAuditSentinel("audit: group is closed", errs.KindConflict, errs.CodeConflict)
	ErrGroupPoisoned       error = newAuditSentinel("audit: group is poisoned", errs.KindConflict, errs.CodeConflict)
	ErrNotWritten          error = newAuditSentinel("audit: evidence was not written", errs.KindRetryable, errs.CodeUnavailable)
	ErrUnconfirmed         error = newAuditSentinel("audit: write outcome is unconfirmed", errs.KindRetryable, errs.CodeUnavailable)
	ErrCommitUnconfirmed   error = newAuditSentinel("audit: commit outcome is unconfirmed", errs.KindRetryable, errs.CodeUnavailable)
	ErrRollbackUnconfirmed error = newAuditSentinel("audit: rollback outcome is unconfirmed", errs.KindRetryable, errs.CodeUnavailable)
	ErrBackend             error = newAuditSentinel("audit: backend failed", errs.KindRetryable, errs.CodeUnavailable)
	ErrClosed              error = newAuditSentinel("audit: store is closed", errs.KindRetryable, errs.CodeUnavailable)
	ErrRefused             error = newAuditSentinel("audit: operation was refused", errs.KindBadRequest, errs.CodeBadQuery)
	ErrBadPosition         error = newAuditSentinel("audit: store position is invalid", errs.KindBadRequest, errs.CodeBadQuery)
	ErrStaleCatalog        error = newAuditSentinel("audit: catalog is stale", errs.KindConflict, errs.CodeConflict)
	ErrCursor              error = newAuditSentinel("audit: cursor is invalid", errs.KindBadRequest, errs.CodeBadQuery)
	ErrFence               error = newAuditSentinel("audit: fence is invalid", errs.KindBadRequest, errs.CodeBadQuery)
	ErrExpired             error = newAuditSentinel("audit: value has expired", errs.KindBadRequest, errs.CodeBadQuery)
	ErrUnknownCatalog      error = newAuditSentinel("audit: catalog is unknown", errs.KindInternal, errs.CodeInternal)
	ErrIntegrity           error = newAuditSentinel("audit: evidence integrity failed", errs.KindInternal, errs.CodeInternal)
	ErrMissingKey          error = newAuditSentinel("audit: a required key is unavailable", errs.KindInternal, errs.CodeInternal)
	ErrMalformedEvidence   error = newAuditSentinel("audit: evidence is malformed", errs.KindInternal, errs.CodeInternal)
	ErrTemporalAmbiguity   error = newAuditSentinel("audit: evidence time is ambiguous", errs.KindInternal, errs.CodeInternal)
)

type auditSentinel struct {
	message string
	kind    errs.Kind
	code    errs.Code
}

func newAuditSentinel(message string, kind errs.Kind, code errs.Code) *auditSentinel {
	return &auditSentinel{message: message, kind: kind, code: code}
}

func (sentinel *auditSentinel) Error() string { return sentinel.message }

func (sentinel *auditSentinel) As(target any) bool {
	fault, ok := target.(**errs.Fault)
	if !ok || fault == nil {
		return false
	}
	*fault = sentinel.fault(nil)
	return true
}

func (sentinel *auditSentinel) fault(violations []errs.Violation) *errs.Fault {
	return &errs.Fault{
		Kind:       sentinel.kind,
		Code:       sentinel.code,
		Violations: cloneViolations(violations),
	}
}

type ownedError struct {
	sentinel *auditSentinel
	cause    error
	detail   []errs.Violation
	denial   DenialEvidenceState
}

func (owned *ownedError) Error() string { return owned.sentinel.message }

func (owned *ownedError) Is(target error) bool {
	return sameError(owned.sentinel, target)
}

func (owned *ownedError) As(target any) bool {
	fault, ok := target.(**errs.Fault)
	if !ok || fault == nil {
		return false
	}
	*fault = owned.sentinel.fault(owned.detail)
	return true
}

func auditError(sentinel, cause error) error {
	if knownSentinel(sentinel) == knownSentinel(ErrDenied) {
		return auditDenial(DenialEvidenceNotConfigured)
	}
	return &ownedError{sentinel: knownSentinel(sentinel), cause: nonNilError(cause)}
}

func auditErrorAt(sentinel error, path string) error {
	if knownSentinel(sentinel) == knownSentinel(ErrDenied) {
		return auditDenial(DenialEvidenceNotConfigured)
	}
	owned := &ownedError{sentinel: knownSentinel(sentinel)}
	if validErrorPath(path) {
		owned.detail = []errs.Violation{{Path: errs.ParsePath(path), Code: owned.sentinel.code}}
	}
	return owned
}

func auditTooLarge(path string, bound int) error {
	owned := &ownedError{sentinel: knownSentinel(ErrTooLarge)}
	if validErrorPath(path) && bound >= 0 {
		owned.detail = []errs.Violation{{
			Path:   errs.ParsePath(path),
			Code:   errs.CodeTooLarge,
			Params: errs.P{"max": bound},
		}}
	}
	return owned
}

func auditDenial(state DenialEvidenceState) error {
	if !state.Valid() {
		state = DenialEvidenceNotConfigured
	}
	return &ownedError{sentinel: knownSentinel(ErrDenied), denial: state}
}

func knownSentinel(sentinel error) *auditSentinel {
	if value, ok := sentinel.(*auditSentinel); ok && value != nil {
		return value
	}
	return ErrBackend.(*auditSentinel)
}

func cloneViolations(input []errs.Violation) []errs.Violation {
	if len(input) == 0 {
		return nil
	}
	output := make([]errs.Violation, len(input))
	for index, value := range input {
		value.Path = append(errs.Path(nil), value.Path...)
		if value.Params != nil {
			params := make(errs.P, len(value.Params))
			for name, parameter := range value.Params {
				params[name] = parameter
			}
			value.Params = params
		}
		output[index] = value
	}
	return output
}

func validErrorPath(path string) bool {
	if path == "" || len(path) > MaxNameBytes || !utf8.ValidString(path) {
		return false
	}
	for _, value := range path {
		if unicode.IsControl(value) {
			return false
		}
	}
	return true
}

type StoreOutcome uint8

const (
	Unclassified StoreOutcome = iota
	Conflict
	Missing
	Corrupt
	NotWritten
	Unconfirmed
	Closed
	BadPosition
	StaleCatalog
	Refused
)

func (outcome StoreOutcome) Valid() bool { return outcome <= Refused }

func (outcome StoreOutcome) String() string {
	switch outcome {
	case Conflict:
		return "conflict"
	case Missing:
		return "missing"
	case Corrupt:
		return "corrupt"
	case NotWritten:
		return "not_written"
	case Unconfirmed:
		return "unconfirmed"
	case Closed:
		return "closed"
	case BadPosition:
		return "bad_position"
	case StaleCatalog:
		return "stale_catalog"
	case Refused:
		return "refused"
	default:
		return "unclassified"
	}
}

type storeFailure struct {
	outcome StoreOutcome
	cause   error
}

func (failure *storeFailure) Error() string {
	return "audit: store reported " + failure.outcome.String()
}

func Failure(outcome StoreOutcome, cause error) error {
	if !outcome.Valid() {
		outcome = Unclassified
	}
	return &storeFailure{outcome: outcome, cause: nonNilError(cause)}
}

func storeOutcomeOf(err error) (StoreOutcome, bool) {
	failure, ok := findErrorAs[*storeFailure](err)
	if !ok {
		return Unclassified, false
	}
	if !failure.outcome.Valid() {
		return Unclassified, true
	}
	return failure.outcome, true
}

type CryptoOutcome uint8

const (
	CryptoUnclassified CryptoOutcome = iota
	CryptoInvalid
	CryptoMissingKey
	CryptoUnsupported
	CryptoMalformed
	CryptoBackend
	CryptoRefused
)

func (outcome CryptoOutcome) Valid() bool { return outcome <= CryptoRefused }

func (outcome CryptoOutcome) String() string {
	switch outcome {
	case CryptoInvalid:
		return "invalid"
	case CryptoMissingKey:
		return "missing_key"
	case CryptoUnsupported:
		return "unsupported"
	case CryptoMalformed:
		return "malformed"
	case CryptoBackend:
		return "backend"
	case CryptoRefused:
		return "refused"
	default:
		return "unclassified"
	}
}

type cryptoFailure struct {
	outcome CryptoOutcome
	cause   error
}

func (failure *cryptoFailure) Error() string {
	return "audit: cryptographic collaborator reported " + failure.outcome.String()
}

func CryptoFailure(outcome CryptoOutcome, cause error) error {
	if !outcome.Valid() {
		outcome = CryptoUnclassified
	}
	return &cryptoFailure{outcome: outcome, cause: nonNilError(cause)}
}

func CryptoOutcomeOf(err error) (CryptoOutcome, bool) {
	failure, ok := findErrorAs[*cryptoFailure](err)
	if !ok {
		return CryptoUnclassified, false
	}
	if !failure.outcome.Valid() {
		return CryptoUnclassified, true
	}
	return failure.outcome, true
}

type DenialEvidenceState uint8

const (
	DenialEvidenceNotConfigured DenialEvidenceState = iota + 1
	DenialEvidenceSuppressed
	DenialEvidenceLimiterFailed
	DenialEvidenceNotWritten
	DenialEvidenceUnconfirmed
	DenialEvidenceCommitted
)

func (state DenialEvidenceState) Valid() bool {
	return state >= DenialEvidenceNotConfigured && state <= DenialEvidenceCommitted
}

func (state DenialEvidenceState) String() string {
	switch state {
	case DenialEvidenceNotConfigured:
		return "not_configured"
	case DenialEvidenceSuppressed:
		return "suppressed"
	case DenialEvidenceLimiterFailed:
		return "limiter_failed"
	case DenialEvidenceNotWritten:
		return "not_written"
	case DenialEvidenceUnconfirmed:
		return "unconfirmed"
	case DenialEvidenceCommitted:
		return "committed"
	default:
		return "unknown"
	}
}

func DenialEvidenceStateOf(err error) (DenialEvidenceState, bool) {
	owned, ok := findErrorAs[*ownedError](err)
	if !ok || owned.sentinel != knownSentinel(ErrDenied) || !owned.denial.Valid() {
		return 0, false
	}
	return owned.denial, true
}

func CauseOf(err error) error {
	var cause error
	found, _ := walkErrors(err, func(node error) bool {
		if owned, ok := errorAsNode[*ownedError](node); ok {
			cause = nonNilError(owned.cause)
			return true
		}
		if failure, ok := errorAsNode[*storeFailure](node); ok {
			cause = nonNilError(failure.cause)
			return true
		}
		if failure, ok := errorAsNode[*cryptoFailure](node); ok {
			cause = nonNilError(failure.cause)
			return true
		}
		return false
	})
	if !found {
		return nil
	}
	return cause
}

const maxErrorNodes = 64

func walkErrors(err error, visit func(error) bool) (found, complete bool) {
	defer func() {
		if recover() != nil {
			found, complete = false, false
		}
	}()
	budget := maxErrorNodes
	found = walkErrorsWithin(err, visit, &budget)
	return found, budget > 0
}

func walkErrorsWithin(err error, visit func(error) bool, budget *int) bool {
	for *budget > 0 && !nilByReflection(err) {
		*budget--
		if visit(err) {
			return true
		}
		switch wrapped := err.(type) {
		case interface{ Unwrap() error }:
			err = wrapped.Unwrap()
		case interface{ Unwrap() []error }:
			for _, child := range wrapped.Unwrap() {
				if walkErrorsWithin(child, visit, budget) {
					return true
				}
			}
			return false
		default:
			return false
		}
	}
	return false
}

func findErrorAs[T error](err error) (T, bool) {
	var result T
	found, _ := walkErrors(err, func(node error) bool {
		candidate, ok := errorAsNode[T](node)
		if !ok {
			return false
		}
		result = candidate
		return true
	})
	return result, found
}

func errorAsNode[T error](node error) (T, bool) {
	var candidate T
	if typed, ok := node.(T); ok {
		candidate = typed
	} else {
		asking, ok := node.(interface{ As(any) bool })
		if !ok || !asking.As(&candidate) {
			return candidate, false
		}
	}
	if nilByReflection(candidate) {
		var zero T
		return zero, false
	}
	return candidate, true
}

func errorMatches(err, target error) bool {
	if nilByReflection(err) || nilByReflection(target) {
		return false
	}
	found, _ := walkErrors(err, func(node error) bool {
		if sameError(node, target) {
			return true
		}
		matcher, ok := node.(interface{ Is(error) bool })
		return ok && matcher.Is(target)
	})
	return found
}

func sameError(left, right error) (same bool) {
	defer func() { _ = recover() }()
	return left == right
}

func nonNilError(err error) error {
	if nilByReflection(err) {
		return nil
	}
	return err
}

func nilByReflection(value any) bool {
	if value == nil {
		return true
	}
	switch reflected := reflect.ValueOf(value); reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}

var (
	_ error = (*auditSentinel)(nil)
	_ error = (*ownedError)(nil)
	_ error = (*storeFailure)(nil)
	_ error = (*cryptoFailure)(nil)
)
