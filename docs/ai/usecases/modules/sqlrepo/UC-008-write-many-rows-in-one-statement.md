# UC-008 — Write many rows in one statement

**Actor:** the application author
**Covered by:** [[FL-002]] [[FL-008]]

## Scenario
"Deactivate every user in this tenant." "Delete every draft older than a year."
Done row by row that is one read and one write per row, and the filter usually
came from a client anyway, so it is already a compiled query. The author wants
the same partial-update DTO to address a *set* rather than one row, in one round
trip, and wants the access-control rules that apply to a single write to apply to
the set as well — including a refusal when the set turns out to be "everything".

## What must hold

1. A filtered update is one statement, whatever the size of the set.
2. It takes the same partial-update DTO as a single-row update, and two of the
   three rules carry over unchanged: a field the DTO does not define is never
   written, and a field defined as null writes SQL `NULL`.
3. The third rule cannot carry over and does not: there is no single row to diff
   against, so every field the DTO defines is written to every matching row,
   including a row that already holds that value.
4. A DTO that defines nothing writes nothing and issues no statement. It is a
   caller asking for nothing, not a caller asking to rewrite the table with its
   own zero values.
5. It reports how many rows the database says it touched. That number is the
   database's, and the engines count differently — one reports rows matched, the
   other rows actually changed — so a caller must not derive "the row was not
   there" from a zero.
6. A filtered delete is the same shape: one statement, a filter, a count.
7. Both carry the repository's permanent narrowing. A filtered write cannot reach
   the rows the repository exists to hide.
8. Both advance the optimistic-lock counter on every row they write, so a
   concurrent single-row update built from an earlier read is refused rather than
   silently undoing them (UC-009).
9. Under an access-control policy, a filtered write with no narrowing from either
   the policy or the caller is refused. The two opt-ins — one for updates, one for
   deletes — are separate, and neither is implied by the other or by a
   table-level rule.
10. Under a policy, a filtered update naming a frozen field is refused before any
    SQL runs, on the same rule as a single-row update.
11. Under a read-only policy, a filtered write is refused.
12. Under a policy with a row-level check, every row the write is about to touch
    is checked, and one refusal vetoes the whole call — nothing is written and
    the count is zero. A filtered write is never partially applied because some
    rows passed.
13. Through the typed specification API, a filtered delete or update built from
    an *empty* specification is refused outright and issues no statement. An
    empty specification means "everything", and that is the one thing this API
    will not do by accident.

## Out of scope

- **Bulk insert is a separate repository verb.** `Repo.InsertBatch` accepts
  typed models and is insert-only even when a row carries an assigned key. On a
  directly capable pgx source it selects `COPY` by default; otherwise it uses
  bind-budgeted, atomic `INSERT` chunks. `crud.PortableBatch()` selects that SQL
  path for one call and `sqlrepo.PortableBatch()` selects it for a blueprint
  (notably for PostgreSQL RLS/rewrite-rule tables). This use case remains about
  filtered `UpdateAll`/`DeleteAll`, not about importing models.
- **Returning the written rows.** A filtered write reports a count, not a set.
- **Per-row values.** One DTO, one set of values, every matching row. Writing
  different values to different rows is many statements.
- **Atomicity across two filtered writes.** One statement is atomic; two are a
  transaction (UC-005).
- **A meaningful count.** Guarantee 5 is deliberately weak. Portable code should
  treat the number as advisory.
- **Arbitrary SQL in `crud.Raw`.** This is an explicit trusted-developer escape
  hatch, not a declarative specification. A generic guard cannot prove whether
  arbitrary SQL is unrestricted; treating it as covered would be a wrong
  contract. Use the direct repository bulk verb when that power is intentional.

## Covered by
| Flow | What it contributes |
|---|---|
| [[FL-002]] | the DTO becoming a `SET` list without a diff, and the version counter riding along |
| [[FL-008]] | the narrowing in the statement, the unscoped-write guards, the frozen-field check, and the row-level veto |

## Status
**covered.** The runtime guarantees and refusal contract hold on their public
paths.

Proven: the one-statement shape, the write-everything-defined rule, the empty-DTO
no-op, the repository narrowing in the statement, the version counter advancing
(including the concurrent-update case that gives it a point), the two unscoped
guards and their separateness, the frozen-field refusal, the read-only refusal,
and the row-level veto. The conformance suite runs a filtered update against
every driver and engine target, so the divergent row count is a measured fact
rather than a warning.

**Closed gap — caller shaping cannot desynchronise the row-level check from the
statement.** The gate's internal victim read retains only filters and relation
scopes. It drops paging, cursors, projection, preloads, sort and `DISTINCT`, so
an `Inspect` hook sees every row the eventual bulk statement can touch. The
control test supplies a limit, cursor and preload, then verifies that a rejected
second row prevents every write.

**Closed gap — an unrestricted declarative specification is a client refusal.**
Delete and update are both tested against every public AST spelling that means
"every row", including a Criteria `Conjunction`, empty `NOT IN`, `PK IS NOT
NULL` and a primary key compared with itself. They do not mistake a nullable
column's `IS NOT NULL` / self-comparison for a tautology. The sentinels wrap
`crud.ErrBadRequest`, so transports return a 400 rather than a private 500.

The asymmetry worth stating: a delete by explicit ids is never subject to the
unscoped-write guard, because the ids are the narrowing. That is right, but it
means "every filtered write is subject to the unscoped guard" is not true as
stated. The adjacent insert path no longer creates a second exception:
`security.Gate.InsertBatch` authorises `Create` once and inspects every private
row copy before I/O, while `faults.Enrich.InsertBatch` preserves operation and
field attribution.

**Historical adjacent gap, closed before the first release (FW-CORE-003).** The
original sweep found a source-level `crud.BulkInserter`/`CopyFrom` assertion. It
required callers to hand-build table, column and value lists, bypassed every
repository decorator, ignored an executor carried by the context and returned
pgx COPY failures without the normal classifier. Those safe-looking names were
removed rather than deprecated pre-release. Driver-level escape hatches now use
only `UnsafeBulkInsert*` / `UnsafeCopyFrom*`; their names state that they bypass
repository policy and lifecycle. `UnsafeBulkInsertFor` still resolves the
source-bound ambient executor so an intentional low-level call cannot silently
leave a transaction.
