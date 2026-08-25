# UC-015 — Map a failure to the transport correctly

**Actor:** an HTTP client reading the status code, and the application author who
does not want to write the mapping once per endpoint
**Covered by:** [[FL-011]] [[FL-014]]

## Scenario
Every endpoint fails in the same handful of ways: the row is not there, the
caller is not allowed, the write collided with something, the request named a
field that does not exist, or the database fell over. The author wants one
mapping for all of them, applied identically on every route, and wants the last
category — the one where the message could be a connection string or a fragment
of SQL — to be the only one that says nothing. The client wants a status it can
branch on and, where the mistake is its own, enough detail to fix it.

## What must hold

1. A missing row is 404. A refusal by an access-control policy is 403. A
   collision — a duplicate key, a foreign key pointing nowhere, a `NOT NULL`
   violation, a lookup that matched several rows, a stale version — is 409. A
   request the model cannot answer — an unknown field, a value that will not
   coerce, an unparseable id, a malformed body, a save with no key — is 400.
   Anything else is 500.
2. Matching is by sentinel, not by type identity. An error with context wrapped
   around it maps to the same status as the bare sentinel, so a decorator or a
   service layer can add its own words without changing the status.
3. The same failure produces the same status on every route. A policy refusal is
   403 whether it arrived through a list, a count, a get, a create, a patch, a
   replace, a delete or a bulk delete.
4. A 500 body carries the status and nothing else. No driver message, no host, no
   credential, no SQL, no wrapped detail — asserted against a message deliberately
   built out of all of those.
5. A 400 caused by the query document names the offending path, so the client can
   see which field it got wrong rather than being told the request was bad.
6. Deleting one row that is not there is 404; a bulk delete that removed nothing
   is a successful `0`. One names a row and the other names a set, and an empty
   result is a truthful answer about a set.
7. A per-request hook that refuses a request produces the refusal's own status and
   the repository is never called. A per-request hook that fails for an
   infrastructure reason is a silent 500, and the repository is still never
   called. The client can tell the two apart from the status alone.
8. The mapping is available as a function, so an application that renders its own
   error bodies gets the same statuses without reimplementing the table.
9. The whole mapping is replaceable, per handler, without giving up the routes.
10. The transport never needs to import the layer that raised the error. Access
    control, the query compiler and the repository all speak in the same
    sentinels.
11. No response body names anything internal. Not at 500, where nothing is said
    at all, and not at 409 or 400 either: no constraint name, table name, column
    name, engine error number, or message parameter derived from one. What a
    client is told is a code, a path into the request it sent, and a sentence
    written for a person.
12. One status may carry more than one violation. A refusal is a status and a
    *list*, and the list is either complete or says it is not.

## Out of scope

- **Whether a failure is the right one.** This use case promises the mapping is
  faithful, not that the repository chose correctly. "A row outside your scope is
  404, not 403" is UC-004's guarantee; this one only promises 404 renders as 404.
- **Validation of business rules.** Wrapping the right sentinel is the service
  layer's job (UC-013). The mapping honours whatever it is handed.
- **Non-HTTP transports.** The sentinels are transport-neutral, but only the HTTP
  mapping is written and tested. Within HTTP the mapping is written once and
  every binding calls it, so it does not multiply as bindings are added.
- **Problem+JSON, i18n, error codes per field.** The body is a small fixed shape:
  an error tag, an optional path, an optional message.

## Covered by
| Flow | What it contributes |
|---|---|
| [[FL-011]] | the sentinel-to-status table, the body shapes, and the point at which a 500 stops carrying detail |
| [[FL-013]] | that a second and a third binding inherit the table rather than restating it |
| [[FL-014]] | where a driver error becomes a classified failure, and why a classified 409 says less than an unclassified one |
| [[FL-015]] | which half of the mapping is transport-neutral and which is HTTP, and the path chain's middle hops |

Guarantee 11 is [[UC-017]]'s territory as well: the two use cases share the
envelope, and the same decision governs both.

## Status
**covered.**

Every status in guarantee 1 has a test, including the branches no route reaches
with a real repository behind it; the "same refusal from every route" table and
the 500-leaks-nothing assertion are both exhaustive over the route set, and both
are run once per HTTP binding, of which there are three.

Guarantee 8 — that the mapping is a function an application can reuse — is now
also what keeps the bindings honest rather than only a convenience: there is one
switch, and removing an arm from it fails every binding's suite identically.

Guarantee 11 does not hold, and the set it fails on is narrower than it was.
Every status *except* 500 puts the error's own text in the response body. That is
what makes guarantee 5 useful, and for a 400 the text is the query compiler's.

For a 409 it now depends on whether the failure was **classified**. A classified
one carries a code and a kind and no driver text at all: the body reads
`conflict` and the classification, and names no constraint, no table, no column,
no engine number and no submitted value. One that was not classified still
carries the driver's own sentence — and there are three ways to be one: the
engine reported a violation number nobody has provoked, the write went through a
transaction the application owns and joined, or the datasource was built without
naming which engine it speaks to. In all three the status is right and the body
is the old one.

Guarantee 11 holds as of phase 4. `err.Error()` no longer reaches a body:
`crudhttp` renders one envelope built from the fault's public projection, and a
refusal that carries no fault is turned into a synthesised one first, so there is
nowhere for a driver's sentence to arrive from. The 500 body is a fixed value
with no message field at all, so [[D-015]]'s silence holds by construction rather
than by a case in a switch somebody may edit.

What proves it is not one hand-written case: a body is rendered from **every**
entry in the captured error corpus and asserted to contain no substring of that
entry's constraint name, table, column, SQLSTATE or native number. Asserting one
violation would have passed for a renderer that leaked a different field. How
wide the leak had been was measured the same way — the corpus records
PostgreSQL's `Key (slug)=(anchor) already exists.` and MySQL naming a database,
two tables and two columns in one sentence.

Two statuses moved in the same change, and the earlier version of this section
said they had not. A data error — a value too long, out of range, of the wrong
type — is now **422**, and a retryable one — a deadlock, a lock timeout, a
serialisation failure — is **503**. Both were 500 with a silent body. What
decides is [[D-049]]: the kind decides the status where a fault exists, and the
sentinel table decides only where none does. The cost is stated there and is
real — a violation the engine reported but nothing classified still answers 409,
so the status depends on whether classification succeeded.

**Guarantee 12 arrived with phase 7**, and it is a change in shape rather than in
mapping: one failed write can now answer one status carrying several violations,
because the database reports the first constraint it reaches and a second
statement finds the rest ([[FL-017]], [[UC-017]]). Nothing about the status
moved. What is new is that the array a client iterates is no longer one entry
long, and that a set the probe could not complete carries `"partial":true` rather
than reading as complete ([[D-042]]).
