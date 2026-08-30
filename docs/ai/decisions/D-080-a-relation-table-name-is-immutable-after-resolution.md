# D-080 — A published relation table name is immutable

**Status:** accepted
**Invariant:** once a model type has supplied a canonical structured table
reference to relation metadata, no later declaration may claim that the type
means a different physical table. A blueprint-local physical view and an
explicitly tagged edge remain explicit, isolated overrides; no dotted string is
ever guessed into identifier components.

## The decision

The first *published* canonical declaration wins, and every later declaration
has to agree:

1. a table registered by `sqlrepo.Define` or `crud.RegisterTable`;
2. the model's `TableName()` method;
3. the snake-case plural convention.

The published value is a `crud.TableRef`, not a string: `Schema` and `Name` are
exact identifier components. `Schema` means PostgreSQL schema, MySQL/MariaDB
database, or SQLite attached database. SQL quotes those components separately.
The validated reference inside `Meta` and `Relation` is private; public
diagnostic strings and value-returning accessors cannot retarget cached SQL or
relation metadata after declaration.

Legacy string entry points (`Define`, `NewMeta`, `RegisterTable`, `CopyFrom`)
accept one component. A dot is an eager error naming the structured path, never
an instruction to split. Declarative code uses `DefineInSchema`; low-level code
uses `TableRef`, `NewMetaRef`, and structured registration. Exact components in
a `TableRef` may themselves contain dots because their boundary is no longer a
guess.

`TableNameOf` is a read-only preview: merely asking for the conventional or
model-owned name does not publish relation metadata. An untagged
`Relation.Target` publishes the canonical answer only when its immutable branch
context has not already fixed a blueprint-local table for that target type; a
successful ordinary `Define` publishes its root answer explicitly.
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

A `TableName()` method that returns an empty or dotted legacy string is a schema
error, but a read-only validation preview is not publication: a later explicit
valid registration or declaration may correct it and retry. Replacement is
refused only after the canonical answer crosses the publication boundary. In
particular, if `Relation.Target` itself publishes and caches the failed outcome,
a late global registration cannot repair that existing Relation; an explicit
structured `table=...,schema=...` edge remains the isolated escape hatch.

`IndependentTable` creates a blueprint-local physical view and does not publish
it as the canonical table. Its `Meta` carries that root choice through
self-relations and through cycles that return to the root model, so an archive
query cannot silently jump into the live table. Other model types still use
their canonical tables.

A relation with `table=...` does not consult the local or canonical answer.
For a qualified target it adds `schema=...`; a qualified many-to-many join adds
`joinSchema=...`. These values become structured references at schema build, so
a dotted legacy tag fails before any relation is traversed. This is the explicit
form for the exceptional edge that deliberately leaves the blueprint-local view
or reaches the same Go type through a different table.

The structured identity continues through schema consumers. A loaded catalog
indexes tables, constraints, and inbound foreign keys by `(schema, table)` and
exposes that through the optional `QualifiedCatalog` and
`QualifiedReferrers` capabilities. PostgreSQL loads non-system schemas outside
`search_path` and records `pg_table_is_visible` separately for legacy bare
lookups. A qualified probe refuses a third-party catalog without structured
lookup rather than asking it for the bare name. `sqlfault.FromCatalog` likewise
uses the driver's `Source.Schema`; if only the legacy columns SPI exists it
leaves columns unknown instead of attributing a same-named constraint from
another schema.

MySQL/MariaDB catalog loading remains scoped to the current `DATABASE()` and
SQLite catalog loading to `main`. Their repository SQL can still use another
database or an attached database, but a catalog-backed probe for that qualifier
fails declaration with `ErrUnknownTable`. This is an explicit capability
boundary, not a fallback.

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
  that relation, plus `schema=...` where qualified, so its ownership is visible
  beside the relation itself.
- Do not pass `"analytics.events"` to a one-string API. Use
  `DefineInSchema("analytics", "events")` or a `TableRef`; a dot is not a parser.
