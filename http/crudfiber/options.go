package crudfiber

import (
	"github.com/gofiber/fiber/v3"

	"github.com/shardit-io/qq/crud"
	"github.com/shardit-io/qq/http/crudhttp"
	"github.com/shardit-io/qq/query"
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
//
// Reads only, and that is not a gap waiting to be filled: Save and Delete take
// no options, so there is nowhere for a predicate to go. The asymmetry is worth
// saying out loud, because it looks like protection and is not — with a scope
// of TenantID = 7, GET /:id on somebody else's row is 404 while DELETE /:id on
// the same row answers 200. Row-level rules on writes belong in security.Gate,
// whose scope really does reach the DELETE and the UPDATE.
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
type ErrorBody = crudhttp.ErrorBody

// Status maps a repository or query error to an HTTP status code. Everything it
// recognises is a client mistake or an access decision; anything else is 500.
//
// It is exported so an application writing its own error bodies gets the same
// statuses without reimplementing the table.
func Status(err error) int { return crudhttp.Status(err) }

// DefaultErrorHandler writes the mapped status and a small JSON body. A 500
// deliberately says nothing: the underlying message could be a SQL error.
func DefaultErrorHandler(c fiber.Ctx, err error) error {
	status, body := crudhttp.Body(err)
	return c.Status(status).JSON(body)
}
