# crudfiber — a full CRUD API on Fiber v3

```go
import "github.com/shardit-io/vv/http/crudfiber"
```

```bash
go get github.com/shardit-io/vv/http/crudfiber
```

**Module:** its own — so a consumer on Gin, Echo or `net/http` never takes Fiber
as a dependency ([[D-033]]) · **Requires:** `github.com/gofiber/fiber/v3`

Ten routes, the full query DSL, pagination, preloads, the create/patch/replace
lifecycle and the error envelope, mounted as a Fiber sub-application.

Every option name, every status code and every response shape is **identical** to
[crudgin](crudgin.md) and [crudnet](crudnet.md). What differs is mounting, which
body encodings are accepted, and what the router does with a path it does not
have.

---

## Mount it

```go
articles := specs.Executor(Articles.Bind(db, security.Gate(policy)))

app := fiber.New()
app.Use("/articles", crudfiber.New(articles).Routes())

app.Listen(":8080")
```

`Routes()` returns a `*fiber.App` — the mountable sub-application Fiber has and
the other bindings do not. `Register(r fiber.Router)` registers onto a router or
group you already have.

## The routes

| Route | Does |
|---|---|
| `GET /` | list, query-string DSL |
| `POST /query` | list, full JSON DSL |
| `GET /count` · `POST /count` | count, same DSL |
| `GET /:id` | one entity, `?preload=…&select=…` |
| `POST /` | create |
| `PATCH /:id` | partial update through the DTO |
| `PUT /:id` | replace; where the database owns the key it will not create |
| `DELETE /:id` | delete one |
| `POST /bulk-delete` | `{"ids": […]}` |

Every route is also a method — `List`, `Query`, `CountGet`, `CountPost`,
`GetByID`, `Create`, `Update`, `Replace`, `Delete`, `BulkDelete` — so you can
register them one at a time.

## The four constructors

```go
crudfiber.New(repo, opts...)                  // a repository; the model is the wire shape
crudfiber.NewFor(repo, mapper, opts...)       // …with a request body of its own
crudfiber.Serving(svc, opts...)               // a port.Service — your business rules
crudfiber.ServingFor(svc, mapper, opts...)    // both
```

`New` takes an **interface** ([[D-022]]), so your own service type satisfies it:

```go
type articleService struct {
    specs.Repo[Article, int64, ArticleUpdate]
}

func (s articleService) Save(ctx context.Context, a *Article) error {
    if err := s.checkQuota(ctx, a); err != nil { return err }
    return s.Repo.Save(ctx, a)
}

app.Use("/articles", crudfiber.New(articleService{…}).Routes())
```

`crudfiber.Repository` and `crudfiber.Service` are **aliases** for the `port`
types, so the same value mounts on Gin, `net/http` and gRPC unchanged.

## Options

| Option | Does |
|---|---|
| `WithQuery(cfg)` | bound the DSL — [`query.Config`](query.md#bounding-it) |
| `WithScope(fn)` | `func(fiber.Ctx) ([]crud.Option, error)` |
| `WithTransform(fn)` | a presenter: `func(fiber.Ctx, M) any` |
| `BeforeSave(fn)` | `func(fiber.Ctx, *M) error`, on create and replace |
| `BeforeUpdate(fn)` | `func(fiber.Ctx, ID, *U) error` |
| `ReadOnly()` | register the reads and nothing else |
| `AllowClientID()` | let a create choose its own database-generated key |
| `MaxBulk(n)` | cap `POST /bulk-delete` |
| `WithRenderer(r)` | replace the envelope |
| `WithErrorHandler(fn)` | `func(fiber.Ctx, error) error` |

```go
crudfiber.WithQuery[Article, int64, ArticleUpdate](&query.Config{
    Filterable: []string{"Title", "Views", "Author.*"},
    Sortable:   []string{"CreatedAt", "Views"},
})
```

## Errors

Two ways in, because Fiber has both:

```go
app := fiber.New(fiber.Config{ErrorHandler: crudfiber.ErrorHandler(
    crudhttp.WithMessages(catalogue),
)})
```

```go
app.Use(crudfiber.Errors(crudhttp.WithMessages(catalogue)))
```

`ErrorHandler` is Fiber's own seam and covers everything the app serves.
`Errors` is the middleware form. `crudfiber.DefaultErrorHandler` is the
zero-config one, and `crudfiber.Status(err)` is exported if you render your own
bodies.

Statuses map by sentinel: `crud.ErrNotFound` → 404, `crud.ErrForbidden` → 403,
`crud.ErrConflict` → 409, query and schema errors → 400 with the offending path
named, everything else → 500 **with no detail**.

With the [error subsystem](errs.md) wired in, a 409 or a 422 also carries
`error_code` and `field` — see [crudhttp](crudhttp.md#the-envelope).

## Fiber-specific behaviour

| | |
|---|---|
| request bodies | **JSON, XML and form** — Fiber's binder dispatches on Content-Type. The other bindings are JSON only |
| `/x` vs `/x/` | both match |
| an unmounted verb | 405 |
| an unclaimed path | 404 |
| query-string repeats | read through `QueryArgs().VisitAll`, so `?f=a&f=b` keeps both — `c.Query` would collapse them |

**One thing to know about the raw-body fallback.** Fiber's `c.Body()` is
documented valid only within the handler, so this binding copies with
`crudhttp.KeepBody` before retaining it. That is what lets an error body name the
key the client sent on a hand-written endpoint.

## What create and replace refuse

On create the body binds onto the model, then a database-generated key and every
`generated` column are cleared — a client cannot choose its own id or forge a
server-side timestamp. `PUT /:id` replaces and **never creates** where the
database owns the key ([[D-012]]). `AllowClientID()` hands it over.

## See also

- [crudgin](crudgin.md) · [crudnet](crudnet.md) · [crudgrpc](crudgrpc.md) — the same API elsewhere
- [crudhttp](crudhttp.md) — the status table, the envelope, the renderer seam
- [port](port.md) — business rules between the handler and the repository
- [`_examples/pgx-fiber`](../../../_examples/pgx-fiber/) · [`_examples/gorm-pgx-fiber`](../../../_examples/gorm-pgx-fiber/) · [`_examples/ent-pgx-fiber`](../../../_examples/ent-pgx-fiber/)
- [[FL-013]] a request through another binding · [[D-034]] a transport binding is a shell over crudhttp
