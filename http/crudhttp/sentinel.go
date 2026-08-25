package crudhttp

import (
	"errors"
	"fmt"

	"github.com/shardit-io/vv/crud"
	"github.com/shardit-io/vv/errs"
	"github.com/shardit-io/vv/query"
)

// faultOf answers the fault a renderer works from, synthesising one for an
// error that carries none.
//
// This is where the disclosure closes. Nothing here reads err.Error(): a
// sentinel becomes a code, and the only sentences that survive are ones this
// library wrote out of the request's own words — a query document's Reason, a
// binding's own refusal. A driver's message has nowhere left to go
// ([[D-044]], [[UC-015]] guarantee 11).
//
// The arm order mirrors sentinelKind's, and for the same reason: an error that
// wraps two sentinels gets the first match, and 404 comes before 403 so a
// hidden row is never confirmed to exist ([[D-008]]).
func faultOf(err error) *errs.Fault {
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
	// 400 useful ([[UC-015]] guarantee 5).
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

// codeForKind is what a fault with no code of its own renders as. A body with
// an empty error_code is a body a client cannot branch on, which is the whole
// job of the field.
func codeForKind(k errs.Kind) errs.Code {
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
