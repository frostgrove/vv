package crudfiber

import (
	"github.com/gofiber/fiber/v3"

	"github.com/shardit-io/vv/crud"
	"github.com/shardit-io/vv/errs"
	"github.com/shardit-io/vv/http/crudhttp"
	"github.com/shardit-io/vv/port"
	"github.com/shardit-io/vv/query"
)

type options[M any, ID comparable, U any] struct {
	query         *query.Config
	renderer      crudhttp.Renderer
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
//
// Three parameters and not four. Nothing an option sets mentions the input
// type, so a handler with one of its own takes the same options as any other,
// and no existing call site has to be touched ([[D-045]]).
type Option[M any, ID comparable, U any] func(*options[M, ID, U])

// collect applies the options once, so the four constructors read the same
// configuration.
func collect[M any, ID comparable, U any](opts []Option[M, ID, U]) options[M, ID, U] {
	var o options[M, ID, U]
	for _, fn := range opts {
		fn(&o)
	}
	return o
}

// service translates the options that are about rules rather than about
// transport into the ones the default service takes.
func (o options[M, ID, U]) service() []port.ServiceOption {
	var out []port.ServiceOption
	if o.query != nil {
		out = append(out, port.WithQuery(o.query))
	}
	if o.allowClientID {
		out = append(out, port.AllowClientID())
	}
	return out
}

// refuseServiceOptions panics when a service-shaped option is handed to a
// constructor that was given a finished service.
//
// A panic and not a silent no-op, named after the option so the message is the
// fix. Serving means the rules are the service's; an ignored WithQuery would
// leave an API accepting everything while its author believed it was bounded,
// and that is exactly the failure [[D-021]] says must happen at start-up.
func (o options[M, ID, U]) refuseServiceOptions(who string) {
	switch {
	case o.query != nil:
		panic(who + ": WithQuery configures the service, which is already built — pass port.WithQuery to it instead")
	case o.allowClientID:
		panic(who + ": AllowClientID configures the service, which is already built — pass port.AllowClientID to it instead")
	}
}

// WithQuery bounds what clients may filter, sort, select and preload.
func WithQuery[M any, ID comparable, U any](cfg *query.Config) Option[M, ID, U] {
	return func(o *options[M, ID, U]) { o.query = cfg }
}

// WithErrorHandler replaces the error-to-response mapping.
func WithErrorHandler[M any, ID comparable, U any](fn func(fiber.Ctx, error) error) Option[M, ID, U] {
	return func(o *options[M, ID, U]) { o.errorHandler = fn }
}

// WithRenderer replaces the body every failed request answers with — the seam
// for RFC 9457, or for a shape a client already speaks. The status table stays
// shared either way ([[D-045]]).
func WithRenderer[M any, ID comparable, U any](r crudhttp.Renderer) Option[M, ID, U] {
	return func(o *options[M, ID, U]) { o.renderer = r }
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

// Envelope is the JSON shape of a failed request.
type Envelope = crudhttp.Envelope

// Renderer is the seam WithRenderer replaces.
type Renderer = crudhttp.Renderer

// defaultRenderer is what a handler and the middleware fall back to. One value,
// built once: a Renderer holds a vocabulary and a catalogue and nothing
// per-request, so sharing it is what makes the zero-config case free.
var defaultRenderer = crudhttp.NewRenderer()

// rendererFor answers the renderer a handler with these path hops needs. No
// hops is the ordinary case and keeps the shared value, so the zero-config case
// stays free; a service or a mapper that declares one gets a renderer of its
// own, wired ahead of the raw-body fallback ([[D-043]]).
func rendererFor(hops []errs.Resolver) crudhttp.Renderer {
	if len(hops) == 0 {
		return defaultRenderer
	}
	return crudhttp.NewRenderer(crudhttp.WithResolvers(hops...))
}

// Status maps a repository or query error to an HTTP status code. Everything it
// recognises is a client mistake or an access decision; anything else is 500.
//
// It is exported so an application writing its own error bodies gets the same
// statuses without reimplementing the table.
func Status(err error) int { return crudhttp.Status(err) }

// DefaultErrorHandler writes the mapped status and the error envelope. A 500
// deliberately says nothing: the underlying message could be a SQL error.
func DefaultErrorHandler(c fiber.Ctx, err error) error {
	return render(defaultRenderer, c, err)
}

// render is the one place a failure leaves this package, whichever renderer
// produced it.
//
// The retained body is read out of Locals rather than out of the repository's
// context, because that is where this binding put it: Fiber's binder owns the
// decode, so the copy is taken beside it rather than inside crudhttp.
func render(rd crudhttp.Renderer, c fiber.Ctx, err error) error {
	ctx := crudhttp.WithBody(c.Context(), fiber.Locals[[]byte](c, bodyKey))
	// The locale is a rendering parameter, read here rather than carried on the
	// fault: a fault crossing a queue must not carry the locale of the request
	// that made it. First tag only — q-values pick between translations we do
	// not have.
	ctx = crudhttp.WithLocale(ctx, crudhttp.AcceptLanguage(c.Get(fiber.HeaderAcceptLanguage)))
	status, header, body := rd.Render(ctx, err)
	for k, vs := range header {
		for _, v := range vs {
			c.Response().Header.Add(k, v)
		}
	}
	if body == nil {
		return c.SendStatus(status)
	}
	return c.Status(status).JSON(body)
}
