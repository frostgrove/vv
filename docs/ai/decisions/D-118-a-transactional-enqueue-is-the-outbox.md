# D-118 — A transactional enqueue is the outbox, and the relay is the application's

**Status:** accepted
**Invariant:** A durable invocation placed while the caller's transaction is
bound to the driver's `crud.Source` is written **inside** that transaction: it
becomes visible when the transaction commits and is gone when it rolls back.
That is this framework's outbox, and no package writes a second durable-intent
table for the same purpose. Delivery to anything that is not a vv worker is
application code behind the application's own port. Delivery to a handler is
**at-least-once and unordered**, and the framework says so rather than leaving a
consumer to assume otherwise.

## The decision

Three statements, and they are one decision because each is only safe with the
other two.

### The outbox already exists; what was missing was the word

`jobspg.Driver.Place` asks `crud.ExecutorFor` whether the caller's context
carries an executor for the driver's own `crud.Source`. When it does, the
placement is staged through `Driver.Stager` into that `*sql.Tx` instead of a
transaction of the driver's own. The invocation row and the rows the caller
wrote commit together or not at all. That is the whole of the outbox pattern:
a durable delivery intent, written atomically with the decision that produced
it, delivered afterwards by something that survives the process that wrote it.

Two things make the claim true rather than approximate, and both are refusals.

**One database.** `New` refuses a `Spec` whose `Source` is not the same data
source as its `DB`. "Atomic" across two handles is a sentence with no meaning,
and the constructor is where that has to be caught — a driver assembled from a
pool and a source that merely look alike would place its rows somewhere else and
say nothing.

**No quiet fallback.** An ambient executor that is *not* a transaction, or one
the `crudsql` adapter cannot produce a `*sql.Tx` from, is
`RejectPlacement(ErrUnsupported)`. It is never placed on autocommit instead. A
placement that silently escaped the caller's transaction is the failure this
whole path exists to prevent, and it would be invisible: the job runs, the row
it was about does not exist, and nothing in either half looks wrong.

The transactional path exists only when the driver was constructed **with** the
application's `crud.Source`. A driver built without one has nothing to detect
and places every invocation in a transaction of its own. That is a legitimate
configuration, not a degraded one, but it is not the outbox and must not be
read as it.

Saying this out loud is the point of the decision. Left unsaid, an application
that needs "this effect happens if and only if this commit happens" builds a
second mechanism — a `pending_effects` table and a poller — to get a property it
already had. Two outboxes over one database is worse than either alone: the
second one has no lease, no fence, no retry budget, no retention and no payload
version, and it is found by the third person to debug an effect that ran twice.

### Delivery to a broker is application code

vv delivers to vv workers. Publishing to a broker is an ordinary job definition
whose handler calls the application's own publisher, and the durability,
leasing, retry, fencing and redrive around it are the ones every other job gets.

No `jobskafka`, `jobsnats` or broker adapter joins this repository for it.
[[D-048]] is the rule — a package with one implementation is an implementation,
not a contract — and the product roadmap already defers broker adapters until
one broker is a real independent consumer choice. A relay handler is a few
dozen lines in the application that chose the broker, and it is the only place
that knows how that broker spells a routing key.

The port that handler calls is the application's own type with the
application's own name. **`Sender` is taken**: `jobs.Sender` is the queue's
backend seam — `Description` and `Place` — and a second meaning for that word
inside one subsystem is a collision a reader resolves wrongly and silently.

### The contract is at-least-once and unordered

- **At-least-once.** `ReasonLeaseLost` and `ReasonShutdown` are retry reasons
  and are deliberately not charged against the attempt budget. A handler that
  completed its effect and lost its lease before recording the outcome runs
  again. This is the design working: a crashed worker is indistinguishable from
  a slow one, and the safe direction is to deliver twice rather than never.
- **Unordered.** A partition is tenancy — `PartitionGlobal` and
  `PartitionTenantRequired` — not a sequence. Priority reorders on purpose, and
  retry backoff reorders further. Nothing here is FIFO and nothing should be
  read as FIFO.
- **Placement deduplication is not delivery deduplication.** `jobs.Unique`,
  `jobs.Collapse` and `EnqueueOnce` collapse two enqueues into one invocation,
  which is a producer-side guarantee. It says nothing about how many times
  that invocation reaches a handler. Conflating the two is how an exactly-once
  claim gets made by accident, and an application that believes it stops
  writing the idempotency it needs.

The consequence belongs in the same breath as the promise: a handler whose
effect must not happen twice carries its own idempotency key, and the natural
one is the `InvocationID` the producer already has — `Place` returns it from
inside the transaction, so the row being written can carry the id of the effect
it will cause.

## What it forbids

- Do not add a second durable-intent table for effects that are already
  expressible as a job. If a job cannot express it, say why in a decision before
  writing the table.
- Do not add a broker adapter package to this repository, and do not name one
  after a layer ([[D-035]]).
- Do not let a placement with an ambient non-transaction fall back to
  autocommit, and do not relax the same-database refusal in `New`.
- Do not describe delivery as exactly-once, and do not describe `jobs.Unique`,
  `jobs.Collapse` or `EnqueueOnce` as making it so.
- Do not promise ordering — not per queue, not per partition, not per
  definition. A sequence belongs in the application's own rows.
- Do not name an application-side publication port `Sender`.
- Do not describe the transactional path as available when the driver has no
  `Source`.

## Where it lives

- `jobs/jobspg/driver.go` — `Place`: the ambient-executor lookup, the two
  refusals and the staged path.
- `jobs/jobspg/stager.go` — `Driver.Stager`, `TxStager.Stage`, and the
  transaction binding a staged placement carries.
- `jobs/jobspg/config.go` — the same-database refusal in `New`.
- `jobs/queue.go` — `Enqueue`, `EnqueueOnce`, `EnqueueIn` and the `Sender` seam
  the name belongs to.
- `jobs/disposition.go` — `retryReason` and `chargedRetryReason`: why a lost
  lease is redelivered and not charged.
- `jobs/scope.go` — `PartitionMode` and `IntentKey`: what a partition is for,
  and what an intent deduplicates.
- `jobs/README.md` — the consumer's half of all three statements.

## Proven by

- `jobs/jobspg/integration_test.go` —
  `TestPostgresAmbientCRUDPlacement`: an enqueue inside `crud.InNewTx` that
  rolls back leaves `ErrInvocationNotFound`, and the committed one is there.
  `TestPostgresVerticalSlice`: the same through an explicit `Stager`, rolled
  back and committed.
- `jobs/jobspg/transaction_test.go` —
  `TestSpecSourceMustNameTheConfiguredDatabase`,
  `TestPlaceRefusesAnAmbientNonTransaction`,
  `TestPlaceRefusesAnUnextractableAmbientCRUDTransaction`.

## See also

[[D-033]] [[D-035]] [[D-048]] [[D-101]] [[D-108]] [[FL-035]] [[UC-031]]
