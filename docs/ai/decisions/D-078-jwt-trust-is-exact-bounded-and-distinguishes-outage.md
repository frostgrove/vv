# D-078 — JWT trust is exact, bounded, and distinguishes an outage

**Status:** accepted
**Invariant:** a static key source pins its algorithm, validates its security
properties and snapshots caller-owned material at declaration; a remote JWKS
trust set pins an exact method to every usable key, has a finite freshness age
and never resolves ambiguous key identifiers; inability to obtain trust
material is a typed infrastructure failure, not an authentication refusal. The
ready-made principal has a non-empty subject.

## The decision

The short path is safe without extra declarations:

- `HMAC(secret)` means exactly HS256 and requires at least 32 bytes.
  `HMAC256`, `HMAC384` and `HMAC512` name the algorithm explicitly and require
  32, 48 and 64 bytes respectively. A short secret panics where it is wired.
- `RSA`, `ECDSA` and `EdDSA` reject malformed or weak public keys where they are
  declared, then deep-copy their mutable numbers or bytes. RSA requires a
  composite odd modulus from 2048 through 16384 bits, coprime to a sane odd
  exponent; ECDSA accepts only P-256/P-384/P-521 and an on-curve point, and pins
  the one algorithm for that curve; Ed25519 requires an exactly 32-byte,
  canonical on-curve point that is not low-order. `Custom` remains the explicit
  caller-owned escape hatch.
- JWKS applies the same RSA strength and exponent checks as `RSA`; a weak entry
  is unusable rather than a weaker remote route into otherwise forbidden trust.
- A usable JWKS key carries one method. EC derives it from `crv`, Ed25519 derives
  `EdDSA`, and a RSA JWK must name one supported `alg` because its modulus alone
  cannot choose hash or padding. A present `alg` must agree with the key, and a
  present `key_ops` must include `verify`. The token never widens that choice.
- `JWKS` refreshes a successfully fetched set after `JWKSFreshness`, five
  minutes by default, even when the presented `kid` is already cached. A
  successful refresh replaces the whole map, so a withdrawn key stops
  verifying without a restart or unrelated cache miss.
- `JWKSMinRefreshEvery` accepts only a positive duration. Removing the outbound
  request bound is possible only through `UnsafeJWKSNoMinRefresh`.
- Removing key-withdrawal freshness is possible only through
  `UnsafeJWKSNoFreshness`. This is the deliberately unsafe compatibility path;
  there is no innocent-looking zero duration that selects it.
- Every JWKS entry must carry a non-empty `kid`, and no two entries may carry
  the same one. Either defect rejects the whole fetched document before it can
  replace a known-good cache. A token may omit `kid` only for a set with one
  usable key; a present empty, non-string or otherwise malformed value is a
  credential refusal rather than omission.
- A fetch, response or JWKS-document failure is marked with
  `ErrKeySourceUnavailable`. `Parser.Parse` preserves that type rather than
  folding it into `auth.ErrUnauthenticated`. An unknown `kid` after a successful
  fetch is still a credential refusal and says nothing about the `kid`.
- Stale-on-error is off by default. `JWKSServeStaleFor` enables it only for a
  positive, finite window and requires a `JWKSDegradedObserver`; the typed
  descriptor is queued only after the completed singleflight releases every
  waiter. Observer calls are deadline-bound, panic-safe, reentrant-safe,
  serialised and coalesced; request availability never depends on callback
  completion. At the exact end of the window the outage wins and no cached key
  is used.
- A JWKS fetch is detached singleflight work bounded by `JWKSFetchTimeout`.
  Initiators and waiters each stop waiting on their own context without
  cancelling the shared fetch. Caller cancellation remains that exact context
  error; a timeout or transport failure of the detached fetch is a provider
  outage and may use an explicitly bounded stale key.
- `Parser.Warm(ctx)` fetches and validates JWKS before readiness is announced.
  Static sources are already validated, so warming them is a no-op. A stale
  cache is not sufficient for readiness when its provider cannot refresh it.
