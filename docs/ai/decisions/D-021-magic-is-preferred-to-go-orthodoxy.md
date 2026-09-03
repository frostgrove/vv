# D-021 — Magic is preferred to Go orthodoxy

**Status:** accepted
**Invariant:** When a design choice trades explicitness inside the library for less boilerplate at the consumer's call site, the library takes the trade — provided the magic is validated eagerly and fails at build or start-up rather than at request time.

## The decision

vv reflects over structs instead of asking for hand-written mappings,
generates code instead of asking for hand-written DTOs, and leans on type
inference so call sites carry no explicit generics. That is deliberate and it is
the owner's stated position, not an accident of implementation.

The owner's words, from the original brief:

> я больше люблю магию, пусть это и не в духе го. Ваще честно поебать на дух go

— "I prefer magic even if it is not in the spirit of Go. Honestly I do not care
about the spirit of Go." The same brief asks for "минимум boilerplate для
конечного потребителя" — minimum boilerplate for the end consumer — and for
`update` to be "умный метод с рефлектом чтобы не писать маппинги вручную", a
smart reflective method so nobody writes mappings by hand.

## Why

Go's default answer to this problem is a generated repository per model, or a
hand-written mapper per model, or `database/sql` scanning by hand. All three are
explicit, all three are auditable, and all three cost per resource, forever. The
per-resource cost is the thing being eliminated: two lines per entity, and a
full filtered, sorted, paginated, relation-loading API falls out.

The trade has a hard edge and the library holds to it: **the magic must fail
early.** Reflection that fails at request time is the worst of both worlds — no
compile-time check and no boilerplate to read. So:

- `sqlrepo.Define` validates the model tags, the ID type parameter and the update
  DTO at package initialisation and **panics** on a broken declaration.
- `crud.NewMeta` / `buildSchema` refuses an unmapped model, a composite key, a
  duplicate column, an unexported tagged field, an impossible `version` column.
- `Blueprint.resolveRelationScopes` and `security.relationFieldName` resolve
  relation paths at declaration time, so a typo panics at start-up rather than
  narrowing nothing in production.
- `specs.Metamodel` validates its attribute struct against the model at
  declaration time.
- The generated metamodel makes a renamed field a **build** error, not a 400.

That is the compromise: magic at the call site, strictness at the declaration.

The second edge is that the magic is not opaque. `crud.Meta` is exported and
introspectable, `crud/crudtest` records the exact SQL a repository produces, and
both usage guides include a per-entity test that diffs the derived column names
against the ORM's own.

## What it forbids

- Do not add a reflective path that reports its failure at request time. If it
  can be checked at `Define` time, check it there.
- Do not "make it explicit" by requiring a hand-written mapping. That is the
  cost this decision exists to refuse.
- Do not add an explicit type parameter to a call site that currently infers.
  See [[D-001]] and [[D-022]] — inference is why `security.Gate(policy)` and
  `crudfiber.New(repo)` are one line.
- Do not remove `unsafe`-based field access from `crud/access.go` on style
  grounds. It is offset arithmetic against a validated schema; the validation is
  what makes it safe, and it is what keeps scanning allocation-free per row.
- Do not make a tag option silently ignored. An unknown option in a `db` tag is
  a `SchemaError` (`crud/meta.go:collectFields`), because a typo'd option is
  indistinguishable from a missing feature otherwise.

## Where it lives

- `crud/meta.go:buildSchema` / `crud/meta.go:collectFields` — the reflection, and
  every declaration it refuses.
- `crud/meta.go:Schema.Field` — the forgiving name resolution that lets a
  TypeScript client keep its own spelling ([[D-013]]).
- `crud/access.go` — `Pointers`, `Values`, `ID`, `SetID` by offset.
- `crud/update.go:PlanFor` / `crud/update.go:buildPlan` — the reflective DTO
  plan, built once per (DTO, model) pair and cached.
