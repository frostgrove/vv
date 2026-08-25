# FL-010 — Codegen: a model becomes a DTO and a metamodel

**Entry point:** `cmd/vv/main.go:main` → `internal/codegen.Run`
**Implements:** [[UC-014]] [[UC-010]] [[UC-007]] · **Governed by:** [[D-018]] [[D-002]] [[D-014]] [[D-050]]

`vv` reads Go source, not compiled types. It never imports the package it
generates for, which is what lets it run on `ent`'s output directory and on a
package that does not compile yet.

## The path

1. **`main`** — `cmd/vv/main.go:55`
   Flags into a `generator`. `-into` moves the output; the output path is
   `filepath.Join(into or dir, out)`. `-import`'s base name becomes `modelAlias`,
   which is how model types get qualified when they are written somewhere else.

2. **`generator.run`** — `internal/codegen/codegen.go:149`
   `load`, then the `-into` rules: it requires `-import`, because the generated
   file has to name the model types (`internal/codegen/codegen.go:155`); the directory is created;
   `packageNameOf` (`internal/codegen/codegen.go:412`) reuses the package clause already declared
   there rather than inventing one. No models found → an error, not an empty
   file.

3. **`generator.load` — the two-pass AST read** — `internal/codegen/codegen.go:176`
   `parser.ParseDir` skips `_test.go` files **and the output file itself**, so a
   previous run's `vv_gen.go` is never read back in as input.
   - **Pass one** (`internal/codegen/codegen.go:192-204`) records every struct type name in the
     package into `g.structs`, with its declaration in `g.embeds`. This exists
     for one reason: a field whose type is another struct in this package has to
     be recognised as a relation holder rather than mistaken for a column. It is
     what keeps ent's `Edges UserEdges` out of the metamodel.
   - **Pass two** (`internal/codegen/codegen.go:206-237`) collects each file's imports (alias →
     path, honouring a named import) and runs `parseModel` on every struct that
     `-types` allows.
   - `sort.Strings(g.order)` — map iteration is not ordered, and the output has
     to be byte-identical across runs.

4. **`generator.parseModel`** — `internal/codegen/codegen.go:242`
   A struct becomes a model if it carries `db` or `rel` tags, **or** if it was
   named explicitly in `-types`. That second door is what makes a generated
   entity from another tool qualify.
   Per field:
   - anonymous → `g.embedded` (below), and the flattened fields are spliced in.
   - unexported → dropped.
   - named in `-skip` → kept with `Skip` set, so it is absent from every
     declaration and its name still reaches the exclusion list (step 7).
   - named in `-readonly` → `Immutable`, so it stays filterable and sortable but
     leaves both wire shapes.
   - **no `rel` tag and a base type that is a struct in this package → dropped**
     (`internal/codegen/codegen.go:278-282`). Neither a column nor an edge.
   - `rel` tag other than `-` → kept as a relation field.
   - otherwise a column: `db` options `pk auto immutable generated version` are
     read, and a field called `ID` is treated as the key even without `pk`
     (`internal/codegen/codegen.go:302`). `version` (spelled `version` or `lock`) is the optimistic
     lock — the same option `crud/meta.go` reads. The generator has to know it
     because `crud.PlanFor` *refuses* a DTO that names that column: emitting it
     produced a package that panicked at `Define` time. That is what happens when
     two features land in one change and neither knows about the other.

5. **Embedded-struct flattening** — `generator.embedded`, `internal/codegen/codegen.go:449`
   Two sources. `wellKnownEmbeds` (`internal/codegen/codegen.go:438`) hard-codes `gorm.Model`, whose
   fields live in another package the generator cannot read. Otherwise the
   embedded type must be a struct declared in this package, and `parseModel`
   runs on it recursively. The runtime flattens embedded structs
   (`crud/meta.go:343`), so the generator has to as well — without it
   `gorm.Model` would silently take `ID` and the timestamps out of the
   metamodel. An embedded type from an unknown package is skipped in silence.

