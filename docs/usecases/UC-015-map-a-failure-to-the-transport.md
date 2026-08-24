# UC-015 — Map a failure to the transport correctly

**Actor:** an HTTP client reading the status code, and the application author who
does not want to write the mapping once per endpoint
**Covered by:** [[FL-011]]

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
| [[FL-013]] | that a second binding inherits the table rather than restating it |

## Status
**covered, with one deliberate leak worth knowing about.**

Every status in guarantee 1 has a test, including the branches no route reaches
with a real repository behind it; the "same refusal from every route" table and
the 500-leaks-nothing assertion are both exhaustive over the route set, and both
are run once per HTTP binding.

Guarantee 8 — that the mapping is a function an application can reuse — is now
also what keeps the bindings honest rather than only a convenience: there is one
switch, and removing an arm from it fails both bindings' suites identically.

The leak: every status *except* 500 puts the error's own text in the response
body. That is what makes guarantee 5 useful, and for a 400 the text is the query
compiler's. For a 409 it is the driver's — the adapters classify an integrity
violation by SQLSTATE and keep the driver error underneath, so a duplicate-key
409 can carry a constraint name, a column name and the driver's own prefix out to
the client. That is the current design (a test asserts a 409 says *something*),
but it is a wider disclosure than "a 500 must leak nothing" implies, and an
application that treats constraint names as internal has to install its own
mapping to close it.
