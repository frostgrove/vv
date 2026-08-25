# D-050 — A generated adapter is total, and says so at start-up

**Status:** accepted
**Invariant:** A generated artefact covers every column its side of the wire can carry, and a column it does not cover is a start-up refusal — never a request-time wrong answer. Totality is a property of *generated* artefacts only; a hand-written DTO, mapper or `port.Fields` stays partial.

## The decision

`cmd/vv -adapter` writes a resource's whole skeleton: `<Model>Input` (the entity
body), `<Model>Mapper` (input → model), `<Model>Paths` (the inverse of that
mapping), `<Model>Service` and `Mount<Model>`. Two of those are checked against
the model at package initialisation:

- `port.MustPathMap[M]` — the inverse map must have an entry for every column a
  request can carry, and no entry for anything else.
- `port.MustCoverUpdate[M, U]` — the update DTO must name every column an
  `UPDATE` may write. This one is emitted **whether or not `-adapter` is on**,
  because it is the half that matters to every consumer.

Both take an exclusion list, which the generator emits from `-skip` and
`-readonly`.

## Why

**Because regenerate-and-diff cannot catch a missing column.** The tree's
existing guard regenerates into a temporary directory and compares. That
compares the generator against itself: a generator that silently skipped a
column would pass it forever, and [[UC-014]] gap 1 said so outright. The check
has to compare **two independent derivations**, and it does:

| derivation | source | mechanism |
|---|---|---|
| what the generator thinks the columns are | the model's **source text** | `go/parser`, in `internal/codegen` |
| what the columns actually are | the **compiled struct** | `reflect`, through `crud.Schema` |

`crud.Schema.Update` is already *every non-PK, non-generated, non-immutable,
non-version field* — exactly the generator's keep-set, derived the other way
round. Comparing the two at package initialisation is a real question with a
real answer. That is why the check reads `crud.Schema` rather than re-reading
the generator's own view of the model.

**Because the failure it prevents is invisible.** Add a column, do not
regenerate: today the application starts cleanly, the column is absent from
updates and from the typed query API, and nothing says anything. That is
[[D-021]] applied to the part of this design most likely to rot — a DTO and a
table drift apart on an ordinary afternoon.

**Because the check belongs on the generated artefact and not on `basic.Define`.**
A totality arm inside `crud.PlanFor` would refuse every hand-narrowed DTO in the
tree and in every consumer's code, and a narrow DTO is a supported shape — a
service that exposes three of nine columns is not broken. `port/path.go` already
argues the same shape for `Fields`: *"A hand-written Fields is partial by
nature."* Generated is where totality can be demanded, because generated is
where it can be produced.

**Because a total map may decline where a partial one must not.** `port.Fields`
passes an undeclared head through: it is hand-written, so an undeclared head is
an ordinary gap, and declining would poison `errs.Chain` and take a path the
raw-body index resolves today and make it *worse*. `port.PathMap` declines,
because an undeclared head cannot be a gap — the map is total and was checked.
It is a column of another table, or one no request carries. Declining marks the
violation approximate, and approximate is honest where a guess is not
([[D-043]]).

The two therefore also differ under a leading index: `PathMap` skips leading
index steps and translates the first *named* one, so phase 7's bulk attribution
`[0,"Email"]` becomes `[0,"email"]`. `Fields` does not, because a hand-written
map may legitimately declare a key called `"3"` and silently ignoring it would
be worse than not looking. A generated map cannot declare an index at all.

**Because the map must be exact as well as total.** An entry for a column no
request carries — a `generated` one, the optimistic lock, or a column the
command line declared out of the wire shape — would translate a violation to a
key the client cannot find in its own payload. That is the same wrong answer as
a missing entry, arrived at from the other side, so it is refused the same way.

**Because a declared hop must not be overturned by the fallback.** The raw-body
index matches a violation's last step against the keys the client sent. Applied
to a path a declared hop has already translated, it can re-match that step onto
a same-named key elsewhere in the payload — a `not_null` violation on a column
the client omitted is the case that produces it. So the renderer runs the
fallback only when the declared hops left the path unchanged. The fallback is
for a path nobody translated.

**Because reflection reads the struct and never the command line.** `-skip` and
`-readonly` keep a column out of the artefacts; reflection sees an ordinary
writable column. The generated file therefore carries the exclusion list as
string literals, which has a second benefit: the flags' effect is visible in the
output rather than only in a `//go:generate` line. A `-skip`ped column's name
appears there and nowhere else in the file, and that is not the same as
surviving.

**Because the exclusion list applies to both bodies, which is where `-readonly`
and `db:",immutable"` part company.** [[D-018]] says `-readonly` has *"the same
effect as `db:",immutable"` for a struct you do not own and cannot tag"* — a
statement made when the update DTO was the only artefact. It is no longer the
whole story: a tag-`immutable` column is *insert-only*, so it belongs in the
entity body and not in the patch body, while a `-readonly` column is
*server-owned* and belongs in neither. Every use of the flag in this tree is the
server-owned case — gorm's timestamps, ent's `CreatedAt`. The alternative,
keeping them identical, would put a column the author called server-owned into
the create body where a client could forge it.

