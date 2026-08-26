# port — the transport-neutral half

```go
import "github.com/frostgrove/vv/port"
```

**Module:** root · **Depends on:** `crud`, `query`, `errs`, and the standard
library · **Contract manifest:** yes ([[D-048]])

Everything a CRUD resource does that is not HTTP. Eight commands, one `Service`
interface, the mapper seam, the path chain and the violation pipeline. Four
transports are shells over it — and one service value mounts on all four
unchanged ([[D-045]]).

**Import it when** you put business rules between the transport and the
repository, when you write a mapper, or when you build a transport of your own.

---

## The Service

```go
type Service[M any, ID comparable, U any] interface {
    Meta() *crud.Meta

    List(ctx, ListCommand)                 (crud.PaginatedResponse[M], error)
    Count(ctx, CountCommand)               (int64, error)
    Get(ctx, GetCommand[ID])               (M, error)
    Create(ctx, CreateCommand[M])          (M, error)
    Update(ctx, UpdateCommand[ID, U])      (M, error)
    Replace(ctx, ReplaceCommand[ID, M])    (M, error)
    Delete(ctx, DeleteCommand[ID])         (int64, error)
    DeleteMany(ctx, BulkDeleteCommand[ID]) (int64, error)

    Paths() errs.Resolver
}
```

Three type parameters and not four: the `Mapper` runs **before** the service, so
a transport's input type never reaches here. That is what lets one value mount
everywhere.

`port.NewService(repo, opts...)` builds the default one — the orchestration and
nothing else: what a create request may not dictate, what a count drops from the
query document, what a `PUT` has to find before it replaces.