- Do not make an exported mutable `Meta` field the SQL authority. Table identity
  is validated once and returned only by value.
- Do not discard a known schema in catalog, probe, foreign-key reverse lookup,
  or fault-column enrichment. A miss is safe; a confident answer from a
  same-named table is not.
- Do not register `*Model`, an interface, or a scalar as though it were a model
  type. Repository models and table registrations are structs.

## Proven by

- `crud/relation_test.go:TestLateAndConflictingTableRegistrationsAreRefused`
- `crud/relation_test.go:TestConflictingTableRegistrationIsRefusedBeforeFirstUse`
- `crud/relation_test.go:TestAnEmptyModelTableNameFailsAndCannotBeRepairedAfterPublication`
- `crud/relation_test.go:TestTableRegistrationAcceptsOnlyStructModelTypes`
- `crud/relation_test.go:TestExplicitRelationTableDoesNotDependOnRegistryOrder`
- `crud/relation_test.go:TestConcurrentRegistrationAndRelationPublicationHaveOneLinearOutcome`
- `crud/relation_test.go:TestValidateRelationPathMatchesRuntimeRelationResolution`
- `crud/table_test.go:TestMetaTableReferenceIsImmutableAfterValidation`
- `crud/table_test.go:TestStructuredTableRegistrationRetainsTheWholePhysicalIdentity`
- `crud/qualified_relation_test.go:TestQualifiedRelationAndJoinTablesRenderEveryComponent`
- `crud/qualified_relation_test.go:TestQualifiedManyToManyPreloadUsesTheSameReferences`
- `crud/sqlrepo/blueprint_edge_test.go:TestTryDefineReturnsALateTableConflictInsteadOfPanicking`
- `crud/sqlrepo/blueprint_edge_test.go:TestFailedTryDefineDoesNotPublishItsTable`
- `crud/sqlrepo/blueprint_edge_test.go:TestFailedTryDefineWithUnknownLocalFieldPublishesNeitherModel`
- `crud/sqlrepo/blueprint_edge_test.go:TestLateRelationScopeFailurePublishesNeitherEarlierTargetNorRoot`
- `crud/sqlrepo/blueprint_edge_test.go:TestIndependentTableKeepsSelfRelationsInItsOwnPhysicalView`
- `crud/sqlrepo/blueprint_edge_test.go:TestIndependentTableKeepsACycleOnItsStartingPhysicalView`
- `crud/sqlrepo/blueprint_edge_test.go:TestConcurrentIndependentRelationViewsAreStableAndBranchLocal`
- `crud/sqlrepo/qualified_table_test.go:TestDottedDefineFailsBeforePublishingAndNamesTheStructuredPath`
- `crud/sqlrepo/qualified_table_test.go:TestQualifiedIndependentAndExplicitRelationTablesStayBranchLocal`
- `crud/catalog/qualified_test.go:TestQualifiedLookupsKeepSameNamedPostgresTablesAndForeignKeysSeparate`
- `crud/probe/declare_test.go:TestAQualifiedDeclarationUsesOnlyItsSchemasTableAndForeignKeys`
- `crud/sqlfault/catalog_test.go:TestFromCatalogUsesTheDriverSchemaForSameNamedConstraints`
- `crud/decorators/faults/faults_test.go:TestAQualifiedFaultRequiresExactSchemaAndTableComponents`
- `crud/adapter/crudpgx/copy_test.go:TestStringCopyFromRefusesADotBeforeCallingPgx`
- `test/integration/driver_pgx_test.go:TestQualifiedRepositoryAndPgxCopyUseTheSameStructuredTable`
- `test/integration/qualified_table_test.go:TestMySQLDatabaseQualifierAndSQLiteAttachedDatabaseAreLive`

## See also

[[D-005]] [[D-006]] [[D-007]] [[D-013]] [[D-020]] [[D-021]]
