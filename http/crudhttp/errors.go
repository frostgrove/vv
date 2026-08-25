package crudhttp

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/shardit-io/vv/crud"
	"github.com/shardit-io/vv/errs"
	"github.com/shardit-io/vv/query"
)

// ErrBadRequest marks a failure the binding itself produced — an id that does
// not parse, a body that does not decode, a bulk delete over the cap. It is one
// sentinel for every binding on purpose: Status matches it with errors.Is, and
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
	msg := fmt.Sprintf(format, args...)
	return errs.BadRequest().Code(code).Message(msg).
		At(path).Code(code).Message(msg).
		Wrapping(ErrBadRequest).Fault()
}

// Status maps a repository or query error to an HTTP status code. Everything it
// recognises is a client mistake or an access decision; anything else is 500.
//
// It is the same function every binding calls, and it takes no wiring: an
// application that renders its own bodies gets the statuses without building a
// Renderer ([[UC-015]] guarantee 8).
func Status(err error) int {
	if err == nil {
		return http.StatusOK
	}
	return StatusFor(KindOf(err))
}

// StatusFor is `ROADMAP-errors.md` §2's table, arm by arm.
//
// Written out rather than derived from the Kind's numeric value. errs.Kind
// documents those values as "not API" — they are a declaration order — and a
// table indexed by them would silently make that order part of the wire
// contract the first time somebody inserted a kind.
func StatusFor(k errs.Kind) int {
	switch k {
	case errs.KindNotFound:
		return http.StatusNotFound
	case errs.KindUnauthorized:
		return http.StatusUnauthorized
	case errs.KindForbidden:
		return http.StatusForbidden
	case errs.KindRetryable:
		return http.StatusServiceUnavailable
	case errs.KindConflict:
		return http.StatusConflict
	case errs.KindValidation:
		return http.StatusUnprocessableEntity
	case errs.KindBadRequest:
		return http.StatusBadRequest
	default:
		return http.StatusInternalServerError
	}
}

// KindOf resolves the one kind that decides the status, using the standard
// vocabulary. A wired renderer resolves through its own ([EnvelopeRenderer]).
func KindOf(err error) errs.Kind { return kindOf(err, standardCodes()) }

// kindOf answers the kind of a whole error: the fault's own, plus the kind of
// every violation's code, resolved by §2's precedence.
//
// A code the vocabulary does not declare contributes no kind. A service that
// declared "too_young" and forgot to wire it must not have its own 422 turned
// into a 500 by the omission — errs.KindInternal is the zero value, so reading
// an unknown code as a kind is exactly that mistake.
func kindOf(err error, codes *errs.Codes) errs.Kind {
	f, ok := errs.AsFault(err)
	if !ok {
		return sentinelKind(err)
	}
	worst := f.Kind
	for _, v := range f.Violations {
		if k, ok := codes.KindOf(v.Code); ok {
			worst = worse(worst, k)
		}
	}
	return worst
}

// worse answers whichever of two kinds decides the response.
func worse(a, b errs.Kind) errs.Kind {
	if rank(a) <= rank(b) {
		return a
	}
	return b
}

// rank is §2's precedence, highest first, written out for the same reason
// StatusFor is. Lower rank wins.
//
// The order is by how much the answer must conceal or defer. Internal first and
// narrowly: a classification that failed leaves a set that may be misleading, so
// 500 with nothing in it is the truthful answer. NotFound before Unauthorized
// and Forbidden is [[D-008]] verbatim — never confirm that a hidden row exists.
// Conflict before Validation because a collision is a fact about the world the
// client cannot fix by editing its own payload; the envelope still carries every
// violation, and only the coarse status is a single value.
func rank(k errs.Kind) int {
	switch k {
	case errs.KindInternal:
		return 0
	case errs.KindNotFound:
		return 1
	case errs.KindUnauthorized:
		return 2
	case errs.KindForbidden:
		return 3
	case errs.KindRetryable:
		return 4
	case errs.KindConflict:
		return 5
	case errs.KindValidation:
		return 6
	case errs.KindBadRequest:
		return 7
	default:
		// A kind from outside the table cannot be given a 4xx it may not
		// support, and errs.Kind.String makes the same choice.
		return 0
	}
}

// sentinelKind is the pre-fault mapping, kept whole: a service layer that wraps
// crud.ErrForbidden gets 403 with no registration step, and nothing has to
// produce a fault to be classified ([[D-015]]).
//
// The order is load-bearing. An error that wraps two sentinels gets the first
// match, and 404 comes before 403 so a hidden row is never confirmed to exist
// by the status code.
func sentinelKind(err error) errs.Kind {
	var qe *query.Error
	var uf *crud.UnknownFieldError
	var se *crud.SchemaError
	switch {
	case err == nil:
		return errs.KindInternal
	case errors.Is(err, crud.ErrNotFound):
		return errs.KindNotFound
	case errors.Is(err, crud.ErrForbidden):
		return errs.KindForbidden
	case errors.Is(err, crud.ErrConflict):
		return errs.KindConflict
	case errors.Is(err, ErrBadRequest), errors.As(err, &qe), errors.As(err, &uf), errors.As(err, &se),
		errors.Is(err, crud.ErrMissingID):
		return errs.KindBadRequest
	default:
		return errs.KindInternal
	}
}
