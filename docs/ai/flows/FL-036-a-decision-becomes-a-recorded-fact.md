# FL-036 — A decision becomes a recorded fact

**Entry points:** `event.Define` / `event.Declare` (the declaration),
`event.Open` / `event.Bind` (the composition root), `event.Repo.Load` /
`event.Repo.Append` (the write path), `event.Repo.StateAt` / `event.Repo.Digest`
(the bounded read and the fingerprint), `event.Read` / `event.Reader.Next` (the
log walk)
**Governed by:** [[D-121]] [[D-122]] [[D-123]] [[D-124]] [[D-125]] [[D-128]]
[[D-142]] [[D-144]]

What happens between an application declaring that an aggregate has facts and
those facts being an append-only history a later process folds back into a
state — including the six checks an append passes before a store hears about it,
the two doors a store is admitted through, and the six classes a refusal can
belong to.

`event` is the vocabulary and the seam; `eventmemory` is a store; `eventtest` is
the contract written as a suite a store runs against itself. No package under
`event/` is imported by any other subsystem, which is what makes the whole thing
optional by the import graph rather than by a paragraph.

[[FL-037]] is the same path over PostgreSQL. [[FL-038]] is the half above the log
walk: a page a consumer finished with becoming a durable checkpoint row, and the
`event.Checkpoints` seam beside the `event.Store` one.

## The declaration, and when it stops being writable

`event/aggregate.go`, `event/fact.go`, `event/chain.go`, `event/seal.go`:

1. `Define[S, ID](family, key)` mints an `*Aggregate`. The family goes through
   the declared-identifier rule (`event/text.go:checkName`) — the kernel's text
   rule and the bracket pair a rendered field is written inside — and the
   identity mapper must be non-nil. `TryDefine` is the same three lines
   returning the error the panicking one raises ([[D-123]]).
2. `From(codec)` starts a `Chain` at revision 1 and `Then(prev, codec, up)`
   adds one. **A revision is the position in the chain and is never typed**, so
   there is no number to duplicate or leave a gap in. Neither call returns an
   error: a defect is carried on the `Chain` and surfaced by the declaration
   that reads it.
3. `Declare(a, name, chain, fold)` charges the wire type name against the same
   `checkName` the family crosses, refuses a nil fold and an empty chain, and
   calls `Aggregate.declare`, which refuses a duplicate name and a fact arriving
   after the table was read.
4. `event.JSON[V]()` charges `event/encodable.go:chargeJSON` when the codec
   value is built, so a payload whose bytes no declaration reads back is
   refused before `main` rather than recorded ([[D-124]]). The walk is that
   file; the rules it applies — which method set `encoding/json` reaches where,
   and a marshaller promoted from an embedded field at any depth of the
   promotion chain — are `event/routing.go`.
5. `Aggregate.seal` flips the table read-only on **first observation** rather
   than on first bind, and `event/seal.go` enumerates its six readers:
   `Aggregate.Family`, `Aggregate.Key`, `Aggregate.Fold`, `Fact.New`,
   `Fact.RoundTrip` and `Bind`. A seventh reader without a row there is what the
   seal test fails on.

## Binding a store

`event/binding.go`:

`Open(store)` is the composition root's binding table and `Bind(b, a)` is five
constant-time checks and one allocation. `admit` is the store-honesty gate:
the backing must be valid, every number in `Limits` must be stated and inside
the kernel's own ceiling (`event/bounds.go`), and every `Support` in
`Capabilities` must be `Unsupported` or `Supported` — `Unstated` is refused,
because a capability nobody stated is not one nobody has.

`Bind` reads `Limits()` and `Capabilities()` **once and retains them**, which is
what makes an empty append cost no store call at all. It does not retain
`Backing()`: a remembered backing cannot see a store that re-pointed at another
database.

`Read(log, after)` is the second door and runs the same `admit`, because a
deployment that only projects never binds.

## A load

`event/repo.go:Repo.Load`, three steps in a fixed order:

1. the identity through the declared mapper, and the key it renders against the
   text rule and the `MaxKey` retained at `Bind` — neither touches the store, so
   a zero identity against a closed store is `ErrKey` and not `ErrClosed`;
2. `Store.Transaction(ctx)`, which is the store's own answer about what the
   context carries;
3. `replay`, which pages `Store.ReadStream` until a short page.

`checkPage` verifies the two bounds only the store can apply and the shape it
cannot be trusted about: a page longer than the published `StreamPage`, an
envelope of another stream, and a page that does not begin at the version it was
read from and rise by one. That last one is the expensive check for the cheapest
reason — a page returned as `[v3, v1, v2]` folds to a state that is wrong at the
right version, with the right count and no refusal anywhere.

`apply` re-reads the store's own strings before using them for anything, a
rendering included: the recorded type name against `checkName`, which is the
rule `Declare` held the declared name to, so a name that breaks it names no
declared fact and can name none; the recorded payload against
`Limits().MaxPayload`; the revision against the fact's own count.

Any non-nil error returns the zero state and the zero token, never the
accumulator.

## An append

`event/repo.go:Repo.Append`, six steps, and nothing reaches the store until all
six pass:

1. the token's own key, against the text rule and the retained `MaxKey`;
2. every change's stream against the token's, and the aggregate it was decided
   on against this repository's (`event/change.go:decidedFor`) — the second is
   `ErrFamily`, because a stream is `{Family, Key}` and two aggregates declared
   over one family name each other's streams exactly;
3. each change's own carried refusal, from `Fact.New`;
4. the store's bounds — the payload per record, the batch count, and the bytes
   this append would hold at once (`Repo.records`);
5. the backing, read from the store now and compared with `Backing.Equal`;
6. the transaction question.

The empty-append short circuit sits between (1) and (2), so a forged token is
refused even when nothing would be written. Past step (6) the outcome is the
store's: a conflict says the stream moved, an uncertainty says the append was
issued and nobody confirmed it, and reading `err != nil` as "nothing was
written" is the inference `ErrUncertain` exists to refuse.

`Commit` is assembled by the kernel from what it already knows — the store's
`Append` answers with an error and nothing else, so nothing sealed crosses the
store boundary in the store's direction.

## A bounded read, and a digest of what an append would write

Two calls that reach nothing new: `event/repo.go:StateAt` is `Load`'s own loop
with a ceiling, and `event/repo.go:Digest` is the first four of `Append`'s six
steps with the store left out.

`replay` takes the bound as a parameter — `Load` passes zero, which means *to the
end of the stream* — so there is one loop and not two, and the failure path is
the one `Load` already had: any non-nil error is the zero state and never the
accumulator. Two orderings inside it are load-bearing and are named in its
comment so nobody tidies them away:

- **check, then truncate.** `checkPage` runs on the page the store returned,
  entire. Truncating first would hide a page answered as `[v3, v1, v2]`, which
  folds to a state that is wrong at the right version with the right count.
