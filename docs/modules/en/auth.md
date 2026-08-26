# auth — who the caller is

```go
import "github.com/frostgrove/vv/auth"
```

**Module:** root — it imports the standard library and `errs`, and nothing else
· **Contract manifest:** no ([[D-048]], [[D-055]])
· **Depends on:** `errs`

A `Principal` is four methods. A transport puts one into the request context; a
policy reads it out. Between those two points this is the only vocabulary either
side has to agree on.

Import it directly when you are writing an authenticator, a middleware, or a
policy that reads the caller.

---

## What you get

| | |
|---|---|
| `Principal` | four methods: `Subject`, `In`, `Has`, `Attr` |
| `Claims` | a ready-made `Principal`, for when you do not have your own type |
| `Role`, `Permission` | strings with a type; `RoleMap` expands one into the other |
| `Credential` | a scheme and a token, both strings — no transport type here |
| `Authenticator` | the seam a provider implements; `Chain` tries several |
| `Guard` | the transport-neutral half of a middleware |
| `WithPrincipal`, `PrincipalFrom`, `Require` | the one context key in the tree that carries an identity |
| `ErrUnauthenticated`, `Unauthenticated` | the 401, and the sentinel it wraps |

## The principal

```go
type Principal interface {
	Subject() string
	In(r Role) bool
	Has(p Permission) bool
	Attr(name string) (any, bool)
}
```

Four answers rather than four lists. The gate asks them once per operation, so a
`Roles() []Role` would be an allocation per request for something nothing in the
library reads.

**Have your own identity type?** Implement it over that type and this package
never sees yours. That is the whole reason it is an interface.

**Do not?** `auth.Claims` is one:

```go
p := auth.Claims{
	Sub:         "u-1",
	Roles:       []auth.Role{"editor"},
	Permissions: []auth.Permission{"article:read"},
	Attrs:       map[string]any{"tenant": int64(7)},
}
```

`Attr` separates absent from present-and-nil, the same distinction `crud.Opt`
draws for a column.

## Roles and permissions

A permission is the unit a rule should name: a rule naming a role has to be
edited when the roles are reorganised, a rule naming a permission does not.

```go
var roles = auth.RoleMap{
	"editor": {"article:read", "article:write"},
	"admin":  {"article:read", "article:delete"},
}

p := auth.Claims{Roles: []auth.Role{"editor"}}.Grant(roles)
p.Has("article:write")
p.Has("article:delete")
```

The first is true. The second is false: `admin` grants it and this caller is not
one, so expansion is scoped to the roles the token actually carried.

**Expansion happens once, when the principal is built.** A `Has` that walked a
role map at check time would answer differently depending on which map was
reachable at the call site, and one token would mean two things in one process.

`RoleMap` is a value you pass, never a registry this package holds. Two
libraries declaring `"admin"` differently must not settle it by link order.

`auth.Scopes("read write admin")` splits an OAuth scope claim into permissions,
because providers spell it that way and you should not have to.

The three quantifiers disagree about the empty case, deliberately:

| | no permissions named |
|---|---|
| `HasAll(p)` | **true** — a rule naming none refuses nothing |
| `HasAny(p)` | **false** — "any of nothing" is not satisfiable |
| `InAny(p)` | **false** — same reason |

## The context

```go
ctx = auth.WithPrincipal(ctx, p)
p, err := auth.Require(ctx)
p, ok := auth.PrincipalFrom(ctx)
```

The first is a transport's, once per request. The second is a policy's, once per
operation. The third is for the callers to whom absence is a normal answer.

**One key for every transport.** A second key in an HTTP package would be
invisible to the gRPC interceptor, both packages' tests would pass, and a policy
would silently fail closed on one protocol only.

`Require` returns the 401 when there is nobody. Failing closed there is the
point: a policy that read an absent principal as "no narrowing" would widen
every query on the one request where the middleware was not mounted.

A nil principal is dropped rather than stored, so `PrincipalFrom` never answers
`(nil, true)`.

## Authenticators

```go
type Authenticator interface {
	Authenticate(ctx context.Context, c Credential) (Principal, error)
}
```

Two ship: [authjwt](authjwt.md) and [apikey](apikey.md). Write a third for
mTLS, a session store, or a gateway header — the rest of the subsystem does not
change.

`auth.Chain` accepts more than one kind of credential at one endpoint:

```go
authn := auth.Chain(jwtAuthn, apiKeyAuthn)
```

The refusal when none succeeds says nothing about how many were tried. Reporting
"3 of 3 refused" would be a list of the schemes a deployment accepts.

## The guard

Everything a middleware does that is not framework-shaped:

```go
guard := auth.NewGuard(authn,
	auth.Header("X-Api-Key"),
	auth.Optional(),
)

ctx, err := guard.Authenticate(r.Context(), r.Header.Get)
```

Both options change a default: without them the credential is read from
`Authorization`, and a request that presents none is refused.

`Authenticate` takes a `func(name string) string`. `http.Header.Get`,
`gin.Context.GetHeader`, `fiber.Ctx.Get` and gRPC metadata can all supply one,
which is what lets the four transports share every decision above them.

`auth.Lookup` replaces the whole rule when a header is not where your credential
is:

```go
auth.Lookup(func(get func(string) string) (auth.Credential, bool) {
	if k := get("X-Api-Key"); k != "" {
		return auth.Credential{Scheme: "ApiKey", Token: k}, true
	}
	return auth.Bearer(get("Authorization"))
})
```

**`Optional` does not accept a bad credential.** A token that fails to verify is
a 401 whether or not the endpoint is optional: treating it as anonymous would
mean a stale session silently sees the public view instead of a prompt to sign
in again.

**A second guard does not authenticate again.** A context that already carries a
principal is handed back untouched, so mounting one globally and again on a
group costs one verification.

## The 401

```go
return auth.Unauthenticated("signature does not verify")
```

- `errs.KindUnauthorized`, so every existing status table answers 401 —
  `porthttp.StatusFor` and `crudgrpc.CodeFor` both did already, with no new arm;
- wraps `ErrUnauthenticated`, so `errors.Is` still branches;
- **the reason never reaches the body.** It travels in the wrapped error.

That last point is not a style choice. `port.Violations` synthesises one
violation for a fault carrying none — which every 401 is — and copies
`Fault.Message` into it, and that violation is rendered. So a reason put there
tells whoever is probing which half of the token to work on. Every refusal
renders identically: `unauthenticated`, *"authentication is required"*
([[D-056]]).

To recover the reason in a log, `errors.As` down to the `*errs.Fault` and call
`Unwrap`. Note that `errors.Unwrap` does **not** reach it: a fault unwraps to a
slice, which the single-error form does not walk.

## Wiring it to a repository

`auth` never decides anything. What holding a permission *allows* is
[security](security.md)'s:

```go
policy := security.Combine(
	security.RequirePermission[Article, int64]("article:read"),
	security.ScopeAttr[Article, int64]("TenantID", "tenant"),
)
articles := Articles.Bind(db, security.Gate(policy))
```

The import runs one way — `security` knows about `auth`, `auth` knows nothing
about a repository — so a middleware never compiles the predicate AST in.

## See also

- [authjwt](authjwt.md) · [apikey](apikey.md) — the two providers
- [authnet](authnet.md) · [authgin](authgin.md) · [authfiber](authfiber.md) · [authgrpc](authgrpc.md) — the four transports
- [security](security.md) — what a principal is allowed to do
- [[D-055]] the contract and its placement · [[D-056]] the 401's shape
- [[UC-019]] authenticate a request · [[FL-019]] where it happens
