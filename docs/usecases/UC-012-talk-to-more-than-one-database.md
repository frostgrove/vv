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

1. A repository can be told which database a transaction belongs to, and only
   repositories bound to that database join it. A repository bound elsewhere
   carries on using its own connection, inside the same block.
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
5. A scoped binding does not hide an unscoped one underneath it. With a scoped
   binding for one database and a general one below, the first database's
   repository uses the scoped executor and everything else still uses the
   general one.
6. A datasource that cannot say which database it is is never matched by a scoped
   binding. It keeps the unscoped behaviour rather than being matched by
   accident.
7. When such an unidentifiable source opens a transaction, that transaction binds
   unscoped and captures every repository the context reaches. The choice is
   deliberate: a write landing *outside* the transaction is no better than one
   landing in the wrong database, and the source cannot tell them apart.
8. A datasource whose identity cannot be compared at all does not panic.
9. The unscoped form captures every repository the context reaches, including one
   on another database, and the write succeeds. This is stated as a guarantee
   because it is the behaviour, not because it is desirable: it is what makes the
   seam work with any framework, and it is the reason guarantee 1 exists.
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
- **Detecting the mistake in guarantee 9.** There is nothing to check the foreign
  executor against — the transaction an ORM hands over has no relationship to
  the source a repository holds — so the library cannot warn. The scoped form is
  the only protection, and it is opt-in.

## Covered by
| Flow | What it contributes |
|---|---|
| [[FL-009]] | how a binding is keyed to a database, what matches it, and how an opened transaction scopes itself |
| [[FL-016]] | how one database's schema is read once and kept per handle, and what happens when it cannot be |

## Status
**covered, with the sharp edge pinned and two blind spots.**

All ten guarantees have tests, and four of them run against two real PostgreSQL
databases with the assertions read through raw handles so they never go through
the seam under test. Guarantee 9 has a test of its own whose purpose is to keep
the behaviour a decision rather than a surprise.

Guarantee 10 arrived with the schema catalog and is the first thing outside the
transaction seam to key on this identity. Its refusals are tested, and its
two-handles-one-server half runs live against two connections that resolve the
same bare table name to different tables — which is the case a key made from a
connection string would have merged without a word. One half of it is owed to a
later phase and named there: nothing yet *declares* a feature that needs a
schema, so "refuses to start" has no declaration site to be tested at.

Blind spots the owner should weigh:

1. **Only one adapter is proven end to end for the transaction half.** The
   multi-database integration tests are `database/sql` on PostgreSQL. The pgx
   adapter implements the same identification and is unit-covered, but scoped
   bindings with pgx, and any combination across two different engines, are
   untested. Guarantee 10 is the exception: the schema half runs live through
   both adapters and on all four engines.
2. **Two ways to write a scoped binding do nothing, silently.** Naming a
   *transaction* rather than the database keys the binding on the transaction
   handle, which no repository bound to the pool matches — the binding is
   ignored and the write goes to the pool, outside the transaction, reporting
   success. Naming nothing at all degrades to the unscoped form. Neither is
   tested and neither is documented, and both look exactly like the correct call
   at the call site.
