// Package crudgin mounts a full CRUD API on a Gin router.
//
// The whole set-up is one line:
//
//	crudgin.New(articles).Mount(r, "/articles")
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
// Request bodies are JSON only. The sibling Fiber binding also accepts XML and
// form encodings, because Fiber's binder dispatches on Content-Type; this one
// decodes with encoding/json so that a `binding:"…"` tag on a model cannot make
// the same request behave differently under the two transports.
//
// Everything this package does that is not routing, decoding or writing a
// response comes from port: the commands, the service, the field clearing
// ([[D-045]]). Three more constructors follow from that. NewFor takes a mapper
// when the request body is not the model's own JSON shape; Serving and
// ServingFor mount a port.Service that is already built, which is how one
// service value serves Gin, Fiber and net/http at once.
package crudgin

import (
	"io"
	"net/http"

	"github.com/gin-gonic/gin"

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
// mounts on this binding, on Fiber and on net/http, because a generic alias is
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
	o.RefuseServiceOptions("crudgin.Serving")
	return build(service, port.Identity[M](), o)
}

// ServingFor mounts an already-built service behind an input type of its own.
func ServingFor[In, M any, ID comparable, U any](service Service[M, ID, U], mapper Mapper[In, M], options ...Option[M, ID, U]) *HandlerFor[M, ID, U, In] {
	o := collect(options)
	o.RefuseServiceOptions("crudgin.ServingFor")
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
		h.opt.errorHandler = func(c *gin.Context, err error) { render(rd, c, err) }
	}
	return h
}

// Mount groups the routes under a prefix. Gin has no mountable sub-application,
// so this is the counterpart of Fiber's app.Use("/prefix", …).
func (this *HandlerFor[M, ID, U, In]) Mount(r gin.IRouter, prefix string) {
	this.Register(r.Group(prefix))
}

// Register mounts the routes on an existing router or group.
//
// The collection routes are registered as "" rather than "/": on a group of
// /widgets the latter produces /widgets/, which does not match GET /widgets.
// Registering both forms is not an option — on the engine itself they collapse
// to the same path and Gin panics. The trailing-slash form is left to Gin's own
// RedirectTrailingSlash, which is on by default.
func (this *HandlerFor[M, ID, U, In]) Register(r gin.IRoutes) {
	if !this.opt.ReadOnly {
		r.POST("", this.Create)
		r.POST("/bulk-delete", this.BulkDelete)
	}
	r.POST("/query", this.Query)
	r.GET("/count", this.CountGet)
	r.POST("/count", this.CountPost)
	r.GET("", this.List)
	r.GET("/:id", this.GetByID)
	if !this.opt.ReadOnly {
		r.PATCH("/:id", this.Update)
		r.PUT("/:id", this.Replace)
		r.DELETE("/:id", this.Delete)
	}
}

// ---------------------------------------------------------------------------
// reads

// List answers GET / using the query-string DSL.
func (this *HandlerFor[M, ID, U, In]) List(c *gin.Context) {
	request, err := this.parseQueryString(c)
	if err != nil {
		this.fail(c, err)
		return
	}
	this.list(c, request)
}

// Query answers POST /query using the full JSON DSL.
func (this *HandlerFor[M, ID, U, In]) Query(c *gin.Context) {
	request, err := this.parseBody(c)
	if err != nil {
		this.fail(c, err)
		return
	}
	this.list(c, request)
}

func (this *HandlerFor[M, ID, U, In]) list(c *gin.Context, request *query.Request) {
	scope, err := this.scope(c)
	if err != nil {
		this.fail(c, err)
		return
	}
	page, err := this.service.List(c.Request.Context(), port.ListCommand{Query: request, Options: scope})
	if err != nil {
		this.fail(c, err)
		return
	}
	if this.opt.transform == nil {
		writeJSON(c, http.StatusOK, page)
		return
	}
	writeJSON(c, http.StatusOK, crud.MapPage(page, func(m M) any {
		return this.opt.transform(c, m)
	}))
}

// CountGet answers GET /count.
func (this *HandlerFor[M, ID, U, In]) CountGet(c *gin.Context) {
	request, err := this.parseQueryString(c)
	if err != nil {
		this.fail(c, err)
		return
	}
	this.count(c, request)
}

