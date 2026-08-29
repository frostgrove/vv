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
| `Cookie(name)` | an `auth.Option` that reads the credential from a cookie |

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
| `Cookie(name)` | an `auth.Option`: read the credential from that cookie, **falling back to the Authorization header** |

Three things about it are load-bearing:

- **It falls back.** `auth.Lookup` replaces the credential lookup rather than
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
  `auth.Lookup`, with this function's body as the shape.

It is here rather than in `auth` because it needs an RFC 6265 parser and
`net/http` has one ([[D-055]]). The other end of the arrangement — which cookie
the access token was written into, and under what name — is
[access](access.md)'s.

## The boot access gate

The other half of this package, and the one a consumer touches directly: every
route mounted under a prefix says what reaching it requires, and start-up fails
when the router and those declarations disagree.

| | |
|---|---|
| `Endpoint` | one declaration: `Method`, `Path`, and either `Needs []auth.Permission` or `Why` |
| `Public(method, path, why)` | anybody may call it, and here is the reason |
| `Requires(method, path, perms…)` | the caller must hold **every** one of them |
| `Authenticated(method, path, why)` | any signed-in caller — "me", "my sessions", "sign me out" |
| `Endpoint.Declares()` | whether it says anything at all |
| `Route` | one entry in a router's own table: `Method`, `Path` |
| `Verify(declared, mounted, opts…)` | the comparison |
| `UnderPrefix(prefix)` | declared paths are relative to it; a mounted route outside it is not part of the surface |
| `ErrSurface` | what every disagreement wraps |

```go
if err := authfiber.Verify(app, declared, authhttp.UnderPrefix("/api/v1")); err != nil {
    return err   // refuse to start
}
```

The half that reads a real routing table is the binding's — [authfiber](authfiber.md),
[authgin](authgin.md), [authnet](authnet.md) — because it is the only half that
cannot be written once.

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

## See also

- [auth](auth.md) — the transport-neutral half
- [porthttp](porthttp.md) — the renderer, the envelope and the status table this refuses through
- [crudhttp](crudhttp.md) — the same split, for the CRUD bindings
- [access](access.md) — the surface that writes the cookie `Cookie` reads
- [[D-055]] · [[D-056]] · [[D-075]] · [[FL-019]]
