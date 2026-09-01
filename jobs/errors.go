package jobs

import (
	"errors"
	"fmt"
)

var (
	ErrInvalid        = errors.New("jobs: invalid value")
	ErrTooLarge       = errors.New("jobs: limit exceeded")
	ErrConflict       = errors.New("jobs: conflict")
	ErrSaturated      = errors.New("jobs: saturated")
	ErrUnsupported    = errors.New("jobs: unsupported")
	ErrNotActivated   = errors.New("jobs: not activated")
	ErrCorrupt        = errors.New("jobs: corrupt data")
	ErrCancelled      = errors.New("jobs: cancelled")
	ErrTerminated     = errors.New("jobs: terminated")
	ErrLeaseLost      = errors.New("jobs: lease lost")
	ErrAmbiguous      = errors.New("jobs: ambiguous outcome")
	ErrDriverContract = errors.New("jobs: driver contract violated")
)

func invalid(field string) error {
	return fmt.Errorf("%w: %s", ErrInvalid, field)
}

func tooLarge(field string) error {
	return fmt.Errorf("%w: %s", ErrTooLarge, field)
}
