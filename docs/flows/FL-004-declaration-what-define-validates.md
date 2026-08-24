# FL-004 — Declaration: what `basic.Define` validates and when

**Entry point:** `repo/basic/blueprint.go:Define`
**Implements:** [[UC-010]] [[UC-014]] [[UC-016]] · **Governed by:** [[D-021]] [[D-007]] [[D-013]]

`Define` runs at package initialisation, so what it checks fails at start-up and
what it does not check fails on a request. Knowing which is which is the whole
value of this flow.

## The path

1. **`basic.Define`** — `repo/basic/blueprint.go:114`
   A thin panic wrapper over `TryDefine` (`blueprint.go:123`). Use `TryDefine`
   in a test that wants to assert on the message.

2. **`crud.NewMeta[M](table)`** — `crud/meta.go:449`
   An empty table name becomes `pluralise(snake(TypeName))` (`meta.go:564`,
   `meta.go:545`). The schema itself comes from `SchemaOf[M]`
   (`crud/meta.go:191`), which caches per `reflect.Type` — including the error,
   so a broken model does not get re-reflected on every call.

3. **`buildSchema` → `collectFields`** — `crud/meta.go:228`, `crud/meta.go:336`
   One pass over the struct fields. Per field, in order:
   - `db:"-"` → skipped entirely.
   - **anonymous struct, no `db` tag, not `Opt`, not a scalar struct** → flattened
     in place with the byte offset carried through (`meta.go:352`). Embedded
     *pointer* structs are refused (`meta.go:344`) and recursive embedding is
     refused (`meta.go:349`).
   - unexported: silently skipped, unless it carries a `db` tag — then it is an
     error, because reflection cannot read it and a silently dropped column
     shows up as a zero in the row rather than as a complaint here
     (`meta.go:361`).
   - **relation candidates** — `relCandidate` (`crud/relation.go:172`): a struct,
     `*struct`, `[]struct` or `[]*struct` that is not an `Opt` and not a scalar
     struct (`time.Time`, a `driver.Valuer`, a `sql.Scanner`, a
     `TextMarshaler` — `meta.go:478`). Such a field is **never** a column. With
     no `rel` tag it is skipped completely; with one it goes to `parseRelation`.
     A `rel` tag on anything else is an error (`meta.go:386`).
   - otherwise a column: name from the tag or `snake(FieldName)`, then the
     options `pk auto noauto immutable generated version` and their aliases
     (`meta.go:399-417`). An unknown option is an error.
   - duplicates (field name, column name) and a second `pk` — composite keys are
     not supported — are errors (`meta.go:418-431`).

4. **`parseRelation`** — `crud/relation.go:186`
   Reads the kind (`belongs_to`, `has_one`, `has_many`, `many_to_many`) or infers
   it: a slice with `join=` is many-to-many, a slice is has-many, a single value
   is belongs-to when this struct carries the foreign key and has-one otherwise
   (`relation.go:235-250`). Cardinality is checked against sliceness
   (`relation.go:255-260`). Many-to-many without a join table is an error.
   **Nothing about the target model is resolved here.** `LocalField` and
   `TargetField` may be left empty to be filled from a primary key later.

5. **Back in `buildSchema`** — the model-level rules
   - Primary key: a `pk` tag, else a field called `ID`, else a column called
     `id`; nothing → error (`meta.go:247-257`). An integer key gets `Auto`
     unless `noauto` was asked for.
   - The key is reordered to `Fields[0]` (`meta.go:263`), which is what makes
     the prebuilt `SELECT` column order stable.
   - `checkVersion` (`meta.go:315`) refuses the four declarations an optimistic
     lock cannot be built on: two version columns, a version on the primary key,
     `version`+`immutable`, `version`+`generated`, and a non-integer type.
   - `Insert`, `InsertGen`, `Update` are computed here (`meta.go:281-297`) —
     see [[FL-003]].
   - A `generated` primary key is an error (`meta.go:298`).
   - Folded aliases are registered last (`meta.go:301-307`); an alias that could
     mean two fields is registered as `ambiguousField` and resolves to nothing,
     rather than guessing (`meta.go:158`).

