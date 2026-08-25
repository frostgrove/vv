# FL-015 — A request through the port layer

**Entry point:** `http/crudnet/handler.go:Create` (and every sibling route, in all four bindings)
**Implements:** [[UC-001]] [[UC-013]] [[UC-015]] · **Governed by:** [[D-045]] [[D-022]] [[D-043]] [[D-050]] [[D-012]] [[D-004]] [[D-021]]

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
   a path the raw-body index resolves today and make it worse. A hand-written
   `Fields` is partial by nature, so an undeclared head is an ordinary gap.

3. **command field → transport field** — the mapper, when it also implements
   `errs.Resolver`. It is a separate optional interface and not a second method
   on `Mapper`, so a hand-written mapper is not forced to write a path map it has
   no use for. `port.Hops` collects hops 2 and 3, in that order.
   A *generated* mapper answers `Resolve` out of `port/pathmap.go:PathMap`, and
   that type is the opposite of `Fields` in two ways. Both are deliberate, both
   are [[D-050]]'s, and neither is safe for the other type:

   | | `Fields` (hop 2) | `PathMap` (hop 3, generated) |
   |---|---|---|
   | where it comes from | hand-written | generated from the model |
   | totality | partial by nature | total, checked at package initialisation |
   | an undeclared head | passes through, so the fallback still runs | **declines**, so the violation is marked approximate |
   | a leading index | not looked past: `[0,"Email"]` is returned unchanged, because a hand-written map may declare a key called `"3"` and silently ignoring it would be worse than not looking (`TestAnEmptyFieldsMapIsTheIdentity`) | **looked past**: `[0,"Email"]` becomes `[0,"email"]`, because a generated map cannot declare an index at all |

   Declining is safe for a `PathMap` precisely because it is total: an
   undeclared head can only be a column of another table — which
   `repo/decorators/faults/faults.go:resolve` already leaves unset — or a column
   no request carries. Neither has a client key to name.

4. **path → wire** — `port/violations.go:Violations`, which applies the chain and
   then the sort, the cap and the message ladder. Both renderers call it: the
   JSON array in the envelope is `http/crudhttp/render.go`'s rendering of the
   result, and `rpc/crudgrpc/status.go` renders the same `errs.Path` as
   `BadRequest_FieldViolation.Field`, dotted. Since phase 9 that sentence is
   present tense — the fourth transport exists and this hop is where it and the
   envelope stop differing.

**The fallback** — `http/crudhttp/bodyindex.go:BodyResolver` — runs *after* every
declared hop, and only over a path the declared hops left **unchanged**. It
reaches the pipeline as `port.ViolationOptions.Fallback`, a field of its own
rather than a last resolver, because a declaration must always beat a guess. The
index matches a
violation's last step against the keys the client sent, so over an
already-translated path it could land on a same-named key elsewhere in the
payload — a `not_null` violation on a column the client omitted is the case that
produces it. A guess must not overturn a declaration ([[D-043]]).
`rendererFor` in each binding's `options.go` builds a per-handler renderer only
when `port.Hops` returns something; with no hops the shared `defaultRenderer` is
kept and the zero-config case stays free. `rpc/crudgrpc/options.go` has the same
function over its own renderer, and passes **no** fallback: that transport has no
retained request bytes to index, so a path nothing declared is marked
approximate rather than guessed.

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
- **The renderer seam stayed behind; the pipeline came down.** Two halves, and
  the distinction is the one most likely to be misremembered. The `Renderer`
  *interface* and `EnvelopeRenderer` are still in `http/crudhttp`, because
  [[D-045]]'s test is whether a non-HTTP transport can implement the interface
  without importing `net/http` and one returning an `http.Header` cannot —
  `errs/spi.go` says so where its absence would otherwise look like an oversight,
  and `rpc/crudgrpc` has a `Renderer` of its own answering a `*status.Status`.
  What did move, at phase 9 and by that decision's own scheduled instruction, is
  the *violations pipeline*: `port.Violations` and `port.ViolationOptions`. Both
  renderers call it. So do not read "the renderer stayed" as "everything in
  render.go stayed".
- **One context key for the locale, in `port`.** `port.WithLocale` /
  `port.LocaleFrom` in `port/locale.go`, with `crudhttp.WithLocale` a forwarder.
  A second key left in an HTTP package would be invisible to a gRPC renderer and
  vice versa, and both packages' own suites would still pass —
  `TestALocaleSetByOneTransportIsReadByAnother` in
  `http/crudhttp/locale_test.go` is what catches it. `port.FirstLanguageTag`
  parses the tag list for both, because `grpc-accept-language` carries the same
  syntax an `Accept-Language` header does.

## Traps

- **PATCH has no mapper.** `Mapper[In, M]` covers the entity body only; a
  transport-specific patch shape would be a fifth type parameter. The generated
  DTO already *is* the transport shape ([[D-018]]), so it costs nothing today. It
  is a stated limit and `port/doc.go` states it. It is also why `<Model>Input`
  and `<Model>Update` share one naming rule: two rules would mean one resource
  needed two inverse maps, and only one of them would have an owner ([[D-050]]).
- **A generated resource has a wire shape of its own.** `<Model>Input` uses
  `lowerFirst(FieldName)`, which is not necessarily the model's own `json` tag.
  A consumer who wants the model's shape mounts with `New` and generates no
  adapter. Both usage guides say so in §12.
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
| a violation at a field a *generated* map does not declare | `PathMap` declines, and the chain stops | the model's field name, marked approximate — the fallback behind it does not run ([[D-050]]) |
| a generated map that no longer matches its model | `port.MustPathMap` at package initialisation | a panic at start-up naming the column, before any request |

