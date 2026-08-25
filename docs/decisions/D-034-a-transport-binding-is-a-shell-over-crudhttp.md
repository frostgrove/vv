# D-034 — A transport binding is a shell over `crudhttp`

**Status:** superseded by [[D-045]]
**Invariant:** everything a transport binding does that is not routing, body binding or writing a response must come from `crud/http/crudhttp`; no binding may re-derive the status table, the bad-request sentinel or the create-time field clearing.

D-045 keeps the rule and changes the address. Phase 5 moved the transport-neutral
half to `port` and left `crud/http/crudhttp` what is genuinely HTTP, so the addresses
in this file are no longer where the code is — read [[D-045]] for those.

[[D-059]] moved the address a second time and for the same reason one axis over:
the HTTP projection of the error contract — the status table, the envelope, the
`Renderer` seam, the body decode — is `port/porthttp`'s, because the auth
middleware needs it and must not import CRUD to get it. A binding is a shell over
`port` **and** `porthttp` now, with `crudhttp` holding only what is HTTP *and*
CRUD. `crudhttp` re-exports every name this file names, so nothing below is a
compile error — only a redirection.

What follows is the argument, not the map, and it is left as it was written. It
is why the rule exists, and it has now survived its own address changing twice.

## The decision

`crud/http/crudhttp` holds the half of the HTTP layer that has no framework in it:

| Symbol | What it is |
|---|---|
| `Repository[M, ID, U]` | the interface the handler holds ([[D-022]]) |
| `Status`, `StatusText`, `ErrorBody`, `Body` | the error-to-response mapping ([[D-015]]) |
| `ErrBadRequest`, `BadRequest`, `BadRequestf` | the sentinel a binding raises for its own refusals |
| `Sanitize`, `ClearGenerated` | what a create request is not allowed to dictate ([[D-003]] on the write side, [[D-012]] on PUT) |
| `CoerceID` | the path parameter, through `query.Coerce` |
| `NarrowForCount`, `NarrowForEntity` | what a count and a single-entity read drop from the request |
| `DecodeJSON` | a body read, with an empty body meaning "no narrowing" |
| `BulkDeleteRequest[ID]` | the bulk-delete body |

A binding — `crudfiber`, `crudgin`, `crudnet`, or one a consumer writes — owns
exactly three things: which routes exist and how they are mounted, how a body becomes a
Go value, and how a response is written. `crudfiber.Repository`,
`crudfiber.ErrorBody` and their `crudgin` counterparts are type aliases, and
`Status` is a one-line call, so the exported surface of each binding is
unchanged and there is still only one table.

## Why

**Because the alternative was measured, not guessed.** [[FL-011]] already warned
that a second mapping "will drift". Two switches over the same sentinels, in two
packages, updated by two changes, is a defect with a delay fuse: the day
`crud.ErrConflict` gains a sibling, one binding returns 409 and the other 500,
and nothing fails.

**Why aliases rather than re-exports.** `crudfiber.Repository[M, ID, U]` has to
keep meaning what [[D-022]] says it means — anything satisfying it is accepted —
and a generic type alias is identical to the aliased type, so a service written
against one binding satisfies the other with no change. The integration suite
mounts the same `articleService` on both.

**Why body binding stayed with the bindings.** It is the one part that is
genuinely framework-shaped. Fiber's `Bind().Body()` dispatches on Content-Type
and accepts XML and form encodings; Gin's binder runs `validator/v10` over
`binding:"…"` tags, which would let a tag on a model change what the CRUD routes
accept under one transport and not the other. `crudgin` therefore decodes with
`encoding/json` through `crudhttp.DecodeJSON`, and accepts JSON only. That is a
real difference between the bindings and it is named in [[FL-013]] rather
than smoothed over.

**Why `crudhttp` is in the root module.** It imports `crud`, `query` and the
standard library, and nothing else. Putting it anywhere else would make one
binding depend on the other's module ([[D-033]]).

