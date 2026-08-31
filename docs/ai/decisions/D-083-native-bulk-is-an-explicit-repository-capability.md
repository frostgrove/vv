# D-083 — Native bulk is an explicit repository capability

**Status:** accepted
**Invariant:** The safe batch entry point is typed `Repo.InsertBatch`; model
metadata, repository policy, fault handling and the source-bound executor are
resolved before any native protocol runs. Native bulk is the magic default when
the exact repository Source exposes it, that Source remains the effect authority
across transaction binding, portable SQL is an explicit opt-out, and every
direct table/column/row escape hatch is named `Unsafe`.

## The decision

`Repo.InsertBatch(ctx, models, options...)` is an insert-only write. It derives
the structured table reference, writable columns and values from immutable
`Meta`; omits an unassigned auto key and every generated or server-owned column;
does not mutate its commands; and never turns an assigned primary key into an
upsert. An empty batch is a universal no-op. Generated-key values are not read
back, so callers needing stored rows use `Save`.

The verb is the optional `crud.BatchInserter[M]` capability rather than a new
`Core` method. Existing consumer Core implementations therefore keep compiling,
but optional does not mean bypassable: `InsertBatchOf` checks only the exact
outer Core and never walks `Nexter`. An unknown decorator returns
`ErrNoBatchInsertSupport` before I/O. The security Gate and faults decorator
implement the verb explicitly. Gate treats every row, including one with an
assigned key, as `Create`, works on private copies and inspects the full batch
before storage. Faults carries `Op=InsertBatch`, the entity and model paths, and
its optional probe receives insert rather than upsert semantics.

`sqlrepo` chooses storage after those layers:

1. resolve and validate the executor scoped to the repository source from `ctx`;
2. preflight the complete batch shape and every model value;
3. invoke `crud.UnsafeBulkInserter` only when the exact repository Source exposes
   it, passing the resolved executor as the effect target;
4. otherwise render bind-budgeted multi-row INSERT chunks and execute multiple
   chunks in one transaction.

The pgx adapter implements native bulk with COPY. COPY is the default because
ordinary imports should receive framework speed without manual type assertions
or duplicated table/column declarations. It is not hidden inside `SaveAll`,
because COPY and INSERT are not semantically interchangeable on every
PostgreSQL table: COPY is refused for row-level security, rewrite rules differ,
and pgx's binary encoding may differ from its parameter path. A call selects
ordinary SQL with `crud.PortableBatch()`; a declaration selects it for every
call with `sqlrepo.PortableBatch()`. The opaque option is monotonic and is
resolved exactly once by sqlrepo after decorators forward it.

A native implementation is atomic on its resolved target executor. Its returned count is
the database count and may be smaller than the input when a trigger skips rows;
the high-level write intentionally exposes no count. `ErrNoBulkInsertSupport`
is reserved for a before-I/O capability refusal. Only that error may select the
portable fallback. A driver/server error is final because COPY may already have
aborted the transaction; retrying as INSERT could duplicate effects.

Native effect discovery does not follow `SourceUnwrapper`. Doing so could skip
the tracing, rate limit, circuit breaker or transaction-local setup owned by the
wrapper. An explicitly transparent wrapper implements `UnsafeBulkInsert`
itself and passes the supplied target to the inner capability; otherwise sqlrepo
selects portable INSERT. That exact outer decision survives `Source → Tx`, so
opening a repository transaction cannot reveal a capability hidden by the
wrapper or bypass one that refuses the effect. A one-statement plan goes
through the wrapper's ordinary `Exec`. When bind-budget chunking opens a
transaction, the chunks execute on the returned transaction handle; observing
every statement requires a transaction-aware wrapper or driver-level
instrumentation ([[D-062]]). `ReadWrite` explicitly forwards the effect to its
primary. This narrows [[D-061]]: discovery may walk, storage effects require
exact preservation.

A bound executor is joined for a chunked portable plan only when it is known to
be transactional. A non-transaction session executor cannot make several
statements atomic, so `InAtomic` opens a transaction from the repository source
instead. Connection-local state therefore requires either a repository bound to
that session source or a transaction begun and bound before `InsertBatch`.

