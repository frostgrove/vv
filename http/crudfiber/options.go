package crudfiber

import (
	"errors"
	"fmt"
	"unsafe"

	"github.com/gofiber/fiber/v3"

	"rx-crud/crud"
	"rx-crud/query"
)

type options[M any, ID comparable, U any] struct {
	query         *query.Config
	errorHandler  func(fiber.Ctx, error) error
	transform     func(fiber.Ctx, M) any
	scope         func(fiber.Ctx) ([]crud.Option, error)
	beforeSave    func(fiber.Ctx, *M) error
	beforeUpdate  func(fiber.Ctx, ID, *U) error
	readOnly      bool
	allowClientID bool
	maxBulk       int
}

// Option configures a handler. Type parameters are inferred from New's
// repository argument, so options never need explicit generics at the call site
// when written inline.
type Option[M any, ID comparable, U any] func(*options[M, ID, U])

// WithQuery bounds what clients may filter, sort, select and preload.
func WithQuery[M any, ID comparable, U any](cfg *query.Config) Option[M, ID, U] {
	return func(o *options[M, ID, U]) { o.query = cfg }
}

// WithErrorHandler replaces the error-to-response mapping.
func WithErrorHandler[M any, ID comparable, U any](fn func(fiber.Ctx, error) error) Option[M, ID, U] {
	return func(o *options[M, ID, U]) { o.errorHandler = fn }
}

// WithTransform renders each entity through a presenter — the place to hide
// columns the API should not expose.
func WithTransform[M any, ID comparable, U any](fn func(fiber.Ctx, M) any) Option[M, ID, U] {
	return func(o *options[M, ID, U]) { o.transform = fn }
}

// WithScope adds repository options to every read, derived from the request.
// The security decorator is the better place for row-level rules; this is for
// things that are genuinely transport-shaped, like an ?includeArchived flag.
func WithScope[M any, ID comparable, U any](fn func(fiber.Ctx) ([]crud.Option, error)) Option[M, ID, U] {
	return func(o *options[M, ID, U]) { o.scope = fn }
}

// BeforeSave runs on create and replace, after binding and sanitising.
func BeforeSave[M any, ID comparable, U any](fn func(fiber.Ctx, *M) error) Option[M, ID, U] {
	return func(o *options[M, ID, U]) { o.beforeSave = fn }
}

// BeforeUpdate runs on PATCH, after binding the DTO.
func BeforeUpdate[M any, ID comparable, U any](fn func(fiber.Ctx, ID, *U) error) Option[M, ID, U] {
	return func(o *options[M, ID, U]) { o.beforeUpdate = fn }
}

// ReadOnly mounts only the read routes.
func ReadOnly[M any, ID comparable, U any]() Option[M, ID, U] {
	return func(o *options[M, ID, U]) { o.readOnly = true }
}

// AllowClientID lets a create request carry its own primary key even when the
// database would generate one.
func AllowClientID[M any, ID comparable, U any]() Option[M, ID, U] {
	return func(o *options[M, ID, U]) { o.allowClientID = true }
}

// MaxBulk caps how many ids one bulk delete may carry.
func MaxBulk[M any, ID comparable, U any](n int) Option[M, ID, U] {
	return func(o *options[M, ID, U]) { o.maxBulk = n }
}

// ---------------------------------------------------------------------------
// errors

// ErrorBody is the JSON shape of a failed request.
type ErrorBody struct {
	Error   string `json:"error"`
	Path    string `json:"path,omitempty"`
	Message string `json:"message,omitempty"`
}

// Status maps a repository or query error to an HTTP status code. Everything it
// recognises is a client mistake or an access decision; anything else is 500.
func Status(err error) int {
	var qe *query.Error
	var uf *crud.UnknownFieldError
	var se *crud.SchemaError
	switch {
	case err == nil:
		return fiber.StatusOK
	case errors.Is(err, crud.ErrNotFound):
		return fiber.StatusNotFound
	case errors.Is(err, crud.ErrForbidden):
		return fiber.StatusForbidden
	case errors.Is(err, crud.ErrConflict):
		return fiber.StatusConflict
	case errors.Is(err, errBadRequest), errors.As(err, &qe), errors.As(err, &uf), errors.As(err, &se),
		errors.Is(err, crud.ErrMissingID):
		return fiber.StatusBadRequest
	default:
		return fiber.StatusInternalServerError
	}
}

// DefaultErrorHandler writes the mapped status and a small JSON body. A 500
// deliberately says nothing: the underlying message could be a SQL error.
func DefaultErrorHandler(c fiber.Ctx, err error) error {
	status := Status(err)
	body := ErrorBody{Error: statusText(status)}

	var qe *query.Error
	switch {
	case errors.As(err, &qe):
		body.Path, body.Message = qe.Path, qe.Reason
	case status != fiber.StatusInternalServerError:
		body.Message = err.Error()
	}
	return c.Status(status).JSON(body)
}

func statusText(status int) string {
	switch status {
	case fiber.StatusNotFound:
		return "not_found"
	case fiber.StatusForbidden:
		return "forbidden"
	case fiber.StatusConflict:
		return "conflict"
	case fiber.StatusBadRequest:
		return "bad_request"
	default:
		return "internal_error"
	}
}

func badRequest(err error) error { return fmt.Errorf("%w: %w", errBadRequest, err) }
func badRequestf(format string, args ...any) error {
	return fmt.Errorf("%w: %s", errBadRequest, fmt.Sprintf(format, args...))
}

func unsafeAdd(p unsafe.Pointer, off uintptr) unsafe.Pointer { return unsafe.Add(p, off) }
