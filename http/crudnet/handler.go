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
package crudnet

import (
	"net/http"
	"strings"

	"github.com/shardit-io/ordo/crud"
	"github.com/shardit-io/ordo/http/crudhttp"
	"github.com/shardit-io/ordo/query"
)

// Repository is everything the handler needs. crud.Repo[M, ID, U] satisfies it,
// and so does specs.Repo and any struct that embeds either — which is how a
// service layer with extra checks takes the repository's place.
type Repository[M any, ID comparable, U any] = crudhttp.Repository[M, ID, U]

// Handler is the mounted API.
type Handler[M any, ID comparable, U any] struct {
	repo Repository[M, ID, U]
	meta *crud.Meta
	cfg  *query.Config
	opt  options[M, ID, U]
}

// New builds a handler. All three type parameters are inferred from the
// repository, so the call site carries no generics.
func New[M any, ID comparable, U any](repo Repository[M, ID, U], opts ...Option[M, ID, U]) *Handler[M, ID, U] {
	h := &Handler[M, ID, U]{repo: repo, meta: repo.Meta()}
	h.opt.errorHandler = DefaultErrorHandler
	for _, o := range opts {
		o(&h.opt)
	}
	h.cfg = h.opt.query
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
func (h *Handler[M, ID, U]) Mount(mux *http.ServeMux, prefix string) {
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
func (h *Handler[M, ID, U]) List(w http.ResponseWriter, r *http.Request) {
	req, err := h.parseQueryString(r)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.list(w, r, req)
}

// Query answers POST /query using the full JSON DSL.
func (h *Handler[M, ID, U]) Query(w http.ResponseWriter, r *http.Request) {
	req, err := h.parseBody(r)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.list(w, r, req)
}

func (h *Handler[M, ID, U]) list(w http.ResponseWriter, r *http.Request, req *query.Request) {
	opts, err := h.compile(r, req)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	page, err := h.repo.Get(r.Context(), opts...)
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
func (h *Handler[M, ID, U]) CountGet(w http.ResponseWriter, r *http.Request) {
	req, err := h.parseQueryString(r)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.count(w, r, req)
}

// CountPost answers POST /count.
func (h *Handler[M, ID, U]) CountPost(w http.ResponseWriter, r *http.Request) {
	req, err := h.parseBody(r)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.count(w, r, req)
}

func (h *Handler[M, ID, U]) count(w http.ResponseWriter, r *http.Request, req *query.Request) {
	crudhttp.NarrowForCount(req)

	opts, err := h.compile(r, req)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	n, err := h.repo.Count(r.Context(), opts...)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"count": n})
}

// GetByID answers GET /{id}, honouring ?preload= and ?select=.
func (h *Handler[M, ID, U]) GetByID(w http.ResponseWriter, r *http.Request) {
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
	crudhttp.NarrowForEntity(req)

	opts, err := h.compile(r, req)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	m, err := h.repo.GetByID(r.Context(), id, opts...)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.entity(w, r, http.StatusOK, m)
}

// ---------------------------------------------------------------------------
// writes

// Create answers POST /. The body is bound straight onto the model; a
// database-generated key and every `generated` column are cleared first, so a
// client cannot pick its own id or forge a server-side timestamp.
func (h *Handler[M, ID, U]) Create(w http.ResponseWriter, r *http.Request) {
	var m M
	if err := crudhttp.DecodeJSON(r.Body, &m); err != nil {
		h.fail(w, r, err)
		return
	}
	if err := h.sanitize(&m); err != nil {
		h.fail(w, r, err)
		return
	}
	if h.opt.beforeSave != nil {
		if err := h.opt.beforeSave(r, &m); err != nil {
			h.fail(w, r, err)
			return
		}
	}
	if err := h.repo.Save(r.Context(), &m); err != nil {
		h.fail(w, r, err)
		return
	}
	h.entity(w, r, http.StatusCreated, m)
}

// Update answers PATCH /{id} with the partial-update DTO.
func (h *Handler[M, ID, U]) Update(w http.ResponseWriter, r *http.Request) {
	id, err := h.id(r)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	var dto U
	if err := crudhttp.DecodeJSON(r.Body, &dto); err != nil {
		h.fail(w, r, err)
		return
	}
	if h.opt.beforeUpdate != nil {
		if err := h.opt.beforeUpdate(r, id, &dto); err != nil {
			h.fail(w, r, err)
			return
		}
	}
	m, err := h.repo.Update(r.Context(), id, dto)
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
func (h *Handler[M, ID, U]) Replace(w http.ResponseWriter, r *http.Request) {
	id, err := h.id(r)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	var m M
	if err := crudhttp.DecodeJSON(r.Body, &m); err != nil {
		h.fail(w, r, err)
		return
	}
	if h.meta.PK.Auto && !h.opt.allowClientID {
		if _, err := h.repo.GetByID(r.Context(), id); err != nil {
			h.fail(w, r, err)
			return
		}
	}
	if err := h.clearGenerated(&m); err != nil {
		h.fail(w, r, err)
		return
	}
	if err := h.meta.SetID(&m, id); err != nil {
		h.fail(w, r, crudhttp.BadRequest(err))
		return
	}
	if h.opt.beforeSave != nil {
		if err := h.opt.beforeSave(r, &m); err != nil {
			h.fail(w, r, err)
			return
		}
	}
	if err := h.repo.Save(r.Context(), &m); err != nil {
		h.fail(w, r, err)
		return
	}
	h.entity(w, r, http.StatusOK, m)
}

// Delete answers DELETE /{id}.
func (h *Handler[M, ID, U]) Delete(w http.ResponseWriter, r *http.Request) {
	id, err := h.id(r)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	n, err := h.repo.Delete(r.Context(), id)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if n == 0 {
		h.fail(w, r, crud.ErrNotFound)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": n})
}

// BulkDeleteRequest is the body of POST /bulk-delete.
type BulkDeleteRequest[ID comparable] = crudhttp.BulkDeleteRequest[ID]

// BulkDelete answers POST /bulk-delete.
func (h *Handler[M, ID, U]) BulkDelete(w http.ResponseWriter, r *http.Request) {
	var req BulkDeleteRequest[ID]
	if err := crudhttp.DecodeJSON(r.Body, &req); err != nil {
		h.fail(w, r, err)
		return
	}
	if len(req.IDs) == 0 {
		writeJSON(w, http.StatusOK, map[string]any{"deleted": 0})
		return
	}
	if h.opt.maxBulk > 0 && len(req.IDs) > h.opt.maxBulk {
		h.fail(w, r, crudhttp.BadRequestf("at most %d ids per request", h.opt.maxBulk))
		return
	}
	n, err := h.repo.Delete(r.Context(), req.IDs...)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": n})
}

// ---------------------------------------------------------------------------
// plumbing

func (h *Handler[M, ID, U]) compile(r *http.Request, req *query.Request) ([]crud.Option, error) {
	opts, err := req.Compile(h.meta, h.cfg)
	if err != nil {
		return nil, err
	}
	if h.opt.scope != nil {
		extra, err := h.opt.scope(r)
		if err != nil {
			return nil, err
		}
		opts = append(opts, extra...)
	}
	return opts, nil
}

// parseQueryString reads the raw query values. URL.Query() keeps every repeat,
// so a second `f=` is a second filter term rather than a silently narrower one.
func (h *Handler[M, ID, U]) parseQueryString(r *http.Request) (*query.Request, error) {
	return query.ParseQuery(r.URL.Query())
}

func (h *Handler[M, ID, U]) parseBody(r *http.Request) (*query.Request, error) {
	req := &query.Request{}
	if err := crudhttp.DecodeJSON(r.Body, req); err != nil {
		return nil, err
	}
	return req, nil
}

// id reads and converts the {id} path parameter.
func (h *Handler[M, ID, U]) id(r *http.Request) (ID, error) {
	return crudhttp.CoerceID[ID](r.PathValue("id"))
}

func (h *Handler[M, ID, U]) sanitize(m *M) error {
	return crudhttp.Sanitize(h.meta, m, h.opt.allowClientID)
}

func (h *Handler[M, ID, U]) clearGenerated(m *M) error {
	return crudhttp.ClearGenerated(h.meta, m)
}

func (h *Handler[M, ID, U]) entity(w http.ResponseWriter, r *http.Request, status int, m M) {
	if h.opt.transform != nil {
		writeJSON(w, status, h.opt.transform(r, m))
		return
	}
	writeJSON(w, status, m)
}

func (h *Handler[M, ID, U]) fail(w http.ResponseWriter, r *http.Request, err error) {
	h.opt.errorHandler(w, r, err)
}
