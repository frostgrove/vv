package crudfiber

import (
	"bytes"
	"net/url"

	"github.com/gofiber/fiber/v3"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/http/crudhttp"
	"github.com/frostgrove/vv/crud/query"
	"github.com/frostgrove/vv/crud/wire"
	"github.com/frostgrove/vv/errs"
	"github.com/frostgrove/vv/port"
)

type Repository[M any, ID comparable, U any] = crudhttp.Repository[M, ID, U]

type Service[M any, ID comparable, U any] = port.Service[M, ID, U]

type Mapper[In, M any] = port.Mapper[In, M]

type PatchMapper[P, U any] = wire.PatchMapper[P, U]

type Presenter[M, R any] = wire.Presenter[M, R]

type ResourceFor[M any, ID comparable, U any, In any, P any, R any] struct {
	service   Service[M, ID, U]
	mapper    Mapper[In, M]
	patcher   PatchMapper[P, U]
	presenter Presenter[M, R]
	opt       options[M, ID, U]
}

type HandlerFor[M any, ID comparable, U any, In any] = ResourceFor[M, ID, U, In, U, M]

type Handler[M any, ID comparable, U any] = ResourceFor[M, ID, U, M, U, M]

func New[M any, ID comparable, U any](repository Repository[M, ID, U], options ...Option[M, ID, U]) *Handler[M, ID, U] {
	o := collect(options)
	return build(port.NewService(repository, o.Service()...), port.Identity[M](), wire.IdentityPatch[U](), wire.IdentityPresenter[M](), o)
}

func NewFor[In, M any, ID comparable, U any](repository Repository[M, ID, U], mapper Mapper[In, M], options ...Option[M, ID, U]) *HandlerFor[M, ID, U, In] {
	o := collect(options)
	return build(port.NewService(repository, o.Service()...), mapper, wire.IdentityPatch[U](), wire.IdentityPresenter[M](), o)
}

func Serving[M any, ID comparable, U any](service Service[M, ID, U], options ...Option[M, ID, U]) *Handler[M, ID, U] {
	o := collect(options)
	o.RefuseServiceOptions("crudfiber.Serving")
	return build(service, port.Identity[M](), wire.IdentityPatch[U](), wire.IdentityPresenter[M](), o)
}

func ServingFor[In, M any, ID comparable, U any](service Service[M, ID, U], mapper Mapper[In, M], options ...Option[M, ID, U]) *HandlerFor[M, ID, U, In] {
	o := collect(options)
	o.RefuseServiceOptions("crudfiber.ServingFor")
	return build(service, mapper, wire.IdentityPatch[U](), wire.IdentityPresenter[M](), o)
}

func NewWire[In, P, R, M any, ID comparable, U any](repository Repository[M, ID, U], mapper Mapper[In, M], patcher PatchMapper[P, U], presenter Presenter[M, R], options ...Option[M, ID, U]) *ResourceFor[M, ID, U, In, P, R] {
	o := collect(options)
	return build(port.NewService(repository, o.Service()...), mapper, patcher, presenter, o)
}

func ServingWire[In, P, R, M any, ID comparable, U any](service Service[M, ID, U], mapper Mapper[In, M], patcher PatchMapper[P, U], presenter Presenter[M, R], options ...Option[M, ID, U]) *ResourceFor[M, ID, U, In, P, R] {
	o := collect(options)
	o.RefuseServiceOptions("crudfiber.ServingWire")
	return build(service, mapper, patcher, presenter, o)
}

func build[M any, ID comparable, U any, In any, P any, R any](service Service[M, ID, U], mapper Mapper[In, M], patcher PatchMapper[P, U], presenter Presenter[M, R], o options[M, ID, U]) *ResourceFor[M, ID, U, In, P, R] {
	h := &ResourceFor[M, ID, U, In, P, R]{service: service, mapper: mapper, patcher: patcher, presenter: presenter, opt: o}
	if h.opt.errorHandler == nil {
		rd := h.opt.renderer
		if rd == nil {
			rd = rendererFor(port.Hops(service, mapper))
		}
		h.opt.errorHandler = func(c fiber.Ctx, err error) error { return render(rd, c, err) }
	}
	return h
}

func (this *ResourceFor[M, ID, U, In, P, R]) Routes() *fiber.App {
	app := fiber.New(fiber.Config{BodyLimit: this.bodyLimit()})
	this.Register(app)
	return app
}

func (this *ResourceFor[M, ID, U, In, P, R]) bodyLimit() int {
	if this.opt.MaxBody > 0 {
		return this.opt.MaxBody + 1
	}
	return crudhttp.MaxBody + 1
}

