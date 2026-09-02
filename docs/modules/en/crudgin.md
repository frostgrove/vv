# crudgin — a full CRUD API on Gin

```go
import "github.com/frostgrove/vv/crud/http/crudgin"
```

```bash
go get github.com/frostgrove/vv/crud/http/crudgin
```

**Module:** its own — so a consumer on Fiber, Echo or `net/http` never takes Gin
as a dependency ([[D-033]]) · **Requires:** `github.com/gin-gonic/gin`

Ten routes, the full query DSL, pagination, preloads, the create/patch/replace
lifecycle and the error envelope, mounted on any `gin.IRouter`.

Every option name, every status code and every response shape is **identical** to
[crudfiber](crudfiber.md) and [crudnet](crudnet.md). What differs is mounting,
which body encodings are accepted, and what the router does with a trailing slash
or a method it does not have.

---

## Mount it

```go
articles := specs.Executor(Articles.Bind(db, security.Gate(policy)))

r := gin.Default()
crudgin.New(articles).Mount(r, "/articles")

r.Run(":8080")
```

`Mount(r gin.IRouter, prefix string)` is the one-liner. `Register(r gin.IRoutes)`
takes an engine or a group that already exists. Gin has no mountable
sub-application, so there is no counterpart to Fiber's `Routes()`.

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

Every route is also a `gin.HandlerFunc` method, so you can register them one at a
time.

## The six constructors

```go
crudgin.New(repo, opts...)                  // a repository; the model is the wire shape
crudgin.NewFor(repo, mapper, opts...)       // …with a request body of its own
crudgin.Serving(svc, opts...)               // a port.Service — your business rules
crudgin.ServingFor(svc, mapper, opts...)    // both
```

`NewWire` and `ServingWire` are the explicit form under the four above. They
take the create mapper, a `wire.PatchMapper` and a `wire.Presenter`, so the
public PATCH body and the answer body are types of their own rather than the
persistence DTO and the model ([[D-105]]):

```go
crudgin.ServingWire(svc, ArticleInputMapper{}, ArticlePatchMapper{}, ArticlePresenter{})
```

The four short constructors fill in `wire.IdentityPatch` and
`wire.IdentityPresenter`, which is why nothing about them changed. See
[wire](wire.md).

`New` takes an **interface** ([[D-022]]), so your own service type satisfies it:

```go
type articleService struct {
    specs.Repo[Article, int64, ArticleUpdate]
}

func (s articleService) Save(ctx context.Context, a *Article) (Article, error) {
    if err := s.checkQuota(ctx, a); err != nil { return Article{}, err }
    return s.Repo.Save(ctx, a)
}

crudgin.New(articleService{…}).Mount(r, "/articles")
```

`crudgin.Repository` and `crudgin.Service` are **aliases** for the `port` types,
so the same value mounts on Fiber, `net/http` and gRPC unchanged.

## Options

