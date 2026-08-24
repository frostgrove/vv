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
package crudgin

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/shardit-io/qq/crud"
	"github.com/shardit-io/qq/http/crudhttp"
	"github.com/shardit-io/qq/query"
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

// Mount groups the routes under a prefix. Gin has no mountable sub-application,
// so this is the counterpart of Fiber's app.Use("/prefix", …).
func (h *Handler[M, ID, U]) Mount(r gin.IRouter, prefix string) {
	h.Register(r.Group(prefix))
}

// Register mounts the routes on an existing router or group.
//
// The collection routes are registered as "" rather than "/": on a group of
// /widgets the latter produces /widgets/, which does not match GET /widgets.
// Registering both forms is not an option — on the engine itself they collapse
// to the same path and Gin panics. The trailing-slash form is left to Gin's own
// RedirectTrailingSlash, which is on by default.
func (h *Handler[M, ID, U]) Register(r gin.IRoutes) {
	if !h.opt.readOnly {
		r.POST("", h.Create)
		r.POST("/bulk-delete", h.BulkDelete)
	}
	r.POST("/query", h.Query)
	r.GET("/count", h.CountGet)
	r.POST("/count", h.CountPost)
	r.GET("", h.List)
	r.GET("/:id", h.GetByID)
	if !h.opt.readOnly {
		r.PATCH("/:id", h.Update)
		r.PUT("/:id", h.Replace)
		r.DELETE("/:id", h.Delete)
	}
}

// ---------------------------------------------------------------------------
// reads

// List answers GET / using the query-string DSL.
func (h *Handler[M, ID, U]) List(c *gin.Context) {
	req, err := h.parseQueryString(c)
	if err != nil {
		h.fail(c, err)
		return
	}
	h.list(c, req)
}

// Query answers POST /query using the full JSON DSL.
func (h *Handler[M, ID, U]) Query(c *gin.Context) {
	req, err := h.parseBody(c)
	if err != nil {
		h.fail(c, err)
		return
	}
	h.list(c, req)
}

func (h *Handler[M, ID, U]) list(c *gin.Context, req *query.Request) {
	opts, err := h.compile(c, req)
	if err != nil {
		h.fail(c, err)
		return
	}
	page, err := h.repo.Get(c.Request.Context(), opts...)
	if err != nil {
		h.fail(c, err)
		return
	}
	if h.opt.transform == nil {
		c.JSON(http.StatusOK, page)
		return
	}
	c.JSON(http.StatusOK, crud.MapPage(page, func(m M) any {
		return h.opt.transform(c, m)
	}))
}

// CountGet answers GET /count.
func (h *Handler[M, ID, U]) CountGet(c *gin.Context) {
	req, err := h.parseQueryString(c)
	if err != nil {
		h.fail(c, err)
		return
	}
	h.count(c, req)
}

// CountPost answers POST /count.
func (h *Handler[M, ID, U]) CountPost(c *gin.Context) {
	req, err := h.parseBody(c)
	if err != nil {
		h.fail(c, err)
		return
	}
	h.count(c, req)
}

func (h *Handler[M, ID, U]) count(c *gin.Context, req *query.Request) {
	crudhttp.NarrowForCount(req)

	opts, err := h.compile(c, req)
	if err != nil {
		h.fail(c, err)
		return
	}
	n, err := h.repo.Count(c.Request.Context(), opts...)
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"count": n})
}

// GetByID answers GET /:id, honouring ?preload= and ?select=.
func (h *Handler[M, ID, U]) GetByID(c *gin.Context) {
	id, err := h.id(c)
	if err != nil {
		h.fail(c, err)
		return
	}
	req, err := h.parseQueryString(c)
	if err != nil {
		h.fail(c, err)
		return
	}
	crudhttp.NarrowForEntity(req)

	opts, err := h.compile(c, req)
	if err != nil {
		h.fail(c, err)
		return
	}
	m, err := h.repo.GetByID(c.Request.Context(), id, opts...)
	if err != nil {
		h.fail(c, err)
		return
	}
	h.entity(c, http.StatusOK, m)
}

// ---------------------------------------------------------------------------
// writes

// Create answers POST /. The body is bound straight onto the model; a
// database-generated key and every `generated` column are cleared first, so a
// client cannot pick its own id or forge a server-side timestamp.
func (h *Handler[M, ID, U]) Create(c *gin.Context) {
	var m M
	if err := crudhttp.DecodeJSON(c.Request.Body, &m); err != nil {
		h.fail(c, err)
		return
	}
	if err := h.sanitize(&m); err != nil {
		h.fail(c, err)
		return
	}
	if h.opt.beforeSave != nil {
		if err := h.opt.beforeSave(c, &m); err != nil {
			h.fail(c, err)
			return
		}
	}
	if err := h.repo.Save(c.Request.Context(), &m); err != nil {
		h.fail(c, err)
		return
	}
	h.entity(c, http.StatusCreated, m)
}

