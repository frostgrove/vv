// Package crudfiber mounts a full CRUD API on a Fiber v3 router.
//
// The whole set-up is one line:
//
//	app.Use("/articles", crudfiber.New(articles).Routes())
//
// where `articles` is anything satisfying Repository — a crud.Repo, a
// specs.Repo, or your own service struct that embeds one and adds business
// rules. The handler never reaches past that interface, so a service layer
// slots in without the handler noticing.
//
// Routes:
//
//	GET    /            list, query-string DSL
//	POST   /query       list, full JSON DSL
//	GET    /count       count, query-string DSL
//	POST   /count       count, JSON DSL
//	GET    /:id         one entity (?preload=…&select=…)
//	POST   /            create
//	PATCH  /:id         partial update
//	PUT    /:id         create-or-replace
//	DELETE /:id         delete one
//	POST   /bulk-delete delete many, {"ids": […]}
//
// Everything this package does that is not routing, decoding or writing a
// response comes from port: the commands, the service, the field clearing
// ([[D-045]]). Three more constructors follow from that. NewFor takes a mapper
// when the request body is not the model's own JSON shape; Serving and
// ServingFor mount a port.Service that is already built, which is how one
// service value serves Fiber, Gin and net/http at once.
package crudfiber

import (
	"bytes"
	"net/url"

	"github.com/gofiber/fiber/v3"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/http/crudhttp"
	"github.com/frostgrove/vv/crud/query"
	"github.com/frostgrove/vv/errs"
	"github.com/frostgrove/vv/port"
)

// Repository is everything the default service needs. crud.Repo[M, ID, U]
// satisfies it, and so does specs.Repo and any struct that embeds either —
// which is how a service layer with extra checks takes the repository's place.
type Repository[M any, ID comparable, U any] = crudhttp.Repository[M, ID, U]

// Service is the transport-neutral seam every route talks to. One value of it
// mounts on this binding, on Gin and on net/http, because a generic alias is
// the same type ([[D-045]]).
type Service[M any, ID comparable, U any] = port.Service[M, ID, U]

// Mapper turns this transport's input type into the model, for a resource whose
// request body is not the model's own JSON shape.
type Mapper[In, M any] = port.Mapper[In, M]

// HandlerFor is the mounted API for a resource whose input type is In.
//
// The fourth parameter is what lets New keep inferring three: [Handler] is an
// alias that fills In in with the model, so every existing signature still
// compiles and only NewFor has to name a fourth type ([[D-022]]).
type HandlerFor[M any, ID comparable, U any, In any] struct {
	service Service[M, ID, U]
	mapper  Mapper[In, M]
	opt     options[M, ID, U]
}

// Handler is the mounted API — a HandlerFor whose input type is the model,
// which is what New means.
type Handler[M any, ID comparable, U any] = HandlerFor[M, ID, U, M]

// New builds a handler over a repository. All three type parameters are
// inferred from it, so the call site carries no generics.
//
// The service it builds is the default one, configured from the options that
// are about rules rather than about transport.
func New[M any, ID comparable, U any](repository Repository[M, ID, U], options ...Option[M, ID, U]) *Handler[M, ID, U] {
	o := collect(options)
	return build(port.NewService(repository, o.Service()...), port.Identity[M](), o)
}

// NewFor builds a handler whose request body is a type of its own, mapped onto
// the model before the service sees it. All four type parameters are inferred
// from the repository and the mapper.
func NewFor[In, M any, ID comparable, U any](repository Repository[M, ID, U], mapper Mapper[In, M], options ...Option[M, ID, U]) *HandlerFor[M, ID, U, In] {
	o := collect(options)
	return build(port.NewService(repository, o.Service()...), mapper, o)
}

// Serving mounts a service that is already built — the one a generator wrote,
// or one an application assembled itself.
//
// An option that configures the service is refused here rather than ignored:
// the service is already made, and a silent no-op is the wrong answer to
// "bound what clients may ask for" ([[D-021]]).
func Serving[M any, ID comparable, U any](service Service[M, ID, U], options ...Option[M, ID, U]) *Handler[M, ID, U] {
	o := collect(options)
	o.RefuseServiceOptions("crudfiber.Serving")
	return build(service, port.Identity[M](), o)
}

// ServingFor mounts an already-built service behind an input type of its own.
func ServingFor[In, M any, ID comparable, U any](service Service[M, ID, U], mapper Mapper[In, M], options ...Option[M, ID, U]) *HandlerFor[M, ID, U, In] {
	o := collect(options)
	o.RefuseServiceOptions("crudfiber.ServingFor")
	return build(service, mapper, o)
}

// build is the one place a handler is assembled, so the four constructors
// cannot drift in what they wire.
func build[M any, ID comparable, U any, In any](service Service[M, ID, U], mapper Mapper[In, M], o options[M, ID, U]) *HandlerFor[M, ID, U, In] {
	h := &HandlerFor[M, ID, U, In]{service: service, mapper: mapper, opt: o}
	if h.opt.errorHandler == nil {
		// After the options, not before: WithRenderer has to be able to reach
		// the handler the routes actually call, and a default installed first
		// would have closed over the wrong renderer.
		rd := h.opt.renderer
		if rd == nil {
			rd = rendererFor(port.Hops(service, mapper))
		}
		h.opt.errorHandler = func(c fiber.Ctx, err error) error { return render(rd, c, err) }
	}
	return h
}

