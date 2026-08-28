package storage

import (
	"context"
	"errors"
	"fmt"
)

// Kind is a backend-neutral storage failure class.
type Kind string

const (
	KindInvalid            Kind = "invalid"
	KindNotFound           Kind = "not_found"
	KindAlreadyExists      Kind = "already_exists"
	KindPreconditionFailed Kind = "precondition_failed"
	KindExpired            Kind = "expired"
	KindUnsupported        Kind = "unsupported"
	KindForbidden          Kind = "forbidden"
	KindConflict           Kind = "conflict"
	KindCancelled          Kind = "cancelled"
	KindSource             Kind = "source"
	KindTemporary          Kind = "temporary"
	KindUnavailable        Kind = "unavailable"
	KindInternal           Kind = "internal"
)

type kindError struct{ kind Kind }

func (e kindError) Error() string { return string(e.kind) }

var (
	ErrInvalid            = kindError{KindInvalid}
	ErrNotFound           = kindError{KindNotFound}
	ErrAlreadyExists      = kindError{KindAlreadyExists}
	ErrPreconditionFailed = kindError{KindPreconditionFailed}
	ErrExpired            = kindError{KindExpired}
	ErrUnsupported        = kindError{KindUnsupported}
	ErrForbidden          = kindError{KindForbidden}
	ErrConflict           = kindError{KindConflict}
	ErrCancelled          = kindError{KindCancelled}
	ErrSource             = kindError{KindSource}
	ErrTemporary          = kindError{KindTemporary}
	ErrUnavailable        = kindError{KindUnavailable}
	ErrInternal           = kindError{KindInternal}
)

// Error reports an operation and a bounded failure class. Its text deliberately
// omits object keys, backend locations and the wrapped provider error.
type Error struct {
	Operation string
	Kind      Kind
	cause     error
}

// NewError constructs a portable storage error while retaining cause for
// controlled errors.Is/errors.As diagnostics.
func NewError(operation string, kind Kind, cause error) error {
	if !validKind(kind) {
		kind = KindInternal
	}
	return &Error{Operation: operation, Kind: kind, cause: cause}
}

func validKind(kind Kind) bool {
	switch kind {
	case KindInvalid, KindNotFound, KindAlreadyExists, KindPreconditionFailed,
		KindExpired, KindUnsupported, KindForbidden, KindConflict, KindCancelled,
		KindSource, KindTemporary, KindUnavailable, KindInternal:
		return true
	default:
		return false
	}
}

func (e *Error) Error() string {
	if e == nil {
		return "storage: internal"
	}
	if e.Operation == "" {
		return "storage: " + string(e.Kind)
	}
	return fmt.Sprintf("storage %s: %s", e.Operation, e.Kind)
}

// Format keeps the wrapped backend diagnostics out of every ordinary fmt verb,
// including Go-syntax formatting with %#v.
func (e *Error) Format(state fmt.State, _ rune) {
	_, _ = fmt.Fprint(state, e.Error())
}

func (e *Error) Is(target error) bool {
	if e == nil {
		return false
	}
	if k, ok := target.(kindError); ok {
		return e.Kind == k.kind
	}
	return errors.Is(e.cause, target)
}

// As exposes a specifically requested diagnostic cause without making it an
// unconditional unwrap chain. This prevents a nested provider/source
// storage.Error from also changing the outer portable Kind.
func (e *Error) As(target any) bool {
	return e != nil && e.cause != nil && errors.As(e.cause, target)
}

// KindOf returns the portable kind carried by err, or the empty kind when err
// is not a storage error.
func KindOf(err error) Kind {
	var e *Error
	if errors.As(err, &e) {
		return e.Kind
	}
	var k kindError
	if errors.As(err, &k) {
		return k.kind
	}
	return ""
}

func projectError(operation string, err error) error {
	if err == nil {
		return nil
	}
	var portable *Error
	if errors.As(err, &portable) {
		// Backend implementations are an extension seam. Preserve their bounded
		// kind and diagnostic cause, but never trust their operation label: it
		// could otherwise smuggle a key, path or URL into public error text.
		return NewError(operation, portable.Kind, err)
	}
	if kind := KindOf(err); kind != "" {
		return NewError(operation, kind, err)
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return NewError(operation, KindCancelled, err)
	}
	return NewError(operation, KindInternal, err)
}
