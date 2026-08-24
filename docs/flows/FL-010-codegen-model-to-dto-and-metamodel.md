# FL-010 — Codegen: a model becomes a DTO and a metamodel

**Entry point:** `cmd/rxcrud/main.go:main`
**Implements:** [[UC-014]] [[UC-010]] [[UC-007]] · **Governed by:** [[D-018]] [[D-002]] [[D-014]]

`rxcrud` reads Go source, not compiled types. It never imports the package it
generates for, which is what lets it run on `ent`'s output directory and on a
package that does not compile yet.

## The path

1. **`main`** — `cmd/rxcrud/main.go:55`
   Flags into a `generator`. `-into` moves the output; the output path is
   `filepath.Join(into or dir, out)`. `-import`'s base name becomes `modelAlias`,
   which is how model types get qualified when they are written somewhere else.

2. **`generator.run`** — `main.go:149`
   `load`, then the `-into` rules: it requires `-import`, because the generated
   file has to name the model types (`main.go:155`); the directory is created;
   `packageNameOf` (`main.go:412`) reuses the package clause already declared
   there rather than inventing one. No models found → an error, not an empty
   file.

3. **`generator.load` — the two-pass AST read** — `main.go:176`
   `parser.ParseDir` skips `_test.go` files **and the output file itself**, so a
   previous run's `rxcrud_gen.go` is never read back in as input.
   - **Pass one** (`main.go:192-204`) records every struct type name in the
     package into `g.structs`, with its declaration in `g.embeds`. This exists
     for one reason: a field whose type is another struct in this package has to
     be recognised as a relation holder rather than mistaken for a column. It is
     what keeps ent's `Edges UserEdges` out of the metamodel.
   - **Pass two** (`main.go:206-237`) collects each file's imports (alias →
     path, honouring a named import) and runs `parseModel` on every struct that
     `-types` allows.
   - `sort.Strings(g.order)` — map iteration is not ordered, and the output has
     to be byte-identical across runs.

4. **`generator.parseModel`** — `main.go:242`
   A struct becomes a model if it carries `db` or `rel` tags, **or** if it was
   named explicitly in `-types`. That second door is what makes a generated
   entity from another tool qualify.
   Per field:
   - anonymous → `g.embedded` (below), and the flattened fields are spliced in.
   - unexported, or named in `-skip` → dropped.
   - named in `-readonly` → `Immutable`, so it stays filterable and sortable but
     leaves the update DTO.
   - **no `rel` tag and a base type that is a struct in this package → dropped**
     (`main.go:278-282`). Neither a column nor an edge.
   - `rel` tag other than `-` → kept as a relation field.
   - otherwise a column: `db` options `pk auto immutable generated version` are
     read, and a field called `ID` is treated as the key even without `pk`
     (`main.go:302`). `version` (spelled `version` or `lock`) is the optimistic
     lock — the same option `crud/meta.go` reads. The generator has to know it
     because `crud.PlanFor` *refuses* a DTO that names that column: emitting it
     produced a package that panicked at `Define` time. That is what happens when
     two features land in one change and neither knows about the other.

5. **Embedded-struct flattening** — `generator.embedded`, `main.go:449`
   Two sources. `wellKnownEmbeds` (`main.go:438`) hard-codes `gorm.Model`, whose
   fields live in another package the generator cannot read. Otherwise the
   embedded type must be a struct declared in this package, and `parseModel`
   runs on it recursively. The runtime flattens embedded structs
   (`crud/meta.go:343`), so the generator has to as well — without it
   `gorm.Model` would silently take `ID` and the timestamps out of the
   metamodel. An embedded type from an unknown package is skipped in silence.

6. **Nullability → `*T` or `Opt[T]`** — `dtoType`, `main.go:379`, over `elem`,
   `main.go:329`
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
   The JSON name comes from `lowerFirst` (`main.go:387`), which keeps an
   all-caps prefix together: `ID` → `id`, `HTTPCode` → `httpCode`.

