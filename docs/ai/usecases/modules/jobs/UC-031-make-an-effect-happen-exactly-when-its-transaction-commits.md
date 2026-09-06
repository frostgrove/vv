# UC-031 — Make an effect happen when, and only when, its transaction commits

**Actor:** the application author whose use case decides something in a
transaction and owes something outside it
**Covered by:** [[FL-035]]

## Scenario

A request creates rows and also owes something that is not a row: another
service has to be told, a message has to reach a broker, a document has to be
handed to a converter. The author has two bad places to put it.

Inside the transaction, the effect happens for work that later rolls back — a
duplicate key, a failed check, a caller who gave up — and the transaction holds
a pooled connection and its locks for as long as somebody else's network is
slow. After the commit, the process can die in the gap between the two, and
nothing anywhere records that the effect was owed; the rows say the work
happened and no one is left to notice it did not.

The author wants the *obligation* to be part of the same commit as the decision,
and the *delivery* to be somebody else's problem — retried, leased, and still
there after the pod restarts. They also want the promise stated precisely enough
to build on, because an effect that runs twice and an effect that never runs are
different bugs and both are expensive.

## What must hold

1. An enqueue made while the caller's transaction is open is part of that
   transaction. If it commits, the work will be delivered; if it rolls back, the
   work never existed. No poller, no second table and no extra call at the
   commit point.
2. That holds in both shapes: with the transaction carried in the context, where
   the enqueue reads the same as one outside a transaction, and with the
   transaction handle passed explicitly.
3. The identity of the pending work is known **before** the commit, so a row
   written in the same transaction can name the effect it will cause.
4. Atomicity is refused rather than approximated. If the work cannot be written
   inside the caller's open transaction, the enqueue fails; it is never quietly
   written outside it instead. The failure is a refusal the caller can tell from
   every other failure, and nothing was written.
5. The obligation and the decision live in one database, and a configuration
   that would put them in two is refused when the program is assembled, not when
   a request arrives.
6. Whether the transactional path exists at all is a property of the wiring, not
   of the call site: a queue assembled without the application's data source
   performs an ordinary durable enqueue that commits on its own. The author can
   assert which one their program built.
7. Delivery outlives the process that enqueued it. Work claimed by a replica
   that dies is taken over by another; the revived original cannot write behind
   the new owner; neither takeover nor a shutdown spends the work's retry
   budget.
8. **Delivery is at-least-once.** A handler that finished its effect and lost
   its lease before recording that it had runs again. An author who cannot
   tolerate that makes the effect idempotent, and the identity from point 3 is
   the key to do it with.
9. **Delivery is unordered.** Two pieces of work enqueued in one transaction may
   run in either order, concurrently, or minutes apart. Priorities and retry
   backoff reorder deliberately. An author who needs a sequence keeps it in
   their own rows.
10. Collapsing two enqueues into one piece of work is available and is a
    *producer-side* guarantee. It does not reduce how many times that work
    reaches a handler, and nothing does.
11. Delivering to something that is not a worker of this framework — a broker, a
    third-party API — is the author's handler calling the author's own port. The
    framework carries the payload, the retries and the lease; it never learns
    what the broker is, and no adapter for one ships here.

## Out of scope

- Exactly-once delivery. Point 8 is the contract; there is no mode that changes
  it.
- Ordering of any kind — per queue, per definition or per tenant partition.
- Undoing an effect that already happened. Compensation, sagas and process
  managers are the application's; the framework rolls forward.
- Atomicity across two databases, or across a database and an object store. The
  refusal in point 5 is the whole of the answer.
- A broker adapter, a broker-specific payload shape or a publish port named by
  this framework.
- Deciding whether this deployment delivers at all. That is a declared role and
  belongs to [[FL-028]].
