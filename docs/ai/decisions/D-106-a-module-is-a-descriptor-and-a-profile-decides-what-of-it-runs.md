# D-106 — A module is a descriptor, and a deployment profile decides what of it runs

**Status:** accepted
**Invariant:** A bounded context contributes one `module.Definition`: a name, an
order and constructors filed under the role each belongs to — routes to `api`,
runners to `worker`, seeders to `seeder`, and providers and health checks to
every deployment. The definition is a value that can be described without being
built: nothing in it is called, imported or connected to answer what a profile
would activate. A deployment profile names roles, and a role the profile does
not name is not wired — not started and then skipped, not wired. The catalog is
the one list of modules a program has, and two modules may not share a name.

## The decision

The composition root of a service that runs three ways — an API, a worker fleet
and a seed command — is normally three hand-written lists of container options,
kept in step by whoever remembers. The lists share most of their entries, differ
in the entries that matter, and nothing in the program can say what the
difference is. A worker that was in two lists and is now in one is a code review
away from being noticed, and a year away from being noticed in production.

What the lists encode is a role. Naming the role at the contribution — this
constructor builds routes, that one builds a runner — makes the three lists one
list read three ways, and makes the difference between them a value rather than
a convention. `module.Serving` and `module.Seeding` are the same catalog; the
profile is what removes the routes from the seed command and the seeders from
the API replica.

**The definition never learns what a container is.** It holds constructors as
opaque values, in the order they were declared, and hands them to whoever asked.
`appfx.Options` is the whole binding to fx: one `fx.Module` per definition, its
active constructors provided. A second container is a second satellite over the
same definition, and neither is in the root module ([[D-033]], [[D-074]]).

**The definition never learns what a transport is either.** A route contribution
is a constructor the composition root already annotated with
`appfiber.AsRoute` — the base package owns the seam, the application owns the
choice ([[D-096]]). That is why `module` imports neither `appfiber` nor
`runtimefx` nor `healthfx` while filing what they produce, and why adding a
second HTTP binding adds no package here.

## Describing is not building

A descriptor is the only thing available before the graph exists, which is
exactly when someone needs to ask what this process is about to be. So
`Describe` and `Doctor` call nothing: no constructor runs, no pool opens, no
migration is checked. A doctor that builds the graph to describe it is a doctor
nobody can run against production, and it is the same doctor that cannot report
on a graph too broken to build.

What the doctor refuses is a wiring mistake — an unnamed profile, a role that is
not one of `api`, `worker` or `seeder`, a profile that activates nothing in the
catalog it was given. What it only remarks on is a shape that is merely worth
reading: a monolith running every role over a catalog with no worker in it is
ordinary, and refusing it would teach everyone to skip the doctor.

## The three layers

`module.Auto(name, constructors…)` is the short form for a module that only
provides. `module.New(name)` is the builder, and it is thin on purpose: every
method appends to a `module.Spec` and `Build` calls `module.Define`, which is
the explicit form and the only place a refusal is decided. There is no behaviour
in the builder that the spec cannot express, and no mistake the short form can
make that the long form accepts ([[D-021]]).

## What this rules out

- Three hand-maintained lists of container options whose difference is a comment.
- A module that registers itself by being imported, or a catalog assembled by
  scanning. The catalog is a call site somebody wrote ([[D-037]]).
- Describing a deployment by building it.
- `module` importing a container, a router or an fx satellite in order to file
  what one produces.
- A role invented at a call site: a profile naming anything but `api`, `worker`
  or `seeder` is refused rather than quietly activating nothing.
- Two modules sharing a name, which makes one descriptor row and two graphs.

## Where it lives

[[FL-030]].

## Proven by

`app/module/module_test.go` — *a builder is the spec written as a call*, *Auto
is the builder with nothing but providers*, *a module that contributes nothing is
refused*, *every problem in one definition is named at once*, *a seed profile
offers the graph without the routes and the workers*, *a serving profile starts
no worker and seeds nothing*.
`app/module/catalog_test.go` — *the catalog is ordered by order and then by
name*, *two modules with one name are refused before anything is built*, *a
definition that was never built is refused by the catalog*, *a profile naming a
role that does not exist is refused*.
`app/module/doctor_test.go` — *a deployment is described without building it*, *a
descriptor says which contributions the profile leaves out*, *a profile that
activates nothing is refused rather than starting an empty process*, *a role no
module contributes to is a notice rather than a refusal*.
`app/appfx/module_test.go` — *the profile is what decides which half of a module
is wired*, *a seeder contributed by a module reaches the seed runner*, *an API
replica runs no seeder*, *a profile nothing answers to fails the graph rather
than providing nothing*.
