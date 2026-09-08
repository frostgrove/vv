# UC-032 — Record what happened, and rebuild state from it

**Actor:** the application author whose domain has a history, not just a current
row
**Covered by:** [[FL-036]]

## Scenario

Some things an application owns are not a row that gets overwritten. A ledger, a
subscription, an order, a case file: what the current balance is matters less
than how it got there, and the auditor, the support agent and the next feature
all want the sequence rather than the summary. The author who keeps only the
latest row discovers this after the fact, when the question is asked about a
month that has already been overwritten.

The author who reaches for event sourcing to fix it then meets the second
problem, which is that the pattern has a dozen sharp edges and every one of them
is silent. Two writers decide from the same starting point and one of them
overwrites the other's decision with no error anywhere. A payload type is
changed and every fact written before the change reads back as a zero value with
no refusal at any door. An identity is rendered two ways and one aggregate turns
into two histories folding each other's facts. A commit is issued, the network
drops, and the caller cannot tell "not written" from "written and unconfirmed" —
so it retries and writes the decision twice.

What the author wants is the discipline without the archaeology: declare the
facts, decide from a state that was rebuilt from the whole history, and have the
framework refuse — loudly, at the earliest possible moment, and in a class they
can branch on — every one of those silent failures. They also want their tests
to run without a database, and their production store to be swappable without
rewriting the domain.

## What must hold

1. An aggregate is declared once, in the application's own code: a family name,
   a mapping from its identity to a stream key, and one entry per kind of fact
   with the fold that applies it. The declaration is a value the application
   owns; the framework holds no registry of them and nothing is registered by
   the act of importing a package.
2. **One family names one aggregate.** Two declarations of one family share a
   history, so wherever the framework can see both parties it refuses them:
   binding the second to a store, and applying a decision made on one of them
   through the other. Without a registry it cannot see every pair — two
   composition roots in one program are its own scope — so the obligation is
   stated, and a runnable check over the author's own declarations reports the
   collision the framework structurally cannot.
3. A declaration that cannot work is refused **before the program serves
   anything** — a missing fold, a duplicate wire name, a payload type whose
   bytes no reader could read back, an identity that renders an illegal key. The
   refusal is a panic at declaration, and there is an error-returning sibling for
   the tests that assert the refusal.
4. A fact's payload may change shape over time. Each shape is one retained
   revision with its own reader, and every earlier revision is carried forward to
   the current one by a conversion the author wrote. The revision is the position
   in that chain and is never a number the author types, so it cannot be
   duplicated or left with a gap.
5. What a payload round-trips through is checkable by the author, over their own
   samples, without a store: a reader that silently drops part of what it was
   given, one that cannot read back its own output, and one that hands back
   memory it will overwrite are all found before a history contains one. **A
   fact whose whole content is that it happened is checkable too** — the shape
   that carries no data has one sample and the check runs the half it can, rather
   than refusing the only sample there is. **A payload carrying a value the
   author does not own the type of is checkable too**, because the check falls
   back to a question about the recorded bytes, which needs nothing declared on
   the type. And **what the check could not establish it reports**: a sample
   larger than it compares, a comparison the payload's own type broke, or a
   payload holding something no format records at all, is named as such and never
   counted as a pass.
6. A load rebuilds the state from the whole history, in order, every time. There
   is no cache, no snapshot and no partial rehydration: a load that fails
   anywhere returns nothing, never a state built from the events that happened to
   arrive first.
7. Deciding produces facts, not a mutated object. Applying them to a state is
   available with no store at all, so a domain rule is unit-testable as a pure
   function.
8. **An append is admitted only at the version the decision was made at.** Two
   writers who loaded the same state cannot both win: exactly one is admitted and
   the other is told the stream moved, in a class of its own. Recovery is a fresh
   load and a fresh decision — never re-proposing the same facts at whatever
   version the store reports, and the refusal deliberately does not report that
   version.
9. "The append failed" and "the append may have landed" are different answers and
   the author can tell them apart. An outcome nobody confirmed is its own class,
   and treating it as "nothing was written" is exactly the inference that class
   exists to refuse.
10. History is append-only. Nothing in the surface rewrites, deletes, filters,
    sorts or searches a recorded fact, and there is no query language over events.
    The two reads are one aggregate's stream in order, and the whole log in the
    order it was committed.
11. A refusal names the rule that was broken and the class it belongs to, never
    the data that broke it. No key, payload, version, position, cursor or
    identity reaches an error message, a log line or a transport response.
12. Every class is one a caller can branch on, and the classes do not overlap: a
    malformed declaration, a mis-assembled program, unusable request data, a fact
    this build cannot read, an append that did not do what was asked, and a store
    that failed are six different answers. A cancelled request stays a cancelled
    request and is never reported as a store failure.
13. Every bound is the store's number, declared by the store and enforced by the
    framework — the largest payload, the largest batch, the longest key, the page
    a read comes back in. A store that states no bound, or one past what the
    framework will hold in memory at once, is refused when it is bound rather
    than when a request arrives.
14. The transaction is the caller's, from the first statement to the last. The
    framework opens none, commits none and rolls none back, and where the store
    supports it an append made inside the caller's transaction disappears with a
    rollback. Two subsystems writing in one transaction can prove that they did.
    Reloading one aggregate inside that transaction sees what it has appended;
    **walking the whole log inside it is the store's business and is promised to
    nobody** — one store answers its own uncommitted events and another answers
    none — so a walk taken inside a write transaction has no resume point worth
    persisting, and an event's place in the log means nothing until the append
    carrying it has committed.
15. Anything the author hands in and anything handed back belongs to whoever
    holds it. Mutating a payload after deciding cannot change what was recorded,
    and a page handed to the author is theirs to write into.
16. Running against a real store and running against an in-memory one are the
    same program. The in-memory one is a complete implementation rather than a
    stand-in: it admits at the expected version, keeps dense versions, and
    supports transactions.
17. A store somebody else writes is held to the same contract by a suite that
    ships with the framework and that the store runs against itself. A capability
    the store does not claim is reported as not certified rather than as a pass,
    and the suite is itself checked against deliberately broken stores so that a
    section which stopped proving anything is a failure rather than a green line.
18. **Reading the whole log tiles it: one uninterrupted pass over a quiescent
    store returns every committed fact once, in commit order.** That is a
    statement about a walk and never about delivery. Anything the author builds
    on top of it — a projection, a publisher — is at least once, and consumers of
    it are idempotent.
19. A checkpoint taken from a walk is safe to persist and to resume from in
    another process. One taken from a different store, or in a format the store
    no longer reads, is refused rather than silently restarting from the
    beginning of the log.
20. None of this costs the author a third-party dependency, and none of it is
    compiled by an application that does not use it.

## Out of scope

- Exactly-once delivery of anything built on the log. Point 18 is the whole of
  the answer, and there is no mode that changes it.
- Snapshots, projections, projectors, subscriptions and read models. The log and
  the walk over it are what is provided; what is built on them is the
  application's.
- A query language over history, an ordinary list-and-filter endpoint over
  events, or anything that makes a fact history behave like a collection.
- Deciding across two aggregates atomically without a transaction the caller
  owns. Where the store has no transactions, one append is one aggregate.
- Retrying anything. A conflict, an unconfirmed append and a backend failure are
  reported; the recovery is the caller's, and the unit of a retry is the
  caller's transaction.
- Deleting or rewriting a recorded fact, including for erasure obligations.
  Crypto-shredding and retention are the application's design.
- A durable store. The one that ships keeps nothing across a process, and says
  so.
