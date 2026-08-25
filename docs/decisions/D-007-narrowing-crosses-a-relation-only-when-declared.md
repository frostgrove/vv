# D-007 — A narrowing crosses a relation only when it is declared

**Status:** accepted
**Invariant:** A `Scope` narrows only the statement's own `FROM`; rows of another model reached by a preload, a nested filter or a nested sort are narrowed only where a `RelationScope` or a policy `RelationScopes` says so.

## The decision

Two separate declarations, both of which apply, neither of which can be widened.

- `sqlrepo.Scope(p)` — a predicate over the repository's own model, ANDed into
  every statement whose `FROM` is that table.
- `sqlrepo.RelationScope(path, p)` — a predicate over the *target* model of a
  relation path, applied wherever this repository reaches that path.
- `security.Policy.RelationScopes(ctx)` — the same thing, computed per request.

`Scope` additionally follows a relation back into the repository's *own* model
at any depth. That case needs no declaration because there is only one possible
answer: `resolveRelationScopes` registers the blueprint's scope under the model,
so a self-relation (`Node.Children []Node`) obeys it wherever it is reached.

## Why

**Why the far side is not covered automatically.** A `WHERE` constrains one
`FROM`. The three ways a query reaches a second table all open a new one:

- a preload is a second statement against a second table ([[D-006]]);
- a nested filter opens a correlated `EXISTS` with its own `FROM` ([[D-005]]);
- a nested sort opens a scalar subquery, likewise.

None of them inherits anything. Without a declaration, `?preload=comments` on a
tenanted article list reads every tenant's comments and hands them back attached
to rows the caller *was* allowed to see. The nested filter is worse: it is an
oracle. "Give me parents that have a kid named X" is answerable over rows the
caller cannot read, so a client enumerates another tenant's data one boolean at
a time.

**Why not just narrow every table automatically.** Because the library does not
know what the other table's rule is. A comment on a published article may be
readable by everyone; another repository over the same table may be allowed to
see more. Guessing "the target has a TenantID column too" is a guess about
somebody else's schema, and a wrong guess here is a wrong answer, not an error.

**Why two places rather than one.** One fact belongs to the table and the other
to the principal, and they are known at different times.

- "comments of a soft-deleted article are hidden" is true of the table, known
  when the blueprint is declared, and the same for every caller. It goes on the
  blueprint, once, at start-up.
- "this caller sees only tenant 7's comments" is true of the principal and
  arrives per request. It cannot be baked into a package-level `Define`.

Both feed the same mechanism. `repository.relScopes` merges the blueprint's
permanent narrowings with whatever the query carries, and
`MergeRelationScopes` ANDs where both name the same path or model — a narrowing
composes with a narrowing and never replaces one, for the same reason `Where`
ANDs ([[D-004]]).

**Why paths are canonicalised and validated at declaration time.** A typo in a
relation path narrows nothing, and narrowing nothing is exactly what a leak
looks like. `Blueprint.resolveRelationScopes` resolves each path through
`Meta.RelationAt` and fails the `Define`, so it panics at package
initialisation. `security.relationFieldName` does the same walk for the policy
helper — and deliberately resolves nothing about *tables*, because policies are
package variables and Go's initialisation order does not promise the blueprint
ran first; walking `Relation.Target` that early would cache a guessed table name
and keep it.

**Why the narrowing is rendered without further narrowing.** A narrowing is the
repository's own declaration, not caller input, so `writer.hopScope` clears
`w.rel` while rendering it. That also settles termination: a scope on a model
whose own path walks back into that model would otherwise recurse.

## What it forbids

- Do not make `Scope` imply a narrowing on other models. It cannot know their
  rules, and a wrong `AND` is a wrong answer.
- Do not make a relation narrowing overridable by a caller option. `Where`,
  `NarrowRelations` and `MergeRelationScopes` all AND.
