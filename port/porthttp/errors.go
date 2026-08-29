package porthttp

import (
	"net/http"

	"github.com/frostgrove/vv/errs"
	"github.com/frostgrove/vv/port"
)

// ErrBadRequest marks a failure the binding itself produced — an id that does
// not parse, a body that does not decode, a bulk delete over the cap. It is the
// same variable as port.ErrBadRequest and not a copy of it: two sentinels would
// each be invisible to the other's mapping.
var ErrBadRequest = port.ErrBadRequest

// BadRequest wraps an error as a client mistake.
func BadRequest(err error) error { return port.BadRequest(err) }

// BadRequestf builds a client mistake from a message.
func BadRequestf(format string, args ...any) error { return port.BadRequestf(format, args...) }

// BadRequestAs builds a client mistake that already knows its code and the part
// of the request it is about.
func BadRequestAs(code errs.Code, path errs.Path, format string, args ...any) error {
	return port.BadRequestAs(code, path, format, args...)
}

// Status maps a repository or query error to an HTTP status code. Everything it
// recognises is a client mistake or an access decision; anything else is 500.
//
// It is the same function every binding calls, and it takes no wiring: an
// application that renders its own bodies gets the statuses without building a
// Renderer ([[UC-015]] guarantee 8).
//
// The kind is port's answer and the status is this package's table. That is the
// whole of [[D-045]]'s split in one line: classification is transport-neutral,
// and 404 is not.
func Status(err error) int {
	if err == nil {
		return http.StatusOK
	}
	return StatusFor(port.KindOf(err))
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
	case errs.KindTooLarge:
		return http.StatusRequestEntityTooLarge
	case errs.KindMethodNotAllowed:
		return http.StatusMethodNotAllowed
	default:
		return http.StatusInternalServerError
	}
}

// Routed is a router's own refusal, turned into a fault the renderer carries.
//
// A router answers before any handler runs, so its refusals never reach the
// error contract on their own: a path nothing claimed and a verb a route does
// not have arrive as whatever the framework's own error type is, and the
// sentinel table has no arm for either. Both then fall through to 500 — and a
// 404 rendered as a 500 reads as an outage. It is the difference between "you
// asked for something that is not there" and "this service is broken", and a
// client retries the second one.
//
// The router's own message is deliberately dropped. It is a sentence built out
// of the path the caller sent, and what a client acts on is the code and the
// status; the message it renders with is the vocabulary's, like every other
// failure's ([[D-044]]).
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
		// Every other 4xx is the caller's mistake with nothing finer known about
		// it; a 5xx the router raised is this service's, and says nothing.
		if status >= 400 && status < 500 {
			return errs.BadRequest().Code(errs.CodeBadQuery).Fault()
		}
		return errs.Internal().Code(errs.CodeInternal).Fault()
	}
}

// KindOf resolves the one kind that decides the status, using the standard
// vocabulary. A wired renderer resolves through its own ([EnvelopeRenderer]).
func KindOf(err error) errs.Kind { return port.KindOf(err) }

// AcceptLanguage reads the language a request asked for out of an
// Accept-Language header. The compatibility hop over port.FirstLanguageTag,
// under the name an HTTP binding looks for.
//
// The parser is port's because gRPC metadata carries the same syntax under
// grpc-accept-language, and a copy here would be a string split two transports
// could disagree about ([[D-045]]).
func AcceptLanguage(header string) string { return port.FirstLanguageTag(header) }
