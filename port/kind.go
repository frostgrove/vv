package port

import (
	"errors"
	"fmt"
	"sync"

	"github.com/shardit-io/vv/crud"
	"github.com/shardit-io/vv/errs"
	"github.com/shardit-io/vv/query"
)

// standardCodes is the vocabulary an unwired caller resolves through. It is
// built once and never handed out: a *errs.Codes a caller could reach is a
// package-level registry two libraries would fight over, which is the fourth
// thing errs/doc.go refuses. That is why this package exports behaviour —
// KindOf, DefaultMessage — and no value.
var standardCodes = sync.OnceValue(errs.StandardCodes)

// KindOf resolves the one kind that decides the response, using the standard
// vocabulary.
func KindOf(err error) errs.Kind { return KindOfWith(err, nil) }

// KindOfWith answers the kind of a whole error through a caller's vocabulary:
// the fault's own kind, plus the kind of every violation's code, resolved by
// the precedence table. A nil vocabulary means the standard one.
//
// A code the vocabulary does not declare contributes no kind. A service that
// declared "too_young" and forgot to wire it must not have its own 422 turned
// into a 500 by the omission — errs.KindInternal is the zero value, so reading
// an unknown code as a kind is exactly that mistake.
func KindOfWith(err error, codes *errs.Codes) errs.Kind {
	if codes == nil {
		codes = standardCodes()
	}
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

// DefaultMessage answers what the standard vocabulary declares for a code, for
// the rung of the message ladder below a catalogue and above the code itself.
func DefaultMessage(code errs.Code) (string, bool) { return standardCodes().MessageFor(code) }

// worse answers whichever of two kinds decides the response.
func worse(a, b errs.Kind) errs.Kind {
	if rank(a) <= rank(b) {
		return a
	}
	return b
}

// rank is the precedence, highest first, written out rather than derived from
// the Kind's numeric value. errs.Kind documents those values as "not API" —
// they are a declaration order — and a table indexed by them would silently
// make that order part of the wire contract the first time somebody inserted a
// kind. Lower rank wins.
//
// The order is by how much the answer must conceal or defer. Internal first and
// narrowly: a classification that failed leaves a set that may be misleading, so
// the silent answer is the truthful one. NotFound before Unauthorized and
// Forbidden is [[D-008]] verbatim — never confirm that a hidden row exists.
// Conflict before Validation because a collision is a fact about the world the
// client cannot fix by editing its own payload; the envelope still carries every
// violation, and only the coarse answer is a single value.
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
// crud.ErrForbidden is classified with no registration step, and nothing has to
// produce a fault to be classified ([[D-015]]).
//
// The order is load-bearing. An error that wraps two sentinels gets the first
// match, and NotFound comes before Forbidden so a hidden row is never confirmed
// to exist.
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

// FaultOf answers the fault a renderer works from, synthesising one for an
// error that carries none.
//
// This is where the disclosure closes. Nothing here reads err.Error(): a
// sentinel becomes a code, and the only sentences that survive are ones this
// library wrote out of the request's own words — a query document's Reason, a
// binding's own refusal. A driver's message has nowhere left to go
// ([[D-044]], [[UC-015]] guarantee 11).
//
// The arm order mirrors sentinelKind's, and for the same reason: an error that
// wraps two sentinels gets the first match, and NotFound comes before Forbidden
// so a hidden row is never confirmed to exist ([[D-008]]).
func FaultOf(err error) *errs.Fault {
	if err == nil {
		return nil
	}
	if f, ok := errs.AsFault(err); ok {
		return f
	}

	var qe *query.Error
	var uf *crud.UnknownFieldError
	var se *crud.SchemaError
	switch {
	case errors.Is(err, crud.ErrNotFound):
		return synth(errs.NotFound(), errs.CodeNotFound, nil, "")

	case errors.Is(err, crud.ErrForbidden):
		return synth(errs.Forbidden(), errs.CodeForbidden, nil, "")

	// Before ErrConflict, which it wraps: a stale write is a conflict, and the
	// finer code is the one a caller can act on by re-reading the row.
	case errors.Is(err, crud.ErrStaleVersion):
		return synth(errs.Conflict(), errs.CodeStaleVersion, nil, "")

	case errors.Is(err, crud.ErrConflict):
		return synth(errs.Conflict(), errs.CodeConflict, nil, "")

	// The typed 400s come before the sentinel one: a *query.Error wrapped in
	// ErrBadRequest would otherwise lose the path and the reason that make a
	// refusal useful ([[UC-015]] guarantee 5).
	case errors.As(err, &qe):
		return synth(errs.BadRequest(), errs.CodeBadQuery, errs.ParsePath(qe.Path), qe.Reason)

	case errors.As(err, &uf):
		return synth(errs.BadRequest(), errs.CodeUnknownField, errs.ParsePath(uf.Field),
			fmt.Sprintf("unknown field %q on model %s", uf.Field, uf.Model))

	case errors.As(err, &se):
		return synth(errs.BadRequest(), errs.CodeBadQuery, errs.ParsePath(se.Field), se.Reason)

	case errors.Is(err, crud.ErrMissingID):
		return synth(errs.BadRequest(), errs.CodeInvalidID, nil, "")

	// A bare ErrBadRequest carries no code, so it renders as the coarse one and
	// its own text stays behind. A call site that wants its sentence rendered
	// says so with BadRequestAs.
	case errors.Is(err, ErrBadRequest):
		return synth(errs.BadRequest(), errs.CodeBadQuery, nil, "")

	default:
		return synth(errs.Internal(), errs.CodeInternal, nil, "")
	}
}

func synth(b *errs.Builder, code errs.Code, path errs.Path, message string) *errs.Fault {
	return b.Code(code).At(path).Code(code).Message(message).Fault()
}

// CodeForKind is what a fault with no code of its own renders as. A body with
// an empty error_code is a body a client cannot branch on, which is the whole
// job of the field.
func CodeForKind(k errs.Kind) errs.Code {
	switch k {
	case errs.KindNotFound:
		return errs.CodeNotFound
	case errs.KindUnauthorized:
		return errs.CodeUnauthenticated
	case errs.KindForbidden:
		return errs.CodeForbidden
	case errs.KindRetryable:
		return errs.CodeUnavailable
	case errs.KindConflict:
		return errs.CodeConflict
	case errs.KindValidation:
		return errs.CodeCheck
	case errs.KindBadRequest:
		return errs.CodeBadQuery
	default:
		return errs.CodeInternal
	}
}
