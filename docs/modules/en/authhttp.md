# authhttp — the HTTP half of the auth middleware

```go
import "github.com/frostgrove/vv/auth/http/authhttp"
```

**Module:** root — it imports only the standard library, so it costs nothing
· **Depends on:** `porthttp`

What is HTTP but not framework. It stands to [authnet](authnet.md),
[authgin](authgin.md) and [authfiber](authfiber.md) exactly as
[crudhttp](crudhttp.md) stands to the three CRUD bindings — and both of them take
the status table, the envelope and the `Renderer` from the same
[porthttp](porthttp.md), which is what keeps a token check from importing the SQL
repository ([[D-059]]).

You rarely import it directly — a binding does. Import it to write a fourth HTTP
binding, or to render a refusal yourself.

---

## What you get

| | |
|---|---|
| `RendererFor(opts)` | the renderer these options describe, sharing one value when there are none |
| `Refuse(w, r, rd, err)` | the one place a 401 leaves a net/http-shaped binding |
| `Locale(r)` | the rendering context, read from `Accept-Language` |
| `Cookie(name)` | an `auth.Option` that reads the credential from a cookie, or refuses a request presenting two |
| `Preflight(method, header)` | whether this is a browser's CORS preflight: `OPTIONS`, an `Origin`, an `Access-Control-Request-Method`, and no credential. A predicate, not a permission: what a binding may do with a match is answer it, never route it ([[D-103]]) |
| `PreflightStatus` | what a binding answers a preflight nobody was named to answer — `204`, and no `Access-Control-Allow-*` header |
| `UnsafeCookieWinsOverAuthorization(name)` | the pre-[[D-099]] precedence, kept for a migration and named for what it costs |

## Why a refusal is written here rather than deferred

Handing the error to an outer error middleware would be tidier and is wrong: a
consumer who mounts an auth middleware without also mounting `crudnet.Errors`
would get an empty 200 for every unauthenticated request. **The failure mode of
"the door was open" must not depend on a second thing being installed.**

It composes rather than conflicts. `crudnet.Errors`, `crudgin.Errors` and
`crudfiber.Errors` all leave an already-written response alone, so an auth
middleware inside any of them renders once.

## What it deliberately does not set

`WWW-Authenticate`. RFC 7235 says a 401 carries one, and:

- a `Basic` challenge makes a browser open a modal login box no API wants;
- a bearer challenge's `error=` parameter exists precisely to say which part of
  the token was wrong, which is the disclosure [[D-056]] exists to prevent.

A consumer who needs the header for a standards-checking client sets it in a
wrapper.

## What is not here

Everything transport-neutral. Whether an optional guard accepts a forged token,
whether a second install re-authenticates, which header is read — all of that is
`auth.Guard`, so the gRPC interceptor gets the same decisions with no HTTP
package in its build ([[D-045]]).

Nothing here knows what a credential *means*. `Cookie` below knows where one may
be written down, which is an HTTP fact and is why it is on this side of the line.

## Reading the credential out of a cookie

A browser that holds its access token in an HttpOnly cookie sends no
`Authorization` header at all, so a guard that only reads one refuses every
request the page makes.

```go
guard := auth.NewGuard(authn, authhttp.Cookie("access"))
```

| | |
|---|---|
| `Cookie(name)` | an `auth.Option`: read the credential from that cookie, **falling back to the Authorization header**, and refuse a request that presents both |

Four things about it are load-bearing:

- **Two credentials are a refusal, not a ranking** ([[D-099]]). A cookie beside
  an `Authorization` header, or two cookies of that name, is a 401 wrapping
  `auth.ErrCredentialCardinality`, and the authenticator is never reached — an
  optional guard included. The browser attaches its cookie to every request the
  page can cause, so a page that deliberately sent a bearer token used to have it
  silently replaced by whatever was left in the jar.
  `UnsafeCookieWinsOverAuthorization(name)` is the old precedence for a
  deployment that needs to migrate, and nothing else selects it.
- **It falls back.** A lookup replaces the credential lookup rather than
  adding to it, so a cookie option written the obvious way turns the header off —
  in the same application that wants both, because its pages send a cookie and
  its native client sends a header. The fallback also keeps a guard usable from
  `authgrpc`, where `Cookie` is a metadata key no client sends.
- **It supplies the `Bearer` scheme**, which a cookie does not carry and every
  authenticator here requires. Presenting a token in a cookie means what
  presenting it in an `Authorization: Bearer` header means.
- **The header it falls back to is `Authorization`**, not whatever
  [auth.Header](auth.md) was given: an option cannot read another option's
  choice. A guard that needs both a cookie and a header of its own writes its own
  `auth.Lookup` — or `auth.LookupOrRefuse`, if it too has something to refuse —
  with this function's body as the shape.

It is here rather than in `auth` because it needs an RFC 6265 parser and
`net/http` has one ([[D-055]]). The other end of the arrangement — which cookie
the access token was written into, and under what name — is
[access](access.md)'s.

## The boot access gate

The other half of this package, and the one a consumer touches directly: every
mounted route says what reaching it requires, and start-up fails when the router
and those declarations disagree.

