# D-105 — The persistence patch and the public PATCH body are two types

**Status:** accepted
**Invariant:** What an `UPDATE` may write and what a client may send are two types with two derivations. A transport binds the public one; the repository keeps the private one; a total mapper is the only road between them. A public body is derived by *narrowing*, and every field it publishes beyond that narrowing is confirmed by name in a manifest checked in beside the package.

## The decision

`<Model>Update` stays what it was: the persistence patch, every column an
`UPDATE` may write, three-state per [[D-002]], generated into `vv_gen.go` by
`vv generate` and asserted total by `port.MustCoverUpdate` ([[D-050]]).
**Nothing in it is a promise to a client.**

A second command, `vv generate resource`, writes the public half into
`vv_wire_gen.go` beside it:

| artefact | what it is |
|---|---|
| `<Model>Input` | the create body |
| `<Model>Patch` + `<Model>PatchMapper` | the PATCH body, and the total map onto `<Model>Update` |
| `<Model>Response` + `<Model>Presenter` | the answer body, and the map from the model onto it |

`crud/wire` owns the two interfaces the transports take — `PatchMapper[P, U]`
and `Presenter[M, R]` — and the three coverage assertions the generated file
calls at package initialisation: `MustCoverCreate`, `MustCoverPatch`,
`MustCoverResponse`.

Every binding grew one explicit constructor pair, `NewWire` and `ServingWire`,
which take the mapper, the patcher and the presenter. `New`, `NewFor`, `Serving`
and `ServingFor` are unchanged and fill in `wire.IdentityPatch[U]()` and
`wire.IdentityPresenter[M]()`, so a resource mounted straight onto the model
still answers the model and still takes the persistence DTO as its body
([[D-021]]: the magic is a thinner call over the explicit one, never the only
one).

**Which fields a body carries is derived by narrowing.** The default for each
body is the widest set that is *safe*, not the widest set that is *possible*:

| body | derived from | minus |
|---|---|---|
| create | the create body's own field set — no relation, no `generated`, no lock, no database-owned key ([[D-050]]) | `secret` |
| patch | the columns `<Model>Update` writes | `secret` |
| response | every column of the model | `secret`, and anything `-skip` removed |

`resource.manifest.yml` is written beside the package and checked in. Per model
and per body it records `narrowed` (what the rule derived), `fields` (what is
actually published), `widened` (the difference), a fingerprint of the
derivation, and `confirmed`. Removing a field from `fields` needs nothing.
Adding one back that the narrowing excluded sets `widened`, and generation stops
with a `ConfirmationError` until a person writes `confirmed: true` beside it.
The confirmation is fingerprinted against the derivation it was given for, so a
model that changes shape drops it. A name the model cannot publish at all — a
`generated` column offered as a patch field — is an error, not something to
confirm.

`vv generate resource -check` regenerates both artefacts in memory and reports
`DriftError` naming the stale files, writing nothing. The model generator took
the same `-check`.

## Why

**Because one type cannot answer two questions.** `UserUpdate` carried
`IsActive *bool` because a use case deactivates an account, and
`LastLoginAt Opt[time.Time]` because signing in stamps it. That same type was
the parameter of the public PATCH binder, so both were things a client could
send. The two fixes available without a second type were both wrong: take the
field out of the DTO and the internal writer loses it (that is what `-readonly`
does, and why the audit called it a dead end); leave it in and the API publishes
it. A field is not "writable" or "not writable" — it is writable *by someone*,
and the someone is what the second type carries.

**Because narrowing is the only default that fails safe.** A public body derived
by widening — start empty, add what the author lists — leaks nothing but is
abandoned after the third model and then the author reaches for the model type
again. A public body derived by copying the model leaks the next column somebody
adds, silently, on the afternoon they add it. Narrowing publishes something
useful on day one *and* makes the leak the loud case: the column has to be typed
into a manifest and confirmed by name.

**Because a confirmation that survives what it confirmed is worse than none.**
This is `cachegen`'s shape, taken as it stands ([[FL-025]]): confirm a specific
derivation, not a body. `derivation_fingerprint` is over the narrowed set, so
adding a column to the model invalidates every confirmation whose narrowing
moved and asks again. A `confirmed: true` that stays true across a model change
would be a rubber stamp somebody applied a year ago to a different question.

**Because the coverage assertions have to compare two derivations.** Same
argument as [[D-050]], and the same mechanism: `MustCoverPatch` reads the
compiled `<Model>Update` and `<Model>Patch` through reflection, `MustCoverCreate`
and `MustCoverResponse` read `crud.Schema`. The generator reads source text. A
column the model gained and nobody regenerated for is a start-up refusal naming
the column, and the exclusions the manifest chose are carried into the generated
file as string literals — reflection reads the struct and never the manifest.

**Because the exclusion has to be a name and not a count.** `compare` reports
four things separately: a source field with no public field, a public field the
source does not carry, a type that disagrees, and an exclusion for a column that
no longer exists. The last one is what stops the exclusion list becoming a
graveyard: a `secret` column deleted from the model leaves a declared exclusion
naming nothing, and that refuses too.

**Because the transports take the seam and not the generator.** `crud/wire`
knows nothing about codegen; `crudfiber`, `crudgin`, `crudnet` and `crudgrpc`
know nothing about the manifest. A consumer who hand-writes a `PatchMapper` gets
the same binding, and a consumer who wants none keeps `New`. The generated file
is one producer of a seam that stands without it.

**Because a second command and not a second flag.** The wire half has its own
manifest, its own confirmation gate and its own `-check`; hanging it off
`-adapter` would mean one run of `vv generate` can stop halfway with a
confirmation error and leave `vv_gen.go` written and `vv_wire_gen.go` not. The
two commands write two artefact sets and either can be run alone.

