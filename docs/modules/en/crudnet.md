# crudnet — a full CRUD API on net/http

```go
import "github.com/frostgrove/vv/crud/http/crudnet"
```

**Module:** root — it imports only the standard library, so it costs nothing
· **Depends on:** `crud`, `query`, `errs`, `port`, `crudhttp`, `net/http`

Ten routes, the full query DSL, pagination, preloads, the create/patch/replace
lifecycle and the error envelope — on `net/http`, with no dependency at all.

On `net/http` over `database/sql`, `go get github.com/frostgrove/vv` is the whole
installation.

---

## Mount it

```go
articles := specs.Executor(Articles.Bind(db, security.Gate(policy)))

mux := http.NewServeMux()
crudnet.New(articles).Mount(mux, "/articles")

http.ListenAndServe(":8080", crudnet.Errors()(mux))
```

```http
POST /articles/query
{
  "filter": { "views": {"gte": 100}, "tags.slug": {"in": ["go","rust"]} },
  "preload": ["author", "comments.author"],
  "sort": ["-views", "author.name"],
  "page": 2, "limit": 20
}
```

## The routes

| Route | Does |
|---|---|
| `GET /` | list, query-string DSL |
| `POST /query` | list, full JSON DSL |
| `GET /count` · `POST /count` | count, same DSL |
| `GET /{id}` | one entity, `?preload=…&select=…` |
| `POST /` | create |
| `PATCH /{id}` | partial update through the DTO |
| `PUT /{id}` | replace; where the database owns the key it will not create |
| `DELETE /{id}` | delete one |
| `POST /bulk-delete` | `{"ids": […]}` |

**Every route method is an ordinary `http.HandlerFunc`**, so chi, gorilla/mux and
httprouter can register them one by one instead of calling `Mount`. That is worth
knowing even if your router is not `ServeMux`.

## The six constructors

```go
crudnet.New(repo, opts...)                  // a repository; the model is the wire shape
crudnet.NewFor(repo, mapper, opts...)       // …with a request body of its own
crudnet.Serving(svc, opts...)               // a port.Service — your business rules
crudnet.ServingFor(svc, mapper, opts...)    // both
```

`NewWire` and `ServingWire` are the explicit form under the four above. They
take the create mapper, a `wire.PatchMapper` and a `wire.Presenter`, so the
public PATCH body and the answer body are types of their own rather than the
persistence DTO and the model ([[D-105]]):

```go
crudnet.ServingWire(svc, ArticleInputMapper{}, ArticlePatchMapper{}, ArticlePresenter{})
```

The four short constructors fill in `wire.IdentityPatch` and
`wire.IdentityPresenter`, which is why nothing about them changed. See
[wire](wire.md).

`New` takes an **interface**, not a concrete repository, so your own service type
satisfies it ([[D-022]]):

```go
type articleService struct {
    specs.Repo[Article, int64, ArticleUpdate]   // embedded: satisfies it for free
}

func (s articleService) Save(ctx context.Context, a *Article) (Article, error) {
    if err := s.checkQuota(ctx, a); err != nil { return Article{}, err }
    return s.Repo.Save(ctx, a)
}

crudnet.New(articleService{…}).Mount(mux, "/articles")
```

`crudnet.Repository` and `crudnet.Service` are **aliases** for the `port` types,
not second interfaces — so the same value mounts on Fiber, Gin and gRPC without a
line of change.

## Options

| Option | Does |
|---|---|
| `WithQuery(cfg)` | bound the DSL — [`query.Config`](query.md#bounding-it) |
| `WithScope(fn)` | per-request narrowing: `func(*http.Request) ([]crud.Option, error)` |
| `WithTransform(fn)` | a presenter: `func(*http.Request, M) any`. Applies to entities and pages alike |
| `BeforeSave(fn)` | `func(*http.Request, *M) error`, on create and replace |
| `BeforeUpdate(fn)` | `func(*http.Request, ID, *U) error` |
| `ReadOnly()` | register the reads and nothing else |
| `AllowClientID()` | let a create choose its own database-generated key |
| `MaxBulk(n)` | cap `POST /bulk-delete` — the default is `port.DefaultMaxBulk` (1024); there is no "unlimited" |
| `MaxBody(n)` | cap the request body this handler reads, in bytes; the default is 4 MiB and a body past it is 413 ([[D-063]]) |
| `WithRenderer(r)` | replace the envelope |
| `WithErrorHandler(fn)` | `func(http.ResponseWriter, *http.Request, error)` |

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

Every one of these is spelled identically on all four bindings.

## What create and replace refuse

On create the body binds onto the model, then a database-generated key and every
`generated` column are **cleared** — a client cannot choose its own id or forge a
server-side timestamp.

`PUT /{id}` is held to the same rule from the other side: where the database
generates the key it replaces an existing row and **never creates one**, so the
id space stays the server's ([[D-012]]). `AllowClientID()` hands it over, and a
key the client owns anyway — a uuid, a slug — is unaffected.

## Errors

```go
mux.Handle("/", crudnet.Errors(crudhttp.WithMessages(catalogue))(routes))
```

`Errors` renders an error a `HandlerFunc` returned, recovers a panic into a
silent 500, and leaves alone a handler that already wrote a response. It covers
this binding's own routes **and** hand-rolled ones, so mounting it over a mux
carrying both is one call.

`crudnet.WithErrors(f, opts...)` adapts a single error-returning handler.

Statuses map by sentinel, so the transport never imports the decorator that
raised them: `crud.ErrNotFound` → 404, `crud.ErrForbidden` → 403,
`crud.ErrConflict` → 409, query and schema errors → 400 with the offending path
named, everything else → 500 **with no detail**. `crudnet.Status(err)` is
exported if you render your own bodies.

With the [error subsystem](errs.md) wired in, a 409 or a 422 also carries
`error_code` and `field` — see [crudhttp](crudhttp.md#the-envelope).

## Two mounting details

- **The collection is registered under both spellings.** `ServeMux` has no
  trailing-slash redirect, so `/articles` and `/articles/` are two patterns and
  the unregistered one answers 404. `Mount` registers both.
- **At the root the collection is `"/{$}"`, never `"/"`.** A bare `/` is
  `ServeMux`'s catch-all: mounted at the root it would answer for every unclaimed
  path in the process, returning 200 and a page of rows where the application
  meant 404.

An unmounted verb on a mounted path answers **405**.

## Routing — the mux's own 404, and the 405 it cannot reach

| | |
|---|---|
| `Routing(mux, opts…)` | registers the catch-all pattern, rendering a 404 in the envelope |

A path nothing claimed is answered by `http.ServeMux` itself, before any handler
or middleware of this library runs, and what it writes is `404 page not found`
as `text/plain` — so a client that parses one shape for every failure has nothing
to parse. `Routing` installs the catch-all `/`, which is the only seam net/http
gives.

**A verb a path does not have is still the mux's own 405.** That refusal never
reaches a handler: the mux matches the path, finds no method and answers by
itself. `crudfiber` and `crudgin` both have a seam for it and this one does not
([[FL-013]]).

Call it once, on a mux that has no `/` of its own. Registering the same pattern
twice is a panic from the standard library, and it is the right one: two
catch-alls mean one of them never answers.

## See also

- [crudfiber](crudfiber.md) · [crudgin](crudgin.md) · [crudgrpc](crudgrpc.md) — the same API elsewhere
- [crudhttp](crudhttp.md) — the status table, the envelope, the renderer seam
- [port](port.md) — business rules between the handler and the repository
- [[UC-001]] expose a CRUD API without handlers · [[FL-013]] a request through another binding
