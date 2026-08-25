# D-052 — A gRPC resource carries documents, not a schema

**Status:** accepted
**Invariant:** Every `rpc/crudgrpc` request and response is a `google.protobuf.Struct` carrying the same JSON document the HTTP bindings speak. There is no generated message per resource, no `protoc` in any build target, and no server reflection. What a client branches on — the code — is spelled identically on every transport.

## The decision

Four shapes, each of which could defensibly have gone the other way. They are
here together because they are one design: a generic CRUD resource has no
compile-time type, and everything below follows from admitting that rather than
working around it.

**1. `structpb.Struct` in, `structpb.Struct` out.** A repository is generic over
`M`. A library cannot generate a proto message for a model it has never seen, so
either the consumer runs a generator this repository does not ship, or the
payload is a self-describing document. It is a document.

**2. No server reflection.** A generic resource has no compiled file descriptor,
so `grpcurl` and its kind cannot list the methods. Clients call by full method
name.

**3. The code keeps its lowercase spelling in `BadRequest_FieldViolation.Reason`
— `unique`, not `UNIQUE`** — against AIP-193's UPPER_SNAKE_CASE convention.

**4. A violation that names no field becomes a field violation with an empty
`Field`.** One list, one detail type, whatever the status is.

## Why

**Documents, because the alternative buys a name and costs a toolchain.** A
checked-in `.proto` with generated `.pb.go` would give named request envelopes —
and the entity inside them would still be a `Struct`, because the entity is the
part that cannot be typed. So the trade is: named envelopes, in exchange for
`protoc`, a C++ binary `go` cannot install, in a repository whose every target
runs with `go` alone. **Refused.** A `wrapperspb.BytesValue` carrying JSON was
also considered and refused: byte-exact and precision-safe, and opaque to
`protojson` and every gRPC tool there is. A gRPC API whose payload is a blob is
not a gRPC API.

**Documents, and also because they are what makes the four-transport claim
measurable.** The payload is exactly the JSON the three HTTP bindings carry, so
one `port.Service` value can be mounted on all four and the answers compared
document to document. With a resource-specific message shape there would be
nothing to compare, and [[D-045]]'s central sentence would go back to being an
argument.

**No reflection, because synthesising a descriptor is the only unboring thing in
the binding.** It can be done — clone a `descriptorpb.FileDescriptorProto`, run
it through `protodesc.NewFile`, register it in `protoregistry.GlobalFiles` at
`Register` time, about sixty lines — and it is legal under [[D-021]], which
prefers magic that fails at start-up. It is deferred rather than refused: the
limit is stated in `rpc/crudgrpc/doc.go` and in [[FL-013]], and a consumer who
needs reflection registers a descriptor it generated itself. Write it when
somebody asks, and add the row here.

**Per-resource service names rather than one fixed service.** `vv.crud.v1.<Name>`
per resource, with a name containing a dot used verbatim. The alternative — one
service with a `resource` field in every request — would buy nothing: reflection
is unavailable either way, and a shared service makes every resource's `Create`
the same full method name, which is what a per-method interceptor and an
authorization rule key on.

**Lowercase, because `ROADMAP-errors.md` §11 asks for *a stable machine code*.**
A code spelled `unique` in an HTTP envelope and `UNIQUE` in a gRPC detail is not
stable: a client speaking both needs two tables, and the identity a code exists
to provide is gone. `Reason` is a free-form string and UPPER_SNAKE_CASE is a
style rule; identity across transports is a contract. When the two conflict the
contract wins.

**An empty `Field` rather than a second detail type.** gRPC has no counterpart of
the envelope's `general` group, and `ROADMAP-errors.md` §16 settled that a
violation with a path and one without are never split into two lists. Dropping
the pathless ones is lossy — a bare `restrict` conflict names no field and is
the whole answer. A second detail type for them is the split under another name.
So: one list, and a field violation whose field is empty says exactly what the
envelope's `general` group says, in the shape this transport has.

