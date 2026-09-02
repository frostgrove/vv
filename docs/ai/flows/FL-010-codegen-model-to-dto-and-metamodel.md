# FL-010 — Codegen: a model becomes a DTO and a metamodel

**Entry point:** `cmd/vv/main.go:main` → `internal/codegen.Run`
**Implements:** [[UC-014]] [[UC-010]] [[UC-007]] · **Governed by:** [[D-018]] [[D-002]] [[D-014]] [[D-050]]

This flow is the **persistence** half: `<Model>Update` is what an `UPDATE` may
write, and it is not a promise to a client. The public bodies — create, patch
and response — are a second command with a manifest of its own, and they are
[[FL-029]].

`vv` keeps the AST as its source of declarations and never executes or imports
the package it generates for. It also builds a best-effort `go/types` view from
module export data. Resolved fields therefore use the same structural and
interface classification as runtime reflection, while an unrelated incomplete
function body does not prevent generation.

## The path

1. **`main`** — `cmd/vv/main.go:55`
   Flags into a `generator`. `-into` moves the output; the output path is
   `filepath.Join(into or dir, out)`. When `-import` is present, the package
   declaration in `-dir` — not the import path's basename — is the preferred
   model qualifier. Thus a path ending in `/v2` with `package models` normally
   becomes `models ".../v2"`; a reserved/colliding name receives a readable
   path-derived alias.

2. **`generator.run`** — `internal/codegen/codegen.go:149`
   The target is first constrained to one basename inside `-dir`/`-into` and an
   existing file must carry the generated header; authored files, symlinks and
   traversal are refused before they can be excluded from parsing. Then `load`
   and the `-into` rules: it requires `-import`, because the generated
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
   - Between the passes, `prepareImports` records each file's qualifier → path
     mapping. Unaliased versioned paths are resolved through `go list` when the
     basename is not the qualifier used by the file. Paths are sorted and
     receive readable path-derived output aliases; two source files may both
     call different packages `common`, while the one generated file uses
     `alphaCommon` and `betaCommon`, never numeric suffixes. Dot imports are
     refused, and aliases that Go would reject against an authored or generated
     package declaration are reported before output is written.
   - `prepareTypes` type-checks declarations with function bodies ignored. Its
     module-aware importer reads `go list -deps -export` data through one cache,
     preserving package identity for `Valuer`, `Scanner` and text interfaces.
     Type errors unrelated to a field being generated are tolerated; an
     anonymous field that remains unresolved is handled fail-loud in pass two.
   - **Pass two** runs `parseModel` on every struct that `-types` allows and
     rewrites type qualifiers through that declaration file's mapping.
   - `sort.Strings(g.order)` — map iteration is not ordered, and the output has
     to be byte-identical across runs.

4. **`generator.parseModel`** — `internal/codegen/codegen.go:242`
   A struct becomes a model if it carries `db` or `rel` tags, lives exported in
   `model.go`, `*.model.go` or `*_model.go`, **or** was named explicitly in
   `-types`. The last door is what makes a generated entity from another tool
   qualify.
   Per field:
   - a completely untagged anonymous non-scalar value struct → `flattenType`
     (below), and the flattened fields are spliced in;
   - an anonymous scalar or explicitly tagged anonymous field → the ordinary
     column/relation path, so the tag is never discarded by flattening;
   - unexported and untagged → dropped; an explicit `db` mapping is refused,
     because reflection cannot read it;
   - named in `-skip` → kept with `Skip` set, so it is absent from every
     declaration and its name still reaches the exclusion list (step 7).
   - named in `-readonly` → `Immutable`, so it stays filterable and sortable but
     leaves both wire shapes.
   - **no `rel` tag and a base type that is a struct in this package → dropped**
     (`internal/codegen/codegen.go:278-282`). Neither a column nor an edge.
   - any present struct-shaped `rel` tag other than `-`, including `rel:""` →
     kept as a relation field; empty retains runtime kind inference and local
     aliases retain the canonical target model;
   - a non-struct with non-`-` `rel` → refused; scalar `rel:"-"` remains a
     column, matching runtime order;
   - otherwise a column: `db` options `pk auto noauto immutable generated
     version` are read. `version` (spelled `version` or `lock`) is the optimistic
     lock — the same option `crud/meta.go` reads. The generator has to know it
     because `crud.PlanFor` *refuses* a DTO that names that column: emitting it
     produced a package that panicked at `Define` time. That is what happens when
     two features land in one change and neither knows about the other.
   - after every flattened field is known, `resolvePrimaryKey` makes the same
     model-wide choice as runtime metadata: one explicit `pk` wins; otherwise
     the exact `ID` field, exact `ID` column, then exact `id` column wins. A selected integral
     key defaults to `auto` unless `noauto` opts out. Resolving after flattening
     prevents an ordinary `ID` column from stealing a differently named explicit
     string/UUID key and makes multiple explicit keys a deterministic error.

