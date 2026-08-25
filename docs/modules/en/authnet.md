# authnet — authenticate a net/http request

```go
import "github.com/shardit-io/vv/http/authnet"
```

**Module:** root — it imports only the standard library, so it costs nothing
· **Depends on:** `auth`, `authhttp`, `crudhttp`, `net/http`

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
mux := http.NewServeMux()
crudnet.New(articles).Mount(mux, "/articles")

http.ListenAndServe(":8080", crudnet.Errors()(authnet.Middleware(guard)(mux)))
```

Step 1 is [authjwt](authjwt.md)'s, step 2 is [auth](auth.md)'s and step 3 is
[security](security.md)'s. This page is only step 4.

`authnet.Handler(guard, next)` is the same thing for one route, where its
neighbours are not authenticated.

## What you get

| | |
|---|---|
| `Middleware(guard, opts...)` | an ordinary `func(http.Handler) http.Handler` |
| `Handler(guard, next, opts...)` | the same, applied to one handler |

`opts` are `crudhttp.RenderOption`s — the same ones `crudnet.Errors` takes — so
a refusal renders through your vocabulary and your message catalogue.

## What it does

Reads the credential, verifies it, and puts an `auth.Principal` into
`r.Context()`. That is the only channel that reaches a repository: a transport
hook can reject a request but cannot rewrite the context the repository sees.

A refusal is written here and the next handler never runs. It renders through
the same envelope as every other failure, so a client sees one error shape
whether the request was refused at the door or by the repository.

**Installing it twice authenticates once.** A context that already carries a
principal is handed back untouched.

`Middleware(nil)` panics. A middleware with no guard authenticates nothing while
looking exactly like one that does.

## Not the only router

`http.ServeMux` is what the example uses; the middleware is a plain
`func(http.Handler) http.Handler`, so chi, gorilla/mux and httprouter take it
directly.

## See also

- [auth](auth.md) — the guard, the options, and everything transport-neutral
- [authhttp](authhttp.md) — where the refusal is written
- [authgin](authgin.md) · [authfiber](authfiber.md) · [authgrpc](authgrpc.md)
- [[UC-019]] · [[FL-019]] · [[FL-013]]