- Do not remove either declaration in favour of the other. Losing
  `RelationScope` costs the table's own facts; losing `RelationScopes` costs
  every per-request rule.
- Do not resolve a relation's *table* while validating a policy path.
- Do not let a relation-scope resolution error fall through as "no narrowing".
  It must fail closed.

## Where it lives

- `crud/scope.go:RelationScopes` — the carrier; by path and by model.
- `crud/scope.go:RelationScopes.At` — a path declaration wins over a model one.
- `crud/scope.go:MergeRelationScopes` — two declarations of one path AND.
- `crud/scope.go:RelationScopes.under` — re-roots the paths for a statement whose
  own `FROM` is already that far down.
- `crud/options.go:NarrowRelations` — the per-query option.
- `crud/predicate.go:writer.hopScope` — renders the narrowing after each hop.
- `crud/render.go:SQL.RelationScopes` — hands them to the writer.
- `crud/preload.go:preloader.fetch` — ANDs the narrowing into the batched load.
- `crud/sqlrepo/blueprint.go:Scope` / `crud/sqlrepo/blueprint.go:RelationScope` — the
  declarations.
- `crud/sqlrepo/blueprint.go:Blueprint.resolveRelationScopes` — validation, and the
  self-relation registration.
- `crud/sqlrepo/repository.go:repository.relScopes` — the merge point.
- `crud/decorators/security/security.go:Policy.RelationScopes` and
  `crud/decorators/security/security.go:gate.narrow`.
- `crud/decorators/security/policies.go:ScopeRelationField` and
  `crud/decorators/security/policies.go:relationFieldName`.

## Proven by

- `TestARelationScopeReachesTheHopItNames` in `crud/predicate_test.go` — a path
  declaration reaches the hop it names at any depth. It did not: the second hop
  of a nested sort resolved its narrowing under the wrong path until the path was
  moved above the recursion.
- `TestTheGatesScopeFollowsAPreload` and `TestTheGatesScopeFollowsANestedFilter`
  in `test/integration/gate_relscope_test.go` — both carry a control case that
  fails if the leak closed itself, so a passing test cannot be vacuous
  ([[D-020]]).
- `TestABlueprintNarrowingAndAPolicyNarrowingBothApply` in
  `test/integration/gate_relscope_test.go` — the two authors, both honoured.
- `TestARelationScopeHidesTheSameRowsAPreloadWouldHaveExposed` and
  `TestARelationNobodyNarrowedIsStillReadWhole` in
  `test/integration/relscope_test.go`.
- `TestAPreloadIsNotNarrowedWithoutTheDeclaration` in
  `crud/decorators/security/relscope_test.go` — the deliberate negative, with its
  own control.
- `TestACallerCannotWidenARelationScope` in `crud/sqlrepo/relscope_test.go` and
  `TestACallerCannotWidenARelationNarrowing` in
  `crud/decorators/security/relscope_test.go`.
- `TestAPreloadOfTheRepositorysOwnModelCarriesItsScope` and
  `TestANestedPreloadOfTheSameModelCarriesTheScopeAtEveryLevel` in
  `crud/sqlrepo/relscope_test.go` — the self-relation case.
- `TestRelationScopeRefusesAPathTheModelDoesNotHave` in
  `crud/sqlrepo/relscope_test.go` — the typo fails at declaration time.
- `TestARelationScopeErrorFailsClosed` in
  `crud/decorators/security/relscope_test.go`.
- `TestTwoNarrowingsOfOnePathAreBothApplied` and
  `TestCombineMergesRelationNarrowings` in
  `crud/decorators/security/relscope_test.go`.
- `TestARelationFilterCarriesTheScopeIntoItsSubquery` and
  `TestANestedSortCarriesTheScopeIntoItsSubquery` in
  `crud/sqlrepo/relscope_test.go`.

## See also

[[D-004]] [[D-005]] [[D-006]] [[D-008]] [[D-020]]
