# D-138 — A declared index is created by a version step, and the declaration cannot change without one

**Status:** accepted
**Invariant:** Every index this package declares — `operationalIndexes` and
`retentionIndexes` both — is created by a `SchemaVersion` step and by nothing
else. Growing or changing either declaration without raising `SchemaVersion`
fails `TestDeclaredIndexesCarryTheSchemaVersionThatCreatesThem` at build time.
A start-up creates only what its version step is entitled to create: an index
that is missing is created by the upgrade, an index that exists with the wrong
definition is refused, and a `VerifySchema` start-up creates nothing at all.

## The decision

`migrateLocked` used to create operational indexes only from inside
`migrationUpgradeStatements()`, gated on `!currentVersion && needsRetentionMigration`
— the branch a database walks exactly once, the first time it is migrated past
version 2 — and to build retention indexes under the same gate. It then
*validated* both unconditionally. Creation was gated; refusal was not.

`deliveries_recent_idx` was appended to `operationalIndexes` with no
`SchemaVersion` bump, and that asymmetry became a production failure: every
database already on `SchemaVersion` took the validate-only branch, found an
index its code declared and its schema did not have, and refused to start —
`jobspg: schema mismatch: operational index "deliveries_recent_idx" is not
ready` — on every subsequent start, with no migration able to fix it short of
dropping the schema. A database parked on version 3 or 4 was worse: too current
for the branch that created operational indexes, not current enough for the
branch that only validated, so neither class was ever created there.

The fix is the ordinary one rather than a clever one. **A change to a declared
index set is a schema change, so it takes a schema version.** `SchemaVersion`
is 6; the upgrade branch issues `operationalIndexStatements()` — the same
idempotent `CREATE INDEX IF NOT EXISTS` the operator path emits — and
`buildRetentionIndexes` runs on any upgrade rather than only the 2→5 one, so a
retention index added later travels the same road. A database that is already
current does nothing: no DDL, no locks, no rebuilt relations.

The alternative considered and rejected was reconciling the declared sets on
every managed start-up, converging the database to the declaration whatever its
version. It heals more, and it costs the property that makes a migration
reviewable: DDL would run outside any version step, at a moment nobody
scheduled, on a table an operator did not expect to be touched. A schema change
a person can see coming is worth more than one that repairs itself quietly.

**What the version step will not do is repair.** `CREATE INDEX IF NOT EXISTS`
matches by name, so an index that exists with the wrong definition is left
exactly as it is and `validateOperationalIndexes` refuses the start-up. That is
deliberate: a same-name index with different columns may be a decision somebody
made, and silently replacing it destroys that decision without a word. The
retention class does drop and rebuild `CONCURRENTLY` on drift — it carries a
provenance comment saying this framework owns it, and it is the only thing
standing between a retention sweep and a full scan. Both halves are pinned by
tests; neither is an oversight.

One more thing this decision exists to prevent, because it is what actually
happened first. Faced with a start-up that refuses over a missing index, the
obvious move — the one reached for before the cause was even understood — is
`DROP SCHEMA frostgrove_jobs CASCADE` and let the process rebuild it. It works,
it takes a second, and it throws away every queued invocation and every retained
record in the database. A refusal is loud and keeps the data; the convenient
answer is quiet and does not. The remedy for a schema this package refuses is a
migration or a person, never a drop.

The guard matters as much as the fix. `declaredIndexDigest` sits next to
`SchemaVersion`, and a unit test holds it against both declarations, so the
omission that caused this — append to a slice, ship it, discover it weeks later
on somebody's start-up — is now a failing build with a message naming the four
things to change. `[[D-021]]`: the magic fails at build time, never at request
time.

The cost is the one every schema version carries. Once one replica has stamped
version 6, a replica still running the version 5 build refuses to start, because
`migrateLocked` rejects a stored version above its own `SchemaVersion`. That is
fail-closed and it is visible in the deployment, unlike the failure this
replaces.