5. **Anonymous-field classification and flattening** — `sourceTypes` and
   `generator.flattenType`, `internal/codegen/types.go`
   The rule is shared with `crud.collectFields`: flatten only an anonymous
   value struct with no `db` tag, no `rel` tag, and no scalar semantics.
   `time.Time`, driver `Valuer`/`Scanner` and text marshal/unmarshal method-set
   shapes follow runtime's exact receiver asymmetry, including scalar pointers. An explicit
   relation belongs to the anonymous field; it is not flattened first.

   `go/types` follows local aliases and instantiated generic bases and reads
   exported dependency struct fields and tags, so resolvable external mixins
   flatten without a registry. `gorm.Model` retains its audited semantic
   override. An untagged pointer to a non-scalar struct is refused, as it is by
   runtime metadata. An unresolved anonymous type, or an exported embedded
   column whose private named type or anonymous structural member identity
   cannot be reproduced from the generated package, is a generation error.
   `db:"-"` is the explicit whole-field opt-out and is
   honoured before type resolution. Problems are collected and sorted so a
   package with several bad fields gets one deterministic diagnostic. After
   flattening, effective Go field names and database columns are checked for
   duplicates before rendering, mirroring runtime Schema refusal.

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
    | `<Model>Input` | the entity body: `inputFields` — every column that is not a relation, not `generated`, not the lock, not a database-owned primary key and not on the exclusion list — under `lowerFirst` JSON names; an assigned string/UUID key or integral `noauto` key remains |
    | `<Model>Mapper` | `Model(ctx, in) (M, error)`, field-for-field assignments on a zero model value (which also reach promoted fields from flattened mixins), plus `Resolve` delegating to the map, so it satisfies `port.Mapper` **and** `errs.Resolver` |
    | `<Model>Paths` | `port.MustPathMap[M](port.PathMap{…}, "ID", "CreatedAt")` — the inverse, plus declared exclusions and the omitted database-owned key |
    | `<Model>Service` | a struct embedding `*port.DefaultService[M, ID, U]`, with `var _ port.Service[…]` beside it so an override that changes a signature is a build failure |
    | `Mount<Model>` | `crudnet.ServingFor(svc, <Model>Mapper{}, opts...).Mount(mux, prefix)` — it takes a built service, so it uses `ServingFor` and cannot trip `port.Rules.RefuseServiceOptions` |
    The id type comes from the primary key's `Type` as written, through
    `model.pk`; a model the generator cannot find a key on is an error rather
    than a file that does not compile. The name is `Mount<Model>` singular:
    pluralising in a generator is a guess, and `MountCategorys` is what guessing
    looks like.

    Key ownership is the result of step 4, not a second adapter heuristic.
    Explicit `auto` is database-owned; an integral explicit/conventional key is
    also auto by default; `noauto` keeps that integral key client-owned. Thus the
    input, mapper and inverse map consume the same resolved field set, while the
    generated exclusion tells runtime totality why a database-owned key is absent.
    If that set is empty, the generator emits `Input struct{}`, a zero-value
    mapper and `PathMap{}` with exclusions; zero fields is still a total mapping.

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
    `-import` is added with the parsed model package name when the output lands
    elsewhere. `extraImports` parses the complete rendered body and walks every
    surviving package selector, including selectors nested in composite and
    generic types. Parser-resolved local selectors such as the adapter's
    `out.ID` are excluded. Imports are merged by path, so a model type and
    generated support code cannot emit the same package twice. Every
    non-canonical import carries its explicit assigned alias. Imports are
    sorted, then `format.Source` runs. `validateDeclarations` and
    `validateRenderedImports` check source/destination file import aliases,
    authored package declarations, generated declarations and the actual final
    import block in both forbidden directions. A formatting or namespace
    failure is reported before any output write.

