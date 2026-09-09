# PostgreSQL event-store migrations

A `Store` and a `Checkpoints` over one schema are two resources at one schema
version: a deployment migrates once, and each verifies at its own `Prepare`.
Both may be given `ManageSchema` — the advisory lock is per schema, so two of
them in one process serialise on it and the second finds the list idempotent and
the assertion satisfied.

`Store.Prepare` migrates only under `SchemaManagement: eventpg.ManageSchema`: it
serialises replicas on a session advisory lock held on one pinned connection,
runs the statement list inside one transaction, and then verifies. Saying nothing
is `UnsetSchemaManagement`, which resolves to `VerifySchema`: start-up then
performs fail-closed verification only and creates nothing. `Store.Migrate`
refuses under `VerifySchema` with `ErrSpec`, so "nothing migrates unless a
deployment asked for it by name" is a property of the store rather than of one
method. The rule is `[[D-101]]` and `[[D-127]]`.

There is no `Open(ctx, db, …)`. An event store's schema is history, so nobody
gets a migrating store by reaching for the short constructor: `New` plus
`Prepare` is the only spelling.

## The operator-managed path

`MigrationStatements(schema)` is the list a deployment's own migration step runs.
Every statement of schema version 2 is transactional DDL — there is no
`CREATE INDEX CONCURRENTLY` and no `ALTER TYPE` — so **run the whole list inside
one transaction**. A refusal then leaves the database exactly as it found it,
which is what makes a half-deployed schema impossible rather than unlikely.

**One list does both jobs.** It builds version 2 on an empty database and
transforms a deployed version 1 into it, because version 2 adds a table and
alters none: the `CREATE … IF NOT EXISTS` statements leave the three existing
tables untouched and the fourth appears.

Thirteen statements, in order:

| # | What it is for |
|---|---|
| 1 | `CREATE SCHEMA IF NOT EXISTS` — the schema name is quoted everywhere it appears, so a name that is also a reserved word deploys |
| 2 | `schema_meta` — one row, the version, the log identity and the fingerprint of the schema this build describes |
| 3 | `streams` — one row per stream, holding the version admission is decided against |
| 4 | `events` — the history. `position` is `GENERATED ALWAYS AS IDENTITY (INCREMENT 1 CACHE 1 NO CYCLE)`, which is the first premise of the read watermark; `UNIQUE (family, key, version)` is what holds even if the admission predicate were wrong |
| 5 | `checkpoints` — added at schema version 2. One row per projection: the store-minted `cursor` as `bytea`, the `advance` the fence is evaluated against, the three progress counters and the instant the consumer recorded. It carries **no trigger**: a checkpoint is not history |
| 6 | `events_are_append_only()` — the function the two refusing triggers run |
| 7 | `events_assign_writer_xid()` — `PERFORM pg_current_xact_id()`, which is what gives a writer a transaction id *before* it draws a position |
| 8–10 | the three triggers, each `DROP TRIGGER IF EXISTS` then `CREATE TRIGGER` inside one `DO` block, so a re-run is a no-op |
| 11 | the `schema_meta` row, `ON CONFLICT (singleton) DO NOTHING`. **The log is minted in the database by `gen_random_uuid()` and never reissued**, so a re-run does not orphan every persisted cursor |
| 12 | an `UPDATE` that stamps version 2 over a deployed **version 1 at exactly this build's bounds**, guarded on `version = 1 AND fingerprint = <the version-1 fingerprint at these bounds>` |
| 13 | a `DO` block that reads `schema_meta` back and raises SQLSTATE `EVPG1` unless it holds version 2 at exactly this build's fingerprint |

**The stamping `UPDATE` is guarded on the deployed fingerprint and not on the
version alone, and that is the whole of what makes one list safe for both jobs.**
A statement list may record a description only of a schema it can produce.
Guarded on `version = 1` only, this build's default-bounds list meeting a
version-1 schema deployed at `MaxPayload 1024` would create the fourth table,
stamp its own version and fingerprint over a schema it did not build, and then
**pass its own assertion** — recording a fact that is false and defeating
verification, `Check` and the incident comparison `Schema.Fingerprint` exists for.
Guarded on the fingerprint the `UPDATE` matches nothing, statement 13 raises, and
the whole transaction rolls back. The same rule holds for a version N: its last
two statements are an `UPDATE` guarded on the version **and the fingerprint** it
migrates from, then this assertion.

**Migrating from version 1.** Run the same list. A version-1 schema at this
build's bounds takes the fourth table and the stamp; one at other bounds is
refused with `EVPG1` and left exactly as it was, checkpoints table included. A
schema already at version 2 takes nothing: the `INSERT` conflicts, the `UPDATE`
matches no row, and the assertion is satisfied.

`EVPG1` rather than plpgsql's default `P0001`, so a caller can tell this refusal
from any other `RAISE` without reading message text.

## The bounds are part of the schema

`Schema.MaxPayload` and `Schema.MaxKey` are `CHECK` constraint operands and
fingerprint inputs. A build that changes either describes a different schema and
will refuse to start against the deployed one, naming both fingerprints. That is
why they live on `Schema` and not beside `MaxBatch`, `StreamPage` and `MaxRead`,
which are Go-side ceilings no row depends on.

`Schema.Fingerprint()` needs no store, no pool and no connection: during an
incident it is a two-line program compared against `schema_meta.fingerprint`.

## Removing a row the read refuses

The read doors check every scanned row against the deployed schema's own promises
before an envelope is built, and a row outside them refuses the **whole page**.
A row like that can only arrive through a dropped constraint, a restored dump or
a foreign writer — and because history is append-only in the database, nobody can
delete it through the documented surface. The remedy is deliberate and is an
operator's, under a role that owns the table:

```sql
BEGIN;
ALTER TABLE <schema>.events DISABLE TRIGGER events_append_only_row;
DELETE FROM <schema>.events WHERE position = <the position>;
ALTER TABLE <schema>.events ENABLE TRIGGER events_append_only_row;
COMMIT;
```

Re-enable inside the same transaction. A running store will refuse to start
against a schema whose trigger is disabled — level 3 compares `tgenabled` — so
leaving it off is loud rather than silent, and deleting an event that a consumer
has already read is a decision about history, not a repair.

## What a rolling deployment may change

Nothing about the deployed schema. Two builds of the same version
must agree byte for byte on the rendering the fingerprint digests: the tables,
the columns and their order, the constraints, the identity parameters, the three
triggers **and the two function bodies**. A `CREATE OR REPLACE FUNCTION` that
changed what the append-only trigger enforces used to leave the fingerprint where
it was; the bodies are model data now, so it does not.

What a deployment may change freely: `Spec.MaxBatch`, `Spec.StreamPage` and
`Spec.MaxRead`, which touch no row, and the schema name, which is a different
backing and therefore a different log.
