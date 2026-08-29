// Package crudnet mounts a full CRUD API on net/http's own ServeMux.
//
// The whole set-up is one line:
//
//	crudnet.New(articles).Mount(mux, "/articles")
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
//	GET    /{id}        one entity (?preload=…&select=…)
//	POST   /            create
//	PATCH  /{id}        partial update
//	PUT    /{id}        create-or-replace
//	DELETE /{id}        delete one
//	POST   /bulk-delete delete many, {"ids": […]}
//
// This binding imports nothing outside the standard library, so it lives in the
// library's own module and costs a consumer nothing. The Fiber and Gin bindings
// are separate modules because their frameworks are not free; this one is.
//
// Every route method is an ordinary http.HandlerFunc, so a router that is
// neither ServeMux nor a framework — chi, gorilla/mux, httprouter — can register
// them one by one instead of calling Mount.
//
// Request bodies are JSON, as they are in the Gin binding.
//
// Everything this package does that is not routing, decoding or writing a
// response comes from port: the commands, the service, the field clearing
// ([[D-045]]). Three more constructors follow from that. NewFor takes a mapper
// when the request body is not the model's own JSON shape; Serving and
// ServingFor mount a port.Service that is already built, which is how one
// service value serves Fiber, Gin and net/http at once.
package crudnet

import (
	"io"
	"net/http"
	"strings"

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
// mounts on this binding, on Fiber and on Gin, because a generic alias is the
// same type ([[D-045]]).
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
	o.RefuseServiceOptions("crudnet.Serving")
	return build(service, port.Identity[M](), o)
}

// ServingFor mounts an already-built service behind an input type of its own.
func ServingFor[In, M any, ID comparable, U any](service Service[M, ID, U], mapper Mapper[In, M], options ...Option[M, ID, U]) *HandlerFor[M, ID, U, In] {
	o := collect(options)
	o.RefuseServiceOptions("crudnet.ServingFor")
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
		h.opt.errorHandler = func(w http.ResponseWriter, r *http.Request, err error) { render(rd, w, r, err) }
	}
	return h
}

// Mount registers the routes on a ServeMux under prefix. An empty prefix, or
// "/", mounts them at the root.
//
// The collection is registered under both spellings, `/articles` and
// `/articles/`. ServeMux treats them as different patterns and answers 404 for
// the one that is not registered — it has no trailing-slash redirect of its own,
// so leaving either out would be a route that silently does not exist.
//
// The fixed paths and `/{id}` are siblings on purpose: ServeMux gives the more
// specific pattern precedence, so `/count` is not read as an entity called
// "count" and the order these are registered in does not matter.
func (this *HandlerFor[M, ID, U, In]) Mount(mux *http.ServeMux, prefix string) {
	p := strings.TrimSuffix(prefix, "/")
	if p != "" && !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	// At the root, `/` would match every unclaimed path in the process, so the
	// collection is only ever registered as the exact-match `/{$}`.
	collection := []string{p + "/{$}"}
	if p != "" {
		collection = append(collection, p)
	}

	if !this.opt.ReadOnly {
		for _, c := range collection {
			mux.HandleFunc("POST "+c, this.Create)
		}
		mux.HandleFunc("POST "+p+"/bulk-delete", this.BulkDelete)
	}
	mux.HandleFunc("POST "+p+"/query", this.Query)
	mux.HandleFunc("GET "+p+"/count", this.CountGet)
	mux.HandleFunc("POST "+p+"/count", this.CountPost)
	for _, c := range collection {
		mux.HandleFunc("GET "+c, this.List)
	}
	mux.HandleFunc("GET "+p+"/{id}", this.GetByID)
	if !this.opt.ReadOnly {
		mux.HandleFunc("PATCH "+p+"/{id}", this.Update)
		mux.HandleFunc("PUT "+p+"/{id}", this.Replace)
		mux.HandleFunc("DELETE "+p+"/{id}", this.Delete)
	}
}

// ---------------------------------------------------------------------------
// reads

// List answers GET / using the query-string DSL.
func (this *HandlerFor[M, ID, U, In]) List(w http.ResponseWriter, r *http.Request) {
	request, err := this.parseQueryString(r)
	if err != nil {
		this.fail(w, r, err)
		return
	}
	this.list(w, r, request)
}

// Query answers POST /query using the full JSON DSL.
func (this *HandlerFor[M, ID, U, In]) Query(w http.ResponseWriter, r *http.Request) {
	request, err := this.parseBody(r)
	if err != nil {
		this.fail(w, r, err)
		return
	}
	this.list(w, r, request)
}

