# FL-006 — A preload becomes batched second queries

**Entry point:** `crud/preload.go:RunPreloads`
**Implements:** [[UC-006]] [[UC-004]] · **Governed by:** [[D-006]] [[D-007]] [[D-025]]

`?preload=comments.author` is one extra statement per relation per level, never
one per row. The interesting part is not the SQL — it is where the loaded
children physically end up, because a nested preload has to keep filling *those*
structs.

## The path

1. **`repository.find` → `repository.preload`** — `crud/sqlrepo/repository.go:242`,
   `crud/sqlrepo/repository.go:364`
   Runs on `r.exec(ctx)`, so a preload inside a transaction sees the
   transaction, and with `r.relScopes(o)` — the blueprint's permanent narrowings
   merged with whatever this query carries.

2. **`RunPreloads` → `buildPreloadTree`** — `crud/preload.go`
   Every path is validated and canonicalised with `Meta.ValidateRelationPath`,
   then folded into a tree, so `Comments` and `comments.author` share one query
   for the comments. Depth is capped against the blueprint's `PreloadDepth`
   (default 5). Every spec's option list is resolved and validated once before
   `addressableRows` inspects the result slice. **The `whole` rule:** if any
   spec asks for a path unnarrowed, filters on that same canonical path are
   discarded. A request for all rows and for a subset would otherwise receive
   only the subset, with a 200 and no way to tell. `addressableRows` turns
   `[]M` or `[]*M` into a pointer per element; nil pointers are skipped.

3. **`preloader.level`** — `crud/preload.go:159`
   Per node: resolve the relation on the current model, extend the canonical
   path with `joinPath`, `load` it, then recurse into the children that `load`
   returned — with the **target's** `Meta`, not the parent's.

4. **`preloader.load`** — `crud/preload.go`
   - It receives only the already-resolved closed preload shape. A refusal cap
     becomes `cap+1` in SQL, saturated at `math.MaxInt`; observing the extra row
     returns an error and never publishes a partial relation.
   - Distinct parent keys are collected by reading `local` at its byte offset,
     canonicalising the value once for both map identity and driver binding,
     and deduplicating that immutable snapshot. A `NULL` foreign key contributes
     no key.
   - No keys at all → `assignEmpty` and return; every parent still gets a zeroed
     or empty relation field rather than whatever was there.
   - Keys are chunked at **900** (`preloadBatch`, `preload.go:17`) by `slices`
     (`preload.go:439`), and each chunk is one `fetch`. Results from all chunks
     land in one `index` map, so chunking is invisible above this line.

5. **`preloader.fetch`** — `crud/preload.go:259`
   The narrowing for this hop is `p.scopes.At(path, target)` (`preload.go:267`),
   ANDed with the caller's per-preload predicate. It is applied here rather than
   left to the caller because a preload is a second statement against a second
   table: the parent query's `WHERE` does nothing for it.
   ```sql
   -- belongs_to / has_one / has_many
   SELECT <target cols> FROM "comments" WHERE "article_id" IN ($1,$2,…) AND <scope>
   -- many_to_many: the owner key rides along as column 0
   SELECT rxj."article_id", rxt."id", rxt."name"
     FROM "tags" AS rxt JOIN "article_tags" AS rxj ON rxj."tag_id" = rxt."id"
    WHERE rxj."article_id" IN ($1,…) AND <scope>
   ```
   `RelationScopes(p.scopes.under(path))` (`crud/scope.go:131`) re-roots the
   remaining path declarations, because this statement's own `FROM` is already
   that far down — so a preload whose own filter hops another relation still
   carries the right narrowing.
   A `has_one` with no explicit sort is given `ORDER BY <target pk>`
   (`preload.go:300`): only a unique index can keep a has-one's promise, and when
   the schema does not, the winner must at least be the same row twice.
   Scanning: `reflect.New(target.Type)` per row, destinations from
   `target.Pointers`. For many-to-many the owner key is scanned into a
   `reflect.New(local.Type)` prepended to the destinations — it has to be read as
   the **parent's** type, because the index is looked up with the parent's own
   value and `int32(1)` is not `int64(1)`.

