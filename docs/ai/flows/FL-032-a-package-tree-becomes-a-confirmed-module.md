# FL-032 — A package tree becomes a confirmed module

**Entry point:** `cmd/vv/main.go:runModule` → `internal/codegen.RunModule`
**Implements:** [[UC-014]] · **Governed by:** [[D-110]] [[D-106]] [[D-109]] [[D-096]]

[[FL-029]] derives what a client may send from a model, [[FL-031]] derives what
a route may claim from the check that runs. This one derives what a bounded
context contributes to a container from what its packages actually construct.
[[FL-030]] is what happens to the value afterwards.

## The path

1. **`runModule`** — `cmd/vv/main.go:104`
   `generate module` is its own subcommand: `-dir`, `-out`
   (`vv_module_gen.go`), `-manifest` (`module.manifest.yml`), `-name` (the
   module's name, defaulting to the directory), `-order`, `-import` (the import
   path of `-dir`, read from the nearest `go.mod` when left out), `-module` (the
   package that owns `Definition`), the four marker types `-check-type`,
   `-route-type`, `-worker-type`, `-seeder-type`, `-recursive` (**off**) and
   `-check`.

2. **`RunModule`** — `internal/codegen/module.go:133`
   ```
   artifactName → runModuleTree (recursive) | runOneModule
   ```

3. **`runModuleTree`** — `internal/codegen/module.go:151`
   `-recursive` here is one level, not a walk: `moduleDirs` lists the
   directories directly under `-dir` that hold Go files, and each is one module
   ([[D-110]] says why). `-name` and `-import` are refused with it, because they
   are per-module. The loop collects rather than stops — every contribution
   waiting for a person, prefixed by its directory, and under `-check` every
   stale module's paths — and a confirmation outranks drift, the same priority
   as [[FL-029]] and [[FL-031]]. A directory with nothing a container could
   build is skipped rather than failing the walk.

4. **`runOneModule`** — `internal/codegen/module.go:190`
   ```
   containedOutputPath → validateGeneratedTarget → moduleImportPath
     → discoverModule → readModuleManifest → buildModuleManifest
     → unconfirmed? → write the manifest, write a file that will not compile, refuse
     → renderModule → validateModuleDeclarations → check or write
   ```

5. **`moduleImportPath`** — `internal/codegen/module.go:289`
   Walks up for a `go.mod`, reads its `module` line and appends the relative
   path. Offline, and the reason `-import` exists is the tree that has no
   `go.mod` above it.

6. **`discoverModule`** — `internal/codegen/module.go:330`
   `packagedDirs` lists every directory in the subtree holding non-test Go
   files, skipping `ignoredDir` names. Each is parsed with `parser.ParseDir`
   without type information (the same reason [[D-109]] gives), test files,
   `*_gen.go` files and the generated output itself left out. One package per
   directory or the directory is refused. The root directory gives the package
   the generated file is written into; every other directory gives an import,
   aliased by its package name — two packages sharing a name, or one named
   `vvmodule`, is refused rather than shadowed.

7. **`collectContributions` and `namedResultType`** —
   `internal/codegen/module.go:395`, `:457`
   A candidate is a top-level function with no receiver, no type parameters and
   a body, exported unless it is in the module's own package, whose first result
   unwraps through `*`, `[…]` and generic instantiation to a named type.
   `unnamedResultTypes` is the list that stops there: `error`, `any`, the
   builtins. A slice, a map, a channel or a bare interface never reaches it.

8. **`markedKind`** — `internal/codegen/module.go:484`
   The result type's qualifier is resolved through the file's own import
   aliases, and `path.Name` is looked up in the marker table built from the four
   flags. A hit is `check`, `route`, `worker` or `seeder`; a miss is `provide`.
   `contributionFingerprint` (`:501`) covers the symbol, that inferred kind and
   the whole signature.

