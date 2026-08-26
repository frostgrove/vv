# authgrpc — authenticate a gRPC call

```go
import "github.com/frostgrove/vv/auth/rpc/authgrpc"
```

**Module:** `github.com/frostgrove/vv/auth/rpc/authgrpc` — one dependency,
`google.golang.org/grpc`
· **Depends on:** `auth`, grpc

It does **not** require [crudgrpc](crudgrpc.md) ([[D-051]]).

---

## Wire it

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
srv := grpc.NewServer(
	grpc.ChainUnaryInterceptor(crudgrpc.Errors(), authgrpc.Unary(guard)),
	grpc.ChainStreamInterceptor(authgrpc.Stream(guard)),
)
crudgrpc.New(articles).Register(srv, "Article")
```

Step 1 is [authjwt](authjwt.md)'s, step 2 is [auth](auth.md)'s and step 3 is
[security](security.md)'s. This page is only step 4.

## What you get

| | |
|---|---|
| `Unary(guard, opts...)` | a `grpc.UnaryServerInterceptor` |
| `Stream(guard, opts...)` | a `grpc.StreamServerInterceptor` |
| `Skip(fullMethods...)` | leave named methods unauthenticated |

## What is different from the three HTTP bindings

**A credential comes from metadata, not a header.** The keys are the same names
lowercased, and `metadata.MD.Get` is case-insensitive, so the guard's default
`Authorization` finds what a client sent as `authorization`.

**There is no status table here.** A refusal is an error returned from the
interceptor; `crudgrpc.Errors` renders it, and `errs.KindUnauthorized` already
maps to `UNAUTHENTICATED`. So this package writes no status and [[D-008]]'s
ordering is untouched.

**There is no 404 to lose to.** A row hidden by a scope is still `NOT_FOUND`
further in — that is the repository's answer, not this one's.

## Skip

```go
authgrpc.Unary(guard, authgrpc.Skip(
	"/grpc.health.v1.Health/Check",
	"/vv.crud.v1.Article/List",
))
```

The name is the full one, with the leading slash, as it appears in
`grpc.UnaryServerInfo.FullMethod`. **A prefix is not accepted**: an exact list is
auditable, and a prefix quietly widens the day somebody adds a method under it.

This is what `crudgrpc.ServicePrefix` was designed for. Each resource gets its
own service name, so `/vv.crud.v1.Article/Create` and
`/vv.crud.v1.Comment/Create` are different methods a rule can tell apart; under
a shared service they would be the same one.

## Two limits, stated rather than left to be discovered

**A stream is authenticated once, when it opens.** A credential that expires
mid-stream is not noticed — an interceptor runs before the first message and
never again. A long-lived stream that must re-check does it in its own loop.

**The peer's TLS certificate is not read.** mTLS is a different authentication
and its principal comes from `credentials.AuthInfo`; write it as an
`auth.Authenticator` and pass it to the same guard.

## See also

- [auth](auth.md) · [crudgrpc](crudgrpc.md)
- [[D-055]] · [[D-056]] · [[UC-019]] · [[FL-019]] · [[FL-013]]
