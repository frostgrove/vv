package storage

import (
	"context"
	"errors"
	"fmt"
)

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

func (this kindError) Error() string { return string(this.kind) }

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

type Error struct {
	Operation string
	Kind      Kind
	cause     error
}

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

func (this *Error) Error() string {
	if this == nil {
		return "storage: internal"
	}
	if this.Operation == "" {
		return "storage: " + string(this.Kind)
	}
	return fmt.Sprintf("storage %s: %s", this.Operation, this.Kind)
}

func (this *Error) Format(state fmt.State, _ rune) {
	_, _ = fmt.Fprint(state, this.Error())
}

func (this *Error) Is(target error) bool {
	if this == nil {
		return false
	}
	if k, ok := target.(kindError); ok {
		return this.Kind == k.kind
	}
	return errors.Is(this.cause, target)
}

func (this *Error) As(target any) bool {
	return this != nil && this.cause != nil && errors.As(this.cause, target)
}

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
