# D-005 — A relation filter is a correlated `EXISTS`, not a join

**Status:** accepted
**Invariant:** A filter through a relation must not change the number of rows the statement's own `FROM` produces: one parent row in, at most one parent row out.

## The decision

`writer.leaf` resolves a dotted field path. Every hop opens
`EXISTS (SELECT 1 FROM <target> AS rxN WHERE rxN.<fk> = <parent>.<pk> AND …)`
and nests the next hop inside it. A many-to-many hop joins the link table
*inside* the `EXISTS`. Nothing is ever joined onto the outer `FROM`. A nested
sort is the same shape one grammar down: a scalar subquery with `LIMIT 1`
(`writer.sortExpr`), and sorting through a to-many relation is refused outright
rather than picking a row.

## Why

A join against a to-many relation multiplies rows, and the damage is in the two
places nobody looks at:

- `LIMIT 20` over a joined result returns fewer than twenty distinct articles.
  An article with two matching tags takes two of the twenty slots.
- `COUNT(*)` returns a number that does not correspond to anything. The client
  gets `total: 143` and 47 pages for 47 articles.

Deduplicating afterwards does not fix it. `SELECT DISTINCT` runs after `LIMIT`
is decided, and moving the `LIMIT` into a subquery turns one statement into
three and still does not make `COUNT` right. `EXISTS` is a semi-join: the
optimiser short-circuits on the first match, the cardinality of the outer query
is untouched, and `COUNT` and `LIMIT` mean what they say.

The correlation column is spelled with the parent's table name or alias
(`scope.correlate`), not bare, because a bare name inside the subquery would
resolve against the subquery's own `FROM` — silently, and to the wrong column,
whenever the two tables share a column name.

Each hop gets a fresh alias (`writer.nextAlias`, `rx1`, `rx2`, …), so a path
that walks through the same table twice does not shadow itself.

## What it forbids

- Do not "optimise" a to-one hop into a `JOIN`. A `belongs_to` does not multiply
  rows today, but the shape is the same code path as a `has_many`, and the next
  person to add a hop kind inherits whichever shape is there.
- Do not add a `DISTINCT` to compensate for a join. See [[D-024]]: `DISTINCT`
  and the primary key in the projection cannot both hold.
- Do not make a to-many sort pick "the first" or "the max". A collection has no
  single value; guessing one produces an order that changes between runs.
- Do not drop the alias qualification on the correlation column.

## Where it lives

- `crud/predicate.go:writer.leaf` — the `EXISTS` per hop, including the
  many-to-many link-table join inside it.
- `crud/predicate.go:scope.correlate` — qualifies the parent column so a deeper
  scope cannot capture it.
- `crud/predicate.go:writer.nextAlias` — one alias per hop.
- `crud/predicate.go:writer.sortExpr` — the scalar subquery for a nested sort,
  and the refusal for a to-many one.
- `crud/relation.go` — `Relation.Resolve` supplies the local and remote columns
  each hop correlates on.

## Proven by

- `TestToManyFilterDoesNotDuplicateOrInflateCount` in
  `test/integration/relations_test.go` — the failure this exists to prevent,
  against a live database: the row count and the `COUNT` both stay honest.
- `TestRelationHopsRenderAsCorrelatedExists` in `crud/predicate_test.go` — the
  rendered SQL.
- `TestAPathThroughTwoToManyHopsNestsRatherThanJoins` in
  `crud/schema_edge_test.go`.
- `TestEveryHopGetsItsOwnAlias` in `crud/predicate_test.go`.
- `TestNestedSortIsAScalarSubquery` in `crud/predicate_test.go`.
- `TestSortThroughAToManyRelationIsRefused` in `crud/predicate_test.go`.
- `TestNestedFiltersAgainstDatabases` and `TestNestedSortAgainstDatabases` in
  `test/integration/matrix_test.go`.

## See also

[[D-006]] [[D-007]] [[D-024]]
