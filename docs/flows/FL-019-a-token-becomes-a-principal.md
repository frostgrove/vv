# FL-019 — A token becomes a principal

**Entry point:** `auth/http/authnet/authnet.go:Middleware`, `auth/http/authgin/authgin.go:Middleware`, `auth/http/authfiber/authfiber.go:Middleware` and `auth/rpc/authgrpc/interceptor.go:Unary`
**Implements:** [[UC-019]] · **Governed by:** [[D-055]] [[D-056]] [[D-045]] [[D-021]] [[D-044]]

## The path

1. **`Middleware`** — `auth/http/authnet/authnet.go:43` — the binding's only
   framework-shaped decision: which handler type it is and where the context
   comes from. It resolves a renderer once, at construction, and panics on a nil
   guard.
2. **`Guard.Authenticate`** — `auth/guard.go:90` — everything that is not
   framework-shaped. It is handed a `func(name string) string`, which is all
   four transports have in common.
3. **the idempotence check** — `auth/guard.go:91` — a context that already
   carries a principal is returned untouched. A guard mounted globally and again
   on a group verifies once.
4. **`Guard.credential`** — `auth/guard.go:116` — the configured `Lookup`, or
   `ParseAuthorization` over the configured header.
5. **`ParseAuthorization`** — `auth/credential.go:55` — splits on the first
   space, trims, and refuses a header with no space: a bare token is a scheme
   with nothing under it, and a truncating proxy produces exactly that.
6. **absent, and the fork** — `auth/guard.go:97` — no credential is
   `Unauthenticated` unless the guard is `Optional`, in which case the context
   passes through unchanged and carries no principal.
7. **`Authenticator.Authenticate`** — `auth/credential.go:36` — the provider.
   `auth/apikey/apikey.go:96` for a shared secret;
   `auth/authjwt/authenticator.go:22` for a JWT.
8. **`Parser.Parse`** — `auth/authjwt/parser.go:137` — verification. The methods
   come from the `KeySource` and never from the token
   (`auth/authjwt/parser.go:110`), `exp` is required unless waived, and every
   failure collapses to one `Unauthenticated`.
9. **`decode`** — `auth/authjwt/parser.go:171` — the verified claim map becomes
   the caller's own struct, through `json.Decoder.UseNumber` so an integer claim
   stays an integer.
