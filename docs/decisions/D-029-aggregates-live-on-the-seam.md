# D-029 — Aggregates live on the decoratable seam, and are not exposed to the wire

**Status:** accepted
**Invariant:** A summary read carries every narrowing a row read carries, and no client can compose one from the outside.

## The decision

`Aggregate(ctx, opts...)` is a method on `crud.Core`, not a helper beside it. It
takes the same `[]crud.Option` every read takes, so the permanent scope, the
relation scopes, the caller's filter and the security gate's row filter all apply
to it unchanged. The function set is closed — `COUNT`, `SUM`, `AVG`, `MIN`,
`MAX` — and every field name is resolved against the model before anything is
rendered.

`security.gate` **overrides** it. That is the load-bearing part: the gate embeds
`crud.Core`, so an `Aggregate` it did not spell out would fall straight through
to the plain repository and summarise every row in the table.

It is deliberately **not** reachable from `query.Request`. An application exposes
the specific totals it wants; a client cannot ask for an arbitrary `GROUP BY`.

## Why

Before this, the surface had `Count` and nothing else, so the first time an
application needed "unread messages per chat" it dropped to raw SQL. That is not
an ergonomic problem. Raw SQL runs outside the repository, which means it runs
outside the layer that enforces tenancy — so the query that produces a number is
the query that leaks another tenant's rows, and it does it in the shape nobody
audits.

**Why it is on `Core` rather than on the concrete repository.** Because a
decorator has to be able to intercept it. Putting it beside the seam would mean
`gate.Aggregate` did not exist to be written, and the hole would be structural
rather than an oversight.

**Why `Inspect` refuses instead of being skipped.** A summary row is not an
entity; a policy that authorises row by row has nothing to look at. Silently
skipping the check would turn "this principal may only see rows it owns" into
"…and may count the ones it does not". Refusing the call says so out loud.

**Why the function set is closed.** The name is rendered into the statement. An
open set would be a second `crud.Raw` with none of `Raw`'s visibility — see
[[D-003]]. The five here are spelled identically by PostgreSQL, MySQL and SQLite;
a sixth would be a dialect question, and `crud` does not have one to ask.

**Why not on the wire.** A count is a disclosure. `GROUP BY` over a column a
client chooses is an oracle: binary-search a filter and the totals reconstruct
rows the scope was hiding. The allow-lists in `query.Config` could bound it, but
the safe default is that an application decides which totals it publishes rather
than which it forbids.

**Why the accessors.** Drivers disagree about the Go type of a summary — MySQL
returns `SUM` over a `DECIMAL` as text, and a `COUNT` may arrive as `int64` or
`[]byte`. `AggregateRow.Int` / `.Float` absorb that so a caller does not write a
type switch per driver.

## What it forbids

- Do not add a decorator that embeds `crud.Core` without deciding what
  `Aggregate` does. Inheriting it is a decision, and usually the wrong one.
- Do not widen `aggFuncs` with anything the three dialects spell differently.
- Do not render an aggregate over a name that was not resolved against the model.
- Do not expose `Aggregate` through `query.Request` without an allow-list at
  least as strict as `Filterable`, and a reason for the change written here.

## Where it lives

- `crud/aggregate.go:Aggregation` — one summary column.
- `crud/aggregate.go:AggregateSpec.Validate` — the resolution and the closed set.
- `crud/aggregate.go:AggregateSpec.Render` — the projection.
- `crud/aggregate.go:AggregateRow` — the result, with the driver-shape accessors.
- `crud/repo.go:Core` — the seam.
- `crud/sqlrepo/repository.go:Aggregate` — the statement, under `scoped(o)`.
- `crud/decorators/security/security.go:Aggregate` — the override.

## Proven by

- `TestAnAggregateHonoursTheSecurityGate` in `test/integration/aggregate_test.go`
  — the one that matters. Removing the gate's override makes it report 5 rows
  where the principal owns 3.
- `TestAnAggregateHonoursThePermanentScope` in
  `test/integration/aggregate_test.go`, with the unscoped repository in the same
  test as the control.
- `TestAggregatesGroupAndSummarise` in `test/integration/aggregate_test.go` — the
  five functions and the grouping column, on both engines.
- `TestAnAggregateHonoursTheFilter` in `test/integration/aggregate_test.go`.
- `TestAnAggregateRefusesWhatTheModelDoesNotHave` in
  `test/integration/aggregate_test.go` — unknown field, unknown group, unknown
  function, duplicate name, no aggregation.

## See also

[[D-003]] [[D-004]] [[D-013]] [[FL-007]]
