# D-006 — A preload is a batched second query; it cannot be paginated

**Status:** accepted
**Invariant:** A preload issues batched second queries, accepts only filtering,
ordering and refusal caps, and rejects every other nested query option before a
child statement can run.

## The decision

`crud.RunPreloads` folds the requested paths into a tree, then walks it one
level at a time. At each level it collects the distinct parent keys, chunks them
into `IN (…)` lists of at most 900, and runs one statement per chunk. The
children are indexed by owner key and written into their parents' fields.
Each path is canonicalised against the root schema before paths are folded.
Each `PreloadSpec` then resolves its options exactly once and in isolation;
folding combines the resulting values declaratively. Pagination and every
other unsupported option on a preload are errors, not hints.

## Why

**Why batched and not per-row.** The per-row shape is N+1. Twenty articles with
comments is twenty-one round trips, and the number grows with the page size, so
it is invisible in a test with three rows.

**Why one statement per level and not one big join.** Same reason as [[D-005]]:
joining a to-many multiplies the parent rows, and the parent page has already
been sized.

**Why it cannot be paginated.** A `LIMIT` on a batched load applies to the whole
result set, not per parent. `LIMIT 5` over a load for twenty articles returns
five comments in total — all belonging to whichever article the engine returned
first — and nineteen articles come back with an empty list. That is not "five
comments each"; it is a wrong answer with a 200. There is no portable per-group
limit that survives the batch, so the option is refused where it is set.

**Why 900.** PostgreSQL's parameter limit is 65535 and MySQL's packet limits bite
sooner; 900 is a round number well under both, and it is a constant rather than
a dialect method because nothing so far needs it to differ.

**Why the tree.** `Preload("Comments")` and `Preload("Comments.Author")` share
the comments query. Folding the *paths* is free. Folding their *narrowings* is
not: a request that asked for all comments and, separately, for approved ones
would get only the approved ones, with a 200 and no way to tell. So the wider
ask wins for the row set — an unnarrowed request for a path sets `whole` and
discards filters for that node. Orthogonal ordering and refusal caps survive.
Separate narrowed requests are intersected, their sort terms retain request
order, and their caps use the strictest positive value. `SortBy` and custom
filter replacement still replace earlier values *inside their own spec*; one
request cannot procedurally rewrite another request while a remote round trip
can only carry their resolved values. A provably true filter is an unnarrowed
ask. The predicate document folds trusted true identities such as an empty
`NotIn` recursively, but validates every Boolean branch before folding, so a
`Raw`, false-only node or other unsupported transport node cannot hide behind
the identity or depend on commutative operand order.

**Why the option set is closed at runtime.** `PreloadWhere` predates a distinct
`PreloadOption` type and changing its variadic parameter would break source
compatibility. The compatible boundary resolves an option list once, permits
only `Filter`, `Sort` and `PreloadRows`, and reflects over the complete
`Options` value so a future non-zero field is rejected by default. Projection,
nested preloads, relation scopes, aggregate state, cursor/paging controls,
datasource selection, sort/total flags, locks and `DISTINCT` all fail with a
`SchemaError`; none can disappear into a successful response. Adapters use
`BuildPreloadOptions` to share that contract without replaying option closures.

**Why a cap applies to every hop.** A cap is a refusal budget, not pagination.
Applying `PreloadRows` only to the terminal relation of `Comments.Author` would
still let `Comments` materialise without bound, and the wire has one `maxRows`
field whose documented meaning is every hop. `PreloadRows`, `PreloadCap`, the
query endpoint budget and the remote representation therefore converge on the
same safer rule; overlapping budgets take the minimum.

**Why children are re-collected after assignment.** A `[]T` relation copies each
row into the parent's slice. A nested preload has to write into that copy or it
writes into a temporary and the caller sees empty children
(`preloader.load`, the `stored` slice).

**Why a to-one preload copies per parent.** Two parents pointing at the same row
used to share one object, so a presenter rewriting a field on one rewrote it on
all its siblings — and whether that happened depended on whether the relation
field was spelled `*T` or `T`, because the value form has always copied
(`crud/preload.go:assignRelation`).

**Why an unsorted `has_one` gets a primary-key order.** A `has_one` promises at
most one row and only a unique index can keep that promise. When the schema does
not, the first row wins, and without an `ORDER BY` the winner can differ between
two runs of the same query.

## What it forbids

