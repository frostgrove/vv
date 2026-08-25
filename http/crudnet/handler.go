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
	"net/http"
	"strings"

	"github.com/shardit-io/vv/crud"
	"github.com/shardit-io/vv/errs"
	"github.com/shardit-io/vv/http/crudhttp"
	"github.com/shardit-io/vv/port"
	"github.com/shardit-io/vv/query"
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
	svc    Service[M, ID, U]
	mapper Mapper[In, M]
	opt    options[M, ID, U]
}

// Handler is the mounted API — a HandlerFor whose input type is the model,
// which is what New means.
type Handler[M any, ID comparable, U any] = HandlerFor[M, ID, U, M]

// New builds a handler over a repository. All three type parameters are
// inferred from it, so the call site carries no generics.
//
// The service it builds is the default one, configured from the options that
// are about rules rather than about transport.
func New[M any, ID comparable, U any](repo Repository[M, ID, U], opts ...Option[M, ID, U]) *Handler[M, ID, U] {
	o := collect(opts)
	return build(port.NewService(repo, o.service()...), port.Identity[M](), o)
}

// NewFor builds a handler whose request body is a type of its own, mapped onto
// the model before the service sees it. All four type parameters are inferred
// from the repository and the mapper.
func NewFor[In, M any, ID comparable, U any](repo Repository[M, ID, U], mapper Mapper[In, M], opts ...Option[M, ID, U]) *HandlerFor[M, ID, U, In] {
	o := collect(opts)
	return build(port.NewService(repo, o.service()...), mapper, o)
}

// Serving mounts a service that is already built — the one a generator wrote,
// or one an application assembled itself.
//
// An option that configures the service is refused here rather than ignored:
// the service is already made, and a silent no-op is the wrong answer to
// "bound what clients may ask for" ([[D-021]]).
func Serving[M any, ID comparable, U any](svc Service[M, ID, U], opts ...Option[M, ID, U]) *Handler[M, ID, U] {
	o := collect(opts)
	o.refuseServiceOptions("crudnet.Serving")
	return build(svc, port.Identity[M](), o)
}

// ServingFor mounts an already-built service behind an input type of its own.
func ServingFor[In, M any, ID comparable, U any](svc Service[M, ID, U], mapper Mapper[In, M], opts ...Option[M, ID, U]) *HandlerFor[M, ID, U, In] {
	o := collect(opts)
	o.refuseServiceOptions("crudnet.ServingFor")
	return build(svc, mapper, o)
}

