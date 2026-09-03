# D-008 — An out-of-scope row is 404, never 403

**Status:** accepted
**Invariant:** When a policy `Scope` hides a row, every id-addressed call — read or write — must answer `crud.ErrNotFound`; `ErrForbidden` is reserved for decisions that do not depend on whether the row exists.

## The decision

`gate.loadScoped` fetches one row through the policy scope by running a scoped
`GetAll` with `Limit(1)` rather than a `GetByID`. An empty result is
`crud.ErrNotFound`. `GetByID` and `Update` both go through it, so a tenant asking
for another tenant's id gets the same 404 as for an id that was never issued.

`ErrForbidden` is what `Authorize`, `Inspect` and the frozen-field checks return.
Those refusals are about the *action*, and they are reached before or
independently of the row's existence.

`Save` was once the exception, in the other direction, and is not one any more.
An assigned key that names a hidden row is `crud.ErrNotFound` from
`gate.saveTarget`, and a row that leaves the scope between the probe and the
write is `crud.ErrNotFound` from `gate.saveScoped`. `PUT /articles/{id}` now
answers what `GET` on the same id answers.

## Why

A 403 confirms the row exists. Under a tenancy policy that is an enumeration
oracle: walk the id space, and 403 versus 404 tells you exactly which ids are
allocated and to whom. For sequential integer keys that is the whole table's
cardinality and growth rate; for anything with meaning in the id it is worse.

404 gives the same answer for "not there" and "not yours", which is the answer
that leaks nothing. The cost is a slightly worse debugging experience for a
legitimate caller who typed the wrong tenant, and that is the right trade for a
multi-tenant API.

**Why `Save` probes without the scope, and still answers 404.** `Save` is an
upsert ([[D-011]]). Left alone, a policy that scoped rows and nothing else gave
it no protection at all: the insert turned into an update and re-tenanted
somebody else's row, with `err == nil`. So `gate.saveTarget` looks twice — once
through the scope, and, on a miss, once without it. A row visible under the scope
is an overwrite of the caller's own row; a row genuinely absent is an insert; a
row present but hidden is refused.

The refusal is `crud.ErrNotFound`, not a denial, and that costs nothing: the
probe's one bit — this id is taken — is read by the gate and not handed back. A
core that cannot answer an unscoped existence question at all is treated the same
way, because the alternative is falling through to an insert that becomes an
overwrite.

`Save` refusing was once the only move, which is where the 403 came from. It is
not any more: the gate hands its per-principal scope to `crud.SaveScopedOf`, and
`repository.saveScopedUpdate` puts it — with a whole-row snapshot of what the
probe saw — into the `UPDATE`'s own `WHERE`. So the second half is a miss rather
than a check, the window between probe and write is closed, and the miss is
reported as `crud.ErrNotFound` like every other id that is not the caller's. A
policy that declares no scope at all still answers that miss with
`Denied(Update, "row is outside the scope")` — a 403, which does not leak
anything here because there is no scope and so no hidden row, but the sentence is
inherited and wrong: with no scope the miss is a lost update race, not a row
outside anything. Fixing the wording is safe; turning the scoped branch back into
a 403 is not.

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
- Do not fold `Save`'s refusal into silence. A 404 is the answer; `err == nil`
  with the row rewritten is the bug this exists to stop.
- Do not drop `saveTarget`'s unscoped probe, or let an unanswerable probe fall
  through to the insert. Without it an assigned key that names somebody else's
  row is an upsert that re-tenants it.

## Where it lives

- `crud/decorators/security/security.go:gate.loadScoped` — the scoped fetch and
  the `ErrNotFound`.
- `crud/decorators/security/security.go:gate.GetByID` and
  `crud/decorators/security/security.go:gate.Update` — both route through it.
- `crud/decorators/security/security.go:gate.saveTarget` — the unscoped probe,
  and the `ErrNotFound` it answers with.
- `crud/decorators/security/security.go:gate.saveScoped` and
  `crud/decorators/security/security.go:gate.saveScopedOnly` — the same answer for
  a row that left the scope between the probe and the write.
- `crud/sqlrepo/repository.go:saveScopedUpdate` — where the gate's scope reaches
  the statement, via `crud/executor.go:ScopedSaver`.
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
- `TestSaveOfAnotherTenantsAssignedKeyLooksMissing` in
  `crud/decorators/security/gate_edge_test.go` — `Save` over a row the scope
  hides, which asserts both halves: not-found comes back, and forbidden does
  not, because forbidden would confirm the row exists.
- `TestScopedSaveKeepsAConcurrentCreateCreateOnly` in
  `crud/decorators/security/security_test.go` — the control for the line above:
  an id no visible row holds still takes the create branch, so the not-found is
  a refusal to overwrite rather than a refusal to write.
- `TestSaveRefusesToOverwriteATombstoneHiddenByRepositoryScope` in
  `crud/decorators/security/gate_edge_test.go` and
  `TestASaveCarryingAHiddenRowsKeyIsRefusedInsteadOfPassingAsACreate` in
  `crud/decorators/security/tombstone_test.go` — the same answer when what hides
  the row is a soft delete rather than a tenant.
- `TestScopedSavePinsAnUpdateToItsInspectedSnapshot` in
  `crud/decorators/security/security_test.go` — the scoped upsert's own miss,
  which is the window between the probe and the write.
- `TestARowHiddenFromReadsIsStillDeletableByID` in
  `crud/http/crudfiber/write_edge_test.go` — pins the documented `WithScope`
  asymmetry so nobody mistakes it for protection.
- `TestScopeReachesDeleteByID` in `crud/sqlrepo/paging_edge_test.go`.
- `TestRepositoryErrorsBecomeStatusCodes` in `crud/http/crudfiber/edge_test.go`.

## See also

[[D-004]] [[D-007]] [[D-011]] [[D-015]] [[D-026]]
