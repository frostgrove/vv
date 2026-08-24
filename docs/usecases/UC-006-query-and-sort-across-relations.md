# UC-006 — Query and sort across relations, from the wire and from Go

**Actor:** an HTTP client asking a question about related rows, and the
application author asking the same question in Go
**Covered by:** [[FL-005]] [[FL-006]] [[FL-001]] [[FL-012]]

## Scenario
"Articles by an author whose name contains 'an'." "Articles tagged go or rust."
"Articles with at least one approved comment, sorted by the author's name." The
client wants to ask these over the wire, with the related rows attached to the
answer, and the author wants the same questions expressible in Go. What neither
wants is the classic result of writing this with joins: an article with two
matching tags coming back twice, a page of twenty holding fourteen distinct
rows, and a total that does not exist.

## What must hold

1. Any filter path may cross a relation, at any declared depth, and the four edge
   kinds — to-one owning the key, to-one owned by the far side, to-many, and
   many-to-many through a join table — all work in filters.
2. A filter on a to-many relation means "has at least one related row that
   matches". A parent with two matching children is one row in the result.
3. The page size and the total both count parents, not matches. A filter that
   matches a parent twice does not turn a page of two into a page of three, and
   does not inflate the count.
4. Negating a relation filter means "has no related row that matches", and a
   parent with no related rows at all satisfies it.
5. A relation filter composes with a root filter and with the boolean
   combinators, in both directions and at nesting depth.
6. Sorting by a column reached through a to-one relation works and produces a
   deterministic order.
7. Sorting through a to-many relation is refused, not silently resolved. A
   collection has no single value to sort by, and picking one would be an
   invention.
8. A relation can be preloaded, and preloading is a fixed number of statements
   per relation per level, never one per parent. Paths sharing a prefix share a
   query.
9. A preload of a path more than one hop long attaches the far rows to the right
   near rows, and every parent gets its own copy — no two parents share a
   pointer to the same loaded child.
10. A preload survives paging: a page's children are the children of that page's
    parents, and a child shared with another page still arrives.
11. A preload is correct past the batching boundary. With more parents than fit
    in one key list, no parent loses its children and no parent gains somebody
    else's.
12. A preload can be narrowed — "load only the approved comments" — from the wire
    and from Go, and the narrowing reaches the loaded rows.
13. A preload cannot be paginated, and asking is refused rather than honoured. A
    limit over a batched load would truncate some parents' children and not
    others.
14. A preload path deeper than the repository allows is refused.
15. A preload of a relation the model does not declare is refused, and the
    refusal names the path.
16. Preloading a to-one edge that the schema does not actually keep unique
    returns the same row on every run rather than whichever the engine felt like.
17. A projection that would drop the column a preload needs to attach its rows
    keeps it anyway, so asking for fewer columns never silently breaks a preload.
18. All of the above is reachable from the wire and from Go with the same
    meaning. A relation path in a query document and a relation path in a Go
    predicate compile to the same thing.

## Out of scope

- **Join semantics.** A relation filter is a semi-join: one row in, one row out.
  If the question genuinely needs the cartesian product — "one row per matching
  tag" — this is the wrong tool.
- **Aggregates.** No `COUNT`, `SUM` or `AVG` over a relation, so "sort by number
  of comments" is not expressible. A view with the aggregate as a column is.
- **Ordering the preloaded collection across parents.** A preload can be sorted,
  but the sort is one statement over all the parents' children at once.
- **Which relations a client may traverse.** Unbounded by default; bounding is
  UC-002.
- **Whose rows come back through a relation.** A filter's subquery and a
  preload's second statement have their own `FROM` and inherit nothing from the
  parent's narrowing. Making them obey a rule is UC-004 and UC-016.
- **Inferring relations from another library's association tags.** Edges are
  declared for this library specifically.

## Covered by
| Flow | What it contributes |
|---|---|
| [[FL-005]] | a relation path in a filter or sort becoming a correlated subquery, and why it is not a join |
| [[FL-006]] | a preload becoming batched second statements, the key batching, and the attaching |
| [[FL-001]] | where the relation work sits in a list request |
| [[FL-012]] | the wire path being resolved against the model before any SQL exists |

## Status
**covered.** Every edge kind, two-hop paths, negation, composition with root
filters and with `or`, nested sorts through both to-one shapes, and the refusal
of a to-many sort are executed against live databases through the wire DSL. The
duplication-and-inflated-count case has a test of its own that asserts the exact
number a join implementation would have produced. Preloads are covered for
paging, for filtered loads, for the reverse direction of an edge, for nesting,
for parent-copy independence, and — deliberately — for a parent count past the
batching boundary in both directions. The ambiguous to-one case is pinned at the
statement level, not just at the result, so it cannot regress into
engine-dependent behaviour.

The honest limit is guarantee 18: the wire and Go paths converge on one
representation by construction, and both are heavily tested, but no test asserts
that a given question expressed both ways produces the identical statement.
