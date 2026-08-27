# D-059 — The HTTP projection of the error contract belongs to `port`, not to a subsystem

**Status:** accepted
**Invariant:** the status table, the response envelope, the `Renderer` seam, the JSON body decode and the raw-body path fallback are one implementation, reachable by any subsystem that answers over HTTP without importing that subsystem's neighbours. No subsystem owns them.

Narrows [[D-045]]'s address the way D-045 narrowed [[D-034]]'s. The rule is
unchanged; the line moves one step further out.

## The decision

`http/crudhttp` held two different things.

One was the HTTP projection of the error contract: the `Kind → status` table and
its inverse, the response envelope and its parser, the `Renderer` seam, the
`Accept-Language` hop, the JSON body decode, and the raw-body fallback that turns
a model field name back into the key the client sent. None of it is about CRUD.
Every one of them is what *any* subsystem needs to answer a failure over HTTP.

The other was CRUD over HTTP: `BulkDeleteRequest`, `CoerceID`, the count and
entity narrowing, the create-time model clearing.

The first half is `port/porthttp` now. The second stays `crud/http/crudhttp`,
which re-exports every name that moved as an alias or a one-line forwarder.

The test that draws the line is [[D-045]]'s, asked one step wider: **could a
subsystem that is not CRUD — auth today, i18n tomorrow — take this without
importing `crud`?**

| file | goes to | because |
|---|---|---|
| `render.go` | `port/porthttp` | `Renderer`, `EnvelopeRenderer`, `RenderOption` — the auth middleware calls all three |
| `envelope.go` | `port/porthttp` | one body shape for the whole framework |
| `errors.go` | `port/porthttp` | the `Kind → status` table; `authgin` has no table of its own precisely because it reads this one |
| `decode.go` | `port/porthttp` | `KindForStatus` and `ParseEnvelope`, the table read backwards |
| `bodyindex.go` | `port/porthttp` | imports `errs` and nothing else; knows nothing about CRUD |
| the body and locale half of `request.go` | `port/porthttp` | `DecodeJSON`, `KeepBody`, `WithBody`, `WithLocale` — general HTTP, and the renderer's fallback reads the retained body |
| `model.go`, `repository.go` | stays `crud/http/crudhttp` | `port.Repository` aliases and the create-time clearing |
| the rest of `request.go` | stays `crud/http/crudhttp` | `BulkDeleteRequest`, `CoerceID`, `NarrowForCount` — these are `query.Request` |
| `transport.go` | `remote/remotehttp` | the client, not the server ([[D-058]]) |

The name is the [[D-035]] cell: subsystem `port` × library `net/http`, exactly as
`crudhttp` is `crud` × `net/http`.

## Why

**Because the auth middleware imported the CRUD binding, and meant it.**
`authhttp`, `authgin`, `authfiber` and `authnet` all imported `crudhttp` — for
`Renderer`, `RenderOption`, `NewRenderer`, `WithLocale` and `AcceptLanguage`.
`crudhttp` pulled `port`, `query`, `crud` **and** `remote`. So a middleware whose
whole job is to check a token transitively depended on the SQL repository, the
predicate AST and an HTTP client to somebody else's service. [[D-058]] gave auth
its own directory; without this it would have been a self-contained subsystem
only in the picture.

The measurement, after: `auth/http/authhttp` and the three middlewares no longer
reach `crud/http/crudhttp`, `crud/sqlrepo` or `remote` at all. What they still
reach is `crud` and `crud/query`, through `port` — because `port.Repository` and
`port.NarrowForCount` are typed in them. That residue is `port`'s to answer for,
not this decision's, and it is stated here rather than left for someone to
discover.

**Because `port` is the address and `errs` is not.** Half of what moved calls
`port.KindOf`, `port.MaxViolations`, `port.Violations` or
`port.FirstLanguageTag`. `errs` is sealed by `scripts/checks.sh:TIER0_SEALED` and has to
reach its first tag with an empty require block, so it cannot take a package that
imports `port`. `port` can, and `port/porthttp` is a subpackage rather than a
change to `port` itself — `port` stays free of `net/http`, which is [[D-045]]'s
own rule about itself.

**Because two status tables is the failure this line of decisions exists to
prevent.** [[D-034]] was written when two bindings had drifted; [[D-045]] when a
fourth transport could not implement the seam. This is the same measurement a
third time, from the subsystem axis instead of the transport axis: an auth
middleware that could not reach the table would grow its own, and the two would
agree until one of them gained a row.

**Because aliases make it a move rather than a break.** Every symbol that left
`crudhttp` is still exported from `crudhttp`. That is the trick [[D-034]] used to
land and [[D-045]] used again, and it costs one file of forwarders with no
behaviour in it.

**What it costs.** One more package, and a file of forwarders that has to be kept
honest. `crud/http/crudhttp/porthttp.go` says so at the top: a symbol in it that
grows a body has stopped being a forwarder.

