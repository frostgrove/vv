package crudfiber

import (
	"encoding/json"
	"github.com/gofiber/fiber/v3"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/http/crudhttp"
	"github.com/frostgrove/vv/crud/query"
	"github.com/frostgrove/vv/errs"
	"github.com/frostgrove/vv/port"
)

type options[M any, ID comparable, U any] struct {
	crudhttp.Rules
	renderer     crudhttp.Renderer
	errorHandler func(fiber.Ctx, error) error
	transform    func(fiber.Ctx, M) any
	scope        func(fiber.Ctx) ([]crud.Option, error)
	beforeSave   func(fiber.Ctx, *M) error
	beforeUpdate func(fiber.Ctx, ID, *U) error
}

// Option configures a handler.
//
// New infers all three type parameters from its repository argument, so the
// constructor is written without them. An option is not: Go infers a function's
// type arguments from its own arguments, and nothing in `WithQuery(cfg)`
// mentions M, ID or U. Every option spells all three.
//
//	crudfiber.New(articles,
//	    crudfiber.WithQuery[Article, int64, ArticleUpdate](cfg),
//	    crudfiber.MaxBulk[Article, int64, ArticleUpdate](100),
//	)
//
// One local helper per resource is what makes that bearable:
//
//	type articleOpt = crudfiber.Option[Article, int64, ArticleUpdate]
//
//	func articleQuery(cfg *query.Config) articleOpt {
//	    return crudfiber.WithQuery[Article, int64, ArticleUpdate](cfg)
//	}
//
// The alias alone does not help — it names the result type, which is not where
// the inference is stuck — so the helper is a function, not a name.
//
// Three parameters and not four. Nothing an option sets mentions the input
// type, so a handler with one of its own takes the same options as any other,
// and no existing call site has to be touched ([[D-045]]).
type Option[M any, ID comparable, U any] func(*options[M, ID, U])

// collect applies the options once, so the four constructors read the same
// configuration.
func collect[M any, ID comparable, U any](optionList []Option[M, ID, U]) options[M, ID, U] {
	var o options[M, ID, U]
	for _, fn := range optionList {
		fn(&o)
	}
	return o
}

// WithQuery bounds what clients may filter, sort, select and preload.
func WithQuery[M any, ID comparable, U any](config *query.Config) Option[M, ID, U] {
	return func(o *options[M, ID, U]) {
		o.Query, o.QueryVariants, o.QuerySelector = config, nil, nil
	}
}

// WithQueryFor selects one declared query vocabulary per request. The selector
// normally reads a principal from context; an undeclared non-empty name is a
// query refusal rather than a permissive fallback.
func WithQueryFor[M any, ID comparable, U any](defaultConfig *query.Config, variants map[string]*query.Config, selectConfig port.QuerySelector) Option[M, ID, U] {
	return func(o *options[M, ID, U]) {
		o.Query, o.QueryVariants, o.QuerySelector = defaultConfig, variants, selectConfig
	}
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
	return func(o *options[M, ID, U]) { o.ReadOnly = true }
}

// AllowClientID lets a create request carry its own primary key even when the
// database would generate one.
func AllowClientID[M any, ID comparable, U any]() Option[M, ID, U] {
	return func(o *options[M, ID, U]) { o.AllowClientID = true }
}

// MaxBody caps how many bytes of request body one route reads before answering
// 413. It defaults to [crudhttp.MaxBody]; zero or less means the default, and
// there is no way to say "unbounded".
func MaxBody[M any, ID comparable, U any](n int) Option[M, ID, U] {
	return func(o *options[M, ID, U]) { o.MaxBody = n }
}

// MaxBulk caps how many ids one bulk delete may carry.
func MaxBulk[M any, ID comparable, U any](n int) Option[M, ID, U] {
	return func(o *options[M, ID, U]) { o.MaxBulk = n }
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

// writeJSON is the one place a successful response leaves this package.
//
// It marshals before touching the status for the reason crudnet's twin gives: a
// value that cannot be encoded — a presenter returning a channel, a NaN — would
// otherwise leave a half-written 200 with no way back, because the status is
// already on the wire. Deciding here makes it a 500 that says nothing, like any
// other server fault.
//
// This binding used to write through the framework's own JSON renderer, and the
// three bindings then disagreed about a failure none of them can prevent: net/http answered a silent 500, Gin answered 200 with a truncated body, and Fiber handed the encoder's error to its default handler, which answers text/plain with the message in it
// ([[FL-013]], [[D-063]]).
func writeJSON(c fiber.Ctx, status int, v any) error {
	body, err := json.Marshal(v)
	if err != nil {
		port.Logger(c.Context()).Error("crudfiber: encoding the response", "err", err)
		status = fiber.StatusInternalServerError
		body, _ = json.Marshal(crudhttp.Internal())
	}
	c.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSONCharsetUTF8)
	return c.Status(status).Send(body)
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
