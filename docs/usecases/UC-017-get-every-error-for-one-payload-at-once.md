# UC-017 — Get every error for one payload in one response

**Actor:** a client rendering a form, and the application author who does not
want to write the same validation twice
**Covered by:** [[FL-017]] [[FL-014]] [[FL-011]]

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
| [[FL-014]] | where the one violation is produced from what the database reported, and what it may carry |
| [[FL-017]] | where the rest of them are found: the plan, the statement, the caps, the transaction matrix and the merge that gives an unnamed violation a field |

## Status

**Covered.**

A refused write now reports every violation the payload caused, keyed to the
fields the client sent, in one response.

| # | holds | since |
|---|---|---|
| 1 — every violation, not only the first | yes | phase 7 |
| 2 — a path into the request, not a column | yes | phase 4, extended by phase 7 with the row index a batch needs |
| 3 — a stable machine-readable code | yes | phase 2 derived it, phase 4 rendered it |
| 4 — a message for a person, falling back rather than emitting a template | yes | phase 1 and 4 |
| 5 — nothing internal in the response | yes | phase 4 ([[D-044]]) |
| 6 — complete, or explicitly marked incomplete | yes | phase 7 |
| 7 — never invented | yes | phase 7 |
| 8 — byte-identical output for the same failing request | yes | phase 4 fixed the order, phase 7 is the first thing that produces enough violations for it to matter |
| 9 — input and state violations in one list, told apart by code | yes | phase 4 |
| 10 — off by default where it costs the most | yes | phase 7: a bulk write gets the cheap answer unless the author names the verb |
| 11 — no per-endpoint code to turn on, no fork to turn off | yes | phase 7: one option at the repository declaration, and one per verb |

Three things are worth being precise about, because they are where the guarantee
is narrower than it reads.

**"Every violation" means every violation the probe can reproduce from a value.**
CHECK constraints, NOT NULL, length, range and enum membership are not in the
set, and that is [[D-042]]'s argument rather than an omission: a Go-side copy of
a rule disagrees with the server eventually, and the copy is the one that is
wrong. Four kinds of unique key are not in the set either — partial, prefix,
expression-keyed and deferrable — because none of them can be replayed from a
value without claiming a check that did not happen. Every one of those is a
narrowing: the answer is short, never wrong.

**Guarantee 1 has a per-engine ceiling.** Inside a transaction, PostgreSQL
reports one violation unless the application asked for the savepoint mode, and
inside a *foreign* transaction it reports one whatever the mode says. MySQL,
MariaDB and SQLite report the full set with no extra statement. [[D-019]]
difference 11 names it, and both usage guides carry it.

**Guarantee 4's foreign-key wording is still owed.** On PostgreSQL and SQLite a
row that is still referred to and a row that refers to nothing are one and the
same key, and phase 2 said telling them apart needs the verb rather than the
error. The probe now tells them apart from the *other* side — it builds a
`restrict` term only for the inbound direction and a `foreign_key` term only for
the outbound one — so a probed violation carries the right one of the two. A
violation the driver reported and the probe did not cover still carries whatever
the classifier decided.

### What each phase contributed

Phase 0 captured the corpus and wrote the decisions. Phase 1 built the contract:
a violation is one type carrying a path, a stable code, a message and where it
came from, and the projection that reaches a client is the type's own rather than
a renderer's habit. Phase 2 made the code derivable on all four engines from the
key alone and never from the sentence the server wrote, and closed a matching
hole in the *existing* 409 — a constraint deferred to `COMMIT` was a 500 where
the same violation raised at the statement was a 409. Phase 3 produced the first
violation from a driver error rather than from nothing. Phase 4 rendered it, and
closed the disclosure: no body names anything internal at any status. Phase 6
read the schema the probe needs. Phase 7 is the one that makes the list longer
than one.
