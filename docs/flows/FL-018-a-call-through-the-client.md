# FL-018 — A call through the client

**Entry point:** `remote/resource.go:Resource.Get` (and every sibling method, over both transports)
**Implements:** [[UC-018]] [[UC-015]] · **Governed by:** [[D-053]] [[D-054]] [[D-045]] [[D-044]] [[D-052]]

[[FL-015]] is a request arriving. This is the same request leaving: one service
holds a `remote.Resource` over a model another service serves, and calls it with
the methods it would use on a repository of its own.

The chain, forwards:

```
crud.Option -> query.Request -> remote.Call -> Transport -> the wire
   ToRequest      MarshalPredicate    route/requestFor
```

and backwards, which is the half that carries the guarantee:

```
status or code -> errs.Kind -> errs.Fault -> the sentinel a caller branches on
  KindForStatus    FaultFrom     Wrapping(crud.ErrNotFound)
  KindForCode
```

The shape mirrors the server exactly. `port` classifies, and
`porthttp`/`crudgrpc` hold two tables over that one answer ([[D-045]]). Here
`port.FaultFrom` rebuilds, and the same two packages hold the two inverse
tables — in the same package as the forward ones, because a client that kept its
own copy would agree with the server until the first time one of them changed.

The two client transports do not sit in the same place, and the reason is a
module boundary rather than a protocol. `remote/remotehttp` reads
`porthttp`'s tables from `port/`, which is where [[D-059]] put them, so it moved
out of the binding and in beside `remote`. `crudgrpc.Transport` stayed with its
binding: `remote` is in the root module and may not import grpc, so a
`remote/remotegrpc` would be a whole module for one file ([[D-058]]).

## The path — a list with a filter

1. **`Resource.Get`** — `remote/resource.go`
   Takes `...crud.Option`, the same signature `port.Repository` declares, so a
   caller cannot tell a remote resource from a local one by its type.

2. **`ToRequest`** — `remote/options.go`
   `crud.Build(opts...)` resolves the option list into an inspectable
   `crud.Options`, and every field gets one of three answers: translated,
   refused by name, or documented as unenforceable ([[D-053]]). The refusals
   happen here, before anything is sent.

3. **`crud.MarshalPredicate`** — `crud/document.go`
   `Options.Predicate()` folds the filter list into one node; each node writes
   its own single-key object through `document(*docWriter)`, which is on the
   `Predicate` interface so a node cannot forget it ([[D-054]]). The result is
   `json.RawMessage` — `query` imports `crud`, so it cannot be a `query.Filter` —
   and `query.RawFilter` wraps it.

4. **`Transport.Do`** — `remote/transport.go`
   A `remote.Call`: a method, a text key, a JSON key array, a query document and
   a raw body. Nothing about a URL, a header or a connection.

5. **The transport** — `remote/remotehttp/transport.go:route` or
   `crud/rpc/crudgrpc/transport.go:requestFor`
   One place per protocol where a call becomes a verb and a path, or a full
   method name and a `structpb.Struct`. Both go through `encoding/json`, which is
   what keeps `crud.Opt`'s three states intact — a `map[string]any` collapses
   absent and null the first time it passes through a Go nil.

6. **The answer** — `remote/resource.go:decode`
   Raw JSON into `crud.PaginatedResponse[M]`, `M`, `{"count":n}` or
   `{"deleted":n}`. A service that answers something unreadable is an
   infrastructure failure and carries no fault: `port.KindOf` reads an
   unrecognised error as internal, which is the status a gateway should pass on.

## The path — a refusal

1. **Is this library speaking?** Over HTTP, `porthttp.ParseEnvelope` checks
   `Envelope.Type == "error"`; over gRPC, the transport looks for an
   `errdetails.ErrorInfo` whose domain is `crudgrpc.ErrorDomain`. A response that
   fails the check is a `*remote.ProtocolError` and never a classified failure,
   whatever the status said. **This is the trap the check exists for** — see
   *Traps* below.

2. **The kind** — `porthttp.KindForStatus` or `crudgrpc.KindForCode`, each in the
   package that holds the forward table it inverts.

