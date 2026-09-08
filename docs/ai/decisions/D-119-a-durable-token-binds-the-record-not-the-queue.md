# D-119 — A durable token binds the record, not the queue

**Status:** accepted
**Invariant:** The MAC over a durable tenancy record covers the invocation
identifier and a digest of the payload, in addition to the queue and the
definition. A token is answerable for the one row it travelled in. Copying an
honest token onto any other row — a second invocation of the same job on the same
queue, or the same invocation with the payload rewritten underneath it — fails
verification. To make that possible, the payload is encoded and the invocation
identifier minted **before** the trusted context provider is asked to capture.

## The decision

### What was wrong

`tenancyjobs.record` bound two fields: the namespace digest and the definition
name. With the origin, the generation and the reference that the sealer adds, the
token authenticated the tuple

    (deployment, queue, definition, tenant, generation)

and nothing that distinguishes one invocation from another. Every field is
constant across every job of one definition on one queue, so one honest token was
a valid token for **all** of them.

That is not a forgery problem, and no amount of MAC strength addresses it. The
adversary the `DurableKey` exists to defeat is named in the code and the docs:
anything else with write access to the shared queue table. Such an adversary does
not need to forge a MAC. It reads a row the tenant really enqueued, copies the
token, and writes a new row — same queue, same definition, its own payload,
its own invocation id. `Unseal` verifies. The control plane, asked about a tenant
that really is active, answers active. The generation matches because it is the
same generation. `RestoredIdentity.validFor` re-digests the partition, which also
matches. The handler then runs attacker-chosen work inside a context bound to
that tenant, for the whole durable class.

Three documents claimed otherwise, in the strongest terms each could manage:
`tenancy/seal.go` said the binding fields "tie the token to the record that
carries it"; `docs/modules/en/tenancy.md` said "a record written by anything that
does not hold the durable key reaches nothing at all"; and [[D-117]] said "queue
write access is therefore not tenant impersonation". The first two were false as
written. The third was true only of a *suspended* tenant, which is the only case
its own rationale — the lifecycle re-check at execution — actually reaches.

### What binds now

`record` binds four fields: the namespace digest, the definition name, the
invocation identifier, and the wire digest of the encoded payload.

The invocation identifier alone would not be enough. It is the row's primary key,
so an adversary who can write the table can usually also update it in place: keep
the identifier, change the payload. The wire digest closes that, and it is free to
trust here because `delivery_record.go` already re-derives it from the bytes it
read and refuses a record whose payload does not match — so a token bound to a
wire digest is bound to the payload the handler will actually be given.

The payload digest was not used for this. It is the *semantic* identity, equal
across two encodings of the same value, which is what makes it right for
`EnqueueOnce` deduplication and wrong here: the point is to bind the bytes.

### The ordering this costs

A token that binds the record can only be minted once the record exists. So the
placement path now encodes the payload and mints the invocation identifier before
it calls `TrustedContextProvider.Capture`, and passes both to it on
`ContextCaptureRequest`. The same two values come back on
`IdentityRestoreRequest` at delivery, from `Invocation.ID()` and
`RestoredDelivery.WireDigest()`.

The previous order was the reverse, and it was pinned by a test then named for
it — now `TestEnqueueContextProviderIsContainedBeforeDriverEffects`: a provider
that refuses cost no payload encode and no entropy draw. That property is
genuinely gone and is not coming back — the two requirements are incompatible,
because one wants the token before the record and the other wants the record
before the token.

It is the right trade, and the asymmetry is the argument. What the old order
bought was *cheapness on a refusal*: one bounded, pure, local encode and one read
from an RNG, on a path that was about to return an error. What it cost was a
cross-tenant execution primitive. A refused enqueue doing slightly more arithmetic
is not a security property; a token that authenticates any row of a queue is.

What a refusal must still cost is nothing **externally observable**, and that is
unchanged and still asserted: no placement, no stage, no transaction. The test
keeps its other half — the provider's error is still contained, so a provider
cannot leak its own message or choose the producer's error identity — and was
renamed to `TestEnqueueContextProviderIsContainedBeforeDriverEffects` for what it
now pins. It asserts `encodes == 1` positively rather than leaving the new order
implicit, so a future reordering has to come back and change that line.

## What it forbids

- Do not bind a durable token to fields that are constant across the records of
  one queue and definition. If a new seam adds a binding, at least one field must
  distinguish one record from the next.
- Do not move the context capture back before the payload encode. It is not a
  performance question; it silently unbinds the token from the record.
- Do not substitute `PayloadDigest` for `WireDigest` in the binding. Two distinct
  encodings with one semantic digest would verify against each other.
- Do not claim in any document that a durable record cannot be replayed, without
  saying what the binding actually covers.

## Proven by

- `TestAnHonestTokenReattachedToAnotherRecordEntersNoHandler` in
  `tenancy/tenancyjobs/durable_test.go` — the replay this decision exists to stop,
  assembled from the public API: an honest token is lifted off a real placement
  produced by a real queue and reattached to a new row, to the same row with the
  payload swapped, and to both at once. Its first assertion is the control — the
  tenant's own record restores — so the refusals below it cannot pass for free.
- `TestADurableRecordCannotBeReplayedIntoAnotherJobOrQueue` in the same file —
  extended with `onto another record of the same job` and `onto the same record
  with another payload`, the two cases the queue-and-definition binding could not
  refuse.
- `TestEnqueueContextProviderIsContainedBeforeDriverEffects` in
  `jobs/queue_test.go` — the driver effects a refused capture must still not have,
  and the encode it now does.

Reverting `record` to the two-field binding fails five subtests across the first
two, which is the check that they are not vacuous.

## See also

- [[D-117]] — a verified scope is minted, never manufactured. Its "queue write
  access is not tenant impersonation" paragraph was corrected to claim only the
  lifecycle re-check it actually argues for, and to point here for the rest.
- [[D-118]] — a transactional enqueue is the outbox. The record binding is what
  makes an outbox row in a shared database safe to trust on the way back out.
- [[FL-033]] — a request becomes a tenant-bound statement.
