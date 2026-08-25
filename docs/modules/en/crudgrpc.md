# crudgrpc — a full CRUD API on gRPC

```go
import "github.com/shardit-io/vv/crud/rpc/crudgrpc"
```

```bash
go get github.com/shardit-io/vv/crud/rpc/crudgrpc
```

**Module:** its own — so a consumer on HTTP never takes gRPC, protobuf and
genproto as dependencies ([[D-033]], [[D-051]])

Eight methods, one per `port` command, over `google.protobuf.Struct` documents.
**There is no `.proto` to write and no `protoc` to install.** The same service
value that mounts on Fiber, Gin and `net/http` mounts here and answers the same
thing.

---

## Mount it

```go
articles := specs.Executor(Articles.Bind(db, security.Gate(policy)))

srv := grpc.NewServer(grpc.UnaryInterceptor(crudgrpc.Errors()))
crudgrpc.New(articles).Register(srv, "Article")

srv.Serve(lis)
```

That mounts `vv.crud.v1.Article`. A name already carrying a package is used
verbatim, so you can put resources in a package of your own.

## The eight methods

| Method | Request | Response |
|---|---|---|
| `List` | the query document | a page |
| `Count` | the query document | `{"count": n}` |
| `Get` | `{"id": "42", "query": {…}}` | the entity |
| `Create` | the entity document | the entity |
| `Update` | `{"id": "42", "patch": {…}}` | the entity |
| `Replace` | `{"id": "42", "entity": {…}}` | the entity |
| `Delete` | `{"id": "42"}` | `{"deleted": n}` |
| `BulkDelete` | `{"ids": ["1","2"]}` | `{"deleted": n}` |

Eight, against HTTP's ten. The two that disappear are the doubled doors: HTTP has
`GET /` **and** `POST /query` for a list because a query string and a JSON body
are two ways in. gRPC has one request message, so there is one of each.

`ReadOnly()` registers the three reads and leaves the five writes unregistered —
gRPC then answers `Unimplemented` on its own.

## The four constructors

```go
crudgrpc.New(repo, opts...)                  // a repository; the model is the wire shape
crudgrpc.NewFor(repo, mapper, opts...)       // …with a request document of its own
crudgrpc.Serving(svc, opts...)               // a port.Service — your business rules
crudgrpc.ServingFor(svc, mapper, opts...)    // both
```

`crudgrpc.Repository` and `crudgrpc.Service` are **aliases** for the `port`
types, so one value serves all four transports:

```go
svc := articleService{port.NewService(articles)}

crudfiber.Serving(svc).Routes()
crudgin.Serving(svc).Mount(r, "/articles")
crudnet.Serving(svc).Mount(mux, "/articles")
crudgrpc.Serving(svc).Register(srv, "Article")
```

## Options

Same names as every other binding, with `context.Context` where the HTTP ones
take their framework's context.