6. **Nullability → `*T` or `Opt[T]`** — `dtoType`, `internal/codegen/codegen.go:379`, over `elem`,
   `internal/codegen/codegen.go:329`
   `elem` strips `crud.Opt[X]` and `*X` down to `X` and reports whether the
   column is nullable. Then:
   | model field | DTO field | JSON tag |
   |---|---|---|
   | `string` | `*string` | `name,omitempty` |
   | `*int` | `crud.Opt[int]` | `age,omitzero` |
   | `crud.Opt[time.Time]` | `crud.Opt[time.Time]` | `deletedAt,omitzero` |
   A nullable column has three intents and a pointer expresses two, so a
   nullable column always becomes `Opt` ([[D-002]]). `omitzero` rather than
   `omitempty` is what makes an undefined `Opt` disappear on the way back out.
   The JSON name comes from `lowerFirst` (`internal/codegen/codegen.go:387`), which keeps an
   all-caps prefix together: `ID` → `id`, `HTTPCode` → `httpCode`.

7. **`renderDTO`** — `internal/codegen/render.go:renderDTO`
   Skips relations, the primary key, `generated`, `immutable` and `version`
   columns — exactly the set `crud.PlanFor` would refuse at `Define` time
   (`crud/update.go:collectPlanFields`). That alignment is why the generated DTO
   always validates ([[FL-004]]).

   **Which flag dropped it is recorded, not only that it was dropped.**
   `generator.exclude` (`internal/codegen/codegen.go`) applies `-skip` and
   `-readonly` *after* the tags are read and sets `field.Excluded` only when
   `field.tagDropped` is false — that is, only when the model's own tags do not
   already keep the column out. Reflection reads the struct and never the
   command line, so that is exactly the set the generated file has to declare.
   A `-skip`ped column stays in `model.Fields` with `Skip` set rather than being
   dropped, because its *name* is still needed for the list.

8. **Metamodel attribute types** — `attrType`, `internal/codegen/codegen.go:363`
   | element type | attribute |
   |---|---|
   | `string` | `specs.Str[M]` |
   | `time.Time` | `specs.Cmp[M, time.Time]` |
   | any `cmp.Ordered` kind (`internal/codegen/codegen.go:355`) | `specs.Ord[M, T]` |
   | anything else | `specs.Attr[M, T]` |
   The attribute is over the *element* type, so `Age *int` gets
   `specs.Ord[User, int]`.

9. **`renderMetamodel` → `renderAttrs`** — `internal/codegen/render.go:128`, `internal/codegen/render.go:144`
   One attribute struct per (root model, relation path), named
   `<Root><ConcatenatedPath>Attrs`. Relation expansion stops on three
   conditions:
   - depth: `level+1 >= g.depth` (`internal/codegen/render.go:163`), `-depth` defaults to 2;
   - the related model is not in this package (`internal/codegen/render.go:169`) — nothing to
     expand into;
   - **a cycle**: the target already appears on the current path
     (`internal/codegen/render.go:172-183`). `Article → Author → Articles → …` has no end.
   Nested structs are emitted before the parent so the file reads top-down, and
   `emitted` stops the same type being written twice. The file ends with
   `var Article_ = specs.Metamodel[Article, ArticleAttrs]()`, which validates the
   attribute struct against the model at package initialisation — a renamed
   field then breaks the build rather than a request.

   **Every nested group opens with `specs.Rel[Root, Target]`**, the relation's
   own path as a handle, so `Article_.Comments.Path()` is `"Comments"`. The root
   group gets none: it is reached through no relation and has no path to answer.
   While the group's fields are walked, a column named `Path` or `String` is
   recorded — the handle is embedded, so such a column shadows the promoted
   method — and the group's doc comment then names `RelPath()` as the spelling
   that still works. The note is derived from field order, so the output stays
   byte-identical across runs ([[D-014]]).

   `specs.Metamodel` binds the handle in `bindRel`
   (`crud/decorators/specs/metamodel.go`): the path is the group's own prefix,
   and the handle's second type parameter is checked against `crud.Relation.Elem`
   — the target struct type, which needs no `Resolve()` and is therefore safe
   before any table is registered. Two refusals, both at package initialisation:
   a handle at the root, and a handle whose declared target is not where the
   relation lands. The check that the field *is* a handle compares its type
   against `relType()`, because embedding promotes the setters and the group
   would otherwise satisfy the same interface.

