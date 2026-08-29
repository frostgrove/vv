package errs

import "strconv"

// A Code is what a client branches on. It is stable, machine-readable, and
// never derived from anything a driver said — a code built out of a CHECK
// expression's source text carries the column names with it ([[D-044]]).
//
// The constants below are the standard set. A consumer declares its own of the
// same type and wires them into a [Codes] value; nothing here is a closed set.
type Code string

const (
	// storage-shaped
	CodeUnique        Code = "unique"
	CodeNotUnique     Code = "not_unique" // a lookup that had to match one row matched several
	CodeForeignKey    Code = "foreign_key"
	CodeRestrict      Code = "restrict"
	CodeRequired      Code = "required"
	CodeCheck         Code = "check"
	CodeExclusion     Code = "exclusion"
	CodeTooLong       Code = "too_long"
	CodeOutOfRange    Code = "out_of_range"
	CodeInvalidFormat Code = "invalid_format"
	CodeInvalidEnum   Code = "invalid_enum"
	CodeStaleVersion  Code = "stale_version"

	// request-shaped
	CodeMalformedBody Code = "malformed_body"
	CodeInvalidID     Code = "invalid_id"
	CodeUnknownField  Code = "unknown_field"
	CodeBadQuery      Code = "bad_query"
	// CodeTooLarge is a request body past the cap the transport reads to. It is
	// request-shaped rather than validation-shaped on purpose: nothing about a
	// field was wrong, and nothing was parsed to find out.
	CodeTooLarge Code = "too_large"

	// decision-shaped
	// CodeConflict is a collision nothing finer was learned about: an engine
	// number no parser lists, or a source built without naming its engine. It
	// is KindConflict's twin of CodeInternal — without it an unclassified 409
	// has no code to render, and a client that branches on error_code would
	// read an empty one at the one status it is most likely to handle.
	CodeConflict        Code = "conflict"
	CodeNotFound        Code = "not_found"
	CodeForbidden       Code = "forbidden"
	CodeUnauthenticated Code = "unauthenticated"

	// infrastructure-shaped
	CodeDeadlock             Code = "deadlock"
	CodeSerializationFailure Code = "serialization_failure"
	CodeLockTimeout          Code = "lock_timeout"
	// CodeTransactionAborted is a transaction poisoned by an earlier failure:
	// PostgreSQL's 25P02, where every statement is refused until a rollback.
	// It reaches a caller after a truthful answer to the statement that
	// actually failed, so without a code of its own the second half of one
	// InTx is an opaque 500 the caller cannot tell from a bug.
	CodeTransactionAborted Code = "transaction_aborted"
	CodeUnavailable        Code = "unavailable"
	// CodeSchemaNotReady means the configured database is missing a table the
	// repository uses. It is an internal operational code: it lets a process
	// recognise an unapplied migration without disclosing its schema to clients.
	CodeSchemaNotReady Code = "schema_not_ready"
	CodeInternal       Code = "internal"
)

// A Kind is the transport class. Transports map the kind and never the code,
// which is what lets a service declare fifty codes of its own without touching
// a status table.
//
// The declaration order is the precedence order in ROADMAP-errors.md §2 —
// KindUnauthorized excepted, which that list omits and phase 4 owes a place
// for. The numeric values are not API: a kind is compared to a constant, never
// to a number, and nothing serialises one as an integer.
//
// KindInternal is zero on purpose. A kind that lost its meaning — a zero value
// somebody forgot to set, a number read out of a stale table — then says 500
// rather than claiming a 4xx it cannot support.
type Kind uint8

const (
	KindInternal Kind = iota
	KindNotFound
	KindUnauthorized
	KindForbidden
	KindRetryable
	KindConflict
	KindValidation
	KindBadRequest
	// KindTooLarge is its own kind rather than a KindBadRequest code, because
	// the status is the whole of what a client acts on here: 413 tells a caller
	// to send less, and 400 tells it to send something else. A code alone
	// cannot carry that — transports map the kind and never the code.
	KindTooLarge
)

// String is total: an unrecognised kind renders as internal, so a value that
// escaped its own table cannot be rendered as something a client may act on.
func (this Kind) String() string {
	switch this {
	case KindNotFound:
		return "not_found"
	case KindUnauthorized:
		return "unauthorized"
	case KindForbidden:
		return "forbidden"
	case KindRetryable:
		return "retryable"
	case KindConflict:
		return "conflict"
	case KindValidation:
		return "validation"
	case KindBadRequest:
		return "bad_request"
	case KindTooLarge:
		return "too_large"
	default:
		return "internal"
	}
}

// MarshalJSON is on the value receiver, like every other renderer here: a
// pointer receiver is bypassed when the value is marshalled as a field, a map
// entry or on its own. See [Violation.MarshalJSON].
func (this Kind) MarshalJSON() ([]byte, error) {
	return []byte(strconv.Quote(this.String())), nil
}
