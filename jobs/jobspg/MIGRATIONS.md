# PostgreSQL jobs migrations

`Driver.Prepare` is the recommended migration path. It serializes replicas, builds the retention index concurrently, validates its exact definition, and keeps an already prepared v4 startup read-only against delivery tables. The v3-to-v4 step changes only catalog metadata and does not request a write lock on the delivery table.

`MigrationStatements` is the operator-managed path. Execute its statements in order, one statement per autocommit operation. Do not wrap the returned list in a transaction: it contains `CREATE INDEX CONCURRENTLY`. The schema version is advanced only after exact index validation and provenance stamping.

For a rolling upgrade from a v2 jobs binary, first deploy v4 with `jobspgfx.HousekeepingSettings{Disabled: true}`, drain every v2 replica, and then enable housekeeping. A running v2 process predates terminal tombstones and cannot safely share their lifecycle. Fresh v4 installations and fleets already fully on v4 use the default enabled housekeeping.

Each definition persists an application-owned codec high-water mark and its supported revisions. A newer deployment must keep every persisted revision readable. Older replicas may continue during a rolling deployment, but they never lower the high-water mark and their claim targets cannot select newer payloads. Queue policy changes are independent of the payload schema and do not require a codec version bump.

Codec identity, codec mode, partition mode, and payload identity are stable wire contracts. Changing one under the same definition name is rejected. Use a new stable definition name for an incompatible contract.

When upgrading an existing v3 catalog row, the first v4 process backfills compatibility metadata only when its definition fingerprint matches the last v3 binding. This fail-closed bootstrap prevents an unprovable wire-contract replacement. Once backfilled, policy-only rolling changes and monotonic codec upgrades use the compatibility metadata instead of the aggregate fingerprint.

Definition removal is not inferred from a smaller local catalog because an older replica must remain valid while a newer replica adds definitions. Keep retired definitions declared until their queued and retained records are drained and an explicit retirement mechanism is available. The `fingerprint` columns preserve bootstrap and high-water snapshots; they are not a live checksum of every policy used by every rolling replica.
