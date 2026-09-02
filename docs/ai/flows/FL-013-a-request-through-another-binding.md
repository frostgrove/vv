# FL-013 — A request through another binding

**Entry point:** `crud/http/crudgin/handler.go:List`, `crud/http/crudnet/handler.go:List` and `crud/rpc/crudgrpc/handler.go:List` (and every sibling method)
**Implements:** [[UC-001]] [[UC-002]] [[UC-013]] [[UC-015]] · **Governed by:** [[D-045]] [[D-033]] [[D-022]] [[D-012]] [[D-015]] [[D-051]] [[D-052]]

There are four bindings and one path. This flow is the short document that says
where they part company: what `crudgin`, `crudnet` and `crudgrpc` do that
`crudfiber` does not, and where a reader should stop reading this file and go
read [[FL-001]], [[FL-002]], [[FL-003]], [[FL-011]] or [[FL-012]] instead.

Everything from the command onwards is identical, by construction rather than by
discipline. The shared half is three packages: `port` for what is not HTTP,
`port/porthttp` for what is HTTP but belongs to no subsystem, and
`crud/http/crudhttp` for what is HTTP *and* CRUD. All four bindings call into
`port`; the three HTTP ones call into `porthttp` and `crudhttp` as well
([[D-045]], [[D-059]]). [[FL-015]] traces that half; this file is only about the
four doors into it.

`crudnet` is the one that costs nothing. It imports only the standard library,
so unlike the other three it is not a module of its own: it ships inside the
library ([[D-033]]).

`crudgrpc` is the one that is not HTTP, and it is what phase 9 measured
[[D-045]] with. Writing it changed no line of `errs`, `errs/sqlerr`, `port` or
the HTTP half — see that decision's *Proven by*, where the measurement is
spelled out.

## The path

1. **`HandlerFor.List`** — `crud/http/crudgin/handler.go:164` — a `gin.HandlerFunc`, so
   `func(*gin.Context)` with no error return. Where the Fiber handler returns an
   error for the framework to render, this one writes the response and returns.
2. **`HandlerFor.parseQueryString`** — `crud/http/crudgin/handler.go:419` — reads
   `c.Request.URL.Query()`, which is already a `url.Values` with every repeat
   intact. Fiber needed `queryValues` walking `QueryArgs().VisitAll` to get the
   same thing; Gin's own `c.Query` has the same collapsing hazard and is not
   used. From here it is `query.ParseQuery` and [[FL-001]].
3. **`HandlerFor.parseBody` → `decodeOnly` → `decode`** — `crud/http/crudgin/handler.go:427` —
   `porthttp.DecodeJSONKeepLimit` over `c.Request.Body`, bounded by this
   handler's `MaxBody` ([[D-063]]). An empty body is not an error:
   `POST /count` and `POST /query` both mean "no narrowing" when sent with none.
4. **`HandlerFor.scope`** — `crud/http/crudgin/handler.go:391` — `WithScope`'s
   options, if any, and nothing else. It is the whole of what a binding
   contributes to a read now: the compile happens in the service, which
   *appends* these afterwards because `crud.Where` ANDs ([[D-004]]).
5. **The service call** — `h.svc.List(c.Request.Context(), port.ListCommand{…})`.
   The context is where a principal reaches the security gate, so an
   application's own middleware puts it there with
   `c.Request = c.Request.WithContext(ctx)`. The handler contributes nothing but
   the pass-through ([[FL-007]]). From here it is [[FL-015]].
6. **`HandlerFor.entity` → `writeJSON`** — `crud/http/crudgin/handler.go:453`,
   `crud/http/crudgin/options.go:151` — one status, one body, marshalled before
   the status is written. `crud.MapPage` renders a page through `WithTransform`
   exactly as in [[FL-001]].
7. **`HandlerFor.fail` → the error handler → `render`** —
   `crud/http/crudgin/options.go:183` — the renderer decides the status, any header
   and the body, then `c.Error(err)` records the cause for Gin's logging
   middleware and `write` puts the response on the wire. The error text still
   never reaches a 500 body ([[FL-011]]).

## What keeps the three in step

`make check-triplets` compares the test names of `crudnet`, `crudgin` and
`crudfiber` — and of the auth triplet, [[FL-019]] — and fails when they disagree.
A test that only makes sense for one binding goes in that binding's
`routing_test.go` (or `binding_test.go` for auth), which the check exempts, and
the difference it pins is written down in this document.

The rule was in `CLAUDE.md` and nowhere else, so it held by everyone remembering
it — and it had already stopped holding. `crudfiber` was the one binding of three
with no `routing_test.go`, on the one framework whose router matches in
registration order.

