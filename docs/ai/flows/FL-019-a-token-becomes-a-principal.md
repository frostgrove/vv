# FL-019 — A token becomes a principal

**Entry point:** `auth/http/authnet/authnet.go:Middleware`, `auth/http/authgin/authgin.go:Middleware`, `auth/http/authfiber/authfiber.go:Middleware` and `auth/rpc/authgrpc/interceptor.go:Unary`
**Implements:** [[UC-019]] · **Governed by:** [[D-055]] [[D-056]] [[D-076]] [[D-045]] [[D-021]] [[D-044]] [[D-075]] [[D-078]] [[D-099]]

## The path

1. **`Middleware`** — `auth/http/authnet/authnet.go:11` — the binding's only
   framework-shaped decision: which handler type it is and where the context
   comes from. It resolves a renderer once and calls `Guard.Validate` at
   construction. Nil and zero-value guards fail here, before a request can call
   a nil authenticator. Gin, Fiber, Unary and Stream do the same.
2. **`Guard.Authenticate`** — `auth/guard.go:101` — everything that is not
   framework-shaped. It is handed a `func(name string) string`, which is all
   four transports have in common. The Guard reached here is a finished copy of
   a private construction draft: `auth.Option` is opaque and cannot be retained
   as a post-publication `*Guard` mutator. `Lookup` is still the low-level source
   hook ([[D-076]]), and `LookupOrRefuse` is the same hook for a source that can
   answer "more than one credential" instead of a credential ([[D-099]]).
3. **the per-guard idempotence check** — `auth/guard.go:authenticationMark` —
   the context carries an immutable chain of concrete guards and the principal
   state each installed. A consecutive repeat of the latest guard verifies
   once; a different guard reaches its authenticator. Re-entering A after B, or
   replacing the principal after A's marker, is `ErrAmbiguousGuardOrder` as an
   internal fault. Auth does not guess whether B is stronger or weaker
   ([[D-076]]).
4. **`Guard.credential`** — `auth/guard.go:159` — the configured lookup, or
   `ParseAuthorization` over the configured header. A lookup *replaces* the
   header rather than adding to it, which is why the one lookup this library
   ships falls back explicitly: `auth/http/authhttp/cookie.go:Cookie` reads a
   named cookie out of the `Cookie` header with `http.ParseCookie`, supplies the
   `Bearer` scheme no cookie carries, and reads the Authorization header when
   there is no such cookie — so a browser holding its access token in an
   HttpOnly cookie ([[D-075]]) and a native client sending a bearer are served by
   one guard. A request that presents both, or the same cookie name twice, is
   refused rather than ranked ([[D-099]]); `UnsafeCookieWinsOverAuthorization` is
   the named legacy precedence. The refusal is possible because the lookup seam
   is `auth.Lookout` — `(Credential, bool, error)` — registered with
   `LookupOrRefuse`, over which `Lookup` is the thin no-refusal wrapper. It lives
   in `authhttp` because `auth` takes no HTTP dependency ([[D-055]]); on
   `authgrpc` the metadata key is one no client sends, and the fallback is what
   keeps that guard working.
5. **`ParseAuthorization`** — `auth/credential.go:33` — splits on the first
   space, trims, and refuses a header with no space: a bare token is a scheme
   with nothing under it, and a truncating proxy produces exactly that.
6. **absent, and the fork** — `auth/guard.go:142` — no credential is
   `Unauthenticated` unless the guard is `Optional`, in which case the context
   passes through unchanged and carries no principal.
7. **`Authenticator.Authenticate`** — `auth/credential.go:24` — the provider.
   `auth/apikey/apikey.go:78` for a shared secret;
   `auth/authjwt/authenticator.go:10` for a JWT. `auth.Chain` has already copied
   its caller's variadic slice and discarded nil-like members. A member that
   answers `(nil-like Principal, nil)` is a refusal and the next member runs.
8. **`Parser.Parse`** — `auth/authjwt/parser.go:98` — verification. The methods
   come from the `KeySource` and never from the token
   (`auth/authjwt/parser.go:72`); for JWKS, `jwks.key` additionally requires the
   exact method cached beside the selected key. EC derives it from `crv`,
   Ed25519 derives `EdDSA`, and RSA requires the provider's `alg`; explicit
   `null` or empty policy members are not omission. A present malformed `kid`
   is refused rather than treated as omission. `exp` is required
   unless waived, and every credential failure collapses to one
   `Unauthenticated`. A JWKS provider or document failure carries
   `ErrKeySourceUnavailable` through instead: no credential verdict was possible
   ([[D-078]]).
