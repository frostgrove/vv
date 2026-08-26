# cmd/vv · internal/codegen — the update DTO, the metamodel and the whole resource, written from the model so they cannot quietly disagree with it

**Covers:** `github.com/frostgrove/vv/cmd/vv`, `github.com/frostgrove/vv/internal/codegen`
**Sweep:** happy paths · release readiness
**Verdict:** not ready — the DTO-and-metamodel half is finished and unusually well pinned, but `-adapter` hands a client the auto primary key on every create with no flag that takes it back, and the most ordinary Go model shape after the plain struct — one that embeds a shared base from another package — cannot be generated for at all, and its start-up refusal names two fixes that both do nothing.

## What a consumer is actually trying to do

They have a struct. It is already the truth — the columns, which ones are
nullable, which ones the database fills in, which ones nobody outside the server
may touch. What they do not want is to type that truth out a second time as a
patch body, a third time as a set of filterable field names, and a fourth time as
the map that turns a rejected column back into the key the client actually sent.
Each of those is mechanical, each is wrong the moment somebody renames a field,
and none of them is wrong loudly.

Then comes the ordinary Tuesday. Somebody adds a column, or widens one from a
32-bit integer, or marks one insert-only. The interesting question is not whether
the tool can regenerate — it is what happens when they forget. The answer they
want is a red build or a refusal to start, naming the column, before any traffic.
What they do not want is the column silently unpatchable for three weeks and a
support ticket that says "the locale never saves".

Some of them do not own the struct at all. It came out of another generator, it
carries that generator's tags and none of this one's, and it is overwritten every
time that tool runs. Others own it but share it: an `ID` and three timestamps sit
in a base struct in a package every service in the company embeds. Both groups
want the patch body and the typed field names written into a package they own,
with the columns somebody else manages declared as nobody's business but the
server's.

A smaller group wants the rest of the resource: the create body, the mapping onto
the model, the inverse of that mapping, and a mount line. The inverse is the one
they cannot write by hand and keep right — it is what makes a validation error
name `authorID`, the key the browser sent, instead of a Go field name the form
has never heard of. They also expect to override one method of it by the second
month, without losing the override on the next regeneration.

And all of them have a reviewer. The output has to be a file a person can skim
in a pull request: same input, same bytes, a header that says not to edit it, and
a diff that is a real change rather than a reshuffle.

## Happy cases

### H-CODEGEN-01 — The first generate, on a package you own
**Who:** a backend developer starting a service, first afternoon
**Wants:** the patch body and the typed field names, without deciding anything.
**Story:** They write the model with its column tags, put one directive above it,
and run the generator over the repository. They declare the repository against
the type that appeared, and move on.
**Must hold:**
1. No flag is required for a package that holds its own models.
2. The package builds and the process starts.
3. The generated file announces that it is generated, and it is the only file the
   run writes.
4. The generator that runs is the one the project already depends on — not
   whatever is newest on the machine.
5. It runs where the build runs: a vendored tree, or a build container with
   module downloads switched off.
**Today:** 🟡 partial
**Evidence:** 1, 2 and 4 hold. `cmd/vv/main.go:65-79` — every flag has a default;
`-dir .`, `-out vv_gen.go`, and the no-flag directive is the one in the command's
own package doc (`cmd/vv/main.go:4`). The nearest thing to it in the tree carries
a single flag (`_examples/example/blog/model.go:10`).
`internal/codegen/render.go:42` writes the `DO NOT EDIT` header.
`TestGeneratedCodeCompilesAndValidates` (`internal/codegen/codegen_test.go:645`)
builds the output in a throwaway module and runs its `init`, which is the only
way to know the start-up checks agree. Guarantee 4 is free rather than designed:
the command lives in the root module the consumer already requires, so `go run`
resolves the version in their own `go.mod`.
3 holds in practice and nothing enforces it. `codegen.go:142` is
`os.WriteFile(outPath, src, 0o644)` with no read of the target first; the only
special handling of the out path is `codegen.go:153-155`, which excludes it from
the *parse*. So `-out helpers.go` in a package whose models live elsewhere skips
`helpers.go` as input and then destroys it as output, with no diagnostic.
5 fails. `go mod vendor` copies what is needed to build and test the main
module's packages, and nothing imports `cmd/vv`, so a vendored consumer cannot
run the generator at all. `go.mod` declares `go 1.26`, so the `tool` directive
(`go get -tool`, `go tool vv`) is available and is the current idiom for exactly
this; neither it nor vendoring is mentioned anywhere —
`grep -rn vendor docs/usage-guides/ docs/modules/en/vv-cli.md` returns nothing.
**If not ready:** The vendored or hermetic consumer un-vendors, or adds a blank
import of `cmd/vv` to a `tools.go`, or runs the generator on a developer machine
only — which is the shape that ends with a checked-in file nobody can reproduce
in CI. Closing it is two sentences in both guides and a `tool` line in the
module doc. The `-out` hole is a cheap guard: refuse a target whose first line is
not the generated header.

### H-CODEGEN-02 — Tuesday's change to the model
**Who:** the same developer, six weeks later
**Wants:** to change a column and have the process refuse to let them
half-finish.
**Story:** They add a nullable column, run the same command, and read a two-line
diff. On another day they widen a column from `int` to `int64`, or drop one, or
mark one insert-only — and forget the command entirely.
**Must hold:**
1. Regenerating is the same command as the first time.
2. A nullable column becomes a three-state field and a plain one a two-state
   field, without anybody choosing.
3. Forgetting is caught before the first request, and the message names the
   column.
4. It is caught for a column *removed* or *retyped* too, not only one added.
5. The catch works with nothing regenerated — it is not the generator marking its
   own homework.