// Update answers PATCH /:id with the partial-update DTO.
func (h *Handler[M, ID, U]) Update(c *gin.Context) {
	id, err := h.id(c)
	if err != nil {
		h.fail(c, err)
		return
	}
	var dto U
	if err := crudhttp.DecodeJSON(c.Request.Body, &dto); err != nil {
		h.fail(c, err)
		return
	}
	if h.opt.beforeUpdate != nil {
		if err := h.opt.beforeUpdate(c, id, &dto); err != nil {
			h.fail(c, err)
			return
		}
	}
	m, err := h.repo.Update(c.Request.Context(), id, dto)
	if err != nil {
		h.fail(c, err)
		return
	}
	h.entity(c, http.StatusOK, m)
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
func (h *Handler[M, ID, U]) Replace(c *gin.Context) {
	id, err := h.id(c)
	if err != nil {
		h.fail(c, err)
		return
	}
	var m M
	if err := crudhttp.DecodeJSON(c.Request.Body, &m); err != nil {
		h.fail(c, err)
		return
	}
	if h.meta.PK.Auto && !h.opt.allowClientID {
		if _, err := h.repo.GetByID(c.Request.Context(), id); err != nil {
			h.fail(c, err)
			return
		}
	}
	if err := h.clearGenerated(&m); err != nil {
		h.fail(c, err)
		return
	}
	if err := h.meta.SetID(&m, id); err != nil {
		h.fail(c, crudhttp.BadRequest(err))
		return
	}
	if h.opt.beforeSave != nil {
		if err := h.opt.beforeSave(c, &m); err != nil {
			h.fail(c, err)
			return
		}
	}
	if err := h.repo.Save(c.Request.Context(), &m); err != nil {
		h.fail(c, err)
		return
	}
	h.entity(c, http.StatusOK, m)
}

// Delete answers DELETE /:id.
func (h *Handler[M, ID, U]) Delete(c *gin.Context) {
	id, err := h.id(c)
	if err != nil {
		h.fail(c, err)
		return
	}
	n, err := h.repo.Delete(c.Request.Context(), id)
	if err != nil {
		h.fail(c, err)
		return
	}
	if n == 0 {
		h.fail(c, crud.ErrNotFound)
		return
	}
	c.JSON(http.StatusOK, gin.H{"deleted": n})
}

// BulkDeleteRequest is the body of POST /bulk-delete.
type BulkDeleteRequest[ID comparable] = crudhttp.BulkDeleteRequest[ID]

// BulkDelete answers POST /bulk-delete.
func (h *Handler[M, ID, U]) BulkDelete(c *gin.Context) {
	var req BulkDeleteRequest[ID]
	if err := crudhttp.DecodeJSON(c.Request.Body, &req); err != nil {
		h.fail(c, err)
		return
	}
	if len(req.IDs) == 0 {
		c.JSON(http.StatusOK, gin.H{"deleted": 0})
		return
	}
	if h.opt.maxBulk > 0 && len(req.IDs) > h.opt.maxBulk {
		h.fail(c, crudhttp.BadRequestf("at most %d ids per request", h.opt.maxBulk))
		return
	}
	n, err := h.repo.Delete(c.Request.Context(), req.IDs...)
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"deleted": n})
}

// ---------------------------------------------------------------------------
// plumbing

func (h *Handler[M, ID, U]) compile(c *gin.Context, req *query.Request) ([]crud.Option, error) {
	opts, err := req.Compile(h.meta, h.cfg)
	if err != nil {
		return nil, err
	}
	if h.opt.scope != nil {
		extra, err := h.opt.scope(c)
		if err != nil {
			return nil, err
		}
		opts = append(opts, extra...)
	}
	return opts, nil
}

// parseQueryString reads the raw query args. Gin's Query() returns the last
// value for a key, which would quietly drop the second `f=` filter; URL.Query()
// keeps every repeat, and the filter is the AND of all of them.
func (h *Handler[M, ID, U]) parseQueryString(c *gin.Context) (*query.Request, error) {
	return query.ParseQuery(c.Request.URL.Query())
}

func (h *Handler[M, ID, U]) parseBody(c *gin.Context) (*query.Request, error) {
	req := &query.Request{}
	if err := crudhttp.DecodeJSON(c.Request.Body, req); err != nil {
		return nil, err
	}
	return req, nil
}

// id reads and converts the :id path parameter.
func (h *Handler[M, ID, U]) id(c *gin.Context) (ID, error) {
	return crudhttp.CoerceID[ID](c.Param("id"))
}

func (h *Handler[M, ID, U]) sanitize(m *M) error {
	return crudhttp.Sanitize(h.meta, m, h.opt.allowClientID)
}

func (h *Handler[M, ID, U]) clearGenerated(m *M) error {
	return crudhttp.ClearGenerated(h.meta, m)
}

func (h *Handler[M, ID, U]) entity(c *gin.Context, status int, m M) {
	if h.opt.transform != nil {
		c.JSON(status, h.opt.transform(c, m))
		return
	}
	c.JSON(status, m)
}

func (h *Handler[M, ID, U]) fail(c *gin.Context, err error) {
	h.opt.errorHandler(c, err)
}
