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
with a column called `Path` shadows the method — the generated file says so in
that group's doc comment, and `RelPath()` is the spelling nothing shadows.

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
- [[UC-014]] keep generated artefacts in sync · [[FL-010]] model to DTO and metamodel
- [[D-018]] DTOs and metamodels are generated · [[D-050]] the generated adapter is total
