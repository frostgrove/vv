# porthttp — the HTTP projection of the error contract

```go
import "github.com/shardit-io/vv/port/porthttp"
```

**Module:** root · **Depends on:** `crud`, `query`, `errs`, `port`, `net/http`

The status table, the error envelope, the `Renderer` seam, the JSON body decode
and the raw-body path fallback — one implementation, for every subsystem that
answers a failure over HTTP. The CRUD bindings render a 409 through it, the auth
middleware renders a 401 through it, and `remotehttp` reads both of them back.

**Import it when** you render your own bodies, install the error middleware,
replace the envelope, or write a middleware of your own that has to refuse a
request. If you are just mounting CRUD routes, [crudnet](crudnet.md),
[crudfiber](crudfiber.md) or [crudgin](crudgin.md) re-export what you need.

> **It is not under `crud/`, and that is the point.** Until [[D-059]] all of this
> lived in `crudhttp`, so an auth middleware that wanted a `Renderer` imported
> the CRUD binding — and with it the SQL repository, the predicate AST and an
> HTTP client to somebody else's service. The name is a grid cell like every
> other: subsystem `port` × library `net/http` ([[D-035]]).
>
> `crudhttp` re-exports every name on this page, so code written against
> `crudhttp.Status` or `crudhttp.NewRenderer` still compiles.

---

## The status table

```go
porthttp.Status(err)          // int — the whole thing, with no wiring
porthttp.StatusFor(kind)      // int — if you already have the kind
porthttp.KindOf(err)          // errs.Kind
```

| Kind | Status |
|---|---|
| `KindNotFound` | 404 |
| `KindUnauthorized` | 401 |
| `KindForbidden` | 403 |
| `KindRetryable` | 503, with `Retry-After` |
| `KindConflict` | 409 |
| `KindValidation` | 422 |
| `KindBadRequest` | 400 |
| anything else | 500 |

**The kind is `port`'s answer and the status is this package's table.** That is
the whole of [[D-045]] in one line: classification is transport-neutral, and 404
is not.

`Status` takes no wiring, so an application rendering its own bodies gets the
statuses without building a renderer ([[UC-015]]).

## The envelope

The only body this library puts on the wire for a failed request:

```json
{
  "type": "error",
  "errors": {
    "validation": [
      {"field": ["user","email"], "error_code": "unique", "message": "that address is taken"}
    ],
    "general": [
      {"error_code": "restrict", "message": "something still refers to this record"}
    ]
  }
}
```

```go
type Envelope struct {
    Type    string `json:"type"`               // always "error"
    Partial bool   `json:"partial,omitempty"`  // a cap was hit; the list is incomplete
    Errors  Groups `json:"errors"`
}

type Groups struct {
    Validation []errs.Violation `json:"validation,omitempty"`  // names a field
    General    []errs.Violation `json:"general,omitempty"`     // does not
}
```

`Type` is always `"error"`, so a client can branch before parsing.

**The group says what the client can act on, not where the failure came from** —
which is why a 409 unique conflict appears under `validation`: it names a field
the client sent, so a form can mark it ([[UC-017]]).

**`Partial` is not decoration.** A response that lists four violations without it
is claiming there were four.

`Groups` is a struct rather than a map so the key order is that declaration
rather than the encoder's habit — the same reason [[D-014]] gives for everything
else here being byte-identical run to run.

> RFC 9457 problem+json is not shipped, and neither is the older
> `{"error":…,"message":…}` body. Two shapes is twice the surface to test and
> keep honest for a choice almost nobody changes; the `Renderer` seam is there
> for a consumer who does.

## The renderer

```go
type Renderer interface {
    Render(ctx context.Context, err error) (int, http.Header, any)
}
```

`porthttp.NewRenderer(opts...)` builds the default one.

| Option | Does |
|---|---|
| `WithCodes(*errs.Codes)` | the vocabulary. Decides kinds and default messages |
| `WithMessages(errs.MessageSource)` | the catalogue rung of the message ladder |
| `WithResolvers(rs...)` | declared path hops, wired **ahead** of the body fallback |
| `WithMaxViolations(n)` | cap the list. Default 100 |
| `WithRetryAfter(seconds)` | the header on a 503. Default 1 |

Replace it wholesale — with RFC 9457, with a legacy shape, with nothing at all —
through `WithRenderer` on any binding.