// Routes returns a standalone app to mount with app.Use("/prefix", …).
//
// The app's own BodyLimit is set to this handler's cap so the refusal is this
// library's envelope rather than Fiber's plain-text one, and so the number is
// the same one crudnet and crudgin enforce. Register cannot do that — the limit
// belongs to the app, which the caller owns there — and [[FL-013]] carries the
// difference.
func (this *HandlerFor[M, ID, U, In]) Routes() *fiber.App {
	app := fiber.New(fiber.Config{BodyLimit: this.bodyLimit()})
	this.Register(app)
	return app
}

// bodyLimit is what Fiber is told to accept: one byte past our own cap, so the
// body that is exactly at the cap reaches the decoder and the one past it is
// refused by us, with our envelope, rather than by Fiber with its own.
func (this *HandlerFor[M, ID, U, In]) bodyLimit() int {
	if this.opt.MaxBody > 0 {
		return this.opt.MaxBody + 1
	}
	return crudhttp.MaxBody + 1
}

// Register mounts the routes on an existing router or group. `/count` is
// registered before `/:id` so it is not swallowed by the parameter route.
func (this *HandlerFor[M, ID, U, In]) Register(r fiber.Router) {
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

// ---------------------------------------------------------------------------
// reads

// List answers GET / using the query-string DSL.
func (this *HandlerFor[M, ID, U, In]) List(c fiber.Ctx) error {
	request, err := this.parseQueryString(c)
	if err != nil {
		return this.fail(c, err)
	}
	return this.list(c, request)
}

// Query answers POST /query using the full JSON DSL.
func (this *HandlerFor[M, ID, U, In]) Query(c fiber.Ctx) error {
	request, err := this.parseBody(c)
	if err != nil {
		return this.fail(c, err)
	}
	return this.list(c, request)
}

func (this *HandlerFor[M, ID, U, In]) list(c fiber.Ctx, request *query.Request) error {
	scope, err := this.scope(c)
	if err != nil {
		return this.fail(c, err)
	}
	page, err := this.service.List(c.Context(), port.ListCommand{Query: request, Options: scope})
	if err != nil {
		return this.fail(c, err)
	}
	if this.opt.transform == nil {
		return writeJSON(c, fiber.StatusOK, page)
	}
	return writeJSON(c, fiber.StatusOK, crud.MapPage(page, func(m M) any {
		return this.opt.transform(c, m)
	}))
}

// CountGet answers GET /count.
func (this *HandlerFor[M, ID, U, In]) CountGet(c fiber.Ctx) error {
	request, err := this.parseQueryString(c)
	if err != nil {
		return this.fail(c, err)
	}
	return this.count(c, request)
}

// CountPost answers POST /count.
func (this *HandlerFor[M, ID, U, In]) CountPost(c fiber.Ctx) error {
	request, err := this.parseBody(c)
	if err != nil {
		return this.fail(c, err)
	}
	return this.count(c, request)
}

func (this *HandlerFor[M, ID, U, In]) count(c fiber.Ctx, request *query.Request) error {
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

// GetByID answers GET /:id, honouring ?preload= and ?select=.
func (this *HandlerFor[M, ID, U, In]) GetByID(c fiber.Ctx) error {
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

// ---------------------------------------------------------------------------
// writes

// Create answers POST /. The body is decoded into this handler's input type and
// mapped onto the model; the service then clears a database-generated key and
// every generated/version/server-owned field, so a client cannot pick its own
// id or forge repository lifecycle state.
func (this *HandlerFor[M, ID, U, In]) Create(c fiber.Ctx) error {
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

// Update answers PATCH /:id with the partial-update DTO.
func (this *HandlerFor[M, ID, U, In]) Update(c fiber.Ctx) error {
	id, err := this.id(c)
	if err != nil {
		return this.fail(c, err)
	}
	var dataTransferObject U
	raw, err := this.decode(c, &dataTransferObject)
	keep(c, raw)
	if err != nil {
		return this.fail(c, err)
	}
	m, err := this.service.Update(c.Context(), port.UpdateCommand[ID, U]{ID: id, Patch: dataTransferObject, Before: this.beforeUpdate(c, id)})
	if err != nil {
		return this.fail(c, err)
	}
	return this.entity(c, fiber.StatusOK, m)
}

// Replace answers PUT /:id: the body becomes the whole row, with the id taken
// from the URL rather than the payload.
//
// When the database generates the key, PUT replaces and never creates: the id
// in the URL has to name a row that already exists. Otherwise PUT is the way
// around AllowClientID — a client cannot pick its id on POST but could put one
// at /999 — and on PostgreSQL an explicit insert into a serial column does not
// advance the sequence, so the next POST collides on the primary key and keeps
// colliding until somebody repairs the sequence by hand. A key the client owns
// (a uuid, a slug) is a different matter and PUT still creates those.
func (this *HandlerFor[M, ID, U, In]) Replace(c fiber.Ctx) error {
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

// Delete answers DELETE /:id.
func (this *HandlerFor[M, ID, U, In]) Delete(c fiber.Ctx) error {
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

// BulkDeleteRequest is the body of POST /bulk-delete.
type BulkDeleteRequest[ID comparable] = crudhttp.BulkDeleteRequest[ID]

// BulkDelete answers POST /bulk-delete.
func (this *HandlerFor[M, ID, U, In]) BulkDelete(c fiber.Ctx) error {
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

// ---------------------------------------------------------------------------
// plumbing

// scope is the transport's own narrowing, handed to the service as options it
// appends after the query document compiles. Appended and not merged, because
// crud.Where ANDs ([[D-004]]).
func (this *HandlerFor[M, ID, U, In]) scope(c fiber.Ctx) ([]crud.Option, error) {
	if this.opt.scope == nil {
		return nil, nil
	}
	return this.opt.scope(c)
}

// beforeSave binds the create-and-replace hook to this request, so the service
// can run it in the one place the order is documented: after the server-owned
// fields are cleared ([[UC-013]] guarantee 7).
func (this *HandlerFor[M, ID, U, In]) beforeSave(c fiber.Ctx) func(*M) error {
	if this.opt.beforeSave == nil {
		return nil
	}
	return func(m *M) error { return this.opt.beforeSave(c, m) }
}

// beforeUpdate binds the PATCH hook to this request and its path id.
func (this *HandlerFor[M, ID, U, In]) beforeUpdate(c fiber.Ctx, id ID) func(*U) error {
	if this.opt.beforeUpdate == nil {
		return nil
	}
	return func(dataTransferObject *U) error { return this.opt.beforeUpdate(c, id, dataTransferObject) }
}

func (this *HandlerFor[M, ID, U, In]) parseQueryString(c fiber.Ctx) (*query.Request, error) {
	return query.ParseQuery(queryValues(c))
}

// queryValues reads the raw query args. Fiber's Queries() collapses repeats
// into a map, which would quietly drop the second `f=` filter.
func queryValues(c fiber.Ctx) url.Values {
	v := url.Values{}
	c.Request().URI().QueryArgs().VisitAll(func(key, val []byte) {
		v.Add(string(key), string(val))
	})
	return v
}

func (this *HandlerFor[M, ID, U, In]) parseBody(c fiber.Ctx) (*query.Request, error) {
	request := &query.Request{}
	if err := this.decodeOnly(c, request); err != nil {
		return nil, err
	}
	return request, nil
}

// decode reads a JSON body onto v under this handler's cap and hands back the
// bytes, for the raw-body path fallback ([[D-043]]).
//
// encoding/json and not c.Bind().Body, which dispatches on Content-Type. The
// binder would make this the one binding of the three that accepts a form or an
// XML body, and the one where a `binding:"required"` tag in the consumer's own
// model changes what the routes accept — a difference in what the API is, owned
// by nobody and visible only under Fiber ([[FL-013]]).
func (this *HandlerFor[M, ID, U, In]) decode(c fiber.Ctx, v any) ([]byte, error) {
	return crudhttp.DecodeJSONKeepLimit(bytes.NewReader(c.Body()), v, this.opt.MaxBody)
}

// decodeOnly is decode for the routes whose body carries no field values, so
// there is nothing worth keeping.
func (this *HandlerFor[M, ID, U, In]) decodeOnly(c fiber.Ctx, v any) error {
	_, err := this.decode(c, v)
	return err
}

// id reads and converts the :id path parameter.
func (this *HandlerFor[M, ID, U, In]) id(c fiber.Ctx) (ID, error) {
	return port.CoerceID[ID](c.Params("id"))
}

func (this *HandlerFor[M, ID, U, In]) entity(c fiber.Ctx, status int, m M) error {
	if this.opt.transform != nil {
		return writeJSON(c, status, this.opt.transform(c, m))
	}
	return writeJSON(c, status, m)
}

func (this *HandlerFor[M, ID, U, In]) fail(c fiber.Ctx, err error) error {
	return this.opt.errorHandler(c, err)
}

// bodyKey is where the retained request body lives on this binding. Locals and
// not the context, because Fiber hands a handler no context it can replace.
type bodyKeyType struct{}

var bodyKey = bodyKeyType{}

// keep files the bytes the decoder already copied, for the raw-body path
// fallback ([[D-043]]).
//
// A copy and never a reference, which is why it takes the decoder's slice
// rather than c.Body(). Fiber documents c.Body() as valid only within the
// handler and this binding builds its app with a plain fiber.New(), so
// Immutable is off — a stored reference is a use-after-free that would surface
// as a corrupted field path under load.
//
// Only on the three routes whose body carries field values: a bulk delete
// carries ids, and a restrict violation raised by one names a column of the
// child table, which this model's Meta could not translate anyway.
func keep(c fiber.Ctx, raw []byte) {
	if len(raw) > 0 {
		fiber.Locals(c, bodyKey, raw)
	}
}