## Files

| File | Role |
|---|---|
| `port/doc.go` | what the layer is, and the four limits it states rather than leaves to be found |
| `port/service.go` | `Service`, `DefaultService`, `NewService`, `ServiceOption`, `WithQuery`, `AllowClientID`, `WithPaths` — the whole orchestration |
| `port/command.go` | the eight commands, and why the write ones carry their hook |
| `port/mapper.go` | `Mapper`, `Identity` |
| `port/path.go` | `Fields` — the service hop and its pass-through rule — and `Hops` |
| `port/pathmap.go` | `PathMap` and its decline rule, `At`, and the two start-up checks `NewPathMap`/`MustPathMap` and `CoversUpdate`/`MustCoverUpdate` ([[D-050]]) |
| `port/repository.go` | `Repository` — what a service is built over ([[D-022]]) |
| `port/model.go` | `Sanitize`, `ClearGenerated` |
| `port/request.go` | `CoerceID`, `NarrowForCount`, `NarrowForEntity` |
| `port/sentinel.go` | `ErrBadRequest`, `BadRequest`, `BadRequestf`, `BadRequestAs` |
| `port/kind.go` | the code vocabulary: `FaultOf`, `KindOf`, `KindOfWith`, `CodeForKind`, `DefaultMessage` |
| `port/violations.go` | `Violations`, `ViolationOptions`, `MaxViolations` — the copy, the chain, the sort, the cap and the message ladder, called by every renderer |
| `port/locale.go` | `WithLocale`, `LocaleFrom`, `FirstLanguageTag` — one context key and one tag parser for every transport |
| `http/crudnet/handler.go` | the traced binding: routes, decode, the four constructors, `HandlerFor`/`Handler` |
| `http/crudnet/options.go` | `collect`, `service`, `refuseServiceOptions`, `rendererFor`, `render`, `writeJSON` |
| `http/crudfiber/handler.go`, `http/crudgin/handler.go` | the same two files each, name for name ([[FL-013]]) |
| `http/crudhttp/doc.go` | where the line between the two shared halves is drawn |
| `http/crudhttp/render.go` | the `Renderer` seam and `EnvelopeRenderer` — the status, the envelope and the header, which are HTTP-shaped on purpose |
| `http/crudhttp/request.go` | the forwarders, including `WithLocale` / `LocaleFrom` over `port` |
| `http/crudhttp/bodyindex.go` | the raw-body fallback, behind every declared hop |
| `rpc/crudgrpc/status.go` | the other renderer over the same pipeline: `codes.Code` and the error details ([[D-052]]) |
| `repo/decorators/faults/faults.go` | `Enrich`, `enricher.resolve` — the chain's first hop ([[FL-014]]) |

## Tests that walk this flow

- `TestOneServiceMountsOnAllThreeBindings` — `test/portmount/mount_test.go` —
  [[D-045]]'s control. One `port.Service` value, three HTTP bindings, same
  status, same body **bytes**, same *command*. The command is the assertion with
  teeth: a compile-only check would pass whatever the bindings did.
- `TestTheServiceIsWhereTheRulesRan` — beside it — and the other half, because
  three bindings that had all forgotten to narrow a count would agree with each
  other.
- `TestOnePortServiceMountsOnAllThreeBindings` —
  `test/integration/http_port_test.go` — the same claim against every live
  engine.
- `TestTheSameServiceMountsOnAllFourTransports` —
  `test/portmount/grpcmount_test.go` — the same claim carried onto a protocol
  that is not HTTP, which is what phase 9 measured [[D-045]] with. One value on
  Fiber, Gin, net/http and gRPC; same command, same answer document. Verified by
  putting `NarrowForCount` back into `crudgrpc`'s Count method.
- `TestOnePortServiceAlsoMountsOnGRPC` — `test/integration/rpc_grpc_test.go` —
  and live.
- `TestTheViolationOrderIsTotalAndByteIdentical`,
  `TestAtOnePathTheInputViolationComesFirst`,
  `TestACappedListKeepsTheFrontOfTheOrder`,
  `TestAFaultWithNoViolationsStillNamesItsCode`,
  `TestTheMessageLadderSeesTheTranslatedPath`,
  `TestAMessageFallsBackToTheCodesDefaultAndThenToTheCode`,
  `TestADeclaredHopBeatsTheFallback` and
  `TestRenderingDoesNotWriteThroughToTheFault` — `port/violations_test.go` — the
  pipeline itself, moved down with it at phase 9 and now measured once for every
  transport rather than once per renderer.
- `TestALocaleSetByOneTransportIsReadByAnother` —
  `http/crudhttp/locale_test.go` — one context key, read from both sides, with
  the control that an unset context reads empty.
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
- `TestAGeneratedMapDeclinesWhereFieldsPassesThrough` — `port/pathmap_test.go` —
  the contrast in the table above, with a recording hop behind each type showing
  that one kept the chain running and the other stopped it. The rest of that
  file pins the start-up checks.
- `TestADeclaredMapBeatsTheRawBodyGuess` — `http/crudhttp/render_test.go` — the
  ordering of hop 3 and the fallback, including a declared path the index would
  otherwise have rewritten, each arm with its no-map control.
- `TestAGeneratedResourceResolvesTheSameFieldOnAllThreeBindings` —
  `test/portmount/mount_test.go` — one generated mapper, three transports, one
  rendered field, with the `New`-mounted control answering the model's own field
  name.
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
