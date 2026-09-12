# FL-043 — An uncertain append becomes a resolvable operation

**Entry points:** `event.Repo.Digest` (the fingerprint), `receipt.Once` (the
whole order in one call), `receipt.Claim` / `receipt.Held.Complete` (the
open-coded form), `receipt.Resolve` (the read, from another process),
`receipt.Ledger` (the application's own table)
**Governed by:** [[D-118]] [[D-122]] [[D-125]] [[D-126]] [[D-142]] [[D-143]]

What happens between a command whose outcome nobody heard and a durable row that
answers *did that operation happen, and what did it write* — the claim that goes
**before** the decision, the fingerprint over the bytes rather than over the
version, the two statements whose **order** is the mechanism, and the third
answer a resolve has that a claim never needs.

[[FL-036]] is the half below this one: the declaration, the append, and
`Repo.Digest`, which is the first four of `Append`'s six steps with the store
left out. [[FL-035]] is the other mechanism in this repository that answers a key
with created / existing-same-content / conflict — `jobs.EnqueueOnce` — and
[[D-142]] is where the two are adjudicated by symbol. This flow starts where a
caller has minted an operation key and ends where a second process reads the row.

`event/receipt` is a package of the **root module** and its closure is `event`,
`crud`, `errs`, `utils` and the standard library. It opens no transaction, starts
no goroutine, reads no clock and writes no line; `scripts/event_test.go` and
`scripts/extensions_test.go` are what hold the first three.

## The window this exists for is not `Append`'s

`event.ErrUncertain` already says *the append was issued and nobody confirmed
it*, and `event/eventpg/classify.go` already refuses to hide that behind a retry.
What neither answers is the question a **new process** asks, holding nothing but
a key: the stream's version does not say who moved it, and a stream that did not
move is *"nothing was written"* and *"written, acknowledgement lost"* read the
same way.

So the answer has to be a durable row under the caller's own key, written in the
same transaction as the events. It is [[D-118]]'s rule and not an exception to
it: *"'atomic' across two handles is a sentence with no meaning."*

## The order, and where it is enforced

`event/receipt/claim.go:Once` is the whole order in one call — claim, run the
work only on `Recorded`, append inside it, complete in the same transaction — and
it exists because the order cannot be enforced by inspection. Nothing in this
package can see an append that already happened on a context.

The claim is **first**, and the case that decides it is two retries at two
different expected versions: both are admitted by the store because neither is a
concurrency conflict, and a receipt written after the append discovers the
collision with both sets of events already in the log ([[D-142]]).

`Claim` / `Append` / `Held.Complete` open-coded stays for a caller that must
answer a repeat differently. `TestEveryRefusalClaimHasIsReachableThroughOnce`
drives every one of `Claim`'s refusals through both doors and asserts the same
sentinel comes back, because "reachable by construction" stops being true on the
first refactor.

## What makes one commit out of two tables

`event/receipt/claim.go:sameUnit` is `event/projection/pass.go`'s `checkUnit` put
to a second purpose, and it asks three things in one place because a claim and
its completion owe the same answer:

1. `Store.Capabilities().Transactions == event.Supported` — a claim written on a
   store's own autocommit commits whether or not the events beside it do;
2. `Store.Transaction(ctx)` is valid — a claim outside one is a row that survives
   the rollback of the events it names;
3. `Ledger.Transaction(ctx)` compares `Authority.Same` with it — two handles
   resolving one context to two transactions are two commits, and a crash between
   them leaves one of the two.

`Resolve` is the mirror: `event/receipt/resolve.go:outsideEveryTransaction`
refuses **inside** either handle's transaction, because a resolve on the claiming
transaction's own context reads its own uncommitted row and answers `Found` for
an operation that can still roll back. That is the whole purpose of
`ResolveSpec.Store`, and nothing else about it is read.

## The fingerprint, and the version that is not in it

`event/repo.go:Digest` runs the token's key, every change's stream and aggregate,
each change's own carried refusal and the store's bounds, then `digestOf` over
the composed stream and each record's type, revision and payload — length-
prefixed, every length and every revision eight big-endian bytes. It reaches no
store: the limits it reads were retained at `Bind`.

**The version the token was loaded at is not in the preimage.** A retry in a new
process loads what the store now holds — which is the version the first attempt
moved the stream to — so a digest over the version could not be reproduced by the
one caller the mechanism exists for.

The preimage is a **frozen format** and `TestTheDigestPreimageIsFrozen` holds it
as six golden vectors, because a fingerprint is read back by a later build than
the one that wrote it: reordering a field or narrowing a length answers every key
still inside a retention window as a collision on an operation nobody performed.

`event/receipt/fingerprint.go` is the rendering — `"sha256:"+hex`, with
`ParseFingerprint` as the other direction so a `Ledger` binds and scans text
without re-deriving the encoding. `event/receipt/key.go` is the identity, which
renders `"[operation key]"` and never its value; `Key.Value` is the one door the
value comes back out of, and it exists for the ledger's own statement.

## The claim is two statements, and the order is the mechanism

`event/receipt/receipt.go:Ledger`'s doc comment carries the contract:
`INSERT … ON CONFLICT (key) DO NOTHING`, **then** a `SELECT` of the same key, in
that order, in the caller's one transaction, with `won` the insert's affected-row
count.

The insert is what blocks: PostgreSQL's speculative insertion waits on a
conflicting uncommitted tuple and then inserts or not on that transaction's
outcome, which is why a claim never has to answer the `Unresolved` a resolve
does. The read is a **statement of its own** because at READ COMMITTED a second
statement is a second snapshot. [[D-142]] carries the measurement that decided
all three spellings, including the `DO UPDATE` form that works and convoys.

`event/receipt/claim.go:claimAnswered` checks the ledger's answer before it is
compared or rendered — the rule the kernel already applies to a store's strings
and numbers — and a winner reading back something other than its own insert is
`ErrLedger`.

## Three verdicts, four standings, and the two that are refusals

`event/receipt/claim.go:verdictOf` answers `Recorded`, `Repeated` or `Collided`.
`Collided` is returned **beside `ErrCollision`**, and a present row with no
completion is `ErrIncomplete` with **no verdict at all**: a state nothing may be
concluded from is a refusal and not an answer, and a verdict is a value it is
legal to discard.

`event/receipt/resolve.go:Resolve` answers `Found`, `Incomplete`, `Unresolved` or
`Expired`. `standingOfAnAbsentRow` is the one cross-clock comparison in the
package — `ResolveSpec.Issued`, the caller's own, against `Ledger.Horizon`, the
database's — and its zero `Issued` gets `Unresolved` rather than a refusal,
because a refusal there sends a caller to `time.Now()`, which is after every
horizon. `findAnswered` refuses a horizon published **after** a row the ledger
still answers for, which is the `MAX(recorded_at)`-for-`MIN(recorded_at)` defect.

**An absent row is never a rollback** ([[D-143]]): a resolver on a second
connection cannot tell a transaction that rolled back from one still open, there
is no row to lock, and `idle_in_transaction_session_timeout` on the application
role is the deployment's lever that bounds it.

## Where the decisions bite

- **[[D-118]]** — the receipt is a durable write inside the caller's bound
  transaction, which is this framework's outbox shape. No second durable-intent
  table is written, and `event/receipt` imports no queue.
- **[[D-126]]** — nothing here chooses an isolation level. What the level decides
  is how a loser fails: blocks and repeats at READ COMMITTED, `40001` and a whole
  unit rolled back above it, with the retry the caller's.
- **[[D-142]]** — the claim's two statements and their order, the fingerprint's
  contents, the frozen preimage, and why `jobs.EnqueueOnce` is convergent rather
  than the same mechanism.
- **[[D-143]]** — the third standing, the clocks, the horizon, and the fourth
  standing that was designed and refused.
- **[[D-125]]** — the composed stream that opens the preimage is `event.Compose`'s
  frozen rendering.
- **[[D-122]]** — a ledger's answer is checked before it is used, the way a
  store's is.

## Traps

- **A repeat answered from an incomplete row is a success that may never have
  happened.** The discriminator is `Receipt.Complete` and never the range: `0..0`
  is also the honest range of a decision that yielded no changes.
- **`Held` holds an unexported pointer on purpose.** A copy of a `Held` is the
  same claim rather than a second one, which is what makes a second `Complete`
  refusable at all.
- **An empty commit carries the invalid authority by design**, so `Complete`
  exempts it from the authority comparison and from it alone.
- **`Digest` answers the same refusals `Append` would**, so a caller that digests
  first learns a malformed append before it spends a key.
- **The retention sweep is the application's.** A horizon shorter than the age of
  a row written milliseconds ago is not a horizon, and the window between the
  claim's two statements is closed by retention rather than by the statement.

## Files

| File | What it holds |
|---|---|
| `event/receipt/doc.go` | the package paragraph, including the sentence that gives the word its meaning here beside `event.Commit`'s |
| `event/receipt/key.go` | `Key`, `NewKey`, `Key.Zero`, `Key.String`, `Key.Value`, `checkKeyText` — the kernel's identifier rule applied to a value this package stores in somebody else's primary key |
| `event/receipt/fingerprint.go` | `Fingerprint`, `NewFingerprint`, `ParseFingerprint`, `Fingerprint.Equal`, `Fingerprint.Zero`, `Fingerprint.String`, `fingerprintPrefix` — the frozen rendering and both directions of it |
| `event/receipt/receipt.go` | `Receipt` and `Ledger` — the durable row's shape, and the application's table behind an interface this framework does not implement; the two claim statements and their order are this file's doc comment |
| `event/receipt/claim.go` | `Verdict` and its three values, `Verdict.Valid`, `Verdict.String`, `ClaimSpec`, `Held`, `claimed`, `Held.Verdict`, `Held.Receipt`, `Claim`, `Once`, `Held.Complete`, `claimable`, `sameUnit`, `claimAnswered`, `verdictOf`, `absent` — the write door, its five completion refusals, and the three questions the atomicity rests on |
| `event/receipt/resolve.go` | `Standing` and its four values, `Standing.Valid`, `Standing.String`, `ResolveSpec`, `Resolution`, `Resolve`, `standingOfAnAbsentRow`, `resolvable`, `outsideEveryTransaction`, `findAnswered` — the read door, and the one cross-clock comparison |
| `event/receipt/errors.go` | `ErrSpec`, `ErrCollision`, `ErrIncomplete`, `ErrLedger` — four, and the split between them is which party got something wrong |
| `event/repo.go` | `Repo.Digest`, `digestOf` — the bytes an append would write, and the frozen preimage ([[FL-036]]) |
| `event/errors.go` | `ErrUncertain`, the answer this flow exists to make resolvable |
| `scripts/event_test.go` | the `event/receipt` row of `charged`: this package costs `crud`, `errs` and `utils` and nothing else |
| `_examples/event-receipts/main.go` | the reference `Ledger` — both claim statements in order, the completing `UPDATE`, a SQL-side `Horizon` from a configured retention, a `jobs` periodic that sweeps, and both refusals branched on |

Every non-test `.go` file under `event/receipt/` has a row above, `doc.go`
included: the reverse index in `docs/ai/flows/Index.md` is what an agent reads
before editing a file, and a file with no row there reads as a file outside every
flow.

## Tests that walk this flow

Untagged, in `make unit`: `event/digest_test.go` (the fingerprint and its frozen
preimage), `event/receipt/key_test.go`, `event/receipt/errors_test.go`,
`event/receipt/receipt_test.go`, `event/receipt/claim_test.go` and
`event/receipt/resolve_test.go`.

Behind `//go:build integration`, against a live PostgreSQL:
`event/eventpg/receipt_integration_test.go` — the two-caller race at three
isolation levels, the lost-connection retry with its naive control, the
held-open transaction that makes `Unresolved` indistinguishable from a rollback,
and the four ledger defects. The gate names its own command:

```sh
FROSTGROVE_EVENTPG_TEST_DSN='postgres://vv:vv@localhost:55432/vv?sslmode=disable' \
  go test -race -count=1 -tags=integration ./event/eventpg/
```

### Proved by

| What holds | Proved by |
|---|---|
| a claim, an append and a completion are one transaction, and a rollback takes all three | `TestAClaimAnAppendAndACompletionAreOneTransaction`, `TestClaimAppendCompleteAndTheRollbackControl` |
| a claim is refused unless the ledger and the store are one transaction, at all three doors | `TestAClaimIsRefusedUnlessTheLedgerAndTheStoreAreOneTransaction`, `TestTheTwoPoolRefusalBesideTheOnePoolAcceptance` |
| a completion that is not the claim's is refused five ways | `TestACompletionThatIsNotTheClaimsIsRefusedFiveWays` |
| a repeat is answered only from a complete row, and an unresolved one refuses its retry | `TestARepeatIsAnsweredOnlyFromACompleteRow`, `TestAnUnresolvedClaimRefusesItsRetry` |
| a collision travels as an error and not only as a verdict, live through a real ledger row | `TestACollisionTravelsAsAnErrorAndNotOnlyAsAVerdict`, `TestTheCollisionTableLiveBesideTheVersionVariant` |
| `Once` runs the work only on `Recorded` and completes it, and every refusal `Claim` has is reachable through it | `TestOnceRunsTheWorkOnlyOnRecordedAndCompletesIt`, `TestEveryRefusalClaimHasIsReachableThroughOnce` |
| two callers racing one key leave one winner and one repeat, at every isolation level | `TestTwoCallersRaceOneKey` |
| the lost-connection retry writes one copy of the events, and the naive control writes two | `TestTheLostConnectionRetryEndToEnd` |
| an absent row is `Unresolved` and a horizon decides `Expired`, with both spellings and both directions of skew | `TestAnAbsentRowIsUnresolvedAndAHorizonDecidesExpired`, `TestExpiredUnresolvedAZeroIssuedAndTheTwoSkewArms` |
| `Unresolved` while the writing transaction is open and `Unresolved` after a rollback are indistinguishable | `TestUnresolvedWhileOpenAndAfterARollbackAreIndistinguishable` |
| a resolve inside the writing transaction is refused, reads no event and offers nothing to append with | `TestAResolveInsideTheWritingTransactionIsRefused`, `TestAResolveReadsNoEventAndOffersNothingToAppendWith` |
| the published claim binds no instant as a parameter | `TestThePublishedClaimBindsNoInstantAsAParameter` |
| a ledger that answers something no ledger answers is refused before it is compared | `TestALedgerThatAnswersSomethingNoLedgerAnswersIsRefused` |
| four ledger defects each break the case that names them | `TestFourLedgerDefectsEachBreakTheCaseThatNamesThem` |
| one key covers one append to one stream, and two misordered callers are caught | `TestOneKeyTwoStreamsAndTheKeyPerAppendControl`, `TestTwoMisorderedCallersAndTheirOnceControl` |
| a digest is the bytes this append would write, is equal across two attempts at different versions, and collides on a byte, a stream or an order | `TestADigestIsTheBytesThisAppendWouldWrite`, `TestTwoAttemptsAtDifferentVersionsDigestEqual`, `TestADigestCollidesOnAByteAStreamAndAnOrder`, `TestTheDigestPreimageIsFrozen` |
| a codec that does not encode the same bytes twice fails closed on every retry | `TestACodecThatDoesNotEncodeTheSameBytesTwice` |
| a key renders nothing of its value, no receipt type exposes an append token, and no refusal names a key or a preimage | `TestAKeyRendersNothingOfItsValue`, `TestNoReceiptTypeExposesAnAppendToken`, `TestNoRefusalOfAReceiptNamesAKeyOrAFingerprintsPreimage` |
| the published reference ledger is the one the live suite proved, both statements and in order | `TestTheExampleLedgerIsTheOneTheLiveSuiteProved` |
| this package costs `crud`, `errs` and `utils` and nothing else, opens no transaction and starts nothing | `TestNoEventPackageCostsMoreThanTheSeamItNames`, `TestNothingInTheProjectionPackageOpensATransaction`, `TestMerelyImportingTheEventExtensionStartsNothing` |
