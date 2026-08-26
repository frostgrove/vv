# UC-009 — Survive concurrent writers

**Actor:** the application author, on behalf of two clients editing the same row
**Covered by:** [[FL-002]] [[FL-003]] [[FL-009]] [[FL-011]]

## Scenario
Two people open the same record and both save. A partial update reads the row,
works out what changed, and writes — and the gap between the read and the write
is where the second save silently erases the first. The author wants that to stop
being possible without hand-writing a compare-and-swap into every endpoint, and
wants the loser to be told something it can act on: retry, rather than a 500 or a
success that quietly threw work away.

## What must hold

1. Declaring an integer column as the version column is the whole opt-in. No
   change to any call site.
2. With a version column, an update pins its write to the version it read and
   advances the counter in the same statement. Both halves are observable: the
   stored counter is one higher after a successful update, and a write built
   from an older read matches no row.
3. The writer that loses gets `crud.ErrStaleVersion`, and that error satisfies
   `errors.Is(err, crud.ErrConflict)`, so a transport answers 409 without knowing
   what a version is.
4. The winner's row is intact. After a refused update, the stored row carries the
   *other* writer's value, and the counter has advanced exactly once.
5. Reading again and reapplying succeeds. The retry is the caller's documented
   way through, and it works because the version now held is the current one.
6. A row that is genuinely gone is `crud.ErrNotFound`, not `ErrStaleVersion`. The
   two demand different answers from the caller — give up versus retry — so they
   are never merged.
7. A filtered update advances the counter on every row it writes. A concurrent
   single-row update built from a read taken before it is refused. A lock only
   one write path respects protects nothing, and this is the guarantee that says
   so.
8. An update with nothing to write leaves the counter alone. Sending a field that
   already holds its value does not burn a version and does not invalidate
   anybody else's copy.
9. The version column belongs to the repository, not the caller. An update DTO
   that names it is refused when the repository is declared. A client cannot set,
   read past, or reset the counter through the update path.
10. `Save` never winds the counter back. An upsert built from a stale copy of the
    model writes the row but leaves the counter where the database had it, and
    the caller's model is refreshed to the counter that is actually stored.
11. Without a version column, an update performed inside a transaction locks the
    row it read for the duration, so the read-modify-write is serialised by the
    database.
12. Rows-affected is never used to decide "no such row" on a write path. The
    engines disagree about whether a no-change update counts as a matched row, so
    the outcome of an update is decided by re-reading or by `RETURNING`, and is
    the same on every dialect.

## Out of scope

- **Pessimistic locking outside a transaction.** Locking a row you are not going
  to hold is meaningless, so the load is a plain read there. The version column
  is the answer for that shape, not a lock.
- **`Save` under contention.** An upsert is a whole-row overwrite with no `WHERE`
  clause to check anything in — MySQL's conflict clause has nowhere to put a
  condition — so `Save` is last-writer-wins by construction. Guarantee 10 only
  promises it cannot *undo* the protection for the next writer.
- **Timestamp versions.** Versions are integers. A timestamp version needs one
  clock, and two application servers do not share one.
- **Row locks on SQLite.** SQLite locks the database, not the row, so `FOR
  UPDATE` renders as nothing there and the serialisation has to come from the
  transaction.
- **Retry.** The library refuses the lost write and names it; retrying is the
  caller's loop.
- **Cross-row invariants.** A version column protects one row. Two rows that must
  change together need a transaction (UC-005).

## Covered by
| Flow | What it contributes |
|---|---|
| [[FL-002]] | the load-then-write gap, the version predicate in the `WHERE`, and how a matched-nothing update is told apart from a missing row |
| [[FL-003]] | why an upsert cannot carry the check, and what it does with the counter instead |
| [[FL-009]] | the in-transaction load taking a row lock |
| [[FL-011]] | the stale write becoming a 409 |

## Status
**covered.** The lost-update race is executed, not reasoned: a hook fires the
competing write into the exact gap between the read and the write, on every
engine target. The refusal, the intact winner, the successful retry, the filtered
update advancing the counter, and the stale `Save` failing to reset it each have
their own test. The declaration-time refusal of a DTO naming the version column
and the "nothing to do leaves the counter alone" case are unit-tested.

One thing the guarantee list deliberately does not claim, and which is the honest
limit of this use case: two concurrent `Save` calls are still last-writer-wins.
