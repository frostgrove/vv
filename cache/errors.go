package cache

import (
	"context"
	"errors"
	"fmt"
	"reflect"
)

var (
	ErrInvalid      = errors.New("invalid cache declaration or call")
	ErrTooLarge     = errors.New("cache value exceeds its declared limit")
	ErrCorrupt      = errors.New("cached value is corrupt or incompatible")
	ErrSaturated    = errors.New("cache loader capacity is occupied")
	ErrNotActivated = errors.New("automatic cache is not activated")
	ErrClosed       = errors.New("cache backend is closed")
	ErrBackend      = errors.New("cache backend operation failed")
	ErrLoader       = errors.New("cache loader failed")
)

type Error struct {
	Operation string
	Cause     error
}

type opaqueError struct {
	category error
	cause    error
}

func (this *opaqueError) Error() string { return this.category.Error() }

func (this *opaqueError) Is(target error) bool {
	return safeErrorIs(this.category, target) || safeErrorIs(this.cause, target)
}

func (this *opaqueError) opaqueErrors() (error, error) { return this.category, this.cause }

func (this *Error) Error() string {
	if this == nil {
		return "cache: unknown failure"
	}
	if this.Operation == "" {
		return fmt.Sprintf("cache: %v", this.Cause)
	}
	return fmt.Sprintf("cache: %s: %v", this.Operation, this.Cause)
}

func (this *Error) Unwrap() error {
	if this == nil {
		return nil
	}
	return this.Cause
}

func failure(operation string, cause error) error {
	if cause == nil {
		return nil
	}
	return &Error{Operation: operation, Cause: cause}
}

func sanitizedError(err, fallback error) error {
	if err == nil {
		return nil
	}
	for _, known := range []error{
		context.Canceled,
		context.DeadlineExceeded,
		ErrInvalid,
		ErrTooLarge,
		ErrCorrupt,
		ErrSaturated,
		ErrNotActivated,
		ErrClosed,
		ErrBackend,
		ErrLoader,
	} {
		if safeErrorIs(err, known) {
			return known
		}
	}
	return &opaqueError{category: fallback, cause: err}
}

func safeErrorIs(err, target error) (matched bool) {
	defer func() {
		if recover() != nil {
			matched = false
		}
	}()
	budget := 64
	return boundedErrorIs(err, target, &budget)
}

func boundedErrorIs(err, target error, budget *int) bool {
	if err == nil || target == nil || *budget <= 0 {
		return err == target
	}
	*budget--
	errType := reflect.TypeOf(err)
	targetType := reflect.TypeOf(target)
	if errType.Comparable() && targetType.Comparable() && err == target {
		return true
	}
	if opaque, ok := err.(interface{ opaqueErrors() (error, error) }); ok {
		category, cause := opaque.opaqueErrors()
		return boundedErrorIs(category, target, budget) || boundedErrorIs(cause, target, budget)
	}
	if matcher, ok := err.(interface{ Is(error) bool }); ok && invokeErrorMatcher(matcher, target) {
		return true
	}
	if multiple, ok := err.(interface{ Unwrap() []error }); ok {
		for _, child := range invokeMultiUnwrap(multiple) {
			if boundedErrorIs(child, target, budget) {
				return true
			}
		}
		return false
	}
	if single, ok := err.(interface{ Unwrap() error }); ok {
		return boundedErrorIs(invokeSingleUnwrap(single), target, budget)
	}
	return false
}

func invokeErrorMatcher(matcher interface{ Is(error) bool }, target error) (matched bool) {
	defer func() { _ = recover() }()
	return matcher.Is(target)
}

func invokeSingleUnwrap(wrapper interface{ Unwrap() error }) (result error) {
	defer func() {
		if recover() != nil {
			result = nil
		}
	}()
	return wrapper.Unwrap()
}

func invokeMultiUnwrap(wrapper interface{ Unwrap() []error }) (result []error) {
	defer func() {
		if recover() != nil {
			result = nil
		}
	}()
	return wrapper.Unwrap()
}