9. **`buildModuleManifest`** — `internal/codegen/module.go:510`
   Per candidate:
   | the prior row is | the row becomes |
   |---|---|
   | absent | `source: inferred-from-signature`, `confirmed: false` |
   | present, fingerprint unchanged, same kind | confirmation carried |
   | present, fingerprint unchanged, kind rewritten by hand | that kind, `source: declared-in-manifest`, confirmation carried |
   | present, fingerprint changed | inferred again, confirmation dropped |
   `excluded` is carried across a fingerprint change — it is a statement about
   the symbol, not its signature — and an excluded row is never confirmed and
   never waited for. A kind the manifest gives that is not one of the five is
   refused by name. A module whose every row is excluded is refused here rather
   than becoming a `Definition` `Define` would reject.

10. **The confirmation gate** — `unconfirmedContributions`,
    `internal/codegen/module.go:567`
    Any included row with `confirmed: false` becomes one entry of a
    `ModuleConfirmationError`. The manifest is written first, so the author has
    the lines to edit, and `unconfirmedModule` (`:657`) replaces the generated
    file with

    ```go
    type vvModule []struct{}

    var VVModule vvModule = "confirm every contribution in module.manifest.yml"
    ```

    which is a compile error whose text is the instruction — [[FL-025]]'s shape,
    taken for [[D-110]]'s reason. Under `-check` neither file is touched.

11. **`renderModule`** — `internal/codegen/module.go:662`
    Included rows are bucketed by kind into `Provide`, `Routes`, `Workers`,
    `Seeders` and `Checks`, in that order, and written as one
    `vvmodule.MustDefine(vvmodule.Spec{…})` bound to `VVModule`. The imports are
    the module package under the alias `vvmodule` plus every subpackage a
    rendered symbol names.

12. **`validateModuleDeclarations`** — `internal/codegen/module.go:646`
    `packageDeclarationNames`, the same collision check the other three
    generators use: `VVModule` must be free.

13. **What reads the value** — [[FL-030]]
    `appfx.Option(VVModule, profile)` activates the kinds the profile carries.
    Nothing here calls a constructor, and nothing here knows what a container
    is.

## Where the decisions bite

- **The package tree is the source, the module is derived.** [[D-073]]'s rule
  again: a hand-written `fx.Module` sheet would make the document the source.
- **The inference asks.** A result type is weak evidence of intent, so nothing
  runs on an inference nobody signed ([[D-110]], following [[D-109]]).
- **Excluding is an answer, not a workaround.** A gate that can only say yes
  becomes a ritual, and `excluded: true` is how a person says no once.
- **The marker types are strings.** `internal/codegen` imports neither `health`,
  `runtime`, `app` nor `appfiber` — the last of which is a satellite module the
  root could not import if it wanted to ([[D-096]]).
- **An unconfirmed module breaks the build; an unconfirmed wire body does not.**
  The same split as [[FL-031]], for the same reason: a constructor that is not
  wired is a feature that does not exist.

## Failure modes

| What goes wrong | Where it is caught | What the caller sees |
|---|---|---|
| `-out` or `-manifest` naming a directory, or escaping `-dir` | `artifactName`, `containedOutputPath` | the same refusals as [[FL-029]] |
| the output file is a symlink, or authored | `validateGeneratedTarget` | `refusing symlink output` / a refusal to overwrite |
| an authored file under the manifest's name | `validateModuleManifestTarget` | `refusing to overwrite an unrelated manifest` |
| the manifest belongs to another package, or names a symbol twice | `readModuleManifest` | the path, and what it claims |
| no `go.mod` above `-dir` and no `-import` | `moduleImportPath` | the directory, and that `-import` is what answers it |
| two packages in one directory | `discoverModule` | the directory and every package name in it |
| two subpackages with the same package name, or one named `vvmodule` | `discoverModule` | both import paths and the name they would share |
| `-dir` is not a Go package | `discoverModule` | the directory, and that there is nothing to declare the module in |
| nothing in the tree returns a named value | `runOneModule` | `no constructor in <dir>` |
| a kind written into the manifest that is not one of the five | `buildModuleManifest` | the symbol, the kind and the five |
| every row excluded | `buildModuleManifest` | `contributes nothing` |
| an inferred kind nobody confirmed | `unconfirmedContributions` | `ModuleConfirmationError`, and a generated file that will not compile |
| a symbol naming a package outside the module | `renderModule` | the symbol and the qualifier |
| `VVModule` already declared in the package | `validateModuleDeclarations` | the package and the name |
| an artefact behind its source under `-check` | `checkArtifacts` | `DriftError` naming both paths, nothing written; under `-recursive` every stale module's paths in one error |
| `-recursive` with `-name` or `-import` | `runModuleTree` | both flags are per-module and the walk names every module after its directory |

