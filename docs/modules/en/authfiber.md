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

**A consecutive double install with the same guard authenticates once.** A
different guard performs its own check. A -> B -> A is refused because no
assurance order can be inferred; mount cumulative checks once each and use one
`auth.Chain` for alternatives ([[D-076]]). `Middleware` validates at
construction; nil and `new(auth.Guard)` panic before traffic.

## Reading the routing table for the gate

| | |
|---|---|
| `Routes(app)` | what this application actually serves, as `[]authhttp.Route` |
| `Verify(app, declared, opts…)` | that, compared against the declarations, in one call |

It reads Fiber's own table rather than a list kept alongside it. That is the
whole point: a declaration is only worth checking against a second statement
arrived at independently, and a recorder wrapped around registration would agree
with the declaration exactly when both were wrong.

HEAD and OPTIONS are left out. Fiber registers a HEAD for every GET itself, once
its start-up process has run, and the flag that marks one as generated is
unexported — so from outside a generated HEAD and a hand-written one are the same
value. A HEAD-only route therefore cannot be declared here, which is the same
reason it cannot be mounted alone. OPTIONS is a CORS middleware's answer, not an
endpoint ([[D-073]]).

## See also

- [auth](auth.md) · [authhttp](authhttp.md) · [crudfiber](crudfiber.md)
- [[UC-019]] · [[FL-019]] · [[FL-013]]
