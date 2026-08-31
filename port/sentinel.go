package port

import (
	"errors"
	"fmt"

	"github.com/frostgrove/vv/errs"
)

var ErrBadRequest = errors.New("bad request")

func BadRequest(err error) error { return fmt.Errorf("%w: %w", ErrBadRequest, err) }

func BadRequestf(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrBadRequest, fmt.Sprintf(format, args...))
}

func BadRequestAs(code errs.Code, path errs.Path, format string, args ...any) error {
	message := fmt.Sprintf(format, args...)
	return errs.BadRequest().Code(code).Message(message).
		At(path).Code(code).Message(message).
		Wrapping(ErrBadRequest).Fault()
}
