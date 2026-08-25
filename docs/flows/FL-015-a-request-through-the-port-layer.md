# FL-015 — A request through the port layer

**Entry point:** `http/crudnet/handler.go:Create` (and every sibling route, in all three bindings)
**Implements:** [[UC-001]] [[UC-013]] [[UC-015]] · **Governed by:** [[D-045]] [[D-022]] [[D-043]] [[D-012]] [[D-004]] [[D-021]]

A transport binding owns three things: which routes exist, how a body becomes a
Go value, and how a response is written. This flow is everything in between —
the half that is the same on Fiber, on Gin, on `net/http` and on whatever comes
next ([[D-045]]).

`crudnet` is the honest one to trace. It imports nothing outside the standard
library, so nothing in the path below is a framework's doing.

The chain, forwards:

```
route -> decode -> Mapper.Model -> Command -> Service -> Repository
```

and the path chain, backwards, one hop per layer and nothing guessed
([[D-043]]):

```
column -> model field -> command field -> transport field -> wire
 faults      Service.Paths      Mapper        renderer
```

## The path — a create

1. **`HandlerFor.Create`** — `http/crudnet/handler.go:Create`
   `var in In` — the handler's *input* type, which is the model itself unless
   `NewFor` gave it one of its own. `crudhttp.DecodeJSONKeep` decodes it and
   hands back the bytes; `keep` puts a capped copy on the context for the
   raw-body path fallback ([[D-043]], [[FL-011]]).

2. **`Mapper.Model`** — `port/mapper.go`
   The resource adapter. `port.Identity[M]()` is what `New` installs, so there is
   one code path and not two: a nil check on every write route is two branches
   that drift. A mapper that fails is an ordinary error and reaches the renderer
   like any other.

3. **The command** — `port/command.go:CreateCommand`
   `{Model: m, Before: h.beforeSave(r)}`. The hook is transport-shaped — it
   closes over the `*http.Request` — and is carried rather than called, because
   *where* it runs is not transport-shaped: [[UC-013]] guarantee 7 pins it as
   running after the server-owned fields are cleared, and that order is the
   service's to keep. A hook left in the binding would start seeing an
   unsanitised model the moment the clearing moved down here, and nothing would
   have failed.

4. **`DefaultService.Create`** — `port/service.go:Create`
   In order, and the order is the guarantee:
   - `port.Sanitize` (`port/model.go`) — a database-generated key is zeroed
     unless `AllowClientID`, and every `generated` column is zeroed always.
   - `cmd.Before` — the hook, now looking at a model a client cannot have
     forged.
   - `repo.Save` — [[FL-003]] from here.

5. **The repository**, through whatever decorators the model was bound with:
   the security gate ([[FL-008]]), the faults decorator, `repo/basic`, the
   adapter, the driver.

6. **The response** — `HandlerFor.entity` → `writeJSON`. 201 and the stored row,
   or the `WithTransform` presenter's view of it. A presenter takes the
   *request* type, so it stays in the binding.

## The other seven commands

| Route | Command | What the service does that the binding used to |
|---|---|---|
| `GET /` · `POST /query` | `ListCommand` | `Request.Compile`, then append the caller's options |
| `GET /count` · `POST /count` | `CountCommand` | `NarrowForCount` first — paging left in would make the answer the size of one page |
| `GET /{id}` | `GetCommand` | `NarrowForEntity` — a filter or a sort on the way to a keyed row means nothing |
| `POST /` | `CreateCommand` | `Sanitize`, then the hook, then `Save` |
| `PATCH /{id}` | `UpdateCommand` | the hook on the patch, then `Update` |
| `PUT /{id}` | `ReplaceCommand` | the existence probe ([[D-012]]), `ClearGenerated`, `SetID` from the command's key, then the hook, then `Save` |
| `DELETE /{id}` | `DeleteCommand` | `n == 0` ⇒ `crud.ErrNotFound` |
| `POST /bulk-delete` | `BulkDeleteCommand` | an empty set never reaches the repository |

What stays in the binding, and why each one is genuinely transport-shaped:
`MaxBulk` (how large one request may be), `ReadOnly` (which routes exist),
`WithTransform` (a presenter takes the request type), `WithScope` (it reads the
request), body decoding, and writing the response.

## The path chain, backwards

Four hops and a fallback. Every one translates only what it already knows and
declines rather than guessing ([[D-043]]).

1. **column → model field** — `repo/decorators/faults/faults.go:resolve`
   Through `crud.Meta` and never `crud.Schema`: `Schema` is cached per type and
   table-independent, so it cannot tell two databases' `users` apart. A column on
   another table marks the violation approximate rather than naming a field of a
   model that had nothing to do with the write.

