# FL-029 — A model becomes a public wire body

**Entry point:** `cmd/vv/main.go:runResource` → `internal/codegen.RunResource`
**Implements:** [[UC-014]] [[UC-001]] · **Governed by:** [[D-105]] [[D-050]] [[D-018]] [[D-002]]

The other half of [[FL-010]]. That one derives what the database may write; this
one derives what a client may send and may see, and refuses to widen either
without a name and a signature.

## The path

1. **`runResource`** — `cmd/vv/main.go:51`
   `generate resource` is its own subcommand with its own flags: `-dir`, `-out`
   (`vv_wire_gen.go`), `-manifest` (`resource.manifest.yml`), `-types`, `-skip`,
   `-readonly`, `-into`, `-import`, `-recursive` (on by default) and `-check`.
   `-into`/`-import` carry the same meaning and the same pairing rule they have
   in [[FL-010]], and both artefacts land in `-into`. It fills a
   `codegen.ResourceOptions` and calls `RunResource`. Nothing else is here —
   same rule as `runModels`.

2. **`RunResource`** — `internal/codegen/resource.go:37`
   `artifactName` refuses an `-out` or `-manifest` carrying a directory, and
   `containedOutputPath` refuses one that escapes `-dir`. Recursive mode walks
   `modelDirs` and runs once per package, skipping a directory with no models
   rather than failing the walk. A stale artefact and a body waiting for
   confirmation do not stop it either: stale paths and `<directory>.<Model>
   <body>` names are collected across every package and reported once — a walk
   that stopped at the first would need one run per package to say what is
   wrong. `-into` and `-import` are refused with
   `-recursive`, because a walk has one destination per package and `-into` is
   one destination. The generator is built with `wireOnly` set and `depth: 1` —
   no metamodel is rendered here.

3. **`generator.runWire`** — `internal/codegen/resource.go:107`
   The order matters and is the whole design:
   ```
   validateGeneratedTarget → load → into? → readResourceManifest → buildManifest
     → unconfirmed? → write the manifest, refuse, generate nothing
     → render → check or write
   ```
   The manifest is written **before** the refusal, so the author has the exact
   lines to edit; the Go file is not, so an unconfirmed body never exists as a
   type. Under `-check` nothing is written at all, including in that branch.

4. **`readResourceManifest`** — `internal/codegen/manifest.go:71`
   `DisallowUnknownFields`, then `format` and `generated_by` have to match, then
   the package name has to be this package, then no model may appear twice.
   Anything else is *refusing to overwrite an unrelated manifest* — the file has
   a name a human might reasonably have used for something else.

5. **The narrowing** — `internal/codegen/resource.go:266-316`
   Three `publishable*` sets say what the body *could* carry; three `narrowed*`
   sets say what it carries by default.
   | body | publishable | narrowed |
   |---|---|---|
   | create | every column that is not `generated` and not `serverowned` | `inputFields` (also without the lock and the database-owned key) minus `secret` |
   | patch | `updateFields` — what `<Model>Update` writes | the same minus `secret` |
   | response | every column | every column minus `secret` and minus `-skip` |
   `field.column()` is what keeps relations and `db:"-"` out of all three.

6. **`generator.buildManifest`** — `internal/codegen/resource.go:183`
   Per model, per body, `mergeManifestBody(narrowed, publishable, prior)`:
   - with no prior entry, `fields` is the narrowed set;
   - with one, `fields` is what the author left there, sorted;
   - a name listed twice is an error, and so is a name outside `publishable` —
     *the model does not carry it, or the shape this body maps onto does not*;
   - `widened` is `fields` minus `narrowed`;
   - `derivation_fingerprint` is SHA-256 over the **narrowed** set, and
     `confirmed` is carried over from the prior entry only when that fingerprint
     is unchanged.
   The bodies that will actually be rendered are `selectFields(publishable,
   fields)`, kept on `generator.bodies`.

7. **The confirmation gate** — `unconfirmedBodies`,
   `internal/codegen/manifest.go:143`
   Any body with a non-empty `widened` and `confirmed: false` becomes one entry
   of a `ConfirmationError`, named `"<Model> <body>"`. The message names the
   manifest and every body waiting. This is `cachegen`'s shape verbatim
   ([[FL-025]]).

8. **`generator.renderWire`** — `internal/codegen/resource.go:318`
   Per model, three struct/behaviour pairs, in this order:
   | artefact | shape |
   |---|---|
   | `<Model>Input`, `<Model>InputMapper` | field types as written, plain JSON names; `Model(ctx, in) (M, error)` assigning field for field; `var _ port.Mapper[…]` beside it |
   | `<Model>Patch`, `<Model>PatchMapper` | `dtoType` per field, so `*T` and `Opt[T]` keep [[D-002]]'s three states, with `omitempty`/`omitzero` to match; `Update(patch) <Model>Update`; `var _ wire.PatchMapper[…]` |
   | `<Model>Response`, `<Model>Presenter` | field types as written; `Response(model) <Model>Response`; `var _ wire.Presenter[…]` |
   The `var _` lines are why a rename in `crud/wire` is a build failure in every
   consumer rather than a silent unmount.

