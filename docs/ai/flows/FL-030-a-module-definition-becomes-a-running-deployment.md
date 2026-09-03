# FL-030 — A module definition becomes a running deployment

**Entry points:** `module.New` / `module.Auto` / `module.Define`,
`module.NewCatalog`, `module.Doctor`, `appfx.Options` / `appfx.Option` /
`appfx.Auto`
**Governed by:** [[D-037]] [[D-074]] [[D-096]] [[D-106]]

What happens between a bounded context declaring what it contributes and a
process running some of it — and what a command can print about that before any
of it is built.

## The declaration

`app/module/module.go`:

1. **`Kind`** — the five buckets a contribution falls into: `ProvideKind`,
   `RouteKind`, `WorkerKind`, `SeederKind`, `CheckKind`. **`Kind.Role`** maps
   the three that are deployment-specific onto `API`, `Worker` and `Seeder`, and
   answers empty for providers and health checks, which every replica carries.
2. **`Spec`** — `Name`, `Order` and the five slices, the explicit form.
   **`Define`** collects every problem at once (no name, a nil constructor, a
   module that offers nothing) and refuses through `refuse(ErrDefinition, …)`;
   **`MustDefine`** panics with the same refusal.
3. **`Definition`** — the value that results: unexported fields, so what reaches
   a catalog was built by `Define` or is the zero value the catalog rejects.
   **`Roles`** answers the roles it contributes to, sorted.
4. **`Active(profile)`** is what a container is handed: the constructors the
   profile carries, in declared order. It calls none of them.

`app/module/builder.go` — **`Builder`**: `Order`, `Provide`, `Routes`,
`Workers`, `Seeders`, `Checks` each append to the spec, and `Build` /
`MustBuild` delegate to `Define` / `MustDefine`. There is nothing else in it;
that is the point ([[D-106]]).

`app/module/errors.go` — **`Refusal`** carries every problem at once and answers
`errors.Is` against the sentinel it was raised with: `ErrDefinition`,
`ErrProfile` or `ErrCatalog`.

## The profile

`app/module/profile.go`:

1. **`Profile`** — a name and a set of roles. `Base`, `Serving`, `Working`,
   `Seeding` and `Complete` are the five a deployment usually is; **`With`**
   widens one and **`Named`** renames it, so a deployment that is an API and a
   worker is `module.Serving.With(module.Worker).Named("monolith")`.
2. **`carries(kind)`** is the whole activation rule: a kind with no role is
   always carried, and one with a role is carried when the profile names it.
3. **`Check`** refuses an unnamed profile, an unknown role and a role named
   twice, through `ErrProfile`.

## The catalog

`app/module/catalog.go` — **`NewCatalog`** sorts by `Order` then name (the same
rule as `app.Sorted`), and refuses an empty catalog, a zero `Definition` and two
modules sharing a name. **`MustCatalog`** panics with that refusal.
**`Check(profile)`** runs the profile's own check first, so a bad profile is
`ErrProfile` and not `ErrCatalog`, and then the doctor.

## Describing without building

`app/module/descriptor.go` — **`Definition.Describe(profile)`** counts the
contributions of each kind, marks each kind active or not under the profile, and
sums what the profile activates. **`Catalog.Describe(profile)`** is the same for
every module in catalog order, and **`CatalogDescriptor.String`** renders it as
the block a command prints.

`app/module/doctor.go` — **`Doctor`** returns a **`Diagnosis`**: the descriptor,
the problems and the notices.

- Problems are refusals: the profile's own, an empty catalog, and a profile that
  activates nothing at all in the catalog it was handed.
- Notices are shapes worth reading: a module that contributes nothing under this
  profile, and a role the profile names that no module answers to.

Nothing here calls a constructor, which is what makes it safe to run from a
command that must not open a connection ([[D-106]]).

## The binding

`app/appfx/module.go`:

1. **`Option(definition, profile)`** — checks the profile, and turns the active
   constructors into one `fx.Module` named after the definition. A definition
   the profile activates nothing of contributes no options rather than an empty
   module.
2. **`Options(catalog, profile)`** — `catalog.Check(profile)` first, so a
   refusal is an `fx.Error` the graph reports rather than a container that was
   handed nothing and starts perfectly. Then one `Option` per definition, in
   catalog order.
3. **`Auto(catalog)`** — `Options` under `module.Complete`.

The constructors themselves were annotated by whoever wrote the definition:
`appfiber.AsRoute`, `runtimefx.AsRunner`, `appfx.AsSeeder`, `healthfx.AsCheck`.
`module` files them; it does not know what they are, and imports none of those
packages ([[D-096]]).

## Where the decisions bite

- Describing a deployment by building it. [[D-106]].
- A profile that activates nothing, accepted: the process starts, reports
  healthy and does nothing. [[D-106]].
- Two modules with one name: one descriptor row, two graphs. [[D-106]].
- `module` importing a router or a container to file what it produces — the
  first step towards a package per intersection. [[D-096]], [[D-074]].

## Files

| File | What it holds |
|---|---|
| `app/module/module.go` | `Role`, `Kind`, `Kind.Role`, `Contribution`, `Spec`, `Definition`, `Define`, `MustDefine`, `Auto`, `Roles`, `Active` |
| `app/module/builder.go` | `Builder`, `New`, `Order`, `Provide`, `Routes`, `Workers`, `Seeders`, `Checks`, `Build`, `MustBuild` |
| `app/module/profile.go` | `Profile`, `Base`, `Serving`, `Working`, `Seeding`, `Complete`, `With`, `Named`, `Carries`, `Check` |
| `app/module/catalog.go` | `Catalog`, `NewCatalog`, `MustCatalog`, `Definitions`, `Names`, `Check` |
| `app/module/descriptor.go` | `KindDescriptor`, `Descriptor`, `CatalogDescriptor`, `Describe`, `String` |
| `app/module/doctor.go` | `Diagnosis`, `Doctor`, `OK`, `String` |
| `app/module/errors.go` | `ErrDefinition`, `ErrProfile`, `ErrCatalog`, `Refusal`, `refuse` |
| `app/appfx/module.go` | `Option`, `Options`, `Auto` |

## Tests that walk this flow

`app/module/module_test.go`, `app/module/catalog_test.go`,
`app/module/doctor_test.go`, `app/appfx/module_test.go`.

## See also

[[FL-032]] — where a `Spec` comes from: `vv generate module` reads a bounded
context's package tree and writes the `Definition` this flow starts with.