- **truncate, then fold.** The loop stops reading the moment the accumulated
  version reaches the bound, and folds nothing above it.

The over-read is at most one page whatever the stream's length, which is the
whole argument for not putting a ceiling on `Store.ReadStream` ([[D-144]]).

`StateAt` returns **a state and an error and nothing else**. Version zero, a
version past the end and a stream with no events are one refusal, `ErrVersion`
(`event/errors.go`), in the request class beside `ErrKey` — an undeclared wrap
would render a 500 for data only the caller can correct. An event this build
cannot read is `ErrUnknownType`, `ErrRevision`, `ErrUpcast` or `ErrPayload`,
inherited from `apply` rather than re-raised.

`Digest` runs the token's key, every change's stream and aggregate, each change's
own carried refusal and the store's bounds — then `digestOf` over the composed
stream and each record's type, revision and payload, length-prefixed, every
length and revision eight big-endian bytes. **It issues no store call**: the
limits it reads were retained at `Bind`. The version the token was loaded at is
**not** in the preimage, and [[D-142]] is why. The layout is frozen and held as
golden vectors, because a fingerprint is read back by a later build than the one
that wrote it and a reordering answers every key still inside a retention window
as a collision on an operation nobody performed.

[[FL-043]] is where that digest goes: an operation key, a receipt row and the
three verdicts a repeat can get.

## The transaction question

`Repo.Within` asks `Store.Transaction(ctx)` and leaves a marker
(`event/marker.go`); it opens nothing, and on a store that does not claim
transactions it is `ErrNoTransactionBinding`. A marker **chains** on any marker
already there and is resolved by backing, which is what lets two stores'
transaction contexts compose in one context.

`Repo.Authority(ctx)` is the store's answer for this context and nothing else,
so two subsystems can compare that they wrote in one transaction. It never
reports closure: the refusal comes from the `Load` or `Append` that follows.

The framework opens no transaction, commits none and rolls none back. The seam
declares no such method, so there is no place for one to be called from
([[D-118]] is the same rule for durable work).

## The log walk

`event/reader.go`: `Read(log, after)` builds a `Reader`, `Next` fetches one page
through `Log.ReadAll` and `Cursor()` is safe to persist and resume from in
another process. A page whose positions do not ascend is refused before a
consumer checkpoints past an event it never saw; gaps between positions are
normal.

That refusal is one half of a kernel law rather than a store's option
([[D-128]]): positions ascend, and one stream's events reach the log in the order
that stream holds them. There is no capability for either, and the alternative —
the reference implementation's `ORDER BY transaction_id, id`, under which
positions go backwards — is refused because vv's store joins a transaction the
caller opened ([[D-118]]), so the writing transaction's id can be older than a
stream predecessor's and that order reverses one stream against itself.

`ReadOnly(store)` hands back a `Log` — the surface with no `Append` — for a
consumer that must not be able to assert its way back to the write path.

**A walk tiles a quiescent log. Delivery is at least once.** Nothing here
promises otherwise and no wording in this subsystem may.

## The refusal partition

`event/errors.go` holds twenty-four sentinels in six classes, and `errors.Is`
never crosses a class:

| Class | Sentinels | What it means |
|---|---|---|
| declaration | `ErrDeclaration`, `ErrSealed`, `ErrCodecType` | a programmer wrote the declaration wrong; panicked, never returned |
| wiring | `ErrFamily`, `ErrWrongStore`, `ErrWrongStream`, `ErrNoTransaction`, `ErrNoTransactionBinding`, `ErrAmbientNotTransaction`, `ErrTransactionMismatch`, `ErrCursor` | this program was assembled from values that do not belong together |
| request | `ErrKey`, `ErrEncode`, `ErrSample`, `ErrTooLarge` | the data this operation was given cannot be used, and a transport answers a client error |
| history | `ErrUnknownType`, `ErrRevision`, `ErrPayload`, `ErrUpcast` | a fact's recorded bytes cannot be read by this declaration |
| write | `ErrConflict`, `ErrUncertain` | the append did not do what you asked, and here is your obligation |
| store | `ErrBackend`, `ErrClosed`, `ErrRefused` | the store itself refused or failed |

A store adds no sentinel of its own. It selects one of seven `Outcome` values
and `event/errors.go:refuse` maps the selection; `event/outcome.go:Failure`
normalises an outcome outside the seven to `Unclassified` before the value can
travel. The door decides exactly one thing — what an unclassified error means —
because the safe guess differs between a write and a read.

`refusal.Is` reaches the sentinel and the declared class wrap and never the
cause; `refusal.As` reaches the declared wrap only; `CauseOf` is the one reader
that has the cause. Both traversals answer false for `context.Canceled` and
`context.DeadlineExceeded`, always: a cancellation travels as itself or not at
all ([[D-122]]).

Every question the kernel asks of an error it did not build runs through
`event/errors.go:walk`, under a hop budget and a `recover`: a store's chain is a
foreign chain, and a cyclic `Unwrap` in one would otherwise wedge the request
goroutine.

## Where the decisions bite

- [[D-121]] — `event` is a root package because a module boundary here is a
  third-party dependency boundary; `eventmemory` is what earns the seam.
- [[D-122]] — a store classifies in a closed vocabulary and the kernel maps it
  through `errors.As`; a cause is never read as a class.
- [[D-123]] — `Define` panics and `TryDefine` returns; both answer the same
  value through one code path.
- [[D-124]] — `event.JSON` refuses nine shapes `encoding/json` accepts and
  cannot read back, and it bounds no depth on the decode path.
- [[D-125]] — `Compose`'s rendering is frozen: it is part of every stream ever
  written under it.
- [[D-128]] — the log delivers in position order and one stream's order is a
  subsequence of it; both are laws with no capability, and `Progress.Highest` is
  a completeness watermark because of them.
- [[D-061]] — every method of `Store` is required, so the value a composition
  root handed over answers every question and a decorator cannot be walked past.
  `ReadOnly` is the one wrapper here with no `Next`, and taking the surface away
  is the point of it.
- [[D-062]] — this subsystem emits no log line, no span and no metric.
- [[D-116]] — the module-boundary rule `event` is a root package under.

## Traps

- **A short page ends the stream.** `replay` issues no confirming read, so a
  store that returns a short page which is not the end truncates a history
  silently. It is a conformance defect the suite drives, not an accident nobody
  thought about.
- **`Fold` consumes the state it is given.** It writes through every reference
  kind it reaches. Take the result and do not use the argument again; on an
  error the returned state is not usable and a reload is the authority.
- **A conflict carries no version.** Reporting the store's actual current
  version invites re-appending a stale decision at a fresh version. Reload,
  decide again, append again — and the retry unit is the *transaction*, because
  a losing insert poisons the block on a SQL store.