| | |
|---|---|
| `Endpoint` | one declaration: `Method`, `Path`, and either `Needs []auth.Permission` or `Why` |
| `Public(method, path, why)` | anybody may call it, and here is the reason |
| `Requires(method, path, perms…)` | the caller must hold **every** one of them |
| `Authenticated(method, path, why)` | any signed-in caller — "me", "my sessions", "sign me out" |
| `Endpoint.Declares()` | whether it says anything at all |
| `Route` | one entry in a router's own table: `Method`, `Path` |
| `AtRoot(endpoint)` | the same declaration, with its path read as absolute rather than relative to the prefix |
| `Verify(declared, mounted, opts…)` | the comparison, over **every** mounted route: the ones the prefix covers against the relative declarations, everything else against the `AtRoot` ones |
| `UnderPrefix(prefix)` | declared paths are relative to it. The comparison is by path segment, so `/api/v10` is not under `/api/v1` — and a route in that other tree is one the `AtRoot` declarations answer for, not one nobody checks |
| `Area` | one verified surface: a `Prefix` and the `Endpoint`s declared under it |
| `Under(prefix, declared…)` / `Rooted(declared…)` | an area, and the one at the root whose declarations are absolute paths |
| `VerifyAreas(mounted, areas…)` | the comparison over **every** mounted route: a route no area covers is a disagreement |
| `ErrSurface` | what every disagreement wraps |

```go
if err := authfiber.Verify(app, declared, authhttp.UnderPrefix("/api/v1")); err != nil {
    return err   // refuse to start
}
```

The half that reads a real routing table is the binding's — [authfiber](authfiber.md),
[authgin](authgin.md), [authnet](authnet.md) — because it is the only half that
cannot be written once.

### A prefix is not an exemption

What lives outside the API prefix is `/`, `/favicon.ico`, `/live` and `/ready` —
the part of the tree an anonymous caller reaches first, and the part nobody
declares. So configuring a prefix does not narrow what is checked, only where the
relative declarations are read: a mounted route the prefix does not cover is
compared against the declarations marked `AtRoot`, and one of those nobody wrote
is `is mounted and declares no access; it answers outside /api/v1, so declare it
with authhttp.AtRoot or mount it under the prefix` — the refusal names the seam
that answers it, because the first person to meet this check is meeting it on a
deployment that will not start.

```go
declared = append(declared,
    authhttp.AtRoot(authhttp.Public(http.MethodGet, "/live", "a load balancer cannot present a credential")),
    authhttp.AtRoot(authhttp.Public(http.MethodGet, "/ready", "the same, and it is read before anything is signed in")),
)

if err := authfiber.Verify(app, declared, authhttp.UnderPrefix("/api/v1")); err != nil {
    return err
}
```

`VerifyAreas` is the explicit form under that convenience, and the one to reach
for when a process serves more than two surfaces:

```go
err := authfiber.VerifyAreas(app,
    authhttp.Under("/api/v1", declared...),
    authhttp.Under("/internal", internal...),
    authhttp.Rooted(
        authhttp.Public(http.MethodGet, "/live", "a load balancer cannot present a credential"),
    ),
)
```

`Rooted` is the catch-all and its declared paths are absolute; every other area
takes the most specific prefix that covers a route. A route no area covers is
`is mounted outside every verified surface`. Two areas whose prefixes cover each
other are refused rather than resolved — which of them checks a route would
otherwise be whichever the loop reached first.

### Why both directions are refused

A route with no authorization check and a route that is deliberately public look
identical from the inside. The gap is only visible from outside, by somebody who
tries it. So the router is compared against a declaration — and a declaration
whose route no longer exists is refused too, because one that outlives its
handler is what makes the list look complete while it covers less every month.

`Verify` reports every problem at once. A start-up failure that names one missing
declaration, gets fixed, and then names the next one is three restarts to learn
what one message could have said.

A zero `Endpoint` is refused, which is what makes "I forgot" impossible to pass
off as "no permissions needed" ([[D-073]]).

### What the gate does not prove

**It is an audit, not enforcement.** `Needs` is compared against a routing table
and nothing else reads it: no middleware, no handler wrapper, nothing at request
time. A route can declare `Requires(http.MethodGet, "/contracts", "contract.read")`,
pass the gate, and hand every caller the list.

That is deliberate, and it is the one thing to understand about this half:
`Verify` answers "was every mounted route considered?", not "is every mounted
route guarded?". Enforcement is `auth.Guard` for who the caller is,
`access.Require(ctx, perms…)` in the handler for what they may do, and
`security.Gate` around the repository for which rows they may see. Proving your
handlers reach one of those is a test over your own surface — a caller without
the permission gets 403 or 404 — and nothing here stands behind it.

## See also

- [auth](auth.md) — the transport-neutral half
- [porthttp](porthttp.md) — the renderer, the envelope and the status table this refuses through
- [crudhttp](crudhttp.md) — the same split, for the CRUD bindings
- [access](access.md) — the surface that writes the cookie `Cookie` reads
- [[D-055]] · [[D-056]] · [[D-075]] · [[FL-019]]
