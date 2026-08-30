# D-077 — Rollback outlives the request, with a bound

**Status:** accepted
**Invariant:** a transaction opened by `crud.InTx` is asked to roll back with a
context that does not inherit request cancellation and has a finite deadline.
The framework does not promise that rollback finishes within that deadline: it
can constrain only adapters and drivers that honour context, while APIs without
contextual rollback keep their native semantics.

## The decision

`crud.InTx` and `crud.InNewTx` roll back with a context derived through
`context.WithoutCancel` and a five-second timeout. The cleanup context keeps the
caller's values, drops its cancellation and deadline, and adds the framework's
own cleanup deadline. Both the returned-error path and the panic path use that
same helper.

This is a guarantee about the context passed across the `crud.Tx` seam, not a
claim that every driver can interrupt rollback at five seconds. `crudpgx` passes
the context to pgx and is context-aware. `database/sql.Tx.Rollback` accepts no
context, so `crudsql` necessarily ignores the argument and retains
`database/sql`'s rollback semantics.

Commit still uses the caller's context. This decision only protects cleanup
after the operation has already failed; it does not let a canceled request
commit work that its caller abandoned.

## Why

A request is commonly canceled because its deadline fired or its client went
away. Passing that already-canceled context to `Rollback` lets a context-aware
driver refuse cleanup immediately. The connection can then remain occupied by
an open transaction until driver or server timeout.

`context.Background` would discard values a context-aware adapter may need and
would carry no deadline. `WithoutCancel` plus a new deadline preserves the
useful half of the context and gives adapters that can honour cancellation a
finite cleanup budget.

## What it forbids

- Do not pass the operation context directly to rollback.
- Do not use an unbounded background context for transaction cleanup.
- Do not move commit onto the cleanup context; cancellation must still prevent
  a normal completion from being reported as committed.

## Where it lives

| File | What it holds |
|---|---|
| `crud/executor.go` | `rollback`, used by both failure exits from `inNewTx` |

## Proven by

- `TestRollbackOutlivesTheCanceledRequestButRemainsBounded` cancels the request
  inside the transaction, then proves rollback received a live context with a
  finite deadline.
- `TestRollbackKeepsTheOperationAndCleanupErrorsInspectable` proves the returned
  error matches both the operation and rollback failures with `errors.Is`.
- `TestPanicRollsBackWithADetachedBoundedContextAndRepanics` covers the panic
  exit and preservation of the original panic.
- `TestRollbackPreservesRequestContextValues` proves detaching cancellation does
  not discard caller values.

All four live in `crud/executor_test.go`.

## See also

[[D-009]] [[FL-009]]
