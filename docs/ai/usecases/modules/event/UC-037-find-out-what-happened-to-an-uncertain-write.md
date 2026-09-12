# UC-037 — Find out what happened to a write nobody confirmed

**Actor:** the application author whose command writes history, and whose client
retries
**Covered by:** [[FL-043]]

## Scenario

The command was issued. The connection died. The caller is holding nothing but
the request identity it minted before it started, and it has to decide whether to
try again.

The version of the stream does not answer the question. If it moved, somebody
moved it — possibly this operation, possibly another one, and the caller cannot
tell which from a number. If it did not move, that is either "nothing was
written" or "it was written and the acknowledgement was lost", and those are the
same reading. So the author does the thing that looks safe and reloads, decides
again and appends: the second attempt is at a **different** expected version, the
store admits it because it is not a concurrency conflict, and the ledger now
contains the same decision twice with no error anywhere.

The author who reaches for an idempotency key next meets the part that is not
obvious. Recording the key **after** the append is too late — that is exactly the
two-retries-at-two-versions case, and the collision is discovered with both sets
of events already in the log. Recording it in a second database is not a
recording at all, because the crash that loses the acknowledgement is also the
crash that lands one of the two writes. And the third answer — "no row, therefore
it did not happen" — is false in the one moment it is asked: the original
transaction may simply still be open.

What the author wants is one durable, readable fact about the operation, written
in the same breath as the events, and an honest answer later — including the
answer *"I cannot tell you yet"*.

## What must hold

1. **The record is the application's own table, in the database the events are
   in, behind an interface this framework declares and does not implement.** No
   store gains a table, a column, a migration or a schema version for it, and a
   deployment that does not want it deploys nothing. The framework declares the
   row's shape and reads it back; the application owns the statements and the
   sweep.
2. **The record and the events are one commit, and that is checked rather than
   documented.** Unless the store has transactions, unless one is bound to the
   caller's context, and unless the record's table resolves that same context to
   that same transaction, the operation is refused before any statement runs. A
   record that is not atomic with its append is a row that outlives a rollback and
   reports an operation that never happened.
3. **The order is claim, decide, append, complete, and the claim is first.** The
   framework publishes the call that makes that order unwritable any other way,
   and the open-coded form exists for the caller that must answer a repeat
   differently rather than as the shape a reader copies.
4. **What identifies an operation is a key the caller minted**, carried with
   every retry of it. The framework derives none: a key derived from the payload
   makes two genuinely different commands one operation — two identical credits
   are two operations and both must land — and a key derived from a request
   identity is the caller's to supply. A key never renders its own value.
5. **What makes two attempts the same operation is the bytes they would write.**
   The comparison is a fingerprint over the stream and the encoded records, and
   **not** over the version the decision was made at: a retry in a new process
   loads what the store now holds, which is the version the first attempt moved
   the stream to, so a fingerprint over the version could not be reproduced by the
   one caller the whole mechanism exists for. The version the operation was
   decided at is **recorded** on the row, for an operator, and compared by nobody.
6. **Three answers at the write door, and two of them are refusals.** A key
   nobody has used is **recorded** and the work runs. The same key with the same
   content is a **repeat**, and the caller answers its client from the range the
   first attempt wrote. The same key with **different** content is a
   **collision**, and it is an error rather than a value, because the worst code
   that compiles — take the answer, ignore it, append — must refuse rather than
   spend somebody else's key.
7. **A repeat is answered only from a completed row.** A row whose claim
   committed without its completion is a defect report, not a state to retry
   through: whether that operation's events reached the log is not a question the
   row answers, and a caller told *"already done"* out of it has reported a
   success that may never have happened.
8. **Four answers at the read door, and the third one is the point. An absent
   record is not a rollback.** A resolver on a second connection cannot tell a
   writing transaction that rolled back from one that is still open, and no lock
   closes that gap. That state is **named** rather than guessed at; the caller
   reports the operation as in flight and asks again once the deployment's own
   idle-transaction timeout has elapsed. It does **not** re-issue the command,
   because a fresh load and a fresh decision at the new version is a different
   operation under the same key.
9. **Retention bounds the window, and a swept row never reads as one that never
   existed.** The record's table publishes an instant at or before the oldest row
   it still answers for, and an absent record whose key predates that instant is
   *older than this table answers for* rather than *unresolved*. Answering an
   instant older than the truth costs a caller one more inconclusive answer;
   answering a newer one is wrong, and is the failure that makes a sweep look like
   a rollback.
10. **Every instant the framework compares comes from the database the records
    are in**, except the caller's own record of when it minted its key, which
    cannot come from anywhere else and is **optional**. Requiring it would send
    the caller that does not have it to the current time, which is after every
    horizon and disables the expiry answer for ever with no error anywhere.
11. **One key covers one append, to one stream.** A completion carrying another
    aggregate's append is refused; a command that writes to two aggregates is two
    operations and needs two keys.
12. **A completion cannot undo what the claim proved.** Completing a repeat,
    completing twice, completing outside the claim's transaction, completing on a
    different transaction and completing with another stream's append are all
    refused.
13. **A decision that yields no changes is a completed operation like any
    other**, recorded with an empty range, so its retry is a repeat rather than a
    second decision.
14. **The framework does not replay the domain decision, and the ordinary append
    is not made idempotent.** On a repeat the caller answers from the recorded
    range; nothing re-proposes the same facts at whatever version the store now
    holds. The write door of the store is unchanged, deliberately: putting the key
    into the store contract would cost every store — including ones this framework
    has never seen — a column, a migration and a conformance section for a
    capability a deployment may not want.
15. **The obligation the framework cannot check is stated where a consumer
    reads.** Two attempts are the same operation only if they encode the same
    bytes, so a payload that records a clock, a fresh identifier or a map in
    iteration order encodes differently every time — which under one key is a
    refusal on every retry rather than a duplicate append. Loud, and never wrong.
16. **Nothing here opens a transaction, starts a goroutine, reads a clock or
    writes a line.** The transaction is the caller's throughout, and the
    enforcement of *"do not proceed past a refusal"* is that transaction rolling
    back.
17. **An implementation that answers something no implementation answers is
    refused before its answer is compared or rendered** — the same rule this
    framework already applies to a store's own strings and numbers.

## Out of scope

- **Proving a rollback.** There is no answer that says the operation definitely
  did not happen. The absent-row case is named and bounded and never resolved into
  a negative.
- **Deduplicating the ordinary append.** The store's write door does not learn
  about operation keys.
- **Retrying anything.** A repeat, a collision and an inconclusive answer are
  reported; the recovery, and the unit a retry runs in, are the caller's.
- **Pruning.** The sweep, its schedule and its retention are the application's;
  this framework removes nothing.
- **Replaying the domain decision on the caller's behalf**, at the recorded
  version or at any other.
- **A second durable-intent table.** The record is a durable write inside the
  caller's own transaction, which is this framework's only outbox shape.
- **Deriving the operation key** from the payload, the state, the clock or the
  request.
- **A conformance suite for the application's own table.** The obligations are
  written down and falsified by injected defects against the reference
  implementation; a published harness is owed and is not here.

## See also

[[UC-032]] is the contract the append half of this belongs to, and none of its
guarantees change: the store still admits one writer per version, still refuses
to deduplicate, and still reports an unconfirmed append rather than retrying it.
This is the durable, readable answer to the question that report leaves open.
[[UC-036]] is the other half of the same request path: what to do once the
outcome **is** known and the read model has not caught up.