- **An empty append is not a no-op you can ignore.** It checks the key, answers
  a receipt whose `Stream()` is the token's and whose `Authority()` is the
  invalid one, and touches no store — so two subsystems proving they wrote
  together ask `Repo.Authority` rather than reading a receipt.
- **A payload handed out is the recipient's, including its capacity.** A store
  that reuses a buffer across two calls rewrites a value the application already
  holds, and `Fact.RoundTrip` is the proxy that catches a codec doing the same.
- **`eventmemory` refuses a claimed stream at once where a SQL store waits.**
  Two transactions taking two streams in opposite orders refuse each other
  instead of deadlocking, and a transaction held open across a network call
  starves every other writer of its streams. The claim names its transaction
  weakly (`log.go:claim`), so one nothing can reach any more — and therefore
  nothing can commit — stops holding its streams once the runtime collects it.
  Reachability is the whole of the rule, so the authority the kernel puts in
  every commit receipt names `txIdentity` — the store's own name for the
  transaction, monotone per log — rather than the `*Tx`: a receipt is a value a
  caller is meant to keep, and one that named the `*Tx` would pin its claims for
  the life of the process.
- **`ctx.Value` is the caller's own code and never runs under a store's lock.**
  `ambient` is the lookup and runs before the mutex; `Tx.live` is the liveness
  check and runs inside it, because that is the half a concurrent `Commit` can
  invalidate. A caller whose context decorator locks in `Value` would otherwise
  deadlock the whole log against any goroutine holding that lock across an
  append.
- **What a log walk answers inside a bound transaction is the store's, not the
  kernel's.** `event.Log.ReadAll` leaves it unspecified and so does
  `event.Envelope.Position` before a commit; `eventmemory` returns no staged
  envelope and answers position `0`, a SQL store reading through the transaction
  it joined returns its own uncommitted rows with real positions. No path the
  kernel initiates opens a transaction and then reads globally — `Reader.Next`
  runs the read on whatever context the consumer hands it and refuses none, so
  the obligation not to persist that cursor is the consumer's and is written on
  `Reader.Cursor`. `eventtest` certifies neither answer.
- **A store's panic is not recovered.** It has an error channel and an outcome
  vocabulary, so recovering one would be the kernel classifying a failure the
  store did not classify. The caller's `defer tx.Rollback()` is what covers it.

## Files

