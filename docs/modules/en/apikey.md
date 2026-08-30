# apikey — authenticate by a shared secret

```go
import "github.com/frostgrove/vv/auth/apikey"
```

**Module:** root — it imports only the standard library, so it costs nothing
· **Depends on:** `auth`, `crypto/subtle`

A caller presents a key verbatim; a `Store` says who that is.

---

## What you get

| | |
|---|---|
| `New(store, opts...)` | the `auth.Authenticator` |
| `Store`, `StoreFunc` | the seam you implement |
| `Static(map)` | an in-memory store for tests and small services |
| `TryStatic(map)` | the same store with a snapshot-validation error |
| `Header(name)` | read a bare key from a dedicated header |
| `Scheme`, `AnyScheme` | which auth-scheme is accepted |

## Wire it

```go
authn := apikey.New(apikey.Static(map[string]auth.Principal{
	"k-batch-1": auth.Claims{
		Sub:         "batch",
		Permissions: []auth.Permission{"article:read"},
	},
}))

guard := auth.NewGuard(authn, apikey.Header("X-Api-Key"))
```

From here it is the same three remaining steps as with a JWT, and nothing in
them is specific to API keys: the guard goes into a transport middleware —
[authnet](authnet.md), [authgin](authgin.md), [authfiber](authfiber.md) or
[authgrpc](authgrpc.md) — and what the caller is then allowed to do is a
[security](security.md) policy on the repository. A guard on its own
authenticates and enforces nothing.

`auth.Chain(jwtAuthn, apiKeyAuthn)` is how one endpoint accepts both kinds.

`apikey.Header` is deliberately different from `auth.Header`. This helper reads
`X-Api-Key: k-batch-1` as a complete bare key. `auth.Header` only moves the
Authorization parser and therefore expects `X-Api-Key: ApiKey k-batch-1`.

## The Store, and its three results

```go
type Store interface {
	Lookup(ctx context.Context, key string) (auth.Principal, bool, error)
}
```

Three, because two failures must not be confused:

| result | means | becomes |
|---|---|---|
| `(p, true, nil)` | this key is issued | the caller |
| `(nil, false, nil)` | nobody issued this key | 401 |
| `(nil, false, err)` | the lookup could not be performed | that error, so a 500 |

Collapsing the third into the second would answer "your key is wrong" to every
caller during a database outage, and the callers would rotate their keys.

`New` refuses both nil and interface-typed nil stores at construction.

**A real deployment does not hold keys in a map.** It holds a hash of each key,
looks the hash up, and revokes by deleting a row. `Store` is that seam:

```go
apikey.New(apikey.StoreFunc(func(ctx context.Context, key string) (auth.Principal, bool, error) {
	sum := sha256.Sum256([]byte(key))
	row, err := keys.Find(ctx, sum[:])
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return nil, false, nil
	case err != nil:
		return nil, false, err
	}
	return row.Principal(), true, nil
}))
```

Whatever implements it must compare in constant time. Indexing a database by the
hash of the presented key does, because the hash is what travels.

## Static compares every entry

`Static` is a slice, not a map, and it compares all of them rather than stopping
at the first match. A map lookup branches on the key's bytes and returns as soon
as it knows, which times differently for a key sharing a prefix with a real one
— enough, over enough requests, to recover a key a character at a time.
`crypto/subtle` costs a few hundred nanoseconds for the handful of keys this is
meant to hold.

It snapshots the map it is given. Value and pointer forms of the built-in
`auth.Claims` are deep-copied, including roles, permissions, cycles and
supported mutable attribute containers, and every lookup receives another
copy. Mutating the declaration or one request's principal therefore cannot
change another request.

The copy contract is fallible rather than reflective guesswork. `TryStatic`
returns `ErrUnsupportedStaticAttribute` for functions, channels, unsafe
pointers, unsafe map keys and structs with unexported state. `bytes.Buffer`,
`big.Int` and a custom struct hiding a slice are refused: shallow-copying them
would share mutable storage. Declarative `Static` panics on that same start-up
error. Store time-like or otherwise opaque values in an immutable scalar form,
or use `TryStatic` and handle the configuration error. A custom `Principal`
cannot be enumerated through its interface and must itself be immutable and
concurrency-safe when placed in either constructor.

## The scheme

By default an Authorization-shaped credential must arrive as `ApiKey <token>`.
`apikey.Header` supplies that scheme internally for a bare dedicated header.
`AnyScheme()` waives the check, and it is opt-in rather than the default for a reason: an endpoint that
also accepts JWTs would otherwise hand every expired JWT to the key store as a
candidate key, and a store that logs misses would then log tokens.

`Scheme("PartnerKey")` replaces it for scheme-shaped credentials. The comparison
is case-insensitive, as RFC 7235 requires. An empty or whitespace-only name
refuses to start; only the visibly named `AnyScheme()` may disable the check.

## Why this exists

Partly because API keys are a real way services authenticate. Partly because
`auth.Authenticator` needs a second implementation to be a contract rather than
a decoration on one struct — that is [[D-048]]'s count rule, and this is the
package that satisfies it ([[D-055]]).

## See also

- [auth](auth.md) · [authjwt](authjwt.md)
- [[D-055]] · [[D-076]] · [[UC-019]] · [[FL-019]]