2. **model field → command field** — `port/path.go:Fields`, wired with
   `port.WithPaths` and answered by `Service.Paths`.
   The head step is looked up and the tail rides along, so one entry covers every
   violation under a field.
   **An undeclared head passes through rather than declining**, and that is the
   one judgement in the type. A declining hop poisons `errs.Chain`: everything
   behind it is dropped and the violation is marked approximate, which would take
   a path the raw-body index resolves today and make it worse. Strictness belongs
   to a *generated* map, which is total by construction and can refuse start-up
   for a column it does not cover (phase 8). A hand-written `Fields` is partial by
   nature.

3. **command field → transport field** — the mapper, when it also implements
   `errs.Resolver`. It is a separate optional interface and not a second method
   on `Mapper`, so a hand-written mapper is not forced to write a path map it has
   no use for. `port.Hops` collects hops 2 and 3, in that order.

4. **path → wire** — `http/crudhttp/render.go:EnvelopeRenderer.violations`, which
   applies the chain and then the sort, the cap and the message ladder. The JSON
   array is the HTTP rendering; a gRPC binding will render the same `errs.Path`
   as a proto field path.

**The fallback** — `http/crudhttp/bodyindex.go:BodyResolver` — runs *after* every
declared hop, so a declared mapping always beats a guess. `rendererFor` in each
binding's `options.go` builds a per-handler renderer only when `port.Hops`
returns something; with no hops the shared `defaultRenderer` is kept and the
zero-config case stays free.

## Where the decisions bite

- **`New` still infers three type parameters** ([[D-022]]). `HandlerFor` has four
  and `Handler[M, ID, U]` is a parameterised alias that fills the fourth in with
  the model. A fourth parameter on `New` itself would be `cannot infer In` at
  every existing call site, and no alias can drop a parameter from a *function*.
  `Option[M, ID, U]` therefore did **not** grow one either: nothing an option
  sets mentions the input type, and that is the load-bearing detail of the whole
  move.
- **`Serving` refuses a service-shaped option at declaration** ([[D-021]]).
  `WithQuery` and `AllowClientID` configure a service, and `Serving` was handed
  one that is already built. A silent no-op would leave an API accepting
  everything while its author believed it was bounded. The panic names the option
  that has to move.
- **A scope is appended, never merged** ([[D-004]]). `crud.Where` ANDs, so the
  service appends the command's options *after* the document compiles and a
  client cannot widen a scope by sending a filter.
- **`port` may import the standard library, `crud`, `query` and `errs`, and
  nothing else.** `Makefile:TIER0` and `make check-tiers` are what make that
  mechanical. The arrow is `crudhttp → port` and never back.
- **The renderer stayed behind.** [[D-045]]'s test is whether a non-HTTP
  transport can implement the interface without importing `net/http`, and one
  returning an `http.Header` cannot. `errs/spi.go` says so where its absence
  would otherwise look like an oversight.

## Traps

- **PATCH has no mapper.** `Mapper[In, M]` covers the entity body only; a
  transport-specific patch shape would be a fifth type parameter. The generated
  DTO already *is* the transport shape ([[D-018]]), so it costs nothing today. It
  is a stated limit and `port/doc.go` states it.
- **`WithScope` runs before the query document compiles now.** The binding reads
  the scope and puts it on the command; the service compiles. So a request with
  both a failing scope and a malformed filter reports the scope's error, where it
  used to report the filter's. Neither order is a documented guarantee and this
  one is the safer of the two — a caller whose scope refused it is not told which
  of its filters was misspelled.
- **`Service.Paths` may return nil**, and most do. `errs.Chain` skips a nil hop;
  `port.Hops` drops it before the chain is built, so the shared renderer is kept.
- **The service is not where transport-shaped rules go.** `BeforeSave` and
  `BeforeUpdate` exist for those. Anything that must hold whatever the transport
  is belongs in the service itself or in a `security.Policy` ([[D-017]]).

## Failure modes

| What goes wrong | Where it is caught | What the caller sees |
|---|---|---|
| a body that will not decode | the binding's decode → `crudhttp.MalformedBody` | 400, `malformed_body`, and the service is never called |
| a mapper that refuses the input | `Mapper.Model` | whatever it returned, through the ordinary mapping ([[FL-011]]) |
| a path key that does not coerce | `port.CoerceID` | 400, `invalid_id` |
| a query document naming a field the model lacks | `Request.Compile`, inside the service | 400 with the offending path ([[FL-001]]) |
| a client-chosen key on create | `port.Sanitize`, before the hook | the key is zeroed; the request succeeds |
| a PUT at a key that does not exist, with an auto key | `DefaultService.Replace`'s existence probe | 404, and nothing is written ([[D-012]]) |
| a delete that removed nothing | `DefaultService.Delete` | 404 |
| a bulk delete of an empty set | `DefaultService.DeleteMany` | `200 {"deleted":0}`, and the repository is never called |
| `WithQuery` handed to `Serving` | `options.refuseServiceOptions` | a panic at declaration naming the option |
| a violation at a field no hop declares | `Fields` passes through, `BodyResolver` resolves | the key the client sent, not marked approximate |
| a violation at a field nothing can resolve | `BodyResolver` declines | the model's field name, marked approximate ([[D-043]]) |

