# D-080 — A published relation table name is immutable

**Status:** accepted
**Invariant:** once a model type has supplied a canonical table name to relation metadata, no later declaration may claim that the type means a different canonical table. A blueprint-local physical view and an explicitly tagged edge remain explicit, isolated overrides.

## The decision

The first *published* canonical declaration wins, and every later declaration
has to agree:

1. a table registered by `sqlrepo.Define` or `crud.RegisterTable`;
2. the model's `TableName()` method;
3. the snake-case plural convention.

`TableNameOf` is a read-only preview: merely asking for the conventional or
model-owned name does not publish relation metadata. An untagged
`Relation.Target`, or a successful ordinary `Define`, publishes the answer.
Registering the same name again is idempotent. Registering a different name
before publication is a conflict and after publication is a late-declaration
`SchemaError`. `RegisterTable` and `Define` turn it into a start-up panic;
`TryRegisterTable`, `TryRegisterTableType`, and `TryDefine` return it for callers
that aggregate declaration failures. Registration accepts a struct model type
only.

`TryDefine` performs every mapping, option and relation-scope check through a
non-publishing validation pass before it publishes the root canonical name. A
failed declaration therefore reserves neither the root nor any relation target
traversed by an earlier valid scope. Target publication stays lazy until an
actual relation is used.

A `TableName()` method that returns the empty string is a schema error, but a
read-only validation preview is not publication: a later explicit non-empty
registration or declaration may correct it and retry. Replacement is refused
only after the canonical answer crosses the publication boundary. In
particular, if `Relation.Target` itself publishes and caches the empty failed
outcome, a late global registration cannot repair that existing Relation; an
explicit `table=...` edge remains the isolated escape hatch.

`IndependentTable` creates a blueprint-local physical view and does not publish
it as the canonical table. Its `Meta` carries that root choice through
self-relations and through cycles that return to the root model, so an archive
query cannot silently jump into the live table. Other model types still use
their canonical tables.

A relation with `table=...` does not consult the local or canonical answer.
That is the explicit form for the exceptional edge that deliberately leaves the
blueprint-local view or reaches the same Go type through a different table.

## Why

`Relation.Target` publishes and caches a `*Meta`. The old registry was a
last-writer-wins `sync.Map`: changing it after a relation resolved reported no
error but could not change the cached `Meta`. New repositories saw the new name
while old relations kept querying the old table. The process then had two
answers for one type, selected by declaration order.

Re-reading the registry on every query would make metadata mutable and would
still leave precomputed SQL and nested relation paths with mixed answers.
Silently replacing cached metadata would introduce races and invalidate pointer
identity. A single global answer is correct for canonical wiring but not for an
explicit additional physical view. The honest boundary is immutable canonical
publication plus an immutable, root-local relation context.

## What it forbids

- Do not register two canonical tables for one model type and expect the last
  call to win.
- Do not call `Define` per request to select a tenant table. Tenant isolation is
  a scope/policy concern; physical table selection needs distinct declarations
  or model types.
- Do not rely on package `init` order to override a relation target.
- Do not use the global registry for one exceptional edge. Put `table=...` on
  that relation so its ownership is visible beside the relation itself.
- Do not register `*Model`, an interface, or a scalar as though it were a model
  type. Repository models and table registrations are structs.

## Proven by

- `crud/relation_test.go:TestLateAndConflictingTableRegistrationsAreRefused`
- `crud/relation_test.go:TestConflictingTableRegistrationIsRefusedBeforeFirstUse`
- `crud/relation_test.go:TestAnEmptyModelTableNameFailsAndCannotBeRepairedAfterPublication`
- `crud/relation_test.go:TestTableRegistrationAcceptsOnlyStructModelTypes`
- `crud/relation_test.go:TestExplicitRelationTableDoesNotDependOnRegistryOrder`
- `crud/sqlrepo/blueprint_edge_test.go:TestTryDefineReturnsALateTableConflictInsteadOfPanicking`
- `crud/sqlrepo/blueprint_edge_test.go:TestFailedTryDefineDoesNotPublishItsTable`
- `crud/sqlrepo/blueprint_edge_test.go:TestFailedTryDefineWithUnknownLocalFieldPublishesNeitherModel`
- `crud/sqlrepo/blueprint_edge_test.go:TestLateRelationScopeFailurePublishesNeitherEarlierTargetNorRoot`
- `crud/sqlrepo/blueprint_edge_test.go:TestIndependentTableKeepsSelfRelationsInItsOwnPhysicalView`
- `crud/sqlrepo/blueprint_edge_test.go:TestIndependentTableKeepsACycleOnItsStartingPhysicalView`

## See also

[[D-005]] [[D-006]] [[D-007]] [[D-013]] [[D-020]] [[D-021]]