| File | What it holds |
|---|---|
| `event/doc.go` | the package sentence |
| `event/identity.go` | `Key`, `Version`, `Position`, `Cursor`, `Stream`, `Stream.String`, `Compose`, `escapePart` |
| `event/text.go` | `checkText` — the kernel text rule, `checkName` — that rule plus `fieldOpen`/`fieldClose`, the pair a rendered field is written inside, applied to both declared identifiers, and the two predicates `Compose` reads through |
| `event/bounds.go` | `MaxPayloadBytes`, `MaxNameBytes`, `MaxKeyBytes`, `MaxBatchCount`, `MaxPageCount`, `MaxResidentBytes`, `MaxCursorBytes`, `ResidentPage` — the seventh bounds what a store **mints** rather than what it accepts, and is not one of a store's `Limits` ([[FL-038]]) |
| `event/backing.go` | `Backing`, `NewBacking`, `Backing.Equal`, `nilByAnyRoute` |
| `event/authority.go` | `Authority`, `NewAuthority`, `Authority.Same`, `Authority.Valid` |
| `event/marker.go` | `marker`, `withMarker`, `markerFor` — what `Within` leaves and how it is resolved |
| `event/token.go` | `At`, `Commit` and its six accessors |
| `event/outcome.go` | `Outcome`, the seven values, `Failure`, `failure` |
| `event/errors.go` | the twenty-five sentinels — `ErrVersion` joined the request class beside `ErrKey` with the bounded read — `vocabulary`, `refusal`, `refuse`, `CauseOf`, `walk`, `findAs`, `causeAsWrap` |
| `event/store.go` | `Support`, `Capabilities`, `Limits`, `Record`, `AppendRequest`, `Envelope`, `Log`, `Store` |
| `event/codec.go` | `Codec`, `JSON`, `encodeWith`, `decodeWith`, `canEncodeWith` |
| `event/encodable.go` | `chargeJSON`, `jsonWalk`, `members` — the type graph, its three budgets, and the JSON names a struct renders |
| `event/routing.go` | `ownMethods`, `promotedMarshaller`, `objectKey`, `writeRoute`, `readRoute` — `encoding/json`'s routing rules restated as refusals; with `encodable.go`, the nine shapes it accepts and cannot read back |
| `event/chain.go` | `Chain`, `From`, `Then`, `link`, `carry`, `upcastTo` |
| `event/aggregate.go` | `Aggregate`, `Define`, `TryDefine`, `Aggregate.Family`, `Aggregate.Key`, `Aggregate.Fold`, `locate`, `Declaration` |
| `event/seal.go` | `Aggregate.seal`, `Aggregate.declare` — and the enumeration of the six readers |
| `event/fact.go` | `Fact`, `Declare`, `TryDeclare`, `Fact.Name`, `Fact.Family`, `Fact.Revisions`, `Fact.Read`, `Fact.New`, `Fact.RoundTrip`, `roundTripping`, `Fact.notAliased`, `Fact.readBack`, `applierOf` — `Read` is the typed reading seam a consumer of one envelope shares with a replay ([[FL-038]]) |
| `event/comparison.go` | `valueWalk`, `valueWalkNodes`, `shares`, `same`, `sameFields`, `sameOpaque`, `equalByMethod`, `sameNumber`, `asFloat`, `sameElements`, `sameEntries`, `unanswered`, `singleValued`, `reusesItsBuffer`, `disturbedAtItsOwnWidth`, `readsBackOnTheWire` — the two walks a round trip makes over an application's own values, the budget they run under, the one position a type's own `Equal` is asked at, the two disturbances behind the address walk — the zero value, and the sample's own payload with a byte changed — and the re-encoding that answers behind the walk where nothing in the value can |
| `event/change.go` | `Change`, `Change.Err`, `decidedFor` |
| `event/binding.go` | `Binding`, `Open`, `Bind`, `admit`, `admitLimits`, `admitCapabilities` |
| `event/repo.go` | `Repo`, `Repo.Load`, `Repo.Append`, `Repo.StateAt`, `Repo.Digest`, `Repo.Within`, `Repo.Authority`, `replay`, `checkPage`, `apply`, `records`, `digestOf` — `replay` takes the bound, so the bounded read is the load's own loop with a ceiling and never a second one |
| `event/reader.go` | `ReadOnly`, `Read`, `Reader`, `Reader.Next`, `Reader.Events`, `Reader.Cursor`, `Reader.checkPage` — the page and the cursor it was answered with are one answer, so both are checked before the reader's own cursor moves |
| `event/eventmemory/log.go` | `Log`, `LogSpec`, `NewLog`, `bound`, `publish`, `claim`, `releaseDeadClaim`, `nameTransaction` — the backing, the two numbers the data depends on, the weak claim a stream is held by, and the name a transaction is known to a receipt by |
| `event/eventmemory/store.go` | `Spec`, `Store`, `New`, `resident`, `Capabilities`, `Limits`, `Backing`, `Close`, `Check` |
| `event/eventmemory/append.go` | `Store.Append` — expected-version admission against the committed height plus this transaction's staged records |
| `event/eventmemory/read.go` | `Store.ReadStream`, `Store.ReadAll`, `handOut` |
| `event/eventmemory/cursor.go` | `mintCursor`, `readCursor` — a cursor of this log's and no other's |
| `event/eventmemory/transaction.go` | `Tx`, `txIdentity`, `WithTransaction`, `Log.begin`, `Store.Begin`, `Log.transaction`, `Store.Transaction`, `Log.ambient`, `Store.ambient`, `Tx.live`, `Tx.Commit`, `Tx.Rollback`, `revalidateSaves`, `checkpointHeld`, `stageSave`, `stage` — two staging areas, and `Rollback`'s position arithmetic reads only the first |
| `event/eventmemory/checkpoints.go` | `CheckpointSpec`, `Checkpoints`, `NewCheckpoints`, its `Capabilities`, `Backing`, `Transaction`, `Begin`, `Load`, `Save`, `Forget`, `Close`, `held`, `refusable` — the rows live on the `*Log`, so two values over one log are one checkpoint store |
| `event/eventmemory/doc.go` | the package sentence, and the two paragraphs about the Log being the backing |
| `event/eventtest/doc.go` | the package sentence, and what the three words a section reports mean |
| `event/eventtest/suite.go` | `Run`, `Factory`, `Tx`, `telling`, `report`, `probing`, `sweep`, `certify`, `admit`, `missing`, `reserve` — `sweep` and `certify` are generic over what a section asserts against, because the checkpoint runner reports through the same three rules |
| `event/eventtest/inventory.go` | `section`, `running`, `inventory` — the twenty sections and what each needs |
| `event/eventtest/probe.go` | `probe`, `recording` — the fixture builder, the store façade and the verdict sink; `recording` is the half that is about running a section rather than about a `Store` |
| `event/eventtest/declaration.go` | `ledger`, `accountID`, `opened`, `credited`, `noted`, `rawCodec`, `declaration`, `declare`, `declareRaw`, `declareWith`, `keyOf` — the aggregate every section is driven through |
| `event/eventtest/report.go` | `word`, `verdict`, `certified`, `recording.verdict` — passed, not certified, failed |
| `event/eventtest/checkpoints.go` | `CheckpointFactory`, `RunCheckpoints`, `tracking`, `checkpoints`, `admitCheckpoints`, `admitInstant`, `missingCheckpointHook`, `checkpointName`, `sameCheckpoint` and the section helpers — the checkpoint store's own runner, under the same three anti-vacuity rules, and `Instant` is where a store declares the grain its own instant column keeps |
| `event/eventtest/sections_checkpoints.go` | `checkpointInventory`, `needsCheckpointTransactions`, `needsCheckpointPersistence`, `cursorOfWidth`, `firstDifference`, `forgetsInAUnit`, `forgetRacingASave` and the twelve section bodies — `binding`, `absence`, `round trip`, `fence`, `forget`, `names`, `bounds`, `refusal classes`, `lifecycle`, `concurrency`, `transactions`, `durability` |
| `event/eventtest/sections_topology.go` | `topologySection`, `topologyHandoffSection`, `checkpoints.handOver`, `checkpoints.absentOutside` — the two sections a `Split` rests on: a cursor written under one projection name reading back unchanged under another, and a `Load`, two `Save`s at advance 1 and a `Forget` in one caller-opened transaction being all or nothing ([[FL-038]]) |
| `event/eventtest/defects_checkpoints.go` | `checkpointDefect`, `checkpointDefects`, `unfenced`, `stale`, `absent`, `oneName`, `detaching` — the five broken checkpoint stores the runner is falsified with |
| `event/eventtest/proxies.go` | `RoundTrip`, `Keys`, `Families` — the three proxies an application runs over its own declaration |
| `event/eventtest/sections_write.go` | `bindingSection`, `streamIdentitySection`, `expectedVersionSection`, `denseVersionsSection`, `concurrencySection`, `sharedBackingSection` |
| `event/eventtest/sections_read.go` | `globalOrderSection`, `conservationSection`, `streamPagingSection`, `globalPagingSection`, `boundsSection`, `monotoneVisibilitySection`, `from`, `reading` |
| `event/eventtest/sections_lifecycle.go` | `cancellationSection`, `lifecycleSection`, `refusalClassesSection`, `durabilitySection`, `storeFailureSection` |
| `event/eventtest/sections_transactions.go` | `transactionsSection` — staged, discarded, contended, crossed, the aftermath of a conflict, an unbound context, `claimed` (one unit of work over two streams, and both writable once it finishes) and `joined` (the same unit carried through a second store value over one backing) |
| `event/eventtest/sections_resumption.go` | `resumptionSection`, `acrossTheFlight`, `inFlight`, `heldWriter`, `foreignCursor`, `unparsableCursor` — a foreign cursor, an unparsable one, and a writer whose transaction is held **open** across the resumed walk |
| `event/eventtest/sections_ownership.go` | `payloadOwnershipSection` — the spilled, inbound and aliased halves of what a payload handed over belongs to |
| `event/eventtest/defects.go` | `defects` — the inventory of purpose-built broken stores the suite is falsified with |
| `event/eventtest/defects_write.go` | `drifting`, `truncatedKeys`, `foldedAccents`, `lostAsNotWritten`, `unrecorded`, `lastWriteWins`, `clipsBatches`, `reading`, `cachedStreams` |
| `event/eventtest/defects_read.go` | `reusedPositions`, `movingPositions`, `reorderedStreams`, `ignoresTheVersionRead`, `cursorlessPages`, `partialLog`, `renamedInTheLog`, `shortPages`, `misOrderedPages`, `foreignPages`, `ownPlace`, `unsafeCursors`, `withheldTails` |
| `event/eventtest/defects_ownership.go` | `writesTheInput`, `pooledPages`, `packedPages`, `retainedPages` |
| `event/eventtest/defects_lifecycle.go` | `refusesWhatItWrote`, `uncertainWhenCancelled`, `deafToCancellation`, `servesTheCancelled`, `closesOnce`, `closesNothing`, `unclassifying` |
| `event/eventtest/stores.go` | `over`, `narrowed`, `policing`, `unconfirming`, `wrapping` — the forwarding decorators the sections drive |
| `event/eventtest/ledger.go` | `LedgerFactory`, `RunLedger`, `claiming`, `receipts`, `admitLedger`, `ledgerKey` and the section helpers — the runner for a `receipt.Ledger`, under the same three anti-vacuity rules ([[FL-043]]) |
| `event/eventtest/sections_ledger.go` | `waiting`, `ledgerInventory`, `ledgerClaimSection`, `ledgerRepeatSection`, `receipts.lost`, `ledgerClaimOrderSection`, `claimed`, `receipts.raced`, `ledgerUnitSection`, `ledgerCompletionSection`, `ledgerHorizonSection`, `receipts.recorded`, `ledgerTransactionSection` — the seven sections, of which `claim order` is the insert-then-select the whole harness exists for |
| `event/eventtest/defects_ledger.go` | `ledgerDefect`, `ledgerDefects`, `overLedger`, `selectsFirst`, `echoes`, `echoesPrint`, `alwaysWon`, `undated`, `completesNothing`, `oneAuthority` — the eleven broken ledgers the runner is falsified with, four of them fixtures rather than decorators |
| `event/eventtest/generations.go` | `Closure`, `ClosureUnstated`, `LockingRead`, `SerializableUnit`, `GenerationsFactory`, `RunGenerations`, `owning`, `generations`, `admitGenerations`, `generationsName` and the section helpers — the runner for a `projection.Generations`, and the field an implementation declares its closure in ([[FL-042]]) |
| `event/eventtest/sections_generations.go` | `generationsInventory`, `needsALockingRead`, `ungeneratedSection`, `activationSection`, `fencedActivationSection`, `generations.contended`, `generationsUnitSection`, `lockingReadSection` — the five sections, of which `locking read` measures the wait [[D-126]] leaves to a caller |
| `event/eventtest/defects_generations.go` | `generationsDefect`, `generationsDefects`, `overGenerations`, `refusesAnAbsentRow`, `movesNothing`, `readThenWrite` — the six broken ownership rows the runner is falsified with, three of them fixtures rather than decorators |
| `event/eventtest/park.go` | `ParkFactory`, `RunPark`, `parking`, `park`, `admitPark`, `parkName`, `widestBound` and the section helpers — the runner for a `projection.Park`, and the two bounds an implementation declares ([[FL-038]]) |
| `event/eventtest/sections_park.go` | `parkInventory`, `needsADeclaredBound`, `outsideAUnitSection`, `parkUnitSection`, `committedStateSection`, `parkCountsSection`, `parkIdentitySection`, `parkBoundsSection`, `park.full` — the six sections, one per tier the four methods run at |
| `event/eventtest/defects_park.go` | `parkDefect`, `parkDefects`, `queue`, `overPark`, `staleHolds`, `holesAreSequences`, `dropsTheGeneration`, `ungenerated`, `bareRefusal`, `cachedCount` — the eight broken queues the runner is falsified with, three of them fixtures rather than decorators |
| `scripts/event_test.go` | the three graph tests: no base subsystem reaches the extension, what each package costs, and that importing it starts nothing |
| `scripts/extensionlisting_test.go` | what those three ask the toolchain: `listedIn`, `firstPartyDependenciesIn`, `modulesUnder`, `publishedModules`, `packagesUnder`, `uncoveredDirectories`, `directoriesWithSource` |
| `scripts/extensions_test.go` | the prohibitions held over those answers: `crossingsInto`, `noBaseSubsystemDependsOn`, `costsNoMoreThanItNames`, `costOverruns`, `startsNothing`, `startsBeforeMain`, `startsAGoroutine`, `initialiserCalls` — shared with the tenancy extension, because two copies of one prohibition drift |
| `scripts/checks.sh` | `SUBSYSTEMS` — what stops a package under `utils/` importing any of it |

