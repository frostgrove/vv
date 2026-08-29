package port

import (
	"errors"
	"fmt"

	"github.com/frostgrove/vv/errs"
)

// ErrBadRequest marks a failure the binding itself produced — an id that does
// not parse, a body that does not decode, a bulk delete over the cap. It is one
// sentinel for every binding on purpose: KindOf matches it with errors.Is, and
// two private copies would each be invisible to the other's mapping.
var ErrBadRequest = errors.New("bad request")

// BadRequest wraps an error as a client mistake.
func BadRequest(err error) error { return fmt.Errorf("%w: %w", ErrBadRequest, err) }

// BadRequestf builds a client mistake from a message.
func BadRequestf(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrBadRequest, fmt.Sprintf(format, args...))
}

// BadRequestAs builds a client mistake that already knows its code and the part
// of the request it is about.
//
// The message is the one text safe by construction: the binding wrote it, out
// of the request's own words, and nothing a driver said passes through here.
// Before the envelope, that sentence reached a client as err.Error(); a fault's
// Error() is classification only ([[D-047]]), so a call site that wants its
// sentence rendered has to say so by naming a code.
func BadRequestAs(code errs.Code, path errs.Path, format string, args ...any) error {
	message := fmt.Sprintf(format, args...)
	return errs.BadRequest().Code(code).Message(message).
		At(path).Code(code).Message(message).
		Wrapping(ErrBadRequest).Fault()
}
