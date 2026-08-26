# UC-016 — Hide rows permanently at the repository level

**Actor:** the application author
**Covered by:** [[FL-004]] [[FL-007]] [[FL-005]] [[FL-006]]

## Scenario
Some rows are not deleted, they are tombstoned — an ORM's soft delete, an
archived flag, a draft revision superseded by a newer one. That is a property of
the *table*, not of the request: it is true for every caller, every endpoint and
every principal, so it belongs on the declaration and not in a per-request rule.
The author needs one line that makes those rows stop existing as far as this
repository is concerned, and needs it to be genuinely permanent — no query
option, no client filter, no clever specification puts them back.

## What must hold

1. The rule is one setting on the repository's declaration, written as an
   ordinary predicate over the model.
2. It is ANDed in ahead of anything a caller passes, so no option composes its way
   out. A client filter that explicitly asks for the hidden rows returns an empty
   result, not the rows.
3. It applies to every read: fetching one row, listing, listing unpaged, counting
   and existence-checking. A hidden row reads as not found.
4. It applies to a delete by id and to a filtered delete, so a hidden row is not
   removable by guessing its key.
5. It applies to a filtered update.
6. It applies to *both* halves of a partial update — the load and the update's own
   `WHERE` — and to the re-read afterwards.
7. It follows a relation back into the same model, at any depth, without being
   declared again. A self-referencing edge — a parent, a manager, a tree — carries
   the rule through a preload, through a filter that hops it, and through a sort
   that walks it, because there is only one possible answer to what those rows
   are.
8. Reaching a *different* model does not inherit it. That is a separate,
   explicit declaration, because another model's rows are another repository's
   business.
9. That separate declaration is written against the target model, may name a
   nested path, and is validated when the repository is declared — so a typo
   fails at start-up rather than narrowing nothing.
10. Once declared, the relation rule reaches every way that relation can be
    reached: the second statement a preload issues, the correlated subquery a
    nested filter opens, and the subquery a nested sort opens.
11. A relation nobody declared a rule for is read whole. This is the contract, not
    an omission.
12. A table-level rule and a per-request rule both apply where both are declared,
    and they compose by AND.
13. A caller cannot widen the relation rule either, from a filter or from a
    narrowed preload, and the argument order shows which was applied first.

## Out of scope

- **Soft delete as a feature.** There is no tombstone column, no automatic
  timestamping, no restore, no "include deleted" switch. There is a predicate on
  the declaration, and soft delete is one thing to write in it.
- **Making a delete soft.** A delete issues a real `DELETE`. Turning it into an
  update is a service layer's job (UC-013).
- **Insert and upsert.** An upsert has no `WHERE` clause for a predicate to live
  in, so the rule cannot apply to it. Guard a create in a service method or an
  access-control policy.
- **Rules that depend on the caller.** A repository declaration is built at
  start-up and a principal arrives per request. That is UC-004.
- **Enforcement outside this repository.** Another repository over the same table,
  or a hand-written statement, sees the rows.

## Covered by
| Flow | What it contributes |
|---|---|
| [[FL-004]] | where the rule is declared, and the validation of a relation path at declaration time |
| [[FL-007]] | the rule entering a read ahead of the caller's own filter |
| [[FL-005]] | the rule travelling into a relation filter's subquery and a nested sort's |
| [[FL-006]] | the rule travelling into a preload's second statement |

## Status
**covered, with one consequence worth reading as a warning.**

Proven: the rule on every read, on both delete shapes, on the filtered update, on
both halves of the partial update; the self-relation following it through a
preload, through a filter hop and through a nested sort, each with the rendered
statement asserted; the explicit relation declaration reaching a preload and a
filter hop, with a negative control proving an undeclared relation really is read
whole; the declaration-time refusal of a path the model does not have; and the
inability of a caller filter or a narrowed preload to widen either rule, with
argument order asserted. The whole thing is also executed end to end against a
live database in its most common form — an ORM's soft-delete tombstones staying
invisible while the ORM itself can still see them, and a client filter for the
tombstones returning nothing.

**The warning is the create path.** Because the rule cannot reach an upsert, a
save carrying the key of a hidden row overwrites it — and under an
access-control gate, worse: the gate's check for "does this row already exist"
goes *through* the rule, so a tombstone reads as absent and the gate treats the
write as a fresh create. A resurrection, silently, through the ordinary create
endpoint. This is not covered by a test, and it is the gap the owner should close
first if soft delete is a real use of this library.
