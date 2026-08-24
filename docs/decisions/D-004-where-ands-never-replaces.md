# D-004 — `Where` ANDs; it never replaces

**Status:** accepted
**Invariant:** `crud.Where` must append to `Options.Filter`; no option, in any position, may remove or weaken a predicate another option already added.

## The decision

`Options.Filter` is a slice. `crud.Where(p)` appends. `Options.Predicate()` folds
the slice into a single `AND` node. There is no `ReplaceWhere`, no
`ClearFilter`, and no last-one-wins rule for filters — unlike `Limit`, `Page`
and `Offset`, which are scalars where the last mention does win.

## Why

This is the property that makes a security scope unremovable, and it has to hold
positionally, not by convention. The gate prepends its scope
(`repo/decorators/security/security.go:gate.scoped`) and then appends the
caller's options behind it. If `Where` replaced, a client sending one more filter
would erase the tenant predicate and read the whole table, and the call would
return 200.

The same property is what lets the layers compose at all:

- the blueprint's permanent `Scope` (soft deletes) ANDs with
- the gate's per-request scope (the tenant), which ANDs with
- the compiled wire filter, which ANDs with
- whatever the service layer adds.

Four authors, none of whom can see the others' predicates, and none of whom can
undo them. `crud.NarrowRelations` is the same rule one level out: relation
narrowings merge and AND rather than overwrite
(`crud/scope.go:MergeRelationScopes`).

The asymmetry with `SortBy` is deliberate: `crud.OrderBy` appends and
`crud.SortBy` replaces, because a sort order is a presentation choice with no
security meaning. A filter is not.

## What it forbids

- Do not add an option that resets `Options.Filter`. Not for tests, not for a
  "clean slate" helper.
- Do not change `crud.With` to overwrite `Filter` — it appends, and that is what
  lets a stored query shape be replayed on top of a scope rather than in place of
  it.
- `crud.SelectAll()` clears `Fields`, and that is *not* a counterexample: a
  projection is not a narrowing, and clearing it can only ever return more
  columns of rows the caller was already allowed to see. It exists because a
  row-level `Inspect` reading a column the client did not select would compare
  against a zero value and believe it
  (`repo/decorators/security/security.go:gate.whole`).
- A decorator must prepend its scope, not append it. Appending is equally safe
  today because AND is commutative, but prepending is what keeps the reading of
  `scoped` obvious.

## Where it lives

- `crud/options.go:Where` — `o.Filter = append(o.Filter, p)`.
- `crud/options.go:Options.Predicate` — folds to one `AND`.
- `crud/options.go:With` — replays a stored shape by appending.
- `crud/options.go:NarrowRelations` — the same rule for relation narrowings.
- `crud/scope.go:MergeRelationScopes` — two declarations of the same path AND.
- `repo/decorators/security/security.go:gate.scoped` — prepends the policy scope
  and the relation narrowing, then the caller's options.
- `repo/basic/repository.go:repository.scoped` — ANDs the blueprint's permanent
  scope over whatever the options resolved to.
- `query/compile.go:Request.Compile` — a compiled document emits `crud.Where`
  per clause; filter, flat terms and search are three separate `Where` calls and
  are therefore ANDed rather than merged into one object.

## Proven by

- `TestWhereAccumulates` in `crud/options_test.go` — the unit-level statement.
- `TestACallerFilterCannotWidenTheScope` in
  `repo/basic/blueprint_edge_test.go`.
- `TestASpecificationCannotEscapeARepositoryScope` in
  `repo/decorators/specs/edge_test.go`.
- `TestTheGateScopeAndTheRepositoryScopeBothApply` in
  `repo/decorators/security/edge_test.go` — two independent scopes, both in the
  statement.
- `TestWithScopeIsANDedWithTheClientFilter` in
  `http/crudfiber/options_test.go`.
- `TestFilterTermsAndSearchAreAndedNotMerged` in `query/compile_test.go`.
- `TestSearchIsParenthesised` in `query/query_test.go` — a search is an OR, and
  it is wrapped, so it cannot leak out of the surrounding AND. That is the
  precedence trap a hand-built `a LIKE ? OR b LIKE ?` string falls into.
- `TestPredicateFoldsTheFilter` in `crud/options_test.go`.

## See also

[[D-003]] [[D-007]] [[D-008]] [[D-013]]
