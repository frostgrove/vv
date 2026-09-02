# authfiber — authenticate a Fiber v3 request

```go
import "github.com/frostgrove/vv/auth/http/authfiber"
```

**Module:** `github.com/frostgrove/vv/auth/http/authfiber` — one dependency,
`github.com/gofiber/fiber/v3`
· **Depends on:** `auth`, `authhttp`, `porthttp`, fiber v3

It does **not** require [crudfiber](crudfiber.md) ([[D-051]]).

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
app := fiber.New(fiber.Config{ErrorHandler: crudfiber.ErrorHandler()})
app.Use(authfiber.Middleware(guard))
app.Use("/articles", crudfiber.New(articles).Routes())
```

Step 1 is [authjwt](authjwt.md)'s, step 2 is [auth](auth.md)'s and step 3 is
[security](security.md)'s. This page is only step 4.

or on one group:

```go
api := app.Group("/api", authfiber.Middleware(guard))
```

## What you get

| | |
|---|---|
| `Middleware(guard, opts...)` | a `fiber.Handler` |

## The one thing that is different here

**The principal goes into the context with `c.SetContext`, not into `Locals`.**

`Locals` is where a Fiber middleware normally puts per-request state, and it is
the wrong place for this: `crudfiber` hands `c.Context()` down to the port layer,
so a principal in `Locals` is invisible to every policy. Both spellings compile,
both look right in review, and only one of them narrows a query.

`TestAnAuthenticatedRequestReachesTheHandlerWithItsPrincipal` in
`auth/http/authfiber/middleware_test.go` is what fails when it is wrong.

## Everything else

A refusal is written here rather than returned, for
[authhttp](authhttp.md)'s reason. `crudfiber.Errors` and
`crudfiber.ErrorHandler` both leave an already-written response alone, so this
composes with either.

**A repeated refusal header keeps every value.** The rendered headers go on with
`c.Response().Header.Add`; `Ctx.Set` overwrites, so a 401 offering two
`WWW-Authenticate` challenges arrived offering the last one and looked fine.
`TestARefusalCarriesEveryHeaderTheRendererAskedFor` in
`auth/http/authfiber/refuse_test.go` is what fails when it is wrong, and the same
test name stands over the other two bindings' response writers.

**A consecutive double install with the same guard authenticates once.** A
different guard performs its own check. A -> B -> A is refused because no
assurance order can be inferred; mount cumulative checks once each and use one
`auth.Chain` for alternatives ([[D-076]]). `Middleware` validates at
construction; nil and `new(auth.Guard)` panic before traffic.

## Reading the routing table for the gate

| | |
|---|---|
| `Routes(app)` | what this application actually serves, as `[]authhttp.Route` |
| `Verify(app, declared, opts…)` | that, compared against the declarations: the prefix's relative ones, and the `authhttp.AtRoot` ones for everything outside it |
| `VerifyAreas(app, areas…)` | the same, over every mounted route — see [authhttp](authhttp.md) |
| `AnswerPreflight(handler, preflight)` | wraps the middleware so a browser's CORS preflight is answered by the handler you name instead of being asked for a credential |
| `SkipPreflight(handler)` | the same wrapper with nobody named: the preflight is answered `204` and reaches no route |

It reads Fiber's own table rather than a list kept alongside it. That is the
whole point: a declaration is only worth checking against a second statement
arrived at independently, and a recorder wrapped around registration would agree
with the declaration exactly when both were wrong.

The HEAD Fiber invents is left out; everything a consumer mounted is not. Fiber
registers a HEAD for every GET once its start-up process has run, and the flag
that marks one as generated is unexported — so the generated half is recognised
by its shape instead: a HEAD whose path also carries a GET. A HEAD with no GET
beside it, and an OPTIONS handler, are surface like any other and have to declare
their access. What this cannot separate is a hand-written HEAD on a path that
also serves GET; it is covered by that path's GET declaration, and that is the
accepted limit ([[D-073]]).

## Letting a CORS preflight through

A browser sends `OPTIONS` with no credential before a cross-origin write. A guard
mounted in front of the CORS middleware answers it with a 401, the browser never
makes the request it was asking about, and the failure looks like a CORS
misconfiguration.

```go
app.Use(authfiber.AnswerPreflight(authfiber.Middleware(guard), cors.New()))
```

A preflight is `OPTIONS`, an `Origin`, an `Access-Control-Request-Method` and no
`Authorization` header, and nothing else. One that matches skips the guard and
goes to the handler you named, which answers it; **the chain ends there and no
route runs** ([[D-103]]). An `OPTIONS` carrying a credential is a request and is
authenticated like one.

`SkipPreflight(handler)` is the same wrapper with nobody named to answer, and
answers `204` itself with no `Access-Control-Allow-*` header. That is a visible
CORS failure in the browser rather than an unauthenticated `OPTIONS` running a
hand-written handler: the two headers the predicate reads are set by the client,
so anything reachable past them is reachable by anyone.

Ordering the CORS middleware before the guard does the same job and is the better
answer where the chain is yours to order; this is for the chain that is not.

## See also

- [auth](auth.md) · [authhttp](authhttp.md) · [crudfiber](crudfiber.md)
- [[UC-019]] · [[FL-019]] · [[FL-013]]
