# D-045 — The shared half is transport-neutral; a binding is a shell over `port`

**Status:** accepted
**Invariant:** Everything a transport binding does that is not routing, decoding or writing a response comes from a transport-neutral package. Nothing shared may be shaped by HTTP, and no binding — HTTP or otherwise — may re-derive the status table, the code mapping or the field clearing.

Supersedes [[D-034]], which is right about the rule and wrong about the address.
Phase 5 moved the shared half, so D-034 is history and this is what the tree
obeys.

**Narrowed by [[D-059]], on the same test asked one step wider.** This decision
splits transport-neutral from HTTP. D-059 splits what is left: the part of the
HTTP half that belongs to *no* subsystem — the status table, the envelope, the
`Renderer` seam, the body decode, the raw-body fallback — is `port/porthttp`, and
`crud/http/crudhttp` keeps only what is HTTP *and* CRUD. Every file named below
under `crud/http/crudhttp` that D-059 lists has moved; `crudhttp` re-exports all
of it, so the rule and the exported surface are both unchanged. Read the
addresses below as D-059 leaves them.

## The decision

D-034 says everything shared *must come from `crud/http/crudhttp`*. That was true
while every binding was HTTP. A gRPC binding breaks it literally: gRPC cannot
implement a renderer returning `(status int, header http.Header, body any)`, so
either gRPC re-derives the mapping — the exact duplication D-034 exists to
prevent — or the shared half moves somewhere with no `net/http` in it.

It moves to `port`. `crud/http/crudhttp` keeps what is genuinely HTTP: the status
table, the response body, the header. `port` holds what is not: the commands,
the `Service` interface, the `Mapper`, the code vocabulary.

This is a narrowing of D-034's address, not a relaxation of its rule. A binding
still owns exactly three things — which routes exist, how a body becomes a Go
value, and how a response is written.

## Why

**Because D-034's own argument generalises further than D-034 did.** It was
written when the third binding was added and the measurement was that two
bindings had already drifted. A fourth binding on a different protocol is the
same measurement with a longer lever.

**Because "transport-neutral" has a test and `crudhttp` fails it.** The test is:
can a non-HTTP transport implement this interface without importing `net/http`?
A renderer returning an `http.Header` cannot be implemented by gRPC, so it is
not the shared half — it is the HTTP half wearing a neutral name. That is why
the renderer seam stays on the HTTP side and does not migrate to `errs`, and
it is worth saying because the obvious place to put a renderer is next to the
errors it renders. [[D-059]] later asked the same question about the *subsystem*
rather than the protocol and moved the seam to `port/porthttp` — still HTTP,
still not `errs`, and no longer CRUD's.

**Because type aliases make it a move rather than a break.** `crudfiber.Repository`
and `crudfiber.Envelope` are already aliases; re-pointing an alias changes no
consumer's code. The same trick that let D-034 land without a breaking change
lets its successor land the same way. Phase 5 measured it: the seven example
stacks under `_examples/` compile untouched, and a parameterised alias —
`Handler[M, ID, U] = HandlerFor[M, ID, U, M]` — supplied the fourth type argument
`New` must not be made to ask for.

**Because [[D-022]] already pointed here.** The handler takes an interface, not a
concrete repository, precisely so a service can sit in between. The port layer is
that seam made explicit, and D-022's type aliases keep every current signature
compiling.

**What it costs.** A layer with a real price: one more indirection, one more
generated artefact, and a hand-written service now has two shapes to satisfy
instead of one. `ROADMAP-errors.md` §13 names it as a hard problem rather than a
free win, and this decision does not pretend otherwise. The price buys a second
protocol without a second status table.

## The split runs both ways

A client is the same shape read backwards, and it inherits this rule rather than
restating it. `port.FaultFrom` rebuilds a fault from a kind, a code, a list of
violations and a partial marker; `porthttp.KindForStatus` and
`crudgrpc.KindForCode` are two tables that produce that kind, and each lives in
the same package as the forward table it inverts.

That placement is the whole of it. A client that kept its own copy of either
table would agree with the server until the first time one of them gained a row,
and the disagreement would be a status silently reclassified — which is the
failure this decision exists to prevent, arriving from the other direction.

This was stated as *a transport lives beside the binding it calls*, and it held
while the tables were beside the binding. [[D-059]] moved the HTTP tables to
`port/porthttp`, so the HTTP client transport moved to `remote/remotehttp` and
reads them from there; the gRPC one stayed in `crud/rpc/crudgrpc`, because
`remote` is in the root module and may not import grpc, so moving it would cost a
module for one file. The rule that survives is the one that mattered: **one copy
of each table, and the inverse never gets its own.** [[D-058]] records the
asymmetry.