**Because one naming rule serves both bodies.** `<Model>Update` uses
`lowerFirst(FieldName)` and is shipped, so `<Model>Input` does too. The rejected
alternative was to honour the model's own `json` tag on the Input: it makes the
two bodies disagree for any model whose tag differs, and then one resource needs
two inverse maps while only one of them has an owner in [[D-043]]'s chain —
`port/doc.go` states that PATCH has no mapper. The consequence is intended and
stated in both usage guides: **a generated resource has a wire shape of its own**
(the roadmap's §3: *"The adapter layer is what introduces a distinct one"*). A
consumer who wants the model's own JSON shape keeps `New` and generates no
adapter.

**Because the map lives on the mapper and not on the service.** [[D-043]]: *"Do
not let a transport binding map a column to a request key. It did not do the
decoding."* The service hop is the command shape and the transport hop is the
adapter's. One owner per hop.

## What it forbids

- Do not put a totality check in `crud.PlanFor` or `basic.Define`. A
  hand-narrowed DTO is a supported shape.
- Do not make `port.PathMap` pass an undeclared head through. Its whole value
  over `Fields` is that an undeclared head means something.
- Do not make `port.Fields` decline. It is partial by nature and a declining hop
  poisons `errs.Chain`.
- Do not check totality against the generator's own view of the model. Two
  derivations that share a source are one derivation.
- Do not hand-write an inverse path map for a generated resource, and do not
  hand-edit one. `DO NOT EDIT` is what the start-up refusal enforces.
- Do not run the raw-body fallback over a path a declared hop translated.
- Do not drop the exclusion list from the generated file to make it tidier. It
  is the only thing carrying a command-line flag into a file reflection reads.
- Do not generate wiring for a binding that lives in a satellite module from the
  library's own generated files ([[D-033]]). `-binding` accepts `net` and `none`
  today and refuses the rest with a message.

## Where it lives

- `port/pathmap.go` — `PathMap`, `At`, `PathMap.Resolve`, `NewPathMap`,
  `MustPathMap`, `CoversUpdate`, `MustCoverUpdate`.
- `port/path.go` — `Fields`, the partial half, and the comment that names the
  difference.
- `crud/update.go:UpdatePlan.Covers` — the model columns a DTO resolves to,
  through the plan the repository already builds, so there is one resolution
  rule rather than two.
- `internal/codegen/adapter.go` — `inputFields`, `renderAdapter`,
  `renderCoverage`.
- `internal/codegen/codegen.go` — `field.Excluded`, `field.tagDropped`,
  `generator.exclude`, `model.excluded`, `wellKnownEmbeds`.
- `http/crudhttp/render.go:EnvelopeRenderer.violations` — declared hops first,
  the fallback only over an untranslated path.
- `cmd/vv/main.go` — `-adapter`, `-binding`.
- `test/versionstore/` — the model that made the version case reachable.

## Proven by

- `TestAGeneratedResourceRefusesToStartWhenAColumnIsMissing` in
  `internal/codegen/codegen_test.go` — three arms and the first is the control:
  the untampered resource starts; a column added to the *model source* with
  nothing regenerated refuses and names the column; an entry deleted from the
  generated map refuses and names it.
- `TestAnUpdateDTOMissingAWritableColumnRefusesAtDeclaration` and
  `TestAMapMissingAWritableColumnRefusesAtDeclaration` in `port/pathmap_test.go`
  — each with the complete artefact accepted as its control.
- `TestTheMapMustMatchWhatARequestCanCarry` — the exact half, both directions.
- `TestADeclaredExclusionIsNotRequired` and `TestAnEntryNamingNoColumnIsRefused`
  — the exclusion list, with the undeclared twin as the control.
- `TestAGeneratedMapDeclinesWhereFieldsPassesThrough` — the contrast *is* the
  control: same undeclared head, both types, opposite outcomes, and a recording
  hop behind each showing that the chain kept running behind one and stopped
  behind the other.
- `TestAGeneratedMapTranslatesUnderALeadingIndex` — with the undeclared-name
  twin. `TestAnEmptyFieldsMapIsTheIdentity` pins the opposite for `Fields`.
- `TestADeclaredMapBeatsTheRawBodyGuess` in `http/crudhttp/render_test.go` — a
  declared path is not overturned by the index, an ambiguous body the index
  declines is still resolved by the map, and an undeclared field declines and is
  marked approximate. Every arm has its no-map control.
- `TestAGeneratedResourceResolvesTheSameFieldOnAllThreeBindings` in
  `test/portmount/mount_test.go` — the same violation names the client's key on
  Fiber, Gin and net/http, with the `New`-mounted control answering the model's
  field name.
- `TestTheGeneratedMapCoversEveryWritableColumn`,
  `TestTheGeneratedAssertionNamesTheReadonlyExclusions`,
  `TestAVersionedModelGeneratesAResourceThatStarts` and
  `TestOutputIsByteIdenticalAcrossRuns` (extended to the adapter fixture) in
  `internal/codegen/codegen_test.go`.
- `TestTheGeneratedDeclarationForAVersionedModelIsAccepted`,
  `TestTheGeneratedWireShapesLeaveOutWhatTheClientDoesNotOwn` and
  `TestTheGeneratedMapperAndItsInverseAgree` in
  `test/versionstore/versionstore_test.go`.
- `TestTheGeneratedStoresAreUpToDate` in `test/codegen/codegen_test.go` — the
  regenerate-and-diff half, now without a database and with its own control that
  the comparison can tell two files apart.

## See also

[[D-021]] [[D-043]] [[D-018]] [[D-014]] [[UC-014]] [[FL-010]] [[FL-015]]
