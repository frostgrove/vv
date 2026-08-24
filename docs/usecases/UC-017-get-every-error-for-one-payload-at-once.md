# UC-017 — Get every error for one payload in one response

**Actor:** a client rendering a form, and the application author who does not
want to write the same validation twice
**Covered by:** [[FL-011]]

## Scenario
A signup form is submitted. The email is taken, the organisation the caller
picked no longer exists, and the display name is longer than the column allows.
The database refuses the write and reports **one** of those, because that is
what a database does: the first constraint it reaches ends the statement.

The client shows the user one red field. The user fixes it and resubmits. The
database reports the next one. Three round trips to learn three things that were
all knowable at the first.

The author wants the whole set at once, keyed to the fields the client actually
sent, so the form can mark all three and the user fixes them together. They want
it without hand-writing a Go copy of every constraint, because the copy and the
database disagree eventually and the database is the one that is right.

## What must hold

1. A refused write can report every violation the payload contains, not only the
   one the database happened to reach first.
2. Each violation names a path into the request the client sent — the nesting the
   client used, not the table's columns. A payload nested one way and the same
   payload nested another way get different paths for the same column.
3. Each violation carries a stable machine-readable code, so a client can branch
   without reading a sentence.
4. Each violation carries a message written for a person, in the request's
   language where a translation exists, falling back rather than emitting a
   template.
5. Nothing in the response names anything internal: no constraint, table,
   column, SQLSTATE or engine error number, and no message parameter derived from
   one.
6. The set is either complete or explicitly marked incomplete. A capped answer
   says so rather than listing four violations in a way that implies there are
   four.
7. A violation is never invented. If the extra work finds nothing, the client
   still gets the violation the database actually reported.
8. The same failing request twice produces byte-identical output, so a response
   body can be asserted on.
9. Violations the caller's own input caused and violations caused by the stored
   state — someone else took that email — arrive in one list, distinguishable by
   code, because a form has to render both the same way.
10. The whole behaviour is off by default where it costs the most: a bulk write
    reports one violation unless the author asks for more.
11. Turning it on requires no per-endpoint code. Turning it off for one resource
    or one verb requires no fork.

## Out of scope

- **Being a validation library.** Length, range, format and required-ness that a
  caller wants checked *before* the database sees them belong to whatever
  validator the application already uses. This use case is about the constraints
  the database enforces, and about giving the two the same shape on the wire.
- **Replacing the constraint.** The index is the truth. Nothing here is a
  substitute for the unique constraint, and nothing here is consulted instead of
  the database.
- **Closing the disclosure.** Reporting that a value is taken tells the caller
  the value exists. That is inherent to a unique constraint on a public endpoint,
  and UC-004's gap 3 records it. This use case makes the trade adjustable, not
  absent.
- **Cross-row business rules.** "This user may not have more than five projects"
  is the service layer's (UC-013), not a database constraint.
- **Retryable failures.** A deadlock or a lock timeout is not something the
  client can fix, and does not belong in a list of things to fix.

## Covered by
| Flow | What it contributes |
|---|---|
| [[FL-011]] | the sentinel-to-status table this extends, and the point at which a body stops carrying detail |

## Status
**not covered.**

Nothing in the tree reports more than one violation. A refused write reaches a
client as a single 409 whose body carries the driver's own message — which is
also the leak UC-015 records.

Guarantee 5 is the one that is actively false today rather than merely absent:
a duplicate-key 409 currently carries the constraint name and the driver's
prefix out to the client.

`ROADMAP-errors.md` owns the work. Its phase 7 is where guarantees 1, 6, 7 and
10 arrive, phase 4 where 2, 3, 4, 5 and 8 do, and phase 0 — the captured error
corpus and the decisions — is what the rest is built on.