Every non-test `.go` file under `event/` has a row above, `doc.go` included: the
reverse index in `docs/ai/flows/Index.md` is what an agent reads before editing
a file, and a file with no row there reads as a file outside every flow.

## Tests that walk this flow

`event/declaration_test.go`, `event/fold_test.go`, `event/roundtrip_test.go`,
`event/repo_test.go`, `event/replay_test.go`, `event/transaction_test.go`,
`event/reader_test.go`, `event/refusal_test.go`, `event/store_test.go`,
`event/bounds_test.go`, `event/identity_test.go`, `event/concurrency_test.go`,
`event/crossings_test.go`, `event/fuzz_test.go`,
`event/mutablestate_test.go`, `event/refusalmessages_test.go`,
`event/stateat_test.go`, `event/digest_test.go`,
`event/transactioncontrol_test.go` — the three structural checks, reading
`event/sources_test.go` for the type-checked package, `event/renderedtypes_test.go`
for which types render an identity, `event/formatverbs_test.go` for which verb
renders which argument, and `event/fixtures_test.go` for the broken sources each
is falsified against — `event/eventmemory/*_test.go` and
`event/eventtest/*_test.go`. The `scripts/` half is falsified the same way:
`scripts/extensionwalk_test.go` drives the four shared walkers over a written
tree that holds what each exists to find.

### Proved by

Every guarantee this flow states, and the test that fails when it stops holding.
A name in lower case — `binding`, `conservation`, `lifecycle` — is one of the
twenty conformance sections, which is a `t.Run` subtest of
`event/eventtest/suite.go:Run` and is itself named by a row of the defect
inventory.

