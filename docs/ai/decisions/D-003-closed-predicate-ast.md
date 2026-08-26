# D-003 — The predicate AST is closed; `crud.Raw` is the only escape hatch

**Status:** accepted
**Invariant:** Every `crud.Predicate` implementation except `rawNode` must live in package `crud`, and `render` must stay an unexported method so nothing outside the package can produce SQL text.

## The decision

`crud.Predicate` has one method, `render(w *writer)`, and it is unexported. The
constructors in `crud/predicate.go` are the complete set of node producers.
`crud.Raw` is the single node that emits caller-supplied text; its markers are
`?` regardless of dialect and are rewritten on render, and the marker count must
match the argument count or the statement fails.

## Why

A security decorator ANDs a scope into a caller's options and then hands the
whole tree to the writer. That is only safe if the caller cannot have smuggled
in a node that closes the parenthesis, or that renders `1=1 OR` in front of
everything. An open interface makes that a one-line attack from any package that
imports `crud`; an unexported method makes it impossible to write such a node at
all, and the audit is `grep -rn 'crud.Raw'` rather than a review of every type
in the tree.

The other half is the wire. `crud/query/` compiles an untrusted JSON document into
predicates. If the AST were open, "which node types can a compiled document
produce" would be an ongoing question. Closed, it is answered once.

What `Raw` costs is real and is not hidden: identifiers in a raw fragment are
not resolved against the schema and not quoted. A raw fragment naming a column
that was renamed fails at run time, not at build time. The one guard that was
added is arithmetic, and it is there because the failure mode is silent:
mismatched markers used to renumber somebody else's bind. Both directions now
fail the statement.

- more `?` than arguments → the statement is refused.
- more arguments than `?` → the statement is refused; a caller who hand-wrote a
  native `$1` would otherwise have had it renumbered against another node's bind.
- `??` escapes a literal question mark — which is what makes PostgreSQL's JSONB
  `?` operator reachable at all.

## What it forbids

- Do not export `Predicate.render`, and do not add an exported `SQL() string`
  to the interface. Both re-open the AST.
- Do not add a "just this once" node that takes a format string. `Raw` already
  is that node, and having one of them is what makes it greppable.
- Do not make `Raw` quote or resolve identifiers. Half-resolution is worse than
  none: callers would stop expecting the sharp edge and would still cut
  themselves on the cases the resolver missed.
- Do not relax the marker/argument check to a warning.

## Where it lives

- `crud/predicate.go:Predicate` — the interface, with the unexported method.
- `crud/predicate.go:Raw` / `crud/predicate.go:rawNode.render` — the escape
  hatch, the `?` rewrite, the `??` escape and both count checks.
- `crud/predicate.go` constructors — `Eq`, `Ne`, `Gt`, `Gte`, `Lt`, `Lte`,
  `IsNull`, `IsNotNull`, `Like`, `NotLike`, `LikeIgnoreCase`, `Contains`,
  `StartsWith`, `EndsWith`, `Between`, `In`, `NotIn`, `InAny`, `NotInAny`,
  `EqField`, `And`, `Or`, `Not`, `True`, `False`.
- `crud/predicate.go:escapeLike` — `%`, `_` and `\` in a `Contains` argument are
  escaped, so a user-supplied search term is not a wildcard.
- `crud/render.go:SQL.Where` and `crud/render.go:SQL.Predicate` — the only two
  ways a predicate reaches a statement, and both go through the same writer.

## Proven by

- `TestRawArgumentsHaveToMatchItsMarkers` in `crud/edge_test.go` — both
  directions of the count mismatch.
- `TestRawEscapesAQuestionMarkForPostgresJSONBOperators` in
  `test/integration/dialect_edge_test.go` — the `??` escape against a live
  PostgreSQL.
- `TestPayloadsInEveryNamePositionAreRefused` in `crud/query/hostile_test.go` — a
  wire document cannot get text into a name position.
- `TestPayloadsInValuePositionsAreBoundNotWritten` in `crud/query/hostile_test.go`.
- `TestOneClauseCannotEscapeAnother` in `crud/query/hostile_test.go` — would catch a
  node that renders unbalanced parentheses.
- `TestWildcardsInAPatternAreEscaped` in `crud/query/hostile_test.go`.
- `TestQuotedIdentifiersSurviveEveryClause` in
  `test/integration/dialect_edge_test.go`.

## See also

[[D-004]] [[D-013]] [[D-008]]
