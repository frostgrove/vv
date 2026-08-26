# UC-003 — Apply a partial update that tells absent from null

**Actor:** a client sending a partial update — a `PATCH` body or a gRPC patch document — and the application author who
holds the same DTO in Go
**Covered by:** [[FL-002]] [[FL-004]] [[FL-010]] [[FL-011]]

## Scenario
A client edits one field of a resource and sends only that field. It has to be
able to say three different things about a nullable column: leave it alone,
clear it, set it. A hand-written handler usually collapses the first two, so a
form that omits a field wipes it. The client also expects that sending a field
whose value is already stored costs nothing — no write, no bumped timestamp, no
audit row. The application author wants the same three states available in Go,
without a `map[string]any` anywhere and without writing the diffing by hand.

## What must hold

1. A field the client did not send is not written. The column is absent from the
   `SET` list, and its stored value is unchanged after the call.
2. A field sent as `null` writes SQL `NULL`. It is distinguishable from (1) by
   the resulting row, not only by the statement.
3. A field sent with a value writes that value.
4. A field sent with the value the row already holds produces no `SET` entry for
   that column.
5. A body that defines nothing — `{}` — issues no `UPDATE` statement at all, and
   the call succeeds, returning the row as it stands.
6. When every field the body defines already holds the sent value, the outcome is
   the same as (5): no statement, the current row returned.
7. The three states survive JSON decoding. An absent key decodes to "undefined",
   an explicit `null` decodes to "null", and the two are never conflated by the
   decoder, by re-marshalling, or by a second decode over a value that was
   already set.
8. The value returned to the caller describes the row as it now is in the
   database, including any column the database itself changed (a trigger, a
   default, the version counter) — not the pre-update copy with the changes
   patched in.
9. An update naming a row that does not exist is `crud.ErrNotFound`, and a row
   deleted between the load and the write is also `crud.ErrNotFound` — never a
   success carrying a fabricated model.
10. A DTO field that names no model field, names the primary key, names an
    `immutable`, `generated` or `version` column, or carries a type the model
    field cannot hold, is refused when the repository is declared — before the
    process serves a request — not on the first `PATCH` that touches it.
11. A DTO field declared as a plain `T` rather than `*T` or a three-state
    optional is *always* applied, including its zero value. This is a property of
    the declaration, not of the request, and the caller can observe it: an empty
    body still writes that column.
12. The same rules apply whether the DTO arrives from an HTTP body, from a gRPC
    request document or is constructed in Go. There is no separate wire path
    with different semantics.

## Out of scope

- **Which fields exist on the DTO.** That is a declaration question — see
  [[FL-010]] and UC-014. This use case only promises what happens to the fields
  that are there.
- **Concurrency.** Between the load and the write another writer can land. What
  happens then is UC-009, not this.
- **Replacing a whole row.** `PUT` binds a whole model, not a DTO, and does not
  diff. Three-state semantics are a property of the DTO path only.
- **Validating the value.** The library checks that the value fits the column's
  Go type. Whether "" is a legal name is the application's business — a hook or a
  service layer (UC-013).
- **Bulk updates.** A filtered update has no single row to diff against, so
  guarantee (4) does not hold there. That is UC-008.

## Covered by
| Flow | What it contributes |
|---|---|
| [[FL-002]] | the load, the diff, the statement that comes out, and the re-read |
| [[FL-004]] | the DTO checked against the model when the repository is declared |
| [[FL-010]] | where the DTO's field types come from, and why a nullable column becomes a three-state field |
| [[FL-011]] | the missing row becoming a 404 |

## Status
**covered.** Every numbered guarantee has a test. The three-state decode and the
undefined/null/set distinction are unit-tested exhaustively; the diffing, the
no-op update and the vanished-row race are exercised against PostgreSQL, MySQL
and SQLite by the conformance suite, so the "no statement" and "returns the
stored row" halves hold on dialects with and without `RETURNING`.

Guarantee 12 is the weakest link and is worth restating rather than trusting: the
handler binds the body onto the same DTO type the repository was declared with,
so there is one path by construction, but nothing asserts the two cannot diverge.

There are now four bindings, and they do not decode bodies the same way — one
dispatches on Content-Type, two take JSON only, and the fourth takes a
`google.protobuf.Struct` where an absent key and an explicit null are a missing
map entry and a `NullValue` entry. So the guarantee is worth more than it was
and is worth watching harder. All four run an absent-versus-null test, including
the explicit null, so the three states survive every decoder today. The gRPC
one carries its own control — an absent key and a null must produce two
different states — because a document format that folded them would otherwise
be indistinguishable from one that kept them. A fifth binding would have to
bring those tests with it.
