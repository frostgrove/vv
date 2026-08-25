# crudhttp — the HTTP half

```go
import "github.com/shardit-io/vv/http/crudhttp"
```

**Module:** root · **Depends on:** `crud`, `query`, `errs`, `port`, `net/http`

Everything the three HTTP bindings share and gRPC does not: the status table, the
error envelope, the renderer seam, and the request helpers each binding calls.

**Import it when** you render your own bodies, install the error middleware, or
replace the envelope. If you are just mounting routes, [crudnet](crudnet.md),
[crudfiber](crudfiber.md) or [crudgin](crudgin.md) re-export what you need.

---

## The status table

```go
crudhttp.Status(err)          // int — the whole thing, with no wiring
crudhttp.StatusFor(kind)      // int — if you already have the kind
crudhttp.KindOf(err)          // errs.Kind
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

`crudhttp.NewRenderer(opts...)` builds the default one.

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
> it is not part of the transport-neutral half.

## Installing it

```go
mux.Handle("/", crudnet.Errors(crudhttp.WithMessages(cat))(routes))   // net/http
app.Use(crudfiber.Errors(crudhttp.WithMessages(cat)))                 // Fiber
r.Use(crudgin.Errors(crudhttp.WithMessages(cat)))                     // Gin
```

It covers hand-rolled routes as well as generated ones. Installing it twice
renders once — the marker is the response-writer wrapper rather than anything on
the error, because a `Fault` is a value two goroutines may render at once.

---

## Naming the field on a hand-written endpoint

A generated [`port.PathMap`](port.md#the-path-chain) knows the mapping. Without
one, the raw-body index **recognises** it:

```go
body, err := crudhttp.DecodeJSONKeep(r.Body, &in)
ctx := crudhttp.WithBody(r.Context(), body)
// the renderer picks it up through crudhttp.BodyFrom
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
`crudhttp.KeepBody(b)` to copy first. `MaxKeptBody` is 64 KiB.

## Calling another service

The same package holds the client side, because the tables a client needs are
the ones defined here: `KindForStatus` is `StatusFor` read backwards and
`ParseEnvelope` reads what `EnvelopeRenderer` wrote. A client with its own copy
of either would agree with the server until the first time one of them changed
([[D-045]]).

```go
articles := remote.New[Article, int64, ArticleInput](
    crudhttp.Transport("https://content.internal/articles"))
```

| Option | Does |
|---|---|
| `WithClient(*http.Client)` | a timeout, connection limits, an instrumented round tripper |
| `WithRequestHook(func(*http.Request) error)` | runs before every request — an `Authorization` header, a trace header, an `Accept-Language` |

There is **one** HTTP client and not three: a consumer calling out uses
`net/http` whatever it serves with, and the three bindings register the same
routes. See [remote](remote.md) and [[FL-018]].

Two functions a client needs and an application answering its own errors may
also want:

| Function | Answers |
|---|---|
| `KindForStatus(code int) errs.Kind` | the class a status came from |
| `ParseEnvelope(body []byte) (Envelope, bool)` | the envelope, and **false** when the body is not one — which is what keeps a router's or a gateway's own 404 from reading as `crud.ErrNotFound` |

## The request helpers

Re-exported from [port](port.md), plus the HTTP-specific ones:

| | |
|---|---|
| `DecodeJSON(r, v)` / `DecodeJSONKeep(r, v)` | decode, and optionally keep the bytes |
| `MalformedBody(err)` | a decode failure becomes a 400 with `malformed_body` |
| `Sanitize` · `ClearGenerated` · `CoerceID` | what a client may not choose |
| `NarrowForEntity` · `NarrowForCount` | drop what those requests may not ask for |
| `BadRequest` · `BadRequestf` · `BadRequestAs` | build a 400 with a path named |
| `AcceptLanguage(header)` | the first tag out of an `Accept-Language` header |
| `WithLocale(ctx, l)` / `LocaleFrom(ctx)` | the locale the message ladder reads |
| `BulkDeleteRequest[ID]` | the `{"ids":[…]}` body |

## See also

- [port](port.md) — where the classification and the violation pipeline live
- [crudnet](crudnet.md) · [crudfiber](crudfiber.md) · [crudgin](crudgin.md) — the shells over this
- [errs](errs.md) — the `Violation` this renders
- [[FL-011]] an error becomes an HTTP status · [[FL-013]] a request through another binding
- [[D-049]] the kind decides the status · [[D-044]] the public payload names nothing internal
