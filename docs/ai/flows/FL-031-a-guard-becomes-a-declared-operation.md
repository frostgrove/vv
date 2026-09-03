# FL-031 — A guard becomes a declared operation

**Entry point:** `cmd/vv/main.go:runRoutes` → `internal/codegen.RunRoutes`
**Implements:** [[UC-014]] · **Governed by:** [[D-109]] [[D-107]] [[D-073]] [[D-050]]

[[FL-029]] derives what a client may send from a model. This one derives what a
route may claim from the check that actually runs. The subject is the operation
that is *not* a CRUD resource: a use case that calls a guard inside its own body,
with a route declared beside it by hand.

## The path

1. **`runRoutes`** — `cmd/vv/main.go:81`
   `generate routes` is its own subcommand: `-dir`, `-out`
   (`vv_routes_gen.go`), `-manifest` (`routes.manifest.yml`), `-guard` (the
   import path whose function a use case calls, `auth/access` by default),
   `-guard-func` (`Require`), `-declare` (`auth/http/authhttp`), `-auth` (the
   package that owns `Permission`), `-recursive` (on) and `-check`. It fills a
   `codegen.RouteOptions` and calls `RunRoutes`.

2. **`RunRoutes`** — `internal/codegen/routes.go:113`
   ```
   artifactName → containedOutputPath → validateGeneratedTarget → discoverRoutes
     → readRoutesManifest → buildRoutesManifest
     → unconfirmed? → write the manifest, write a file that will not compile, refuse
     → renderRoutes → validateRouteDeclarations → check or write
   ```
   Recursive mode walks `guardedDirs`, which greps for `.<guard-func>(` rather
   than parsing every package, and runs once per directory that has one. A
   directory whose match turns out to be something else is skipped rather than
   failing the walk, and confirmations are collected across every package and
   reported once, prefixed by directory — otherwise a tree of guarded packages
   would need one run per package to see what it is waiting for. Drift under
   `-check` is collected the same way, and a confirmation outranks it: nothing
   is worth comparing while an operation is still waiting for a person.

3. **`discoverRoutes`** — `internal/codegen/routes.go:227`
   One package per directory, files sorted, `parser.ParseDir` without type
   information ([[D-109]] says why). Two collectors run over each file:
   - **`collectGuards`** walks every `FuncDecl` body for a call to
     `<alias>.<guard-func>(…)` where the alias resolves to `-guard`. The
     enclosing method names the operation — `DeadJobsUseCase.List`, receiver
     type and method — and the arguments after the context are the policy, kept
     as the expressions a person wrote. Two guards in one body with different
     policies is an error naming both positions: one operation cannot mean two
     things.
   - **`collectDeclarations`** walks the whole file for
     `<alias>.Requires(method, path, …)` against `-declare`. The method is a
     string literal or a `…MethodGet`-shaped selector; the path must be a
     literal. A call the generator cannot read is not guessed at — it is not a
     declaration as far as this flow is concerned.

   `boundOperation` is the third shape: `Require(ctx, OperationX.Permissions()...)`
   is not a policy at all, it is a use case that has already adopted the
   generated carrier.

   Both collectors read the aliases of the file they are in, because that is
   where an import name is bound. The package-wide map exists only for the
   generated file's own import block, and `refuseAmbiguousQualifiers` is what
   keeps it honest: an alias two files bind to two different paths holds no
   entry there at all, and a policy that names such an alias is refused with
   both paths and both file names. Taking whichever file sorted first is how a
   guard on `perm.Read` gets published under a `perm` from somewhere else. A
   collision on an alias no policy is written with changes nothing and is left
   alone.

4. **`buildRoutesManifest`** — `internal/codegen/routes.go:526`
   Declarations are indexed by their sorted policy. Per guard:
   | the guard is | the row is |
   |---|---|
   | bound to `OperationX` | policy and route carried from the manifest, `source: bound-to-operation`, confirmed by construction |
   | paired with exactly one declaration | `source: inferred-from-guard`, method and path from that declaration |
   | paired with none or several, with a prior row | `source: declared-in-manifest`, method and path from the manifest |
   | paired with none, with no prior row | routeless, and therefore unconfirmable |
   `routeFingerprint` covers the operation name, the sorted policy and the
   *inferred* method and path — never the ones a person supplied. `confirmed` is
   carried from the prior row only when that fingerprint is unchanged.

