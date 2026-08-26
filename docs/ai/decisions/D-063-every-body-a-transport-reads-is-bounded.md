# D-063 — Every body a transport reads is bounded, and the three bindings bound it the same

**Status:** accepted
**Invariant:** No transport reads a request body, or a response body, without a byte cap. A body past the cap is refused before it is parsed, with `errs.CodeTooLarge` and `errs.KindTooLarge` — 413 over HTTP, `ResourceExhausted` over gRPC. There is no spelling for "unbounded".

## The decision

`porthttp.DecodeJSONKeep` reads through an `io.LimitReader` bounded by
`porthttp.MaxBody`, which is **4 MiB**. A handler that needs another number says
so with the binding's `MaxBody` option. Zero or less means the default; nothing
means unlimited.

`remotehttp` does the same thing on the way back: `MaxResponse` is 32 MiB, and
`WithMaxResponse` names another.

## Why

**Because there was no cap at all, and two of the three bindings had none from
their framework either.** The read was `io.ReadAll` on a body nobody bounded.
net/http brings no limit and Gin brings no limit, so one request could hold as
much memory as a client cared to send — on the collection POST, on PATCH, on
PUT, and on `POST /query`, which every list route offers.

**Because Fiber brought one and the other two did not, which made the API
different depending on the framework.** `fiber.New()` defaults to a 4 MiB
`BodyLimit`. So the same request answered 413 on Fiber and 200 on the other two,
and nothing anywhere recorded that as a difference. The number here is Fiber's
for that reason: it is the one the triplet was already half-obeying, so adopting
it changes the fewest answers.

**Because the cap has to be checked, not just applied.** The first version read
`io.LimitReader(r, MaxBody)` and stopped there — which silently *truncates*. A
truncated JSON body usually fails to parse and becomes a 400, which is wrong but
loud; a truncated body that happens to still parse is a write with fields
missing, which is wrong and silent. The reader is given `MaxBody+1` and the
length is compared, so "exactly at the cap" fits and "one past it" is refused.

**Because 413 is a different instruction from 400.** A client that is told 400
looks for something wrong with what it sent. A client told 413 sends less. The
kind carries that — transports map the kind and never the code ([[D-049]]) — so
`errs.KindTooLarge` is a kind of its own rather than a `KindBadRequest` code,
and gRPC answers `ResourceExhausted`, which is its own code for a message over
the receive limit and therefore one a client already branches on.

**Because a client is a transport too.** `remotehttp` read the answer with an
unbounded `io.ReadAll`. A remote resource is another service, and another
service can be wrong: a paging bug on the far side, a proxy substituting an HTML
page, a peer that has been taken over. Any of those became this process running
out of memory — the one failure a client cannot report. `authjwt` already read
JWKS through an `io.LimitReader`; this is the same reflex applied where it was
missing.

## The response is bounded the same way, and for a related reason

A response the *consumer's* presenter returns can fail to encode — a channel, a
NaN — and that is a failure this library cannot prevent, only answer honestly.
`crudnet` marshalled before touching the status precisely so it becomes a silent
500. The other two wrote through their framework's renderer and did not: Gin
answered **200** with a truncated body, and Fiber returned the encoder's error to
`fiber.New`'s default handler, which answers `text/plain` with the message in
it — a presenter's internals on the wire, at a status that said success, which is
[[D-044]] broken by a code path nobody had looked at.

All three now marshal first through a `writeJSON` of their own and answer the
same silent 500. The Content-Type went the same way: `crudnet` answered
`application/json` where Gin, Fiber and `authhttp.Refuse` all answer
`application/json; charset=utf-8`.

## What it forbids

- Do not add a transport that reads a body without a cap.
- Do not offer an option that means "unbounded". A resource that needs more says
  a bigger number.
- Do not use `io.LimitReader` without comparing the length afterwards. Silent
  truncation is worse than the unbounded read it replaces.
- Do not write a response through a framework's own JSON renderer. Marshal first,
  so an unencodable value is a 500 rather than a half-written 200.
- Do not give `KindTooLarge` a status other than 413, or map it to
  `InvalidArgument` on gRPC. It exists to be told apart from a bad request.

## Where it lives

- `port/porthttp/body.go` — `MaxBody`, `DecodeJSONKeepLimit`, `TooLarge`.
- `errs/code.go` — `CodeTooLarge`, `KindTooLarge`.
- `errs/codes.go:StandardCodes` — the row that gives the code its kind.
- `port/porthttp/errors.go:StatusFor` and `port/porthttp/decode.go:KindForStatus`
  — 413, both directions.
- `crud/rpc/crudgrpc/status.go:CodeFor` and `:KindForCode` — `ResourceExhausted`,
  both directions.
- `crud/http/crudnet/options.go:MaxBody`, and its twins in `crudgin` and
  `crudfiber`.
- `crud/http/crudfiber/handler.go:Routes` — the standalone app's `BodyLimit`.
- `remote/remotehttp/transport.go` — `MaxResponse`, `WithMaxResponse`.

## Proven by

- `TestABodyPastTheCapIsRefusedAndReachesNoRepository` in all three of
  `crud/http/crudnet/edge_test.go`, `crud/http/crudgin/edge_test.go` and
  `crud/http/crudfiber/edge_test.go` — with the under-the-cap control, so the
  test cannot pass on a route that refuses everything.
- `TestTheDefaultCapAcceptsAnOrdinaryBody`, the same three files — the control
  on the constant itself. A default of zero read as "read nothing" would make
  every write 413 and leave every other test passing.
- `TestTheStandaloneAppCarriesTheHandlersBodyCap` in
  `crud/http/crudfiber/routing_test.go`, with the control that an unconfigured
  app does not fall through to Fiber's "0 means no limit".
- `TestAnAnswerPastTheCapIsRefusedRatherThanBuffered` in
  `remote/remotehttp/transport_test.go`, with its under-the-cap control.
- `TestAnUnencodableResponseIsAServerFaultThatSaysNothing` in all three
  `edge_test.go` files — with the encodable-presenter control. Restoring the
  framework renderer makes Gin answer 200 and Fiber leak
  `json: unsupported type: chan int` in plain text.
- `TestEveryKindHasAStatusAndTheTableIsTotal` in
  `port/porthttp/errors_test.go`, `TestKindMapsToTheCodeItPromisesTo` in
  `crud/rpc/crudgrpc/status_test.go`, and
  `TestEveryKindRendersACodeAndTheTableIsTotal` in `port/kind_test.go` — the
  three kind-keyed tables, each refusing to pass while a kind has no row. The
  third was added last: it has a `default` arm, so when `errs` gained
  `KindTooLarge` it silently rendered `error_code: "internal"` — a 413 whose body
  told the client the server had broken.

## See also

[[D-045]] [[D-049]] [[D-044]] [[D-021]] [[D-060]] [[FL-013]] [[UC-015]]