6. **`assignRelation`** — `crud/preload.go:359`
   The step that earns this flow its own document. It writes the children into
   the parent's relation field and **returns pointers to where they now live**:
   | field shape | what happens | what is returned |
   |---|---|---|
   | `[]*T` | children appended as-is | the same pointers |
   | `[]T` | each child copied into the parent's slice | `dst.Index(i).Addr()` — into the slice |
   | `*T` | a fresh `reflect.New` copy per parent | the new pointer |
   | `T` | copied into the field | `dst.Addr()` |
   Two things depend on this. A nested preload continues from the returned
   values, so with `[]T` it must write into the copy or it writes into a
   temporary nobody reads. And a `*T` to-one gets **its own copy per parent**:
   two parents pointing at the same row used to share one object, so a presenter
   that rewrote a field on one rewrote it on all its siblings — and which
   spelling of the relation field was used decided whether that happened.

7. **`canonicalPreloadKey`** — `crud/preload.go`
   Normalises both ends of the relation onto one immutable map key and a matching
   driver value: integers use checked canonical forms, string kinds flatten,
   byte slices are copied, optional/pointer/valuer wrappers are unwrapped under
   a fail-closed allow-list. Unsupported mutable or non-comparable values and
   `NaN` are errors. Without one snapshot for both uses, mutation or a key-shape
   collision can file children under the wrong parent.

## Where the decisions bite

- **One statement per relation per level.** `level` recurses over the tree, not
  over rows. Anything that turns `load` into a per-parent call reintroduces N+1.
- **The narrowing travels with the hop.** `p.scopes.At(path, target)` in `fetch`
  is the only thing standing between `?preload=comments` and every tenant's
  comments. See [[FL-007]] for where those scopes come from at request time.
- **Nested options are a closed contract.** Only `Where`, `OrderBy`/`SortBy`
  and `PreloadRows` survive the boundary. Every other non-zero `Options` field
  is refused while the whole tree is prepared, before rows are inspected or a
  child statement runs.
- **A refusal cap covers every hop.** Root `PreloadRows`, nested
  `PreloadRows`, `PreloadCap` and the public query budget all min-merge into the
  same per-hop ceiling.
- **The projection must carry the join column.** `repository.projection`
  (`crud/sqlrepo/repository.go:292`) adds it; a `DISTINCT` projection cannot, so
  `find` refuses the combination (`repository.go:206`).
- **Equivalent paths fold before execution.** Each path is canonicalised
  against the root schema, so `comments` and `Comments` cannot become two
  queries that bypass wider-ask-wins.
- **Specs fold declaratively.** Each option list resolves once in isolation;
  narrowed specs intersect, an unnarrowed or provably tautological spec clears
  filters, sorts append in request order and refusal caps take the minimum.
  Trusted true identities are folded recursively at the predicate-document
  boundary only after every branch is validated. The result is identical
  before and after a remote round trip.

## Failure modes

| What goes wrong | Where it is caught | What the caller sees |
|---|---|---|
| preload path deeper than `PreloadDepth` | `buildPreloadTree` (`preload.go:77`) | 400 (`SchemaError`) |
| empty segment in the path (`a..b`) | `buildPreloadTree` (`preload.go:84`) | 400 |
| unknown relation name | `Meta.ValidateRelationPath` during tree preparation | 400 (`UnknownFieldError`), even for an empty root |
| relation whose fk/ref does not resolve | `Meta.ValidateRelationPath` during tree preparation | 400 (`SchemaError`) |
| any nested option except filter, sort or `PreloadRows` | preload-tree preparation / `BuildPreloadOptions` | 400 (`SchemaError`), no child SQL |
| negative nested, path or root preload cap | preload-tree/repository/remote preparation | 400 (`SchemaError`/`OptionError`) |
| preload over a `DISTINCT` projection | `repository.find` (`repository.go:206`) | 400 (`SchemaError`) |
| unsafe or ambiguous parent/child relation key | relation-path validation / canonical key conversion | 400 (`SchemaError`), never a shared bucket |
| more preloads than `MaxPreloads` | `Request.Compile` (`crud/query/compile.go:185`) | 400 |

