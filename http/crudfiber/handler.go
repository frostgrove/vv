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
package crudfiber

import (
	"net/url"

	"github.com/gofiber/fiber/v3"

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

// Routes returns a standalone app to mount with app.Use("/prefix", …).
func (h *Handler[M, ID, U]) Routes() *fiber.App {
	app := fiber.New()
	h.Register(app)
	return app
}

// Register mounts the routes on an existing router or group. `/count` is
// registered before `/:id` so it is not swallowed by the parameter route.
func (h *Handler[M, ID, U]) Register(r fiber.Router) {
	if !h.opt.readOnly {
		r.Post("/", h.Create)
		r.Post("/bulk-delete", h.BulkDelete)
	}
	r.Post("/query", h.Query)
	r.Get("/count", h.CountGet)
	r.Post("/count", h.CountPost)
	r.Get("/", h.List)
	r.Get("/:id", h.GetByID)
	if !h.opt.readOnly {
		r.Patch("/:id", h.Update)
		r.Put("/:id", h.Replace)
		r.Delete("/:id", h.Delete)
	}
}

// ---------------------------------------------------------------------------
// reads

// List answers GET / using the query-string DSL.
func (h *Handler[M, ID, U]) List(c fiber.Ctx) error {
	req, err := h.parseQueryString(c)
	if err != nil {
		return h.fail(c, err)
	}
	return h.list(c, req)
}

// Query answers POST /query using the full JSON DSL.
func (h *Handler[M, ID, U]) Query(c fiber.Ctx) error {
	req, err := h.parseBody(c)
	if err != nil {
		return h.fail(c, err)
	}
	return h.list(c, req)
}

func (h *Handler[M, ID, U]) list(c fiber.Ctx, req *query.Request) error {
	opts, err := h.compile(c, req)
	if err != nil {
		return h.fail(c, err)
	}
	page, err := h.repo.Get(c.Context(), opts...)
	if err != nil {
		return h.fail(c, err)
	}
	if h.opt.transform == nil {
		return c.Status(fiber.StatusOK).JSON(page)
	}
	return c.Status(fiber.StatusOK).JSON(crud.MapPage(page, func(m M) any {
		return h.opt.transform(c, m)
	}))
}

// CountGet answers GET /count.
func (h *Handler[M, ID, U]) CountGet(c fiber.Ctx) error {
	req, err := h.parseQueryString(c)
	if err != nil {
		return h.fail(c, err)
	}
	return h.count(c, req)
}

// CountPost answers POST /count.
func (h *Handler[M, ID, U]) CountPost(c fiber.Ctx) error {
	req, err := h.parseBody(c)
	if err != nil {
		return h.fail(c, err)
	}
	return h.count(c, req)
}

func (h *Handler[M, ID, U]) count(c fiber.Ctx, req *query.Request) error {
	crudhttp.NarrowForCount(req)

	opts, err := h.compile(c, req)
	if err != nil {
		return h.fail(c, err)
	}
	n, err := h.repo.Count(c.Context(), opts...)
	if err != nil {
		return h.fail(c, err)
	}
	return c.Status(fiber.StatusOK).JSON(fiber.Map{"count": n})
}

// GetByID answers GET /:id, honouring ?preload= and ?select=.
func (h *Handler[M, ID, U]) GetByID(c fiber.Ctx) error {
	id, err := h.id(c)
	if err != nil {
		return h.fail(c, err)
	}
	req, err := h.parseQueryString(c)
	if err != nil {
		return h.fail(c, err)
	}
	crudhttp.NarrowForEntity(req)

	opts, err := h.compile(c, req)
	if err != nil {
		return h.fail(c, err)
	}
	m, err := h.repo.GetByID(c.Context(), id, opts...)
	if err != nil {
		return h.fail(c, err)
	}
	return h.entity(c, fiber.StatusOK, m)
}

// ---------------------------------------------------------------------------
// writes

// Create answers POST /. The body is bound straight onto the model; a
// database-generated key and every `generated` column are cleared first, so a
// client cannot pick its own id or forge a server-side timestamp.
func (h *Handler[M, ID, U]) Create(c fiber.Ctx) error {
	var m M
	if err := c.Bind().Body(&m); err != nil {
		return h.fail(c, crudhttp.BadRequest(err))
	}
	if err := h.sanitize(&m); err != nil {
		return h.fail(c, err)
	}
	if h.opt.beforeSave != nil {
		if err := h.opt.beforeSave(c, &m); err != nil {
			return h.fail(c, err)
		}
	}
	if err := h.repo.Save(c.Context(), &m); err != nil {
		return h.fail(c, err)
	}
	return h.entity(c, fiber.StatusCreated, m)
}

// Update answers PATCH /:id with the partial-update DTO.
func (h *Handler[M, ID, U]) Update(c fiber.Ctx) error {
	id, err := h.id(c)
	if err != nil {
		return h.fail(c, err)
	}
	var dto U
	if err := c.Bind().Body(&dto); err != nil {
		return h.fail(c, crudhttp.BadRequest(err))
	}
	if h.opt.beforeUpdate != nil {
		if err := h.opt.beforeUpdate(c, id, &dto); err != nil {
			return h.fail(c, err)
		}
	}
	m, err := h.repo.Update(c.Context(), id, dto)
	if err != nil {
		return h.fail(c, err)
	}
	return h.entity(c, fiber.StatusOK, m)
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
func (h *Handler[M, ID, U]) Replace(c fiber.Ctx) error {
	id, err := h.id(c)
	if err != nil {
		return h.fail(c, err)
	}
	var m M
	if err := c.Bind().Body(&m); err != nil {
		return h.fail(c, crudhttp.BadRequest(err))
	}
	if h.meta.PK.Auto && !h.opt.allowClientID {
		if _, err := h.repo.GetByID(c.Context(), id); err != nil {
			return h.fail(c, err)
		}
	}
	if err := h.clearGenerated(&m); err != nil {
		return h.fail(c, err)
	}
	if err := h.meta.SetID(&m, id); err != nil {
		return h.fail(c, crudhttp.BadRequest(err))
	}
	if h.opt.beforeSave != nil {
		if err := h.opt.beforeSave(c, &m); err != nil {
			return h.fail(c, err)
		}
	}
	if err := h.repo.Save(c.Context(), &m); err != nil {
		return h.fail(c, err)
	}
	return h.entity(c, fiber.StatusOK, m)
}

// Delete answers DELETE /:id.
func (h *Handler[M, ID, U]) Delete(c fiber.Ctx) error {
	id, err := h.id(c)
	if err != nil {
		return h.fail(c, err)
	}
	n, err := h.repo.Delete(c.Context(), id)
	if err != nil {
		return h.fail(c, err)
	}
	if n == 0 {
		return h.fail(c, crud.ErrNotFound)
	}
	return c.Status(fiber.StatusOK).JSON(fiber.Map{"deleted": n})
}

// BulkDeleteRequest is the body of POST /bulk-delete.
type BulkDeleteRequest[ID comparable] = crudhttp.BulkDeleteRequest[ID]

// BulkDelete answers POST /bulk-delete.
func (h *Handler[M, ID, U]) BulkDelete(c fiber.Ctx) error {
	var req BulkDeleteRequest[ID]
	if err := c.Bind().Body(&req); err != nil {
		return h.fail(c, crudhttp.BadRequest(err))
	}
	if len(req.IDs) == 0 {
		return c.Status(fiber.StatusOK).JSON(fiber.Map{"deleted": 0})
	}
	if h.opt.maxBulk > 0 && len(req.IDs) > h.opt.maxBulk {
		return h.fail(c, crudhttp.BadRequestf("at most %d ids per request", h.opt.maxBulk))
	}
	n, err := h.repo.Delete(c.Context(), req.IDs...)
	if err != nil {
		return h.fail(c, err)
	}
	return c.Status(fiber.StatusOK).JSON(fiber.Map{"deleted": n})
}

// ---------------------------------------------------------------------------
// plumbing

func (h *Handler[M, ID, U]) compile(c fiber.Ctx, req *query.Request) ([]crud.Option, error) {
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

func (h *Handler[M, ID, U]) parseQueryString(c fiber.Ctx) (*query.Request, error) {
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

func (h *Handler[M, ID, U]) parseBody(c fiber.Ctx) (*query.Request, error) {
	req := &query.Request{}
	if len(c.Body()) == 0 {
		return req, nil
	}
	if err := c.Bind().Body(req); err != nil {
		return nil, crudhttp.BadRequest(err)
	}
	return req, nil
}

// id reads and converts the :id path parameter.
func (h *Handler[M, ID, U]) id(c fiber.Ctx) (ID, error) {
	return crudhttp.CoerceID[ID](c.Params("id"))
}

func (h *Handler[M, ID, U]) sanitize(m *M) error {
	return crudhttp.Sanitize(h.meta, m, h.opt.allowClientID)
}

func (h *Handler[M, ID, U]) clearGenerated(m *M) error {
	return crudhttp.ClearGenerated(h.meta, m)
}

func (h *Handler[M, ID, U]) entity(c fiber.Ctx, status int, m M) error {
	if h.opt.transform != nil {
		return c.Status(status).JSON(h.opt.transform(c, m))
	}
	return c.Status(status).JSON(m)
}

func (h *Handler[M, ID, U]) fail(c fiber.Ctx, err error) error {
	return h.opt.errorHandler(c, err)
}