| Option | Does |
|---|---|
| `WithQuery(cfg)` | bound the query DSL ([query.Config](query.md#bounding-it)) |
| `AllowClientID()` | let a create request choose its own database-generated key |
| `WithPaths(r)` | add this service's hop to the path chain |

## Business rules between transport and repository

Embed and override. The handler never notices ([[UC-013]]).

```go
type articleService struct {
    *port.DefaultService[Article, int64, ArticleUpdate]
}

func (s articleService) Create(ctx context.Context, cmd port.CreateCommand[Article]) (Article, error) {
    if err := s.checkQuota(ctx, cmd.Model); err != nil {
        return Article{}, err
    }
    return s.DefaultService.Create(ctx, cmd)
}

svc := articleService{port.NewService(articles)}

crudfiber.Serving(svc).Routes()
crudgin.Serving(svc).Mount(r, "/articles")
crudnet.Serving(svc).Mount(mux, "/articles")
crudgrpc.Serving(svc).Register(srv, "Article")
```

**The same value on all four**, and one integration test compares the *command*
each binding handed over rather than the answer, so a binding that re-derives a
rule is named.

Passing a plain **repository** instead of a service works everywhere too — the
binding wraps it in a `DefaultService` for you. `New`, `NewFor`, `Serving` and
`ServingFor` are the four constructors every binding carries.

## The commands

| Command | Carries |
|---|---|
| `ListCommand` | `Query *query.Request`, `Options []crud.Option` |
| `CountCommand` | the same, narrowed |
| `GetCommand[ID]` | `ID`, `Query`, `Options` |
| `CreateCommand[M]` | `Model M`, `Before func(*M) error` |
| `UpdateCommand[ID, U]` | `ID`, `Patch U`, `Before` |
| `ReplaceCommand[ID, M]` | `ID`, `Model M`, `Before` |
| `DeleteCommand[ID]` | `ID` |
| `BulkDeleteCommand[ID]` | `IDs []ID` |

`Options` are appended **after** the query document compiles, so a transport
scope narrows the client's filter instead of replacing it ([[D-004]]).

---

## The mapper seam

A `Mapper` turns a transport's input type into the model, before the service ever
sees it.

```go
type Mapper[In, M any] interface {
    Model(ctx context.Context, in In) (M, error)
}
```

```go
type ArticleInput struct {
    Title    string `json:"title"`
    AuthorID int64  `json:"authorId"`
}

func (ArticleMapper) Model(ctx context.Context, in ArticleInput) (Article, error) {
    return Article{Title: in.Title, AuthorID: in.AuthorID}, nil
}

crudnet.ServingFor(svc, ArticleMapper{}).Mount(mux, "/articles")
```

`port.Identity[M]()` is the no-op one, and what `New`/`Serving` use when you pass
no mapper: the model *is* the wire shape.

A mapper **may also** implement `errs.Resolver`. One that does contributes the
adapter's hop to the path chain, because it is the layer that performed the
mapping and is therefore the only one that can invert it. [`cmd/vv
-adapter`](vv-cli.md) generates exactly that.

---

## The path chain

A violation happens at a column. A client wants to hear about the key it sent.
Between the two are several mappings, and **each layer translates one hop and
only its own** ([[D-043]]).

```
column ──► model field ──► command field ──► the key the client sent
  faults          port.Fields        the mapper's PathMap
```

| Type | Written by | Undeclared head |
|---|---|---|
| `port.Fields` | you, by hand | **passes through** — a hand-written map is partial by nature |
| `port.PathMap` | the generator | **declines**, and the violation is marked approximate |

That difference is the whole reason to generate one. A `PathMap` is derived from
the model and validated against it at package initialisation, so it is *total*:
every column a client can write has an entry. An undeclared head is therefore not
a gap — it is a column of another table — and honest beats invented ([[D-050]]).

```go
var ArticlePaths = port.MustPathMap[Article](port.PathMap{
    "Title":    port.At("title"),
    "AuthorID": port.At("authorId"),
})
```

`MustPathMap` refuses at start-up when the map is not exact **and** total: a
missing entry, and equally an entry for a `generated` column or the lock — either
one translates a violation to a key the client cannot find in its own body.

`port.At("shipping", "line1")` builds a path. `port.Hops(svc, mapper)` collects
the declared hops in order, which is what a binding wires ahead of its own
fallback.

## The violation pipeline

```go
vs := port.Violations(ctx, fault, port.ViolationOptions{
    Resolvers: port.Hops(svc, mapper),
    Fallback:  porthttp.BodyResolver(rawBody),
    Messages:  catalogue,
    Codes:     codes,
    Max:       port.MaxViolations,   // 100
})
```

Five steps, and **the order is load-bearing**: copy, path chain, sort, cap,
message.

- **Messages come after path translation**, because the ladder is derived from
  the path — expanding first would key a catalogue entry on the model's field
  name on one deployment and on the client's on another.
- **The cap comes after the sort**, so what survives is the front of a total
  order rather than whatever the classifier happened to append first.
- **`Fallback` is a separate field, not a last resolver**, so a declaration always
  beats a guess. It runs only for a path no declared hop changed.

The locale is read from the context (`port.WithLocale`, `port.LocaleFrom`) rather
than passed, so a transport that installed it gets the same ladder whichever
renderer runs.

Nothing writes through to the fault: it is a value two goroutines may render at
once.

## Classification happens once, here

```go
port.KindOf(err)              errs.Kind
port.KindOfWith(err, codes)   errs.Kind, through your vocabulary
port.CodeForKind(k)           errs.Code
port.FaultOf(err)             *errs.Fault
```

`port/porthttp` turns a `Kind` into a status and `crud/rpc/crudgrpc` turns it into a
`codes.Code` — **one classification, spelled per transport**, rather than one per
framework ([[UC-015]]).

## The shared request helpers

Every binding uses these, and they are exported because a hand-rolled endpoint
wants the same rules:

| | |
|---|---|
| `Sanitize(meta, *m, allowClientID)` | clear what a client may not choose on create |
| `ClearGenerated(meta, *m)` | clear every `generated` column |
| `CoerceID[ID](raw)` | a path parameter becomes the key type |
| `NarrowForEntity(req)` / `NarrowForCount(req)` | drop what a single-entity or count request may not ask for |
| `BadRequest(err)` · `BadRequestf` · `BadRequestAs(code, path, …)` | build a 400 with a path named |
| `CoversUpdate[M, U]()` / `MustCoverUpdate[M, U]()` | the DTO still covers every writable column |
| `FirstLanguageTag(list)` | the first tag out of an `Accept-Language`-shaped list |

## The rules a binding does not own

Five of a handler's settings say nothing about a transport: what a client may
ask for, what it may not choose, and how much may arrive at once. They live here
once, and each binding embeds them:

```go
type Rules struct {
    Query         *query.Config  // WithQuery
    ReadOnly      bool           // ReadOnly
    AllowClientID bool           // AllowClientID
    MaxBulk       int            // MaxBulk, read through BulkCap()
    MaxBody       int            // MaxBody
}
```

`Rules.Service()` turns the two that belong to the service into
`port.ServiceOption`s. `Rules.RefuseServiceOptions(who)` is the start-up panic a
binding raises when one of those is handed to `Serving`, which was already built
([[D-021]]).

The option *constructors* stay in the bindings, because each binding's `Option`
is its own three-parameter type ([[D-045]]) and a shared constructor could not
return one. What is shared is the state and the two methods over it — which is
the half that used to be copied four times, and where `MaxBody` had to be written
four times to exist at all.

`Rules.BulkCap()` answers how many ids a bulk delete accepts: the field when it
is set, and `DefaultMaxBulk` (1024) otherwise. A method rather than a defaulted
field, so the four transports cannot disagree about what an unset `MaxBulk` means
— which is how they came to agree it meant no cap at all ([[D-060]]).

`MaxBody` is honoured by the three HTTP bindings only; gRPC bounds a message at
the server with its own `MaxRecvMsgSize`, before a handler runs.

`crudhttp.Rules` is an alias of this type, under the name an HTTP binding looks
for.

## Where the library's own log lines go

```go
port.Logger(ctx)                 // the context's logger, or slog.Default()
ctx = port.WithLogger(ctx, log)  // put one there
```

This library writes a line only where something failed that nobody can be
returned an error for: a handler panicked and the connection has to be closed, a
response would not marshal, a status could not carry its details, a refusal could
not be written. Nine call sites across four transports and the shared auth half.

They go through `Logger` rather than `log.Printf` so an application can give them
the request's trace id, route them to its own handler, or silence them — without
touching the process-wide default every other library in the binary shares
([[D-062]]). `Logger` never returns nil, and `WithLogger(ctx, nil)` stores
nothing.

**Statements are a different question** and have a different seam: wrap
`crud.Source`. See [crud](crud.md#wrapping-a-source--for-tracing-timing-statement-logs).

## See also

- [porthttp](porthttp.md) — the HTTP projection of the error contract and the status table
- [crudhttp](crudhttp.md) — what is HTTP *and* CRUD
- [crudnet](crudnet.md) · [crudfiber](crudfiber.md) · [crudgin](crudgin.md) · [crudgrpc](crudgrpc.md)
- [cmd/vv](vv-cli.md) — generates the mapper, the path map and a service shell
- [[UC-013]] business rules between handler and repository · [[FL-015]] a request through the port layer
- [[D-045]] the shared half is transport-neutral · [[D-050]] the generated adapter is total