// CountPost answers POST /count.
func (this *HandlerFor[M, ID, U, In]) CountPost(c *gin.Context) {
	request, err := this.parseBody(c)
	if err != nil {
		this.fail(c, err)
		return
	}
	this.count(c, request)
}

func (this *HandlerFor[M, ID, U, In]) count(c *gin.Context, request *query.Request) {
	scope, err := this.scope(c)
	if err != nil {
		this.fail(c, err)
		return
	}
	n, err := this.service.Count(c.Request.Context(), port.CountCommand{Query: request, Options: scope})
	if err != nil {
		this.fail(c, err)
		return
	}
	writeJSON(c, http.StatusOK, gin.H{"count": n})
}

// GetByID answers GET /:id, honouring ?preload= and ?select=.
func (this *HandlerFor[M, ID, U, In]) GetByID(c *gin.Context) {
	id, err := this.id(c)
	if err != nil {
		this.fail(c, err)
		return
	}
	request, err := this.parseQueryString(c)
	if err != nil {
		this.fail(c, err)
		return
	}
	scope, err := this.scope(c)
	if err != nil {
		this.fail(c, err)
		return
	}
	m, err := this.service.Get(c.Request.Context(), port.GetCommand[ID]{ID: id, Query: request, Options: scope})
	if err != nil {
		this.fail(c, err)
		return
	}
	this.entity(c, http.StatusOK, m)
}

// ---------------------------------------------------------------------------
// writes

// Create answers POST /. The body is decoded into this handler's input type and
// mapped onto the model; the service then clears a database-generated key and
// every `generated` column, so a client cannot pick its own id or forge a
// server-side timestamp.
func (this *HandlerFor[M, ID, U, In]) Create(c *gin.Context) {
	var in In
	raw, err := this.decode(c.Request.Body, &in)
	keep(c, raw)
	if err != nil {
		this.fail(c, err)
		return
	}
	m, err := this.mapper.Model(c.Request.Context(), in)
	if err != nil {
		this.fail(c, err)
		return
	}
	m, err = this.service.Create(c.Request.Context(), port.CreateCommand[M]{Model: m, Before: this.beforeSave(c)})
	if err != nil {
		this.fail(c, err)
		return
	}
	this.entity(c, http.StatusCreated, m)
}

