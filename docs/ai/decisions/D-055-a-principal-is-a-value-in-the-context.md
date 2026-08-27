# D-055 — A principal is a value in the context, and the library never puts it there

**Status:** accepted
**Invariant:** `auth` defines what an authenticated caller is and nothing else: it holds no credential store, no key, no transport type and no registry, and the only way a principal reaches a policy is a `context.Context` a transport binding wrote. `auth` is a package, not a manifest entry — `scripts/checks.sh:TIER0` is unchanged.

## The decision

Four packages in the root module and four satellites, and the line between them
is what a consumer would substitute:

```
auth/                 the contract: Principal, Role, Permission, Credential,
                      Authenticator, Guard, the context key, the 401
auth/apikey/          an Authenticator over a shared secret          (no dependency)
auth/http/authhttp/        the HTTP-shaped shared half of the middleware  (no dependency)
auth/http/authnet/         the net/http middleware                        (no dependency)

auth/authjwt/         MODULE — golang-jwt/v5
auth/http/authgin/         MODULE — gin
auth/http/authfiber/       MODULE — fiber v3
auth/rpc/authgrpc/         MODULE — grpc
```

`crud/decorators/security` gains `principal.go` and imports `auth`. The import
runs one way and only one way.

**`auth` does not join the contract manifest.** [[D-048]] closed it at `crud
query errs port` and refused `authjwt` by name, and that refusal stands: what it
forbids is the *contract*, and its own closing paragraph says so — *"A package
may exist without being on the manifest. `vvflag`, `utils/vvcfg`,
`internal/codegen` and `app` are all implementations with no contract at all,
and that is the normal case rather than the exception."* `auth` is in that
company. `make check-tiers` is untouched and stays green by construction rather
than by having been re-checked.

## Why

**Because the seam already existed and was one closure wide.** Every hook on
`security.Policy` is a `func(context.Context) ...`, and every one of them
answered "who is calling?" by reading a context key the application invented.
The whole tree had three `context.WithValue` sites — the executor, the locale
and the retained body — and not one of them carried an identity. So each
consumer wrote the key, the type of a role, the JWT parsing and the shape of a
401 again. What was missing was never a mechanism; it was a vocabulary.

**Because a contract with one implementation is an implementation.** That is
[[D-048]]'s count rule, and it is why `auth/apikey` ships in the same change as
`auth/authjwt`. Two `Authenticator`s exist on the day the interface does, and
they are genuinely different — one verifies a signature, one looks a secret up —
so the interface is describing something rather than decorating one struct.

**Because `security` imports `auth` and not the reverse.** A middleware has no
reason to compile a repository in. Inverted, `auth/http/authgin` would drag `crud`,
`crud/sqlrepo` and the whole predicate AST into a process that only wanted to read
a header. The direction also settles where a rule lives: `auth` says what a
permission *is*, `security` says what holding one *allows*.

**Because a `Principal` is an interface and a `Claims` is a struct.** A consumer
whose identity already has a Go type implements four methods over it and this
library never sees the type. That is the difference between an ecosystem a
consumer opts into and one it has to convert into: `authjwt.Parser[C]` parses
into *their* struct, and the bridge to `Principal` is a second, separate call
that a consumer who wants only a JWT parser never makes.

**Because the four methods answer rather than enumerate.** `In`, `Has`,
`Subject`, `Attr`. The gate asks them once per operation, and a `Roles() []Role`
would be an allocation per request for a list nothing in the library reads.
Enumeration is `auth.Claims`'s, where it is a field.

**Because roles expand once, when the principal is built.** `auth.RoleMap` is a
value the application passes to a provider, and `Claims.Grant` folds it in
before the principal is stored. A `Has` that walked a role map at check time
would answer differently depending on which map was reachable at the call site,
and one token would then mean two things in one process.

**Because there is no `init()` and no package-level table.** `errs/doc.go`
refuses those for a reason that applies here unchanged: two libraries declaring
`"admin"` with different permissions must not settle it by link order, and a
`go.work` joining twelve modules makes an `init()` in the wrong one invisible.

**Because the transport-neutral half is `auth.Guard` rather than four copies.**
Whether an optional guard accepts a forged token, whether a second install
re-verifies, which header is read — those are five decisions, and four bindings
each holding their own copy is four chances to fix one and not the others.
`Guard.Authenticate` takes a `func(name string) string`, which `http.Header.Get`,
`gin.Context.GetHeader`, `fiber.Ctx.Get` and gRPC metadata can all supply, so the
gRPC interceptor gets the same decisions with no HTTP package in its build
([[D-045]]).

