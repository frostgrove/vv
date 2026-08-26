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

- **Bulk insert.** Nothing in the library reaches for a driver's bulk-copy path;
  a save is one statement per row. The pgx adapter exposes a copy interface, but
  an application that wants it type-asserts its own datasource and calls it, and
  that call ignores any transaction in the context.
- **Returning the written rows.** A filtered write reports a count, not a set.
- **Per-row values.** One DTO, one set of values, every matching row. Writing
  different values to different rows is many statements.
- **Atomicity across two filtered writes.** One statement is atomic; two are a
  transaction (UC-005).
- **A meaningful count.** Guarantee 5 is deliberately weak. Portable code should
  treat the number as advisory.

## Covered by
| Flow | What it contributes |
|---|---|
| [[FL-002]] | the DTO becoming a `SET` list without a diff, and the version counter riding along |
| [[FL-008]] | the narrowing in the statement, the unscoped-write guards, the frozen-field check, and the row-level veto |

## Status
**partially covered.** Guarantee 12 does not hold in one reachable case, and
guarantee 13 is unevenly proven.

Proven: the one-statement shape, the write-everything-defined rule, the empty-DTO
no-op, the repository narrowing in the statement, the version counter advancing
(including the concurrent-update case that gives it a point), the two unscoped
guards and their separateness, the frozen-field refusal, the read-only refusal,
and the row-level veto. The conformance suite runs a filtered update against
every driver and engine target, so the divergent row count is a measured fact
rather than a warning.

**Gap 1 — a caller-supplied limit desynchronises the row-level check from the
statement, breaking guarantee 12.** The rows a policy inspects are fetched with
the caller's own options, and that fetch honours a limit; the `UPDATE` and
`DELETE` carry no limit at all. So a filtered write with a limit inspects one row
and writes every matching row. A single-row delete by id is not affected.

**Gap 2 — the empty-specification refusal is unevenly proven.** The delete side is
tested against thirteen distinct spellings of "empty", each asserting no
statement ran. The update side is tested against exactly one spelling, and only
in the database-backed suite. Both refusals are also plain sentinels wrapping
nothing, so an HTTP transport maps them to 500 rather than 400 unless the
application handles them.

The asymmetry worth stating: a delete by explicit ids is never subject to the
unscoped-write guard, because the ids are the narrowing. That is right, but it
means "every bulk write is guarded" is not true as stated.
