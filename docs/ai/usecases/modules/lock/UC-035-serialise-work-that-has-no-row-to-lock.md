# UC-035 — Serialise work that has no row to lock

**Actor:** the application author, and the framework's own PostgreSQL drivers
**Covered by:** [[FL-041]]

## Scenario

Something must not happen twice at once, and there is no single row whose lock would say so. The
decision is spread across several tables, or it is about the schema itself, or it is "whoever gets
here first does this sweep and the rest do nothing".

The author names it, says whether the hold is exclusive or shared with other readers of the same
decision, and runs the work. Whether the lock is released, and when, is not their problem: the
database releases it when the transaction ends, including when the process holding it dies.

Sometimes several such names have to be held at once — a deployment-wide decision and one document
— and two different callers may name the same pair.

## What must hold

1. Two callers naming the same thing against the same database never run their work at the same
   time, whatever process, pool or connection each is on.
2. Two callers naming the same thing against **different** databases do not exclude each other. The
   name alone is not the identity of a critical section; the database it is taken in is half of it.
3. A hold declared shared does not exclude another shared hold on the same name, and does exclude
   an exclusive one. A deployment with many concurrent readers of one decision does not serialise
   them.
4. Holding several names at once cannot deadlock against another caller holding the same set,
   however each of them wrote the list.
5. A hold is released when the work ends, whether it ended by returning, by failing, or by the
   process dying. No caller has to release anything, and no expiry can strand a hold or free one
   early while its holder is still working.
6. A failure the engine says will pass on its own — a deadlock, a serialisation failure, a wait
   that timed out, a transaction the engine gave up on — is retried when the framework owns the
   transaction, and reported when it does not. It never reaches a client as though the client had
   sent something wrong.
7. An engine that cannot provide the above is refused, in full, at the moment the caller sets the
   mechanism up, and before any statement runs against it. It is never served a hold that is weaker
   than the one asked for, and never a hold that silently is not one.
8. Asking for a hold outside a transaction is refused rather than granted, because such a hold
   would be released before the caller could use it.
9. The number a name resolves to does not change. Two deployments of different ages agree about
   what a given name means.

## Out of scope

- A lease with an expiry, a fencing token, or coordination of anything that is not in the database
  the lock is taken in. That is a different contract with different failure modes, and the
  framework's job leases and cache primitives are where it lives.
- Deciding *what* deserves a critical section. This says only that one can be had.
- Any engine but PostgreSQL, until one is implemented rather than approximated.
