# crudhttp — what is HTTP *and* CRUD

```go
import "github.com/shardit-io/vv/crud/http/crudhttp"
```

**Module:** root · **Depends on:** `crud`, `query`, `errs`, `port`, `port/porthttp`, `net/http`

The remainder after two splits: the request shapes a CRUD route has and nothing
else does, plus a compatibility surface over everything that left.

**You probably do not need to import it.** If you are mounting routes,
[crudnet](crudnet.md), [crudfiber](crudfiber.md) and [crudgin](crudgin.md)
re-export what you need. If you are rendering your own bodies or installing the
error middleware, the page you want is [porthttp](porthttp.md).

---

## Where everything went

| What | Where | Why |
|---|---|---|
| the commands, the `Service`, the `Mapper`, the code vocabulary, `port.Violations` | [port](port.md) | none of it is HTTP ([[D-045]]) |
| the status table, the envelope, the `Renderer` seam, `DecodeJSON`, the locale, the raw-body fallback | [porthttp](porthttp.md) | all of it is HTTP and none of it is CRUD ([[D-059]]) |
| the client transport | [remotehttp](remotehttp.md) | it is the client, not the server ([[D-058]]) |
| what is on this page | here | it is HTTP *and* CRUD |

Two tests, asked in that order:

1. **Could a non-HTTP transport implement this without importing `net/http`?**
   If not, it is not `port`'s — a `Renderer` returning an `http.Header` could
   not, which is why the seam is not in `errs`.
2. **Could a subsystem that is not CRUD take this without importing `crud`?**
   If yes, it is `porthttp`'s — the auth middleware answers a 401 through the
   same status table and the same envelope.

## What is actually here

| | |
|---|---|
| `Repository[M, ID, U]` | a generic alias for `port.Repository` — the interface every binding takes ([[D-022]]) |
| `BulkDeleteRequest[ID]` | the `{"ids":[…]}` body of `POST /bulk-delete` |
| `CoerceID[ID](raw string)` | a path parameter becomes the key type — which is why a uuid or a slug key works in a URL with no extra code |
| `NarrowForCount(*query.Request)` | drop everything that means nothing to a `COUNT` |
| `NarrowForEntity(*query.Request)` | keep only the shaping options |
| `Sanitize` · `ClearGenerated` | what a client may not choose on create: a generated key, a `generated` column |
| `Rules` | the five settings that say nothing about a transport, shared by all four bindings — an alias for [`port.Rules`](port.md#the-rules-a-binding-does-not-own) |

`CoerceID` through `Sanitize` · `ClearGenerated` are forwarders over
[port](port.md); they are exported here because an application that writes its
own create route calls them ([[D-045]]). `Rules` is not one of them — it is an
alias for a type that never lived here.

## The forwarders

`crud/http/crudhttp/porthttp.go` re-exports everything [[D-059]] moved:
`Renderer`, `EnvelopeRenderer`, `RenderOption`, `Envelope`, `Groups`,
`MaxViolations`, `DefaultRetryAfter`, `MaxKeptBody`, `MaxBody`, `ErrBadRequest`,
`NewRenderer`, the five `With…` options, `Internal`, `Status`, `StatusFor`,
`KindForStatus`, `KindOf`, `ParseEnvelope`, `BadRequest`, `BadRequestf`,
`BadRequestAs`, `MalformedBody`, `TooLarge`, `BodyResolver`, `DecodeJSON`,
`DecodeJSONKeep`, `DecodeJSONKeepLimit`,
`KeepBody`, `WithBody`, `BodyFrom`, `WithLocale`, `LocaleFrom` and
`AcceptLanguage`.

They are aliases and one-line calls with no behaviour, and the file says so at
the top: a symbol there that grows a body has stopped being a forwarder and
belongs on one side of the split or the other. Re-pointing an alias is not a
breaking change, which is the same trick [[D-034]] landed on.

New code should import [porthttp](porthttp.md) directly.

## See also

- [porthttp](porthttp.md) — the status table, the envelope, the renderer
- [port](port.md) — the transport-neutral half
- [remotehttp](remotehttp.md) — the client side
- [crudnet](crudnet.md) · [crudfiber](crudfiber.md) · [crudgin](crudgin.md) — the shells over this
- [[FL-013]] a request through another binding · [[FL-015]] a request through the port layer
- [[D-059]] the HTTP projection belongs to `port` · [[D-045]] the shared half is transport-neutral