5. **The unenforced-declaration refusal** — same function
   Every declaration whose policy no guard in the package enforces becomes one
   line of an `UnenforcedDeclarationError`, with its method, path, policy and
   source position. Nothing is written.

6. **The confirmation gate** — `unconfirmedOperations`,
   `internal/codegen/routes.go:607`
   Any row with `confirmed: false` becomes one entry of a
   `RouteConfirmationError`. The manifest is written first, so the author has
   the lines to edit, and then `unconfirmedRoutes` replaces the generated file
   with

   ```go
   type vvRouteSet []struct{}

   var VVRouteSet vvRouteSet = "confirm every operation in routes.manifest.yml"
   ```

   which is a compile error whose text is the instruction. Under `-check`
   neither file is touched.

7. **`renderRoutes`** — `internal/codegen/routes.go:712`
   One `Operation` value type per package — `Name`, `Method`, `Path`,
   `Permissions` (a copy, so a caller cannot edit the carrier) and `Endpoint`,
   which is `authhttp.Requires` or, for a policy that is empty,
   `authhttp.Authenticated` for the same reason [[D-107]] gives. Then one
   exported `Operation<Name>` var per row, `Operations()` and `Declarations()`.
   A permission written as `pkg.Perm` pulls that package's import path out of
   the package's imports; a qualifier that would collide with `auth` or
   `authhttp` is refused rather than shadowed, and so is one whose alias names
   two packages — the policy a manifest carries for a bound guard reaches this
   function without a file to be read in, so this is where that case is caught.

8. **`validateRouteDeclarations`** — `internal/codegen/routes.go:690`
   `packageDeclarationNames` is the same collision check the model generator
   uses. `Operation`, `Operations`, `Declarations` and every `Operation<Name>`
   must be free.

9. **Adoption, and the end of the confirmation** — the consumer
   The handler returns `Declarations()` instead of a literal list; the use case
   calls `Require(ctx, OperationX.Permissions()...)`. The next run sees a bound
   guard and no literal declarations, writes `source: bound-to-operation`, and
   never asks again. What the confirmation stood in for is now a compiler fact.

10. **The boot gate** — [[FL-024]]
    Nothing here checks that the declared path is the mounted path. That is
    `appfiber`'s job and it already does it against the router's own table, so
    the two halves of an endpoint are covered by different machinery on purpose.

## Where the decisions bite

- **The guard is the source, the route is derived.** [[D-073]]'s rule — what
  runs is the source, the document is the projection — is what makes the arrow
  point this way rather than deriving a check from a manifest.
- **The confirmation is temporary by design.** It exists for the window in which
  a person, and not the compiler, is the only thing that can say those two lines
  are one policy. `bound-to-operation` closes that window ([[D-109]]).
- **An unconfirmed route breaks the build; an unconfirmed wire body does not.**
  The opposite choice from [[FL-029]], for the opposite hazard: a stale
  permission claim is worse than a stale response body.
- **The generator imports no authorization package.** `-guard` is a string.
  `internal/codegen` links to neither `auth/access` nor `authhttp`; only the
  generated file imports them.
- **The fingerprint is over the derivation.** The same rule as
  `derivationFingerprint` in [[FL-029]], for the same reason: a fingerprint over
  the whole row makes the confirmation either permanent or unobtainable.

## Failure modes

| What goes wrong | Where it is caught | What the caller sees |
|---|---|---|
| `-out` or `-manifest` naming a directory, or escaping `-dir` | `artifactName`, `containedOutputPath` | the same refusals as [[FL-029]] |
| the output file is a symlink, or authored | `validateGeneratedTarget` | `refusing symlink output` / a refusal to overwrite |
| an authored file under the manifest's name | `validateRoutesManifestTarget` | `refusing to overwrite an unrelated manifest` |
| the manifest belongs to another package, or names an operation twice | `readRoutesManifest` | the path, and what it claims |
| two packages in one directory | `discoverRoutes` | the directory and every package name in it |
| nothing in the package calls the guard | `RunRoutes` | `no guarded use case in <dir>` |
| one method enforcing two policies | `collectGuards` | both policies and both positions |
| a declaration whose policy no guard enforces | `buildRoutesManifest` | `UnenforcedDeclarationError` naming route, policy and position |
| a row confirmed but carrying no route | `buildRoutesManifest` | the operation, and where to write the route |
| a bound guard with no row in the manifest | `buildRoutesManifest` | the operation and the variable it is bound to |
| an inferred pair nobody confirmed | `unconfirmedOperations` | `RouteConfirmationError`, and a generated file that will not compile |
| a generated name the package already declares | `validateRouteDeclarations` | the package and the name |
| a permission qualified by a package the generator cannot see | `renderRoutes` | the operation and the qualifier |
| a permission whose alias two files bind to two different packages | `refuseAmbiguousQualifiers`, or `renderRoutes` for a policy the manifest carries | the operation, the permission, and both import paths with the file each is written in |
| an artefact behind its source under `-check` | `checkArtifacts` | `DriftError` naming both paths, nothing written; under `-recursive` every stale package's paths in one error |

