# D-101 — Migrating the jobs schema is a deployment-profile choice, never a default

**Status:** accepted
**Invariant:** No zero value, and no absence of configuration, ever migrates a
PostgreSQL jobs schema. `jobspg` with nothing said verifies and refuses; the
only thing that migrates is `jobspg.ManageSchema`, and `jobspgfx.Application`
puts it there only for a development or test deployment profile. In a
production profile it is a refusal until `AllowManagedSchemaInProduction` says
otherwise, and that override is recorded in the graph.

## The decision

`SchemaManagement` used to be `ManageSchema = iota`. Every consumer that said
nothing — and the one real consumer said nothing — got a process that created
and migrated `frostgrove_jobs` on start-up, in every environment, because that
is what a zero value meant. Nothing asked which deployment this was. The safe
mode existed and was reachable, but reaching it required knowing it was there.

Three things changed and they are one decision.

**The zero value is not a mode.** `UnsetSchemaManagement` is the zero value and
means nobody chose. `New` resolves it to `VerifySchema` — fail-closed
verification that creates, migrates and binds nothing. A driver therefore
always answers a real choice from `SchemaManagement()`, and a driver that does
not exist answers `UnsetSchemaManagement` rather than claiming it would migrate.
The safe direction is the direction silence points in.

**The profile is derived where the environment is already known.**
`jobspgfx.ApplicationSettings` has carried an `Environment` string all along —
it is half of the jobs namespace. `ProfileOf` reads it: `dev`/`local` is
`DevelopmentProfile`, `test`/`ci` is `TestProfile`, and **everything else,
including an empty or misspelled value, is `ProductionProfile`**. An
environment nobody recognises is the one most likely to be the real one, so it
gets the strict answer. That keeps the zero-configuration development path
exactly as it was and removes it from production without the application
changing a line.

**An override is explicit and leaves a record.** `ManageSchema` under a
production profile is not silently downgraded and not silently honoured: it is
a refusal naming `jobspg.MigrationStatements` — the operator-managed path that
already exists — unless `AllowManagedSchemaInProduction` is set. When it is,
the resulting `SchemaManagementDecision` carries `Overridden: true` and is
supplied into the container, so what the process decided is readable by
anything assembling a descriptor, a health answer or a start-up log, rather
than being inferred from a field nobody printed.

The magic stays magic: an application still writes an application name and an
environment and gets a working development backend ([[D-021]]). What it can no
longer do is get a migrating one by accident. The explicit constructor
underneath — `jobspgfx.Module(Settings{...})` and `jobspg.New(Spec{...})` — has
no environment to read and therefore refuses to guess; it verifies until told
otherwise.

## What it forbids

- Do not give `SchemaManagement` a zero value that migrates, and do not restore
  a nil-driver answer of `ManageSchema`.
- Do not infer a deployment profile from anything but an explicit environment,
  and do not default an unrecognised environment to anything but production.
- Do not migrate from a production process without the named override; the
  deployment step runs `MigrationStatements`.
- Do not silently downgrade a requested `ManageSchema` to `VerifySchema`. A
  request the profile refuses is an error, not a quiet substitution.
- Do not spread the profile beyond this choice. It selects schema management;
  it is not a general feature-flag namespace and no other package reads it.

## Where it lives

- `jobs/jobspg/config.go` — `SchemaManagement`, the unset-to-verify resolution
  in `New`, `Open` asking for `ManageSchema` by name, and `Prepare`, which
  migrates and binds only under `ManageSchema`.
- `jobs/jobspg/jobspgfx/profile.go` — `DeploymentProfile`, `ProfileOf`,
  `SchemaManagementDecision` and the refusal.
- `jobs/jobspg/jobspgfx/application.go` — where the decision is taken and
  supplied into the graph.
- `jobs/jobspg/MIGRATIONS.md` — the operator's half.

## Proven by

- `jobs/jobspg/schema_management_test.go` — a driver built without a choice
  verifies rather than migrates, and a driver that does not exist claims no
  choice at all.
- `jobs/jobspg/jobspgfx/profile_test.go` — an unrecognised, empty or
  production-shaped environment resolves to the production profile; a
  production application's driver verifies; `dev` and `test` still manage;
  `ManageSchema` in production is refused with the message that names the
  operator path; and the acknowledged override both migrates and arrives in the
  container marked `Overridden`.
- `jobs/jobspg/schema_management_integration_test.go` — verify-only start-up
  never mutates a missing schema.

## See also

[[D-021]] [[D-096]]