| What holds | Proved by |
|---|---|
| a declaration takes a family, facts and revisions, and reports them | `TestADeclaration`, `TestOneFamilyNamesOneAggregate` |
| every malformed declaration panics and the `Try…` sibling returns it | `TestEveryMalformedDeclarationPanicsAndTryDefineReturnsIt` |
| a fact declared after the table was read is refused, and the six readers are enumerated | `TestTheSealRefusesALateFact` |
| a codec that decodes into memory it reuses is caught before a stream holds one | `TestACodecThatDecodesIntoAReusedBufferIsCaught`, `TestTheProxiesCompareSomethingThatCanDiffer` |
| a payload survives its own codec, and the three proxies report what they exist to find | `TestAnApplicationRunsTheThreeProxiesOverItsOwnDeclaration`, `TestEachProxyReportsTheThingItExistsToFind`, `FuzzKeysReportsACollisionExactlyWhenTwoIdentitiesRenderOneKey` |
| the composed key's rendering is frozen, legal and reversible | `TestComposeRendersTheFrozenKey`, `FuzzComposeRendersAKeyThatIsLegalAndReversible` |
| one family is one aggregate, and two families sharing one key are two streams | `TestTwoFamiliesSharingOneKeyAreTwoStreams`, `binding` |
| a bind interrogates no codec, mutates no declaration and costs the same per request | `TestABindInterrogatesNoCodecAndMutatesNoDeclaration`, `TestAStoreIsBuiltPerRequestOverALogThatOutlivesIt`, `TestABindWithNothingToBindIsRefused` |
| both doors check the store, and an unstated capability is refused at each | `TestBothDoorsCheckTheStore`, `TestTheStoreDeclaresWhatItCanDoAndSaysWhatItCannot`, `TestAClaimWithNoHookAndACapabilityNobodyStatedAreBothNamed` |
| every number a store and its log take is bounded at both ends | `TestEveryNumberAStoreAndItsLogTakeIsBoundedAtBothEnds`, `TestAStoreIsBuiltOverEveryLogTheKernelWouldAdmit`, `TestTheResidentPageIsTheCeilingCountedInEnvelopes`, `TestAStoreWhoseProductExceedsTheResidentCeilingIsRefused` |
| a fresh stream loads as the zero state and a stream with history folds to its current one | `TestAFreshStreamLoadsAsZero`, `TestAStreamWithHistoryFoldsToItsCurrentState`, `TestAStreamIsReadInPagesTheStorePublished` |
| a load returns nothing rather than a partially rehydrated state | `TestALoadThatFailsMidStreamReturnsNothing`, `FuzzALoadFoldsAStoredStreamOrRefusesItWhole` |
| every history-class refusal is raised by the thing it names | `TestEveryHistoryClassRefusalIsRaisedByTheThingItNames`, `FuzzAStoredPayloadIsFoldedOrRefused` |
| an over-long page and a mis-paged stream are refused before anything is folded | `TestAnOverLongPageIsRefused`, `TestAMisPagedStreamIsRefusedBeforeItIsFolded`, `bounds`, `stream paging` |
| an upcaster may refuse and a fold may not, and a codec's panic becomes that method's own refusal | `TestAnUpcasterMayRefuseAndAFoldMayNot`, `TestACodecPanicBecomesThatMethodsOwnRefusal`, `TestAPanickingCodecBecomesTheRefusalItsErrorWouldHaveBeen`, `TestAFoldPanicUnwindsOutOfLoad` |
| a fold is pure over the state it is given, and a reference kind is not aliased out of a change | `TestAFoldIsPureOverTheStateItIsGiven`, `TestAReferenceKindStateFoldsWithoutAliasing`, `TestAChangeRetainsNoApplicationValue` |
| an append is refused in its stated order, and a change of another stream or another aggregate of this family never folds | `TestAppendRefusesInItsStatedOrder`, `TestFoldRefusesAnotherInstance`, `TestAnAppendRefusesAChangeDecidedOnAnotherAggregateOfThisFamily`, `TestTheRenderedKeyIsCheckedBeforeTheStoresBound`, `stream identity` |
| an append is admitted only at the version it was decided at | `TestAnAppendIsAdmittedOnlyAtTheVersionItWasDecidedAt`, `TestTwoTransactionsOnOneStreamLeaveOneWinnerAndAConflictFromAppend`, `expected version` |
| a bounded read folds the complete prefix and stops at the page it needs | `TestAPrefixFoldsToTheStateItsVersionHolds`, `TestABoundedReadStopsAtThePageItNeeds`, `TestThePrefixAtEveryBoundaryOfARealStream` |
| version zero, a version past the end and an empty stream are one refusal, and a page out of order is refused before it is truncated | `TestAVersionPastTheEndAndAnEmptyStreamAreOneRefusal`, `TestAPageOutOfOrderIsRefusedBeforeItIsTruncated`, `TestPastTheEndAnEmptyStreamAndVersionZeroLive` |
| an unreadable event in the prefix is the zero state and `Load`'s own refusal | `TestAnUnreadableEventInThePrefixReturnsTheZeroState`, `TestTheUnreadableEventTableLive` |
| a bounded read yields nothing that can append, and no refusal of one names a version | `TestABoundedReadYieldsNothingThatCanAppend`, `TestNoRefusalOfABoundedReadNamesAVersion`, `TestNoEventGuideOffersATimestampBoundary` |
| a digest is the bytes this append would write, reaches no store, and is frozen as a preimage | `TestADigestIsTheBytesThisAppendWouldWrite`, `TestTheDigestPreimageIsFrozen`, `TestADigestCollidesOnAByteAStreamAndAnOrder`, `TestTwoAttemptsAtDifferentVersionsDigestEqual` |
| an empty append checks the key and touches no store | `TestAnEmptyAppendChecksTheKeyAndNothingElse`, `TestOneAppendCarriesTwoIdenticalChanges` |
| a forged token buys nothing and does not compile where it would matter | `TestAForgedTokenIsRefusedBeforeAnyStatement`, `TestTheCrossingsThatMustNotCompile` |
| one load and one append make exactly the store calls the contract names | `TestOneLoadAndOneAppendMakeExactlyTheStoreCallsTheContractNames` |
| the batch bounds are counted in envelopes and in actual bytes | `TestAnAppendWhoseActualBytesExceedTheResidentCeilingIsRefused` |
| versions are dense, positions ascend whatever the clock says, and many writers leave one history | `TestVersionsAreDenseAndPositionsAscendWhateverTheClockSays`, `TestManyWritersLeaveOneDenseHistoryAndAMonotoneLog`, `dense versions`, `global order` |
| an envelope carries the type and revision of the record it was written from | `TestAnEnvelopeCarriesTheTypeAndRevisionOfTheRecordItWasWrittenFrom` |
| `Within` answers the store's transaction question and nothing else | `TestWithinAnswersTheStoresTransactionQuestion`, `TestWithinComposesForTwoBackings`, `TestTransactionAnswersWhatTheContextCarriesAfterTheStoreIsClosed` |
| an authority is its transaction's identity, and two subsystems compare it | `TestABackingAndAnAuthorityAreComparedAndNeverIdentical`, `TestRepoAuthorityIsTheComparisonTwoSubsystemsUse` |
| a second append in one transaction is admitted, and a transaction never crosses two streams | `TestASecondAppendInOneTransactionIsAdmitted`, `TestATransactionThatStagesToTwoStreamsNeverCrossesThem`, `TestAClaimCoversOnlyTheStreamsATransactionWroteTo`, `TestAReadInsideATransactionSeesItsOwnStagedAppends`, `transactions` |
| a unit of work claims every stream it wrote to and frees every one of them | `TestAClaimCoversEveryStreamOfATransactionAndIsReleasedOnEveryOne`, `transactions` |
| a unit of work is the backing's, so every store value over it names and joins the same one | `TestATransactionIsFoundThroughEveryStoreValueOverItsLog`, `transactions` |
| a rolled-back append burns its positions and leaves no fact | `TestARolledBackAppendBurnsItsPositions` |
| a transaction of another log is neither shadowed nor mistaken for this store's | `TestTwoLogsCarryTheirOwnTransactionsInOneContext`, `TestATransactionThisStoreCannotUseIsRefusedAtEveryDoor`, `TestOneTransactionUsedFromManyGoroutinesRefusesRatherThanCrashes` |
| a caller's own `ctx.Value` runs under no lock of the store's, at all three doors | `TestACallersOwnContextIsNeverRunInsideTheLogsLock`, `TestAParkedContextStopsTheDoorItWasGivenTo` |
| a stream is claimed only while something can still commit the transaction holding it, and a retained commit receipt is not such a thing | `TestAStreamIsClaimedOnlyWhileSomethingCanStillCommitIt` |
| a walk tiles a quiescent log and the two reads describe one set | `global paging`, `conservation`, `TestAConsumerReadsThroughPagesTheStorePublished` |
| a cursor is the backing's, resumes through any store value over it, and is refused when foreign | `TestACursorIsTheBackingsAndResumesThroughAnyStoreValueOverIt`, `TestACursorAStoreCannotParseIsRefusedRatherThanReadFromTheBeginning`, `FuzzACursorEitherResumesInsideTheLogOrIsRefused`, `resumption` |
| nothing held and nothing handed out share mutable memory | `TestNothingACallerHandsToAnAppendIsRetainedOrRewritten`, `TestAPageIsTheCallersIncludingItsCapacity`, `TestAPageOfStagedRecordsIsTheCallersToo`, `FuzzADecidedFactIsFrozenAgainstItsCallersBuffer`, `FuzzAPayloadIsHandedBackByteForByteAndBelongsToWhoeverReadsIt`, `payload ownership` |
| the refusal vocabulary is a partition, and a declared wrap is reachable where a cause is not | `TestTheRefusalVocabularyIsAPartition`, `TestADeclaredWrapIsReachableByErrorsAsAndACauseIsNot`, `TestARequestClassRefusalRendersAClientStatusAndAHistoryClassOneDoesNot`, `refusal classes` |
| a refusal names a class and never a value, in its rendering and in its source | `TestEveryRenderingNamesAClassAndNeverAValue`, `TestNoRefusalRendersAnIdentityAPayloadAKeyOrACursor`, `TestEveryIdentityTheChecksNameIsOneTheVocabularyDeclaresAndEveryOneItDeclaresIsNamed` |
| a cancellation travels as itself and never through a refusal | `TestAContextCauseNeverTravelsThroughARefusal`, `TestACancellationTravelsAsItselfFromEveryDoorTheStoreOperates`, `cancellation` |
| an outcome outside the vocabulary normalises and takes the fail-safe default | `TestAnOutcomeOutsideTheVocabularyNormalises`, `TestAnOutcomeOutsideTheVocabularyTakesTheFailSafeDefault`, `store failure classification` |
| a foreign error chain cannot wedge or panic the kernel | `TestAJoinedCauseCannotOutlastTheWalksBudget`, `TestANilOrLyingCauseNeverPanicsTheKernel` |
| the seam is eight required methods, none of which mutates, queries or controls a transaction | `TestTheStoreSeamHasEightMethodsAndNoneMutatesOrQueries`, `TestTheSeamsValuesAreDefinedTypesWithTheDeclaredFields`, `TestTheKernelNeverIssuesTransactionControlAndNeverRetries` |
| close is idempotent and decides nothing, and readiness answers on both sides of it | `TestCloseIsIdempotentAndDecidesNothing`, `TestCheckAnswersWhileTheStoreIsOpenAndAfterItIsClosed`, `lifecycle` |
| two store values over one log are one store | `TestTwoStoreValuesOverOneLog`, `TestATransactionIsFoundThroughEveryStoreValueOverItsLog`, `shared backing` |
| the kernel's values answer the same from many goroutines, and one declaration is folded from many | `TestTheKernelsValuesAnswerTheSameFromManyGoroutines`, `TestOneDeclarationIsDecidedAndFoldedFromManyGoroutines`, `concurrency` |
| nothing under `event/` holds package-level mutable state or starts anything when imported | `TestNoPackageLevelStateIsEverMutated`, `TestMerelyImportingTheEventExtensionStartsNothing`, `TestALifecycleAPackageStartsForItselfIsReportedAndTheIdiomIsNot` |
| no base subsystem reaches the extension, and no package of it costs more than its row says | `TestNoBaseSubsystemDependsOnTheEventExtension`, `TestNoEventPackageCostsMoreThanTheSeamItNames`, `TestAnImportOfTheExtensionFromOutsideItIsReportedAndOneInsideItIsNot`, `TestADirectoryHoldingSourceThatNoPackageListedIsReported`, `TestAPackageCostingMoreThanItsRowSaysIsReportedAndOneCostingExactlyItIsNot` |
| which verb renders which argument, so a source check judges the argument the message actually renders | `TestAFormatThatShiftsItsOwnArgumentsIsRefusedRatherThanReadWrongly`, `FuzzAFormatIsMappedVerbByVerbOrRefusedWhole` |
| the three source checks are pure over the one package they share, and over every package the extension holds | `TestTheStructuralChecksReadOneTypedPackageFromManyGoroutines`, `TestTheChecksWalkEveryPackageTheExtensionHoldsAndLinkAllButTheSuite` |
| a second store needs no `event`-internal access | `TestATrivialStoreNeedsNoInternalAccess` |
| the memory store satisfies the contract, at its own limits and at the narrowest a store may publish | `TestTheMemoryStoreSatisfiesTheContract`, `TestTheMemoryStoreSatisfiesTheContractAtNarrowerLimits`, `TestATransactionCapableStoreSatisfiesTheContract`, `TestEverySectionIsCertifiedAtTheNarrowestLimitsAStoreMayPublish` |
| the suite reports every section it holds, and says not certified rather than passed | `TestEverySectionInTheInventoryWasReported`, `TestAGatedSectionIsReportedNotCertified`, `TestTheRunReportsWhatItFound`, `TestTheVerdictOfASectionThatNeverReturnedIsNotPassed` |
| the suite still detects every defect it was built to detect | `TestTheSuiteStillDetectsEveryDefectItWasBuiltToDetect`, `TestEverySectionIsNamedByADefectThatBreaksIt`, `TestTheDefectInventoryIsTheSizeItSaysItIs`, `TestEverySectionFailsAgainstAStoreThatRefusesEverything` |
| a store that lies about what it published or what it recorded is not certified | `TestAFactoryWhoseLaterStoresPublishSomethingElseIsRefused`, `TestAStoreWhoseCapabilitiesOrBackingChangeUnderOneValueIsNotCertified`, `TestAStoreThatMintsTheRecordedInstantWhenAnEventIsReadIsNotCertified`, `TestAStoreThatKeepsWhatItWroteIsCertifiedForDurability` |
| every store call a section makes carries a deadline, and a section reaches a verdict over a log somebody else is writing to | `TestEveryStoreCallASectionMakesCarriesADeadline`, `TestASectionReachesAVerdictOverALogSomebodyElseIsStillWritingTo`, `TestASectionWalksItsOwnTailOfALogSomebodyElseFilled` |
| no doc promises exactly-once delivery | `TestNoDocPromisesExactlyOnceDelivery`, `TestAPromiseOfExactlyOnceDeliveryIsReportedAndARefusalOfOneIsNot`, `TestTheSentenceBeforeAClaimIsTheOneThePagePutsThere`, `FuzzADeliveryClaimIsReportedAtALineTheDocumentHas` |
| the memory checkpoint store satisfies the contract, and its rows are the log's rather than a value's | `TestTheCheckpointStoreSatisfiesTheContract`, `TestForgetTouchesOneNameAndAnAbsentOneIsNotARefusal`, `TestTwoCheckpointValuesOverOneLogAreOneStore`, `TestARolledBackUnitStagedBothAndBurntOnlyTheAppends` |
| absence is total inside a unit that staged a removal, not only outside one | `TestALoadInsideAUnitThatStagedAForgetAnswersTheZeroCheckpoint`, `transactions` |
| a removal committing while a save is in flight leaves no row behind | `forget` (`forgetRacingASave`), `TestASaveAboveAdvanceOneCannotResurrectARowAForgetRemoved` (FL-037) |
| the suite measures the round trip of the instant and not one backing's precision | `TestACoarserInstantIsCertifiedWhenTheFactoryDeclaresItsGrainAndNotWhenItDoesNot`, `TestACheckpointFactoryClaimingPersistenceWithNoSiblingFailsBeforeASectionRuns` |
| the checkpoint suite still detects every defect it was built to detect | `TestEveryCheckpointDefectIsReportedByItsOwnSection`, `TestAStoreThatDoesNotPersistDeclinesDurabilityAndCertifiesTheOtherThirteen` |

