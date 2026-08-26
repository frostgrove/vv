# D-060 — A request may not choose how much comes back

**Status:** accepted
**Invariant:** `query.Config` bounds what a request may *name*, and every one of those bounds is open by default. Every bound on *how much comes back* is closed by default: `AllowUnpaged` must be declared, and the list, sort and bulk-id caps have non-zero defaults.

## The decision

The zero `query.Config` allows anything the model maps: any field filtered,
sorted, selected or preloaded, up to `MaxDepth` 6, `MaxConditions` 64 and
`MaxPreloads` 16. That stays.

`Request.Unpaged` does not. A request carrying `unpaged` is refused with a
`query.Error` at path `unpaged` unless the endpoint declared
`query.Config{AllowUnpaged: true}`.

## The three caps that measure volume rather than names

Added with the same reasoning and the same shape, after an audit found the class
was not exhausted. Each bounds something the condition budget cannot see:

| Cap | Default | What it stops |
|---|---|---|
| `query.Config.MaxInValues` | 1024 | An `in` or `notIn` list is charged as **one** condition however long it is, and every element becomes a bound parameter. PostgreSQL refuses a statement past 65535 of them for the whole statement, so the honest 400 arrived from the driver, as a 500, after the statement was built. |
| `query.Config.MaxSort` | 16 | A sort had no budget at all. A term that hops a relation renders as a correlated scalar subquery ([[D-005]]), so a long list is not linear in the way a projection is. A repeated canonical path is now dropped rather than rendered twice: the second `ORDER BY` over a column already sorted decides nothing and still costs whatever the term costs. |
| `port.Rules.BulkCap` | 1024 | `MaxBulk` meant *unlimited* at zero, which is what an unset field is — so a bulk delete's cardinality was bounded only by the request body, with the same parameter-limit ending. `BulkCap()` is a method rather than a defaulted field so the four transports cannot disagree about what unset means, which is how they came to agree it meant no cap. |

The numbers are generous on purpose. These are the ceilings that stop a statement
no engine will accept, not page sizes — an endpoint that wants a page size says
so with `sqlrepo.MaxLimit`.

## Why

**Because every other bound is a bound on names, and this one is a bound on
volume.** `Filterable` and its siblings decide whether a client may *mention*
`Comments.Author.Email`. Getting that wrong exposes a column. `unpaged` decides
how many rows come back, and getting it wrong is a full table scan and a full
table in memory, chosen by whoever sent the request. The two are not the same
kind of default and should not have had the same one.

**Because the ceiling that was supposed to catch it is unset by default.**
`crud.Options.Resolved` clamps unpaged down to the repository's `MaxLimit`, and
its doc comment says exactly why: "a repository that declares a maximum page size
must not be talked out of it by one flag arriving from the wire." That works —
`sqlrepo.MaxLimit` is the lever, and it still is. But `MaxLimit(0)` is the
default and means no cap, so with both defaults the defence was never armed. Two
open defaults that only protect in combination protect nothing.

**Because a default page size does not help.** `DefaultLimit` is 20 and applies
when the request names no limit. `unpaged` is the request naming that it wants
no limit, so the default never runs.

**Because the dangerous direction is the one that gets named.** This is the shape
`security.Policy` already uses for `AllowUnscopedDeleteAll` and
`AllowUnscopedUpdateAll` — a bulk operation that is occasionally correct and
usually a mistake is spelled out at the declaration rather than inferred. An
endpoint that genuinely serves an export says so in one field.

## What this costs, and why it is still right

**`remote.GetAll` needs the far endpoint to declare it.** There is no "every row"
route over HTTP or gRPC: the client emulates `GetAll` with the unpaged flag. So a
resource that never agreed to serve whole tables now refuses a remote `GetAll`,
with a message that names the fix. That is the honest answer rather than a
regression — an endpoint's bounds should not depend on whether the caller is in
this process — but it is a real cost and it is written down here rather than
discovered.

The in-process `crud.Repo.GetAll` is untouched. It has always ignored `MaxLimit`
("GetAll's contract is every matching row, and MaxLimit is a cap on a page") and
still does; nothing about it goes through `query.Config`.

## What is deliberately still open, and why

Recorded so the next reader knows these were decided rather than missed:

