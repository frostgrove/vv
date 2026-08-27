# UC-004 — Isolate tenants so a caller cannot see or touch another's rows

**Actor:** the application author, on behalf of every tenant of the service
**Covered by:** [[FL-007]] [[FL-008]] [[FL-005]] [[FL-006]] [[FL-011]] [[FL-020]]

## Scenario
Every table has a tenant column and every request belongs to one tenant. The
author wants that to be a property of the repository rather than a line at the
top of every handler, because the line at the top of every handler is the one
that gets forgotten on the twelfth endpoint. It has to hold for reads, for
writes, for deletes, for counts, and — the part hand-rolled versions always miss
— for rows reached *through a relation*, where the parent's `WHERE` clause does
not reach. And a caller must not be able to learn that a row exists by being told
it is forbidden.

## What must hold

1. The rule is declared once, next to the repository, and derives its value from
   the request. Nothing at the call site changes. Where that value comes from is
   the application's: a context key it set itself, or the authenticated
   principal ([[UC-019]]).
2. Every read is narrowed in SQL, not in Go: fetching one row, listing, listing
   unpaged, counting and existence-checking all carry the narrowing in the
   statement.
3. Naming another tenant's row by id answers `crud.ErrNotFound`, never
   `crud.ErrForbidden`. A 403 would confirm the row exists.
4. A partial update of another tenant's row answers `crud.ErrNotFound` and issues
   no `UPDATE`.
5. The narrowing is in the update's *own* `WHERE`, not only in the load. A row
   that leaves the tenant between the load and the write is not written, and the
   caller is not handed a row it was never allowed to see.
6. A delete by id of another tenant's row removes nothing and reports zero. The
   narrowing is in the `DELETE`, so a row hidden from a read is not deletable by
   guessing its id.
7. A filtered delete and a filtered update both carry the narrowing in the
   statement.
8. A filtered delete or filtered update with no narrowing from either the policy
   or the caller is refused outright, unless the policy explicitly opts in. The
   two opt-ins are separate permissions: allowing an unscoped delete does not
   allow an unscoped update.
9. A caller cannot widen the narrowing with any option the public surface offers:
   a filter, a narrowed preload, a specification, or a second narrowing all
   compose by AND. The narrowing is prepended, so it survives whatever follows.
10. Rows reached through a relation are narrowed too, where the author says they
    should be. A preload's second statement and a filter's correlated subquery
    both carry the narrowing.
11. A relation nobody narrowed is read whole. This is the contract, not an
    oversight: the far side is another repository's business and the library will
    not guess.
12. A rule that depends on the principal and a rule that is a property of the
    table both apply where both are declared, and they compose by AND.
13. A relation path in a policy declaration is resolved when the policy is
    declared, so a typo fails at start-up rather than narrowing nothing in
    production.
14. Columns can be frozen. A partial update or a filtered update that *names* a
    frozen field is refused before any SQL runs, whether or not the value would
    have changed, and the refusal names the field.
15. An `INSERT`-or-overwrite into another tenant is refused, and so is
    overwriting a row belonging to one.
16. Anything the policy cannot answer fails closed. A rule that errors — no
    principal in the context, an unreachable directory — makes every entry point
    return that error with zero statements executed.
17. A refusal is `crud.ErrForbidden` and an invisible row is `crud.ErrNotFound`,
    so the transport answers 403 and 404 respectively without knowing what a
    tenant is.
18. Row-level checks, when enabled, veto the whole call rather than trimming the
    result. A page containing one row the caller may not see fails; it does not
    silently return the rest.
19. A projection cannot be used to dodge a row-level check. When a row will be
    inspected, the read returns whole rows regardless of what was selected.

## Out of scope

- **Enforcement below the application.** This is a predicate the library ANDs in,
  not database row-level security. A process that opens its own connection and
  writes SQL is not covered.
- **A relation the author did not declare a rule for.** Stated as guarantee 11
  and repeated here because it is the most common way to leak: exposing
  `?preload=comments` on a tenanted parent without saying what happens to
  comments reads that table raw.
- **Narrowing a create.** An upsert has no `WHERE` clause for a predicate to live
  in. What protects a create is a row-level check, and it has to be declared —
  see the Status section.
- **Multi-tenancy across databases.** One tenant per row here. Tenant-per-database
  is UC-012.
- **Field-level read masking.** Hiding a column from the wire is a presenter
  (UC-013), not this.
- **A developer writing a raw SQL fragment.** The guarantees hold against every
  predicate the closed AST and the wire can build. A raw fragment is trusted
  developer input.

## Covered by
| Flow | What it contributes |
|---|---|
| [[FL-007]] | the narrowing entering a read, and what a caller sees for a row that is there but invisible |
| [[FL-008]] | the narrowing entering an update and a delete, the frozen-field check, and what a create can and cannot be held to |
| [[FL-005]] | the narrowing travelling into a relation filter's subquery |
| [[FL-006]] | the narrowing travelling into a preload's second statement |
| [[FL-011]] | the 404-not-403 distinction reaching the client |

## Status
**covered for declarative policies.** Reads, page totals, relations, writes and
bulk writes all receive the same AND-composed narrowing. A hidden assigned key
answers `ErrNotFound` without reaching a write, while a scope-only policy refuses
body writes before it can make an unchecked create or update. `ScopeField`,
`ScopeAttr` and `ScopeSubject` include the row inspection and frozen-column
checks needed for an insert; a hand-written scope without `Inspect` is therefore
fail-closed rather than a weaker version of the helper.

The controls cover the update's own `WHERE`, the filtered-write snapshot,
relation narrowing in filters, sorts, preloads and page totals, frozen names at
declaration time, and a caller limit/cursor/projection/preload being unable to
make `Inspect` approve only part of a bulk statement.

The unique-constraint result remains an inherent oracle of any public create
endpoint, but no driver or constraint detail reaches the transport (UC-015); an
application requiring non-enumerability must avoid exposing that unique-create
question.

`ScopeRelationField` and the principal helpers resolve their path while the
policy is declared. The escape hatch `RelationScopes func(context.Context)` is
intentionally dynamic, so its paths cannot be proved at startup; they are
validated and fail closed before SQL instead. Treating an arbitrary callback as
a startup-declarative declaration would be a wrong contract — use the helper
when startup validation is required.