7. **`renderDTO`** — `cmd/rxcrud/render.go:96`
   Skips relations, the primary key, `generated` and `immutable` columns —
   exactly the set `crud.PlanFor` would refuse at `Define` time
   (`crud/update.go:113`). That alignment is why the generated DTO always
   validates ([[FL-004]]).

8. **Metamodel attribute types** — `attrType`, `main.go:363`
   | element type | attribute |
   |---|---|
   | `string` | `specs.Str[M]` |
   | `time.Time` | `specs.Cmp[M, time.Time]` |
   | any `cmp.Ordered` kind (`main.go:355`) | `specs.Ord[M, T]` |
   | anything else | `specs.Attr[M, T]` |
   The attribute is over the *element* type, so `Age *int` gets
   `specs.Ord[User, int]`.

9. **`renderMetamodel` → `renderAttrs`** — `render.go:128`, `render.go:144`
   One attribute struct per (root model, relation path), named
   `<Root><ConcatenatedPath>Attrs`. Relation expansion stops on three
   conditions:
   - depth: `level+1 >= g.depth` (`render.go:163`), `-depth` defaults to 2;
   - the related model is not in this package (`render.go:169`) — nothing to
     expand into;
   - **a cycle**: the target already appears on the current path
     (`render.go:172-183`). `Article → Author → Articles → …` has no end.
   Nested structs are emitted before the parent so the file reads top-down, and
   `emitted` stops the same type being written twice. The file ends with
   `var Article_ = specs.Metamodel[Article, ArticleAttrs]()`, which validates the
   attribute struct against the model at package initialisation — a renamed
   field then breaks the build rather than a request.

10. **`render` and the import block** — `render.go:11`
    `used` flags decide `time`, the `crud` package and the `specs` package.
    `-import` is added when the output lands elsewhere. `extraImports`
    (`render.go:75`) walks every column type for a `pkg.Type` prefix and pulls
    the matching path out of the import map collected in `load` — a `uuid.UUID`
    or `decimal.Decimal` column would otherwise dangle. Imports are sorted, then
    `format.Source` runs; a formatting failure is reported with the offending
    source attached.

## Where the decisions bite

- **The DTO's exclusions must match the runtime's.** `renderDTO` skips exactly
  what `collectPlanFields` refuses. Add a tag option that the plan rejects, and
  the generator has to learn it in the same change or `Define` panics on
  generated code.
- **Nullable means `Opt`, always.** Collapsing it to `*T` because "the field is
  already a pointer" is the one edit that silently removes a feature.
- **The output must be byte-identical across runs.** Sorted model order, sorted
  imports, `emitted` guarding duplicates. `TestOutputIsByteIdenticalAcrossRuns`
  and the `TestTheGeneratedStoresAreUpToDate` /
  `TestGeneratedFileIsUpToDate` checks in the tree depend on it.
- **The generator reads text, not types.** `exprString` (`main.go:314`) renders
  the type as written. A type alias, a dot-import or a type from a package the
  file does not import will be reproduced literally and fail to compile in the
  output. That is the boundary of this tool.
- **`-into` + `-import` are a pair.** Writing `UserUpdate` into ent's own
  package would collide with ent's update builder, and `ent generate` owns that
  directory.

## Failure modes

| What goes wrong | Where it is caught | What the caller sees |
|---|---|---|
| directory does not parse | `parser.ParseDir` (`main.go:178`) | `rxcrud: <parse error>`, exit 1 |
| no tagged models, and no `-types` | `run` (`main.go:163`) | `no tagged models found in <dir>` |
| `-into` without `-import` | `run` (`main.go:155`) | `-into needs -import …` |
| generated source does not parse | `render` (`render.go:65`) | the error plus the full generated text |
| a column type from an unimported package | not caught | the output does not compile |
| an embedded type from an unknown package | `embedded` returns false | the field is silently absent from DTO and metamodel |
| metamodel field that no longer maps | `specs.Metamodel` at package init | panic at start-up in the consumer's package |