**And what the two `codes.Code` collisions cost.** `KindValidation` and
`KindBadRequest` both answer `InvalidArgument`, so 422 and 400 collapse; every
conflict answers `AlreadyExists`, including `restrict` and `stale_version`,
where `FailedPrecondition` and `Aborted` read better. Refining per *code* would
be a second table keyed on the thing [[D-049]] says must not decide a response,
and a service declaring fifty codes of its own would then owe fifty rows. The
cost is accepted the way [[UC-015]] already accepts D-049's: the kind decides,
and the machine code in the details is what separates the cases.

## What it forbids

- Do not add `protoc`, `buf` or any generated `.pb.go` to this repository to give
  a resource a message type. If a consumer wants one it generates it in its own
  build, over the document this binding already speaks.
- Do not import `http/crudhttp` from `rpc/crudgrpc`. A non-HTTP transport
  reaching into the HTTP package makes [[D-045]]'s sentence meaningless, and it
  is the shortcut the violations pipeline would have been taken through. That
  pipeline is `port.Violations`.
- Do not reimplement the violations pipeline here. Forty lines of sort, cap and
  message ladder in a second place is exactly what [[D-034]] and [[D-045]] exist
  to prevent, and nothing would fail when the two drifted.
- Do not spell a code differently on this transport, in either direction.
- Do not give `ErrorInfo.Metadata` a second key. It is `partial` or it is empty:
  a proto map has no order, so a second key is a determinism hazard, and it is
  the obvious place an internal name would end up ([[D-044]]).
- Do not put `err.Error()` in a status message. A fault's own text names the
  entity.

## Where it lives

- `rpc/crudgrpc/doc.go` — the method table and the four stated limits.
- `rpc/crudgrpc/message.go` — the document conversion, the key spelling, and the
  number-precision limit.
- `rpc/crudgrpc/status.go` — `CodeFor`, `Code`, `StatusRenderer` and the details.
- `rpc/crudgrpc/service.go` — `ServiceName`, `ServicePrefix`, the hand-built
  `grpc.ServiceDesc`.
- [[FL-013]] — the per-binding difference table, with this transport's column.

## Proven by

- `TestKindMapsToTheCodeItPromisesTo` — `rpc/crudgrpc/status_test.go` — every
  arm, with a control asserting the table covers exactly the kinds `errs`
  declares, so a ninth kind fails rather than being mapped to `Internal`.
- `TestAStatusMessageNamesNoEntityAndNoDriverText` — the same file — the
  strongest single assertion here: `status.New(code, err.Error())` is the
  natural wrong answer and it ships the table name. Its control asserts the
  fault's own `Error()` *does* name the entity, so avoiding it means something.
  Verified by making the renderer use `err.Error()`: the test fails naming
  `users`.
- `TestAClassifiedConflictReachesAGrpcClientWithNothingInternal` — the same
  file, over every entry of the captured corpus on all four engines, with
  per-engine counters so an emptied loop cannot pass; and
  `test/integration/rpc_grpc_test.go`, the same claim against errors a live
  server raised, over every target including the two that classify nothing.
- `TestAViolationWithNoPathIsStillInTheOneList` and
  `TestTheReasonIsTheCodeSpelledTheUsualWay` — the empty-`Field` rule and the
  spelling.
- `TestTheSameCodeIsSpelledTheSameOnBothTransports` —
  `test/portmount/grpcmount_test.go` — the envelope's `error_code` and the
  detail's `Reason` are the identical string, asserted to be literally
  `"unique"` so an implementation that UPPER_SNAKE_CASEs one side fails loudly.
- `TestAbsentNullAndValueSurviveTheStructRoundTrip` —
  `rpc/crudgrpc/handler_test.go` — [[UC-003]] on this wire shape, with the
  control that an absent key and an explicit null produce two different states.
  Verified by making the decoder drop null-valued keys: both legs fail.
- `TestAnInt64KeyIsCarriedAsAString` — the documented precision limit measured
  rather than claimed, with the control that the same number *inside* an entity
  document really does lose precision.
- `TestEveryCommandHasAMethod` — eight methods, each handing over the command
  `port` declares, with `ReadOnly` registering three and a write answering
  `Unimplemented`.

## See also

[[D-045]] [[D-049]] [[D-044]] [[D-043]] [[D-051]] [[D-021]] [[UC-015]] [[FL-013]]