3. **The code refines the kind, on one transport.** `crudgrpc.CodeFor` sends both
   `KindValidation` and `KindBadRequest` to `InvalidArgument` ([[D-052]]), so
   `KindForCode` answers the coarser of the two and
   `transport.kindOf` sharpens it with `errs.Codes.KindOf`. HTTP needs none of
   this: 422 and 400 are distinct.

4. **`port.FaultFrom`** — `port/kind.go`
   The kind, the code, the violations, the partial marker. It wraps the sentinel
   — `sentinelFor` is `sentinelKind` read backwards — and derives
   `Violation.Origin` from the kind, because the wire does not carry it
   ([[D-044]]) and its zero value would blame the payload for a collision.

## Where the decisions bite

- **[[D-044]]** decides what a decoded violation *is*: three public fields out
  of seven. `errs.Path` has an `UnmarshalJSON` and `errs.Violation` deliberately
  does not, so each transport spells its lossy shape out and whoever reads it
  can see how much arrives.
- **[[D-053]]** decides which options never leave. `crud.NarrowRelations` is the
  one that matters: a scope silently dropped is a scope that stops at the
  preload, and a `security.Gate` over a remote resource would appear to work.
- **[[D-054]]** decides that `crud.Raw` is refused rather than escaped.
- **[[D-045]]** decides where the two inverse tables live: beside the forward
  ones, not in `remote`.
- **[[D-052]]** decides the one lossy arm and, having accepted it, gives the
  machine code the job of undoing it here.
- **[[UC-003]]** decides `remote.New`'s start-up refusal: a `crud.Opt` field
  without `omitzero` marshals an undefined value as `null`, and a patch built
  from it would empty every column the caller left alone.

## Traps

- **A router's 404 is not a missing row.** A wrong base URL gets
  `404 page not found` from `http.ServeMux`, and an API gateway or a service
  mesh answers JSON — which parses. Read as a status alone, either becomes
  `crud.ErrNotFound`, and a misconfigured service then reports an empty table
  for as long as nobody looks. `Envelope.Type` is what tells them apart.
- **gRPC has no 404**, so the same trap wears `Unimplemented` — reachable
  without a typo, from a service mounted with `crudgrpc.ReadOnly`.
- **A key in a URL is text and a key in a body is not.** `remote.Call.ID` is a
  string because a path is a string; `Call.IDs` is `json.RawMessage` because the
  far side decodes it into its own key type, and an `int64` sent as `"42"`
  arrives as a string where a number was expected.
- **A patch DTO written by hand can empty a row.** `cmd/vv` writes `omitzero`
  on every generated `crud.Opt` field; `remote.New` refuses one that lacks it.
- **`GET /{id}` carries preload paths in a query string** and has nowhere to put
  a per-relation filter, so a narrowed preload is refused there. gRPC sends the
  whole document and does not have this limit.

## Failure modes

| what happens | what the caller gets |
|---|---|
| an option that changes which rows come back | `*remote.OptionError`, nothing sent |
| `crud.Raw`, `crud.EqField`, `crud.False` | `*crud.PredicateError`, nothing sent |
| a patch DTO with an untagged `crud.Opt` | a panic from `remote.New`, at start-up |
| the address is wrong, or a proxy answered | `*remote.ProtocolError`, no sentinel |
| the service refused, classified | `*errs.Fault`, sentinel and violations intact |
| the service failed internally | `*errs.Fault`, `KindInternal`, no violations |
| the answer will not decode | a plain error; `port.KindOf` reads it as internal |

## Files