`remote` itself is transport-neutral by the same test [[D-034]]'s successor
applies here: nothing in it names a status, a header, a code or a connection,
and its seam is a `Call` — the mirror of `port`'s commands.

## What it forbids

- Do not put an HTTP type in the shared half. If gRPC cannot implement it, it is
  not shared.
- Do not give a client its own copy of a status or code table. The inverse of
  a table lives in the same file as the table.
- Do not re-derive the status table, the code mapping, the bad-request sentinel
  or the create-time field clearing in a binding. That is D-034's forbid and it
  survives verbatim.
- Do not give a binding its own error codes so it can skip the chain — see
  [[D-043]].
- Do not move the renderer out of the HTTP half. It is HTTP-shaped on purpose —
  `port/porthttp` since [[D-059]], and never `port` itself or `errs`.
- Do not break a binding's exported surface while moving. Alias, as D-034 did.

## Where it lives

The neutral half, all of it in the root module and all of it stdlib plus
`crud`, `query` and `errs` — which `scripts/checks.sh:TIER0` is what enforces:

- `port/doc.go` — what the layer is and the four limits it states rather than
  leaves to be discovered.
- `port/service.go` — `Service`, `DefaultService`, `NewService`, `ServiceOption`
  and the whole read and write orchestration.
- `port/command.go` — the eight commands a transport builds.
- `port/mapper.go` — `Mapper` and `Identity`.
- `port/path.go` — `Fields`, the service's hop, and `Hops`, which a binding
  wires ahead of the raw-body fallback.
- `port/repository.go` — `Repository`, moved from `crud/http/crudhttp`.
- `port/model.go` — `Sanitize` / `ClearGenerated`, the create-time clearing.
- `port/request.go` — `CoerceID` / `NarrowForCount` / `NarrowForEntity`.
- `port/sentinel.go` — `ErrBadRequest` and its three builders.
- `port/kind.go` — the code vocabulary: `FaultOf`, `KindOf`, `KindOfWith`,
  `CodeForKind`, `DefaultMessage`, and the precedence table behind them.
- `port/violations.go` — `Violations`, `ViolationOptions` and `MaxViolations`:
  the copy, the path chain, the sort, the cap and the message ladder. Moved down
  from `crud/http/crudhttp` at phase 9 — see *The named follow-up*, which this
  discharges.
- `port/locale.go` — `WithLocale`, `LocaleFrom` and `FirstLanguageTag`. One
  context key for every transport, because two would each be invisible to the
  other's renderer.

The HTTP half, unchanged in what it exports:

- `port/porthttp/errors.go:Status` / `:StatusFor` — the status table, over
  `port`'s answer. In `crud/http/crudhttp` until [[D-059]].
- `port/porthttp/render.go` — the `Renderer` seam and `EnvelopeRenderer`: the
  status, the envelope, the `Retry-After` header, the 500 short-circuit and
  every `RenderOption`. The seam stays on the HTTP side on purpose — see the
  second *Why* above — while the pipeline it used to own is `port.Violations`.
- `port/porthttp/envelope.go`, `:bodyindex.go`, `:decode.go`, `:body.go` — the
  body, the fallback, the table read backwards, and the JSON decode.
- `crud/http/crudhttp/repository.go`, `:model.go`, `:request.go` — what is HTTP
  *and* CRUD: the `port.Repository` alias, the create-time clearing, and the
  request shapes.
- `crud/http/crudhttp/porthttp.go` — the aliases and one-line forwarders that
  keep every current signature compiling, including `WithLocale`, `LocaleFrom`
  and `AcceptLanguage`.

The four shells — three HTTP and one that is not:

- `crud/http/crudfiber/handler.go`, `crud/http/crudgin/handler.go`,
  `crud/http/crudnet/handler.go` — `HandlerFor[M, ID, U, In]`, the `Handler` alias
  over it, and the four constructors `New` / `NewFor` / `Serving` /
  `ServingFor`.
- `crud/http/crudfiber/options.go` and its two counterparts — `collect`, `service`,
  `rendererFor`. `collect` and the two rule methods — `port.Rules.Service` and
  `port.Rules.RefuseServiceOptions` — are shared rather than copied, and
  `port.Rules` is where the five transport-neutral settings live.
- `crud/rpc/crudgrpc/handler.go`, `:options.go`, `:service.go` — the same four
  constructors and the same option set on gRPC, with a `context.Context` where
  the HTTP ones take a request.
- `crud/rpc/crudgrpc/status.go` — the renderer this transport needs, which is not
  and cannot be `porthttp.Renderer` ([[D-052]]).

[[FL-015]] traces one request through all of it.