10. **the mapper** — the caller's `func(context.Context, C) (auth.Principal,
    error)`, or `Claims.Grant` under `authjwt.Standard`
    (`auth/authjwt/claims.go:121`), which folds the role map in once.
11. **a bad credential, even when optional** — `auth/guard.go:103` — the
    provider's error is returned whether or not the guard is optional. A forged
    token never becomes anonymous.
12. **`auth.WithPrincipal`** — `auth/context.go:22` — the one context key in the
    tree that carries an identity.
13. **the binding writes the context back** — `r.WithContext(ctx)`,
    `c.Request = c.Request.WithContext(ctx)`, `c.SetContext(ctx)`, or the
    replaced `grpc.ServerStream`. This is the step with a wrong answer that
    compiles; see the table below.
14. **`authhttp.Refuse`** — `auth/http/authhttp/authhttp.go:66` — on the refusing
    path, the status, the headers and the envelope, written here rather than
    deferred.

## Where the bindings differ

| | context in | context out | refusal |
|---|---|---|---|
| `authnet` | `r.Context()` | `r.WithContext(ctx)` | `authhttp.Refuse`, handler not called |
| `authgin` | `c.Request.Context()` | `c.Request = c.Request.WithContext(ctx)` | `c.Error(err)`, `authhttp.Refuse`, `c.Abort()` |
| `authfiber` | `c.Context()` | `c.SetContext(ctx)` — **not `Locals`** | `authfiber.refuse`, `c.Status().JSON()` |
| `authgrpc` | `ss.Context()` / call ctx | returned ctx, or a wrapping `ServerStream` | error returned; `crudgrpc.Errors` renders it |

The Fiber row is the one that matters. `Locals` is where a Fiber middleware
normally puts per-request state and it is the wrong place here: `crudfiber`
hands `c.Context()` to the port layer, so a principal in `Locals` is invisible
to every policy. Both spellings compile and both look right in review.
`TestAnAuthenticatedRequestReachesTheHandlerWithItsPrincipal` in
`auth/http/authfiber/middleware_test.go` is what fails when it is wrong — verified by
changing `SetContext` to `Locals` and watching it.

`authgrpc` writes no status. The refusal is an error carrying
`errs.KindUnauthorized`, and `crudgrpc.Errors` is what turns it into
`UNAUTHENTICATED` — so the gRPC binding has no status table of its own and
[[D-008]]'s ordering is untouched.

## Where the decisions bite

- [[D-055]] — the principal only ever travels in the context, and `auth`
  imports nothing below `errs`.
- [[D-056]] — the refusal is a fault wrapping a sentinel, and its reason is in
  the wrapped error because `port.Violations` renders `Fault.Message`.
- [[D-045]] — `Guard` takes a header-getter rather than a request, which is what
  lets the gRPC interceptor share every decision above step 13.
- [[D-021]] — a nil guard, a nil authenticator, a key source that verifies
  nothing and an unconfigured issuer or audience all panic at construction.
- [[D-044]] — no body names anything internal; `authjwt` does not echo the
  caller's own `kid` back at them.

## Failure modes

- **No credential.** `auth.ErrUnauthenticated`, 401, handler not run — unless
  optional, in which case the chain proceeds with no principal and the
  repository refuses instead.
- **A credential that does not verify.** The same 401, the same body, whether it
  was forged, expired, for another audience, or names an unknown key.
- **A key set that cannot be reached.** `authjwt.JWKS` returns an error and the
  call is refused. It is not distinguished from a bad token, deliberately.
- **A key store that cannot be reached.** `apikey.Store`'s third result travels
  unchanged, so an outage renders as the 500 it is rather than telling every
  caller their key is wrong.
- **A mapper that refuses.** A token that verified but names something the
  application will not accept is a 401 with the same silent body.
- **A claims struct the payload does not fit.** A 401, not a 500: the token is
  not one this service can read.

## Files

| File | Role |
|---|---|
| `auth/guard.go` | the transport-neutral half: lookup, optional, idempotence |
| `auth/credential.go` | `Credential`, `Authenticator`, `ParseAuthorization`, `Bearer`, `Chain` |
| `auth/context.go` | the context key, `WithPrincipal`, `PrincipalFrom`, `Require` |
| `auth/principal.go` | `Principal`, `Role`, `Permission`, `Claims`, `RoleMap` |
| `auth/errors.go` | `ErrUnauthenticated`, `Unauthenticated` |
| `auth/apikey/apikey.go` | the shared-secret authenticator and its `Store` |
| `auth/authjwt/parser.go` | verification and the decode into C |
| `auth/authjwt/key.go` | `KeySource` — the key and the methods, inseparable |
| `auth/authjwt/jwks.go` | the fetched key set, its cache and its rate limit |
| `auth/authjwt/claims.go` | the ready-made claims and `Grant` |
| `auth/authjwt/authenticator.go` | the bridge, and `Standard` |
| `auth/http/authhttp/authhttp.go` | the shared renderer and `Refuse`, over `port/porthttp` — the same status table and envelope the CRUD bindings answer through, reached without importing CRUD ([[D-059]]) |
| `auth/http/authnet/authnet.go` | the net/http middleware |
| `auth/http/authgin/authgin.go` | the Gin middleware |
| `auth/http/authfiber/authfiber.go` | the Fiber middleware, and `SetContext` |
| `auth/http/authfiber/locale.go` | the refusal's locale, read from the header |
| `auth/rpc/authgrpc/interceptor.go` | `Unary`, `Stream`, `Skip` |
| `auth/authjwt/go.mod` | the module boundary that keeps golang-jwt off everybody else |
| `auth/http/authgin/go.mod`, `auth/http/authfiber/go.mod`, `auth/rpc/authgrpc/go.mod` | the same, per framework |
| — | `auth`, `auth/apikey`, `auth/http/authhttp` and `auth/http/authnet` have no `go.mod`: there is no dependency to keep off anybody |
| `port/porthttp/render.go`, `:errors.go` | not auth's, and that is the point: the `Renderer` seam, the `RenderOption`s, the status table and `AcceptLanguage` are one implementation for every subsystem. Before [[D-059]] these were `crud/http/crudhttp`'s, so a middleware that checks a token transitively pulled the SQL repository, the predicate AST and an HTTP client to another service |

## Tests that walk this flow

- `TestAnAuthenticatedRequestReachesTheHandlerWithItsPrincipal` —
  `auth/http/authnet/middleware_test.go`, `auth/http/authgin/middleware_test.go`,
  `auth/http/authfiber/middleware_test.go`; `TestAnAuthenticatedCallReachesTheMethodWithItsPrincipal`
  — `auth/rpc/authgrpc/interceptor_test.go`. The Fiber one is the `SetContext` guard.
- `TestAnUnauthenticatedRequestIs401AndTheHandlerNeverRuns` — all three HTTP
  bindings; `TestAnUnauthenticatedCallIsRefusedAndTheMethodNeverRuns` for gRPC.
- `TestTheRefusalBodyIsTheSharedEnvelopeAndNamesNoReason` — all three;
  `TestTheRefusalCarriesUnauthenticatedAndNamesNoReason` for gRPC.
- `TestAnOptionalGuardLetsAnAnonymousRequestThrough` — all four, each with the
  subtest that a bad credential is still refused. That subtest is what makes the
  option safe rather than a hole.
- `TestADoubleInstallAuthenticatesOnce` — all four.
- `TestAnOptionalGuardStillRefusesABadCredential` — `auth/guard_test.go`.
- `TestASecondGuardDoesNotAuthenticateAgain` — `auth/guard_test.go`.
- `TestHeaderAndLookupReplaceWhereTheCredentialComesFrom` —
  `auth/guard_test.go`, with the control subtest asserting the default header is
  then *not* read, so the option is doing the work.
- `TestOnlyTheMethodsTheKeyDeclaresAreAccepted` —
  `auth/authjwt/parser_test.go`. The one that isolates the method pinning from
  golang-jwt's own key typing: PS256 and RS256 share a key type, so only the
  declared method list separates them. Verified by removing
  `jwt.WithValidMethods` and watching it fail.
- `TestAnUnsignedTokenIsRefused`, `TestAnRSAParserRefusesAnHMACTokenSignedWithItsPublicKey`,
  `TestAnHMACParserRefusesAnRSAToken` — same file, the forgery families.
- `TestARotatedKidIsPickedUp` and `TestUnknownKidsDoNotBecomeOneFetchEach` —
  `auth/authjwt/jwks_test.go`. The second is verified by removing the rate limit
  and watching twenty tokens cost twenty fetches.
- `TestASymmetricKeyInAKeySetIsNotUsable` — same file.
- `TestAStoreFailureIsNotARefusal` — `auth/apikey/apikey_test.go`.
- `TestSkipLeavesTheNamedMethodAlone` — `auth/rpc/authgrpc/interceptor_test.go`, with
  the control that every unnamed method is still authenticated.

## See also

[[FL-020]] [[FL-011]] [[FL-013]] [[FL-007]] [[FL-008]]
