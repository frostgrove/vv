package crudhttp

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/shardit-io/go-rx-crud/crud"
	"github.com/shardit-io/go-rx-crud/query"
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

// ErrorBody is the JSON shape of a failed request.
type ErrorBody struct {
	Error   string `json:"error"`
	Path    string `json:"path,omitempty"`
	Message string `json:"message,omitempty"`
}

// Status maps a repository or query error to an HTTP status code. Everything it
// recognises is a client mistake or an access decision; anything else is 500.
//
// The order of the arms is load-bearing. An error that wraps two sentinels gets
// the first match, and 404 comes before 403 so a hidden row is never confirmed
// to exist by the status code.
func Status(err error) int {
	var qe *query.Error
	var uf *crud.UnknownFieldError
	var se *crud.SchemaError
	switch {
	case err == nil:
		return http.StatusOK
	case errors.Is(err, crud.ErrNotFound):
		return http.StatusNotFound
	case errors.Is(err, crud.ErrForbidden):
		return http.StatusForbidden
	case errors.Is(err, crud.ErrConflict):
		return http.StatusConflict
	case errors.Is(err, ErrBadRequest), errors.As(err, &qe), errors.As(err, &uf), errors.As(err, &se),
		errors.Is(err, crud.ErrMissingID):
		return http.StatusBadRequest
	default:
		return http.StatusInternalServerError
	}
}

// StatusText is the tag that goes in the "error" field of ErrorBody.
func StatusText(status int) string {
	switch status {
	case http.StatusNotFound:
		return "not_found"
	case http.StatusForbidden:
		return "forbidden"
	case http.StatusConflict:
		return "conflict"
	case http.StatusBadRequest:
		return "bad_request"
	default:
		return "internal_error"
	}
}

// Body builds the response body for a failed request. A 500 deliberately says
// nothing: the underlying message could be a SQL statement, a constraint name
// or a connection string. A *query.Error is safe by construction and its path
// and reason are handed back verbatim.
func Body(err error) (int, ErrorBody) {
	status := Status(err)
	body := ErrorBody{Error: StatusText(status)}

	var qe *query.Error
	switch {
	case errors.As(err, &qe):
		body.Path, body.Message = qe.Path, qe.Reason
	case status != http.StatusInternalServerError:
		body.Message = err.Error()
	}
	return status, body
}
