# FL-035 — A committed decision becomes a delivered effect

**Entry points:** `jobs.Enqueue` / `jobs.EnqueueOnce` (ambient),
`jobs.EnqueueIn` / `jobs.EnqueueOnceIn` (explicit), `jobspg.Driver.Place`,
`jobspg.Driver.Stager`
**Governed by:** [[D-118]] [[D-101]] [[D-108]]

What happens between an application deciding, inside a transaction, that
something must happen elsewhere, and that something running — including the two
places the path refuses rather than guesses, and the shape of a handler that
publishes to a broker.

## The two ways in

`jobs/queue.go`:

1. **Ambient.** `Enqueue(ctx, queue, definition, payload)` builds a `Placement`
   and hands it to `place(queue.sender, ...)`. The caller says nothing about a
   transaction; the context carries it. This is the form an ordinary use case
   writes, and it reads the same whether or not a transaction is open.
2. **Explicit.** `EnqueueIn(ctx, queue, stager, definition, payload)` takes a
   `Stager` the caller obtained from the driver and returns `Staged` rather than
   an id. `validateStager` checks the stager's `TransactionContext` names the
   same backend as the queue before anything is encoded, and `EnqueueIn` checks
   afterwards that the `Staged` it got back carries the transaction it asked
   for — `ErrAmbiguous` if not. Use it where the transaction is a `*sql.Tx` the
   code already holds rather than a context binding.

Both produce an `InvocationID` **before the commit**, so the rows being written
in the same transaction can carry the id of the effect they will cause.

## What `Place` decides

`jobs/jobspg/driver.go:Place`, in order:

1. If the driver has no `source`, there is nothing to detect: the placement goes
   through `place`, which opens a transaction of its own and retries an intent
   or candidate conflict up to three times. **This is not the outbox** — it is
   an ordinary durable enqueue, and it commits whether or not the caller's work
   does.
2. Otherwise `crud.ExecutorFor(ctx, d.source)` looks for an executor bound to
   *this driver's* source. Not found means the caller is outside a transaction,
   and the path above is taken.
3. Found, but `crud.IsTransaction` says no → `RejectPlacement(ErrUnsupported)`.
4. Found and transactional, but `crudsql.Transaction(executor)` cannot produce a
   `*sql.Tx` → `RejectPlacement(ErrUnsupported)`.
5. Otherwise `d.Stager(tx)` then `stager.Stage(ctx, placement)`, and the result
   is `NewPlacementResult(staged.InvocationID(), staged.Outcome())`.

Steps 3 and 4 are the ones that matter. Neither falls back to a transaction of
the driver's own: a placement that escaped the caller's transaction would run a
job about a row that was never committed, and both halves would look correct in
isolation.

## Why the atomicity is real

`jobs/jobspg/config.go:New` refuses a `Spec` where
`spec.Source != nil && !crud.SameDataSource(crud.KeyOf(spec.Source), spec.DB)`.
The invocation row and the caller's rows are in one PostgreSQL database or the
driver does not exist. Two handles that happen to point at the same server are
still two transactions, and "atomic" would be a claim nothing enforces.

`jobs/jobspg/stager.go` builds the binding: `Driver.Stager` mints a
`TransactionBinding` from driver entropy and wraps it in a
`jobs.NewTransactionContext` with the driver's id and durability, so a `Staged`
can be checked against the stager that produced it rather than trusted.

## From a committed row to a running handler

The commit is the publication. Everything after it is ordinary delivery:

1. A worker claims work with a `ClaimRequest` and gets a `LeaseRef`
   (`jobs/delivery_driver.go`). `DefaultLeaseTTL` is a minute and
   `DefaultHeartbeat` fifteen seconds (`jobs/bounds.go`).
2. A worker that dies stops renewing. `RecoverRequest` / `RecoveredDelivery` is
   how another worker takes the invocation over, bounded by
   `ReclaimInterval` and `MaxReclaimBatch`.
3. The revived owner cannot write behind the new one: `AttemptController.Guard`
   takes a `LeaseFence`, and `FencedTransactions.InFencedTx` orders the guard
   before the effect.
4. A lost lease is a retry. `jobs/disposition.go:retryReason` includes
   `ReasonLeaseLost` and `ReasonShutdown`; `chargedRetryReason` does not, so
   neither spends the attempt budget.

Step 4 is where at-least-once comes from, and it is a design property rather
than a gap: a worker that finished its effect and died before recording the
outcome is indistinguishable from one that died before starting.

## Publishing to something that is not a vv worker

There is no broker adapter in this repository and none is planned ([[D-118]]).
A relay is an ordinary job in the application:

