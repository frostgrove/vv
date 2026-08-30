# authjwt — verify a JWT, into your own claims struct

```go
import "github.com/frostgrove/vv/auth/authjwt"
```

**Module:** `github.com/frostgrove/vv/auth/authjwt` — one dependency,
`github.com/golang-jwt/jwt/v5`
· **Depends on:** `auth`, golang-jwt/v5

Generic over the claims. Give it your struct and it is the only thing you ever
see — no embedded type of ours, none of golang-jwt's.

---

## What you get

| | |
|---|---|
| `New[C]`, `Parser[C].Warm`, `Parser[C].Parse` | readiness and verification, into your own type |
| `HMAC`, `HMAC256/384/512`, `RSA`, `ECDSA`, `EdDSA`, `JWKS`, `Custom` | key sources, each pinning the methods it verifies |
| `Issuer`, `Audience`, `Leeway` | what is checked |
| `AllowAnyIssuer`, `AllowAnyAudience`, `AllowNoExpiry` | how to say a check is deliberately skipped |
| `Authenticator` | the bridge to `auth.Principal` |
| `Claims`, `Standard` | the ready-made claims type, and both calls in one |

## The parser

```go
type MyClaims struct {
	Subject string `json:"sub"`
	Tenant  int64  `json:"tenant"`
	Scope   string `json:"scope"`
}

parser := authjwt.New[MyClaims](
	authjwt.HMAC(secret),
	authjwt.Issuer("https://id.example.com"),
	authjwt.Audience("articles-api"),
)

claims, err := parser.Parse(ctx, token)
```

Nothing above mentions `auth.Principal`. **A consumer who wants a JWT parser and
none of the rest of this library stops here** — that is why the parser and the
bridge are two calls.

What it costs: one map-to-JSON-to-struct hop after verification. The registered
claims are validated by golang-jwt against the map, and only then is the payload
shaped into `C`. That hop is where the freedom comes from.

An integer claim stays an integer. Decoded the ordinary way every number arrives
as a `float64`, and a tenant id read back then compiles into a float in the
`WHERE` clause of every scoped query.

## Key sources pin the algorithm

A `KeySource` carries the key **and** the methods it can verify, and they cannot
be separated:

| | verifies |
|---|---|
| `HMAC(secret)`, `HMAC256(secret)` | HS256, at least 32 secret bytes |
| `HMAC384(secret)` | HS384, at least 48 secret bytes |
| `HMAC512(secret)` | HS512, at least 64 secret bytes |
| `RSA(pub)` | RS256/384/512, PS256/384/512 |
| `ECDSA(pub)` | ES256 for P-256, ES384 for P-384, or ES512 for P-521 |
| `EdDSA(pub)` | EdDSA |
| `JWKS(url)` | every asymmetric method above |
| `Custom(methods, keyfunc)` | whatever you list — the escape hatch |

A key alone would leave the token's own `alg` header to decide how it is used,
which is the key-confusion forgery: hand an RS256 verifier a token signed HS256
and it verifies using the RSA *public* key as an HMAC secret — so anybody who
can read a public key, which is everybody, can mint tokens. `alg: none` is the
same family.

`Custom` takes the method list as a separate argument rather than as a field, so
an unpinned parser is visible in a diff. Never derive the list from the token.

`HMAC` is the short safe default: exactly HS256, not an algorithm family. A
short secret panics where the source is declared. The explicit constructors are
for an issuer that chose another HMAC algorithm; a token never upgrades that
choice through its own `alg` header.

Static asymmetric declarations are checked and snapshotted at the same point.
RSA requires an odd composite modulus from 2048 through 16384 bits, coprime to a
sane odd exponent; the upper bound prevents a remote-sized big integer from
becoming verification CPU work. ECDSA requires an on-curve P-256, P-384 or
P-521 point. Ed25519 is strictly decoded as a canonical curve point and
low-order points are refused — length alone is unsafe because the identity
public key admits a universal `R=identity,S=0` forgery. The parser retains deep
copies, so mutating the caller's `big.Int` or byte slice later cannot change
trust or race verification. `Custom` is the explicit route for a caller-owned
verification scheme and therefore owns its validation and lifetime too.

## What a caller cannot forget