9. **`generator.renderWireCoverage`** — `internal/codegen/resource.go:390`
   One `init`, three lines per model:
   ```go
   wire.MustCoverCreate[Account, AccountInput]("ID", "Password")
   wire.MustCoverPatch[AccountUpdate, AccountPatch]("Password")
   wire.MustCoverResponse[Account, AccountResponse]("Password")
   ```
   The literals come from `excludedFrom(publishable, published)` — everything the
   body could have carried and does not, whether the narrowing dropped it or the
   manifest did. Reflection reads the struct and never the manifest, so those
   literals are the only thing carrying the decision into a file the runtime
   reads ([[D-050]] makes the same argument for `-skip`/`-readonly`).

10. **`wire.Covers*`** — `crud/wire/cover.go`
    Two shapes and one comparison. `CoversPatch` reads both structs with
    `reflect.VisibleFields`; `CoversCreate` reads `crud.SchemaOf[M]().Insert` and
    `CoversResponse` reads `.Fields`. `compare` names four disagreements
    separately: a source column with no public field, a public field the source
    does not carry, a field whose type disagrees, and an exclusion for a column
    that no longer exists. The last is what stops the exclusion list becoming a
    graveyard. `MustCover*` panics, so the refusal is at package initialisation.

11. **`checkArtifacts`** — `internal/codegen/resource.go:168`
    Under `-check`, both rendered artefacts are compared with what is on disk;
    a missing file counts as stale. The result is a `DriftError` naming the
    sorted paths and nothing is written. `Run` (the model generator) took the
    same flag and the same error type.

12. **The transport** — `crudfiber`, `crudgin`, `crudnet`, `crudgrpc`
    `ResourceFor[M, ID, U, In, P, R]` holds the mapper, the patcher and the
    presenter. `NewWire` and `ServingWire` take all three; `New`, `NewFor`,
    `Serving` and `ServingFor` pass `wire.IdentityPatch[U]()` and
    `wire.IdentityPresenter[M]()`, which is why `Handler[M, ID, U]` is still
    `ResourceFor[M, ID, U, M, U, M]` and the shorter mounts are unchanged.
    `Update` decodes the body into `P` and calls `this.patcher.Update(patch)` on
    the way into `port.UpdateCommand[ID, U]` ([[FL-002]] step 1). `entity` and
    the collection page both go through `this.presenter.Response`, unless
    `WithTransform` was given — a consumer's explicit presenter still wins over
    the generated one.

## Where the decisions bite

- **Narrowing is the default; widening is signed.** Taking a field out of
  `fields` needs nothing, because a smaller public surface cannot leak. Putting
  one back sets `widened` and stops generation until somebody writes
  `confirmed: true` next to it ([[D-105]]).
- **A confirmation is for a derivation, not for a body.** The fingerprint is
  over `narrowed`, so the question is asked again the moment the model's shape
  moves. Fingerprinting `fields` instead would make the confirmation permanent
  and useless.
- **An impossible field is an error, not a question.** A `generated` column
  offered as a patch field fails `mergeManifestBody`, and deliberately never
  becomes a `ConfirmationError` — there is nothing a person could confirm that
  would make it work.
- **The Go file is never written for an unconfirmed body.** `cachegen` renders a
  file that does not compile; here the file is simply not produced, because the
  previous one is still valid Go and leaving it in place is the safer half-state.
- **The two derivations stay independent.** The generator reads source text; the
  coverage assertions read the compiled struct and `crud.Schema`. A check that
  called into `internal/codegen` would be one derivation agreeing with itself
  ([[D-050]]).
- **The seam is in `crud/wire`, not in the generator.** `PatchMapper` and
  `Presenter` are two one-method interfaces a consumer can hand-write. The
  generated file is one producer of a seam that stands without it.

## Failure modes

| What goes wrong | Where it is caught | What the caller sees |
|---|---|---|
| `-out` or `-manifest` naming a directory | `artifactName` | `-out must be a file name without directories` |
| `-into` without `-import` | `runWire` | `-into needs -import so the generated file can name the model types` |
| `-into` or `-import` with `-recursive` | `RunResource` | a refusal naming both |
| `-out` escaping `-dir` | `containedOutputPath` | a refusal naming the path |
| the output file is a symlink, or authored | `validateGeneratedTarget` | `refusing symlink output` / a refusal to overwrite |
| an authored file under the manifest's name | `readResourceManifest` / `validateManifestTarget` | `refusing to overwrite an unrelated manifest` |
| the manifest belongs to another package | `readResourceManifest` | the path, the package it claims and the package it is in |
| the manifest names a model twice | `readResourceManifest` | the path and the model |
| a field listed twice in one body | `mergeManifestBody` | `<Name> is listed twice` |
| a field the body cannot publish at all | `mergeManifestBody` | `<Name> cannot be published here: …` |
| a body publishing past the narrowing, unconfirmed | `unconfirmedBodies` | `ConfirmationError` naming the manifest and each `<Model> <body>`; under `-recursive` every package's, prefixed by directory |
| the model gained a column, nothing regenerated | `wire.MustCover*` at package init | panic naming the column |
| a public body carrying a field the model does not | `wire.MustCover*` at package init | panic naming the field |
| an exclusion for a column the model no longer has | `compare`'s `stale` arm | panic naming the exclusion |
| an artefact behind its model under `-check` | `checkArtifacts` | `DriftError` naming both paths, nothing written; under `-recursive` every stale package's paths in one error |
| no models in the directory | `runWire` | `no models found in <dir>` (skipped, not fatal, under `-recursive`) |