**The named follow-up, discharged at phase 9.** The violations pipeline —
`EnvelopeRenderer.violations`, the chain application, the sort, the cap and the
message ladder — is equally neutral, and the instruction was: move it when
`crud/rpc/crudgrpc` exists, not before, on [[D-048]]'s count rule. It exists, and the
pipeline is now `port.Violations` with `port.ViolationOptions` naming what it
needs. The second implementation settled two things the first could not: the
transport's own guess is a separate `Fallback` field rather than a last
resolver, because a declaration must always beat it; and the locale is read from
the context rather than passed, so one key serves every transport.

Two things deliberately did **not** move with it. The `Renderer` seam stayed —
it answers an `http.Header` and gRPC cannot implement it, which is this
decision's own test. And "the list was cut short" stayed with each transport:
the *cap* is `port`'s, and how a client is told is the envelope's `partial` flag
on one side and an `ErrorInfo` metadata key on the other.

## Proven by

- `TestOneServiceMountsOnAllThreeBindings` in `test/portmount/mount_test.go` —
  [[D-034]]'s check in its D-045 form. One `port.Service` value, mounted with
  `crudfiber.Serving`, `crudgin.Serving` and `crudnet.Serving`, answering the
  same status, the same body **bytes** and — the assertion with teeth — the same
  *command*. A compile-only assertion would pass whatever the three bindings
  did; this fails the moment one of them re-derives a rule. Verified by putting
  `NarrowForCount` back into `crudnet`'s count route: the recorded command
  diverges and the test names which binding did it.
- `TestTheServiceIsWhereTheRulesRan` beside it — the other half, because three
  bindings that had all forgotten to narrow a count would agree with each other.
- `TestOnePortServiceMountsOnAllThreeBindings` in
  `test/integration/http_port_test.go` — the same claim against a live database,
  across every engine.
- The triplet suites keep their meaning: removing the `crud.ErrNotFound` arm from
  `porthttp.StatusFor` fails `TestRepositoryErrorsBecomeStatusCodes`,
  `TestDeletingNothingIs404ForOneRowAndZeroForASet` and
  `TestPutIsNotAWayAroundAllowClientID` in **all three** packages, identically.
  Verified by breaking it.
- `make check-tiers` — `port` may import only the standard library, `crud`,
  `query` and `errs`. The arm had never run against code before this phase
  (`go list ./port/...` matched no packages); verified by importing `catalog`
  from `port` and watching it fail.
- `TestNewForInfersItsInputFromTheMapper` and
  `TestNewInfersItsTypeParametersFromTheRepository` in every binding's
  `options_test.go` — the move cost no inference, which is [[D-022]]'s half.
- `make examples` — the seven example stacks compile untouched. That is the
  no-breaking-change claim, measured rather than asserted.
- **Adding a transport requires no change to `errs`.** Phase 9 paid this, and
  the measurement is worth spelling out because the claim has a strict and a
  loose reading. Phase 9 landed in three parts. The first executed the move this
  decision scheduled above — `port/violations.go`, `port/locale.go` — and is
  behaviour-preserving: every existing test in `crud/http/crudhttp` and in the three
  binding suites passes **unedited**, and `errs/` and `errs/sqlerr/` are
  untouched. The second wrote `crud/rpc/crudgrpc`, and its diff over `errs/`,
  `errs/sqlerr/`, `port/` **and** `crud/http/crudhttp/` is empty: a whole binding on
  a second protocol, with no line changed in anything shared. The third added
  message catalogues to `errs`, which is additive and touches no transport. So
  the strict reading holds, once the move this decision itself ordered has
  landed — and a `port` diff in the first part is this decision executing its
  own instruction rather than gRPC forcing one.
- `TestTheSameServiceMountsOnAllFourTransports` —
  `test/portmount/grpcmount_test.go` — the claim above with teeth. One
  `port.Service` value on Fiber, Gin, net/http and gRPC; the same command
  recorded by all four and the same answer document. Verified by putting
  `NarrowForCount` back into `crudgrpc`'s Count method: the recorded command
  diverges and the test names the offender.
- `TestAGeneratedResourceResolvesTheSameFieldOnAllFourTransports` and
  `TestTheSameCodeIsSpelledTheSameOnBothTransports` — the same file — the path
  chain and the code vocabulary reach a fourth transport without a second copy
  of either.
- `TestOnePortServiceAlsoMountsOnGRPC` — `test/integration/rpc_grpc_test.go` —
  the same service value `http_port_test.go` mounts on the three HTTP bindings,
  answering over gRPC against every live engine.
- `TestALocaleSetByOneTransportIsReadByAnother` —
  `port/porthttp/locale_test.go` — the bug the move could have introduced: a
  second context key left behind in `crudhttp` would be invisible to a gRPC
  renderer, and both packages' own suites would still have passed.

## See also

[[D-034]] [[D-022]] [[D-015]] [[D-043]] [[D-033]] [[D-051]] [[D-052]] [[D-053]] [[D-058]] [[D-059]] [[FL-018]]