10. **The adapter half** — `internal/codegen/adapter.go:renderAdapter`, only
    under `-adapter`
    Five artefacts per model, in this order:
    | artefact | shape |
    |---|---|
    | `<Model>Input` | the entity body: `inputFields` — every column that is not a relation, not `generated`, not the lock and not on the exclusion list — under `lowerFirst` JSON names |
    | `<Model>Mapper` | `Model(ctx, in) (M, error)`, a field-for-field assignment, plus `Resolve` delegating to the map, so it satisfies `port.Mapper` **and** `errs.Resolver` |
    | `<Model>Paths` | `port.MustPathMap[M](port.PathMap{…}, "CreatedAt")` — the inverse, plus the exclusions |
    | `<Model>Service` | a struct embedding `*port.DefaultService[M, ID, U]`, with `var _ port.Service[…]` beside it so an override that changes a signature is a build failure |
    | `Mount<Model>` | `crudnet.ServingFor(svc, <Model>Mapper{}, opts...).Mount(mux, prefix)` — it takes a built service, so it uses `ServingFor` and cannot trip `options.refuseServiceOptions` |
    The id type comes from the primary key's `Type` as written, through
    `model.pk`; a model the generator cannot find a key on is an error rather
    than a file that does not compile. The name is `Mount<Model>` singular:
    pluralising in a generator is a guess, and `MountCategorys` is what guessing
    looks like.

11. **The coverage assertion** — `internal/codegen/adapter.go:renderCoverage`,
    whenever the DTO half runs
    One `init` at the end of the file, one line per model:
    `port.MustCoverUpdate[Model, ModelUpdate]("CreatedAt")`. This is the half
    that ships with or without `-adapter`, and the half that fires with nothing
    regenerated ([[D-050]]).

12. **`render` and the import block** — `internal/codegen/render.go:render`
    `used` flags decide `context`, `net/http`, `time`, and the `crud`, `errs`,
    `crudnet`, `port` and `specs` packages. A flag per package rather than a
    scan of the rendered text: the text is what the flags produced, so reading
    it back to decide would be one derivation checking itself.
    `-import` is added when the output lands elsewhere. `extraImports`
    (`internal/codegen/render.go:75`) walks every column type for a `pkg.Type` prefix and pulls
    the matching path out of the import map collected in `load` — a `uuid.UUID`
    or `decimal.Decimal` column would otherwise dangle. Imports are sorted, then
    `format.Source` runs; a formatting failure is reported with the offending
    source attached.

## Where the decisions bite

- **The DTO's exclusions must match the runtime's.** `renderDTO` skips exactly
  what `collectPlanFields` refuses. Add a tag option that the plan rejects, and
  the generator has to learn it in the same change or `Define` panics on
  generated code.
- **The domain is derived twice, on purpose.** The generator reads the model's
  *source text*; `port.CoversUpdate` and `port.NewPathMap` read the *compiled
  struct* through `crud.Schema`. That duplication is the whole point: a check
  that read the generator's own view of the model would be one derivation
  agreeing with itself, and `TestTheGeneratedStoresAreUpToDate` already is that
  test. Do not "simplify" the start-up check by having it call into
  `internal/codegen` ([[D-050]]).
- **A flag is invisible to reflection.** `-skip` and `-readonly` take a column
  out of the artefacts and leave it an ordinary writable column at run time, so
  the exclusion list has to be emitted or the start-up check refuses a column
  dropped on purpose. That is why `field.Excluded` exists and why it is set only
  when the tags do not already do the job — a redundant `-readonly` on a
  `generated` column emits nothing.
- **Nullable means `Opt`, always.** Collapsing it to `*T` because "the field is
  already a pointer" is the one edit that silently removes a feature.
- **The output must be byte-identical across runs.** Sorted model order, sorted
  imports, `emitted` guarding duplicates. `TestOutputIsByteIdenticalAcrossRuns`
  and the `TestTheGeneratedStoresAreUpToDate` /
  `TestGeneratedFileIsUpToDate` checks in the tree depend on it.
- **The generator reads text, not types.** `exprString` (`internal/codegen/codegen.go:314`) renders
  the type as written. A type alias, a dot-import or a type from a package the
  file does not import will be reproduced literally and fail to compile in the
  output. That is the boundary of this tool.
