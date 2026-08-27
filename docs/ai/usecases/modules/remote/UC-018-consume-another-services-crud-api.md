# UC-018 — Consume another service's CRUD API

**Actor:** the application author on the calling side — one service in an estate reading and writing another's resource
**Covered by:** [[FL-018]] [[FL-013]] [[FL-015]]

## Scenario

One service declares a CRUD API over a model. Another needs that model: to read
a page of it, to look one up, to create one, to patch one. Today that author
writes an `http.Client`, a set of body structs that duplicate the model, and a
switch over status codes — and every guarantee the serving side established
stops at the network. A 409 becomes `resp.StatusCode == 409`, the violations
become a body to re-parse by hand, and the branch they had already written
against a local repository has to be written a second time in a different
vocabulary.

They want the same model, the same query language and the same error branches,
whichever transport is underneath — and they want to be told, rather than to
discover, what the network costs.

## What must hold

1. The calling side holds the resource at the same `port.Repository` seam it
   would hold a local repository, and calls the same methods with the same
   arguments. Swapping a local repository for a remote one is a wiring change.
2. A filter written in the library's own predicate vocabulary asks the far side
   the same question a local call would have asked, including a filter on a
   keyed read. Not an approximation of it: the same eligible rows.
3. Every method the repository interface declares works — a page, everything, one
   by key, a create, a replace, a patch, one delete, a bulk delete, a count.
4. A refusal keeps its class. A missing row is a missing row, a collision is a
   collision, a refusal by policy is a refusal by policy, and each matches the
   same sentinel it would have matched in this process.
5. A refusal keeps its violations: every one it carried, with the path into the
   payload the caller sent and the machine code it can branch on, and the marker
   that says a set was cut short.
6. Nothing internal arrives. What the serving side would not tell a browser it
   does not tell a calling service either — no constraint name, no table, no
   engine number, no driver sentence — and an internal failure arrives saying
   nothing at all.
7. An answer that did not come from this library is never read as one. A wrong
   address, a proxy, a gateway or a method that was never registered arrives as
   a failure of the call, not as an answer about a row.
8. Anything the caller asks for that cannot cross is refused before the call is
   made, and named. The only exceptions are options that change the order or the
   freshness of the rows rather than which rows they are, and those are written
   down where the author will read them.
9. A partial update sends only what it defines. A field the caller left alone
   arrives as absent and not as null, and a request that cannot keep that
   distinction fails when the program starts rather than when a row is emptied.
10. The same claims hold on every transport the library serves, and where one
    transport can carry less than another, the difference is stated rather than
    discovered.
11. A remote resource can be served again. The calling service can mount it on
    its own routes and become a gateway. Decorators requiring `crud.Core`,
    including `security.Gate`, remain an explicit far-service boundary rather
    than a composition this transactionless resource pretends to support.

## Out of scope

- **Transactions.** A remote resource is not a `crud.Core` and has no `Tx`. A
  transaction does not cross a stateless call, and offering one would be a
  promise nothing can keep.
- **Retries, circuit breaking, back-off.** The framework does not retry on the
  caller's behalf ([[D-040]]); a retryable failure says so and the decision is
  the caller's. The HTTP client takes an `http.Client` and the gRPC one takes a
  connection, which is where a policy belongs.
- **Discovery, load balancing, connection management.** An address and a
  connection are given to the client, not found by it.
- **A generated client.** The resource is typed by the model, not by generated
  per-field methods.
- **Aggregates and row locks.** No transport serves the first and nothing across
  a call can hold the second.
- **A cross-page database snapshot.** `GetAll` is a sequence of bounded remote
  reads, so concurrent far-side changes can move rows between pages. A
  snapshot-consistent export is a separate far-side operation, not a property a
  stateless CRUD client can manufacture.

## Covered by

| Flow | What it contributes |
|---|---|
| [[FL-018]] | the call out and the failure back, the option answers, and where each inverse table lives |
| [[FL-013]] | what the two transports do differently, in both directions |
| [[FL-015]] | the receiving half, which this one is the mirror of |

Guarantees 4, 5 and 6 are [[UC-015]]'s and [[UC-017]]'s guarantees read from the
other end: the same envelope and the same status table, inverted.

## Status

**covered.**

Guarantees 1 through 10 and the gateway half of 11 have tests over both transports, run
against a real binding on a real server — `httptest` for HTTP and `bufconn` for
gRPC — so what is asserted is that the encode and the decode agree rather than
that the client agrees with itself. In particular, both exercise `GetByID`'s
List fallback with a root filter and a filtered, sorted, capped preload.

`GetAll` is additionally checked against a far-side page cap on both transports:
it reads one bounded page, follows its cursor edges, and returns the combined
items. The HTTP path is also exercised against a real SQL repository with
`MaxLimit(1)` and `MaxOffset(1)`, proving the cursor transition avoids an
offset-budget failure. If progress is malformed — an inconsistent offset total,
an empty page claiming more, or a missing/repeated edge — it returns
`*remote.PartialResultError`. These checks catch detectable structural
contradictions; they do not manufacture a cross-page snapshot or prove that a
dishonest service omitted a row while returning internally coherent metadata.
A custom list that does not provide cursor edges remains supported through
offset pages and must configure a sufficient `MaxOffset`, as documented in the
module reference.

Guarantee 2 is the one that needed new machinery: the predicate AST is closed,
so a filter could not leave the process at all. `crud.MarshalPredicate` is
[[D-054]], and what proves it is not a table of expected documents but a round
trip asserted on rendered SQL and binds — the document is a shape, the SQL is
the question. A filtered `GetByID` uses List with the key equality added to the
same document, because an HTTP entity route has no root-filter spelling. A
per-preload `maxRows` travels in that document too and can only tighten the
far endpoint's `MaxPreloadRows` ceiling. Because it is the ordinary public
List grammar, a restrictive peer must list the primary key and the caller's
filter fields in `query.Config.Filterable`; that refusal is preferable to a
keyed read that silently weakens its eligibility check.

Guarantee 8's line is [[D-053]]: refuse what changes which rows come back,
document what changes only their order or their freshness. Two options are in
the second set, and both are named in the package documentation and in the
decision. The cost is real and is stated there: `crud.PrimaryOnly` cannot be
honoured, so a read that decides a write is served by whatever the far side's
replica policy allows.

Guarantee 10's differences are two, both in [[FL-013]]: the fault's own code
arrives exactly over gRPC and is recovered from the first violation over HTTP,
because the envelope carries no separate field for it; and gRPC's `InvalidArgument` is two of HTTP's statuses,
so the code has to undo the collapse [[D-052]] accepted.

The `crud.Core` decorator half of former guarantee 11 is deliberately not
claimed as covered: a remote resource has no transaction and cannot make a
client-side gate enforce the owning service's policy. The far service's gate is
the authority for that rule.

There is one HTTP client and not three. A consumer calling out uses `net/http`
whatever it serves with, and the three bindings register the same routes —
which their own `routing_test.go` triplets already prove.
