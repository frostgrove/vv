# UC-011 — Test repository behaviour without a database

**Actor:** the application author writing tests, and CI
**Covered by:** [[FL-001]] [[FL-002]] [[FL-004]]

## Scenario
Most of what can go wrong in this layer is visible in the statement: the wrong
column in the `SET` list, an argument in the wrong position, a scope that did not
make it into the `WHERE`, a page-size calculation off by one, a decorator that
did not wrap what it thought it wrapped. None of that needs a database, and a
test that needs one runs in a container, takes seconds, and does not run on a
laptop with no Docker. The author wants to assert the statement a repository
produces, and to drive a handler over a repository that is not there.

## What must hold

1. There is a public in-memory datasource that a repository binds to exactly like
   a real one. Nothing about the declaration changes.
2. It records every statement in order, with its arguments and whether it was a
   read or a write, and the recording is readable from the test.
3. The most recent statement is directly reachable, and asking a recorder that
   has run nothing yields a zero value rather than a panic.
4. There is a whitespace-normalising helper, so a test can assert the shape of a
   statement without pinning the statement builder's formatting.
5. Rows can be queued and are handed back one queued set per read, in order.
   Values are assigned positionally and converted the way Go would convert them,
   so a hand-written literal row is ergonomic.
6. A queued row that cannot fill the destinations is an error naming what went
   wrong — wrong number of values, a type that cannot be assigned, a destination
   that is not a pointer — rather than a silently zeroed model.
7. Null values behave as they do against a real driver: a pointer stays nil, and
   a three-state optional becomes explicitly null rather than undefined.
8. The dialect is chosen by the test, including the three the library ships, and
   any dialect written outside it. Choosing the dialect is how a test covers both
   the path that reads values back from the write and the path that has to
   re-read them.
9. Failures are injectable in two independent ways: a queued read that returns an
   error, and a one-shot write failure that fails exactly the next write and lets
   the following one succeed. A failed statement is still recorded.
10. A write's result — rows affected and a database-generated key — is settable,
    so the repository's handling of both is testable.
11. Transactions work against it: opening one records into the same recorder, so
    a test asserts the begin, the statements and the commit in one place, and the
    transaction helper drives it end to end. The number of transactions opened is
    readable.
12. The handler layer is testable over a stand-in repository, because the handler
    takes an interface. A test can assert which methods were called, with what
    id, what model, what DTO, and what compiled options — and can make any of them
    fail to test the error mapping.
13. The compiled options are inspectable as data: the filter can be rendered to
    SQL, and the sort terms, preload paths and per-preload narrowings read off,
    without a database and without going through the repository.
14. The whole of this library's own unit suite runs this way — statement shape,
    argument order, pagination arithmetic, decorator composition, the security
    gate and the HTTP layer — with no container.

## Out of scope

- **Proving the statement is *correct* for an engine.** A recorder confirms what
  was sent, not that PostgreSQL likes it. Dialect behaviour, upsert forms,
  rows-affected divergence and locking need the database-backed suite.
- **Query planning, indexes, performance.** Nothing here says anything about
  cost.
- **Constraint violations.** A unique index does not exist in memory. A conflict
  can be injected as an error but not provoked.
- **A shipped fake repository for the handler.** Guarantee 12 describes what is
  possible through the public interface, not a facility the library hands over.
  See Status.
- **Round-tripping.** Rows queued in are not related to statements recorded; a
  test that writes and then reads has to queue the row it expects to read.

## Covered by
| Flow | What it contributes |
|---|---|
| [[FL-001]] | the read path whose statement a recorder captures |
| [[FL-002]] | the write path, and why the dialect choice changes what a test sees |
| [[FL-004]] | binding a declared repository to a recorder rather than a database |

## Status
**covered for the repository, partially covered for the handler.**

The recorder is a public, importable package. Statement order, the read/write
flag, argument capture, positional row assignment with Go's own conversions, the
three malformed-row errors, null handling for pointers and three-state
optionals, the dialect shorthands, both failure seams, the settable write result
and the transaction recording all have tests. The claim in guarantee 14 is
verifiable by running the suite with no container.

**The gap is guarantee 12.** The handler accepts an interface, and this
repository's own handler tests exercise that seam thoroughly — including a
service layer standing in for the repository, every error mapping, and
assertions over the compiled options. But the stand-in repository and every
helper around it (mounting, sending a request, rendering the filter, reading the
sort terms and preload paths) live in an internal test file and are not exported.
An application wanting the same leverage rewrites them, or takes the lower road:
bind a real repository to a recorder, mount the handler over that, and assert the
SQL. Nothing prevents the second approach and nothing demonstrates it.

Smaller sharp edges worth knowing: running out of queued rows is not an error — an
unexpected extra read gets an empty result set, indistinguishable from "no rows
matched", so a test asserting emptiness can pass for the wrong reason. And
resetting the recorder clears the statements and the queue but not the write
result, a pending failure, or the transaction count.
