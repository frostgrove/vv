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

type Option[M any, ID comparable, U any] func(*options[M, ID, U])

func collect[M any, ID comparable, U any](optionList []Option[M, ID, U]) options[M, ID, U] {
	var o options[M, ID, U]
	for _, fn := range optionList {
		fn(&o)
	}
	return o
}

func WithQuery[M any, ID comparable, U any](config *query.Config) Option[M, ID, U] {
	return func(o *options[M, ID, U]) {
		o.Query, o.QueryVariants, o.QuerySelector = config, nil, nil
	}
}

func WithQueryFor[M any, ID comparable, U any](defaultConfig *query.Config, variants map[string]*query.Config, selectConfig port.QuerySelector) Option[M, ID, U] {
	return func(o *options[M, ID, U]) {
		o.Query, o.QueryVariants, o.QuerySelector = defaultConfig, variants, selectConfig
	}
}

func WithErrorHandler[M any, ID comparable, U any](fn func(fiber.Ctx, error) error) Option[M, ID, U] {
	return func(o *options[M, ID, U]) { o.errorHandler = fn }
}

func WithRenderer[M any, ID comparable, U any](r crudhttp.Renderer) Option[M, ID, U] {
	return func(o *options[M, ID, U]) { o.renderer = r }
}

func WithTransform[M any, ID comparable, U any](fn func(fiber.Ctx, M) any) Option[M, ID, U] {
	return func(o *options[M, ID, U]) { o.transform = fn }
}

func WithScope[M any, ID comparable, U any](fn func(fiber.Ctx) ([]crud.Option, error)) Option[M, ID, U] {
	return func(o *options[M, ID, U]) { o.scope = fn }
}

func BeforeSave[M any, ID comparable, U any](fn func(fiber.Ctx, *M) error) Option[M, ID, U] {
	return func(o *options[M, ID, U]) { o.beforeSave = fn }
}

func BeforeUpdate[M any, ID comparable, U any](fn func(fiber.Ctx, ID, *U) error) Option[M, ID, U] {
	return func(o *options[M, ID, U]) { o.beforeUpdate = fn }
}

func ReadOnly[M any, ID comparable, U any]() Option[M, ID, U] {
	return func(o *options[M, ID, U]) { o.ReadOnly = true }
}

func AllowClientID[M any, ID comparable, U any]() Option[M, ID, U] {
	return func(o *options[M, ID, U]) { o.AllowClientID = true }
}

func MaxBody[M any, ID comparable, U any](n int) Option[M, ID, U] {
	return func(o *options[M, ID, U]) { o.MaxBody = n }
}

func MaxBulk[M any, ID comparable, U any](n int) Option[M, ID, U] {
	return func(o *options[M, ID, U]) { o.MaxBulk = n }
}

type Envelope = crudhttp.Envelope

type Renderer = crudhttp.Renderer

var defaultRenderer = crudhttp.NewRenderer()

func rendererFor(hops []errs.Resolver) crudhttp.Renderer {
	if len(hops) == 0 {
		return defaultRenderer
	}
	return crudhttp.NewRenderer(crudhttp.WithResolvers(hops...))
}

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

func Status(err error) int { return crudhttp.Status(err) }

func DefaultErrorHandler(c fiber.Ctx, err error) error {
	return render(defaultRenderer, c, err)
}

func render(rd crudhttp.Renderer, c fiber.Ctx, err error) error {
	ctx := crudhttp.WithBody(c.Context(), fiber.Locals[[]byte](c, bodyKey))

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
