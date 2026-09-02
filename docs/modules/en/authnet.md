# authnet — authenticate a net/http request

```go
import "github.com/frostgrove/vv/auth/http/authnet"
```

**Module:** root — it imports only the standard library, so it costs nothing
· **Depends on:** `auth`, `authhttp`, `porthttp`, `net/http`

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
| `AnswerPreflight(middleware, preflight)` | wraps it so a browser's CORS preflight — `OPTIONS`, an `Origin`, an `Access-Control-Request-Method` and no `Authorization` — skips the guard and is answered by the handler you name, without ever reaching `next` ([[D-103]]). An `OPTIONS` carrying a credential is authenticated like any other request |
| `SkipPreflight(middleware)` | the same wrapper with nobody named to answer: the preflight gets a bare `204` and no `Access-Control-Allow-*` header, which the browser refuses — visibly, instead of a forgeable pair of headers reaching a handler unauthenticated |

`opts` are `porthttp.RenderOption`s — the same ones `crudnet.Errors` takes — so
a refusal renders through your vocabulary and your message catalogue.

## What it does

Reads the credential, verifies it, and puts an `auth.Principal` into
`r.Context()`. That is the only channel that reaches a repository: a transport
hook can reject a request but cannot rewrite the context the repository sees.

A refusal is written here and the next handler never runs. It renders through
the same envelope as every other failure, so a client sees one error shape
whether the request was refused at the door or by the repository.

**A consecutive double install with the same guard authenticates once.** A
different guard performs its own check. A -> B -> A is refused because this
binding cannot infer whether B is stronger or weaker; mount cumulative checks
once each, and use one `auth.Chain` for alternatives ([[D-076]]).

`Middleware` calls `Guard.Validate`; nil and `new(auth.Guard)` panic while the
graph is built. A middleware with no ready guard authenticates nothing while
looking exactly like one that does.

## Not the only router

`http.ServeMux` is what the example uses; the middleware is a plain
`func(http.Handler) http.Handler`, so chi, gorilla/mux and httprouter take it
directly.

## Recording the surface for the gate

| | |
|---|---|
| `Over(mux)` | a mux that remembers what was registered on it; `nil` gets a new one |
| `Surface.Handle` / `HandleFunc` | register, and record |
| `Surface.Handler()` | what the surface serves, and nothing more: no way to register past the recorder |
| `Surface.Mux()` | the mux itself — the escape hatch, and the thing `Handler` exists to make unnecessary |
| `Surface.Routes()` | what was registered, as `[]authhttp.Route` |
| `Surface.Verify(declared, opts…)` | that, compared against the declarations: the prefix's relative ones, and the `authhttp.AtRoot` ones for everything outside it |
| `Surface.VerifyAreas(areas…)` | the same, over every recorded route — see [authhttp](authhttp.md) |
| `AnyMethod` | the method recorded for a pattern that names none |

This is the one place the three HTTP bindings do not do the same thing, and the
difference is not a choice. `authfiber` and `authgin` read the router's own
table; an `http.ServeMux` cannot be asked what it holds, so the second statement
has to be recorded as it is made.

**What that costs:** a handler registered straight on the wrapped mux is
invisible here. On net/http the gate catches a stale declaration and an endpoint
added *through the Surface* without one, and cannot catch one that went around
it. That is a documented limit of this binding rather than a defect to hide —
[[D-073]] refuses to make the recorder look like the other two, and
`TestARouteRegisteredPastTheSurfaceIsInvisibleToTheGate` pins it.

Serve with `Surface.Handler()`. It forwards to the same mux and offers no way to
add anything to it, so nothing in an application has to hold the value that can
register past the gate. `Surface.Mux()` remains for a caller that genuinely needs
the mux; a route mounted through it is one nobody verified.

A pattern with no verb answers every verb, which is a decision and not an
omission — so it is declared as one, with `AnyMethod`, rather than silently
matching the GET somebody had in mind ([[D-073]], [[FL-019]]).

## See also

- [auth](auth.md) — the guard, the options, and everything transport-neutral
- [authhttp](authhttp.md) — where the refusal is written
- [authgin](authgin.md) · [authfiber](authfiber.md) · [authgrpc](authgrpc.md)
- [[UC-019]] · [[FL-019]] · [[FL-013]]
