package port

import (
	"errors"
	"fmt"
	"sync"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/query"
	"github.com/frostgrove/vv/errs"
)

var standardCodes = sync.OnceValue(errs.StandardCodes)

func KindOf(err error) errs.Kind { return KindOfWith(err, nil) }

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

func DefaultMessage(code errs.Code) (string, bool) { return standardCodes().MessageFor(code) }

func worse(a, b errs.Kind) errs.Kind {
	if rank(a) <= rank(b) {
		return a
	}
	return b
}

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
	case errs.KindMethodNotAllowed:
		return 4
	case errs.KindRetryable:
		return 5
	case errs.KindConflict:
		return 6
	case errs.KindValidation:
		return 7
	case errs.KindBadRequest:
		return 8
	case errs.KindTooLarge:
		return 9
	default:
		return 0
	}
}

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
	case errors.Is(err, ErrBadRequest), errors.Is(err, crud.ErrBadRequest), errors.As(err, &qe), errors.As(err, &uf), errors.As(err, &se),
		errors.Is(err, crud.ErrMissingID):
		return errs.KindBadRequest
	default:
		return errs.KindInternal
	}
}

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

	case errors.Is(err, crud.ErrStaleVersion):
		return synth(errs.Conflict(), errs.CodeStaleVersion, nil, "")

	case errors.Is(err, crud.ErrConflict):
		return synth(errs.Conflict(), errs.CodeConflict, nil, "")

	case errors.As(err, &qe):
		return synth(errs.BadRequest(), errs.CodeBadQuery, errs.ParsePath(qe.Path), qe.Reason)

	case errors.As(err, &uf):
		return synth(errs.BadRequest(), errs.CodeUnknownField, errs.ParsePath(uf.Field),
			fmt.Sprintf("unknown field %q on model %s", uf.Field, uf.Model))

	case errors.As(err, &se):
		return synth(errs.BadRequest(), errs.CodeBadQuery, errs.ParsePath(se.Field), se.Reason)

	case errors.Is(err, crud.ErrMissingID):
		return synth(errs.BadRequest(), errs.CodeInvalidID, nil, "")

	case errors.Is(err, ErrBadRequest), errors.Is(err, crud.ErrBadRequest):
		return synth(errs.BadRequest(), errs.CodeBadQuery, nil, "")

	default:
		return synth(errs.Internal(), errs.CodeInternal, nil, "")
	}
}

func synth(b *errs.Builder, code errs.Code, path errs.Path, message string) *errs.Fault {
	return b.Code(code).At(path).Code(code).Message(message).Fault()
}

func CodeForKind(k errs.Kind) errs.Code {
	switch k {
	case errs.KindNotFound:
		return errs.CodeNotFound
	case errs.KindUnauthorized:
		return errs.CodeUnauthenticated
	case errs.KindForbidden:
		return errs.CodeForbidden
	case errs.KindMethodNotAllowed:
		return errs.CodeMethodNotAllowed
	case errs.KindRetryable:
		return errs.CodeUnavailable
	case errs.KindConflict:
		return errs.CodeConflict
	case errs.KindValidation:
		return errs.CodeCheck
	case errs.KindBadRequest:
		return errs.CodeBadQuery
	case errs.KindTooLarge:
		return errs.CodeTooLarge
	default:
		return errs.CodeInternal
	}
}

func FaultFrom(kind errs.Kind, code errs.Code, vs []errs.Violation, partial bool) *errs.Fault {
	b := errs.New(kind)
	if s := sentinelFor(kind, code); s != nil {
		b = b.Wrapping(s)
	}
	if code != "" {
		b = b.Code(code)
	}
	b = b.Partial(partial)

	origin := errs.OriginInput
	if kind == errs.KindConflict {
		origin = errs.OriginState
	}
	for _, v := range vs {
		b = b.At(v.Path).Origin(origin)
		if v.Code != "" {
			b = b.Code(v.Code)
		}
		if v.Message != "" {
			b = b.Message(v.Message)
		}
		if v.Approximate {
			b = b.Approximate(true)
		}
	}
	return b.Fault()
}

func sentinelFor(kind errs.Kind, code errs.Code) error {
	switch kind {
	case errs.KindNotFound:
		return crud.ErrNotFound
	case errs.KindForbidden:
		return crud.ErrForbidden
	case errs.KindConflict:
		if code == errs.CodeStaleVersion {
			return crud.ErrStaleVersion
		}
		return crud.ErrConflict
	case errs.KindBadRequest:
		return ErrBadRequest
	default:
		return nil
	}
}