- `Standard` refuses a missing, empty or whitespace-only `sub`. A deployment
  whose issuer identifies callers another way uses the existing generic
  `Authenticator` mapper and derives the subject explicitly.

This narrows [[D-056]]. Credential verification failures still collapse to one
silent 401. Failure to perform verification because the configured trust
provider is unavailable is not a credential verdict and therefore does not.

## Why

An HMAC verifier is also a signer. Accepting an empty or short secret turns a
missing configuration value into a key an attacker can reproduce, and accepting
the HS256/384/512 family lets the token choose more of the verification contract
than the deployment did. The declaration has all information needed to reject
both before the process serves traffic, which is [[D-021]]'s boundary.

Public asymmetric keys are mutable Go values. Keeping the caller's pointer or
slice would let an unrelated post-startup mutation change the parser's trust
anchor and race verification. Strength and point validation are configuration
checks, not request work; normalising and snapshotting them at declaration is
the same fail-fast boundary as the HMAC length check.

Length is not point validation. The Edwards25519 identity is a valid-looking
32-byte encoding for which `R=identity,S=0` verifies every message under the Go
verifier; accepting it turns a public trust declaration into a universal JWT
forgery. Canonical decoding and the cofactor check therefore belong on both the
static and JWKS paths. RSA has the analogous obvious-invalid cases: a prime or
even modulus exposes its factorisation, a modulus sharing the exponent is not a
valid RSA group, and an unbounded remote modulus turns verification into
attacker-triggered big-integer work. The 16384-bit ceiling is deliberately far
above ordinary 2048/3072/4096-bit deployments while putting a finite CPU bound
on one key.

A JWK is also a policy declaration, not just coordinates. `kty` narrows the Go
key type but does not always narrow the JWT method: the ECDSA dependency's
verification path accepts the curve carried by the key and does not compare it
with the method's nominal curve size. Keeping `alg`/curve policy beside each
cached key prevents a token header from making that choice.

A JWKS cache without an age is not a cache policy; it is permanent trust. It
makes provider-side key withdrawal conditional on a restart or on traffic for
some other key. Five minutes gives the default path a bounded answer while the
one-minute request floor still prevents attacker-selected `kid` values from
becoming attacker-paced provider requests.

Freshness and outage pull in opposite directions. Silently serving stale keys
forever hides both a provider outage and a compromised-key withdrawal. Turning
every stale refresh failure into a 401 tells a caller that valid credentials are
bad. The default therefore returns a typed infrastructure failure. A deployment
that deliberately values availability more may buy a finite stale interval, but
the bound and its operational signal are one declaration and cannot drift
apart.

A remote fetch belongs to the cache, not to whichever HTTP request happened to
miss first. Binding shared work to that request makes one disconnected client
cancel every waiter; making the initiator run the work synchronously makes it
ignore its own cancellation. A detached, bounded worker plus per-waiter context
selection gives both lifetimes an owner. The degraded observer is extension
code and is deliberately outside the flight before it is invoked: it can
re-enter the parser, panic or block past its one-second context without parking
requests. One delivery loop coalesces a newer descriptor while an earlier call
is still running, bounding even a callback that ignores cancellation to one
goroutine. Readiness uses the same path rather than manufacturing a token merely
to prime the cache.

The subject rule belongs on `Standard`, not on the generic parser. A parser is
allowed to decode the issuer's own shape, including one where identity is held
in another claim. The ready-made principal is different: its subject is the key
used by audit and subject scopes, so a permission-bearing principal with no key
is an invalid standard identity.

## What it forbids

- Do not make `HMAC` accept an algorithm family or a secret shorter than the
  selected hash output.
- Do not retain a caller-owned asymmetric key or accept RSA outside 2048..16384
  bits, an even/prime/non-coprime modulus, an implausible exponent, an
  unsupported/off-curve EC key, or a non-canonical/low-order Ed25519 point
  through either the static or JWKS path.
