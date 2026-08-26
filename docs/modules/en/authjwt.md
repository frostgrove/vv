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
| `New[C]`, `Parser[C].Parse` | verification, into your own type |
| `HMAC`, `RSA`, `ECDSA`, `EdDSA`, `JWKS`, `Custom` | key sources, each pinning the methods it verifies |
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
| `HMAC(secret)` | HS256, HS384, HS512 |
| `RSA(pub)` | RS256/384/512, PS256/384/512 |
| `ECDSA(pub)` | ES256, ES384, ES512 |
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

## What a caller cannot forget

`New` **panics at construction** on three things, because all three produce a
parser that over-trusts and a process that does not start is the better outcome
([[D-021]]):

- a key source that verifies nothing;
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

RSA, EC (P-256/384/521) and Ed25519 keys are read; an `oct` entry is ignored,
because a key set is a public document and an HMAC entry in one is a shared
secret published to the internet. An entry with `use` set to anything but `sig`
is ignored too, and one unusable entry does not cost the whole set.

**Refetching is rate-limited, and the limit holds when the provider is down.** A
token naming a `kid` the cached set does not have triggers at most one refetch
per `JWKSMinRefresh` (one minute by default), and at most one at a time. Without
those two bounds, one forged token per fetch is a denial-of-service against the
identity provider — and a `kid` is the caller's own input, so its rate is theirs
to choose.

Both bounds are the ones the obvious version gets wrong. Arming the limit on a
successful fetch leaves it doing nothing while the provider is failing, which is
when it matters; and the lock has to be dropped across the HTTP call, so a burst
arriving together passes the check before any of it records an attempt. The limit
reads the last *attempt*, and concurrent misses share one in-flight fetch rather
than being refused — after a rotation they are asking for a `kid` that really is
about to exist.

A token with no `kid` matches only when the set holds exactly one key. Anything
else would be this package choosing which key to trust on your behalf.

The default client has no timeout of its own — `JWKSClient` is how a deployment
that cares supplies one. `JWKSMinRefreshEvery(d)` moves the rate limit, for a
provider that rotates faster or slower than a minute.

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
`Attr` reaches every other claim in the payload.

## Every refusal is one answer

Bad signature, expired, not yet valid, wrong issuer, wrong audience, no expiry,
unparseable, payload that does not fit `C` — all of them are
`auth.Unauthenticated`, and a client cannot tell them apart. Reporting which
check failed tells whoever is probing exactly what to change next ([[D-056]]).

## What it does not do

No signer, no refresh, no key rotation on the issuing side, no user store. This
reads what was presented.

## See also

- [auth](auth.md) — the contract this feeds
- [apikey](apikey.md) — the other provider
- [[D-055]] the placement · [[D-056]] the refusal's shape
- [[UC-019]] · [[FL-019]]
- [`_examples/auth-jwt-gin`](../../../_examples/auth-jwt-gin/) — the whole chain, running