## Files

| File | Role |
|---|---|
| `cmd/vv/main.go` | `runRoutes` and the `generate routes` flag set |
| `internal/codegen/routes.go` | `RunRoutes`, `discoverRoutes`, `bindImportAlias`, `refuseAmbiguousQualifiers`, `collectGuards`, `collectDeclarations`, `boundOperation`, `buildRoutesManifest`, `routeFingerprint`, `unconfirmedOperations`, `unconfirmedRoutes`, `renderRoutes`, `validateRouteDeclarations`, `readRoutesManifest`, `validateRoutesManifestTarget`, `guardedDirs`, `RouteConfirmationError`, `UnenforcedDeclarationError` |
| `internal/codegen/codegen.go` | `containedOutputPath`, `validateGeneratedTarget`, `writeArtifact`, `writeGenerated`, `packageDeclarationNames`, `exprString` — shared with [[FL-010]] |
| `internal/codegen/resource.go` | `artifactName`, `checkArtifacts`, `DriftError` — shared with [[FL-029]] |
| `auth/http/authhttp/surface.go` | `Endpoint`, `Requires`, `Authenticated` — what the generated file produces |
| `internal/cachegen/render.go` | the precedent for a generated file that refuses to compile ([[FL-025]]) |

## Tests that walk this flow

- `TestTheRouteOfAnOperationIsInferredFromTheGuardThatEnforcesIt` —
  `internal/codegen/routes_test.go`.
- `TestAnUnconfirmedOperationLeavesAFileThatWillNotCompile`.
- `TestAConfirmedOperationBecomesTheOneValueTheRouteAndTheGuardBothRead`.
- `TestChangingThePermissionTheGuardEnforcesWithdrawsTheConfirmation`.
- `TestFillingInARouteTheGeneratorCouldNotInferDoesNotWithdrawItsOwnConfirmation`.
- `TestADeclarationNamingAPermissionNoUseCaseEnforcesIsRefused`.
- `TestAGuardBoundToTheGeneratedOperationIsNotConfirmedAgain`.
- `TestTheGeneratedCarrierCompilesAsBothTheDeclarationAndTheGuardsArgument` —
  a real module, generated, adopted and built.
- `TestTheRouteCheckReportsDriftWithoutWriting`.
- `TestTheWalkNamesEveryPackageWaitingForConfirmationAndSkipsTheOnesWithNoGuard`
  — the recursive mode, with the unguarded package left alone as the control.
- `TestTheRecursiveRoutesCheckNamesEveryStalePackageAndNotOnlyTheFirst` —
  `internal/codegen/check_test.go` — the same walk under `-check`, with the
  just-generated tree passing as the control.
- `TestAPackageThatAlreadyDeclaresOperationsKeepsItsOwn`.
- `TestOneUseCaseCannotEnforceTwoPoliciesUnderOneName`.
- `TestAnAliasThatNamesTwoPackagesIsRefusedRatherThanReadFromWhicheverFileSortsFirst`
  — the guard lives in the file that sorts second.
- `TestACarriedPolicyWhoseQualifierNamesTwoPackagesIsRefusedBeforeTheCarrierIsRewritten`
  — the same refusal for a bound guard, whose policy comes from the manifest.
- `TestAnAliasCollisionNoPolicyReadsLeavesGenerationAlone` — the control: the
  refusal is over the aliases a policy names, not over every import in the
  package.
- `TestTheRoutesSubcommandRefusesAnInferredPairNobodyConfirmed`,
  `TestTheRoutesSubcommandHasItsOwnFlags` — `cmd/vv/main_test.go`.

## See also

[[FL-029]] [[FL-025]] [[FL-024]] [[FL-020]] [[FL-010]]