func (this *HandlerFor[M, ID, U, In]) list(w http.ResponseWriter, r *http.Request, request *query.Request) {
	scope, err := this.scope(r)
	if err != nil {
		this.fail(w, r, err)
		return
	}
	page, err := this.service.List(r.Context(), port.ListCommand{Query: request, Options: scope})
	if err != nil {
		this.fail(w, r, err)
		return
	}
	if this.opt.transform == nil {
		writeJSON(r.Context(), w, http.StatusOK, page)
		return
	}
	writeJSON(r.Context(), w, http.StatusOK, crud.MapPage(page, func(m M) any {
		return this.opt.transform(r, m)
	}))
}

// CountGet answers GET /count.
func (this *HandlerFor[M, ID, U, In]) CountGet(w http.ResponseWriter, r *http.Request) {
	request, err := this.parseQueryString(r)
	if err != nil {
		this.fail(w, r, err)
		return
	}
	this.count(w, r, request)
}

// CountPost answers POST /count.
func (this *HandlerFor[M, ID, U, In]) CountPost(w http.ResponseWriter, r *http.Request) {
	request, err := this.parseBody(r)
	if err != nil {
		this.fail(w, r, err)
		return
	}
	this.count(w, r, request)
}

func (this *HandlerFor[M, ID, U, In]) count(w http.ResponseWriter, r *http.Request, request *query.Request) {
	scope, err := this.scope(r)
	if err != nil {
		this.fail(w, r, err)
		return
	}
	n, err := this.service.Count(r.Context(), port.CountCommand{Query: request, Options: scope})
	if err != nil {
		this.fail(w, r, err)
		return
	}
	writeJSON(r.Context(), w, http.StatusOK, map[string]any{"count": n})
}

// GetByID answers GET /{id}, honouring ?preload= and ?select=.
func (this *HandlerFor[M, ID, U, In]) GetByID(w http.ResponseWriter, r *http.Request) {
	id, err := this.id(r)
	if err != nil {
		this.fail(w, r, err)
		return
	}
	request, err := this.parseQueryString(r)
	if err != nil {
		this.fail(w, r, err)
		return
	}
	scope, err := this.scope(r)
	if err != nil {
		this.fail(w, r, err)
		return
	}
	m, err := this.service.Get(r.Context(), port.GetCommand[ID]{ID: id, Query: request, Options: scope})
	if err != nil {
		this.fail(w, r, err)
		return
	}
	this.entity(w, r, http.StatusOK, m)
}

// ---------------------------------------------------------------------------
// writes

// Create answers POST /. The body is decoded into this handler's input type and
// mapped onto the model; the service then clears a database-generated key and
// every `generated` column, so a client cannot pick its own id or forge a
// server-side timestamp.
func (this *HandlerFor[M, ID, U, In]) Create(w http.ResponseWriter, r *http.Request) {
	var in In
	raw, err := this.decode(r.Body, &in)
	r = keep(r, raw)
	if err != nil {
		this.fail(w, r, err)
		return
	}
	m, err := this.mapper.Model(r.Context(), in)
	if err != nil {
		this.fail(w, r, err)
		return
	}
	m, err = this.service.Create(r.Context(), port.CreateCommand[M]{Model: m, Before: this.beforeSave(r)})
	if err != nil {
		this.fail(w, r, err)
		return
	}
	this.entity(w, r, http.StatusCreated, m)
}

// Update answers PATCH /{id} with the partial-update DTO.
func (this *HandlerFor[M, ID, U, In]) Update(w http.ResponseWriter, r *http.Request) {
	id, err := this.id(r)
	if err != nil {
		this.fail(w, r, err)
		return
	}
	var dataTransferObject U
	raw, err := this.decode(r.Body, &dataTransferObject)
	r = keep(r, raw)
	if err != nil {
		this.fail(w, r, err)
		return
	}
	m, err := this.service.Update(r.Context(), port.UpdateCommand[ID, U]{ID: id, Patch: dataTransferObject, Before: this.beforeUpdate(r, id)})
	if err != nil {
		this.fail(w, r, err)
		return
	}
	this.entity(w, r, http.StatusOK, m)
}

// Replace answers PUT /{id}: the body becomes the whole row, with the id taken
// from the URL rather than the payload.
//
// When the database generates the key, PUT replaces and never creates: the id
// in the URL has to name a row that already exists. Otherwise PUT is the way
// around AllowClientID — a client cannot pick its id on POST but could put one
// at /999 — and on PostgreSQL an explicit insert into a serial column does not
// advance the sequence, so the next POST collides on the primary key and keeps
// colliding until somebody repairs the sequence by hand. A key the client owns
// (a uuid, a slug) is a different matter and PUT still creates those.
func (this *HandlerFor[M, ID, U, In]) Replace(w http.ResponseWriter, r *http.Request) {
	id, err := this.id(r)
	if err != nil {
		this.fail(w, r, err)
		return
	}
	var in In
	raw, err := this.decode(r.Body, &in)
	r = keep(r, raw)
	if err != nil {
		this.fail(w, r, err)
		return
	}
	m, err := this.mapper.Model(r.Context(), in)
	if err != nil {
		this.fail(w, r, err)
		return
	}
	m, err = this.service.Replace(r.Context(), port.ReplaceCommand[ID, M]{ID: id, Model: m, Before: this.beforeSave(r)})
	if err != nil {
		this.fail(w, r, err)
		return
	}
	this.entity(w, r, http.StatusOK, m)
}