- **`-into` + `-import` are a pair.** Writing `UserUpdate` into ent's own
  package would collide with ent's update builder, and `ent generate` owns that
  directory.

## Failure modes

| What goes wrong | Where it is caught | What the caller sees |
|---|---|---|
| directory does not parse | `parser.ParseDir` (`internal/codegen/codegen.go:178`) | `vv: <parse error>`, exit 1 |
| no tagged models, and no `-types` | `run` (`internal/codegen/codegen.go:163`) | `no tagged models found in <dir>` |
| `-into` without `-import` | `run` (`internal/codegen/codegen.go:155`) | `-into needs -import …` |
| generated source does not parse | `render` (`internal/codegen/render.go:65`) | the error plus the full generated text |
| a column type from an unimported package | not caught | the output does not compile |
| an embedded type from an unknown package | `embedded` returns false | the field is silently absent from DTO and metamodel |
| metamodel field that no longer maps | `specs.Metamodel` at package init | panic at start-up in the consumer's package |
| a relation handle declaring the wrong target model | `bindRel` at package init | panic naming the path, the model it reaches and the one declared |
| a relation handle in the root attribute group | `bindRel` at package init | panic saying the root model is not a relation |
| a target model with a column called `Path` | not caught; the generated doc comment says so | `Article_.Comments.Path()` does not compile for that relation; `RelPath()` does |
| a column the model gained, with nothing regenerated | `port.MustCoverUpdate` at package init | panic at start-up naming the column ([[D-050]]) |
| a column the inverse map does not cover, or an entry naming one no request carries | `port.MustPathMap` at package init | panic at start-up naming the entry |
| `-adapter` on a model with no key the generator can name | `renderAdapter` | `-adapter needs a key it can name: tag one field of X db:",pk"` |
| `-adapter` with `-no-dto` | `run` | `-adapter needs the update DTO; drop -no-dto` |
| `-binding` naming anything but `net` or `none` | `Run` | `-binding X: only net and none are generated today` |

## Files

| File | Role |
|---|---|
| `cmd/vv/main.go` | the flags, and nothing else — it fills a `codegen.Options` and calls `Run` |
| `internal/codegen/codegen.go` | `Options`, `Run`, the two-pass load, `parseModel`, `embedded`, `elem`, `dtoType`, `attrType`, `qual` |
| `internal/codegen/render.go` | `render`, `used`, `renderDTO`, `renderMetamodel`, `renderAttrs`, `extraImports` |
| `internal/codegen/adapter.go` | `inputFields`, `renderAdapter`, `renderCoverage`, `quoteList` — the `-adapter` half, kept separate so the DTO half stays readable |
| `port/pathmap.go` | `PathMap`, `At`, `NewPathMap`/`MustPathMap`, `CoversUpdate`/`MustCoverUpdate` — what the generated file calls at package initialisation |
| `crud/update.go` | `UpdatePlan.Covers` — the model columns a DTO resolves to, through the plan the repository already builds |
| `crud/update.go` | `collectPlanFields` — the rules the DTO has to satisfy |
| `crud/meta.go` | the tag vocabulary and the runtime's own embedded-struct flattening |
| `crud/decorators/specs/metamodel.go` | `Metamodel`, `bindMetamodel`, `bindRel`, `Rel` and the attribute types the generator emits |
| `_examples/example/blog/vv_gen.go`, `test/entstore/`, `test/gormstore/`, `test/versionstore/` | checked-in output, verified up to date by tests. `blog` and `versionstore` are the two generated with `-adapter`; `versionstore` is the only model in the tree with a `version` column |
| `_examples/entstore/`, and the `vv_gen.go` in each `_examples/*-*/` stack | the same generator run the usage guides tell a consumer to run, checked in so an example is readable without running anything |

## Tests that walk this flow

