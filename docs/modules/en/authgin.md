# authgin — authenticate a Gin request

```go
import "github.com/frostgrove/vv/auth/http/authgin"
```

**Module:** `github.com/frostgrove/vv/auth/http/authgin` — one dependency,
`github.com/gin-gonic/gin`
· **Depends on:** `auth`, `authhttp`, `porthttp`, gin

It does **not** require [crudgin](crudgin.md). Authenticating a request and
serving a CRUD resource are two things you choose separately ([[D-051]]).

---

## Mount it

Four steps, and the third is the one that is easy to leave out.

**1. What verifies a credential.** Here it is your own HMAC secret.
`authjwt.RSA` and `authjwt.JWKS` are for tokens somebody else issues, and
[apikey](apikey.md) is a different provider altogether.

```go
authn := authjwt.Standard(
	authjwt.HMAC([]byte(os.Getenv("JWT_SECRET"))),
	auth.RoleMap{"editor": {"article:read", "article:write"}},
	authjwt.Issuer("https://id.example.com"),
	authjwt.Audience("articles-api"),
)
```

**2. The guard** — what reads the request and puts the caller into the context.
Which header it reads, whether a credential is required at all, and not
verifying the same request twice all live here.

```go
guard := auth.NewGuard(authn)
```

**3. What that caller is then allowed to do.** Leave this step out and the
middleware authenticates and nothing else: a principal sitting in the context
narrows no query on its own.

```go
policy := security.Combine(
	security.RequirePermission[Article, int64]("article:read"),
	security.ScopeAttr[Article, int64]("TenantID", "tenant"),
)
articles := Articles.Bind(db, security.Gate(policy))
```

**4. Mount it.**

```go
r := gin.New()
r.Use(crudgin.Errors(), authgin.Middleware(guard))
crudgin.New(articles).Mount(r, "/articles")
```

Step 1 is [authjwt](authjwt.md)'s, step 2 is [auth](auth.md)'s and step 3 is
[security](security.md)'s. This page is only step 4.

or on one group:

```go
api := r.Group("/api", authgin.Middleware(guard))
```

## What you get

| | |
|---|---|
| `Middleware(guard, opts...)` | a `gin.HandlerFunc` |

`opts` are `porthttp.RenderOption`s, the same ones `crudgin.Errors` takes.

## What it does

Puts an `auth.Principal` into **`c.Request`'s context** — not `c.Set`. That is
the only one a repository sees; a principal in Gin's own key store is invisible
to every policy, and both spellings compile.

A refusal is written here and `c.Abort()` stops the chain. The error is also
filed with `c.Error`, so Gin's own logging middleware sees the cause the body
deliberately does not carry.

**A consecutive double install with the same guard authenticates once** — on
the engine and again on a group is the ordinary way that happens. A different
guard on the group performs its own check. A -> B -> A is refused because no
assurance order can be inferred; mount cumulative checks once each and use one
`auth.Chain` for alternatives ([[D-076]]).

`Middleware` validates at construction; nil and `new(auth.Guard)` panic before
traffic.

## Reading the routing table for the gate

| | |
|---|---|
| `Routes(engine)` | what this application actually serves, as `[]authhttp.Route` |
| `Verify(engine, declared, opts…)` | that, compared against the declarations: the prefix's relative ones, and the `authhttp.AtRoot` ones for everything outside it |
| `VerifyAreas(engine, areas…)` | the same, over every mounted route — see [authhttp](authhttp.md) |
| `AnswerPreflight(middleware, preflight)` | wraps the middleware so a browser's CORS preflight is answered by the handler you name instead of being asked for a credential |
| `SkipPreflight(middleware)` | the same wrapper with nobody named: the preflight is answered `204` and reaches no route |

It reads Gin's own table rather than a list kept alongside it: a declaration is
only worth checking against a second statement arrived at independently.

Nothing is left out. Gin generates neither a HEAD nor an OPTIONS route, so every
method in this table was registered on purpose and has to declare its access —
including the `OPTIONS` handler somebody wrote by hand, which is a route however
much it looks like a CORS answer ([[D-073]]).

## Letting a CORS preflight through

A guard in front of the CORS middleware answers a browser's preflight with a 401,
and the request it was asking about never happens.

```go
engine.Use(authgin.AnswerPreflight(authgin.Middleware(guard), cors.Default()))
```

It recognises a preflight and nothing else: `OPTIONS`, an `Origin`, an
`Access-Control-Request-Method`, and no `Authorization` header. That one skips
the guard and goes to the handler you named, and the context is aborted after it
returns, so **the `OPTIONS` route Gin lets you write never runs unauthenticated**
([[D-103]]). An `OPTIONS` carrying a credential is authenticated like any other
request.

`SkipPreflight(middleware)` is the same wrapper with nobody named to answer, and
answers `204` itself with no `Access-Control-Allow-*` header — a visible CORS
failure rather than an open door, since the two headers the predicate reads are
the client's to set.

## See also

- [auth](auth.md) · [authhttp](authhttp.md) · [crudgin](crudgin.md)
- [[D-051]] why it does not require crudgin · [[UC-019]] · [[FL-019]] · [[FL-013]]