1. The application declares a definition — a stable wire name and version, e.g.
   `"integration.publish"` v1 — whose payload carries the routing key and the
   encoded message, not a Go value only this build can decode.
2. Its handler calls the application's own publication port. That port is the
   application's type with the application's name: `jobs.Sender` is the queue's
   backend seam (`jobs/queue.go`), not a broker publisher, and reusing the word
   inside one subsystem is a collision a reader resolves wrongly.
3. The handler passes a deduplication key to the broker.
   `DeliveryMeta.InvocationID()` and `DeliveryMeta.AttemptOrdinal()` are
   separate, and the invocation is the one that is stable across attempts; its
   `String()` is a canonical UUID. A handler only receives `DeliveryMeta` when
   it is bound with `jobsfx.AutoAdapterFor` rather than `jobsfx.AutoFor`.
4. Whatever consumes the message deduplicates on that key. The relay is
   at-least-once like every other handler.

## Where the decisions bite

- [[D-118]] — the transactional placement is the outbox; there is no second
  durable-intent table, no broker package, no ordering promise and no
  exactly-once reading of `EnqueueOnce`.
- [[D-101]] — none of this migrates a schema by itself; a production profile
  verifies and refuses.
- [[D-108]] — whether this process delivers at all is a declared deployment
  role, not something inferred from the graph holding a consumer.
- [[D-061]] — the ambient lookup walks declared wrappers; a `Source` decorator
  without `Next()` hides the executor and the placement quietly stops being
  transactional.

## Traps

- **A driver without a `Source` looks identical at the call site.** The same
  `jobs.Enqueue` line is transactional in one wiring and not in another. It is a
  composition-root property, and it is worth asserting in the application's own
  boot test.
- **`jobs.Unique`, `jobs.Collapse` and `EnqueueOnce` are producer-side.** They
  collapse two enqueues into one invocation. They do not make delivery
  exactly-once, and nothing does.
- **A partition is a tenant, not a sequence.** `PartitionGlobal` and
  `PartitionTenantRequired` are the whole of `PartitionMode`. Priority and retry
  backoff both reorder deliberately.
- **The pgx source cannot carry an ambient placement.** The staged path needs a
  `*sql.Tx` through `crudsql.Transaction`; a `crudpgx` executor is
  `ErrUnsupported`, which is a refusal and not a silent bypass, but it is a
  refusal at run time rather than at construction.

## Files

| File | What it holds |
|---|---|
| `jobs/queue.go` | `Enqueue`, `EnqueueOnce`, `EnqueueIn`, `EnqueueOnceIn`, `Unique`, `Collapse`, `preparePlacement`, `validateStager`, `place`, `stage`, `Sender`, `Stager`, `QueueSpec` |
| `jobs/jobspg/driver.go` | `Place` — the ambient lookup, the two refusals, the staged path, and `place`/`placeInTx` underneath |
| `jobs/jobspg/stager.go` | `Driver.Stager`, `TxStager.Transaction`, `TxStager.Stage` |
| `jobs/jobspg/config.go` | `Spec`, `New` — the same-database refusal, the capability set the backend advertises |
| `jobs/delivery_command.go` | `LeaseRef`, `NewLeaseRef` |
| `jobs/delivery_meta.go` | `LeaseFence`, `AttemptController`, `DeliveryMeta.InvocationID`, `DeliveryMeta.AttemptOrdinal` |
| `jobs/delivery_driver.go` | `ClaimRequest`, `RecoverRequest`, `RecoveredDelivery` |
| `jobs/fenced_transaction.go` | `FencedTransactions.InFencedTx` |
| `jobs/disposition.go` | `retryReason`, `chargedRetryReason`, `ReasonLeaseLost`, `ReasonShutdown` |
| `jobs/scope.go` | `PartitionMode`, `IntentKey`, `IntentPurpose`, `IntentScopeBinding` |
| `jobs/bounds.go` | `DefaultLeaseTTL`, `DefaultHeartbeat`, `DefaultReclaimInterval`, `DefaultReclaimBatch` |
| `crud/executor.go` | `ExecutorFor`, `IsTransaction`, `InTx`, `InNewTx`, `SameDataSource`, `KeyOf` |
| `crud/adapter/crudsql/crudsql.go` | `Transaction`, `TransactionFor` — the `*sql.Tx` the stager needs |

## Tests that walk this flow

`jobs/jobspg/integration_test.go` (`TestPostgresAmbientCRUDPlacement`,
`TestPostgresVerticalSlice`), `jobs/jobspg/transaction_test.go`,
`jobs/jobspg/intent_keys_test.go`,
`jobs/jobspg/intent_keys_integration_test.go`,
`jobs/queue_test.go`, `jobs/disposition_test.go`.