## The gRPC half is not done, deliberately

`crud/rpc/crudgrpc` and `auth/rpc/authgrpc` each hold their own `Kind → gRPC
code` table, and they can already disagree. A `port/portgrpc` is where the shared
one belongs and this decision does not create it: it would be a whole module for
one table, and [[D-048]]'s count rule says wait for the second caller that
actually needs it. Written down because the place is now obvious and the reason
for the gap is not.

## What it forbids

- Do not re-derive the status table, the envelope, the code mapping or the
  bad-request sentinel in a subsystem. That is [[D-034]]'s forbid, still verbatim.
- Do not import `crud/http/crudhttp` from a package that is not about CRUD.
  `port/porthttp` is what a non-CRUD subsystem takes.
- Do not let `port/porthttp` import a subsystem. `scripts/checks.sh:TIER0` lists it, so
  this one is mechanical rather than a matter of care.
- Do not put an HTTP type into `port` itself. The split [[D-045]] drew still
  holds: `port` has no `net/http` in it, and `porthttp` is where it goes.
- Do not give a symbol in `crud/http/crudhttp/porthttp.go` a body. It is a
  forwarder file; a body means the symbol belongs on one side or the other.
- Do not delete the forwarders before the deprecation cycle the first tag starts.

## Where it lives

- `port/porthttp/render.go` — `Renderer`, `EnvelopeRenderer`, `RenderOption`,
  `NewRenderer`, `MaxViolations`, `DefaultRetryAfter`, and the five options.
- `port/porthttp/envelope.go` — `Envelope`, `Groups`, `Internal`.
- `port/porthttp/errors.go` — `Status`, `StatusFor`, `KindOf`, `AcceptLanguage`,
  `ErrBadRequest` and its three builders.
- `port/porthttp/decode.go` — `KindForStatus`, `ParseEnvelope`,
  `Envelope.Violations`, and the wire shapes behind them.
- `port/porthttp/bodyindex.go` — `BodyResolver` and the leaf-path index.
- `port/porthttp/body.go` — `DecodeJSON`, `DecodeJSONKeep`, `KeepBody`,
  `MaxKeptBody`, `MalformedBody`, `WithBody`, `BodyFrom`, `WithLocale`,
  `LocaleFrom`.
- `crud/http/crudhttp/porthttp.go` — the forwarders, all of them, with no
  behaviour.
- `crud/http/crudhttp/request.go` — `BulkDeleteRequest`, `CoerceID`,
  `NarrowForCount`, `NarrowForEntity`.
- `crud/http/crudhttp/model.go`, `:repository.go` — unchanged.
- `auth/http/authhttp/authhttp.go` and the three middlewares — importing
  `port/porthttp` now.
- `remote/remotehttp/` — the client transport, which reads
  `porthttp.KindForStatus` and `porthttp.ParseEnvelope` rather than a copy.
- `scripts/checks.sh:TIER0` — `port/porthttp` is a manifest entry.

## Proven by

- `go list -deps ./auth/http/authnet` and the same for `authgin`, `authfiber` and
  `authhttp` — no `crud/http/crudhttp`, no `crud/sqlrepo`, no `remote`. That is
  the claim this decision makes, measured rather than asserted.
- `go list -deps ./crud/http/crudhttp` — no `remote`. The server half stopped
  importing the client when `transport.go` left ([[D-058]]).
- `make check-tiers` with `port/porthttp` in `TIER0` — verified by importing
  `crud/sqlrepo` from it and watching the arm name the package and fail.
- The tests moved with the code and the only line that changed in any of them is
  the `package` clause: `render_test.go`, `errors_test.go`, `bodyindex_test.go`
  and `locale_test.go` are `port/porthttp`'s now, and not one assertion was
  touched. That is what makes this a move rather than a rewrite.
  `TestALocaleSetByOneTransportIsReadByAnother` is the one with teeth — a second
  context key left behind in `crudhttp` would be invisible to a gRPC renderer and
  both packages' own suites would still pass.
- `TestTheSameServiceMountsOnAllFourTransports` and
  `TestOneServiceMountsOnAllThreeBindings` in `test/portmount/` — the four
  transports still answer the same status, the same body bytes and the same
  command with the table one directory further out.
- The auth triplet suites — `auth/http/authnet`, `auth/http/authgin`,
  `auth/http/authfiber` carry the same test names file for file, and all three
  pass against `porthttp`'s renderer with no test edited.
- `make examples` — the example stacks compile through the forwarders, which is
  the no-breaking-change claim.

## See also

[[D-045]] [[D-034]] [[D-058]] [[D-035]] [[D-043]] [[D-048]] [[D-052]] [[D-053]] [[FL-011]] [[FL-015]] [[FL-019]]