func (this *ResourceFor[M, ID, U, In, P, R]) Register(r fiber.Router) {
	if !this.opt.ReadOnly {
		r.Post("/", this.Create)
		r.Post("/bulk-delete", this.BulkDelete)
	}
	r.Post("/query", this.Query)
	r.Get("/count", this.CountGet)
	r.Post("/count", this.CountPost)
	r.Get("/", this.List)
	r.Get("/:id", this.GetByID)
	if !this.opt.ReadOnly {
		r.Patch("/:id", this.Update)
		r.Put("/:id", this.Replace)
		r.Delete("/:id", this.Delete)
	}
}

func (this *ResourceFor[M, ID, U, In, P, R]) List(c fiber.Ctx) error {
	request, err := this.parseQueryString(c)
	if err != nil {
		return this.fail(c, err)
	}
	return this.list(c, request)
}

func (this *ResourceFor[M, ID, U, In, P, R]) Query(c fiber.Ctx) error {
	request, err := this.parseBody(c)
	if err != nil {
		return this.fail(c, err)
	}
	return this.list(c, request)
}

func (this *ResourceFor[M, ID, U, In, P, R]) list(c fiber.Ctx, request *query.Request) error {
	scope, err := this.scope(c)
	if err != nil {
		return this.fail(c, err)
	}
	page, err := this.service.List(c.Context(), port.ListCommand{Query: request, Options: scope})
	if err != nil {
		return this.fail(c, err)
	}
	if this.opt.transform == nil {
		return writeJSON(c, fiber.StatusOK, crud.MapPage(page, this.presenter.Response))
	}
	return writeJSON(c, fiber.StatusOK, crud.MapPage(page, func(m M) any {
		return this.opt.transform(c, m)
	}))
}

func (this *ResourceFor[M, ID, U, In, P, R]) CountGet(c fiber.Ctx) error {
	request, err := this.parseQueryString(c)
	if err != nil {
		return this.fail(c, err)
	}
	return this.count(c, request)
}

func (this *ResourceFor[M, ID, U, In, P, R]) CountPost(c fiber.Ctx) error {
	request, err := this.parseBody(c)
	if err != nil {
		return this.fail(c, err)
	}
	return this.count(c, request)
}

func (this *ResourceFor[M, ID, U, In, P, R]) count(c fiber.Ctx, request *query.Request) error {
	scope, err := this.scope(c)
	if err != nil {
		return this.fail(c, err)
	}
	n, err := this.service.Count(c.Context(), port.CountCommand{Query: request, Options: scope})
	if err != nil {
		return this.fail(c, err)
	}
	return writeJSON(c, fiber.StatusOK, fiber.Map{"count": n})
}

func (this *ResourceFor[M, ID, U, In, P, R]) GetByID(c fiber.Ctx) error {
	id, err := this.id(c)
	if err != nil {
		return this.fail(c, err)
	}
	request, err := this.parseQueryString(c)
	if err != nil {
		return this.fail(c, err)
	}
	scope, err := this.scope(c)
	if err != nil {
		return this.fail(c, err)
	}
	m, err := this.service.Get(c.Context(), port.GetCommand[ID]{ID: id, Query: request, Options: scope})
	if err != nil {
		return this.fail(c, err)
	}
	return this.entity(c, fiber.StatusOK, m)
}

func (this *ResourceFor[M, ID, U, In, P, R]) Create(c fiber.Ctx) error {
	var in In
	raw, err := this.decode(c, &in)
	keep(c, raw)
	if err != nil {
		return this.fail(c, err)
	}
	m, err := this.mapper.Model(c.Context(), in)
	if err != nil {
		return this.fail(c, err)
	}
	m, err = this.service.Create(c.Context(), port.CreateCommand[M]{Model: m, Before: this.beforeSave(c)})
	if err != nil {
		return this.fail(c, err)
	}
	return this.entity(c, fiber.StatusCreated, m)
}

func (this *ResourceFor[M, ID, U, In, P, R]) Update(c fiber.Ctx) error {
	id, err := this.id(c)
	if err != nil {
		return this.fail(c, err)
	}
	var patch P
	raw, err := this.decode(c, &patch)
	keep(c, raw)
	if err != nil {
		return this.fail(c, err)
	}
	m, err := this.service.Update(c.Context(), port.UpdateCommand[ID, U]{ID: id, Patch: this.patcher.Update(patch), Before: this.beforeUpdate(c, id)})
	if err != nil {
		return this.fail(c, err)
	}
	return this.entity(c, fiber.StatusOK, m)
}

