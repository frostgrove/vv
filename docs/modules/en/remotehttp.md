# remotehttp — the HTTP client transport

```go
import "github.com/shardit-io/vv/remote/remotehttp"
```

**Module:** root · **Depends on:** `crud`, `query`, `errs`, `port`, `port/porthttp`, `remote`, `net/http`

One function and two options. It turns a `remote.Call` into an HTTP request
against the routes every HTTP binding registers, so the service on the other end
may be running Fiber, Gin or net/http and you do not have to know which.

**Import it when** you hold another service's resource as a repository. The page
about that shape is [remote](remote.md); this is only how it reaches the wire.

---

## Using it

```go
articles := remote.New[Article, int64, ArticleInput](
    remotehttp.Transport("https://content.internal/articles"))

page, err := articles.Get(ctx,
    crud.Where(crud.Eq("Status", "draft")),
    crud.OrderBy(crud.Desc("CreatedAt")),
    crud.Limit(20))
```

`baseURL` is the prefix the resource was mounted under. A trailing slash is
trimmed.

| Option | Does |
|---|---|
| `WithClient(*http.Client)` | a timeout, connection limits, an instrumented round tripper |
| `WithRequestHook(func(*http.Request) error)` | runs before every request — an `Authorization` header, a trace header, an `Accept-Language`. Returning an error aborts the call |

There is **one** HTTP client and not three: a consumer calling out uses
`net/http` whatever it serves with, and the three bindings register the same
routes.

## It reads the server's tables, not its own

`porthttp.KindForStatus` is `porthttp.StatusFor` read backwards, and
`porthttp.ParseEnvelope` reads what `porthttp.EnvelopeRenderer` wrote. This
package calls both rather than keeping copies. A client with its own copy of
either would agree with the server until the first time one of them gained a
row, and the disagreement would be a status silently reclassified ([[D-045]]).

`ParseEnvelope` answering **false** is what keeps a router's or a gateway's own
`404 page not found` — text/plain, from `http.ServeMux` — from arriving as
`crud.ErrNotFound`. A body that is not an envelope is a `*remote.ProtocolError`,
whatever the status said.

## Why it is here and not in `crudgrpc`'s neighbourhood

It used to be `crudhttp.Transport`, on the rule *a transport lives beside the
binding it calls* — which held while the tables it reads backwards lived beside
that binding. They are `port/porthttp`'s now ([[D-059]]), so this moved in beside
`remote` and the CRUD binding stopped importing the client ([[D-058]]).

`crudgrpc.Transport` did not move, and the reason is a module boundary rather
than a protocol: `remote` is in the root module and may not import grpc, so a
`remote/remotegrpc` would be a whole module for one file. The asymmetry is on the
record rather than smoothed over.

## See also

- [remote](remote.md) — the resource this feeds, and what it refuses
- [porthttp](porthttp.md) — the two tables read forwards
- [crudgrpc](crudgrpc.md) — the other transport, and where it lives
- [[FL-018]] a call through the client
- [[D-058]] the layout axis is the subsystem · [[D-053]] a client refuses what changes the answer