Default-only models remain portable. PostgreSQL, SQLite and external dialects
use SQL-standard `DEFAULT VALUES`; MySQL/MariaDB use `() VALUES ()` through the
optional `DefaultValuesInserter`. Each default-only row is one statement and a
multi-row call is atomic.

## The low-level boundary

Applications that deliberately work below repositories have explicit names:

- `UnsafeExecFor` and `UnsafeQueryFor` select the source-bound executor before
  running raw SQL;
- `UnsafeBulkInsertFor` validates the same source-bound resolution, then passes
  its resolved executor to the exact Source's native effect;
- `crudpgx.Executor.UnsafeCopyFrom` and `UnsafeCopyFromTable` run on the
  receiver's exact handle.

The `For` helpers prevent a raw call from silently escaping an ambient
transaction, but `Unsafe` still means the call bypasses repository metadata,
Gate, lifecycle and decorator hooks. The exact pgx methods are for callers that
already hold the intended pool/connection/transaction handle. Before the first
release, the ambiguous `BulkInserter`, `CopyFrom` and `CopyFromTable` surface was
removed rather than deprecated: safe migration is `Repo.InsertBatch`; deliberate
bypass migration is one of the names above.

## What it forbids

- Do not hand-write a table or column list for ordinary application imports.
- Do not make native COPY an invisible optimisation of `SaveAll`.
- Do not upsert an assigned key through `InsertBatch`.
- Do not walk through an unknown Core or Source wrapper to execute a storage
  effect.
- Do not retry portable SQL after a native driver/server failure.
- Do not return `ErrNoBulkInsertSupport` after any row was consumed or written.
- Do not call an exact-handle `UnsafeCopyFrom*` method when the intended session
  lives only in `ctx`; use the repository or `UnsafeBulkInsertFor`.
- Do not route arbitrary raw queries to a replica: `UnsafeQueryFor` stays on the
  writable source because raw SQL cannot be proved read-only.

## Where it lives

- `crud/batch.go` — the typed facade capability and closed storage option.
- `crud/executor.go` — exact native effect discovery and context-bound unsafe
  helpers.
- `crud/sqlrepo/repository.go:InsertBatch` — metadata derivation, native choice
  and portable atomic plan.
- `crud/sqlrepo/blueprint.go:PortableBatch` — declarative opt-out.
- `crud/decorators/security/security.go:InsertBatch` — policy obligation.
- `crud/decorators/faults/faults.go:InsertBatch` and `probe.go` — fault/probe
  semantics.
- `crud/adapter/crudpgx/crudpgx.go` — COPY and exact-handle unsafe methods.
- `crud/dialect.go:DefaultValuesInserter` — default-only syntax.

## Proven by

- `crud/sqlrepo/insert_batch_test.go` — qualified metadata, server-owned and
  generated fields, three-state values, assigned-key create-only semantics,
  whole-batch preflight, native selection, portable fallback and declarations,
  ambient/owned transactions, wrappers that hide, refuse or forward native bulk
  across `Source → Tx`, direct-statement wrapper observability, primary routing,
  the before-I/O sentinel, no retry after driver failure, Gate, decorator order
  and default-only syntax.
- `crud/batch_test.go` — unknown repository decorators fail closed, empty
  batches are universal no-ops and portable options are monotonic.
- `crud/executor_effect_test.go` — raw helpers select only a matching session,
  preserve poisoned declarations and reject nil sources.
- `crud/adapter/crudpgx/copy_test.go` — exact structured identifiers, dotted
  string refusal, empty no-op, capability error, conflict classification and
  exact-handle versus context-resolved behaviour.
- `TestPgxInsertBatchSelectsCopyFromTheRepository`,
  `TestPgxInsertBatchJoinsRepositoryTransaction`,
  `TestPgxPortableBatchIsTheExplicitRLSPath` and
  `TestQualifiedRepositoryAndPgxCopyUseTheSameStructuredTable` in
  `test/integration/driver_pgx_test.go` — live COPY selection, rollback,
  qualified tables and fault enrichment.

## See also

[[D-019]] [[D-030]] [[D-061]] [[D-062]] [[D-079]] [[D-080]] [[D-082]]
