# remote — a repository that is not in this process

```go
import "github.com/frostgrove/vv/remote"
```

**Module:** root · **Depends on:** `crud`, `query`, `port`, `errs`, and the
standard library · **Contract manifest:** no

The consuming half. One service declares a CRUD API with `crudnet`,
`crudfiber`, `crudgin` or `crudgrpc`; another holds a `remote.Resource` over the
same model and calls it with the methods it would use on a repository of its
own.

**Import it when** a service in your estate needs another service's resource and
you would otherwise write an `http.Client`, a set of body structs and a switch
over status codes.

---

## Wiring one

```go
articles := remote.New[Article, int64, ArticleInput](
    remotehttp.Transport("https://content.internal/articles"))

page, err := articles.Get(ctx,
    crud.Where(crud.Eq("Status", "draft")),
    crud.OrderBy(crud.Desc("CreatedAt")),
    crud.Limit(20))
```

Swap the transport and nothing else changes:

```go
articles := remote.New[Article, int64, ArticleInput](
    crudgrpc.Transport(conn, "Article"))
```

`New` panics when the model cannot be described, when its key does not match
`ID`, or when the patch DTO would empty a column — the same start-up failures
`sqlrepo.Define` answers with, and for the same reason. `TryNew` is the same
without the panic.

## What you get

A `remote.Resource[M, ID, U]` satisfies `port.Repository`, so every method is
the one you already know:

| Method | Route |
|---|---|
| `Get(ctx, opts...)` | `POST /query` · `List` |
| `GetAll(ctx, opts...)` | every matching row (or the requested explicit subset); cursor-edge walks make remote page/offset caps chunking controls rather than a truncated success. Cursorless and DISTINCT-without-PK shapes still need a sufficient `MaxOffset`. |
| `GetByID(ctx, id, opts...)` | direct: `GET /{id}` · `Get`; root filter or narrowed/capped preload: `POST /query` · `List` with the primary-key equality |
| `Count(ctx, opts...)` | `POST /count` · `Count` |
| `Save(ctx, *m)` | `POST /` when the key is unset, `PUT /{id}` when it is set |
| `Update(ctx, id, dto)` | `PATCH /{id}` · `Update` |
| `Delete(ctx, id)` | `DELETE /{id}` · `Delete` |
| `Delete(ctx, ids...)` | `POST /bulk-delete` · `BulkDelete` |

It is **not** a `crud.Core`: that has `Tx`, and a transaction does not cross a
stateless call.

Because it is a `port.Repository`, it also composes. Mount it on your own routes
and you have a gateway:

```go
crudnet.New(articles).Mount(mux, "/articles")
```

## Errors keep working

This is the reason the package exists. A refusal arrives as an `*errs.Fault`
wrapping the same sentinel it would have wrapped locally:

```go
if _, err := articles.GetByID(ctx, 42); errors.Is(err, crud.ErrNotFound) {
    // the same branch, whichever side of the network the row is on
}
```

and it carries what a client can act on:

```go
if f, ok := errs.AsFault(err); ok {
    for _, v := range f.Violations {
        fmt.Println(v.Path, v.Code, v.Message) // ["email"] unique "…"
    }
}
```

What never arrives is anything internal — no constraint name, no table, no
engine number, no driver sentence ([[D-044]]). An internal failure arrives
saying nothing at all.

**An answer that did not come from this library is never read as one.** A wrong
base URL, a proxy, an API gateway, or a method a read-only service never
registered arrives as a `*remote.ProtocolError`:

```go
var pe *remote.ProtocolError
if errors.As(err, &pe) {
    log.Printf("%s answered %s", pe.Where, pe.Status)
}
```

Without that check a router's 404 would read as `crud.ErrNotFound`, and a
misconfigured service would report an empty table for as long as nobody looked.

## What the network costs

Every `crud.Option` gets one of three answers, and never a fourth ([[D-053]]).

**Translated** — `Page`, `Limit`, `Offset`, `OrderBy`/`SortBy`, `Select`,
`Preload`/`PreloadWhere`/`PreloadCap`, `After`, `Before`, `Unpaged`, `SkipTotal`, `Distinct`, and `Where`
with any predicate the wire DSL can spell. A keyed `GetByID` keeps its root
filter too: when one is present (or a preload is narrowed or capped), the client uses
the document-shaped List route with an additional primary-key equality. A
direct entity route retains only projection and plain preload paths; ordering
and paging cannot change a keyed result and are discarded.

That fallback is deliberately an ordinary public List request: a restrictive
peer must grant its primary key **and every field in the caller's filter** in
`query.Config.Filterable`, just as it would for a direct `Get`. Otherwise the
peer returns its normal query refusal rather than weakening the keyed read.

`GetAll` clears `SkipTotal` and turns an all-rows request — no page controls,
or `Unpaged` (which wins a supplied page/limit) — into bounded requests. With
`Unpaged+Offset` it returns the whole suffix after that offset. It requests one
first page, then follows any cursor edge it receives. It uses the caller's
sort, or an explicit primary-key sort when none was supplied; that avoids the
far endpoint's `MaxOffset` as well as its maximum page size. An endpoint with
restrictive allow-lists must therefore permit that sort and cursor's
primary-key filter (or the caller supplies an allowed unique sort).