| Option | Does |
|---|---|
| `WithQuery(cfg)` | bound the DSL — [`query.Config`](query.md#bounding-it) |
| `WithScope(fn)` | `func(*gin.Context) ([]crud.Option, error)` |
| `WithTransform(fn)` | a presenter: `func(*gin.Context, M) any` |
| `BeforeSave(fn)` | `func(*gin.Context, *M) error`, on create and replace |
| `BeforeUpdate(fn)` | `func(*gin.Context, ID, *U) error` |
| `ReadOnly()` | register the reads and nothing else |
| `AllowClientID()` | let a create choose its own database-generated key |
| `MaxBulk(n)` | cap `POST /bulk-delete` — the default is `port.DefaultMaxBulk` (1024); there is no "unlimited" |
| `MaxBody(n)` | cap the request body this handler reads, in bytes; the default is 4 MiB and a body past it is 413 ([[D-063]]) |
| `WithRenderer(r)` | replace the envelope |
| `WithErrorHandler(fn)` | `func(*gin.Context, error)` |

Every option below takes the resource's three type parameters explicitly —
`WithQuery[Article, int64, ArticleUpdate](cfg)`. `New` infers them from the
repository it is given; an option is a value built before `New` is called, and Go
infers a function's type arguments from its own arguments, which name none of
them. A local helper per resource is the usual way to keep call sites short.

**`WithScope` is reads only, and that is not a gap waiting to be filled.** `Save`
and `Delete` take no options, so there is nowhere for a per-request predicate to
go. The asymmetry looks like protection and is not: with a scope of
`TenantID = 7`, `GET /{id}` on somebody else's row is 404 while `DELETE /{id}` on
the same row answers 200. Row-level rules on writes belong in
[`security.Gate`](security.md), whose scope really does reach the DELETE and the
UPDATE.

## Errors

```go
r.Use(crudgin.Errors(crudhttp.WithMessages(catalogue)))
```

It renders whatever a handler failed with, recovers a panic into a silent 500,
and leaves alone a handler that already wrote a response. It also calls
`c.Error(err)`, so the cause reaches Gin's own logging middleware.

`crudgin.DefaultErrorHandler` is the zero-config one, and `crudgin.Status(err)`
is exported if you render your own bodies.

Statuses map by sentinel: `crud.ErrNotFound` → 404, `crud.ErrForbidden` → 403,
`crud.ErrConflict` → 409, query and schema errors → 400 with the offending path
named, everything else → 500 **with no detail**.

With the [error subsystem](errs.md) wired in, a 409 or a 422 also carries
`error_code` and `field` — see [crudhttp](crudhttp.md#the-envelope).

## Gin-specific behaviour

| | |
|---|---|
| request bodies | **JSON only** |
| `/x` vs `/x/` | `/x` matches; `/x/` gets a **301** from Gin's `RedirectTrailingSlash` |
| an unmounted verb | **404** — set `Engine.HandleMethodNotAllowed` for 405 |
| an unclaimed path | 404 |
| query-string repeats | read from `c.Request.URL.Query()`, so `?f=a&f=b` keeps both — `c.Query` would collapse them |

Three details worth knowing:

- **The collection route is registered as `""`, not `"/"`.** On a group of
  `/articles` the `"/"` form produces `/articles/`, which does not match
  `GET /articles`. Registering both is not an option — on the engine itself they
  collapse and Gin panics with *handlers are already registered*.
- **`c.ShouldBindJSON` is deliberately not used.** Gin's binder runs
  `validator/v10` over `binding:"…"` tags, so a tag on your model would change
  what the CRUD routes accept under **one** transport and not the others. This
  binding decodes with `encoding/json` ([[D-045]]).
- **`HandleMethodNotAllowed` is the application's setting**, and this handler
  does not touch it.

## What create and replace refuse

On create the body binds onto the model, then a database-generated key and every
`generated` column are cleared — a client cannot choose its own id or forge a
server-side timestamp. `PUT /:id` replaces and **never creates** where the
database owns the key ([[D-012]]). `AllowClientID()` hands it over.

## Routing — the router's own refusals

| | |
|---|---|
| `Routing(engine, opts…)` | installs `NoRoute` and `NoMethod`, and turns `HandleMethodNotAllowed` on |

A path nothing claimed and a verb a route does not have are answered by Gin
itself, before any handler or middleware of this library runs: a bare 404 with no
body, which a client that parses one shape for every failure has nothing to
parse. Worse, an application that maps unknown errors to 500 turns "you asked for
something that is not there" into "this service is broken", which a client
retries.

`Routing` renders both in the same envelope, with a code a client can branch on:
`not_found` and `method_not_allowed`. `HandleMethodNotAllowed` is turned on
because without it Gin answers a known path with an unknown verb as 404 — and the
two are different statements to a client.

Call it once, on the engine, after the routes are mounted.

## See also

- [crudfiber](crudfiber.md) · [crudnet](crudnet.md) · [crudgrpc](crudgrpc.md) — the same API elsewhere
- [crudhttp](crudhttp.md) — the status table, the envelope, the renderer seam
- [port](port.md) — business rules between the handler and the repository
- [`_examples/sqlx-pgx-gin`](../../../_examples/sqlx-pgx-gin/) · [`_examples/gorm-mysql-gin`](../../../_examples/gorm-mysql-gin/) · [`_examples/ent-pgx-gin`](../../../_examples/ent-pgx-gin/)
- [[FL-013]] a request through another binding · [[D-034]] a transport binding is a shell over crudhttp
