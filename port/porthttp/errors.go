package porthttp

import (
	"net/http"

	"github.com/frostgrove/vv/errs"
	"github.com/frostgrove/vv/port"
)

var ErrBadRequest = port.ErrBadRequest

func BadRequest(err error) error { return port.BadRequest(err) }

func BadRequestf(format string, args ...any) error { return port.BadRequestf(format, args...) }

func BadRequestAs(code errs.Code, path errs.Path, format string, args ...any) error {
	return port.BadRequestAs(code, path, format, args...)
}

func Status(err error) int {
	if err == nil {
		return http.StatusOK
	}
	return StatusFor(port.KindOf(err))
}

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
	case errs.KindTooLarge:
		return http.StatusRequestEntityTooLarge
	case errs.KindMethodNotAllowed:
		return http.StatusMethodNotAllowed
	default:
		return http.StatusInternalServerError
	}
}

func Routed(status int) error {
	switch status {
	case http.StatusNotFound:
		return errs.NotFound().Code(errs.CodeNotFound).Fault()
	case http.StatusMethodNotAllowed:
		return errs.MethodNotAllowed().Code(errs.CodeMethodNotAllowed).Fault()
	case http.StatusRequestEntityTooLarge:
		return errs.TooLarge().Code(errs.CodeTooLarge).Fault()
	case http.StatusUnauthorized:
		return errs.Unauthorized().Code(errs.CodeUnauthenticated).Fault()
	case http.StatusForbidden:
		return errs.Forbidden().Code(errs.CodeForbidden).Fault()
	default:
		if status >= 400 && status < 500 {
			return errs.BadRequest().Code(errs.CodeBadQuery).Fault()
		}
		return errs.Internal().Code(errs.CodeInternal).Fault()
	}
}

func KindOf(err error) errs.Kind { return port.KindOf(err) }

func AcceptLanguage(header string) string { return port.FirstLanguageTag(header) }