- Do not discard a JWK's `alg` or `key_ops`, infer a RSA algorithm from the token,
  or let an EC token select a method other than the one its curve determines.
- Do not add a safe-looking legacy HMAC or JWKS constructor whose name hides
  weaker verification or unbounded trust.
- Do not let a zero or negative refresh interval disable the unknown-`kid`
  request bound.
- Do not install a partial JWKS when any entry has an empty or duplicate `kid`.
- Do not treat a present malformed token `kid` as though the header omitted it.
- Do not turn `ErrKeySourceUnavailable` into `auth.ErrUnauthenticated`, including
  on rate-limited retries and concurrent waiters.
- Do not bind shared JWKS work to one waiter's cancellation, or hide a caller's
  own cancellation behind stale trust or an unavailable error.
- Do not serve stale trust material without both a finite maximum and the typed
  degraded observer, or at and beyond that maximum; observer extension code
  must never own or block the fetch singleflight.
- Do not make every generic claims struct carry `sub`; the strict subject rule
  is the ready-made `Standard` contract and the mapper remains the escape hatch.

## Where it lives

| File | What it holds |
|---|---|
| `auth/authjwt/key.go` | exact algorithms, declaration-time validation and immutable static-key snapshots |
| `auth/authjwt/jwks.go` | bounded detached singleflight, freshness, stale policy, document/key validation and `ErrKeySourceUnavailable` |
| `auth/authjwt/parser.go` | `Warm` and the split between provider failure, caller cancellation and credential refusal |
| `auth/authjwt/authenticator.go` | the non-empty subject rule on `Standard` |

## Proven by

- `TestHMACRefusesShortSecretsAtDeclaration` and
  `TestEachHMACConstructorPinsOneAlgorithm` in
  `auth/authjwt/parser_test.go`.
- `TestStaticAsymmetricKeysAreValidatedAtDeclaration` and
  `TestStaticAsymmetricKeysAreSnapshottedAtDeclaration` plus
  `TestLowOrderEd25519TrustWouldAcceptAUniversalJWTForgery` in
  `auth/authjwt/key_test.go`.
- `TestARetiredCachedKidStopsVerifyingAtTheFreshnessBoundary`,
  `TestAStaleCacheDoesNotHideAnOutageByDefault` and
  `TestStaleOnErrorIsBoundedAndSignalsTheDegradedDecision` in
  `auth/authjwt/jwks_test.go`.
- `TestAKeySetThatCannotBeReachedIsUnavailableAndNotARefusal` and
  `TestAFailingProviderIsStillOnlyFetchedOnce` in the same file.
- `TestAConcurrentFailedRefreshSharesOneErrorAndOneFetch`,
  `TestAZeroOriginClockStillRecordsFetchAndAttemptState`,
  `TestDetachedFetchTimeoutCanUseTheExplicitStaleWindow` and
  `TestWarmFetchesAndValidatesJWKSBeforeTraffic` in the same file.
- `TestAnEmptyOrDuplicateKidRefusesTheWholeKeySet` and
  `TestANonPositiveMinRefreshRefusesToStart` in the same file.
- `TestJWKSMethodsAndOperationsBelongToEachKey`,
  `TestJWKSRejectsLowOrderEd25519AndMalformedTokenKids`,
  `TestDegradedObserverCannotHoldOrReenterTheSingleflight` and
  `TestWarmRefusesAStaleCacheWhileParseMayUseItsExplicitWindow` in the same
  file.
- `TestAKeyProviderOutageIsInfrastructureAndTheHandlerNeverRuns` in the three
  HTTP bindings and `TestAKeyProviderOutageRemainsTypedAndTheMethodNeverRuns`
  in `authgrpc`.
- `TestStandardRefusesATokenWithoutASubject` in
  `auth/authjwt/claims_test.go`.

## See also

[[D-021]] [[D-044]] [[D-055]] [[D-056]] [[D-062]] [[FL-019]] [[UC-019]]