// Update answers PATCH /:id with the partial-update DTO.
func (this *HandlerFor[M, ID, U, In]) Update(c *gin.Context) {
	id, err := this.id(c)
	if err != nil {
		this.fail(c, err)
		return
	}
	var dataTransferObject U
	raw, err := this.decode(c.Request.Body, &dataTransferObject)
	keep(c, raw)
	if err != nil {
		this.fail(c, err)
		return
	}
	m, err := this.service.Update(c.Request.Context(), port.UpdateCommand[ID, U]{ID: id, Patch: dataTransferObject, Before: this.beforeUpdate(c, id)})
	if err != nil {
		this.fail(c, err)
		return
	}
	this.entity(c, http.StatusOK, m)
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
func (this *HandlerFor[M, ID, U, In]) Replace(c *gin.Context) {
	id, err := this.id(c)
	if err != nil {
		this.fail(c, err)
		return
	}
	var in In
	raw, err := this.decode(c.Request.Body, &in)
	keep(c, raw)
	if err != nil {
		this.fail(c, err)
		return
	}
	m, err := this.mapper.Model(c.Request.Context(), in)
	if err != nil {
		this.fail(c, err)
		return
	}
	m, err = this.service.Replace(c.Request.Context(), port.ReplaceCommand[ID, M]{ID: id, Model: m, Before: this.beforeSave(c)})
	if err != nil {
		this.fail(c, err)
		return
	}
	this.entity(c, http.StatusOK, m)
}

// Delete answers DELETE /:id.
func (this *HandlerFor[M, ID, U, In]) Delete(c *gin.Context) {
	id, err := this.id(c)
	if err != nil {
		this.fail(c, err)
		return
	}
	n, err := this.service.Delete(c.Request.Context(), port.DeleteCommand[ID]{ID: id})
	if err != nil {
		this.fail(c, err)
		return
	}
	writeJSON(c, http.StatusOK, gin.H{"deleted": n})
}

// BulkDeleteRequest is the body of POST /bulk-delete.
type BulkDeleteRequest[ID comparable] = crudhttp.BulkDeleteRequest[ID]

// BulkDelete answers POST /bulk-delete.
func (this *HandlerFor[M, ID, U, In]) BulkDelete(c *gin.Context) {
	var request BulkDeleteRequest[ID]
	if err := this.decodeOnly(c.Request.Body, &request); err != nil {
		this.fail(c, err)
		return
	}
	if len(request.IDs) > this.opt.BulkCap() {
		this.fail(c, crudhttp.BadRequestAs(errs.CodeBadQuery, nil, "at most %d ids per request", this.opt.BulkCap()))
		return
	}
	n, err := this.service.DeleteMany(c.Request.Context(), port.BulkDeleteCommand[ID]{IDs: request.IDs})
	if err != nil {
		this.fail(c, err)
		return
	}
	writeJSON(c, http.StatusOK, gin.H{"deleted": n})
}

// ---------------------------------------------------------------------------
// plumbing

// scope is the transport's own narrowing, handed to the service as options it
// appends after the query document compiles. Appended and not merged, because
// crud.Where ANDs ([[D-004]]).
func (this *HandlerFor[M, ID, U, In]) scope(c *gin.Context) ([]crud.Option, error) {
	if this.opt.scope == nil {
		return nil, nil
	}
	return this.opt.scope(c)
}

// beforeSave binds the create-and-replace hook to this request, so the service
// can run it in the one place the order is documented: after the server-owned
// fields are cleared ([[UC-013]] guarantee 7).
func (this *HandlerFor[M, ID, U, In]) beforeSave(c *gin.Context) func(*M) error {
	if this.opt.beforeSave == nil {
		return nil
	}
	return func(m *M) error { return this.opt.beforeSave(c, m) }
}

// beforeUpdate binds the PATCH hook to this request and its path id.
func (this *HandlerFor[M, ID, U, In]) beforeUpdate(c *gin.Context, id ID) func(*U) error {
	if this.opt.beforeUpdate == nil {
		return nil
	}
	return func(dataTransferObject *U) error { return this.opt.beforeUpdate(c, id, dataTransferObject) }
}

// parseQueryString reads the raw query args. Gin's Query() returns the last
// value for a key, which would quietly drop the second `f=` filter; URL.Query()
// keeps every repeat, and the filter is the AND of all of them.
func (this *HandlerFor[M, ID, U, In]) parseQueryString(c *gin.Context) (*query.Request, error) {
	return query.ParseQuery(c.Request.URL.Query())
}

func (this *HandlerFor[M, ID, U, In]) parseBody(c *gin.Context) (*query.Request, error) {
	request := &query.Request{}
	if err := this.decodeOnly(c.Request.Body, request); err != nil {
		return nil, err
	}
	return request, nil
}

// decode reads a JSON body onto v under this handler's cap and hands back the
// bytes, for the raw-body path fallback ([[D-043]]).
func (this *HandlerFor[M, ID, U, In]) decode(r io.Reader, v any) ([]byte, error) {
	return crudhttp.DecodeJSONKeepLimit(r, v, this.opt.MaxBody)
}

// decodeOnly is decode for the routes whose body carries no field values, so
// there is nothing worth keeping.
func (this *HandlerFor[M, ID, U, In]) decodeOnly(r io.Reader, v any) error {
	_, err := this.decode(r, v)
	return err
}

// id reads and converts the :id path parameter.
func (this *HandlerFor[M, ID, U, In]) id(c *gin.Context) (ID, error) {
	return port.CoerceID[ID](c.Param("id"))
}

func (this *HandlerFor[M, ID, U, In]) entity(c *gin.Context, status int, m M) {
	if this.opt.transform != nil {
		writeJSON(c, status, this.opt.transform(c, m))
		return
	}
	writeJSON(c, status, m)
}

func (this *HandlerFor[M, ID, U, In]) fail(c *gin.Context, err error) {
	this.opt.errorHandler(c, err)
}

// keep carries the decoded bytes to the renderer, for the raw-body path
// fallback ([[D-043]]). One copy per write request, capped, and only on the
// three routes whose body carries field values — a bulk delete carries ids, and
// a restrict violation raised by one names a column of the child table, which
// this model's Meta could not translate anyway.
func keep(c *gin.Context, raw []byte) {
	if len(raw) == 0 {
		return
	}
	c.Request = c.Request.WithContext(crudhttp.WithBody(c.Request.Context(), raw))
}
