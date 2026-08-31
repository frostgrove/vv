package auth

import (
	"errors"
	"fmt"

	"github.com/frostgrove/vv/errs"
)

var ErrUnauthenticated = errors.New("auth: authentication is required")

var ErrCredentialCardinality = errors.New("auth: credential source must contain at most one value")

var ErrGuardNotReady = errors.New("auth: guard is not ready")

var ErrAmbiguousGuardOrder = errors.New("auth: ambiguous guard order")

func Unauthenticated(reason string) error {
	return errs.Unauthorized().
		Code(errs.CodeUnauthenticated).
		Wrapping(fmt.Errorf("%w: %s", ErrUnauthenticated, reason)).
		Fault()
}

func Unauthenticatedf(format string, args ...any) error {
	return Unauthenticated(fmt.Sprintf(format, args...))
}

func invalidCredentialCardinality(count int) error {
	return errs.Unauthorized().
		Code(errs.CodeUnauthenticated).
		Wrapping(
			ErrUnauthenticated,
			fmt.Errorf("%w: credential source contained %d values", ErrCredentialCardinality, count),
		).
		Fault()
}

func internal(cause error) error {
	return errs.Internal().Wrapping(cause).Fault()
}
