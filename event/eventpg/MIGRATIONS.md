# PostgreSQL event-store migrations

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
Every statement of schema version 1 is transactional DDL — there is no
`CREATE INDEX CONCURRENTLY` and no `ALTER TYPE` — so **run the whole list inside
one transaction**. A refusal then leaves the database exactly as it found it,
which is what makes a half-deployed schema impossible rather than unlikely.

Eleven statements, in order:

| # | What it is for |
|---|---|
| 1 | `CREATE SCHEMA IF NOT EXISTS` — the schema name is quoted everywhere it appears, so a name that is also a reserved word deploys |
| 2 | `schema_meta` — one row, the version, the log identity and the fingerprint of the schema this build describes |
| 3 | `streams` — one row per stream, holding the version admission is decided against |
| 4 | `events` — the history. `position` is `GENERATED ALWAYS AS IDENTITY (INCREMENT 1 CACHE 1 NO CYCLE)`, which is the first premise of the read watermark; `UNIQUE (family, key, version)` is what holds even if the admission predicate were wrong |
| 5 | `events_are_append_only()` — the function the two refusing triggers run |
| 6 | `events_assign_writer_xid()` — `PERFORM pg_current_xact_id()`, which is what gives a writer a transaction id *before* it draws a position |
| 7–9 | the three triggers, each `DROP TRIGGER IF EXISTS` then `CREATE TRIGGER` inside one `DO` block, so a re-run is a no-op |
| 10 | the `schema_meta` row, `ON CONFLICT (singleton) DO NOTHING`. **The log is minted in the database by `gen_random_uuid()` and never reissued**, so a re-run does not orphan every persisted cursor |
| 11 | a `DO` block that reads `schema_meta` back and raises SQLSTATE `EVPG1` unless it holds version 1 at exactly this build's fingerprint |

**Statement eleven asserts and never assigns, and that is deliberate.** A
statement list may record a description only of a schema it can produce. Version
1 creates objects that are absent and replaces two functions and three triggers;
it alters no table. Against a schema another expectation built — a different
`MaxPayload`, a different `MaxKey` — it changes nothing, so stamping this build's
fingerprint over it would record a fact that is false and defeat verification,
`Check` and the incident comparison `Schema.Fingerprint` exists for. A schema
version N whose list carries the `ALTER`s that turn N-1 into N *does* produce the
new description; its last statement is an `UPDATE` guarded on `version = N-1`,
followed by this same assertion.

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

Nothing about the deployed schema, at version 1. Two builds of the same version
must agree byte for byte on the rendering the fingerprint digests: the tables,
the columns and their order, the constraints, the identity parameters, the three
triggers **and the two function bodies**. A `CREATE OR REPLACE FUNCTION` that
changed what the append-only trigger enforces used to leave the fingerprint where
it was; the bodies are model data now, so it does not.

What a deployment may change freely: `Spec.MaxBatch`, `Spec.StreamPage` and
`Spec.MaxRead`, which touch no row, and the schema name, which is a different
backing and therefore a different log.
