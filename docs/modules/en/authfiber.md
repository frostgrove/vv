# authfiber — authenticate a Fiber v3 request

```go
import "github.com/shardit-io/vv/http/authfiber"
```

**Module:** `github.com/shardit-io/vv/http/authfiber` — one dependency,
`github.com/gofiber/fiber/v3`
· **Depends on:** `auth`, `authhttp`, `crudhttp`, fiber v3

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
`http/authfiber/middleware_test.go` is what fails when it is wrong.

## Everything else

A refusal is written here rather than returned, for
[authhttp](authhttp.md)'s reason. `crudfiber.Errors` and
`crudfiber.ErrorHandler` both leave an already-written response alone, so this
composes with either.

**Installing it twice authenticates once.** `Middleware(nil)` panics.

## See also

- [auth](auth.md) · [authhttp](authhttp.md) · [crudfiber](crudfiber.md)
- [[UC-019]] · [[FL-019]] · [[FL-013]]
