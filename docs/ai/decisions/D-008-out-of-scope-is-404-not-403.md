# D-008 — An out-of-scope row is 404, never 403

**Status:** accepted
**Invariant:** When a policy `Scope` hides a row, every id-addressed read must answer `crud.ErrNotFound`; `ErrForbidden` is reserved for decisions that do not depend on whether the row exists.

## The decision

`gate.loadScoped` fetches one row through the policy scope by running a scoped
`GetAll` with `Limit(1)` rather than a `GetByID`. An empty result is
`crud.ErrNotFound`. `GetByID` and `Update` both go through it, so a tenant asking
for another tenant's id gets the same 404 as for an id that was never issued.

`ErrForbidden` is what `Authorize`, `Inspect` and the frozen-field checks return.
Those refusals are about the *action*, and they are reached before or
independently of the row's existence.

There is one deliberate exception, and it is the other way round: `Save` with an
id that names a hidden row is `Denied(Update, "row is outside the scope")` —
a 403.

## Why

A 403 confirms the row exists. Under a tenancy policy that is an enumeration
oracle: walk the id space, and 403 versus 404 tells you exactly which ids are
allocated and to whom. For sequential integer keys that is the whole table's
cardinality and growth rate; for anything with meaning in the id it is worse.

404 gives the same answer for "not there" and "not yours", which is the answer
that leaks nothing. The cost is a slightly worse debugging experience for a
legitimate caller who typed the wrong tenant, and that is the right trade for a
multi-tenant API.

**Why `Save` is 403 instead.** `Save` is an upsert ([[D-011]]). Left alone, a
policy that scoped rows and nothing else gave `Save` no protection at all: the
insert turned into an update and re-tenanted somebody else's row, with
`err == nil`. Refusing is the only move available, and refusing is observable, so
it is spelled honestly. `gate.saveTarget` distinguishes the two cases: a row
visible under the scope is an overwrite of the caller's own row, a row invisible
but present is the denial, and a row genuinely absent is an insert.

The statement *can* now be made to miss — [[D-011]]'s fourth row writes an
`UPDATE … WHERE pk = ? AND <scope>` — and that does not change this decision. The
scope that reaches the statement is the blueprint's, fixed at declaration and the
same for everyone. The gate's scope is per-principal and arrives with the request,
so it is not in the statement and there is nowhere to put it without giving `Save`
options ([[D-011]] forbids that). What the fourth row changed is that a
*declaration*-level scope no longer needs the gate to be safe; a policy scope
still does, and it still answers 403 rather than a silent miss.

**Why `loadScoped` does not run `Authorize`.** The caller decides which action is
being authorised — `GetByID` authorises `Read`, `Update` authorises `Update` —
so the load has to stay neutral.

## What it forbids

- Do not "improve" the error for an out-of-scope id to 403 or to a message that
  distinguishes the two cases. The message is the leak.
- Do not replace `loadScoped`'s scoped `GetAll` with a plain `GetByID` plus a
  post-hoc scope check in Go. That is check-then-act, and it also reads a row
  the caller is not allowed to see into this process's memory.
- Do not drop the scope from the *write* half of `Update`. Checking on the load
  and writing unscoped was exactly that bug: a row that left the scope between
  the two statements was updated anyway, and the fresh copy of somebody else's
  record was handed back with `err == nil`
  (`crud/decorators/security/security.go:gate.Update`).
- Do not fold `Save`'s denial into a 404 for consistency. A policy scope is not in
  the statement; silence there means a successful overwrite.

## Where it lives

- `crud/decorators/security/security.go:gate.loadScoped` — the scoped fetch and
  the `ErrNotFound`.
- `crud/decorators/security/security.go:gate.GetByID` and
  `crud/decorators/security/security.go:gate.Update` — both route through it.
- `crud/decorators/security/security.go:gate.saveTarget` — the deliberate 403.
- `crud/decorators/security/security.go:Denied` — wraps `security.ErrForbidden`,
  which wraps `crud.ErrForbidden`.
- `crud/sqlrepo/repository.go:repository.Delete` — the blueprint scope is in the
  `DELETE`'s own `WHERE`, so a row that is invisible to `GET /:id` is not
  deletable by id either.
- `crud/http/crudfiber/options.go:Status` — `ErrNotFound` → 404, `ErrForbidden` → 403.
- `crud/http/crudfiber/options.go:WithScope` — documents the asymmetry it does *not*
  fix: a handler-level scope reaches reads only, so `GET /:id` is 404 while
  `DELETE /:id` is 200. Row-level rules on writes belong in `security.Gate`.

## Proven by

- `TestOutOfScopeIDLooksMissing` in
  `crud/decorators/security/security_test.go`.
- `TestAnIDInAnotherTenantIsInvisibleRatherThanForbidden` in
  `crud/decorators/security/edge_test.go`.
- `TestUpdateLoadsThroughTheScopeSoAnOutsideRowIsNotFound` in
  `crud/sqlrepo/blueprint_edge_test.go` — the same rule for the blueprint's own
  permanent scope.
- `TestAnUpdateOfARowThatLeftTheScopeIsNotFound` in
  `crud/decorators/security/gate_edge_test.go` — the check-then-act window.
- `TestTheGateScopeIsInTheUpdatesOwnWhereClause` in
  `crud/decorators/security/gate_edge_test.go`.
- `TestAScopeWithoutInspectStillRefusesAnOverwriteOfAHiddenRow` in
  `crud/decorators/security/gate_edge_test.go` — the `Save` exception.
- `TestAScopedSaveOfAnUnusedIDIsStillAnInsert` in
  `crud/decorators/security/gate_edge_test.go` — the control for the line above.
- `TestARowHiddenFromReadsIsStillDeletableByID` in
  `crud/http/crudfiber/write_edge_test.go` — pins the documented `WithScope`
  asymmetry so nobody mistakes it for protection.
- `TestScopeReachesDeleteByID` in `crud/sqlrepo/paging_edge_test.go`.
- `TestRepositoryErrorsBecomeStatusCodes` in `crud/http/crudfiber/edge_test.go`.

## See also

[[D-004]] [[D-007]] [[D-011]] [[D-015]] [[D-026]]
