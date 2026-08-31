package errs

import "strconv"

type Code string

const (
	CodeUnique        Code = "unique"
	CodeNotUnique     Code = "not_unique"
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

	CodeMalformedBody Code = "malformed_body"
	CodeInvalidID     Code = "invalid_id"
	CodeUnknownField  Code = "unknown_field"
	CodeBadQuery      Code = "bad_query"

	CodeTooLarge Code = "too_large"

	CodeConflict  Code = "conflict"
	CodeNotFound  Code = "not_found"
	CodeForbidden Code = "forbidden"

	CodeMethodNotAllowed Code = "method_not_allowed"
	CodeUnauthenticated  Code = "unauthenticated"

	CodeDeadlock             Code = "deadlock"
	CodeSerializationFailure Code = "serialization_failure"
	CodeLockTimeout          Code = "lock_timeout"

	CodeTransactionAborted Code = "transaction_aborted"
	CodeUnavailable        Code = "unavailable"

	CodeSchemaNotReady Code = "schema_not_ready"
	CodeInternal       Code = "internal"
)

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

	KindTooLarge

	KindMethodNotAllowed
)

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
	case KindMethodNotAllowed:
		return "method_not_allowed"
	default:
		return "internal"
	}
}

func (this Kind) MarshalJSON() ([]byte, error) {
	return []byte(strconv.Quote(this.String())), nil
}