// Delete answers DELETE /{id}.
func (this *HandlerFor[M, ID, U, In]) Delete(w http.ResponseWriter, r *http.Request) {
	id, err := this.id(r)
	if err != nil {
		this.fail(w, r, err)
		return
	}
	n, err := this.service.Delete(r.Context(), port.DeleteCommand[ID]{ID: id})
	if err != nil {
		this.fail(w, r, err)
		return
	}
	writeJSON(r.Context(), w, http.StatusOK, map[string]any{"deleted": n})
}

// BulkDeleteRequest is the body of POST /bulk-delete.
type BulkDeleteRequest[ID comparable] = crudhttp.BulkDeleteRequest[ID]

// BulkDelete answers POST /bulk-delete.
func (this *HandlerFor[M, ID, U, In]) BulkDelete(w http.ResponseWriter, r *http.Request) {
	var request BulkDeleteRequest[ID]
	if err := this.decodeOnly(r.Body, &request); err != nil {
		this.fail(w, r, err)
		return
	}
	if len(request.IDs) > this.opt.BulkCap() {
		this.fail(w, r, crudhttp.BadRequestAs(errs.CodeBadQuery, nil, "at most %d ids per request", this.opt.BulkCap()))
		return
	}
	n, err := this.service.DeleteMany(r.Context(), port.BulkDeleteCommand[ID]{IDs: request.IDs})
	if err != nil {
		this.fail(w, r, err)
		return
	}
	writeJSON(r.Context(), w, http.StatusOK, map[string]any{"deleted": n})
}

// ---------------------------------------------------------------------------
// plumbing

// scope is the transport's own narrowing, handed to the service as options it
// appends after the query document compiles. Appended and not merged, because
// crud.Where ANDs ([[D-004]]).
func (this *HandlerFor[M, ID, U, In]) scope(r *http.Request) ([]crud.Option, error) {
	if this.opt.scope == nil {
		return nil, nil
	}
	return this.opt.scope(r)
}

// beforeSave binds the create-and-replace hook to this request, so the service
// can run it in the one place the order is documented: after the server-owned
// fields are cleared ([[UC-013]] guarantee 7).
func (this *HandlerFor[M, ID, U, In]) beforeSave(r *http.Request) func(*M) error {
	if this.opt.beforeSave == nil {
		return nil
	}
	return func(m *M) error { return this.opt.beforeSave(r, m) }
}

// beforeUpdate binds the PATCH hook to this request and its path id.
func (this *HandlerFor[M, ID, U, In]) beforeUpdate(r *http.Request, id ID) func(*U) error {
	if this.opt.beforeUpdate == nil {
		return nil
	}
	return func(dataTransferObject *U) error { return this.opt.beforeUpdate(r, id, dataTransferObject) }
}

// parseQueryString reads the raw query values. URL.Query() keeps every repeat,
// so a second `f=` is a second filter term rather than a silently narrower one.
func (this *HandlerFor[M, ID, U, In]) parseQueryString(r *http.Request) (*query.Request, error) {
	return query.ParseQuery(r.URL.Query())
}

func (this *HandlerFor[M, ID, U, In]) parseBody(r *http.Request) (*query.Request, error) {
	request := &query.Request{}
	if err := this.decodeOnly(r.Body, request); err != nil {
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

// id reads and converts the {id} path parameter.
func (this *HandlerFor[M, ID, U, In]) id(r *http.Request) (ID, error) {
	return port.CoerceID[ID](r.PathValue("id"))
}

func (this *HandlerFor[M, ID, U, In]) entity(w http.ResponseWriter, r *http.Request, status int, m M) {
	if this.opt.transform != nil {
		writeJSON(r.Context(), w, status, this.opt.transform(r, m))
		return
	}
	writeJSON(r.Context(), w, status, m)
}

func (this *HandlerFor[M, ID, U, In]) fail(w http.ResponseWriter, r *http.Request, err error) {
	this.opt.errorHandler(w, r, err)
}

// keep carries the decoded bytes to the renderer, for the raw-body path
// fallback ([[D-043]]). One copy per write request, capped, and only on the
// three routes whose body carries field values — a bulk delete carries ids, and
// a restrict violation raised by one names a column of the child table, which
// this model's Meta could not translate anyway.
func keep(r *http.Request, raw []byte) *http.Request {
	if len(raw) == 0 {
		return r
	}
	return r.WithContext(crudhttp.WithBody(r.Context(), raw))
}