## What it forbids

- Do not add or change an entry in `operationalIndexes` or `retentionIndexes`
  without raising `SchemaVersion`, extending the version list in
  `finalizeMigration` and `MigrationStatements`, and updating
  `declaredIndexDigest`. The unit test refuses the build until all of it is done.
- Do not weaken that test into a warning, and do not compute the digest from
  anything but the declarations themselves.
- Do not move index creation back behind `needsRetentionMigration`, and do not
  restore a branch where a version is current enough to skip creation but not
  current enough to be validated as complete.
- Do not give the operational class a drop and rebuild, and do not reduce the
  retention class to a `CREATE INDEX IF NOT EXISTS`. Each repairs exactly what
  it is allowed to repair.
- Do not create anything from `repository.check`, `Driver.Check` or any path a
  `VerifySchema` deployment reaches. `[[D-101]]` is untouched: silence verifies
  and mutates nothing.
- Do not make declared indexes reconcile on every start-up in order to avoid a
  version bump. That trade was considered and refused above.
- **Do not answer a schema mismatch by destroying schema state.** Not
  `DROP SCHEMA ... CASCADE`, not dropping `deliveries` or `intents`, not
  truncating them, and not "recreate it from scratch, it is only a queue".
  Those tables hold queued invocations and retained records that no rerun
  reconstructs: dropping them silently cancels work an application already
  committed to, and the outbox rule `[[D-118]]` means some of it was committed
  in the caller's own transaction. A refusal is a safe state that keeps every
  row; the repair is a migration, or a person deciding with the data in front
  of them. Recreating a development database nobody minds losing is a personal
  convenience, never a remedy this package, this document or an agent reading
  it may recommend.

## Where it lives

- `jobs/jobspg/config.go` — `SchemaVersion` and `declaredIndexDigest`.
- `jobs/jobspg/repo.go` — `migrateLocked`'s upgrade branch, which issues the
  index statements, and `MigrationStatements`, the operator's list.
- `jobs/jobspg/operational_migration.go` — `operationalIndexStatements` creates,
  `validateOperationalIndexes` refuses drift.
- `jobs/jobspg/retention_migration.go` — `buildRetentionIndexes`, which repairs
  drift, and `finalizeMigration`, which stamps the version last.
- `jobs/jobspg/MIGRATIONS.md` — the operator's half.

## Proven by

- `jobs/jobspg/schema_migration_test.go` —
  `TestDeclaredIndexesCarryTheSchemaVersionThatCreatesThem`: the digest guard,
  and the proof that the operator list creates every declared index before it
  stamps the version.
- `jobs/jobspg/declared_index_migration_integration_test.go` —
  `TestPostgresVersionFiveStartUpCreatesAnIndexDeclaredAfterIt` (the production
  failure, reproduced and fixed),
  `TestPostgresIntermediateVersionStartUpCreatesBothIndexClasses` (versions 3
  and 4, which created neither class),
  `TestPostgresCurrentSchemaRunsNoDDLOnRestart` (a current schema is not written
  to, with a control proving a real rebuild does change the relation), and
  `TestPostgresVerifySchemaStartUpRefusesAMissingIndexAndCreatesNothing`
  (confinement to `ManageSchema`, with a managed control).
- `jobs/jobspg/schema_hardening_integration_test.go` —
  `TestPostgresSchemaHardeningUpgradesV4AndRejectsDrift` replaces every
  operational index with a same-name wrong-column one and requires the start-up
  to refuse. It is the control that creation did not become repair.
- `jobs/jobspg/retention_integration_test.go` —
  `TestPostgresManualMigrationRejectsWrongRetentionIndexBeforeVersionStamp` is
  the other half: retention drift is repaired, and an exact index is not rebuilt.

## See also

[[D-101]] — the deployment-profile choice this start-up path runs under, and the
reason a `VerifySchema` process creates nothing.
[[D-021]] — why the omission had to become a build failure rather than a
start-up failure.