6. **`Schema.CheckID`** — `crud/access.go:109`
   The repository's `ID` type parameter against the key's Go type, allowing
   `Opt[T]` and `*T` wrappers.

7. **`crud.PlanFor[U]`** — `crud/update.go:54` → `collectPlanFields`
   (`crud/update.go:78`)
   Every exported DTO field must name a model field. Anonymous structs are
   flattened the same way; `db:"-"` opts out; `db:"Other"` retargets. Then four
   refusals (`crud/update.go:113-121`): the primary key, a `generated` column, an
   `immutable` column and the `version` column cannot be in an update DTO. Then a
   type check: `Opt[T]`, `*T` and `T` must all reduce to the model field's
   element type. The plan is cached by `(dtoType, modelType)`.

8. **`crud.RegisterTable[M]`** — `crud/relation.go:139`, called from
   `blueprint.go:147`
   This is the line that teaches *other* models which table this one lives in.
   `TableNameOf` (`relation.go:154`) consults the registry first, then a
   `TableName()` method, then the snake-case plural. Declaring the repository is
   normally enough for a relation on another model to resolve correctly — and if
   a relation resolves before the blueprint runs, it caches the guessed name.

9. **Settings, then `resolveRelationScopes`** — `blueprint.go:148`,
   `blueprint.go:164`
   Each `RelationScope(path, pred)` is validated with `meta.RelationAt`
   (`crud/relation.go:399`) — **this does resolve the relation**, target schema
   and table name included. A path the model does not declare is an error at
   declaration time, because a typo that silently narrows nothing reads as
   protection and is not. Finally `ForModel(meta.Type, set.scope)`
   (`crud/scope.go:51`) settles the one case that needs no declaring: a relation
   pointing back at this repository's own model is narrowed by `Scope`.

10. **`Blueprint.Bind`** — `blueprint.go:182`
    `newRepository` assembles the static statement fragments once
    (`repo/basic/repository.go:27`), then `crud.Chain` wraps the decorators with
    the first one outermost.

## What is deferred to first use

| Deferred | Where | Why |
|---|---|---|
| the target schema of a relation | `Relation.Target` — `crud/relation.go:82`, behind a `sync.Once` | two models may reference each other; resolving eagerly cannot terminate |
| `LocalField` / `TargetField` defaults | `Relation.resolveDefaults` — `crud/relation.go:290` | they default to the *target's* primary key, which is not known yet |
| a relation naming a field that does not exist | first `Resolve()`, from the SQL writer or the preloader | the target schema is not available at declaration time |
| `DefaultSort` naming a column that is not there | the SQL writer's `Err()` — `crud/render.go:132` | not validated by `Define` at all; the statement is refused before it is sent, never handed to the database |
| the update plan for a DTO that is not `U` | `UpdatePlan.dtoValue` — `crud/update.go:174` | only reachable through a decorator that passes `any` |

## Where the decisions bite

- **A broken mapping is a start-up failure, not a 500 on the first request.**
  That is the point of `Define` panicking. Anything moved out of `TryDefine` into
  the request path loses that.
- **Lazy relation resolution is load-bearing.** `Relation.Target` caches the
  table it computes on first call, so resolving a path *early* pins a guessed
  table name. `security.relationFieldName`
  (`repo/decorators/security/policies.go:102`) walks element types rather than
  calling `Resolve` for exactly this reason: policies are package variables and
  Go's initialisation order does not promise the blueprint ran first.
- **A struct-shaped field with no `rel` tag is neither a column nor an edge.**
  This is what keeps ent's `Edges UserEdges` and gorm's association fields out of
  the column list without anybody tagging them.
- **An ambiguous folded alias resolves to nothing.** Guessing which field the
  client meant would be a silent wrong answer.

## Failure modes

