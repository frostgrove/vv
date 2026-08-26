# UC-013 — Insert business rules between the handler and the repository

**Actor:** the application author
**Covered by:** [[FL-001]] [[FL-002]] [[FL-003]] [[FL-011]] [[FL-013]] [[FL-015]]

## Scenario
The generated endpoints are right for eighteen of twenty resources. The other two
have a rule: a quota to check before a create, an audit row to write after one, a
field only an admin may patch. The author does not want to abandon the generated
API for those two, and does not want the handler to grow a special case. What
they want is a place to stand between the two — a type the handler accepts in the
repository's stead — and, for the rules too small to justify one, a hook on the
handler itself.

## What must hold

1. The handler accepts an interface, not a concrete repository. Any type
   satisfying it can be mounted, and the routes, the query DSL, the pagination
   and the error mapping are unchanged.
2. A struct that embeds a repository satisfies that interface without writing a
   single forwarding method. Overriding one method is the whole cost of
   intercepting one operation.
3. A service method that refuses a request stops it: the wrapped repository is
   never called, and no statement reaches the database.
4. A refusal expressed by wrapping a library sentinel gets that sentinel's status
   (UC-015). The service layer does not import the transport, and the transport
   does not import the service layer.
5. A service method may mutate what it forwards, and the mutation is what the
   repository writes.
6. The same interception is available below the handler as well, as a decorator
   that wraps the repository itself, so a rule that must hold for *every* caller —
   not only for HTTP — is enforced in one place. Decorators compose, and the
   order they are declared in is the order they observe a call.
7. For rules that do not need a whole layer, the handler exposes hooks: one that
   runs on create and replace after the body is bound and the server-owned fields
   are cleared, and one that runs on a partial update after the DTO is bound. A
   hook sees the request, can mutate the value, and can refuse. That ordering is
   the guarantee and not an accident of where the code sits: the hook runs where
   the clearing runs, so moving one moves the other.
8. A hook's mutation reaches the repository, and a hook's refusal maps like any
   other error.
9. A per-request read narrowing is available for genuinely transport-shaped rules
   — an `?includeArchived` flag — and it is ANDed with whatever the client sent,
   so the client cannot widen it.
10. That read narrowing reaches reads and *only* reads. Create, replace, patch,
    delete and bulk delete take no options, so there is nowhere for a predicate to
    go: with a read narrowing in place, a row hidden from `GET /:id` is still
    deletable through `DELETE /:id`. Row-level rules that must cover writes belong
    to the access-control gate (UC-004), not here.
11. A presenter can render every entity on its way out, on every read and write
    route, so a column can exist in the model and never reach the wire. A
    presenter that returns something the encoder cannot write is the author's
    mistake and the library cannot prevent it, but the client is told so: the
    answer is a silent server failure, and never a success status over a body
    that was half written or a plain-text copy of the encoder's complaint.
12. The request body may be a type of its own rather than the model, mapped onto
    the model before any rule runs, and choosing one costs none of the generated
    routes. The mapping is declared once and the same routes, options, hooks and
    error mapping apply to it.
13. The seam a rule is written against is one value on every transport,
    including one that is not HTTP. A layer written once mounts on each of the
    bindings unchanged — no per-transport interface, no second copy of a rule,
    and adding a transport does not ask the author to write anything.

## Out of scope

- **Row-level access control.** A service layer is the wrong shape for "which
  rows may this principal see" — it runs in Go, per call, and cannot narrow a
  `COUNT`. That is UC-004.
- **Ordering guarantees against the database.** A rule that must be atomic with
  the write needs a transaction the service layer opens (UC-005). Nothing here
  makes a hook and the statement it precedes atomic.
- **ORM hooks.** A write issued by this library does not go through an ORM's
  callback chain, so the ORM's own `BeforeCreate` and its Go-side defaults do not
  fire. That is UC-010's problem and it is stated there.
- **Non-CRUD endpoints.** A service layer that adds *methods* is fine, but the
  generated routes only call the interface's methods. A new verb is a route the
  author writes.

## Covered by
| Flow | What it contributes |
|---|---|
| [[FL-001]] | the read path the interface is spliced into |
| [[FL-002]] | the update hook's position relative to the DTO binding |
| [[FL-003]] | the create hook's position relative to clearing server-owned fields |
| [[FL-011]] | a service refusal becoming a status |
| [[FL-013]] | the same hooks under the other two bindings, each with the request type its framework uses |
| [[FL-015]] | the service seam itself, the distinct input type, and where the hook ordering is now enforced |

## Status
**covered.** The stand-in interface, the embedding shortcut, both hooks (mutation
and refusal), the presenter on every read and write shape, the per-request read
narrowing and its AND with the client filter, and the read/write asymmetry in
guarantee 10 all have tests. The asymmetry is pinned deliberately — a test fails
if writes ever start being narrowed — so the documentation and the behaviour
cannot drift apart. A service layer intercepting a write is also exercised end to
end through the HTTP suite against a live database.

Guarantees 12 and 13 arrived with the transport-neutral service seam. The
distinct input type has a test in each binding whose control is the same body
through the plain constructor, where the input type's keys mean nothing; the
one-value-on-every-transport claim has a test that mounts one service on all
three and compares what each of them asked the service for, not merely that they
compiled.

Guarantee 13's "adding a transport does not ask the author to write anything"
was an argument until phase 9 and is now measured. A fourth binding on a
protocol that is not HTTP mounts the same service value, is handed the same
commands, and asked for nothing new: the whole of it was written without a line
changed in the shared half. One test runs the same request through all four and
compares the *command* each handed over, so a binding that started re-deriving a
rule the service owns is named rather than merely suspected. Guarantee 7's ordering
moved from three bindings to one service and is pinned in both places — in the
service directly, and through each binding with a control that hands the key
space back to the client and watches the hook see it.