- **`Filterable`, `Sortable`, `Selectable`, `Preloadable`, `Searchable` empty
  means everything.** Tightening them per endpoint is the documented job, and a
  closed default would make the first `Define` of every model a wall of
  allow-lists before anything worked. The exposure is bounded by what the model
  maps, which the consumer wrote.
- **`DefaultSearchFields` empty means every text column of the root.** Expensive,
  and the fix is a `Searchable` list rather than a different default.
- **`security.Policy`'s nil fields mean "allow".** A `Policy` that names a
  `Scope` and no `Authorize` permits every verb on the rows the scope leaves
  visible, and that is the right reading — a scope is not an authorisation. The
  zero `Policy` is not "no gate": it still refuses an unscoped `DeleteAll` and an
  unscoped `UpdateAll`. A refusal for a policy that "decides nothing" was written
  and then removed, because the zero policy does decide those two things and the
  tests that pin them are right.

## What it forbids

- Do not make `unpaged` allowed by default again without also giving `MaxLimit` a
  non-zero default — and note that a non-zero `MaxLimit` silently truncates a
  remote `GetAll`, which is worse than refusing it.
- Do not close the allow-list defaults one at a time. They are one posture, and
  half of it is the worst version.

## Where it lives

- `crud/query/compile.go` — `Config.AllowUnpaged` and the refusal.
- `crud/options.go:Resolved` — the clamp this decision says was never armed.
- `crud/sqlrepo/blueprint.go` — `DefaultLimit`, `MaxLimit`.
- `crud/decorators/security/security.go:Gate` — the doc comment that says what
  the zero policy does and does not do.

## Proven by

- `TestUnpagedIsRefusedUnlessTheEndpointServesIt` in
  `crud/query/compile_test.go` — the three configs that refuse, the control that
  a declaring endpoint really gets unpaged options, and the assertion that the
  refusal is a `query.Error` naming the parameter rather than a 500.
- `TestUnpagedIsRefusedOnAnEndpointThatDidNotDeclareIt` in all three of
  `crud/http/crudnet/handler_test.go`, `crud/http/crudgin/handler_test.go` and
  `crud/http/crudfiber/handler_test.go`, each beside the
  `TestListHonoursUnpagedAndSkipTotal` it controls.
- `TestAClientChosenListAndSortAreBounded` in `crud/query/compile_test.go` — the
  two query caps and the sort dedup, with the control that makes the point: the
  same 40-value list compiles clean under `MaxConditions: 1`, so the condition
  budget really never saw it.
- `TestAnUnconfiguredBulkDeleteIsStillCapped` in `options_test.go` in all three
  HTTP bindings — the default, not the option, with an under-the-cap control.
- `TestEveryMethodMakesTheRoundTrip/GetAll` in `remote/roundtrip_test.go` and
  `crud/rpc/crudgrpc/client_test.go` — both endpoints declare `AllowUnpaged`,
  which is the cost above made visible in the fixture rather than hidden.

- `TestAClientChosenListAndSortAreBounded/the_flat-term_spelling_is_bounded_too`
  and `/the_search_field_list_is_bounded` in `crud/query/compile_test.go` — the
  cap reached two of the four spellings of a value list. The flat-term `in` is
  reachable from `POST /query` through `Term.Values`, and `c.count` charges the
  whole term as **one** condition however long it is, so the parameter-limit 500
  this decision was written to end was still reachable on a stock config.
- `TestACursorCannotCompareAColumnTheEndpointHidesFromFiltering` in the same file
  — a cursor is a filter. Its payload is the sort tuple and the repository turns
  it into an inequality over those columns ([[D-028]]), so a column that was
  `Sortable` and not `Filterable` became comparable with `>` and `<` by forging a
  token, while the same comparison written as a filter was refused by name. That
  is a binary search over a column the deployment kept back.
- `TestAnAllowListEntryThatNamesNothingIsRefusedAtDeclaration`, same file — the
  allow-lists were never resolved against the model, so a misspelled entry was
  inert: the field stayed closed and every request naming it was refused as the
  *client's* mistake, forever. `Config.Check` moves that to declaration
  ([[D-021]]); the seven controls keep it from refusing legal shapes.

## See also

[[D-021]] [[D-013]] [[D-053]] [[D-063]] [[UC-002]] [[UC-015]] [[FL-002]]