| File | What it does here |
|---|---|
| `remote/resource.go` | `Resource`, `New`, `TryNew`, the eight methods, `decode`, `keyOf` |
| `remote/options.go` | `ToRequest`, `OptionError`, the three answers, `sortsOf` |
| `remote/transport.go` | `Method`, `Call`, `Transport`, `ProtocolError`, `Truncate` |
| `remote/dto.go` | `checkPatchable` — the `omitzero` start-up refusal |
| `crud/document.go` | `MarshalPredicate`, `PredicateError`, `docWriter`, one `document` per node |
| `crud/predicate.go` | `Predicate`, which declares `document` beside `render` |
| `port/kind.go` | `FaultFrom`, `sentinelFor` |
| `port/request.go` | `FormatID` — `CoerceID`'s inverse |
| `errs/path.go` | `Path.UnmarshalJSON` — the lossless half of the decode |
| `remote/remotehttp/transport.go` | `Transport`, `WithClient`, `WithRequestHook`, `route`, `entityQuery`, `fault`, `faultCode` |
| `port/porthttp/decode.go` | `KindForStatus`, `ParseEnvelope`, `Envelope.Violations`, the wire shapes |
| `crud/rpc/crudgrpc/transport.go` | `Transport`, `WithVocabulary`, `WithCallOptions`, `requestFor`, `fault`, `kindOf` |
| `crud/rpc/crudgrpc/status.go` | `KindForCode` |

## Tests that walk this flow

| Test | File | What it pins |
|---|---|---|
| `TestEveryMethodMakesTheRoundTrip` | `remote/roundtrip_test.go`, `crud/rpc/crudgrpc/client_test.go` | all eight methods, real client to real binding |
| `TestAFilterWrittenInGoArrivesAsTheSameNarrowing` | both | the filter reaches the far repository as the same `crud.Options` |
| `TestAConflictArrivesAsAConflictWithItsViolations` | both | sentinel, kind, violations, and nothing internal |
| `TestAnInternalFailureArrivesEmpty` | both | a 500 carries no violations and no driver text |
| `TestAStaleWriteKeepsTheBranchACallerRereadsFrom` | `remote/roundtrip_test.go` | `ErrStaleVersion` and `ErrConflict` both match |
| `TestARouters404IsNotAMissingRow` | `remote/roundtrip_test.go` | a plain-text *and* a JSON 404 from elsewhere |
| `TestAnUnregisteredMethodIsNotAMissingRow` | `crud/rpc/crudgrpc/client_test.go` | the same trap wearing `Unimplemented` |
| `TestAValidationFailureAndAMalformedRequestAreToldApartByTheirCode` | `crud/rpc/crudgrpc/client_test.go` | the `InvalidArgument` collapse, undone by the code |
| `TestAnOptionThatCannotCrossIsRefusedBeforeAnythingIsSent` | `remote/roundtrip_test.go` | [[D-053]]'s three refusals |
| `TestRawSQLIsNeverPutOnTheWire` | `remote/roundtrip_test.go` | [[D-054]]'s strongest refusal |
| `TestAPatchDtoThatWouldEmptyAColumnIsRefusedAtStartup` | `remote/roundtrip_test.go` | the `omitzero` check |
| `TestARemoteResourceMountsAsAGateway` | `remote/roundtrip_test.go` | two hops, filter and sentinel both intact |
| `TestEveryFilterDocumentSurvivesARoundTripThroughAPredicate` | `crud/query/roundtrip_test.go` | every operator, byte-identical SQL and binds |
| `TestAPredicateTheWireCannotCarryIsRefusedByName` | `crud/query/roundtrip_test.go` | eight refusals, each blamed by constructor |
| `TestAnUnconditionalPredicateNarrowsNothingAndSwallowsAnOr` | `crud/query/roundtrip_test.go` | the True/False asymmetry, all four arms |
| `TestTwoConditionsOnOneFieldBothSurvive` | `crud/query/roundtrip_test.go` | the repeated-JSON-key hazard |
| `TestADecodedFaultStillMatchesTheSentinelItLeftWith` | `port/inbound_test.go` | `FaultFrom` wraps what `sentinelKind` reads |
| `TestAKeyThatWentOutAsTextComesBackTheSameKey` | `port/inbound_test.go` | `FormatID` and `CoerceID` are inverses |
| `TestAPathSurvivesTheWireExactly` | `errs/path_test.go` | names and indices, in order |

## See also

[[FL-015]] [[FL-013]] [[FL-011]] [[FL-012]] [[UC-018]] [[D-053]] [[D-054]]