## Files

| File | Role |
|---|---|
| `crud/preload.go` | the whole mechanism: tree, batching, fetch, assignment, key normalisation |
| `crud/scope.go` | `At` for this hop, `under` for the hops below it |
| `crud/relation.go` | `Resolve`, join-table columns, `Relation.fieldValue` |
| `crud/access.go` | `Pointers` — scan destinations by offset |
| `crud/sqlrepo/repository.go` | `preload`, and `projection` keeping the join column |
| `crud/query/compile.go` | `PreloadWhere` per relation, allow-list, `MaxPreloads` |
| `crud/sqlrepo/blueprint.go` | `PreloadDepth`, `RelationScope` |

## Tests that walk this flow

- `TestPreloadBelongsToIsOneBatchedQuery` — `crud/preload_test.go` — one statement, keys deduplicated.
- `TestPreloadHasManyDistributesChildren` — `crud/preload_test.go` — the index.
- `TestPreloadManyToManyCarriesTheOwnerKey` — `crud/preload_test.go` — column 0.
- `TestPreloadManyToManyReadsTheOwnerKeyAsTheOwnersType` — `crud/preload_test.go`.
- `TestPreloadMatchesKeysOfDifferentWidths` — `crud/preload_test.go` — `mapKey`.
- `TestNestedPreloadReachesIntoTheStoredChildren` — `crud/preload_test.go` — the `assignRelation` return value.
- `TestAPreloadedToOneIsNotSharedBetweenParents` — `crud/preload_edge_test.go` — the per-parent copy.
- `TestANestedPreloadFillsEveryParentsOwnCopy` — `crud/preload_edge_test.go`.
- `TestPreloadChunksLargeKeySets` — `crud/preload_test.go` — the 900 boundary.
- `TestPreloadBatchesSpanTheChunkBoundary` — `test/integration/matrix_test.go` — against real engines.
- `TestPreloadCannotBePaginated` — `crud/preload_test.go` and `crud/query/preload_test.go`.
- `TestPreloadDepthIsCapped` — `crud/preload_test.go`.
- `TestABarePreloadWinsOverANarrowedOneForTheSamePath` — `crud/preload_edge_test.go`.
- `TestTwoNarrowedPreloadsOfOnePathStillBothApply` — `crud/preload_edge_test.go`.
- `TestPreloadRefusesEveryUnsupportedGenericOptionBeforeRowsOrSQL` and
  `TestNestedPreloadRowsCapsTheIntermediateHop` — `crud/preload_test.go`.
- `TestFoldedPreloadsKeepDirectAndRemoteSemanticsIdentical` and
  `TestRemotePreloadOptionsAreResolvedExactlyOnce` — `remote/roundtrip_test.go`.
- `TestRemoteNormalisesTrueIdentitiesWithoutHidingUnsupportedNodes` and
  `TestTrueIdentityInsideOnePreloadSpecKeepsItsRealNarrowingAcrossTheWire` —
  `remote/roundtrip_test.go`.
- `TestProjectionKeepsPreloadKeys` — `crud/query/preload_test.go`.
- `TestAPreloadOfTheRepositorysOwnModelCarriesItsScope` — `crud/sqlrepo/relscope_test.go`.
- `TestANestedPreloadOfTheSameModelCarriesTheScopeAtEveryLevel` — `crud/sqlrepo/relscope_test.go`.
- `TestAHasOneWithTwoMatchesPicksTheSameRowEveryTime` — `test/integration/relations_test.go`.
- `TestPreloadsSurvivePaging` — `test/integration/matrix_test.go`.

## See also

[[FL-001]] [[FL-005]] [[FL-007]] [[FL-004]]
