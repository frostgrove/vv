# D-079 — Bind budgets are statement-wide, and write chunks are atomic

**Status:** accepted
**Invariant:** no statement built through `crud.SQL` reaches a datasource with
more bound values than its dialect declares; when one logical repository write
needs several statements, all of them join one transaction or none of them run.

## The decision

The parameter ceiling belongs to the dialect. `crud.BindBudget` is an optional
capability with `MaxBindValues`; `crud.BindLimit` reads it. PostgreSQL and MySQL
declare 65,535. SQLite declares 999, the conservative value accepted across the
supported builds. A dialect written outside this module keeps compiling and
receives that same 999-value default. A non-positive declaration narrows to the
default rather than disabling the guard.

The count covers the complete statement. `SQL.Err` / `SQL.Done` compare the
final argument list with the limit after predicates, relation scopes, update
values and every other fragment have contributed. The refusal is a typed
`*crud.SchemaError` naming both counts and an alternative, and it happens before
`Exec` or `Query`.

An `IN` predicate cannot be repaired by rendering several `IN` clauses in the
same statement: the number of parameters does not change. A direct Go predicate
that is too large is therefore refused. A caller can reduce the set or choose a
temporary table or driver-specific bulk path explicitly.

`SaveAll` and `Delete(ids...)` can change statement boundaries without changing
their logical operation, so they chunk instead:

- the boundary is deterministic and preserves input order;
- `SaveAll` divides the budget by the selected insert column count, retaining
  the generated-key versus assigned-key fork for every chunk and never mutating
  a model;
- `Delete` first charges the permanent root and relation scopes, plus the
  soft-delete timestamp when present, then uses the remainder for ids; every
  chunk carries the same scopes and one captured timestamp;
- every model is read and every statement is rendered before the first
  statement executes;
- one chunk executes as one statement with no extra transaction; several chunks
  join an executor already bound to the datasource or open one transaction;
- a source that cannot provide either atomic boundary returns
  `crud.ErrNoTxSupport` before executing a chunk;
- empty inputs remain complete no-ops.

Joining an ambient transaction keeps [[D-009]]'s ownership rule: the outer
owner decides commit or rollback. When vv opens the transaction, a chunk error
rolls the whole operation back through [[FL-009]].

## Why

All three supported engines have a hard statement-parameter ceiling, and the
driver's answer arrives too late and in the wrong vocabulary. It may be an
opaque 500 after the application has already allocated and sent a statement.
Counting only an `IN` list is also wrong: a repository scope and a relation
scope share the same argument vector, as do a soft-delete value and its ids.

Executing independent chunks fixes the ceiling and creates a worse bug: the
first half of a batch survives when the second fails. Rendering chunks lazily
inside the transaction avoids that persistence but still begins a transaction
for a declaration error that could have been known before it. A complete plan
before execution gives the operation one failure boundary and keeps client
errors out of the transaction path.

The conservative fallback is deliberate. `Dialect` is implemented outside this
repository, so adding a required method would be a source break forbidden by
[[D-019]]. Assuming an unknown engine accepts PostgreSQL's maximum would instead
turn compatibility into a runtime failure.

## What it forbids

- Do not add a required bind-limit method to `Dialect`; use the optional
  capability.
- Do not branch on a dialect name to choose a limit.
- Do not count only the obvious list or VALUES rows. The final statement count
  is authoritative.
- Do not split a large predicate into several clauses in the same statement and
  call it chunking.
- Do not execute write chunks independently or start a nested transaction when
  a caller already supplied one.
- Do not build later chunks after earlier ones have executed.
- Do not drop or weaken a permanent or relation scope at a chunk boundary.

## Where it lives

- `crud/dialect.go:BindBudget`, `BindLimit`, `PortableBindLimit` and the three
  built-in dialect declarations.
- `crud/render.go:SQL.Err` / `SQL.Done` — the statement-wide preflight.
- `crud/sqlrepo/repository.go:batchInsertPlan`, `deletePlan`, `executePrepared` —
  deterministic plans and their atomic execution.
- `crud/sqlrepo/repository.go:SaveAll` / `InsertBatch` / `Delete` — the public
  chunked write paths.

## Proven by

- `TestBindLimitsAreDialectOwnedAndExternalDialectsStayPortable` in
  `crud/dialect_test.go`.
- `TestStatementBindBudgetIsCheckedBeforeExecution` in `crud/render_test.go`.
- `TestDirectInOverTheDialectBudgetFailsBeforeTheDatabase`,
  `TestSaveAllChunksAtTheDialectBudgetAndKeepsInputOrder`,
  `TestGeneratedKeySaveAllKeepsItsWriteOnlySemanticsAcrossChunks`,
  `TestSaveAllRollsEveryChunkBackWhenALaterChunkFails`,
  `TestChunkedSaveAllJoinsAnAmbientTransaction`,
  `TestChunkedWriteWithoutTransactionSupportRunsNoStatement`,
  `TestChunkedDeleteWithoutTransactionSupportRunsNoStatement`,
  `TestDeleteChunksAfterChargingScopeAndSoftDeleteBinds`,
  `TestDeleteRollsEveryChunkBackWhenALaterChunkFails`, and
  `TestDeleteRefusesWhenScopesExhaustTheBudgetBeforeOpeningATransaction` in
  `crud/sqlrepo/bind_budget_test.go`.
- `TestSaveAllChunksRollBackAsOneWriteAgainstEveryEngine` and
  `TestDeleteChunksRemoveTheWholeIDSetAgainstEveryEngine` in
  `test/integration/saveall_test.go` exercise the transaction and chunk
  boundaries against each live adapter.

## See also

[[D-009]] [[D-014]] [[D-019]] [[FL-003]] [[FL-005]] [[FL-008]] [[FL-009]]
[[UC-005]] [[UC-006]]