// build is the one place a handler is assembled, so the four constructors
// cannot drift in what they wire.
func build[M any, ID comparable, U any, In any](svc Service[M, ID, U], mapper Mapper[In, M], o options[M, ID, U]) *HandlerFor[M, ID, U, In] {
	h := &HandlerFor[M, ID, U, In]{svc: svc, mapper: mapper, opt: o}
	if h.opt.errorHandler == nil {
		// After the options, not before: WithRenderer has to be able to reach
		// the handler the routes actually call, and a default installed first
		// would have closed over the wrong renderer.
		rd := h.opt.renderer
		if rd == nil {
			rd = rendererFor(port.Hops(svc, mapper))
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
func (h *HandlerFor[M, ID, U, In]) Mount(mux *http.ServeMux, prefix string) {
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

	if !h.opt.readOnly {
		for _, c := range collection {
			mux.HandleFunc("POST "+c, h.Create)
		}
		mux.HandleFunc("POST "+p+"/bulk-delete", h.BulkDelete)
	}
	mux.HandleFunc("POST "+p+"/query", h.Query)
	mux.HandleFunc("GET "+p+"/count", h.CountGet)
	mux.HandleFunc("POST "+p+"/count", h.CountPost)
	for _, c := range collection {
		mux.HandleFunc("GET "+c, h.List)
	}
	mux.HandleFunc("GET "+p+"/{id}", h.GetByID)
	if !h.opt.readOnly {
		mux.HandleFunc("PATCH "+p+"/{id}", h.Update)
		mux.HandleFunc("PUT "+p+"/{id}", h.Replace)
		mux.HandleFunc("DELETE "+p+"/{id}", h.Delete)
	}
}

// ---------------------------------------------------------------------------
// reads

// List answers GET / using the query-string DSL.
func (h *HandlerFor[M, ID, U, In]) List(w http.ResponseWriter, r *http.Request) {
	req, err := h.parseQueryString(r)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.list(w, r, req)
}

// Query answers POST /query using the full JSON DSL.
func (h *HandlerFor[M, ID, U, In]) Query(w http.ResponseWriter, r *http.Request) {
	req, err := h.parseBody(r)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.list(w, r, req)
}

func (h *HandlerFor[M, ID, U, In]) list(w http.ResponseWriter, r *http.Request, req *query.Request) {
	scope, err := h.scope(r)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	page, err := h.svc.List(r.Context(), port.ListCommand{Query: req, Options: scope})
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if h.opt.transform == nil {
		writeJSON(w, http.StatusOK, page)
		return
	}
	writeJSON(w, http.StatusOK, crud.MapPage(page, func(m M) any {
		return h.opt.transform(r, m)
	}))
}

// CountGet answers GET /count.
func (h *HandlerFor[M, ID, U, In]) CountGet(w http.ResponseWriter, r *http.Request) {
	req, err := h.parseQueryString(r)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.count(w, r, req)
}

// CountPost answers POST /count.
func (h *HandlerFor[M, ID, U, In]) CountPost(w http.ResponseWriter, r *http.Request) {
	req, err := h.parseBody(r)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.count(w, r, req)
}

func (h *HandlerFor[M, ID, U, In]) count(w http.ResponseWriter, r *http.Request, req *query.Request) {
	scope, err := h.scope(r)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	n, err := h.svc.Count(r.Context(), port.CountCommand{Query: req, Options: scope})
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"count": n})
}

// GetByID answers GET /{id}, honouring ?preload= and ?select=.
func (h *HandlerFor[M, ID, U, In]) GetByID(w http.ResponseWriter, r *http.Request) {
	id, err := h.id(r)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	req, err := h.parseQueryString(r)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	scope, err := h.scope(r)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	m, err := h.svc.Get(r.Context(), port.GetCommand[ID]{ID: id, Query: req, Options: scope})
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.entity(w, r, http.StatusOK, m)
}

// ---------------------------------------------------------------------------
// writes

// Create answers POST /. The body is decoded into this handler's input type and
// mapped onto the model; the service then clears a database-generated key and
// every `generated` column, so a client cannot pick its own id or forge a
// server-side timestamp.
func (h *HandlerFor[M, ID, U, In]) Create(w http.ResponseWriter, r *http.Request) {
	var in In
	raw, err := crudhttp.DecodeJSONKeep(r.Body, &in)
	r = keep(r, raw)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	m, err := h.mapper.Model(r.Context(), in)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	m, err = h.svc.Create(r.Context(), port.CreateCommand[M]{Model: m, Before: h.beforeSave(r)})
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.entity(w, r, http.StatusCreated, m)
}

// Update answers PATCH /{id} with the partial-update DTO.
func (h *HandlerFor[M, ID, U, In]) Update(w http.ResponseWriter, r *http.Request) {
	id, err := h.id(r)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	var dto U
	raw, err := crudhttp.DecodeJSONKeep(r.Body, &dto)
	r = keep(r, raw)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	m, err := h.svc.Update(r.Context(), port.UpdateCommand[ID, U]{ID: id, Patch: dto, Before: h.beforeUpdate(r, id)})
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.entity(w, r, http.StatusOK, m)
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
func (h *HandlerFor[M, ID, U, In]) Replace(w http.ResponseWriter, r *http.Request) {
	id, err := h.id(r)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	var in In
	raw, err := crudhttp.DecodeJSONKeep(r.Body, &in)
	r = keep(r, raw)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	m, err := h.mapper.Model(r.Context(), in)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	m, err = h.svc.Replace(r.Context(), port.ReplaceCommand[ID, M]{ID: id, Model: m, Before: h.beforeSave(r)})
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.entity(w, r, http.StatusOK, m)
}

// Delete answers DELETE /{id}.
func (h *HandlerFor[M, ID, U, In]) Delete(w http.ResponseWriter, r *http.Request) {
	id, err := h.id(r)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	n, err := h.svc.Delete(r.Context(), port.DeleteCommand[ID]{ID: id})
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": n})
}

// BulkDeleteRequest is the body of POST /bulk-delete.
type BulkDeleteRequest[ID comparable] = crudhttp.BulkDeleteRequest[ID]

// BulkDelete answers POST /bulk-delete.
func (h *HandlerFor[M, ID, U, In]) BulkDelete(w http.ResponseWriter, r *http.Request) {
	var req BulkDeleteRequest[ID]
	if err := crudhttp.DecodeJSON(r.Body, &req); err != nil {
		h.fail(w, r, err)
		return
	}
	if h.opt.maxBulk > 0 && len(req.IDs) > h.opt.maxBulk {
		h.fail(w, r, crudhttp.BadRequestAs(errs.CodeBadQuery, nil, "at most %d ids per request", h.opt.maxBulk))
		return
	}
	n, err := h.svc.DeleteMany(r.Context(), port.BulkDeleteCommand[ID]{IDs: req.IDs})
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": n})
}

// ---------------------------------------------------------------------------
// plumbing

// scope is the transport's own narrowing, handed to the service as options it
// appends after the query document compiles. Appended and not merged, because
// crud.Where ANDs ([[D-004]]).
func (h *HandlerFor[M, ID, U, In]) scope(r *http.Request) ([]crud.Option, error) {
	if h.opt.scope == nil {
		return nil, nil
	}
	return h.opt.scope(r)
}

// beforeSave binds the create-and-replace hook to this request, so the service
// can run it in the one place the order is documented: after the server-owned
// fields are cleared ([[UC-013]] guarantee 7).
func (h *HandlerFor[M, ID, U, In]) beforeSave(r *http.Request) func(*M) error {
	if h.opt.beforeSave == nil {
		return nil
	}
	return func(m *M) error { return h.opt.beforeSave(r, m) }
}

// beforeUpdate binds the PATCH hook to this request and its path id.
func (h *HandlerFor[M, ID, U, In]) beforeUpdate(r *http.Request, id ID) func(*U) error {
	if h.opt.beforeUpdate == nil {
		return nil
	}
	return func(dto *U) error { return h.opt.beforeUpdate(r, id, dto) }
}

// parseQueryString reads the raw query values. URL.Query() keeps every repeat,
// so a second `f=` is a second filter term rather than a silently narrower one.
func (h *HandlerFor[M, ID, U, In]) parseQueryString(r *http.Request) (*query.Request, error) {
	return query.ParseQuery(r.URL.Query())
}

func (h *HandlerFor[M, ID, U, In]) parseBody(r *http.Request) (*query.Request, error) {
	req := &query.Request{}
	if err := crudhttp.DecodeJSON(r.Body, req); err != nil {
		return nil, err
	}
	return req, nil
}

// id reads and converts the {id} path parameter.
func (h *HandlerFor[M, ID, U, In]) id(r *http.Request) (ID, error) {
	return port.CoerceID[ID](r.PathValue("id"))
}

func (h *HandlerFor[M, ID, U, In]) entity(w http.ResponseWriter, r *http.Request, status int, m M) {
	if h.opt.transform != nil {
		writeJSON(w, status, h.opt.transform(r, m))
		return
	}
	writeJSON(w, status, m)
}

func (h *HandlerFor[M, ID, U, In]) fail(w http.ResponseWriter, r *http.Request, err error) {
	h.opt.errorHandler(w, r, err)
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