The constructors **panic at construction** on declarations that would
over-trust, because a process that does not start is the better outcome
([[D-021]]):

- a key source that verifies nothing;
- an HMAC secret shorter than the selected algorithm requires;
- a malformed or weak static RSA, ECDSA or Ed25519 public key;
- no `Issuer` and no `AllowAnyIssuer()`;
- no `Audience` and no `AllowAnyAudience()`.

Each is fixed by one line at the call site. The waivers exist so that skipping a
check is a deliberate, greppable act rather than an omission — an unaudienced
token is replayable against every other service that trusts the same issuer.

`exp` is required unless `AllowNoExpiry()`. A token with no expiry is a
credential that never stops working: one leak is permanent, and revoking it
means rotating the signing key for everybody.

`Leeway` defaults to none. Thirty seconds is the usual production setting for
two clocks that may not be synchronised.

## JWKS

```go
authjwt.JWKS("https://id.example.com/.well-known/jwks.json",
	authjwt.JWKSClient(&http.Client{Timeout: 3 * time.Second}),
)
```

For readiness rather than first-request lazy loading:

```go
parser := authjwt.New[MyClaims](authjwt.JWKS(jwksURL),
	authjwt.Issuer(issuer), authjwt.Audience(audience))
if err := parser.Warm(startupCtx); err != nil {
	return err
}
```

`Warm` fetches and validates the document through the same cache path as
`Parse`. It is a no-op for already validated static sources. A stale set whose
provider cannot refresh does not pass readiness, even when request traffic has
an explicit stale-on-error window.

RSA, EC (P-256/384/521) and Ed25519 keys are read; an `oct` entry is ignored,
because a key set is a public document and an HMAC entry in one is a shared
secret published to the internet. An entry with `use` set to anything but `sig`
is ignored too. A present `key_ops` must be a real string array containing
`verify`; JSON `null`, null members and an empty array are not omission. One
unusable entry does not cost the whole set. Every entry must still have a
non-empty `kid`, and
duplicate ids reject the whole document: trust is never chosen by JSON array
order. Remote RSA and Ed25519 keys obey the same strength and point rules as
their static constructors; publishing a key through JWKS is not a weaker path.

Every cached key also owns exactly one JWT method. EC derives it from `crv` and
Ed25519 derives `EdDSA`; a RSA JWK must carry one supported `alg`, because the
modulus cannot choose hash and padding. When `alg` is present on EC or Ed25519,
it must be a non-null, non-empty match for the derived method. Only an absent
member means unspecified. This per-key check is separate from the parser's
asymmetric-family check: the token header cannot widen either one.

**A cached set has a finite lifetime.** `JWKSFreshness` is five minutes by
default. The first token at that boundary refreshes the whole set even when its
`kid` is cached and the token's method differs from the stale per-key policy;
the policy must be refreshed before that difference can become a credential
verdict. A successful response replaces the map, so a provider-withdrawn key
stops verifying without a restart. `JWKSStaleAfter(d)` changes that age.
`UnsafeJWKSNoFreshness()` is the explicit compatibility waiver for trusting a
cached key until a miss or restart.

**Refetching is rate-limited, and the limit holds when the provider is down.** A
stale hit or a token naming a `kid` the cached set does not have triggers at most
one refetch per `JWKSMinRefresh` (one minute by default), and at most one at a
time. Without those two bounds, one forged token per fetch is a denial-of-service
against the identity provider — and a `kid` is the caller's own input, so its
rate is theirs to choose.

Both bounds are the ones the obvious version gets wrong. Arming the limit on a
successful fetch leaves it doing nothing while the provider is failing, which is
when it matters; and the lock has to be dropped across the HTTP call, so a burst
arriving together passes the check before any of it records an attempt. The limit
reads the last *attempt*, and concurrent misses share one in-flight fetch rather
than being refused — after a rotation they are asking for a `kid` that really is
about to exist.

The fetch is detached from the request that initiated it and bounded by
`JWKSFetchTimeout`. The initiator and every waiter select independently on their
own context, so any of them can return promptly without cancelling the work
needed by the others. That caller receives its original `context.Canceled` or
`context.DeadlineExceeded`. By contrast, a timeout or transport error from the
detached provider fetch is an outage; it returns `ErrKeySourceUnavailable`, or
uses the explicitly configured stale-on-error window while it remains valid.