- `crud/sqlrepo/blueprint.go:Define` — panics; `TryDefine` is the same without it.
- `crud/sqlrepo/blueprint.go:Blueprint.resolveRelationScopes` — path validation at
  declaration time.
- `crud/decorators/specs/metamodel.go:Metamodel` — attribute-struct validation at
  declaration time.
- `cmd/vv` — the codegen half ([[D-018]]).
- `port/pathmap.go:MustPathMap` / `:MustCoverUpdate` — the same rule applied to
  the part of the design most likely to rot: a generated artefact that no longer
  covers its model refuses to start ([[D-050]]).
- `crud/repo.go` — the inference half ([[D-001]]).
- `prompt` — the original brief, including the sentence quoted above.

## Proven by

- `TestBadDeclarationsPanicEarly` in `crud/sqlrepo/repository_test.go` — the
  edge this decision stands on.
- `TestBadDeclarationsAreRefusedAndSayWhy` in
  `crud/sqlrepo/blueprint_edge_test.go` — `TryDefine`, the same checks without the
  panic.
- `TestSchemaRefusesBrokenModels` in `crud/schema_edge_test.go` and
  `TestSchemaRejectsBadDeclarations` in `crud/meta_test.go`.
- `TestADbTagOnAnUnexportedFieldIsRefused` in `crud/schema_edge_test.go` — a
  silently dropped column would show up as a zero in the row, not as an error.
- `TestABadRelationDeclarationPanics` in
  `crud/decorators/security/relscope_test.go` and
  `TestRelationTagsAreCheckedWhenTheyAreDeclared` in
  `crud/schema_edge_test.go`.
- `TestMetamodelValidatesAtDeclarationTime` and
  `TestAMetamodelThatCannotBindIsRefusedAtDeclarationTime` in
  `crud/decorators/specs/specs_test.go` and
  `crud/decorators/specs/edge_test.go`.
- `TestPlanRefusesDTOsThatCannotBeApplied` in `crud/edge_test.go` and
  `TestPlanRejectsAForeignDTO` in `crud/update_test.go`.
- `TestSchemaOfIsCachedByType` in `crud/schema_edge_test.go` — the reflection happens
  once, which is the other half of the trade.
- `TestHTTPWorksWithoutExtraDeclarations` in `test/integration/http_test.go` —
  the payoff, stated as a test.
- `TestAGeneratedResourceRefusesToStartWhenAColumnIsMissing` in
  `internal/codegen/codegen_test.go` — the newest application of "fail at
  build or start-up, never at request time", and the one that had to compare two
  independent derivations to mean anything ([[D-050]]).

- `TestAFrozenFieldIsFrozenByEitherSpellingAndThroughBothVerbs` and
  `TestFreezingAFieldTheModelDoesNotHavePanicsAtDeclaration` in
  `crud/decorators/security/security_test.go` — `Policy.Immutable` was matched as
  a raw string on `Update` and resolved forgivingly on `Save`, so
  `Freeze("tenant_id")` froze the column on PUT and **not** on PATCH. The names
  now resolve once in `Gate`, and one that resolves to nothing panics there: after
  a release that panic would stop an already-deployed application from booting,
  which is why it has to land before the tag rather than after.
- `TestAClaimOfADifferentWidthThanTheColumnStillWorks` and
  `TestAnUncomparableClaimTypeFailsClosed` in
  `crud/decorators/security/policies_test.go` — `ScopeField`'s two halves consumed
  the extractor's `any` differently and only one coerced, so an `int64` claim
  against a `uint` column read perfectly and denied every create at request time.
  The shipped gorm guide's own line is a working reproduction. A claim the column
  cannot take is now a refusal that reaches no statement rather than a panic:
  still a request-time verdict, which is the one place in that package this
  decision does not get its way, and no longer a 500.

## See also

[[D-001]] [[D-018]] [[D-013]] [[D-022]] [[D-023]] [[D-050]]