**Today:** ✅ ready
**Evidence:** Codegen's own share is 1, 2, and that the assertion ships at all:
`internal/codegen/adapter.go:130-144` emits `port.MustCoverUpdate` for every
model whenever the DTO half is on (`render.go:36-39`), so it reaches consumers
who never touch `-adapter`, and it carries the exclusion list reflection cannot
see. Guarantee 2 is pinned byte-for-byte by `TestUpdateDTOFollowsNullability`
(`codegen_test.go:139`).
3, 4 and 5 are `port`'s and `crud`'s, and are stronger than this module usually
claims. The missing-column message is `port/pathmap.go:221` — "no field for …
— regenerate it, or declare the exclusion". `CoversUpdate` runs `crud.PlanFor`
(`port/pathmap.go:180-186`), and that refuses a DTO field with no model field
behind it (`crud/update.go:104-110`), one that now names a PK, generated,
immutable or version column (`:112-120`), and a type mismatch naming both types
(`:136-143`). Every one of those fires from the generated `init`.
`TestAGeneratedResourceRefusesToStartWhenAColumnIsMissing`
(`codegen_test.go:672`) runs three arms, the first being the untampered control;
the second adds the column to the *model source* with nothing regenerated.
**If not ready:** —

### H-CODEGEN-03 — The pull request that changed the model and not the generated file
**Who:** whoever owns the build
**Wants:** the check in CI, not at 3am in production.
**Story:** They want one step in the pipeline that fails when the checked-in
artefacts no longer match the models, on a machine with no database and no
running service.
**Must hold:**
1. There is a way to ask "is this current?", and it is written down.
2. It fails loudly, and names the file and the command that fixes it.
3. It needs no database.
**Today:** 🟡 partial
**Evidence:** The capability is there and nobody wrote the recipe down.
`go generate ./... && git diff --exit-code` works: the output is byte-identical
across runs, pinned by `TestOutputIsByteIdenticalAcrossRuns`
(`codegen_test.go:555`), and it needs no database. Neither usage guide mentions
CI at all. The library's own gates are two Go tests that shell out to the
published command and regenerate into a temporary directory —
`test/codegen/codegen_test.go:38` and `_examples/example/blog/blog_test.go:140`.
A consumer *can* copy either verbatim; neither imports `internal/codegen`
(`test/codegen/codegen_test.go:127` is
`exec.Command("go", "run", "github.com/frostgrove/vv/cmd/vv", …)`). What is
missing is a mode that writes nothing — `cmd/vv/main.go:63-79` has no `-check`,
`-diff` or `-n`, and `codegen.go:142` writes unconditionally — which matters on a
read-only checkout and nowhere else.
**If not ready:** The consumer writes the git-diff line themselves and finds it by
knowing the Go idiom rather than by reading anything here. Worth saying plainly
wherever it lands: regenerate-and-diff measures the generator against itself. It
catches a checked-in file that is stale for this generator version, and it can
never replace the start-up assertion, because [[D-050]] is explicit that two
derivations sharing a source are one derivation.

### H-CODEGEN-04 — The columns the server owns
**Who:** the author of a multi-tenant SaaS
**Wants:** the tenant key, the created timestamp and the row version out of the
client's reach, and still filterable.
**Story:** They tag the tenant column insert-only, the timestamp
database-filled, and the version column as the lock. They filter and sort by all
three from the admin tool. Then they turn `-adapter` on and ship a create
endpoint.
**Must hold:**
1. None of the three appears in the patch body.
2. All three still appear in the typed field names, so a report can filter on
   them.
3. The row version is handled without being asked about — a patch body naming it
   would be refused when the repository is declared.
4. A column the server owns is not something a client can set on a create
   either, or the file says loudly that it is.