A token with no `kid` matches only when the set holds exactly one key. Anything
else would be this package choosing which key to trust on your behalf. A
present empty, non-string or otherwise malformed `kid` is refused; it is never
reinterpreted as omission.

The default client has no timeout of its own — `JWKSClient` is how a deployment
that cares supplies one. `JWKSMinRefreshEvery(d)` moves the rate limit, for a
provider that rotates faster or slower than a minute, and rejects zero or a
negative duration. `UnsafeJWKSNoMinRefresh()` is the named waiver that permits
one provider request per sequential miss. `JWKSClock` replaces only the cache
clock, which makes boundary tests deterministic without changing HTTP timeouts.

When a fetch, response or key-set document fails, parsing returns the typed
`ErrKeySourceUnavailable`; it is not `auth.ErrUnauthenticated`, because no
credential verdict was possible. Stale-on-error is off by default. A deployment
that deliberately prefers a bounded availability window declares both its
maximum and its operational signal together:

```go
authjwt.JWKS(jwksURL,
	authjwt.JWKSServeStaleFor(10*time.Minute,
		func(ctx context.Context, state authjwt.JWKSDegraded) {
			metrics.JWKSDegraded(ctx, state)
		}),
)
```

The observer is queued only after the provider singleflight has released its
waiters. It carries no explicit `kid` or URL and receives the provider cause,
cache age and both bounds. Calls have a one-second context, are serialised and
coalesced to the newest pending descriptor; a callback may re-enter the parser,
panic, or ignore cancellation without blocking requests or creating one stuck
goroutine per refresh. Observation is therefore operational work outside the
authentication critical path. At the exact end of the window the cached key is
no longer eligible and the typed unavailable error wins.

## The bridge

```go
authn := authjwt.Authenticator(parser,
	func(_ context.Context, c MyClaims) (auth.Principal, error) {
		return auth.Claims{
			Sub:         c.Subject,
			Permissions: auth.Scopes(c.Scope),
			Attrs:       map[string]any{"tenant": c.Tenant},
		}, nil
	})
```

The mapping is yours because only you know that `org` is the tenant, that
`groups` are roles, or that a subject needs a prefix to be unique across two
issuers.

**The mapper may refuse.** Returning an error rejects a token that verified,
which is where "valid, but its tenant was deleted" belongs. Build it with
`auth.Unauthenticated` so the reason stays out of the response.

For the ordinary shape of token, `Standard` is both calls:

```go
authn := authjwt.Standard(authjwt.HMAC(secret),
	auth.RoleMap{"editor": {"article:read", "article:write"}},
	authjwt.Issuer("https://id.example.com"),
	authjwt.Audience("articles-api"),
)
```

`authjwt.Claims` reads `sub`, `roles`, `permissions` and `scope` — both
spellings of a permission, because issuers disagree about which to send — and
`Attr` reaches every other claim in the payload. `Standard` requires `sub` to
be non-empty: this principal is used as an audit and ownership key, so
permissions alone are not an identity. If the issuer identifies callers through
another claim, use `New[C]` plus `Authenticator` and derive the subject in the
explicit mapper.

## Every refusal is one answer

Bad signature, expired, not yet valid, wrong issuer, wrong audience, no expiry,
unparseable, payload that does not fit `C` — all of them are
`auth.Unauthenticated`, and a client cannot tell them apart. Reporting which
check failed tells whoever is probing exactly what to change next ([[D-056]]).

An unavailable JWKS provider is deliberately outside that list. It returns
`ErrKeySourceUnavailable` and becomes an infrastructure answer rather than
telling a caller that credentials which may be valid are wrong ([[D-078]]).

## What it does not do

No signer, no refresh, no key rotation on the issuing side, no user store. This
reads what was presented.

## See also

- [auth](auth.md) — the contract this feeds
- [apikey](apikey.md) — the other provider
- [[D-055]] the placement · [[D-056]] the refusal's shape · [[D-078]] the trust bounds
- [[UC-019]] · [[FL-019]]
- [`_examples/auth-jwt-gin`](../../../_examples/auth-jwt-gin/) — the whole chain, running