> The `Renderer` interface lives here rather than in `errs` on purpose: an
> interface returning `http.Header` cannot be implemented by a gRPC binding, so
> it is not part of the transport-neutral half. `crudgrpc` has a `Renderer` of
> its own, answering a `*status.Status` over the same `port.Violations`
> pipeline ([[D-052]]).

## Installing it

```go
mux.Handle("/", crudnet.Errors(porthttp.WithMessages(cat))(routes))   // net/http
app.Use(crudfiber.Errors(porthttp.WithMessages(cat)))                 // Fiber
r.Use(crudgin.Errors(porthttp.WithMessages(cat)))                     // Gin
```

It covers hand-rolled routes as well as generated ones. Installing it twice
renders once — the marker is the response-writer wrapper rather than anything on
the error, because a `Fault` is a value two goroutines may render at once.

The auth middlewares take the same options, for the same reason: there is one
renderer, so a 401 and a 422 come out of the same catalogue in the same shape.

```go
mux.Handle("/", authnet.Middleware(guard, porthttp.WithMessages(cat))(routes))
```

---

## Naming the field on a hand-written endpoint

A generated [`port.PathMap`](port.md#the-path-chain) knows the mapping. Without
one, the raw-body index **recognises** it:

```go
body, err := porthttp.DecodeJSONKeep(r.Body, &in)
ctx := porthttp.WithBody(r.Context(), body)
// the renderer picks it up through porthttp.BodyFrom
```

`BodyResolver(raw)` walks the retained body into a leaf-path index and matches a
model field name against the keys the client actually sent.

**It is the fallback, not the mechanism**, and it runs only over a path the
declared hops left unchanged — so a guess cannot overturn a declaration
([[D-043]]).

Three limits, stated rather than discovered:

- **JSON only.** A form body has no nesting to index; a non-JSON body declines
  and the path degrades to the model field name.
- **A name folding to more than one leaf declines.** Two `email` keys at two
  nestings are two candidates and this layer does not pick.
- **It declines rather than guessing, always.** A decline marks the violation
  `Approximate`.

Fiber's `c.Body()` is valid only within the handler, so that binding uses
`porthttp.KeepBody(b)` to copy first. `MaxKeptBody` is 64 KiB.

## Reading a failure back

The inverse of the two tables above, in the same package as the forward ones. A
client with its own copy of either would agree with the server until the first
time one of them gained a row, and the disagreement would be a status silently
reclassified ([[D-045]]). [remotehttp](remotehttp.md) is the caller;
an application answering its own errors may want them too:

| Function | Answers |
|---|---|
| `KindForStatus(code int) errs.Kind` | the class a status came from |
| `ParseEnvelope(body []byte) (Envelope, bool)` | the envelope, and **false** when the body is not one — which is what keeps a router's or a gateway's own 404 from reading as `crud.ErrNotFound` |

## The request helpers

| | |
|---|---|
| `DecodeJSON(r, v)` / `DecodeJSONKeep(r, v)` | decode, and optionally keep the bytes |
| `KeepBody(b)` | a copy that outlives the handler; nil past `MaxKeptBody` |
| `MalformedBody(err)` | a decode failure becomes a 400 with `malformed_body` |
| `BadRequest` · `BadRequestf` · `BadRequestAs` | build a 400 with a path named |
| `AcceptLanguage(header)` | the first tag out of an `Accept-Language` header |
| `WithLocale(ctx, l)` / `LocaleFrom(ctx)` | the locale the message ladder reads |
| `WithBody(ctx, raw)` / `BodyFrom(ctx)` | the retained body, for the fallback above |

`CoerceID`, `Sanitize`, `ClearGenerated`, `NarrowForCount` and `NarrowForEntity`
are **not** here: they are about a CRUD request, so they are [port](port.md)'s,
forwarded by [crudhttp](crudhttp.md).

## See also

- [port](port.md) — where the classification and the violation pipeline live
- [crudhttp](crudhttp.md) — what is HTTP *and* CRUD, and the forwarders
- [remotehttp](remotehttp.md) — the client that reads these tables backwards
- [authhttp](authhttp.md) — the other subsystem answering through this one
- [crudnet](crudnet.md) · [crudfiber](crudfiber.md) · [crudgin](crudgin.md) — the shells over this
- [errs](errs.md) — the `Violation` this renders
- [[FL-011]] an error becomes an HTTP status · [[FL-013]] a request through another binding
- [[D-059]] the HTTP projection belongs to `port` · [[D-049]] the kind decides the status · [[D-044]] the public payload names nothing internal