- Do not add `Limit` support to `PreloadWhere`, in any spelling. Per-parent
  limits need a window function and a different statement shape; if that is ever
  wanted it is a new feature with its own decision, not a relaxation of this one.
- Do not switch the preloader to a join to "save a round trip".
- Do not let folded narrowings constrain a path when any spec asks for the
  complete relation. Narrowed specs intentionally intersect; an unnarrowed or
  provably tautological spec clears filters. Changing either half needs a reason
  written down.
- Do not resolve duplicate specs into one mutable `Options`. That makes
  `SortBy`, custom setters and repeated caps behave differently locally and
  after serialisation.
- Do not key the tree by the caller's raw spelling. Equivalent aliases and case
  variants must canonicalise before the wider-ask rule is evaluated.
- Do not remove the deduplication of parent keys. The `IN` list is bounded by
  distinct keys, not by page size.
- Do not drop the `has_one` fallback sort.

## Where it lives

- `crud/preload.go:RunPreloads` — the entry point; takes the relation narrowings
  so the second statement is not read raw ([[D-007]]).
- `crud/preload.go:preloadBatch` — `= 900`.
- `crud/preload.go:buildPreloadTree` — path folding and the wider-ask-wins rule.
- `crud/preload.go:BuildPreloadOptions` — the shared fail-closed nested-option
  boundary used by adapters.
- `crud/preload.go:preloader.load` — key dedup, chunking, and re-collecting the
  stored children for the next level.
- `crud/preload.go:preloader.fetch` — the one statement, the many-to-many owner
  column, the `has_one` fallback sort.
- `crud/preload.go:assignRelation` — `T`, `*T`, `[]T`, `[]*T`, and the per-parent
  copy.
- `crud/preload.go:DefaultPreloadDepth` — `= 5`; paths arrive from HTTP clients
  and `a.b.a.b.a.b` should not turn one request into a dozen queries.
- `crud/sqlrepo/repository.go:repository.preload` — runs against the same executor,
  so a preload inside a transaction sees the transaction.
- `crud/sqlrepo/repository.go:repository.projection` — a projection keeps the
  columns a preload joins on, or the children have nothing to attach to.

## Proven by

- `TestPreloadCannotBePaginated` in `crud/preload_test.go` and in
  `crud/query/preload_test.go` — the refusal at both doors.
- `TestPreloadBatchesAndWires` in `crud/query/preload_test.go`.
- `TestPreloadChunksLargeKeySets` in `crud/preload_test.go`, and
  `TestPreloadBatchesSpanTheChunkBoundary` in
  `test/integration/matrix_test.go` — the second one is against a live database,
  which is the only place an off-by-one in the chunk boundary shows up.
- `TestABarePreloadWinsOverANarrowedOneForTheSamePath` in
  `crud/preload_edge_test.go` — including equivalent path spellings.
- `TestPreloadRefusesEveryUnsupportedGenericOptionBeforeRowsOrSQL`,
  `TestPreloadOptionsAreResolvedExactlyOnce` and
  `TestNestedPreloadRowsCapsTheIntermediateHop` in `crud/preload_test.go`.
- `TestFoldedPreloadsKeepDirectAndRemoteSemanticsIdentical` in
  `remote/roundtrip_test.go` — duplicate filters, sorts and caps survive a full
  `ToRequest` → `Compile` round trip unchanged.
- `TestRemoteNormalisesTrueIdentitiesWithoutHidingUnsupportedNodes` and
  `TestTrueIdentityInsideOnePreloadSpecKeepsItsRealNarrowingAcrossTheWire` in
  `remote/roundtrip_test.go` — recursive identities and fail-loud validation.
- `TestANestedPreloadFillsEveryParentsOwnCopy` and
  `TestAPreloadedToOneIsNotSharedBetweenParents` in
  `crud/preload_edge_test.go`.
- `TestNestedPreloadReachesIntoTheStoredChildren` in `crud/preload_test.go`.
- `TestAHasOneWithTwoMatchesPicksTheSameRowEveryTime` in
  `test/integration/relations_test.go`.
- `TestPreloadMatchesKeysOfDifferentWidths` in `crud/preload_test.go`; [[D-025]]
  records the fail-fast rule for non-comparable keys.
- `TestPreloadsSurvivePaging` in `test/integration/matrix_test.go`.

## See also

[[D-005]] [[D-007]] [[D-025]] [[D-024]]