9. **`decode`** — `auth/authjwt/parser.go:134` — the verified claim map becomes
   the caller's own struct, through `json.Decoder.UseNumber` so an integer claim
   stays an integer.
10. **the mapper** — the caller's `func(context.Context, C) (auth.Principal,
    error)`, or `Claims.Grant` under `authjwt.Standard`
    (`auth/authjwt/claims.go:97`), which first refuses an empty subject
    and then folds the role map in once. A custom mapper is the explicit path
    for an issuer that derives identity from another claim.
11. **a bad credential, even when optional** — `auth/guard.go:149` — the
    provider's error is returned whether or not the guard is optional. A forged
    token never becomes anonymous. A nil-like Principal with no error is also a
    refusal; it cannot mark the guard successful.
12. **`auth.WithPrincipal`** — `auth/context.go:17` — the one context key in the
    tree that carries an identity. It and `PrincipalFrom` use the shared
    interface-aware nil predicate, so a typed-nil pointer is absent rather than
    an apparent principal waiting to panic in a policy.
13. **`markAuthenticated`** — `auth/guard.go` — adds this guard to a new marker
    node after authentication succeeds and binds the marker to the principal
    state just installed. It never mutates a shared map. Earlier marks exist to
    detect and refuse ambiguous re-entry, not to infer assurance ordering.
14. **the binding writes the context back** — `r.WithContext(ctx)`,
    `c.Request = c.Request.WithContext(ctx)`, `c.SetContext(ctx)`, or the
    replaced `grpc.ServerStream`. This is the step with a wrong answer that
    compiles; see the table below.
15. **`Guard.refuse`** — `auth/refusal.go` — every refusing path above goes
    through it: it hands each registered `auth.Observer` a `Reason` — a kind from
    a closed vocabulary, a `Detail` written in `auth`, and the error the caller
    is about to get — and returns that error unchanged. Registered with
    `auth.Observe` at construction, so no binding decides for itself what to do
    with a cause nobody asked it about ([[D-056]]). It carries no credential, it
    runs on the request's goroutine, and `auth.Sampled` is the decorator for a
    surface under a stuffing run.
16. **`authhttp.Refuse`** — `auth/http/authhttp/authhttp.go:28` — on the refusing
    path, the status, the headers and the envelope, written here rather than
    deferred.

## Where the bindings differ

| | context in | context out | refusal |
|---|---|---|---|
| `authnet` | `r.Context()` | `r.WithContext(ctx)` | `authhttp.Refuse`, handler not called |
| `authgin` | `c.Request.Context()` | `c.Request = c.Request.WithContext(ctx)` | `c.Error(err)`, `authhttp.Refuse`, `c.Abort()` |
| `authfiber` | `c.Context()` | `c.SetContext(ctx)` — **not `Locals`** | `authfiber.refuse`, `Response().Header.Add` then `c.Status().JSON()` |
| `authgrpc` | `ss.Context()` / call ctx | returned ctx, or a wrapping `ServerStream` | error returned; `crudgrpc.Errors` renders it |

The Fiber row is the one that matters. `Locals` is where a Fiber middleware
normally puts per-request state and it is the wrong place here: `crudfiber`
hands `c.Context()` to the port layer, so a principal in `Locals` is invisible
to every policy. Both spellings compile and both look right in review.
`TestAnAuthenticatedRequestReachesTheHandlerWithItsPrincipal` in
`auth/http/authfiber/middleware_test.go` is what fails when it is wrong — verified by
changing `SetContext` to `Locals` and watching it.

The refusal row hides a second one. `authhttp.Refuse` *adds* each rendered header
value; Fiber's `Ctx.Set` overwrites, so writing the rendered map through it kept
only the last value of a repeated header — a 401 offering two challenges arrived
offering one, and nothing about the response looked wrong. `refuse` therefore
writes through `c.Response().Header.Add`, and all three bindings carry
`TestARefusalCarriesEveryHeaderTheRendererAskedFor` over their own response
writer.

### The preflight the guard must not answer, and must not route either

A browser sends `OPTIONS` with no credential before a cross-origin write. A guard
mounted ahead of the CORS middleware refuses it, the request it was asking about
never happens, and the failure reads as a CORS misconfiguration.
`authnet.AnswerPreflight`, `authgin.AnswerPreflight` and
`authfiber.AnswerPreflight` decorate the middleware rather than changing it:
`authhttp.Preflight` says yes only to `OPTIONS` carrying an `Origin` and an
`Access-Control-Request-Method` and no `Authorization`, and everything else, an
`OPTIONS` with a credential included, goes through the guard. A match does not
continue down the chain — it goes to the handler the consumer named, which
answers it and ends the request; on Gin the wrapper calls `Abort` after it,
because a Gin middleware that merely returns lets the route run. That is
[[D-103]]: both headers the predicate reads are the client's to set, so a
continuing preflight is an unauthenticated `OPTIONS` reaching whatever hand-written
handler the router has for that path.

`SkipPreflight` is the same wrapper with nobody named to answer, kept because
mounting is meant to be one statement: it answers `authhttp.PreflightStatus`
(`204`) and no `Access-Control-Allow-*` header, so a consumer who has not said
who answers CORS gets a browser-visible CORS failure rather than an open door.
Ordering CORS ahead of auth does the same job where the chain is the consumer's
to order.

`authgrpc` writes no status. The refusal is an error carrying
`errs.KindUnauthorized`, and `crudgrpc.Errors` is what turns it into
`UNAUTHENTICATED` — so the gRPC binding has no status table of its own and
[[D-008]]'s ordering is untouched.

### What each binding tests that the others cannot

`make check-triplets` holds `authnet`, `authgin` and `authfiber` to the same test
names, and exempts `binding_test.go` — which is where a difference goes, named.
The check found the first two rows on the day it was written; neither had
been recorded anywhere.

| Binding | What lives in its `binding_test.go` | Why it cannot be mirrored |
|---|---|---|
| `authnet` | `TestARefusalIsNotRenderedTwiceUnderTheErrorMiddleware` | It composes the auth middleware under `crudnet.Errors`, which needs both halves of the stack in one test binary. On net/http both are in the root module. Doing it for Gin would mean `authgin`'s tests requiring `crudgin`, and [[D-051]] is the rule against exactly that — a consumer mounting auth on Gin must not be made to take the CRUD binding, and a test dependency is still a dependency in the graph they resolve. The behaviour is the same on all three; only one of them can say so. |
| `authgin` | `TestTheCauseIsFiledWithGinsErrorBag` | Gin carries an error bag on the context that its own logging middleware reads. net/http and Fiber have no equivalent, so there is nothing to mirror it to. |
| `authnet` | `TestATypedNilPreflightAnswerIsStillNothingToAnswerWith` | The preflight answer is an `http.Handler` here and a func type on Gin and Fiber. A nil func compares equal to nil; a nil `*T` in an interface does not, so only this binding has a typed nil to refuse, and it refuses it with `internal/nilvalue`, the typed-nil predicate `auth`'s context and observer seams already use. |

`authfiber` has no `binding_test.go`, and that is the honest state: everything it
does differently is the `SetContext` row above, which every binding's copy of
`TestAnAuthenticatedRequestReachesTheHandlerWithItsPrincipal` already covers.

## Where the decisions bite

- [[D-055]] — the principal only ever travels in the context, and `auth`
  imports nothing below `errs`.
- [[D-076]] — a marker proves only that its exact guard ran; a different guard
  re-authenticates, bare API-key headers are explicit, and invalid sources fail
  while the graph is built.
- [[D-056]] — the refusal is a fault wrapping a sentinel, and its reason is in
  the wrapped error because `port.Violations` renders `Fault.Message`.
- [[D-045]] — `Guard` takes a header-getter rather than a request, which is what
  lets the gRPC interceptor share every decision above step 13.
- [[D-021]] — a nil or typed-nil authenticator/store, an empty header or scheme,
  a nil lookup, a key source that verifies nothing and an unconfigured issuer or
  audience all panic at construction.
- [[D-044]] — no body names anything internal; `authjwt` does not echo the
  caller's own `kid` back at them.
- [[D-078]] — HMAC pins one algorithm and minimum key size; JWKS trust,
  per-key methods, stale-on-error and outbound refresh are bounded; strict
  Ed25519/RSA declarations fail before traffic; provider failure is not 401;
  the ready-made principal has a subject.

## Failure modes

- **No credential.** `auth.ErrUnauthenticated`, 401, handler not run — unless
  optional, in which case the chain proceeds with no principal and the
  repository refuses instead.
- **Two credentials.** One source carrying two values, a cookie beside an
  Authorization header, or two cookies of the configured name: a 401 wrapping
  `ErrCredentialCardinality`, and the authenticator is not reached. An optional
  guard refuses it too — an ambiguous credential is a bad credential, not an
  absent one ([[D-099]]).
- **A credential that does not verify.** The same 401, the same body, whether it
  was forged, expired, for another audience, or names an unknown key.
- **A key set that cannot be reached or parsed.** `authjwt.JWKS` returns
  `ErrKeySourceUnavailable`, which remains an infrastructure failure rather
  than becoming 401. A cached set inside an explicit `JWKSServeStaleFor` window
  may be used while its typed degraded descriptor is delivered outside the
  completed singleflight; observer re-entry, blocking and panic do not park
  requests. The stale bound restores the unavailable answer.
- **A key the provider withdrew.** A hit at `JWKSFreshness` lazily refreshes the
  whole set. After a successful refresh the removed `kid` is the same silent
  401 as any other unknown key.
- **An empty or duplicate key id.** The fetched document is rejected as a
  provider failure and never partially installed.
- **A JWK method/operation mismatch or weak point/modulus.** That entry is not
  installed. If nothing usable remains, readiness and parsing report provider
  unavailability. Explicit JSON `null` or empty `alg`/`key_ops` values do not
  waive the declaration. A token selecting another method for a fresh installed
  key is the same silent 401 as a bad signature; when that method metadata is
  stale, the set refreshes before deciding, and refresh failure remains typed
  provider unavailability.
- **A present malformed token key id.** Empty, non-string and structured `kid`
  values are silent 401s even if the set has one key; only actual omission uses
  the sole-key rule.
- **A key store that cannot be reached.** `apikey.Store`'s third result travels
  unchanged, so an outage renders as the 500 it is rather than telling every
  caller their key is wrong.
- **A mapper that refuses.** A token that verified but names something the
  application will not accept is a 401 with the same silent body.
- **An extension answers a nil-like identity.** Guard and Chain turn it into an
  authentication refusal; context helpers and the principal quantifiers also
  treat it as absent, without invoking its methods.
- **A Guard re-enters after another successful Guard.** The handler does not
  run; the internal fault wraps `ErrAmbiguousGuardOrder`, not
  `ErrUnauthenticated`. A -> B mounted once is cumulative; alternative
  credentials belong in one `Chain`.
- **A transport receives nil or `new(auth.Guard)`.** Its constructor panics on
  `Validate`; direct low-level Authenticate fails with `ErrGuardNotReady`
  rather than panicking on traffic.
- **A claims struct the payload does not fit.** A 401, not a 500: the token is
  not one this service can read.

## Files

| File | Role |
|---|---|
| `auth/guard.go` | lookup, optional, ready validation, adjacent idempotence and ambiguous re-entry refusal |
| `auth/credential.go` | `Credential`, `Authenticator`, `ParseAuthorization`, `Bearer`, snapshotted `Chain` |
| `auth/context.go` | the context key and nil-like-safe `WithPrincipal`, `PrincipalFrom`, `Require` |
| `auth/principal.go` | `Principal`, nil-like-safe quantifiers, `Role`, `Permission`, `Claims`, `RoleMap` |
| `internal/nilvalue/nilvalue.go` | the shared typed-nil predicate used by auth seams |
| `auth/errors.go` | `ErrUnauthenticated`, `Unauthenticated`, `AmbiguousCredential` and the cardinality refusal built from it |
| `auth/refusal.go` | `Reason`, `ReasonKind`, `Observer`, `ObserverFunc`, `Observe`, `Sampled`, `Guard.refuse` — where the reason goes, since it does not go in the body |
| `auth/http/authhttp/preflight.go` | `Preflight`, `HeaderOrigin`, `HeaderRequestMethod`, `PreflightStatus` — what a CORS preflight looks like, and what answers one nobody was named for, once, for the three bindings |
| `auth/http/authhttp/cookie.go` | the cookie lookup, its refusal of a second credential, and the named legacy precedence |
| `auth/apikey/apikey.go` | the shared-secret authenticator and its `Store` |
| `auth/authjwt/parser.go` | verification and the decode into C |
| `auth/authjwt/key.go` | `KeySource` — the key and the methods, inseparable |
| `auth/authjwt/jwks.go` | the fetched key set, its cache and its rate limit |
| `auth/authjwt/claims.go` | the ready-made claims and `Grant` |
| `auth/authjwt/authenticator.go` | the bridge, and `Standard` |
| `auth/http/authhttp/authhttp.go` | the shared renderer and `Refuse`, over `port/porthttp` — the same status table and envelope the CRUD bindings answer through, reached without importing CRUD ([[D-059]]) |
| `port/log.go` | `port.Logger` — where the two lines `Refuse` writes go when the refusal itself cannot be encoded or written. The application's logger, never the process-wide one ([[D-062]]) |
| `auth/http/authnet/authnet.go` | the net/http middleware |
| `auth/http/authgin/authgin.go` | the Gin middleware |
| `auth/http/authfiber/authfiber.go` | the Fiber middleware, `SetContext`, and the refusal written with `Header.Add` |
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
- `TestDifferentGuardsAuthenticateIndependently` — all three HTTP bindings and
  gRPC. It is the control on the idempotence optimisation: a principal from one
  guard cannot bypass a second guard.
- `TestARefusalCarriesEveryHeaderTheRendererAskedFor` — `auth/http/authhttp/refuse_test.go`
  for the shared writer, and once per binding in `auth/http/authnet/refuse_test.go`,
  `auth/http/authgin/refuse_test.go` and `auth/http/authfiber/refuse_test.go`, each
  over the response writer that binding actually holds. The Fiber one is the
  `Ctx.Set` guard: with `Set` in place of `Add` it reports one challenge where
  the renderer asked for two.
- `TestARequestPresentingTwoCredentialsIsRefusedRatherThanRanked` and
  `TestTheLegacyCookiePrecedenceIsAvailableOnlyByNamingIt` —
  `auth/http/authhttp/cookie_test.go`, with
  `TestACookieLookupStillReadsTheAuthorizationHeader` as the control that the
  fallback still works.
- `TestARefusalWithNoBodyIsTheStatusAndNothingElse`,
  `TestARefusalThatWillNotEncodeIs500AndSaysNothing`,
  `TestARefusalIsRenderedInTheLanguageTheRequestAskedFor` and
  `TestRendererForKeepsOneRendererForTheOrdinaryCase` —
  `auth/http/authhttp/refuse_test.go`. The branches of the shared writer that no
  binding's middleware test reaches, because a binding is handed the default
  renderer and these need one that answers a second challenge, no body, or a
  body the encoder refuses.
- `TestEveryRefusalReachesTheObserverWithTheReasonTheCallerNeverSees` and
  `TestASampledObserverSeesOneRefusalInEveryRun` — `auth/observer_test.go`. The
  first asserts the successful request is *not* observed and that the presented
  token is nowhere in what the observer was handed.
- `TestACorsPreflightIsAnsweredByTheHandlerNamedForItAndABareOptionsIsNot` — all
  three HTTP bindings, each with the two controls: a bare `OPTIONS` is still
  refused, and an `OPTIONS` carrying a credential is authenticated like any other
  request and does reach the route. The preflight arm asserts the route did *not*
  run, which is what [[D-103]] rests on.
- `TestAPreflightNobodyAnsweredStopsAtTheDoorInsteadOfAtTheRoute` — all three,
  over `SkipPreflight`: a forged `Origin` and `Access-Control-Request-Method`
  answer `204` and reach no handler.
- `TestAPreflightAnswerThatIsNotThereRefusesToStart` — all three; a nil answer is
  a wiring mistake and is refused at construction, like a nil guard.
- `TestAnOptionalGuardStillRefusesABadCredential` — `auth/guard_test.go`.
- `TestASecondGuardDoesNotAuthenticateAgain` — `auth/guard_test.go`.
- `TestADifferentGuardAuthenticatesAgain` — `auth/guard_test.go`; the final
  A -> B control is paired with
  `TestAReenteredGuardAfterAnotherIdentityBoundaryFailsClosed`, which pins both
  possible assurance readings of A -> B -> A.
- `TestHeaderAndLookupReplaceWhereTheCredentialComesFrom` —
  `auth/guard_test.go`, with the control subtest asserting the default header is
  then *not* read, so the option is doing the work.
- `TestAnEmptyCredentialSourceRefusesToStart` — `auth/guard_test.go`.
- `TestNewGuardDoesNotPublishTheOptionDraft` — `auth/guard_internal_test.go`;
  the retained draft is mutated concurrently with authentication.
- `TestChainSnapshotsTheVariadicSlice` and the typed-nil member/principal arms
  in `auth/credential_test.go`.
- `TestANilPrincipalIsNotStored` and the typed-nil quantifier controls in
  `auth/context_test.go` and `auth/principal_test.go`.
- `TestStaticSnapshotsClaimsAndReturnsAPerRequestCopy`,
  `TestStaticClaimsAreIndependentAcrossConcurrentRequests`, and
  `TestStaticDoesNotRetainTheMutableClaimsDeclaration` in
  `auth/apikey/apikey_test.go`.
- `TestGuardValidateRejectsNilAndZeroValuesBeforeARequest` and each transport's
  nil/zero constructor cases.
- `TestADoubleStreamInstallAuthenticatesOnce`,
  `TestDifferentStreamGuardsAuthenticateIndependently`, and
  `TestAReenteredStreamGuardFailsClosedWithoutGuessingAssurance` cover the gRPC
  Stream context-replacement path separately from Unary.
- `TestOnlyTheMethodsTheKeyDeclaresAreAccepted` —
  `auth/authjwt/parser_test.go`. The one that isolates the method pinning from
  golang-jwt's own key typing: PS256 and RS256 share a key type, so only the
  declared method list separates them. Verified by removing
  `jwt.WithValidMethods` and watching it fail.
- `TestAnUnsignedTokenIsRefused`, `TestAnRSAParserRefusesAnHMACTokenSignedWithItsPublicKey`,
  `TestAnHMACParserRefusesAnRSAToken` — same file, the forgery families.
- `TestLowOrderEd25519TrustWouldAcceptAUniversalJWTForgery` —
  `auth/authjwt/key_test.go`, proving the identity-point signature before the
  safe constructor rejects its trust key.
- `TestARotatedKidIsPickedUp` and `TestUnknownKidsDoNotBecomeOneFetchEach` —
  `auth/authjwt/jwks_test.go`. The second is verified by removing the rate limit
  and watching twenty tokens cost twenty fetches.
- `TestAFailingProviderIsStillOnlyFetchedOnce` and
  `TestAConcurrentBurstOfMissesIsOneFetch` — same file, the two holes the
  sequential test above cannot see. The limit used to be armed by a *successful*
  fetch, so it did nothing while the provider was down; and the lock is dropped
  across the HTTP call, so a concurrent burst all passed the check before any of
  it recorded an attempt. Removing the two guards makes them report twenty and
  twenty-four fetches respectively.
- `TestASymmetricKeyInAKeySetIsNotUsable` — same file.
- `TestHMACRefusesShortSecretsAtDeclaration` and
  `TestEachHMACConstructorPinsOneAlgorithm` — `auth/authjwt/parser_test.go`.
- `TestARetiredCachedKidStopsVerifyingAtTheFreshnessBoundary`,
  `TestAStaleCacheDoesNotHideAnOutageByDefault` and
  `TestStaleOnErrorIsBoundedAndSignalsTheDegradedDecision` —
  `auth/authjwt/jwks_test.go`, with a fake clock and server.
- `TestAKeySetThatCannotBeReachedIsUnavailableAndNotARefusal`,
  `TestAnEmptyOrDuplicateKidRefusesTheWholeKeySet` and
  `TestANonPositiveMinRefreshRefusesToStart` — same file.
- `TestJWKSMethodsAndOperationsBelongToEachKey`,
  `TestJWKSRejectsLowOrderEd25519AndMalformedTokenKids`,
  `TestDegradedObserverCannotHoldOrReenterTheSingleflight` and
  `TestWarmRefusesAStaleCacheWhileParseMayUseItsExplicitWindow` — same file.
- Each transport's key-provider-outage test — 500 rather than 401 on the HTTP
  triplet, and the original typed error from gRPC, with no handler invocation.
- `TestStandardRefusesATokenWithoutASubject` —
  `auth/authjwt/claims_test.go`, including the explicit custom-mapper control.
- `TestAStoreFailureIsNotARefusal` — `auth/apikey/apikey_test.go`.
- `TestHeaderReadsABareKeyWithoutChangingTheAuthorizationAPI`,
  `TestANilStoreRefusesToStart`, `TestAnEmptyBareHeaderRefusesToStart` and the
  empty-scheme arms of `TestTheSchemeIsCheckedUnlessItIsWaived` —
  `auth/apikey/apikey_test.go`.
- `TestSkipLeavesTheNamedMethodAlone` — `auth/rpc/authgrpc/interceptor_test.go`, with
  the control that every unnamed method is still authenticated.
- `TestARefusalIsNotRenderedTwiceUnderTheErrorMiddleware` —
  `auth/http/authnet/binding_test.go`, and only there; the table above says why.
- `TestTheCauseIsFiledWithGinsErrorBag` — `auth/http/authgin/binding_test.go`,
  and only there.

## See also

[[FL-020]] [[FL-011]] [[FL-013]] [[FL-007]] [[FL-008]] [[D-051]] [[D-062]]