func (this *ResourceFor[M, ID, U, In, P, R]) Replace(c fiber.Ctx) error {
	id, err := this.id(c)
	if err != nil {
		return this.fail(c, err)
	}
	var in In
	raw, err := this.decode(c, &in)
	keep(c, raw)
	if err != nil {
		return this.fail(c, err)
	}
	m, err := this.mapper.Model(c.Context(), in)
	if err != nil {
		return this.fail(c, err)
	}
	m, err = this.service.Replace(c.Context(), port.ReplaceCommand[ID, M]{ID: id, Model: m, Before: this.beforeSave(c)})
	if err != nil {
		return this.fail(c, err)
	}
	return this.entity(c, fiber.StatusOK, m)
}

func (this *ResourceFor[M, ID, U, In, P, R]) Delete(c fiber.Ctx) error {
	id, err := this.id(c)
	if err != nil {
		return this.fail(c, err)
	}
	n, err := this.service.Delete(c.Context(), port.DeleteCommand[ID]{ID: id})
	if err != nil {
		return this.fail(c, err)
	}
	return writeJSON(c, fiber.StatusOK, fiber.Map{"deleted": n})
}

type BulkDeleteRequest[ID comparable] = crudhttp.BulkDeleteRequest[ID]

func (this *ResourceFor[M, ID, U, In, P, R]) BulkDelete(c fiber.Ctx) error {
	var request BulkDeleteRequest[ID]
	if err := this.decodeOnly(c, &request); err != nil {
		return this.fail(c, err)
	}
	if len(request.IDs) > this.opt.BulkCap() {
		return this.fail(c, crudhttp.BadRequestAs(errs.CodeBadQuery, nil, "at most %d ids per request", this.opt.BulkCap()))
	}
	n, err := this.service.DeleteMany(c.Context(), port.BulkDeleteCommand[ID]{IDs: request.IDs})
	if err != nil {
		return this.fail(c, err)
	}
	return writeJSON(c, fiber.StatusOK, fiber.Map{"deleted": n})
}

func (this *ResourceFor[M, ID, U, In, P, R]) scope(c fiber.Ctx) ([]crud.Option, error) {
	if this.opt.scope == nil {
		return nil, nil
	}
	return this.opt.scope(c)
}

func (this *ResourceFor[M, ID, U, In, P, R]) beforeSave(c fiber.Ctx) func(*M) error {
	if this.opt.beforeSave == nil {
		return nil
	}
	return func(m *M) error { return this.opt.beforeSave(c, m) }
}

func (this *ResourceFor[M, ID, U, In, P, R]) beforeUpdate(c fiber.Ctx, id ID) func(*U) error {
	if this.opt.beforeUpdate == nil {
		return nil
	}
	return func(dataTransferObject *U) error { return this.opt.beforeUpdate(c, id, dataTransferObject) }
}

func (this *ResourceFor[M, ID, U, In, P, R]) parseQueryString(c fiber.Ctx) (*query.Request, error) {
	return query.ParseQuery(queryValues(c))
}

func queryValues(c fiber.Ctx) url.Values {
	v := url.Values{}
	c.Request().URI().QueryArgs().VisitAll(func(key, val []byte) {
		v.Add(string(key), string(val))
	})
	return v
}

func (this *ResourceFor[M, ID, U, In, P, R]) parseBody(c fiber.Ctx) (*query.Request, error) {
	request := &query.Request{}
	if err := this.decodeOnly(c, request); err != nil {
		return nil, err
	}
	return request, nil
}

func (this *ResourceFor[M, ID, U, In, P, R]) decode(c fiber.Ctx, v any) ([]byte, error) {
	return crudhttp.DecodeJSONKeepLimit(bytes.NewReader(c.Body()), v, this.opt.MaxBody)
}

func (this *ResourceFor[M, ID, U, In, P, R]) decodeOnly(c fiber.Ctx, v any) error {
	_, err := this.decode(c, v)
	return err
}

func (this *ResourceFor[M, ID, U, In, P, R]) id(c fiber.Ctx) (ID, error) {
	return port.CoerceID[ID](c.Params("id"))
}

func (this *ResourceFor[M, ID, U, In, P, R]) entity(c fiber.Ctx, status int, m M) error {
	if this.opt.transform != nil {
		return writeJSON(c, status, this.opt.transform(c, m))
	}
	return writeJSON(c, status, this.presenter.Response(m))
}

func (this *ResourceFor[M, ID, U, In, P, R]) fail(c fiber.Ctx, err error) error {
	return this.opt.errorHandler(c, err)
}

type bodyKeyType struct{}

var bodyKey = bodyKeyType{}

func keep(c fiber.Ctx, raw []byte) {
	if len(raw) > 0 {
		fiber.Locals(c, bodyKey, raw)
	}
}
