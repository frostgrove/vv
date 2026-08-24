# D-034 — A transport binding is a shell over `crudhttp`

**Status:** accepted
**Invariant:** everything a transport binding does that is not routing, body binding or writing a response must come from `http/crudhttp`; no binding may re-derive the status table, the bad-request sentinel or the create-time field clearing.

## The decision

`http/crudhttp` holds the half of the HTTP layer that has no framework in it:

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

A binding — `crudfiber`, `crudgin`, or one a consumer writes — owns exactly
three things: which routes exist and how they are mounted, how a body becomes a
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
real difference between the two bindings and it is named in [[FL-013]] rather
than smoothed over.

**Why `crudhttp` is in the root module.** It imports `crud`, `query` and the
standard library, and nothing else. Putting it anywhere else would make one
binding depend on the other's module ([[D-033]]).

## What it forbids

- Do not copy the `Status` switch into a binding. Call it.
- Do not give a binding its own bad-request sentinel. `errors.Is` against
  `crudhttp.ErrBadRequest` is how a 400 is recognised, and a private copy is
  invisible to the shared mapping.
- Do not let a 500 carry a message in a new binding either. The rule is
  [[D-015]]'s and it is enforced in `crudhttp.Body`, so a binding that builds
  its own body is re-opening a closed hole.
- Do not import a web framework from `http/crudhttp`. It is what makes the
  package shareable.
- Do not move body binding into `crudhttp` to "finish the job". The differences
  are real, and hiding them behind one function would make one transport's
  behaviour silently wrong.
- Do not add a symbol to `crudhttp` that only one binding uses. It is the shared
  half by definition; a one-caller helper belongs in that binding.

## Where it lives

- `http/crudhttp/repository.go:Repository` — the interface.
- `http/crudhttp/errors.go:Status` — the whole mapping in one switch.
- `http/crudhttp/errors.go:Body` — the response body, and the 500 silence.
- `http/crudhttp/errors.go:ErrBadRequest` — the one sentinel, with the reason on
  it.
- `http/crudhttp/model.go:Sanitize` / `:ClearGenerated` — the create-time
  clearing.
- `http/crudhttp/request.go:CoerceID` / `:NarrowForCount` / `:NarrowForEntity` /
  `:DecodeJSON`.
- `http/crudfiber/options.go` and `http/crudgin/options.go` — the aliases and the
  one-line `Status`.

## Proven by

The two bindings run the same test suite: `http/crudfiber` and `http/crudgin`
have the same 147 test and subtest names, ported one to one, and both are green.

- Removing the `crud.ErrNotFound` arm from `crudhttp.Status` fails
  `TestRepositoryErrorsBecomeStatusCodes`, `TestDeletingNothingIs404ForOneRowAndZeroForASet`
  and `TestPutIsNotAWayAroundAllowClientID` in **both** packages, identically.
  That is the check that the shared half is genuinely shared rather than
  duplicated.
- `TestGinHTTPServiceLayerIsHonoured` in `test/integration/http_gin_test.go`
  mounts the same `articleService` declared in `test/integration/http_test.go`,
  which only compiles because the two `Repository` types are one type.
- `TestStatusMapsWhatItPromisesTo` in `http/crudfiber/edge_test.go` and
  `http/crudgin/edge_test.go` — the table, arm by arm, from both sides.

## See also

[[D-015]] [[D-022]] [[D-033]] [[D-012]] [[FL-011]] [[FL-013]]