### What the four structural checks reach, and what they do not

They are the rows above that read a source tree rather than run it, so what
they *cannot* see is part of what they prove.

- `TestNoRefusalRendersAnIdentityAPayloadAKeyOrACursor`
  (`event/refusalmessages_test.go`) reads the packages under `event/` that a
  program **links** — the vocabulary and `event/eventmemory` today. The scope is
  derived rather than named: a package that imports `testing` outside its own
  tests cannot be in a production binary, which is why `event/eventtest` is not
  read and needs no exemption written for it. The predicate is the package's
  **import list** and not its bytes, so a comment or a string literal carrying
  the quoted word does not drop a store out of the walk; which packages are
  walked at all is compared against the tree itself rather than counted, so the
  fourth store extends both sets on its own. What is forbidden is decided from
  the argument's *type* through `go/types`: `Key`, `Cursor`, `Version` and
  `Position` are named, everything reaching one of them or reaching a byte slice
  falls out, and a type that declares its own `String` or `Error` decides its own
  rendering — which is how `Stream` renders a family and not the key it carries.
  A message is any call returning an error that takes a `string`. A string
  built into a **local** one statement earlier is followed back to what was
  written into it, so `detail := string(key)` and a `fmt.Sprintf` over an
  identity are read where they are wrapped. What is still not followed: a string
  reaching a message through a struct field, a package-level value or another
  function's return. Which verb renders which argument is read by
  `event/formatverbs_test.go` in `fmt`'s own order — flags, argument index or
  star width, width, precision, verb — and a format that moves its own
  arguments with `%[2]s` or `%*d` is refused whole rather than read wrongly;
  `FuzzAFormatIsMappedVerbByVerbOrRefusedWhole` differentially checks that
  reading against `fmt` itself.
