# cmd/vv — the generator

```bash
go run github.com/frostgrove/vv/cmd/vv
```

```go
//go:generate go run github.com/frostgrove/vv/cmd/vv
```

**Module:** root · **Depends on:** `go/ast`, `go/parser`, `go/types`

The update DTO and the metamodel are mechanical restatements of your model, so
this writes them. With `-adapter` it writes the rest of the resource too: the
request body, the mapper, **the inverse of that mapping**, a service shell and
the wiring.

There are five subcommands, and they write different artefacts:

| Command | Writes | Page |
|---|---|---|
| `vv generate` (or no subcommand) | `vv_gen.go` — the update DTO, the metamodel, the repository blueprint | this one |
| `vv generate resource` | `vv_wire_gen.go` and `resource.manifest.yml` — the **public** create, patch and response bodies | [below](#generate-resource) |
| `vv generate routes` | `vv_routes_gen.go` and `routes.manifest.yml` — the operation an application route and its guard both read | [below](#generate-routes) |
| `vv generate module` | `vv_module_gen.go` and `module.manifest.yml` — the module a composition root registers | [below](#generate-module) |
| `vv generate cache` | `vv_cache_gen.go` and `cache.manifest.yml` — the cache scope set | [cache](cache.md) |

For a package, it reads exported structs in `model.go`, `*.model.go`, and
`*_model.go`, then writes `vv_gen.go` next to them. Ordinary Go fields need no
`db`, GORM, or other ORM tags: the normal snake-case column and plural table
conventions apply, and `ID`/`Id` is the primary key. `db` and `rel` are only
for exceptions. `vv generate -dir ./src/app` walks the tree and generates one
file beside each model package.

---

## What comes out

From this:

```go
type Article struct {
    ID          int64               `db:"id,pk,auto"`
    Title       string              `db:"title"`
    Rating      *float64            `db:"rating"`
    PublishedAt utils.Opt[time.Time] `db:"published_at"`
    TenantID    int64               `db:"tenant_id,immutable"`
    CreatedAt   time.Time           `db:"created_at,generated"`

    Author   *Author   `rel:"belongs_to"`
    Comments []Comment `rel:"has_many"`
}
```

**The update DTO** — pointers for optional columns, `Opt` for nullable ones, and
nothing at all for the key, the immutable and the generated columns:

```go
type ArticleUpdate struct {
    Title       *string             `json:"title,omitempty"`
    Rating      utils.Opt[float64]   `json:"rating,omitzero"`
    PublishedAt utils.Opt[time.Time] `json:"publishedAt,omitzero"`
}
```

**The metamodel**, expanded through relations:

```go
var Article_ = specs.Metamodel[Article, ArticleAttrs]()

Article_.Views.Gte(100)                 // "views" >= $1
Article_.Author.Name.Eq("Ann")          // EXISTS (… authors … name = $1)
Article_.Comments.Approved.Eq(true)     // EXISTS (… comments … approved = $1)
Article_.Author.Name.Desc()             // ORDER BY (SELECT … LIMIT 1) DESC
```

Every one of those is compile-time typed and validated against the schema at
package initialisation, so **a renamed column breaks the build** rather than a
request. Relation expansion stops at `-depth` (2 by default) and never walks back
into a model already on the path.

Each expanded relation also carries its own **path** as a handle, so the settings
and options that take a path rather than a predicate are identifiers too:

```go
Article_.Comments.Path()                // "Comments"
Article_.Comments.Author.Path()         // "Comments.Author"

sqlrepo.RelationScope(Article_.Comments.Path(), specs.Predicate(Comment_.Approved.Eq(true)))
crud.Preload(Article_.Comments.Path())
```

The handle records the model the path lands on, so pointing one at the wrong
model is refused at package initialisation. It is embedded, so a target model
with a column called `Path` shadows the method. The generated file says nothing
about it, and `RelPath()` is the spelling nothing shadows.

**And a coverage assertion**, whether or not `-adapter` is on:

```go
func init() {
    port.MustCoverUpdate[Article, ArticleUpdate]()
}
```

Add a column and forget to regenerate, and the package **refuses to start**,
naming the column ([[UC-014]]).

> It reads the **compiled struct**, not the generator's view of the source —
> which is what lets it disagree with the checked-in file at all. Regenerating
> and diffing only ever measures the generator against itself.

---

## Embedded model bases

A completely untagged, value-embedded, non-scalar struct is flattened exactly
as runtime metadata flattens it. The generator resolves local aliases,
instantiated generic bases and exported dependency data, so an ordinary shared
base package needs no registration. `gorm.Model` keeps its audited built-in
semantics. Anonymous scalar structs follow runtime's exact method-set rule:
`time.Time`, driver `Valuer`/`Scanner` shapes and text marshal/unmarshal shapes,
including scalar pointers, stay one column.

An explicit `db` or `rel` tag belongs to the anonymous field itself and prevents
flattening. A struct-shaped `rel` field follows the normal relation rules;
`rel:""` preserves tag presence and asks runtime to infer the kind, including
through local type aliases, while `rel:"-"` suppresses that relation. A scalar
with a non-`-` `rel` tag is refused; scalar `rel:"-"` remains a column.
`db:"-"` excludes the whole anonymous
field and is the low-level escape hatch. An untagged pointer to a non-scalar
struct is refused, matching runtime metadata.

If Go type information cannot resolve an anonymous type, or an exported base
column has a private named type or a structural type with a foreign unexported
field/method identity the generated package cannot reproduce, generation fails
before writing and names the model and type. Resolve the dependency, flatten
the fields, or explicitly exclude the whole embed with `db:"-"`.
If flattening would create duplicate effective Go field names or database
columns, generation likewise refuses before rendering.

The exclusion is deliberately explicit and is the low-level escape hatch: it
means those embedded fields are not database columns. It is not a way to retain
the base's columns without teaching the generator their types.

---

## Flags

| Flag | Default | Does |
|---|---|---|
| `-dir` | `.` | the package directory to read |
| `-out` | `vv_gen.go` | the output file name |
| `-types` | tagged structs and exported structs in model files | comma-separated model names |
| `-depth` | `2` | how far to expand relation paths into the metamodel |
| `-skip` | — | field names to leave out entirely, like `db:"-"` |
| `-readonly` | — | field names kept out of the DTO but still filterable and sortable, like `db:",immutable"` |
| `-into` | `-dir` | write somewhere else |
| `-import` | — | import path of `-dir`, so model types are qualified when written elsewhere |
| `-no-dto` | off | skip the update DTOs |
| `-no-meta` | off | skip the metamodels |
| `-no-repo` | off | skip the repository blueprint and binding factory |
| `-recursive` | off | walk model files below `-dir` and generate beside each package; implied by `vv generate` |
| `-adapter` | **off** | also generate the resource adapter |
| `-binding` | `net` | which transport the generated wiring is written for: `net` or `none` |
| `-specs` | the specs package | import path override |
| `-crud` | the crud package | import path override |
| `-check` | off | render everything and compare with what is on disk instead of writing; a file that differs or is missing is reported by path, and nothing is written |

`-check` is what belongs in CI. It fails naming every package whose artefacts
are behind their models — not only the first one the walk reaches — so one run
tells you the whole list.

Without `-types`, exported structs in `model.go`, `*.model.go` and `*_model.go`
are models by convention; elsewhere a `db`/`rel` tag opts a struct in.

`-import` is a path, not a requested Go identifier. The generator reads the
package declaration in `-dir` as the preferred alias, so
`-import example.com/acme/models/v2` with `package models` normally produces
`models.User`, not `v2.User`; a reserved/colliding name receives a readable
path-derived alias. Renamed column imports
are preserved. Collisions receive deterministic path-derived names:
`/alpha/common` and `/beta/common` become `alphaCommon` and `betaCommon`, with
no numeric collision fallback. Composite and generic types bring every
selector import with them,
and one path is emitted once even when generated support code uses it too. Dot
imports are refused. When output stays in the model package, if a source import
itself is called `ProductUpdate` while generation will declare `ProductUpdate`,
rename that source import; Go rejects the collision across files and the
generator reports it before writing. With `-into`, package declarations and
file import aliases already in the destination are checked in both directions.
Only imports that survive into the final rendered file participate, so a local
adapter selector such as `out.ID` cannot resurrect an unused source import.

## `-adapter`: the rest of the resource

Off by default, because turning it on rewrites the wire shape of every resource
that switches to the generated wiring.

```go
type ArticleInput struct{ … }                      // the create/replace body
type ArticleMapper struct{}                        // port.Mapper + errs.Resolver
var  ArticlePaths = port.MustPathMap[Article](port.PathMap{ … })
type ArticleService struct{ *port.DefaultService[Article, int64, ArticleUpdate] }
func MountArticle(mux *http.ServeMux, prefix string, svc, opts ...)
```

An integral primary key — whether tagged `pk` or selected through runtime's
`ID` lookup (field or column), then its `id` column fallback — is database-owned
by default and is deliberately absent from `ArticleInput` and `ArticlePaths`.
Add `noauto` when an integral key is assigned by the caller. An assigned UUID,
slug, or other non-integral key remains in both.
Explicit `auto` remains available for non-integral database-generated primary
keys. The generated `MustPathMap` exclusion records the same decision and keeps
its start-up coverage check exact.

**`ArticlePaths` is why the flag exists.** It maps a model field back to the key
the client sent, so an error body names `authorID` rather than `AuthorID` — and
because it is generated *with* the mapping it inverts, `MustPathMap` can insist
it covers every column a request carries, and refuse to boot when it stops
([[D-050]]).

A hand-written inverse is wrong the first time somebody renames a key, and the
symptom is a wrong `field` in a production error body.

`ArticleMapper` satisfies `errs.Resolver`, so `port.Hops` picks it up and **no
binding changes at all**.

> The generated body derives its JSON names from the **Go field names**, not from
> the model's own `json` tags. That is deliberate — one rule for both bodies, so
> one inverse map serves the resource — and it means a generated resource has a
> wire shape of its own. Mount with `New` and generate no adapter if the model's
> shape is the API you want.

`-binding net` writes the `net/http` wiring; `-binding none` leaves it out, for
mounting on Fiber or Gin with `ServingFor` yourself. Fiber and Gin wiring is not
generated, because a generated file in this library may not import a satellite
module ([[D-033]]).

## generate resource

The public bodies. `vv_gen.go` is the **persistence** half: `ArticleUpdate` is what an `UPDATE` may
write, including the columns only your own code writes. It is not a promise to a
client, and parameterising a public PATCH binder with it makes it one
([[D-105]]).

```bash
go run github.com/frostgrove/vv/cmd/vv generate resource -dir ./src/mod
```

writes `vv_wire_gen.go` beside each model package:

```go
type ArticleInput struct{ … }                 // the create body
type ArticleInputMapper struct{}              // port.Mapper[ArticleInput, Article]

type ArticlePatch struct{ … }                 // the PATCH body
type ArticlePatchMapper struct{}              // wire.PatchMapper[ArticlePatch, ArticleUpdate]

type ArticleResponse struct{ … }              // the answer body
type ArticlePresenter struct{}                // wire.Presenter[Article, ArticleResponse]

func init() {
    wire.MustCoverCreate[Article, ArticleInput]("ID", "CreatedAt")
    wire.MustCoverPatch[ArticleUpdate, ArticlePatch]("TenantID")
    wire.MustCoverResponse[Article, ArticleResponse]()
}
```

Mount them with the binding's explicit constructor — the same name on all four
transports:

```go
crudfiber.ServingWire(svc, ArticleInputMapper{}, ArticlePatchMapper{}, ArticlePresenter{})
```

`New`, `NewFor`, `Serving` and `ServingFor` are unchanged and still take the
model and the persistence DTO. See [wire](wire.md).

### The manifest

Each body is derived by **narrowing** — the widest set that is safe, not the
widest set that is possible — and the result is written to
`resource.manifest.yml` beside the package, where it is checked in and reviewed:

```json
{
  "format": 1,
  "generated_by": "vv generate resource",
  "package": "blog",
  "resources": [
    {
      "model": "Article",
      "patch": {
        "narrowed": ["Rating", "Title"],
        "fields": ["Title"],
        "widened": [],
        "derivation_fingerprint": "3850a0…",
        "confirmed": false
      }
    }
  ]
}
```

| body | starts from | drops |
|---|---|---|
| create | the create field set — no relation, no `generated`, no lock, no database-owned key | `secret` |
| patch | the columns `ArticleUpdate` writes | `secret` |
| response | every column | `secret`, and anything `-skip` removed |

- **Narrowing needs nothing.** Delete a name from `fields` and the next run
  generates a smaller body and declares the omission in the coverage assertion.
- **Widening is signed.** Put back a name the narrowing excluded and it appears
  in `widened`; generation stops with an error naming `Article patch` until
  `confirmed: true` sits beside it. The manifest is still written so you have
  the line to edit; the Go file is not.
- **A confirmation does not outlive its derivation.** `derivation_fingerprint`
  is over the narrowed set, so a model that gains or loses a column asks again.
- **An impossible field is an error, not a question.** A `generated` column
  offered as a patch field is refused outright — no amount of confirming would
  make a mapper for it.

### Flags

| Flag | Default | Does |
|---|---|---|
| `-dir` | `.` | the package directory to read |
| `-out` | `vv_wire_gen.go` | the generated Go file name |
| `-manifest` | `resource.manifest.yml` | the manifest file name |
| `-types` | tagged structs and exported structs in model files | comma-separated model names |
| `-skip` | — | field names to leave out entirely |
| `-readonly` | — | field names kept out of the patch body |
| `-into` | `-dir` | write somewhere else; needs `-import`, and cannot be combined with `-recursive` |
| `-import` | — | import path of `-dir`, so model types are qualified when written elsewhere |
| `-recursive` | **on** | walk model files below `-dir` and generate beside each package |
| `-check` | off | render and compare, writing nothing; both artefacts are checked |

Under `-recursive` the walk finishes before it refuses. One run names every
stale package, and every body still waiting for confirmation, prefixed by the
directory it is in — the same rule the model generator follows, so a tree does
not take one run per package to unblock.

Both artefacts are refused if the file under that name was not written by this
generator, so an authored `resource.manifest.yml` is never overwritten.

## generate routes

For a use case that is **not** a CRUD resource. A CRUD route reads its
permission off the policy its repository is gated with ([[D-107]],
[crudhttp](crudhttp.md)). An operation — a dead-jobs listing, a password reset —
has no table to derive from: it guards itself in its own body, and the route
beside it declares the same permission a second time by hand. This generates the
value both of them read instead ([[D-109]]).

```bash
go run github.com/frostgrove/vv/cmd/vv generate routes -dir ./src/mod
```

Given

```go
func (this *DeadJobsUseCase) List(ctx context.Context) (DeadJobsView, error) {
    if _, err := access.Require(ctx, PermJobsRead); err != nil {
        return DeadJobsView{}, err
    }
    …
}

func (this *Handler) Access() []authhttp.Endpoint {
    return []authhttp.Endpoint{
        authhttp.Requires(fiber.MethodGet, "/ops/jobs/dead", PermJobsRead),
    }
}
```

the guard is read, the declaration naming the same permission is paired with it,
and the pair is written to `routes.manifest.yml`:

```json
{
  "format": 1,
  "generated_by": "vv generate routes",
  "package": "ops",
  "operations": [
    {
      "operation": "DeadJobsUseCase.List",
      "policy": ["PermJobsRead"],
      "method": "GET",
      "path": "/ops/jobs/dead",
      "source": "inferred-from-guard",
      "guard_fingerprint": "9f21c4…",
      "confirmed": false
    }
  ]
}
```

Until `confirmed: true` sits beside it, `vv_routes_gen.go` is a file that does
not compile:

```go
var VVRouteSet vvRouteSet = "confirm every operation in routes.manifest.yml"
```

Confirmed, it is the carrier:

```go
var OperationDeadJobsUseCaseList = Operation{…}

func Operations() []Operation
func Declarations() []authhttp.Endpoint
```

which both sides then read, and neither writes:

```go
func (this *Handler) Access() []authhttp.Endpoint { return Declarations() }

access.Require(ctx, OperationDeadJobsUseCaseList.Permissions()...)
```

After that the run sees a guard bound to the operation, records `source:
bound-to-operation`, and stops asking: what the confirmation stood in for is a
compiler fact now.

- **The guard is the source.** The route is derived from it, never the other way
  round — the permission that runs is the one that is documented ([[D-073]]).
- **A declaration nothing enforces is an error.** A route declaring a permission
  no use case in the package checks fails generation, naming the route and the
  line. That direction is the dangerous one: an audited surface whose audit is of
  the wrong list.
- **A confirmation does not outlive what it was given for.** `guard_fingerprint`
  covers the operation, the guard's policy and the *inferred* route. Change the
  permission the guard enforces and the confirmation drops and the build stops. A
  method and path you wrote into the manifest yourself are outside it, so filling
  in a route the generator could not infer does not invalidate the confirmation
  you wrote in the same edit.
- **A permission the generator cannot resolve stops the run.** A policy written
  as `perm.Read` needs one package behind `perm`. An import alias is bound per
  file, so if two files of the package bind `perm` to two different paths, the
  generated file — which has one import block — could only pick one, and the
  operation would be declared and enforced under a permission its guard never
  named. The run is refused instead, naming both paths and the file each is
  written in. An alias no policy is written with is left alone.
- **The generator imports no authorization package.** `-guard` is a string. If
  your permissions are checked by something other than `auth/access`, name it.

### Flags

| Flag | Default | Does |
|---|---|---|
| `-dir` | `.` | the package directory to read |
| `-out` | `vv_routes_gen.go` | the generated Go file name |
| `-manifest` | `routes.manifest.yml` | the manifest file name |
| `-guard` | `github.com/frostgrove/vv/auth/access` | import path whose function a use case calls to enforce a permission |
| `-guard-func` | `Require` | the function name inside `-guard`; its arguments after the context are the policy |
| `-declare` | `github.com/frostgrove/vv/auth/http/authhttp` | import path whose `Requires` declares a route |
| `-auth` | `github.com/frostgrove/vv/auth` | import path that owns `Permission` |
| `-recursive` | **on** | walk packages below `-dir` that call the guard and generate beside each one |
| `-check` | off | render and compare, writing nothing; both artefacts are checked |

The walk collects rather than stops: one run names every operation waiting for
confirmation and, under `-check`, every stale package, each prefixed by its
directory. A confirmation outranks drift, because there is nothing worth
comparing while an operation is still waiting for a person.

## generate module

For the composition root. [[D-106]] gave a bounded context a value — a
`module.Definition` naming constructors, sorted into five kinds, with a
`Profile` deciding which of them a process runs. This is where the list comes
from, so that it is not a two-hundred-line `fx.Module` sheet nobody reads
([[D-110]]).

```bash
go run github.com/frostgrove/vv/cmd/vv generate module -dir ./src/mod/workspace
```

The whole package tree under `-dir` is read. A **contribution** is a top-level
function whose first result is a *named* type — `*ContractRepo`,
`health.Contribution`, `translation.Store`. A function returning `string`,
`[]Language`, `any` or only an `error` is a helper, not something a container
builds, and is never offered. In subpackages it must be exported; in the
module's own package an unexported one is reachable and is offered too.

The **kind** is read off that result type:

| The constructor returns | The kind | Carried by |
|---|---|---|
| `health.Contribution` | `check` | every replica |
| `runtime.Runner` | `worker` | a profile with the `worker` role |
| `app.Seeder` | `seeder` | a profile with the `seeder` role |
| `appfiber.Route` | `route` | a profile with the `api` role |
| anything else named | `provide` | every replica |

Each becomes a row in `module.manifest.yml`:

```json
{
  "format": 1,
  "generated_by": "vv generate module",
  "package": "workspace",
  "module": "workspace",
  "order": 0,
  "contributions": [
    {
      "symbol": "contract.NewContractRepo",
      "kind": "provide",
      "source": "inferred-from-signature",
      "signature_fingerprint": "4c81aa…",
      "excluded": false,
      "confirmed": false
    }
  ]
}
```

Three answers are available to you there:

- **`confirmed: true`** — yes, this belongs in the module as that kind.
- **a different `kind`** — the inference was wrong. The row's `source` becomes
  `declared-in-manifest` and the generator stops re-deriving it.
- **`excluded: true`** — this is not a contribution. An excluded row is never
  waited for and never generated, and the exclusion survives a signature change.

Until every included row is confirmed, `vv_module_gen.go` is a file that does
not compile:

```go
var VVModule vvModule = "confirm every contribution in module.manifest.yml"
```

Confirmed, it is the definition:

```go
var VVModule = vvmodule.MustDefine(vvmodule.Spec{
    Name:  "workspace",
    Order: 0,
    Provide: []any{contract.NewContractRepo, …},
    Workers: []any{pipeline.NewDebtSweeper},
    Checks:  []any{converterCheck},
})
```

which the composition root hands to `appfx.Option(workspace.VVModule, profile)`
— see [app](app.md) and [[FL-030]].

On a module that already exists, the first pass is mostly an exclusion pass: a
pure function returning a named domain type looks exactly like a constructor
from the outside, and the generator would rather ask about one than stay silent
about a constructor nobody wired. It asks once per symbol.

- **A constructor nobody placed stops the build.** That is what this is for: a
  new exported constructor appears as an unconfirmed row rather than as a
  feature that silently is not wired.
- **A confirmation does not outlive the signature it was given for.**
  `signature_fingerprint` covers the symbol, the inferred kind and the whole
  signature. Change what a constructor takes or returns and that one row is
  asked again — and only that one.
- **The marker types are strings.** If your health contribution is your own
  type, pass `-check-type your/pkg.Contribution`; pass `-` to infer that kind
  from nothing. The generator imports none of them.

### Flags

| Flag | Default | Does |
|---|---|---|
| `-dir` | `.` | the module directory; its whole package tree is read |
| `-out` | `vv_module_gen.go` | the generated Go file name |
| `-manifest` | `module.manifest.yml` | the manifest file name |
| `-name` | the directory name | the module's name |
| `-order` | `0` | the order the module takes in a catalog |
| `-import` | from the nearest `go.mod` | import path of `-dir`, used to name its subpackages |
| `-module` | `github.com/frostgrove/vv/app/module` | import path of the package that owns `Definition` |
| `-check-type` | `github.com/frostgrove/vv/health.Contribution` | result type that makes a constructor a check |
| `-route-type` | `github.com/frostgrove/vv/app/http/appfiber.Route` | result type that makes a constructor a route |
| `-worker-type` | `github.com/frostgrove/vv/runtime.Runner` | result type that makes a constructor a worker |
| `-seeder-type` | `github.com/frostgrove/vv/app.Seeder` | result type that makes a constructor a seeder |
| `-recursive` | off | treat every package directly under `-dir` as its own module |
| `-check` | off | render and compare, writing nothing; both artefacts are checked |

`-recursive` is one level deep, not a walk: a module is a package *tree*, so
recursing would make every subpackage a module of its own. `vv generate module
-dir ./src/mod -recursive -check` is the CI line for a whole `src/mod`. The walk
collects rather than stops — every contribution waiting for a person and, under
`-check`, every stale module, each prefixed by its directory — and a
confirmation outranks drift.

## Generated repository

Alongside the DTO and metamodel, the default output provides a driver-neutral
repository blueprint, a short alias, and a factory returning a pointer:

```go
type ArticleRepo = crud.Repo[Article, int64, ArticleUpdate]

var ArticleRepository = sqlrepo.Define[Article, int64, ArticleUpdate]("")

func NewArticleRepository(src crud.Source) *ArticleRepo {
    return ArticleRepository.Bind(src)
}
```

Give the factory any `crud.Source`: `crudsql.Postgres(sqlDB)` for
`database/sql` (including GORM's shared pool), `crudpgx.Open(pool)` for native
pgx, or a test source. In Fx, depend on `*ArticleRepo`, not on the full
`crud.Repo[Article, int64, ArticleUpdate]` spelling. Use `-no-repo` when a
package needs only DTOs and metamodels.

## Adopting an ORM's generated model

With `-types` the named structs are taken as models **even without `db` tags**,
which is what makes ent's generated entities work as-is. Write the result into
your own package rather than into ent's, where the names would collide:

```bash
go run github.com/frostgrove/vv/cmd/vv \
    -dir ./ent -types User,Article -skip CreatedAt \
    -import myapp/ent -into ./internal/store
```

See [usage-guides/ent.md](../../usage-guides/ent.md) and
[usage-guides/gorm.md](../../usage-guides/gorm.md) for the whole recipe.

## Keeping it honest

`_examples/example/blog` is the worked example: `model.go` is what you write,
`vv_gen.go` is what comes out — with `-adapter`, so both halves are visible — and
a test regenerates and diffs so the two cannot drift.

```bash
make generate   # regenerate every DTO and metamodel in the tree
```

## See also

- [specs](specs.md) — what the metamodel plugs into
- [port](port.md) — `PathMap`, `Mapper` and `MustCoverUpdate`
- [wire](wire.md) — `PatchMapper`, `Presenter` and the three coverage assertions
- [[UC-014]] keep generated artefacts in sync · [[FL-010]] model to DTO and metamodel · [[FL-029]] model to public wire body · [[FL-031]] guard to declared operation · [[FL-032]] package tree to confirmed module
- [[D-018]] DTOs and metamodels are generated · [[D-050]] the generated adapter is total · [[D-105]] the persistence patch is not the public body · [[D-109]] a route is inferred from its guard · [[D-110]] a module is inferred from what its packages construct
