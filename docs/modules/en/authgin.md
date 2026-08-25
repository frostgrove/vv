# authgin — authenticate a Gin request

```go
import "github.com/shardit-io/vv/auth/http/authgin"
```

**Module:** `github.com/shardit-io/vv/auth/http/authgin` — one dependency,
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

**Installing it twice authenticates once** — on the engine and again on a group
is the ordinary way that happens.

`Middleware(nil)` panics.

## See also

- [auth](auth.md) · [authhttp](authhttp.md) · [crudgin](crudgin.md)
- [[D-051]] why it does not require crudgin · [[UC-019]] · [[FL-019]] · [[FL-013]]
