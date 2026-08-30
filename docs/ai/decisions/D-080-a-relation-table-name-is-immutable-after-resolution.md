# D-080 — A relation table name is immutable after resolution

**Status:** accepted
**Invariant:** once a model type has supplied a table name to relation metadata, no later declaration may claim that the type means a different table; an explicitly tagged relation owns its own table choice.

## The decision

The first of these declarations wins only when every later declaration agrees:

1. a table registered by `sqlrepo.Define` or `crud.RegisterTable`;
2. the model's `TableName()` method;
3. the snake-case plural convention.

`TableNameOf` marks the winning name resolved. Registering the same name again
is idempotent. Registering a different name either before or after resolution
is a `SchemaError`. `RegisterTable` and `Define` turn it into a start-up panic;
`TryRegisterTable`, `TryRegisterTableType`, and `TryDefine` return it for callers
that aggregate declaration failures.

A relation with `table=...` does not consult this registry. That is the explicit
form for a projection, archive, or other edge that deliberately reaches the
same Go type through a different table.

## Why

`Relation.Target` publishes and caches a `*Meta`. The old registry was a
last-writer-wins `sync.Map`: changing it after a relation resolved reported no
error but could not change the cached `Meta`. New repositories saw the new name
while old relations kept querying the old table. The process then had two
answers for one type, selected by declaration order.

Re-reading the registry on every query would make metadata mutable and would
still leave precomputed SQL and nested relation paths with mixed answers.
Silently replacing cached metadata would introduce races and invalidate pointer
identity. The honest boundary is declaration time: a change that cannot be
applied everywhere is refused everywhere.

## What it forbids

- Do not register two tables for one model type and expect the last call to win.
- Do not call `Define` per request to select a tenant table. Tenant isolation is
  a scope/policy concern; physical table selection needs distinct declarations
  or model types.
- Do not rely on package `init` order to override a relation target.
- Do not use the global registry for one exceptional edge. Put `table=...` on
  that relation so its ownership is visible beside the relation itself.

## Proven by

- `crud/relation_test.go:TestLateAndConflictingTableRegistrationsAreRefused`
- `crud/relation_test.go:TestConflictingTableRegistrationIsRefusedBeforeFirstUse`
- `crud/relation_test.go:TestExplicitRelationTableDoesNotDependOnRegistryOrder`
- `crud/sqlrepo/blueprint_edge_test.go:TestTryDefineReturnsALateTableConflictInsteadOfPanicking`

## See also

[[D-005]] [[D-006]] [[D-007]] [[D-013]] [[D-020]] [[D-021]]
