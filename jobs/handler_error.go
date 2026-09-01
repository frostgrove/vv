package jobs

import (
	"errors"
	"time"
)

type permanentHandlerError struct{ cause error }

type handlerErrorIntent uint8

const (
	handlerIntentPermanent handlerErrorIntent = iota + 1
	handlerIntentDeferred
)

type handlerIntentMarker interface {
	handlerIntent() handlerErrorIntent
}

func (e permanentHandlerError) Error() string { return e.cause.Error() }
func (e permanentHandlerError) Unwrap() error { return e.cause }
func (permanentHandlerError) handlerIntent() handlerErrorIntent {
	return handlerIntentPermanent
}

func Permanent(err error) error {
	if err == nil {
		return nil
	}
	return permanentHandlerError{cause: err}
}

func IsPermanent(err error) bool {
	var target permanentHandlerError
	return errors.As(err, &target)
}

type deferredHandlerError struct {
	cause error
	after time.Duration
}

func (e deferredHandlerError) Error() string { return e.cause.Error() }
func (e deferredHandlerError) Unwrap() error { return e.cause }
func (deferredHandlerError) handlerIntent() handlerErrorIntent {
	return handlerIntentDeferred
}

func Deferred(err error) error {
	if err == nil {
		return nil
	}
	return deferredHandlerError{cause: err, after: DefaultRetryDelay}
}

func IsDeferred(err error) bool {
	var target deferredHandlerError
	return errors.As(err, &target)
}

func classifiedHandlerDisposition(failure HandlerFailure) (Disposition, bool) {
	var marker handlerIntentMarker
	if !errors.As(failure, &marker) {
		return Disposition{}, false
	}
	switch marker.handlerIntent() {
	case handlerIntentPermanent:
		disposition, _ := PermanentFailureDisposition(ReasonHandlerFailure, PublicFailure{})
		return disposition, true
	case handlerIntentDeferred:
		deferred := marker.(deferredHandlerError)
		disposition, _ := DeferredDisposition(PublicFailure{}, deferred.after)
		return disposition, true
	default:
		return Disposition{}, false
	}
}