## Files

| File | Role |
|---|---|
| `cmd/rxcrud/main.go` | flags, the two-pass load, `parseModel`, `embedded`, `elem`, `dtoType`, `attrType`, `qual` |
| `cmd/rxcrud/render.go` | `render`, `renderDTO`, `renderMetamodel`, `renderAttrs`, `extraImports` |
| `crud/update.go` | `collectPlanFields` — the rules the DTO has to satisfy |
| `crud/meta.go` | the tag vocabulary and the runtime's own embedded-struct flattening |
| `repo/decorators/specs/metamodel.go` | `Metamodel`, and the attribute types the generator emits |
| `_examples/example/blog/rxcrud_gen.go`, `test/entstore/`, `test/gormstore/` | checked-in output, verified up to date by tests |
| `_examples/entstore/`, and the `rxcrud_gen.go` in each `_examples/*-*/` stack | the same generator run the usage guides tell a consumer to run, checked in so an example is readable without running anything |

## Tests that walk this flow

- `TestUpdateDTOFollowsNullability` — `cmd/rxcrud/gen_test.go` — `*T` vs `Opt[T]`.
- `TestUpdateDTOLeavesOutWhatCannotBeWritten` — `cmd/rxcrud/gen_test.go` — PK, generated, immutable.
- `TestTheVersionColumnIsLeftOutOfTheDTO` — `cmd/rxcrud/gen_test.go` — the lock leaves the DTO and stays in the metamodel.
- `TestTheDeclarationAGeneratorProducesForAVersionedModelIsAccepted` — `repo/basic/version_test.go` — the other half of the loop: what the generator now emits is what `Define` accepts, with a control that naming the lock is still refused.
- `TestReadonlyKeepsAFieldQueryableButNotWritable` — `cmd/rxcrud/gen_test.go`.
- `TestSkipRemovesAFieldEverywhere` — `cmd/rxcrud/gen_test.go`.
- `TestRelationsBecomeNestedAttributeStructs` — `cmd/rxcrud/gen_test.go`.
- `TestRelationCyclesAreCutShort` — `cmd/rxcrud/gen_test.go`.
- `TestDepthBoundsHowFarRelationsExpand` — `cmd/rxcrud/gen_test.go`.
- `TestEmbeddedStructsAreFlattened` — `cmd/rxcrud/gen_test.go`.
- `TestGormModelIsFlattenedFromTheWellKnownTable` — `cmd/rxcrud/gen_test.go`.
- `TestIntoAnotherPackageQualifiesTheModelTypes` — `cmd/rxcrud/gen_test.go` — `qual` and the import block.
- `TestIntoAnExistingPackageKeepsItsName` — `cmd/rxcrud/gen_test.go` — `packageNameOf`.
- `TestIntoWithoutImportIsRefused` — `cmd/rxcrud/gen_test.go`.
- `TestAPackageWithNothingToGenerateIsAnError` — `cmd/rxcrud/gen_test.go`.
- `TestGeneratingOnlyOneHalf` — `cmd/rxcrud/gen_test.go` — `-no-dto` / `-no-meta`.
- `TestOutputIsByteIdenticalAcrossRuns` — `cmd/rxcrud/gen_test.go`.
- `TestGeneratedCodeCompilesAndValidates` — `cmd/rxcrud/gen_test.go` — the end-to-end guarantee.
- `TestGeneratedFileIsUpToDate` — `_examples/example/blog/blog_test.go`.
- `TestTheGeneratedStoresAreUpToDate` — `test/integration/codegen_test.go` — the ent and gorm stores.
- `TestGeneratedDTOTypesFollowNullability` — `_examples/example/blog/blog_test.go` — the generated types, from the consumer's side.

## See also

[[FL-004]] [[FL-002]]
