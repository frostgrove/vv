# D-144 — A bounded read is a read, and never a load

**Status:** accepted
**Invariant:** `Repo.StateAt` answers a state and an error and **no second
value** — no token, no provenance, no prefix descriptor — so a historical read
cannot be the first half of a `Load → Decide → Append`. The boundary is an exact
**version** and nothing else: no timestamp parameter, overload or option
anywhere. Version zero, a version past the end and a stream with no events are
**one** refusal, `ErrVersion`, and no refusal names a number. `Store.ReadStream`
gains no ceiling.

## The decision

### The absence of a second return **is** the mechanism

```go
func (this *Repo[S, ID]) StateAt(ctx context.Context, id ID, version Version) (S, error)
```

A `Prefix` or `Provenance` struct was designed and refused. Everything it could
honestly carry the caller already has: the stream it asked about, the version it
asked for, and — because a short prefix is a refusal rather than a truncation —
the fact that it got exactly that version. The one thing it could not carry
without a second read is the stream's current head, and `replay`'s own rule
forbids that (*"the loop stops on a short page and issues no confirming read"*).
What is left is a value whose only job is to be **not** an `At[S]`, and a value
that does not exist does that job better than one that does.

The danger is not a silent overwrite. An `At[S]` minted at version 7 of a stream
now at version 30 is refused by `Append`'s own version comparison. The danger is
the **caller's reasoning**: a value that looks like a load token invites a
`Load → Decide → Append` whose decision was made against 23 events of missing
history, and which fails only at the store, and only if the stream happens not to
have moved back in the meantime.

Marten draws the same line and draws it as two calls rather than as a flag —
`FetchForWriting` yields the token, `FetchLatest` is read-only and is *"only
available off of `IDocumentSession` and not `IQuerySession`"*. This is that
division, spelled as a method with one return value.

### Three refusals are one refusal, and that is deliberate

`version == 0`, a version past the end of the stream, and a stream with no events
at all are all `ErrVersion`.

They are one answer because in an append-only log they are one thing a reader can
see. Version zero is the empty stream, so answering the zero state would hide a
caller's off-by-one behind a value that was never anybody's. A version past the
end and an empty stream differ only in where the head is, and *"a stream with no
events"* and *"a stream that never existed"* are indistinguishable through
`Store.ReadStream`, which surfaces no registry a reader could consult. Marten
answers `null` for four different situations and a caller cannot tell them apart;
this gives one refusal that covers three of them honestly and says why the three
are not told apart.

(`eventpg` has a `streams` table and *could* tell an empty stream from an absent
one. Surfacing it is a store-contract change, refused for the same reason the
version ceiling is.)

An event the declaration cannot read — `ErrRevision`, `ErrUnknownType`,
`ErrUpcast`, `ErrPayload` — is its own refusal and the zero state, **inherited
rather than re-implemented**: `replay`'s error path already answers the zero
state and never the accumulator, and `StateAt` shares that loop rather than
writing a second one. That is *«возвращают отказ, не частичное состояние»*
literally, and the shared code path is what makes it true by construction.

**No refusal names a number**, on UC-032 §11's rule and §8's precedent, where the
conflict refusal deliberately does not report the version it saw. The short-prefix
refusal says the stream is shorter than the version this read was bounded at, and
names neither.

### The class is `crud.ErrBadRequest`, and that is not cosmetic

A version a caller asked for and the stream does not hold is data only the caller
can correct. `ErrVersion` therefore wraps `crud.ErrBadRequest` and is named in
`vocabulary()` in the same change — an undeclared wrap falls through to
`errs.KindInternal`, which is a 500 for a client error, and `refusal.Is`'s
cross-class guard stops covering a sentinel the vocabulary does not list.

### There is no timestamp boundary, and one could only ever be a lookup

`recorded_at` is `statement_timestamp()` — a database clock, comparable across
writers, which is already better than the reference's application clock. It is
still **not an ordering**, and [[D-128]] closes the door by name: *"Do not order a
global read by `recorded_at` … two writers can share an instant, and a commit can
land long after the statement timestamp it carries."*