**Where the naming lands, and the one cell that is not obvious.** [[D-035]]'s
grid is subsystem × library, and `auth` fills a row `ROADMAP` left empty:
`authjwt`, `authgin`, `authfiber`, `authnet`, `authgrpc`, `authhttp`. `apikey`
takes no prefix because nothing collides — there is no `apikey` package one
import away — and that asymmetry is the rule working, not an exception to it.
`auth/http/authgin` may not be a file in `crud/http/crudgin`: D-035 forbids a subsystem ×
library cell holding a second subsystem, and the consequence is concrete rather
than stylistic — a consumer wanting authentication on Gin without this library's
CRUD routes would otherwise have no way to get it.

## What it forbids

- Do not add `auth` to `scripts/checks.sh:TIER0`. [[D-048]] is in force; this is a
  package, and the manifest stays four contracts.
- Do not import `crud/decorators/security`, `crud` or `port` from `auth` or from
  any `auth*` binding. The direction is one-way, and it is what keeps a
  middleware from compiling a repository in.
- Do not put a principal anywhere but the context. `gin.Context.Set`,
  `fiber.Ctx.Locals` and a handler field are all invisible to a policy, and all
  three compile.
- Do not give `auth` a transport type. An `http.Header` in it would put the gRPC
  interceptor out of reach, which is [[D-045]]'s test verbatim.
- Do not add an `init()`, a package-level `RoleMap` or a registry of
  authenticators. They are wired at a call site or they are not wired.
- Do not resolve a role to permissions at check time. Expand when the principal
  is built, or the same token means two things in one process.
- Do not let a policy read an absent principal as "no narrowing". Every helper
  in `crud/decorators/security/principal.go` refuses, which is [[UC-004]]'s guarantee 16.
- Do not put an auth middleware in `crudgin`, `crudfiber` or `crudnet`, and do
  not make an `auth*` module require its `crud*` sibling. Two decisions, not one
  ([[D-051]]).

## Where it lives

- `auth/principal.go` — `Principal`, `Role`, `Permission`, `Claims`, `RoleMap`,
  `HasAll`, `HasAny`, `InAny`, `Scopes`.
- `auth/context.go` — the one context key, `WithPrincipal`, `PrincipalFrom`,
  `Require`.
- `auth/credential.go` — `Credential`, `Authenticator`, `ParseAuthorization`,
  `Bearer`, `Chain`.
- `auth/guard.go` — `Guard`, `NewGuard`, `Optional`, `Header`, `Lookup`.
- `auth/apikey/apikey.go` — the second `Authenticator`, and `Store`'s three
  results.
- `auth/authjwt/` — the JWT provider; `go.mod` carries the dependency decision.
- `auth/http/authhttp/`, `auth/http/authnet/`, `auth/http/authgin/`, `auth/http/authfiber/`,
  `auth/rpc/authgrpc/` — the four transports and their shared HTTP half.
- `crud/decorators/security/principal.go` — the bridge, and the only file in
  the tree where `auth` and `crud` meet.
- `scripts/checks.sh:TIER0` — unchanged, and that is the assertion.

## Proven by

```
make check-tiers
make check-deps
```

The first passes because no manifest package is in the diff at all. The second
is the one that means something: the root module still lists zero third-party
packages with `auth`, `auth/apikey`, `auth/http/authhttp` and `auth/http/authnet` in it,
and `auth/http/authgin` resolves gin without fiber while `auth/http/authfiber` resolves
fiber without gin.

Tests:

- `TestEveryPrincipalPolicyFailsClosedWithoutOne` —
  `crud/decorators/security/principal_test.go`. Every helper, one subtest each,
  asserting `auth.ErrUnauthenticated` **and** that no statement reached the
  recorder.
- `TestPerActionRefusesAVerbNobodyDeclared` — same file, including the arm where
  a caller holding every permission is still refused a verb the map does not
  name ([[D-030]]).
- `TestAMissingClaimIsADenialAndNotAZeroValue` — same file. Verified by making
  `attrOf` return `int64(0)` for an absent claim and watching it fail.
- `TestRequirePermissionRefusesTheCallerThatLacksOne` — same file, with the
  control subtest that lets a qualifying caller through, so the refusal is not
  a repository that refuses everything.
- `TestASecondGuardDoesNotAuthenticateAgain` — `auth/guard_test.go`.
- `TestAnOptionalGuardStillRefusesABadCredential` — `auth/guard_test.go`, and
  its transport twin `TestAnOptionalGuardLetsAnAnonymousRequestThrough` in each
  binding's `middleware_test.go`.
- `TestAStoreFailureIsNotARefusal` — `auth/apikey/apikey_test.go`, the arm the
  three-result `Lookup` exists for.

## See also

[[D-048]] [[D-035]] [[D-045]] [[D-051]] [[D-021]] [[D-056]] [[UC-004]]