**Today:** 🟡 partial
**Evidence:** 1, 2 and 3 hold. `codegen.go:267-283` reads the tag options and
their aliases; `render.go:148` drops skipped columns, relations, the key,
generated, immutable and version columns from the patch DTO. The metamodel keeps
them — `_examples/example/blog/vv_gen.go:52-66` carries `TenantID` and
`CreatedAt` as attributes while the DTO above it does not. The version case has a
model of its own precisely because nothing else in the tree had one:
`test/versionstore/model.go:32`, with
`TestAVersionedModelGeneratesAResourceThatStarts` and
`TestTheVersionColumnIsLeftOutOfTheDTO` (`codegen_test.go:772`, `:814`).
4 does not hold, and the file that would say so does not exist.
`adapter.go:22-31` excludes Skip, relations, Generated, Version and Excluded —
and **not** `PK`, and **not** `Immutable`. So
`_examples/example/blog/vv_gen.go:76` and `:83` put `ID` and `TenantID` in
`ArticleInput`, and `:94`/`:101` copy both straight into the model.
For `immutable` that is deliberate — insert-only means settable on create — and
`test/versionstore/versionstore_test.go:38` pins it in words ("`immutable` is
insert-only: settable on create, refused on update"). Nothing else says it. The
`db:",immutable"` tag and the `-readonly` flag diverge exactly here: the tag
keeps the column out of the patch body only, the flag sets `Excluded` and keeps
it out of both (`codegen.go:301-312`, `adapter.go:25`), and neither this file,
`docs/modules/en/vv-cli.md` nor either guide says so.
The escape has a trap in it. `-readonly TenantID` takes the column out of both
bodies, so the generated mapper leaves the model field zero — and the documented
multi-tenant policy `security.ScopeField` does not stamp it, it *compares* it
(`crud/decorators/security/policies.go:86-100`). Zero against the context tenant
is a mismatch, so the consumer who follows both pieces of advice denies every
create with "row belongs to a different TenantID".
For the primary key it is not deliberate at all. `field.Auto` is parsed and never
read — `grep -rn Auto internal/codegen/` returns the declaration
(`codegen.go:29`), the tag switch (`:272`) and the gorm table (`:452`) — so the
generator knows the key is database-filled and does not use the fact.
`POST /articles {"id": 99}` is a key the generated resource accepts.
**If not ready:** For the tenant, they leave it in the create body and wire
`security.Gate` with `ScopeField`, which refuses a mismatched tenant in Go
because an INSERT has no WHERE clause to narrow. That works and nothing writes it
down. For the key there is no answer at all: `-skip ID` and `-readonly ID`
produce a package that compiles and panics at start-up (see
[H-CODEGEN-09](#h-codegen-09--the-whole-resource-for-an-internal-admin-api)).
Closing it: `inputFields` drops `f.PK && f.Auto`, and the generated
`port.MustPathMap` carries the key's name in the `except` list it already
accepts — a non-auto key, the client-chosen UUID, stays.

### H-CODEGEN-05 — Filtering through a relation
**Who:** whoever builds the internal back office
**Wants:** "articles whose author is Ann", and then "articles whose comment's
author is banned", typed.
**Story:** They tag the relations on the model, regenerate, and write the filter
against the generated field names. The one-hop filter compiles. The two-hop one
does not, and they have to work out why.
**Must hold:**
1. After tagging the relation and regenerating, `Article_.Author.Name.Eq("Ann")`
   compiles with no further declaration.
2. The relation's own path is an identifier too, not a string literal.
3. Expansion stops somewhere and never loops on a cyclic schema.
4. When a relation is *not* expanded, the consumer is told which knob controls
   it — before they write the filter, not after the compiler refuses a field
   they cannot find.
**Today:** 🟡 partial
**Evidence:** 1, 2 and 3 hold. `render.go:186-270` — the depth bound at `:218`,
the cross-package cut at `:224`, the cycle cut at `:236`, the embedded handle at
`:203`. `_examples/example/blog/vv_gen.go:28-49` shows the three groups it
produces. `TestRelationsBecomeNestedAttributeStructs`,
`TestDepthBoundsHowFarRelationsExpand` and `TestRelationCyclesAreCutShort`
(`codegen_test.go:220`, `:333`, `:315`); `TestRelationGroupsCarryATypedPath`
(`:261`) uses the root as its control, since the root is reached through no
relation and must not be handed a path. The shadowing case — a target model with
a column called `Path` — is announced in the group's own doc comment
(`render.go:263-267`, `TestATargetColumnNamedPathIsCalledOut`). Guarantee 2 is
`specs`'s to keep: this module's share is emitting the embed with the right two
type arguments (`render.go:203`); the compile-time check on the model it lands on
is `specs.Rel[M, T]` (`crud/decorators/specs/metamodel.go:161`), validated at
package initialisation.
4 is one silence reached three ways, and all three are bare `continue`s inside
twenty lines: the depth cut (`render.go:218`), the target in another package
(`:224`), the cycle (`:236`). None writes to `g.log`; the only output on success
is `vv: wrote … (N models)` (`codegen.go:146`). The blog fixture is the ordinary
version: `Comment` carries `Author *Author rel:"belongs_to"`
(`_examples/example/blog/model.go:37`), and `ArticleCommentsAttrs`
(`vv_gen.go:36-43`) has no `Author` member, because the default `-depth 2`
stopped there. `-depth` is documented as a number in a flag table with nothing
saying that raising it is the answer to a field that is not there.
**If not ready:** They read `-depth` in the flag table and guess, or they raise it
blindly and watch the file double in size. One line per cut relation — the
model, the field, and which of the three reasons — closes all of it, and it is
the same line the ent consumer in H-CODEGEN-06 needs.

### H-CODEGEN-06 — Adopting structs another generator owns
**Who:** a team already on ent or gorm
**Wants:** the patch body and the typed names for entities they cannot annotate
and must not edit.
**Story:** They point the generator at the other tool's package, name the types,
declare that tool's timestamps server-owned, and write the result into a package
of their own.
**Must hold:**
1. A struct with none of this library's tags is still a model when it is named.
2. The generated file drops into a package that already has other files, without
   a package-name clash or a second `package` clause to reconcile.
3. The foreign model types are spelled so the generated file compiles.
4. The other tool's bookkeeping fields do not become columns.
5. What the flags took out is written into the file, because nothing at run time
   can see a flag.
**Today:** 🟡 partial
**Evidence:** 1, 2, 4 and 5 hold and are pinned. `codegen.go:219-221` makes a
named type a model without tags; `codegen.go:249-253` keeps a same-package struct
field (ent's `Edges`) out; `TestIntoAnotherPackageQualifiesTheModelTypes`,
`TestIntoAnExistingPackageKeepsItsName` and `TestIntoWithoutImportIsRefused`
(`codegen_test.go:443`, `:483`, `:499`); the exclusion list is emitted at
`adapter.go:91-95` and `:140`, with
`TestTheGeneratedAssertionNamesTheReadonlyExclusions` (`:751`). Both recipes are
checked in and regenerated by a drift test that compares the directive verbatim
(`test/codegen/codegen_test.go:38-80`).
3 is untested and carries the defect H-CODEGEN-07 names for column types.
`codegen.go:557` is `g.modelAlias = filepath.Base(g.modelImport)`, and
`render.go:69-71` emits the path unaliased. Both tests above set
`g.modelAlias = "ent"` by hand (`codegen_test.go:449`, `:492`), and nothing in
the tree reaches the derivation except `codegen.Run`, which no test calls. So
`-import example.com/app/ent/v2` writes `v2.User` under an import of a package
called `ent`, and `-import myapp/data-model` writes `data-model.User`, which does
not parse. The ORM guides are what tell people to use `-import`.
**If not ready:** The consumer adds an explicit alias — to a file they do not own,
in a directory another generator overwrites. The fix belongs in the emit loop:
`codegen.go:184-190` already records `alias -> path` from the import spec and
`extraImports` (`render.go:114-131`) already returns the pair; writing
`fmt.Sprintf("%s %q", alias, path)` when `alias != filepath.Base(path)` closes
this and the column-type half at once. Relations across packages are out of scope
by design ([[UC-014]]) — being told about them is H-CODEGEN-05's guarantee 4.

### H-CODEGEN-07 — A model with a money column, a UUID key and a status enum
**Who:** anybody past the tutorial
**Wants:** the same generated file, for the types real products use.
**Story:** The key is a `uuid.UUID`, the price is a `decimal.Decimal`, and the
status is a named string type. They regenerate and then try to filter: price
above a threshold, status in a set, slug containing a term.
**Must hold:**
1. The generated file compiles.
2. A column keeps the operators its underlying type can support — a range on a
   money column, ordering on a named string type.
3. When it cannot, the consumer is told rather than left to discover it at the
   call site.
**Today:** 🟡 partial
**Evidence:** Guarantee 1 is conditional on how the model spells its imports, and
the condition is narrow and sharp: it holds unless the import path's **last
segment is not the package's name**. `codegen.go:186` derives the alias with
`filepath.Base(path)`; `render.go:114-130` looks a column's prefix up under that
alias; `render.go:69-78` emits paths and throws the alias away. So a model
importing `github.com/gofrs/uuid/v5` unaliased is filed under `v5`, the prefix
`uuid.` never matches, and the import is missing from a file that references it;
one importing it as `uid` gets the path emitted under the wrong name. The same
loop appends without a seen-set, so one path reached through two aliases is
written twice. `gorm.io/gorm` happens to satisfy the condition, which is why the
existing third-party coverage passes and hides the rest of it:
`test/gormstore/vv_gen.go:9` imports `gorm.io/gorm` and `:25`, `:48`, `:57`,
`:81`, `:93`, `:102` declare `specs.Attr[…, gorm.DeletedAt]`, and
`TestGormModelIsFlattenedFromTheWellKnownTable` (`codegen_test.go:385`) asserts
the import string explicitly. No test in the tree generates for an import whose
last segment differs from its package name.
Guarantee 2 fails quietly. `codegen.go:354-374` chooses the attribute from the
type's *spelling* against a fixed list of builtin names, so `decimal.Decimal` and
`type Status string` both get `specs.Attr` — equality, membership, sorting, and
no `Gte`, `Between`, `Like` or `Contains`. Guarantee 3: nothing is printed.
Worth being exact about what a fix can reach. `Ord[M any, T cmp.Ordered]`
(`crud/decorators/specs/metamodel.go:77`) accepts `~string`, so a named string
type can have its range operators today with no API change. `Cmp[M any, T any]`
(`:118`) is already there "for a type that is not cmp.Ordered", which is what a
money column wants. But `Like`, `Contains` and `StartsWith` live only on
`Str[M any] struct{ Ord[M, string] }` (`:95`), which hardcodes `string`, and the
binder compares reflect types (`:257`) through `crud.ElemType`, which unwraps
`Opt` and pointers and nothing else (`crud/meta.go:521-529`). A metamodel field
declared `specs.Str[Article]` against a `Status` column panics at package
initialisation. Pattern operators on a named string type need `Str` to gain a
type parameter — a breaking change to an exported generic that is in the surface
baseline at `docs/api/surface.md:329`.
**If not ready:** For the imports, the consumer adds an explicit alias to their
own model file and hopes `goimports` does not drop it again; the real fix is in
the emit loop (H-CODEGEN-06). For the lost operators they fall back to the string
API, `crud.Gte("Price", v)`, which works and gives up exactly the compile-time
checking the metamodel exists for.

### H-CODEGEN-08 — A model that embeds a shared base struct
**Who:** anyone in a company with more than one service
**Wants:** `type Team struct { base.Model; Name string }` to generate like any
other model.
**Story:** The base struct carries `ID` and the three timestamps and lives in a
shared package every service imports. They run the generator, get a clean file,
and the package will not start.
**Must hold:**
1. Fields the runtime treats as columns are fields the generator treats as
   columns.
2. When the generator cannot read an embedded type, it says so at generate time.
3. If neither, the start-up refusal names a fix that works.
**Today:** ❌ missing
**Evidence:** None of the three. `codegen.go:461-478` resolves an embedded struct
from *this* package or from `wellKnownEmbeds`, and `wellKnownEmbeds`
(`codegen.go:450-457`) has exactly one entry: `gorm.Model`. Anything else returns
`nil, false` and `parseModel` (`codegen.go:222-231`) drops it with a bare
`continue`. The runtime flattens every embedded struct — `crud/meta.go:347` — so
reflection sees `CreatedAt` and `UpdatedAt` as ordinary writable columns.
`port.MustCoverUpdate` then panics with "no field for CreatedAt, UpdatedAt —
regenerate it, or declare the exclusion" (`port/pathmap.go:213-221`).
Regenerating changes nothing: the fields were never parsed. Declaring the
exclusion changes nothing either — `-readonly`/`-skip` are applied per parsed
field in `g.exclude` (`codegen.go:301-311`) and `m.excluded()` iterates
`m.Fields` (`:67-79`), so a name the generator never saw is a silent no-op and
never reaches the emitted exclusion list.
**If not ready:** The consumer moves the base struct into every model's own
package, or stops embedding it. Tagging the embedded field `db:"-"` makes the two
sides agree by dropping the columns from both — which costs them the primary key
if the base carries it, and then the schema refuses for a different reason
(`crud/meta.go:251-260`). Two things close it, and the second is the one that
scales: make `-readonly`/`-skip` names that matched nothing an error
(H-CODEGEN-05's guarantee 4 again), and resolve an embedded type from an import
the model already declares, the way `-import` resolves a model package — or, at
minimum, refuse the run naming the embedded type the generator could not read.
Failing silently and letting `MustCoverUpdate` mislead the consumer afterwards is
the worst of the three options and is what happens today.

### H-CODEGEN-09 — The whole resource, for an internal admin API
**Who:** the author of a back-office service with fourteen tables and no appetite
for fourteen handlers
**Wants:** a create body, a mapper, a mount line, and error bodies that name the
client's own keys.
**Story:** They turn the adapter flag on, regenerate, and mount each resource in
one line. A client posts an invalid body and reads back the field name it sent.
**Must hold:**
1. Mounting is one line per resource.
2. The generated map is the inverse of the generated mapper, because the two come
   out of one loop and not out of two decisions.
3. Delete an entry from the generated map by hand and the package refuses to
   start, naming the column.
4. Turning the flag on for a package does not depend on every struct in it being
   resource-shaped.
**Today:** 🟡 partial
**Evidence:** 1, 2 and 3 hold. `adapter.go:33-125` writes all five pieces from
one `inputFields` list read twice — `:73-77` for the mapper, `:86-90` for the
inverse — which is codegen's own share of the guarantee; the checking in both
directions is `port.NewPathMap`'s (`port/pathmap.go:98-129`), and a rejected
column being named the client's way is `port`'s and the bindings'.
`_examples/example/blog/vv_gen.go:139-145` is the mount line.
The tests that run against **real generated output** are
`TestTheGeneratedWireShapesLeaveOutWhatTheClientDoesNotOwn` and
`TestTheGeneratedMapperAndItsInverseAgree`
(`test/versionstore/versionstore_test.go:38`, `:73`), over the checked-in
`test/versionstore/vv_gen.go`; the second has a control — the version column
declines rather than being invented.
`TestAGeneratedResourceResolvesTheSameFieldOnAllThreeBindings`
(`test/portmount/mount_test.go:488`) is not one of them: its fixture says so in
its own comment at `:458-461` ("Written out by hand here because the generator
cannot run against a model declared inside a test file"), and its map uses a wire
name — `"Name": port.At("label")` — the generator would never emit. It proves the
bindings, not this module.
4 does not hold. `-adapter` is all-or-nothing for a directory: a model with no
single-field key returns an error from `renderAdapter` (`adapter.go:36`), and
`render.go:29-31` propagates it, so nothing is written for the other thirteen
models either. One join table or one keyless lookup row refuses the whole run,
and there is no way to say "adapter for these four, DTO and metamodel for the
rest". Separately, `-skip ID` — the obvious first move for
[H-CODEGEN-12](#h-codegen-12--a-public-api-whose-keys-are-not-go-field-names)'s
author — generates a package that compiles and panics: `exclude` sets `Excluded`
only when `!f.tagDropped()` (`codegen.go:306`), and `tagDropped()` is already
true for a PK (`:46-48`), so no exclusion literal is emitted; but `inputFields`
drops the field anyway on `f.Skip` (`adapter.go:25`), so it leaves `<Model>Input`
and `<Model>Paths` while `NewPathMap`'s domain still holds it
(`port/pathmap.go:104-109` over `s.Insert`, which is "written by INSERT,
including the PK", `crud/meta.go:96`). The same is true of `-skip` or `-readonly`
on any `immutable` column.
**If not ready:** They split the package, or they run a second directive with
`-types` and a different `-out` — which forces the *first* directive to gain a
`-types` list naming every other model, or both files declare `TeamUpdate` and
the package does not compile. That list is the central hand-maintained registry
the directive design exists to avoid, and a model left off it gets no artefacts
and no coverage assertion.

### H-CODEGEN-10 — One method of the generated resource has to do something else
**Who:** the same author, second month
**Wants:** to hash a password on create, or publish an event after update,
without giving up the other eight methods.
**Story:** They find the generated service shell, override `Create`, and
regenerate on Tuesday.
**Must hold:**
1. Overriding one method does not mean writing the other eight.
2. An override with the wrong signature is a build failure, not a service that
   quietly no longer mounts.
3. The override survives regeneration.
**Today:** 🟡 partial
**Evidence:** 1 and 2 are in the code and deliberate. `adapter.go:98-100` emits
`type <Model>Service struct { *port.DefaultService[…] }` precisely so one method
can be overridden, and `:104` emits
`var _ port.Service[…] = (*<Model>Service)(nil)`, which turns a bad override into
a build failure. 3 depends on the consumer knowing something nothing tells them:
the shell is inside the `DO NOT EDIT` file, so the override has to go in a second
file in the same package. A consumer whose first instinct is to edit `vv_gen.go`
loses the change on the next regeneration, and the drift test in their own repo —
if they wrote one, see H-CODEGEN-03 — turns red for a reason that reads like a
tool bug.
**If not ready:** Nothing to build. The shell's doc comment already says
"somewhere to override one method without writing the other eight"; it does not
say *where*, and the `-adapter` section of `docs/modules/en/vv-cli.md` ends at
the mount line. One sentence and a three-line example close it.

### H-CODEGEN-11 — The same generated resource, on the framework the team already uses
**Who:** a team on Gin or Fiber
**Wants:** the generated resource mounted on their router.
**Story:** They generate with the wiring turned off and write the mount
themselves.
**Must hold:**
1. Turning the wiring off is a flag, not a hand-edit.
2. Asking for a framework that is not generated is a message, not a broken file.
**Today:** ✅ ready
**Evidence:** `codegen.go:521-531` accepts `net` and `none` and refuses the rest
with "only net and none are generated today". The reason it stops there is
binding: a generated file in this library may not import a satellite module
([[D-033]]), and `docs/roadmaps/Roadmap.md:137-140` records it as open rather
than settled, with `-binding none` plus `ServingFor` named as the way in. The
hand-written mount is one statement, the same one the generated `net` wiring
contains: `crudgin.ServingFor(svc, ArticleMapper{}).Mount(r, "/articles")`
(`crud/http/crudgin/handler.go:111`, `:136`; `crud/http/crudfiber/handler.go:106`;
`docs/modules/en/crudgin.md:62`). Nothing tests the refusal — see release
blocker 6, which is wider than this one message.
**If not ready:** —

### H-CODEGEN-12 — A public API whose keys are not Go field names
**Who:** the author of a documented, versioned, external API
**Wants:** `author_id` on the wire, or whatever the published schema already
says.
**Story:** They generate the resource, look at the create body, and find
`authorID`.
**Must hold:**
1. The wire names are the consumer's decision, or the generated resource is not
   used for a public API.
2. Whichever way they go, the inverse map still cannot drift.
**Today:** ❌ missing, deliberately
**Evidence:** `codegen.go:386-399` lower-cases the leading run of capitals and
nothing else, so `AuthorID` becomes `authorID`. There is no case setting.
[[D-050]] argues at length for one naming rule across both bodies and against
honouring the model's own `json` tags, and states the consequence plainly: a
generated resource has a wire shape of its own. Guarantee 2 survives the escape
hatch — `port.MustPathMap` is exported (`port/pathmap.go:154`) and checks a
hand-written map exactly as it checks a generated one.
**If not ready:** They do not generate the adapter. They hand-write the body, the
mapper and the map — roughly twenty-five lines per resource — and keep the
start-up check. A uniform `-case snake` would not contradict [[D-050]]'s
reasoning, which forbids following the *model's* tags rather than choosing a
house style. It would have to move three artefacts in lockstep, not two:
`<Model>Update`'s json tags (`render.go:152`), `<Model>Input`'s
(`adapter.go:60`) and the `port.At(…)` values in `<Model>Paths`
(`adapter.go:89`), because `MustPathMap` checks Go field names against the model
and the `At` values are what the client sees. `<Model>Update` ships for every
consumer, `-adapter` or not, so such a flag rewrites the PATCH wire shape of
people who never asked for a resource. That is the argument for defaulting it
off, and it is stronger than "it would change an existing resource".

### H-CODEGEN-13 — Reviewing the diff
**Who:** whoever approves the pull request
**Wants:** to tell a real change from a reshuffle in five seconds.
**Story:** They open the generated file in the diff, see two changed lines, and
approve.
**Must hold:**
1. Same input, same bytes — models and imports in a stable order.
2. The file announces that it is generated.
3. A diff can be attributed: this changed because the model changed, not because
   the tool did.
**Today:** 🟡 partial
**Evidence:** 2 holds (`render.go:42`). 1 holds for everything anybody has run
into: `codegen.go:213` sorts the models, `render.go:80` sorts the imports,
`codegen.go:67-79` sorts the exclusion list, and
`TestOutputIsByteIdenticalAcrossRuns` (`codegen_test.go:555`) runs eight times
over both halves and asserts the model order. It has one counterexample the test
cannot see: `g.imports` is keyed by package *name* and filled while ranging
`pkg.Files`, which is a map (`codegen.go:183-191`), so two files in the model
package importing `github.com/foo/uuid` and `github.com/bar/uuid` make
`g.imports["uuid"]` last-writer-wins in map order. `sort.Strings` at
`render.go:80` sorts the survivor and cannot recover the other. Narrow, and it
shares a root cause with the alias hole in H-CODEGEN-07.
3 does not hold: `render.go:42` writes no version, so an upgrade that changes the
template produces a diff on every generated file in the repository with nothing
in the file saying why.
**If not ready:** The reviewer reads the library's changelog, or guesses. A
version in the header is the obvious fix and it has a cost — every generated file
in the tree changes on every release, which is the reason to think about it
rather than to do it quickly.

## The DX this should have

### The call site

```go
//go:generate go run github.com/frostgrove/vv/cmd/vv
```

```bash
go generate ./...
```

Two lines today, two lines in the ideal. That is the strongest thing this module
has and nothing below asks to change it.

### Turning one knob

```go
// one line, because go generate has no continuation: a wrapped directive is one
// directive and two inert comments
//go:generate go run github.com/frostgrove/vv/cmd/vv -adapter -readonly Team.TenantID,Team.CreatedAt -attr decimal.Decimal=cmp
```

Two additions, on the line that is already there.

`-readonly` and `-skip` accept `Model.Field` as well as a bare name, and a name
that matches nothing is an error rather than a shrug. The bare form stays
package-wide, so today's directives keep working.

`-attr` classifies by the **type**, not by the field: `decimal.Decimal=cmp`,
`Status=ord`, or `github.com/shopspring/decimal.Decimal=cmp` when two packages
collide. One entry then covers every model in the package and every package that
uses the type, it is stable under a field rename, and it needs no `Model.Field`
grammar of its own. It is also a lookup in front of a switch that already
dispatches on the type spelling (`codegen.go:362-373`), which is why it is one
flag and not a feature.

Three things are missing from that line on purpose. The auto primary key leaving
the create body is a **default**, not a flag — `field.Auto` is already parsed.
Server-owned-on-create is already `-readonly`; what it needs is a sentence saying
so, and a second saying that the model field is then yours to stamp. And the CI
answer stays what it is:

```bash
go generate ./... && git diff --exit-code
```

`-check` is worth adding as the per-directory spelling for a Makefile that will
not write (`go run github.com/frostgrove/vv/cmd/vv -check`), and it is worth
saying in the same breath what it does not do: it is regenerate-and-diff by
another name, it catches a file stale for this generator version, and it can
never stand in for the start-up assertion.

### Why this shape

The directive is the configuration, and it lives three lines above the model it
configures. The generator reads one directory per run (`codegen.go:151`), which
is what makes the directive the right home for the settings, and `go generate
./...` the command — inside one module. A monorepo of several modules needs one
invocation per module, and this repository is the example, not the counter-example
it was cited as before: `Makefile:415-420` is five commands, and [[D-018]] records
that a store added without a line there is silently never regenerated. That is
`go generate`'s boundary, not this tool's, and a `-check` over `./...` would have
to re-discover every directive — a second copy of the flags, which is the failure
`test/codegen/codegen_test.go:57`, `:68`, `:77` exist to prevent by comparing each
directive verbatim.

The per-model qualification exists because the flag it fixes is the one written
for foreign models, and foreign packages hold twenty entities that share field
names. It has a wrinkle worth knowing before it is built: `g.exclude` is also
called from `embedded()` (`codegen.go:461-479`), where no model name is in
scope — which is exactly where gorm's `-readonly UpdatedAt,DeletedAt` lands.

The classification flag exists because a metamodel that silently drops `Gte` from
a money column is worse than one that refuses: the consumer does not discover it
until they need the operator, and by then the string API is right there and the
compile-time checking is quietly gone.

### What it must not break

- [[D-014]] — deterministic output. `-attr` is a sorted lookup and touches
  nothing about ordering; the existing violation is `g.imports`
  (H-CODEGEN-13) and closing it is a prerequisite, not a consequence.
- [[D-018]] — `-skip` and `-readonly` stay two flags, because they differ in
  whether the column survives in the metamodel; the exclusion list stays in the
  file, since reflection cannot see a flag. A qualified `-readonly Team.CreatedAt`
  renders into Team's assertion as `"CreatedAt"` and nowhere else, so that
  invariant is untouched. Making an unmatched name an error is new behaviour for
  a directive that is currently green, and it will fail some existing directive
  somewhere — that is the point of it.
- [[D-050]] — every generated artefact stays total, and one naming rule serves
  both bodies. Dropping the auto key from the create body is a change *inside*
  that rule, not around it: `MustPathMap` already takes an `except` list and the
  generator already emits one, so both directions stay checked. And the decision's
  own line — two derivations that share a source are one derivation — is what
  bounds `-check`.
- [[D-033]] — a generated file in this library may not import a satellite module,
  so generated Gin and Fiber wiring stays out however often it is asked for.
- [[D-021]] — the magic stays concentrated. Resolving an embedded type from an
  import the model already declares (H-CODEGEN-08) is more of the parsing the
  generator already does; type-checking the whole package is not, and would not
  work anyway while its generated half is stale.
- **The exported-surface baseline**, `docs/api/surface.md`. `-attr` needs no
  change to it: `Ord`, `Cmp` and `Attr` already exist. Pattern operators on a
  named string type do — `Str[M any]` (`:329`) would have to become
  `Str[M any, T ~string]`, and every checked-in `vv_gen.go` spells `specs.Str[M]`
  today. CLAUDE.md's rule is that after the first tag a line that disappears from
  that baseline is a breaking change. **This is a deliberate challenge**: either
  it happens before the tag or H-CODEGEN-07's guarantee 2 is narrowed to the
  range operators and the pattern half is written down as an accepted limit.

## DX verdict

| What the ideal asks for | Today | Distance |
|---|---|---|
| One directive, no flags, for a package you own | Exactly that: 2 lines, and 2 in the ideal | none |
| The same command when the model changes, and a refusal when you forget | Exactly that, and the refusal names the column and the type | none |
| Generated wiring for Gin or Fiber | `-binding none`, then one statement per resource — the same one the generated `net` wiring contains | none in lines, and deliberate |
| A CI answer that writes nothing | `go generate ./... && git diff --exit-code`. Works, needs a writable checkout and a toolchain, documented in neither guide | small |
| Per-model exceptions | Bare field names matched across the package, or a second directive with `-types` — which forces a `-types` list onto the first one too | small in flags, large in what the second directive costs |
| A column type the generator cannot classify | Nothing: the plain attribute, no message, `crud.Gte("Price", v)` as the fallback. Flag surface goes 14 → 15 to fix it | small in lines, large in what the metamodel was for |
| A model whose column type comes from a versioned or renamed import | The generated file may not compile, and today's fix is an alias in a file the consumer may not own | large |
| A column the server owns, on a create | In the create body and mapped through. `-readonly` takes it out of both bodies and leaves the model field zero, which denies every create behind the documented tenant policy | large |
| A model embedding a base struct from another package | Nothing works, and the start-up refusal names two fixes that are both no-ops | large |
| Overriding one method of a generated resource | Works, in a second file in the same package, and nothing says so | small |
| Your own wire names on a public API | Do not generate the adapter; hand-write the body, the mapper and the map, keeping the start-up check | large, and argued for |
| A diff a reviewer can attribute | Deterministic and stable, with no version marker | small |

**Overall:** For the case it was built for — your own package, your own builtin
types, one directive — this is as short as it can be, and the start-up refusal is
the part most libraries leave out. Three of the twelve rows are large, and none of
them is a flag away. Two are about what the generator can only recognise by
*spelling*: a package name it never verified, a type name it matches against a
fixed list. The third is `-adapter`, where the short path is not wordy but wrong —
the create body carries the key and the columns the server owns, the one flag that
removes them removes them too far, and a shape the runtime handles fine cannot be
generated for at all. Customising never means abandoning the short path; the
problem before the tag is not ceremony, it is that the default output of
`-adapter` is not what H-CODEGEN-04's consumer thinks they asked for.

## Release blockers found here

| # | What | Severity | Why it blocks |
|---|---|---|---|
| 1 | With `-adapter`, the create body carries the auto primary key and maps it into the model; `POST {"id": 99}` is accepted, and both `-skip ID` and `-readonly ID` produce a package that compiles and panics at start-up | blocker | `field.Auto` is parsed and never read, so the generator knows the key is database-filled and hands it to the client anyway, and the consumer has no way out that is not a crash |
| 2 | A model embedding a base struct from any package but `gorm.Model` is dropped by the generator and flattened by the runtime, and the resulting start-up panic tells the consumer to regenerate or declare the exclusion — both no-ops | blocker | It is the commonest Go model shape after the plain struct, it fails with no exit, and the error message actively points the wrong way |
| 3 | Nothing says the create body carries `immutable` columns; `-readonly` is the only way out and leaves the model field zero, which denies every create behind the documented `security.ScopeField` policy | serious | A consumer who reads the multi-tenant guidance, tags the column and generates the adapter ships either a client-settable tenant or an endpoint that refuses every create |
| 4 | Import aliases are derived with `filepath.Base`, for column types and for `-import` alike, so a versioned or renamed path produces a file that does not compile or qualifies models with a name nothing binds | serious | The first UUID-keyed model, and every ent adoption whose module path ends in `/v2`, and the emit loop already holds the alias it needs |
| 5 | The metamodel attribute is chosen from the type's spelling, so a named type or a foreign one silently loses its range and text operators | serious | A price range or a status ordering on a real product's model has no typed spelling, and the consumer finds out at the call site with nothing explaining why |
| 6 | Nothing in the tree runs `codegen.Run` or `codegen.Options` — every test builds a `*generator` literal and calls `g.run` | serious | The whole flags-to-generator translation is unproven: the `-binding` refusal, the `-types` split, the `modelAlias` derivation, the package defaults and the `-into`/`-out` join, which is where two of the findings above live |
| 7 | Four silences: the depth cut, a relation whose target is in another package, the cycle cut, and any `-types`, `-skip` or `-readonly` name that matched nothing | serious | Every one ends as a compile error about a field that was never generated, or as a flag that did nothing, and the only output on success is how many models were written |
| 8 | `-adapter` is all-or-nothing for a directory: one keyless struct refuses the run for every other model in the package | sharp edge | A join table or a lookup row is ordinary, and the workaround converts the package from directive-driven to a hand-maintained `-types` list on both directives |
| 9 | `-skip` and `-readonly` match bare field names across every model in the package | sharp edge | The flags exist for foreign packages, and foreign packages are exactly where twenty entities share a field name |
| 10 | No mode that asks whether the checked-in artefacts are current without writing them, no CI recipe in either guide, and no `tool`-directive or vendoring story | sharp edge | The module's remit is keeping artefacts from drifting; a vendored or hermetic build cannot run the generator at all, since nothing imports `cmd/vv` |
| 11 | No version marker in the generated header, and `g.imports` is last-writer-wins across files in map order | sharp edge | An upgrade that changes the template diffs every generated file with nothing saying why, and two imports sharing a last path segment are a counterexample to "same input, same bytes" |

[[UC-014]] is this module's only use case and still reads **covered**, with the
attribute-spelling problem recorded as "one smaller thing, still open". Blockers
1, 2, 3, 4, 6, 7, 8, 10 and 11 are not in it, and blocker 5 is the smaller thing
graded higher here because it is the one a real product's model hits first. My
read is that the sweep is right and `docs/ai/usecases/Index.md:85` is stale;
either UC-014's Status moves or these gaps join the list there, but the two
documents should not disagree quietly.

## Contested

- **"No test in the tree has ever generated for a model with a third-party field
  type."** Withdrawn — it was false. `test/gormstore/vv_gen.go:9` has imported
  `gorm.io/gorm` all along. The narrower claim is what stands, and it is sharper:
  no test generates for an import whose last path segment differs from its
  package name, which is precisely why `gorm.io/gorm` passes and hides the rest.
- **Classification keyed on the type, not on `Model.Field`.** One reviewer asked
  for `-attr Team.Price=cmp` to match the qualified `-readonly`. Kept the
  type-keyed form: a type is what `attrType` already switches on, one entry then
  covers every package that uses the type, and it survives a field rename. The
  qualified grammar stays where it is needed — the two flags that name a column
  the model does not describe.
- **`-check` on the directive rather than `VV_CHECK=1 go generate ./...`.** The
  environment variable needs no directive edits, which is real. Kept the flag:
  an environment variable that silently changes whether a build command writes
  files is the kind of second place for one setting this tree already treats as a
  defect, and the recipe that works today needs neither.
- **H-CODEGEN-11 graded ✅ although nothing tests the `-binding` refusal.** A
  guarantee that holds in the code is ✅; the missing test is blocker 6, which is
  wider than that one message. The previous draft graded the same kind of
  unproven-but-true guarantee both ways in one file.