| What goes wrong | Where it is caught | What the caller sees |
|---|---|---|
| no primary key, composite key, duplicate column | `buildSchema` / `collectFields` | panic at start-up (`SchemaError`) |
| `db` tag on an unexported field | `collectFields` (`meta.go:361`) | panic at start-up |
| `rel` tag on a non-struct type | `collectFields` (`meta.go:386`) | panic at start-up |
| a version column that cannot be a lock | `checkVersion` (`meta.go:315`) | panic at start-up |
| `ID` type parameter ≠ key type | `CheckID` (`access.go:109`) | panic at start-up |
| DTO field naming the PK / a generated / immutable / version column | `collectPlanFields` (`update.go:113`) | panic at start-up |
| DTO field of the wrong type | `collectPlanFields` (`update.go:136`) | panic at start-up |
| `RelationScope` on a path that does not exist | `resolveRelationScopes` (`blueprint.go:166`) | panic at start-up |
| relation pointing at a missing field | first `Resolve()` | 400 (`SchemaError`) on the request that uses it |
| `DefaultSort` on an unknown column | `SQL.Done` | 400 (`UnknownFieldError`), no statement sent |

## Files

| File | Role |
|---|---|
| `repo/basic/blueprint.go` | `Define`, `TryDefine`, `Setting`s, `resolveRelationScopes`, `Bind` |
| `crud/meta.go` | `SchemaOf`, `buildSchema`, `collectFields`, `checkVersion`, tag vocabulary |
| `crud/relation.go` | `parseRelation`, `relCandidate`, `Target`, `resolveDefaults`, `RegisterTable`, `TableNameOf` |
| `crud/update.go` | `PlanFor`, `collectPlanFields` |
| `crud/access.go` | `CheckID` |
| `crud/scope.go` | `AtPath`, `ForModel` |
| `repo/basic/repository.go` | `newRepository` — the statement fragments built at `Bind` |

## Tests that walk this flow

- `TestBadDeclarationsAreRefusedAndSayWhy` — `repo/basic/blueprint_edge_test.go` — the table of declaration-time refusals.
- `TestBadDeclarationsPanicEarly` — `repo/basic/repository_test.go` — `Define` panics rather than deferring.
- `TestSchemaRefusesBrokenModels` — `crud/schema_edge_test.go` — the model-level rules.
- `TestADbTagOnAnUnexportedFieldIsRefused` — `crud/schema_edge_test.go`.
- `TestAnUntaggedUnexportedFieldIsSimplyIgnored` — `crud/schema_edge_test.go` — the other half of that rule.
- `TestPrimaryKeyFallsBackToIDByNameThenByColumn` — `crud/schema_edge_test.go`.
- `TestNoAutoOptOut` / `TestAssignedKeyIsNotAuto` — `crud/meta_test.go`.
- `TestADeclarationThatCannotBeALockIsRefused` — `crud/version_test.go` — `checkVersion`.
- `TestAnUpdateDTOCannotSetTheVersion` — `crud/version_test.go`.
- `TestPlanRefusesDTOsThatCannotBeApplied` — `crud/edge_test.go` — the DTO rules.
- `TestRelationTagsAreCheckedWhenTheyAreDeclared` — `crud/schema_edge_test.go` — what `parseRelation` catches.
- `TestARelationPointingAtAMissingFieldIsReportedOnUse` — `crud/schema_edge_test.go` — what it deliberately does not.
- `TestSelfReferencingRelationResolves` — `crud/relation_test.go` — why resolution is lazy.
- `TestRelationScopeRefusesAPathTheModelDoesNotHave` — `repo/basic/relscope_test.go`.
- `TestAnUnknownDefaultSortIsRefusedBeforeTheQueryIsSent` — `repo/basic/blueprint_edge_test.go` — the one setting `Define` does not check.
- `TestAnEmptyTableNameBecomesThePluralOfTheModel` — `repo/basic/blueprint_edge_test.go`.
- `TestTableNameOf` / `TestRelationTargetUsesTheTablerName` — `crud/relation_test.go`.
- `TestRegisterTableTypeRedirectsARelationsTarget` — `crud/decorate_test.go`.
- `TestAnAmbiguousAliasResolvesToNothing` — `crud/schema_edge_test.go`.
- `TestSchemaOfIsCachedByType` — `crud/schema_edge_test.go`.

## See also

[[FL-002]] [[FL-003]] [[FL-005]] [[FL-006]] [[FL-010]]
