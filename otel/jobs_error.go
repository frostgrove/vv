package vvotel

import (
	"context"
	"errors"

	"github.com/frostgrove/vv/jobs"
)

func classifyJobsError(err error) (string, string) {
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, jobs.ErrCancelled):
		return OutcomeCanceled, ErrorTypeCanceled
	case errors.Is(err, context.DeadlineExceeded):
		return OutcomeTimeout, ErrorTypeTimeout
	case errors.Is(err, jobs.ErrConflict), errors.Is(err, jobs.ErrSaturated):
		return OutcomeError, ErrorTypeConflict
	case errors.Is(err, jobs.ErrNotActivated):
		return OutcomeError, ErrorTypeNotFound
	case errors.Is(err, jobs.ErrInvalid), errors.Is(err, jobs.ErrTooLarge), errors.Is(err, jobs.ErrUnsupported):
		return OutcomeError, ErrorTypeInvalid
	case errors.Is(err, jobs.ErrLeaseLost), errors.Is(err, jobs.ErrAdmissionStale):
		return OutcomeError, ErrorTypeStaleVersion
	default:
		return classifyCommandError(err)
	}
}