13. **`-check`** — `Options.Check`, `checkArtifacts` (`internal/codegen/resource.go`)
    Everything above runs; the rendered bytes are compared with the file on disk
    instead of replacing it. A file that differs, or does not exist at all, is a
    `DriftError` naming the path, and nothing is written — including in
    `-recursive` mode, where every package behind its models is named rather
    than only the first. This is the same flag and the same error type
    `vv generate resource` uses ([[FL-029]]).

14. **`writeGenerated` — owned atomic replacement**
    The candidate is written beside the target, chmodded, synced and closed,
    target ownership is revalidated, and an atomic rename replaces only a
    generated regular file. The destination directory is synced afterwards;
    failed writes remove the temporary candidate.

## Where the decisions bite

- **The DTO's exclusions must match the runtime's.** `renderDTO` skips exactly
  what `collectPlanFields` refuses. Add a tag option that the plan rejects, and
  the generator has to learn it in the same change or `Define` panics on
  generated code.
- **Database-owned identity is not a request field.** `resolvePrimaryKey`
  mirrors runtime's explicit-key / exact-`ID` field-or-column / exact-`id` selection and its
  integral-auto default; `inputFields` drops the resolved auto key and
  `inputExclusions` states that omission to the runtime path-map check. A
  `noauto` integral key or genuinely assigned string/UUID key stays in the
  input, mapper and map.
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
- **The AST remains authoritative; type information classifies it.** Resolved
  fields are rendered with `types.TypeString`; the syntactic fallback rewrites
  every selector in one AST pass so `crud → alpha → beta` mappings cannot
  cascade. `prepareImports` also resolves declared package names for unaliased
  version paths. Dot imports are refused because their unqualified identifiers
  cannot be reproduced safely in one generated file.
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
| an unresolved anonymous type | `load`, after `parseModel` records the unresolved field | generation fails naming the model/type and the resolve/flatten/`db:"-"` remedies |
| an exported embedded column has a private named type or an anonymous structural type with a foreign unexported member | `flattenType` | generation fails before emitting a type the output package cannot reproduce |
| an embedded pointer struct | `parseModel` | generation fails before runtime metadata can refuse the generated package |
| flattening produces duplicate effective Go fields or database columns | `validateEffectiveFields` | generation fails deterministically before a duplicate-field Go file is written |
| a source/destination import alias collides with an authored or generated package declaration, or a rendered import collides in the reverse direction | `validateDeclarations` / `validateRenderedImports` | generation fails before writing and names the alias/path; `-into` inspects both declarations and file imports in its output package |
| two relation paths or models derive the same generated declaration | `validateDeclarations` | generation fails naming the colliding generated declaration and both owners |
| metamodel field that no longer maps | `specs.Metamodel` at package init | panic at start-up in the consumer's package |
| a relation handle declaring the wrong target model | `bindRel` at package init | panic naming the path, the model it reaches and the one declared |
| a relation handle in the root attribute group | `bindRel` at package init | panic saying the root model is not a relation |
| a target model with a column called `Path` | not caught; the generated doc comment says so | `Article_.Comments.Path()` does not compile for that relation; `RelPath()` does |
| a column the model gained, with nothing regenerated | `port.MustCoverUpdate` at package init | panic at start-up naming the column ([[D-050]]) |
| a column the inverse map does not cover, or an entry naming one no request carries | `port.MustPathMap` at package init | panic at start-up naming the entry |
| `-adapter` on a model with no key the generator can name | `renderAdapter` | `-adapter needs a key it can name: tag one field of X db:",pk"` |
| `-adapter` with `-no-dto` | `run` | `-adapter needs the update DTO; drop -no-dto` |
| `-binding` naming anything but `net` or `none` | `Run` | `-binding X: only net and none are generated today` |
| an artefact behind its model under `-check` | `checkArtifacts` | `DriftError` naming every stale path, nothing written |

