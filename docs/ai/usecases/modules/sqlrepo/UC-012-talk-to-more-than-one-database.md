# UC-012 — Talk to more than one database in one process

**Actor:** the application author
**Covered by:** [[FL-009]] [[FL-016]]

## Scenario
The service writes its own tables to the primary database and events to an
analytics one, or it shards. Both repositories are used in the same request, and
one of them runs inside a transaction. The author needs the transaction to reach
exactly the repositories that live on that database and no others — because the
failure mode is not an error, it is a write that lands on the wrong server and
reports success.

## What must hold

1. A source and foreign executor form one checked session. Only repositories
   bound to that database join it; a repository bound elsewhere carries on using
   its own connection in the same context.
2. Naming the raw database handle and naming any datasource built over it are the
   same statement. The author does not have to keep a reference to the exact
   wrapper the repository was bound with. This reaches through the author's own
   wrappers too — instrumenting statements is the ordinary reason to write one —
   on one condition, which is that the wrapper says what it wraps. A wrapper that
   does not is opaque, and guarantee 6 is what it gets.
3. Two independently constructed datasources over one handle resolve to the same
   database. Two repositories built separately still share one transaction.
4. A transaction the repository opens itself is scoped the same way, without the
   author writing anything: a sibling repository on the same database joins it, a
   repository on another database does not, and a write to the other database
   survives a rollback of the first.
5. A safe session does not hide an explicitly unsafe binding underneath it. The
   matching database uses the session; everything else reaches the unsafe
   fallback exactly because the caller named that opt-out.
6. A datasource that cannot expose a canonical identity is refused by `Session`
   and before an owned `InTx` calls `Begin`. It is never guessed from wrapper
   identity and never silently made unscoped.
7. A transaction handle used as the canonical source is refused. It identifies
   the transaction, not the pool repositories were built from.
8. A datasource whose identity cannot be compared does not panic and is refused
   with an inspectable scope error.
9. `WithUnsafeExecutor` captures every repository the context reaches, including
   one on another database, and the write succeeds. This legacy behaviour remains
   only behind a name that states the waiver.
10. A feature that needs to know a database's schema reads each database's own
    schema and never merges two, on the same identity as everything above: two
    datasources over one handle share one reading, two handles do not — even two
    handles to the same server, because they can resolve the same bare table name
    to different tables. It is read once, when the feature is declared, and never
    again during a request. A handle whose identity cannot be compared is refused
    at that declaration rather than taking the process down later, and a schema
    that cannot be read at all is a refusal too rather than a feature that
    silently reports nothing for the rest of the process's life.

## Out of scope

- **Cross-database transactions.** Two databases, two transactions. Nothing here
  makes them atomic.
- **Routing by tenant, read replicas, failover.** A repository is bound to one
  datasource; choosing which is the application's.
- **Joins across databases.** A relation crosses a table, not a server.
- **Making `WithUnsafeExecutor` safe.** It is the explicit waiver. Code that
  cannot state a source accepts cross-database adoption by choosing it.

## Covered by
| Flow | What it contributes |
|---|---|
| [[FL-009]] | how a binding is keyed to a database, what matches it, and how an opened transaction scopes itself |
| [[FL-016]] | how one database's schema is read once and kept per handle, and what happens when it cannot be |

## Status
**covered, with the unsafe escape hatch pinned.**

All ten guarantees have tests. The transaction half runs through database/sql
against two real PostgreSQL databases and through pgx, including rollback,
wrong-database refusal, transaction-as-source refusal and the explicit unsafe
control. Assertions read through raw handles where routing itself is under test.

Guarantee 10 arrived with the schema catalog and is the first thing outside the
transaction seam to key on this identity. Its refusals are tested, and its
two-handles-one-server half runs live against two connections that resolve the
same bare table name to different tables — which is the case a key made from a
connection string would have merged without a word. One half of it is owed to a
later phase and named there: nothing yet *declares* a feature that needs a
schema, so "refuses to start" has no declaration site to be tested at.