There is one intentional exception: `Distinct` with an explicit projection
that excludes the primary key cannot be safely keyset-sorted without changing
the distinct result. That shape stays on offset pages; a custom list that emits
no cursor edge does too, and in both cases its `MaxOffset` must cover the
export. Otherwise the client returns the endpoint refusal instead of treating
that refusal as a successful partial result. A non-empty cursor page with an edge is followed even when `HasNext`
or `HasPrev` is stale; the terminal empty page reached through that edge proves
completion. A custom cursor response with no terminal edge instead declares its
end through the matching `HasNext`/`HasPrev` flag. An empty page claiming more,
a repeated/missing edge, or an inconsistent offset total returns
`*remote.PartialResultError` (`errors.Is(err, remote.ErrPartialResult)`). Only
an explicit offset without `Unpaged`, or an explicit page/limit without
`Unpaged`, retains ordinary one-subset semantics.

Like any multi-request network read, this is an enumeration, not a transaction
snapshot: rows inserted, deleted or reordered by the far service while it walks
can move between pages. The consistency checks catch a contradictory protocol,
not a changing dataset. An export that needs one database snapshot belongs to a
far-side export/snapshot operation with its own contract.

**Refused, before anything is sent** — a `*remote.OptionError`:

| Option | Why |
|---|---|
| `crud.NarrowRelations` | a relation scope follows a preload into another table, and no filter document reaches there. A `security.Gate` over a remote resource must fail loudly rather than leak |
| `crud.ForUpdate` | a row lock belongs to a transaction, and the transaction is not in this process |
| `crud.Aggregate` / `crud.GroupBy` | no binding serves an aggregate route |

and inside a filter, a `*crud.PredicateError` ([[D-054]]):

| Predicate | Why |
|---|---|
| `crud.Raw` | it is SQL, and a filter document carries field paths and values |
| `crud.EqField` | the DSL compares a field to a value, never to another field |
| `crud.False`, `crud.Or()` | they match no rows, and no document says that |

**Accepted, and cannot be honoured** — named here because a silently dropped
option is the one failure a caller cannot see:

- `crud.PrimaryOnly` — the DSL has no word for "do not serve this from a
  replica". Where a replica lags, the far service is what has to be configured.
  It is accepted rather than refused because `security.Gate` sets it on nearly
  every call.
- `crud.Unsorted` — an empty sort in the document means "the service decides",
  not "no order". The rows are the same rows.

## Patch DTOs

Use the one `cmd/vv` generates. A hand-written DTO whose `crud.Opt` fields
lack `omitzero` marshals an undefined value as `null`, so a patch of one column
would empty every other nullable column in the row. `remote.New` refuses such a
DTO at start-up rather than letting it reach a database.

```go
type ArticleInput struct {
    Title *string          `json:"title,omitempty"`
    Views *int             `json:"views,omitempty"`
    Note  crud.Opt[string] `json:"note,omitzero"`   // this tag is load-bearing
}
```

## Transports

| Transport | Where | Options |
|---|---|---|
| `remotehttp.Transport(baseURL, …)` | `vv/remote/remotehttp` | `WithClient(*http.Client)`, `WithMaxResponse(int)`, `WithRequestHook(func(*http.Request) error)` |
| `crudgrpc.Transport(conn, name, …)` | `vv/crud/rpc/crudgrpc` | `WithVocabulary(*errs.Codes)`, `WithCallOptions(…)` |

A transport lives with the binding it calls, so the table that turns a status or
a code back into a class sits in the same file as the one that produced it
([[D-045]]).

**There is one HTTP client, not three.** A consumer calling out uses `net/http`
whatever it serves with, and the three HTTP bindings register the same routes.

`WithRequestHook` is where an `Authorization` header, a trace header or an
`Accept-Language` goes:

```go
remotehttp.Transport(base, remotehttp.WithRequestHook(func(r *http.Request) error {
    r.Header.Set("Authorization", "Bearer "+token)
    return nil
}))
```

Two differences a caller can see ([[FL-013]]):

- A filtered `GetByID`, including one with a narrowed or capped preload, uses List on
  both transports so the document crosses intact. An unfiltered HTTP entity
  read stays `GET /{id}` and therefore carries only projection and plain
  preload paths; gRPC can carry that same shape as a document.
- Over gRPC, 422 and 400 are one `InvalidArgument`; the machine code undoes the
  collapse, so `errs.AsFault(err).Kind` still tells them apart.

## Writing your own transport

Implement one method:

```go
type Transport interface {
    Do(ctx context.Context, call remote.Call) (json.RawMessage, error)
}
```

A `remote.Call` carries a method, a text key, a JSON key array, a query document
and a raw body — and nothing about a URL, a header or a connection. A failed
call must come back as a `*errs.Fault` built with `port.FaultFrom`, which is
what keeps the caller's `errors.Is` branch working.

## See also

[[UC-018]] · [[FL-018]] · [[D-053]] · [[D-054]] · [[D-045]] ·
[crudhttp](crudhttp.md) · [crudgrpc](crudgrpc.md) · [port](port.md)
