# FL-005 — A relation filter becomes a correlated EXISTS

**Entry point:** `crud/predicate.go:writer.leaf`
**Implements:** [[UC-006]] [[UC-004]] · **Governed by:** [[D-005]] [[D-007]] [[D-003]]

`{"comments.author.name": {"contains": "an"}}` reaches SQL as nested `EXISTS`
subqueries, never as a JOIN. That choice is the reason `COUNT` and `LIMIT` stay
honest on a to-many filter.

## The path

1. **The predicate arrives already canonical.** `crud/query/filter.go:condition`
   resolved `comments.author.name` to `Comments.Author.Name` and built
   `crud.Contains("Comments.Author.Name", "an")` — a `likeNode`
   (`crud/predicate.go:243`). Every node's `render` calls `w.leaf(field, emit)`.

2. **`writer.leaf`** — `crud/predicate.go:81`
   `cur.meta.WalkPath(path)` (`crud/relation.go:344`) returns the hops, the
   terminal field and the canonical spelling. A path that stops on a relation is
   refused here (`predicate.go:90`) — a relation is not a comparable value.
   With zero hops it emits `cur.qualify(d, f.Column)` and returns: a top-level
   column stays unqualified, because the root scope has no alias
   (`predicate.go:25`).

3. **The hop loop** — `predicate.go:102-121`
   Per hop:
   - `w.nextAlias()` (`predicate.go:64`) → `rx1`, `rx2`, … The counter is
     per-writer, so aliases never collide across sibling clauses of the same
     statement.
   - open `EXISTS (SELECT 1 FROM "<target>" AS rxN`
   - correlate: `cur.correlate(d, hop.Local.Column)` (`predicate.go:34`). From
     the **root** scope that renders table-qualified — `"articles"."id"` — because
     a bare name inside the subquery would be ambiguous. From a deeper scope it
     renders `rx1."id"`.
   - `WHERE rxN."<remote>" = <correlated local>` then ` AND `
   - step into the new scope and extend the path:
     `w.cur, w.path = cur, joinPath(w.path, hop.Rel.Name)`
   - `w.hopScope()` — the relation narrowing for where we now are; another
     ` AND ` if it rendered anything.
   `w.cur` and `w.path` are saved and restored with a `defer`
   (`predicate.go:99`), so the next clause in the same `AND` starts from the
   root again.

4. **The many-to-many hop** — `predicate.go:108-111`
   Two aliases, in this order: the target gets `rx1`, the join table `rx2`.
   ```sql
   EXISTS (SELECT 1 FROM "tags" AS rx1
           JOIN "article_tags" AS rx2 ON rx2."tag_id" = rx1."id"
           WHERE rx2."article_id" = "articles"."id" AND …)
   ```
   The join is inside the `EXISTS`, so it still cannot multiply the outer rows.

5. **`writer.hopScope`** — `crud/predicate.go:130`
   `w.rel.At(w.path, w.cur.meta)` (`crud/scope.go:68`) — a path declaration wins
   over a model one. It renders with `w.rel` temporarily set to `nil`: a
   narrowing is the repository's own declaration, not caller input, so it is not
   narrowed further — and that is also what makes termination certain when a
   scope on a model walks back into that same model.

6. **`emit` and the closing parens** — `predicate.go:122-123`
   The comparison is written against the innermost scope, then
   `strings.Repeat(")", len(hops))` closes one paren per hop. This is why the
   parens are correct however deep the path goes and however many binds the leaf
   contributed — count of hops, not count of anything else.

   A worked result, from `crud/sqlrepo/relscope_test.go`:
   ```sql
   SELECT … FROM "nodes"
   WHERE ("deleted" = $1
          AND EXISTS (SELECT 1 FROM "nodes" AS rx1
                      WHERE rx1."parent_id" = "nodes"."id"
                        AND rx1."deleted" = $2
                        AND rx1."name" = $3))
   ```

7. **Nested sort: a scalar subquery** — `writer.sortExpr`, `crud/predicate.go:532`
   `Order.render` (`predicate.go:577`) splits the field on `.` and calls
   `sortExpr`. One segment → a plain column. More → a correlated scalar
   subquery, the only shape `ORDER BY` accepts:
   ```sql
   ORDER BY (SELECT rx1."name" FROM "employees" AS rx1
             WHERE rx1."id" = "employees"."manager_id"
               AND rx1."retired" = $2
             LIMIT 1) ASC, "id" ASC
   ```
   `LIMIT 1` is unconditional, so even a schema that admits several matches
   yields one value.

8. **To-many sorts are refused** — `predicate.go:549`
   `rel.Kind.ToMany()` → `SchemaError`, "cannot sort through a has_many
   relation". There is no single value to sort by, and picking one quietly is
   worse than a 400 the client can read.

## Where the decisions bite

- **`EXISTS`, not `JOIN`.** A `has_many` join returns one row per child, so the
  page would carry duplicates and `COUNT(*)` would report the number of children
  rather than of parents. `TestToManyFilterDoesNotDuplicateOrInflateCount`
  exists to keep anybody from "optimising" this into a join.
- **The relation scope belongs inside every hop.** The subquery has its own
  `FROM` and inherits nothing from the outer `WHERE`. Without `hopScope` a filter
  through a relation is an oracle: a client cannot read a hidden row but can ask
  whether one exists and read the answer off the parent page.