## Where the bindings differ

Every option name, every status code and every response shape is the same in all
three. What differs is mounting and what the router does with a path or a method
it does not have.

Body decoding used to be on that list and is not any more. `crudfiber` bound
through `c.Bind().Body()`, which dispatches on Content-Type — so the same request
sent as a form or as XML was a 201 on Fiber and a 400 on the other two, and a
`binding:"required"` tag in the consumer's own model changed what the routes
accepted, under one framework only. All three now decode with
`porthttp.DecodeJSONKeepLimit`, for the reason that function's doc comment gives:
a framework binder validates, and validation is not the binding's to add.

| | `crudfiber` | `crudgin` | `crudnet` | `crudgrpc` |
|---|---|---|---|---|
| module | its own | its own | the library's — stdlib only | its own |
| dependencies | Fiber | Gin | none | grpc + protobuf + genproto — one decision ([[D-051]]) |
| protocol | HTTP | HTTP | HTTP | gRPC over HTTP/2 |
| mount | `app.Use(prefix, h.Routes())` | `h.Mount(r, prefix)` | `h.Mount(mux, prefix)` | `h.Register(srv, "Widget")` |
| handler type | `func(fiber.Ctx) error` | `gin.HandlerFunc` | `http.HandlerFunc` | `func(context.Context, *structpb.Struct) (*structpb.Struct, error)` |
| hooks carry | `fiber.Ctx` | `*gin.Context` | `*http.Request` | `context.Context` |
| request bodies | JSON | JSON | JSON | `google.protobuf.Struct` |
| body cap | `MaxBody`, 4 MiB ([[D-063]]) | the same | the same | gRPC's own `MaxRecvMsgSize` |
| response write | `writeJSON` — marshal, then status | the same | the same | `answer`, the same shape |
| a response that will not encode | a silent 500 | the same | the same | `Internal` |
| Content-Type | `application/json; charset=utf-8` | the same | the same | n/a |
| body past the cap | 413, the envelope | the same | the same | `ResourceExhausted` |
| routes / methods | 10 routes | 10 routes | 10 routes | 8 methods |
| query-string door | yes | yes | yes | **no** — one document, always |
| `/x` vs `/x/` | both | `/x`, and 301 from `/x/` | both | n/a |
| unmounted verb | 405 | 404, or 405 with `HandleMethodNotAllowed` | 405 | `Unimplemented` |
| unclaimed path | 404 | 404 | 404 | `Unimplemented` |
| renderer | `porthttp.Renderer` | `porthttp.Renderer` | `porthttp.Renderer` | `crudgrpc.Renderer` — a `*status.Status` |
| failure shape | the envelope | the envelope | the envelope | a status code plus `BadRequest` / `ErrorInfo` / `RetryInfo` details |
| status vocabulary | 404/401/403/503/409/422/400/413/500 | the same | the same | `NotFound`/`Unauthenticated`/`PermissionDenied`/`Unavailable`/`AlreadyExists`/`InvalidArgument`/`ResourceExhausted`/`Internal` — **422 and 400 collapse** |
| retry hint | `Retry-After: 1` | the same | the same | `RetryInfo{1s}` |
| locale from | `Accept-Language` | the same | the same | `grpc-accept-language`, `accept-language`, `x-locale` metadata |
| raw-body fallback | yes | yes | yes | **no** — declared hops only |
| schema / reflection | n/a | n/a | n/a | **none** — a generic resource has no descriptor ([[D-052]]) |
| double-install marker | the response writer | the response writer | the response writer | the error already carrying a status |
| **client transport** | — | — | `remotehttp.Transport` | `crudgrpc.Transport` |

### Calling out, and why there are two rows and not four

The last row is the one that breaks the pattern the other rows keep. Every
binding has a column because every binding serves; only two have a transport
because **a consumer calling out uses `net/http` whatever it serves with**.
`crudfiber` and `crudgin` are server frameworks, and a service on Fiber that
calls another service does not call it through Fiber. The three HTTP bindings
register the same routes, which their own `routing_test.go` triplets prove, so
one HTTP client reaches all three.

That makes the client the one thing in this repository outside the
change-one-binding-change-all-three rule. `remote/roundtrip_test.go` exists once
and runs against `crudnet`, which is the stdlib one and therefore the one where
nothing in the path is a framework's doing.

Three differences a caller can see, in the same direction the table above reads
([[FL-018]]):