## Files

| File | Role |
|---|---|
| `port/doc.go` | what the layer is, and the four limits it states rather than leaves to be found |
| `port/service.go` | `Service`, `DefaultService`, `NewService`, `ServiceOption`, `WithQuery`, `AllowClientID`, `WithPaths` — the whole orchestration |
| `port/command.go` | the eight commands, and why the write ones carry their hook |
| `port/mapper.go` | `Mapper`, `Identity` |
| `port/path.go` | `Fields` — the service hop and its pass-through rule — and `Hops` |
| `port/repository.go` | `Repository` — what a service is built over ([[D-022]]) |
| `port/model.go` | `Sanitize`, `ClearGenerated` |
| `port/request.go` | `CoerceID`, `NarrowForCount`, `NarrowForEntity` |
| `port/sentinel.go` | `ErrBadRequest`, `BadRequest`, `BadRequestf`, `BadRequestAs` |
| `port/kind.go` | the code vocabulary: `FaultOf`, `KindOf`, `KindOfWith`, `CodeForKind`, `DefaultMessage` |
| `http/crudnet/handler.go` | the traced binding: routes, decode, the four constructors, `HandlerFor`/`Handler` |
| `http/crudnet/options.go` | `collect`, `service`, `refuseServiceOptions`, `rendererFor`, `render`, `writeJSON` |
| `http/crudfiber/handler.go`, `http/crudgin/handler.go` | the same two files each, name for name ([[FL-013]]) |
| `http/crudhttp/doc.go` | where the line between the two shared halves is drawn |
| `http/crudhttp/render.go` | the renderer, which is HTTP-shaped on purpose |
| `http/crudhttp/bodyindex.go` | the raw-body fallback, behind every declared hop |
| `repo/decorators/faults/faults.go` | `Enrich`, `enricher.resolve` — the chain's first hop ([[FL-014]]) |

## Tests that walk this flow

- `TestOneServiceMountsOnAllThreeBindings` — `test/portmount/mount_test.go` —
  [[D-045]]'s control. One `port.Service` value, three bindings, same status,
  same body **bytes**, same *command*. The command is the assertion with teeth: a
  compile-only check would pass whatever the bindings did.
- `TestTheServiceIsWhereTheRulesRan` — beside it — and the other half, because
  three bindings that had all forgotten to narrow a count would agree with each
  other.
- `TestOnePortServiceMountsOnAllThreeBindings` —
  `test/integration/http_port_test.go` — the same claim against every live
  engine.
- `TestTheDefaultServiceAppliesTheRulesInOrder` — `port/service_test.go` — the
  sanitise / hook / save order, the replace probe, and the control that with
  `AllowClientID` the hook does see the client's key.
- `TestDeletingNothingIsAMissForOneRowAndZeroForASet` — `port/service_test.go`.
- `TestTheReadsNarrowTheDocumentAndAppendTheCallersOptions` —
  `port/service_test.go` — including the compiled SQL of a client filter ANDed
  with a caller's option.
- `TestWithQueryBoundsTheServiceAndNotTheTransport` — `port/service_test.go`.
- `TestFieldsPassAnUndeclaredHeadThrough`,
  `TestADeclaredHeadIsRewrittenAndTheTailSurvives`,
  `TestAnEmptyFieldsMapIsTheIdentity`,
  `TestHopsCollectsTheServiceAndTheMapperInThatOrder` — `port/path_test.go`.
- `TestNewForInfersItsInputFromTheMapper`,
  `TestTheHookStillRunsAfterTheServerOwnedFieldsAreCleared`,
  `TestAServiceShapedOptionOnServingIsRefusedAtDeclaration` —
  `options_test.go` in **all three** bindings, same names, file for file.
- `TestADistinctInputDTOReachesTheModelThroughTheMapper` — `handler_test.go` in
  all three, with the control that the same body through `New` means nothing.
- `TestAServicePathHopReachesTheRenderedField` — `edge_test.go` in all three,
  with the control that an undeclared field still reaches the body index.
- `make check-tiers` — `port`'s arm, which had never run against code before
  this phase.

## See also

[[FL-013]] [[FL-011]] [[FL-014]] [[FL-001]] [[FL-002]] [[FL-003]]
