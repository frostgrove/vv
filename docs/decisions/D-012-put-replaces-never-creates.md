# D-012 — Where the database owns the key, PUT replaces and never creates

**Status:** accepted
**Invariant:** `PUT /:id` on a model whose primary key is `auto` must 404 for an id that names no existing row, unless `AllowClientID` was set.

## The decision

`DefaultService.Replace` checks `meta.PK.Auto && !allowClientID` and, when both
hold, does a `GetByID` first. A missing row fails there with `crud.ErrNotFound`,
which is a 404, and no write happens. When the key is not database-generated —
a UUID, a slug, a natural key — PUT creates as well as replaces, as it should.

`AllowClientID` is the single switch. It also governs `POST`, where
`port.Sanitize` otherwise clears the key on create.

## Why

**The id space stays the server's.** `POST` clears the client's key so a client
cannot choose its own id. Leaving `PUT` to upsert freely would make that
protection decorative: `PUT /users/999` is one request away from `POST` with a
chosen key.

**On PostgreSQL it also breaks the sequence.** An explicit insert into a `serial`
column does not advance the sequence. So the next `POST` collides on the primary
key — and keeps colliding, once per row the client planted, until somebody
repairs the sequence by hand. That is a production incident with a cause three
weeks upstream of the symptom.

**Why the extra read rather than an insert-guard in SQL.** `Save` is an upsert
with no `WHERE` clause ([[D-011]]), so the guard cannot live in the statement.
The read is one round trip on a route that is already writing.

**Why a client-owned key is different.** If the key is a UUID the client
generated, the server never owned that id space in the first place. There is no
sequence to desynchronise and nothing to protect. PUT-creates is then the
correct REST reading.

**Why `AllowClientID` rather than two options.** One question — "does the client
own the id space?" — with one answer per resource. Splitting it into
`AllowClientIDOnPost` and `AllowClientIDOnPut` would let the two get out of step,
and the out-of-step configuration is exactly the hole this closes.

## What it forbids

- Do not make `Replace` upsert unconditionally for an `auto` key. That is the
  bypass.
- Do not add a separate switch for PUT. One option, both routes.
- Do not take the id from the body. `Replace` binds the body, clears `generated`
  columns, then writes the path id over whatever arrived
  (`DefaultService.Replace`), in that order.
- Do not drop the `ClearGenerated` call on `Replace`. A client could otherwise
  forge a server-owned timestamp on the replace path even though `POST` clears
  it.

## Where it lives

- `port/service.go:DefaultService.Replace` — the existence check, the
  `ClearGenerated`, the `SetID` from the command's key, in that order and in one
  place for all three bindings since phase 5 ([[D-045]], [[FL-015]]).
- `Replace` in `http/crudfiber/handler.go`, `http/crudgin/handler.go` and
  `http/crudnet/handler.go` — the routes that build the command and do nothing
  else.
- `port/model.go:Sanitize` — the `POST` half.
- `port/model.go:ClearGenerated` — shared by both routes and every binding
  ([[D-045]]).
- `AllowClientID` in each binding's `options.go` — the switch.
- `crud/meta.go:Field.Auto` — set for an integer primary key unless `noauto` says
  otherwise; this is what the branch reads.

## Proven by

- `TestPutIsNotAWayAroundAllowClientID` in `http/crudfiber/write_edge_test.go`
  and `http/crudgin/write_edge_test.go` — the whole point of the decision,
  stated as a test, once per binding.
- `TestReplaceTakesTheIDFromThePathNotTheBody` in every binding's
  `handler_test.go`.
- `TestAllowClientIDLetsTheClientChooseTheKey` in every binding's
  `options_test.go` — the opt-in still works.
- `TestHTTPReplace`, `TestGinHTTPReplace` and `TestNetHTTPReplace` in
  `test/integration/http_test.go`, `http_gin_test.go` and `http_net_test.go` —
  against a live database, which is the only place the PostgreSQL sequence
  hazard is real.
- `TestCreateRefusesAClientChosenKeyAndGeneratedColumns` in every binding's
  `handler_test.go` — the `POST` half this exists to defend.

## See also

[[D-011]] [[D-022]] [[D-015]] [[D-045]]