## Files

| File | Role |
|---|---|
| `cmd/vv/main.go` | `runResource` and the `generate resource` flag set |
| `internal/codegen/resource.go` | `RunResource`, `runWire`, the narrowing sets, `buildManifest`, `renderWire`, `renderWireStruct`, `renderWireCoverage`, `checkArtifacts` |
| `internal/codegen/manifest.go` | the manifest document and body, `readResourceManifest`, `mergeManifestBody`, `derivationFingerprint`, `unconfirmedBodies`, `marshalManifest`, `validateManifestTarget`, `ConfirmationError`, `DriftError` |
| `internal/codegen/codegen.go`, `internal/codegen/render.go` | `updateFields`, `containedOutputPath`, `validateGeneratedTarget`, `writeArtifact`, `writeGenerated`, `dtoType` — shared with [[FL-010]] |
| `internal/codegen/adapter.go` | `inputFields`, `quoteList` — the create field set and the literal list, shared with the `-adapter` half |
| `crud/wire/wire.go` | `PatchMapper`, `Presenter`, `IdentityPatch`, `IdentityPresenter` |
| `crud/wire/cover.go` | `CoversCreate`, `CoversPatch`, `CoversResponse`, their `Must` twins, `shapeOf`, `compare` |
| `crud/http/crudfiber/handler.go`, `crud/http/crudgin/handler.go`, `crud/http/crudnet/handler.go` | `ResourceFor`, `NewWire`, `ServingWire`, the identity defaults, `Update` and `entity` |
| `crud/rpc/crudgrpc/handler.go` | the same four constructors over `port` vocabulary |
| `internal/cachegen/manifest.go` | the precedent — same `Confirmed`, same fingerprint-drops-confirmation rule ([[FL-025]]) |

## Tests that walk this flow

- `TestThePatchBodyPublishesLessThanTheUpdateDTOWrites` —
  `internal/codegen/resource_test.go` — the split, with the update DTO's ability
  to write the column asserted first as the control.
- `TestTheResponseBodyLeavesOutWhatOnlyTheServerReads` — the response half.
- `TestNarrowingAPublicBodyInTheManifestNeedsNoConfirmation` — and that the
  removed column is declared as an omission rather than forgotten.
- `TestWideningAPublicBodyRefusesUntilTheManifestConfirmsIt` — the refusal, that
  nothing was generated, that the manifest records what is waiting, and the
  confirmed re-run as a subtest.
- `TestAConfirmationDoesNotSurviveAChangeToWhatItWasDerivedFrom` — the
  fingerprint, with the unchanged model accepted first.
- `TestAFieldTheModelCannotPublishIsRefusedRatherThanConfirmed` — an error and
  explicitly not a `ConfirmationError`.
- `TestAStaleWireArtefactFailsTheCheck` and `TestTheCheckWritesNothing` —
  `-check`, with the just-written pair passing as the control.
- `TestAnUnrelatedManifestIsNeverOverwritten`.
- `TestTheWireBodiesCanBeWrittenIntoAPackageThatOwnsNeitherTheModelNorItsGenerator`
  — the ORM-adoption shape: the model package is only read, both artefacts land
  in the owned package, the manifest belongs to the package it sits in, and the
  presenter names the model through its import. The `-into` without `-import`
  subtest is the refusal beside it.
- `TestTheGeneratedWireBodiesRefuseToStartWhenTheyStopCoveringTheirShapes` — the
  generated package is built and run: untampered it starts, a field cut from the
  response body panics naming the column.
- `TestThePublicPatchBodyIsNotThePersistenceUpdate` and
  `TestTheAnswerIsWhatThePresenterMade` — `crud/http/crudfiber/handler_test.go`
  and the identical twins in `crudgin` and `crudnet`. The first carries the
  mapperless mount as its control.
- `TestAColumnTheGeneratedFileNeverSawIsNamedInsteadOfWrittenOver`,
  `TestAPackageWithNoGeneratedFileAtAllFailsTheCheckWithoutCreatingOne`,
  `TestTheRecursiveCheckNamesEveryPackageBehindItsModelsAndNotOnlyTheFirst` —
  `internal/codegen/check_test.go` — `-check` on the model generator.
- `TestTheRecursiveWireCheckNamesEveryStalePackageAndNotOnlyTheFirst` and
  `TestTheRecursiveWireRunNamesEveryBodyWaitingForConfirmationAndSaysWhichPackageItIsIn`
  — `internal/codegen/check_test.go` — the walk over two packages, with the
  just-generated tree passing the check as the control.

## See also

[[FL-010]] [[FL-002]] [[FL-013]] [[FL-015]] [[FL-025]]