**Why `crudnet` is too.** The `net/http` binding imports nothing outside the
standard library either, so [[D-033]]'s rule puts it in the library: a module of
its own would be a second `go get` bought for no dependency. It is the shape
this decision predicts — a binding is thin enough that when the framework is
free, the binding is free.

## What it forbids

- Do not copy the `Status` switch into a binding. Call it.
- Do not give a binding its own bad-request sentinel. `errors.Is` against
  `crudhttp.ErrBadRequest` is how a 400 is recognised, and a private copy is
  invisible to the shared mapping.
- Do not let a 500 carry a message in a new binding either. The rule is
  [[D-015]]'s and it is enforced in `crudhttp.Body`, so a binding that builds
  its own body is re-opening a closed hole.
- Do not import a web framework from `crud/http/crudhttp`. It is what makes the
  package shareable.
- Do not move body binding into `crudhttp` to "finish the job". The differences
  are real, and hiding them behind one function would make one transport's
  behaviour silently wrong.
- Do not add a symbol to `crudhttp` that only one binding uses. It is the shared
  half by definition; a one-caller helper belongs in that binding.

## Where it lived

The addresses below were correct when this decision was in force. `Status`,
`StatusText` and `Body` are the shape phase 4's renderer replaced, and the rest
moved to `port` at phase 5. [[D-045]] and [[FL-015]] carry the current map.


- `crud/http/crudhttp/repository.go:Repository` — the interface.
- `port/porthttp/errors.go:Status` — the whole mapping in one switch.
- `port/porthttp/errors.go:Body` — the response body, and the 500 silence.
- `port/porthttp/errors.go:ErrBadRequest` — the one sentinel, with the reason on
  it.
- `crud/http/crudhttp/model.go:Sanitize` / `:ClearGenerated` — the create-time
  clearing.
- `crud/http/crudhttp/request.go:CoerceID` / `:NarrowForCount` / `:NarrowForEntity` /
  `:DecodeJSON`.
- `crud/http/crudfiber/options.go` and `crud/http/crudgin/options.go` — the aliases and the
  one-line `Status`.

## Proven by

All three bindings run the same test suite: `crud/http/crudfiber`, `crud/http/crudgin`
and `crud/http/crudnet` have the same 147 test and subtest names, ported one to one,
and all three are green.

- Removing the `crud.ErrNotFound` arm from `crudhttp.Status` fails
  `TestRepositoryErrorsBecomeStatusCodes`, `TestDeletingNothingIs404ForOneRowAndZeroForASet`
  and `TestPutIsNotAWayAroundAllowClientID` in **all three** packages,
  identically. That is the check that the shared half is genuinely shared rather
  than duplicated.
- `TestGinHTTPServiceLayerIsHonoured` and `TestNetHTTPServiceLayerIsHonoured`
  mount the same `articleService` declared in `test/integration/http_test.go`,
  which only compiles because the three `Repository` types are one type.
- `TestStatusMapsWhatItPromisesTo` in every binding's `edge_test.go` — the
  table, arm by arm, from every side.

The sharper check is the third binding. `crud/http/crudnet` was written against this
package and needed nothing added to it: it holds `crudhttp.Repository` and calls
`Body`, `CoerceID`, `Sanitize`, `ClearGenerated`, `NarrowForCount`,
`NarrowForEntity`, `DecodeJSON` and `BulkDeleteRequest` for everything that is
not routing, body decoding or writing a response. If the split in this decision
were in the wrong place, writing that package is where it would have shown.

It carries the same 147 test and subtest names as the other two, name for name,
and `test/integration/http_net_test.go` runs the same nine end-to-end tests
against live PostgreSQL and MySQL — mounting the very same `articleService` the
other two suites mount, which only compiles because all three `Repository` types
are one type.

One divergence did show up and was closed rather than tolerated: writing the
response with `json.Encoder` appends a newline, so `crudnet`'s bodies were a
byte different from the other two. `writeJSON` marshals first instead, which
also lets an unencodable value become a 500 rather than a half-written 200.

## See also

[[D-015]] [[D-022]] [[D-033]] [[D-012]] [[D-045]] [[D-059]] [[FL-011]] [[FL-013]]