## Files

| File | Role |
|---|---|
| `cmd/vv/main.go` | `runModule` and the `generate module` flag set |
| `internal/codegen/module.go` | `RunModule`, `runModuleTree`, `runOneModule`, `moduleImportPath`, `discoverModule`, `packagedDirs`, `moduleDirs`, `collectContributions`, `namedResultType`, `markedKind`, `contributionFingerprint`, `buildModuleManifest`, `includedContributions`, `unconfirmedContributions`, `unconfirmedModule`, `renderModule`, `validateModuleDeclarations`, `readModuleManifest`, `validateModuleManifestTarget`, `ModuleConfirmationError` |
| `internal/codegen/routes.go` | `importAliases`, `sourceFromManifest` — shared with [[FL-031]] |
| `internal/codegen/codegen.go` | `containedOutputPath`, `validateGeneratedTarget`, `writeArtifact`, `writeGenerated`, `packageDeclarationNames`, `exprString`, `ignoredDir` — shared with [[FL-010]] |
| `internal/codegen/resource.go` | `artifactName`, `checkArtifacts`, `DriftError` — shared with [[FL-029]] |
| `app/module/module.go` | `Spec`, `MustDefine` — what the generated file produces ([[FL-030]]) |
| `internal/cachegen/render.go` | the precedent for a generated file that refuses to compile ([[FL-025]]) |

## Tests that walk this flow

- `TestTheKindOfAContributionIsInferredFromWhatItsConstructorReturns` —
  `internal/codegen/module_test.go`, with the helper, the primitive result and
  the unexported subpackage function as controls.
- `TestAnUnconfirmedModuleLeavesAFileThatWillNotCompile`.
- `TestAConfirmedModuleBecomesOneDefinitionSortedIntoTheKindItWasGiven`.
- `TestChangingWhatAConstructorReturnsWithdrawsOnlyItsOwnConfirmation`.
- `TestAKindWrittenIntoTheManifestOutranksTheOneInferredFromTheSignature`.
- `TestAContributionExcludedByHandIsNeitherWaitedForNorGenerated`.
- `TestAModuleWhoseEveryContributionIsExcludedIsRefusedRatherThanWritten`.
- `TestTheModuleCheckReportsDriftWithoutWriting`.
- `TestAPackageThatAlreadyDeclaresTheModuleVariableKeepsItsOwn`.
- `TestTheGeneratedDefinitionCompilesAndActivatesByRole` — a real module,
  generated, built and run.
- `TestTheRecursiveModuleRunNamesEveryContributionWaitingAndSaysWhichModuleItIsIn`,
  `TestTheRecursiveModuleCheckNamesEveryStaleModuleAndNotOnlyTheFirst` —
  `internal/codegen/check_test.go`.
- `TestTheModuleSubcommandRefusesAContributionNobodyConfirmed`,
  `TestTheModuleSubcommandHasItsOwnFlags` — `cmd/vv/main_test.go`.

## See also

[[FL-030]] [[FL-031]] [[FL-029]] [[FL-025]] [[FL-010]]