- **A narrowing is never narrowed.** `hopScope` nils `w.rel` while rendering.
  Removing that both changes the meaning and reintroduces the possibility of
  unbounded recursion on a self-relation.
- **`WalkPath` is the single source of truth for paths.** The SQL writer, the
  preloader and the wire DSL all go through it. Two resolvers would drift, and
  the drift would be an allow-list that guards one route and not the other.

## Traps

- **The alias counter is shared with the sort.** `nextAlias` is per-writer, and
  `OrderBy` renders on the same writer as `Where`, so a statement's `EXISTS`
  aliases and its scalar-subquery aliases come from one sequence. Assertions on
  exact alias numbers in tests are therefore order-sensitive.
- **`sortExpr` extends `w.path` before it recurses, and the order matters.** It
  used to be the other way round: the inner subquery was rendered first and the
  path grew afterwards, so for a sort of two or more hops the deeper hop looked
  up its narrowing under a path spelled from the *second* segment — a
  `RelationScope("Manager.Department", …)` did not match there, and one declared
  for `Manager` matched twice. Model-scoped narrowings (`ForModel`, which is what
  `sqlrepo.Scope` installs) applied either way, which is what kept it invisible.
  `leaf` never had this shape; it updates the path inside the loop, before the
  next hop. If you touch this function, the assignment stays above the recursive
  call.
- **The first failure wins and the rest is a constant.** On a resolution error
  `leaf` writes `1 = 0` and records the error (`predicate.go:86`). The statement
  still assembles; `SQL.Done` is what refuses it. So a half-resolved column never
  reaches the database, but the SQL string in a debugger will contain the stub.

## Failure modes

| What goes wrong | Where it is caught | What the caller sees |
|---|---|---|
| unknown segment in the path | `WalkPath` → `UnknownFieldError` | 400, statement never sent |
| path stops on a relation | `leaf` (`predicate.go:90`) | 400 "path names a relation, not a column" |
| relation whose fk/ref names a missing field | `Relation.Resolve` inside `WalkPath` | 400 (`SchemaError`) |
| sort through a `has_many` / `many_to_many` | `sortExpr` (`predicate.go:549`) | 400 (`SchemaError`) |
| `DISTINCT` plus a sort through a relation | `repository.distinctSort` (`crud/sqlrepo/repository.go:343`) | 400 — the subquery can never be in the select list |
| `crud.Raw` with mismatched `?` markers | `rawNode.render` (`predicate.go:361`) | 400 (`SchemaError`) — never a renumbered bind |

## Files

| File | Role |
|---|---|
| `crud/predicate.go` | `writer`, `leaf`, `hopScope`, `sortExpr`, every node type |
| `crud/relation.go` | `WalkPath`, `PathHop`, `Relation.Resolve`, join-table columns |
| `crud/scope.go` | `RelationScopes.At` — path declaration wins over model |
| `crud/render.go` | `SQL.RelationScopes`, `Where`, `OrderBy`, `Done` |
| `crud/query/filter.go` | where the canonical path and the operator become a node |
| `crud/sqlrepo/repository.go` | attaches the repository's scopes to every statement |

## Tests that walk this flow

- `TestRelationHopsRenderAsCorrelatedExists` — `crud/predicate_test.go` — the shape, per relation kind.
- `TestEveryHopGetsItsOwnAlias` — `crud/predicate_test.go` — alias allocation.
- `TestRelationHopFollowsTheDialect` — `crud/predicate_test.go` — quoting and placeholders.
- `TestPredicateOnARelationPathIsRefused` — `crud/predicate_test.go`.
- `TestAPathThroughTwoToManyHopsNestsRatherThanJoins` — `crud/schema_edge_test.go` — nesting and paren count.
- `TestNestedSortIsAScalarSubquery` — `crud/predicate_test.go`.
- `TestARelationScopeReachesTheHopItNames` — `crud/predicate_test.go` — a narrowing declared for the inner hop of a two-hop sort lands in the inner subquery and not the outer one. Its control is `TestATwoHopSortCarriesNoNarrowingWhenNoneIsDeclared`.
- `TestSortThroughAToManyRelationIsRefused` — `crud/predicate_test.go`.
- `TestARelationFilterCarriesTheScopeIntoItsSubquery` — `crud/sqlrepo/relscope_test.go` — the full expected statement.
- `TestANestedSortCarriesTheScopeIntoItsSubquery` — `crud/sqlrepo/relscope_test.go`.
- `TestRelationScopeNarrowsBothThePreloadAndTheFilterHop` — `crud/sqlrepo/relscope_test.go`.
- `TestACallerCannotWidenARelationScope` — `crud/sqlrepo/relscope_test.go`.
- `TestToManyFilterDoesNotDuplicateOrInflateCount` — `test/integration/relations_test.go` — the reason for `EXISTS`.
- `TestNestedFiltersAgainstDatabases` / `TestNestedSortAgainstDatabases` — `test/integration/relations_test.go`.
- `TestDistinctRefusesASortThroughARelation` — `crud/sqlrepo/paging_edge_test.go`.

## See also

[[FL-001]] [[FL-006]] [[FL-007]] [[FL-004]]