## What it forbids

- Do not parameterise a public binder with `<Model>Update`. `Serving` still
  accepts it and that is for a resource whose model *is* its wire shape; a
  resource with a generated patch body is mounted with `ServingWire`.
- Do not put an internally-written column back into the public patch body to
  "keep the types down". That is the defect this decision exists for.
- Do not derive a public body by widening from empty, and do not derive it by
  copying the model. Narrowing is the default and the manifest is the exception
  channel.
- Do not let a confirmation outlive the derivation it was given for. The
  fingerprint is over the narrowed set for that reason and must not become a
  fingerprint of the published set.
- Do not offer a field the model cannot publish as something to confirm. A
  `generated` column in a patch body is an error; confirming it would produce a
  body no mapper could make total.
- Do not hand-edit `vv_wire_gen.go` or `resource.manifest.yml` beyond the
  `fields` and `confirmed` lines the manifest exists for. The generator refuses
  to overwrite a file it did not write, in both directions.
- Do not check totality by asking the generator what the columns are. Two
  derivations that share a source are one derivation ([[D-050]]).
- Do not add `NewWire` to one binding only. The three HTTP bindings carry the
  same names file for file, and `crudgrpc` carries the same constructors
  ([[FL-013]]).

## Where it lives

- `crud/wire/wire.go` — `PatchMapper`, `Presenter`, `IdentityPatch`,
  `IdentityPresenter`.
- `crud/wire/cover.go` — `CoversCreate`/`MustCoverCreate`,
  `CoversPatch`/`MustCoverPatch`, `CoversResponse`/`MustCoverResponse`, and
  `compare`, which is where the four kinds of disagreement are named.
- `crud/http/crudfiber/handler.go`, `crud/http/crudgin/handler.go`,
  `crud/http/crudnet/handler.go`, `crud/rpc/crudgrpc/handler.go` — `ResourceFor`,
  `NewWire`, `ServingWire`, and the identity defaults behind `New`/`Serving`.
- `internal/codegen/resource.go` — `RunResource`, the narrowing rules
  (`narrowedCreateFields`, `narrowedPatchFields`, `narrowedResponseFields` and
  the three `publishable*` sets they narrow from), `buildManifest`, `renderWire`,
  `renderWireCoverage`.
- `internal/codegen/manifest.go` — the manifest document, `mergeManifestBody`,
  `derivationFingerprint`, `unconfirmedBodies`, `ConfirmationError`,
  `DriftError`, `validateManifestTarget`.
- `cmd/vv/main.go` — `runResource` and the `generate resource` flags, including
  `-into`/`-import`, which pair the same way they do for the DTO ([[D-018]]).
- `internal/cachegen/manifest.go` — the precedent this copies.

## Proven by

- `TestThePatchBodyPublishesLessThanTheUpdateDTOWrites` in
  `internal/codegen/resource_test.go` — the finding itself: the update DTO can
  write the secret column, the patch body cannot, and the assertion in the
  generated file names the omission. The first check is the control — if the DTO
  ever stopped writing the column the split would prove nothing and the test
  says so.
- `TestTheResponseBodyLeavesOutWhatOnlyTheServerReads` — the response half, with
  the two columns every client does need asserted present.
- `TestNarrowingAPublicBodyInTheManifestNeedsNoConfirmation` and
  `TestWideningAPublicBodyRefusesUntilTheManifestConfirmsIt` — the two
  directions, and the second carries the confirmed re-run as its subtest so the
  refusal is a gate rather than a wall.
- `TestAConfirmationDoesNotSurviveAChangeToWhatItWasDerivedFrom` — the
  fingerprint, with the unchanged model accepted first as its control.
- `TestAFieldTheModelCannotPublishIsRefusedRatherThanConfirmed` — a `generated`
  column is an error and explicitly *not* a `ConfirmationError`.
- `TestAStaleWireArtefactFailsTheCheck` and `TestTheCheckWritesNothing` —
  `-check`, with the freshly generated pair passing it as the control.
- `TestAnUnrelatedManifestIsNeverOverwritten` — an authored file under the
  manifest's name.
- `TestTheWireBodiesCanBeWrittenIntoAPackageThatOwnsNeitherTheModelNorItsGenerator`
  — `-into`/`-import`, so an ent or gorm model gets public bodies in a package
  the ORM's generator does not own; with the `-into` without `-import` refusal
  beside it.
- `TestTheGeneratedWireBodiesRefuseToStartWhenTheyStopCoveringTheirShapes` in
  `internal/codegen/resource_test.go` — the generated file is built and run;
  untampered it starts, and a field cut out of the response body panics naming
  the column.
- `TestThePublicPatchBodyIsNotThePersistenceUpdate` in
  `crud/http/crudfiber/handler_test.go` and its twins in `crudgin` and
  `crudnet` — the public field becomes a column through the mapper and a column
  the public body does not carry is not written, with the mapperless mount as
  the control that the same body means the persistence DTO there.
- `TestTheAnswerIsWhatThePresenterMade` in the same three files — one entity and
  the collection, both through `Presenter`.
- `TestAColumnTheGeneratedFileNeverSawIsNamedInsteadOfWrittenOver`,
  `TestAPackageWithNoGeneratedFileAtAllFailsTheCheckWithoutCreatingOne` and
  `TestTheRecursiveCheckNamesEveryPackageBehindItsModelsAndNotOnlyTheFirst` in
  `internal/codegen/check_test.go` — `-check` on the model generator.

## See also

[[D-002]] [[D-018]] [[D-021]] [[D-050]] [[UC-014]] [[FL-002]] [[FL-010]]
[[FL-013]] [[FL-025]] [[FL-029]]