So there is no timestamp parameter, no timestamp overload and no timestamp
option, and no example anywhere sorts by one. If a time-shaped entry point is
ever added it can only be a **lookup that resolves to a version** — answering
*"which version was this stream at"* and then reading the prefix at that version —
and never an ordering. It needs its own contract. That is stated here so the next
reader starts from the conclusion rather than from the temptation.

### `Store.ReadStream` gains no ceiling, and the arithmetic is the argument

`StateAt` pages with the existing call and truncates the last page in Go.

**The over-read is bounded by one page, ever.** A bound at version 7 of a
100 000-event stream reads one page of `StreamPage` — 256 for `eventpg` — and
discards 249 envelopes. A bound at version 99 999 reads 391 pages either way. The
waste is never proportional to the stream's length.

So a ceiling on the contract would buy at most one partial page of I/O, in
exchange for a change to an interface every store implements, a new conformance
section, a `check-event-kernel` re-baseline of the contract file, and a
third-party store whose certified suite goes red. `EVENTSOURCE_REFERENCE.md`
§Reject 3 argued exactly that trade for `Log.ReadAll`'s filter parameter and it
lands the same way here.

**Two orderings inside the loop are load-bearing and are named so nobody "tidies"
them:**

- **Check, then truncate.** The page the store returned is checked entire, before
  anything is discarded. Truncating first would hide a store that answered
  `[v3, v1, v2]` — a page that folds to a state that is wrong at the right
  version, with the right count and no refusal anywhere.
- **Truncate, then fold.** Folding the discarded envelopes and then "stopping"
  produces a state at the wrong version. The loop stops reading the moment the
  accumulated version reaches the bound, and folds nothing above it.

### Why this is not a widening of [[UC-032]], clause by clause

UC-032 §6 is the clause that looks like it forbids this:

> A load rebuilds the state from the whole history, in order, every time. There
> is no cache, no snapshot and no partial rehydration: a load that fails anywhere
> returns nothing, never a state built from the events that happened to arrive
> first.

`StateAt` is not a `Load`, and it satisfies all three prohibitions literally:

- **Not a cache.** Nothing is retained across a call; §INV-042's rule that the
  kernel retains no application value is untouched.
- **Not a snapshot.** There is no second authority. The state is folded from
  events, in order, every time, and [[D-132]]'s invariant — *"full replay is the
  only authority a folded state has"* — is true of this read over its prefix.
- **Not a partial rehydration.** §6's *"partial"* means a load that skipped
  events. A prefix skips none: it is dense from version 1 to the bound, and a gap
  is a refusal.

And it is not the first half of a `Load → Decide → Append`, because it returns
nothing to append with.

UC-032's `Out of scope` line stays true verbatim: *"A query language over
history, an ordinary list-and-filter endpoint over events, or anything that makes
a fact history behave like a collection."* **A version ceiling is a prefix; a
predicate is a query language**, and that sentence is the whole of the boundary.

So ES-08 gets its own use case, **[[UC-038]]**, and **UC-032 gains no clause**:
no line of `What must hold` changes and `Out of scope` gains nothing, because
nothing that was out of scope came in. The one edit UC-032 receives is a
`See also` pointer, because leaving a shipped call unmentioned in the document a
reader trusts is the drift this repository treats as a defect.

### `Repo.Digest` is the same argument one level over

The bytes an append would write are not reachable from outside `event`:
`Change[S]` carries its encoded payload in unexported fields and `Repo.records`
is where a batch becomes `[]Record`. A caller that fingerprinted anything else —
the domain command, the `Change` values' Go representation — would fingerprint a
thing the store never sees.

It bypasses no policing decorator, and that was checked rather than assumed. A
policing decorator in a consumer's path is a `Store` decorator, because that is
the only seam `event.Open` takes, and `Digest` **reaches no store at all**: it
runs the first four of `Append`'s six steps, all of which read values the caller
handed in plus the limits retained at `Bind` from `Store.Limits()`, which is
already exported on the `Store` interface. There is nothing a decorator would
have refused and nothing a caller learns that it could not already read.

### The sentence of `Park.Holds` that moved with the wait

One sentence of contract widened in this phase and it is announced here because
no automated gate can see it: `check-event-kernel` watches file digests and
`make api` watches signatures, and a sentence of contract is invisible to both.

