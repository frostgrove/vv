# D-113 — A resource states its mounted operations as one set

**Status:** accepted, generalises the `ReadOnly` switch · closes H-CRUDHTTP-18
**Invariant:** which of a resource's ten routes are mounted is one value on
`port.Rules`, read by every transport and by the declaration `crudhttp.Table`
derives. A resource cannot be mounted with one surface and declared with another,
and there is no set of routes that can only be reached by hand-registration.

## The decision

`port.Operations` is a bitmask over the ten operations a CRUD resource performs —
`OpList`, `OpQuery`, `OpCount`, `OpCountQuery`, `OpGet`, `OpCreate`, `OpUpdate`,
`OpReplace`, `OpDelete`, `OpBulkDelete` — with `Reads`, `Writes`, `Deletes` and
`AllOperations` as the groups that cover the ordinary cases. `Rules.Expose`
carries it, `Rules.Mounted()` resolves it, and each binding's `Exposing(ops)`
option sets it. `crudhttp.Table` carries the same field, so the access
declaration is built from the same set the router is walked with.

`ReadOnly` stays and means `Reads`. It is the commonest case, it is public, and
consumers spell it in their composition roots; a rename would buy nothing.

**The unit is the operation, not the HTTP verb.** A verb is not enough to name a
route here: `POST` is create, query, count-as-a-document and bulk-delete, and a
consumer removing "the POSTs" wants a different four of those in each case.
`crudhttp.Table` already named the ten routes for the declaration, and this is
that same vocabulary made available to the mount.

**A whitelist, not a blocklist.** `Without(...)` composes more prettily and was
rejected: an operation added to this library later would mount itself on every
existing resource, and on a resource whose surface was deliberately narrow that
is a new write route nobody asked for. The zero value still means every
operation, so nothing changes for a resource that never states a set.

**`ReadOnly` and `Expose` together is a start-up panic.** Both fields say what is
mounted. Letting one win silently means the answer is discoverable from a
customer; intersecting them means a resource whose surface is the intersection of
two things nobody wrote down. `Rules.RefuseContradictions(who)` refuses it in
`build`, which every constructor in every binding funnels through — not in
`Serving*` alone, because the mistake does not depend on whether the caller
brought a repository or a finished service.

**gRPC mounts the subset it has.** `crudgrpc` has no `query` and no
`count-query` method — its `List` takes the document — so those two bits mount
nothing there. That is the same shape as every other difference in [[FL-013]]:
the set is transport-neutral, and what a transport has no route for it does not
grow one.

## Why not eleven options

The obvious alternative is a `DisableCreate()`, `DisableUpdate()`, … per
operation. It is eleven names on four bindings, it does not compose, and it
cannot be handed to `crudhttp.Table`, which is a struct and not an option list —
so the declaration would go on being derived from a second, hand-maintained
description of the same surface. One field on the shared struct is reachable from
both, which is the property that matters.

## Proven by

- `TestExposingMountsExactlyTheOperationsItNames` —
  `crud/http/crudfiber/options_test.go`, `crudgin`, `crudnet`. Reads plus both
  deletes: every named route answers, every unnamed one does not run, and the
  repository saw exactly the calls the set allows.
- `TestExposingCanDropTheReadPosts` — same files. The half of H-CRUDHTTP-18 about
  a gateway rule of "POST means a write": `OpList|OpGet|OpCount` leaves no POST
  mounted at all, which `ReadOnly` could not do.
- `TestReadOnlyAndExposingTogetherIsRefusedAtDeclaration` — same files. The
  contradiction panics where the resource is built, and the message says what to
  do about it.
- `TestReadOnlyMountsOnlyTheReadRoutes` — same files, unchanged. `ReadOnly` is
  now one value of the new mechanism, and it still mounts what it always did.

## See also

[[D-107]] [[D-073]] [[D-100]] [[D-045]] [[FL-013]] [[UC-002]]