- `TestUpdateDTOFollowsNullability` — `internal/codegen/codegen_test.go` — `*T` vs `Opt[T]`.
- `TestUpdateDTOLeavesOutWhatCannotBeWritten` — `internal/codegen/codegen_test.go` — PK, generated, immutable.
- `TestTheVersionColumnIsLeftOutOfTheDTO` — `internal/codegen/codegen_test.go` — the lock leaves the DTO and stays in the metamodel.
- `TestTheDeclarationAGeneratorProducesForAVersionedModelIsAccepted` — `crud/sqlrepo/version_test.go` — the other half of the loop: what the generator now emits is what `Define` accepts, with a control that naming the lock is still refused.
- `TestReadonlyKeepsAFieldQueryableButNotWritable` — `internal/codegen/codegen_test.go`.
- `TestSkipRemovesAFieldEverywhere` — `internal/codegen/codegen_test.go`.
- `TestRelationsBecomeNestedAttributeStructs` — `internal/codegen/codegen_test.go`.
- `TestRelationGroupsCarryATypedPath` — `internal/codegen/codegen_test.go` — the handle, with the root as its control.
- `TestATargetColumnNamedPathIsCalledOut` — `internal/codegen/codegen_test.go` — the shadowing note, with the unaffected direction of the same schema as its control.
- `TestARelationHandleAnswersItsCanonicalPath`, `TestARelationHandleDeclaringTheWrongTargetIsRefused`, `TestARelationHandleAtTheRootIsRefused` — `crud/decorators/specs/edge_test.go` — the binding half.
- `TestARelationScopeAcceptsAGeneratedPath` — `crud/sqlrepo/relscope_test.go` — the handle driving a real declaration, against the literal spelling as control.
- `TestRelationCyclesAreCutShort` — `internal/codegen/codegen_test.go`.
- `TestDepthBoundsHowFarRelationsExpand` — `internal/codegen/codegen_test.go`.
- `TestEmbeddedStructsAreFlattened` — `internal/codegen/codegen_test.go`.
- `TestGormModelIsFlattenedFromTheWellKnownTable` — `internal/codegen/codegen_test.go`.
- `TestIntoAnotherPackageQualifiesTheModelTypes` — `internal/codegen/codegen_test.go` — `qual` and the import block.
- `TestIntoAnExistingPackageKeepsItsName` — `internal/codegen/codegen_test.go` — `packageNameOf`.
- `TestIntoWithoutImportIsRefused` — `internal/codegen/codegen_test.go`.
- `TestAPackageWithNothingToGenerateIsAnError` — `internal/codegen/codegen_test.go`.
- `TestGeneratingOnlyOneHalf` — `internal/codegen/codegen_test.go` — `-no-dto` / `-no-meta`.
- `TestOutputIsByteIdenticalAcrossRuns` — `internal/codegen/codegen_test.go`.
- `TestGeneratedCodeCompilesAndValidates` — `internal/codegen/codegen_test.go` — the end-to-end guarantee.
- `TestGeneratedFileIsUpToDate` — `_examples/example/blog/blog_test.go` — and it checks the `//go:generate` line verbatim, so the command it runs cannot drift from the one the tree carries.
- `TestTheGeneratedStoresAreUpToDate` — `test/codegen/codegen_test.go` — the ent, gorm and version stores. It has no build tag: it needs no database, and living in the integration suite is what hid it from a contributor with no containers. Its own control tampers with the regenerated copy, so a helper that read one file twice could not stay green.
- `TestAGeneratedResourceRefusesToStartWhenAColumnIsMissing` — `internal/codegen/codegen_test.go` — the phase's load-bearing test: untampered starts (control), a column added to the model source refuses, an entry deleted from the map refuses.
- `TestTheGeneratedMapCoversEveryWritableColumn` — `internal/codegen/codegen_test.go` — with a `generated` column as the control that the domain is not "every column".
- `TestTheGeneratedAssertionNamesTheReadonlyExclusions` — `internal/codegen/codegen_test.go` — with the no-flag twin.
- `TestAVersionedModelGeneratesAResourceThatStarts` — `internal/codegen/codegen_test.go` — with a map claiming the lock as its refused control.
- `TestTheGeneratedDeclarationForAVersionedModelIsAccepted`, `TestTheGeneratedWireShapesLeaveOutWhatTheClientDoesNotOwn`, `TestTheGeneratedMapperAndItsInverseAgree` — `test/versionstore/versionstore_test.go`.
- `TestGeneratedDTOTypesFollowNullability` — `_examples/example/blog/blog_test.go` — the generated types, from the consumer's side.

## See also

[[FL-004]] [[FL-002]] [[FL-015]]