- `TestNoPackageLevelStateIsEverMutated` (`event/mutablestate_test.go`) walks
  all three packages, including the suite — pinned by
  `TestTheChecksWalkEveryPackageTheExtensionHoldsAndLinkAllButTheSuite`, because
  a walk narrowed to the kernel leaves every assertion here green — and takes
  the kind from the type, so
  `make(...)`, `new(...)` and a composite literal are one finding. A name a
  function writes through its parameter is reported where it is *declared*,
  because that is the shape no syntax at the call site reveals. Two further
  shapes are read from the type rather than from a mutation: a value of a named
  type that declares any **pointer-receiver method**, which is the ordinary
  spelling of a registry and which no assignment in this package reveals; and an
  **interface** holder, judged by what it can hold — one naming no method holds
  anything, and one naming methods is judged by what was written into it at the
  declaration, so `var held any = map[string]int{}` is a finding and an `error`
  sentinel and a `reflect.Type` token are not, without either being exempted by
  name.
- `TestTheKernelNeverIssuesTransactionControlAndNeverRetries`
  (`event/transactioncontrol_test.go`) reads the kernel alone. Transaction
  control is matched case-blind over methods and free functions; an `Append` is
  the store's when its receiver also answers `ReadStream`, so a builder with an
  `Append` of its own is not reported. A name in **value** position is read too,
  so `finish := tx.Commit` and a commit handed to something taking a `func()`
  are the same finding as a call. Direct recursion around a store append is the
  last arm. What is not seen, as a closed list: mutual recursion through two
  functions, which the loop arm catches in its ordinary form, and a commit
  reached through an interface method of some other name.
- `TestMerelyImportingTheEventExtensionStartsNothing` (`scripts/event_test.go`,
  walking `scripts/extensions_test.go` over what
  `scripts/extensionlisting_test.go` listed) reaches every module under `event/`, not
  only the packages a `go list` pattern reports, and fails when a directory
  holding source was listed by nobody — which is what will still be true on the
  day `event/eventpg` becomes a module. The cost arm is the same shared walk,
  which is what makes the tenancy extension reach a nested module too; only the
  table of what a package may cost, and the three sentences that report a breach
  of it, stay in each extension's own file. That walk is falsified like the other
  three, over a tree written to understate what it costs and then read again
  against a table that states it exactly.
- Nothing in `docs/` may promise it, and
  `TestNoDocPromisesExactlyOnceDelivery` (`scripts/docs_test.go`) reads a
  per-language table of wordings rather than one literal. No page of this
  repository may carry any of them about delivery without refusing it in the
  same breath. Every wording in the table is a claim spelled **positively** —
  two English ones and four Russian ones, listed in `deliveryWordings` — because
  that is what its negation model can classify. The same promise spelled as a
  denial, "no duplicates ever reach your handler", is **not** read: its negation
  is the claim, and "deduplicates on that key" is ordinary prose about a
  mechanism this tree already writes eleven times. A refusal counts when it
  **governs** the claim: a negation in the claim's own clause, or a heading that
  itself refuses, or — further away, in an earlier clause or the sentence before
  — a negation in a span that also names the claim, by saying *promise*,
  *guarantee* or *claim*, or by spelling the frequency itself. A negation that
  merely stands nearby is not a refusal, and the difference is not academic in
  prose written as negatively as this: "The framework deduplicates nothing" is
  four words that would otherwise license the sentence after them. The wider
  two-sentence window still decides whether the phrase is about delivery at all,
  so a promise whose subject is named one sentence earlier is read rather than
  missed. Extending the table with a
  de-duplication vocabulary would turn the check red on correct pages, which is
  how a structural check gets loosened on its first run.
