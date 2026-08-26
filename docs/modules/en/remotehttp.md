# remotehttp — the HTTP client transport

```go
import "github.com/shardit-io/vv/remote/remotehttp"
```

**Module:** root · **Depends on:** `crud`, `query`, `errs`, `port`, `port/porthttp`, `remote`, `net/http`

One function and three options. It turns a `remote.Call` into an HTTP request
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
| `WithClient(*http.Client)` | a timeout other than `DefaultTimeout`, connection limits, an instrumented round tripper |
| `WithMaxResponse(n)` | cap how many bytes of an answer this transport reads; the default is `MaxResponse`, 32 MiB |
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

## Two limits it brings of its own

```go
remotehttp.DefaultTimeout   // 30s — the client's, when nobody named one
remotehttp.MaxResponse      // 32 MiB — how much of an answer it reads
```

Neither is a tuning knob; both are the answer to "and what if the other service
is wrong".

**The client is never `http.DefaultClient`.** That one has no timeout at all, so
a peer that accepts the connection and then says nothing holds the caller until
something else gives up — inside a request handler with no deadline of its own,
that is never. It also belongs to the whole binary, so a consumer setting a
timeout on it for this transport would be setting one for every other library
that reached for the same value. The caller's own context deadline still wins;
`DefaultTimeout` is the backstop underneath it.

**The answer is read under a cap.** A remote resource is another service, and
another service can be wrong: a paging bug on the far side, a proxy substituting
an HTML page, a peer that has been taken over. Reading it whole turns any of
those into this process running out of memory — the one failure a client cannot
report ([[D-063]]).

## What the far end has to declare

`remote.GetAll` needs `query.Config{AllowUnpaged: true}` on the resource it
calls. There is no "every row" route: `GetAll` is emulated with the `unpaged`
flag, and an endpoint that never agreed to serve whole tables refuses it
([[D-060]]). The refusal names the fix.


## See also

- [remote](remote.md) — the resource this feeds, and what it refuses
- [porthttp](porthttp.md) — the two tables read forwards
- [crudgrpc](crudgrpc.md) — the other transport, and where it lives
- [[FL-018]] a call through the client
- [[D-058]] the layout axis is the subsystem · [[D-053]] a client refuses what changes the answer