## Files

| File | Role |
|---|---|
| `cmd/vv/main.go` | the flags, and nothing else — it fills a `codegen.Options` and calls `Run` |
| `internal/codegen/codegen.go` | `Options`, `Run`, the two-pass load, import/declaration validation, `parseModel`, `elem`, `dtoType`, `attrType`, `qual` |
| `internal/codegen/types.go` | module-aware export-data importer, scalar/relation classification, alias/generic flattening and inaccessible-type refusal |
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
- `TestUnknownExternalEmbeddedStructIsRefusedDuringGeneration` /
  `TestEmbeddedPointerIsRefusedLikeRuntimeMetadata` /
  `TestUnknownExternalEmbedCanBeExplicitlyExcluded` —
  `internal/codegen/codegen_test.go`.
- `TestAnonymousTypeClassificationMatchesRuntimeAndGeneratedPackageCompiles` /
  `TestFlattenedFieldAndColumnCollisionsFailBeforeRendering` /
  `TestExternalEmbedWithAnInaccessibleColumnTypeFailsAtGeneration` — resolved
  aliases (including relation targets), empty relation tags, generic/external
  bases, scalar receiver asymmetry, structural accessibility and the fail-loud boundary.
- `TestIntoAnotherPackageQualifiesTheModelTypes` — `internal/codegen/codegen_test.go` — `qual` and the import block.
- `TestIntoUsesTheDeclaredPackageNameForAVersionedImportPath` /
  `TestVersionedColumnImportReadsTheDeclaredPackageName` — the model package
  and a column package whose paths end in `/v2`.
- `TestRenamedSourceImportKeepsItsAliasInGeneratedCode` /
  `TestImportAliasCollisionsAcrossSourceFilesAreMadeStable` /
  `TestReadableCollisionAliasesCompile` — explicit aliases, readable path
  names, transitive selectors and two source-file-local aliases sharing one
  generated import block.
- `TestSourceImportAliasCollidingWithGeneratedDeclarationIsRefused` /
  `TestIntoDestinationImportAliasCollidingWithGeneratedDeclarationIsRefused` /
  `TestIntoGeneratedImportAliasCollidingWithDestinationDeclarationIsRefused` /
  `TestAuthoredDeclarationCollidingWithGeneratedDeclarationIsRefused` /
  `TestConcatenatedRelationDeclarationCollisionIsRefused` — declarations that
  no import-block rewrite can make legal are refused before write.
- `TestIntoAnExistingPackageKeepsItsName` — `internal/codegen/codegen_test.go` — `packageNameOf`.
- `TestIntoWithoutImportIsRefused` — `internal/codegen/codegen_test.go`.
- `TestOutputNameCannotEscapeItsControlledDirectory` /
  `TestAuthoredOutputIsNeverOverwritten` /
  `TestSymlinkOutputIsRefusedWithoutFollowingIt` /
  `TestGeneratedOutputIsAtomicallyReplaceable` — target ownership and atomic
  persistence through the public `Run` seam.
- `TestAPackageWithoutModelFilesIsAnError` — `internal/codegen/codegen_test.go`.
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
- `TestAColumnTheGeneratedFileNeverSawIsNamedInsteadOfWrittenOver`, `TestAPackageWithNoGeneratedFileAtAllFailsTheCheckWithoutCreatingOne` and `TestTheRecursiveCheckNamesEveryPackageBehindItsModelsAndNotOnlyTheFirst` — `internal/codegen/check_test.go` — `-check`, each with the freshly generated tree passing as its control and each asserting the check wrote nothing.

## See also

[[FL-004]] [[FL-002]] [[FL-015]] [[FL-029]]
