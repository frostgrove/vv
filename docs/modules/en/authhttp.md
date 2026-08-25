# authhttp — the HTTP half of the auth middleware

```go
import "github.com/shardit-io/vv/http/authhttp"
```

**Module:** root — it imports only the standard library, so it costs nothing
· **Depends on:** `crudhttp`

What is HTTP but not framework. It stands to [authnet](authnet.md),
[authgin](authgin.md) and [authfiber](authfiber.md) exactly as
[crudhttp](crudhttp.md) stands to the three CRUD bindings.

You rarely import it directly — a binding does. Import it to write a fourth HTTP
binding, or to render a refusal yourself.

---

## What you get

| | |
|---|---|
| `RendererFor(opts)` | the renderer these options describe, sharing one value when there are none |
| `Refuse(w, r, rd, err)` | the one place a 401 leaves a net/http-shaped binding |
| `Locale(r)` | the rendering context, read from `Accept-Language` |

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

Nothing here knows what a credential is.

## See also

- [auth](auth.md) — the transport-neutral half
- [crudhttp](crudhttp.md) — the same split, for the CRUD bindings
- [[D-055]] · [[D-056]] · [[FL-019]]