| | over HTTP | over gRPC |
|---|---|---|
| the fault's own code | read off the first violation — the envelope carries one per violation and no field for the fault's | verbatim in `ErrorInfo.Reason` |
| a narrowed or capped preload on `GetByID` | sent through the List fallback with the primary-key equality | the same List fallback; the document crosses unchanged |
| a 422 and a 400 | distinct statuses | one `InvalidArgument`, undone by the code ([[D-052]]) |
| an answer from elsewhere | `Envelope.Type` says whether this library wrote it | the `ErrorInfo` domain does |

The last row is the same trap in two costumes. A router's 404 and an
`Unimplemented` both look like an answer about a row and are answers about an
address; a client that read the status alone would turn a wrong base URL or a
read-only service into "the row is not there".

The Gin-specific detail behind that table:

- **Mounting.** Gin has no mountable sub-application, so there is no counterpart
  to `Routes() *fiber.App`. `Mount(r gin.IRouter, prefix string)` is the
  one-liner and `Register(r gin.IRoutes)` takes an engine or a group that already
  exists.
- **The collection route is registered as `""`, not `"/"`.** On a group of
  `/widgets` the `"/"` form produces `/widgets/`, which does not match
  `GET /widgets`. Registering both is not an option: on the engine itself they
  collapse to the same path and Gin panics with "handlers are already registered".
  The trailing-slash spelling is left to Gin's `RedirectTrailingSlash`, on by
  default, which answers 301.
