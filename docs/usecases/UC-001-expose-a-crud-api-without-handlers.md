# UC-001 — Expose a full CRUD API for a resource without writing handlers

**Actor:** the application author, on behalf of every HTTP client of the resource
**Covered by:** [[FL-001]] [[FL-002]] [[FL-003]] [[FL-004]] [[FL-011]] [[FL-012]]

## Scenario
A resource needs the usual endpoints: list it, query it, count it, fetch one,
create, patch, replace, delete, delete a set. Written by hand that is nine
handlers with the same page/limit/sort parsing, the same id parsing, the same
five error mappings, and the same three-state PATCH problem — per resource, times
twenty resources. The author wants to declare the model once and mount the
resource, and wants the twentieth resource to cost exactly what the first did.

## What must hold

1. Declaring a resource is two statements: one that declares the model, its key
   type and its update DTO, and one that mounts the routes. Adding a resource
   requires no new handler, no new route registration and no new error mapping.
2. Mounting produces all of: list, query, count (both verbs), fetch one, create,
   partial update, replace, delete one, delete many.
3. The routes are mountable both as a standalone group and onto an existing
   router or prefix, and the fixed paths are not swallowed by the id parameter
   route.
4. A list answers with a page envelope carrying the items, the page number, the
   page size, the total, the number of pages and whether there is a next and a
   previous page. The arithmetic is the library's, not the caller's.
5. A create returns 201 and the stored row — including every value the database
   produced: a generated key, a default, a computed column.
6. A create cannot choose a database-generated key, and cannot set a column
   declared as generated. Both are cleared from the body before the write. Where
   the client legitimately owns the key — a uuid, a slug — this does not apply,
   and there is an explicit opt-in for handing the key space over.
7. Where the database generates the key, replace *replaces*: the id in the URL
   must name an existing row, and replace never creates one. Otherwise replace
   would be the way around guarantee 6.
8. A replace takes the id from the URL and not from the body.
9. Delete of one row reports how many rows went away, and 404 when that is zero.
10. Bulk delete takes a set of ids, passes them to a single call, reports the
    count, and answers zero for an empty set without touching the database. The
    number of ids in one request can be capped.
11. A count honours the request's filter and ignores everything that means
    nothing to a count — paging, sorting, preloads, projection.
12. Fetching one entity honours the shaping options — which relations to load,
    which columns to return — and ignores filtering, sorting and paging.
13. Every failure maps to a status by the rules of UC-015.
14. A broken declaration — a model with no key, a DTO field that names nothing,
    an id type that does not match the key — fails when the package is
    initialised, not on the first request that touches it.
15. Read-only mounting is available and mounts only the read routes; the write
    routes are absent, not merely refusing.

## Out of scope

- **What a client may filter, sort or preload.** Unbounded by default; bounding
  it is UC-002.
- **Who may see which rows.** The generated API has no opinion. That is UC-004.
- **Business rules.** A quota, an audit row, a field only an admin may change:
  UC-013.
- **Custom verbs.** A resource that needs `POST /users/deactivate` gets a route
  the author writes; the library compiles the client's query document for it, but
  does not invent the endpoint.
- **The response shape of an entity.** It is the model, as JSON, unless a
  presenter is installed (UC-013).
- **Any transport but HTTP.** Three HTTP bindings are written and tested —
  Fiber, Gin and `net/http` — and the transport-shaped half of a binding is a
  package of its own, so a fourth is small. gRPC, GraphQL and a message queue
  are not written.

## Covered by
| Flow | What it contributes |
|---|---|
| [[FL-001]] | the list and count routes, from request to page envelope |
| [[FL-002]] | the partial-update route |
| [[FL-003]] | the create and replace routes, and what is cleared before the write |
| [[FL-004]] | the declaration, and what is checked when the package initialises |
| [[FL-011]] | every route's failure path |
| [[FL-012]] | the path id and the query-string values becoming Go values |
| [[FL-013]] | the second binding: what it shares and the four things it does differently |

## Status
**covered.** The route set, the mounting shapes, the page envelope arithmetic,
the cleared key and generated columns, the replace-never-creates rule, the
delete counts, the count dropping everything but the filter, and the read-only
mount all have unit tests against a recording datasource, and the create /
patch / delete lifecycle plus pagination and the rejection paths are re-run end
to end against live PostgreSQL and MySQL.

There are three bindings — Fiber, Gin and `net/http` — and they answer the same
147 unit tests and the same end-to-end suite. Mounting one or another is the
same line of code with a different package name, and a service type written
against one satisfies the others unchanged: the interface is literally the same
type, which the integration suite proves by mounting one service on all three.
The `net/http` binding costs nothing to have, because it needs no dependency.

The gap that remains is narrower than it was. A project on Echo, chi or gRPC
still writes its own routes — though one on chi or gorilla/mux can register the
`net/http` binding's handler methods one by one instead, because they are
ordinary `http.HandlerFunc`s. What nobody has to write is the interesting part:
the status table, the bad-request sentinel, the create-time clearing of a
generated key, the id coercion and the count/entity narrowing all live in a
package with no framework in it. Writing the third binding is the evidence: it
needed nothing added to that package.

Where the three differ is named rather than papered over — mounting, body
encodings, and what each router does with a trailing slash or a method it does
not have. [[FL-013]] carries the table.