**`Park.Holds` is now also asked outside a unit of work, by a wait, and an
implementation must answer the committed state there.** Nothing else about `Park`
moved: `Sequences`'s outside-a-unit rule is unchanged, `Park` itself is still a
pass's write, and **`Holes` is asked by no wait at all**. The `Sequences`-first
gate means a projection that has parked nothing never reaches the widened clause.

### What the phase that adds a base state must not re-derive

ES-08 and ES-09 are one design and this phase implements one half of one clause,
so two warnings are recorded here rather than left to be rediscovered.

**`replay`'s lower bound already exists and is its seed.** The loop begins
`this.store.ReadStream(ctx, stream, at)` with `at = 0`, and `ReadStream`'s
contract is *first version `after + 1`* — which is the `version > :from` half of
the reference's SQL exactly. A snapshot-resumed load is the same function seeded
with the snapshot's version and the snapshot's state; the `version <= :to` half is
what this phase added.

1. **The accumulated version must be seeded with the base version.** `at +=
   Version(len(page))` is right only from zero; from a snapshot it must start at
   the snapshot's version, or the ceiling is computed against a stream that starts
   at one.
2. **`checkPage`'s `after` argument is the same seed.** A resumed read checks
   density from the snapshot forward and not from version 1 — which is correct,
   and this is the sentence that stops somebody "fixing" it.

## What it forbids

- Do not add a second return to `StateAt`, in any shape: no token, no
  provenance, no prefix descriptor, no boolean.
- Do not add a timestamp parameter, overload or option, and do not sort or order
  anything by `recorded_at` ([[D-128]]).
- Do not split the three `ErrVersion` refusals, and do not name a version,
  position or key in any of them.
- Do not put a ceiling, a filter or a predicate on `Store.ReadStream`.
- Do not truncate a page before it is checked, and do not fold above the bound.
- Do not widen [[UC-032]]'s `What must hold` or `Out of scope` for this.
- Do not write a second replay loop for the bounded read.
- Do not widen a second sentence of `Park`'s contract without announcing it in
  the four places this one is announced in.

## Where it lives

- `event/repo.go` — `Repo.StateAt`, `Repo.Digest`, and `replay`'s `upTo`
  parameter with the two load-bearing orderings in its comment.
- `event/errors.go` — `ErrVersion`, in the request class beside `ErrKey`.
- `event/projection/park.go` — `Park.Holds`'s widened sentence.
- `docs/modules/en/event.md` · `docs/modules/ru/event.md` — the bounded read, the
  digest, and the sentence that a timestamp is neither business time nor commit
  order.

## Proven by

- `TestAPrefixFoldsToTheStateItsVersionHolds` — with the version-6-against-7
  control.
- `TestABoundedReadStopsAtThePageItNeeds` — recorded page counts at 1, 4, 5, 8
  and 11 over a `StreamPage` of 4, with `Load`'s three-page control.
- `TestAVersionPastTheEndAndAnEmptyStreamAreOneRefusal` — the three arms
  asserting the refusals are the **same** refusal, with the at-head control.
- `TestAnUnreadableEventInThePrefixReturnsTheZeroState` — four variants, each
  against `Load`'s identical refusal.
- `TestAPageOutOfOrderIsRefusedBeforeItIsTruncated` — with the in-order control.
- `TestABoundedReadYieldsNothingThatCanAppend` — the signature, beside `Load`'s
  unchanged one.
- `TestNoRefusalOfABoundedReadNamesAVersion` — the input never appears in the
  output.
- `TestNoEventGuideOffersATimestampBoundary` — both `event.md` pages, with a
  fixture control.
- `TestThePrefixAtEveryBoundaryOfARealStream`,
  `TestPastTheEndAnEmptyStreamAndVersionZeroLive`,
  `TestTheUnreadableEventTableLive` — the same three live.
- `TestADigestIsTheBytesThisAppendWouldWrite` — the recording store asserting
  `Digest` made no store call of any kind.

## See also

[[D-128]] [[D-132]] [[D-142]] [[D-145]] [[FL-036]] [[UC-032]] [[UC-038]]
