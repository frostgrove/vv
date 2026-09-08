# D-127 — An event schema is migrated on the same deployment-profile choice as the jobs schema

**Status:** accepted
**Invariant:** No zero value and no absence of configuration ever migrates a
PostgreSQL event schema. `eventpg.UnsetSchemaManagement` resolves to
`VerifySchema`; the only thing that migrates is `eventpg.ManageSchema`, and
`Store.Migrate` refuses under `VerifySchema` with `ErrSpec`. A verified start-up
that meets a schema which is not the one the build expects refuses with
`ErrSchemaMismatch` and says which of the three levels refused.

## The decision

[[D-101]] settled this question for `jobs/jobspg` and is written about `jobspg`
by name. A second subsystem now answers the same question, and there are only two
honest ways to do that: repeat the reasoning in a second place where the two
copies can drift, or state once that it is **one** rule with two implementations.
This is the second.

The rule is [[D-101]]'s, unchanged: migrating is a property of the deployment
profile and never of a zero value, because the failure it prevents is a process
that creates and migrates a schema in every environment merely because nobody
said anything. What phase 2 adds is that the rule binds a subsystem whose data is
**history**. A jobs schema migrated by accident costs a surprising table. An
event schema migrated by accident under a running deployment mints a **new log
id**, and every cursor every consumer has persisted becomes foreign — the
consumers stop, correctly, and nothing they hold names anything any more.

Three differences from `jobspg`, each one a consequence of that and each stated
so the two are not "made consistent" later by somebody who reads only one:

**There is no `Open(ctx, db, …)`.** `jobspg.Open` exists for a
zero-configuration development path and asks for `ManageSchema` by name. `eventpg`
exports no such constructor at all: `New` plus `Prepare` is the only spelling, so
nobody gets a migrating store by reaching for the short one.

**`Migrate` refuses under `VerifySchema`.** "Nothing migrates unless a deployment
asked for it by name" is a property of the **store**, not only of `Prepare`. A
`Migrate` that ran whatever the store was built with would make the profile a
suggestion for anyone who called the exported method directly.

**Verification is three levels and the third one is the catalog.** Version, then
fingerprint, then `pg_catalog` itself — relations, columns, constraints, the
identity sequence's parameters, the exact trigger set **and the source of the
functions those triggers run**. A schema that verifies at two levels and enforces
something else at the third is a history whose append-only guarantee is a
sentence. `Check` then re-reads version, fingerprint **and the log**, because a
schema dropped and migrated again under a running store fingerprints identically
and is the one drift the first two levels cannot see.

**Why no `eventpgfx`.** [[D-101]]'s profile machinery lives in `jobspgfx` because
`jobs` has an fx surface. Phase 2 writes no container binding, so there is no
`Environment` here to read: the deployment says `ManageSchema` or it says
nothing. When an `eventpgfx` is written it derives the choice exactly as
`jobspgfx.Application` does, from the same `DeploymentProfile`, and refuses
`ManageSchema` in production unless the same acknowledged override is set. That
is a promise about a package that does not exist yet, and it is recorded here so
that package is not designed from scratch.

## What it forbids

- Do not make any zero value migrate. `UnsetSchemaManagement` resolves to
  `VerifySchema` and to nothing else.
- Do not add an `Open` that asks for `ManageSchema`, however convenient a
  one-line development path would be.
- Do not let `Migrate` run under `VerifySchema`.
- Do not skip level 3 on a start-up path because it is expensive. It runs at
  `Prepare` and never on a probe path, which is what makes it affordable.
- Do not add an `UPDATE` of `schema_meta` to schema version 1's statement list. A
  statement list may record a description only of a schema it can produce, and
  version 1 alters no table — see `event/eventpg/MIGRATIONS.md`.
- Do not reissue the log. It is minted in the database by statement ten under
  `ON CONFLICT DO NOTHING`, and a re-run that reissued it would orphan every
  persisted cursor.

## Where it lives

- `event/eventpg/config.go` — `SchemaManagement`, its zero value and its `String`.
- `event/eventpg/migration.go` — `Store.Migrate`, the refusal under
  `VerifySchema`, and the advisory lock held on one pinned connection.
- `event/eventpg/verify.go` — `Store.Prepare`, `Store.Verify`, `Store.Check`.
- `event/eventpg/catalog.go` — level 3.
- `event/eventpg/schema.go` — `MigrationStatements`, the operator-managed path
  [[D-101]] makes first class.
- `event/eventpg/MIGRATIONS.md` — the operator's half, and the remedy for a row
  the read refuses.

## Proven by

- `TestTheZeroSchemaManagementVerifiesAndMigratesNothing` and
  `TestPrepareAgainstAMissingSchemaRefusesBeforeAnyAppend` — saying nothing
  verifies, and a verify-only start-up leaves no schema behind.
- `TestVerificationRefusesEveryMutationAndPassesTheIntactSchema` — thirty-two
  mutations refused at `Prepare`, each naming the object, with two controls that
  must pass: the intact schema and an exempt extra index.
- `TestMigrationStatementsBuildTheSchemaTheFingerprintDescribes` and
  `TestRunningTheMigrationTwiceReissuesNoLog` — the list builds what the
  fingerprint describes, and a re-run is a no-op that leaves the log byte for
  byte where it was.
- `TestAMigrationOverASchemaItDidNotBuildRefusesRatherThanRestampingIt` — the
  `EVPG1` raise, with the same build re-running its own list as the control.
- `TestTwoConcurrentPreparesTakeOneLockOnOneBackend` and
  `TestASecondMigrationBlocksAgainstAHeldLock` — one granted advisory row on one
  backend pid, and a run in which somebody really waited.
- `TestCheckIsAReadinessAnswerAndNamesNoImportance` — including the case where a
  schema was dropped and migrated again under a running store, which levels 1 and
  2 cannot see.

## See also

[[D-021]] [[D-091]] [[D-096]] [[D-101]] [[D-121]] [[D-126]] [[FL-037]] [[UC-032]]
