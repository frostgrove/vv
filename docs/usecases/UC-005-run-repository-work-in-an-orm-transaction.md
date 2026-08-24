# UC-005 — Run repository work inside a transaction the ORM owns

**Actor:** the application author
**Covered by:** [[FL-009]] [[FL-002]]

## Scenario
A request has to do two things that must happen together: something the ORM's
typed builder expresses better, and something the generated CRUD layer already
does. The author does not want two transactions, does not want to give up
either library, and does not want the CRUD layer to own the connection. Which
side opens the transaction should be a local decision — sometimes the ORM's
usecase already has one, sometimes the repository is the outermost thing in the
call — and either way a rollback has to take everything.

## What must hold

1. Handing a foreign transaction to the repository is one call that derives a new
   context. Every repository call made with that context runs on that
   transaction; no repository opens a connection of its own while it is in force.
2. The only thing asked of the foreign object is that it can execute a statement
   and return rows. Anything an ORM, a query builder or a driver exposes with
   that shape can be handed over, without the library knowing what it is.
3. Both libraries see each other's uncommitted work. A row the ORM wrote inside
   the transaction is readable through the repository, and a row the repository
   wrote is readable through the ORM's own builder.
4. A read on a context *without* the transaction does not see the uncommitted
   rows, so the isolation is real and not an artefact of both calls sharing a
   pool.
5. When the ORM rolls back, the repository's writes are gone too — observable
   from both sides after the block ends.
6. The repository can open the transaction itself, and it commits when the
   function returns nil and rolls back when it returns an error. A write made
   inside is visible to reads inside the same block.
7. Opening a transaction on a context that already carries one **joins** it: no
   second transaction is begun, and the inner block neither commits nor rolls
   back. The outer owner keeps control. An inner function that fails can only
   return the error to that owner.
8. Where genuinely independent nesting is wanted, a savepoint is available on
   sources that can offer one, and rolling one back leaves the enclosing
   transaction alive. Savepoints nest, and unwind one level at a time.
9. A source that cannot begin a transaction refuses with `crud.ErrNoTxSupport`,
   and the function is not run. The refusal is a sentinel, so it is testable
   without knowing which source it came from.
10. A transaction that has already been committed or rolled back is not silently
    downgraded to the pool. A call on a stale transaction context fails; it does
    not quietly succeed against the connection pool and land a write nobody meant
    to make.
11. Every statement the repository issues on that context goes to that executor —
    reads, counts, existence checks, inserts, updates, deletes, and the re-read a
    write does to refresh the model.
12. A partial update performed inside a transaction locks the row it loaded for
    the length of the transaction, so the read-modify-write is serialised by the
    database rather than by hope.
13. The query document is compiled and validated before any transaction exists,
    so a client's typo fails the request rather than opening and rolling back a
    transaction.

## Out of scope

- **Which database the transaction belongs to.** In a process with one database
  this never comes up; in a process with two it is the whole problem, and it is
  UC-012.
- **Handing an rx-crud-owned transaction back to the ORM.** The context flows
  inward. The adapters expose the underlying handle, but joining in that
  direction is the author's plumbing.
- **The ORM's callbacks.** A statement issued here does not travel through the
  ORM's builder, so its hooks and Go-side defaults do not fire even inside a
  shared transaction. See UC-010.
- **Distributed transactions.** One connection, one transaction.
- **Isolation levels and retry.** The level is whatever the transaction was
  opened with; a serialisation failure surfaces as the driver's error and the
  retry is the caller's.
- **Atomicity between a page and its total.** A paged list issues the rows and
  the `COUNT` as two statements. They are atomic only if the caller wrapped them
  in a transaction.

## Covered by
| Flow | What it contributes |
|---|---|
| [[FL-009]] | pushing a foreign executor into the context, opening one, joining one already there, and savepoints |
| [[FL-002]] | the row lock the load takes when a transaction is present |

## Status
**covered, with three named gaps.**

Sharing a transaction with a foreign owner is proven for `database/sql`, pgx,
sqlx, gorm, ent and sqlc (on both `database/sql` and pgx, and on MySQL) — each
with the cross-visibility assertion in both directions. Rollback taking both
halves is proven for gorm, ent and pgx, including a case where the ORM's write
and the repository's write are on different tables. Commit, rollback and the
join-rather-than-nest rule run against every driver target in the conformance
suite. Savepoints, the stale-transaction refusal and `ErrNoTxSupport` all have
tests.

The gaps, in the order they are likely to bite:

1. **The reverse rollback is not proven.** No test writes through an ORM *inside*
   a repository-owned transaction block and then shows the rollback taking the
   ORM's write. Guarantee 5 is proven only in the direction where the ORM owns
   the transaction.
2. **Guarantee 7's second half is not proven.** That a joined inner block cannot
   roll back independently follows from the code and is documented, but no test
   asserts an inner failure leaving the outer transaction to decide.
3. **Guarantees 11 and 12 are partly reasoned.** Preloads route through the
   context executor by construction, but every preload test runs on a plain
   context — a preload inside a transaction is untested. Likewise, that a partial
   update takes the row lock *by itself* when a transaction is present is
   untested; only an explicitly requested lock is.

Also worth knowing: a savepoint is not reachable under a gorm- or sqlx-owned
transaction, because what is handed over there is a bare executor with no way to
begin anything. That is a limitation of the seam, not a bug, but nothing
documents it.