- **JSON bodies only.** Fiber's `Bind().Body()` dispatches on Content-Type and
  accepts XML and form encodings. `crudgin` decodes with `encoding/json`, and
  deliberately does not use `c.ShouldBindJSON`, because Gin's binder runs
  `validator/v10` over `binding:"…"` tags — a tag on a consumer's model would
  then change what the CRUD routes accept under one transport and not the other
  ([[D-045]], and [[D-034]] before it; the guarantee it protects is UC-003's).
- **An unmounted route is 404, not 405.** Gin answers 404 for a path that exists
  under another verb unless `Engine.HandleMethodNotAllowed` is set. That is the
  application's setting to make, and the handler does not touch it.

And the two that are specific to `crudnet`:

- **The collection is registered under both spellings.** `ServeMux` has no
  trailing-slash redirect of its own — `/articles` and `/articles/` are two
  patterns and the unregistered one answers 404 — so `Mount` registers both.
  Gin needed the opposite treatment: registering both there collapses to one
  path and panics.
- **At the root the collection is `"/{$}"`, never `"/"`.** A bare `/` is
  `ServeMux`'s catch-all: mounted at the root it would answer for every path in
  the process that no other pattern claims, returning 200 and a page of rows
  where the application meant 404. `{$}` matches the root path and nothing else.

### Two body-cap differences that remain, and are the framework's

**Fiber refuses before the handler when it owns the app.** `fiber.New()` carries
a `BodyLimit` of its own, and it runs before any handler. `Routes()` therefore
builds its app with `BodyLimit` set to this handler's cap plus one, so the body
at the cap reaches our decoder and the body past it is refused by us, with our
envelope. `Register()` cannot do that — the limit belongs to the app, which the
caller owns there — so a Fiber consumer who mounts with `Register` on their own
app gets Fiber's plain-text 413 above whatever cap they set on the app, and ours
below it. `TestTheStandaloneAppCarriesTheHandlersBodyCap` pins the half this
library controls.

**gRPC has its own receive limit and it is not ours.** `MaxRecvMsgSize` is the
server's, defaults to 4 MiB and refuses with `ResourceExhausted` before a handler
runs — which is the same code `KindTooLarge` maps to, so a client sees one
answer either way. `crudgrpc` therefore adds no cap of its own: the message is
already bounded before it is unmarshalled, and a second limit inside would refuse
at a number the server operator did not set.

## What is specific to `crudgrpc`

Enough that it is a section rather than rows. The path from the command onwards
is the same one; everything below is the door.

- **Eight methods, one per `port` command** — `List`, `Count`, `Get`, `Create`,
  `Update`, `Replace`, `Delete`, `BulkDelete` — against ten HTTP routes. The two
  that disappear are the doubled doors: HTTP has `GET /` and `POST /query` for a
  list, and `GET /count` and `POST /count` for a count, because a query string
  and a JSON body are two ways in. gRPC has one request message, so there is one
  of each. `ReadOnly` registers the three reads; the five writes are then not
  registered at all, and gRPC answers `Unimplemented` on its own.
- **`google.protobuf.Struct` in and out, never a generated message.** A resource
  is generic over its model, so no compiled proto message for it can exist in a
  library. The document inside the Struct is exactly the JSON the three HTTP
  bindings carry, which is what lets one `port.Service` value serve all four and
  answer the same thing ([[D-052]]). `crud/rpc/crudgrpc/message.go` converts through
  `protojson` and `encoding/json`, so the model's own `json` tags decide the
  document and `crud.Opt` keeps its three states: an absent key is not in
  `Struct.Fields`, an explicit null is a `NullValue` entry.
- **A number in a Struct is a double.** `google.protobuf.Value` has no integer,
  so it cannot carry every `int64` exactly. The API treats integral values at
  magnitude 2^53 and beyond as outside its safe Struct range: a raw caller sends
  `id` or `ids` as decimal strings, while the framework client does that for
  keyed and bulk routes. A model whose entity document needs such values declares
  `json:"id,string"`. `TestGRPCStructNeverRoundsIDsOrPreloadFilterIntegers` pins
  numeric refusal at both signs and exact string recovery;
  `TestAnInt64KeyIsCarriedAsAString` separately shows why an entity needs the
  string tag.
- **No query-string door and no raw-body fallback.** A read is the query
  document or nothing, and `porthttp.WithBody`'s index — which turns a column
  name back into the key the client sent — has no counterpart. The declared hops
  are the whole chain, so a path nothing owns is marked approximate rather than
  guessed ([[D-043]]).
- **The service name is per resource.** `Register(srv, "Widget")` mounts
  `vv.crud.v1.Widget`; a name containing a dot is used verbatim. The
  `grpc.ServiceDesc` is hand-built with a nil implementation, which is what makes
  a generic resource registrable at all — `RegisterService` checks the handler
  type only when it is given one.
- **The double-install marker is the error, not a wrapper.** An HTTP binding
  marks the response writer, because a response is a stream a handler may have
  half-written. A gRPC response is a return value, so `Errors` passes through any
  error that already carries a status — whether that came from an inner copy of
  itself or from the application's own `status.Error`.
- **A status message is never `err.Error()`.** A fault's own text names the
  entity, and a table name in a status message is the disclosure [[D-044]]
  closes. What is used is the message ladder's answer: the first violation in the
  rendered order, or — for a fault carrying none — the ladder's answer for the
  fault's own code. An `Internal` status says `internal` and carries no details
  at all.

### `unpaged` is refused the same way on all four

`query.Config{AllowUnpaged: true}` is what an endpoint declares to serve whole
result sets, and without it a request carrying `unpaged` is a `query.Error` at
path `unpaged` — a 400 over HTTP and `InvalidArgument` over gRPC ([[D-060]]).

It matters here because the client half of both transports emulates
`remote.GetAll` with that flag: there is no "every row" route. So a resource
meant to be read whole by a remote caller declares it once, at the far end, and
the fixtures in `remote/fake_test.go` and `crud/rpc/crudgrpc/client_test.go` do
exactly that rather than hiding it.

## Where the decisions bite

- **The status table is not here, and it is not in the CRUD subsystem either.**
  `crudgin.Status` is one line over `porthttp.Status`, which is one line over
  `port.KindOf`. [[D-045]] forbids a second copy, because two switches over the
  same sentinels drift the day one gains an arm and nothing fails when they do —
  which is exactly what [[FL-011]] predicted before the second binding existed.
  [[D-059]] moved the table out from under `crud/` so the auth middleware could
  read the same one without importing the repository; `crudhttp.Status` still
  exists and still answers, as a forwarder.
- **And neither is the gRPC one, in the half that matters.**
  `crudgrpc.CodeFor` is a second *vocabulary* — `codes.Code` instead of an HTTP
  status — over the same `port.KindOfWith` answer. The kind is decided once; each
  transport spells it. What is emphatically not duplicated is the violations
  pipeline: the copy, the path chain, the sort, the cap and the message ladder
  are `port.Violations`, called by both renderers ([[D-045]], and the follow-up
  it discharged at phase 9).
- **Two codes collapse on gRPC, and that is a cost rather than a bug.**
  `KindValidation` and `KindBadRequest` both answer `InvalidArgument`, so 422 and
  400 are one code; every conflict answers `AlreadyExists`, including `restrict`
  and `stale_version`. Refining per *code* would be a second table keyed on the
  thing [[D-049]] says must not decide a response. The machine code in the
  details is what separates the cases, and it is spelled identically on both
  transports so a client needs one table ([[D-052]]).
- **`Repository` and `Service` are aliases, not second interfaces.**
  `crudgin.Repository` and `crudfiber.Repository` are the same type, and so are
  `crudgin.Service` and `crudfiber.Service` ([[D-022]], [[D-045]]). The
  integration suite mounts one `articleService` on all three engines through
  `New`, and one `articlePort` through `Serving`; either would stop compiling if
  a binding declared its own.
- **PUT still refuses to create, and the binding is not where it happens.**
  `DefaultService.Replace` does the existence probe, the `ClearGenerated` and the
  `SetID` from the command's key, in that order ([[D-012]]). The four bindings
  each hand it the same `port.ReplaceCommand` and do nothing else — on gRPC the
  method is `Replace` and the key comes off the request document rather than a
  URL, which is the whole of the difference. The
  PostgreSQL sequence hazard the decision exists for is engine-level, so it
  reaches every binding unchanged.
- **Gin is not a dependency of anybody else.** `crud/http/crudgin` is its own module
  ([[D-033]]); sonic, validator/v10, quic-go, protobuf and mongo-driver arrive
  with it and reach no other consumer. `crud/rpc/crudgrpc` is its own module for the
  same reason, with three requires that are one decision ([[D-051]]).

## Failure modes

| What goes wrong | Where it is caught | What the caller sees |
|---|---|---|
| `GET /widgets/count` read as an entity named "count" | Gin's router gives a static segment priority over `:id` | it does not happen; pinned by `TestStaticRoutesAreNotSwallowedByTheIDRoute` |
| `GET /widgets/` with the collection mounted at `""` | `Engine.RedirectTrailingSlash` | 301 to `/widgets` |
| `GET /widgets/abc` where the key is `int64` | `port.CoerceID` (`port/request.go`) | 400, and the envelope names `invalid_id` ([[FL-011]]) |
| a body that is not JSON — including a form or XML body on Fiber, which used to be accepted | `porthttp.DecodeJSONKeepLimit` (`port/porthttp/body.go`) | 400 `malformed_body`, and the service is never called |
| a body larger than 4 MiB | `porthttp.DecodeJSONKeepLimit`, before anything parses it | 413 `too_large` naming the limit; `ResourceExhausted` on gRPC ([[D-063]]) |
| a bulk delete past the cap | each binding's `BulkDelete`, reading `port.Rules.BulkCap()` | 400 `bad_query` naming the cap. It is 1024 by default, where an unset `MaxBulk` used to mean unlimited ([[D-060]]) |
| an `in` list or a sort longer than the endpoint allows | `Request.Compile` | 400 naming the path and the cap ([[D-060]]) |
| a presenter that returns a value JSON cannot encode | each binding's `writeJSON`, before the status is written | a silent 500. It used to be 200 with a truncated body on Gin and a `text/plain` leak of the encoder's message on Fiber ([[D-063]], [[D-044]]) |
| `?unpaged=true` on an endpoint that did not declare it | `Request.Compile` (`crud/query/compile.go`) | 400 `bad_query` at the spelling the client sent — `unpaged` or its alias `all` — and the repository is never asked ([[D-060]]) |
| an option that configures the service, handed to `Serving` | `port.Rules.RefuseServiceOptions` | a panic at declaration naming the option ([[D-021]]) |
| `?filtr=` — a parameter one edit from a real one | `query.ParseQuery` → `checkParams` | 400 with the offending path named |
| a write verb on a `ReadOnly` handler | the route was never registered | 404, or 405 with `HandleMethodNotAllowed`; `Unimplemented` on gRPC |
| a gRPC request whose `patch` or `entity` is absent, null or not an object | `message.go:requiredSub` | `InvalidArgument`, `malformed_body`, and the service is never called |
| a gRPC integral key at magnitude 2^53 or greater sent as a number | `message.go:scalar`, before ID coercion | `InvalidArgument` / `invalid_id`, telling the raw caller to send a decimal string; framework keyed and bulk calls already send that spelling |
| `grpcurl describe` against a resource | nothing — there is no descriptor | the tool reports the service is unknown; call by full method name ([[D-052]]) |

## Files

| File | Role |
|---|---|
| `crud/http/crudgin/handler.go` | routes, `Mount`/`Register`, query-string reading, body decoding, the six constructors |
| `crud/http/crudgin/options.go` | the transport-shaped options, `collect`, `rendererFor`, `Status`, `DefaultErrorHandler`, `writeJSON` — the rest is `port.Rules` |
| `crud/http/crudnet/handler.go` | the same for `net/http`: `Mount`, the pattern set, and the root-path choice |
| `crud/http/crudnet/options.go` | the same set again; all three carry a `writeJSON` of their own, and that is the point of it ([[D-063]]) |
| `crud/http/crudfiber/handler.go` | the same for Fiber, plus `Routes` and `bodyLimit` — the standalone app's own cap |
| `crud/http/crudhttp/doc.go` | where the lines between the three shared halves are drawn |
| `crud/http/crudhttp/repository.go` | `Repository` and `Rules` — the two aliases every HTTP binding aliases in turn, so it embeds `port.Rules` without importing `port` for one field |
| `crud/http/crudhttp/request.go` | `BulkDeleteRequest`, and the forwarders for `CoerceID` / `NarrowForCount` / `NarrowForEntity` |
| `crud/http/crudhttp/porthttp.go` | the aliases and forwarders for everything [[D-059]] moved, so a binding written against the old names still compiles |
| `port/porthttp/errors.go` | `Status`, `StatusFor`, `KindOf`, `AcceptLanguage`, `ErrBadRequest`, `BadRequest`, `BadRequestf`, `BadRequestAs` |
| `port/porthttp/render.go` | the `Renderer` seam and `EnvelopeRenderer` — the status, the envelope and the header, which is the HTTP half |
| `port/porthttp/body.go` | `DecodeJSON`, `DecodeJSONKeep`, `DecodeJSONKeepLimit`, `MaxBody`, `MaxKeptBody`, `KeepBody`, `MalformedBody`, `TooLarge`, `WithBody`/`BodyFrom`, `WithLocale`/`LocaleFrom` |
| `crud/rpc/crudgrpc/doc.go` | the method table and the four stated limits |
| `crud/rpc/crudgrpc/handler.go` | the eight methods, the six constructors, `build`, the hooks and the scope |
| `crud/rpc/crudgrpc/service.go` | `ServiceName`, `ServicePrefix`, `Desc`, `Register`, and the hand-built `grpc.ServiceDesc` |
| `crud/rpc/crudgrpc/message.go` | `google.protobuf.Struct` ⇄ Go: `toStruct`, `fromStruct`, `sub`, `queryOf`, `queryIn`, `idOf`, `idsOf`, `countDoc`, `deletedDoc` |
| `crud/rpc/crudgrpc/status.go` | `Renderer`, `StatusRenderer`, `Code`, `CodeFor`, `KindForCode`, the five `RenderOption`s, and the details |
| `crud/rpc/crudgrpc/options.go` | the transport-shaped options, `collect`, `rendererFor` — the rest is `port.Rules` |
| `port/rules.go` | `Rules`, `Service`, `RefuseServiceOptions` — the five rules every binding shares, once ([[D-045]]) |
| `port/log.go` | `Logger`, `WithLogger` — where every binding's own lines go ([[D-062]]) |
| `crud/http/crudnet/middleware.go` | `Errors`, `WithErrors`, `HandlerFunc`, `recorder` — the middleware over a mux carrying hand-rolled routes as well, the double-install guard, and the panic that becomes a silent 500 |
| `crud/http/crudgin/middleware.go` | `Errors` — the same for Gin |
| `crud/http/crudfiber/middleware.go` | `Errors` and `ErrorHandler` — the same for Fiber, plus the `fiber.Config` hook, which the other two frameworks have no equivalent of |
| `crud/rpc/crudgrpc/interceptor.go` | `Errors` and the double-install guard |
| `crud/rpc/crudgrpc/locale.go` | `LocaleKeys`, `WithLocale`, `withRequestLocale` |
| `remote/remotehttp/transport.go` | the HTTP client transport — `route`, `entityQuery`, `fault` ([[FL-018]]) |
| `port/porthttp/decode.go` | `KindForStatus`, `ParseEnvelope`, and the wire shapes a client reads |
| `crud/rpc/crudgrpc/transport.go` | the gRPC client transport — `requestFor`, `fault`, `kindOf` ([[FL-018]]) |
| `port/service.go` | the service every binding hands its commands to |
| `port/command.go` | the commands themselves |
| `port/request.go` | `CoerceID`, `NarrowForCount`, `NarrowForEntity` |
| `port/model.go` | `Sanitize`, `ClearGenerated` |
| `port/violations.go` | the pipeline both renderers build from |
| `port/locale.go` | `WithLocale`, `LocaleFrom`, `FirstLanguageTag` — one key and one parser for every transport |
| `crud/http/crudgin/go.mod` | the module boundary that keeps Gin off everybody else |
| `crud/rpc/crudgrpc/go.mod` | the same for grpc, protobuf and genproto |
| — | `crudnet` has no `go.mod`: there is no dependency to keep off anybody |

Everything the request touches after `compile` is in [[FL-001]]'s file table.

## Tests that walk this flow

- `TestStaticRoutesAreNotSwallowedByTheIDRoute` — `crud/http/crudgin/routing_test.go`
  and `crud/http/crudfiber/routing_test.go` — the fixed paths, with a control case
  showing the `:id` route really is live and really would have taken them. The
  Fiber copy is the one that matters most and was the last to be written: Fiber
  matches in registration order, so on that binding the order of the lines in
  `Register` is the only thing keeping `/count` from being an entity id, and
  reordering them makes the test fail.
- `TestABodyPastTheCapIsRefusedAndReachesNoRepository` and
  `TestTheDefaultCapAcceptsAnOrdinaryBody` — `edge_test.go` in all three HTTP
  bindings, byte for byte the same test ([[D-063]]).
- `TestUnpagedIsRefusedOnAnEndpointThatDidNotDeclareIt` — `handler_test.go` in
  all three, beside the `TestListHonoursUnpagedAndSkipTotal` it controls
  ([[D-060]]).
- `TestTheStandaloneAppCarriesTheHandlersBodyCap` —
  `crud/http/crudfiber/routing_test.go` — the one body-cap behaviour that is
  Fiber's alone.
- `TestAnUnencodableResponseIsAServerFaultThatSaysNothing` — `edge_test.go` in
  all three. This is the test that found the three bindings disagreeing about a
  failure none of them can prevent, and it is why the response write is a row in
  the table above rather than three separate implementations.
- `TestTheCollectionRouteAnswersWithoutATrailingSlash` —
  `crud/http/crudgin/routing_test.go` — `GET /widgets` is 200 and `/widgets/` is 301.
- `TestMountingAtTheRootDoesNotCollide` — `crud/http/crudgin/routing_test.go` — the
  panic the `""` choice avoids.
- `TestRoutesMountEveryDocumentedEndpoint` — `crud/http/crudgin/handler_test.go` —
  every route, its verb, its status and the repository method behind it.
- `TestRepeatedFilterTermsAllSurvive` — `crud/http/crudgin/handler_test.go` — the
  repeats `URL.Query()` keeps.
- `TestAServiceLayerCanStandInForTheRepository` — `crud/http/crudgin/handler_test.go`.
- `TestADistinctInputDTOReachesTheModelThroughTheMapper` —
  `crud/http/crudgin/handler_test.go` — the mapper, with the control that the same
  body through `New` means nothing.
- `TestThePublicPatchBodyIsNotThePersistenceUpdate` and
  `TestTheAnswerIsWhatThePresenterMade` — `crud/http/crudgin/handler_test.go` —
  the `NewWire` half, with the mapperless mount as the control. `crudgrpc`
  carries both names too: the seam is `port`-shaped rather than HTTP-shaped, so
  there is nothing to spell differently there ([[FL-029]]).
- `TestNewForInfersItsInputFromTheMapper`,
  `TestTheHookStillRunsAfterTheServerOwnedFieldsAreCleared` and
  `TestAServiceShapedOptionOnServingIsRefusedAtDeclaration` —
  `crud/http/crudgin/options_test.go`.
- `TestAServicePathHopReachesTheRenderedField` — `crud/http/crudgin/edge_test.go`.
- `TestStatusMapsWhatItPromisesTo` and `TestA500NeverEchoesTheInternalError` —
  `crud/http/crudgin/edge_test.go` — the shared table, from this side.
- `TestPutIsNotAWayAroundAllowClientID` — `crud/http/crudgin/write_edge_test.go`.
- `TestGinHTTP*` — `test/integration/http_gin_test.go` — nine tests end to end
  against live PostgreSQL and MySQL, mounting the same service type the Fiber
  suite mounts.

And the `crudnet` half:

- `TestStaticRoutesAreNotSwallowedByTheIDRoute` — `crud/http/crudnet/routing_test.go`
  — the same guarantee, with the same control case.
- `TestBothSpellingsOfTheCollectionAnswer` — `crud/http/crudnet/routing_test.go` —
  `/widgets` and `/widgets/` both reach the list handler, and a create through
  either reaches `Save`.
- `TestMountingAtTheRootClaimsOnlyTheRootPath` — `crud/http/crudnet/routing_test.go`
  — the catch-all the `{$}` choice avoids. Its control asserts that an
  unregistered path is a 404 that never reaches the repository; with a bare `/`
  it is a 200 and a page of rows, which is why the control is the test.
- `TestNetHTTP*` — `test/integration/http_net_test.go` — the same nine tests end
  to end, mounting the same service type again.

And the `crudgrpc` half:

- `TestEveryCommandHasAMethod` — `crud/rpc/crudgrpc/handler_test.go` — eight methods,
  each handing the recorder service the command `port` declares. Its control is
  `ReadOnly`: three reads registered, five writes answering `Unimplemented`.
- `TestAbsentNullAndValueSurviveTheStructRoundTrip` — the same file — [[UC-003]]
  on a fourth transport, with the control that an absent key and an explicit
  null produce two different `Opt` states.
- `TestGRPCStructNeverRoundsIDsOrPreloadFilterIntegers` — `message_test.go` —
  the safe numeric edges, both refused boundaries and exact string recovery for
  `id` and `ids`; `TestAnInt64KeyIsCarriedAsAString` separately proves the
  entity-document string tag.
- `TestAResourceIsRegisteredUnderItsOwnName`,
  `TestAKeyThatDoesNotParseIsAClientMistake`,
  `TestDeletingNothingIsAMissForOneRowAndZeroForASet`,
  `TestReplaceIsNotAWayAroundAllowClientID`,
  `TestACreateIsClearedBelowTheBinding`, `TestMaxBulkCapsOneRequest`,
  `TestWithTransformHidesColumnsOnEveryReadShape` — the shared guarantees in
  this transport's vocabulary.
- `TestNewForInfersItsInputFromTheMapper`,
  `TestADistinctInputDTOReachesTheModelThroughTheMapper`,
  `TestAServicePathHopReachesTheRenderedField` and
  `TestAServiceShapedOptionOnServingIsRefusedAtDeclaration` — the same names the
  three HTTP suites carry, because they are about `port` rather than about HTTP.
- `TestKindMapsToTheCodeItPromisesTo`, `TestAnInternalStatusSaysNothing`,
  `TestAStatusMessageNamesNoEntityAndNoDriverText`,
  `TestEveryViolationBecomesAFieldViolationInTheSameOrder`,
  `TestAViolationWithNoPathIsStillInTheOneList`,
  `TestTheReasonIsTheCodeSpelledTheUsualWay`,
  `TestARetryableStatusCarriesRetryInfo`,
  `TestTheFrameworkDoesNotRetryOnTheCallersBehalf`,
  `TestPartialIsTheOnlyMetadataKey`, `TestTheErrorInfoNamesTheFaultsOwnCode` and
  `TestAClassifiedConflictReachesAGrpcClientWithNothingInternal` —
  `crud/rpc/crudgrpc/status_test.go` — the second vocabulary and what it may say.
  The last runs over every entry of the captured corpus on all four engines.
- `TestInstallingTheInterceptorTwiceRendersOnce`,
  `TestTheInterceptorRendersAMethodOfYourOwn`,
  `TestTheRequestLocaleReachesTheMessageLadder` and
  `TestALocalizedMessageNamesTheRequestedLocale` —
  `crud/rpc/crudgrpc/handler_test.go`.
- `TestOnePortServiceAlsoMountsOnGRPC` and
  `TestAClassifiedConflictReachesAGrpcClientWithNothingInternal` —
  `test/integration/rpc_grpc_test.go` — end to end against every live engine and
  every classifying target, with per-engine counters so an emptied loop cannot
  pass.

And the ones that are about all four at once:

- `TestOneServiceMountsOnAllThreeBindings` and `TestTheServiceIsWhereTheRulesRan`
  — `test/portmount/mount_test.go` — one `port.Service` value on all three HTTP
  bindings, same status, same bytes, same command.
- `TestTheSameServiceMountsOnAllFourTransports`,
  `TestAGeneratedResourceResolvesTheSameFieldOnAllFourTransports`,
  `TestTheSameCodeIsSpelledTheSameOnBothTransports` and
  `TestARefusalIsTheSameClassOnBothTransports` —
  `test/portmount/grpcmount_test.go` — the same claim carried onto a protocol
  that is not HTTP. It is the only place all four can be imported together, so it
  lives in the `test` module and runs under `make unit`.
- `TestOnePortServiceMountsOnAllThreeBindings` —
  `test/integration/http_port_test.go` — the same, live.

**The triplet rule is about the three HTTP bindings, and the fourth transport is
not in it.** All three HTTP unit suites carry the same 181 test and subtest
names, plus the routing tests each router needs (`crudfiber` has 7 more,
`crudgin` 8, `crudnet` 11). A change that makes the shared 181 diverge is either
a bug or a new row in the table above. `make check-triplets` compares the
top-level names; the subtests are the reader's job, which is why the number is
written down here. `crudgrpc` carries the subset that is about `port` rather than
about HTTP — the constructors, the mapper, the hooks, the service-shaped
refusal, the clearing, the key coercion — under the same names, and spells the
rest in its own vocabulary because there is no 404 and no `PUT` here to name. A
test that only makes sense for one HTTP router still belongs in that binding's
`routing_test.go`; a test that only makes sense for gRPC belongs in
`crud/rpc/crudgrpc/` and the difference it pins belongs in the table above.

## See also

[[FL-001]] [[FL-002]] [[FL-003]] [[FL-011]] [[FL-012]] [[FL-007]]
