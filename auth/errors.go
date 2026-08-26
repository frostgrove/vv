package auth

import (
	"errors"
	"fmt"

	"github.com/frostgrove/vv/errs"
)

// ErrUnauthenticated is the sentinel every authentication failure wraps, so a
// caller branches with errors.Is rather than on a type ([[D-015]]).
//
// It is here rather than beside crud.ErrForbidden because nothing in the
// repository layer raises it: authentication happens in a transport, one layer
// above anything crud knows about. Putting it in crud would also be a change to
// a package make check-tiers seals, for a sentinel that package never returns.
var ErrUnauthenticated = errors.New("auth: authentication is required")

// Unauthenticated builds the 401.
//
// The reason goes into the wrapped error and nowhere else. Fault.Message is not
// a private field: when a fault carries no violations, port.Violations
// synthesises one from the fault's code and copies Fault.Message into it, and
// that violation is rendered. So "signature does not verify" put there reaches
// the client and tells an attacker which half of the token to work on
// ([[D-044]], [[D-056]]).
//
// What the client gets is the code — errs.CodeUnauthenticated — and its
// declared message, "authentication is required". Every reason renders
// identically, which is the property that matters: a 401 that distinguishes
// "no such user" from "wrong password" is a user-enumeration oracle in the same
// way [[D-008]] describes for rows.
func Unauthenticated(reason string) error {
	return errs.Unauthorized().
		Code(errs.CodeUnauthenticated).
		Wrapping(fmt.Errorf("%w: %s", ErrUnauthenticated, reason)).
		Fault()
}

// Unauthenticatedf is [Unauthenticated] with a format string, for a reason that
// needs one. The formatted text is still never rendered.
func Unauthenticatedf(format string, args ...any) error {
	return Unauthenticated(fmt.Sprintf(format, args...))
}
