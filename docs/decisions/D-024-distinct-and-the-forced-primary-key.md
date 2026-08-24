# D-024 — `DISTINCT` cannot deduplicate while the projection forces the primary key

**Status:** open
**Invariant:** A `DISTINCT` query must produce a statement both engines accept, or a refusal the client can read — never a 500 and never a statement whose `DISTINCT` is a no-op the caller cannot see.

## The decision

`DISTINCT`, `select` and `sort` all arrive from the wire and can arrive together.
The rules as they stand:

- **A `DISTINCT` projection carries no primary key.** Every other projection adds
  it, because a preload attaches by key and the pagination tiebreaker breaks ties
  by it. Under `DISTINCT` nothing is added — not the key, not a preload's join
  column — because the key is unique by definition and carrying it makes
  `DISTINCT` unable to remove a single row. `?distinct=1&select=title` used to
  return one row per article rather than one per title.
- **A preload under a `DISTINCT` projection is refused**, because there is no key
  to attach children to.
- **A sort the projection cannot cover is refused when the *caller* asked for
  it**, and **dropped when it is the repository's default**. Both engines reject
  a `SELECT DISTINCT` ordered by a column outside the select list (42P10 /
  `ER_FIELD_IN_ORDER_NOT_SELECT`).
- **A sort through a relation under `DISTINCT` is refused**, because it renders
  as a scalar subquery that can never appear in a select list.
- **A paged `DISTINCT` does not append the primary-key tiebreaker.**
- **`Count` with `DISTINCT` uses a derived table**, because `count(*)` would
  count the rows the `SELECT DISTINCT` is about to collapse and `Get` would then
  hand the client a total — and a page count — for pages that do not exist.

## Why

The rejected alternative is the one that looks helpful: widen the projection to
cover the sort. It produces a statement that runs and an answer nobody asked
for. The extra column is exactly what tells the duplicate rows apart, so
`DISTINCT` stops removing them, and the response carries a column the client did
not select. Refusing says the two requests genuinely cannot both be honoured.

The asymmetry between a caller's sort and the repository's default sort is
deliberate: the caller can avoid a refusal by changing their request, and cannot
avoid one caused by a `DefaultSort` they never asked for.

## What is unresolved

**A bare `?distinct=1` with no `select` is a no-op that costs a keyword.**
`projection` returns `nil` for an empty `Fields`, and `find` then substitutes
`r.meta.Fields` — every column, primary key included. Every row differs by
primary key, so `SELECT DISTINCT` removes nothing. The client gets a correct
answer, a slightly more expensive plan (`DISTINCT` usually forces a sort or a
hash), and no indication that the flag did nothing.

The same holds for `Count` with `DISTINCT` and no projection: the derived table
selects every column, so the count equals `count(*)`.

Three ways out, none obviously right:

1. **Refuse `Distinct()` without a projection.** Honest, and it breaks
   `?distinct=1` for every caller who is using it harmlessly today.
2. **Make it a documented no-op.** Cheapest. Leaves a flag in the API that
   sometimes does nothing.
3. **Drop the primary key from the full projection under `DISTINCT`.** Then a
   bare `?distinct=1` deduplicates on the non-key columns, which is probably what
   was meant — but it silently stops returning the id, which breaks every client
   that addresses rows, and it would have to refuse preloads and the tiebreaker
   for a request that never mentioned `select`.

**Second unresolved edge: `DISTINCT` and pagination are not really compatible.**
A paged `DISTINCT` has no stable tiebreaker (adding one would put a unique
column in the select list, defeating the keyword), so two requests for page 2 of
the same `DISTINCT` query can legitimately return different rows. This is
currently accepted silently. `Get` with `DISTINCT` is a shape the API allows and
cannot make stable.

Nothing here is a bug in the current code — the statement is valid and the
refusals are honest. It is a design question with no answer yet.

## What it forbids

While this is open, do not:

- Widen a `DISTINCT` projection to cover a sort. That is the bug the current
  code fixed.
- Add the primary key back to a `DISTINCT` projection.
- Add a preload column to a `DISTINCT` projection.
- Append the pagination tiebreaker to a paged `DISTINCT`.
- Turn any of the current refusals into a 500 by letting the statement reach the
  engine.

## Where it lives

- `repo/basic/repository.go:repository.projection` — the "nothing is added under
  DISTINCT" rule, with the reason.
- `repo/basic/repository.go:repository.find` — spells the column list out when
  `Distinct` arrives without a `select`; this is where the no-op is created.
- `repo/basic/repository.go:repository.distinctSort` — the refuse/drop asymmetry
  and the relation-sort refusal.
- `repo/basic/repository.go:hasPK` — what a projection needs to still identify
  its rows; drives the preload refusal.
- `repo/basic/repository.go:repository.Count` — the derived table.
- `crud/options.go:Distinct` — the option.
- `query/compile.go:Request.Compile` — `{"distinct": true}` from the wire.

## Proven by

- `TestDistinctRefusesASortItCannotProject` in
  `repo/basic/paging_edge_test.go` — including that no statement reaches the
  database.
- `TestDistinctDropsADefaultSortItCannotProject` in
  `repo/basic/paging_edge_test.go` — the other half of the asymmetry.
- `TestDistinctRefusesASortThroughARelation` in
  `repo/basic/paging_edge_test.go`.
- `TestAPagedDistinctDoesNotAppendThePrimaryKey` in
  `repo/basic/paging_edge_test.go`.
- `TestDistinctProjectsOnlyWhatWasSelected` and
  `TestDistinctRefusesAPreloadItCannotAttach` in
  `repo/basic/repository_test.go`.
- `TestDistinctWithoutAProjectionStillNamesItsColumns` in
  `repo/basic/repository_test.go` — this is the test that pins the *current*
  behaviour of the unresolved case. Whichever way the question is settled, this
  test changes.
- `TestDistinctActuallyRemovesDuplicateRows` and
  `TestDistinctRefusesASortOutsideItsProjectionOnBothEngines` in
  `test/integration/dialect_edge_test.go` — against both engines, which is where
  the two SQLSTATEs are confirmed.

## See also

[[D-005]] [[D-006]] [[D-019]]