| Option | Does |
|---|---|
| `WithQuery(cfg)` | bound the DSL — [`query.Config`](query.md#bounding-it) |
| `WithScope(fn)` | `func(context.Context) ([]crud.Option, error)` |
| `WithTransform(fn)` | a presenter: `func(context.Context, M) any` |
| `BeforeSave(fn)` | `func(context.Context, *M) error` |
| `BeforeUpdate(fn)` | `func(context.Context, ID, *U) error` |
| `ReadOnly()` | register the three reads only |
| `AllowClientID()` | let a create choose its own database-generated key |
| `MaxBulk(n)` | cap `BulkDelete` |
| `WithRenderer(r)` | replace the status renderer |

**There is no `WithErrorHandler`.** A gRPC response is a return value rather than
a stream a handler may have half-written, so there is nothing for a handler to
take over: `WithRenderer` is the whole seam.

## Errors

```go
srv := grpc.NewServer(grpc.UnaryInterceptor(crudgrpc.Errors(
    crudgrpc.WithMessages(catalogue),
    crudgrpc.WithCodes(codes),
)))
```

| Kind | Code |
|---|---|
| `KindNotFound` | `NotFound` |
| `KindUnauthorized` | `Unauthenticated` |
| `KindForbidden` | `PermissionDenied` |
| `KindRetryable` | `Unavailable`, with `RetryInfo{1s}` |
| `KindConflict` | `AlreadyExists` |
| `KindValidation` · `KindBadRequest` | `InvalidArgument` |
| anything else | `Internal` |

A failure arrives as a status code plus **`BadRequest` / `ErrorInfo` /
`RetryInfo` details** rather than the JSON envelope. The machine codes in those
details are spelled identically to the HTTP `error_code`, so a client needs one
table ([[D-052]]).

`crudgrpc.Code(err)` is exported if you answer your own calls.
`crudgrpc.CodeFor(kind)` is the table itself.

**Two codes collapse, and it is a cost rather than a bug.** `KindValidation` and
`KindBadRequest` both answer `InvalidArgument`, and every conflict answers
`AlreadyExists` — including `restrict` and `stale_version`. Refining per *code*
would be a second table keyed on the thing [[D-049]] says must not decide a
response.

**A status message is never `err.Error()`.** A fault's own text names the entity,
and a table name in a status message is the disclosure [[D-044]] closes. What is
used is the message ladder's answer. An `Internal` status says `internal` and
carries no details at all.

The locale comes from metadata: `grpc-accept-language`, `accept-language` or
`x-locale`. `crudgrpc.WithLocale(ctx, l)` sets it directly.

Installing `Errors` twice renders once — the marker is the error already carrying
a status, so the interceptor never overrides a method that answered for itself.

---

## Calling another service

There is no generated stub on this side either. Every method is one
`google.protobuf.Struct` in and one out, so a call is `grpc.Invoke` with the
document in it — the property the Struct shape was chosen for ([[D-052]]), read
from the other side.

```go
articles := remote.New[Article, int64, ArticleInput](
    crudgrpc.Transport(conn, "Article"))
```

`name` is what the far side passed to `Register`, and `ServiceName` turns it
into the same full service name on both ends from the same function.

| Option | Does |
|---|---|
| `WithVocabulary(*errs.Codes)` | the codes the class is sharpened through — the same value the far side's renderer was given |
| `WithCallOptions(...grpc.CallOption)` | per-call credentials, a compressor, a size limit |

`KindForCode` is `CodeFor` read backwards, and it is where the one collapse this
transport makes is undone: `InvalidArgument` is both 422 and 400, so the class
comes back coarse and the `ErrorInfo.Reason` sharpens it. A caller gets
`errs.KindValidation` or `errs.KindBadRequest`, told apart by the code.

A status this library did not write — `Unimplemented` from a method a
[`ReadOnly`](#options) service never registered, anything from an interceptor in
between — arrives as a `*remote.ProtocolError` and never as a class. See
[remote](remote.md) and [[FL-018]].

## Four limits, stated rather than discovered

**There is no schema for a resource.** A repository is generic over its model, so
no compiled proto message for it can exist in a library. Every request and
response is a `google.protobuf.Struct` carrying the same JSON document the HTTP
bindings speak — which is exactly what lets one service value serve all four
([[D-052]]).

**Server reflection cannot describe the service.** A generic resource has no file
descriptor, so grpcurl and its kind cannot list the methods. Clients call by full
method name, or the application registers a descriptor it generated itself.

**A number in a Struct is a double.** `google.protobuf.Value` has no integer, so
an `int64` above 2⁵³ loses precision *in an entity document*. **Keys do not**: a
request carries `id` as a string and `port.CoerceID` converts it the same way an
HTTP path parameter is. A model needing exact large keys in its entity as well
declares `json:"id,string"`.

**There is no raw-body fallback.** The HTTP bindings keep the decoded bytes so a
violation on a column nothing declared can still name the key the client sent.
Here the declared hops — the service's and the mapper's — are the whole chain,
and a path nothing owns is marked approximate rather than guessed ([[D-043]]).

## `crud.Opt` survives the wire

Conversion goes through `protojson` and `encoding/json`, so the model's own
`json` tags decide the document and `crud.Opt` keeps its three states: an absent
key is **not in `Struct.Fields`**, an explicit null is a `NullValue` entry
([[UC-003]]).

## Constants

```go
crudgrpc.ServicePrefix      // "vv.crud.v1."
crudgrpc.ErrorDomain        // "vv" — the ErrorInfo domain
crudgrpc.PartialKey         // "partial" — the ErrorInfo metadata key
crudgrpc.DefaultRetryDelay  // time.Second
crudgrpc.LocaleKeys         // the metadata keys read for a locale
crudgrpc.MaxViolations      // 100
```

## See also

- [crudnet](crudnet.md) · [crudfiber](crudfiber.md) · [crudgin](crudgin.md) — the same API on HTTP
- [port](port.md) — the commands and the violation pipeline this shares with them
- [`_examples/pgx-grpc`](../../../_examples/pgx-grpc/)
- [[FL-013]] a request through another binding
- [[D-052]] a gRPC resource carries documents, not a schema · [[D-051]] a satellite carries one dependency decision
